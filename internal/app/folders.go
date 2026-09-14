package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/links"
)

// replayOf returns the stored item for a client id that already exists. The
// same creator in the same space gets it back, anybody else ErrExists. It
// returns nil, nil for an unused id.
func (a *App) replayOf(ctx context.Context, actor *core.Actor, id string, kind core.ItemKind, spaceID string) (*core.Item, error) {
	if !core.ValidID(id) {
		return nil, core.Invalid("id", "id must be 26 characters from the lowercase base32 alphabet")
	}
	got, err := a.loadItem(ctx, id)
	if errors.Is(err, core.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("check item id: %w", err)
	}
	if got.Kind == kind && got.SpaceID() == spaceID && itemCreatedBy(got) == actor.UserID() && actor.UserID() != "" {
		return got, nil
	}
	return nil, fmt.Errorf("item %s: %w", id, core.ErrExists)
}

// CreateFolder makes a folder on disk and in the index. It needs edit access
// on the parent folder, or on the space at the root.
func (a *App) CreateFolder(ctx context.Context, actor *core.Actor, in core.FolderInput) (*core.Folder, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	space, parent, err := a.resolveParent(ctx, actor, in.SpaceID, in.ParentID, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	if err := a.checkName(core.KindFolder, in.Name); err != nil {
		return nil, err
	}
	id := core.NewID()
	if in.ID != "" {
		got, err := a.replayOf(ctx, actor, in.ID, core.KindFolder, space.ID)
		if err != nil {
			return nil, err
		}
		if got != nil {
			got.Folder.SpaceSlug = space.Slug
			return got.Folder, nil
		}
		id = in.ID
	}
	// The checks are done. The folder and its index row finish together even
	// when the client disconnects.
	ctx = context.WithoutCancel(ctx)
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	f, err := a.makeFolder(ctx, actor, space, parent, id, in.Name, 0)
	if err != nil {
		return nil, err
	}
	a.log.Info("folder created", "folderId", f.ID, "space", space.Slug, "path", f.Path, "by", actor.UserID())
	a.publishItem("folder.created", core.KindFolder, space.ID, f.ID, actor.UserID(), f.Path)
	a.publishItem("tree.changed", core.KindFolder, space.ID, f.ID, actor.UserID(), f.Path)
	return f, nil
}

// makeFolder creates one folder with the space lock held. A zero position
// appends.
func (a *App) makeFolder(ctx context.Context, actor *core.Actor, space *core.Space, parent *core.Folder, id, name string, position int) (*core.Folder, error) {
	dir := folderPath(parent)
	taken, err := a.nameTaken(ctx, space, dir, name, "", "")
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, fmt.Errorf("name %q in %q: %w", name, dir, core.ErrExists)
	}
	rel := joinRel(dir, name)
	if err := a.files.MakeDir(space.Slug, rel); err != nil {
		return nil, fmt.Errorf("make folder: %w", err)
	}
	a.markWrite(space.Slug, rel)
	if position == 0 {
		position = a.nextPosition(ctx, space.ID, folderID(parent))
	}
	now := a.now()
	f := &core.Folder{
		ID: id, SpaceID: space.ID, ParentID: folderID(parent), Name: name, Path: rel,
		Position: position, CreatedAt: now, UpdatedAt: now, CreatedBy: actor.UserID(),
	}
	if err := a.db.CreateFolder(ctx, f); err != nil {
		if rerr := a.files.RemoveEntry(space.Slug, rel); rerr != nil {
			a.log.Warn("remove orphan folder failed", "space", space.Slug, "path", rel, "err", rerr)
		}
		return nil, fmt.Errorf("record folder: %w", err)
	}
	f.SpaceSlug = space.Slug
	return f, nil
}

// ensureFolderPath returns the folder at rel, creating each missing folder
// below the existing ones with the space lock held. The actor needs edit
// access on the folder that receives each new one.
func (a *App) ensureFolderPath(ctx context.Context, actor *core.Actor, space *core.Space, rel string) (*core.Folder, error) {
	if rel == "" {
		return nil, nil
	}
	var parent *core.Folder
	cur := ""
	for _, seg := range strings.Split(rel, "/") {
		cur = joinRel(cur, seg)
		f, err := a.db.GetFolderByPath(ctx, space.ID, cur)
		if err == nil && f != nil {
			parent = f
			continue
		}
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return nil, fmt.Errorf("load folder %s: %w", cur, err)
		}
		if _, err := a.requireContainer(ctx, actor, space, folderID(parent), core.AccessEdit); err != nil {
			return nil, err
		}
		if err := a.checkName(core.KindFolder, seg); err != nil {
			return nil, err
		}
		made, err := a.makeFolder(ctx, actor, space, parent, core.NewID(), seg, 0)
		if err != nil {
			return nil, err
		}
		a.publishItem("folder.created", core.KindFolder, space.ID, made.ID, actor.UserID(), made.Path)
		parent = made
	}
	return parent, nil
}

