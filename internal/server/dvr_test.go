package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/dvr"
	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/livetv"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/userdata"
)

func newDVRServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	// Upstream MPEG-TS clip served as the channel.
	clip := filepath.Join(t.TempDir(), "up.ts")
	render := exec.Command(ffmpeg, "-y", "-nostdin",
		"-f", "lavfi", "-i", "testsrc=duration=3:size=128x96:rate=10",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-f", "mpegts", clip)
	if out, err := render.CombinedOutput(); err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, clip)
	}))
	t.Cleanup(upstream.Close)

	m3u := filepath.Join(t.TempDir(), "ch.m3u")
	os.WriteFile(m3u, []byte("#EXTM3U\n#EXTINF:-1 tvg-id=\"c.tv\",Recordable\n"+upstream.URL+"/s.ts\n"), 0o644)

	st, _ := store.OpenJSON(t.TempDir())
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	authSvc := auth.New(st, log)
	authSvc.SeedAdmin("admin", "dvr-pass-1")
	srv := New(&config.Config{ServerName: "Test"}, st, authSvc, media.NewService(st, log), userdata.New(st), log)

	ltv, _ := livetv.NewService(m3u, time.Hour, log)
	ltv.Start(context.Background())
	t.Cleanup(ltv.Close)
	srv.SetLiveTV(ltv)

	rec, ok := dvr.New(st, "", t.TempDir(), ltv, log)
	if !ok {
		t.Skip("ffmpeg manager unavailable")
	}
	rec.Start()
	t.Cleanup(rec.Close)
	srv.SetRecorder(rec)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	token, _ := login(t, ts.URL, "admin", "dvr-pass-1")

	var chans jellyfin.QueryResult[jellyfin.BaseItemDto]
	authReq(t, http.MethodGet, ts.URL+"/LiveTv/Channels", token, &chans)
	return ts, token, chans.Items[0].ID
}

func TestDVRRecordThenPlay(t *testing.T) {
	ts, token, chID := newDVRServer(t)

	// Schedule a 2-second recording starting now.
	now := time.Now().UTC()
	body := `{"ChannelId":"` + chID + `","Name":"Test Rec","StartDate":"` +
		now.Format(time.RFC3339) + `","EndDate":"` + now.Add(2*time.Second).Format(time.RFC3339) + `"}`
	var timer jellyfin.TimerInfoDto
	if code := post(t, ts.URL+"/LiveTv/Timers", token, body, &timer); code != http.StatusOK {
		t.Fatalf("create timer status = %d", code)
	}
	if timer.Type != "Timer" || timer.ChannelID != chID {
		t.Fatalf("unexpected timer: %+v", timer)
	}

	// It shows up under Timers while active.
	var timers jellyfin.QueryResult[jellyfin.TimerInfoDto]
	authReq(t, http.MethodGet, ts.URL+"/LiveTv/Timers", token, &timers)
	if timers.TotalRecordCount != 1 {
		t.Fatalf("expected 1 timer, got %d", timers.TotalRecordCount)
	}

	// Wait for it to become a completed recording.
	var recs jellyfin.QueryResult[jellyfin.BaseItemDto]
	deadline := time.Now().Add(15 * time.Second)
	for {
		authReq(t, http.MethodGet, ts.URL+"/LiveTv/Recordings", token, &recs)
		if recs.TotalRecordCount == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recording never completed")
		}
		time.Sleep(300 * time.Millisecond)
	}
	recording := recs.Items[0]
	if recording.Type != "Recording" || recording.Name != "Test Rec" {
		t.Fatalf("unexpected recording: %+v", recording)
	}

	// PlaybackInfo advertises direct play of the recorded file.
	var info jellyfin.PlaybackInfoResponse
	post(t, ts.URL+"/Items/"+recording.ID+"/PlaybackInfo", token, "{}", &info)
	if !info.MediaSources[0].SupportsDirectPlay {
		t.Fatalf("recording should support direct play: %+v", info.MediaSources[0])
	}

	// And the recorded bytes stream (with Range support).
	resp, err := http.Get(ts.URL + "/Videos/" + recording.ID + "/stream?api_key=" + token)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recording stream status = %d", resp.StatusCode)
	}
	if resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatal("recording stream should support ranges")
	}
	b, _ := io.ReadAll(resp.Body)
	if len(b) == 0 {
		t.Fatal("empty recording")
	}
	_ = strings.TrimSpace
}

func TestSeriesTimerLifecycle(t *testing.T) {
	ts, token, chID := newDVRServer(t)

	// Create a series timer scoped to the channel.
	body := `{"ChannelId":"` + chID + `","Name":"My Series","RecordAnyChannel":false}`
	var st jellyfin.SeriesTimerInfoDto
	if code := post(t, ts.URL+"/LiveTv/SeriesTimers", token, body, &st); code != http.StatusOK {
		t.Fatalf("create series timer status = %d", code)
	}
	if st.Type != "SeriesTimer" || st.Name != "My Series" || st.ChannelID != chID {
		t.Fatalf("unexpected series timer: %+v", st)
	}

	// It is listed.
	var list jellyfin.QueryResult[jellyfin.SeriesTimerInfoDto]
	authReq(t, http.MethodGet, ts.URL+"/LiveTv/SeriesTimers", token, &list)
	if list.TotalRecordCount != 1 || list.Items[0].ID != st.ID {
		t.Fatalf("expected 1 series timer, got %+v", list)
	}

	// Delete it.
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/LiveTv/SeriesTimers/"+st.ID, nil)
	req.Header.Set("X-Emby-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete series timer status = %d", resp.StatusCode)
	}

	authReq(t, http.MethodGet, ts.URL+"/LiveTv/SeriesTimers", token, &list)
	if list.TotalRecordCount != 0 {
		t.Fatalf("series timer should be gone, got %d", list.TotalRecordCount)
	}
}

func TestCreateSeriesTimerRequiresName(t *testing.T) {
	ts, token, _ := newDVRServer(t)
	// RecordAnyChannel with no name → 400.
	body := `{"RecordAnyChannel":true,"Name":""}`
	var st jellyfin.SeriesTimerInfoDto
	if code := post(t, ts.URL+"/LiveTv/SeriesTimers", token, body, &st); code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty name, got %d", code)
	}
}
