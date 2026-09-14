package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/links"
	"github.com/gamemaker1/markspace/internal/ports"
)

// folderNoteName is the file name of a folder note.
const folderNoteName = "index.md"

// itemRef is what grant resolution needs to know about an item.
type itemRef struct {
	kind    core.ItemKind
	id      string
	spaceID string
	path    string
	deleted bool
}

func noteRef(n *core.Note) itemRef {
	return itemRef{kind: core.KindNote, id: n.ID, spaceID: n.SpaceID, path: n.Path, deleted: n.Deleted}
}

func folderRef(f *core.Folder) itemRef {
	return itemRef{kind: core.KindFolder, id: f.ID, spaceID: f.SpaceID, path: f.Path, deleted: f.Deleted}
}

func fileRef(f *core.File) itemRef {
	return itemRef{kind: core.KindFile, id: f.ID, spaceID: f.SpaceID, path: f.Path, deleted: f.Deleted}
}

// refOf returns the grant reference of an item.
func refOf(it *core.Item) itemRef {
	switch {
	case it.Folder != nil:
		return folderRef(it.Folder)
	case it.Note != nil:
		return noteRef(it.Note)
	case it.File != nil:
		return fileRef(it.File)
	}
	return itemRef{}
}

// unixNano returns a trash time for sorting, 0 when unset.
func unixNano(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixNano()
}

// itemDeleted reports whether an item is in the trash.
func itemDeleted(it *core.Item) bool { return refOf(it).deleted }

// itemParentID returns the id of the folder holding an item, "" at the root.
func itemParentID(it *core.Item) string {
	switch {
	case it.Folder != nil:
		return it.Folder.ParentID
	case it.Note != nil:
		return it.Note.ParentID
	case it.File != nil:
		return it.File.ParentID
	}
	return ""
}

// itemCreatedBy returns the creator of an item.
func itemCreatedBy(it *core.Item) string {
	switch {
	case it.Folder != nil:
		return it.Folder.CreatedBy
	case it.Note != nil:
		return it.Note.CreatedBy
	case it.File != nil:
		return it.File.CreatedBy
	}
	return ""
}

// dirOf returns the folder part of an entry path, "" at the root.
func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

// baseOf returns the last segment of an entry path.
func baseOf(p string) string { return p[strings.LastIndexByte(p, '/')+1:] }

// joinRel joins a folder path and a name. An empty folder is the root.
func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// underPath reports whether p lies strictly below the folder at dir.
func underPath(p, dir string) bool { return strings.HasPrefix(p, dir+"/") }

