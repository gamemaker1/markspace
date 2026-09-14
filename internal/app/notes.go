package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/links"
)

const (
	// defaultTitle is the title of a note created without one.
	defaultTitle = "Untitled"
	// positionStep is the gap left between sibling positions.
	positionStep = 100
	// keepVersions is how many snapshots a note keeps.
	keepVersions = 50
	// snapshotInterval is the age past which an edit takes a new snapshot.
	snapshotInterval = 5 * time.Minute
	// snapshotDelta is the length change that forces a snapshot.
	snapshotDelta = 200
	// conflictSlack absorbs clock and serialization jitter in the optimistic
	// lock check.
	conflictSlack = time.Second
)

// ListNotes lists the notes of a space the actor may view.
func (a *App) ListNotes(ctx context.Context, actor *core.Actor, spaceID string, opts core.NoteListOptions) ([]*core.Note, error) {
	space, grant, _, err := a.spaceView(ctx, actor, spaceID)
	if err != nil {
		return nil, err
	}
	notes, err := a.db.ListNotes(ctx, space.ID, opts)
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	notes, err = a.visibleNotes(ctx, actor, grant, notes)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		n.SpaceSlug = space.Slug
	}
	if opts.WithContent {
		for _, n := range notes {
			if err := a.noteBody(ctx, space, n); err != nil {
				a.log.Debug("load note body failed", "noteId", n.ID, "err", err)
			}
		}
	}
	return notes, nil
}

// GetNote returns one note with its body read from disk and the actor's
// resolved access. A missing file drops the stale index row.
func (a *App) GetNote(ctx context.Context, actor *core.Actor, id string) (*core.Note, *core.Grant, error) {
	note, grant, err := a.requireNoteAny(ctx, actor, id, core.AccessView)
	if err != nil {
		return nil, nil, err
	}
	space, err := a.space(ctx, note.SpaceID)
	if err != nil {
		return nil, nil, err
	}
	if err := a.noteBody(ctx, space, note); err != nil {
		return nil, nil, err
	}
	a.decorate(ctx, actor, space, note)
	return note, grant, nil
}

// GetNoteByPath resolves <spaceSlug>/<path>, where path omits the .md suffix.
func (a *App) GetNoteByPath(ctx context.Context, actor *core.Actor, spaceSlug, path string) (*core.Note, *core.Grant, error) {
	space, err := a.db.GetSpaceBySlug(ctx, spaceSlug)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, nil, fmt.Errorf("load space %s: %w", spaceSlug, err)
	}
	if space == nil {
		return nil, nil, fmt.Errorf("space %s: %w", spaceSlug, denied(actor))
	}
	rel := strings.TrimPrefix(path, "/")
	if !strings.HasSuffix(rel, ".md") {
		rel += ".md"
	}
	note, err := a.db.GetNoteByPath(ctx, space.ID, rel)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, nil, fmt.Errorf("load note %s: %w", rel, err)
	}
	if note == nil {
		return nil, nil, fmt.Errorf("note %s: %w", rel, denied(actor))
	}
	return a.GetNote(ctx, actor, note.ID)
}

// noteBody loads a note body from disk. A trashed note that heads its own
// trash entry is read from there, and its index row survives a missing file.
func (a *App) noteBody(ctx context.Context, space *core.Space, note *core.Note) error {
	if !note.Deleted {
		return a.loadBody(ctx, space, note)
	}
	if body, ok := a.trashedNoteBody(space, note); ok {
		note.Content = body
	}
	return nil
}

// trashedNoteBody returns the body of a trashed note from its trash entry.
func (a *App) trashedNoteBody(space *core.Space, note *core.Note) (string, bool) {
	slot, ok := a.trashSlot(space, note.ID)
	if !ok || slot.Note == nil {
		return "", false
	}
	return slot.Note.Content, true
}

