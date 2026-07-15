package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/obtuseaglet/cubozoa/internal/security"
)

// boltStore is a durable, embedded key/value-backed implementation of Store.
//
// Compared to the JSON store it (a) never rewrites the whole dataset on a
// mutation — each call is a single transaction touching only the pages it
// changes, (b) is not bound by RAM because bbolt is memory-mapped, and (c)
// serves the hot lookups (session-by-token, items-by-library, items-by-parent)
// from secondary-index buckets instead of scanning everything.
//
// There is deliberately no SQL layer: values are JSON blobs and lookups are
// B+tree range scans, so the store adds no query-language parsing surface.
type boltStore struct {
	db       *bolt.DB
	serverID string
}

// Bucket names. Primary buckets are keyed by entity ID; idx* buckets are
// secondary indexes whose keys encode a lookup dimension.
var (
	bkMeta         = []byte("meta")
	bkUsers        = []byte("users")
	bkSessions     = []byte("sessions")
	bkLibraries    = []byte("libraries")
	bkItems        = []byte("items")
	bkUserData     = []byte("userdata")
	bkRecordings   = []byte("recordings")
	bkSeriesTimers = []byte("series_timers")

	// idxSessionToken maps a session token hash to its session ID.
	idxSessionToken = []byte("idx_session_token")
	// idxItemLibrary keys are "<libraryID>\x00<itemID>"; the value is unused.
	idxItemLibrary = []byte("idx_item_library")
	// idxItemParent keys are "<parentID>\x00<itemID>"; the value is unused.
	idxItemParent = []byte("idx_item_parent")

	metaServerID = []byte("server_id")
)

var allBuckets = [][]byte{
	bkMeta, bkUsers, bkSessions, bkLibraries, bkItems, bkUserData,
	bkRecordings, bkSeriesTimers, idxSessionToken, idxItemLibrary, idxItemParent,
}

const sep = "\x00" // composite-key separator; entity IDs never contain it

// OpenBolt opens (or initializes) a bbolt-backed store rooted at dataDir.
func OpenBolt(dataDir string) (Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("store: creating data dir: %w", err)
	}
	// The datastore holds password/token hashes; keep it owner-only. A short
	// open timeout turns a stale lock into an error instead of a hang.
	db, err := bolt.Open(filepath.Join(dataDir, "cubozoa.db"), 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("store: opening datastore: %w", err)
	}

	s := &boltStore{db: db}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range allBuckets {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		meta := tx.Bucket(bkMeta)
		if v := meta.Get(metaServerID); v != nil {
			s.serverID = string(v)
			return nil
		}
		id, err := security.NewID()
		if err != nil {
			return err
		}
		s.serverID = id
		return meta.Put(metaServerID, []byte(id))
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: initializing datastore: %w", err)
	}
	return s, nil
}

// --- encode/decode helpers -------------------------------------------------

func encode(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("store: encoding record: %w", err)
	}
	return b, nil
}

func decode[T any](b []byte) (*T, error) {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("store: decoding record: %w", err)
	}
	return &v, nil
}

// idxKey composes a "<a>\x00<b>" secondary-index key.
func idxKey(a, b string) []byte { return []byte(a + sep + b) }

// idxPrefix is the "<a>\x00" prefix used to range-scan an index by its first
// dimension.
func idxPrefix(a string) []byte { return []byte(a + sep) }

// forEachPrefix invokes fn for every key in bucket b that begins with prefix.
func forEachPrefix(b *bolt.Bucket, prefix []byte, fn func(k, v []byte) error) error {
	c := b.Cursor()
	for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
		if err := fn(k, v); err != nil {
			return err
		}
	}
	return nil
}

// --- Meta ------------------------------------------------------------------

func (s *boltStore) ServerID() string { return s.serverID }

func (s *boltStore) Close() error { return s.db.Close() }

// --- Users -----------------------------------------------------------------

