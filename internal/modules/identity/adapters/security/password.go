// Package security implements the module's cryptographic ports: password
// hashes with argon2id and HS256 JWT access tokens.
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

// Argon2Params tunes argon2id. Adjust on the real server until a verification
// takes 150-300 ms.
type Argon2Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2Params: 64 MiB, 3 passes, 1 thread.
var DefaultArgon2Params = Argon2Params{MemoryKiB: 64 * 1024, Iterations: 3, Parallelism: 1, SaltLength: 16, KeyLength: 32}

var errMalformedHash = errors.New("malformed password hash")

// maxKeyLength bounds the key read from a stored hash.
const maxKeyLength = 1024

// Argon2Hasher stores hashes in PHC format:
// $argon2id$v=19$m=65536,t=3,p=1$<salt>$<hash>, so changing the parameters does not
// invalidate old hashes.
type Argon2Hasher struct {
	Params Argon2Params
}

// Hash hashes a password with a fresh salt.
func (h Argon2Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.Params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	p := h.Params
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.MemoryKiB, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.MemoryKiB, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify compares in constant time using the parameters stored in the hash.
func (Argon2Hasher) Verify(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errMalformedHash
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, errMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errMalformedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 || len(want) > maxKeyLength {
		return false, errMalformedHash
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want))) //nolint:gosec // bounded by maxKeyLength
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
