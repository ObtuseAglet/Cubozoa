package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/livetv"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/userdata"
)

const testPlaylist = `#EXTM3U
#EXTINF:-1 tvg-id="bbc1.uk" tvg-logo="http://logo/bbc1.png" group-title="UK" tvg-chno="1",BBC One
http://upstream.example/bbc1.m3u8
#EXTINF:-1 tvg-id="cnn.us" group-title="News" tvg-chno="2",CNN
http://upstream.example/cnn.ts
`

func newLiveTVServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	m3u := filepath.Join(t.TempDir(), "channels.m3u")
	if err := os.WriteFile(m3u, []byte(testPlaylist), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	authSvc.SeedAdmin("admin", "livetv-pass-1")

	srv := New(&config.Config{ServerName: "Test"}, st, authSvc, media.NewService(st, log), userdata.New(st), log)
	ltv, ok := livetv.NewService(m3u, time.Hour, log)
	if !ok {
		t.Fatal("live TV service should be enabled with a playlist")
	}
	ltv.Start(context.Background())
	t.Cleanup(ltv.Close)
	srv.SetLiveTV(ltv)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	token, _ := login(t, ts.URL, "admin", "livetv-pass-1")
	return ts, token
}

func TestLiveTvChannelsListed(t *testing.T) {
	ts, token := newLiveTVServer(t)

	var info jellyfin.LiveTvInfo
	authReq(t, http.MethodGet, ts.URL+"/LiveTv/Info", token, &info)
	if !info.IsEnabled {
		t.Fatal("Live TV should report enabled")
	}

	var res jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/LiveTv/Channels", token, &res)
	if res.TotalRecordCount != 2 {
		t.Fatalf("expected 2 channels, got %d", res.TotalRecordCount)
	}
	bbc := res.Items[0]
	if bbc.Type != "TvChannel" || bbc.ChannelType != "TV" || bbc.Number != "1" || bbc.Name != "BBC One" {
		t.Fatalf("unexpected channel dto: %+v", bbc)
	}

	// Single-channel lookup.
	var one jellyfin.BaseItemDto
	if code := authReq(t, http.MethodGet, ts.URL+"/LiveTv/Channels/"+bbc.ID, token, &one); code != http.StatusOK {
		t.Fatalf("channel detail status = %d", code)
	}
	if one.ID != bbc.ID {
		t.Fatalf("channel detail id = %q", one.ID)
	}
}

func TestLiveTvViewPresent(t *testing.T) {
	ts, token := newLiveTVServer(t)
	var views jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/Users/me/Views", token, &views)

	found := false
	for _, v := range views.Items {
		if v.CollectionType == "livetv" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Live TV view missing from /Views: %+v", views.Items)
	}
}

func TestLiveTvEmptyDvrEndpoints(t *testing.T) {
	ts, token := newLiveTVServer(t)
	for _, path := range []string{"/LiveTv/Recordings", "/LiveTv/Timers", "/LiveTv/SeriesTimers", "/LiveTv/Programs"} {
		var res jellyfin.QueryResult[jellyfin.BaseItemDto]
		if code := authReq(t, http.MethodGet, ts.URL+path, token, &res); code != http.StatusOK {
			t.Fatalf("%s status = %d", path, code)
		}
		if res.TotalRecordCount != 0 {
			t.Fatalf("%s should be empty, got %d", path, res.TotalRecordCount)
		}
	}
}

const guideXML = `<?xml version="1.0"?>
<tv>
  <programme start="20240115180000 +0000" stop="20240115190000 +0000" channel="bbc1.uk">
    <title>Evening News</title><desc>Headlines.</desc>
  </programme>
  <programme start="20240115190000 +0000" stop="20240115200000 +0000" channel="cnn.us">
    <title>World Report</title>
  </programme>
</tv>`

