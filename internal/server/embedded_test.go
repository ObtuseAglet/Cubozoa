package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/transcode"
	"github.com/obtuseaglet/cubozoa/internal/userdata"
)

func TestEmbeddedSubtitleAdvertisedAndDelivered(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}

	mediaDir := t.TempDir()
	movies := filepath.Join(mediaDir, "Movies")
	if err := os.MkdirAll(movies, 0o755); err != nil {
		t.Fatal(err)
	}
	srt := filepath.Join(t.TempDir(), "s.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:02,000\nBaked in subtitle\n"), 0o644)
	clip := filepath.Join(movies, "Embedded (2021).mkv")
	render := exec.Command(ffmpeg, "-y", "-nostdin",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=128x96:rate=10",
		"-i", srt, "-map", "0:v", "-map", "1:0",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:s", "srt", "-shortest", clip)
	if out, err := render.CombinedOutput(); err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}

	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	authSvc.SeedAdmin("admin", "embedded-pass-1")
	mediaSvc := media.NewService(st, log)
	prober, ok := transcode.NewProber("")
	if !ok {
		t.Skip("ffprobe not available")
	}
	mediaSvc.SetProber(prober)
	mediaSvc.SyncLibrariesFromMediaDir(mediaDir)
	mediaSvc.ScanAll()

	srv := New(&config.Config{ServerName: "Test"}, st, authSvc, mediaSvc, userdata.New(st), log)
	mgr, ok := transcode.NewManager("", t.TempDir(), log)
	if !ok {
		t.Skip("ffmpeg manager unavailable")
	}
	t.Cleanup(mgr.Close)
	srv.SetTranscoder(mgr)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	token, _ := login(t, ts.URL, "admin", "embedded-pass-1")
	libs, _ := mediaSvc.Libraries()
	var list jellyfin.QueryResult[jellyfin.BaseItemDto]
	authGet(t, ts.URL+"/Items?ParentId="+libs[0].ID, token, &list)
	itemID := list.Items[0].ID

	// PlaybackInfo advertises the embedded subtitle with an extraction URL.
	r, _ := http.NewRequest(http.MethodPost, ts.URL+"/Items/"+itemID+"/PlaybackInfo", strings.NewReader("{}"))
	r.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	var info jellyfin.PlaybackInfoResponse
	decodeJSON(t, resp, &info)
	resp.Body.Close()

	var subURL string
	for _, ms := range info.MediaSources[0].MediaStreams {
		if ms.Type == "Subtitle" && ms.DeliveryURL != "" {
			subURL = ms.DeliveryURL
		}
	}
	if subURL == "" || !strings.Contains(subURL, "/Subtitles/embedded/") {
		t.Fatalf("embedded subtitle not advertised: %+v", info.MediaSources[0].MediaStreams)
	}

	// Fetch it — it is extracted to WebVTT on the fly.
	sresp, err := http.Get(ts.URL + subURL) // DeliveryUrl already carries api_key
	if err != nil {
		t.Fatal(err)
	}
	defer sresp.Body.Close()
	if sresp.StatusCode != http.StatusOK {
		t.Fatalf("subtitle fetch status = %d", sresp.StatusCode)
	}
	if ct := sresp.Header.Get("Content-Type"); !strings.Contains(ct, "text/vtt") {
		t.Fatalf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(sresp.Body)
	if !strings.HasPrefix(string(body), "WEBVTT") || !strings.Contains(string(body), "Baked in subtitle") {
		t.Fatalf("unexpected subtitle body:\n%s", body)
	}
}
