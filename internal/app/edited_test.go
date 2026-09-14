package app

import (
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// TestCommentEditedAtOnlyOnBodyEdit checks that resolving and reopening a
// thread leave EditedAt unset, that a body edit sets it, and that a later
// resolve keeps the edit time.
func TestCommentEditedAtOnlyOnBodyEdit(t *testing.T) {
	h := newRealHarness(t)
	person := h.user("Person", false)
	sp := h.space(person, "Alpha")
	n := h.note(person, sp, "", "Page", "some body")
	c, err := h.app.CreateComment(h.ctx, person, core.CommentInput{NoteID: n.ID, Body: "Is this right?"})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if c.EditedAt != nil {
		t.Fatalf("new comment EditedAt = %v", c.EditedAt)
	}
	load := func() *core.Comment {
		t.Helper()
		got, err := h.app.db.GetComment(h.ctx, c.ID)
		if err != nil {
			t.Fatalf("GetComment: %v", err)
		}
		return got
	}
	if _, err := h.app.ResolveComment(h.ctx, person, c.ID, true); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := h.app.ResolveComment(h.ctx, person, c.ID, false); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := load(); got.EditedAt != nil {
		t.Fatalf("EditedAt after resolve and reopen = %v", got.EditedAt)
	}
	edited, err := h.app.UpdateComment(h.ctx, person, c.ID, "Is this still right?")
	if err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	if edited.EditedAt == nil {
		t.Fatalf("UpdateComment returned no EditedAt")
	}
	at := load().EditedAt
	if at == nil {
		t.Fatalf("stored EditedAt is nil after an edit")
	}
	if _, err := h.app.ResolveComment(h.ctx, person, c.ID, true); err != nil {
		t.Fatalf("resolve after edit: %v", err)
	}
	got := load()
	if got.EditedAt == nil || !got.EditedAt.Equal(*at) || !got.Resolved {
		t.Fatalf("after resolve EditedAt = %v, want %v, resolved %v", got.EditedAt, *at, got.Resolved)
	}
	threads, err := h.app.ListComments(h.ctx, person, n.ID)
	if err != nil || len(threads) != 1 || threads[0].EditedAt == nil {
		t.Fatalf("ListComments = %+v, %v", threads, err)
	}
}
