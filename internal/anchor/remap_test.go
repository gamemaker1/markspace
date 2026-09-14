package anchor

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/gamemaker1/markspace/internal/core"
)

// index16 returns the UTF-16 offset of the nth occurrence of sub in s.
func index16(t *testing.T, s, sub string, nth int) int {
	t.Helper()
	b, p := units(s), units(sub)
	seen := -1
	for i := 0; i+len(p) <= len(b); i++ {
		if string(utf16.Decode(b[i:i+len(p)])) == sub {
			seen++
			if seen == nth {
				return i
			}
		}
	}
	t.Fatalf("%q occurrence %d not in %q", sub, nth, s)
	return -1
}

// anchorOn builds the anchor a browser would send for the nth occurrence of
// sub in body.
func anchorOn(t *testing.T, body, sub string, nth int) core.CommentAnchor {
	t.Helper()
	i := index16(t, body, sub, nth)
	a, ok := Make(body, i, i+len(units(sub)))
	if !ok {
		t.Fatalf("no anchor for %q", sub)
	}
	return *a
}

// TestRemap walks the boundary rules of docs/comments.md "Following edits".
func TestRemap(t *testing.T) {
	const base = "Intro.\nThe sync engine runs.\nEnd.\n"
	emoji := "\U0001F600"

	var longOld, longNew strings.Builder
	for i := range 40 {
		fmt.Fprintf(&longOld, "Line %d says hello to line %d.\n", i, i+1)
		switch {
		case i == 20:
			longOld.WriteString("The sync engine runs here.\n")
			longNew.WriteString(fmt.Sprintf("Line %d says hello to line %d.\n", i, i+1))
			longNew.WriteString("The sync engine runs here.\n")
		case i%3 == 0:
			fmt.Fprintf(&longNew, "Line %d waves at line %d!\n", i, i+1)
		case i%7 == 0:
			// Line deleted.
		case i%5 == 0:
			fmt.Fprintf(&longNew, "Line %d says hello to line %d.\nAn added line.\n", i, i+1)
		default:
			fmt.Fprintf(&longNew, "Line %d says hello to line %d.\n", i, i+1)
		}
	}

	tests := []struct {
		name       string
		old, new   string
		passage    string // selected in old
		nth        int
		want       string // expected passage in new, "" for unchanged
		wantNth    int
		wantOrphan bool
	}{
		{name: "insertion before", old: base, new: "Hello.\n" + base, passage: "sync engine", want: "sync engine"},
		{name: "insertion at start stays outside", old: base,
			new: "Intro.\nThe Xsync engine runs.\nEnd.\n", passage: "sync engine", want: "sync engine"},
		{name: "insertion at end stays outside", old: base,
			new: "Intro.\nThe sync engineX runs.\nEnd.\n", passage: "sync engine", want: "sync engine"},
		{name: "insertion inside joins", old: base,
			new: "Intro.\nThe sync big engine runs.\nEnd.\n", passage: "sync engine", want: "sync big engine"},
		{name: "deletion before", old: base, new: "The sync engine runs.\nEnd.\n", passage: "sync engine", want: "sync engine"},
		{name: "deletion after", old: base, new: "Intro.\nThe sync engine\n", passage: "sync engine", want: "sync engine"},
		{name: "deletion covering the passage keeps the anchor", old: base,
			new: "Intro.\nThe runs.\nEnd.\n", passage: "sync engine", wantOrphan: true},
		{name: "deletion of the passage and its context", old: base,
			new: "End.\n", passage: "sync engine", wantOrphan: true},
		{name: "deletion inside shrinks", old: base,
			new: "Intro.\nThe syncengine runs.\nEnd.\n", passage: "sync engine", want: "syncengine"},
		{name: "deletion across the start", old: base,
			new: "Intro.\nThe ngine runs.\nEnd.\n", passage: "sync engine", want: "ngine"},
		{name: "deletion across the end", old: base,
			new: "Intro.\nThe sync e.\nEnd.\n", passage: "sync engine", want: "sync e"},
		{name: "word replaced inside", old: base,
			new: "Intro.\nThe sync motor runs.\nEnd.\n", passage: "The sync engine runs", want: "The sync motor runs"},
		{name: "word replaced at the start", old: base,
			new: "Intro.\nThe Sync engine runs.\nEnd.\n", passage: "sync engine", want: "Sync engine"},
		{name: "word replaced at the end", old: base,
			new: "Intro.\nThe sync motor runs.\nEnd.\n", passage: "sync engine", want: "sync motor"},
		{name: "passage replaced whole is orphaned", old: base,
			new: "Intro.\nThe sync motor runs.\nEnd.\n", passage: "engine", wantOrphan: true},
		{name: "emoji before shifts two units", old: base, new: emoji + base, passage: "sync engine", want: "sync engine"},
		{name: "emoji inside joins", old: base,
			new: "Intro.\nThe sync " + emoji + "engine runs.\nEnd.\n", passage: "sync engine", want: "sync " + emoji + "engine"},
		{name: "emoji in the old body before the passage", old: emoji + emoji + base,
			new: emoji + "Hi " + emoji + base, passage: "sync engine", want: "sync engine"},
		{name: "CRLF line inserted before",
			old: "Intro.\r\nThe sync engine runs.\r\nEnd.\r\n", new: "New line.\r\nIntro.\r\nThe sync engine runs.\r\nEnd.\r\n",
			passage: "sync engine", want: "sync engine"},
		{name: "CRLF line endings converted",
			old: "Intro.\r\nThe sync engine runs.\r\nEnd.\r\n", new: "Intro.\nThe sync engine runs.\nEnd.\n",
			passage: "sync engine", want: "sync engine"},
		{name: "many unrelated edits", old: longOld.String(), new: longNew.String(), passage: "sync engine", want: "sync engine"},
		{name: "repeated text keeps its copy", old: "x sync y sync z\n", new: "sync x sync y sync z\n",
			passage: "sync", nth: 1, want: "sync", wantNth: 2},
		{name: "identical bodies", old: base, new: base, passage: "sync engine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := anchorOn(t, tt.old, tt.passage, tt.nth)
			got, changed := Remap(tt.old, tt.new, a)
			if tt.want == "" {
				if changed || got != a {
					t.Fatalf("Remap = %+v, %v, want the anchor unchanged", got, changed)
				}
				if tt.wantOrphan {
					if _, _, state := Resolve(tt.new, got); state != core.AnchorOrphaned {
						t.Fatalf("unchanged anchor resolves %s in the new body, want orphaned", state)
					}
				}
				return
			}
			want := anchorOn(t, tt.new, tt.want, tt.wantNth)
			if got != want {
				t.Fatalf("Remap = %+v\nwant   %+v", got, want)
			}
			if changed != (want != a) {
				t.Fatalf("changed = %v, want %v", changed, want != a)
			}
			if _, _, state := Resolve(tt.new, got); state != core.AnchorExact {
				t.Fatalf("remapped anchor resolves %s, want exact", state)
			}
		})
	}
}

