package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/obtuseaglet/cubozoa/internal/security"
)

// pgStore is a PostgreSQL-backed implementation of Store for large or
// multi-node deployments. It stays opt-in (CUBOZOA_STORE=postgres): the default
// single-binary deployment uses the embedded bolt store and needs no external
// database.
//
// Each entity is stored as a jsonb blob keyed by ID, with separate indexed
// columns only for the dimensions the interface actually looks up by
// (name/token/path and item library/parent). This mirrors the bolt store's
// blob-plus-secondary-index design, so the two backends stay behaviorally
// identical, and keeps the persisted shape owned by the Go models rather than a
// hand-maintained column mapping.
//
// Every query is parameterized ($1, $2, …) — user-supplied values are never
// interpolated into SQL — so there is no injection surface. Table names in the
// SQL are compile-time constants, never derived from input.
type pgStore struct {
	pool     *pgxpool.Pool
	serverID string
}

// pgConn is the subset of pgx shared by *pgxpool.Pool and pgx.Tx, so helpers
// work both inside and outside a transaction.
type pgConn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const pgSchema = `
CREATE TABLE IF NOT EXISTS meta (
	key   text PRIMARY KEY,
	value text NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
	id         text  PRIMARY KEY,
	name_lower text  NOT NULL UNIQUE,
	data       jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	id         text  PRIMARY KEY,
	token_hash text  NOT NULL UNIQUE,
	data       jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS libraries (
	id   text  PRIMARY KEY,
	path text  NOT NULL UNIQUE,
	data jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS items (
	id         text  PRIMARY KEY,
	library_id text  NOT NULL,
	parent_id  text  NOT NULL,
	data       jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS items_library_idx ON items (library_id);
CREATE INDEX IF NOT EXISTS items_parent_idx  ON items (parent_id);
CREATE TABLE IF NOT EXISTS user_item_data (
	user_id text  NOT NULL,
	item_id text  NOT NULL,
	data    jsonb NOT NULL,
	PRIMARY KEY (user_id, item_id)
);
CREATE TABLE IF NOT EXISTS recordings (
	id   text  PRIMARY KEY,
	data jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS series_timers (
	id   text  PRIMARY KEY,
	data jsonb NOT NULL
);
`

