package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testDatabaseURL is the connection string for a throwaway Postgres used by the
// conformance suite. When unset, the Postgres backend is skipped (CI/dev without
// a database still fully exercises JSON and bolt).
func testDatabaseURL() string { return os.Getenv("CUBOZOA_TEST_DATABASE_URL") }

// resetPostgres ensures the schema exists and truncates all data tables so each
// test starts from an empty store.
func resetPostgres(t *testing.T, url string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, pgSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`TRUNCATE users, sessions, libraries, items, user_item_data, recordings, series_timers`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// storeFactories yields a fresh, empty store of each implementation. Every
// conformance test runs against all of them, guaranteeing the backends are
// behaviorally identical. Postgres is included only when CUBOZOA_TEST_DATABASE_URL
// is set.
func storeFactories() map[string]func(t *testing.T) Store {
	factories := map[string]func(t *testing.T) Store{
		"json": func(t *testing.T) Store {
			s, err := OpenJSON(t.TempDir())
			if err != nil {
				t.Fatalf("OpenJSON: %v", err)
			}
			t.Cleanup(func() { s.Close() })
			return s
		},
		"bolt": func(t *testing.T) Store {
			s, err := OpenBolt(t.TempDir())
			if err != nil {
				t.Fatalf("OpenBolt: %v", err)
			}
			t.Cleanup(func() { s.Close() })
			return s
		},
	}
	if url := testDatabaseURL(); url != "" {
		factories["postgres"] = func(t *testing.T) Store {
			resetPostgres(t, url)
			s, err := OpenPostgres(url)
			if err != nil {
				t.Fatalf("OpenPostgres: %v", err)
			}
			t.Cleanup(func() { s.Close() })
			return s
		}
	}
	return factories
}

// forEachStore runs fn as a subtest against every backend.
func forEachStore(t *testing.T, fn func(t *testing.T, s Store)) {
	t.Helper()
	for name, factory := range storeFactories() {
		name, factory := name, factory
		t.Run(name, func(t *testing.T) {
			fn(t, factory(t))
		})
	}
}

func TestServerIDPersists(t *testing.T) {
	// A reopened store keeps the same server ID.
	dir := t.TempDir()
	s1, err := OpenBolt(dir)
	if err != nil {
		t.Fatal(err)
	}
	id1 := s1.ServerID()
	if id1 == "" {
		t.Fatal("server ID should be generated on first open")
	}
	s1.Close()

	s2, err := OpenBolt(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.ServerID() != id1 {
		t.Fatalf("server ID changed on reopen: %q != %q", s2.ServerID(), id1)
	}
}

func TestUsersConformance(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		u := &User{ID: "u1", Name: "Alice", PasswordHash: "h", IsAdmin: true, CreatedAt: time.Now().UTC()}
		if err := s.CreateUser(u); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}

		// Case-insensitive unique name.
		if err := s.CreateUser(&User{ID: "u2", Name: "alice"}); !errors.Is(err, ErrConflict) {
			t.Fatalf("duplicate name should conflict, got %v", err)
		}

		got, err := s.GetUserByID("u1")
		if err != nil || got.Name != "Alice" || !got.IsAdmin {
			t.Fatalf("GetUserByID = %+v, %v", got, err)
		}

		// Returned value is a copy: mutating it must not affect the store.
		got.Name = "Mutated"
		reread, _ := s.GetUserByID("u1")
		if reread.Name != "Alice" {
			t.Fatalf("store returned a live reference; name became %q", reread.Name)
		}

		byName, err := s.GetUserByName("ALICE")
		if err != nil || byName.ID != "u1" {
			t.Fatalf("GetUserByName case-insensitive failed: %+v, %v", byName, err)
		}

		if _, err := s.GetUserByID("missing"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing user should be ErrNotFound, got %v", err)
		}

		if n, _ := s.CountUsers(); n != 1 {
			t.Fatalf("CountUsers = %d, want 1", n)
		}

		u.Name = "Alice2"
		if err := s.UpdateUser(u); err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}
		if got, _ := s.GetUserByID("u1"); got.Name != "Alice2" {
			t.Fatalf("update not persisted: %q", got.Name)
		}
		if err := s.UpdateUser(&User{ID: "nope"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("updating missing user should be ErrNotFound, got %v", err)
		}

		if users, _ := s.ListUsers(); len(users) != 1 {
			t.Fatalf("ListUsers len = %d, want 1", len(users))
		}
	})
}