// CreateNote writes a new markdown file and indexes it. A parent that is a
// note, other than a folder note, becomes the folder note of a new folder
// with its name, and the new note goes into that folder.
func (a *App) CreateNote(ctx context.Context, actor *core.Actor, in core.NoteInput) (*core.Note, error) {
	var (
		space   *core.Space
		parent  *core.Folder
		convert *core.Note
		err     error
	)
	switch {
	case in.FolderID != "" && !in.FolderNote:
		return nil, core.Invalid("folderId", "folderId is used only with folderNote")
	case in.FolderNote:
		if in.FolderID == "" {
			return nil, core.Invalid("folderId", "a folder note needs folderId")
		}
		if in.ParentID != "" && in.ParentID != in.FolderID {
			return nil, core.Invalid("parentId", "parentId must be empty or equal to folderId")
		}
		if space, parent, err = a.resolveParent(ctx, actor, in.SpaceID, in.FolderID, core.AccessEdit); err != nil {
			return nil, err
		}
	case in.ParentID == "":
		if space, _, err = a.requireSpace(ctx, actor, in.SpaceID, core.AccessEdit); err != nil {
			return nil, err
		}
	default:
		kind, _, kerr := a.db.ItemKind(ctx, in.ParentID)
		if errors.Is(kerr, core.ErrNotFound) {
			return nil, fmt.Errorf("parent %s: %w", in.ParentID, denied(actor))
		}
		if kerr != nil {
			return nil, fmt.Errorf("load parent: %w", kerr)
		}
		if kind != core.KindNote {
			if space, parent, err = a.resolveParent(ctx, actor, in.SpaceID, in.ParentID, core.AccessEdit); err != nil {
				return nil, err
			}
			break
		}
		pn, _, err := a.requireNote(ctx, actor, in.ParentID, core.AccessEdit)
		if err != nil {
			return nil, err
		}
		if in.SpaceID != "" && in.SpaceID != pn.SpaceID {
			return nil, core.Invalid("parentId", "parent note is in another space")
		}
		if space, err = a.space(ctx, pn.SpaceID); err != nil {
			return nil, err
		}
		if isFolderNotePath(pn.Path) {
			if parent, err = a.db.GetFolderByPath(ctx, space.ID, dirOf(pn.Path)); err != nil {
				return nil, fmt.Errorf("load folder of %s: %w", pn.ID, err)
			}
		} else {
			if _, err := a.requireContainer(ctx, actor, space, pn.ParentID, core.AccessEdit); err != nil {
				return nil, err
			}
			convert = pn
		}
	}

	id := core.NewID()
	if in.ID != "" {
		got, err := a.replayOf(ctx, actor, in.ID, core.KindNote, space.ID)
		if err != nil {
			return nil, err
		}
		if got != nil {
			stored, _, err := a.GetNote(ctx, actor, got.Note.ID)
			return stored, err
		}
		id = in.ID
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = defaultTitle
	}
	// The checks are done. A client that disconnects from here on must not
	// leave a file without its index row, which a replay would copy.
	ctx = context.WithoutCancel(ctx)
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	if convert != nil {
		if parent, err = a.convertToFolderNote(ctx, actor, space, convert); err != nil {
			return nil, err
		}
	}
	dir := folderPath(parent)
	var path string
	if in.FolderNote {
		if path, err = a.folderNotePath(ctx, space, dir, id); err != nil {
			return nil, err
		}
	} else if path, err = a.allocNotePath(ctx, space, dir, title, "", id); err != nil {
		return nil, fmt.Errorf("allocate note path: %w", err)
	}
	position := in.Position
	if position == 0 {
		position = a.nextPosition(ctx, space.ID, folderID(parent))
	}
	now := a.now()
	note := &core.Note{
		ID:         id,
		SpaceID:    space.ID,
		ParentID:   folderID(parent),
		Slug:       pathSlug(path),
		Path:       path,
		Title:      title,
		Icon:       strings.TrimSpace(in.Icon),
		Tags:       in.Tags,
		Content:    in.Content,
		Position:   position,
		CreatedAt:  now,
		UpdatedAt:  now,
		CreatedBy:  actor.UserID(),
		UpdatedBy:  actor.UserID(),
		UpdatedVia: actor.Via,
		Extra:      mergeExtra(nil, in.Extra),
	}
	if err := a.writeNote(space.Slug, note); err != nil {
		return nil, err
	}
	if err := a.indexNote(ctx, space, note); err != nil {
		return nil, err
	}
	if isFolderNotePath(path) {
		a.syncFolderNote(ctx, space.ID, dir)
	}
	note.SpaceSlug = space.Slug
	a.log.Info("note created", "noteId", note.ID, "space", space.Slug, "path", note.Path, "by", actor.UserID())
	a.publishItem("note.created", core.KindNote, space.ID, note.ID, actor.UserID(), note.Path)
	a.publishItem("tree.changed", core.KindNote, space.ID, note.ID, actor.UserID(), note.Path)
	return note, nil
}

