package app

import (
	"errors"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// accTreeIDs flattens a tree into sorted ids.
func accTreeIDs(level []*core.TreeItem) []string {
	var out []string
	var walk func([]*core.TreeItem)
	walk = func(l []*core.TreeItem) {
		for _, it := range l {
			out = append(out, treeID(it))
			walk(it.Children)
		}
	}
	walk(level)
	sort.Strings(out)
	return out
}

// accWantErr fails when err does not match want, where nil means success.
func accWantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		return
	}
	if !errors.Is(err, want) {
		t.Fatalf("%s err = %v, want %v", what, err, want)
	}
}

func TestFolderShareWithoutChildrenListsChildren(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	box := h.folder(owner, sp, "", "Box")
	note := h.note(owner, sp, box.ID, "Inside", "secret")
	file := h.upload(owner, sp, box.ID, "b.png", "png")
	sub := h.folder(owner, sp, box.ID, "Sub")
	flat := h.user("Flat", false)
	h.share(box.ID, flat, core.AccessView, false)
	kids := h.user("Kids", false)
	h.share(box.ID, kids, core.AccessView, true)

	view, grant, err := h.app.GetFolder(h.ctx, flat, box.ID)
	if err != nil || grant.Access != core.AccessView || len(view.Children) != 3 {
		t.Fatalf("GetFolder = %+v, %v, %v", view, grant, err)
	}
	_, _, err = h.app.GetNote(h.ctx, flat, note.ID)
	accWantErr(t, "flat GetNote", err, core.ErrNotFound)
	_, _, err = h.app.GetFile(h.ctx, flat, file.ID)
	accWantErr(t, "flat GetFile", err, core.ErrNotFound)
	_, _, err = h.app.GetFolder(h.ctx, flat, sub.ID)
	accWantErr(t, "flat GetFolder sub", err, core.ErrNotFound)

	if _, _, err := h.app.GetFile(h.ctx, kids, file.ID); err != nil {
		t.Fatalf("kids GetFile: %v", err)
	}
	if _, _, err := h.app.GetFolder(h.ctx, kids, sub.ID); err != nil {
		t.Fatalf("kids GetFolder sub: %v", err)
	}
	if n, _, err := h.app.GetNote(h.ctx, kids, note.ID); err != nil || n.Content != "secret" {
		t.Fatalf("kids GetNote = %+v, %v", n, err)
	}
	_, err = h.app.RenameFile(h.ctx, kids, file.ID, "c.png")
	accWantErr(t, "view share rename", err, core.ErrForbidden)
}

func TestFolderNoteShareCoversFolder(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	guide := h.note(owner, sp, "", "Guide", "top")
	install := h.note(owner, sp, guide.ID, "Install", "steps")
	folder, err := h.db.GetFolderByPath(h.ctx, sp.ID, "guide")
	if err != nil {
		t.Fatal(err)
	}
	pic := h.upload(owner, sp, folder.ID, "p.png", "png")
	other := h.note(owner, sp, "", "Other", "")
	withKids := h.user("WithKids", false)
	h.share(guide.ID, withKids, core.AccessComment, true)
	noKids := h.user("NoKids", false)
	h.share(guide.ID, noKids, core.AccessComment, false)

	if _, g, err := h.app.GetFolder(h.ctx, withKids, folder.ID); err != nil || g.Access != core.AccessComment {
		t.Fatalf("folder through folder note = %v, %v", g, err)
	}
	if _, _, err := h.app.GetNote(h.ctx, withKids, install.ID); err != nil {
		t.Fatalf("page through folder note: %v", err)
	}
	if _, _, err := h.app.GetFile(h.ctx, withKids, pic.ID); err != nil {
		t.Fatalf("file through folder note: %v", err)
	}
	_, _, err = h.app.GetNote(h.ctx, withKids, other.ID)
	accWantErr(t, "sibling outside the folder", err, core.ErrNotFound)
	if _, _, err := h.app.GetNote(h.ctx, noKids, guide.ID); err != nil {
		t.Fatalf("folder note itself: %v", err)
	}
	_, _, err = h.app.GetNote(h.ctx, noKids, install.ID)
	accWantErr(t, "page without children", err, core.ErrNotFound)
}

