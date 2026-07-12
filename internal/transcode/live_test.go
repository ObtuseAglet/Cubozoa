package transcode

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeVP8 renders a short VP8/WebM clip — a codec our live path re-encodes to
// H.264 — and returns its path.
func makeVP8(t *testing.T) string {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	out := filepath.Join(t.TempDir(), "clip.webm")
	cmd := exec.Command(ffmpeg, "-y", "-nostdin",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=128x96:rate=10",
		"-c:v", "libvpx", "-b:v", "200k", "-an", "-f", "webm", out)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("libvpx unavailable: %v\n%s", err, combined)
	}
	return out
}

func TestLiveSessionFailureDetected(t *testing.T) {
	mgr, ok := NewManager("", t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !ok {
		t.Skip("ffmpeg not available")
	}
	defer mgr.Close()

	// A non-media input: ffmpeg cannot demux it and exits without producing a
	// playlist, which Playlist must report as ErrSessionFailed.
	bogus := filepath.Join(t.TempDir(), "garbage.ts")
	os.WriteFile(bogus, []byte("this is not a media stream"), 0o644)

	if _, err := mgr.EnsureLiveSession("bad", bogus); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Playlist("bad"); !errors.Is(err, ErrSessionFailed) {
		t.Fatalf("expected ErrSessionFailed, got %v", err)
	}
}

func TestLiveTranscodeReencodesToH264(t *testing.T) {
	video := makeVP8(t) // VP8 source
	mgr, ok := NewManager("", t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !ok {
		t.Skip("ffmpeg not available")
	}
	defer mgr.Close()

	if _, err := mgr.EnsureLiveTranscodeSession("tc", video); err != nil {
		t.Fatal(err)
	}
	playlist, err := mgr.Playlist("tc")
	if err != nil {
		t.Fatalf("playlist: %v", err)
	}
	var seg string
	for _, line := range strings.Split(string(playlist), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			seg = line
			break
		}
	}
	if seg == "" {
		t.Fatalf("no segment in live playlist:\n%s", playlist)
	}
	path, err := mgr.Segment("tc", seg)
	if err != nil {
		t.Fatalf("segment: %v", err)
	}

	// The re-encoded segment must be H.264, proving the VP8 source was transcoded.
	ffprobe, _ := exec.LookPath("ffprobe")
	out, _ := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name", "-of", "csv=p=0", path).Output()
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, "h264") || strings.Contains(got, "vp8") {
		t.Fatalf("segment codec = %q, want h264 (transcoded from vp8)", got)
	}
	_ = context.Background()
}
