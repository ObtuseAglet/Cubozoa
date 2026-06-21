package auth

import (
	"errors"

	"github.com/obtuseaglet/cubozoa/internal/security"
	"github.com/obtuseaglet/cubozoa/internal/store"
)

// Two-factor errors.
var (
	ErrTOTPNotEnabled     = errors.New("auth: two-factor is not enabled")
	ErrTOTPAlreadyEnabled = errors.New("auth: two-factor is already enabled")
	ErrNoPendingTOTP      = errors.New("auth: no pending two-factor enrollment")
	ErrTOTPInvalidCode    = errors.New("auth: invalid two-factor code")
)

const recoveryCodeCount = 10

// TOTPSetup is the data a user needs to enroll an authenticator app.
type TOTPSetup struct {
	Secret          string // base32 shared secret
	ProvisioningURI string // otpauth:// URI for QR import
}

// BeginTOTPSetup generates a pending TOTP secret for a user and returns the
// enrollment data. It does not enable 2FA until the secret is confirmed with a
// code via ActivateTOTP. Calling it again replaces any unconfirmed secret.
func (s *Service) BeginTOTPSetup(userID, issuer, account string) (*TOTPSetup, error) {
	u, err := s.store.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	if u.TOTPEnabled {
		return nil, ErrTOTPAlreadyEnabled
	}
	secret, err := security.NewTOTPSecret()
	if err != nil {
		return nil, err
	}
	u.PendingTOTPSecret = secret
	if err := s.store.UpdateUser(u); err != nil {
		return nil, err
	}
	return &TOTPSetup{
		Secret:          secret,
		ProvisioningURI: security.TOTPProvisioningURI(issuer, account, secret),
	}, nil
}

// ActivateTOTP confirms a pending enrollment with a current code and, on
// success, enables 2FA and returns one-time recovery codes (shown to the user
// once; only their hashes are stored).
func (s *Service) ActivateTOTP(userID, code string) ([]string, error) {
	u, err := s.store.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	if u.TOTPEnabled {
		return nil, ErrTOTPAlreadyEnabled
	}
	if u.PendingTOTPSecret == "" {
		return nil, ErrNoPendingTOTP
	}
	if !security.VerifyTOTP(u.PendingTOTPSecret, code) {
		return nil, ErrTOTPInvalidCode
	}

	codes, hashes, err := security.NewRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return nil, err
	}
	u.TOTPSecret = u.PendingTOTPSecret
	u.PendingTOTPSecret = ""
	u.TOTPEnabled = true
	u.RecoveryCodeHashes = hashes
	if err := s.store.UpdateUser(u); err != nil {
		return nil, err
	}
	return codes, nil
}

// DisableTOTP turns off 2FA for an authenticated user, requiring a current TOTP
// code (or a valid recovery code) so a hijacked session cannot silently remove
// the second factor.
func (s *Service) DisableTOTP(userID, code string) error {
	u, err := s.store.GetUserByID(userID)
	if err != nil {
		return err
	}
	if !u.TOTPEnabled {
		return ErrTOTPNotEnabled
	}
	if !security.VerifyTOTP(u.TOTPSecret, code) && !consumeRecoveryCode(u, code) {
		return ErrTOTPInvalidCode
	}
	clearTOTP(u)
	return s.store.UpdateUser(u)
}

// RecoverTOTP disables 2FA for a user who has lost their authenticator, using
// their password plus a single-use recovery code. It is unauthenticated by
// design (the user cannot log in), so it verifies the password itself and is
// subject to the same lockout protection as login.
func (s *Service) RecoverTOTP(username, password, recoveryCode string) error {
	u, err := s.store.GetUserByName(username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_, _ = security.VerifyPassword(password, dummyHash)
			return ErrInvalidCredentials
		}
		return err
	}
	if s.isLocked(u) {
		return ErrAccountLocked
	}
	if ok, err := security.VerifyPassword(password, u.PasswordHash); err != nil || !ok {
		s.recordFailure(u)
		return ErrInvalidCredentials
	}
	if !u.TOTPEnabled {
		return ErrTOTPNotEnabled
	}
	if !consumeRecoveryCode(u, recoveryCode) {
		s.recordFailure(u)
		return ErrTOTPInvalidCode
	}
	clearTOTP(u)
	u.FailedLoginAttempts = 0
	return s.store.UpdateUser(u)
}

// TOTPEnabled reports whether a user has two-factor enabled.
func (s *Service) TOTPEnabled(userID string) (bool, error) {
	u, err := s.store.GetUserByID(userID)
	if err != nil {
		return false, err
	}
	return u.TOTPEnabled, nil
}

// consumeRecoveryCode removes a matching (single-use) recovery code from the
// user, returning whether one matched. Comparison is via stored hashes.
func consumeRecoveryCode(u *store.User, code string) bool {
	if code == "" {
		return false
	}
	want := security.HashToken(code)
	for i, h := range u.RecoveryCodeHashes {
		if security.ConstantTimeEqual(h, want) {
			u.RecoveryCodeHashes = append(u.RecoveryCodeHashes[:i], u.RecoveryCodeHashes[i+1:]...)
			return true
		}
	}
	return false
}

func clearTOTP(u *store.User) {
	u.TOTPEnabled = false
	u.TOTPSecret = ""
	u.PendingTOTPSecret = ""
	u.RecoveryCodeHashes = nil
}
