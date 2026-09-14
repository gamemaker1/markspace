package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestCreateNoteAllocatesPathAndParent(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")

	guide := h.folder(owner, sp, "", "guide")
	first := h.note(owner, sp, guide.ID, "Install", "one")
	second := h.note(owner, sp, guide.ID, "Install", "two")
	if first.Path != "guide/install.md" || first.ParentID != guide.ID || first.Position != 100 {
		t.Fatalf("first child = %s parent %s pos %d", first.Path, first.ParentID, first.Position)
	}
	if second.Path != "guide/install-2.md" || second.Position != 200 {
		t.Fatalf("second child = %s pos %d", second.Path, second.Position)
	}
	if row := h.row(second.ID); row.ParentID != guide.ID || row.CreatedBy != owner.UserID() || row.CreatedAt.IsZero() {
		t.Fatalf("row = %+v", row)
	}
	untitled := h.note(owner, sp, "", "  ", "")
	if untitled.Title != "Untitled" || untitled.Path != "untitled.md" || untitled.ParentID != "" {
		t.Fatalf("untitled = %+v", untitled)
	}
	if !h.exists(sp, "guide/install-2.md") {
		t.Fatal("file not written")
	}
	if disk, err := h.files.Read(sp.Slug, "guide/install.md"); err != nil || disk.ParentID != "" {
		t.Fatalf("frontmatter carries a parent: %+v, %v", disk, err)
	}

	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)
	stranger := h.user("Stranger", false)
	if _, err := h.app.CreateNote(h.ctx, viewer, core.NoteInput{SpaceID: sp.ID, Title: "x"}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("viewer create err = %v", err)
	}
	if _, err := h.app.CreateNote(h.ctx, stranger, core.NoteInput{SpaceID: sp.ID, Title: "x"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("stranger create err = %v", err)
	}
	if _, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, ParentID: core.NewID(), Title: "x"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("create under missing parent err = %v", err)
	}
	if err := h.app.TrashItem(h.ctx, owner, guide.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, ParentID: guide.ID, Title: "x"}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("create under trashed folder err = %v", err)
	}
}

func TestCreateNoteClientIDReplay(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	id := core.NewID()
	first, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{ID: id, SpaceID: sp.ID, Title: "Offline", Content: "a"})
	if err != nil || first.ID != id {
		t.Fatalf("create with client id = %+v, %v", first, err)
	}
	again, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{ID: id, SpaceID: sp.ID, Title: "Offline", Content: "a"})
	if err != nil || again.ID != id || again.Path != first.Path {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	other := h.user("Other", false)
	h.member(sp, other, core.RoleEditor)
	if _, err := h.app.CreateNote(h.ctx, other, core.NoteInput{ID: id, SpaceID: sp.ID, Title: "Offline"}); !errors.Is(err, core.ErrExists) {
		t.Fatalf("replay by another user err = %v", err)
	}
	if _, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{ID: "bad id", SpaceID: sp.ID}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad id err = %v", err)
	}
}

func TestGetNoteReadsDiskAndDropsMissing(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "from disk")

	got, grant, err := h.app.GetNote(h.ctx, owner, n.ID)
	if err != nil || got.Content != "from disk" || grant.Access != core.AccessManage || got.SpaceSlug != sp.Slug {
		t.Fatalf("GetNote = %+v, %+v, %v", got, grant, err)
	}
	byPath, _, err := h.app.GetNoteByPath(h.ctx, owner, sp.Slug, "plan")
	if err != nil || byPath.ID != n.ID {
		t.Fatalf("GetNoteByPath = %v, %v", byPath, err)
	}

	h.removeDisk(sp, n.Path)
	if _, _, err := h.app.GetNote(h.ctx, owner, n.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing file err = %v", err)
	}
	if _, err := h.db.GetNote(h.ctx, n.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("stale row not dropped")
	}
}

func TestUpdateNoteConflict(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "v1")
	stale := n.UpdatedAt

	h.clock.Advance(10 * time.Second)
	body := "v2"
	updated, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: &body, BaseUpdatedAt: &stale})
	if err != nil {
		t.Fatalf("first update: %v", err)
	}

	h.clock.Advance(10 * time.Second)
	body = "v3"
	if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: &body, BaseUpdatedAt: &stale}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("stale base err = %v, want conflict", err)
	}

	nearly := updated.UpdatedAt.Add(-500 * time.Millisecond)
	if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: &body, BaseUpdatedAt: &nearly}); err != nil {
		t.Fatalf("base within tolerance: %v", err)
	}
}

