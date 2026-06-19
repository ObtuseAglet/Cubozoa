package server

import (
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

// newSubtitleServer builds a server with one movie that has an English SRT
// sidecar subtitle, returning the server, token and item ID.
func newSubtitleServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	mediaDir := t.TempDir()
	movies := filepath.Join(mediaDir, "Movies")
	if err := os.MkdirAll(movies, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(movies, "Movie (2020).mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srt := "1\n00:00:01,000 --> 00:00:04,000\nHello there\n"
	if err := os.WriteFile(filepath.Join(movies, "Movie (2020).en.srt"), []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	authSvc.SeedAdmin("admin", "subtitle-pass-1")
	mediaSvc := media.NewService(st, log)
	mediaSvc.SyncLibrariesFromMediaDir(mediaDir)
	mediaSvc.ScanAll()

	ts := httptest.NewServer(New(&config.Config{ServerName: "Test"}, st, authSvc, mediaSvc, userdata.New(st), log).Handler())
	t.Cleanup(ts.Close)

	token, _ := login(t, ts.URL, "admin", "subtitle-pass-1")
	libs, _ := mediaSvc.Libraries()
	var list jellyfin.QueryResult[jellyfin.BaseItemDto]
	authGet(t, ts.URL+"/Items?ParentId="+libs[0].ID, token, &list)
	return ts, token, list.Items[0].ID
}

func TestPlaybackInfoIncludesSubtitleStream(t *testing.T) {
	ts, token, itemID := newSubtitleServer(t)

	r, _ := http.NewRequest(http.MethodPost, ts.URL+"/Items/"+itemID+"/PlaybackInfo", strings.NewReader("{}"))
	r.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var info jellyfin.PlaybackInfoResponse
	decodeJSON(t, resp, &info)

	var sub *jellyfin.MediaStream
	for i := range info.MediaSources[0].MediaStreams {
		if info.MediaSources[0].MediaStreams[i].Type == "Subtitle" {
			sub = &info.MediaSources[0].MediaStreams[i]
		}
	}
	if sub == nil {
		t.Fatalf("no subtitle stream advertised: %+v", info.MediaSources[0].MediaStreams)
	}
	if !sub.IsExternal || sub.DeliveryMethod != "External" || sub.DeliveryURL == "" {
		t.Fatalf("subtitle stream not external/deliverable: %+v", sub)
	}
	if sub.Language != "en" {
		t.Fatalf("subtitle language = %q, want en", sub.Language)
	}
}

func TestSubtitleDeliveredAsVTT(t *testing.T) {
	ts, token, itemID := newSubtitleServer(t)

	resp, err := http.Get(ts.URL + "/Videos/" + itemID + "/Subtitles/0/Stream.vtt?api_key=" + token)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/vtt") {
		t.Fatalf("content-type = %q, want text/vtt", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	got := string(body)
	if !strings.HasPrefix(got, "WEBVTT") {
		t.Fatalf("not converted to VTT:\n%s", got)
	}
	if !strings.Contains(got, "00:00:01.000 --> 00:00:04.000") {
		t.Fatalf("SRT timestamps not converted:\n%s", got)
	}
	if !strings.Contains(got, "Hello there") {
		t.Fatalf("subtitle text missing:\n%s", got)
	}
}

func TestSubtitleRequiresAuthAndBounds(t *testing.T) {
	ts, token, itemID := newSubtitleServer(t)

	// No auth.
	resp, err := http.Get(ts.URL + "/Videos/" + itemID + "/Subtitles/0/Stream.vtt")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated subtitle = %d, want 401", resp.StatusCode)
	}

	// Out-of-range index.
	code := authReq(t, http.MethodGet, ts.URL+"/Videos/"+itemID+"/Subtitles/9/Stream.vtt?api_key="+token, token, nil)
	if code != http.StatusNotFound {
		t.Fatalf("out-of-range subtitle = %d, want 404", code)
	}
}
