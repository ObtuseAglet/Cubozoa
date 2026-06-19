package media

import (
	"path/filepath"
	"strings"
	"testing"
)

func parts(p string) []string {
	return strings.Split(filepath.ToSlash(p), "/")
}

func TestParseEpisode(t *testing.T) {
	cases := []struct {
		rel        string
		wantSeries string
		wantSeason int
		wantEp     int
		wantTitle  string
	}{
		{"Breaking Bad/Season 01/Breaking Bad - S01E03 - ...And the Bag's in the River.mkv", "Breaking Bad", 1, 3, "And the Bag's in the River"},
		{"The Office/Season 02/The.Office.S02E05.720p.mkv", "The Office", 2, 5, ""},
		{"Firefly/Firefly 1x04 Shindig.mkv", "Firefly", 1, 4, "Shindig"},
		{"Planet Earth/Planet Earth S01E02.mkv", "Planet Earth", 1, 2, ""},
		{"Loose Show/Season 3/episode without markers.mkv", "Loose Show", 3, 0, "episode without markers"},
	}
	for _, c := range cases {
		series, season, ep, title := parseEpisode(parts(c.rel))
		if series != c.wantSeries || season != c.wantSeason || ep != c.wantEp {
			t.Errorf("parseEpisode(%q) = (%q, S%d E%d), want (%q, S%d E%d)",
				c.rel, series, season, ep, c.wantSeries, c.wantSeason, c.wantEp)
		}
		if c.wantTitle != "" && title != c.wantTitle {
			t.Errorf("parseEpisode(%q) title = %q, want %q", c.rel, title, c.wantTitle)
		}
	}
}

func TestParseEpisodeSeasonFromFolder(t *testing.T) {
	// No SxxExx in the filename: season comes from the folder, episode is 0.
	series, season, ep, _ := parseEpisode(parts("My Show/Season 4/random.mkv"))
	if series != "My Show" || season != 4 || ep != 0 {
		t.Fatalf("got (%q, S%d E%d)", series, season, ep)
	}
}
