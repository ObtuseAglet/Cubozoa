package server

import (
	"context"
	"net/http"

	"github.com/obtuseaglet/cubozoa/internal/jellyfin"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

type contextKey int

const (
	ctxKeyClientAuth contextKey = iota
	ctxKeyUser
	ctxKeySession
	ctxKeyRequestID
)

// clientAuthFrom returns the parsed client/device metadata for the request.
func clientAuthFrom(r *http.Request) jellyfin.ClientAuth {
	if ca, ok := r.Context().Value(ctxKeyClientAuth).(jellyfin.ClientAuth); ok {
		return ca
	}
	return jellyfin.ClientAuth{}
}

// userFrom returns the authenticated user, or nil if the request is anonymous.
func userFrom(r *http.Request) *store.User {
	if u, ok := r.Context().Value(ctxKeyUser).(*store.User); ok {
		return u
	}
	return nil
}

// sessionFrom returns the authenticated session, or nil if anonymous.
func sessionFrom(r *http.Request) *store.Session {
	if s, ok := r.Context().Value(ctxKeySession).(*store.Session); ok {
		return s
	}
	return nil
}

func requestIDFrom(r *http.Request) string {
	if id, ok := r.Context().Value(ctxKeyRequestID).(string); ok {
		return id
	}
	return ""
}

func withValue(r *http.Request, key contextKey, val any) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), key, val))
}
