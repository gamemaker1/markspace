// Package auth holds the credential primitives Markspace needs: argon2id
// password hashing, password and email validation, and the token hashing used
// for browser sessions and API tokens. It depends only on the standard
// library, golang.org/x/crypto and internal/core.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/gamemaker1/markspace/internal/core"
)

// Current argon2id cost parameters. NeedsRehash compares a stored hash
// against these values.
const (
	// ArgonMemory is the memory cost in KiB, which is 64 MiB.
	ArgonMemory uint32 = 64 * 1024
	// ArgonTime is the number of passes over memory.
	ArgonTime uint32 = 1
	// ArgonThreads is the degree of parallelism.
	ArgonThreads uint8 = 4
	// ArgonSaltLength is the random salt length in bytes.
	ArgonSaltLength int = 16
	// ArgonKeyLength is the derived key length in bytes.
	ArgonKeyLength uint32 = 32
)

// Password length bounds, counted in bytes.
const (
	// MinPasswordBytes is the shortest accepted password.
	MinPasswordBytes = 8
	// MaxPasswordBytes is the longest accepted password.
	MaxPasswordBytes = 200
)

// Upper bounds on the parameters of a stored hash. A hash beyond them is
// treated as malformed, so a corrupt or hostile row cannot make one login
// allocate gigabytes of memory or run for minutes. The current parameters use
// a quarter of the memory cap and one pass.
const (
	maxArgonMemory uint32 = 256 * 1024
	maxArgonTime   uint32 = 8
	maxSaltBytes          = 256
	maxKeyBytes           = 256
)

// ErrMalformedHash reports a stored password hash that is not a valid
// argon2id encoding. It unwraps to core.ErrInvalid.
var ErrMalformedHash = fmt.Errorf("auth: malformed password hash: %w", core.ErrInvalid)

// b64 is the encoding used for the salt and key inside an encoded hash.
var b64 = base64.RawStdEncoding

// HashPassword derives an argon2id hash of plain with the current cost
// parameters and a fresh 16-byte salt. The result is the PHC-style string
// $argon2id$v=19$m=65536,t=1,p=4$<salt>$<key> with raw standard base64.
func HashPassword(plain string) (string, error) {
	salt := make([]byte, ArgonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, ArgonTime, ArgonMemory, ArgonThreads, ArgonKeyLength)
	return encodeHash(ArgonMemory, ArgonTime, ArgonThreads, salt, key), nil
}

// VerifyPassword reports whether plain matches the encoded argon2id hash. A
// wrong password returns false with a nil error. An error is returned only
// when encoded is not a valid argon2id string.
func VerifyPassword(encoded, plain string) (bool, error) {
	p, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(plain), p.salt, p.time, p.memory, p.threads, uint32(len(p.key)))
	return subtle.ConstantTimeCompare(got, p.key) == 1, nil
}

// NeedsRehash reports whether the stored hash was produced with parameters
// that differ from the current ones, which means the password should be
// rehashed on the next successful login. A malformed hash needs a rehash.
func NeedsRehash(encoded string) bool {
	p, err := parseHash(encoded)
	if err != nil {
		return true
	}
	return p.memory != ArgonMemory ||
		p.time != ArgonTime ||
		p.threads != ArgonThreads ||
		len(p.salt) != ArgonSaltLength ||
		uint32(len(p.key)) != ArgonKeyLength
}

// ValidatePassword checks the length bounds on a new password and returns a
// core.Invalid error when it does not fit.
func ValidatePassword(plain string) error {
	switch {
	case len(plain) < MinPasswordBytes:
		return core.Invalid("password", fmt.Sprintf("must be at least %d bytes", MinPasswordBytes))
	case len(plain) > MaxPasswordBytes:
		return core.Invalid("password", fmt.Sprintf("must be at most %d bytes", MaxPasswordBytes))
	}
	return nil
}

// hashParams is one decoded argon2id hash string.
type hashParams struct {
	memory  uint32
	time    uint32
	threads uint8
	salt    []byte
	key     []byte
}

// encodeHash renders the PHC-style argon2id string.
func encodeHash(memory, time uint32, threads uint8, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, time, threads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// parseHash decodes the PHC-style argon2id string back into its parameters.
func parseHash(encoded string) (hashParams, error) {
	var p hashParams
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" {
		return p, fmt.Errorf("auth: want 5 hash fields, got %d: %w", len(parts)-1, ErrMalformedHash)
	}
	if parts[1] != "argon2id" {
		return p, fmt.Errorf("auth: unsupported algorithm %q: %w", parts[1], ErrMalformedHash)
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, fmt.Errorf("auth: bad version field %q: %w", parts[2], ErrMalformedHash)
	}
	if version != argon2.Version {
		return p, fmt.Errorf("auth: unsupported argon2 version %d: %w", version, ErrMalformedHash)
	}
	memory, time, threads, err := parseCost(parts[3])
	if err != nil {
		return p, err
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return p, fmt.Errorf("auth: decode salt: %w", errors.Join(err, ErrMalformedHash))
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil {
		return p, fmt.Errorf("auth: decode key: %w", errors.Join(err, ErrMalformedHash))
	}
	if len(salt) == 0 || len(key) == 0 {
		return p, fmt.Errorf("auth: empty salt or key: %w", ErrMalformedHash)
	}
	if len(salt) > maxSaltBytes || len(key) > maxKeyBytes {
		return p, fmt.Errorf("auth: salt or key longer than %d bytes: %w", maxKeyBytes, ErrMalformedHash)
	}
	p = hashParams{memory: memory, time: time, threads: threads, salt: salt, key: key}
	return p, nil
}

// parseCost reads the m, t and p fields of an encoded hash.
func parseCost(s string) (memory, time uint32, threads uint8, err error) {
	var seenM, seenT, seenP bool
	for _, field := range strings.Split(s, ",") {
		name, value, ok := strings.Cut(field, "=")
		if !ok {
			return 0, 0, 0, fmt.Errorf("auth: bad cost field %q: %w", field, ErrMalformedHash)
		}
		n, convErr := strconv.ParseUint(value, 10, 32)
		if convErr != nil {
			return 0, 0, 0, fmt.Errorf("auth: bad cost value %q: %w", field, ErrMalformedHash)
		}
		switch name {
		case "m":
			memory, seenM = uint32(n), true
		case "t":
			time, seenT = uint32(n), true
		case "p":
			if n == 0 || n > 255 {
				return 0, 0, 0, fmt.Errorf("auth: parallelism %d out of range: %w", n, ErrMalformedHash)
			}
			threads, seenP = uint8(n), true
		default:
			return 0, 0, 0, fmt.Errorf("auth: unknown cost field %q: %w", name, ErrMalformedHash)
		}
	}
	if !seenM || !seenT || !seenP {
		return 0, 0, 0, fmt.Errorf("auth: cost string %q misses a field: %w", s, ErrMalformedHash)
	}
	if memory == 0 || time == 0 {
		return 0, 0, 0, fmt.Errorf("auth: zero cost in %q: %w", s, ErrMalformedHash)
	}
	if memory > maxArgonMemory || time > maxArgonTime {
		return 0, 0, 0, fmt.Errorf("auth: cost %q is above m=%d,t=%d: %w", s, maxArgonMemory, maxArgonTime, ErrMalformedHash)
	}
	return memory, time, threads, nil
}