func TestPublicLinksOnFilesAndFolders(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	box := h.folder(owner, sp, "", "Box")
	pic := h.upload(owner, sp, box.ID, "pic.png", "png-bytes")
	loose := h.upload(owner, sp, "", "loose.txt", "text")

	fileLink, err := h.app.CreatePublicLink(h.ctx, owner, core.PublicLinkInput{NoteID: pic.ID, Access: core.AccessManage})
	if err != nil || fileLink.Access != core.AccessComment {
		t.Fatalf("file link = %+v, %v", fileLink, err)
	}
	actor, item, err := h.app.ResolvePublicItem(h.ctx, fileLink.Token, "")
	if err != nil || item.File == nil || item.File.ID != pic.ID || actor.Link == nil {
		t.Fatalf("ResolvePublicItem file = %+v, %v", item, err)
	}
	_, _, rc, err := h.app.OpenFile(h.ctx, actor, pic.ID, "")
	if err != nil {
		t.Fatalf("link OpenFile: %v", err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "png-bytes" {
		t.Fatalf("content = %q", data)
	}
	_, _, _, err = h.app.OpenFile(h.ctx, actor, loose.ID, "")
	accWantErr(t, "link opens another file", err, core.ErrNotFound)
	_, _, err = h.app.ResolvePublicLink(h.ctx, fileLink.Token, "")
	accWantErr(t, "note resolve of a file link", err, core.ErrNotFound)

	flat, err := h.app.CreatePublicLink(h.ctx, owner, core.PublicLinkInput{NoteID: box.ID})
	if err != nil {
		t.Fatal(err)
	}
	flatActor, flatItem, err := h.app.ResolvePublicItem(h.ctx, flat.Token, "")
	if err != nil || flatItem.Folder == nil {
		t.Fatalf("folder link = %+v, %v", flatItem, err)
	}
	view, _, err := h.app.GetFolder(h.ctx, flatActor, box.ID)
	if err != nil || len(view.Children) != 1 {
		t.Fatalf("link GetFolder = %+v, %v", view, err)
	}
	_, _, err = h.app.GetFile(h.ctx, flatActor, pic.ID)
	accWantErr(t, "link without children opens child", err, core.ErrNotFound)

	kids, err := h.app.CreatePublicLink(h.ctx, owner, core.PublicLinkInput{NoteID: box.ID, Access: core.AccessComment, IncludeChildren: true})
	if err != nil {
		t.Fatal(err)
	}
	kidActor, _, err := h.app.ResolvePublicItem(h.ctx, kids.Token, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, g, err := h.app.GetFile(h.ctx, kidActor, pic.ID); err != nil || g.Access != core.AccessComment {
		t.Fatalf("link with children GetFile = %v, %v", g, err)
	}
	_, err = h.app.RenameFile(h.ctx, kidActor, pic.ID, "x.png")
	accWantErr(t, "link actor renames", err, core.ErrForbidden)

	if err := h.app.TrashItem(h.ctx, owner, pic.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = h.app.ResolvePublicItem(h.ctx, fileLink.Token, "")
	accWantErr(t, "link on a trashed file", err, core.ErrNotFound)
	_, _, err = h.app.GetFile(h.ctx, kidActor, pic.ID)
	accWantErr(t, "link reaches a trashed child", err, core.ErrNotFound)
	if err := h.app.TrashItem(h.ctx, owner, box.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = h.app.ResolvePublicItem(h.ctx, kids.Token, "")
	accWantErr(t, "link on a trashed folder", err, core.ErrNotFound)
}

func TestCommentsOnFilesAndFolders(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	box := h.folder(owner, sp, "", "Box")
	pic := h.upload(owner, sp, box.ID, "pic.png", "png")
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)

	for _, id := range []string{box.ID, pic.ID} {
		c, err := h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: id, Body: "looks good"})
		if err != nil || c.NoteID != id || c.Anchor != nil {
			t.Fatalf("comment on %s = %+v, %v", id, c, err)
		}
		_, err = h.app.CreateComment(h.ctx, owner, core.CommentInput{NoteID: id, Body: "anchored", Anchor: &core.CommentAnchor{Exact: "x", End: 1}})
		accWantErr(t, "anchored comment on "+id, err, core.ErrInvalid)
		_, err = h.app.CreateComment(h.ctx, viewer, core.CommentInput{NoteID: id, Body: "nope"})
		accWantErr(t, "viewer comment on "+id, err, core.ErrForbidden)
		reply, err := h.app.CreateComment(h.ctx, viewer, core.CommentInput{NoteID: id, ParentID: c.ID, Body: "reply"})
		if !errors.Is(err, core.ErrForbidden) {
			t.Fatalf("viewer reply = %+v, %v", reply, err)
		}
		list, err := h.app.ListComments(h.ctx, viewer, id)
		if err != nil || len(list) != 1 || list[0].Body != "looks good" {
			t.Fatalf("ListComments(%s) = %+v, %v", id, list, err)
		}
		if _, err := h.app.ResolveComment(h.ctx, owner, c.ID, true); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
	stranger := h.user("Stranger", false)
	_, err := h.app.ListComments(h.ctx, stranger, pic.ID)
	accWantErr(t, "stranger lists file comments", err, core.ErrNotFound)
}

func TestCreateNeedsEditOnParent(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	box := h.folder(owner, sp, "", "Box")
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)
	shared := h.user("Shared", false)
	h.share(box.ID, shared, core.AccessEdit, false)
	reader := h.user("Reader", false)
	h.share(box.ID, reader, core.AccessView, true)

	cases := []struct {
		name     string
		actor    *core.Actor
		parentID string
		want     error
	}{
		{"owner at the root", owner, "", nil},
		{"viewer at the root", viewer, "", core.ErrForbidden},
		{"viewer in a folder", viewer, box.ID, core.ErrForbidden},
		{"edit share in the folder", shared, box.ID, nil},
		{"edit share at the root", shared, "", core.ErrNotFound},
		{"view share in the folder", reader, box.ID, core.ErrForbidden},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			suffix := string(rune('a' + i))
			_, err := h.app.CreateFolder(h.ctx, tc.actor, core.FolderInput{SpaceID: sp.ID, ParentID: tc.parentID, Name: "F" + suffix})
			accWantErr(t, "CreateFolder", err, tc.want)
			_, err = h.app.UploadFile(h.ctx, tc.actor, core.FileUpload{SpaceID: sp.ID, ParentID: tc.parentID, Name: "f" + suffix + ".txt"}, strings.NewReader("x"))
			accWantErr(t, "UploadFile", err, tc.want)
			_, err = h.app.CreateNote(h.ctx, tc.actor, core.NoteInput{SpaceID: sp.ID, ParentID: tc.parentID, Title: "N" + suffix})
			accWantErr(t, "CreateNote", err, tc.want)
		})
	}
}

