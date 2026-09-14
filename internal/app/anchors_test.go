package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/markdown"
)

// anchorAt builds an anchor for the first occurrence of sub in an ASCII body.
func anchorAt(t *testing.T, body, sub string) *core.CommentAnchor {
	t.Helper()
	i := strings.Index(body, sub)
	if i < 0 {
		t.Fatalf("%q not in %q", sub, body)
	}
	return &core.CommentAnchor{Exact: sub, Start: i, End: i + len(sub)}
}

// wantFieldError fails unless err is a validation error on field.
func wantFieldError(t *testing.T, err error, field string) {
	t.Helper()
	var ve *core.ValidationError
	if !errors.Is(err, core.ErrInvalid) || !errors.As(err, &ve) || ve.Field != field {
		t.Fatalf("error = %v, want a validation error on %s", err, field)
	}
}

// storedComment reads a comment row from the index.
func (h *rharness) storedComment(id string) *core.Comment {
	h.t.Helper()
	c, err := h.db.GetComment(h.ctx, id)
	if err != nil {
		h.t.Fatalf("get comment %s: %v", id, err)
	}
	return c
}

// rootByID finds a root comment in a listing.
func rootByID(t *testing.T, roots []*core.Comment, id string) *core.Comment {
	t.Helper()
	for _, c := range roots {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("comment %s not among the roots", id)
	return nil
}

func TestCreateCommentAnchorRules(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	body := "Intro.\nThe sync engine runs.\nThe sync engine stops.\n"
	n := h.note(owner, sp, "", "Plan", body)

	root, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: "general"})
	if err != nil {
		t.Fatalf("general comment: %v", err)
	}
	if root.Anchor != nil || root.AnchorState != "" {
		t.Fatalf("general comment has anchor %+v, state %q", root.Anchor, root.AnchorState)
	}

	_, err = h.app.CreateComment(h.ctx, owner, core.CommentInput{
		NoteID: n.ID, ParentID: root.ID, Body: "reply", Anchor: anchorAt(t, body, "sync"),
	})
	wantFieldError(t, err, "anchor")

	_, err = h.app.CreateComment(h.ctx, owner, core.CommentInput{
		NoteID: n.ID, Body: "missing", Anchor: &core.CommentAnchor{Exact: "warp drive", Start: 0, End: 10},
	})
	wantFieldError(t, err, "anchor")
	if !strings.Contains(err.Error(), "not in the note") {
		t.Fatalf("error %q should say the text is not in the note", err)
	}

	for name, a := range map[string]*core.CommentAnchor{
		"empty exact":     {Exact: ""},
		"negative start":  {Exact: "sync", Start: -1, End: 3},
		"negative end":    {Exact: "sync", Start: 11, End: -4},
		"exact too large": {Exact: strings.Repeat("x", 5001)},
	} {
		_, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: name, Anchor: a})
		wantFieldError(t, err, "anchor")
	}

	// Stale offsets with context pick the second copy and store where it is.
	second := strings.LastIndex(body, "sync engine")
	c, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: "second", Anchor: &core.CommentAnchor{
		Exact: "sync engine", Prefix: "The sync engine runs.\nThe ", Suffix: " stops.", Start: 0, End: 11,
	}})
	if err != nil {
		t.Fatalf("anchored comment: %v", err)
	}
	if c.AnchorState != core.AnchorExact || c.Anchor.Start != second || c.Anchor.End != second+11 {
		t.Fatalf("created anchor %+v, state %q, want start %d", c.Anchor, c.AnchorState, second)
	}
	if got := h.storedComment(c.ID).Anchor; got == nil || got.Start != second || got.End != second+11 {
		t.Fatalf("stored anchor %+v, want start %d", got, second)
	}

	// A form post carries only the exact text.
	stops := strings.Index(body, "stops")
	c, err = h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: "form", Anchor: &core.CommentAnchor{Exact: "stops"}})
	if err != nil {
		t.Fatalf("form comment: %v", err)
	}
	if got := h.storedComment(c.ID).Anchor; got.Start != stops || got.End != stops+5 {
		t.Fatalf("form anchor %+v, want start %d", got, stops)
	}

	// Long context is cut to 32 units.
	c, err = h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: "context", Anchor: &core.CommentAnchor{
		Exact: "Intro", Prefix: strings.Repeat("p", 40), Suffix: strings.Repeat("s", 40), Start: 0, End: 5,
	}})
	if err != nil {
		t.Fatalf("context comment: %v", err)
	}
	if got := h.storedComment(c.ID).Anchor; got.Prefix != strings.Repeat("p", 32) || got.Suffix != strings.Repeat("s", 32) {
		t.Fatalf("context not cut: %+v", got)
	}

	// Files and folders take comments without an anchor.
	folder := h.folder(owner, sp, "", "Drawings")
	file := h.upload(owner, sp, folder.ID, "plan.txt", "the sync engine")
	for _, id := range []string{folder.ID, file.ID} {
		_, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: id, Body: "anchored", Anchor: &core.CommentAnchor{Exact: "sync"}})
		wantFieldError(t, err, "anchor")
		c, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: id, Body: "on the item"})
		if err != nil {
			t.Fatalf("comment on %s: %v", id, err)
		}
		if _, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: id, ParentID: c.ID, Body: "reply"}); err != nil {
			t.Fatalf("reply on %s: %v", id, err)
		}
		roots, err := h.app.ListComments(h.ctx, owner, id)
		if err != nil || len(roots) != 1 || len(roots[0].Children) != 1 || roots[0].Anchor != nil {
			t.Fatalf("comments on %s = %+v, %v", id, roots, err)
		}
		if _, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, ParentID: c.ID, Body: "cross"}); err == nil {
			t.Fatalf("a reply on a note accepted a parent comment on %s", id)
		}
	}
}

