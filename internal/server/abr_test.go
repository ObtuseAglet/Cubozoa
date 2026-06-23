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

// newABRServer wires a server with a real transcoder over a 720p clip so the
// adaptive ladder offers multiple rungs.
func newABRServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	mediaDir := t.TempDir()
	movies := filepath.Join(mediaDir, "Movies")
	if err := os.MkdirAll(movies, 0o755); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(movies, "Big (2021).mp4")
	render := exec.Command(ffmpeg, "-y", "-nostdin",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=1280x720:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", clip)
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
	authSvc.SeedAdmin("admin", "abr-pass-1")
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

	token, _ := login(t, ts.URL, "admin", "abr-pass-1")
	libs, _ := mediaSvc.Libraries()
	var list jellyfin.QueryResult[jellyfin.BaseItemDto]
	authGet(t, ts.URL+"/Items?ParentId="+libs[0].ID, token, &list)
	return ts, token, list.Items[0].ID
}

func TestABRMasterAndVariant(t *testing.T) {
	ts, token, itemID := newABRServer(t)

	// PlaybackInfo advertises the master playlist as the transcoding URL.
	r, _ := http.NewRequest(http.MethodPost, ts.URL+"/Items/"+itemID+"/PlaybackInfo", strings.NewReader("{}"))
	r.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	var info jellyfin.PlaybackInfoResponse
	decodeJSON(t, resp, &info)
	resp.Body.Close()
	if !strings.Contains(info.MediaSources[0].TranscodingURL, "/master.m3u8") {
		t.Fatalf("transcoding URL is not the ABR master: %q", info.MediaSources[0].TranscodingURL)
	}

	// Fetch the master playlist — it should list multiple variants for 720p.
	master := httpGetBody(t, ts.URL+"/Videos/"+itemID+"/master.m3u8?api_key="+token)
	if !strings.HasPrefix(master, "#EXTM3U") {
		t.Fatalf("master not HLS:\n%s", master)
	}
	streams := strings.Count(master, "#EXT-X-STREAM-INF")
	if streams < 2 {
		t.Fatalf("expected multiple ABR variants for 720p, got %d:\n%s", streams, master)
	}
	if !strings.Contains(master, "RESOLUTION=") || !strings.Contains(master, "BANDWIDTH=") {
		t.Fatalf("master missing stream attributes:\n%s", master)
	}

	// Follow the first variant playlist URL.
	var variantURI string
	for _, line := range strings.Split(master, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			variantURI = line
			break
		}
	}
	if !strings.HasPrefix(variantURI, "hls/") || !strings.HasSuffix(strings.Split(variantURI, "?")[0], "main.m3u8") {
		t.Fatalf("unexpected variant URI: %q", variantURI)
	}

	variant := httpGetBody(t, ts.URL+"/Videos/"+itemID+"/"+variantURI)
	if !strings.HasPrefix(variant, "#EXTM3U") || !strings.Contains(variant, ".ts") {
		t.Fatalf("variant playlist invalid:\n%s", variant)
	}

	// Fetch a segment of that variant (its URIs are relative to the variant).
	var seg string
	for _, line := range strings.Split(variant, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			seg = line
			break
		}
	}
	base := variantURI[:strings.LastIndex(variantURI, "/")+1] // .../hls/{quality}/
	sresp, err := http.Get(ts.URL + "/Videos/" + itemID + "/" + base + seg)
	if err != nil {
		t.Fatal(err)
	}
	defer sresp.Body.Close()
	if sresp.StatusCode != http.StatusOK {
		t.Fatalf("variant segment status = %d", sresp.StatusCode)
	}
	if ct := sresp.Header.Get("Content-Type"); ct != "video/mp2t" {
		t.Fatalf("variant segment content-type = %q", ct)
	}
}

func httpGetBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", url, resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
