package media

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/store"
)

// Service manages libraries and serves the queries that back the browse API.
// It owns scanning so handlers never touch the filesystem directly.
type Service struct {
	store store.Store
	log   *slog.Logger

	scanMu sync.Mutex // serializes scans so two refreshes don't race
}

// NewService constructs a media Service.
func NewService(st store.Store, log *slog.Logger) *Service {
	return &Service{store: st, log: log}
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
	if err := s.store.ReplaceLibraryItems(id, items); err != nil {
		return err
	}
	s.log.Info("scanned library",
		"name", lib.Name,
		"items", len(items),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
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
		candidates, err = s.store.ListItemsByLibrary(q.ParentID)
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
