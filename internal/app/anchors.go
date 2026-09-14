package app

import (
	"context"
	"fmt"
	"sort"

	"github.com/gamemaker1/markspace/internal/anchor"
	"github.com/gamemaker1/markspace/internal/core"
)

// commentAnchor checks the anchor of a new root comment and finds it in the
// current body of the note. The stored offsets are the ones found, so an
// anchor sent with stale offsets, or with the exact text only as a form post
// sends it, is stored where the text is now. Text that is not in the note is
// rejected.
func (a *App) commentAnchor(ctx context.Context, note *core.Note, in core.CommentAnchor) (*core.CommentAnchor, error) {
	norm, err := anchor.Normalize(in)
	if err != nil {
		return nil, err
	}
	body, err := a.anchorBody(ctx, note)
	if err != nil {
		return nil, fmt.Errorf("read note body for comment anchor: %w", err)
	}
	start, end, state := anchor.Resolve(body, norm)
	if state == core.AnchorOrphaned {
		return nil, core.Invalid("anchor", "the selected text is not in the note")
	}
	norm.Start, norm.End = start, end
	return &norm, nil
}

// anchorBody reads the body of a note from its file, or from its trash entry
// when the note is trashed. Unlike noteBody it never drops an index row, and it
// returns an error when the body cannot be read.
func (a *App) anchorBody(ctx context.Context, note *core.Note) (string, error) {
	space, err := a.space(ctx, note.SpaceID)
	if err != nil {
		return "", err
	}
	path := note.Path
	if note.Deleted {
		body, ok := a.trashedNoteBody(space, note)
		if !ok {
			return "", fmt.Errorf("trash entry of note %s: %w", note.ID, core.ErrNotFound)
		}
		return body, nil
	}
	disk, err := a.files.Read(space.Slug, path)
	if err != nil {
		return "", fmt.Errorf("read note %s: %w", path, err)
	}
	if disk == nil {
		return "", fmt.Errorf("read note %s: %w", path, core.ErrNotFound)
	}
	return disk.Content, nil
}

// resolveCommentAnchors resolves the anchor of every root comment against the
// current body, read once. It sets the resolved offsets and AnchorState on each
// comment, with offsets of -1 for an orphaned anchor. An anchor found away from
// its stored offsets is written back, so the next listing is exact. When the
// body cannot be read, the comments keep their stored anchors and an empty
// AnchorState.
func (a *App) resolveCommentAnchors(ctx context.Context, note *core.Note, roots []*core.Comment) {
	anchored := make([]*core.Comment, 0, len(roots))
	for _, c := range roots {
		if c.Anchor != nil {
			anchored = append(anchored, c)
		}
	}
	if len(anchored) == 0 {
		return
	}
	body, err := a.anchorBody(ctx, note)
	if err != nil {
		a.log.Debug("read note body for comment anchors failed", "noteId", note.ID, "err", err)
		return
	}
	b := anchor.NewBody(body)
	for _, c := range anchored {
		stored := *c.Anchor
		start, end, state := b.Resolve(stored)
		resolved := stored
		resolved.Start, resolved.End = start, end
		c.Anchor = &resolved
		c.AnchorState = state
		if state == core.AnchorMoved {
			a.storeCommentAnchor(ctx, c.ID, stored, resolved)
		}
	}
}

// sortCommentRoots orders root comments as docs/comments.md specifies: anchored
// threads by the start of their passage, then general comments, then orphaned
// threads. Each group keeps creation order otherwise. An anchor whose body
// could not be read counts as anchored at its stored offsets.
func sortCommentRoots(roots []*core.Comment) {
	group := func(c *core.Comment) int {
		switch {
		case c.Anchor == nil:
			return 1
		case c.AnchorState == core.AnchorOrphaned:
			return 2
		}
		return 0
	}
	sort.SliceStable(roots, func(i, j int) bool {
		gi, gj := group(roots[i]), group(roots[j])
		if gi != gj {
			return gi < gj
		}
		if gi == 0 && roots[i].Anchor.Start != roots[j].Anchor.Start {
			return roots[i].Anchor.Start < roots[j].Anchor.Start
		}
		return roots[i].CreatedAt.Before(roots[j].CreatedAt)
	})
}

// remapCommentAnchors moves the anchors of a note's root comments through an
// edit from oldBody to newBody and stores the ones that changed. The diff is
// computed once and only when an anchored comment exists. Failures are logged
// and never fail the edit.
func (a *App) remapCommentAnchors(ctx context.Context, noteID, oldBody, newBody string) {
	if oldBody == newBody {
		return
	}
	// Suggested edits anchor to the body the same way, so every path that
	// remaps comment anchors remaps them too.
	a.remapSuggestions(ctx, noteID, oldBody, newBody)
	list, err := a.db.ListComments(ctx, noteID)
	if err != nil {
		a.log.Warn("list comments for anchor remap failed", "noteId", noteID, "err", err)
		return
	}
	var edit *anchor.Edit
	for _, c := range list {
		if c.ParentID != "" || c.Anchor == nil {
			continue
		}
		if edit == nil {
			edit = anchor.NewEdit(oldBody, newBody)
		}
		if next, changed := edit.Remap(*c.Anchor); changed {
			a.storeCommentAnchor(ctx, c.ID, *c.Anchor, next)
		}
	}
}

// storeCommentAnchor replaces the stored anchor of a comment. It is internal
// bookkeeping, so it skips the author and trash checks of a user edit. The
// database writes only the anchor column, and only while the stored anchor
// still equals prev, so UpdatedAt stays as stored and a concurrent edit,
// resolve or anchor change is never overwritten. Failures are logged.
func (a *App) storeCommentAnchor(ctx context.Context, id string, prev, next core.CommentAnchor) {
	wrote, err := a.db.UpdateCommentAnchor(ctx, id, &prev, &next)
	if err != nil {
		a.log.Warn("store comment anchor failed", "commentId", id, "err", err)
		return
	}
	if !wrote {
		a.log.Debug("comment anchor changed since it was read, skipping write", "commentId", id)
	}
}
