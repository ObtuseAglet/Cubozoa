// Package audit emits structured, security-relevant events (logins, lockouts,
// logouts) to a dedicated channel so they can be shipped to a SIEM or reviewed
// independently of operational logs.
//
// Events are intentionally minimal and never contain secrets — no passwords, no
// tokens. They identify who did what, from where, and whether it succeeded.
package audit

import (
	"io"
	"log/slog"
)

// Logger writes audit events as structured JSON lines.
type Logger struct {
	l *slog.Logger
}

// New returns an audit Logger writing JSON events to w.
func New(w io.Writer) *Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})
	return &Logger{l: slog.New(h).With("category", "audit")}
}

// Event records a named security event with structured fields.
func (a *Logger) Event(event string, attrs ...any) {
	if a == nil {
		return
	}
	a.l.Info(event, attrs...)
}

// Common event names.
const (
	LoginSuccess  = "auth.login.success"
	LoginFailure  = "auth.login.failure"
	AccountLocked = "auth.account.locked"
	Logout        = "auth.logout"
)
