package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/links"
	"github.com/gamemaker1/markspace/internal/ports"
)

// MoveItem moves an item to another folder of its space, or reorders it in
// its folder when the parent does not change. It needs edit access on the
// item, on its old parent and on its new parent, and rewrites links.
func (a *App) MoveItem(ctx context.Context, actor *core.Actor, id, parentID string, position int) (*core.Item, error) {
	it, _, err := a.requireItem(ctx, actor, id, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	if position < 0 {
		return nil, core.Invalid("position", "position must not be negative")
	}
	if parentID == id {
		return nil, core.Invalid("parentId", "an item cannot be its own parent")
	}
	space, err := a.space(ctx, it.SpaceID())
	if err != nil {
		return nil, err
	}
	if _, err := a.requireContainer(ctx, actor, space, itemParentID(it), core.AccessEdit); err != nil {
		return nil, err
	}
	parent, err := a.requireContainer(ctx, actor, space, parentID, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	fresh, err := a.loadItem(ctx, id)
	if err != nil {
		return nil, err
	}
	if itemDeleted(fresh) {
		return nil, fmt.Errorf("item %s is in the trash: %w", id, core.ErrConflict)
	}
	if parent != nil {
		if parent, err = a.db.GetFolder(ctx, parent.ID); err != nil {
			return nil, err
		}
		if parent.Deleted {
			return nil, fmt.Errorf("folder %s is in the trash: %w", parent.ID, core.ErrConflict)
		}
	}
	if err := a.moveLocked(ctx, actor, space, fresh, parent, position); err != nil {
		return nil, err
	}
	out, err := a.loadItem(ctx, id)
	if err != nil {
		return nil, err
	}
	out.Access = it.Access
	return decorateItem(space, out), nil
}

// moveLocked moves an item with the space lock held.
func (a *App) moveLocked(ctx context.Context, actor *core.Actor, space *core.Space, it *core.Item, parent *core.Folder, position int) error {
	oldRel := it.Path()
	name := baseOf(oldRel)
	newDir := folderPath(parent)
	newRel := joinRel(newDir, name)
	if it.Folder != nil && parent != nil && (parent.ID == it.ID() || parent.Path == oldRel || underPath(parent.Path, oldRel)) {
		return core.Invalid("parentId", "a folder cannot move into itself")
	}
	now := a.now()
	if newRel == oldRel {
		return a.reorderLocked(ctx, actor, space, it, position)
	}
	taken, err := a.nameTaken(ctx, space, newDir, name, it.ID(), "")
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("name %q in %q: %w", name, newDir, core.ErrExists)
	}
	base, err := a.loadRewriteBase(ctx, space.ID)
	if err != nil {
		return err
	}
	if position == 0 {
		position = a.nextPosition(ctx, space.ID, folderID(parent))
	}
	var note *core.Note
	if it.Note != nil {
		n := *it.Note
		if err := a.loadForWrite(ctx, space, &n); err != nil {
			return err
		}
		note = &n
	}
	if err := a.files.MoveEntry(space.Slug, oldRel, newRel); err != nil {
		return fmt.Errorf("move entry: %w", err)
	}
	a.markTree(space.Slug, oldRel)
	a.markTree(space.Slug, newRel)
	switch {
	case it.Folder != nil:
		next := *it.Folder
		next.ParentID, next.Path, next.Position, next.UpdatedAt = folderID(parent), newRel, position, now
		err = a.db.UpdateFolder(ctx, &next)
	case it.File != nil:
		next := *it.File
		next.ParentID, next.Path, next.Position = folderID(parent), newRel, position
		next.UpdatedAt, next.UpdatedBy = now, actor.UserID()
		err = a.db.UpdateFile(ctx, &next)
	case note != nil:
		note.Path, note.Slug, note.ParentID, note.Position = newRel, pathSlug(newRel), folderID(parent), position
		note.UpdatedAt, note.UpdatedBy, note.UpdatedVia = now, actor.UserID(), actor.Via
		if err = a.writeNote(space.Slug, note); err == nil {
			err = a.indexNote(ctx, space, note)
		}
	}
	if err != nil {
		if merr := a.files.MoveEntry(space.Slug, newRel, oldRel); merr != nil {
			a.log.Warn("undo move on disk failed", "space", space.Slug, "from", newRel, "to", oldRel, "err", merr)
		}
		return fmt.Errorf("record move: %w", err)
	}
	if name == folderNoteName {
		a.syncFolderNote(ctx, space.ID, dirOf(oldRel))
		a.syncFolderNote(ctx, space.ID, newDir)
	}
	a.applyRewrites(ctx, actor, space, base, []links.Change{{Kind: "move", ID: it.ID(), OldPath: oldRel, NewPath: newRel}})
	a.log.Info("item moved", "itemId", it.ID(), "kind", it.Kind, "from", oldRel, "to", newRel, "by", actor.UserID())
	a.publishItem("item.moved", it.Kind, space.ID, it.ID(), actor.UserID(), newRel)
	a.publishItem(kindEvent(it.Kind, "updated"), it.Kind, space.ID, it.ID(), actor.UserID(), newRel)
	a.publishItem("tree.changed", it.Kind, space.ID, it.ID(), actor.UserID(), newRel)
	return nil
}

// reorderLocked sets the position of an item in its own folder.
func (a *App) reorderLocked(ctx context.Context, actor *core.Actor, space *core.Space, it *core.Item, position int) error {
	if position == 0 {
		return nil
	}
	now := a.now()
	var err error
	switch {
	case it.Folder != nil:
		if it.Folder.Position == position {
			return nil
		}
		next := *it.Folder
		next.Position, next.UpdatedAt = position, now
		err = a.db.UpdateFolder(ctx, &next)
	case it.File != nil:
		if it.File.Position == position {
			return nil
		}
		next := *it.File
		next.Position, next.UpdatedAt, next.UpdatedBy = position, now, actor.UserID()
		err = a.db.UpdateFile(ctx, &next)
	case it.Note != nil:
		if it.Note.Position == position {
			return nil
		}
		n := *it.Note
		if err := a.loadForWrite(ctx, space, &n); err != nil {
			return err
		}
		n.Position, n.UpdatedAt, n.UpdatedBy, n.UpdatedVia = position, now, actor.UserID(), actor.Via
		if err = a.writeNote(space.Slug, &n); err == nil {
			err = a.indexNote(ctx, space, &n)
		}
	}
	if err != nil {
		return fmt.Errorf("record position: %w", err)
	}
	a.publishItem("tree.changed", it.Kind, space.ID, it.ID(), actor.UserID(), it.Path())
	return nil
}

// CopyItem copies an item into a folder of any space. A folder copy is
// recursive. Every copied note gets a new id in its frontmatter and every
// copied file a new id without versions.
func (a *App) CopyItem(ctx context.Context, actor *core.Actor, id string, in core.CopyInput) (*core.Item, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	src, _, err := a.requireItemAny(ctx, actor, id, core.AccessView)
	if err != nil {
		return nil, err
	}
	if itemDeleted(src) {
		return nil, fmt.Errorf("item %s is in the trash: %w", id, core.ErrConflict)
	}
	srcSpace, err := a.space(ctx, src.SpaceID())
	if err != nil {
		return nil, err
	}
	dstSpaceID := in.SpaceID
	if dstSpaceID == "" {
		dstSpaceID = srcSpace.ID
	}
	dst, parent, err := a.resolveParent(ctx, actor, dstSpaceID, in.ParentID, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	name := in.Name
	if name != "" && src.Note == nil {
		if err := a.checkName(src.Kind, name); err != nil {
			return nil, err
		}
	}
	unlock := a.lockSpaces(srcSpace.ID, dst.ID)
	defer unlock()
	var out *core.Item
	if src.Note != nil {
		n, err := a.copyNoteLocked(ctx, actor, srcSpace, src.Note, dst, parent, strings.TrimSpace(name))
		if err != nil {
			return nil, err
		}
		out = &core.Item{Kind: core.KindNote, Note: n}
	} else if out, err = a.copyEntryLocked(ctx, actor, srcSpace, src, dst, parent, name); err != nil {
		return nil, err
	}
	a.publishItem("tree.changed", out.Kind, dst.ID, out.ID(), actor.UserID(), out.Path())
	g, err := a.grants(actor).item(ctx, refOf(out))
	if err == nil {
		out.Access = g.Access
	}
	return decorateItem(dst, out), nil
}

// copyNoteLocked writes a copy of one note into a folder. An empty title keeps
// the source title, and a copy that would share its file name takes
// "<title> copy", then "<title> copy N". An explicit title that is taken
// returns ErrExists.
func (a *App) copyNoteLocked(ctx context.Context, actor *core.Actor, srcSpace *core.Space, src *core.Note, dst *core.Space, parent *core.Folder, title string) (*core.Note, error) {
	n := *src
	if err := a.loadForWrite(ctx, srcSpace, &n); err != nil {
		return nil, err
	}
	dir := folderPath(parent)
	explicit := title != ""
	base := title
	if !explicit {
		base = n.Title
	}
	newTitle := base
	var rel string
	for i := 1; ; i++ {
		fileName := core.Slugify(newTitle) + ".md"
		taken, err := a.nameTaken(ctx, dst, dir, fileName, "", "")
		if err != nil {
			return nil, err
		}
		if !taken {
			rel = joinRel(dir, fileName)
			break
		}
		if explicit || i > 1000 {
			return nil, fmt.Errorf("name %q in %q: %w", fileName, dir, core.ErrExists)
		}
		newTitle = base + " copy"
		if i > 1 {
			newTitle = fmt.Sprintf("%s copy %d", base, i)
		}
	}
	extra := map[string]any{}
	for k, v := range n.Extra {
		if k != "trashed_from" && k != "trash_path" {
			extra[k] = v
		}
	}
	now := a.now()
	copied := &core.Note{
		ID: core.NewID(), SpaceID: dst.ID, ParentID: folderID(parent), Slug: pathSlug(rel), Path: rel,
		Title: newTitle, Icon: n.Icon, Tags: n.Tags, Content: n.Content, Extra: extra,
		Position:  a.nextPosition(ctx, dst.ID, folderID(parent)),
		CreatedAt: now, UpdatedAt: now, CreatedBy: actor.UserID(), UpdatedBy: actor.UserID(),
		UpdatedVia: actor.Via,
	}
	if err := a.writeNote(dst.Slug, copied); err != nil {
		return nil, err
	}
	if err := a.indexNote(ctx, dst, copied); err != nil {
		return nil, err
	}
	a.log.Info("note copied", "from", src.ID, "noteId", copied.ID, "space", dst.Slug, "path", rel, "by", actor.UserID())
	a.publishItem("note.created", core.KindNote, dst.ID, copied.ID, actor.UserID(), rel)
	copied.SpaceSlug = dst.Slug
	return copied, nil
}

// copyEntryLocked copies a folder or file on disk and indexes every entry of
// the copy, parents first.
func (a *App) copyEntryLocked(ctx context.Context, actor *core.Actor, srcSpace *core.Space, src *core.Item, dst *core.Space, parent *core.Folder, name string) (*core.Item, error) {
	dir := folderPath(parent)
	srcRel := src.Path()
	isDir := src.Folder != nil
	if isDir && srcSpace.ID == dst.ID && (dir == srcRel || underPath(dir, srcRel)) {
		return nil, core.Invalid("parentId", "a folder cannot be copied into itself")
	}
	if name == "" {
		name = baseOf(srcRel)
		taken, err := a.nameTaken(ctx, dst, dir, name, "", "")
		if err != nil {
			return nil, err
		}
		if taken {
			if name, err = a.copyName(ctx, dst, dir, name, isDir); err != nil {
				return nil, err
			}
		}
	} else if taken, err := a.nameTaken(ctx, dst, dir, name, "", ""); err != nil {
		return nil, err
	} else if taken {
		return nil, fmt.Errorf("name %q in %q: %w", name, dir, core.ErrExists)
	}
	dstRel := joinRel(dir, name)
	copied, err := a.files.CopyEntry(srcSpace.Slug, srcRel, dst.Slug, dstRel, nil)
	if err != nil {
		return nil, fmt.Errorf("copy entry: %w", err)
	}
	a.markTree(dst.Slug, dstRel)

	srcFiles := map[string]*core.File{}
	srcFolders := map[string]*core.Folder{}
	if rows, err := a.db.TreeItems(ctx, srcSpace.ID); err == nil {
		for _, f := range rows.Files {
			if f.Path == srcRel || underPath(f.Path, srcRel) {
				srcFiles[f.Path] = f
			}
		}
		for _, f := range rows.Folders {
			if f.Path == srcRel || underPath(f.Path, srcRel) {
				srcFolders[f.Path] = f
			}
		}
	}
	sort.SliceStable(copied, func(i, j int) bool {
		di, dj := strings.Count(copied[i].NewRel, "/"), strings.Count(copied[j].NewRel, "/")
		if di != dj {
			return di < dj
		}
		return copied[i].NewRel < copied[j].NewRel
	})
	folderIDs := map[string]string{dir: folderID(parent)}
	now := a.now()
	var (
		rootID   string
		rootKind core.ItemKind
		notes    []*core.Note
		folders  []string
		firstErr error
	)
	fail := func(err error) {
		a.log.Warn("index copied entry failed", "space", dst.Slug, "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}
	for _, c := range copied {
		pid, ok := folderIDs[dirOf(c.NewRel)]
		if !ok {
			fail(fmt.Errorf("copied entry %s has no indexed parent", c.NewRel))
			continue
		}
		position := 0
		if c.NewRel == dstRel {
			position = a.nextPosition(ctx, dst.ID, pid)
		}
		switch c.Kind {
		case core.KindFolder:
			if position == 0 {
				if s := srcFolders[c.OldRel]; s != nil {
					position = s.Position
				}
			}
			f := &core.Folder{
				ID: core.NewID(), SpaceID: dst.ID, ParentID: pid, Name: baseOf(c.NewRel), Path: c.NewRel,
				Position: position, CreatedAt: now, UpdatedAt: now, CreatedBy: actor.UserID(),
			}
			if err := a.db.CreateFolder(ctx, f); err != nil {
				fail(err)
				continue
			}
			folderIDs[c.NewRel] = f.ID
			folders = append(folders, c.NewRel)
			a.publishItem("folder.created", core.KindFolder, dst.ID, f.ID, actor.UserID(), f.Path)
			if c.NewRel == dstRel {
				rootID, rootKind = f.ID, core.KindFolder
			}
		case core.KindFile:
			s := srcFiles[c.OldRel]
			f := &core.File{
				ID: core.NewID(), SpaceID: dst.ID, ParentID: pid, Name: baseOf(c.NewRel), Path: c.NewRel,
				Position: position, ModTime: now, CreatedAt: now, UpdatedAt: now,
				CreatedBy: actor.UserID(), UpdatedBy: actor.UserID(),
			}
			if s != nil {
				f.MIME, f.Size, f.SHA256, f.Width, f.Height = s.MIME, s.Size, s.SHA256, s.Width, s.Height
				f.Duration, f.Pages = s.Duration, s.Pages
				if position == 0 {
					f.Position = s.Position
				}
			}
			if info, err := a.files.StatEntry(dst.Slug, c.NewRel); err == nil {
				if f.Size != info.Size() {
					f.Size, f.SHA256 = info.Size(), ""
				}
				f.ModTime = info.ModTime().UTC()
			}
			if err := a.db.CreateFile(ctx, f); err != nil {
				fail(err)
				continue
			}
			a.publishItem("file.created", core.KindFile, dst.ID, f.ID, actor.UserID(), f.Path)
			if c.NewRel == dstRel {
				rootID, rootKind = f.ID, core.KindFile
			}
		case core.KindNote:
			n, err := a.files.Read(dst.Slug, c.NewRel)
			if err != nil {
				fail(err)
				continue
			}
			n.Path = c.NewRel
			notes = append(notes, n)
		}
	}
	if len(notes) > 0 {
		extra := make([]links.Entry, 0, len(notes))
		for _, n := range notes {
			extra = append(extra, links.Entry{ID: n.ID, Kind: links.KindNote, Path: n.Path, Title: n.Title})
		}
		snap, _, err := a.loadSnapshot(ctx, dst.ID, extra...)
		if err != nil {
			return nil, err
		}
		for _, n := range notes {
			if err := a.indexNoteSnap(ctx, dst, n, snap); err != nil {
				fail(err)
				continue
			}
			a.publishItem("note.created", core.KindNote, dst.ID, n.ID, actor.UserID(), n.Path)
		}
	}
	for _, f := range folders {
		a.syncFolderNote(ctx, dst.ID, f)
	}
	a.kickQueue()
	if rootID == "" {
		if firstErr != nil {
			return nil, fmt.Errorf("index copy: %w", firstErr)
		}
		return nil, fmt.Errorf("copy of %s produced no entry: %w", srcRel, core.ErrConflict)
	}
	if firstErr != nil {
		a.log.Warn("copy indexed partly, a rescan repairs the rest", "space", dst.Slug, "path", dstRel, "err", firstErr)
		a.scheduleScan(dst.Slug, scanDebounce)
	}
	a.log.Info("item copied", "from", src.ID(), "itemId", rootID, "space", dst.Slug, "path", dstRel, "by", actor.UserID())
	out, err := a.loadItem(ctx, rootID)
	if err != nil {
		return nil, err
	}
	if out.Kind != rootKind {
		return nil, fmt.Errorf("copy root kind changed: %w", core.ErrConflict)
	}
	return out, nil
}

// TrashItem moves an item to the space trash. A folder goes with its whole
// subtree, and every item keeps its id.
func (a *App) TrashItem(ctx context.Context, actor *core.Actor, id string) error {
	it, _, err := a.requireItem(ctx, actor, id, core.AccessEdit)
	if err != nil {
		return err
	}
	space, err := a.space(ctx, it.SpaceID())
	if err != nil {
		return err
	}
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	fresh, err := a.loadItem(ctx, id)
	if err != nil {
		return err
	}
	if itemDeleted(fresh) {
		return fmt.Errorf("item %s is in the trash: %w", id, core.ErrConflict)
	}
	rel := fresh.Path()
	trashRel, err := a.files.TrashEntry(space.Slug, rel, id, actor.UserID())
	if err != nil {
		return fmt.Errorf("trash entry: %w", err)
	}
	a.markTree(space.Slug, rel)
	now := a.now()
	switch fresh.Kind {
	case core.KindFolder:
		err = a.db.TrashFolder(ctx, id, now, actor.UserID())
	case core.KindFile:
		err = a.db.TrashFile(ctx, id, now, actor.UserID())
	default:
		err = a.db.SetNoteDeleted(ctx, id, true, &now, actor.UserID())
	}
	if err != nil {
		if rerr := a.files.RestoreEntry(space.Slug, trashRel, rel); rerr != nil {
			a.log.Warn("undo trash on disk failed", "space", space.Slug, "path", rel, "err", rerr)
		}
		return fmt.Errorf("record trash: %w", err)
	}
	if baseOf(rel) == folderNoteName {
		a.syncFolderNote(ctx, space.ID, dirOf(rel))
	}
	a.log.Info("item trashed", "itemId", id, "kind", fresh.Kind, "space", space.Slug, "path", rel, "by", actor.UserID())
	a.publishItem(kindEvent(fresh.Kind, "deleted"), fresh.Kind, space.ID, id, actor.UserID(), rel)
	a.publishItem("tree.changed", fresh.Kind, space.ID, id, actor.UserID(), rel)
	return nil
}

// RestoreItem returns a trashed item to its old path. When the old parent
// folder is gone the item returns to the space root, and a taken name gets
// "<stem> (N).<ext>". An item that went to the trash inside a folder is
// restored with that folder, so restoring it alone returns ErrConflict.
func (a *App) RestoreItem(ctx context.Context, actor *core.Actor, id string) (*core.Item, error) {
	it, _, err := a.requireItemAny(ctx, actor, id, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	if !itemDeleted(it) {
		return nil, fmt.Errorf("item %s is not in the trash: %w", id, core.ErrConflict)
	}
	space, err := a.space(ctx, it.SpaceID())
	if err != nil {
		return nil, err
	}
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	fresh, err := a.loadItem(ctx, id)
	if err != nil {
		return nil, err
	}
	if !itemDeleted(fresh) {
		return nil, fmt.Errorf("item %s is not in the trash: %w", id, core.ErrConflict)
	}
	slot, ok := a.trashSlot(space, id)
	if !ok {
		return nil, fmt.Errorf("item %s went to the trash with its folder, restore that folder instead: %w", id, core.ErrConflict)
	}
	oldRel := fresh.Path()
	dir := dirOf(oldRel)
	var parent *core.Folder
	if dir != "" {
		if f, err := a.db.GetFolderByPath(ctx, space.ID, dir); err == nil && f != nil && !f.Deleted {
			parent = f
		}
	}
	dir = folderPath(parent)
	if _, err := a.requireContainer(ctx, actor, space, folderID(parent), core.AccessEdit); err != nil {
		return nil, err
	}
	name, err := a.freeName(ctx, space, dir, baseOf(oldRel), fresh.Kind == core.KindFolder)
	if err != nil {
		return nil, err
	}
	target := joinRel(dir, name)
	if err := a.files.RestoreEntry(space.Slug, slot.TrashRel, target); err != nil {
		return nil, fmt.Errorf("restore entry: %w", err)
	}
	a.markTree(space.Slug, target)
	switch fresh.Kind {
	case core.KindFolder:
		err = a.db.RestoreFolder(ctx, id, folderID(parent), target)
	case core.KindFile:
		err = a.db.RestoreFile(ctx, id, folderID(parent), target)
	default:
		err = a.restoreNoteRow(ctx, actor, space, fresh.Note, parent, target)
	}
	if err != nil {
		a.log.Warn("restore recorded partly, a rescan repairs it", "itemId", id, "err", err)
		a.scheduleScan(space.Slug, scanDebounce)
		return nil, fmt.Errorf("record restore: %w", err)
	}
	if name == folderNoteName {
		a.syncFolderNote(ctx, space.ID, dir)
	}
	if fresh.Kind == core.KindFolder {
		a.syncFolderNote(ctx, space.ID, target)
	}
	a.log.Info("item restored", "itemId", id, "kind", fresh.Kind, "space", space.Slug, "path", target, "by", actor.UserID())
	a.publishItem(kindEvent(fresh.Kind, "updated"), fresh.Kind, space.ID, id, actor.UserID(), target)
	a.publishItem("tree.changed", fresh.Kind, space.ID, id, actor.UserID(), target)
	out, err := a.loadItem(ctx, id)
	if err != nil {
		return nil, err
	}
	out.Access = it.Access
	return decorateItem(space, out), nil
}

// restoreNoteRow reindexes a note that returned from the trash to target.
func (a *App) restoreNoteRow(ctx context.Context, actor *core.Actor, space *core.Space, row *core.Note, parent *core.Folder, target string) error {
	disk, err := a.files.Read(space.Slug, target)
	if err != nil {
		return fmt.Errorf("read restored note: %w", err)
	}
	n := *row
	n.Content, n.Tags, n.Extra = disk.Content, disk.Tags, disk.Extra
	delete(n.Extra, "trashed_from")
	delete(n.Extra, "trash_path")
	n.Path, n.Slug, n.ParentID = target, pathSlug(target), folderID(parent)
	n.Deleted, n.DeletedAt, n.DeletedBy = false, nil, ""
	n.UpdatedAt, n.UpdatedBy, n.UpdatedVia = a.now(), actor.UserID(), actor.Via
	if err := a.writeNote(space.Slug, &n); err != nil {
		return err
	}
	return a.indexNote(ctx, space, &n)
}

// DeleteItem removes an item for good. It needs manage access, or edit access
// for the creator of the item. A folder takes every item below it that shares
// its state.
func (a *App) DeleteItem(ctx context.Context, actor *core.Actor, id string) error {
	it, grant, err := a.requireItemAny(ctx, actor, id, core.AccessView)
	if err != nil {
		return err
	}
	author := itemCreatedBy(it) != "" && itemCreatedBy(it) == actor.UserID()
	if !grant.Access.AtLeast(core.AccessManage) && !(author && grant.Access.AtLeast(core.AccessEdit)) {
		return fmt.Errorf("item %s needs manage access: %w", id, core.ErrForbidden)
	}
	space, err := a.space(ctx, it.SpaceID())
	if err != nil {
		return err
	}
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	fresh, err := a.loadItem(ctx, id)
	if err != nil {
		return fmt.Errorf("item %s: %w", id, denied(actor))
	}
	rel := fresh.Path()
	if itemDeleted(fresh) {
		slot, ok := a.trashSlot(space, id)
		if !ok {
			return fmt.Errorf("item %s went to the trash with its folder, delete that folder instead: %w", id, core.ErrConflict)
		}
		if err := a.files.PurgeTrashEntry(space.Slug, slot.TrashRel); err != nil && !errors.Is(err, core.ErrNotFound) {
			return fmt.Errorf("purge trash entry: %w", err)
		}
	} else {
		if err := a.files.RemoveEntry(space.Slug, rel); err != nil && !errors.Is(err, core.ErrNotFound) {
			return fmt.Errorf("remove entry: %w", err)
		}
		a.markTree(space.Slug, rel)
	}
	if err := a.dropItemRows(ctx, space, fresh); err != nil {
		return err
	}
	if baseOf(rel) == folderNoteName && !itemDeleted(fresh) {
		a.syncFolderNote(ctx, space.ID, dirOf(rel))
	}
	a.log.Info("item deleted", "itemId", id, "kind", fresh.Kind, "space", space.Slug, "path", rel, "by", actor.UserID())
	a.publishItem(kindEvent(fresh.Kind, "deleted"), fresh.Kind, space.ID, id, actor.UserID(), rel)
	a.publishItem("tree.changed", fresh.Kind, space.ID, id, actor.UserID(), rel)
	return nil
}

// dropItemRows removes the index rows of a deleted item with its comments,
// shares, links, file versions and thumbnails. A folder takes the items
// below it that share its state.
func (a *App) dropItemRows(ctx context.Context, space *core.Space, it *core.Item) error {
	switch {
	case it.Folder != nil:
		fileIDs := a.subtreeFileIDs(ctx, space, it.Folder)
		if err := a.db.DeleteFolder(ctx, it.Folder.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return fmt.Errorf("delete folder rows: %w", err)
		}
		for _, id := range fileIDs {
			a.dropFileData(space, id)
		}
		a.sweepSuggestions(ctx, space.ID)
	case it.File != nil:
		if err := a.db.DeleteFile(ctx, it.File.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return fmt.Errorf("delete file row: %w", err)
		}
		a.dropFileData(space, it.File.ID)
	case it.Note != nil:
		if err := a.db.DeleteNoteRow(ctx, it.Note.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return fmt.Errorf("delete note row: %w", err)
		}
		a.dropNoteRelations(ctx, []*core.Note{it.Note})
	}
	return nil
}

// subtreeFileIDs lists the files below a folder that share its state, which
// are the files DeleteFolder removes.
func (a *App) subtreeFileIDs(ctx context.Context, space *core.Space, folder *core.Folder) []string {
	files, err := a.db.ListFiles(ctx, space.ID, ports.FileListOptions{IncludeDeleted: true, Sort: "path"})
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range files {
		if !underPath(f.Path, folder.Path) || f.Deleted != folder.Deleted {
			continue
		}
		if folder.Deleted && (f.DeletedAt == nil || folder.DeletedAt == nil || !f.DeletedAt.Equal(*folder.DeletedAt)) {
			continue
		}
		out = append(out, f.ID)
	}
	return out
}

// dropFileData removes the versions and thumbnails of a deleted file.
func (a *App) dropFileData(space *core.Space, fileID string) {
	if err := a.files.DeleteFileHistory(space.ID, fileID); err != nil && !errors.Is(err, core.ErrNotFound) {
		a.log.Warn("delete file history failed", "fileId", fileID, "err", err)
	}
	if err := a.files.DeleteThumbnails(fileID); err != nil && !errors.Is(err, core.ErrNotFound) {
		a.log.Debug("delete thumbnails failed", "fileId", fileID, "err", err)
	}
}

// EmptyTrash permanently deletes every trashed item of a space.
func (a *App) EmptyTrash(ctx context.Context, actor *core.Actor, spaceID string) error {
	space, _, err := a.requireSpace(ctx, actor, spaceID, core.AccessManage)
	if err != nil {
		return err
	}
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	folders, notes, files, err := a.trashedRows(ctx, space.ID)
	if err != nil {
		return err
	}
	if err := a.files.EmptyTrash(space.Slug); err != nil {
		return fmt.Errorf("empty trash: %w", err)
	}
	for _, f := range folders {
		if err := a.db.DeleteFolder(ctx, f.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			a.log.Warn("delete trashed folder row failed", "folderId", f.ID, "err", err)
		}
	}
	for _, f := range files {
		if err := a.db.DeleteFile(ctx, f.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			a.log.Warn("delete trashed file row failed", "fileId", f.ID, "err", err)
		}
		a.dropFileData(space, f.ID)
	}
	for _, n := range notes {
		if err := a.db.DeleteNoteRow(ctx, n.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			a.log.Warn("delete trashed note row failed", "noteId", n.ID, "err", err)
		}
	}
	a.dropNoteRelations(ctx, notes)
	a.log.Info("trash emptied", "space", space.Slug, "folders", len(folders), "notes", len(notes), "files", len(files), "by", actor.UserID())
	a.publish("tree.changed", space.ID, "", actor.UserID(), "")
	return nil
}

// dropNoteRelations removes the shares, public links and comments of notes
// that are gone for good, so none of them can surface again, for example on a
// note created later with the same client-supplied id.
func (a *App) dropNoteRelations(ctx context.Context, notes []*core.Note) {
	for _, n := range notes {
		a.deleteNoteSuggestions(ctx, n.ID)
		if shares, err := a.db.ListSharesForNote(ctx, n.ID); err == nil {
			for _, s := range shares {
				if err := a.db.DeleteShare(ctx, s.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
					a.log.Warn("delete share of deleted note failed", "noteId", n.ID, "shareId", s.ID, "err", err)
				}
			}
		}
		if list, err := a.db.ListPublicLinks(ctx, n.ID); err == nil {
			for _, l := range list {
				if err := a.db.DeletePublicLink(ctx, l.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
					a.log.Warn("delete link of deleted note failed", "noteId", n.ID, "linkId", l.ID, "err", err)
				}
			}
		}
		if comments, err := a.db.ListComments(ctx, n.ID); err == nil {
			for _, c := range comments {
				if err := a.db.DeleteComment(ctx, c.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
					a.log.Warn("delete comment of deleted note failed", "noteId", n.ID, "commentId", c.ID, "err", err)
				}
			}
		}
	}
}