// escapePath percent-escapes each segment of an entry path for a URL.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// thumbable reports whether the thumbnail route can serve a file.
func thumbable(mime string) bool {
	switch strings.ToLower(mime) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

// fileContentURL is the web path serving a file's content.
func fileContentURL(slug string, f *core.File) string {
	return "/files/" + url.PathEscape(slug) + "/" + url.PathEscape(f.ID) + "/" + url.PathEscape(f.Name)
}

// decorateFile fills the display fields of a file.
func decorateFile(space *core.Space, f *core.File) *core.File {
	if f == nil || space == nil {
		return f
	}
	f.SpaceSlug = space.Slug
	f.URL = fileContentURL(space.Slug, f)
	f.Thumb = ""
	if thumbable(f.MIME) {
		f.Thumb = "/thumbs/" + url.PathEscape(space.Slug) + "/" + url.PathEscape(f.ID)
	}
	return f
}

// decorateItem fills the display fields of any item.
func decorateItem(space *core.Space, it *core.Item) *core.Item {
	if it == nil || space == nil {
		return it
	}
	switch {
	case it.Folder != nil:
		it.Folder.SpaceSlug = space.Slug
	case it.Note != nil:
		it.Note.SpaceSlug = space.Slug
	case it.File != nil:
		decorateFile(space, it.File)
	}
	return it
}

// itemURL is the web path that opens an item.
func itemURL(slug string, kind core.ItemKind, relPath string) string {
	switch kind {
	case core.KindNote:
		return "/s/" + url.PathEscape(slug) + "/n/" + escapePath(strings.TrimSuffix(relPath, ".md"))
	case core.KindFolder:
		return "/s/" + url.PathEscape(slug) + "/d/" + escapePath(relPath)
	default:
		return "/s/" + url.PathEscape(slug) + "/f/" + escapePath(relPath)
	}
}

// kindEvent names the event of one change to an item kind.
func kindEvent(kind core.ItemKind, change string) string { return string(kind) + "." + change }

// publishItem sends an event about one item.
func (a *App) publishItem(typ string, kind core.ItemKind, spaceID, id, actorID, relPath string) {
	ev := core.Event{
		Type: typ, SpaceID: spaceID, ItemID: id, Kind: kind,
		ActorID: actorID, Path: relPath, At: a.now().UnixMilli(),
	}
	if kind == core.KindNote {
		ev.NoteID = id
	}
	a.emit(ev)
}

// loadItem reads an item of any kind without an access check.
func (a *App) loadItem(ctx context.Context, id string) (*core.Item, error) {
	if id == "" {
		return nil, fmt.Errorf("item id is empty: %w", core.ErrInvalid)
	}
	kind, _, err := a.db.ItemKind(ctx, id)
	if err != nil {
		return nil, err
	}
	it := &core.Item{Kind: kind}
	switch kind {
	case core.KindFolder:
		it.Folder, err = a.db.GetFolder(ctx, id)
	case core.KindNote:
		it.Note, err = a.db.GetNote(ctx, id)
	case core.KindFile:
		it.File, err = a.db.GetFile(ctx, id)
	default:
		return nil, fmt.Errorf("item %s: %w", id, core.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if it.Folder == nil && it.Note == nil && it.File == nil {
		return nil, fmt.Errorf("item %s: %w", id, core.ErrNotFound)
	}
	return it, nil
}

// requireItemAny loads an item, resolves the grant and enforces want. A
// hidden item returns the same error as a missing one.
func (a *App) requireItemAny(ctx context.Context, actor *core.Actor, id string, want core.Access) (*core.Item, *core.Grant, error) {
	it, err := a.loadItem(ctx, id)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return nil, nil, fmt.Errorf("item %s: %w", id, denied(actor))
		}
		return nil, nil, fmt.Errorf("load item %s: %w", id, err)
	}
	grant, err := a.grants(actor).item(ctx, refOf(it))
	if err != nil {
		return nil, nil, err
	}
	if grant.Access == core.AccessNone {
		return nil, nil, fmt.Errorf("item %s: %w", id, denied(actor))
	}
	if !grant.Access.AtLeast(want) {
		return nil, nil, fmt.Errorf("item %s needs %s access: %w", id, want, core.ErrForbidden)
	}
	it.Access = grant.Access
	return it, grant, nil
}

// requireItem is requireItemAny that refuses any write to a trashed item with
// core.ErrConflict.
func (a *App) requireItem(ctx context.Context, actor *core.Actor, id string, want core.Access) (*core.Item, *core.Grant, error) {
	it, grant, err := a.requireItemAny(ctx, actor, id, want)
	if err != nil {
		return nil, nil, err
	}
	if itemDeleted(it) && want > core.AccessView {
		return nil, nil, fmt.Errorf("item %s is in the trash: %w", id, core.ErrConflict)
	}
	return it, grant, nil
}

// requireContainer checks want on the folder parentID, or on the space when
// parentID is empty, and returns the folder, nil at the root.
func (a *App) requireContainer(ctx context.Context, actor *core.Actor, space *core.Space, parentID string, want core.Access) (*core.Folder, error) {
	if parentID == "" {
		if _, _, err := a.requireSpace(ctx, actor, space.ID, want); err != nil {
			return nil, err
		}
		return nil, nil
	}
	it, _, err := a.requireItem(ctx, actor, parentID, want)
	if err != nil {
		return nil, err
	}
	if it.Folder == nil {
		return nil, core.Invalid("parentId", "the parent is not a folder")
	}
	if it.Folder.SpaceID != space.ID {
		return nil, core.Invalid("parentId", "the parent folder is in another space")
	}
	return it.Folder, nil
}

// resolveParent finds the space and folder a new item goes into and checks
// want on it. A parent folder decides the space. spaceID must agree with it
// when both are set.
func (a *App) resolveParent(ctx context.Context, actor *core.Actor, spaceID, parentID string, want core.Access) (*core.Space, *core.Folder, error) {
	if parentID == "" {
		space, _, err := a.requireSpace(ctx, actor, spaceID, want)
		if err != nil {
			return nil, nil, err
		}
		return space, nil, nil
	}
	it, _, err := a.requireItem(ctx, actor, parentID, want)
	if err != nil {
		return nil, nil, err
	}
	if it.Folder == nil {
		return nil, nil, core.Invalid("parentId", "the parent is not a folder")
	}
	if spaceID != "" && spaceID != it.Folder.SpaceID {
		return nil, nil, core.Invalid("parentId", "the parent folder is in another space")
	}
	space, err := a.space(ctx, it.Folder.SpaceID)
	if err != nil {
		return nil, nil, err
	}
	return space, it.Folder, nil
}

// folderPath returns the path of a folder, "" for the root.
func folderPath(f *core.Folder) string {
	if f == nil {
		return ""
	}
	return f.Path
}

// folderID returns the id of a folder, "" for the root.
func folderID(f *core.Folder) string {
	if f == nil {
		return ""
	}
	return f.ID
}

// folderIDAt returns the id of the live folder at relPath, "" at the root or
// when no folder row exists.
func (a *App) folderIDAt(ctx context.Context, spaceID, relPath string) string {
	if relPath == "" {
		return ""
	}
	f, err := a.db.GetFolderByPath(ctx, spaceID, relPath)
	if err != nil || f == nil {
		return ""
	}
	return f.ID
}

// checkName applies the name rules for a new or renamed folder or file.
func (a *App) checkName(kind core.ItemKind, name string) error {
	if err := a.files.ValidName(name); err != nil {
		return core.Invalid("name", strings.TrimSuffix(err.Error(), ": "+core.ErrInvalid.Error()))
	}
	if kind == core.KindFile && strings.HasSuffix(name, ".md") {
		return core.Invalid("name", "a file name may not end in .md, because a .md file is a note")
	}
	return nil
}

// nameTaken reports whether a live item other than exceptID holds name in a
// folder, in the index or on disk, compared ignoring case.
func (a *App) nameTaken(ctx context.Context, space *core.Space, dir, name, exceptID, exceptRel string) (bool, error) {
	taken, err := a.db.SiblingNameTaken(ctx, space.ID, dir, name, exceptID)
	if err != nil {
		return false, fmt.Errorf("check name: %w", err)
	}
	if taken {
		return true, nil
	}
	_, onDisk := a.files.CaseCollision(space.Slug, dir, name, exceptRel)
	return onDisk, nil
}

// nextPosition returns a position after the last item in a folder.
func (a *App) nextPosition(ctx context.Context, spaceID, parentID string) int {
	top := 0
	if notes, err := a.db.ListNotes(ctx, spaceID, core.NoteListOptions{ParentID: parentID, HasParent: true}); err == nil {
		for _, n := range notes {
			top = max(top, n.Position)
		}
	}
	if folders, err := a.db.ListFolders(ctx, spaceID, false); err == nil {
		for _, f := range folders {
			if f.ParentID == parentID {
				top = max(top, f.Position)
			}
		}
	}
	if files, err := a.db.ListFiles(ctx, spaceID, ports.FileListOptions{ParentID: parentID, HasParent: true}); err == nil {
		for _, f := range files {
			top = max(top, f.Position)
		}
	}
	return top + positionStep
}

// spaceLock returns the mutex that serializes structural writes and scans
// in one space.
func (a *App) spaceLock(spaceID string) *sync.Mutex {
	a.locksMu.Lock()
	defer a.locksMu.Unlock()
	m, ok := a.locks[spaceID]
	if !ok {
		m = &sync.Mutex{}
		a.locks[spaceID] = m
	}
	return m
}

// lockSpaces locks the spaces in id order and returns the unlock function.
func (a *App) lockSpaces(ids ...string) func() {
	uniq := map[string]bool{}
	var order []string
	for _, id := range ids {
		if id != "" && !uniq[id] {
			uniq[id] = true
			order = append(order, id)
		}
	}
	sort.Strings(order)
	for _, id := range order {
		a.spaceLock(id).Lock()
	}
	return func() {
		for i := len(order) - 1; i >= 0; i-- {
			a.spaceLock(order[i]).Unlock()
		}
	}
}

// syncFolderNote points the folder at dir to its live index.md, or clears
// the pointer when there is none.
func (a *App) syncFolderNote(ctx context.Context, spaceID, dir string) {
	if dir == "" {
		return
	}
	f, err := a.db.GetFolderByPath(ctx, spaceID, dir)
	if err != nil || f == nil {
		return
	}
	want := ""
	if n, err := a.db.GetNoteByPath(ctx, spaceID, joinRel(f.Path, folderNoteName)); err == nil && n != nil && !n.Deleted {
		want = n.ID
	}
	if f.IndexNoteID == want {
		return
	}
	f.IndexNoteID = want
	f.UpdatedAt = a.now()
	if err := a.db.UpdateFolder(ctx, f); err != nil {
		a.log.Warn("update folder note failed", "folderId", f.ID, "err", err)
	}
}

// GetItem returns an item of any kind with the actor's access.
func (a *App) GetItem(ctx context.Context, actor *core.Actor, id string) (*core.Item, error) {
	it, _, err := a.requireItemAny(ctx, actor, id, core.AccessView)
	if err != nil {
		return nil, err
	}
	space, err := a.space(ctx, it.SpaceID())
	if err != nil {
		return nil, err
	}
	return decorateItem(space, it), nil
}

// spaceByIDOrSlug loads a space by id, then by slug.
func (a *App) spaceByIDOrSlug(ctx context.Context, actor *core.Actor, key string) (*core.Space, error) {
	if key == "" {
		return nil, fmt.Errorf("space is empty: %w", core.ErrInvalid)
	}
	sp, err := a.db.GetSpace(ctx, key)
	if errors.Is(err, core.ErrNotFound) || (err == nil && sp == nil) {
		sp, err = a.db.GetSpaceBySlug(ctx, key)
	}
	if errors.Is(err, core.ErrNotFound) || (err == nil && sp == nil) {
		return nil, fmt.Errorf("space %s: %w", key, denied(actor))
	}
	if err != nil {
		return nil, fmt.Errorf("load space %s: %w", key, err)
	}
	return sp, nil
}

// GetItemByPath resolves an entry path in a space. A note path may omit .md.
func (a *App) GetItemByPath(ctx context.Context, actor *core.Actor, spaceKey, relPath string) (*core.Item, error) {
	space, err := a.spaceByIDOrSlug(ctx, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	rel := strings.Trim(relPath, "/")
	if rel == "" {
		return nil, fmt.Errorf("item path is empty: %w", core.ErrInvalid)
	}
	id := ""
	if f, err := a.db.GetFolderByPath(ctx, space.ID, rel); err == nil && f != nil {
		id = f.ID
	} else if f, err := a.db.GetFileByPath(ctx, space.ID, rel); err == nil && f != nil {
		id = f.ID
	} else {
		for _, p := range []string{rel, rel + ".md"} {
			if n, err := a.db.GetNoteByPath(ctx, space.ID, p); err == nil && n != nil {
				id = n.ID
				break
			}
		}
	}
	if id == "" {
		return nil, fmt.Errorf("item %s: %w", rel, denied(actor))
	}
	return a.GetItem(ctx, actor, id)
}

// ItemBacklinks lists the live notes linking to an item that the actor may
// view.
func (a *App) ItemBacklinks(ctx context.Context, actor *core.Actor, id string) ([]*core.Backlink, error) {
	if _, _, err := a.requireItemAny(ctx, actor, id, core.AccessView); err != nil {
		return nil, err
	}
	list, err := a.db.BacklinksTo(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("backlinks: %w", err)
	}
	cache := a.grants(actor)
	out := make([]*core.Backlink, 0, len(list))
	for _, l := range list {
		if l == nil || l.FromNote == nil {
			continue
		}
		g, err := cache.note(ctx, l.FromNote)
		if err != nil {
			return nil, err
		}
		if g.Access.AtLeast(core.AccessView) {
			out = append(out, l)
		}
	}
	return out, nil
}

// trashedRows lists the trashed folders, notes and files of a space.
func (a *App) trashedRows(ctx context.Context, spaceID string) ([]*core.Folder, []*core.Note, []*core.File, error) {
	all, err := a.db.ListFolders(ctx, spaceID, true)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list folders: %w", err)
	}
	var folders []*core.Folder
	for _, f := range all {
		if f.Deleted {
			folders = append(folders, f)
		}
	}
	notes, err := a.db.ListNotes(ctx, spaceID, core.NoteListOptions{OnlyTrash: true, IncludeTrash: true})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list trashed notes: %w", err)
	}
	allFiles, err := a.db.ListFiles(ctx, spaceID, ports.FileListOptions{IncludeDeleted: true, Sort: "path"})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list files: %w", err)
	}
	var files []*core.File
	for _, f := range allFiles {
		if f.Deleted {
			files = append(files, f)
		}
	}
	return folders, notes, files, nil
}

