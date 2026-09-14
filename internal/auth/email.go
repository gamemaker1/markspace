package auth

import (
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// MaxEmailBytes is the longest accepted address, matching the SMTP limit on a
// forward path.
const MaxEmailBytes = 254

// NormalizeEmail trims surrounding space and lowercases the address. The
// database column is COLLATE NOCASE as well, so a stored address compares
// case-insensitively either way.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidateEmail applies a structural check: exactly one @, a non-empty local
// part, a domain that contains a dot and neither starts nor ends with one, no
// whitespace, and at most 254 bytes. It does not check that the domain
// resolves. A failure is a core.Invalid error.
func ValidateEmail(s string) error {
	if s == "" {
		return core.Invalid("email", "is required")
	}
	if len(s) > MaxEmailBytes {
		return core.Invalid("email", "must be at most 254 bytes")
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return core.Invalid("email", "must not contain whitespace")
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok {
		return core.Invalid("email", "must contain @")
	}
	if strings.Contains(domain, "@") {
		return core.Invalid("email", "must contain exactly one @")
	}
	if local == "" {
		return core.Invalid("email", "must have a local part before @")
	}
	if domain == "" {
		return core.Invalid("email", "must have a domain after @")
	}
	if !strings.Contains(domain, ".") {
		return core.Invalid("email", "domain must contain a dot")
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return core.Invalid("email", "domain must not start or end with a dot")
	}
	if strings.Contains(domain, "..") {
		return core.Invalid("email", "domain must not contain an empty label")
	}
	return nil
}
