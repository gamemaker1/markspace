package app

import (
	"context"
	"fmt"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// SpaceTree returns the live items of a space the actor may view, nested by
// folder. A folder note is not a child row when its folder is shown, because
// the folder carries it as IndexNoteID. An item whose folder is hidden
// becomes a root, so a user who reaches the space through shares sees each
// shared subtree as a root.
func (a *App) SpaceTree(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.TreeItem, error) {
	space, grant, _, err := a.spaceView(ctx, actor, spaceID)
	if err != nil {
		return nil, err
	}
	rows, err := a.db.TreeItems(ctx, space.ID)
	if err != nil {
		return nil, fmt.Errorf("tree items: %w", err)
	}
	full := grant.Access.AtLeast(core.AccessView)
	cache := a.grants(actor)
	visible := func(ref itemRef) (bool, error) {
		if full {
			return true, nil
		}
		g, err := cache.item(ctx, ref)
		if err != nil {
			return false, err
		}
		return g.Access.AtLeast(core.AccessView), nil
	}
	return buildItemTree(space, rows, visible)
}

// buildItemTree nests visible rows under their folders.
func buildItemTree(space *core.Space, rows *ports.TreeRows, visible func(itemRef) (bool, error)) ([]*core.TreeItem, error) {
	folders := map[string]*core.TreeItem{}
	roots := []*core.TreeItem{}
	attach := func(node *core.TreeItem, relPath string) {
		if parent, ok := folders[dirOf(relPath)]; ok {
			parent.Children = append(parent.Children, node)
			return
		}
		roots = append(roots, node)
	}
	for _, f := range rows.Folders {
		ok, err := visible(folderRef(f))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		f.SpaceSlug = space.Slug
		node := &core.TreeItem{Kind: core.KindFolder, Folder: f}
		attach(node, f.Path)
		folders[f.Path] = node
	}
	for _, n := range rows.Notes {
		ok, err := visible(noteRef(n))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		n.SpaceSlug = space.Slug
		if isFolderNotePath(n.Path) {
			if folder, shown := folders[dirOf(n.Path)]; shown {
				if folder.Folder.IndexNoteID == "" {
					folder.Folder.IndexNoteID = n.ID
				}
				continue
			}
		}
		attach(&core.TreeItem{Kind: core.KindNote, Note: n}, n.Path)
	}
	for _, f := range rows.Files {
		ok, err := visible(fileRef(f))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		attach(&core.TreeItem{Kind: core.KindFile, File: decorateFile(space, f)}, f.Path)
	}
	sortTreeItems(roots, 0)
	return roots, nil
}

// GetFolder returns a folder with its direct children. Any actor who may view
// the folder sees every child listed, including children a share without
// children does not let them open. A trashed folder lists the items trashed
// with it.
func (a *App) GetFolder(ctx context.Context, actor *core.Actor, id string) (*core.TreeItem, *core.Grant, error) {
	it, grant, err := a.requireItemAny(ctx, actor, id, core.AccessView)
	if err != nil {
		return nil, nil, err
	}
	if it.Folder == nil {
		return nil, nil, fmt.Errorf("item %s is not a folder: %w", id, core.ErrNotFound)
	}
	space, err := a.space(ctx, it.Folder.SpaceID)
	if err != nil {
		return nil, nil, err
	}
	children, err := a.folderChildren(ctx, space, it.Folder)
	if err != nil {
		return nil, nil, err
	}
	it.Folder.SpaceSlug = space.Slug
	return &core.TreeItem{Kind: core.KindFolder, Folder: it.Folder, Children: children}, grant, nil
}

// folderChildren lists the direct children of a folder, sorted, without its
// folder note.
func (a *App) folderChildren(ctx context.Context, space *core.Space, folder *core.Folder) ([]*core.TreeItem, error) {
	deleted := folder.Deleted
	out := []*core.TreeItem{}
	folders, err := a.db.ListFolders(ctx, space.ID, deleted)
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	for _, f := range folders {
		if f.ParentID == folder.ID && f.Deleted == deleted {
			f.SpaceSlug = space.Slug
			out = append(out, &core.TreeItem{Kind: core.KindFolder, Folder: f})
		}
	}
	notes, err := a.db.ListNotes(ctx, space.ID, core.NoteListOptions{ParentID: folder.ID, HasParent: true, IncludeTrash: deleted})
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	for _, n := range notes {
		if n.Deleted != deleted || n.Path == joinRel(folder.Path, folderNoteName) {
			continue
		}
		n.SpaceSlug = space.Slug
		out = append(out, &core.TreeItem{Kind: core.KindNote, Note: n})
	}
	files, err := a.db.ListFiles(ctx, space.ID, ports.FileListOptions{ParentID: folder.ID, HasParent: true, IncludeDeleted: deleted})
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	for _, f := range files {
		if f.Deleted != deleted {
			continue
		}
		out = append(out, &core.TreeItem{Kind: core.KindFile, File: decorateFile(space, f)})
	}
	sortTreeItems(out, 0)
	return out, nil
}
