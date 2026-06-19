// Package server wires Cubozoa's HTTP surface together: routing, middleware and
// the Jellyfin-compatible handlers.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/config"
	"github.com/obtuseaglet/cubozoa/internal/media"
	"github.com/obtuseaglet/cubozoa/internal/store"
	"github.com/obtuseaglet/cubozoa/internal/transcode"
	"github.com/obtuseaglet/cubozoa/internal/userdata"
)

// Server holds the dependencies shared by all handlers.
type Server struct {
	cfg        *config.Config
	store      store.Store
	auth       *auth.Service
	media      *media.Service
	userData   *userdata.Service
	transcoder *transcode.Manager // optional; nil disables transcoding
	log        *slog.Logger

	limiter *rateLimiter
}

// SetTranscoder enables HLS transcoding. A nil manager leaves transcoding off
// (direct play still works), so this is safe to skip when ffmpeg is absent.
func (s *Server) SetTranscoder(m *transcode.Manager) {
	s.transcoder = m
}

// New constructs a Server.
func New(cfg *config.Config, st store.Store, authSvc *auth.Service, mediaSvc *media.Service, userDataSvc *userdata.Service, log *slog.Logger) *Server {
	return &Server{
		cfg:      cfg,
		store:    st,
		auth:     authSvc,
		media:    mediaSvc,
		userData: userDataSvc,
		log:      log,
		limiter:  newRateLimiter(),
	}
}

// Handler returns the fully assembled HTTP handler with middleware applied.
func (s *Server) Handler() http.Handler {
	mux := s.routes()

	// Middleware is applied outermost-first. Recovery is outermost so it can
	// catch panics from anything below it; logging wraps the request next so
	// every request — including denied ones — is recorded.
	var h http.Handler = mux
	h = s.withAuthContext(h)
	h = stripEmbyPrefix(h)
	h = securityHeaders(h)
	h = s.logRequests(h)
	h = recoverPanics(s.log)(h)
	return h
}

// writeJSON serializes v as JSON with the given status code.
func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.log.Error("encoding response", "err", err)
	}
}

// writeError returns a minimal error body. Cubozoa never echoes internal
// details to clients; specifics go to the structured log instead.
func (s *Server) writeError(w http.ResponseWriter, status int) {
	s.writeJSON(w, status, map[string]string{"error": http.StatusText(status)})
}