// OpenPostgres connects to PostgreSQL, ensures the schema exists, and returns a
// Store. connString is a libpq/pgx connection URL or key/value DSN.
func OpenPostgres(connString string) (Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("store: connecting to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: pinging postgres: %w", err)
	}

	s := &pgStore{pool: pool}
	if _, err := pool.Exec(ctx, pgSchema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: initializing schema: %w", err)
	}
	if err := s.initServerID(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

func (s *pgStore) initServerID(ctx context.Context) error {
	id, err := security.NewID()
	if err != nil {
		return err
	}
	// Insert only if absent, then read back whatever value won the race.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO meta (key, value) VALUES ('server_id', $1) ON CONFLICT (key) DO NOTHING`, id); err != nil {
		return fmt.Errorf("store: seeding server id: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT value FROM meta WHERE key='server_id'`).Scan(&s.serverID); err != nil {
		return fmt.Errorf("store: reading server id: %w", err)
	}
	return nil
}

// --- helpers ---------------------------------------------------------------

func (s *pgStore) ctx() context.Context { return context.Background() }

// tx runs fn inside a transaction, committing on success and rolling back on
// error (the deferred rollback is a no-op after a successful commit).
func (s *pgStore) tx(fn func(tx pgx.Tx) error) error {
	ctx := s.ctx()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// mapWriteErr converts a unique-constraint violation into ErrConflict.
func mapWriteErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

// pgGetByID reads a single jsonb blob by primary key, returning ErrNotFound.
func pgGetByID[T any](ctx context.Context, q pgConn, sql, id string) (*T, error) {
	var raw []byte
	err := q.QueryRow(ctx, sql, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: query: %w", err)
	}
	return decode[T](raw)
}

// pgList reads a set of jsonb blobs. The result is always non-nil.
func pgList[T any](ctx context.Context, q pgConn, sql string, args ...any) ([]*T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query: %w", err)
	}
	defer rows.Close()
	out := []*T{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		v, err := decode[T](raw)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// --- Meta ------------------------------------------------------------------

func (s *pgStore) ServerID() string { return s.serverID }

func (s *pgStore) Close() error { s.pool.Close(); return nil }

// --- Users -----------------------------------------------------------------

func (s *pgStore) CreateUser(u *User) error {
	data, err := encode(u)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(s.ctx(),
		`INSERT INTO users (id, name_lower, data) VALUES ($1, lower($2), $3)`,
		u.ID, u.Name, string(data))
	return mapWriteErr(err)
}

func (s *pgStore) GetUserByID(id string) (*User, error) {
	return pgGetByID[User](s.ctx(), s.pool, `SELECT data FROM users WHERE id=$1`, id)
}

func (s *pgStore) GetUserByName(name string) (*User, error) {
	var raw []byte
	err := s.pool.QueryRow(s.ctx(), `SELECT data FROM users WHERE name_lower=lower($1)`, name).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: query: %w", err)
	}
	return decode[User](raw)
}

func (s *pgStore) ListUsers() ([]*User, error) {
	return pgList[User](s.ctx(), s.pool, `SELECT data FROM users`)
}

func (s *pgStore) UpdateUser(u *User) error {
	data, err := encode(u)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(s.ctx(),
		`UPDATE users SET name_lower=lower($2), data=$3 WHERE id=$1`, u.ID, u.Name, string(data))
	if err != nil {
		return mapWriteErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *pgStore) CountUsers() (int, error) {
	var n int
	err := s.pool.QueryRow(s.ctx(), `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// --- Sessions --------------------------------------------------------------

func (s *pgStore) CreateSession(sess *Session) error {
	data, err := encode(sess)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(s.ctx(),
		`INSERT INTO sessions (id, token_hash, data) VALUES ($1, $2, $3)`,
		sess.ID, sess.TokenHash, string(data))
	return mapWriteErr(err)
}

func (s *pgStore) GetSessionByTokenHash(tokenHash string) (*Session, error) {
	var raw []byte
	err := s.pool.QueryRow(s.ctx(), `SELECT data FROM sessions WHERE token_hash=$1`, tokenHash).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: query: %w", err)
	}
	return decode[Session](raw)
}

func (s *pgStore) DeleteSession(id string) error {
	tag, err := s.pool.Exec(s.ctx(), `DELETE FROM sessions WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *pgStore) TouchSession(id string) error {
	return s.tx(func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(s.ctx(), `SELECT data FROM sessions WHERE id=$1 FOR UPDATE`, id).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		sess, err := decode[Session](raw)
		if err != nil {
			return err
		}
		sess.LastActivity = time.Now().UTC()
		data, err := encode(sess)
		if err != nil {
			return err
		}
		_, err = tx.Exec(s.ctx(), `UPDATE sessions SET data=$2 WHERE id=$1`, id, string(data))
		return err
	})
}

// --- Libraries -------------------------------------------------------------

func (s *pgStore) CreateLibrary(l *Library) error {
	data, err := encode(l)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(s.ctx(),
		`INSERT INTO libraries (id, path, data) VALUES ($1, $2, $3)`, l.ID, l.Path, string(data))
	return mapWriteErr(err)
}

func (s *pgStore) GetLibrary(id string) (*Library, error) {
	return pgGetByID[Library](s.ctx(), s.pool, `SELECT data FROM libraries WHERE id=$1`, id)
}

func (s *pgStore) GetLibraryByPath(path string) (*Library, error) {
	var raw []byte
	err := s.pool.QueryRow(s.ctx(), `SELECT data FROM libraries WHERE path=$1`, path).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: query: %w", err)
	}
	return decode[Library](raw)
}

func (s *pgStore) ListLibraries() ([]*Library, error) {
	return pgList[Library](s.ctx(), s.pool, `SELECT data FROM libraries`)
}

func (s *pgStore) DeleteLibrary(id string) error {
	return s.tx(func(tx pgx.Tx) error {
		tag, err := tx.Exec(s.ctx(), `DELETE FROM libraries WHERE id=$1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(s.ctx(), `DELETE FROM items WHERE library_id=$1`, id)
		return err
	})
}

// --- Media items -----------------------------------------------------------

func (s *pgStore) GetItem(id string) (*MediaItem, error) {
	return pgGetByID[MediaItem](s.ctx(), s.pool, `SELECT data FROM items WHERE id=$1`, id)
}

func (s *pgStore) ListItemsByLibrary(libraryID string) ([]*MediaItem, error) {
	return pgList[MediaItem](s.ctx(), s.pool, `SELECT data FROM items WHERE library_id=$1`, libraryID)
}

func (s *pgStore) ListItemsByParent(parentID string) ([]*MediaItem, error) {
	return pgList[MediaItem](s.ctx(), s.pool, `SELECT data FROM items WHERE parent_id=$1`, parentID)
}

func (s *pgStore) AllItems() ([]*MediaItem, error) {
	return pgList[MediaItem](s.ctx(), s.pool, `SELECT data FROM items`)
}

func (s *pgStore) ReplaceLibraryItems(libraryID string, items []*MediaItem) error {
	return s.tx(func(tx pgx.Tx) error {
		var libRaw []byte
		err := tx.QueryRow(s.ctx(), `SELECT data FROM libraries WHERE id=$1 FOR UPDATE`, libraryID).Scan(&libRaw)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		lib, err := decode[Library](libRaw)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(s.ctx(), `DELETE FROM items WHERE library_id=$1`, libraryID); err != nil {
			return err
		}

		if len(items) > 0 {
			batch := &pgx.Batch{}
			for _, it := range items {
				clone := *it
				clone.LibraryID = libraryID
				data, err := encode(&clone)
				if err != nil {
					return err
				}
				batch.Queue(`INSERT INTO items (id, library_id, parent_id, data) VALUES ($1, $2, $3, $4)`,
					clone.ID, libraryID, clone.ParentID, string(data))
			}
			br := tx.SendBatch(s.ctx(), batch)
			for range items {
				if _, err := br.Exec(); err != nil {
					br.Close()
					return err
				}
			}
			if err := br.Close(); err != nil {
				return err
			}
		}

		lib.ItemCount = len(items)
		lib.ScannedAt = time.Now().UTC()
		data, err := encode(lib)
		if err != nil {
			return err
		}
		_, err = tx.Exec(s.ctx(), `UPDATE libraries SET data=$2 WHERE id=$1`, libraryID, string(data))
		return err
	})
}

// --- Per-user item data ----------------------------------------------------

func (s *pgStore) GetUserItemData(userID, itemID string) (*UserItemData, error) {
	var raw []byte
	err := s.pool.QueryRow(s.ctx(),
		`SELECT data FROM user_item_data WHERE user_id=$1 AND item_id=$2`, userID, itemID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: query: %w", err)
	}
	return decode[UserItemData](raw)
}

func (s *pgStore) UpsertUserItemData(d *UserItemData) error {
	data, err := encode(d)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(s.ctx(),
		`INSERT INTO user_item_data (user_id, item_id, data) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, item_id) DO UPDATE SET data=excluded.data`,
		d.UserID, d.ItemID, string(data))
	return err
}

func (s *pgStore) ListUserItemData(userID string) ([]*UserItemData, error) {
	return pgList[UserItemData](s.ctx(), s.pool, `SELECT data FROM user_item_data WHERE user_id=$1`, userID)
}

// --- DVR recordings --------------------------------------------------------

func (s *pgStore) CreateRecording(rec *Recording) error {
	data, err := encode(rec)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(s.ctx(), `INSERT INTO recordings (id, data) VALUES ($1, $2)`, rec.ID, string(data))
	return mapWriteErr(err)
}

func (s *pgStore) GetRecording(id string) (*Recording, error) {
	return pgGetByID[Recording](s.ctx(), s.pool, `SELECT data FROM recordings WHERE id=$1`, id)
}

func (s *pgStore) ListRecordings() ([]*Recording, error) {
	return pgList[Recording](s.ctx(), s.pool, `SELECT data FROM recordings`)
}

func (s *pgStore) UpdateRecording(rec *Recording) error {
	data, err := encode(rec)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(s.ctx(), `UPDATE recordings SET data=$2 WHERE id=$1`, rec.ID, string(data))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *pgStore) DeleteRecording(id string) error {
	tag, err := s.pool.Exec(s.ctx(), `DELETE FROM recordings WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- DVR series timers -----------------------------------------------------

func (s *pgStore) CreateSeriesTimer(st *SeriesTimer) error {
	data, err := encode(st)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(s.ctx(), `INSERT INTO series_timers (id, data) VALUES ($1, $2)`, st.ID, string(data))
	return mapWriteErr(err)
}

func (s *pgStore) GetSeriesTimer(id string) (*SeriesTimer, error) {
	return pgGetByID[SeriesTimer](s.ctx(), s.pool, `SELECT data FROM series_timers WHERE id=$1`, id)
}

func (s *pgStore) ListSeriesTimers() ([]*SeriesTimer, error) {
	return pgList[SeriesTimer](s.ctx(), s.pool, `SELECT data FROM series_timers`)
}

func (s *pgStore) DeleteSeriesTimer(id string) error {
	tag, err := s.pool.Exec(s.ctx(), `DELETE FROM series_timers WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
