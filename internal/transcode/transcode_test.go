package transcode

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeTestVideo renders a short H.264/AAC test clip with ffmpeg and returns its
// path. The whole transcode test suite is skipped when ffmpeg is unavailable
// (e.g. in a minimal CI image), so these tests never fail for lack of a binary.
func makeTestVideo(t *testing.T) string {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available; skipping transcode tests")
	}
	out := filepath.Join(t.TempDir(), "clip.mp4")
	cmd := exec.Command(ffmpeg,
		"-y", "-nostdin",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=12",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest",
		out,
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendering test video: %v\n%s", err, combined)
	}
	return out
}

func TestProbeExtractsMetadata(t *testing.T) {
	prober, ok := NewProber("")
	if !ok {
		t.Skip("ffprobe not available")
	}
	video := makeTestVideo(t)

	res, err := prober.Probe(context.Background(), video)
	if err != nil {
		t.Fatal(err)
	}
	if res.DurationTicks <= 0 {
		t.Fatalf("expected positive duration, got %d", res.DurationTicks)
	}
	if res.VideoCodec != "h264" {
		t.Fatalf("video codec = %q, want h264", res.VideoCodec)
	}
	if res.Width != 160 || res.Height != 120 {
		t.Fatalf("dimensions = %dx%d, want 160x120", res.Width, res.Height)
	}
	var haveVideo, haveAudio bool
	for _, s := range res.Streams {
		switch s.Type {
		case "Video":
			haveVideo = true
		case "Audio":
			haveAudio = true
		}
	}
	if !haveVideo || !haveAudio {
		t.Fatalf("expected video+audio streams, got %+v", res.Streams)
	}
}

func TestProbeMissingFile(t *testing.T) {
	prober, ok := NewProber("")
	if !ok {
		t.Skip("ffprobe not available")
	}
	if _, err := prober.Probe(context.Background(), "/no/such/file.mp4"); err == nil {
		t.Fatal("expected an error probing a missing file")
	}
}

func TestHLSSessionProducesPlaylistAndSegments(t *testing.T) {
	video := makeTestVideo(t) // also ensures ffmpeg exists
	mgr, ok := NewManager("", t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !ok {
		t.Skip("ffmpeg not available")
	}
	defer mgr.Close()

	const id = "test-session-1"
	if _, err := mgr.EnsureSession(id, video); err != nil {
		t.Fatal(err)
	}

	playlist, err := mgr.Playlist(id)
	if err != nil {
		t.Fatalf("playlist: %v", err)
	}
	text := string(playlist)
	if !strings.HasPrefix(text, "#EXTM3U") {
		t.Fatalf("playlist does not look like HLS:\n%s", text)
	}
	if !strings.Contains(text, ".ts") {
		t.Fatalf("playlist has no segments:\n%s", text)
	}

	// Resolve the first segment named in the playlist.
	var segLine string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			segLine = line
			break
		}
	}
	if segLine == "" {
		t.Fatal("no segment line found in playlist")
	}
	path, err := mgr.Segment(id, segLine)
	if err != nil {
		t.Fatalf("segment %q: %v", segLine, err)
	}
	if filepath.Base(path) != segLine {
		t.Fatalf("resolved segment = %q, want %q", path, segLine)
	}

	// Reusing the id returns the same session, not a new one.
	if _, err := mgr.EnsureSession(id, video); err != nil {
		t.Fatal(err)
	}
}

func TestSegmentNameValidation(t *testing.T) {
	video := makeTestVideo(t)
	mgr, ok := NewManager("", t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !ok {
		t.Skip("ffmpeg not available")
	}
	defer mgr.Close()
	mgr.EnsureSession("s1", video)

	for _, bad := range []string{"../../etc/passwd", "seg0.mp4", "index.m3u8", "seg.ts", "../seg00000.ts"} {
		if _, err := mgr.Segment("s1", bad); err == nil {
			t.Errorf("expected rejection of segment name %q", bad)
		}
	}
}
