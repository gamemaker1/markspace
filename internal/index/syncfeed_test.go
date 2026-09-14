package index

import (
	"context"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestNoteRevisionsOrderTheFeed(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	docs := mustSpace(t, db, "docs")
	other := mustSpace(t, db, "other")

	a := mustNote(t, db, docs.ID, "Alpha", "a")
	b := mustNote(t, db, docs.ID, "Beta", "b")
	c := mustNote(t, db, other.ID, "Gamma", "c")

	get := func(id string) int64 {
		t.Helper()
		n, err := db.GetNote(ctx, id)
		if err != nil {
			t.Fatalf("GetNote: %v", err)
		}
		return n.Rev
	}
	ra, rb, rc := get(a.ID), get(b.ID), get(c.ID)
	if !(ra > 0 && rb > ra && rc > rb) {
		t.Fatalf("revisions not increasing: %d %d %d", ra, rb, rc)
	}
	max, err := db.MaxNoteRev(ctx)
	if err != nil || max != rc {
		t.Fatalf("MaxNoteRev = %d, %v, want %d", max, err, rc)
	}

	// The window is (since, upTo], ordered by revision, filtered by scope.
	got, err := db.NotesChangedSince(ctx, []string{docs.ID}, nil, 0, max, 10)
	if err != nil || len(got) != 2 || got[0].ID != a.ID || got[1].ID != b.ID {
		t.Fatalf("docs feed = %v, %v", ids(got), err)
	}
	got, err = db.NotesChangedSince(ctx, []string{docs.ID}, nil, ra, max, 10)
	if err != nil || len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("feed after first rev = %v, %v", ids(got), err)
	}
	got, err = db.NotesChangedSince(ctx, []string{docs.ID}, []string{c.ID}, 0, max, 10)
	if err != nil || len(got) != 3 {
		t.Fatalf("space or id scope = %v, %v", ids(got), err)
	}
	got, err = db.NotesChangedSince(ctx, []string{docs.ID}, []string{c.ID}, 0, max, 2)
	if err != nil || len(got) != 2 {
		t.Fatalf("limit = %v, %v", ids(got), err)
	}
	got, err = db.NotesChangedSince(ctx, []string{docs.ID}, nil, 0, ra, 10)
	if err != nil || len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("upTo bound = %v, %v", ids(got), err)
	}
	if got, err := db.NotesChangedSince(ctx, nil, nil, 0, max, 10); err != nil || len(got) != 0 {
		t.Fatalf("empty scope = %v, %v", ids(got), err)
	}

	// Trashing and moving advance the revision, so the feed carries them.
	now := time.Now().UTC()
	if err := db.SetNoteDeleted(ctx, a.ID, true, &now, "u1"); err != nil {
		t.Fatalf("SetNoteDeleted: %v", err)
	}
	if r := get(a.ID); r <= max {
		t.Fatalf("trash rev %d not above %d", r, max)
	}
	max2, _ := db.MaxNoteRev(ctx)
	if err := db.SetNotePath(ctx, b.ID, "moved/beta.md", "beta", ""); err != nil {
		t.Fatalf("SetNotePath: %v", err)
	}
	if r := get(b.ID); r <= max2 {
		t.Fatalf("move rev %d not above %d", r, max2)
	}

	// A failed write leaves the counter where it was.
	before, _ := db.MaxNoteRev(ctx)
	if err := db.SetNotePath(ctx, "missing-note", "x.md", "x", ""); err == nil {
		t.Fatal("SetNotePath on a missing note succeeded")
	}
	if after, _ := db.MaxNoteRev(ctx); after != before {
		t.Fatalf("failed write moved the counter from %d to %d", before, after)
	}

	// Deleting the newest note never lets its revision be reused.
	top, _ := db.MaxNoteRev(ctx)
	if err := db.DeleteNoteRow(ctx, b.ID); err != nil {
		t.Fatalf("DeleteNoteRow: %v", err)
	}
	d := mustNote(t, db, docs.ID, "Delta", "d")
	if r := get(d.ID); r <= top {
		t.Fatalf("revision %d reused after delete, max was %d", r, top)
	}

	// Id listing includes trashed notes and excludes deleted rows.
	all, err := db.NoteIDsInSpaces(ctx, []string{docs.ID})
	if err != nil {
		t.Fatalf("NoteIDsInSpaces: %v", err)
	}
	want := map[string]bool{a.ID: true, d.ID: true}
	if len(all) != len(want) || !want[all[0]] || !want[all[1]] {
		t.Fatalf("NoteIDsInSpaces = %v, want trashed alpha and delta", all)
	}
}

func ids(notes []*core.Note) []string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		out = append(out, n.ID)
	}
	return out
}