func TestSessionsConformance(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		sess := &Session{ID: "s1", UserID: "u1", TokenHash: "abc123", CreatedAt: time.Now().UTC()}
		if err := s.CreateSession(sess); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		got, err := s.GetSessionByTokenHash("abc123")
		if err != nil || got.ID != "s1" {
			t.Fatalf("GetSessionByTokenHash = %+v, %v", got, err)
		}
		if _, err := s.GetSessionByTokenHash("nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown token should be ErrNotFound, got %v", err)
		}

		before := got.LastActivity
		time.Sleep(2 * time.Millisecond)
		if err := s.TouchSession("s1"); err != nil {
			t.Fatalf("TouchSession: %v", err)
		}
		after, _ := s.GetSessionByTokenHash("abc123")
		if !after.LastActivity.After(before) {
			t.Fatalf("TouchSession did not advance LastActivity")
		}

		if err := s.DeleteSession("s1"); err != nil {
			t.Fatalf("DeleteSession: %v", err)
		}
		// Both primary and token index must be gone.
		if _, err := s.GetSessionByTokenHash("abc123"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("token index not cleared after delete, got %v", err)
		}
		if err := s.DeleteSession("s1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleting missing session should be ErrNotFound, got %v", err)
		}
	})
}

func TestLibrariesAndItemsConformance(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		lib := &Library{ID: "lib1", Name: "Movies", Type: "movies", Path: "/media/movies"}
		if err := s.CreateLibrary(lib); err != nil {
			t.Fatalf("CreateLibrary: %v", err)
		}
		// Unique path.
		if err := s.CreateLibrary(&Library{ID: "lib2", Path: "/media/movies"}); !errors.Is(err, ErrConflict) {
			t.Fatalf("duplicate path should conflict, got %v", err)
		}

		got, err := s.GetLibraryByPath("/media/movies")
		if err != nil || got.ID != "lib1" {
			t.Fatalf("GetLibraryByPath = %+v, %v", got, err)
		}

		items := []*MediaItem{
			{ID: "i1", ParentID: "lib1", Name: "A"},
			{ID: "i2", ParentID: "lib1", Name: "B"},
			{ID: "i3", ParentID: "series1", Name: "Ep1"},
		}
		if err := s.ReplaceLibraryItems("lib1", items); err != nil {
			t.Fatalf("ReplaceLibraryItems: %v", err)
		}

		// Library count + scan time updated.
		lg, _ := s.GetLibrary("lib1")
		if lg.ItemCount != 3 || lg.ScannedAt.IsZero() {
			t.Fatalf("library not updated: count=%d scannedAt=%v", lg.ItemCount, lg.ScannedAt)
		}

		if got, _ := s.ListItemsByLibrary("lib1"); len(got) != 3 {
			t.Fatalf("ListItemsByLibrary = %d, want 3", len(got))
		}
		if got, _ := s.ListItemsByParent("lib1"); len(got) != 2 {
			t.Fatalf("ListItemsByParent(lib1) = %d, want 2", len(got))
		}
		if got, _ := s.ListItemsByParent("series1"); len(got) != 1 {
			t.Fatalf("ListItemsByParent(series1) = %d, want 1", len(got))
		}
		if it, err := s.GetItem("i1"); err != nil || it.LibraryID != "lib1" {
			t.Fatalf("GetItem i1 = %+v, %v (LibraryID should be stamped)", it, err)
		}
		if all, _ := s.AllItems(); len(all) != 3 {
			t.Fatalf("AllItems = %d, want 3", len(all))
		}

		// Re-scan replaces the set and rebuilds indexes (no stale entries).
		if err := s.ReplaceLibraryItems("lib1", []*MediaItem{{ID: "i9", ParentID: "lib1", Name: "New"}}); err != nil {
			t.Fatalf("re-scan: %v", err)
		}
		if got, _ := s.ListItemsByLibrary("lib1"); len(got) != 1 || got[0].ID != "i9" {
			t.Fatalf("re-scan left stale items: %+v", got)
		}
		if got, _ := s.ListItemsByParent("series1"); len(got) != 0 {
			t.Fatalf("stale parent index after re-scan: %d", len(got))
		}
		if _, err := s.GetItem("i1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("old item still present after re-scan, got %v", err)
		}

		// Deleting the library cascades to its items and indexes.
		if err := s.DeleteLibrary("lib1"); err != nil {
			t.Fatalf("DeleteLibrary: %v", err)
		}
		if all, _ := s.AllItems(); len(all) != 0 {
			t.Fatalf("items not cascaded on library delete: %d remain", len(all))
		}
		if got, _ := s.ListItemsByParent("lib1"); len(got) != 0 {
			t.Fatalf("parent index not cleared on library delete: %d", len(got))
		}
		if _, err := s.GetLibrary("lib1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("library should be gone, got %v", err)
		}

		if err := s.ReplaceLibraryItems("ghost", nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ReplaceLibraryItems on missing library should be ErrNotFound, got %v", err)
		}
	})
}