// convertToFolderNote turns a note a/x.md into the folder note a/x/index.md
// with the space lock held, keeping its id, and returns the folder. A folder
// a/x that exists without a folder note takes the note. Links follow the
// move.
func (a *App) convertToFolderNote(ctx context.Context, actor *core.Actor, space *core.Space, note *core.Note) (*core.Folder, error) {
	fresh, err := a.db.GetNote(ctx, note.ID)
	if err != nil {
		return nil, err
	}
	if fresh.Deleted {
		return nil, fmt.Errorf("note %s is in the trash: %w", note.ID, core.ErrConflict)
	}
	if isFolderNotePath(fresh.Path) {
		return a.db.GetFolderByPath(ctx, space.ID, dirOf(fresh.Path))
	}
	dir := dirOf(fresh.Path)
	stem := strings.TrimSuffix(baseOf(fresh.Path), ".md")
	folderRel := joinRel(dir, stem)
	base, err := a.loadRewriteBase(ctx, space.ID)
	if err != nil {
		return nil, err
	}
	var folder *core.Folder
	if f, err := a.db.GetFolderByPath(ctx, space.ID, folderRel); err == nil && f != nil && f.Path == folderRel {
		if n, err := a.db.GetNoteByPath(ctx, space.ID, joinRel(folderRel, folderNoteName)); err == nil && n != nil && !n.Deleted {
			return nil, fmt.Errorf("folder %s already has a folder note: %w", folderRel, core.ErrExists)
		}
		folder = f
	} else {
		if err := a.checkName(core.KindFolder, stem); err != nil {
			return nil, err
		}
		var holder *core.Folder
		if fresh.ParentID != "" {
			if holder, err = a.db.GetFolder(ctx, fresh.ParentID); err != nil {
				return nil, fmt.Errorf("load folder of %s: %w", fresh.ID, err)
			}
		}
		if folder, err = a.makeFolder(ctx, actor, space, holder, core.NewID(), stem, fresh.Position); err != nil {
			return nil, err
		}
		a.publishItem("folder.created", core.KindFolder, space.ID, folder.ID, actor.UserID(), folder.Path)
	}
	n := *fresh
	if err := a.loadForWrite(ctx, space, &n); err != nil {
		return nil, err
	}
	oldRel := fresh.Path
	newRel := joinRel(folder.Path, folderNoteName)
	if err := a.files.MoveEntry(space.Slug, oldRel, newRel); err != nil {
		return nil, fmt.Errorf("move note into its folder: %w", err)
	}
	a.markWrite(space.Slug, oldRel)
	a.markWrite(space.Slug, newRel)
	n.Path, n.Slug, n.ParentID = newRel, pathSlug(newRel), folder.ID
	n.UpdatedAt, n.UpdatedBy, n.UpdatedVia = a.now(), actor.UserID(), actor.Via
	if err := a.writeNote(space.Slug, &n); err != nil {
		return nil, err
	}
	if err := a.indexNote(ctx, space, &n); err != nil {
		return nil, err
	}
	a.syncFolderNote(ctx, space.ID, folder.Path)
	a.applyRewrites(ctx, actor, space, base, []links.Change{{Kind: "move", ID: n.ID, OldPath: oldRel, NewPath: newRel}})
	a.log.Info("note became a folder note", "noteId", n.ID, "folderId", folder.ID, "path", newRel, "by", actor.UserID())
	a.publishItem("item.moved", core.KindNote, space.ID, n.ID, actor.UserID(), newRel)
	a.publishItem("note.updated", core.KindNote, space.ID, n.ID, actor.UserID(), newRel)
	return a.db.GetFolder(ctx, folder.ID)
}

