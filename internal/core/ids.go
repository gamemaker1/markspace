package core

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"strings"
	"time"
	"unicode"
)

var idEncoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

// NewID returns a 26-character lowercase identifier. The first 48 bits hold
// the millisecond timestamp and the remaining 80 bits are random, so ids sort
// by creation time to the millisecond. The first 9 characters hold 45 of the
// 48 timestamp bits, and the 10th mixes the last 3 with random bits.
func NewID() string {
	var buf [16]byte
	ms := uint64(time.Now().UTC().UnixMilli())
	binary.BigEndian.PutUint64(buf[0:8], ms<<16)
	if _, err := rand.Read(buf[6:]); err != nil {
		panic("core: entropy source failed: " + err.Error())
	}
	return idEncoding.EncodeToString(buf[:])
}

// ValidID reports whether s has the shape NewID produces: 26 characters from
// the lowercase base32 alphabet. Clients that create notes offline generate
// ids in this shape.
func ValidID(s string) bool {
	if len(s) != 26 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune("0123456789abcdefghjkmnpqrstvwxyz", rune(s[i])) {
			return false
		}
	}
	return true
}

// NewToken returns a URL-safe random token with n bytes of entropy.
func NewToken(n int) string {
	if n <= 0 {
		n = 24
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic("core: entropy source failed: " + err.Error())
	}
	return idEncoding.EncodeToString(buf)
}

// Slugify converts a title into a filesystem-safe and URL-safe slug. It keeps
// letters and digits from any script, folds separators to single hyphens, and
// truncates to 80 runes. An empty result becomes "untitled".
func Slugify(s string) string {
	var b strings.Builder
	lastHyphen := false
	count := 0
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if count >= 80 {
			break
		}
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastHyphen = false
			count++
		case r == '_':
			b.WriteRune('_')
			lastHyphen = false
			count++
		default:
			if !lastHyphen && b.Len() > 0 {
				b.WriteByte('-')
				lastHyphen = true
				count++
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "untitled"
	}
	if isReservedName(out) {
		return out + "-note"
	}
	return out
}

// reserved names that cannot be used as files on Windows or inside the store.
var reservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true,
	".": true, "..": true, "index": true,
}

func isReservedName(s string) bool { return reservedNames[s] }
