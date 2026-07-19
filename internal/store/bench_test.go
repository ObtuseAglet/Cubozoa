package store

import (
	"context"
	"fmt"
	"testing"
)

// benchStores returns a fresh store of each backend for benchmarking.
func benchStores(b *testing.B) map[string]Store {
	b.Helper()
	js, err := OpenJSON(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	bs, err := OpenBolt(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	stores := map[string]Store{"json": js, "bolt": bs}
	if url := testDatabaseURL(); url != "" {
		ps, err := OpenPostgres(url)
		if err != nil {
			b.Fatal(err)
		}
		// Start from an empty schema so counts are deterministic.
		if _, err := ps.(*pgStore).pool.Exec(context.Background(),
			`TRUNCATE users, sessions, libraries, items, user_item_data, recordings, series_timers`); err != nil {
			b.Fatal(err)
		}
		stores["postgres"] = ps
	}
	b.Cleanup(func() {
		for _, s := range stores {
			s.Close()
		}
	})
	return stores
}

func seedLibrary(tb testing.TB, s Store, n int) {
	s.CreateLibrary(&Library{ID: "lib1", Path: "/m"})
	items := make([]*MediaItem, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, &MediaItem{
			ID: fmt.Sprintf("i%06d", i), ParentID: "lib1", LibraryID: "lib1",
			Name: fmt.Sprintf("Item %d", i),
		})
	}
	if err := s.ReplaceLibraryItems("lib1", items); err != nil {
		tb.Fatal(err)
	}
}

// BenchmarkUpsertUserItemData exercises the write-hot path (resume position on
// every playback tick). The JSON store rewrites the whole dataset each call; the
// bolt store writes only the touched pages.
func BenchmarkUpsertUserItemData(b *testing.B) {
	const libSize = 10_000
	for name, s := range benchStores(b) {
		seedLibrary(b, s, libSize)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = s.UpsertUserItemData(&UserItemData{
					UserID: "u1", ItemID: "i000001", PlaybackPositionTicks: int64(i),
				})
			}
		})
	}
}

// BenchmarkGetSessionByTokenHash exercises the read-hot path (every
// authenticated request). JSON scans all sessions; bolt indexes by token hash.
func BenchmarkGetSessionByTokenHash(b *testing.B) {
	const sessions = 2_000
	for name, s := range benchStores(b) {
		for i := 0; i < sessions; i++ {
			s.CreateSession(&Session{
				ID:        fmt.Sprintf("s%06d", i),
				TokenHash: fmt.Sprintf("hash%06d", i),
			})
		}
		target := fmt.Sprintf("hash%06d", sessions-1) // worst case for a scan
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := s.GetSessionByTokenHash(target); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkListItemsByParent exercises browse. JSON scans all items; bolt
// range-scans the parent index.
func BenchmarkListItemsByParent(b *testing.B) {
	const libSize = 50_000
	for name, s := range benchStores(b) {
		s.CreateLibrary(&Library{ID: "lib1", Path: "/m"})
		items := make([]*MediaItem, 0, libSize)
		for i := 0; i < libSize; i++ {
			parent := "other"
			if i < 50 {
				parent = "season1" // only 50 children among 50k items
			}
			items = append(items, &MediaItem{ID: fmt.Sprintf("i%06d", i), ParentID: parent, Name: "x"})
		}
		s.ReplaceLibraryItems("lib1", items)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				got, _ := s.ListItemsByParent("season1")
				if len(got) != 50 {
					b.Fatalf("got %d", len(got))
				}
			}
		})
	}
}