func TestUpdateNoteRenamesWithinFolder(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	folder := h.folder(owner, sp, "", "guide")
	n := h.note(owner, sp, folder.ID, "Install", "child")

	title := "Handbook"
	body := "renamed #alpha"
	tags := []string{"beta"}
	got, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Title: &title, Content: &body, Tags: &tags})
	if err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	if got.Path != "guide/handbook.md" || got.Slug != "handbook" {
		t.Fatalf("renamed path = %s slug %s", got.Path, got.Slug)
	}
	if !h.exists(sp, "guide/handbook.md") || h.exists(sp, "guide/install.md") {
		t.Fatal("file not renamed on disk")
	}
	if row := h.row(n.ID); row.Path != "guide/handbook.md" || row.ParentID != folder.ID {
		t.Fatalf("row = %+v", row)
	}
	disk, _ := h.files.Read(sp.Slug, "guide/handbook.md")
	if strings.Join(disk.Tags, ",") != "beta" {
		t.Fatalf("frontmatter tags = %v, want only the explicit tags", disk.Tags)
	}
	if strings.Join(h.row(n.ID).Tags, ",") != "beta,alpha" {
		t.Fatalf("index tags = %v", h.row(n.ID).Tags)
	}
	body = "renamed again without hashtag"
	if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: &body}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.row(n.ID).Tags, ",") != "beta" {
		t.Fatalf("stale inline tag kept: %v", h.row(n.ID).Tags)
	}

	// A folder note keeps its name when its title changes.
	page := h.note(owner, sp, folder.ID, "Page", "folder page")
	h.note(owner, sp, page.ID, "Kid", "")
	if row := h.row(page.ID); row.Path != "guide/page/index.md" {
		t.Fatalf("converted folder note path = %s", row.Path)
	}
	title = "The page"
	if got, err := h.app.UpdateNote(h.ctx, owner, page.ID, core.NoteUpdate{Title: &title}); err != nil || got.Path != "guide/page/index.md" {
		t.Fatalf("retitled folder note = %+v, %v", got, err)
	}
	if f, _ := h.db.GetFolderByPath(h.ctx, sp.ID, "guide/page"); f.IndexNoteID != page.ID {
		t.Fatalf("folder note pointer = %q, want %s", f.IndexNoteID, page.ID)
	}
}

func TestUpdateNoteSnapshots(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "start")
	count := func() int {
		v, _ := h.files.ListVersions(sp.ID, n.ID)
		return len(v)
	}
	update := func(body string, force bool) {
		t.Helper()
		if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: &body, Snapshot: force}); err != nil {
			t.Fatal(err)
		}
	}

	update("start plus", false)
	if count() != 1 {
		t.Fatalf("first edit versions = %d, want 1", count())
	}
	update("start plus a bit", true)
	if count() != 2 {
		t.Fatalf("forced versions = %d, want 2", count())
	}
	update("start plus a bit", true)
	if count() != 2 {
		t.Fatalf("unchanged body versions = %d, want 2", count())
	}
	update(strings.Repeat("x", 500), false)
	if count() != 3 {
		t.Fatalf("large edit versions = %d, want 3", count())
	}

	list, _ := h.app.ListVersions(h.ctx, owner, n.ID)
	diff, err := h.app.DiffVersion(h.ctx, owner, n.ID, list[len(list)-1].ID)
	if err != nil || !strings.Contains(diff, "start") {
		t.Fatalf("DiffVersion = %q, %v", diff, err)
	}
	restored, err := h.app.RestoreVersion(h.ctx, owner, n.ID, list[len(list)-1].ID)
	if err != nil || restored.Content != "start" {
		t.Fatalf("RestoreVersion = %v, %v", restored, err)
	}
}