func TestUserItemDataConformance(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		// Two users, one with a prefix of the other's ID, to catch composite-key
		// prefix bugs (e.g. "u1" leaking into "u12"'s listing).
		if err := s.UpsertUserItemData(&UserItemData{UserID: "u1", ItemID: "i1", PlaybackPositionTicks: 5}); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertUserItemData(&UserItemData{UserID: "u1", ItemID: "i2", Played: true}); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertUserItemData(&UserItemData{UserID: "u12", ItemID: "i1", IsFavorite: true}); err != nil {
			t.Fatal(err)
		}

		got, err := s.GetUserItemData("u1", "i1")
		if err != nil || got.PlaybackPositionTicks != 5 {
			t.Fatalf("GetUserItemData = %+v, %v", got, err)
		}
		if _, err := s.GetUserItemData("u1", "missing"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing userdata should be ErrNotFound, got %v", err)
		}

		// Upsert overwrites.
		if err := s.UpsertUserItemData(&UserItemData{UserID: "u1", ItemID: "i1", PlaybackPositionTicks: 99}); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.GetUserItemData("u1", "i1"); got.PlaybackPositionTicks != 99 {
			t.Fatalf("upsert did not overwrite: %d", got.PlaybackPositionTicks)
		}

		list, _ := s.ListUserItemData("u1")
		if len(list) != 2 {
			t.Fatalf("ListUserItemData(u1) = %d, want 2 (u12 must not leak in)", len(list))
		}
		if list2, _ := s.ListUserItemData("u12"); len(list2) != 1 {
			t.Fatalf("ListUserItemData(u12) = %d, want 1", len(list2))
		}
	})
}

func TestRecordingsConformance(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		rec := &Recording{ID: "r1", ChannelID: "c1", Name: "Show", Status: RecScheduled, CreatedAt: time.Now().UTC()}
		if err := s.CreateRecording(rec); err != nil {
			t.Fatal(err)
		}
		if got, err := s.GetRecording("r1"); err != nil || got.Status != RecScheduled {
			t.Fatalf("GetRecording = %+v, %v", got, err)
		}
		rec.Status = RecCompleted
		if err := s.UpdateRecording(rec); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.GetRecording("r1"); got.Status != RecCompleted {
			t.Fatalf("update not persisted: %q", got.Status)
		}
		if err := s.UpdateRecording(&Recording{ID: "ghost"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("updating missing recording should be ErrNotFound, got %v", err)
		}
		if list, _ := s.ListRecordings(); len(list) != 1 {
			t.Fatalf("ListRecordings = %d, want 1", len(list))
		}
		if err := s.DeleteRecording("r1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetRecording("r1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("recording should be gone, got %v", err)
		}
		if err := s.DeleteRecording("r1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleting missing recording should be ErrNotFound, got %v", err)
		}
	})
}