func (s *boltStore) CreateUser(u *User) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bkUsers)
		// Unique (case-insensitive) name. The user set is small; a scan is fine.
		conflict := false
		if err := b.ForEach(func(_, v []byte) error {
			existing, err := decode[User](v)
			if err != nil {
				return err
			}
			if strings.EqualFold(existing.Name, u.Name) {
				conflict = true
			}
			return nil
		}); err != nil {
			return err
		}
		if conflict {
			return ErrConflict
		}
		data, err := encode(u)
		if err != nil {
			return err
		}
		return b.Put([]byte(u.ID), data)
	})
}

func (s *boltStore) GetUserByID(id string) (*User, error) {
	var out *User
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bkUsers).Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		u, err := decode[User](v)
		out = u
		return err
	})
	return out, err
}

func (s *boltStore) GetUserByName(name string) (*User, error) {
	var out *User
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkUsers).ForEach(func(_, v []byte) error {
			if out != nil {
				return nil
			}
			u, err := decode[User](v)
			if err != nil {
				return err
			}
			if strings.EqualFold(u.Name, name) {
				out = u
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, ErrNotFound
	}
	return out, nil
}

func (s *boltStore) ListUsers() ([]*User, error) {
	var out []*User
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkUsers).ForEach(func(_, v []byte) error {
			u, err := decode[User](v)
			if err != nil {
				return err
			}
			out = append(out, u)
			return nil
		})
	})
	if out == nil {
		out = []*User{}
	}
	return out, err
}

func (s *boltStore) UpdateUser(u *User) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bkUsers)
		if b.Get([]byte(u.ID)) == nil {
			return ErrNotFound
		}
		data, err := encode(u)
		if err != nil {
			return err
		}
		return b.Put([]byte(u.ID), data)
	})
}

func (s *boltStore) CountUsers() (int, error) {
	n := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkUsers).ForEach(func(_, _ []byte) error {
			n++
			return nil
		})
	})
	return n, err
}

// --- Sessions --------------------------------------------------------------

func (s *boltStore) CreateSession(sess *Session) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := encode(sess)
		if err != nil {
			return err
		}
		if err := tx.Bucket(bkSessions).Put([]byte(sess.ID), data); err != nil {
			return err
		}
		return tx.Bucket(idxSessionToken).Put([]byte(sess.TokenHash), []byte(sess.ID))
	})
}

func (s *boltStore) GetSessionByTokenHash(tokenHash string) (*Session, error) {
	var out *Session
	err := s.db.View(func(tx *bolt.Tx) error {
		id := tx.Bucket(idxSessionToken).Get([]byte(tokenHash))
		if id == nil {
			return ErrNotFound
		}
		v := tx.Bucket(bkSessions).Get(id)
		if v == nil {
			return ErrNotFound
		}
		sess, err := decode[Session](v)
		out = sess
		return err
	})
	return out, err
}

func (s *boltStore) DeleteSession(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bkSessions)
		v := b.Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		sess, err := decode[Session](v)
		if err != nil {
			return err
		}
		if err := b.Delete([]byte(id)); err != nil {
			return err
		}
		return tx.Bucket(idxSessionToken).Delete([]byte(sess.TokenHash))
	})
}

func (s *boltStore) TouchSession(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bkSessions)
		v := b.Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		sess, err := decode[Session](v)
		if err != nil {
			return err
		}
		sess.LastActivity = time.Now().UTC()
		data, err := encode(sess)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), data)
	})
}

// --- Libraries -------------------------------------------------------------

func (s *boltStore) CreateLibrary(l *Library) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bkLibraries)
		conflict := false
		if err := b.ForEach(func(_, v []byte) error {
			existing, err := decode[Library](v)
			if err != nil {
				return err
			}
			if existing.Path == l.Path {
				conflict = true
			}
			return nil
		}); err != nil {
			return err
		}
		if conflict {
			return ErrConflict
		}
		data, err := encode(l)
		if err != nil {
			return err
		}
		return b.Put([]byte(l.ID), data)
	})
}

func (s *boltStore) GetLibrary(id string) (*Library, error) {
	var out *Library
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bkLibraries).Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		l, err := decode[Library](v)
		out = l
		return err
	})
	return out, err
}

