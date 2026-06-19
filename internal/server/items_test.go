package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/userdata"
)

// newTestServerWithLibrary wires a server backed by a scanned movie library and
// returns the server, a valid token, and the library ID.
func newTestServerWithLibrary(t *testing.T, files []string) (*httptest.Server, string, string) {
	t.Helper()

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

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	const password = "browse-password-1"
	if _, _, err := authSvc.SeedAdmin("admin", password); err != nil {
		t.Fatal(err)
	}

	mediaSvc := media.NewService(st, log)
	if err := mediaSvc.SyncLibrariesFromMediaDir(mediaDir); err != nil {
		t.Fatal(err)
	}
	if err := mediaSvc.ScanAll(); err != nil {
		t.Fatal(err)
	}
	libs, _ := mediaSvc.Libraries()

	cfg := &config.Config{ServerName: "Test"}
	ts := httptest.NewServer(New(cfg, st, authSvc, mediaSvc, userdata.New(st), log).Handler())
	t.Cleanup(ts.Close)

	token, _ := login(t, ts.URL, "admin", password)
	return ts, token, libs[0].ID
}

func authGet(t *testing.T, url, token string, v any) int {
	t.Helper()
	r, _ := http.NewRequest(http.MethodGet, url, nil)
	r.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if v != nil && resp.StatusCode == http.StatusOK {
		json.NewDecoder(resp.Body).Decode(v)
	}
	return resp.StatusCode
}

func TestViewsListLibraries(t *testing.T) {
	ts, token, libID := newTestServerWithLibrary(t, []string{"A (2001).mkv"})

	var views jellyfin.QueryResult[jellyfin.BaseItemDto]
	if code := authGet(t, ts.URL+"/Users/u/Views", token, &views); code != http.StatusOK {
		t.Fatalf("views status = %d", code)
	}
	if views.TotalRecordCount != 1 {
		t.Fatalf("views count = %d, want 1", views.TotalRecordCount)
	}
	v := views.Items[0]
	if v.ID != libID || v.Type != "CollectionFolder" || v.CollectionType != "movies" || !v.IsFolder {
		t.Fatalf("unexpected view dto: %+v", v)
	}
}

func TestBrowseItemsByParent(t *testing.T) {
	ts, token, libID := newTestServerWithLibrary(t, []string{"A (2001).mkv", "B (2002).mkv"})

	var res jellyfin.QueryResult[jellyfin.BaseItemDto]
	code := authGet(t, ts.URL+"/Users/u/Items?ParentId="+libID+"&IncludeItemTypes=Movie", token, &res)
	if code != http.StatusOK {
		t.Fatalf("items status = %d", code)
	}
	if res.TotalRecordCount != 2 {
		t.Fatalf("items count = %d, want 2", res.TotalRecordCount)
	}
	for _, it := range res.Items {
		if it.Type != "Movie" || it.MediaType != "Video" {
			t.Fatalf("unexpected item dto: %+v", it)
		}
	}
}

func TestItemDetailAndNotFound(t *testing.T) {
	ts, token, libID := newTestServerWithLibrary(t, []string{"Dune (2021).mkv"})

	var list jellyfin.QueryResult[jellyfin.BaseItemDto]
	authGet(t, ts.URL+"/Items?ParentId="+libID, token, &list)
	id := list.Items[0].ID

	var detail jellyfin.BaseItemDto
	if code := authGet(t, ts.URL+"/Items/"+id, token, &detail); code != http.StatusOK {
		t.Fatalf("detail status = %d", code)
	}
	if detail.Name != "Dune" || detail.ProductionYear != 2021 {
		t.Fatalf("unexpected detail: %+v", detail)
	}

	if code := authGet(t, ts.URL+"/Items/deadbeef", token, nil); code != http.StatusNotFound {
		t.Fatalf("missing item status = %d, want 404", code)
	}
}

func TestBrowseRequiresAuth(t *testing.T) {
	ts, _, libID := newTestServerWithLibrary(t, []string{"A (2001).mkv"})
	resp, err := http.Get(ts.URL + "/Items?ParentId=" + libID)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous browse status = %d, want 401", resp.StatusCode)
	}
}

// TestItemDtoNeverLeaksPath guards the security property that client-facing item
// responses never expose the server's filesystem layout.
func TestItemDtoNeverLeaksPath(t *testing.T) {
	ts, token, libID := newTestServerWithLibrary(t, []string{"Secret Movie (2020).mkv"})

	r, _ := http.NewRequest(http.MethodGet, ts.URL+"/Items?ParentId="+libID, nil)
	r.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	got := string(body)

	for _, leak := range []string{`"Path"`, "/Movies/", ".mkv"} {
		if strings.Contains(got, leak) {
			t.Fatalf("client response leaked filesystem detail %q: %s", leak, got)
		}
	}
}
