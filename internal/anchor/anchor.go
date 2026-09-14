// Package anchor builds, finds and remaps comment anchors as docs/comments.md
// specifies. Every offset counts UTF-16 code units, the unit browsers and
// CodeMirror use, so the functions work on []uint16 and never on bytes or
// runes. tools/anchors.py is the reference implementation, and
// test/vectors/anchors.json holds the cases this package must reproduce.
package anchor

import (
	"unicode/utf16"

	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// Context is the number of code units kept on each side of the selection.
	Context = 32
	// MaxExact is the longest selected text the server accepts, in code units.
	MaxExact = 5000
)

// Body is a note body converted to UTF-16 code units once, so a note with many
// comments is not converted again for every anchor.
type Body struct {
	u []uint16
}

// NewBody converts a body to code units. Invalid UTF-8 bytes become U+FFFD,
// the same replacement the diff in Remap applies.
func NewBody(s string) Body {
	return Body{u: units(s)}
}

// Len returns the body length in code units.
func (b Body) Len() int { return len(b.u) }

// Slice returns the text between two code unit offsets. The offsets are
// clamped into the body.
func (b Body) Slice(start, end int) string {
	start = clamp(start, 0, len(b.u))
	end = clamp(end, start, len(b.u))
	return text(b.u[start:end])
}

// Make builds an anchor for the selection [start, end) of body. It reports
// false when the selection is empty after clamping.
func Make(body string, start, end int) (*core.CommentAnchor, bool) {
	return NewBody(body).Make(start, end)
}

// Make builds an anchor for the selection [start, end) of the body. It clamps
// both offsets, swaps them when reversed, widens a selection that splits a
// surrogate pair, and takes up to Context units of text on each side without
// splitting a pair. It reports false when the selection is empty.
func (b Body) Make(start, end int) (*core.CommentAnchor, bool) {
	n := len(b.u)
	start = clamp(start, 0, n)
	end = clamp(end, 0, n)
	if start > end {
		start, end = end, start
	}
	if splitsPair(b.u, start) {
		start--
	}
	if splitsPair(b.u, end) {
		end++
	}
	if start == end {
		return nil, false
	}
	ps := max(0, start-Context)
	if splitsPair(b.u, ps) {
		ps++
	}
	se := min(n, end+Context)
	if splitsPair(b.u, se) {
		se--
	}
	return &core.CommentAnchor{
		Exact:  text(b.u[start:end]),
		Prefix: text(b.u[ps:start]),
		Suffix: text(b.u[end:se]),
		Start:  start,
		End:    end,
	}, true
}

// Resolve finds an anchor in body and returns its offsets and state. An
// orphaned anchor returns -1 for both offsets.
func Resolve(body string, a core.CommentAnchor) (start, end int, state string) {
	return NewBody(body).Resolve(a)
}

// Resolve finds an anchor in the body. Every occurrence of the exact text,
// overlapping ones included, is scored by the length of the stored prefix
// found before it plus the length of the stored suffix found after it. The
// highest score wins, then the smallest distance to the stored start, then
// the earlier offset. The state is core.AnchorExact when the winner sits at
// the stored offsets, core.AnchorMoved when it sits elsewhere, and
// core.AnchorOrphaned with offsets of -1 when the text does not occur.
func (b Body) Resolve(a core.CommentAnchor) (start, end int, state string) {
	e := units(a.Exact)
	if len(e) == 0 || len(e) > len(b.u) {
		return -1, -1, core.AnchorOrphaned
	}
	p := units(a.Prefix)
	s := units(a.Suffix)
	// The distance |i - a.Start| orders occurrences the same way as the
	// distance to a.Start clamped into the body, and the clamp avoids an
	// integer overflow for extreme stored offsets.
	hint := clamp(a.Start, 0, len(b.u))

	best, bestScore, bestDist := -1, -1, 0
	fail := failure(e)
	k := 0
	for i, c := range b.u {
		for k > 0 && e[k] != c {
			k = fail[k-1]
		}
		if e[k] == c {
			k++
		}
		if k < len(e) {
			continue
		}
		at := i + 1 - len(e)
		k = fail[k-1]

		score := commonSuffix(p, b.u[max(0, at-len(p)):at]) +
			commonPrefix(s, b.u[at+len(e):min(len(b.u), at+len(e)+len(s))])
		dist := at - hint
		if dist < 0 {
			dist = -dist
		}
		if best < 0 || score > bestScore || (score == bestScore && dist < bestDist) {
			best, bestScore, bestDist = at, score, dist
		}
	}
	if best < 0 {
		return -1, -1, core.AnchorOrphaned
	}
	start, end = best, best+len(e)
	if best == a.Start && a.End == end {
		return start, end, core.AnchorExact
	}
	return start, end, core.AnchorMoved
}

// Normalize applies the server rules to an anchor sent by a client. The exact
// text must be 1 to MaxExact code units long and neither offset may be
// negative, otherwise it returns a core.ValidationError for the field
// "anchor". Prefix is cut to its last Context units and Suffix to its first
// Context units, never splitting a surrogate pair.
func Normalize(a core.CommentAnchor) (core.CommentAnchor, error) {
	n := len(units(a.Exact))
	switch {
	case n == 0:
		return a, core.Invalid("anchor", "the selected text is empty")
	case n > MaxExact:
		return a, core.Invalid("anchor", "the selected text is longer than 5000 characters")
	case a.Start < 0 || a.End < 0:
		return a, core.Invalid("anchor", "anchor offsets must not be negative")
	}
	if p := units(a.Prefix); len(p) > Context {
		cut := len(p) - Context
		if splitsPair(p, cut) {
			cut++
		}
		a.Prefix = text(p[cut:])
	}
	if s := units(a.Suffix); len(s) > Context {
		cut := Context
		if splitsPair(s, cut) {
			cut--
		}
		a.Suffix = text(s[:cut])
	}
	return a, nil
}

// units converts a string to UTF-16 code units. Each invalid UTF-8 byte
// becomes U+FFFD.
func units(s string) []uint16 {
	return utf16.Encode([]rune(s))
}

// text converts code units back to a string. A lone surrogate becomes U+FFFD.
func text(u []uint16) string {
	return string(utf16.Decode(u))
}

// splitsPair reports whether offset i falls between the two halves of a
// surrogate pair.
func splitsPair(u []uint16, i int) bool {
	return i > 0 && i < len(u) && isLow(u[i]) && isHigh(u[i-1])
}

// isHigh reports whether u is the first half of a surrogate pair.
func isHigh(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// isLow reports whether u is the second half of a surrogate pair.
func isLow(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// clamp limits v to [lo, hi].
func clamp(v, lo, hi int) int {
	return max(lo, min(v, hi))
}

// commonSuffix returns the length of the longest common suffix of a and b.
func commonSuffix(a, b []uint16) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

// commonPrefix returns the length of the longest common prefix of a and b.
func commonPrefix(a, b []uint16) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// failure returns the Knuth-Morris-Pratt failure table of a pattern: entry i
// is the length of the longest proper prefix of pattern[:i+1] that is also its
// suffix. Resolve uses it to find every occurrence, overlapping ones included,
// in linear time.
func failure(pattern []uint16) []int {
	f := make([]int, len(pattern))
	k := 0
	for i := 1; i < len(pattern); i++ {
		for k > 0 && pattern[i] != pattern[k] {
			k = f[k-1]
		}
		if pattern[i] == pattern[k] {
			k++
		}
		f[i] = k
	}
	return f
}
