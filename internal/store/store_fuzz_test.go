package store

import (
	"io/fs"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// fuzzModTime is the file modification time handed to parseNote.
var fuzzModTime = time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)

// FuzzFrontmatterRoundTrip checks that parsing never panics, and that a note
// which parses survives serializeNote followed by another parse unchanged.
func FuzzFrontmatterRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"",
		"no frontmatter\n",
		"---\nid: abc\ntitle: Getting started\ntags: [a, b]\nposition: 100\n---\nbody\n",
		"---\ntitle: \"quoted: yes\"\ncreated: 2026-09-13T09:00:00Z\nupdated: 2026-09-13\n---\n",
		"---\ndeleted: true\ndeleted_at: 2026-01-01 10:00:00\ndeleted_by: u1\ntrashed_from: a/b.md\n---\n",
		"---\ncustom: {nested: [1, 2.5, true, null]}\nlist:\n  - x\n  - y\n---\nbody\n---\nmore\n---\n",
		"---\nid: x\n---\n---\n...\n---\n",
		"---\ntitle: yes\ntags: \"a, b ,c\"\n---\n",
		"# Heading title\n\ntext\n",
		"---\nanchor: &a [1, 2]\nref: *a\n---\n",
		"---\nx: 0x1F\ny: 1.0\nz: .nan\n---\n",
		"---\r\ntitle: crlf\r\n---\r\nbody\r\n",
		"---\n- not a mapping\n---\n",
		"# \xc2\x85 NEL heading\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		first, _, err := parseNote(data, "dir/note.md", fuzzModTime)
		if err != nil {
			return
		}
		out, err := serializeNote(first)
		if err != nil {
			t.Fatalf("serializeNote of a parsed note: %v\ninput: %q", err, data)
		}
		second, _, err := parseNote(out, "dir/note.md", fuzzModTime)
		if err != nil {
			t.Fatalf("reparse of serialized note: %v\ninput: %q\nserialized: %q", err, data, out)
		}
		if diff := noteDiff(first, second); diff != "" {
			t.Fatalf("round trip changed %s\ninput: %q\nserialized: %q", diff, data, out)
		}
	})
}

// TestFrontmatterBodyWithDelimiters keeps a body made of "---" lines intact.
func TestFrontmatterBodyWithDelimiters(t *testing.T) {
	note := &core.Note{ID: "n1", Title: "T", Content: "---\n---\nx: 1\n...\n---"}
	out, err := serializeNote(note)
	if err != nil {
		t.Fatalf("serializeNote: %v", err)
	}
	back, _, err := parseNote(out, "t.md", fuzzModTime)
	if err != nil {
		t.Fatalf("parseNote: %v", err)
	}
	if back.Content != note.Content {
		t.Fatalf("body = %q, want %q", back.Content, note.Content)
	}
}

// noteDiff names the first field that differs between two parsed notes.
func noteDiff(a, b *core.Note) string {
	switch {
	case a.ID != b.ID:
		return "id " + q(a.ID) + " -> " + q(b.ID)
	case a.Title != b.Title:
		return "title " + q(a.Title) + " -> " + q(b.Title)
	case a.Icon != b.Icon:
		return "icon " + q(a.Icon) + " -> " + q(b.Icon)
	case strings.Join(a.Tags, "\x00") != strings.Join(b.Tags, "\x00"):
		return "tags " + q(strings.Join(a.Tags, "|")) + " -> " + q(strings.Join(b.Tags, "|"))
	case a.ParentID != b.ParentID:
		return "parent " + q(a.ParentID) + " -> " + q(b.ParentID)
	case a.Position != b.Position:
		return "position"
	case !a.CreatedAt.Truncate(time.Second).Equal(b.CreatedAt.Truncate(time.Second)):
		return "created " + a.CreatedAt.String() + " -> " + b.CreatedAt.String()
	case !a.UpdatedAt.Truncate(time.Second).Equal(b.UpdatedAt.Truncate(time.Second)):
		return "updated " + a.UpdatedAt.String() + " -> " + b.UpdatedAt.String()
	case a.CreatedBy != b.CreatedBy:
		return "author"
	case a.UpdatedBy != b.UpdatedBy:
		return "updated_by"
	case a.Deleted != b.Deleted:
		return "deleted"
	case !sameTimePtr(a.DeletedAt, b.DeletedAt):
		return "deleted_at"
	case a.DeletedBy != b.DeletedBy:
		return "deleted_by"
	case a.Content != b.Content:
		return "body " + q(a.Content) + " -> " + q(b.Content)
	}
	if asString(a.Extra[keyTrashedFrom]) != asString(b.Extra[keyTrashedFrom]) {
		return "trashed_from"
	}
	ka, kb := extraKeys(a.Extra), extraKeys(b.Extra)
	if strings.Join(ka, "\x00") != strings.Join(kb, "\x00") {
		return "extra keys " + q(strings.Join(ka, "|")) + " -> " + q(strings.Join(kb, "|"))
	}
	for _, k := range ka {
		if !sameValue(a.Extra[k], b.Extra[k]) {
			return "extra value of " + q(k)
		}
	}
	return ""
}

func q(s string) string { return "\"" + s + "\"" }

// extraKeys lists the Extra keys other than trashed_from, sorted.
func extraKeys(m map[string]any) []string {
	var out []string
	for k := range m {
		if k != keyTrashedFrom {
			out = append(out, k)
		}
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func sameTimePtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Truncate(time.Second).Equal(b.Truncate(time.Second))
}

// sameValue compares decoded frontmatter values. An int and a float with the
// same numeric value are equal, and so are two NaNs, because YAML writes a
// whole float without its fraction.
func sameValue(a, b any) bool {
	if fa, ok := number(a); ok {
		fb, ok := number(b)
		return ok && (fa == fb || (math.IsNaN(fa) && math.IsNaN(fb)))
	}
	switch av := a.(type) {
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !sameValue(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			w, ok := bv[k]
			if !ok || !sameValue(v, w) {
				return false
			}
		}
		return true
	}
	return a == b
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// FuzzValidRelPath checks that an accepted note path stays inside the space
// directory, avoids dot directories and ends in .md.
func FuzzValidRelPath(f *testing.F) {
	for _, seed := range []string{
		"", "a.md", "a/b.md", ".md", "a/.md", "../a.md", "a/../b.md", "/a.md",
		".trash/x/a.md", "a\\b.md", "a\x00.md", " a.md", "a/ b.md", "a//b.md",
		"a.md/b.md", "./a.md", "a/./b.md", "..md", "...md", "a/..../b.md", "a.MD",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, p string) {
		if validRelPath(p) != nil {
			return
		}
		root := t.TempDir()
		joined := filepath.Clean(filepath.Join(root, filepath.FromSlash(p)))
		if !withinDir(root, joined) || joined == root {
			t.Fatalf("accepted path %q leaves the space dir: %q", p, joined)
		}
		rel, _ := filepath.Rel(root, joined)
		for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
			if strings.HasPrefix(seg, ".") {
				t.Fatalf("accepted path %q enters dot segment %q", p, seg)
			}
		}
		if !strings.HasSuffix(joined, mdExt) {
			t.Fatalf("accepted path %q does not end in .md", p)
		}
	})
}

// listTree returns every path below root, including directories.
func listTree(root string) map[string]bool {
	out := map[string]bool{}
	filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		out[full] = true
		return nil
	})
	return out
}
