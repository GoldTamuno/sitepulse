// Package security holds the primitives that, if implemented wrong, become
// CVEs: password hashing, token generation, and JWT signing/verification.
// Deliberately kept separate from service/handler code and given no
// business logic of its own — the smaller and more isolated this code is,
// the easier it is to review carefully and get right once.
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

// Argon2id parameters. These follow OWASP's current baseline recommendation
// (as of their Password Storage Cheat Sheet): 19 MiB memory, 2 iterations,
// 1 degree of parallelism, when memory is constrained — we go a bit higher
// on memory since this runs per-login, not per-request, so the cost is
// bounded and acceptable.
//
// The trade-off to understand: raising `memory` is the single biggest lever
// against GPU/ASIC cracking attempts (that's the whole point of a
// memory-hard function), but it directly costs your server RAM per
// concurrent hash operation — under real load (e.g. 50 logins/sec) this is
// a capacity number you must plan for, not a fire-and-forget constant.
const (
	argonMemory      = 64 * 1024 // KiB = 64 MiB
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLength  = 16
	argonKeyLength   = 32
)

// HashPassword returns a self-describing encoded hash string (algorithm +
// parameters + salt + hash, all base64), similar in spirit to the PHC
// string format. Storing the parameters alongside the hash — rather than
// only in application config — means we can change argonMemory/Iterations
// later without breaking verification of hashes created under the old
// parameters; VerifyPassword reads the parameters back out of the stored
// string instead of assuming today's constants.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("security: generating salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, nil
}

// VerifyPassword re-derives a hash from the candidate password using the
// parameters embedded in the stored string, then compares in constant time.
//
// subtle.ConstantTimeCompare matters here: a naive `==` or bytes.Equal
// comparison returns as soon as it finds the first differing byte, which
// means comparison time leaks information about how many leading bytes
// matched. Over enough attempts, that's a timing side-channel an attacker
// can use to guess the hash byte-by-byte. Constant-time comparison always
// examines every byte regardless of where the first mismatch is.
func VerifyPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("security: invalid hash format")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("security: parsing version: %w", err)
	}

	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false, fmt.Errorf("security: parsing params: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("security: decoding salt: %w", err)
	}
	storedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("security: decoding hash: %w", err)
	}

	// A real Argon2id hash is always a small, fixed number of bytes
	// (argonKeyLength = 32 here). A storedHash this large could only come
	// from a corrupted or tampered stored value — reject it explicitly
	// rather than converting its length to uint32 unchecked, which is
	// what gosec's G115 rule flags as a theoretical integer-overflow risk.
	const maxHashLength = 1 << 20 // 1 MiB — generous upper bound, nowhere close to a real hash's size
	if len(storedHash) > maxHashLength {
		return false, errors.New("security: stored hash exceeds maximum expected length")
	}

	computedHash := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(storedHash))) // #nosec G115

	match := subtle.ConstantTimeCompare(storedHash, computedHash) == 1
	return match, nil
}
