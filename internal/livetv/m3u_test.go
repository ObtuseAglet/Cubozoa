package livetv

import "testing"

const samplePlaylist = `#EXTM3U
#EXTINF:-1 tvg-id="bbc1.uk" tvg-name="BBC One" tvg-logo="http://logo/bbc1.png" group-title="UK",BBC One HD
http://example.com/stream/bbc1.m3u8
#EXTINF:-1 tvg-id="cnn.us" tvg-logo="http://logo/cnn.png" group-title="News" tvg-chno="202",CNN International
http://example.com/stream/cnn.ts
#EXTINF:-1,Channel With, Comma
http://example.com/stream/comma.ts
# a stray comment
#EXTINF:-1 tvg-id="skip",Not A Stream
not-a-url
`

func TestParseM3U(t *testing.T) {
	chans := ParseM3U([]byte(samplePlaylist))
	if len(chans) != 3 {
		t.Fatalf("expected 3 channels, got %d: %+v", len(chans), chans)
	}

	bbc := chans[0]
	if bbc.Name != "BBC One HD" || bbc.TvgID != "bbc1.uk" || bbc.Group != "UK" {
		t.Fatalf("bbc parsed wrong: %+v", bbc)
	}
	if bbc.Logo != "http://logo/bbc1.png" || bbc.URL != "http://example.com/stream/bbc1.m3u8" {
		t.Fatalf("bbc logo/url wrong: %+v", bbc)
	}
	if bbc.ID == "" {
		t.Fatal("channel ID must be set")
	}

	cnn := chans[1]
	if cnn.Number != "202" || cnn.Name != "CNN International" || cnn.Group != "News" {
		t.Fatalf("cnn parsed wrong: %+v", cnn)
	}

	// A display name containing a comma is preserved.
	if chans[2].Name != "Channel With, Comma" {
		t.Fatalf("comma name = %q", chans[2].Name)
	}
}

func TestParseM3UStableIDs(t *testing.T) {
	a := ParseM3U([]byte(samplePlaylist))
	b := ParseM3U([]byte(samplePlaylist))
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("channel %d ID not stable: %s vs %s", i, a[i].ID, b[i].ID)
		}
	}
	// tvg-id drives identity, so reordering the URL does not change the ID.
	reordered := ParseM3U([]byte("#EXTM3U\n#EXTINF:-1 tvg-id=\"bbc1.uk\",BBC One HD\nhttp://other/url.ts\n"))
	if reordered[0].ID != a[0].ID {
		t.Fatal("tvg-id should determine a stable ID independent of URL")
	}
}

func TestParseM3UEmpty(t *testing.T) {
	if len(ParseM3U([]byte("#EXTM3U\n"))) != 0 {
		t.Fatal("empty playlist should yield no channels")
	}
	if len(ParseM3U(nil)) != 0 {
		t.Fatal("nil playlist should yield no channels")
	}
}
