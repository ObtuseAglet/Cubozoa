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

// Service provides authentication operations over a Store.
type Service struct {
	store store.Store
	log   *slog.Logger
}

// New constructs an auth Service.
func New(s store.Store, log *slog.Logger) *Service {
	return &Service{store: s, log: log}
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

// Authenticate verifies credentials and, on success, returns the user. To
// resist timing-based user enumeration, a verification is always performed —
// against a dummy hash when the user does not exist — so the response time does
// not reveal whether the username was valid.
func (s *Service) Authenticate(username, password string) (*store.User, error) {
	u, err := s.store.GetUserByName(username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_, _ = security.VerifyPassword(password, dummyHash)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	ok, err := security.VerifyPassword(password, u.PasswordHash)
	if err != nil || !ok {
		return nil, ErrInvalidCredentials
	}

	u.LastLoginAt = time.Now().UTC()
	if err := s.store.UpdateUser(u); err != nil {
		s.log.Warn("failed to update last login", "user", u.ID, "err", err)
	}
	return u, nil
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
