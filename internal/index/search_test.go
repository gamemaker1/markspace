package index

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestFTSMatchQuery(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"only space", "   ", ""},
		{"one term gets a prefix star", "sqlite", `"sqlite"*`},
		{"two terms", "pure go", `"pure" "go"*`},
		{"operators are quoted", "cats AND dogs", `"cats" "AND" "dogs"*`},
		{"user star is dropped", "inst*", `"inst"*`},
		{"internal quote is doubled", `say"hi`, `"say""hi"*`},
		{"column filter is neutralised", "title:secret", `"titlesecret"*`},
		{"parentheses are dropped", "(a OR b)", `"a" "OR" "b"*`},
		{"lone operator characters vanish", `* ^ ( ) "`, ""},
		{"hyphen stays inside the phrase", "well-known", `"well-known"*`},
		{"NUL separates tokens", "a\x00b", `"a b"*`},
		{"lone NUL vanishes", "\x00", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ftsMatchQuery(tc.in); got != tc.want {
				t.Fatalf("ftsMatchQuery(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestSearchNotesWithNULQuery is the FuzzSearchNotes failure: a NUL byte in
// the query made MATCH fail with "unterminated string", which the API turned
// into a 500.
func TestSearchNotesWithNULQuery(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	docs := mustSpace(t, db, "docs")
	note := mustNote(t, db, docs.ID, "Storage", "sqlite engine notes")
	for _, q := range []string{"\x00", "sqlite\x00engine", "sql\x00"} {
		hits, err := db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: q})
		if err != nil {
			t.Fatalf("SearchNotes(%q): %v", q, err)
		}
		if q == "sqlite\x00engine" && (len(hits) != 1 || hits[0].Note.ID != note.ID) {
			t.Fatalf("SearchNotes(%q) gave %d hits, want the note", q, len(hits))
		}
	}
}

func TestSearchNotes(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	docs := mustSpace(t, db, "docs")
	private := mustSpace(t, db, "private")

	sqliteNote := mustNote(t, db, docs.ID, "Storage engine",
		"Markspace keeps its index in SQLite with FTS5 and no cgo.", withTags("database"))
	goNote := mustNote(t, db, docs.ID, "Build notes",
		"The binary is pure Go. Run go build to compile it.", withTags("build"))
	mustNote(t, db, private.ID, "Secret plans", "SQLite appears here too, in a private space.")
	cafe := mustNote(t, db, docs.ID, "Café", "Crème brûlée recipe.")

	// A term is found and the snippet marks it.
	hits, err := db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: "sqlite"})
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 1 || hits[0].Note.ID != sqliteNote.ID {
		t.Fatalf("search for sqlite in docs gave %d hits", len(hits))
	}
	if !strings.Contains(hits[0].Snippet, "<mark>SQLite</mark>") {
		t.Fatalf("snippet %q does not mark the term", hits[0].Snippet)
	}
	if hits[0].Score <= 0 {
		t.Fatalf("score %v should be positive, higher is better", hits[0].Score)
	}
	if hits[0].Note.Content != "" {
		t.Fatal("hits should carry no content")
	}

	// The space filter is respected.
	hits, _ = db.SearchNotes(ctx, []string{docs.ID, private.ID}, core.SearchQuery{Text: "sqlite"})
	if len(hits) != 2 {
		t.Fatalf("search across both spaces gave %d hits, want 2", len(hits))
	}
	hits, _ = db.SearchNotes(ctx, []string{docs.ID, private.ID},
		core.SearchQuery{Text: "sqlite", SpaceIDs: []string{private.ID}})
	if len(hits) != 1 || hits[0].Note.SpaceID != private.ID {
		t.Fatalf("q.SpaceIDs should narrow the allowed spaces, got %d hits", len(hits))
	}
	hits, _ = db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: "sqlite", SpaceIDs: []string{private.ID}})
	if len(hits) != 0 {
		t.Fatalf("q.SpaceIDs must not widen access, got %d hits", len(hits))
	}

	// An empty space list returns nothing instead of everything.
	hits, err = db.SearchNotes(ctx, nil, core.SearchQuery{Text: "sqlite"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("search with no spaces gave %d hits, %v", len(hits), err)
	}

	// Prefix matching on the last term.
	hits, _ = db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: "compi"})
	if len(hits) != 1 || hits[0].Note.ID != goNote.ID {
		t.Fatalf("prefix search gave %d hits", len(hits))
	}

	// Diacritics fold.
	hits, _ = db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: "creme brulee"})
	if len(hits) != 1 || hits[0].Note.ID != cafe.ID {
		t.Fatalf("diacritic folding gave %d hits", len(hits))
	}

	// Hostile input does not break the MATCH expression.
	for _, text := range []string{
		`"sqlite`,
		`sqlite"`,
		`"unterminated AND *`,
		`sql* AND "go" OR NOT (x`,
		`NEAR(a b)`,
		`title:sqlite`,
		`***`,
		`AND`,
		`-sqlite`,
		`^sqlite`,
	} {
		if _, err := db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: text}); err != nil {
			t.Errorf("SearchNotes(%q) failed: %v", text, err)
		}
	}
	hits, _ = db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: `"SQLite" AND`})
	if len(hits) != 0 {
		// AND is a literal term that every word must match, so no note has
		// both. The point is that the query ran.
		t.Logf("quoted AND query gave %d hits", len(hits))
	}

	// Tag and author filters.
	hits, _ = db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: "go", Tags: []string{"database"}})
	if len(hits) != 0 {
		t.Fatalf("tag filter let through %d hits", len(hits))
	}
	hits, _ = db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: "go", Tags: []string{"BUILD"}})
	if len(hits) != 1 || hits[0].Note.ID != goNote.ID {
		t.Fatalf("tag filter gave %d hits, want the build note", len(hits))
	}
	hits, _ = db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: "go", AuthorID: "nobody"})
	if len(hits) != 0 {
		t.Fatalf("author filter let through %d hits", len(hits))
	}

	// Trash is excluded unless asked for.
	now := time.Now().UTC()
	if err := db.SetNoteDeleted(ctx, sqliteNote.ID, true, &now, ""); err != nil {
		t.Fatalf("SetNoteDeleted: %v", err)
	}
	hits, _ = db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: "sqlite"})
	if len(hits) != 0 {
		t.Fatalf("trashed note is still found: %d hits", len(hits))
	}
	hits, _ = db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: "sqlite", IncludeTrash: true})
	if len(hits) != 1 {
		t.Fatalf("IncludeTrash gave %d hits, want 1", len(hits))
	}

	// Empty text lists by update time with no snippet.
	hits, err = db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Text: "  "})
	if err != nil {
		t.Fatalf("empty search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("empty search gave %d hits, want the 2 live notes", len(hits))
	}
	for _, h := range hits {
		if h.Snippet != "" {
			t.Fatalf("empty search produced snippet %q", h.Snippet)
		}
	}
	hits, _ = db.SearchNotes(ctx, []string{docs.ID}, core.SearchQuery{Limit: 1, Offset: 1})
	if len(hits) != 1 {
		t.Fatalf("paged empty search gave %d hits, want 1", len(hits))
	}
}