// UpdateNote applies a change to a note, snapshots the previous body when the
// edit is worth keeping, and renames the file when the title changes. A
// folder note keeps the name index.md.
func (a *App) UpdateNote(ctx context.Context, actor *core.Actor, id string, in core.NoteUpdate) (*core.Note, error) {
	checked, _, err := a.requireNote(ctx, actor, id, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	space, err := a.space(ctx, checked.SpaceID)
	if err != nil {
		return nil, err
	}
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	note, err := a.db.GetNote(ctx, id)
	if err != nil {
		return nil, err
	}
	if note.Deleted {
		return nil, fmt.Errorf("note %s is in the trash: %w", id, core.ErrConflict)
	}
	stale := in.BaseUpdatedAt != nil && !in.BaseUpdatedAt.IsZero() &&
		note.UpdatedAt.After(in.BaseUpdatedAt.Add(conflictSlack))
	indexTags := note.Tags
	if err := a.loadForWrite(ctx, space, note); err != nil {
		return nil, err
	}
	if stale {
		// A client replays an update whose response it never received. When
		// the note already holds every requested value, the update is not a
		// conflict and the note comes back unchanged.
		if !updateApplied(note, indexTags, in) {
			return nil, fmt.Errorf("note %s changed at %s: %w",
				id, note.UpdatedAt.Format(time.RFC3339Nano), core.ErrConflict)
		}
		note.Tags = indexTags
		a.decorate(ctx, actor, space, note)
		return note, nil
	}
	// The checks are done. A client that disconnects from here on must not
	// leave the file written and the index stale.
	ctx = context.WithoutCancel(ctx)

	oldBody := note.Content
	newBody := oldBody
	if in.Content != nil {
		newBody = *in.Content
	}
	oldTitle := note.Title
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			title = defaultTitle
		}
		note.Title = title
	}
	if in.Icon != nil {
		note.Icon = strings.TrimSpace(*in.Icon)
	}
	if in.Tags != nil {
		note.Tags = *in.Tags
	}
	if in.Extra != nil {
		note.Extra = mergeExtra(note.Extra, in.Extra)
	}

	a.maybeSnapshot(space, note, oldBody, newBody, in.Snapshot)

	note.Content = newBody
	note.UpdatedAt = a.now()
	note.UpdatedBy, note.UpdatedVia = actor.UserID(), actor.Via

	var (
		base    *rewriteBase
		oldPath = note.Path
	)
	if note.Title != oldTitle && baseOf(note.Path) != folderNoteName {
		newPath, err := a.allocNotePath(ctx, space, dirOf(note.Path), note.Title, note.Path, note.ID)
		if err != nil {
			return nil, fmt.Errorf("allocate note path: %w", err)
		}
		if newPath != note.Path {
			// The snapshot carries the new title at the old path, because a
			// change list moves paths only. A link that matched the old stem
			// then counts as broken by the rename and is rewritten.
			titled := links.Entry{ID: note.ID, Kind: links.KindNote, Path: oldPath, Title: note.Title}
			if base, err = a.loadRewriteBase(ctx, space.ID, titled); err != nil {
				return nil, err
			}
			if err := a.files.MoveEntry(space.Slug, oldPath, newPath); err != nil {
				return nil, fmt.Errorf("rename note file: %w", err)
			}
			a.markWrite(space.Slug, oldPath)
			note.Path = newPath
			note.Slug = pathSlug(newPath)
			a.log.Info("note renamed", "noteId", note.ID, "from", oldPath, "to", newPath)
		}
	}
	if err := a.writeNote(space.Slug, note); err != nil {
		return nil, err
	}
	if err := a.indexNote(ctx, space, note); err != nil {
		return nil, err
	}
	a.remapCommentAnchors(ctx, note.ID, oldBody, newBody)
	if base != nil {
		a.applyRewrites(ctx, actor, space, base, []links.Change{{Kind: "rename", ID: note.ID, OldPath: oldPath, NewPath: note.Path}})
		if fresh, err := a.db.GetNote(ctx, id); err == nil && fresh.UpdatedAt.After(note.UpdatedAt) {
			if err := a.loadForWrite(ctx, space, fresh); err == nil {
				note = fresh
			}
		}
	}
	note.SpaceSlug = space.Slug
	a.publishItem("note.updated", core.KindNote, space.ID, note.ID, actor.UserID(), note.Path)
	if base != nil {
		a.publishItem("item.moved", core.KindNote, space.ID, note.ID, actor.UserID(), note.Path)
		a.publishItem("tree.changed", core.KindNote, space.ID, note.ID, actor.UserID(), note.Path)
	}
	return note, nil
}

// updateApplied reports whether every field an update sets already holds the
// requested value. note carries the fields the file owns. indexTags are the
// tags of the index row, which also hold inline hashtags.
func updateApplied(note *core.Note, indexTags []string, in core.NoteUpdate) bool {
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			title = defaultTitle
		}
		if title != note.Title {
			return false
		}
	}
	if in.Content != nil && *in.Content != note.Content {
		return false
	}
	if in.Icon != nil && strings.TrimSpace(*in.Icon) != note.Icon {
		return false
	}
	if in.Tags != nil && !slices.Equal(*in.Tags, note.Tags) && !slices.Equal(*in.Tags, indexTags) {
		return false
	}
	if in.Extra != nil && !sameExtra(mergeExtra(note.Extra, in.Extra), note.Extra) {
		return false
	}
	return true
}

