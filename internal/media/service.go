package media

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/transcode"
)

// Service manages libraries and serves the queries that back the browse API.
// It owns scanning so handlers never touch the filesystem directly.
type Service struct {
	store  store.Store
	log    *slog.Logger
	prober *transcode.Prober // optional; nil disables ffprobe metadata

	scanMu sync.Mutex // serializes scans so two refreshes don't race
}

// NewService constructs a media Service.
func NewService(st store.Store, log *slog.Logger) *Service {
	return &Service{store: st, log: log}
}

// SetProber enables ffprobe metadata extraction during scans. A nil prober
// leaves scanning metadata-free (titles/years only).
func (s *Service) SetProber(p *transcode.Prober) {
	s.prober = p
}

// SyncLibrariesFromMediaDir registers a library for each immediate subdirectory
// of mediaDir that is not already known, inferring the collection type from the
// folder name. Existing libraries are left untouched, and nothing is deleted —
// removing a library is always an explicit action.
func (s *Service) SyncLibrariesFromMediaDir(mediaDir string) error {
	if mediaDir == "" {
		return nil
	}
	entries, err := os.ReadDir(mediaDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(mediaDir, e.Name())
		if _, err := s.store.GetLibraryByPath(path); err == nil {
			continue // already registered
		}
		if _, err := s.RegisterLibrary(e.Name(), inferLibraryType(e.Name()), path); err != nil {
			s.log.Warn("registering library", "path", path, "err", err)
		}
	}
	return nil
}

// RegisterLibrary creates a library with a stable, path-derived ID. Registering
// the same path twice is a no-op that returns the existing library.
func (s *Service) RegisterLibrary(name, libType, path string) (*store.Library, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if existing, err := s.store.GetLibraryByPath(abs); err == nil {
		return existing, nil
	}
	lib := &store.Library{
		ID:        libraryID(abs),
		Name:      name,
		Type:      libType,
		Path:      abs,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateLibrary(lib); err != nil && !errors.Is(err, store.ErrConflict) {
		return nil, err
	}
	s.log.Info("registered library", "name", name, "type", libType, "path", abs)
	return lib, nil
}

// ScanAll scans every registered library, publishing each result atomically.
func (s *Service) ScanAll() error {
	libs, err := s.store.ListLibraries()
	if err != nil {
		return err
	}
	for _, lib := range libs {
		if err := s.ScanLibrary(lib.ID); err != nil {
			s.log.Warn("scanning library", "library", lib.Name, "err", err)
		}
	}
	return nil
}

// ScanLibrary re-scans a single library and replaces its item set.
func (s *Service) ScanLibrary(id string) error {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	lib, err := s.store.GetLibrary(id)
	if err != nil {
		return err
	}
	start := time.Now()
	items, err := Scan(lib)
	if err != nil {
		return err
	}
	if s.prober != nil {
		s.probeItems(items)
	}
	if err := s.store.ReplaceLibraryItems(id, items); err != nil {
		return err
	}
	s.log.Info("scanned library",
		"name", lib.Name,
		"items", len(items),
		"probed", s.prober != nil,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// probeItems fills duration/codec/stream metadata for each item using ffprobe,
// with bounded concurrency so a large library scans in reasonable time without
// spawning an unbounded number of processes.
func (s *Service) probeItems(items []*store.MediaItem) {
	const workers = 4
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(it *store.MediaItem) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := s.prober.Probe(context.Background(), it.Path)
			if err != nil {
				s.log.Warn("probe failed", "path", it.Path, "err", err)
				return
			}
			applyProbe(it, res)
		}(it)
	}
	wg.Wait()
}

// applyProbe copies probe results onto a media item.
func applyProbe(it *store.MediaItem, res *transcode.ProbeResult) {
	it.RunTimeTicks = res.DurationTicks
	it.Width = res.Width
	it.Height = res.Height
	it.VideoCodec = res.VideoCodec
	it.AudioCodec = res.AudioCodec
	it.Bitrate = res.Bitrate
	it.Streams = make([]store.MediaStreamInfo, 0, len(res.Streams))
	for _, s := range res.Streams {
		it.Streams = append(it.Streams, store.MediaStreamInfo{
			Index:     s.Index,
			Type:      s.Type,
			Codec:     s.Codec,
			Language:  s.Language,
			Channels:  s.Channels,
			Width:     s.Width,
			Height:    s.Height,
			IsDefault: s.IsDefault,
			Title:     s.Title,
		})
	}
}

// Libraries returns all registered libraries.
func (s *Service) Libraries() ([]*store.Library, error) {
	libs, err := s.store.ListLibraries()
	if err != nil {
		return nil, err
	}
	sort.Slice(libs, func(i, j int) bool { return libs[i].Name < libs[j].Name })
	return libs, nil
}

// Item returns a single item by ID.
func (s *Service) Item(id string) (*store.MediaItem, error) {
	return s.store.GetItem(id)
}

// Seasons returns a series' seasons, ordered by season number.
func (s *Service) Seasons(seriesID string) ([]*store.MediaItem, error) {
	all, err := s.store.AllItems()
	if err != nil {
		return nil, err
	}
	var out []*store.MediaItem
	for _, it := range all {
		if it.Type == "Season" && it.SeriesID == seriesID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IndexNumber < out[j].IndexNumber })
	return out, nil
}

