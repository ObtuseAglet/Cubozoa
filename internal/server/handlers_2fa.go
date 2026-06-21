package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/obtuseaglet/cubozoa/internal/audit"
	"github.com/obtuseaglet/cubozoa/internal/auth"
)

// These are Cubozoa-native endpoints (not part of the Jellyfin API): standard
// clients have no 2FA UI, so enrollment and recovery happen out of band here.
// Once enabled, login itself stays Jellyfin-compatible — the user appends their
// 6-digit code to the password field.

const max2FABody = 4 << 10

func decode2FABody(w http.ResponseWriter, r *http.Request, v any) bool {
	body := http.MaxBytesReader(w, r.Body, max2FABody)
	return json.NewDecoder(body).Decode(v) == nil
}

// GET /Cubozoa/2FA/Status — whether the caller has two-factor enabled.
func (s *Server) handle2FAStatus(w http.ResponseWriter, r *http.Request) {
	enabled, err := s.auth.TOTPEnabled(userFrom(r).ID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"Enabled": enabled})
}

// POST /Cubozoa/2FA/Setup — begin enrollment; returns the secret and an
// otpauth:// URI to add to an authenticator app. Not active until confirmed.
func (s *Server) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	setup, err := s.auth.BeginTOTPSetup(u.ID, s.cfg.ServerName, u.Name)
	if err != nil {
		if errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
			s.writeError(w, http.StatusConflict)
			return
		}
		s.writeError(w, http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{
		"Secret":          setup.Secret,
		"ProvisioningUri": setup.ProvisioningURI,
	})
}

// POST /Cubozoa/2FA/Activate {Code} — confirm enrollment; returns one-time
// recovery codes (shown once).
func (s *Server) handle2FAActivate(w http.ResponseWriter, r *http.Request) {
	var req struct{ Code string }
	if !decode2FABody(w, r, &req) {
		s.writeError(w, http.StatusBadRequest)
		return
	}
	u := userFrom(r)
	codes, err := s.auth.ActivateTOTP(u.ID, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrTOTPInvalidCode):
			s.writeError(w, http.StatusUnauthorized)
		case errors.Is(err, auth.ErrNoPendingTOTP), errors.Is(err, auth.ErrTOTPAlreadyEnabled):
			s.writeError(w, http.StatusConflict)
		default:
			s.writeError(w, http.StatusInternalServerError)
		}
		return
	}
	s.auditEvent(audit.TwoFAEnabled, "user", u.Name, "remote", clientIP(r, s.cfg.TrustedProxies))
	s.writeJSON(w, http.StatusOK, map[string][]string{"RecoveryCodes": codes})
}

// POST /Cubozoa/2FA/Disable {Code} — turn off 2FA, requiring a current TOTP or
// recovery code so a hijacked session cannot remove it silently.
func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	var req struct{ Code string }
	if !decode2FABody(w, r, &req) {
		s.writeError(w, http.StatusBadRequest)
		return
	}
	u := userFrom(r)
	if err := s.auth.DisableTOTP(u.ID, req.Code); err != nil {
		switch {
		case errors.Is(err, auth.ErrTOTPInvalidCode):
			s.writeError(w, http.StatusUnauthorized)
		case errors.Is(err, auth.ErrTOTPNotEnabled):
			s.writeError(w, http.StatusConflict)
		default:
			s.writeError(w, http.StatusInternalServerError)
		}
		return
	}
	s.auditEvent(audit.TwoFADisabled, "user", u.Name, "remote", clientIP(r, s.cfg.TrustedProxies))
	w.WriteHeader(http.StatusNoContent)
}

// POST /Cubozoa/2FA/Recover {Username, Pw, RecoveryCode} — disable 2FA for a
// user locked out of their authenticator, using password + a one-time recovery
// code. Unauthenticated but rate-limited and lockout-protected like login.
func (s *Server) handle2FARecover(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.cfg.TrustedProxies)
	if !s.limiter.allow("2fa-recover:" + ip) {
		w.Header().Set("Retry-After", "5")
		s.writeError(w, http.StatusTooManyRequests)
		return
	}

	var req struct {
		Username     string
		Pw           string
		RecoveryCode string
	}
	if !decode2FABody(w, r, &req) {
		s.writeError(w, http.StatusBadRequest)
		return
	}

	err := s.auth.RecoverTOTP(req.Username, req.Pw, req.RecoveryCode)
	if err != nil {
		// Collapse all failure modes to a generic 401 to avoid revealing whether
		// the account exists or has 2FA enabled.
		s.log.Warn("2fa recovery failed", "user", req.Username, "remote", ip)
		s.writeError(w, http.StatusUnauthorized)
		return
	}
	s.auditEvent(audit.TwoFARecover, "user", req.Username, "remote", ip)
	w.WriteHeader(http.StatusNoContent)
}