func (s *boltStore) GetLibraryByPath(path string) (*Library, error) {
	var out *Library
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkLibraries).ForEach(func(_, v []byte) error {
			if out != nil {
				return nil
			}
			l, err := decode[Library](v)
			if err != nil {
				return err
			}
			if l.Path == path {
				out = l
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, ErrNotFound
	}
	return out, nil
}

func (s *boltStore) ListLibraries() ([]*Library, error) {
	var out []*Library
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkLibraries).ForEach(func(_, v []byte) error {
			l, err := decode[Library](v)
			if err != nil {
				return err
			}
			out = append(out, l)
			return nil
		})
	})
	if out == nil {
		out = []*Library{}
	}
	return out, err
}

func (s *boltStore) DeleteLibrary(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		lb := tx.Bucket(bkLibraries)
		if lb.Get([]byte(id)) == nil {
			return ErrNotFound
		}
		if err := lb.Delete([]byte(id)); err != nil {
			return err
		}
		return s.deleteLibraryItems(tx, id)
	})
}

// deleteLibraryItems removes every item belonging to a library and its index
// entries. Callers hold a write transaction.
func (s *boltStore) deleteLibraryItems(tx *bolt.Tx, libraryID string) error {
	items := tx.Bucket(bkItems)
	byLib := tx.Bucket(idxItemLibrary)
	byParent := tx.Bucket(idxItemParent)

	// Collect first; deleting through a cursor mid-iteration is fragile.
	var itemIDs []string
	prefix := idxPrefix(libraryID)
	if err := forEachPrefix(byLib, prefix, func(k, _ []byte) error {
		itemIDs = append(itemIDs, string(k[len(prefix):]))
		return nil
	}); err != nil {
		return err
	}
	for _, itemID := range itemIDs {
		v := items.Get([]byte(itemID))
		if v != nil {
			if it, err := decode[MediaItem](v); err == nil {
				_ = byParent.Delete(idxKey(it.ParentID, itemID))
			}
		}
		if err := items.Delete([]byte(itemID)); err != nil {
			return err
		}
		if err := byLib.Delete(idxKey(libraryID, itemID)); err != nil {
			return err
		}
	}
	return nil
}

// --- Media items -----------------------------------------------------------

func (s *boltStore) GetItem(id string) (*MediaItem, error) {
	var out *MediaItem
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bkItems).Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		it, err := decode[MediaItem](v)
		out = it
		return err
	})
	return out, err
}

func (s *boltStore) listItemsByIndex(indexBucket []byte, dim string) ([]*MediaItem, error) {
	out := []*MediaItem{}
	err := s.db.View(func(tx *bolt.Tx) error {
		items := tx.Bucket(bkItems)
		prefix := idxPrefix(dim)
		return forEachPrefix(tx.Bucket(indexBucket), prefix, func(k, _ []byte) error {
			itemID := k[len(prefix):]
			v := items.Get(itemID)
			if v == nil {
				return nil // index/primary skew; skip defensively
			}
			it, err := decode[MediaItem](v)
			if err != nil {
				return err
			}
			out = append(out, it)
			return nil
		})
	})
	return out, err
}

func (s *boltStore) ListItemsByLibrary(libraryID string) ([]*MediaItem, error) {
	return s.listItemsByIndex(idxItemLibrary, libraryID)
}

func (s *boltStore) ListItemsByParent(parentID string) ([]*MediaItem, error) {
	return s.listItemsByIndex(idxItemParent, parentID)
}

func (s *boltStore) AllItems() ([]*MediaItem, error) {
	out := []*MediaItem{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkItems).ForEach(func(_, v []byte) error {
			it, err := decode[MediaItem](v)
			if err != nil {
				return err
			}
			out = append(out, it)
			return nil
		})
	})
	return out, err
}