// Episodes returns a series' episodes, ordered by season then episode number.
// If seasonID is non-empty, only that season's episodes are returned.
func (s *Service) Episodes(seriesID, seasonID string) ([]*store.MediaItem, error) {
	all, err := s.store.AllItems()
	if err != nil {
		return nil, err
	}
	var out []*store.MediaItem
	for _, it := range all {
		if it.Type != "Episode" || it.SeriesID != seriesID {
			continue
		}
		if seasonID != "" && it.SeasonID != seasonID {
			continue
		}
		out = append(out, it)
	}
	sortEpisodes(out)
	return out, nil
}

// AllEpisodes returns every episode across all libraries, ordered within each
// series by season then episode. Used to compute "Next Up".
func (s *Service) AllEpisodes() ([]*store.MediaItem, error) {
	all, err := s.store.AllItems()
	if err != nil {
		return nil, err
	}
	var out []*store.MediaItem
	for _, it := range all {
		if it.Type == "Episode" {
			out = append(out, it)
		}
	}
	sortEpisodes(out)
	return out, nil
}

func sortEpisodes(eps []*store.MediaItem) {
	sort.Slice(eps, func(i, j int) bool {
		a, b := eps[i], eps[j]
		if a.SeriesID != b.SeriesID {
			return a.SeriesID < b.SeriesID
		}
		if a.ParentIndexNumber != b.ParentIndexNumber {
			return a.ParentIndexNumber < b.ParentIndexNumber
		}
		return a.IndexNumber < b.IndexNumber
	})
}

// ErrNoImage indicates the requested image does not exist for an item.
var ErrNoImage = errors.New("media: no such image")

// Image describes a resolvable artwork file on disk.
type Image struct {
	Path        string
	ContentType string
	Tag         string
}

// ItemImage resolves an item's artwork of the given type ("Primary" or
// "Backdrop") to a file on disk. As defense in depth it verifies the stored
// path still lives within the owning library's root, so even a tampered
// datastore cannot turn an image request into an arbitrary-file read.
func (s *Service) ItemImage(itemID, imageType string) (Image, error) {
	it, err := s.store.GetItem(itemID)
	if err != nil {
		return Image{}, err
	}

	var path, tag string
	switch strings.ToLower(imageType) {
	case "primary":
		path, tag = it.PrimaryImagePath, it.PrimaryImageTag
	case "backdrop":
		path, tag = it.BackdropImagePath, it.BackdropImageTag
	default:
		return Image{}, ErrNoImage
	}
	if path == "" {
		return Image{}, ErrNoImage
	}

	if lib, err := s.store.GetLibrary(it.LibraryID); err == nil && !withinRoot(lib.Path, path) {
		s.log.Warn("rejecting image outside library root", "item", itemID, "path", path)
		return Image{}, ErrNoImage
	}
	return Image{Path: path, ContentType: imageContentType(path), Tag: tag}, nil
}

