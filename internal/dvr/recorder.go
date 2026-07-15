// Package dvr records Live TV channels to disk on a schedule (timers) and makes
// the completed captures playable. It runs one ffmpeg per active recording,
// persists timers/recordings in the store, and resumes in-progress recordings
// after a restart.
package dvr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/security"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// ErrUnavailable is returned when DVR is not configured (no ffmpeg).
var ErrUnavailable = errors.New("dvr: unavailable")

// ChannelSource resolves a channel ID to its upstream stream URL. The Live TV
// service satisfies this.
type ChannelSource interface {
	StreamURL(channelID string) (string, bool)
}

// Recorder schedules and runs channel recordings.
type Recorder struct {
	store    store.Store
	ffmpeg   string
	dir      string
	channels ChannelSource
	log      *slog.Logger

	mu     sync.Mutex
	active map[string]context.CancelFunc // recordingID -> cancel
	stop   chan struct{}
}

// New constructs a Recorder. It returns ok=false when ffmpeg is unavailable, so
// DVR stays disabled. ffmpegPath empty means "look up ffmpeg on PATH".
func New(st store.Store, ffmpegPath, dir string, channels ChannelSource, log *slog.Logger) (*Recorder, bool) {
	bin := ffmpegPath
	if bin == "" {
		if p, err := exec.LookPath("ffmpeg"); err == nil {
			bin = p
		}
	} else if _, err := exec.LookPath(bin); err != nil {
		bin = ""
	}
	if bin == "" {
		return nil, false
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Warn("dvr: cannot create recordings dir; DVR disabled", "dir", dir, "err", err)
		return nil, false
	}
	return &Recorder{
		store:    st,
		ffmpeg:   bin,
		dir:      dir,
		channels: channels,
		log:      log,
		active:   map[string]context.CancelFunc{},
		stop:     make(chan struct{}),
	}, true
}

// Start resumes any in-progress recordings and begins the scheduler loop.
func (r *Recorder) Start() {
	r.resume()
	go r.loop()
}

// Close stops the scheduler and cancels active recordings (they are marked
// completed if a file was produced).
func (r *Recorder) Close() {
	close(r.stop)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cancel := range r.active {
		cancel()
	}
}

// Schedule creates a timer to record a channel between start and end.
func (r *Recorder) Schedule(channelID, channelName, programID, name string, start, end time.Time) (*store.Recording, error) {
	if end.Before(time.Now()) || !end.After(start) {
		return nil, fmt.Errorf("dvr: invalid recording window")
	}
	id, err := security.NewID()
	if err != nil {
		return nil, err
	}
	rec := &store.Recording{
		ID:          id,
		ChannelID:   channelID,
		ChannelName: channelName,
		ProgramID:   programID,
		Name:        name,
		StartAt:     start.UTC(),
		EndAt:       end.UTC(),
		Status:      store.RecScheduled,
		CreatedAt:   time.Now().UTC(),
	}
	if err := r.store.CreateRecording(rec); err != nil {
		return nil, err
	}
	r.log.Info("recording scheduled", "id", id, "channel", channelName, "start", start, "end", end)
	// If it should already be running, start it immediately.
	r.maybeStart(rec)
	return rec, nil
}

// Cancel stops an active recording or removes a scheduled one.
func (r *Recorder) Cancel(id string) error {
	rec, err := r.store.GetRecording(id)
	if err != nil {
		return err
	}
	r.mu.Lock()
	cancel, running := r.active[id]
	r.mu.Unlock()

	if running {
		cancel() // stops ffmpeg; the watcher finalizes the record
		return nil
	}
	if rec.Status == store.RecScheduled {
		rec.Status = store.RecCancelled
		return r.store.UpdateRecording(rec)
	}
	return nil
}

// Timers returns scheduled and in-progress recordings.
func (r *Recorder) Timers() []*store.Recording {
	return r.filter(func(rec *store.Recording) bool {
		return rec.Status == store.RecScheduled || rec.Status == store.RecRecording
	})
}

// Recordings returns completed recordings.
func (r *Recorder) Recordings() []*store.Recording {
	return r.filter(func(rec *store.Recording) bool { return rec.Status == store.RecCompleted })
}

// Recording returns a single recording by ID.
func (r *Recorder) Recording(id string) (*store.Recording, bool) {
	rec, err := r.store.GetRecording(id)
	if err != nil {
		return nil, false
	}
	return rec, true
}

