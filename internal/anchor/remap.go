package anchor

import (
	"unicode/utf16"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/gamemaker1/markspace/internal/core"
)

// segment is one run of a diff measured in code units. An equal run has
// del == ins. A change run holds every deletion and insertion between two
// equal runs, so a replacement is one segment.
type segment struct {
	equal    bool
	old, new int // offsets of the run in the old and the new body
	del, ins int // units taken from the old body and added to the new one
}

// Edit is the diff from one body to another, computed once and applied to any
// number of anchors.
type Edit struct {
	old, new Body
	same     bool
	segs     []segment
}

// Remap moves an anchor through an edit from oldBody to newBody. It reports
// whether the anchor changed. Callers remapping many anchors over the same
// edit should use NewEdit once and call Edit.Remap for each anchor.
func Remap(oldBody, newBody string, a core.CommentAnchor) (core.CommentAnchor, bool) {
	return NewEdit(oldBody, newBody).Remap(a)
}

// NewEdit diffs oldBody against newBody. The diff runs line by line first and
// then character by character inside the changed lines, or character by
// character when the texts are short, and every run length is converted to
// UTF-16 code units.
func NewEdit(oldBody, newBody string) *Edit {
	e := &Edit{old: NewBody(oldBody), new: NewBody(newBody), same: oldBody == newBody}
	if e.same {
		return e
	}
	dmp := diffmatchpatch.New()
	oldRunes, newRunes := []rune(oldBody), []rune(newBody)
	diffs := dmp.DiffMainRunes(oldRunes, newRunes, true)
	// Semantic cleanup turns a retyped word into one replacement instead of
	// scattered single characters, which keeps anchor boundaries on words.
	diffs = dmp.DiffCleanupSemantic(diffs)
	if segs, ok := segments(diffs, e.old.u, e.new.u); ok {
		e.segs = segs
	} else {
		e.segs = fallbackSegments(e.old.u, e.new.u)
	}
	return e
}

// segments converts a diff to code unit runs and checks that the runs rebuild
// both bodies exactly. It reports false when they do not.
func segments(diffs []diffmatchpatch.Diff, oldU, newU []uint16) ([]segment, bool) {
	var (
		out      []segment
		op, np   int
		pending  segment
		changing bool
	)
	flush := func() {
		if changing {
			out = append(out, pending)
			changing = false
		}
	}
	for _, d := range diffs {
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			n, ok := consume(d.Text, oldU, op)
			if !ok {
				return nil, false
			}
			if m, ok := consume(d.Text, newU, np); !ok || m != n {
				return nil, false
			}
			if n == 0 {
				continue
			}
			flush()
			out = append(out, segment{equal: true, old: op, new: np, del: n, ins: n})
			op += n
			np += n
		case diffmatchpatch.DiffDelete:
			n, ok := consume(d.Text, oldU, op)
			if !ok {
				return nil, false
			}
			if n == 0 {
				continue
			}
			if !changing {
				pending = segment{old: op, new: np}
				changing = true
			}
			pending.del += n
			op += n
		case diffmatchpatch.DiffInsert:
			n, ok := consume(d.Text, newU, np)
			if !ok {
				return nil, false
			}
			if n == 0 {
				continue
			}
			if !changing {
				pending = segment{old: op, new: np}
				changing = true
			}
			pending.ins += n
			np += n
		}
	}
	flush()
	return out, op == len(oldU) && np == len(newU)
}

// fallbackSegments describes an edit as one change between the common prefix
// and the common suffix of the two bodies.
func fallbackSegments(oldU, newU []uint16) []segment {
	pre := commonPrefix(oldU, newU)
	suf := commonSuffix(oldU[pre:], newU[pre:])
	var out []segment
	if pre > 0 {
		out = append(out, segment{equal: true, del: pre, ins: pre})
	}
	if d, i := len(oldU)-pre-suf, len(newU)-pre-suf; d > 0 || i > 0 {
		out = append(out, segment{old: pre, new: pre, del: d, ins: i})
	}
	if suf > 0 {
		out = append(out, segment{equal: true, old: len(oldU) - suf, new: len(newU) - suf, del: suf, ins: suf})
	}
	return out
}

// consume matches the code units of text against want starting at pos. It
// returns the number of units matched and false when text does not match.
func consume(s string, want []uint16, pos int) (int, bool) {
	i := pos
	for _, r := range s {
		if r >= 0x10000 {
			hi, lo := utf16.EncodeRune(r)
			if i+1 >= len(want) || want[i] != uint16(hi) || want[i+1] != uint16(lo) {
				return 0, false
			}
			i += 2
			continue
		}
		if i >= len(want) || want[i] != uint16(r) {
			return 0, false
		}
		i++
	}
	return i - pos, true
}

// Remap moves an anchor through the edit, following the steps in docs/comments.md
// "Following edits":
//
//  1. The anchor is resolved against the old body. An orphaned anchor is
//     returned unchanged.
//  2. Both offsets are mapped through the diff.
//  3. Text inserted exactly at the start goes before the passage and text
//     inserted exactly at the end goes after it. Text inserted strictly inside
//     joins the passage.
//  4. An offset inside deleted text moves to where the deletion was. When the
//     deletion was replaced by new text, the start moves to the beginning of
//     that text and the end to its end, so an edited word at either edge stays
//     in the passage. A passage lying entirely inside deleted text maps to an
//     empty passage.
//  5. An empty mapped passage returns the anchor unchanged, which then
//     resolves as orphaned. Otherwise the exact text, prefix and suffix are
//     rebuilt from the new body.
//
// The boolean reports whether the returned anchor differs from a.
func (e *Edit) Remap(a core.CommentAnchor) (core.CommentAnchor, bool) {
	if e.same {
		return a, false
	}
	start, end, state := e.old.Resolve(a)
	if state == core.AnchorOrphaned {
		return a, false
	}
	ns, startIn := e.mapOffset(start, false)
	ne, endIn := e.mapOffset(end, true)
	if ns >= ne || (startIn >= 0 && startIn == endIn) {
		return a, false
	}
	next, ok := e.new.Make(ns, ne)
	if !ok {
		return a, false
	}
	return *next, *next != a
}

// mapOffset maps an old body offset to the new body. end selects the rules
// for the end of a passage instead of its start. The second result is the
// index of the change segment whose deleted text holds the offset, counting
// the start of the deletion for a start offset and its end for an end
// offset, or -1.
func (e *Edit) mapOffset(x int, end bool) (int, int) {
	for i, g := range e.segs {
		switch {
		case g.equal:
			if x < g.old+g.del {
				return g.new + x - g.old, -1
			}
		case g.del == 0:
			// A pure insertion at x: outside the passage on both sides.
			if x == g.old {
				if end {
					return g.new, -1
				}
				return g.new + g.ins, -1
			}
		case x > g.old+g.del:
			// The deletion lies wholly before x.
		case !end:
			if x < g.old+g.del {
				return g.new, i
			}
		case x == g.old:
			// The passage ends where the deletion begins.
			return g.new, -1
		default:
			return g.new + g.ins, i
		}
	}
	return len(e.new.u), -1
}