// withinRoot reports whether p is the same as, or nested under, root. Both are
// expected to be absolute, cleaned paths.
func withinRoot(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ItemSubtitle resolves an external subtitle track by its ordinal, applying the
// same library-root containment check as media and image serving.
func (s *Service) ItemSubtitle(itemID string, index int) (store.SubtitleTrack, error) {
	it, err := s.store.GetItem(itemID)
	if err != nil {
		return store.SubtitleTrack{}, err
	}
	if index < 0 || index >= len(it.Subtitles) {
		return store.SubtitleTrack{}, store.ErrNotFound
	}
	tr := it.Subtitles[index]
	if lib, err := s.store.GetLibrary(it.LibraryID); err == nil && !withinRoot(lib.Path, tr.Path) {
		s.log.Warn("rejecting subtitle outside library root", "item", itemID, "path", tr.Path)
		return store.SubtitleTrack{}, store.ErrNotFound
	}
	return tr, nil
}

// Stream describes the on-disk source for direct play.
type Stream struct {
	Path        string
	Container   string
	ContentType string
	Size        int64
}

// ItemStream resolves an item to its on-disk media file for direct play,
// applying the same library-root containment check as image serving so a
// stream request can never read a file outside a known library.
func (s *Service) ItemStream(itemID string) (Stream, error) {
	it, err := s.store.GetItem(itemID)
	if err != nil {
		return Stream{}, err
	}
	if it.Path == "" {
		return Stream{}, store.ErrNotFound
	}
	if lib, err := s.store.GetLibrary(it.LibraryID); err == nil && !withinRoot(lib.Path, it.Path) {
		s.log.Warn("rejecting stream outside library root", "item", itemID, "path", it.Path)
		return Stream{}, store.ErrNotFound
	}
	return Stream{
		Path:        it.Path,
		Container:   it.Container,
		ContentType: StreamContentType(it.Container),
		Size:        it.SizeBytes,
	}, nil
}

// BrowseQuery describes a browse request. Zero values are sensible defaults.
type BrowseQuery struct {
	ParentID         string   // a library ID (or item folder ID)
	IncludeItemTypes []string // e.g. ["Movie"]; empty means all
	Recursive        bool     // ignore ParentID folder boundaries
	SearchTerm       string
	SortBy           string // SortName | Name | DateCreated | ProductionYear
	SortDescending   bool
	StartIndex       int
	Limit            int // 0 means no limit
}

// Browse returns the items matching a query plus the total count before paging.
func (s *Service) Browse(q BrowseQuery) ([]*store.MediaItem, int, error) {
	var (
		candidates []*store.MediaItem
		err        error
	)
	switch {
	case q.Recursive || q.ParentID == "":
		candidates, err = s.store.AllItems()
	default:
		// Direct children of the parent: movies under a library, series under a
		// tvshows library, seasons under a series, episodes under a season.
		candidates, err = s.store.ListItemsByParent(q.ParentID)
	}
	if err != nil {
		return nil, 0, err
	}

	typeFilter := make(map[string]bool, len(q.IncludeItemTypes))
	for _, t := range q.IncludeItemTypes {
		if t != "" {
			typeFilter[t] = true
		}
	}
	search := strings.ToLower(strings.TrimSpace(q.SearchTerm))

	filtered := candidates[:0]
	for _, it := range candidates {
		if len(typeFilter) > 0 && !typeFilter[it.Type] {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(it.Name), search) {
			continue
		}
		filtered = append(filtered, it)
	}

	sortItems(filtered, q.SortBy, q.SortDescending)
	total := len(filtered)

	// Apply paging defensively: clamp indexes so bad input cannot panic.
	if q.StartIndex < 0 {
		q.StartIndex = 0
	}
	if q.StartIndex > total {
		q.StartIndex = total
	}
	filtered = filtered[q.StartIndex:]
	if q.Limit > 0 && q.Limit < len(filtered) {
		filtered = filtered[:q.Limit]
	}
	return filtered, total, nil
}

func sortItems(items []*store.MediaItem, by string, desc bool) {
	less := func(i, j int) bool {
		a, b := items[i], items[j]
		switch by {
		case "Name":
			return a.Name < b.Name
		case "DateCreated":
			return a.DateCreated.Before(b.DateCreated)
		case "ProductionYear":
			if a.ProductionYear != b.ProductionYear {
				return a.ProductionYear < b.ProductionYear
			}
			return a.SortName < b.SortName
		default: // SortName
			return a.SortName < b.SortName
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if desc {
			return less(j, i)
		}
		return less(i, j)
	})
}
