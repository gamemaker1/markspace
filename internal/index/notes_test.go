package index

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

func TestUpsertNoteUpdatesInPlace(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")

	n := mustNote(t, db, space.ID, "Getting started", "first body", withTags("docs"))

	n.Title = "Getting started again"
	n.Tags = []string{"onboarding"}
	n.Words = 42
	row := ports.NoteRow{Note: n, Plain: "second body", ModTime: time.Now(), Size: 11}
	if err := db.UpsertNote(ctx, row); err != nil {
		t.Fatalf("second UpsertNote: %v", err)
	}

	notes, err := db.ListNotes(ctx, space.ID, core.NoteListOptions{IncludeTrash: true})
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("ListNotes gave %d rows after two upserts of one id, want 1", len(notes))
	}
	got := notes[0]
	if got.Title != "Getting started again" || got.Words != 42 {
		t.Fatalf("the row was not updated: %+v", got)
	}
	if !slices.Equal(got.Tags, []string{"onboarding"}) {
		t.Fatalf("tags are %v, want [onboarding]", got.Tags)
	}
	if got.Content != "" {
		t.Fatal("Content should stay empty, bodies are not stored in the index")
	}

	var ftsRows int
	if err := db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notes_fts WHERE note_id = ?`, n.ID).Scan(&ftsRows); err != nil {
		t.Fatalf("count fts rows: %v", err)
	}
	if ftsRows != 1 {
		t.Fatalf("notes_fts holds %d rows for one note, want 1", ftsRows)
	}
	hits, err := db.SearchNotes(ctx, []string{space.ID}, core.SearchQuery{Text: "first"})
	if err != nil {
		t.Fatalf("SearchNotes: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("the old body is still searchable: %d hits", len(hits))
	}

	if _, err := db.GetNoteByPath(ctx, space.ID, n.Path); err != nil {
		t.Fatalf("GetNoteByPath: %v", err)
	}
	if _, err := db.GetNote(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetNote for a missing id gave %v, want core.ErrNotFound", err)
	}
	if err := db.UpsertNote(ctx, ports.NoteRow{Note: &core.Note{SpaceID: space.ID}}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("an upsert without an id gave %v, want core.ErrInvalid", err)
	}
}

func TestUpsertNotePathConflict(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")
	mustNote(t, db, space.ID, "Clash", "one")

	other := &core.Note{ID: core.NewID(), SpaceID: space.ID, Slug: "clash", Path: "clash.md", Title: "Other"}
	err := db.UpsertNote(ctx, ports.NoteRow{Note: other})
	if !errors.Is(err, core.ErrExists) {
		t.Fatalf("a second note on the same path gave %v, want core.ErrExists", err)
	}
}

func TestTagsReplaceAndFilter(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")

	a := mustNote(t, db, space.ID, "Alpha", "a", withTags("go", "sqlite", "Go"))
	mustNote(t, db, space.ID, "Beta", "b", withTags("go"))

	tagged, err := db.ListNotes(ctx, space.ID, core.NoteListOptions{Tag: "sqlite"})
	if err != nil {
		t.Fatalf("ListNotes by tag: %v", err)
	}
	if len(tagged) != 1 || tagged[0].ID != a.ID {
		t.Fatalf("tag filter gave %d notes", len(tagged))
	}
	tagged, _ = db.ListNotes(ctx, space.ID, core.NoteListOptions{Tag: "GO"})
	if len(tagged) != 2 {
		t.Fatalf("the tag filter should ignore case, got %d notes", len(tagged))
	}

	a.Tags = []string{"rust"}
	if err := db.UpsertNote(ctx, ports.NoteRow{Note: a, Plain: "a"}); err != nil {
		t.Fatalf("UpsertNote: %v", err)
	}
	if tagged, _ = db.ListNotes(ctx, space.ID, core.NoteListOptions{Tag: "sqlite"}); len(tagged) != 0 {
		t.Fatalf("the old tag still matches %d notes", len(tagged))
	}
	if tagged, _ = db.ListNotes(ctx, space.ID, core.NoteListOptions{Tag: "rust"}); len(tagged) != 1 {
		t.Fatalf("the new tag matches %d notes, want 1", len(tagged))
	}

	counts, err := db.ListTags(ctx, []string{space.ID})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	want := map[string]int{"go": 1, "rust": 1}
	if len(counts) != len(want) {
		t.Fatalf("ListTags gave %d tags, want %d", len(counts), len(want))
	}
	for _, c := range counts {
		if want[c.Tag] != c.Count {
			t.Errorf("tag %q counted %d, want %d", c.Tag, c.Count, want[c.Tag])
		}
	}
	if none, err := db.ListTags(ctx, nil); err != nil || len(none) != 0 {
		t.Fatalf("ListTags with no spaces gave %d tags, %v", len(none), err)
	}
}

func TestListNotesOptions(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	root := mustNote(t, db, space.ID, "Root", "", withPosition(2), withUpdated(base))
	second := mustNote(t, db, space.ID, "Another root", "", withPosition(1), withUpdated(base.Add(2*time.Hour)))
	child := mustNote(t, db, space.ID, "Child", "", withParent(root.ID), withUpdated(base.Add(time.Hour)))
	gone := mustNote(t, db, space.ID, "Gone", "", withPosition(3))
	now := time.Now().UTC()
	if err := db.SetNoteDeleted(ctx, gone.ID, true, &now, "someone"); err != nil {
		t.Fatalf("SetNoteDeleted: %v", err)
	}

	ids := func(notes []*core.Note) []string {
		out := make([]string, 0, len(notes))
		for _, n := range notes {
			out = append(out, n.ID)
		}
		return out
	}

	tests := []struct {
		name string
		opts core.NoteListOptions
		want []string
	}{
		{"default sort is position then title", core.NoteListOptions{}, []string{child.ID, second.ID, root.ID}},
		{"top level only", core.NoteListOptions{HasParent: true}, []string{second.ID, root.ID}},
		{"children of root", core.NoteListOptions{ParentID: root.ID}, []string{child.ID}},
		{"title sort", core.NoteListOptions{Sort: "title"}, []string{second.ID, child.ID, root.ID}},
		{"updated sort", core.NoteListOptions{Sort: "updated"}, []string{second.ID, child.ID, root.ID}},
		{"only trash", core.NoteListOptions{OnlyTrash: true}, []string{gone.ID}},
		{"include trash", core.NoteListOptions{IncludeTrash: true, Sort: "title"}, []string{second.ID, child.ID, gone.ID, root.ID}},
		{"limit and offset", core.NoteListOptions{Sort: "title", Limit: 1, Offset: 1}, []string{child.ID}},
		{"offset without limit", core.NoteListOptions{Sort: "title", Offset: 2}, []string{root.ID}},
		{"with content is accepted", core.NoteListOptions{WithContent: true, HasParent: true}, []string{second.ID, root.ID}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			notes, err := db.ListNotes(ctx, space.ID, tc.opts)
			if err != nil {
				t.Fatalf("ListNotes: %v", err)
			}
			if got := ids(notes); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}

	byIDs, err := db.ListNotesByIDs(ctx, []string{child.ID, "missing", root.ID, gone.ID})
	if err != nil {
		t.Fatalf("ListNotesByIDs: %v", err)
	}
	if got := ids(byIDs); !slices.Equal(got, []string{child.ID, root.ID, gone.ID}) {
		t.Fatalf("ListNotesByIDs gave %v, want the input order without the missing id", got)
	}

	if err := db.SetNoteDeleted(ctx, gone.ID, false, nil, ""); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, _ := db.GetNote(ctx, gone.ID)
	if restored.Deleted || restored.DeletedAt != nil || restored.DeletedBy != "" {
		t.Fatalf("the restored note still looks trashed: %+v", restored)
	}
	if n, err := db.CountNotes(ctx); err != nil || n != 4 {
		t.Fatalf("CountNotes = %d, %v, want 4", n, err)
	}
	if err := db.SetNoteDeleted(ctx, "missing", true, &now, ""); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("SetNoteDeleted on a missing id gave %v", err)
	}
}

func TestSetNotePathIndexStateAndDeleteUnder(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")

	parent := mustNote(t, db, space.ID, "Guide", "parent")
	child := &core.Note{ID: core.NewID(), SpaceID: space.ID, ParentID: parent.ID,
		Slug: "install", Path: "guide/install.md", Title: "Install"}
	grandchild := &core.Note{ID: core.NewID(), SpaceID: space.ID, ParentID: child.ID,
		Slug: "docker", Path: "guide/install/docker.md", Title: "Docker"}
	// A sibling whose path shares the text prefix but not the directory.
	sibling := &core.Note{ID: core.NewID(), SpaceID: space.ID, Slug: "guidebook",
		Path: "guidebook.md", Title: "Guidebook"}
	mod := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	for _, n := range []*core.Note{child, grandchild, sibling} {
		if err := db.UpsertNote(ctx, ports.NoteRow{Note: n, Plain: n.Title, ModTime: mod, Size: 7}); err != nil {
			t.Fatalf("UpsertNote(%s): %v", n.Path, err)
		}
	}

	state, err := db.IndexState(ctx, space.ID)
	if err != nil {
		t.Fatalf("IndexState: %v", err)
	}
	if len(state) != 4 {
		t.Fatalf("IndexState has %d entries, want 4", len(state))
	}
	st, ok := state["guide/install.md"]
	if !ok || st.NoteID != child.ID || st.Size != 7 || !st.ModTime.Equal(mod) {
		t.Fatalf("IndexState entry is %+v", st)
	}

	if err := db.SetNotePath(ctx, sibling.ID, "archive/guidebook.md", "guidebook", ""); err != nil {
		t.Fatalf("SetNotePath: %v", err)
	}
	moved, err := db.GetNoteByPath(ctx, space.ID, "archive/guidebook.md")
	if err != nil || moved.ID != sibling.ID {
		t.Fatalf("GetNoteByPath after a move gave %v, %v", moved, err)
	}
	if err := db.SetNotePath(ctx, sibling.ID, "guidebook.md", "guidebook", ""); err != nil {
		t.Fatalf("SetNotePath back: %v", err)
	}

	if ids, err := db.NoteIDsInSpace(ctx, space.ID); err != nil || len(ids) != 4 {
		t.Fatalf("NoteIDsInSpace gave %d ids, %v", len(ids), err)
	}

	if err := db.DeleteNoteRowsUnder(ctx, space.ID, "guide"); err != nil {
		t.Fatalf("DeleteNoteRowsUnder: %v", err)
	}
	state, _ = db.IndexState(ctx, space.ID)
	if len(state) != 1 {
		t.Fatalf("after deleting under guide %d entries remain, want 1: %v", len(state), state)
	}
	if _, ok := state["guidebook.md"]; !ok {
		t.Fatal("the sibling with a shared text prefix was deleted")
	}
	var fts int
	_ = db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM notes_fts`).Scan(&fts)
	if fts != 1 {
		t.Fatalf("notes_fts holds %d rows, want 1", fts)
	}

	if err := db.DeleteNoteRow(ctx, sibling.ID); err != nil {
		t.Fatalf("DeleteNoteRow: %v", err)
	}
	if err := db.DeleteNoteRow(ctx, sibling.ID); err != nil {
		t.Fatalf("DeleteNoteRow should be idempotent, got %v", err)
	}
	if _, err := db.GetNote(ctx, sibling.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetNote after DeleteNoteRow gave %v", err)
	}
}

