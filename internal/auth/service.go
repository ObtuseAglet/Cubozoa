// Package auth implements Cubozoa's authentication: account seeding, password
// verification, and access-token session lifecycle.
//
// Security properties enforced here:
//   - Passwords are only ever stored as argon2id hashes.
//   - Access tokens are stored only as SHA-256 hashes; the plaintext exists
//     just long enough to return it to the client once.
//   - Authentication failures are deliberately indistinguishable (same error)
//     whether the user is missing or the password is wrong, to avoid user
//     enumeration.
package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/obtuseaglet/cubozoa/internal/security"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// ErrInvalidCredentials is returned for any failed authentication. It is
// intentionally generic to avoid leaking whether an account exists.
var ErrInvalidCredentials = errors.New("auth: invalid username or password")

// ErrAccountLocked is returned (internally) when an account is temporarily
// locked after too many failed attempts. Handlers map it to the same generic
// 401 as bad credentials so it does not reveal that the account exists.
var ErrAccountLocked = errors.New("auth: account temporarily locked")

// Default brute-force protection policy.
const (
	defaultLockoutThreshold = 5
	defaultLockoutDuration  = 15 * time.Minute
)

// Service provides authentication operations over a Store.
type Service struct {
	store store.Store
	log   *slog.Logger

	lockoutThreshold int
	lockoutDuration  time.Duration
}

// New constructs an auth Service.
func New(s store.Store, log *slog.Logger) *Service {
	return &Service{
		store:            s,
		log:              log,
		lockoutThreshold: defaultLockoutThreshold,
		lockoutDuration:  defaultLockoutDuration,
	}
}

// SetLockoutPolicy overrides the account-lockout threshold and duration. A
// threshold of 0 disables lockout (per-IP rate limiting still applies).
func (s *Service) SetLockoutPolicy(threshold int, duration time.Duration) {
	s.lockoutThreshold = threshold
	if duration > 0 {
		s.lockoutDuration = duration
	}
}

// SeedAdmin creates the initial administrator account if no users exist. If
// password is empty, a strong random password is generated and returned so the
// caller can surface it to the operator exactly once.
//
// It returns the generated password (empty if one was supplied or if an admin
// already existed) and whether a new account was created.
func (s *Service) SeedAdmin(username, password string) (generated string, created bool, err error) {
	count, err := s.store.CountUsers()
	if err != nil {
		return "", false, err
	}
	if count > 0 {
		return "", false, nil
	}

	if password == "" {
		password, err = generatePassword()
		if err != nil {
			return "", false, err
		}
		generated = password
	}

	if err := s.CreateUser(username, password, true); err != nil {
		return "", false, err
	}
	return generated, true, nil
}

// CreateUser creates a new account with an argon2id-hashed password.
func (s *Service) CreateUser(username, password string, admin bool) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("auth: username must not be empty")
	}
	if len(password) < 8 {
		return errors.New("auth: password must be at least 8 characters")
	}

	hash, err := security.HashPassword(password)
	if err != nil {
		return err
	}
	id, err := security.NewID()
	if err != nil {
		return err
	}

	u := &store.User{
		ID:           id,
		Name:         username,
		PasswordHash: hash,
		IsAdmin:      admin,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.store.CreateUser(u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("auth: user %q already exists", username)
		}
		return err
	}
	return nil
}