func TestSuggestNotesOrdering(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	// Title substring, newest overall.
	substring := mustNote(t, db, space.ID, "Advanced install", "", withUpdated(base.Add(3*time.Hour)))
	// Title prefix, older.
	prefixOld := mustNote(t, db, space.ID, "Install on Linux", "", withUpdated(base))
	// Title prefix, newer.
	prefixNew := mustNote(t, db, space.ID, "INSTALL on macOS", "", withUpdated(base.Add(time.Hour)))
	// Slug-only match.
	slugOnly := &core.Note{ID: core.NewID(), SpaceID: space.ID, Slug: "setup-install-notes",
		Path: "setup-install-notes.md", Title: "Setup", UpdatedAt: base.Add(4 * time.Hour)}
	mustUpsert(t, db, slugOnly)
	mustNote(t, db, space.ID, "Unrelated", "")

	got, err := db.SuggestNotes(ctx, []string{space.ID}, "Install", 0)
	if err != nil {
		t.Fatalf("SuggestNotes: %v", err)
	}
	var ids []string
	for _, n := range got {
		ids = append(ids, n.ID)
	}
	want := []string{prefixNew.ID, prefixOld.ID, substring.ID, slugOnly.ID}
	if !slices.Equal(ids, want) {
		titles := make([]string, 0, len(got))
		for _, n := range got {
			titles = append(titles, n.Title)
		}
		t.Fatalf("SuggestNotes order is %v", titles)
	}

	if limited, _ := db.SuggestNotes(ctx, []string{space.ID}, "install", 2); len(limited) != 2 {
		t.Fatalf("limit 2 gave %d rows", len(limited))
	}
	if wild, _ := db.SuggestNotes(ctx, []string{space.ID}, "%", 0); len(wild) != 0 {
		t.Fatalf("a literal %% matched %d notes", len(wild))
	}
	if none, err := db.SuggestNotes(ctx, nil, "install", 0); err != nil || len(none) != 0 {
		t.Fatalf("SuggestNotes with no spaces gave %d rows, %v", len(none), err)
	}

	recent, err := db.RecentNotes(ctx, []string{space.ID}, 2)
	if err != nil {
		t.Fatalf("RecentNotes: %v", err)
	}
	if len(recent) != 2 || recent[0].ID == prefixOld.ID {
		t.Fatalf("RecentNotes gave %d rows starting with %q", len(recent), recent[0].Title)
	}
}

