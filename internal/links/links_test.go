package links

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

const vectorPath = "../../test/vectors/links.json"

// vectorFile mirrors test/vectors/links.json. Offsets are UTF-8 byte offsets.
// The browser test converts them to UTF-16 code unit offsets.
type vectorFile struct {
	Comment []string           `json:"comment"`
	Trees   map[string][]Entry `json:"trees"`
	Parse   []parseCase        `json:"parse"`
	Resolve []resolveCase      `json:"resolve"`
	Rewrite []rewriteCase      `json:"rewrite"`
}

type vecLink struct {
	Form        string `json:"form"`
	Embed       bool   `json:"embed,omitempty"`
	External    bool   `json:"external,omitempty"`
	Angle       bool   `json:"angle,omitempty"`
	Target      string `json:"target"`
	Heading     string `json:"heading,omitempty"`
	Label       string `json:"label,omitempty"`
	Raw         string `json:"raw"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	TargetStart int    `json:"targetStart"`
	TargetEnd   int    `json:"targetEnd"`
	DestStart   int    `json:"destStart"`
	DestEnd     int    `json:"destEnd"`
}

type parseCase struct {
	Name string    `json:"name"`
	Body string    `json:"body"`
	Want []vecLink `json:"want"`
}

type resolveCase struct {
	Name   string  `json:"name"`
	Tree   string  `json:"tree"`
	From   string  `json:"from"`
	Target string  `json:"target"`
	Form   string  `json:"form"`
	Want   *string `json:"want"`
}

type rewriteCase struct {
	Name    string   `json:"name"`
	Tree    string   `json:"tree"`
	Changes []Change `json:"changes"`
	NoteOld string   `json:"noteOld"`
	NoteNew string   `json:"noteNew"`
	Body    string   `json:"body"`
	Want    string   `json:"want"`
}

func loadVectors(tb testing.TB) *vectorFile {
	tb.Helper()
	raw, err := os.ReadFile(vectorPath)
	if err != nil {
		tb.Fatalf("read vectors: %v", err)
	}
	var v vectorFile
	if err := json.Unmarshal(raw, &v); err != nil {
		tb.Fatalf("decode vectors: %v", err)
	}
	return &v
}

func toVec(l Link) vecLink {
	return vecLink{
		Form: l.Form.String(), Embed: l.Embed, External: l.External, Angle: l.Angle,
		Target: l.Target, Heading: l.Heading, Label: l.Label, Raw: l.Raw,
		Start: l.Start, End: l.End, TargetStart: l.TargetStart, TargetEnd: l.TargetEnd,
		DestStart: l.DestStart, DestEnd: l.DestEnd,
	}
}

func parseVecs(body string) []vecLink {
	out := []vecLink{}
	for _, l := range Parse(body) {
		out = append(out, toVec(l))
	}
	return out
}

// TestVectors runs every case in test/vectors/links.json. With
// LINKS_VECTORS_UPDATE=parse it rewrites the parse expectations from this
// implementation instead, for review before committing them.
func TestVectors(t *testing.T) {
	v := loadVectors(t)
	if os.Getenv("LINKS_VECTORS_UPDATE") == "parse" {
		for i := range v.Parse {
			v.Parse[i].Want = parseVecs(v.Parse[i].Body)
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if n := len(v.Parse) + len(v.Resolve) + len(v.Rewrite); n < 60 || len(v.Parse) == 0 || len(v.Resolve) == 0 || len(v.Rewrite) == 0 {
		t.Fatalf("vectors: %d parse, %d resolve, %d rewrite", len(v.Parse), len(v.Resolve), len(v.Rewrite))
	}
	for _, c := range v.Parse {
		t.Run("parse/"+c.Name, func(t *testing.T) {
			got := parseVecs(c.Body)
			if len(got) != len(c.Want) {
				t.Fatalf("got %d links %+v, want %d %+v", len(got), got, len(c.Want), c.Want)
			}
			for i := range got {
				if got[i] != c.Want[i] {
					t.Errorf("link %d\n got %+v\nwant %+v", i, got[i], c.Want[i])
				}
			}
		})
	}
	snaps := map[string]*Snapshot{}
	snap := func(t *testing.T, name string) *Snapshot {
		entries, ok := v.Trees[name]
		if !ok {
			t.Fatalf("unknown tree %q", name)
		}
		if snaps[name] == nil {
			snaps[name] = NewSnapshot(entries)
		}
		return snaps[name]
	}
	for _, c := range v.Resolve {
		t.Run("resolve/"+c.Name, func(t *testing.T) {
			form, ok := ParseForm(c.Form)
			if !ok {
				t.Fatalf("unknown form %q", c.Form)
			}
			got := snap(t, c.Tree).ResolveTarget(c.From, c.Target, form)
			switch {
			case c.Want == nil && got != nil:
				t.Fatalf("got %q, want no match", got.Path)
			case c.Want != nil && got == nil:
				t.Fatalf("got no match, want %q", *c.Want)
			case c.Want != nil && got.Path != *c.Want:
				t.Fatalf("got %q, want %q", got.Path, *c.Want)
			}
		})
	}
	for _, c := range v.Rewrite {
		t.Run("rewrite/"+c.Name, func(t *testing.T) {
			before := snap(t, c.Tree)
			rw := NewRewriter(before, c.Changes)
			got := Apply(c.Body, rw.Rewrite(c.NoteOld, c.NoteNew, c.Body))
			if got != c.Want {
				t.Fatalf("got\n%s\nwant\n%s", got, c.Want)
			}
			if again := NewRewriter(rw.After(), nil).Rewrite(c.NoteNew, c.NoteNew, got); len(again) != 0 {
				t.Fatalf("second rewrite gave %+v", again)
			}
		})
	}
}
