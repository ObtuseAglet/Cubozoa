package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// NewToken returns a cryptographically random opaque token, hex encoded. The
// default 32 bytes yields 256 bits of entropy, which is well beyond brute-force
// reach and matches the size clients expect for an access token.
func NewToken() (string, error) {
	return randomHex(32)
}

// NewID returns a random 16-byte identifier, hex encoded (32 chars). This is
// the shape Jellyfin clients expect for server, user and item identifiers.
func NewID() (string, error) {
	return randomHex(16)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("security: reading random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// HashToken returns a SHA-256 hash of a token, hex encoded. Access tokens are
// high-entropy secrets, so a fast hash (not a slow KDF) is the correct choice:
// it lets us store only the hash at rest — so a leaked datastore does not leak
// usable tokens — without adding latency to every authenticated request.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeEqual compares two strings without leaking length-independent
// timing information. Use it for any secret comparison that is not already
// covered by a dedicated verifier.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
