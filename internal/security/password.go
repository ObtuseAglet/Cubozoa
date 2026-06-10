// Package security provides the cryptographic primitives used across Cubozoa:
// password hashing, secure token generation, and constant-time comparison.
//
// Everything here is designed to be safe by default. There are no knobs that
// let a caller accidentally weaken a hash or compare a secret in variable time.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. These follow current OWASP guidance for argon2id and
// are tuned to be expensive for an attacker while remaining fast enough for an
// interactive login on modest self-hosted hardware.
const (
	argonTime    = 3         // iterations
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrInvalidHash is returned when a stored hash cannot be parsed.
var ErrInvalidHash = errors.New("security: invalid password hash format")

// HashPassword returns an argon2id PHC-formatted hash of the password. The
// returned string is self-describing (algorithm, parameters, salt and digest)
// so it can be verified later without any external configuration, and so the
// cost parameters can be migrated over time.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("security: generating salt: %w", err)
	}

	digest := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		b64(salt), b64(digest),
	), nil
}

// VerifyPassword reports whether password matches the stored PHC hash. The
// comparison is constant time. A malformed hash returns ErrInvalidHash rather
// than a silent mismatch so that data corruption is never confused with a bad
// password.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// "" $argon2id $v=.. $m=..,t=..,p=.. $salt $hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, ErrInvalidHash
	}
	if version != argon2.Version {
		return false, ErrInvalidHash
	}

	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHash
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