// sameExtra compares two sets of extra frontmatter keys by their JSON form.
// A value that does not encode counts as different.
func sameExtra(x, y map[string]any) bool {
	if len(x) == 0 || len(y) == 0 {
		return len(x) == len(y)
	}
	jx, errX := json.Marshal(x)
	jy, errY := json.Marshal(y)
	return errX == nil && errY == nil && bytes.Equal(jx, jy)
}

// maybeSnapshot writes a version of the previous body when the edit is forced,
// the last snapshot is old, or the body length moved by more than 200 bytes.
func (a *App) maybeSnapshot(space *core.Space, note *core.Note, oldBody, newBody string, force bool) {
	if oldBody == newBody {
		return
	}
	should := force
	if !should {
		delta := len(newBody) - len(oldBody)
		if delta < 0 {
			delta = -delta
		}
		should = delta > snapshotDelta
	}
	if !should {
		versions, err := a.files.ListVersions(space.ID, note.ID)
		if err != nil {
			a.log.Debug("list versions failed", "noteId", note.ID, "err", err)
			should = true
		} else if len(versions) == 0 || a.now().Sub(versions[0].CreatedAt) > snapshotInterval {
			should = true
		}
	}
	if !should {
		return
	}
	prev := *note
	prev.Content = oldBody
	if _, err := a.files.SaveVersion(space.ID, &prev, ""); err != nil {
		a.log.Warn("save version failed", "noteId", note.ID, "err", err)
		return
	}
	if err := a.files.PruneVersions(space.ID, note.ID, keepVersions); err != nil {
		a.log.Warn("prune versions failed", "noteId", note.ID, "err", err)
	}
}

// MoveNote moves a note into a folder, "" for the space root.
func (a *App) MoveNote(ctx context.Context, actor *core.Actor, id, newParentID string, position int) (*core.Note, error) {
	if _, _, err := a.requireNote(ctx, actor, id, core.AccessEdit); err != nil {
		return nil, err
	}
	if _, err := a.MoveItem(ctx, actor, id, newParentID, position); err != nil {
		return nil, err
	}
	note, _, err := a.GetNote(ctx, actor, id)
	return note, err
}

// DuplicateNote copies a note into its own folder under a copy title.
func (a *App) DuplicateNote(ctx context.Context, actor *core.Actor, id string) (*core.Note, error) {
	src, _, err := a.requireNote(ctx, actor, id, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	it, err := a.CopyItem(ctx, actor, src.ID, core.CopyInput{ParentID: src.ParentID})
	if err != nil {
		return nil, err
	}
	return it.Note, nil
}

// TrashNote moves a note into the space trash.
func (a *App) TrashNote(ctx context.Context, actor *core.Actor, id string) error {
	if _, _, err := a.requireNote(ctx, actor, id, core.AccessEdit); err != nil {
		return err
	}
	return a.TrashItem(ctx, actor, id)
}

// RestoreNote returns a trashed note, see RestoreItem.
func (a *App) RestoreNote(ctx context.Context, actor *core.Actor, id string) error {
	if _, _, err := a.requireNoteAny(ctx, actor, id, core.AccessEdit); err != nil {
		return err
	}
	_, err := a.RestoreItem(ctx, actor, id)
	return err
}

// DeleteNote removes a note for good, see DeleteItem.
func (a *App) DeleteNote(ctx context.Context, actor *core.Actor, id string) error {
	if _, _, err := a.requireNoteAny(ctx, actor, id, core.AccessView); err != nil {
		return err
	}
	return a.DeleteItem(ctx, actor, id)
}

// ListTrash lists the trashed notes of a space.
func (a *App) ListTrash(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.Note, error) {
	space, grant, _, err := a.spaceView(ctx, actor, spaceID)
	if err != nil {
		return nil, err
	}
	notes, err := a.db.ListNotes(ctx, space.ID, core.NoteListOptions{OnlyTrash: true, IncludeTrash: true})
	if err != nil {
		return nil, fmt.Errorf("list trash: %w", err)
	}
	notes, err = a.visibleNotes(ctx, actor, grant, notes)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		n.SpaceSlug = space.Slug
	}
	return notes, nil
}

// SetStar stars or unstars a note for the actor.
func (a *App) SetStar(ctx context.Context, actor *core.Actor, noteID string, starred bool) error {
	if err := requireUser(actor); err != nil {
		return err
	}
	if _, _, err := a.requireNoteAny(ctx, actor, noteID, core.AccessView); err != nil {
		return err
	}
	if err := a.db.SetStar(ctx, actor.User.ID, noteID, starred); err != nil {
		return fmt.Errorf("set star: %w", err)
	}
	return nil
}