// StreamPath returns the on-disk file for a completed recording, verified to be
// inside the recordings directory.
func (r *Recorder) StreamPath(id string) (string, bool) {
	rec, err := r.store.GetRecording(id)
	if err != nil || rec.Status != store.RecCompleted || rec.Path == "" {
		return "", false
	}
	if rel, err := filepath.Rel(r.dir, rec.Path); err != nil ||
		rel == ".." || filepath.IsAbs(rel) || hasParentEscape(rel) {
		return "", false
	}
	return rec.Path, true
}

func (r *Recorder) filter(keep func(*store.Recording) bool) []*store.Recording {
	all, err := r.store.ListRecordings()
	if err != nil {
		return nil
	}
	out := make([]*store.Recording, 0, len(all))
	for _, rec := range all {
		if keep(rec) {
			out = append(out, rec)
		}
	}
	return out
}

// loop periodically starts due recordings and expires missed ones.
func (r *Recorder) loop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			r.tick()
		}
	}
}

func (r *Recorder) tick() {
	now := time.Now()
	for _, rec := range r.filter(func(rec *store.Recording) bool { return rec.Status == store.RecScheduled }) {
		switch {
		case !rec.EndAt.After(now):
			rec.Status = store.RecFailed // missed entirely
			_ = r.store.UpdateRecording(rec)
		case !rec.StartAt.After(now):
			r.maybeStart(rec)
		}
	}
}

// resume re-establishes state after a restart.
func (r *Recorder) resume() {
	now := time.Now()
	for _, rec := range r.filter(func(rec *store.Recording) bool { return rec.Status == store.RecRecording }) {
		if rec.EndAt.After(now) {
			r.maybeStart(rec) // continue for the remaining window
		} else {
			r.finalize(rec.ID)
		}
	}
}

// maybeStart begins a recording if it is due and not already running.
func (r *Recorder) maybeStart(rec *store.Recording) {
	now := time.Now()
	if rec.StartAt.After(now) || !rec.EndAt.After(now) {
		return
	}
	r.mu.Lock()
	if _, running := r.active[rec.ID]; running {
		r.mu.Unlock()
		return
	}
	upstream, ok := r.channels.StreamURL(rec.ChannelID)
	if !ok {
		r.mu.Unlock()
		rec.Status = store.RecFailed
		_ = r.store.UpdateRecording(rec)
		return
	}

	path := filepath.Join(r.dir, rec.ID+".ts")
	duration := time.Until(rec.EndAt)
	ctx, cancel := context.WithCancel(context.Background())
	r.active[rec.ID] = cancel
	r.mu.Unlock()

	rec.Path = path
	rec.Status = store.RecRecording
	_ = r.store.UpdateRecording(rec)

	cmd := exec.CommandContext(ctx, r.ffmpeg, captureArgs(upstream, path, duration)...)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		cancel()
		r.mu.Lock()
		delete(r.active, rec.ID)
		r.mu.Unlock()
		rec.Status = store.RecFailed
		_ = r.store.UpdateRecording(rec)
		return
	}
	r.log.Info("recording started", "id", rec.ID, "channel", rec.ChannelName, "duration", duration)
	go func() {
		_ = cmd.Wait()
		cancel()
		r.finalize(rec.ID)
	}()
}

// finalize marks a recording completed (if a non-empty file exists) or failed.
func (r *Recorder) finalize(id string) {
	r.mu.Lock()
	delete(r.active, id)
	r.mu.Unlock()

	rec, err := r.store.GetRecording(id)
	if err != nil {
		return
	}
	if rec.Status == store.RecCancelled {
		return
	}
	if fi, err := os.Stat(rec.Path); err == nil && fi.Size() > 0 {
		rec.Status = store.RecCompleted
	} else {
		rec.Status = store.RecFailed
	}
	_ = r.store.UpdateRecording(rec)
	r.log.Info("recording finalized", "id", id, "status", rec.Status)
}

// captureArgs records an upstream stream to a file for a duration, copying
// codecs (cheap) into an MPEG-TS container.
func captureArgs(input, path string, duration time.Duration) []string {
	secs := int(duration.Seconds())
	if secs < 1 {
		secs = 1
	}
	return []string{
		"-nostdin", "-y",
		"-i", input,
		"-t", fmt.Sprintf("%d", secs),
		"-c", "copy",
		"-f", "mpegts",
		path,
	}
}

// hasParentEscape reports whether a relative path steps out of its root.
func hasParentEscape(rel string) bool {
	return rel == ".." ||
		len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}
