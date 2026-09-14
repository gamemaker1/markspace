package sdk

import (
	"regexp"
	"unicode/utf8"
)

// Id rules. test/vectors/protocol.json lists them under ids.rules.
var (
	pluginIDRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	localIDRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	qualifiedIDRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}\.[a-z0-9][a-z0-9._-]{0,62}$`)
	objectIDRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	secretNameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	eventPatternRe = regexp.MustCompile(`^[a-z0-9*][a-z0-9._*-]{0,127}$`)
)

// MaxStorageKeyBytes is the longest kv or blob key.
const MaxStorageKeyBytes = 512

// ValidPluginID reports whether s is a valid plugin id, such as bookmark-wiki.
func ValidPluginID(s string) bool { return pluginIDRe.MatchString(s) }

// ValidLocalID reports whether s is a valid command, panel, action, importer
// or form id, or a job kind.
func ValidLocalID(s string) bool { return localIDRe.MatchString(s) }

// ValidQualifiedID reports whether s is <pluginId>.<localId>, as used by
// openPanel.
func ValidQualifiedID(s string) bool { return qualifiedIDRe.MatchString(s) }

// ValidObjectID reports whether s is a valid job, item, space, user or
// delivery id for a host API path.
func ValidObjectID(s string) bool { return objectIDRe.MatchString(s) }

// ValidSecretName reports whether s is a valid manifest secret name.
func ValidSecretName(s string) bool { return secretNameRe.MatchString(s) }

// ValidStorageKey reports whether s is a valid kv or blob key. A key is 1 to
// 512 bytes of UTF-8 with no control characters.
func ValidStorageKey(s string) bool {
	if len(s) == 0 || len(s) > MaxStorageKeyBytes || !utf8.ValidString(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return false
		}
	}
	return true
}

// ValidEventPattern reports whether s is a valid event pattern for On.
func ValidEventPattern(s string) bool { return eventPatternRe.MatchString(s) }

// MatchEvent reports whether the event type typ matches pattern. A * matches
// any run of characters, dots and the empty run included. No other character
// is special.
func MatchEvent(pattern, typ string) bool {
	p, t := 0, 0
	star, mark := -1, 0
	for t < len(typ) {
		switch {
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, t
			p++
		case p < len(pattern) && pattern[p] == typ[t]:
			p++
			t++
		case star >= 0:
			p = star + 1
			mark++
			t = mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
