package index

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// ftsSeeds are search strings aimed at the FTS5 expression builder.
var ftsSeeds = []string{
	"", "sqlite", "pure go", `"`, `""`, `"a`, `a"b`, "*", "-", "- -", "a -b",
	"NOT", "a AND b", "a OR", "NEAR(a b)", "title:secret", "col:", "^a", "a*b",
	"(", ")", "{a b}", "[x]", "+", "a\x00b", "\x00", "\xff\xfe", "é", "日本語",
	"'", "a'b", "\\", "%", "_", "a\tb\nc", " ", "​", "the of and",
	"what is the", `"" ""`, `*"`, `":`, "a:b:c", ".", "..", "#tag", "a--b",
	"😀", "́", "0", "-0", "a+b", "@", "~", "|", "&", ",",
}

// openFuzzDB opens a database with two spaces and a few notes. It is shared
// by every input run in one fuzz worker process.
func openFuzzDB(f *testing.F) (*DB, []string) {
	f.Helper()
	db, err := Open(filepath.Join(f.TempDir(), "index.db"), testLogger())
	if err != nil {
		f.Fatalf("Open: %v", err)
	}
	f.Cleanup(func() { db.Close() })
	ctx := context.Background()
	var spaceIDs []string
	for _, slug := range []string{"docs", "private"} {
		s := &core.Space{Slug: slug, Name: slug}
		if err := db.CreateSpace(ctx, s); err != nil {
			f.Fatalf("CreateSpace: %v", err)
		}
		spaceIDs = append(spaceIDs, s.ID)
	}
	now := time.Now().UTC()
	for i, body := range []string{
		"Markspace keeps its index in SQLite with FTS5 and no cgo.",
		"The binary is pure Go. Run go build to compile it. well-known",
		"Crème brûlée recipe 日本語 😀 \"quoted\" a*b title:secret",
	} {
		title := []string{"Storage engine", "Build notes", "Café %_\\"}[i]
		slug := core.Slugify(title)
		n := &core.Note{
			ID: core.NewID(), SpaceID: spaceIDs[i%2], Slug: slug, Path: slug + ".md",
			Title: title, Tags: []string{"t" + slug}, CreatedAt: now, UpdatedAt: now,
		}
		row := ports.NoteRow{Note: n, Plain: body, ModTime: now, Size: int64(len(body))}
		if err := db.UpsertNote(ctx, row); err != nil {
			f.Fatalf("UpsertNote: %v", err)
		}
	}
	return db, spaceIDs
}

// FuzzSearchNotes checks that SearchNotes and SuggestNotes never fail for any
// query text, which means the FTS5 MATCH expression is always valid.
func FuzzSearchNotes(f *testing.F) {
	for _, seed := range ftsSeeds {
		f.Add(seed)
	}
	db, spaceIDs := openFuzzDB(f)
	f.Fuzz(func(t *testing.T, text string) {
		ctx := context.Background()
		if _, err := db.SearchNotes(ctx, spaceIDs, core.SearchQuery{Text: text}); err != nil {
			t.Fatalf("SearchNotes(%q): %v (match %s, any %s)", text, err, ftsMatchQuery(text), ftsAnyQuery(text))
		}
		if _, err := db.SearchNotes(ctx, spaceIDs, core.SearchQuery{Text: text, Tags: []string{text}, Limit: 2, Offset: 1}); err != nil {
			t.Fatalf("SearchNotes(%q) with tag and offset: %v", text, err)
		}
		if _, err := db.SuggestNotes(ctx, spaceIDs, text, 5); err != nil {
			t.Fatalf("SuggestNotes(%q): %v", text, err)
		}
	})
}

// FuzzFTSQuery runs ftsMatchQuery and ftsAnyQuery directly against FTS5. A
// non-empty expression must always be accepted by MATCH, and every term must
// be one double-quoted phrase.
func FuzzFTSQuery(f *testing.F) {
	for _, seed := range ftsSeeds {
		f.Add(seed)
	}
	db, _ := openFuzzDB(f)
	f.Fuzz(func(t *testing.T, text string) {
		for i, term := range ftsTerms(text, false) {
			if i == len(ftsTerms(text, false))-1 {
				term = strings.TrimSuffix(term, "*")
			}
			if len(term) < 2 || term[0] != '"' || term[len(term)-1] != '"' {
				t.Fatalf("ftsTerms(%q) produced unquoted term %s", text, term)
			}
			if strings.Contains(strings.ReplaceAll(term[1:len(term)-1], `""`, ""), `"`) {
				t.Fatalf("ftsTerms(%q) produced term %s with a lone quote", text, term)
			}
		}
		for _, expr := range []string{ftsMatchQuery(text), ftsAnyQuery(text)} {
			if expr == "" {
				continue
			}
			var n int
			err := db.SQL().QueryRowContext(context.Background(),
				`SELECT count(*) FROM notes_fts WHERE notes_fts MATCH ?`, expr).Scan(&n)
			if err != nil {
				t.Fatalf("MATCH %s (from %q): %v", expr, text, err)
			}
		}
	})
}

// FuzzParseTime checks that the tolerant time parser never panics, always
// returns UTC, and that a parsed time written with fmtTime reads back as the
// same instant.
func FuzzParseTime(f *testing.F) {
	for _, seed := range []string{
		"", " ", "2026-09-13T09:00:00.000000000Z", "2026-09-13T09:00:00Z",
		"2026-09-13T11:00:00+02:00", "2026-09-13 09:00:00", "2026-09-13",
		"2026-09-13T09:00:00.5", "2026-09-13 09:00:00.123-07:00", "junk",
		"0001-01-01T00:00:00Z", "0000-01-01T00:00:00+00:01", "9999-12-31T23:59:59-23:59",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := parseTime(s)
		if got.IsZero() {
			if parseTimePtr(s) != nil {
				t.Fatalf("parseTimePtr(%q) is not nil for a zero time", s)
			}
			return
		}
		if got.Location() != time.UTC {
			t.Fatalf("parseTime(%q) location = %v", s, got.Location())
		}
		stored := fmtTime(got)
		if back := parseTime(stored); !back.Equal(got) {
			t.Fatalf("parseTime(%q) = %v, stored as %q, read back as %v", s, got, stored, back)
		}
	})
}

// FuzzVectorRoundTrip checks that DecodeVector accepts exactly the byte
// strings whose length is a multiple of four, and that EncodeVector gives the
// same bytes back.
func FuzzVectorRoundTrip(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 0, 128, 63})
	f.Add([]byte{0, 0, 192, 127, 1, 0, 128, 127})
	f.Add([]byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, b []byte) {
		v, err := DecodeVector(b)
		if len(b)%vectorElementSize != 0 {
			if err == nil {
				t.Fatalf("DecodeVector accepted %d bytes", len(b))
			}
			return
		}
		if err != nil {
			t.Fatalf("DecodeVector(%d bytes): %v", len(b), err)
		}
		if len(v) != len(b)/vectorElementSize {
			t.Fatalf("DecodeVector(%d bytes) gave %d elements", len(b), len(v))
		}
		if out := EncodeVector(v); !bytes.Equal(out, b) {
			t.Fatalf("EncodeVector(DecodeVector(%x)) = %x", b, out)
		}
	})
}