func TestMoveNeedsEditOnItemAndBothParents(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "A")
	b := h.folder(owner, sp, "", "B")
	c := h.folder(owner, sp, "", "C")
	file := h.upload(owner, sp, a.ID, "f.txt", "x")
	u := h.user("Mover", false)
	h.share(a.ID, u, core.AccessEdit, true)
	h.share(b.ID, u, core.AccessView, true)
	h.share(c.ID, u, core.AccessEdit, false)
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)

	_, err := h.app.MoveItem(h.ctx, u, file.ID, b.ID, 0)
	accWantErr(t, "move into a view-only folder", err, core.ErrForbidden)
	_, err = h.app.MoveItem(h.ctx, u, file.ID, "", 0)
	accWantErr(t, "move to a root without space access", err, core.ErrNotFound)
	_, err = h.app.MoveItem(h.ctx, viewer, file.ID, c.ID, 0)
	accWantErr(t, "space viewer moves", err, core.ErrForbidden)
	moved, err := h.app.MoveItem(h.ctx, u, file.ID, c.ID, 0)
	if err != nil || moved.File.Path != "C/f.txt" {
		t.Fatalf("move = %+v, %v", moved, err)
	}
	// In C only the folder share without children applies, so the file itself
	// is out of reach and cannot move back.
	_, err = h.app.MoveItem(h.ctx, u, file.ID, a.ID, 0)
	accWantErr(t, "move of an item without access", err, core.ErrNotFound)
	_, err = h.app.MoveItem(h.ctx, owner, a.ID, a.ID, 0)
	accWantErr(t, "folder into itself", err, core.ErrInvalid)
}

