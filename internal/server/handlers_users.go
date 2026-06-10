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

// GET /Users/{id}/Views and GET /UserViews — the user's library views. No
// libraries exist yet (that is milestone 2), so this returns an empty result.
// Returning a well-formed empty QueryResult lets clients reach the home screen
// rather than erroring.
func (s *Server) handleUserViews(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, jellyfin.QueryResult[any]{
		Items:            []any{},
		TotalRecordCount: 0,
		StartIndex:       0,
	})
}