// ListStarred returns the actor's starred notes they may still view.
func (a *App) ListStarred(ctx context.Context, actor *core.Actor) ([]*core.Note, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	notes, err := a.db.ListStarred(ctx, actor.User.ID)
	if err != nil {
		return nil, fmt.Errorf("list starred: %w", err)
	}
	return a.visibleNotes(ctx, actor, noAccess(), notes)
}

// RecentNotes returns the most recently updated notes across the spaces the
// actor may read.
func (a *App) RecentNotes(ctx context.Context, actor *core.Actor, limit int) ([]*core.Note, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	ids, err := a.accessibleSpaceIDs(ctx, actor)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	notes, err := a.db.RecentNotes(ctx, ids, limit)
	if err != nil {
		return nil, fmt.Errorf("recent notes: %w", err)
	}
	return a.visibleNotes(ctx, actor, noAccess(), notes)
}

// Backlinks lists the notes linking to a note, filtered by what the actor may
// view.
func (a *App) Backlinks(ctx context.Context, actor *core.Actor, noteID string) ([]*core.Backlink, error) {
	if _, _, err := a.requireNoteAny(ctx, actor, noteID, core.AccessView); err != nil {
		return nil, err
	}
	return a.ItemBacklinks(ctx, actor, noteID)
}

// ListTags counts the tags of one space, or of every space the actor may read
// when spaceID is empty.
func (a *App) ListTags(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.TagCount, error) {
	var ids []string
	if spaceID != "" {
		space, _, _, err := a.spaceView(ctx, actor, spaceID)
		if err != nil {
			return nil, err
		}
		ids = []string{space.ID}
	} else {
		var err error
		ids, err = a.accessibleSpaceIDs(ctx, actor)
		if err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	tags, err := a.db.ListTags(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	return tags, nil
}

// allocNotePath picks a free "<slug>.md" path for a note in a folder. The
// store checks the disk. A name another live index row holds is skipped as
// well, by numbering the title. noteID is the note being placed and exceptRel
// its current path, neither of which counts as a collision.
func (a *App) allocNotePath(ctx context.Context, space *core.Space, dir, title, exceptRel, noteID string) (string, error) {
	try := title
	for i := 2; i < 1000; i++ {
		p, err := a.files.AllocNotePath(space.Slug, dir, try, exceptRel)
		if err != nil {
			return "", err
		}
		taken, err := a.db.SiblingNameTaken(ctx, space.ID, dir, baseOf(p), noteID)
		if err != nil {
			return "", fmt.Errorf("check note path: %w", err)
		}
		if !taken {
			return p, nil
		}
		try = fmt.Sprintf("%s %d", title, i)
	}
	return "", fmt.Errorf("no free path for %q: %w", title, core.ErrConflict)
}

// folderNotePath returns <dir>/index.md with the space lock held. It fails
// with ErrExists when the folder already has an entry named index.md, in the
// index or on disk.
func (a *App) folderNotePath(ctx context.Context, space *core.Space, dir, noteID string) (string, error) {
	p := joinRel(dir, folderNoteName)
	taken, err := a.db.SiblingNameTaken(ctx, space.ID, dir, folderNoteName, noteID)
	if err != nil {
		return "", fmt.Errorf("check folder note path: %w", err)
	}
	if taken || a.files.Exists(space.Slug, p) {
		return "", fmt.Errorf("folder %q already has a folder note: %w", dir, core.ErrExists)
	}
	return p, nil
}

// maxExtraKeys bounds the extra frontmatter keys of one note.
const maxExtraKeys = 100

// mergeExtra applies extra frontmatter to a note's extra keys. Owned keys are
// skipped and a nil value removes a key. It returns nil for an empty result.
func mergeExtra(cur, in map[string]any) map[string]any {
	if len(in) == 0 {
		return cur
	}
	out := make(map[string]any, len(cur)+len(in))
	for k, v := range cur {
		out[k] = v
	}
	for k, v := range in {
		k = strings.TrimSpace(k)
		if k == "" || slices.Contains(core.OwnedFrontmatter, strings.ToLower(k)) {
			continue
		}
		if v == nil {
			delete(out, k)
			continue
		}
		if _, ok := out[k]; !ok && len(out) >= maxExtraKeys {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
