package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// canonical is a note file in exactly the form serializeNote produces.
const canonical = `---
id: 0abc123456789abcdefghjkmnp
title: Getting started
icon: "🚀"
tags: [onboarding, docs]
parent: 0abc123456789abcdefghjkmnq
position: 100
created: 2026-09-13T09:00:00Z
updated: 2026-09-13T09:12:00.5Z
author: 0user00000000000000000000
updated_by: 0user00000000000000000000
custom_flag: true
custom_list:
  - one
  - two
weird key: hello
zeta: 42
---
# Getting started

Body text with a --- line in it.
`

func TestFrontmatterRoundTripIsByteExact(t *testing.T) {
	s := newTestStore(t)
	full := filepath.Join(s.SpacesDir(), "docs", "guide.md")
	if err := os.WriteFile(full, []byte(canonical), fileMode); err != nil {
		t.Fatal(err)
	}
	note, err := s.Read("docs", "guide.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if note.ID != "0abc123456789abcdefghjkmnp" || note.Title != "Getting started" {
		t.Fatalf("known keys lifted wrong: %+v", note)
	}
	// The containing folder is the parent, so the parent key is ignored.
	if note.Icon != "🚀" || note.ParentID != "" || note.Position != 100 {
		t.Fatalf("known keys lifted wrong: %+v", note)
	}
	if !reflect.DeepEqual(note.Tags, []string{"onboarding", "docs"}) {
		t.Fatalf("tags = %v", note.Tags)
	}
	if note.CreatedAt != time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC) {
		t.Fatalf("created = %v", note.CreatedAt)
	}
	if note.CreatedBy != "0user00000000000000000000" || note.UpdatedBy != "0user00000000000000000000" {
		t.Fatalf("author keys = %q %q", note.CreatedBy, note.UpdatedBy)
	}
	wantExtra := map[string]any{
		"custom_flag": true,
		"custom_list": []any{"one", "two"},
		"weird key":   "hello",
		"zeta":        42,
	}
	if !reflect.DeepEqual(note.Extra, wantExtra) {
		t.Fatalf("extra = %#v, want %#v", note.Extra, wantExtra)
	}
	if err := s.Write("docs", note); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(canonical, "parent: 0abc123456789abcdefghjkmnq\n", "", 1)
	if string(got) != want {
		t.Fatalf("round trip changed the file:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFrontmatterValuePreservation(t *testing.T) {
	tests := []struct {
		name string
		body string
		key  string
		want any
	}{
		{"timestamp string", "legacy_date: 2020-01-02\n", "legacy_date", "2020-01-02"},
		{"quoted number", "code: '007'\n", "code", "007"},
		{"nested map", "meta:\n  a: 1\n  b: two\n", "meta", map[string]any{"a": 1, "b": "two"}},
		{"float", "ratio: 1.5\n", "ratio", 1.5},
		{"null", "empty:\n", "empty", nil},
		{"colon in value", "note: 'a: b'\n", "note", "a: b"},
		{"unicode", "emoji: \"🚀 go\"\n", "emoji", "🚀 go"},
	}
	s := newTestStore(t)
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			relPath := core.Slugify(tt.name) + mdExt
			raw := "---\nid: id" + core.Slugify(tt.name) + "\ntitle: T\n" + tt.body + "---\nbody\n"
			full := filepath.Join(s.SpacesDir(), "docs", relPath)
			if err := os.WriteFile(full, []byte(raw), fileMode); err != nil {
				t.Fatal(err)
			}
			note, err := s.Read("docs", relPath)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !reflect.DeepEqual(note.Extra[tt.key], tt.want) {
				t.Fatalf("%d: extra[%q] = %#v, want %#v", i, tt.key, note.Extra[tt.key], tt.want)
			}
			if err := s.Write("docs", note); err != nil {
				t.Fatalf("Write: %v", err)
			}
			again, err := s.Read("docs", relPath)
			if err != nil {
				t.Fatalf("re-read: %v", err)
			}
			if !reflect.DeepEqual(again.Extra[tt.key], tt.want) {
				t.Fatalf("after rewrite extra[%q] = %#v, want %#v", tt.key, again.Extra[tt.key], tt.want)
			}
			if again.Content != "body\n" {
				t.Fatalf("body = %q", again.Content)
			}
		})
	}
}