// trashedWithFolder reports whether a trashed item went to the trash inside
// one of the trashed folders, rather than on its own.
func trashedWithFolder(folders []*core.Folder, id, relPath string, at *time.Time) bool {
	for _, f := range folders {
		if f.ID == id || f.DeletedAt == nil || at == nil {
			continue
		}
		if underPath(relPath, f.Path) && f.DeletedAt.Equal(*at) {
			return true
		}
	}
	return false
}

// ListTrashItems lists the trashed items of a space that head their own
// trash entry, newest first.
func (a *App) ListTrashItems(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.TreeItem, error) {
	space, grant, _, err := a.spaceView(ctx, actor, spaceID)
	if err != nil {
		return nil, err
	}
	folders, notes, files, err := a.trashedRows(ctx, space.ID)
	if err != nil {
		return nil, err
	}
	cache := a.grants(actor)
	visible := func(ref itemRef) bool {
		if grant.Access.AtLeast(core.AccessView) {
			return true
		}
		g, err := cache.item(ctx, ref)
		return err == nil && g.Access.AtLeast(core.AccessView)
	}
	type row struct {
		item *core.TreeItem
		at   int64
	}
	var rows []row
	for _, f := range folders {
		if trashedWithFolder(folders, f.ID, f.Path, f.DeletedAt) || !visible(folderRef(f)) {
			continue
		}
		f.SpaceSlug = space.Slug
		rows = append(rows, row{&core.TreeItem{Kind: core.KindFolder, Folder: f}, unixNano(f.DeletedAt)})
	}
	for _, n := range notes {
		if trashedWithFolder(folders, n.ID, n.Path, n.DeletedAt) || !visible(noteRef(n)) {
			continue
		}
		n.SpaceSlug = space.Slug
		rows = append(rows, row{&core.TreeItem{Kind: core.KindNote, Note: n}, unixNano(n.DeletedAt)})
	}
	for _, f := range files {
		if trashedWithFolder(folders, f.ID, f.Path, f.DeletedAt) || !visible(fileRef(f)) {
			continue
		}
		rows = append(rows, row{&core.TreeItem{Kind: core.KindFile, File: decorateFile(space, f)}, unixNano(f.DeletedAt)})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at > rows[j].at })
	out := make([]*core.TreeItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.item)
	}
	return out, nil
}

