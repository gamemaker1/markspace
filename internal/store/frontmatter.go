// Package store implements ports.Files over the local filesystem. Notes are
// markdown files with YAML frontmatter, and the directory next to a note file
// holds its children.
package store

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/gamemaker1/markspace/internal/core"
)

// Frontmatter keys the application owns. Every other key is read into
// Note.Extra and written back after the owned keys.
const (
	keyID    = "id"
	keyTitle = "title"
	keyIcon  = "icon"
	keyTags  = "tags"
	// keyParent is read and dropped. The containing folder is the parent.
	keyParent      = "parent"
	keyPosition    = "position"
	keyCreated     = "created"
	keyUpdated     = "updated"
	keyAuthor      = "author"
	keyUpdatedBy   = "updated_by"
	keyUpdatedVia  = "updated_via"
	keyDeleted     = "deleted"
	keyDeletedAt   = "deleted_at"
	keyDeletedBy   = "deleted_by"
	keyTrashedFrom = "trashed_from"
	keyLabel       = "label"
)

// knownKeys are lifted out of the frontmatter map into Note fields.
var knownKeys = map[string]bool{
	keyID: true, keyTitle: true, keyIcon: true, keyTags: true,
	keyParent: true, keyPosition: true, keyCreated: true, keyUpdated: true,
	keyAuthor: true, keyUpdatedBy: true, keyUpdatedVia: true, keyDeleted: true, keyDeletedAt: true,
	keyDeletedBy: true, keyTrashedFrom: true,
}

// timeLayouts are tried in order when a frontmatter time is read.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

var headingRe = regexp.MustCompile(`^#{1,6}[ \t]+(.+?)[ \t]*#*[ \t]*$`)

// splitFrontmatter separates a leading YAML block from the body. It reports
// whether a block was present. The body keeps its exact bytes, including the
// trailing newline state.
func splitFrontmatter(data []byte) (yamlText string, body string, ok bool) {
	s := string(data)
	rest, found := cutOpener(s)
	if !found {
		return "", s, false
	}
	lines := strings.SplitAfter(rest, "\n")
	var fm strings.Builder
	consumed := 0
	for _, line := range lines {
		consumed += len(line)
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "---" || trimmed == "..." {
			return fm.String(), rest[consumed:], true
		}
		fm.WriteString(line)
	}
	// No closing delimiter, so there is no frontmatter block.
	return "", s, false
}

// cutOpener removes a leading "---" line and returns the remainder.
func cutOpener(s string) (string, bool) {
	switch {
	case strings.HasPrefix(s, "---\n"):
		return s[4:], true
	case strings.HasPrefix(s, "---\r\n"):
		return s[5:], true
	case s == "---":
		return "", true
	default:
		return "", false
	}
}

// parseFrontmatter decodes a YAML mapping into a plain map. Scalars keep their
// source text when YAML would resolve them to a timestamp, so unknown keys
// survive a round trip unchanged.
func parseFrontmatter(yamlText string) (map[string]any, error) {
	out := map[string]any{}
	if strings.TrimSpace(yamlText) == "" {
		return out, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w: %v", core.ErrInvalid, err)
	}
	if len(doc.Content) == 0 {
		return out, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse frontmatter: %w: not a mapping", core.ErrInvalid)
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		out[root.Content[i].Value] = nodeValue(root.Content[i+1])
	}
	return out, nil
}

// nodeValue converts a YAML node into a JSON-friendly Go value.
func nodeValue(n *yaml.Node) any {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.AliasNode:
		return nodeValue(n.Alias)
	case yaml.SequenceNode:
		items := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			items = append(items, nodeValue(c))
		}
		return items
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			m[n.Content[i].Value] = nodeValue(n.Content[i+1])
		}
		return m
	}
	switch n.Tag {
	case "!!null":
		return nil
	case "!!bool":
		b, err := strconv.ParseBool(n.Value)
		if err != nil {
			return n.Value
		}
		return b
	case "!!int":
		i, err := strconv.ParseInt(n.Value, 0, 64)
		if err != nil {
			return n.Value
		}
		return int(i)
	case "!!float":
		f, err := strconv.ParseFloat(n.Value, 64)
		if err != nil {
			return n.Value
		}
		return f
	default:
		// Strings and timestamps keep their source text.
		return n.Value
	}
}

