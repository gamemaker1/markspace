package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// cancelOnWrite cancels the request context just before a note file or a
// file's content is written, the way a client that navigates away aborts a
// save after the checks passed.
type cancelOnWrite struct {
	ports.Files
	cancel context.CancelFunc
}

func (c *cancelOnWrite) Write(spaceSlug string, note *core.Note) error {
	c.cancel()
	return c.Files.Write(spaceSlug, note)
}

func (c *cancelOnWrite) ReplaceFile(spaceSlug, rel string, r io.Reader, limit int64, key ports.FileHistoryKey) (ports.SavedFile, *core.FileVersion, error) {
	c.cancel()
	return c.Files.ReplaceFile(spaceSlug, rel, r, limit, key)
}

func TestUpdateNoteReplayWithOldBase(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "v1")
	base := n.UpdatedAt
	versions := func() int {
		v, _ := h.files.ListVersions(sp.ID, n.ID)
		return len(v)
	}

	h.clock.Advance(10 * time.Second)
	title, body, icon := "Plan", "v2 #inline", "rocket"
	tags := []string{"work"}
	in := core.NoteUpdate{
		Title: &title, Content: &body, Tags: &tags, Icon: &icon, BaseUpdatedAt: &base,
		Extra: map[string]any{"status": "draft"},
	}
	first, err := h.app.UpdateNote(h.ctx, owner, n.ID, in)
	if err != nil {
		t.Fatalf("first update: %v", err)
	}
	kept := versions()

	// The replay carries the same values and the old base.
	h.clock.Advance(10 * time.Minute)
	again, err := h.app.UpdateNote(h.ctx, owner, n.ID, in)
	if err != nil {
		t.Fatalf("replay err = %v, want the note", err)
	}
	if again.Content != body || !again.UpdatedAt.Equal(first.UpdatedAt) || strings.Join(again.Tags, ",") != "work,inline" {
		t.Fatalf("replay = %+v, want the stored note", again)
	}
	if row := h.row(n.ID); !row.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("replay changed updatedAt to %s", row.UpdatedAt)
	}
	if versions() != kept {
		t.Fatalf("versions after replay = %d, want %d", versions(), kept)
	}

	// Tags as the client reads them back, with the inline hashtag, also count.
	merged := []string{"work", "inline"}
	in.Tags = &merged
	if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, in); err != nil {
		t.Fatalf("replay with index tags: %v", err)
	}

	// Different values with the old base still conflict.
	for name, change := range map[string]core.NoteUpdate{
		"content": {Content: new("v3"), BaseUpdatedAt: &base},
		"title":   {Title: new("Other"), BaseUpdatedAt: &base},
		"icon":    {Icon: new("star"), BaseUpdatedAt: &base},
		"tags":    {Tags: &[]string{"home"}, BaseUpdatedAt: &base},
		"extra":   {Extra: map[string]any{"status": "done"}, BaseUpdatedAt: &base},
	} {
		if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, change); !errors.Is(err, core.ErrConflict) {
			t.Fatalf("%s change with old base err = %v, want conflict", name, err)
		}
	}
	if disk, err := h.files.Read(sp.Slug, first.Path); err != nil || disk.Content != body {
		t.Fatalf("disk after refused updates = %+v, %v", disk, err)
	}
}

func TestUpdateNoteFinishesAfterCancel(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "before")

	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	h.app.files = &cancelOnWrite{Files: h.app.files, cancel: cancel}
	h.clock.Advance(10 * time.Second)
	title, body := "Renamed plan", "after the disconnect"
	if _, err := h.app.UpdateNote(ctx, owner, n.ID, core.NoteUpdate{Title: &title, Content: &body}); err != nil {
		t.Fatalf("update with a canceled request: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("the request context was not canceled")
	}
	row := h.row(n.ID)
	if row.Title != title || row.Path != "renamed-plan.md" || !strings.Contains(row.Excerpt, "after the disconnect") || !row.UpdatedAt.After(n.UpdatedAt) {
		t.Fatalf("index row = %+v, want the update", row)
	}
	disk, err := h.files.Read(sp.Slug, "renamed-plan.md")
	if err != nil || disk.Content != body {
		t.Fatalf("file = %+v, %v, want the update", disk, err)
	}
	if h.exists(sp, "plan.md") {
		t.Fatal("old file kept after the rename")
	}

	// A create whose client disconnects still gets its index row.
	ctx, cancel = context.WithCancel(h.ctx)
	defer cancel()
	h.app.files = &cancelOnWrite{Files: h.files, cancel: cancel}
	id := core.NewID()
	if _, err := h.app.CreateNote(ctx, owner, core.NoteInput{ID: id, SpaceID: sp.ID, Title: "Draft"}); err != nil {
		t.Fatalf("create with a canceled request: %v", err)
	}
	if row := h.row(id); row.Path != "draft.md" {
		t.Fatalf("created row = %+v", row)
	}
}

func TestReplaceFileReplayAndCancel(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	f := h.upload(owner, sp, "", "data.txt", "one")
	in := core.FileReplace{BaseSHA256: itSHA("one")}

	first, err := h.app.ReplaceFile(h.ctx, owner, f.ID, in, strings.NewReader("two"))
	if err != nil {
		t.Fatalf("first replace: %v", err)
	}
	again, err := h.app.ReplaceFile(h.ctx, owner, f.ID, in, strings.NewReader("two"))
	if err != nil || again.SHA256 != itSHA("two") || again.Versions != first.Versions {
		t.Fatalf("replay = %+v, %v, want the stored file", again, err)
	}
	if _, err := h.app.ReplaceFile(h.ctx, owner, f.ID, in, strings.NewReader("three")); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("different content with old base err = %v, want conflict", err)
	}

	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	h.app.files = &cancelOnWrite{Files: h.files, cancel: cancel}
	if _, err := h.app.ReplaceFile(ctx, owner, f.ID, core.FileReplace{BaseSHA256: itSHA("two")}, strings.NewReader("four")); err != nil {
		t.Fatalf("replace with a canceled request: %v", err)
	}
	if row, err := h.db.GetFile(h.ctx, f.ID); err != nil || row.SHA256 != itSHA("four") {
		t.Fatalf("index row = %+v, %v, want the new hash", row, err)
	}
}
