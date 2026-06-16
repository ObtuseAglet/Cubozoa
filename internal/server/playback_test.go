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
)

// newPlaybackServer builds a server whose Movies library holds one movie file
// with known bytes, so streaming and Range behavior can be asserted exactly.
func newPlaybackServer(t *testing.T, content []byte) (*httptest.Server, string, string) {
	t.Helper()

	mediaDir := t.TempDir()
	movies := filepath.Join(mediaDir, "Movies")
	if err := os.MkdirAll(movies, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(movies, "Sample (2020).mp4"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	const password = "playback-pass-1"
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

	ts := httptest.NewServer(New(&config.Config{ServerName: "Test"}, st, authSvc, mediaSvc, log).Handler())
	t.Cleanup(ts.Close)

	token, _ := login(t, ts.URL, "admin", password)

	libs, _ := mediaSvc.Libraries()
	var list jellyfin.QueryResult[jellyfin.BaseItemDto]
	authGet(t, ts.URL+"/Items?ParentId="+libs[0].ID, token, &list)
	return ts, token, list.Items[0].ID
}

func TestPlaybackInfoAdvertisesDirectPlay(t *testing.T) {
	ts, token, itemID := newPlaybackServer(t, []byte("hello world"))

	r, _ := http.NewRequest(http.MethodPost, ts.URL+"/Items/"+itemID+"/PlaybackInfo", strings.NewReader(`{"DeviceProfile":{}}`))
	r.Header.Set("X-Emby-Token", token)
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var info jellyfin.PlaybackInfoResponse
	json.NewDecoder(resp.Body).Decode(&info)
	if info.PlaySessionID == "" {
		t.Fatal("missing PlaySessionId")
	}
	if len(info.MediaSources) != 1 {
		t.Fatalf("expected 1 media source, got %d", len(info.MediaSources))
	}
	src := info.MediaSources[0]
	if !src.SupportsDirectPlay || src.SupportsTranscoding {
		t.Fatalf("unexpected source capabilities: %+v", src)
	}
	if src.Container != "mp4" || src.ID != itemID {
		t.Fatalf("unexpected source: %+v", src)
	}
}

func TestStreamFullContent(t *testing.T) {
	body := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	ts, token, itemID := newPlaybackServer(t, body)

	r, _ := http.NewRequest(http.MethodGet, ts.URL+"/Videos/"+itemID+"/stream.mp4?static=true&api_key="+token, nil)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("content-type = %q, want video/mp4", ct)
	}
	if ar := resp.Header.Get("Accept-Ranges"); ar != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", ar)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(body) {
		t.Fatalf("body = %q, want %q", got, body)
	}
}

func TestStreamRangeRequest(t *testing.T) {
	body := []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	ts, token, itemID := newPlaybackServer(t, body)

	r, _ := http.NewRequest(http.MethodGet, ts.URL+"/Videos/"+itemID+"/stream?api_key="+token, nil)
	r.Header.Set("Range", "bytes=5-9")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "bytes 5-9/26" {
		t.Fatalf("Content-Range = %q, want bytes 5-9/26", cr)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != "FGHIJ" {
		t.Fatalf("range body = %q, want FGHIJ", got)
	}
}

func TestStreamRequiresAuth(t *testing.T) {
	ts, _, itemID := newPlaybackServer(t, []byte("data"))
	resp, err := http.Get(ts.URL + "/Videos/" + itemID + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestStreamMissingItem404(t *testing.T) {
	ts, token, _ := newPlaybackServer(t, []byte("data"))
	code := authGet(t, ts.URL+"/Videos/deadbeefdeadbeef/stream?api_key="+token, token, nil)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestPlaybackReportAccepted(t *testing.T) {
	ts, token, itemID := newPlaybackServer(t, []byte("data"))
	for _, path := range []string{"/Sessions/Playing", "/Sessions/Playing/Progress", "/Sessions/Playing/Stopped"} {
		r, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(`{"ItemId":"`+itemID+`","PositionTicks":1000}`))
		r.Header.Set("X-Emby-Token", token)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s status = %d, want 204", path, resp.StatusCode)
		}
	}
}