func TestMoveNote(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "a")
	b := h.folder(owner, sp, a.ID, "b")
	c := h.note(owner, sp, b.ID, "C", "")
	d := h.folder(owner, sp, "", "d")

	for _, tc := range []struct {
		name   string
		parent string
	}{{"into itself", a.ID}, {"into its child", b.ID}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.app.MoveItem(h.ctx, owner, a.ID, tc.parent, 0); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("err = %v, want invalid", err)
			}
		})
	}
	if _, err := h.app.MoveNote(h.ctx, owner, c.ID, c.ID, 0); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("note under itself err = %v", err)
	}
	if _, err := h.app.MoveNote(h.ctx, owner, c.ID, h.note(owner, sp, "", "Other", "").ID, 0); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("note under a note err = %v", err)
	}

	moved, err := h.app.MoveItem(h.ctx, owner, b.ID, d.ID, 300)
	if err != nil {
		t.Fatalf("MoveItem: %v", err)
	}
	if moved.Folder.Path != "d/b" || moved.Folder.ParentID != d.ID || moved.Folder.Position != 300 {
		t.Fatalf("moved = %+v", moved.Folder)
	}
	if row := h.row(c.ID); row.Path != "d/b/c.md" || row.ParentID != b.ID {
		t.Fatalf("descendant row = %+v", row)
	}
	if !h.exists(sp, "d/b/c.md") || h.exists(sp, "a/b") {
		t.Fatal("files not moved")
	}

	top, err := h.app.MoveNote(h.ctx, owner, c.ID, "", 0)
	if err != nil || top.Path != "c.md" || top.ParentID != "" {
		t.Fatalf("move to root = %+v, %v", top, err)
	}
	if _, err := h.app.MoveNote(h.ctx, owner, c.ID, d.ID, 0); err != nil {
		t.Fatal(err)
	}
	h.note(owner, sp, "", "C", "")
	if _, err := h.app.MoveNote(h.ctx, owner, c.ID, "", 0); !errors.Is(err, core.ErrExists) {
		t.Fatalf("move onto a taken name err = %v", err)
	}
}

func TestTrashFolderMarksDescendants(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "a")
	b := h.note(owner, sp, a.ID, "B", "")
	inner := h.folder(owner, sp, a.ID, "inner")
	c := h.note(owner, sp, inner.ID, "C", "")
	keep := h.note(owner, sp, "", "Keep", "body keep")

	if err := h.app.TrashItem(h.ctx, owner, a.ID); err != nil {
		t.Fatalf("TrashItem: %v", err)
	}
	for _, id := range []string{a.ID, b.ID, inner.ID, c.ID} {
		if !itemDeleted(h.item(id)) {
			t.Fatalf("item %s not deleted", id)
		}
	}
	if h.row(keep.ID).Deleted {
		t.Fatal("unrelated note deleted")
	}
	if h.exists(sp, "a") || !h.exists(sp, ".trash/"+a.ID+"/a/inner/c.md") {
		t.Fatal("folder not moved to its trash slot")
	}
	tree, err := h.app.SpaceTree(h.ctx, owner, sp.ID)
	if err != nil || len(tree) != 1 || tree[0].Note == nil || tree[0].Note.ID != keep.ID {
		t.Fatalf("tree after trash = %v, %v", tree, err)
	}
	trash, err := h.app.ListTrash(h.ctx, owner, sp.ID)
	if err != nil || len(trash) != 2 {
		t.Fatalf("ListTrash = %d, %v", len(trash), err)
	}
	items, err := h.app.ListTrashItems(h.ctx, owner, sp.ID)
	if err != nil || len(items) != 1 || items[0].Folder == nil || items[0].Folder.ID != a.ID {
		t.Fatalf("ListTrashItems = %v, %v", items, err)
	}

	if err := h.app.TrashNote(h.ctx, owner, keep.ID); err != nil {
		t.Fatal(err)
	}
	got, _, err := h.app.GetNote(h.ctx, owner, keep.ID)
	if err != nil || got.Content != "body keep" || !got.Deleted {
		t.Fatalf("GetNote trashed = %+v, %v", got, err)
	}
	body := "edit"
	if _, err := h.app.UpdateNote(h.ctx, owner, keep.ID, core.NoteUpdate{Content: &body}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("edit trashed err = %v", err)
	}
	if err := h.app.TrashNote(h.ctx, owner, keep.ID); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("trash twice err = %v", err)
	}
	if err := h.app.RestoreNote(h.ctx, owner, b.ID); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("restore a note trashed with its folder err = %v", err)
	}
}

