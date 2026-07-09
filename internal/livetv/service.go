package livetv

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Service loads IPTV channels from an M3U source (URL or file) and keeps them
// refreshed, serving lookups for the Live TV API and stream proxy.
type Service struct {
	playlist string
	refresh  time.Duration
	log      *slog.Logger
	client   *http.Client

	mu       sync.RWMutex
	channels []Channel
	byID     map[string]Channel
	loadedAt time.Time

	stop chan struct{}
}

// NewService constructs a Live TV service for the given M3U source. It returns
// ok=false when no playlist is configured (Live TV stays disabled).
func NewService(playlist string, refresh time.Duration, log *slog.Logger) (*Service, bool) {
	if strings.TrimSpace(playlist) == "" {
		return nil, false
	}
	if refresh <= 0 {
		refresh = 12 * time.Hour
	}
	return &Service{
		playlist: playlist,
		refresh:  refresh,
		log:      log,
		client:   &http.Client{Timeout: 30 * time.Second},
		byID:     map[string]Channel{},
		stop:     make(chan struct{}),
	}, true
}

// Start performs an initial load and then refreshes periodically until Close.
func (s *Service) Start(ctx context.Context) {
	if err := s.reload(ctx); err != nil {
		s.log.Warn("initial IPTV playlist load failed", "err", err)
	}
	go s.loop()
}

func (s *Service) loop() {
	t := time.NewTicker(s.refresh)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			if err := s.reload(context.Background()); err != nil {
				s.log.Warn("IPTV playlist refresh failed", "err", err)
			}
		}
	}
}

// Close stops the refresh loop.
func (s *Service) Close() { close(s.stop) }

// reload fetches and parses the playlist, replacing the channel set atomically.
func (s *Service) reload(ctx context.Context) error {
	data, err := s.fetch(ctx, s.playlist)
	if err != nil {
		return err
	}
	channels := ParseM3U(data)

	byID := make(map[string]Channel, len(channels))
	for _, c := range channels {
		byID[c.ID] = c
	}

	s.mu.Lock()
	s.channels = channels
	s.byID = byID
	s.loadedAt = time.Now()
	s.mu.Unlock()

	s.log.Info("loaded IPTV channels", "count", len(channels), "source", s.playlist)
	return nil
}

// fetch reads a playlist/guide from an http(s) URL or a local file path.
func (s *Service) fetch(ctx context.Context, src string) ([]byte, error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("livetv: fetching %s: %w", src, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("livetv: fetching %s: status %d", src, resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64 MiB cap
	}
	return os.ReadFile(src)
}

// Channels returns all channels, ordered by numeric channel number then name.
func (s *Service) Channels() []Channel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Channel, len(s.channels))
	copy(out, s.channels)
	sort.SliceStable(out, func(i, j int) bool {
		ni, nj := numeric(out[i].Number), numeric(out[j].Number)
		if ni != nj {
			return ni < nj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Channel returns one channel by ID.
func (s *Service) Channel(id string) (Channel, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.byID[id]
	return c, ok
}

// StreamURL returns the upstream stream URL for a channel ID.
func (s *Service) StreamURL(id string) (string, bool) {
	c, ok := s.Channel(id)
	if !ok {
		return "", false
	}
	return c.URL, true
}

// Count returns the number of loaded channels.
func (s *Service) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.channels)
}

func numeric(s string) int {
	n := 0
	seen := false
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
		seen = true
	}
	if !seen {
		return 1 << 30 // sort unnumbered channels last
	}
	return n
}