func TestListCommentsResolvesAndWritesBack(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	body := "Intro.\nThe sync engine runs.\n"
	n := h.note(owner, sp, "", "Plan", body)
	c, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: "on engine", Anchor: anchorAt(t, body, "engine")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before := h.storedComment(c.ID)

	// An edit made outside the application gives no diff.
	disk, err := h.files.Read(sp.Slug, n.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	edited := "Written in vim.\n" + body
	disk.Content = edited
	if err := h.files.Write(sp.Slug, disk); err != nil {
		t.Fatalf("write: %v", err)
	}
	h.clock.Advance(time.Hour)

	want := strings.Index(edited, "engine")
	roots, err := h.app.ListComments(h.ctx, owner, n.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := rootByID(t, roots, c.ID)
	if got.AnchorState != core.AnchorMoved || got.Anchor.Start != want || got.Anchor.End != want+6 {
		t.Fatalf("first listing %+v, state %q, want moved to %d", got.Anchor, got.AnchorState, want)
	}
	stored := h.storedComment(c.ID)
	if stored.Anchor.Start != want || stored.Anchor.End != want+6 {
		t.Fatalf("moved offsets not written back: %+v", stored.Anchor)
	}
	if !stored.UpdatedAt.Equal(before.UpdatedAt) || stored.Body != before.Body {
		t.Fatalf("write-back changed the comment: updated %v to %v, body %q", before.UpdatedAt, stored.UpdatedAt, stored.Body)
	}

	roots, err = h.app.ListComments(h.ctx, owner, n.ID)
	if err != nil {
		t.Fatalf("second list: %v", err)
	}
	if got := rootByID(t, roots, c.ID); got.AnchorState != core.AnchorExact {
		t.Fatalf("second listing state %q, want exact", got.AnchorState)
	}

	// A body that cannot be read leaves the state empty instead of failing.
	h.removeDisk(sp, n.Path)
	roots, err = h.app.ListComments(h.ctx, owner, n.ID)
	if err != nil {
		t.Fatalf("list without a file: %v", err)
	}
	if got := rootByID(t, roots, c.ID); got.AnchorState != "" || got.Anchor == nil || got.Anchor.Start != want {
		t.Fatalf("unreadable body gave %+v, state %q", got.Anchor, got.AnchorState)
	}
}

func TestListCommentsOnTrashedNote(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	body := "Intro.\nThe sync engine runs.\n"
	n := h.note(owner, sp, "", "Plan", body)
	c, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: "x", Anchor: anchorAt(t, body, "sync")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := h.app.TrashNote(h.ctx, owner, n.ID); err != nil {
		t.Fatalf("trash: %v", err)
	}
	roots, err := h.app.ListComments(h.ctx, owner, n.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := rootByID(t, roots, c.ID); got.AnchorState != core.AnchorExact {
		t.Fatalf("trashed note anchor state %q, want exact from the trash entry", got.AnchorState)
	}
}

func TestListCommentsSortOrder(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	body := "alpha beta gamma delta\n"
	n := h.note(owner, sp, "", "Order", body)

	create := func(text string, a *core.CommentAnchor, parent string) *core.Comment {
		t.Helper()
		h.clock.Advance(time.Second)
		c, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, ParentID: parent, Body: text, Anchor: a})
		if err != nil {
			t.Fatalf("create %s: %v", text, err)
		}
		return c
	}
	general1 := create("general 1", nil, "")
	gamma := create("gamma", anchorAt(t, body, "gamma"), "")
	alpha := create("alpha, deleted later", anchorAt(t, body, "alpha"), "")
	beta := create("beta", anchorAt(t, body, "beta"), "")
	delta := create("delta, deleted later", anchorAt(t, body, "delta"), "")
	general2 := create("general 2", nil, "")
	beta2 := create("beta again", anchorAt(t, body, "beta"), "")
	reply := create("reply to gamma", nil, gamma.ID)

	if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: ptrTo("beta gamma\n")}); err != nil {
		t.Fatalf("update: %v", err)
	}
	roots, err := h.app.ListComments(h.ctx, owner, n.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []*core.Comment{beta, beta2, gamma, general1, general2, alpha, delta}
	if len(roots) != len(want) {
		t.Fatalf("%d roots, want %d", len(roots), len(want))
	}
	for i, w := range want {
		if roots[i].ID != w.ID {
			var got []string
			for _, r := range roots {
				got = append(got, r.Body)
			}
			t.Fatalf("root order %q, want %q at %d", got, w.Body, i)
		}
	}
	if len(roots[2].Children) != 1 || roots[2].Children[0].ID != reply.ID {
		t.Fatalf("gamma thread lost its reply: %+v", roots[2].Children)
	}
	for _, r := range roots[5:] {
		if r.AnchorState != core.AnchorOrphaned || r.Anchor.Exact == "" || r.Anchor.Start != -1 {
			t.Fatalf("orphaned root %q has %+v, state %q", r.Body, r.Anchor, r.AnchorState)
		}
	}
	for _, r := range roots[:3] {
		if r.AnchorState != core.AnchorExact {
			t.Fatalf("root %q state %q, want exact after the remap", r.Body, r.AnchorState)
		}
	}
}

