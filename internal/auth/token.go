package auth

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/gamemaker1/markspace/internal/core"
)

// Token shape constants.
const (
	// SessionTokenBytes is the entropy of a browser session token.
	SessionTokenBytes = 32
	// APITokenBytes is the entropy of an API token, excluding the prefix.
	APITokenBytes = 24
	// APITokenPrefix is the visible marker at the start of every API token.
	APITokenPrefix = "msk_"
	// APITokenPrefixLength is how many leading characters of an API token are
	// stored in clear, so the interface can show which token is which.
	APITokenPrefixLength = 12
)

// HashToken returns the lowercase hex SHA-256 of a token. The database stores
// only this digest, for both sessions and API tokens, so a database leak does
// not hand out usable credentials.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// NewSessionToken returns a fresh session token and its digest. The plaintext
// goes in the cookie and is never stored. The hash goes in the sessions row.
func NewSessionToken() (plaintext, hash string) {
	plaintext = core.NewToken(SessionTokenBytes)
	return plaintext, HashToken(plaintext)
}

// NewAPIToken returns a fresh API token, its digest and its display prefix.
// The plaintext is shown to the user once at creation time. The prefix is the
// first 12 characters of the plaintext, which includes the msk_ marker.
func NewAPIToken() (plaintext, hash, prefix string) {
	plaintext = APITokenPrefix + core.NewToken(APITokenBytes)
	prefix = plaintext
	if len(prefix) > APITokenPrefixLength {
		prefix = prefix[:APITokenPrefixLength]
	}
	return plaintext, HashToken(plaintext), prefix
}