func TestReadFileWithoutFrontmatter(t *testing.T) {
	tests := []struct {
		name      string
		relPath   string
		raw       string
		wantTitle string
	}{
		{"leading heading", "release-notes.md", "# Release notes\n\nText\n", "Release notes"},
		{"heading after blanks", "spaced.md", "\n\n## Deep heading\n\nText\n", "Deep heading"},
		{"no heading", "plain-file.md", "Just text, no heading\n", "plain-file"},
		{"no trailing newline", "terse.md", "# Terse", "Terse"},
		{"unterminated block", "open.md", "---\nid: x\nstill open\n", "open"},
	}
	s := newTestStore(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			full := filepath.Join(s.SpacesDir(), "docs", tt.relPath)
			if err := os.WriteFile(full, []byte(tt.raw), fileMode); err != nil {
				t.Fatal(err)
			}
			mtime := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
			if err := os.Chtimes(full, mtime, mtime); err != nil {
				t.Fatal(err)
			}
			note, err := s.Read("docs", tt.relPath)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if note.ID == "" {
				t.Fatal("no id minted")
			}
			if note.Title != tt.wantTitle {
				t.Fatalf("title = %q, want %q", note.Title, tt.wantTitle)
			}
			if note.Content != tt.raw {
				t.Fatalf("content = %q, want %q", note.Content, tt.raw)
			}
			if !note.CreatedAt.Equal(mtime) || !note.UpdatedAt.Equal(mtime) {
				t.Fatalf("times = %v %v, want %v", note.CreatedAt, note.UpdatedAt, mtime)
			}
			// The minted id is persisted and the body is untouched.
			again, err := s.Read("docs", tt.relPath)
			if err != nil {
				t.Fatalf("re-read: %v", err)
			}
			if again.ID != note.ID {
				t.Fatalf("id changed from %q to %q", note.ID, again.ID)
			}
			if again.Content != tt.raw {
				t.Fatalf("body changed to %q", again.Content)
			}
		})
	}
}

