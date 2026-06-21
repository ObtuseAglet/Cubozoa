package transcode

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeVideoWithEmbeddedSub renders a clip with an embedded SRT subtitle track
// and returns its path plus the subtitle stream's absolute index.
func makeVideoWithEmbeddedSub(t *testing.T) (string, int) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	srt := filepath.Join(dir, "sub.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:02,000\nEmbedded hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "clip.mkv")
	cmd := exec.Command(ffmpeg, "-y", "-nostdin",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=128x96:rate=10",
		"-i", srt,
		"-map", "0:v", "-map", "1:0",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:s", "srt", "-shortest",
		out)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendering mkv with subtitle: %v\n%s", err, combined)
	}

	// Find the subtitle stream index via ffprobe.
	prober, ok := NewProber("")
	if !ok {
		t.Skip("ffprobe not available")
	}
	res, err := prober.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Streams {
		if s.Type == "Subtitle" {
			return out, s.Index
		}
	}
	t.Fatal("no subtitle stream found in generated mkv")
	return "", 0
}

func TestExtractSubtitleToVTT(t *testing.T) {
	video, idx := makeVideoWithEmbeddedSub(t)
	mgr, ok := NewManager("", t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !ok {
		t.Skip("ffmpeg not available")
	}
	defer mgr.Close()

	data, err := mgr.ExtractSubtitle(context.Background(), video, idx)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	out := string(data)
	if !strings.HasPrefix(out, "WEBVTT") {
		t.Fatalf("extracted subtitle is not WebVTT:\n%s", out)
	}
	if !strings.Contains(out, "Embedded hello") {
		t.Fatalf("subtitle text missing:\n%s", out)
	}
}