// RenameFolder renames a folder in place and rewrites links.
func (a *App) RenameFolder(ctx context.Context, actor *core.Actor, id, name string) (*core.Folder, error) {
	it, _, err := a.requireItem(ctx, actor, id, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	if it.Folder == nil {
		return nil, fmt.Errorf("item %s is not a folder: %w", id, core.ErrNotFound)
	}
	out, err := a.renameItem(ctx, actor, it, name)
	if err != nil {
		return nil, err
	}
	return out.Folder, nil
}

// renameItem renames a folder or file within its folder, then rewrites the
// links the rename affects. Renaming a note goes through its title.
func (a *App) renameItem(ctx context.Context, actor *core.Actor, it *core.Item, name string) (*core.Item, error) {
	if it.Note != nil {
		return nil, core.Invalid("name", "a note is renamed through its title")
	}
	if err := a.checkName(it.Kind, name); err != nil {
		return nil, err
	}
	space, err := a.space(ctx, it.SpaceID())
	if err != nil {
		return nil, err
	}
	// The checks are done. The move on disk, the index row and the link
	// rewrites finish together even when the client disconnects.
	ctx = context.WithoutCancel(ctx)
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	// Reload under the lock, so a concurrent move does not act on a stale path.
	fresh, err := a.loadItem(ctx, it.ID())
	if err != nil {
		return nil, err
	}
	fresh.Access = it.Access
	if itemDeleted(fresh) {
		return nil, fmt.Errorf("item %s is in the trash: %w", it.ID(), core.ErrConflict)
	}
	if err := a.renameLocked(ctx, actor, space, fresh, name); err != nil {
		return nil, err
	}
	return decorateItem(space, fresh), nil
}

// renameLocked renames a folder or file with the space lock held and updates
// it in place.
func (a *App) renameLocked(ctx context.Context, actor *core.Actor, space *core.Space, it *core.Item, name string) error {
	oldRel := it.Path()
	dir := dirOf(oldRel)
	newRel := joinRel(dir, name)
	if newRel == oldRel {
		return nil
	}
	taken, err := a.nameTaken(ctx, space, dir, name, it.ID(), oldRel)
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("name %q in %q: %w", name, dir, core.ErrExists)
	}
	base, err := a.loadRewriteBase(ctx, space.ID)
	if err != nil {
		return err
	}
	if err := a.files.MoveEntry(space.Slug, oldRel, newRel); err != nil {
		return fmt.Errorf("rename entry: %w", err)
	}
	a.markTree(space.Slug, oldRel)
	a.markTree(space.Slug, newRel)
	now := a.now()
	switch {
	case it.Folder != nil:
		next := *it.Folder
		next.Name, next.Path, next.UpdatedAt = name, newRel, now
		err = a.db.UpdateFolder(ctx, &next)
		if err == nil {
			*it.Folder = next
		}
	case it.File != nil:
		next := *it.File
		next.Name, next.Path, next.UpdatedAt, next.UpdatedBy = name, newRel, now, actor.UserID()
		err = a.db.UpdateFile(ctx, &next)
		if err == nil {
			*it.File = next
		}
	}
	if err != nil {
		if merr := a.files.MoveEntry(space.Slug, newRel, oldRel); merr != nil {
			a.log.Warn("undo rename on disk failed", "space", space.Slug, "from", newRel, "to", oldRel, "err", merr)
		}
		return fmt.Errorf("record rename: %w", err)
	}
	a.applyRewrites(ctx, actor, space, base, []links.Change{{Kind: "rename", ID: it.ID(), OldPath: oldRel, NewPath: newRel}})
	a.log.Info("item renamed", "itemId", it.ID(), "kind", it.Kind, "from", oldRel, "to", newRel, "by", actor.UserID())
	a.publishItem(kindEvent(it.Kind, "updated"), it.Kind, space.ID, it.ID(), actor.UserID(), newRel)
	a.publishItem("item.moved", it.Kind, space.ID, it.ID(), actor.UserID(), newRel)
	a.publishItem("tree.changed", it.Kind, space.ID, it.ID(), actor.UserID(), newRel)
	return nil
}
