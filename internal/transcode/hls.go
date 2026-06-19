package transcode

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Manager runs and supervises on-demand HLS transcode sessions backed by
// ffmpeg. Each session transcodes one file into a private temp directory whose
// segments are served back to the client. Sessions are reaped when idle and
// killed on shutdown, so a wedged client cannot leak a process or disk forever.
type Manager struct {
	bin     string
	baseDir string
	log     *slog.Logger

	mu       sync.Mutex
	sessions map[string]*Session

	maxSessions int
	idleTimeout time.Duration

	stop chan struct{}
}

// Session is a single running (or completed) transcode.
type Session struct {
	ID  string
	Dir string

	cancel     context.CancelFunc
	lastAccess time.Time
}

// segmentName is the strict pattern for a servable segment file. Anything else
// is rejected, so a client can never request an arbitrary path.
var segmentName = regexp.MustCompile(`^seg\d{1,6}\.ts$`)

const (
	playlistName = "index.m3u8"
	segmentGlob  = "seg%05d.ts"
)

// NewManager returns a transcode Manager, or ok=false if ffmpeg is unavailable.
// baseDir is created if missing and used to hold per-session segment dirs.
func NewManager(path, baseDir string, log *slog.Logger) (*Manager, bool) {
	bin := resolveBinary(path, "ffmpeg")
	if bin == "" {
		return nil, false
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		log.Warn("transcode: cannot create work dir; transcoding disabled", "dir", baseDir, "err", err)
		return nil, false
	}

	m := &Manager{
		bin:         bin,
		baseDir:     baseDir,
		log:         log,
		sessions:    make(map[string]*Session),
		maxSessions: 4,
		idleTimeout: 2 * time.Minute,
		stop:        make(chan struct{}),
	}
	// Clear any stale segment dirs from a previous run.
	_ = cleanDir(baseDir)
	go m.reaper()
	return m, true
}

// ErrUnavailable is returned by operations when transcoding is not configured.
var ErrUnavailable = errors.New("transcode: ffmpeg unavailable")

// EnsureSession returns the session for id, starting an ffmpeg transcode of
// inputPath if one is not already running. The session id is supplied by the
// caller (derived from the item) so repeated playlist requests reuse one
// transcode rather than spawning duplicates.
func (m *Manager) EnsureSession(id, inputPath string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sess, ok := m.sessions[id]; ok {
		sess.lastAccess = time.Now()
		return sess, nil
	}
	if len(m.sessions) >= m.maxSessions {
		m.evictOldestLocked()
	}

	dir := filepath.Join(m.baseDir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("transcode: session dir: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, m.bin, ffmpegArgs(inputPath, dir)...)
	// Detach from our stdio; ffmpeg is noisy on stderr.
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("transcode: starting ffmpeg: %w", err)
	}

	sess := &Session{ID: id, Dir: dir, cancel: cancel, lastAccess: time.Now()}
	m.sessions[id] = sess
	m.log.Info("transcode session started", "id", id)

	// Reap process state when ffmpeg exits so it does not linger as a zombie.
	go func() { _ = cmd.Wait() }()
	return sess, nil
}

// ffmpegArgs builds the ffmpeg command line for an on-demand HLS transcode to
// H.264/AAC in MPEG-TS segments. A VOD playlist with unlimited list size is
// written incrementally as segments complete.
func ffmpegArgs(input, dir string) []string {
	return []string{
		"-nostdin",
		"-i", input,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-c:a", "aac", "-ac", "2", "-b:a", "128k",
		"-f", "hls",
		"-hls_time", "6",
		"-hls_playlist_type", "vod",
		"-hls_list_size", "0",
		"-hls_segment_type", "mpegts",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_segment_filename", filepath.Join(dir, segmentGlob),
		filepath.Join(dir, playlistName),
	}
}

// Playlist returns the raw HLS media playlist for a session, waiting briefly for
// ffmpeg to produce it on first request.
func (m *Manager) Playlist(id string) ([]byte, error) {
	sess, ok := m.get(id)
	if !ok {
		return nil, ErrUnavailable
	}
	path := filepath.Join(sess.Dir, playlistName)
	deadline := time.Now().Add(15 * time.Second)
	for {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("transcode: playlist not ready")
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// Segment resolves and validates a segment path within a session, waiting
// briefly if a client races slightly ahead of the encoder.
func (m *Manager) Segment(id, name string) (string, error) {
	if !segmentName.MatchString(name) {
		return "", fmt.Errorf("transcode: invalid segment name")
	}
	sess, ok := m.get(id)
	if !ok {
		return "", ErrUnavailable
	}
	path := filepath.Join(sess.Dir, name)
	deadline := time.Now().Add(20 * time.Second)
	for {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("transcode: segment not ready")
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func (m *Manager) get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[id]
	if ok {
		sess.lastAccess = time.Now()
	}
	return sess, ok
}

func (m *Manager) evictOldestLocked() {
	var oldestID string
	var oldest time.Time
	for id, s := range m.sessions {
		if oldestID == "" || s.lastAccess.Before(oldest) {
			oldestID, oldest = id, s.lastAccess
		}
	}
	if oldestID != "" {
		m.killLocked(oldestID)
	}
}

// killLocked stops a session's ffmpeg and removes its directory. Caller holds m.mu.
func (m *Manager) killLocked(id string) {
	sess, ok := m.sessions[id]
	if !ok {
		return
	}
	sess.cancel()
	_ = os.RemoveAll(sess.Dir)
	delete(m.sessions, id)
	m.log.Info("transcode session stopped", "id", id)
}

func (m *Manager) reaper() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.mu.Lock()
			cutoff := time.Now().Add(-m.idleTimeout)
			for id, s := range m.sessions {
				if s.lastAccess.Before(cutoff) {
					m.killLocked(id)
				}
			}
			m.mu.Unlock()
		}
	}
}

// Close stops the reaper and tears down all sessions.
func (m *Manager) Close() {
	close(m.stop)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.sessions {
		m.killLocked(id)
	}
	_ = cleanDir(m.baseDir)
}

// cleanDir removes the contents of dir without removing dir itself.
func cleanDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
	return nil
}
