package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// maxCommentBytes caps one comment body.
const maxCommentBytes = 20000

// deletedUserName is the author name shown on a comment whose account was
// deleted. The comment keeps the old user id.
const deletedUserName = "Deleted user"

// ListComments returns the comment threads of an item of any kind. Authors are populated,
// and the author of a deleted account shows as deletedUserName.
// A deleted comment keeps its place with an empty body and DeletedAt set, so
// its replies stay attached. Each root anchor is resolved against the current
// body, and roots are ordered by passage, then general comments, then
// orphaned threads.
func (a *App) ListComments(ctx context.Context, actor *core.Actor, noteID string) ([]*core.Comment, error) {
	it, _, err := a.requireItemAny(ctx, actor, noteID, core.AccessView)
	if err != nil {
		return nil, err
	}
	note := it.Note
	list, err := a.db.ListComments(ctx, noteID)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	authors := map[string]*core.User{}
	for _, c := range list {
		c.Children = nil
		if c.DeletedAt != nil {
			c.Body = ""
		}
		if c.UserID == "" {
			continue
		}
		u, ok := authors[c.UserID]
		if !ok {
			full, err := a.db.GetUser(ctx, c.UserID)
			switch {
			case err == nil && full != nil:
				u = &core.User{ID: full.ID, Name: full.Name, Email: full.Email, AvatarColor: full.AvatarColor}
			case full == nil && (err == nil || errors.Is(err, core.ErrNotFound)):
				u = &core.User{ID: c.UserID, Name: deletedUserName}
			default:
				a.log.Debug("load comment author failed", "userId", c.UserID, "err", err)
			}
			authors[c.UserID] = u
		}
		c.Author = u
	}
	roots := threadComments(list)
	if note != nil {
		a.resolveCommentAnchors(ctx, note, roots)
	}
	sortCommentRoots(roots)
	return roots, nil
}

// threadComments nests replies under their parents and orders each level by
// creation time.
func threadComments(list []*core.Comment) []*core.Comment {
	byID := make(map[string]*core.Comment, len(list))
	for _, c := range list {
		byID[c.ID] = c
	}
	roots := make([]*core.Comment, 0)
	for _, c := range list {
		parent, ok := byID[c.ParentID]
		if c.ParentID == "" || !ok || parent == c {
			roots = append(roots, c)
			continue
		}
		parent.Children = append(parent.Children, c)
	}
	var order func(level []*core.Comment, depth int)
	order = func(level []*core.Comment, depth int) {
		if depth > 256 {
			return
		}
		sort.SliceStable(level, func(i, j int) bool { return level[i].CreatedAt.Before(level[j].CreatedAt) })
		for _, c := range level {
			order(c.Children, depth+1)
		}
	}
	order(roots, 0)
	return roots
}

// CreateComment adds a comment to an item of any kind. It needs a signed-in
// user with AccessComment on the item. A root comment may carry an anchor, which must
// name text found in the current body. A reply may not.
func (a *App) CreateComment(ctx context.Context, actor *core.Actor, in core.CommentInput) (*core.Comment, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	it, _, err := a.requireItem(ctx, actor, in.NoteID, core.AccessComment)
	if err != nil {
		return nil, err
	}
	note := it.Note
	if note == nil && in.Anchor != nil {
		return nil, core.Invalid("anchor", "only a comment on a note can be anchored to a passage")
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return nil, core.Invalid("body", "comment body is required")
	}
	if len(body) > maxCommentBytes {
		return nil, fmt.Errorf("comment body: %w", core.ErrTooLarge)
	}
	if in.Anchor != nil && in.ParentID != "" {
		return nil, core.Invalid("anchor", "a reply cannot be anchored to a passage")
	}
	if in.ParentID != "" {
		parent, err := a.db.GetComment(ctx, in.ParentID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return nil, fmt.Errorf("load parent comment: %w", err)
		}
		if parent == nil || parent.NoteID != it.ID() {
			return nil, core.Invalid("parentId", "the parent comment is not on this item")
		}
	}
	var anchored *core.CommentAnchor
	if in.Anchor != nil {
		if anchored, err = a.commentAnchor(ctx, note, *in.Anchor); err != nil {
			return nil, err
		}
	}
	now := a.now()
	c := &core.Comment{
		ID:        core.NewID(),
		NoteID:    it.ID(),
		SpaceID:   it.SpaceID(),
		UserID:    actor.User.ID,
		ParentID:  in.ParentID,
		Body:      body,
		Anchor:    anchored,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := a.db.CreateItemComment(ctx, it.Kind, c); err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
	}
	if anchored != nil {
		c.AnchorState = core.AnchorExact
	}
	c.Author = &core.User{ID: actor.User.ID, Name: actor.User.Name, Email: actor.User.Email, AvatarColor: actor.User.AvatarColor}
	a.publishItem("comment.created", it.Kind, it.SpaceID(), it.ID(), actor.User.ID, it.Path())
	return c, nil
}

