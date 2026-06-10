package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/auth"
	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/security"
)

// recoverPanics converts a panic in any handler into a 500 instead of crashing
// the server, logging the failure with its request ID for diagnosis.
func recoverPanics(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered",
						"request_id", requestIDFrom(r),
						"path", r.URL.Path,
						"panic", rec,
					)
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"Internal Server Error"}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder captures the response status for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// logRequests emits one structured log line per request and attaches a request
// ID to the context. Secrets (tokens, passwords) are never logged.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID, _ := security.NewID()
		r = withValue(r, ctxKeyRequestID, reqID)
		w.Header().Set("X-Request-Id", reqID)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		s.log.Info("request",
			"request_id", reqID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"remote", clientIP(r, s.cfg.TrustedProxies),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// securityHeaders applies conservative, broadly-compatible security headers to
// every response. These harden the API surface without breaking native clients.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-site")
		next.ServeHTTP(w, r)
	})
}

// stripEmbyPrefix lets clients that prepend the legacy "/emby" path segment
// (an Emby/Jellyfin compatibility convention) reach the same routes.
func stripEmbyPrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := strings.TrimPrefix(r.URL.Path, "/emby"); p != r.URL.Path {
			if p == "" {
				p = "/"
			}
			r.URL.Path = p
		}
		next.ServeHTTP(w, r)
	})
}

// withAuthContext parses client/device metadata on every request and, if a
// token is present and valid, resolves the authenticated user and session into
// the context. It never rejects a request on its own — enforcement is the job
// of requireAuth — so anonymous endpoints keep working.
func (s *Server) withAuthContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ca := jellyfin.ParseClientAuth(r)
		r = withValue(r, ctxKeyClientAuth, ca)

		if ca.Token != "" {
			if user, sess, err := s.auth.ValidateToken(ca.Token); err == nil {
				r = withValue(r, ctxKeyUser, user)
				r = withValue(r, ctxKeySession, sess)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth wraps a handler so it is only reachable with a valid access
// token, returning 401 otherwise.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if userFrom(r) == nil {
			s.writeError(w, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// requireAdmin wraps a handler so it is only reachable by an administrator.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if u := userFrom(r); u == nil || !u.IsAdmin {
			s.writeError(w, http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

// errInvalidCreds is re-exported for handler comparisons.
var errInvalidCreds = auth.ErrInvalidCredentials

func isInvalidCreds(err error) bool { return errors.Is(err, errInvalidCreds) }
