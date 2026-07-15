package livetv

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNormalizeChannelName(t *testing.T) {
	cases := map[string]string{
		"BBC One HD":     "bbcone",
		"bbc.one":        "bbcone",
		"BBC  One":       "bbcone",
		"CNN FHD":        "cnn",
		"ESPN 1080p":     "espn",
		"Sky Sports UHD": "skysports",
		"Discovery ᴴᴰ":   "discovery",
	}
	for in, want := range cases {
		if got := normalizeChannelName(in); got != want {
			t.Errorf("normalizeChannelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildNameIndex(t *testing.T) {
	idx := buildNameIndex([]guideChannel{
		{ID: "bbc1.uk", Names: []string{"BBC One", "BBC 1"}},
		{ID: "cnn.us", Names: []string{"CNN International"}},
	})
	if idx["bbcone"] != "bbc1.uk" || idx["bbc1"] != "bbc1.uk" {
		t.Fatalf("bbc names not indexed: %v", idx)
	}
	if idx["cnninternational"] != "cnn.us" {
		t.Fatalf("cnn not indexed: %v", idx)
	}
}

// TestGuideMatchesByNameWhenTvgIdMismatched verifies that a playlist channel
// whose tvg-id does not appear in the guide is still matched to programs by its
// display name.
func TestGuideMatchesByNameWhenTvgIdMismatched(t *testing.T) {
	// Guide keyed by a different id than the playlist uses, but same name.
	const guide = `<tv>
	  <channel id="bbcone.uk"><display-name>BBC One</display-name></channel>
	  <programme start="20240115180000 +0000" stop="20240115190000 +0000" channel="bbcone.uk"><title>The News</title></programme>
	</tv>`
	epg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(guide))
	}))
	defer epg.Close()

	pl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// tvg-id "wrong.id" is absent from the guide; the name "BBC One HD" is not.
		w.Write([]byte("#EXTM3U\n#EXTINF:-1 tvg-id=\"wrong.id\",BBC One HD\nhttp://s/bbc.ts\n"))
	}))
	defer pl.Close()

	svc, _ := NewService(pl.URL+"/l.m3u", time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetGuide(epg.URL + "/g.xml")
	if err := svc.reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	chID := svc.Channels()[0].ID
	progs := svc.Programs([]string{chID}, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC))
	if len(progs) != 1 || progs[0].Title != "The News" {
		t.Fatalf("name-based match failed: %+v", progs)
	}
}

func TestGuideDirectTvgIdStillWins(t *testing.T) {
	const guide = `<tv>
	  <channel id="a.tv"><display-name>Alpha</display-name></channel>
	  <programme start="20240115180000 +0000" stop="20240115190000 +0000" channel="a.tv"><title>Direct</title></programme>
	</tv>`
	epg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(guide)) }))
	defer epg.Close()
	pl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#EXTM3U\n#EXTINF:-1 tvg-id=\"a.tv\",Totally Different Name\nhttp://s/a.ts\n"))
	}))
	defer pl.Close()

	svc, _ := NewService(pl.URL+"/l.m3u", time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetGuide(epg.URL + "/g.xml")
	svc.reload(context.Background())
	defer svc.Close()

	chID := svc.Channels()[0].ID
	progs := svc.Programs([]string{chID}, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC))
	if len(progs) != 1 || progs[0].Title != "Direct" {
		t.Fatalf("direct tvg-id match failed: %+v", progs)
	}
}
