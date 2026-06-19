package media

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/metadata"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// fakeProvider is a metadata.Provider returning canned data and pointing image
// URLs at a test server.
type fakeProvider struct{ imageURL string }

func (f fakeProvider) Movie(_ context.Context, title string, year int) (*metadata.Result, error) {
	if title == "Unknown" {
		return nil, metadata.ErrNotFound
	}
	return &metadata.Result{
		Title:           "Official " + title,
		Overview:        "An overview.",
		Year:            2010,
		Rating:          7.5,
		Genres:          []string{"Drama", "Thriller"},
		PrimaryImageURL: f.imageURL,
	}, nil
}

func (f fakeProvider) Series(_ context.Context, title string, year int) (*metadata.Result, error) {
	return &metadata.Result{Title: title, Overview: "Series overview.", Genres: []string{"Comedy"}}, nil
}

func enrichTestService(t *testing.T, files []string) (*Service, *store.Library, string) {
	t.Helper()
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("\xFF\xD8\xFFfake-jpeg-bytes"))
	}))
	t.Cleanup(imgSrv.Close)

	mediaDir := t.TempDir()
	movies := filepath.Join(mediaDir, "Movies")
	if err := os.MkdirAll(movies, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(movies, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	svc := NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cacheDir := filepath.Join(t.TempDir(), "metadata-images")
	svc.SetEnricher(fakeProvider{imageURL: imgSrv.URL + "/poster.jpg"}, cacheDir)
	if err := svc.SyncLibrariesFromMediaDir(mediaDir); err != nil {
		t.Fatal(err)
	}
	if err := svc.ScanAll(); err != nil {
		t.Fatal(err)
	}
	libs, _ := svc.Libraries()
	return svc, libs[0], cacheDir
}

func TestEnrichFillsMetadataAndDownloadsArtwork(t *testing.T) {
	svc, lib, cacheDir := enrichTestService(t, []string{"Inception (2010).mkv"})

	items, _, _ := svc.Browse(BrowseQuery{ParentID: lib.ID})
	it := items[0]

	if it.Name != "Official Inception" {
		t.Fatalf("name not refined: %q", it.Name)
	}
	if it.Overview != "An overview." || it.CommunityRating != 7.5 {
		t.Fatalf("overview/rating not set: %+v", it)
	}
	if len(it.Genres) != 2 {
		t.Fatalf("genres = %v", it.Genres)
	}

	// Poster was downloaded into the cache and resolves via ItemImage.
	if it.PrimaryImagePath == "" {
		t.Fatal("expected downloaded poster path")
	}
	if filepath.Dir(it.PrimaryImagePath) != cacheDir {
		t.Fatalf("poster not in cache dir: %q", it.PrimaryImagePath)
	}
	img, err := svc.ItemImage(it.ID, "Primary")
	if err != nil {
		t.Fatalf("cached image should be served: %v", err)
	}
	if data, err := os.ReadFile(img.Path); err != nil || len(data) == 0 {
		t.Fatalf("cached image unreadable: %v", err)
	}
}

func TestEnrichKeepsLocalArtwork(t *testing.T) {
	// A movie with a local poster must not have it overwritten by the provider.
	svc, lib, cacheDir := enrichTestService(t, []string{"Inception (2010).mkv", "Inception (2010).jpg"})
	items, _, _ := svc.Browse(BrowseQuery{ParentID: lib.ID})
	it := items[0]
	if filepath.Dir(it.PrimaryImagePath) == cacheDir {
		t.Fatalf("local artwork should win over downloaded: %q", it.PrimaryImagePath)
	}
	// Metadata text is still applied.
	if it.Overview == "" {
		t.Fatal("overview should still be enriched")
	}
}

func TestEnrichNoMatchIsNonFatal(t *testing.T) {
	svc, lib, _ := enrichTestService(t, []string{"Unknown (1999).mkv"})
	items, _, _ := svc.Browse(BrowseQuery{ParentID: lib.ID})
	if items[0].Overview != "" {
		t.Fatal("no-match item should have no overview")
	}
	if items[0].Name != "Unknown" {
		t.Fatalf("name should be unchanged on no match: %q", items[0].Name)
	}
}