// noteFromFrontmatter fills a note from a decoded frontmatter map. Unknown
// keys and trashed_from land in Note.Extra.
func noteFromFrontmatter(fm map[string]any, note *core.Note) {
	note.ID = asString(fm[keyID])
	note.Title = asString(fm[keyTitle])
	note.Icon = asString(fm[keyIcon])
	note.Tags = asStrings(fm[keyTags])
	note.Position = asInt(fm[keyPosition])
	note.CreatedAt, _ = asTime(fm[keyCreated])
	note.UpdatedAt, _ = asTime(fm[keyUpdated])
	note.CreatedBy = asString(fm[keyAuthor])
	note.UpdatedBy = asString(fm[keyUpdatedBy])
	note.UpdatedVia = asString(fm[keyUpdatedVia])
	note.Deleted = asBool(fm[keyDeleted])
	if t, ok := asTime(fm[keyDeletedAt]); ok {
		note.DeletedAt = &t
	}
	note.DeletedBy = asString(fm[keyDeletedBy])
	extra := map[string]any{}
	for k, v := range fm {
		if knownKeys[k] && k != keyTrashedFrom {
			continue
		}
		extra[k] = v
	}
	if len(extra) > 0 {
		note.Extra = extra
	}
}

// serializeNote renders a note as the full file text: a frontmatter block
// followed by the body bytes unchanged.
func serializeNote(note *core.Note) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("---\n")
	writeScalar(&b, keyID, note.ID)
	writeScalar(&b, keyTitle, note.Title)
	writeScalar(&b, keyIcon, note.Icon)
	if len(note.Tags) > 0 {
		b.WriteString(keyTags)
		b.WriteString(": ")
		b.WriteString(flowSeq(note.Tags))
		b.WriteByte('\n')
	}
	if note.Position != 0 {
		fmt.Fprintf(&b, "%s: %d\n", keyPosition, note.Position)
	}
	writeTime(&b, keyCreated, note.CreatedAt)
	writeTime(&b, keyUpdated, note.UpdatedAt)
	writeScalar(&b, keyAuthor, note.CreatedBy)
	writeScalar(&b, keyUpdatedBy, note.UpdatedBy)
	writeScalar(&b, keyUpdatedVia, note.UpdatedVia)
	if note.Deleted {
		fmt.Fprintf(&b, "%s: true\n", keyDeleted)
	}
	if note.DeletedAt != nil && !note.DeletedAt.IsZero() {
		writeTime(&b, keyDeletedAt, *note.DeletedAt)
	}
	writeScalar(&b, keyDeletedBy, note.DeletedBy)
	if from, ok := note.Extra[keyTrashedFrom]; ok {
		if s := asString(from); s != "" {
			writeScalar(&b, keyTrashedFrom, s)
		}
	}
	keys := make([]string, 0, len(note.Extra))
	for k := range note.Extra {
		if knownKeys[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		chunk, err := marshalKey(k, note.Extra[k])
		if err != nil {
			return nil, fmt.Errorf("serialize frontmatter key %q: %w", k, err)
		}
		b.WriteString(chunk)
	}
	b.WriteString("---\n")
	b.WriteString(note.Content)
	return b.Bytes(), nil
}

