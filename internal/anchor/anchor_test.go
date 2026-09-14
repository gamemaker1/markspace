package anchor

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// referenceCases mirrors test/vectors/anchors.json, which
// tools/anchors.py generates.
type referenceCases struct {
	Context int `json:"context"`
	Make    []struct {
		Name  string              `json:"name"`
		Body  string              `json:"body"`
		Start int                 `json:"start"`
		End   int                 `json:"end"`
		Want  *core.CommentAnchor `json:"want"`
	} `json:"make"`
	Resolve []struct {
		Name   string             `json:"name"`
		Body   string             `json:"body"`
		Anchor core.CommentAnchor `json:"anchor"`
		Want   struct {
			Start int    `json:"start"`
			End   int    `json:"end"`
			State string `json:"state"`
		} `json:"want"`
	} `json:"resolve"`
}

// TestReferenceCases checks every make and resolve case of the shared file
// field by field.
func TestReferenceCases(t *testing.T) {
	raw, err := os.ReadFile("../../test/vectors/anchors.json")
	if err != nil {
		t.Fatalf("read cases: %v", err)
	}
	var cases referenceCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("decode cases: %v", err)
	}
	if cases.Context != Context {
		t.Fatalf("context %d, want %d", cases.Context, Context)
	}
	if len(cases.Make) == 0 || len(cases.Resolve) == 0 {
		t.Fatal("the case file has no make or resolve cases")
	}
	for _, c := range cases.Make {
		t.Run("make/"+c.Name, func(t *testing.T) {
			got, ok := Make(c.Body, c.Start, c.End)
			if c.Want == nil {
				if ok || got != nil {
					t.Fatalf("Make = %+v, %v, want no anchor", got, ok)
				}
				return
			}
			if !ok || got == nil {
				t.Fatalf("Make returned no anchor, want %+v", *c.Want)
			}
			checkField(t, "exact", got.Exact, c.Want.Exact)
			checkField(t, "prefix", got.Prefix, c.Want.Prefix)
			checkField(t, "suffix", got.Suffix, c.Want.Suffix)
			checkField(t, "start", got.Start, c.Want.Start)
			checkField(t, "end", got.End, c.Want.End)
		})
	}
	for _, c := range cases.Resolve {
		t.Run("resolve/"+c.Name, func(t *testing.T) {
			start, end, state := Resolve(c.Body, c.Anchor)
			checkField(t, "start", start, c.Want.Start)
			checkField(t, "end", end, c.Want.End)
			checkField(t, "state", state, c.Want.State)
		})
	}
}

func checkField[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %#v, want %#v", name, got, want)
	}
}

// TestNormalize covers the server rules for a client anchor.
func TestNormalize(t *testing.T) {
	emoji := "\U0001F600"
	tests := []struct {
		name    string
		in      core.CommentAnchor
		want    core.CommentAnchor
		invalid bool
	}{
		{name: "empty exact", in: core.CommentAnchor{Exact: ""}, invalid: true},
		{name: "exact at the limit", in: core.CommentAnchor{Exact: strings.Repeat("a", MaxExact)},
			want: core.CommentAnchor{Exact: strings.Repeat("a", MaxExact)}},
		{name: "exact over the limit", in: core.CommentAnchor{Exact: strings.Repeat("a", MaxExact+1)}, invalid: true},
		{name: "emoji count two units toward the limit", in: core.CommentAnchor{Exact: strings.Repeat(emoji, MaxExact/2)},
			want: core.CommentAnchor{Exact: strings.Repeat(emoji, MaxExact/2)}},
		{name: "emoji over the limit", in: core.CommentAnchor{Exact: strings.Repeat(emoji, MaxExact/2) + "a"}, invalid: true},
		{name: "negative start", in: core.CommentAnchor{Exact: "x", Start: -1, End: 1}, invalid: true},
		{name: "negative end", in: core.CommentAnchor{Exact: "x", Start: 0, End: -1}, invalid: true},
		{name: "short context kept", in: core.CommentAnchor{Exact: "x", Prefix: "ab", Suffix: "cd", Start: 2, End: 3},
			want: core.CommentAnchor{Exact: "x", Prefix: "ab", Suffix: "cd", Start: 2, End: 3}},
		{name: "long prefix keeps its last 32 units",
			in:   core.CommentAnchor{Exact: "x", Prefix: strings.Repeat("p", 8) + strings.Repeat("q", 32)},
			want: core.CommentAnchor{Exact: "x", Prefix: strings.Repeat("q", 32)}},
		{name: "long suffix keeps its first 32 units",
			in:   core.CommentAnchor{Exact: "x", Suffix: strings.Repeat("s", 32) + strings.Repeat("t", 8)},
			want: core.CommentAnchor{Exact: "x", Suffix: strings.Repeat("s", 32)}},
		{name: "prefix cut never splits a pair",
			in:   core.CommentAnchor{Exact: "x", Prefix: strings.Repeat(emoji, 16) + "a"},
			want: core.CommentAnchor{Exact: "x", Prefix: strings.Repeat(emoji, 15) + "a"}},
		{name: "suffix cut never splits a pair",
			in:   core.CommentAnchor{Exact: "x", Suffix: "a" + strings.Repeat(emoji, 16)},
			want: core.CommentAnchor{Exact: "x", Suffix: "a" + strings.Repeat(emoji, 15)}},
		{name: "context of exactly 32 units kept",
			in:   core.CommentAnchor{Exact: "x", Prefix: strings.Repeat(emoji, 16), Suffix: strings.Repeat(emoji, 16)},
			want: core.CommentAnchor{Exact: "x", Prefix: strings.Repeat(emoji, 16), Suffix: strings.Repeat(emoji, 16)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.in)
			if tt.invalid {
				var ve *core.ValidationError
				if !errors.Is(err, core.ErrInvalid) || !errors.As(err, &ve) || ve.Field != "anchor" {
					t.Fatalf("Normalize error = %v, want a validation error on anchor", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Normalize = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestBodySlice checks that Slice clamps its offsets.
func TestBodySlice(t *testing.T) {
	b := NewBody("x\U0001F600y")
	if b.Len() != 4 {
		t.Fatalf("Len = %d, want 4", b.Len())
	}
	if got := b.Slice(1, 3); got != "\U0001F600" {
		t.Fatalf("Slice(1, 3) = %q", got)
	}
	if got := b.Slice(-5, 99); got != "x\U0001F600y" {
		t.Fatalf("Slice(-5, 99) = %q", got)
	}
	if got := b.Slice(3, 1); got != "" {
		t.Fatalf("Slice(3, 1) = %q, want empty", got)
	}
}

// TestResolveLongPattern checks a pattern whose partial matches overlap, the
// case the failure table exists for.
func TestResolveLongPattern(t *testing.T) {
	body := strings.Repeat("ab", 50) + "abc" + strings.Repeat("ab", 50)
	start, end, state := Resolve(body, core.CommentAnchor{Exact: "ababc", Start: 98, End: 103})
	if start != 98 || end != 103 || state != core.AnchorExact {
		t.Fatalf("Resolve = %d, %d, %s, want 98, 103, exact", start, end, state)
	}
	start, _, state = Resolve("aaaaa", core.CommentAnchor{Exact: "aaa", Start: 2, End: 5})
	if start != 2 || state != core.AnchorExact {
		t.Fatalf("overlapping Resolve = %d, %s, want 2, exact", start, state)
	}
}