func TestSeriesTimersConformance(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		st := &SeriesTimer{ID: "st1", Name: "NOVA", ChannelID: "c1", CreatedAt: time.Now().UTC()}
		if err := s.CreateSeriesTimer(st); err != nil {
			t.Fatal(err)
		}
		if got, err := s.GetSeriesTimer("st1"); err != nil || got.Name != "NOVA" {
			t.Fatalf("GetSeriesTimer = %+v, %v", got, err)
		}
		if list, _ := s.ListSeriesTimers(); len(list) != 1 {
			t.Fatalf("ListSeriesTimers = %d, want 1", len(list))
		}
		if err := s.DeleteSeriesTimer("st1"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetSeriesTimer("st1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("series timer should be gone, got %v", err)
		}
		if err := s.DeleteSeriesTimer("st1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleting missing series timer should be ErrNotFound, got %v", err)
		}
	})
}

// TestConcurrentAccess hammers each backend with concurrent readers and
// writers; run under -race it proves the store is safe for the server's
// many-goroutines-per-request usage.
func TestConcurrentAccess(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		s.CreateLibrary(&Library{ID: "lib1", Path: "/m"})
		s.ReplaceLibraryItems("lib1", []*MediaItem{{ID: "i1", ParentID: "lib1", Name: "X"}})

		const workers = 16
		done := make(chan struct{})
		for w := 0; w < workers; w++ {
			go func(w int) {
				defer func() { done <- struct{}{} }()
				for i := 0; i < 100; i++ {
					// Mixed writes and reads across entity types.
					_ = s.UpsertUserItemData(&UserItemData{
						UserID: fmt.Sprintf("u%d", w), ItemID: "i1", PlaybackPositionTicks: int64(i),
					})
					_, _ = s.GetItem("i1")
					_, _ = s.ListItemsByParent("lib1")
					_, _ = s.ListUserItemData(fmt.Sprintf("u%d", w))
				}
			}(w)
		}
		for w := 0; w < workers; w++ {
			<-done
		}

		// Each worker's userdata must be independently intact.
		for w := 0; w < workers; w++ {
			got, err := s.GetUserItemData(fmt.Sprintf("u%d", w), "i1")
			if err != nil || got.PlaybackPositionTicks != 99 {
				t.Fatalf("worker %d userdata = %+v, %v", w, got, err)
			}
		}
	})
}

// TestBoltDurability confirms writes survive a close/reopen cycle.
func TestBoltDurability(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenBolt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(&User{ID: "u1", Name: "Persisted"}); err != nil {
		t.Fatal(err)
	}
	lib := &Library{ID: "lib1", Path: "/m"}
	s.CreateLibrary(lib)
	s.ReplaceLibraryItems("lib1", []*MediaItem{{ID: "i1", ParentID: "lib1", Name: "Keep"}})
	s.Close()

	reopened, err := OpenBolt(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.GetUserByName("Persisted"); err != nil {
		t.Fatalf("user did not survive reopen: %v", err)
	}
	if got, _ := reopened.ListItemsByParent("lib1"); len(got) != 1 {
		t.Fatalf("items/index did not survive reopen: %d", len(got))
	}
}

// TestBoltIndexBenchmarkShape is a lightweight sanity check that large-library
// listing is index-scoped (returns only the parent's children), not a full
// scan of everything.
func TestBoltLargeLibraryListing(t *testing.T) {
	s, err := OpenBolt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.CreateLibrary(&Library{ID: "lib1", Path: "/m"})

	const n = 5000
	items := make([]*MediaItem, 0, n)
	for i := 0; i < n; i++ {
		parent := "lib1"
		if i%2 == 0 {
			parent = "season1"
		}
		items = append(items, &MediaItem{ID: fmt.Sprintf("i%05d", i), ParentID: parent, Name: fmt.Sprintf("Item %d", i)})
	}
	if err := s.ReplaceLibraryItems("lib1", items); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ListItemsByParent("season1"); len(got) != n/2 {
		t.Fatalf("ListItemsByParent(season1) = %d, want %d", len(got), n/2)
	}
}
