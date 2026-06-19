package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/userdata"
)

// newTVServer builds a server with a "TV Shows" library containing one series
// with two seasons (2 + 1 episodes), and returns the server and a token.
func newTVServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	mediaDir := t.TempDir()
	files := []string{
		"TV Shows/Demo Show/Season 01/Demo Show - S01E01 - Pilot.mp4",
		"TV Shows/Demo Show/Season 01/Demo Show - S01E02 - Second.mp4",
		"TV Shows/Demo Show/Season 02/Demo Show - S02E01 - Return.mp4",
	}
	for _, f := range files {
		full := filepath.Join(mediaDir, f)
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

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	const password = "tv-password-1"
	authSvc.SeedAdmin("admin", password)
	mediaSvc := media.NewService(st, log)
	mediaSvc.SyncLibrariesFromMediaDir(mediaDir)
	mediaSvc.ScanAll()

	ts := httptest.NewServer(New(&config.Config{ServerName: "Test"}, st, authSvc, mediaSvc, userdata.New(st), log).Handler())
	t.Cleanup(ts.Close)

	token, _ := login(t, ts.URL, "admin", password)
	return ts, token
}

func TestTVHierarchyBrowse(t *testing.T) {
	ts, token := newTVServer(t)
	uid := meID(t, ts.URL, token)

	// Library view -> one Series.
	var views jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Users/"+uid+"/Views", token, &views)
	libID := views.Items[0].ID

	var seriesList jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Items?ParentId="+libID, token, &seriesList)
	if seriesList.TotalRecordCount != 1 {
		t.Fatalf("expected 1 series, got %d", seriesList.TotalRecordCount)
	}
	series := seriesList.Items[0]
	if series.Type != "Series" || !series.IsFolder || series.Name != "Demo Show" || series.ChildCount != 2 {
		t.Fatalf("unexpected series: %+v", series)
	}

	// Series -> seasons via /Shows endpoint.
	var seasons jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Shows/"+series.ID+"/Seasons", token, &seasons)
	if seasons.TotalRecordCount != 2 {
		t.Fatalf("expected 2 seasons, got %d", seasons.TotalRecordCount)
	}
	if seasons.Items[0].IndexNumber != 1 || seasons.Items[1].IndexNumber != 2 {
		t.Fatalf("seasons not ordered: %+v", seasons.Items)
	}

	// All episodes of the series, ordered season then episode.
	var eps jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Shows/"+series.ID+"/Episodes", token, &eps)
	if eps.TotalRecordCount != 3 {
		t.Fatalf("expected 3 episodes, got %d", eps.TotalRecordCount)
	}
	first := eps.Items[0]
	if first.Type != "Episode" || first.ParentIndexNumber != 1 || first.IndexNumber != 1 {
		t.Fatalf("unexpected first episode: %+v", first)
	}
	if first.SeriesName != "Demo Show" || first.SeriesID != series.ID {
		t.Fatalf("episode not linked to series: %+v", first)
	}

	// Single-season filter.
	season1 := seasons.Items[0]
	var s1eps jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Shows/"+series.ID+"/Episodes?seasonId="+season1.ID, token, &s1eps)
	if s1eps.TotalRecordCount != 2 {
		t.Fatalf("expected 2 episodes in season 1, got %d", s1eps.TotalRecordCount)
	}

	// Browsing a season directly returns its episodes.
	var seasonChildren jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Items?ParentId="+season1.ID, token, &seasonChildren)
	if seasonChildren.TotalRecordCount != 2 {
		t.Fatalf("season browse: expected 2 episodes, got %d", seasonChildren.TotalRecordCount)
	}
}

func TestNextUpAfterWatching(t *testing.T) {
	ts, token := newTVServer(t)
	uid := meID(t, ts.URL, token)

	// Identify the series and its ordered episodes.
	var views jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Users/"+uid+"/Views", token, &views)
	var seriesList jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Items?ParentId="+views.Items[0].ID, token, &seriesList)
	seriesID := seriesList.Items[0].ID

	var eps jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Shows/"+seriesID+"/Episodes", token, &eps)

	// Nothing watched yet -> Next Up is empty.
	var nextUp jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Shows/NextUp", token, &nextUp)
	if nextUp.TotalRecordCount != 0 {
		t.Fatalf("expected empty Next Up, got %d", nextUp.TotalRecordCount)
	}

	// Watch S01E01 -> Next Up becomes S01E02.
	postJSON(t, ts.URL+"/Users/"+uid+"/PlayedItems/"+eps.Items[0].ID, token, nil)
	authReq(t, http.MethodGet, ts.URL+"/Shows/NextUp", token, &nextUp)
	if nextUp.TotalRecordCount != 1 {
		t.Fatalf("expected 1 Next Up item, got %d", nextUp.TotalRecordCount)
	}
	if nextUp.Items[0].ID != eps.Items[1].ID {
		t.Fatalf("Next Up = %q, want S01E02 (%q)", nextUp.Items[0].ID, eps.Items[1].ID)
	}
}

func TestMoviesLibraryStaysFlat(t *testing.T) {
	// Regression: a non-tvshows library must remain a flat list of items.
	ts, token, libID := newTestServerWithLibrary(t, []string{"Solo (2018).mkv", "Dune (2021).mkv"})
	var res jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Items?ParentId="+libID, token, &res)
	if res.TotalRecordCount != 2 {
		t.Fatalf("expected 2 flat movies, got %d", res.TotalRecordCount)
	}
	for _, it := range res.Items {
		if it.Type != "Movie" || it.IsFolder {
			t.Fatalf("movie library produced a non-movie/folder: %+v", it)
		}
	}
}
