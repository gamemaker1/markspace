package anchor

import (
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// fuzzBodies are seed bodies shared by the fuzz targets.
var fuzzBodies = []string{
	"",
	"sync",
	"# Plan\n\nShip the **sync** engine.\n\nThen ship the sync engine docs.\n",
	"x\U0001F600 sync \U0001F600 sync",
	"aaaa",
	"café 你好 world",
	"line one\r\nline two\r\n",
	"\xff\xfe invalid \xed\xa0\x80 bytes",
}

// FuzzResolve checks that Resolve never panics, and that a found anchor lies
// inside the body and covers exactly the anchor text.
func FuzzResolve(f *testing.F) {
	for i, b := range fuzzBodies {
		f.Add(b, "sync", "the ", " engine", i, i+4)
	}
	f.Add("aaaa", "aa", "a", "a", 0, 2)
	f.Add("tiny sync", "sync", "", "", 500, 504)
	f.Add("ab", "b", "", "", -9223372036854775808, 9223372036854775807)
	f.Fuzz(func(t *testing.T, body, exact, prefix, suffix string, start, end int) {
		a := core.CommentAnchor{Exact: exact, Prefix: prefix, Suffix: suffix, Start: start, End: end}
		b := NewBody(body)
		s, e, state := b.Resolve(a)
		if state == core.AnchorOrphaned {
			if s != -1 || e != -1 {
				t.Fatalf("orphaned with offsets %d, %d", s, e)
			}
			return
		}
		if state != core.AnchorExact && state != core.AnchorMoved {
			t.Fatalf("state %q", state)
		}
		if s < 0 || e > b.Len() || s >= e {
			t.Fatalf("offsets %d, %d outside body of %d units", s, e, b.Len())
		}
		if got, want := b.Slice(s, e), text(units(exact)); got != want {
			t.Fatalf("text at %d:%d is %q, want %q", s, e, got, want)
		}
		if state == core.AnchorExact && (s != start || e != end) {
			t.Fatalf("exact at %d:%d but stored %d:%d", s, e, start, end)
		}
	})
}

// FuzzMakeResolve checks that an anchor made on a body resolves exact at the
// same offsets on that body.
func FuzzMakeResolve(f *testing.F) {
	for _, b := range fuzzBodies {
		f.Add(b, 0, 4)
		f.Add(b, 2, 3)
	}
	f.Add("\U0001F600\U0001F600\U0001F600HIT", 7, 1)
	f.Fuzz(func(t *testing.T, body string, start, end int) {
		b := NewBody(body)
		a, ok := b.Make(start, end)
		if !ok {
			return
		}
		if a.Start < 0 || a.End > b.Len() || a.Start >= a.End {
			t.Fatalf("made offsets %d, %d outside body of %d units", a.Start, a.End, b.Len())
		}
		if len(units(a.Prefix)) > Context || len(units(a.Suffix)) > Context {
			t.Fatalf("context longer than %d units: %+v", Context, a)
		}
		if n, err := Normalize(*a); err == nil && n != *a && len(units(a.Exact)) <= MaxExact {
			t.Fatalf("Normalize changed a made anchor: %+v to %+v", *a, n)
		}
		s, e, state := b.Resolve(*a)
		if state != core.AnchorExact || s != a.Start || e != a.End {
			t.Fatalf("Resolve(Make) = %d, %d, %s, want %d, %d, exact", s, e, state, a.Start, a.End)
		}
	})
}

// FuzzRemap checks that Remap never panics, leaves an anchor alone when the
// body did not change, and that a changed anchor lies inside the new body and
// quotes the new text at its offsets.
func FuzzRemap(f *testing.F) {
	for i, b := range fuzzBodies {
		f.Add(b, "Intro\n"+b, "", "", "", i, i+4)
		f.Add(b, b+" tail", "sync", "", "", 0, 0)
	}
	f.Add("The sync engine runs.", "The Sync motor runs.", "", "", "", 4, 15)
	f.Add("a\r\nb\r\nc", "a\nb\nc", "b", "", "", 3, 4)
	f.Fuzz(func(t *testing.T, oldBody, newBody, exact, prefix, suffix string, start, end int) {
		a := core.CommentAnchor{Exact: exact, Prefix: prefix, Suffix: suffix, Start: start, End: end}
		if exact == "" {
			made, ok := Make(oldBody, start, end)
			if !ok {
				return
			}
			a = *made
		}
		if same, changed := Remap(oldBody, oldBody, a); changed || same != a {
			t.Fatalf("identical body changed the anchor: %+v to %+v", a, same)
		}
		got, changed := Remap(oldBody, newBody, a)
		if !changed {
			if got != a {
				t.Fatalf("unchanged result %+v differs from %+v", got, a)
			}
			return
		}
		nb := NewBody(newBody)
		if got.Start < 0 || got.End > nb.Len() || got.Start >= got.End {
			t.Fatalf("remapped offsets %d, %d outside body of %d units", got.Start, got.End, nb.Len())
		}
		if s := nb.Slice(got.Start, got.End); s != got.Exact {
			t.Fatalf("exact %q, new text at %d:%d is %q", got.Exact, got.Start, got.End, s)
		}
		if s, e, state := nb.Resolve(got); state != core.AnchorExact || s != got.Start || e != got.End {
			t.Fatalf("remapped anchor resolves %d, %d, %s", s, e, state)
		}
	})
}