func TestTimeLayoutsAccepted(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{"2026-09-13T09:00:00Z", time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)},
		{"2026-09-13T09:00:00.25Z", time.Date(2026, 9, 13, 9, 0, 0, 250000000, time.UTC)},
		{"2026-09-13", time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)},
		{"2026-09-13 09:00:00", time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)},
		{"2026-09-13T11:00:00+02:00", time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		got, ok := asTime(tt.in)
		if !ok {
			t.Fatalf("asTime(%q) failed", tt.in)
		}
		if !got.Equal(tt.want) {
			t.Errorf("asTime(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
	if _, ok := asTime("not a time"); ok {
		t.Error("asTime accepted junk")
	}
	if _, ok := asTime(""); ok {
		t.Error("asTime accepted an empty string")
	}
}

func TestSerializeOmitsEmptyValuesAndOrdersKeys(t *testing.T) {
	note := &core.Note{
		ID:       "0id",
		ParentID: "0folder",
		Title:    "Only a title",
		Content:  "body",
		Extra:    map[string]any{"zz": 1, "aa": 2},
	}
	data, err := serializeNote(note)
	if err != nil {
		t.Fatalf("serializeNote: %v", err)
	}
	want := "---\nid: 0id\ntitle: Only a title\naa: 2\nzz: 1\n---\nbody"
	if string(data) != want {
		t.Fatalf("serializeNote =\n%q\nwant\n%q", data, want)
	}
}

func TestSerializeTrashKeys(t *testing.T) {
	at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	note := &core.Note{
		ID:        "0id",
		Title:     "Gone",
		Deleted:   true,
		DeletedAt: &at,
		DeletedBy: "0user",
		Extra:     map[string]any{keyTrashedFrom: "guide/install.md", "other": "x"},
		Content:   "body\n",
	}
	data, err := serializeNote(note)
	if err != nil {
		t.Fatalf("serializeNote: %v", err)
	}
	want := "---\nid: 0id\ntitle: Gone\ndeleted: true\ndeleted_at: 2026-02-03T04:05:06Z\n" +
		"deleted_by: 0user\ntrashed_from: guide/install.md\nother: x\n---\nbody\n"
	if string(data) != want {
		t.Fatalf("serializeNote =\n%q\nwant\n%q", data, want)
	}
}

func TestYAMLScalarQuoting(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"Getting started", "Getting started"},
		{"a: b", `"a: b"`},
		{"# not a comment", `"# not a comment"`},
		{"true", `"true"`},
		{"42", `"42"`},
		{"2026-09-13", `"2026-09-13"`},
		{"🚀", `"🚀"`},
		{" padded ", `" padded "`},
		{"has \"quote\"", `"has \"quote\""`},
		{"line\nbreak", `"line\nbreak"`},
		{"-leading dash", `"-leading dash"`},
		{"a, b", `"a, b"`},
	}
	for _, tt := range tests {
		if got := yamlScalar(tt.in); got != tt.want {
			t.Errorf("yamlScalar(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

// TestYAMLScalarAlwaysReparses writes strings the YAML reader rejects or
// resolves to a non-string when unquoted, and checks each reads back as the
// same string. Before the fix, a control character or a C1 rune was written
// unquoted and made the whole note unreadable.
func TestYAMLScalarAlwaysReparses(t *testing.T) {
	for _, in := range []string{
		"0\x06", "a\x01b", "tab\tin", "del\x7f", "c1\u0080x", "nel\u0085x",
		"ls\u2028x", "ps\u2029x", "bom\ufeffx", "x\ufffe", "x\uffff",
		"0x1F", "0o17", "0b101", "1_000", "12_3.5", "2026-09-13T09:00:00Z", "1e5",
		"Ünïcode", "日本",
	} {
		note := &core.Note{ID: "n1", Title: in, Tags: []string{in}}
		data, err := serializeNote(note)
		if err != nil {
			t.Fatalf("serializeNote(%q): %v", in, err)
		}
		back, _, err := parseNote(data, "n.md", time.Now())
		if err != nil {
			t.Fatalf("parseNote after writing title %q: %v\n%s", in, err, data)
		}
		if back.Title != in {
			t.Errorf("title %q read back as %q", in, back.Title)
		}
		if len(back.Tags) != 1 || back.Tags[0] != in {
			t.Errorf("tag %q read back as %q", in, back.Tags)
		}
	}
}

// TestDerivedTitleWithControlCharacterStaysReadable is the store-level form of
// the FuzzFrontmatterRoundTrip failure: a file whose heading holds a control
// character is given a title from that heading, and the rewritten file must
// still parse.
func TestDerivedTitleWithControlCharacterStaysReadable(t *testing.T) {
	s := newTestStore(t)
	full := filepath.Join(s.SpacesDir(), "docs", "odd.md")
	if err := os.WriteFile(full, []byte("# 0\x06 and \xff\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := s.Read("docs", "odd.md")
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}
	if first.Title != "0\x06 and \ufffd" {
		t.Fatalf("title = %q", first.Title)
	}
	second, err := s.Read("docs", "odd.md")
	if err != nil {
		t.Fatalf("Read after the id was minted into the file: %v", err)
	}
	if second.ID != first.ID || second.Title != first.Title {
		t.Fatalf("reread gave id %q title %q, want %q %q", second.ID, second.Title, first.ID, first.Title)
	}
}

// TestEmptyFrontmatterKeySurvivesRewrite covers a YAML mapping entry whose key
// is empty. It is read into Note.Extra[""] and used to be dropped when the
// note was written back.
func TestEmptyFrontmatterKeySurvivesRewrite(t *testing.T) {
	raw := "---\nid: n1\ntitle: T\n\"\": kept\n? \n: also\n---\nbody\n"
	note, _, err := parseNote([]byte(raw), "n.md", time.Now())
	if err != nil {
		t.Fatalf("parseNote: %v", err)
	}
	if note.Extra[""] == nil {
		t.Fatalf("empty key not read: %#v", note.Extra)
	}
	data, err := serializeNote(note)
	if err != nil {
		t.Fatalf("serializeNote: %v", err)
	}
	back, _, err := parseNote(data, "n.md", time.Now())
	if err != nil {
		t.Fatalf("reparse: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(back.Extra[""], note.Extra[""]) {
		t.Fatalf("empty key value %#v became %#v\n%s", note.Extra[""], back.Extra[""], data)
	}
}

func TestTagsWriteAsFlowSequence(t *testing.T) {
	note := &core.Note{ID: "0id", Title: "T", Tags: []string{"one", "two words", "a,b"}}
	data, err := serializeNote(note)
	if err != nil {
		t.Fatalf("serializeNote: %v", err)
	}
	want := "tags: [one, two words, \"a,b\"]\n"
	if !strings.Contains(string(data), want) {
		t.Fatalf("serializeNote = %q, want it to contain %q", data, want)
	}
}