func TestUpdateNoteRemapsAnchors(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	body := "Intro.\nThe sync engine runs.\n"
	n := h.note(owner, sp, "", "Plan", body)
	c, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: "x", Anchor: anchorAt(t, body, "sync engine")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, ParentID: c.ID, Body: "reply"}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	created := h.storedComment(c.ID)

	update := func(content string) {
		t.Helper()
		h.clock.Advance(time.Minute)
		if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: &content}); err != nil {
			t.Fatalf("update: %v", err)
		}
	}

	next := "Hello.\n" + body
	update(next)
	got := h.storedComment(c.ID)
	if got.Anchor.Exact != "sync engine" || got.Anchor.Start != strings.Index(next, "sync engine") {
		t.Fatalf("after insertion before: %+v", got.Anchor)
	}
	if !got.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("remap changed UpdatedAt from %v to %v", created.UpdatedAt, got.UpdatedAt)
	}

	next = "Hello.\nIntro.\nThe sync big engine runs.\n"
	update(next)
	if got := h.storedComment(c.ID).Anchor; got.Exact != "sync big engine" || got.Start != strings.Index(next, "sync big engine") {
		t.Fatalf("after insertion inside: %+v", got)
	}

	title := "Renamed"
	if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Title: &title}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	kept := *h.storedComment(c.ID).Anchor

	update("Hello.\nIntro.\nThe runs.\n")
	if got := *h.storedComment(c.ID).Anchor; got != kept {
		t.Fatalf("deleting the passage changed the anchor from %+v to %+v", kept, got)
	}
	roots, err := h.app.ListComments(h.ctx, owner, n.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if r := rootByID(t, roots, c.ID); r.AnchorState != core.AnchorOrphaned || r.Anchor.Exact != "sync big engine" {
		t.Fatalf("deleted passage lists %+v, state %q", r.Anchor, r.AnchorState)
	}
}

