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

	guide string // optional XMLTV source (URL or path)

	mu            sync.RWMutex
	channels      []Channel
	byID          map[string]Channel
	programsByTvg map[string][]Program
	guideStart    time.Time
	guideEnd      time.Time
	loadedAt      time.Time

	stop chan struct{}
}

// GuideEntry is a program annotated with the Cubozoa channel ID it belongs to.
type GuideEntry struct {
	ChannelID string
	Program
}

// SetGuide configures an optional XMLTV EPG source, loaded alongside the
// playlist on the next refresh.
func (s *Service) SetGuide(src string) { s.guide = strings.TrimSpace(src) }

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
		playlist:      playlist,
		refresh:       refresh,
		log:           log,
		client:        &http.Client{Timeout: 30 * time.Second},
		byID:          map[string]Channel{},
		programsByTvg: map[string][]Program{},
		stop:          make(chan struct{}),
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

	// Load the optional EPG. A guide failure is non-fatal — channels still work.
	programs := map[string][]Program{}
	var gStart, gEnd time.Time
	if s.guide != "" {
		if data, err := s.fetch(ctx, s.guide); err != nil {
			s.log.Warn("loading EPG failed", "err", err)
		} else if progs, err := ParseXMLTV(data); err != nil {
			s.log.Warn("parsing EPG failed", "err", err)
		} else {
			programs, gStart, gEnd = groupPrograms(progs)
			s.log.Info("loaded EPG", "programs", len(progs))
		}
	}

	s.mu.Lock()
	s.channels = channels
	s.byID = byID
	s.programsByTvg = programs
	s.guideStart, s.guideEnd = gStart, gEnd
	s.loadedAt = time.Now()
	s.mu.Unlock()

	s.log.Info("loaded IPTV channels", "count", len(channels), "source", s.playlist)
	return nil
}

// groupPrograms buckets programs by channel (sorted by start) and returns the
// overall guide window.
func groupPrograms(progs []Program) (map[string][]Program, time.Time, time.Time) {
	byTvg := map[string][]Program{}
	var start, end time.Time
	for _, p := range progs {
		byTvg[p.ChannelTvgID] = append(byTvg[p.ChannelTvgID], p)
		if start.IsZero() || p.Start.Before(start) {
			start = p.Start
		}
		if end.IsZero() || p.Stop.After(end) {
			end = p.Stop
		}
	}
	for _, list := range byTvg {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Start.Before(list[j].Start) })
	}
	return byTvg, start, end
}

// Programs returns EPG entries for the given channel IDs (or all channels when
// empty) that overlap the [from, to] window, ordered by start time.
func (s *Service) Programs(channelIDs []string, from, to time.Time) []GuideEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var targets []Channel
	if len(channelIDs) == 0 {
		targets = s.channels
	} else {
		for _, id := range channelIDs {
			if c, ok := s.byID[id]; ok {
				targets = append(targets, c)
			}
		}
	}

	var out []GuideEntry
	for _, ch := range targets {
		if ch.TvgID == "" {
			continue
		}
		for _, p := range s.programsByTvg[ch.TvgID] {
			if p.Stop.After(from) && p.Start.Before(to) {
				out = append(out, GuideEntry{ChannelID: ch.ID, Program: p})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// CurrentProgram returns the program airing on a channel at time `at`, if the
// EPG has one.
func (s *Service) CurrentProgram(channelID string, at time.Time) (GuideEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ch, ok := s.byID[channelID]
	if !ok || ch.TvgID == "" {
		return GuideEntry{}, false
	}
	for _, p := range s.programsByTvg[ch.TvgID] {
		if !p.Start.After(at) && p.Stop.After(at) {
			return GuideEntry{ChannelID: ch.ID, Program: p}, true
		}
	}
	return GuideEntry{}, false
}

// GuideWindow returns the time span the loaded EPG covers (zero times if none).
func (s *Service) GuideWindow() (time.Time, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.guideStart, s.guideEnd
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
