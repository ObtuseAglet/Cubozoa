package server

import (
	"errors"
	"net/http"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// GET /Users/Me — the authenticated user's own record.
func (s *Server) handleUsersMe(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	s.writeJSON(w, http.StatusOK, s.toUserDto(u))
}

// GET /Users/{id} — fetch a user by id. Non-admins may only read themselves.
func (s *Server) handleUserByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	caller := userFrom(r)
	if !caller.IsAdmin && caller.ID != id {
		s.writeError(w, http.StatusForbidden)
		return
	}

	u, err := s.store.GetUserByID(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusNotFound)
			return
		}
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, http.StatusOK, s.toUserDto(u))
}

// GET /Users/Public — the public user list shown on the login screen. Only
// non-sensitive fields are exposed.
func (s *Server) handleUsersPublic(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	out := make([]jellyfin.UserDto, 0, len(users))
	for _, u := range users {
		out = append(out, jellyfin.UserDto{
			Name:        u.Name,
			ID:          u.ID,
			ServerID:    s.store.ServerID(),
			HasPassword: true,
		})
	}
	s.writeJSON(w, http.StatusOK, out)
}

// GET /Users — admin listing of all users.
func (s *Server) handleUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	out := make([]jellyfin.UserDto, 0, len(users))
	for _, u := range users {
		out = append(out, s.toUserDto(u))
	}
	s.writeJSON(w, http.StatusOK, out)
}

// GET /Users/{id}/Views and GET /UserViews — the user's library views, one
// CollectionFolder per registered library.
func (s *Server) handleUserViews(w http.ResponseWriter, r *http.Request) {
	libs, err := s.media.Libraries()
	if err != nil {
		s.log.Error("listing libraries", "err", err)
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	views := make([]jellyfin.BaseItemDto, 0, len(libs)+1)
	for _, lib := range libs {
		views = append(views, s.libraryToDto(lib))
	}
	// A synthetic Live TV view appears alongside the media libraries when IPTV
	// channels are configured; clients route it to their Live TV section.
	if s.liveTVEnabled() {
		views = append(views, jellyfin.BaseItemDto{
			Name:           "Live TV",
			ServerID:       s.store.ServerID(),
			ID:             "livetv",
			Type:           "CollectionFolder",
			CollectionType: "livetv",
			IsFolder:       true,
		})
	}
	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[jellyfin.BaseItemDto]{
		Items:            views,
		TotalRecordCount: len(views),
		StartIndex:       0,
	})
}