// Authenticate verifies credentials and, on success, returns the user.
//
// Two abuse protections are layered in:
//   - Timing-based user enumeration is resisted by always performing a
//     verification (against a dummy hash when the user does not exist).
//   - Repeated failures against a real account increment a counter and, past
//     the threshold, lock the account for a cool-off period. A locked account
//     returns ErrAccountLocked, which callers surface as a generic 401.
func (s *Service) Authenticate(username, password string) (*store.User, error) {
	u, err := s.store.GetUserByName(username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_, _ = security.VerifyPassword(password, dummyHash)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	if s.isLocked(u) {
		return nil, ErrAccountLocked
	}

	ok, err := security.VerifyPassword(password, u.PasswordHash)
	if err != nil || !ok {
		s.recordFailure(u)
		if s.isLocked(u) {
			return nil, ErrAccountLocked
		}
		return nil, ErrInvalidCredentials
	}

	// Success: clear any failure state and record the login.
	u.FailedLoginAttempts = 0
	u.LockedUntil = time.Time{}
	u.LastLoginAt = time.Now().UTC()
	if err := s.store.UpdateUser(u); err != nil {
		s.log.Warn("failed to update user on login", "user", u.ID, "err", err)
	}
	return u, nil
}

// isLocked reports whether the account is currently within a lockout window.
func (s *Service) isLocked(u *store.User) bool {
	return !u.LockedUntil.IsZero() && time.Now().UTC().Before(u.LockedUntil)
}

// recordFailure increments the failure counter and locks the account once the
// threshold is reached, persisting the change.
func (s *Service) recordFailure(u *store.User) {
	if s.lockoutThreshold <= 0 {
		return
	}
	u.FailedLoginAttempts++
	if u.FailedLoginAttempts >= s.lockoutThreshold {
		u.LockedUntil = time.Now().UTC().Add(s.lockoutDuration)
		s.log.Warn("account locked after repeated failures", "user", u.Name, "until", u.LockedUntil)
	}
	if err := s.store.UpdateUser(u); err != nil {
		s.log.Warn("failed to record login failure", "user", u.ID, "err", err)
	}
}

// IssuedToken bundles a new session with the one-time plaintext access token.
type IssuedToken struct {
	Token   string
	Session *store.Session
}

// CreateSession issues a new access token for a user and persists the session
// (storing only the token hash). The returned plaintext token is the only time
// it is available.
func (s *Service) CreateSession(userID, client, device, deviceID, appVersion, remoteAddr string) (*IssuedToken, error) {
	token, err := security.NewToken()
	if err != nil {
		return nil, err
	}
	id, err := security.NewID()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	sess := &store.Session{
		ID:           id,
		UserID:       userID,
		TokenHash:    security.HashToken(token),
		Client:       client,
		Device:       device,
		DeviceID:     deviceID,
		AppVersion:   appVersion,
		RemoteAddr:   remoteAddr,
		CreatedAt:    now,
		LastActivity: now,
	}
	if err := s.store.CreateSession(sess); err != nil {
		return nil, err
	}
	return &IssuedToken{Token: token, Session: sess}, nil
}

// ValidateToken resolves an access token to its user and session. It returns
// ErrInvalidCredentials for any unknown or malformed token.
func (s *Service) ValidateToken(token string) (*store.User, *store.Session, error) {
	if token == "" {
		return nil, nil, ErrInvalidCredentials
	}
	sess, err := s.store.GetSessionByTokenHash(security.HashToken(token))
	if err != nil {
		return nil, nil, ErrInvalidCredentials
	}
	u, err := s.store.GetUserByID(sess.UserID)
	if err != nil {
		return nil, nil, ErrInvalidCredentials
	}
	_ = s.store.TouchSession(sess.ID)
	return u, sess, nil
}

// Logout revokes a session by access token. Unknown tokens are a no-op.
func (s *Service) Logout(token string) error {
	if token == "" {
		return nil
	}
	sess, err := s.store.GetSessionByTokenHash(security.HashToken(token))
	if err != nil {
		return nil
	}
	return s.store.DeleteSession(sess.ID)
}

// dummyHash is a precomputed argon2id hash used to keep authentication timing
// uniform when a username does not exist. Its plaintext is irrelevant.
var dummyHash = mustHash("cubozoa-timing-equalizer")

func mustHash(pw string) string {
	h, err := security.HashPassword(pw)
	if err != nil {
		panic(err)
	}
	return h
}