// ResolveLinks resolves link targets as a note in the space would, and
// answers only for items the actor may view.
func (a *App) ResolveLinks(ctx context.Context, actor *core.Actor, in core.ResolveRequest) ([]*core.ResolvedTarget, error) {
	if anonymous(actor) {
		return nil, fmt.Errorf("resolve: %w", core.ErrUnauthorized)
	}
	if len(in.Targets) > 500 {
		return nil, core.Invalid("targets", "at most 500 targets")
	}
	space, _, _, err := a.spaceView(ctx, actor, in.SpaceID)
	if err != nil {
		return nil, err
	}
	fromPath := ""
	if in.FromNoteID != "" {
		it, _, err := a.requireItemAny(ctx, actor, in.FromNoteID, core.AccessView)
		if err != nil {
			return nil, err
		}
		if it.Note == nil || it.Note.SpaceID != space.ID {
			return nil, core.Invalid("fromNoteId", "the note is not in this space")
		}
		fromPath = it.Note.Path
	}
	snap, _, err := a.loadSnapshot(ctx, space.ID)
	if err != nil {
		return nil, err
	}
	cache := a.grants(actor)
	out := make([]*core.ResolvedTarget, 0, len(in.Targets))
	for _, target := range in.Targets {
		res := &core.ResolvedTarget{Target: target}
		out = append(out, res)
		e := resolveTargetText(snap, fromPath, target)
		if e == nil {
			continue
		}
		ref := itemRef{kind: core.ItemKind(e.Kind), id: e.ID, spaceID: space.ID, path: e.Path}
		if g, err := cache.item(ctx, ref); err != nil || !g.Access.AtLeast(core.AccessView) {
			continue
		}
		res.Kind, res.ID, res.Path = core.ItemKind(e.Kind), e.ID, e.Path
		res.URL = itemURL(space.Slug, res.Kind, e.Path)
	}
	return out, nil
}

