package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
