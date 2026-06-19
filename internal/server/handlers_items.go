package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// browseQueryFromRequest translates Jellyfin's query parameters into a media
// browse query. Unknown or malformed values fall back to safe defaults rather
// than erroring, matching how clients expect the API to behave.
func browseQueryFromRequest(r *http.Request) media.BrowseQuery {
	q := r.URL.Query()
	return media.BrowseQuery{
		ParentID:         q.Get("ParentId"),
		IncludeItemTypes: splitCSV(q.Get("IncludeItemTypes")),
		Recursive:        parseBool(q.Get("Recursive")),
		SearchTerm:       q.Get("SearchTerm"),
		SortBy:           firstCSV(q.Get("SortBy")),
		SortDescending:   strings.EqualFold(q.Get("SortOrder"), "Descending"),
		StartIndex:       parseInt(q.Get("StartIndex"), 0),
		Limit:            parseInt(q.Get("Limit"), 0),
	}
}

// GET /Items and GET /Users/{userId}/Items — the primary browse endpoint.
func (s *Server) handleItems(w http.ResponseWriter, r *http.Request) {
	q := browseQueryFromRequest(r)
	items, total, err := s.media.Browse(q)
	if err != nil {
		s.log.Error("browsing items", "err", err)
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	udMap := s.userData.Map(userFrom(r).ID)
	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.BaseItemDto]{
		Items:            s.itemsToDtos(items, udMap),
		TotalRecordCount: total,
		StartIndex:       q.StartIndex,
	})
}

// GET /Items/{itemId} and GET /Users/{userId}/Items/{itemId} — item detail.
func (s *Server) handleItemDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("itemId")
	it, err := s.media.Item(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound)
			return
		}
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	ud := s.userData.Get(userFrom(r).ID, it.ID)
	s.writeJSON(w, http.StatusOK, s.itemToDto(it, ud))
}

// GET /Items/Latest and GET /Users/{userId}/Items/Latest — most recently added
// items. Note this endpoint returns a bare array, not a QueryResult envelope.
func (s *Server) handleItemsLatest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := parseInt(q.Get("Limit"), 20)
	items, _, err := s.media.Browse(media.BrowseQuery{
		ParentID:         q.Get("ParentId"),
		IncludeItemTypes: splitCSV(q.Get("IncludeItemTypes")),
		Recursive:        true,
		SortBy:           "DateCreated",
		SortDescending:   true,
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	// "Latest" shows playable media, not the synthetic Series/Season folders.
	playable := items[:0]
	for _, it := range items {
		if !isFolderType(it.Type) {
			playable = append(playable, it)
		}
		if len(playable) >= limit {
			break
		}
	}
	s.writeJSON(w, http.StatusOK, s.itemsToDtos(playable, s.userData.Map(userFrom(r).ID)))
}

// GET /Library/VirtualFolders — admin listing of configured libraries.
func (s *Server) handleVirtualFolders(w http.ResponseWriter, r *http.Request) {
	libs, err := s.media.Libraries()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	out := make([]jellyfin.VirtualFolderInfo, 0, len(libs))
	for _, lib := range libs {
		out = append(out, jellyfin.VirtualFolderInfo{
			Name:           lib.Name,
			ItemID:         lib.ID,
			CollectionType: lib.Type,
			Locations:      []string{lib.Path},
		})
	}
	s.writeJSON(w, http.StatusOK, out)
}

// POST /Library/Refresh — admin-triggered rescan of all libraries. Runs in the
// background so the request returns promptly.
func (s *Server) handleLibraryRefresh(w http.ResponseWriter, r *http.Request) {
	go func() {
		if err := s.media.ScanAll(); err != nil {
			s.log.Warn("library refresh", "err", err)
		}
	}()
	w.WriteHeader(http.StatusNoContent)
}

// --- small query-parameter helpers ---

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstCSV(s string) string {
	if i := strings.IndexByte(s, ','); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func parseBool(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}

func parseInt(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