// resolveTargetText resolves one target as a client writes it: a whole
// wikilink or markdown link, or a bare wikilink target with an optional
// heading and label.
func resolveTargetText(snap *links.Snapshot, fromPath, target string) *links.Entry {
	t := strings.TrimSpace(target)
	if t == "" {
		return nil
	}
	if strings.HasPrefix(t, "[") || strings.HasPrefix(t, "![") {
		for _, l := range links.Parse(t) {
			return snap.Resolve(fromPath, l)
		}
		return nil
	}
	if i := strings.IndexByte(t, '|'); i >= 0 {
		t = t[:i]
	}
	if i := strings.IndexByte(t, '#'); i >= 0 {
		t = t[:i]
	}
	t = strings.TrimSpace(t)
	form := links.FormWikilink
	if strings.Contains(t, "/") {
		form = links.FormPathWikilink
	}
	return snap.ResolveTarget(fromPath, t, form)
}

// trashSlot returns the trash entry an item heads, if any.
func (a *App) trashSlot(space *core.Space, id string) (ports.TrashedEntry, bool) {
	entries, err := a.files.ScanTrashEntries(space.Slug)
	if err != nil {
		a.log.Debug("scan trash failed", "space", space.Slug, "err", err)
		return ports.TrashedEntry{}, false
	}
	for _, e := range entries {
		if e.ID == id {
			return e, true
		}
	}
	return ports.TrashedEntry{}, false
}