func TestSearchFallsBackToAnyTerm(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	ops := mustSpace(t, db, "ops")

	runbook := mustNote(t, db, ops.ID, "Upgrade runbook", "Drain each cluster node before upgrading kubelet.")
	both := mustNote(t, db, ops.ID, "Alpha beta", "alpha and beta together")
	alphaOnly := mustNote(t, db, ops.ID, "Alpha only", "alpha on its own")

	// No note holds every word of a question, so any term matches instead.
	hits, err := db.SearchNotes(ctx, []string{ops.ID}, core.SearchQuery{Text: "how do I drain the cluster"})
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) == 0 || hits[0].Note.ID != runbook.ID {
		t.Fatalf("question search gave %d hits, want the runbook first", len(hits))
	}

	// When some note holds every term, only those notes come back.
	hits, err = db.SearchNotes(ctx, []string{ops.ID}, core.SearchQuery{Text: "alpha beta"})
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 1 || hits[0].Note.ID != both.ID {
		t.Fatalf("all-terms search gave %d hits, want only the note with both terms", len(hits))
	}
	_ = alphaOnly

	// A later page never switches matching mode, so pages stay consistent.
	hits, err = db.SearchNotes(ctx, []string{ops.ID}, core.SearchQuery{Text: "how do I drain the cluster", Offset: 1})
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("offset page used the fallback and gave %d hits", len(hits))
	}

	// Stopwords do not match in the fallback, so a note sharing only "the"
	// with the question stays out.
	mustNote(t, db, ops.ID, "Filler", "the cat sat on the mat")
	hits, err = db.SearchNotes(ctx, []string{ops.ID}, core.SearchQuery{Text: "how do I drain the cluster"})
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	for _, h := range hits {
		if h.Note.Title == "Filler" {
			t.Fatal("fallback matched a note on a stopword only")
		}
	}

	// A single missing term does not fall back.
	hits, err = db.SearchNotes(ctx, []string{ops.ID}, core.SearchQuery{Text: "nonexistentword"})
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("single-term miss gave %d hits", len(hits))
	}
}

func TestFTSAnyQuery(t *testing.T) {
	cases := map[string]string{
		"drain cluster": `"drain" OR "cluster"*`,
		"single":        `"single"*`,
		`a"b OR`:        `"a""b"*`,
		// The tokenizer drops "?" at match time, so it may stay in the quoted term.
		"how do I drain the cluster?": `"drain" OR "cluster?"*`,
		"the and or":                  `"the" OR "and" OR "or"*`,
		"   ":                         "",
	}
	for in, want := range cases {
		if got := ftsAnyQuery(in); got != want {
			t.Errorf("ftsAnyQuery(%q) = %q, want %q", in, got, want)
		}
	}
}
