package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImageContentType(t *testing.T) {
	cases := map[string]string{
		"a.jpg": "image/jpeg", "a.JPEG": "image/jpeg", "a.png": "image/png",
		"a.webp": "image/webp", "a.gif": "image/gif", "a.txt": "application/octet-stream",
	}
	for in, want := range cases {
		if got := imageContentType(in); got != want {
			t.Errorf("imageContentType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithinRoot(t *testing.T) {
	root := filepath.FromSlash("/srv/media/Movies")
	if !withinRoot(root, filepath.FromSlash("/srv/media/Movies/x/poster.jpg")) {
		t.Error("nested path should be within root")
	}
	if withinRoot(root, filepath.FromSlash("/srv/media/Other/poster.jpg")) {
		t.Error("sibling path must NOT be within root")
	}
	if withinRoot(root, filepath.FromSlash("/etc/passwd")) {
		t.Error("absolute escape must NOT be within root")
	}
}

func TestImageTagChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "poster.jpg")
	if err := os.WriteFile(p, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	t1 := imageTag(p)
	if t1 == "" {
		t.Fatal("tag should be non-empty for an existing file")
	}
	if err := os.WriteFile(p, []byte("different content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if imageTag(p) == t1 {
		t.Error("tag should change when the file content/size changes")
	}
	if imageTag(filepath.Join(dir, "missing.jpg")) != "" {
		t.Error("tag for a missing file should be empty")
	}
}

// writeFiles creates empty files under dir.
func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindArtworkNameMatched(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "The Matrix (1999).mkv", "The Matrix (1999).jpg", "The Matrix (1999)-fanart.jpg")
	d := newDirLister()
	primary, backdrop := findArtwork(d, filepath.Join(dir, "The Matrix (1999).mkv"))
	if filepath.Base(primary) != "The Matrix (1999).jpg" {
		t.Errorf("primary = %q", primary)
	}
	if filepath.Base(backdrop) != "The Matrix (1999)-fanart.jpg" {
		t.Errorf("backdrop = %q", backdrop)
	}
}

func TestFindArtworkGenericOnlyWhenSoleVideo(t *testing.T) {
	// Sole video in its own folder: poster.jpg/fanart.jpg apply to it.
	soleDir := t.TempDir()
	writeFiles(t, soleDir, "movie.mkv", "poster.jpg", "fanart.jpg")
	d := newDirLister()
	primary, backdrop := findArtwork(d, filepath.Join(soleDir, "movie.mkv"))
	if filepath.Base(primary) != "poster.jpg" || filepath.Base(backdrop) != "fanart.jpg" {
		t.Errorf("sole video: primary=%q backdrop=%q", primary, backdrop)
	}

	// Multiple videos sharing a directory: a generic poster.jpg is ambiguous
	// and must NOT be attached to either video.
	sharedDir := t.TempDir()
	writeFiles(t, sharedDir, "a.mkv", "b.mkv", "poster.jpg")
	d2 := newDirLister()
	primary, _ = findArtwork(d2, filepath.Join(sharedDir, "a.mkv"))
	if primary != "" {
		t.Errorf("shared dir: generic poster should be ignored, got %q", primary)
	}
}