func TestRestoreVersionRemapsAnchors(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	body := "Intro.\nThe sync engine runs.\n"
	n := h.note(owner, sp, "", "Plan", body)
	c, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: "x", Anchor: anchorAt(t, body, "engine")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	longer := "A preface that pushes everything down.\n\n" + body
	if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: &longer, Snapshot: true}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := h.storedComment(c.ID).Anchor; got.Start != strings.Index(longer, "engine") {
		t.Fatalf("after update: %+v", got)
	}
	versions, err := h.app.ListVersions(h.ctx, owner, n.ID)
	if err != nil || len(versions) == 0 {
		t.Fatalf("versions = %v, %v", versions, err)
	}
	versionID := ""
	for _, v := range versions {
		full, err := h.app.GetVersion(h.ctx, owner, n.ID, v.ID)
		if err == nil && full.Content == body {
			versionID = v.ID
		}
	}
	if versionID == "" {
		t.Fatal("no snapshot holds the first body")
	}
	h.clock.Advance(time.Minute)
	if _, err := h.app.RestoreVersion(h.ctx, owner, n.ID, versionID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := h.storedComment(c.ID).Anchor; got.Start != strings.Index(body, "engine") || got.Exact != "engine" {
		t.Fatalf("after restore: %+v", got)
	}
}

// failingCommentDB refuses comment updates, to show that a failed anchor store
// does not fail the note edit.
type failingCommentDB struct {
	*index.DB
}

func (d failingCommentDB) UpdateComment(ctx context.Context, c *core.Comment) error {
	return errors.New("disk full")
}

func (d failingCommentDB) UpdateCommentAnchor(ctx context.Context, id string, prev, next *core.CommentAnchor) (bool, error) {
	return false, errors.New("disk full")
}

func TestAnchorStoreFailureKeepsEdit(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	body := "Intro.\nThe sync engine runs.\n"
	n := h.note(owner, sp, "", "Plan", body)
	if _, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: "x", Anchor: anchorAt(t, body, "engine")}); err != nil {
		t.Fatalf("create: %v", err)
	}
	broken, err := New(Deps{
		Files: h.files, DB: failingCommentDB{h.db}, Renderer: markdown.New(slog.New(slog.NewTextHandler(io.Discard, nil))), AI: h.ai, Hasher: fakeHasher{},
		Config: h.cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: h.clock.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = broken.Close() })

	next := "Hello.\n" + body
	if _, err := broken.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: &next}); err != nil {
		t.Fatalf("update with a failing comment store: %v", err)
	}
	roots, err := broken.ListComments(h.ctx, owner, n.ID)
	if err != nil {
		t.Fatalf("list with a failing comment store: %v", err)
	}
	if len(roots) != 1 || roots[0].AnchorState != core.AnchorMoved {
		t.Fatalf("roots %+v", roots)
	}
}

// TestAnchorWriteBackKeepsOtherFields checks that storing a moved anchor
// leaves UpdatedAt alone and does not undo a resolve made after listing.
func TestAnchorWriteBackKeepsOtherFields(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	body := "Intro.\nThe sync engine runs.\n"
	n := h.note(owner, sp, "", "Plan", body)
	c, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: n.ID, Body: "x", Anchor: anchorAt(t, body, "engine")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stored := h.storedComment(c.ID)
	prev := *stored.Anchor
	updatedAt := stored.UpdatedAt

	h.clock.Advance(time.Hour)
	next := prev
	next.Start, next.End = prev.Start+7, prev.End+7
	h.app.storeCommentAnchor(h.ctx, c.ID, prev, next)
	after := h.storedComment(c.ID)
	if *after.Anchor != next || !after.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("write-back changed UpdatedAt or missed the anchor: %+v", after)
	}

	// A resolve lands, then a write-back computed from the old anchor arrives.
	if _, err := h.app.ResolveComment(h.ctx, owner, c.ID, true); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	stale := next
	stale.Start, stale.End = 0, len("engine")
	h.app.storeCommentAnchor(h.ctx, c.ID, prev, stale)
	final := h.storedComment(c.ID)
	if !final.Resolved {
		t.Fatal("a stale anchor write undid the resolve")
	}
	if *final.Anchor != next {
		t.Fatalf("a stale anchor write replaced the current anchor: %+v", final.Anchor)
	}
}

func ptrTo[T any](v T) *T { return &v }
