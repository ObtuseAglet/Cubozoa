package dvr

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/store"
)

// fakeChannels resolves a single channel ID to a URL.
type fakeChannels map[string]string

func (f fakeChannels) StreamURL(id string) (string, bool) { u, ok := f[id]; return u, ok }

// tsUpstream renders a short MPEG-TS clip and serves it, returning the URL.
func tsUpstream(t *testing.T) string {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	clip := filepath.Join(t.TempDir(), "up.ts")
	cmd := exec.Command(ffmpeg, "-y", "-nostdin",
		"-f", "lavfi", "-i", "testsrc=duration=3:size=128x96:rate=10",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-f", "mpegts", clip)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render upstream: %v\n%s", err, out)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, clip)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/live.ts"
}

func newRecorder(t *testing.T, channels ChannelSource) (*Recorder, store.Store) {
	t.Helper()
	st, err := store.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rec, ok := New(st, "", t.TempDir(), channels, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !ok {
		t.Skip("ffmpeg not available")
	}
	return rec, st
}

func TestRecordCompletes(t *testing.T) {
	url := tsUpstream(t)
	rec, _ := newRecorder(t, fakeChannels{"ch1": url})
	defer rec.Close()

	// A 2-second recording starting now.
	now := time.Now()
	r, err := rec.Schedule("ch1", "Chan One", "", "My Recording", now, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != store.RecRecording && r.Status != store.RecScheduled {
		t.Fatalf("unexpected initial status %q", r.Status)
	}

	// Wait for it to finish.
	deadline := time.Now().Add(15 * time.Second)
	for {
		got, ok := rec.Recording(r.ID)
		if ok && got.Status == store.RecCompleted {
			// The file exists and is servable.
			path, ok := rec.StreamPath(r.ID)
			if !ok {
				t.Fatal("completed recording should have a stream path")
			}
			if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
				t.Fatalf("recording file missing/empty: %v", err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recording did not complete; status=%v", func() string {
				if got != nil {
					return got.Status
				}
				return "?"
			}())
		}
		time.Sleep(200 * time.Millisecond)
	}

	// It now appears under Recordings, not Timers.
	if len(rec.Recordings()) != 1 {
		t.Fatalf("expected 1 recording, got %d", len(rec.Recordings()))
	}
	if len(rec.Timers()) != 0 {
		t.Fatalf("expected 0 active timers, got %d", len(rec.Timers()))
	}
}

func TestScheduleFutureIsTimer(t *testing.T) {
	rec, _ := newRecorder(t, fakeChannels{"ch1": "http://unused"})
	defer rec.Close()

	future := time.Now().Add(time.Hour)
	r, err := rec.Schedule("ch1", "Chan", "", "Later", future, future.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != store.RecScheduled {
		t.Fatalf("future recording should be scheduled, got %q", r.Status)
	}
	if len(rec.Timers()) != 1 {
		t.Fatalf("expected 1 timer, got %d", len(rec.Timers()))
	}

	// Cancelling removes it from active timers.
	if err := rec.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := rec.Recording(r.ID)
	if got.Status != store.RecCancelled {
		t.Fatalf("cancelled timer status = %q", got.Status)
	}
}

func TestScheduleRejectsBadWindow(t *testing.T) {
	rec, _ := newRecorder(t, fakeChannels{})
	defer rec.Close()
	now := time.Now()
	if _, err := rec.Schedule("ch1", "c", "", "n", now, now.Add(-time.Minute)); err == nil {
		t.Fatal("end before start should be rejected")
	}
}

func TestStreamPathRejectsEscape(t *testing.T) {
	rec, st := newRecorder(t, fakeChannels{})
	defer rec.Close()
	// A completed recording whose path escapes the recordings dir must not serve.
	bad := &store.Recording{ID: "x", Status: store.RecCompleted, Path: "/etc/passwd"}
	st.CreateRecording(bad)
	if _, ok := rec.StreamPath("x"); ok {
		t.Fatal("path outside recordings dir must be rejected")
	}
}