// writeScalar appends "key: value" when value is not empty.
func writeScalar(b *bytes.Buffer, key, value string) {
	if value == "" {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(yamlScalar(value))
	b.WriteByte('\n')
}

// writeTime appends an RFC 3339 nanosecond UTC timestamp when t is set.
func writeTime(b *bytes.Buffer, key string, t time.Time) {
	if t.IsZero() {
		return
	}
	fmt.Fprintf(b, "%s: %s\n", key, t.UTC().Format(time.RFC3339Nano))
}

// flowSeq renders a string slice as a YAML flow sequence.
func flowSeq(items []string) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, yamlScalar(it))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// marshalKey renders one arbitrary frontmatter entry with yaml.v3.
func marshalKey(key string, v any) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(map[string]any{key: v}); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// yamlScalar renders a string as a YAML scalar, preferring the plain form and
// falling back to a double-quoted form that keeps non-ASCII runes literal.
func yamlScalar(s string) string {
	if plainSafe(s) {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case 0x2028:
			b.WriteString(`\L`)
		case 0x2029:
			b.WriteString(`\P`)
		default:
			switch {
			case r < 0x20 || (r >= 0x7f && r <= 0x9f):
				fmt.Fprintf(&b, `\x%02x`, r)
			case r == 0xfeff || r == 0xfffe || r == 0xffff:
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// yamlPlainRune reports whether r may appear unescaped in a plain scalar. It
// excludes every rune the YAML reader rejects, and the NEL, LS and PS line
// breaks, which would split the scalar.
func yamlPlainRune(r rune) bool {
	switch {
	case r < 0x20, r >= 0x7f && r <= 0x9f:
		return false
	case r == 0x2028, r == 0x2029, r == 0xfeff, r == 0xfffe, r == 0xffff:
		return false
	case r == utf8.RuneError:
		return false
	}
	return true
}

// resolvesToString reports whether yaml.v3 reads s, written plain, back as the
// same string. It catches the integer forms the number pattern misses, such
// as "0x1F", "0o17" and "1_000", and every timestamp form.
func resolvesToString(s string) bool {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("["+s+"]"), &doc); err != nil {
		return false
	}
	if len(doc.Content) != 1 || len(doc.Content[0].Content) != 1 {
		return false
	}
	n := doc.Content[0].Content[0]
	return n.Kind == yaml.ScalarNode && n.Tag == "!!str" && n.Value == s
}

// plainSafeSpecial lists runes that force a quoted scalar.
const plainSafeSpecial = "\"'#:,[]{}|>&*!%@`\\\n\r\t"

// plainBoolNull lists plain scalars YAML would resolve to a non-string.
var plainBoolNull = map[string]bool{
	"true": true, "false": true, "yes": true, "no": true,
	"on": true, "off": true, "null": true, "~": true,
	"True": true, "False": true, "Null": true, "NULL": true,
	"TRUE": true, "FALSE": true, "Yes": true, "No": true,
}

var numberRe = regexp.MustCompile(`^[-+]?(\d+(\.\d*)?|\.\d+)([eE][-+]?\d+)?$`)

// plainSafe reports whether s can be written as an unquoted YAML scalar,
// including inside a flow sequence.
func plainSafe(s string) bool {
	if s == "" || s != strings.TrimSpace(s) {
		return false
	}
	first, _ := utf8First(s)
	if !unicode.IsLetter(first) && !unicode.IsDigit(first) {
		return false
	}
	if strings.ContainsAny(s, plainSafeSpecial) {
		return false
	}
	for _, r := range s {
		if !yamlPlainRune(r) {
			return false
		}
	}
	if plainBoolNull[s] || numberRe.MatchString(s) {
		return false
	}
	// A leading digit run with dashes reads as a timestamp.
	if len(s) >= 8 && unicode.IsDigit(first) && strings.Contains(s, "-") {
		return false
	}
	// Only a scalar starting with an ASCII digit can resolve to a number or a
	// timestamp once the checks above pass, so only those pay for a parse.
	if first >= '0' && first <= '9' && !resolvesToString(s) {
		return false
	}
	return true
}

// utf8First returns the first rune of s.
func utf8First(s string) (rune, int) {
	for i, r := range s {
		return r, i
	}
	return 0, 0
}

// asString coerces a frontmatter value to a string.
func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(t)
	}
}

// asStrings coerces a frontmatter value to a string slice. A single scalar
// becomes a one-element slice and a comma-separated string is split.
func asStrings(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, 0, len(t))
		for _, it := range t {
			if s := asString(it); s != "" {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []string:
		return t
	case string:
		var out []string
		for _, part := range strings.Split(t, ",") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		if s := asString(t); s != "" {
			return []string{s}
		}
		return nil
	}
}

// asInt coerces a frontmatter value to an int.
func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

// asBool coerces a frontmatter value to a bool.
func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(t))
		return err == nil && b
	case int:
		return t != 0
	default:
		return false
	}
}

// asTime parses a frontmatter timestamp. It accepts RFC 3339, "2006-01-02"
// and "2006-01-02 15:04:05".
func asTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t.UTC(), !t.IsZero()
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return time.Time{}, false
		}
		for _, layout := range timeLayouts {
			if parsed, err := time.Parse(layout, s); err == nil {
				return parsed.UTC(), true
			}
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

// leadingHeading returns the text of a markdown heading that opens the body.
func leadingHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		m := headingRe.FindStringSubmatch(trimmed)
		if m == nil {
			return ""
		}
		return strings.TrimSpace(m[1])
	}
	return ""
}
