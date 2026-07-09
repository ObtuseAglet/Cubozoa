package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/livetv"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/transcode"
	"github.com/obtuseaglet/cubozoa/internal/userdata"
)

// newLiveStreamServer wires a server whose single channel points at an httptest
// "upstream" serving a finite MPEG-TS clip, so the live remux path can be
// exercised without a real IPTV feed.
func newLiveStreamServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}

	// Render a short H.264/AAC MPEG-TS clip to act as the upstream stream.
	clip := filepath.Join(t.TempDir(), "up.ts")
	render := exec.Command(ffmpeg, "-y", "-nostdin",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-f", "mpegts", clip)
	if out, err := render.CombinedOutput(); err != nil {
		t.Fatalf("render upstream: %v\n%s", err, out)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, clip)
	}))
	t.Cleanup(upstream.Close)

	m3u := filepath.Join(t.TempDir(), "ch.m3u")
	os.WriteFile(m3u, []byte("#EXTM3U\n#EXTINF:-1 tvg-id=\"test.ch\",Test Channel\n"+upstream.URL+"/live.ts\n"), 0o644)

	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	authSvc.SeedAdmin("admin", "live-pass-1")

	srv := New(&config.Config{ServerName: "Test"}, st, authSvc, media.NewService(st, log), userdata.New(st), log)
	mgr, ok := transcode.NewManager("", t.TempDir(), log)
	if !ok {
		t.Skip("ffmpeg manager unavailable")
	}
	t.Cleanup(mgr.Close)
	srv.SetTranscoder(mgr)

	ltv, _ := livetv.NewService(m3u, time.Hour, log)
	ltv.Start(context.Background())
	t.Cleanup(ltv.Close)
	srv.SetLiveTV(ltv)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	token, _ := login(t, ts.URL, "admin", "live-pass-1")

	var chans jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/LiveTv/Channels", token, &chans)
	if len(chans.Items) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(chans.Items))
	}
	return ts, token, chans.Items[0].ID
}

func TestChannelPlaybackInfoIsInfinite(t *testing.T) {
	ts, token, chID := newLiveStreamServer(t)

	r, _ := http.NewRequest(http.MethodPost, ts.URL+"/Items/"+chID+"/PlaybackInfo", strings.NewReader("{}"))
	r.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	var info jellyfin.PlaybackInfoResponse
	decodeJSON(t, resp, &info)
	resp.Body.Close()

	src := info.MediaSources[0]
	if !src.IsInfiniteStream {
		t.Fatalf("channel source should be infinite: %+v", src)
	}
	if !strings.Contains(src.TranscodingURL, "/live.m3u8") {
		t.Fatalf("channel transcoding URL not the live remux: %q", src.TranscodingURL)
	}
}

func TestLiveStreamRemuxServed(t *testing.T) {
	ts, token, chID := newLiveStreamServer(t)

	playlist := httpGetBody(t, ts.URL+"/Videos/"+chID+"/live.m3u8?api_key="+token)
	if !strings.HasPrefix(playlist, "#EXTM3U") {
		t.Fatalf("live playlist not HLS:\n%s", playlist)
	}
	var seg string
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "live/") {
			seg = line
			break
		}
	}
	if seg == "" {
		t.Fatalf("no live segment in playlist:\n%s", playlist)
	}
	resp, err := http.Get(ts.URL + "/Videos/" + chID + "/" + seg)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("live segment status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp2t" {
		t.Fatalf("live segment content-type = %q", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	if len(b) == 0 {
		t.Fatal("empty live segment")
	}
}
