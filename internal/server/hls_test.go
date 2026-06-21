package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestRewritePlaylist(t *testing.T) {
	in := "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6.0,\nseg00000.ts\n#EXTINF:4.0,\nseg00001.ts\n#EXT-X-ENDLIST\n"
	out := string(rewritePlaylist([]byte(in), "tok&en", 0))

	// Tag lines are preserved.
	if !strings.Contains(out, "#EXT-X-ENDLIST") || !strings.Contains(out, "#EXTINF:6.0,") {
		t.Fatalf("tag lines were altered:\n%s", out)
	}
	// Segment lines are rewritten with the segment endpoint and an escaped key.
	if !strings.Contains(out, "hls/seg00000.ts?api_key=tok%26en") {
		t.Fatalf("segment not rewritten with api_key:\n%s", out)
	}
	// No bare segment line survives.
	for _, line := range strings.Split(out, "\n") {
		if line == "seg00000.ts" || line == "seg00001.ts" {
			t.Fatalf("found un-rewritten segment line: %q", line)
		}
	}
}

// newTranscodeServer wires a server with a real ffmpeg transcoder over a tiny
// rendered test video, or skips if ffmpeg/ffprobe are unavailable.
func newTranscodeServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available; skipping HLS server test")
	}

	mediaDir := t.TempDir()
	movies := filepath.Join(mediaDir, "Movies")
	if err := exec.Command("mkdir", "-p", movies).Run(); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(movies, "Clip (2021).mp4")
	render := exec.Command(ffmpeg, "-y", "-nostdin",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=12",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", clip)
	if out, err := render.CombinedOutput(); err != nil {
		t.Fatalf("rendering clip: %v\n%s", err, out)
	}

	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	const password = "transcode-pass-1"
	authSvc.SeedAdmin("admin", password)

	mediaSvc := media.NewService(st, log)
	if prober, ok := transcode.NewProber(""); ok {
		mediaSvc.SetProber(prober)
	}
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

	token, _ := login(t, ts.URL, "admin", password)
	libs, _ := mediaSvc.Libraries()
	var list jellyfin.QueryResult[jellyfin.BaseItemDto]
	authGet(t, ts.URL+"/Items?ParentId="+libs[0].ID, token, &list)
	return ts, token, list.Items[0].ID
}

func TestPlaybackInfoAdvertisesTranscoding(t *testing.T) {
	ts, token, itemID := newTranscodeServer(t)

	r, _ := http.NewRequest(http.MethodPost, ts.URL+"/Items/"+itemID+"/PlaybackInfo", strings.NewReader(`{}`))
	r.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var info jellyfin.PlaybackInfoResponse
	decodeJSON(t, resp, &info)

	src := info.MediaSources[0]
	if !src.SupportsTranscoding || src.TranscodingURL == "" {
		t.Fatalf("transcoding not advertised: %+v", src)
	}
	if src.TranscodingSubProtocol != "hls" {
		t.Fatalf("sub-protocol = %q, want hls", src.TranscodingSubProtocol)
	}
	// ffprobe ran during the scan, so duration and streams are populated.
	if src.RunTimeTicks <= 0 {
		t.Fatalf("expected a probed duration, got %d", src.RunTimeTicks)
	}
	if len(src.MediaStreams) == 0 {
		t.Fatal("expected probed media streams")
	}
}

func TestHlsPlaylistAndSegmentServed(t *testing.T) {
	ts, token, itemID := newTranscodeServer(t)

	// Fetch the playlist.
	var playlist string
	{
		r, _ := http.NewRequest(http.MethodGet, ts.URL+"/Videos/"+itemID+"/main.m3u8?api_key="+token, nil)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("playlist status = %d", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "mpegurl") {
			t.Fatalf("playlist content-type = %q", ct)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		playlist = string(b)
	}
	if !strings.HasPrefix(playlist, "#EXTM3U") {
		t.Fatalf("not an HLS playlist:\n%s", playlist)
	}

	// Extract the first rewritten segment URI and fetch it.
	var segPath string
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "hls/") {
			segPath = line
			break
		}
	}
	if segPath == "" {
		t.Fatalf("no rewritten segment in playlist:\n%s", playlist)
	}

	resp, err := http.Get(ts.URL + "/Videos/" + itemID + "/" + segPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("segment status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp2t" {
		t.Fatalf("segment content-type = %q, want video/mp2t", ct)
	}
	b, _ := io.ReadAll(resp.Body)
	if len(b) == 0 {
		t.Fatal("empty segment")
	}
}

func TestHlsSeekPlaylistAndSegment(t *testing.T) {
	ts, token, itemID := newTranscodeServer(t)

	// Request the playlist with a seek offset (1s = 10,000,000 ticks).
	url := ts.URL + "/Videos/" + itemID + "/main.m3u8?StartTimeTicks=10000000&api_key=" + token
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seek playlist status = %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	playlist := string(b)

	// Segment URIs must carry the start offset so they route to the seek session.
	var seg string
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "hls/") {
			seg = line
			break
		}
	}
	if seg == "" || !strings.Contains(seg, "StartTimeTicks=10000000") {
		t.Fatalf("seek not propagated to segment URI:\n%s", playlist)
	}

	// And the seeked segment is served.
	sresp, err := http.Get(ts.URL + "/Videos/" + itemID + "/" + seg)
	if err != nil {
		t.Fatal(err)
	}
	defer sresp.Body.Close()
	if sresp.StatusCode != http.StatusOK {
		t.Fatalf("seek segment status = %d", sresp.StatusCode)
	}
	if ct := sresp.Header.Get("Content-Type"); ct != "video/mp2t" {
		t.Fatalf("seek segment content-type = %q", ct)
	}
}

func TestHlsRequiresAuth(t *testing.T) {
	ts, _, itemID := newTranscodeServer(t)
	resp, err := http.Get(ts.URL + "/Videos/" + itemID + "/main.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated playlist status = %d, want 401", resp.StatusCode)
	}
}
