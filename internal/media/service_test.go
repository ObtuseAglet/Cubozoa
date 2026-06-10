package media

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/store"
)

// buildLibrary creates a media directory tree and returns a Service with the
// library registered and scanned.
func buildLibrary(t *testing.T, files []string) (*Service, *store.Library) {
	t.Helper()
	mediaDir := t.TempDir()
	movies := filepath.Join(mediaDir, "Movies")
	if err := os.MkdirAll(movies, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		full := filepath.Join(movies, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	svc := NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.SyncLibrariesFromMediaDir(mediaDir); err != nil {
		t.Fatal(err)
	}
	if err := svc.ScanAll(); err != nil {
		t.Fatal(err)
	}
	libs, _ := svc.Libraries()
	if len(libs) != 1 {
		t.Fatalf("expected 1 library, got %d", len(libs))
	}
	return svc, libs[0]
}

func TestScanAndBrowse(t *testing.T) {
	svc, lib := buildLibrary(t, []string{
		"The Matrix (1999).mkv",
		"Inception (2010).mp4",
		"poster.jpg",  // non-media: must be ignored
		".hidden.mkv", // hidden: must be ignored
	})

	if lib.Type != "movies" {
		t.Fatalf("library type = %q, want movies", lib.Type)
	}
	if lib.ItemCount != 2 {
		t.Fatalf("item count = %d, want 2 (non-media and hidden excluded)", lib.ItemCount)
	}

	items, total, err := svc.Browse(BrowseQuery{ParentID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("browse total=%d len=%d, want 2", total, len(items))
	}
	if items[0].Type != "Movie" {
		t.Fatalf("item type = %q, want Movie", items[0].Type)
	}
}

func TestBrowseFilteringSortingPaging(t *testing.T) {
	svc, lib := buildLibrary(t, []string{
		"Alpha (2001).mkv",
		"Bravo (2005).mkv",
		"Charlie (2003).mkv",
	})

	// Search narrows to one.
	items, total, _ := svc.Browse(BrowseQuery{ParentID: lib.ID, SearchTerm: "brav"})
	if total != 1 || items[0].Name != "Bravo" {
		t.Fatalf("search: total=%d items=%v", total, items)
	}

	// Sort by year descending.
	items, _, _ = svc.Browse(BrowseQuery{ParentID: lib.ID, SortBy: "ProductionYear", SortDescending: true})
	if items[0].Name != "Bravo" {
		t.Fatalf("sort by year desc: first = %q, want Bravo", items[0].Name)
	}

	// Paging: limit 2, start 1 -> second and third by sort name.
	items, total, _ = svc.Browse(BrowseQuery{ParentID: lib.ID, StartIndex: 1, Limit: 2})
	if total != 3 || len(items) != 2 {
		t.Fatalf("paging: total=%d len=%d", total, len(items))
	}
	if items[0].Name != "Bravo" || items[1].Name != "Charlie" {
		t.Fatalf("paging order: %q, %q", items[0].Name, items[1].Name)
	}
}

func TestScanIsIdempotent(t *testing.T) {
	svc, lib := buildLibrary(t, []string{"Solo (2018).mkv"})
	id1, _, _ := svc.Browse(BrowseQuery{ParentID: lib.ID})
	if err := svc.ScanLibrary(lib.ID); err != nil {
		t.Fatal(err)
	}
	id2, total, _ := svc.Browse(BrowseQuery{ParentID: lib.ID})
	if total != 1 {
		t.Fatalf("rescan produced %d items, want 1 (stable IDs)", total)
	}
	if id1[0].ID != id2[0].ID {
		t.Fatalf("item ID changed across rescans: %q vs %q", id1[0].ID, id2[0].ID)
	}
}

func TestItemPathNeverEmptyButInternal(t *testing.T) {
	svc, lib := buildLibrary(t, []string{"Dune (2021).mkv"})
	items, _, _ := svc.Browse(BrowseQuery{ParentID: lib.ID})
	if items[0].Path == "" {
		t.Fatal("internal item should retain its path")
	}
}
