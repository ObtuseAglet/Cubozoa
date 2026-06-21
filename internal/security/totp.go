package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238) parameters. These match what authenticator apps default to,
// so a generated secret scans and works without extra configuration.
const (
	totpDigits = 6
	totpPeriod = 30 * time.Second
	totpSkew   = 1 // accept the adjacent windows (±30s) for clock drift
)

// base32NoPad is the encoding authenticator apps expect for the shared secret.
var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a fresh base32-encoded TOTP secret (160 bits of
// entropy, the RFC-recommended size for HMAC-SHA1).
func NewTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("security: generating totp secret: %w", err)
	}
	return base32NoPad.EncodeToString(b), nil
}

// totpCode computes the TOTP value for a secret at a point in time.
func totpCode(secret string, t time.Time) (string, error) {
	key, err := base32NoPad.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("security: invalid totp secret: %w", err)
	}
	counter := uint64(t.Unix()) / uint64(totpPeriod.Seconds())

	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	// Dynamic truncation (RFC 4226 §5.3).
	offset := sum[len(sum)-1] & 0x0f
	binCode := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])

	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, binCode%mod), nil
}

// CurrentTOTP returns the TOTP value for a secret at the current time.
func CurrentTOTP(secret string) (string, error) {
	return totpCode(secret, time.Now())
}

// VerifyTOTP reports whether code is valid for the secret at the current time,
// tolerating one period of clock skew in either direction. The comparison is
// constant time.
func VerifyTOTP(secret, code string) bool {
	return verifyTOTPAt(secret, code, time.Now())
}

func verifyTOTPAt(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	for w := -totpSkew; w <= totpSkew; w++ {
		want, err := totpCode(secret, now.Add(time.Duration(w)*totpPeriod))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// TOTPProvisioningURI builds the otpauth:// URI an authenticator app imports
// (typically via QR code).
func TOTPProvisioningURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", int(totpPeriod.Seconds())))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// NewRecoveryCodes returns n single-use recovery codes (for when an
// authenticator is lost) and their SHA-256 hashes for storage. The plaintext is
// shown to the user once; only the hashes are persisted.
func NewRecoveryCodes(n int) (codes []string, hashes []string, err error) {
	for i := 0; i < n; i++ {
		c, err := randomHex(5) // 10 hex chars
		if err != nil {
			return nil, nil, err
		}
		codes = append(codes, c)
		hashes = append(hashes, HashToken(c))
	}
	return codes, hashes, nil
}
