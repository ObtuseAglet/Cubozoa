package media

import "testing"

func TestParseTrack(t *testing.T) {
	cases := []struct {
		rel        string
		wantArtist string
		wantAlbum  string
		wantTrack  int
		wantTitle  string
	}{
		{"Pink Floyd/The Wall/05 - Another Brick in the Wall.flac", "Pink Floyd", "The Wall", 5, "Another Brick in the Wall"},
		{"Daft Punk/Discovery/01. One More Time.mp3", "Daft Punk", "Discovery", 1, "One More Time"},
		{"Adele/21/03_Turning Tables.m4a", "Adele", "21", 3, "Turning Tables"},
		{"Some Artist/Loose Track.mp3", "Some Artist", "Unknown Album", 0, "Loose Track"},
		{"JustAFile.mp3", "Unknown Artist", "Unknown Album", 0, "JustAFile"},
	}
	for _, c := range cases {
		artist, album, track, title := parseTrack(parts(c.rel))
		if artist != c.wantArtist || album != c.wantAlbum || track != c.wantTrack || title != c.wantTitle {
			t.Errorf("parseTrack(%q) = (%q, %q, %d, %q), want (%q, %q, %d, %q)",
				c.rel, artist, album, track, title, c.wantArtist, c.wantAlbum, c.wantTrack, c.wantTitle)
		}
	}
}
