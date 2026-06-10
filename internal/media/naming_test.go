package media

import "testing"

func TestParseName(t *testing.T) {
	cases := []struct {
		in        string
		wantTitle string
		wantYear  int
	}{
		{"The.Matrix.1999.1080p.BluRay.x264.mkv", "The Matrix", 1999},
		{"Inception (2010).mp4", "Inception", 2010},
		{"Blade_Runner_2049_2017_2160p.mkv", "Blade Runner 2049", 2017},
		{"Some Home Movie.mov", "Some Home Movie", 0},
		{"Arrival.2016.WEB-DL.mkv", "Arrival", 2016},
	}
	for _, c := range cases {
		title, year := parseName(c.in)
		if title != c.wantTitle || year != c.wantYear {
			t.Errorf("parseName(%q) = (%q, %d), want (%q, %d)", c.in, title, year, c.wantTitle, c.wantYear)
		}
	}
}

func TestInferLibraryType(t *testing.T) {
	cases := map[string]string{
		"Movies":      "movies",
		"Films":       "movies",
		"TV Shows":    "tvshows",
		"Series":      "tvshows",
		"Music":       "music",
		"Home Videos": "homevideos",
		"Random":      "mixed",
	}
	for in, want := range cases {
		if got := inferLibraryType(in); got != want {
			t.Errorf("inferLibraryType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMediaKind(t *testing.T) {
	if k, c := mediaKind("/x/movie.MKV"); k != "Video" || c != "mkv" {
		t.Errorf("video: got (%q,%q)", k, c)
	}
	if k, _ := mediaKind("/x/song.flac"); k != "Audio" {
		t.Errorf("audio: got %q", k)
	}
	if k, _ := mediaKind("/x/poster.jpg"); k != "" {
		t.Errorf("non-media should be empty, got %q", k)
	}
}

func TestItemTypeFor(t *testing.T) {
	if got := itemTypeFor("movies", "Video"); got != "Movie" {
		t.Errorf("movies -> %q", got)
	}
	if got := itemTypeFor("music", "Audio"); got != "Audio" {
		t.Errorf("music -> %q", got)
	}
	if got := itemTypeFor("mixed", "Video"); got != "Video" {
		t.Errorf("mixed video -> %q", got)
	}
}
