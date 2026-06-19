package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/obtuseaglet/cubozoa/internal/audit"
	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
)

// maxAuthBody caps the AuthenticateByName request body. Credentials are tiny;
// anything larger is rejected to avoid memory-exhaustion attempts.
const maxAuthBody = 4 << 10 // 4 KiB

// POST /Users/AuthenticateByName — the primary login endpoint.
//
// Flow: rate-limit by client IP, parse credentials, verify, then issue an
// access token bound to the client's declared device metadata.
func (s *Server) handleAuthenticateByName(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.cfg.TrustedProxies)
	if !s.limiter.allow("auth:" + ip) {
		w.Header().Set("Retry-After", "5")
		s.writeError(w, http.StatusTooManyRequests)
		return
	}

	var req jellyfin.AuthenticateRequest
	body := http.MaxBytesReader(w, r.Body, maxAuthBody)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		if err == io.EOF {
			s.writeError(w, http.StatusBadRequest)
			return
		}
		s.writeError(w, http.StatusBadRequest)
		return
	}

	ca := clientAuthFrom(r)

	user, err := s.auth.Authenticate(req.Username, req.Pw)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrAccountLocked):
			s.log.Warn("login attempt on locked account", "user", req.Username, "remote", ip)
			s.auditEvent(audit.AccountLocked, "user", req.Username, "remote", ip, "client", ca.Client)
		case isInvalidCreds(err):
			s.log.Warn("failed login", "user", req.Username, "remote", ip)
			s.auditEvent(audit.LoginFailure, "user", req.Username, "remote", ip, "client", ca.Client)
		default:
			s.log.Error("authentication error", "err", err, "remote", ip)
			s.writeError(w, http.StatusInternalServerError)
			return
		}
		s.writeError(w, http.StatusUnauthorized)
		return
	}

	issued, err := s.auth.CreateSession(user.ID, ca.Client, ca.Device, ca.DeviceID, ca.Version, ip)
	if err != nil {
		s.log.Error("creating session", "err", err)
		s.writeError(w, http.StatusInternalServerError)
		return
	}

	s.log.Info("login", "user", user.Name, "client", ca.Client, "device", ca.Device, "remote", ip)
	s.auditEvent(audit.LoginSuccess, "user", user.Name, "remote", ip, "client", ca.Client, "device", ca.Device)

	s.writeJSON(w, http.StatusOK, jellyfin.AuthenticationResult{
		User:        s.toUserDto(user),
		AccessToken: issued.Token,
		ServerID:    s.store.ServerID(),
		SessionInfo: jellyfin.SessionInfoDto{
			ID:                 issued.Session.ID,
			UserID:             user.ID,
			UserName:           user.Name,
			Client:             ca.Client,
			DeviceName:         ca.Device,
			DeviceID:           ca.DeviceID,
			ApplicationVersion: ca.Version,
			ServerID:           s.store.ServerID(),
			PlayableMediaTypes: []string{"Audio", "Video"},
		},
	})
}

// POST /Sessions/Logout — revoke the current session's access token.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	ca := clientAuthFrom(r)
	if err := s.auth.Logout(ca.Token); err != nil {
		s.log.Warn("logout", "err", err)
	}
	if u := userFrom(r); u != nil {
		s.auditEvent(audit.Logout, "user", u.Name, "remote", clientIP(r, s.cfg.TrustedProxies))
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /Sessions/Capabilities/Full — clients report their playback
// capabilities here after login. We accept and acknowledge; capability
// negotiation is handled at stream time in a later milestone.
func (s *Server) handleSessionCapabilities(w http.ResponseWriter, r *http.Request) {
	// Drain and discard the body within a sane bound.
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 64<<10))
	w.WriteHeader(http.StatusNoContent)
}