func TestCopyNeedsViewOnSourceAndEditOnTarget(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	src := h.space(owner, "Source")
	dst := h.space(owner, "Target")
	pic := h.upload(owner, src, "", "pic.png", "png")
	u := h.user("Copier", false)
	h.share(pic.ID, u, core.AccessView, false)
	h.member(dst, u, core.RoleEditor)
	viewer := h.user("Viewer", false)
	h.member(dst, viewer, core.RoleViewer)
	h.share(pic.ID, viewer, core.AccessView, false)
	stranger := h.user("Stranger", false)
	h.member(dst, stranger, core.RoleEditor)

	out, err := h.app.CopyItem(h.ctx, u, pic.ID, core.CopyInput{SpaceID: dst.ID})
	if err != nil || out.File == nil || out.File.SpaceID != dst.ID || out.File.ID == pic.ID {
		t.Fatalf("copy across spaces = %+v, %v", out, err)
	}
	_, err = h.app.CopyItem(h.ctx, u, pic.ID, core.CopyInput{})
	accWantErr(t, "copy into a space without edit", err, core.ErrNotFound)
	_, err = h.app.CopyItem(h.ctx, viewer, pic.ID, core.CopyInput{SpaceID: dst.ID})
	accWantErr(t, "copy into a view-only space", err, core.ErrForbidden)
	_, err = h.app.CopyItem(h.ctx, stranger, pic.ID, core.CopyInput{SpaceID: dst.ID})
	accWantErr(t, "copy of a hidden source", err, core.ErrNotFound)
}

func TestDeleteNeedsManageOrCreatorEdit(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	alice := h.user("Alice", false)
	h.member(sp, alice, core.RoleEditor)
	bob := h.user("Bob", false)
	h.member(sp, bob, core.RoleEditor)
	mine := h.upload(alice, sp, "", "mine.txt", "x")
	folder := h.folder(alice, sp, "", "Mine")
	theirs := h.upload(owner, sp, "", "theirs.txt", "y")

	accWantErr(t, "editor deletes another's file", h.app.DeleteItem(h.ctx, bob, mine.ID), core.ErrForbidden)
	accWantErr(t, "editor deletes another's folder", h.app.DeleteItem(h.ctx, bob, folder.ID), core.ErrForbidden)
	accWantErr(t, "creator deletes own file", h.app.DeleteItem(h.ctx, alice, mine.ID), nil)
	accWantErr(t, "creator deletes own folder", h.app.DeleteItem(h.ctx, alice, folder.ID), nil)
	accWantErr(t, "editor deletes owner's file", h.app.DeleteItem(h.ctx, alice, theirs.ID), core.ErrForbidden)
	accWantErr(t, "manager deletes", h.app.DeleteItem(h.ctx, owner, theirs.ID), nil)
	if h.exists(sp, "mine.txt") || h.exists(sp, "Mine") || h.exists(sp, "theirs.txt") {
		t.Fatal("deleted entries remain on disk")
	}
}

func TestSpaceTreeForShareOnlyUser(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "A")
	b := h.folder(owner, sp, a.ID, "B")
	inB := h.note(owner, sp, b.ID, "In B", "")
	fileB := h.upload(owner, sp, b.ID, "b.txt", "x")
	hidden := h.note(owner, sp, a.ID, "Hidden", "")
	loose := h.note(owner, sp, "", "Loose", "")
	u := h.user("Guest", false)
	h.share(b.ID, u, core.AccessView, true)
	h.share(loose.ID, u, core.AccessView, false)

	tree, err := h.app.SpaceTree(h.ctx, u, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 {
		t.Fatalf("roots = %d, want the shared folder and the shared note", len(tree))
	}
	got := accTreeIDs(tree)
	want := []string{b.ID, inB.ID, fileB.ID, loose.ID}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tree ids = %v, want %v", got, want)
	}
	for _, root := range tree {
		if root.Folder != nil && len(root.Children) != 2 {
			t.Fatalf("shared folder children = %d", len(root.Children))
		}
	}
	full, err := h.app.SpaceTree(h.ctx, owner, sp.ID)
	if err != nil || len(full) != 2 {
		t.Fatalf("owner roots = %d, %v", len(full), err)
	}
	if ids := accTreeIDs(full); len(ids) != 6 {
		t.Fatalf("owner tree ids = %v", ids)
	}
	_ = hidden
	stranger := h.user("Stranger", false)
	_, err = h.app.SpaceTree(h.ctx, stranger, sp.ID)
	accWantErr(t, "stranger tree", err, core.ErrNotFound)
}
