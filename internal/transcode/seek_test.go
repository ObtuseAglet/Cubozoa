package transcode

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestFFmpegArgsSeek(t *testing.T) {
	// No seek: there must be no -ss flag.
	from0 := strings.Join(ffmpegArgs("/in.mkv", "/dir", 0, AutoRendition), " ")
	if strings.Contains(from0, "-ss") {
		t.Errorf("start=0 should not seek:\n%s", from0)
	}

	// With an offset: -ss must appear *before* -i (a fast input seek).
	seeked := ffmpegArgs("/in.mkv", "/dir", 42.5, AutoRendition)
	ssIdx, iIdx := indexOf(seeked, "-ss"), indexOf(seeked, "-i")
	if ssIdx < 0 || iIdx < 0 || ssIdx > iIdx {
		t.Fatalf("-ss must precede -i: %v", seeked)
	}
	if seeked[ssIdx+1] != "42.500" {
		t.Errorf("seek seconds = %q, want 42.500", seeked[ssIdx+1])
	}
}

func TestSeekSessionsAreDistinct(t *testing.T) {
	video := makeTestVideo(t)
	mgr, ok := NewManager("", t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !ok {
		t.Skip("ffmpeg not available")
	}
	defer mgr.Close()

	s0, err := mgr.EnsureSession("item:0", video, 0, AutoRendition)
	if err != nil {
		t.Fatal(err)
	}
	s1, err := mgr.EnsureSession("item:10000000", video, 1, AutoRendition)
	if err != nil {
		t.Fatal(err)
	}
	if s0.Dir == s1.Dir || s0.ID == s1.ID {
		t.Fatalf("seek sessions should be distinct: %s vs %s", s0.ID, s1.ID)
	}

	// Both produce a valid playlist.
	for _, key := range []string{"item:0", "item:10000000"} {
		pl, err := mgr.Playlist(key)
		if err != nil {
			t.Fatalf("playlist(%s): %v", key, err)
		}
		if !strings.HasPrefix(string(pl), "#EXTM3U") {
			t.Fatalf("playlist(%s) not HLS:\n%s", key, pl)
		}
	}

	// Re-requesting the same key reuses the session.
	again, _ := mgr.EnsureSession("item:0", video, 0, AutoRendition)
	if again.Dir != s0.Dir {
		t.Fatal("same key should reuse the session")
	}
	_ = context.Background()
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}
