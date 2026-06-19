package server

import (
	"net/http"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
)

// effectiveUserID resolves which user a "/Users/{userId}/..." request may act
// on. A caller may always act on themselves; only an administrator may act on
// another user. On denial it writes 403 and returns ok=false.
func (s *Server) effectiveUserID(w http.ResponseWriter, r *http.Request, pathUserID string) (string, bool) {
	caller := userFrom(r) // guaranteed non-nil behind requireAuth
	if pathUserID == "" || pathUserID == caller.ID {
		return caller.ID, true
	}
	if caller.IsAdmin {
		return pathUserID, true
	}
	s.writeError(w, http.StatusForbidden)
	return "", false
}

// POST /Users/{userId}/PlayedItems/{itemId} — mark an item watched.
func (s *Server) handleMarkPlayed(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.effectiveUserID(w, r, r.PathValue("userId"))
	if !ok {
		return
	}
	itemID := r.PathValue("itemId")
	ud, err := s.userData.MarkPlayed(uid, itemID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, http.StatusOK, userDataDto(itemID, ud))
}

// DELETE /Users/{userId}/PlayedItems/{itemId} — mark an item unwatched.
func (s *Server) handleMarkUnplayed(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.effectiveUserID(w, r, r.PathValue("userId"))
	if !ok {
		return
	}
	itemID := r.PathValue("itemId")
	ud, err := s.userData.MarkUnplayed(uid, itemID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, http.StatusOK, userDataDto(itemID, ud))
}

// POST /Users/{userId}/FavoriteItems/{itemId} — add to favorites.
func (s *Server) handleAddFavorite(w http.ResponseWriter, r *http.Request) {
	s.setFavorite(w, r, true)
}

// DELETE /Users/{userId}/FavoriteItems/{itemId} — remove from favorites.
func (s *Server) handleRemoveFavorite(w http.ResponseWriter, r *http.Request) {
	s.setFavorite(w, r, false)
}

func (s *Server) setFavorite(w http.ResponseWriter, r *http.Request, fav bool) {
	uid, ok := s.effectiveUserID(w, r, r.PathValue("userId"))
	if !ok {
		return
	}
	itemID := r.PathValue("itemId")
	ud, err := s.userData.SetFavorite(uid, itemID, fav)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, http.StatusOK, userDataDto(itemID, ud))
}

// GET /Users/{userId}/Items/Resume — the user's "Continue Watching" row: items
// with a saved resume position, most recently played first.
func (s *Server) handleResumeItems(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.effectiveUserID(w, r, r.PathValue("userId"))
	if !ok {
		return
	}

	limit := parseInt(r.URL.Query().Get("Limit"), 0)
	ids := s.userData.Resumable(uid)
	udMap := s.userData.Map(uid)

	items := make([]jellyfin.BaseItemDto, 0, len(ids))
	for _, id := range ids {
		if limit > 0 && len(items) >= limit {
			break
		}
		it, err := s.media.Item(id)
		if err != nil {
			continue // item may have been removed since it was played
		}
		items = append(items, s.itemToDto(it, udMap[id]))
	}

	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.BaseItemDto]{
		Items:            items,
		TotalRecordCount: len(items),
		StartIndex:       0,
	})
}