func (s *boltStore) ReplaceLibraryItems(libraryID string, items []*MediaItem) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		lb := tx.Bucket(bkLibraries)
		lv := lb.Get([]byte(libraryID))
		if lv == nil {
			return ErrNotFound
		}
		lib, err := decode[Library](lv)
		if err != nil {
			return err
		}

		// Swap out the old item set for the new one atomically.
		if err := s.deleteLibraryItems(tx, libraryID); err != nil {
			return err
		}
		ib := tx.Bucket(bkItems)
		byLib := tx.Bucket(idxItemLibrary)
		byParent := tx.Bucket(idxItemParent)
		for _, it := range items {
			clone := *it
			clone.LibraryID = libraryID
			data, err := encode(&clone)
			if err != nil {
				return err
			}
			if err := ib.Put([]byte(clone.ID), data); err != nil {
				return err
			}
			if err := byLib.Put(idxKey(libraryID, clone.ID), nil); err != nil {
				return err
			}
			if err := byParent.Put(idxKey(clone.ParentID, clone.ID), nil); err != nil {
				return err
			}
		}

		lib.ItemCount = len(items)
		lib.ScannedAt = time.Now().UTC()
		data, err := encode(lib)
		if err != nil {
			return err
		}
		return lb.Put([]byte(libraryID), data)
	})
}

// --- Per-user item data ----------------------------------------------------

func (s *boltStore) GetUserItemData(userID, itemID string) (*UserItemData, error) {
	var out *UserItemData
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bkUserData).Get(idxKey(userID, itemID))
		if v == nil {
			return ErrNotFound
		}
		d, err := decode[UserItemData](v)
		out = d
		return err
	})
	return out, err
}

func (s *boltStore) UpsertUserItemData(d *UserItemData) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := encode(d)
		if err != nil {
			return err
		}
		return tx.Bucket(bkUserData).Put(idxKey(d.UserID, d.ItemID), data)
	})
}

func (s *boltStore) ListUserItemData(userID string) ([]*UserItemData, error) {
	out := []*UserItemData{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return forEachPrefix(tx.Bucket(bkUserData), idxPrefix(userID), func(_, v []byte) error {
			d, err := decode[UserItemData](v)
			if err != nil {
				return err
			}
			out = append(out, d)
			return nil
		})
	})
	return out, err
}

// --- DVR recordings --------------------------------------------------------

func (s *boltStore) CreateRecording(rec *Recording) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := encode(rec)
		if err != nil {
			return err
		}
		return tx.Bucket(bkRecordings).Put([]byte(rec.ID), data)
	})
}

func (s *boltStore) GetRecording(id string) (*Recording, error) {
	var out *Recording
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bkRecordings).Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		rec, err := decode[Recording](v)
		out = rec
		return err
	})
	return out, err
}

func (s *boltStore) ListRecordings() ([]*Recording, error) {
	out := []*Recording{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkRecordings).ForEach(func(_, v []byte) error {
			rec, err := decode[Recording](v)
			if err != nil {
				return err
			}
			out = append(out, rec)
			return nil
		})
	})
	return out, err
}

func (s *boltStore) UpdateRecording(rec *Recording) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bkRecordings)
		if b.Get([]byte(rec.ID)) == nil {
			return ErrNotFound
		}
		data, err := encode(rec)
		if err != nil {
			return err
		}
		return b.Put([]byte(rec.ID), data)
	})
}

func (s *boltStore) DeleteRecording(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bkRecordings)
		if b.Get([]byte(id)) == nil {
			return ErrNotFound
		}
		return b.Delete([]byte(id))
	})
}

// --- DVR series timers -----------------------------------------------------

func (s *boltStore) CreateSeriesTimer(st *SeriesTimer) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := encode(st)
		if err != nil {
			return err
		}
		return tx.Bucket(bkSeriesTimers).Put([]byte(st.ID), data)
	})
}

func (s *boltStore) GetSeriesTimer(id string) (*SeriesTimer, error) {
	var out *SeriesTimer
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bkSeriesTimers).Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		st, err := decode[SeriesTimer](v)
		out = st
		return err
	})
	return out, err
}

func (s *boltStore) ListSeriesTimers() ([]*SeriesTimer, error) {
	out := []*SeriesTimer{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bkSeriesTimers).ForEach(func(_, v []byte) error {
			st, err := decode[SeriesTimer](v)
			if err != nil {
				return err
			}
			out = append(out, st)
			return nil
		})
	})
	return out, err
}

func (s *boltStore) DeleteSeriesTimer(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bkSeriesTimers)
		if b.Get([]byte(id)) == nil {
			return ErrNotFound
		}
		return b.Delete([]byte(id))
	})
}
