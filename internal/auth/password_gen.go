package auth

import (
	"crypto/rand"
	"math/big"
	"strings"
)

// passwordAlphabet excludes visually ambiguous characters (0/O, 1/l/I) so the
// generated admin password can be read off a console without transcription
// errors, while still providing strong entropy.
const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// generatePassword returns a 20-character random password drawn uniformly from
// passwordAlphabet using a cryptographically secure source. 20 chars over a
// 55-symbol alphabet is ~115 bits of entropy.
func generatePassword() (string, error) {
	const length = 20
	max := big.NewInt(int64(len(passwordAlphabet)))
	var b strings.Builder
	b.Grow(length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(passwordAlphabet[n.Int64()])
	}
	return b.String(), nil
}