// sortTreeItems orders siblings by position, then kind, then name ignoring
// case, at every level.
func sortTreeItems(level []*core.TreeItem, depth int) {
	if depth > 512 {
		return
	}
	sort.SliceStable(level, func(i, j int) bool {
		pi, pj := treePosition(level[i]), treePosition(level[j])
		if pi != pj {
			return pi < pj
		}
		ki, kj := kindOrder(level[i].Kind), kindOrder(level[j].Kind)
		if ki != kj {
			return ki < kj
		}
		ni, nj := strings.ToLower(treeName(level[i])), strings.ToLower(treeName(level[j]))
		if ni != nj {
			return ni < nj
		}
		return treeID(level[i]) < treeID(level[j])
	})
	for _, it := range level {
		sortTreeItems(it.Children, depth+1)
	}
}

func kindOrder(k core.ItemKind) int {
	switch k {
	case core.KindFolder:
		return 0
	case core.KindNote:
		return 1
	}
	return 2
}

func treePosition(it *core.TreeItem) int {
	switch {
	case it.Folder != nil:
		return it.Folder.Position
	case it.Note != nil:
		return it.Note.Position
	case it.File != nil:
		return it.File.Position
	}
	return 0
}

func treeName(it *core.TreeItem) string {
	switch {
	case it.Folder != nil:
		return it.Folder.Name
	case it.Note != nil:
		return it.Note.Title
	case it.File != nil:
		return it.File.Name
	}
	return ""
}

func treeID(it *core.TreeItem) string {
	switch {
	case it.Folder != nil:
		return it.Folder.ID
	case it.Note != nil:
		return it.Note.ID
	case it.File != nil:
		return it.File.ID
	}
	return ""
}

// treePath returns the entry path of a tree row.
func treePath(it *core.TreeItem) string {
	switch {
	case it.Folder != nil:
		return it.Folder.Path
	case it.Note != nil:
		return it.Note.Path
	case it.File != nil:
		return it.File.Path
	}
	return ""
}

// isFolderNotePath reports whether relPath is the folder note of a folder,
// which the space root has none of.
func isFolderNotePath(relPath string) bool {
	return path.Base(relPath) == folderNoteName && dirOf(relPath) != ""
}