func TestLiveTvProgramsFromGuide(t *testing.T) {
	m3u := filepath.Join(t.TempDir(), "channels.m3u")
	if err := os.WriteFile(m3u, []byte(testPlaylist), 0o644); err != nil {
		t.Fatal(err)
	}
	epg := filepath.Join(t.TempDir(), "guide.xml")
	if err := os.WriteFile(epg, []byte(guideXML), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	authSvc.SeedAdmin("admin", "epg-pass-1")
	srv := New(&config.Config{ServerName: "Test"}, st, authSvc, media.NewService(st, log), userdata.New(st), log)
	ltv, _ := livetv.NewService(m3u, time.Hour, log)
	ltv.SetGuide(epg)
	ltv.Start(context.Background())
	t.Cleanup(ltv.Close)
	srv.SetLiveTV(ltv)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	token, _ := login(t, ts.URL, "admin", "epg-pass-1")

	// Guide window reflects the loaded EPG.
	var gi jellyfin.GuideInfo
	authReq(t, http.MethodGet, ts.URL+"/LiveTv/GuideInfo", token, &gi)
	if !strings.HasPrefix(gi.StartDate, "2024-01-15") {
		t.Fatalf("guide start not from EPG: %q", gi.StartDate)
	}

	// Programs for the window return both entries, linked to their channels.
	var res jellyfin.QueryResult[jellyfin.BaseItemDto]
	url := ts.URL + "/LiveTv/Programs?MinStartDate=2024-01-15T00:00:00Z&MaxStartDate=2024-01-16T00:00:00Z"
	authReq(t, http.MethodGet, url, token, &res)
	if res.TotalRecordCount != 2 {
		t.Fatalf("expected 2 programs, got %d", res.TotalRecordCount)
	}
	p := res.Items[0]
	if p.Type != "Program" || p.Name != "Evening News" || p.ChannelID == "" {
		t.Fatalf("unexpected program dto: %+v", p)
	}
	if p.StartDate == "" || p.EndDate == "" || p.Overview != "Headlines." {
		t.Fatalf("program missing fields: %+v", p)
	}
}

func TestLiveTvCurrentProgramOnChannel(t *testing.T) {
	// A guide whose program spans "now" should appear as the channel's
	// CurrentProgram (the "what's on" line on a tile).
	const xmltvLayout = "20060102150405 -0700"
	now := time.Now().UTC()
	guide := `<tv><programme start="` + now.Add(-30*time.Minute).Format(xmltvLayout) +
		`" stop="` + now.Add(30*time.Minute).Format(xmltvLayout) +
		`" channel="bbc1.uk"><title>Live Now Show</title><desc>On air.</desc></programme></tv>`

	m3u := filepath.Join(t.TempDir(), "channels.m3u")
	os.WriteFile(m3u, []byte(testPlaylist), 0o644)
	epg := filepath.Join(t.TempDir(), "guide.xml")
	os.WriteFile(epg, []byte(guide), 0o644)

	st, _ := store.OpenJSON(t.TempDir())
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	authSvc.SeedAdmin("admin", "now-pass-1")
	srv := New(&config.Config{ServerName: "Test"}, st, authSvc, media.NewService(st, log), userdata.New(st), log)
	ltv, _ := livetv.NewService(m3u, time.Hour, log)
	ltv.SetGuide(epg)
	ltv.Start(context.Background())
	t.Cleanup(ltv.Close)
	srv.SetLiveTV(ltv)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	token, _ := login(t, ts.URL, "admin", "now-pass-1")

	var res jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/LiveTv/Channels", token, &res)

	var bbc *jellyfin.BaseItemDto
	for i := range res.Items {
		if res.Items[i].Name == "BBC One" {
			bbc = &res.Items[i]
		}
	}
	if bbc == nil {
		t.Fatal("BBC One channel missing")
	}
	if bbc.CurrentProgram == nil {
		t.Fatalf("channel should carry a current program: %+v", bbc)
	}
	if bbc.CurrentProgram.Name != "Live Now Show" || bbc.CurrentProgram.Type != "Program" {
		t.Fatalf("unexpected current program: %+v", bbc.CurrentProgram)
	}
}

func TestLiveTvRequiresAuth(t *testing.T) {
	ts, _ := newLiveTVServer(t)
	resp, err := http.Get(ts.URL + "/LiveTv/Channels")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated Live TV = %d, want 401", resp.StatusCode)
	}
}