// commentForChange loads a comment the actor may change, with its note: the
// author, or a holder of AccessManage on the note.
func (a *App) commentForChange(ctx context.Context, actor *core.Actor, id string) (*core.Comment, *core.Item, error) {
	if err := requireUser(actor); err != nil {
		return nil, nil, err
	}
	c, err := a.db.GetComment(ctx, id)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, nil, fmt.Errorf("load comment: %w", err)
	}
	if c == nil {
		return nil, nil, fmt.Errorf("comment %s: %w", id, core.ErrNotFound)
	}
	it, grant, err := a.requireItemAny(ctx, actor, c.NoteID, core.AccessView)
	if err != nil {
		return nil, nil, err
	}
	if c.UserID != actor.User.ID && !grant.Access.AtLeast(core.AccessManage) {
		return nil, nil, fmt.Errorf("comment %s belongs to another user: %w", id, core.ErrForbidden)
	}
	return c, it, nil
}

// UpdateComment edits a comment body. Only the author or a note manager may,
// and not while the note is in the trash.
func (a *App) UpdateComment(ctx context.Context, actor *core.Actor, id, body string) (*core.Comment, error) {
	c, it, err := a.commentForChange(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if itemDeleted(it) {
		return nil, fmt.Errorf("item %s is in the trash: %w", it.ID(), core.ErrConflict)
	}
	if c.DeletedAt != nil {
		return nil, fmt.Errorf("comment %s is deleted: %w", id, core.ErrConflict)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, core.Invalid("body", "comment body is required")
	}
	if len(body) > maxCommentBytes {
		return nil, fmt.Errorf("comment body: %w", core.ErrTooLarge)
	}
	c.Body = body
	now := a.now()
	c.UpdatedAt, c.EditedAt = now, &now
	if err := a.db.UpdateComment(ctx, c); err != nil {
		return nil, fmt.Errorf("update comment: %w", err)
	}
	return c, nil
}

// DeleteComment removes a comment. A comment with replies becomes a tombstone
// so the thread keeps its shape. Only the author or a note manager may delete.
func (a *App) DeleteComment(ctx context.Context, actor *core.Actor, id string) error {
	c, _, err := a.commentForChange(ctx, actor, id)
	if err != nil {
		return err
	}
	siblings, err := a.db.ListComments(ctx, c.NoteID)
	if err != nil {
		return fmt.Errorf("list comments: %w", err)
	}
	hasReplies := false
	for _, s := range siblings {
		if s.ParentID == c.ID {
			hasReplies = true
			break
		}
	}
	if !hasReplies {
		if err := a.db.DeleteComment(ctx, c.ID); err != nil {
			return fmt.Errorf("delete comment: %w", err)
		}
		return nil
	}
	now := a.now()
	c.Body = ""
	c.DeletedAt = &now
	c.UpdatedAt = now
	if err := a.db.UpdateComment(ctx, c); err != nil {
		return fmt.Errorf("tombstone comment: %w", err)
	}
	return nil
}

// ResolveComment marks a thread resolved or open. It needs AccessComment.
func (a *App) ResolveComment(ctx context.Context, actor *core.Actor, id string, resolved bool) (*core.Comment, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	c, err := a.db.GetComment(ctx, id)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load comment: %w", err)
	}
	if c == nil {
		return nil, fmt.Errorf("comment %s: %w", id, core.ErrNotFound)
	}
	if _, _, err := a.requireItem(ctx, actor, c.NoteID, core.AccessComment); err != nil {
		return nil, err
	}
	c.Resolved = resolved
	c.ResolvedBy = ""
	if resolved {
		c.ResolvedBy = actor.User.ID
	}
	c.UpdatedAt = a.now()
	if err := a.db.UpdateComment(ctx, c); err != nil {
		return nil, fmt.Errorf("resolve comment: %w", err)
	}
	return c, nil
}
