package livetv

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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

	guide   string // optional XMLTV source (URL or path)
	logoDir string // cache dir for downloaded channel logos ("" disables)

	aliases map[string]string // normalized channel name -> guide tvg-id (operator override)

	mu            sync.RWMutex
	channels      []Channel
	byID          map[string]Channel
	programsByTvg map[string][]Program
	nameIndex     map[string]string // normalized display name -> guide channel id
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

// SetAliases installs operator overrides mapping a channel name to a guide
// tvg-id. Keys are normalized so quality-tag variants ("BBC One HD") match.
func (s *Service) SetAliases(aliases map[string]string) {
	idx := make(map[string]string, len(aliases))
	for name, tvg := range aliases {
		if k := normalizeChannelName(name); k != "" && tvg != "" {
			idx[k] = tvg
		}
	}
	s.mu.Lock()
	s.aliases = idx
	s.mu.Unlock()
}

// SetLogoCache enables downloading and caching channel logos under dir.
func (s *Service) SetLogoCache(dir string) {
	s.logoDir = dir
	if dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
}

// LogoPath returns a local, cached copy of a channel's logo, downloading it on
// first use. It returns ok=false when the channel has no logo, caching is
// disabled, or the download fails.
func (s *Service) LogoPath(ctx context.Context, channelID string) (path, contentType string, ok bool) {
	c, found := s.Channel(channelID)
	if !found || c.Logo == "" || s.logoDir == "" {
		return "", "", false
	}
	ext := logoExt(c.Logo)
	dest := filepath.Join(s.logoDir, channelID+ext)
	if fi, err := os.Stat(dest); err == nil && fi.Size() > 0 {
		return dest, logoContentType(ext), true
	}

	data, err := s.fetch(ctx, c.Logo)
	if err != nil || len(data) == 0 {
		return "", "", false
	}
	if len(data) > 8<<20 { // 8 MiB cap for a logo
		return "", "", false
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", "", false
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", "", false
	}
	return dest, logoContentType(ext), true
}

func logoExt(u string) string {
	u = strings.SplitN(u, "?", 2)[0]
	switch {
	case strings.HasSuffix(strings.ToLower(u), ".png"):
		return ".png"
	case strings.HasSuffix(strings.ToLower(u), ".webp"):
		return ".webp"
	case strings.HasSuffix(strings.ToLower(u), ".gif"):
		return ".gif"
	default:
		return ".jpg"
	}
}

func logoContentType(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "image/jpeg"
	}
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
		playlist:      playlist,
		refresh:       refresh,
		log:           log,
		client:        &http.Client{Timeout: 30 * time.Second},
		byID:          map[string]Channel{},
		programsByTvg: map[string][]Program{},
		nameIndex:     map[string]string{},
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

	// Determine EPG sources: explicit configuration wins; otherwise fall back to
	// a url-tvg advertised in the playlist header. Multiple comma-separated URLs
	// are merged. A guide failure is non-fatal — channels still work.
	sources := splitSources(s.guide)
	if len(sources) == 0 {
		if headerURL := M3UGuideURL(data); headerURL != "" {
			sources = splitSources(headerURL)
			s.log.Info("using EPG advertised by playlist", "url", headerURL)
		}
	}
	guide := s.loadGuide(ctx, sources)

	s.mu.Lock()
	s.channels = channels
	s.byID = byID
	s.programsByTvg = guide.byTvg
	s.nameIndex = guide.nameIndex
	s.guideStart, s.guideEnd = guide.start, guide.end
	s.loadedAt = time.Now()
	s.mu.Unlock()

	s.log.Info("loaded IPTV channels", "count", len(channels), "source", s.playlist)
	return nil
}

// guideData is the parsed result of one or more EPG sources.
type guideData struct {
	byTvg     map[string][]Program
	nameIndex map[string]string // normalized display name -> guide channel id
	start     time.Time
	end       time.Time
}

// loadGuide fetches and parses each EPG source, merging all programs and
// building a name index for channels the playlist can't match by tvg-id.
func (s *Service) loadGuide(ctx context.Context, sources []string) guideData {
	var all []Program
	var chans []guideChannel
	for _, src := range sources {
		data, err := s.fetch(ctx, src)
		if err != nil {
			s.log.Warn("loading EPG failed", "src", src, "err", err)
			continue
		}
		progs, channels, err := parseGuide(data)
		if err != nil {
			s.log.Warn("parsing EPG failed", "src", src, "err", err)
			continue
		}
		all = append(all, progs...)
		chans = append(chans, channels...)
	}
	if len(all) == 0 {
		return guideData{byTvg: map[string][]Program{}, nameIndex: map[string]string{}}
	}
	byTvg, start, end := groupPrograms(all)
	s.log.Info("loaded EPG", "programs", len(all), "channels", len(chans), "sources", len(sources))
	return guideData{byTvg: byTvg, nameIndex: buildNameIndex(chans), start: start, end: end}
}

// splitSources splits a comma-separated list of non-empty, trimmed sources.
func splitSources(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
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
		tvg := s.resolveTvgIDLocked(ch)
		if tvg == "" {
			continue
		}
		for _, p := range s.programsByTvg[tvg] {
			if p.Stop.After(from) && p.Start.Before(to) {
				out = append(out, GuideEntry{ChannelID: ch.ID, Program: p})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// resolveTvgIDLocked returns the guide channel id that supplies programs for a
// playlist channel: its tvg-id when the guide has it, else a name match. Callers
// must hold at least the read lock.
func (s *Service) resolveTvgIDLocked(ch Channel) string {
	norm := normalizeChannelName(ch.Name)
	// An operator alias is an explicit override and wins over everything.
	if id, ok := s.aliases[norm]; ok {
		return id
	}
	if ch.TvgID != "" {
		if _, ok := s.programsByTvg[ch.TvgID]; ok {
			return ch.TvgID
		}
	}
	if id, ok := s.nameIndex[norm]; ok {
		return id
	}
	return ch.TvgID
}

// CurrentProgram returns the program airing on a channel at time `at`, if the
// EPG has one.
func (s *Service) CurrentProgram(channelID string, at time.Time) (GuideEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ch, ok := s.byID[channelID]
	if !ok {
		return GuideEntry{}, false
	}
	tvg := s.resolveTvgIDLocked(ch)
	if tvg == "" {
		return GuideEntry{}, false
	}
	for _, p := range s.programsByTvg[tvg] {
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
		data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64 MiB cap
		if err != nil {
			return nil, err
		}
		return maybeGunzip(data), nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	return maybeGunzip(data), nil
}

// maybeGunzip transparently decompresses gzip data (many public playlists and
// XMLTV guides are served as .gz), returning the input unchanged otherwise.
func maybeGunzip(data []byte) []byte {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return data
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, 256<<20)) // 256 MiB decompressed cap
	if err != nil || len(out) == 0 {
		return data
	}
	return out
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
