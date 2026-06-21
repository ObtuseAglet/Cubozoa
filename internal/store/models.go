package store

import "time"

// User is Cubozoa's internal representation of an account. It is intentionally
// separate from the Jellyfin wire DTOs: the API layer maps between them, so the
// persisted shape can evolve without breaking client compatibility.
type User struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"password_hash"`
	IsAdmin      bool      `json:"is_admin"`
	CreatedAt    time.Time `json:"created_at"`
	LastLoginAt  time.Time `json:"last_login_at"`

	// Brute-force protection: consecutive failed logins and, once the threshold
	// is crossed, the time until which the account is locked.
	FailedLoginAttempts int       `json:"failed_login_attempts,omitempty"`
	LockedUntil         time.Time `json:"locked_until,omitempty"`

	// Two-factor authentication (TOTP). TOTPSecret is the active shared secret;
	// PendingTOTPSecret holds a not-yet-confirmed secret during enrollment.
	// RecoveryCodeHashes are SHA-256 hashes of single-use recovery codes.
	TOTPEnabled        bool     `json:"totp_enabled,omitempty"`
	TOTPSecret         string   `json:"totp_secret,omitempty"`
	PendingTOTPSecret  string   `json:"pending_totp_secret,omitempty"`
	RecoveryCodeHashes []string `json:"recovery_code_hashes,omitempty"`
}

// Session is an authenticated client session. The access token is never stored
// in the clear; only its SHA-256 hash is persisted, so a compromised datastore
// does not yield usable tokens.
type Session struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	TokenHash    string    `json:"token_hash"`
	Client       string    `json:"client"`
	Device       string    `json:"device"`
	DeviceID     string    `json:"device_id"`
	AppVersion   string    `json:"app_version"`
	RemoteAddr   string    `json:"remote_addr"`
	CreatedAt    time.Time `json:"created_at"`
	LastActivity time.Time `json:"last_activity"`
}