// TestRemapStaleOffsets checks that an anchor whose stored offsets are stale
// in the old body is resolved first and then mapped.
func TestRemapStaleOffsets(t *testing.T) {
	old := "Intro.\nThe sync engine runs.\n"
	a := core.CommentAnchor{Exact: "sync engine", Start: 0, End: 11}
	got, changed := Remap(old, "Hello.\n"+old, a)
	if !changed || got.Start != 18 || got.End != 29 || got.Exact != "sync engine" || got.Prefix == "" {
		t.Fatalf("Remap = %+v, %v, want sync engine at 18", got, changed)
	}
}

// TestRemapOrphanedInOldBody leaves an anchor alone when its text is already
// gone.
func TestRemapOrphanedInOldBody(t *testing.T) {
	a := core.CommentAnchor{Exact: "gone", Start: 3, End: 7}
	got, changed := Remap("abc def", "abc def ghi", a)
	if changed || got != a {
		t.Fatalf("Remap = %+v, %v, want the anchor unchanged", got, changed)
	}
}

// TestEditReuse checks that one edit maps several anchors like Remap does.
func TestEditReuse(t *testing.T) {
	old := "alpha beta gamma delta\n"
	next := "zero alpha beta GAMMA delta epsilon\n"
	e := NewEdit(old, next)
	for _, word := range []string{"alpha", "beta", "delta", "alpha beta"} {
		a := anchorOn(t, old, word, 0)
		viaEdit, c1 := e.Remap(a)
		direct, c2 := Remap(old, next, a)
		if viaEdit != direct || c1 != c2 {
			t.Fatalf("%s: Edit.Remap = %+v, %v; Remap = %+v, %v", word, viaEdit, c1, direct, c2)
		}
		if viaEdit.Exact != word {
			t.Fatalf("%s: exact %q", word, viaEdit.Exact)
		}
	}
}

// TestFallbackSegments checks the prefix and suffix diff used when a library
// diff does not rebuild the bodies.
func TestFallbackSegments(t *testing.T) {
	old := "The sync engine runs."
	next := "The sync big engine runs."
	e := &Edit{old: NewBody(old), new: NewBody(next)}
	e.segs = fallbackSegments(e.old.u, e.new.u)
	got, changed := e.Remap(anchorOn(t, old, "sync engine", 0))
	if !changed || got.Exact != "sync big engine" {
		t.Fatalf("fallback Remap = %+v, %v", got, changed)
	}
	got, _ = e.Remap(anchorOn(t, old, "runs", 0))
	if got.Exact != "runs" || got.Start != index16(t, next, "runs", 0) {
		t.Fatalf("fallback Remap after the change = %+v", got)
	}
	if segs := fallbackSegments(units("same"), units("same")); len(segs) != 1 || !segs[0].equal {
		t.Fatalf("fallback of identical bodies = %+v", segs)
	}
}

// TestSegmentsRejectMismatch checks that a diff that does not rebuild the
// bodies is refused.
func TestSegmentsRejectMismatch(t *testing.T) {
	e := NewEdit("abc", "abd")
	if len(e.segs) == 0 {
		t.Fatal("no segments")
	}
	if _, ok := segments(nil, units("abc"), units("abd")); ok {
		t.Fatal("an empty diff should not rebuild two different bodies")
	}
}