func TestLinkResolutionAndBacklinks(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	space := mustSpace(t, db, "docs")
	otherSpace := mustSpace(t, db, "other")

	target := mustNote(t, db, space.ID, "Install Guide", "how to install")
	// A note with the same title in another space must not capture the link.
	mustNote(t, db, otherSpace.ID, "Install Guide", "elsewhere")

	source := &core.Note{ID: core.NewID(), SpaceID: space.ID, Slug: "readme", Path: "readme.md", Title: "Readme"}
	links := []string{"install guide", "Later Note", "install-guide.md#setup", "Install Guide"}
	if err := db.UpsertNote(ctx, ports.NoteRow{Note: source, Plain: "see links", Links: links}); err != nil {
		t.Fatalf("UpsertNote source: %v", err)
	}

	back, err := db.Backlinks(ctx, target.ID)
	if err != nil {
		t.Fatalf("Backlinks: %v", err)
	}
	if len(back) != 1 || back[0].FromNote.ID != source.ID {
		t.Fatalf("Backlinks of the target gave %d rows, want the source once", len(back))
	}

	// Both directions: the source has no backlinks of its own.
	if back, _ = db.Backlinks(ctx, source.ID); len(back) != 0 {
		t.Fatalf("the source has %d backlinks, want 0", len(back))
	}

	var dangling int
	_ = db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM note_links WHERE note_id = ? AND target_note_id IS NULL`, source.ID).Scan(&dangling)
	if dangling != 1 {
		t.Fatalf("%d links are unresolved, want 1 for Later Note", dangling)
	}

	// The late-created target picks up the link that was waiting for it.
	later := mustNote(t, db, space.ID, "Later Note", "arrived later")
	back, err = db.Backlinks(ctx, later.ID)
	if err != nil {
		t.Fatalf("Backlinks of the late note: %v", err)
	}
	if len(back) != 1 || back[0].FromNote.ID != source.ID {
		t.Fatalf("the late-created target has %d backlinks, want 1", len(back))
	}

	// The late note links back to the source by slug, which resolves at once.
	later.Title = "Later Note"
	if err := db.UpsertNote(ctx, ports.NoteRow{Note: later, Plain: "x", Links: []string{"readme"}}); err != nil {
		t.Fatalf("UpsertNote later: %v", err)
	}
	back, _ = db.Backlinks(ctx, source.ID)
	if len(back) != 1 || back[0].FromNote.ID != later.ID {
		t.Fatalf("the source has %d backlinks after the reply link, want 1", len(back))
	}

	// ResolveLink prefers the title, then the slug, then the path.
	got, err := db.ResolveLink(ctx, space.ID, "INSTALL GUIDE")
	if err != nil || got.ID != target.ID {
		t.Fatalf("ResolveLink by title gave %v, %v", got, err)
	}
	got, err = db.ResolveLink(ctx, space.ID, "install-guide.md")
	if err != nil || got.ID != target.ID {
		t.Fatalf("ResolveLink by path gave %v, %v", got, err)
	}
	if _, err := db.ResolveLink(ctx, space.ID, "nothing here"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("ResolveLink for a missing target gave %v", err)
	}

	// Removing the target leaves the link unresolved rather than gone.
	if err := db.DeleteNoteRow(ctx, target.ID); err != nil {
		t.Fatalf("DeleteNoteRow: %v", err)
	}
	_ = db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM note_links WHERE note_id = ? AND target_note_id IS NULL`, source.ID).Scan(&dangling)
	// Two rows named the target: "install guide" by title and "install-guide"
	// by slug. "Install Guide" was folded into the first as a duplicate.
	if dangling != 2 {
		t.Fatalf("%d links are unresolved after the target left, want 2", dangling)
	}
	// Recreating it resolves the link again.
	mustNote(t, db, space.ID, "Install Guide", "back again")
	_ = db.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM note_links WHERE note_id = ? AND target_note_id IS NULL`, source.ID).Scan(&dangling)
	if dangling != 0 {
		t.Fatalf("%d links are unresolved after the target returned, want 0", dangling)
	}
}

func TestNormalizeTarget(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Install Guide", "Install Guide"},
		{"  spaced  ", "spaced"},
		{"page.md", "page"},
		{"page.MD", "page"},
		{"./dir/page.md", "dir/page"},
		{"/dir/page/", "dir/page"},
		{"page#section", "page"},
		{"page.md#section", "page"},
		{"#only-fragment", ""},
		{"", ""},
		{".md", ".md"},
	}
	for _, tc := range tests {
		if got := normalizeTarget(tc.in); got != tc.want {
			t.Errorf("normalizeTarget(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