func TestRestoreFolderToOccupiedPath(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	guide := h.folder(owner, sp, "", "guide")
	child := h.note(owner, sp, guide.ID, "Install", "old install")
	if err := h.app.TrashItem(h.ctx, owner, guide.ID); err != nil {
		t.Fatal(err)
	}
	h.folder(owner, sp, "", "guide")

	restored, err := h.app.RestoreItem(h.ctx, owner, guide.ID)
	if err != nil {
		t.Fatalf("RestoreItem: %v", err)
	}
	if restored.Folder.Path != "guide (2)" || restored.Folder.Deleted {
		t.Fatalf("restored folder = %+v", restored.Folder)
	}
	crow := h.row(child.ID)
	if crow.Deleted || crow.Path != "guide (2)/install.md" {
		t.Fatalf("restored child row = %+v", crow)
	}
	got, _, err := h.app.GetNote(h.ctx, owner, child.ID)
	if err != nil || got.Content != "old install" {
		t.Fatalf("restored note = %+v, %v", got, err)
	}
	if _, err := h.app.RestoreItem(h.ctx, owner, guide.ID); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("restore live folder err = %v", err)
	}

	n := h.note(owner, sp, "", "Plan", "old")
	if err := h.app.TrashNote(h.ctx, owner, n.ID); err != nil {
		t.Fatal(err)
	}
	h.writeDisk(sp, "plan.md", "---\nid: "+core.NewID()+"\ntitle: Plan\n---\nnew")
	if err := h.app.RestoreNote(h.ctx, owner, n.ID); err != nil {
		t.Fatalf("RestoreNote: %v", err)
	}
	row := h.row(n.ID)
	if row.Deleted || row.Path != "plan (2).md" {
		t.Fatalf("restored row = %+v", row)
	}
	if extraString(row.Extra, "trashed_from") != "" {
		t.Fatalf("trash keys kept: %v", row.Extra)
	}
}

func TestDeleteNotePermissions(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	editor := h.user("Editor", false)
	h.member(sp, editor, core.RoleEditor)
	mine := h.folder(editor, sp, "", "mine")
	child := h.note(editor, sp, mine.ID, "Kid", "")
	theirs := h.note(owner, sp, "", "Theirs", "")

	if err := h.app.DeleteNote(h.ctx, editor, theirs.ID); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("editor deletes other's note err = %v", err)
	}
	if err := h.app.DeleteItem(h.ctx, editor, mine.ID); err != nil {
		t.Fatalf("author delete: %v", err)
	}
	if _, err := h.db.GetNote(h.ctx, child.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("child row kept: %v", err)
	}
	if _, err := h.db.GetFolder(h.ctx, mine.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("folder row kept: %v", err)
	}
	if h.exists(sp, "mine") {
		t.Fatal("folder kept on disk")
	}
	if err := h.app.TrashNote(h.ctx, owner, theirs.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.app.DeleteNote(h.ctx, owner, theirs.ID); err != nil {
		t.Fatalf("manager deletes trashed: %v", err)
	}
	if trashed, _ := h.files.ScanTrashEntries(sp.Slug); len(trashed) != 0 {
		t.Fatalf("trash entry kept: %d", len(trashed))
	}
}

// itShape renders a tree as names with children in parentheses.
func itShape(nodes []*core.TreeItem) string {
	parts := make([]string, 0, len(nodes))
	for _, n := range nodes {
		s := treeName(n)
		if n.Kind == core.KindFolder && n.Folder.IndexNoteID != "" {
			s += "*"
		}
		if len(n.Children) > 0 {
			s += "(" + itShape(n.Children) + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

func TestSpaceTreeOrderingAndShape(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	h.note(owner, sp, "", "B", "")
	a := h.note(owner, sp, "", "A", "")
	c, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, Title: "C", Position: 50})
	if err != nil {
		t.Fatal(err)
	}
	folder := h.folder(owner, sp, "", "Folder")
	for _, title := range []string{"Zeta", "yankee"} {
		if _, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, ParentID: folder.ID, Title: title, Position: 10}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.app.CreateFolder(h.ctx, owner, core.FolderInput{SpaceID: sp.ID, ParentID: folder.ID, Name: "sub"}); err != nil {
		t.Fatal(err)
	}
	h.upload(owner, sp, folder.ID, "pic.png", "png")
	h.note(owner, sp, a.ID, "Leaf", "")

	tree, err := h.app.SpaceTree(h.ctx, owner, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := itShape(tree), "C B a*(Leaf) Folder(yankee Zeta sub pic.png)"; got != want {
		t.Fatalf("tree = %q, want %q", got, want)
	}
	if tree[0].Note.ID != c.ID {
		t.Fatal("first root is not C")
	}

	guest := h.user("Guest", false)
	h.share(folder.ID, guest, core.AccessView, true)
	guestTree, err := h.app.SpaceTree(h.ctx, guest, sp.ID)
	if err != nil {
		t.Fatalf("guest tree: %v", err)
	}
	if got, want := itShape(guestTree), "Folder(yankee Zeta sub pic.png)"; got != want {
		t.Fatalf("guest tree = %q, want %q", got, want)
	}
	if _, err := h.app.SpaceTree(h.ctx, h.user("Nobody", false), sp.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("stranger tree err = %v", err)
	}
}
