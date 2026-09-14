package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// itIs fails the test unless err wraps want.
func itIs(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

// itSHA returns the hex SHA-256 of s.
func itSHA(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// itRead opens a file through the app and returns its content.
func itRead(t *testing.T, h *rharness, actor *core.Actor, id, version string) string {
	t.Helper()
	_, _, rc, err := h.app.OpenFile(h.ctx, actor, id, version)
	if err != nil {
		t.Fatalf("OpenFile %s %s: %v", id, version, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// itPNG encodes a solid image.
func itPNG(t *testing.T, w, hgt int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, hgt))); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestCreateFolderRulesAndReplay(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	docs := h.folder(owner, sp, "", "Docs")
	if docs.Path != "Docs" || docs.Position != 100 || docs.CreatedBy != owner.UserID() || !h.exists(sp, "Docs") {
		t.Fatalf("folder = %+v", docs)
	}
	sub := h.folder(owner, sp, docs.ID, "Sub folder")
	if sub.Path != "Docs/Sub folder" || sub.ParentID != docs.ID {
		t.Fatalf("sub = %+v", sub)
	}
	_, err := h.app.CreateFolder(h.ctx, owner, core.FolderInput{SpaceID: sp.ID, Name: "docs"})
	itIs(t, "case collision", err, core.ErrExists)
	for _, bad := range []string{"", ".hidden", "a/b", " pad", "Thumbs.db", ".."} {
		_, err := h.app.CreateFolder(h.ctx, owner, core.FolderInput{SpaceID: sp.ID, Name: bad})
		itIs(t, "name "+bad, err, core.ErrInvalid)
	}
	note := h.note(owner, sp, "", "Page", "")
	_, err = h.app.CreateFolder(h.ctx, owner, core.FolderInput{SpaceID: sp.ID, ParentID: note.ID, Name: "x"})
	itIs(t, "note parent", err, core.ErrInvalid)

	id := core.NewID()
	first, err := h.app.CreateFolder(h.ctx, owner, core.FolderInput{ID: id, SpaceID: sp.ID, Name: "Offline"})
	if err != nil || first.ID != id {
		t.Fatalf("client id = %+v, %v", first, err)
	}
	again, err := h.app.CreateFolder(h.ctx, owner, core.FolderInput{ID: id, SpaceID: sp.ID, Name: "Offline"})
	if err != nil || again.ID != id || again.Path != "Offline" {
		t.Fatalf("replay = %+v, %v", again, err)
	}
	editor := h.user("Editor", false)
	h.member(sp, editor, core.RoleEditor)
	_, err = h.app.CreateFolder(h.ctx, editor, core.FolderInput{ID: id, SpaceID: sp.ID, Name: "Offline"})
	itIs(t, "replay by another user", err, core.ErrExists)
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)
	_, err = h.app.CreateFolder(h.ctx, viewer, core.FolderInput{SpaceID: sp.ID, Name: "Nope"})
	itIs(t, "viewer", err, core.ErrForbidden)

	// A folder shared with edit lets its holder create inside it only.
	guest := h.user("Guest", false)
	h.share(docs.ID, guest, core.AccessEdit, false)
	if _, err := h.app.CreateFolder(h.ctx, guest, core.FolderInput{ParentID: docs.ID, Name: "Guest"}); err != nil {
		t.Fatalf("guest create in shared folder: %v", err)
	}
	_, err = h.app.CreateFolder(h.ctx, guest, core.FolderInput{SpaceID: sp.ID, Name: "Root"})
	itIs(t, "guest at root", err, core.ErrNotFound)

	// A name held only on disk also counts.
	if err := os.WriteFile(h.diskPath(sp, "Outside.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = h.app.CreateFolder(h.ctx, owner, core.FolderInput{SpaceID: sp.ID, Name: "outside.TXT"})
	itIs(t, "disk collision", err, core.ErrExists)
}

func TestUploadFileConflicts(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	folder := h.folder(owner, sp, "", "Files")
	a := h.upload(owner, sp, folder.ID, "a.txt", "alpha")
	if a.Path != "Files/a.txt" || a.Size != 5 || a.SHA256 != itSHA("alpha") || a.ParentID != folder.ID {
		t.Fatalf("upload = %+v", a)
	}
	if a.URL != "/files/"+sp.Slug+"/"+a.ID+"/a.txt" || a.MIME == "" {
		t.Fatalf("decorated = %+v", a)
	}
	_, err := h.app.UploadFile(h.ctx, owner, core.FileUpload{ParentID: folder.ID, Name: "A.TXT"}, strings.NewReader("x"))
	itIs(t, "taken name", err, core.ErrExists)
	renamed, err := h.app.UploadFile(h.ctx, owner, core.FileUpload{ParentID: folder.ID, Name: "a.txt", OnConflict: core.OnConflictRename}, strings.NewReader("beta"))
	if err != nil || renamed.Name != "a (2).txt" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	replaced, err := h.app.UploadFile(h.ctx, owner, core.FileUpload{ParentID: folder.ID, Name: "a.txt", OnConflict: core.OnConflictReplace}, strings.NewReader("gamma"))
	if err != nil || replaced.ID != a.ID || replaced.Versions != 1 || replaced.SHA256 != itSHA("gamma") {
		t.Fatalf("replace = %+v, %v", replaced, err)
	}
	if got := itRead(t, h, owner, a.ID, ""); got != "gamma" {
		t.Fatalf("content = %q", got)
	}
	h.folder(owner, sp, folder.ID, "dir")
	_, err = h.app.UploadFile(h.ctx, owner, core.FileUpload{ParentID: folder.ID, Name: "dir", OnConflict: core.OnConflictReplace}, strings.NewReader("x"))
	itIs(t, "replace a folder", err, core.ErrExists)
	_, err = h.app.UploadFile(h.ctx, owner, core.FileUpload{SpaceID: sp.ID, Name: "note.md"}, strings.NewReader("x"))
	itIs(t, "markdown upload", err, core.ErrInvalid)
	_, err = h.app.UploadFile(h.ctx, owner, core.FileUpload{SpaceID: sp.ID, Name: "x.bin", OnConflict: "overwrite"}, strings.NewReader("x"))
	itIs(t, "bad policy", err, core.ErrInvalid)
	_, err = h.app.UploadFile(h.ctx, owner, core.FileUpload{SpaceID: sp.ID, Name: " "}, strings.NewReader("x"))
	itIs(t, "empty name", err, core.ErrInvalid)
	if got, err := h.app.UploadFile(h.ctx, owner, core.FileUpload{SpaceID: sp.ID, Name: `C:\dir\photo.jpg`}, strings.NewReader("x")); err != nil || got.Name != "photo.jpg" {
		t.Fatalf("windows path name = %+v, %v", got, err)
	}

	h.cfg.MaxUploadMB = 1
	_, err = h.app.UploadFile(h.ctx, owner, core.FileUpload{SpaceID: sp.ID, Name: "big.bin"}, bytes.NewReader(make([]byte, 2<<20)))
	itIs(t, "over size", err, core.ErrTooLarge)
	if h.exists(sp, "big.bin") {
		t.Fatal("oversized upload left on disk")
	}
	h.cfg.MaxUploadMB = 50

	id := core.NewID()
	first, err := h.app.UploadFile(h.ctx, owner, core.FileUpload{ID: id, SpaceID: sp.ID, Name: "offline.txt"}, strings.NewReader("one"))
	if err != nil || first.ID != id {
		t.Fatalf("client id upload = %+v, %v", first, err)
	}
	again, err := h.app.UploadFile(h.ctx, owner, core.FileUpload{ID: id, SpaceID: sp.ID, Name: "offline.txt"}, strings.NewReader("one"))
	if err != nil || again.ID != id || again.Path != "offline.txt" {
		t.Fatalf("replayed upload = %+v, %v", again, err)
	}
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)
	_, err = h.app.UploadFile(h.ctx, viewer, core.FileUpload{SpaceID: sp.ID, Name: "v.txt"}, strings.NewReader("x"))
	itIs(t, "viewer upload", err, core.ErrForbidden)
}

func TestUploadPasteFor(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	notes := h.folder(owner, sp, "", "notes")
	n := h.note(owner, sp, notes.ID, "Plan", "")
	f, err := h.app.UploadFile(h.ctx, owner, core.FileUpload{PasteFor: n.ID, Name: "shot.png"}, strings.NewReader("png"))
	if err != nil || f.Path != "notes/shot.png" || f.ParentID != notes.ID {
		t.Fatalf("paste next to note = %+v, %v", f, err)
	}
	folder := " attachments/img/ "
	updated, err := h.app.UpdateSpace(h.ctx, owner, sp.ID, core.SpaceInput{PasteFolder: &folder})
	if err != nil || updated.PasteFolder != "attachments/img" {
		t.Fatalf("paste folder setting = %+v, %v", updated, err)
	}
	f, err = h.app.UploadFile(h.ctx, owner, core.FileUpload{PasteFor: n.ID, Name: "shot.png"}, strings.NewReader("png"))
	if err != nil || f.Path != "notes/attachments/img/shot.png" {
		t.Fatalf("paste into paste folder = %+v, %v", f, err)
	}
	img, err := h.db.GetFolderByPath(h.ctx, sp.ID, "notes/attachments/img")
	if err != nil || img.ID != f.ParentID {
		t.Fatalf("paste folder row = %+v, %v", img, err)
	}
	bad := "../up"
	_, err = h.app.UpdateSpace(h.ctx, owner, sp.ID, core.SpaceInput{PasteFolder: &bad})
	itIs(t, "bad paste folder", err, core.ErrInvalid)
	_, err = h.app.UploadFile(h.ctx, owner, core.FileUpload{PasteFor: notes.ID, Name: "x.png"}, strings.NewReader("png"))
	itIs(t, "paste for a folder", err, core.ErrInvalid)
}

func TestRenameFolderAndFile(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	proj := h.folder(owner, sp, "", "Projects")
	n := h.note(owner, sp, proj.ID, "Survey", "")
	f := h.upload(owner, sp, proj.ID, "wall.jpg", "jpg")
	h.upload(owner, sp, proj.ID, "door.jpg", "jpg")

	renamed, err := h.app.RenameFolder(h.ctx, owner, proj.ID, "Work")
	if err != nil || renamed.Path != "Work" || renamed.Name != "Work" {
		t.Fatalf("RenameFolder = %+v, %v", renamed, err)
	}
	if row := h.row(n.ID); row.Path != "Work/survey.md" {
		t.Fatalf("note row = %s", row.Path)
	}
	if !h.exists(sp, "Work/wall.jpg") || h.exists(sp, "Projects") {
		t.Fatal("disk not renamed")
	}
	if got, err := h.app.RenameFolder(h.ctx, owner, proj.ID, "work"); err != nil || got.Path != "work" {
		t.Fatalf("case-only rename = %+v, %v", got, err)
	}
	file, err := h.app.RenameFile(h.ctx, owner, f.ID, "east wall.jpg")
	if err != nil || file.Path != "work/east wall.jpg" || file.URL == "" {
		t.Fatalf("RenameFile = %+v, %v", file, err)
	}
	_, err = h.app.RenameFile(h.ctx, owner, f.ID, "Door.JPG")
	itIs(t, "rename onto a sibling", err, core.ErrExists)
	_, err = h.app.RenameFile(h.ctx, owner, f.ID, "wall.md")
	itIs(t, "rename to .md", err, core.ErrInvalid)
	_, err = h.app.RenameFile(h.ctx, owner, n.ID, "x")
	itIs(t, "rename a note as a file", err, core.ErrNotFound)
	_, err = h.app.RenameFolder(h.ctx, owner, f.ID, "x")
	itIs(t, "rename a file as a folder", err, core.ErrNotFound)
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)
	_, err = h.app.RenameFile(h.ctx, viewer, f.ID, "x.jpg")
	itIs(t, "viewer rename", err, core.ErrForbidden)
	if err := h.app.TrashItem(h.ctx, owner, f.ID); err != nil {
		t.Fatal(err)
	}
	_, err = h.app.RenameFile(h.ctx, owner, f.ID, "y.jpg")
	itIs(t, "rename trashed", err, core.ErrConflict)
}

func TestMoveItemReorderAndErrors(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "a")
	b := h.folder(owner, sp, a.ID, "b")
	f := h.upload(owner, sp, "", "f.txt", "f")

	_, err := h.app.MoveItem(h.ctx, owner, a.ID, b.ID, 0)
	itIs(t, "into a descendant", err, core.ErrInvalid)
	_, err = h.app.MoveItem(h.ctx, owner, f.ID, f.ID, 0)
	itIs(t, "into itself", err, core.ErrInvalid)
	_, err = h.app.MoveItem(h.ctx, owner, f.ID, "", -1)
	itIs(t, "negative position", err, core.ErrInvalid)

	moved, err := h.app.MoveItem(h.ctx, owner, f.ID, b.ID, 0)
	if err != nil || moved.File.Path != "a/b/f.txt" || moved.File.ParentID != b.ID || moved.File.Position != 100 {
		t.Fatalf("move file = %+v, %v", moved, err)
	}
	if !h.exists(sp, "a/b/f.txt") {
		t.Fatal("file not on disk at new path")
	}
	reordered, err := h.app.MoveItem(h.ctx, owner, f.ID, b.ID, 5)
	if err != nil || reordered.File.Position != 5 || reordered.File.Path != "a/b/f.txt" {
		t.Fatalf("reorder = %+v, %v", reordered, err)
	}

	other := h.space(owner, "Other")
	target := h.folder(owner, other, "", "t")
	_, err = h.app.MoveItem(h.ctx, owner, f.ID, target.ID, 0)
	itIs(t, "across spaces", err, core.ErrInvalid)

	// Moving needs edit on the old and the new parent.
	guest := h.user("Guest", false)
	h.share(b.ID, guest, core.AccessEdit, true)
	_, err = h.app.MoveItem(h.ctx, guest, f.ID, "", 0)
	itIs(t, "guest move out of its folder", err, core.ErrNotFound)
	sibling := h.folder(owner, sp, b.ID, "c")
	if _, err := h.app.MoveItem(h.ctx, guest, f.ID, sibling.ID, 0); err != nil {
		t.Fatalf("guest move inside its folder: %v", err)
	}
}

func TestCopyItem(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	proj := h.folder(owner, sp, "", "Proj")
	sub := h.folder(owner, sp, proj.ID, "sub")
	note := h.note(owner, sp, proj.ID, "Plan", "see [pic](sub/pic.png)")
	pic := h.upload(owner, sp, sub.ID, "pic.png", "png-1")
	if _, err := h.app.ReplaceFile(h.ctx, owner, pic.ID, core.FileReplace{BaseSHA256: itSHA("png-1")}, strings.NewReader("png-2")); err != nil {
		t.Fatal(err)
	}

	copied, err := h.app.CopyItem(h.ctx, owner, proj.ID, core.CopyInput{})
	if err != nil {
		t.Fatalf("CopyItem folder: %v", err)
	}
	if copied.Folder == nil || copied.Folder.Path != "Proj copy" || copied.Folder.ID == proj.ID {
		t.Fatalf("copy = %+v", copied.Folder)
	}
	cnote, err := h.db.GetNoteByPath(h.ctx, sp.ID, "Proj copy/plan.md")
	if err != nil || cnote.ID == note.ID {
		t.Fatalf("copied note row = %+v, %v", cnote, err)
	}
	if disk, _ := h.files.Read(sp.Slug, "Proj copy/plan.md"); disk.ID != cnote.ID {
		t.Fatalf("frontmatter id %s, row id %s", disk.ID, cnote.ID)
	}
	cpic, err := h.db.GetFileByPath(h.ctx, sp.ID, "Proj copy/sub/pic.png")
	if err != nil || cpic.ID == pic.ID || cpic.Versions != 0 || cpic.SHA256 != itSHA("png-2") {
		t.Fatalf("copied file = %+v, %v", cpic, err)
	}
	if v, _ := h.app.ListFileVersions(h.ctx, owner, cpic.ID); len(v) != 0 {
		t.Fatalf("copied file has %d versions", len(v))
	}
	csub, err := h.db.GetFolderByPath(h.ctx, sp.ID, "Proj copy/sub")
	if err != nil || csub.ID == sub.ID || cpic.ParentID != csub.ID {
		t.Fatalf("copied sub = %+v, %v", csub, err)
	}
	if again, err := h.app.CopyItem(h.ctx, owner, proj.ID, core.CopyInput{}); err != nil || again.Folder.Path != "Proj copy 2" {
		t.Fatalf("second copy = %+v, %v", again, err)
	}
	_, err = h.app.CopyItem(h.ctx, owner, proj.ID, core.CopyInput{ParentID: sub.ID})
	itIs(t, "copy into itself", err, core.ErrInvalid)
	_, err = h.app.CopyItem(h.ctx, owner, pic.ID, core.CopyInput{ParentID: sub.ID, Name: "pic.png"})
	itIs(t, "explicit taken name", err, core.ErrExists)
	if c, err := h.app.CopyItem(h.ctx, owner, pic.ID, core.CopyInput{ParentID: sub.ID}); err != nil || c.File.Name != "pic copy.png" {
		t.Fatalf("file copy name = %+v, %v", c, err)
	}

	noteCopy, err := h.app.CopyItem(h.ctx, owner, note.ID, core.CopyInput{ParentID: proj.ID})
	if err != nil || noteCopy.Note.Title != "Plan copy" || noteCopy.Note.Path != "Proj/plan-copy.md" || noteCopy.Note.ID == note.ID {
		t.Fatalf("note copy = %+v, %v", noteCopy, err)
	}
	if dup, err := h.app.DuplicateNote(h.ctx, owner, note.ID); err != nil || dup.Title != "Plan copy 2" {
		t.Fatalf("DuplicateNote = %+v, %v", dup, err)
	}

	other := h.space(owner, "Other")
	across, err := h.app.CopyItem(h.ctx, owner, proj.ID, core.CopyInput{SpaceID: other.ID})
	if err != nil || across.Folder.SpaceID != other.ID || across.Folder.Path != "Proj" {
		t.Fatalf("copy across spaces = %+v, %v", across, err)
	}
	onote, err := h.db.GetNoteByPath(h.ctx, other.ID, "Proj/plan.md")
	if err != nil || onote.ID == note.ID || onote.SpaceID != other.ID {
		t.Fatalf("note in other space = %+v, %v", onote, err)
	}
	if disk, _ := h.files.Read(other.Slug, "Proj/plan.md"); disk.ID != onote.ID || disk.ID == note.ID {
		t.Fatalf("other space frontmatter id = %s", disk.ID)
	}
	ofile, err := h.db.GetFileByPath(h.ctx, other.ID, "Proj/sub/pic.png")
	if err != nil || ofile.ID == pic.ID || ofile.Versions != 0 {
		t.Fatalf("file in other space = %+v, %v", ofile, err)
	}

	// Copy needs view on the source and edit on the target.
	reader := h.user("Reader", false)
	h.member(sp, reader, core.RoleViewer)
	_, err = h.app.CopyItem(h.ctx, reader, note.ID, core.CopyInput{})
	itIs(t, "viewer copy into the same space", err, core.ErrForbidden)
	mine := h.space(reader, "Mine")
	if _, err := h.app.CopyItem(h.ctx, reader, note.ID, core.CopyInput{SpaceID: mine.ID}); err != nil {
		t.Fatalf("viewer copy into own space: %v", err)
	}
}

func TestReplaceFileHashAndVersions(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	f := h.upload(owner, sp, "", "data.txt", "one")

	_, err := h.app.ReplaceFile(h.ctx, owner, f.ID, core.FileReplace{BaseSHA256: itSHA("stale")}, strings.NewReader("two"))
	itIs(t, "stale hash", err, core.ErrConflict)
	_, err = h.app.ReplaceFile(h.ctx, owner, f.ID, core.FileReplace{}, strings.NewReader("two"))
	itIs(t, "missing hash", err, core.ErrInvalid)
	if got := itRead(t, h, owner, f.ID, ""); got != "one" {
		t.Fatalf("content after refused replace = %q", got)
	}
	got, err := h.app.ReplaceFile(h.ctx, owner, f.ID, core.FileReplace{BaseSHA256: strings.ToUpper(itSHA("one"))}, strings.NewReader("two"))
	if err != nil || got.SHA256 != itSHA("two") || got.Versions != 1 || got.Name != "data.txt" {
		t.Fatalf("replace = %+v, %v", got, err)
	}
	versions, err := h.app.ListFileVersions(h.ctx, owner, f.ID)
	if err != nil || len(versions) != 1 || versions[0].SHA256 != itSHA("one") {
		t.Fatalf("versions = %+v, %v", versions, err)
	}
	if old := itRead(t, h, owner, f.ID, versions[0].ID); old != "one" {
		t.Fatalf("version content = %q", old)
	}
	_, _, _, err = h.app.OpenFile(h.ctx, owner, f.ID, "1")
	if err == nil {
		t.Fatal("unknown version opened")
	}

	csv, err := h.app.ReplaceFile(h.ctx, owner, f.ID, core.FileReplace{Name: "export.CSV", BaseSHA256: itSHA("two")}, strings.NewReader("a,b"))
	if err != nil || csv.Name != "data.CSV" || csv.Path != "data.CSV" || !h.exists(sp, "data.CSV") || h.exists(sp, "data.txt") {
		t.Fatalf("extension change = %+v, %v", csv, err)
	}
	restored, err := h.app.RestoreFileVersion(h.ctx, owner, f.ID, versions[0].ID)
	if err != nil || restored.SHA256 != itSHA("one") || restored.Versions != 3 {
		t.Fatalf("RestoreFileVersion = %+v, %v", restored, err)
	}
	if content := itRead(t, h, owner, f.ID, ""); content != "one" {
		t.Fatalf("restored content = %q", content)
	}
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)
	_, err = h.app.ReplaceFile(h.ctx, viewer, f.ID, core.FileReplace{BaseSHA256: itSHA("one")}, strings.NewReader("x"))
	itIs(t, "viewer replace", err, core.ErrForbidden)
	if v, err := h.app.ListFileVersions(h.ctx, viewer, f.ID); err != nil || len(v) != 3 {
		t.Fatalf("viewer versions = %d, %v", len(v), err)
	}
}

func TestTrashRestoreDeleteFile(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	folder := h.folder(owner, sp, "", "box")
	f := h.upload(owner, sp, folder.ID, "pic.png", itPNG(t, 40, 20))

	if err := h.app.TrashItem(h.ctx, owner, f.ID); err != nil {
		t.Fatal(err)
	}
	if !h.exists(sp, ".trash/"+f.ID+"/pic.png") || h.exists(sp, "box/pic.png") {
		t.Fatal("file not in its trash slot")
	}
	_, _, _, err := h.app.OpenFile(h.ctx, owner, f.ID, "")
	itIs(t, "open trashed", err, core.ErrConflict)
	itIs(t, "trash twice", h.app.TrashItem(h.ctx, owner, f.ID), core.ErrConflict)

	// The parent folder goes away, so the file returns to the root, where the
	// name is taken.
	if err := h.app.DeleteItem(h.ctx, owner, folder.ID); err != nil {
		t.Fatal(err)
	}
	h.upload(owner, sp, "", "pic.png", "other")
	restored, err := h.app.RestoreItem(h.ctx, owner, f.ID)
	if err != nil || restored.File.Path != "pic (2).png" || restored.File.ParentID != "" || restored.File.Deleted {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	_, err = h.app.RestoreItem(h.ctx, owner, f.ID)
	itIs(t, "restore live", err, core.ErrConflict)

	if _, rc, _, err := h.app.OpenThumbnail(h.ctx, owner, f.ID, 128); err != nil {
		t.Fatalf("thumbnail: %v", err)
	} else {
		rc.Close()
	}
	if _, err := h.app.ReplaceFile(h.ctx, owner, f.ID, core.FileReplace{BaseSHA256: restored.File.SHA256}, strings.NewReader(itPNG(t, 30, 30))); err != nil {
		t.Fatal(err)
	}
	thumbs := filepath.Join(h.dir, "internal", "thumbs", f.ID)
	if _, rc, _, err := h.app.OpenThumbnail(h.ctx, owner, f.ID, 128); err != nil {
		t.Fatalf("thumbnail after replace: %v", err)
	} else {
		rc.Close()
	}
	if _, err := os.Stat(thumbs); err != nil {
		t.Fatalf("thumbnail cache missing: %v", err)
	}
	editor := h.user("Editor", false)
	h.member(sp, editor, core.RoleEditor)
	itIs(t, "editor delete other's file", h.app.DeleteItem(h.ctx, editor, f.ID), core.ErrForbidden)
	if err := h.app.DeleteItem(h.ctx, owner, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.GetFile(h.ctx, f.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("row kept: %v", err)
	}
	if v, _ := h.files.ListFileVersions(sp.ID, f.ID); len(v) != 0 {
		t.Fatalf("versions kept: %d", len(v))
	}
	if _, err := os.Stat(thumbs); !os.IsNotExist(err) {
		t.Fatalf("thumbnails kept: %v", err)
	}
	if h.exists(sp, "pic (2).png") {
		t.Fatal("file kept on disk")
	}
}

func TestEmptyTrashRemovesEveryKind(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	folder := h.folder(owner, sp, "", "box")
	inner := h.upload(owner, sp, folder.ID, "in.txt", "in")
	loose := h.note(owner, sp, "", "Loose", "")
	file := h.upload(owner, sp, "", "f.txt", "f")
	for _, id := range []string{folder.ID, loose.ID, file.ID} {
		if err := h.app.TrashItem(h.ctx, owner, id); err != nil {
			t.Fatal(err)
		}
	}
	editor := h.user("Editor", false)
	h.member(sp, editor, core.RoleEditor)
	itIs(t, "editor empties trash", h.app.EmptyTrash(h.ctx, editor, sp.ID), core.ErrForbidden)
	if err := h.app.EmptyTrash(h.ctx, owner, sp.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{folder.ID, inner.ID, loose.ID, file.ID} {
		if _, _, err := h.db.ItemKind(h.ctx, id); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("item %s kept: %v", id, err)
		}
	}
	if h.exists(sp, ".trash") {
		entries, _ := os.ReadDir(h.diskPath(sp, ".trash"))
		if len(entries) != 0 {
			t.Fatalf("trash dir has %d entries", len(entries))
		}
	}
}

func TestOpenThumbnail(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	img := h.upload(owner, sp, "", "wide.png", itPNG(t, 300, 200))
	if img.Width != 300 || img.Height != 200 || img.Thumb == "" {
		t.Fatalf("image upload = %+v", img)
	}
	_, rc, mt, err := h.app.OpenThumbnail(h.ctx, owner, img.ID, 128)
	if err != nil {
		t.Fatalf("OpenThumbnail: %v", err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if len(data) == 0 || !strings.HasPrefix(mt, "image/") {
		t.Fatalf("thumbnail %d bytes, type %q", len(data), mt)
	}
	_, _, _, err = h.app.OpenThumbnail(h.ctx, owner, img.ID, 100)
	itIs(t, "width", err, core.ErrInvalid)
	txt := h.upload(owner, sp, "", "a.txt", "x")
	_, _, _, err = h.app.OpenThumbnail(h.ctx, owner, txt.ID, 128)
	itIs(t, "text thumbnail", err, core.ErrUnsupported)
	svg := h.upload(owner, sp, "", "a.svg", `<svg xmlns="http://www.w3.org/2000/svg"/>`)
	if svg.Thumb != "" {
		t.Fatalf("svg has a thumb url %q", svg.Thumb)
	}
	_, _, _, err = h.app.OpenThumbnail(h.ctx, owner, svg.ID, 128)
	itIs(t, "svg thumbnail", err, core.ErrUnsupported)
	stranger := h.user("Stranger", false)
	_, _, _, err = h.app.OpenThumbnail(h.ctx, stranger, img.ID, 128)
	itIs(t, "stranger thumbnail", err, core.ErrNotFound)
}

func TestFolderNotes(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "a")
	x := h.note(owner, sp, a.ID, "X", "see [[Other]] and [up](../top.md)")
	top := h.note(owner, sp, "", "Top", "")
	h.note(owner, sp, "", "Other", "")
	child := h.note(owner, sp, x.ID, "Child", "")

	row := h.row(x.ID)
	if row.Path != "a/x/index.md" || child.Path != "a/x/child.md" {
		t.Fatalf("converted = %s, child %s", row.Path, child.Path)
	}
	folder, err := h.db.GetFolderByPath(h.ctx, sp.ID, "a/x")
	if err != nil || folder.IndexNoteID != x.ID || folder.ParentID != a.ID || child.ParentID != folder.ID || row.ParentID != folder.ID {
		t.Fatalf("folder = %+v, %v, child parent %s", folder, err, child.ParentID)
	}
	if body := h.body(sp, "a/x/index.md"); body != "see [[Other]] and [up](../../top.md)" {
		t.Fatalf("own relative link not rewritten: %q", body)
	}
	_ = top
	second := h.note(owner, sp, x.ID, "Second", "")
	if second.Path != "a/x/second.md" {
		t.Fatalf("second child of a folder note = %s", second.Path)
	}
	tree, _ := h.app.SpaceTree(h.ctx, owner, sp.ID)
	if got := itShape(tree); got != "a(x*(Child Second)) Top Other" {
		t.Fatalf("tree = %q", got)
	}
	view, _, err := h.app.GetFolder(h.ctx, owner, folder.ID)
	if err != nil || len(view.Children) != 2 {
		t.Fatalf("GetFolder children = %v, %v", view, err)
	}

	// An existing folder without a folder note takes the note.
	h.folder(owner, sp, "", "top")
	h.note(owner, sp, top.ID, "Inside", "")
	if row := h.row(top.ID); row.Path != "top/index.md" {
		t.Fatalf("note into existing folder = %s", row.Path)
	}
	// Trashing the folder note clears the pointer.
	if err := h.app.TrashItem(h.ctx, owner, x.ID); err != nil {
		t.Fatal(err)
	}
	if f, _ := h.db.GetFolder(h.ctx, folder.ID); f.IndexNoteID != "" {
		t.Fatalf("pointer kept after trash: %q", f.IndexNoteID)
	}
	h.app.syncFolderNote(h.ctx, sp.ID, "a/x")
}

func TestCreateFolderNote(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	wiki := h.folder(owner, sp, "", "Wiki")
	h.note(owner, sp, wiki.ID, "Page", "")

	in := core.NoteInput{SpaceID: sp.ID, FolderID: wiki.ID, FolderNote: true, Title: "Wiki index", Content: "# Index\n"}
	idx, err := h.app.CreateNote(h.ctx, owner, in)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Path != "Wiki/index.md" || idx.ParentID != wiki.ID || idx.Title != "Wiki index" {
		t.Fatalf("folder note = %s parent %s title %q", idx.Path, idx.ParentID, idx.Title)
	}
	if body := h.body(sp, "Wiki/index.md"); body != "# Index\n" {
		t.Fatalf("body = %q", body)
	}
	if f, _ := h.db.GetFolder(h.ctx, wiki.ID); f.IndexNoteID != idx.ID {
		t.Fatalf("folder pointer = %q, want %s", f.IndexNoteID, idx.ID)
	}
	tree, _ := h.app.SpaceTree(h.ctx, owner, sp.ID)
	if got := itShape(tree); got != "Wiki*(Page)" {
		t.Fatalf("tree = %q", got)
	}

	_, err = h.app.CreateNote(h.ctx, owner, in)
	itIs(t, "second folder note", err, core.ErrExists)
	same := in
	same.ParentID = wiki.ID
	_, err = h.app.CreateNote(h.ctx, owner, same)
	itIs(t, "second folder note with parentId", err, core.ErrExists)

	// A file named index.md on disk that the index has not seen also blocks it.
	other := h.folder(owner, sp, "", "Other")
	h.writeDisk(sp, "Other/index.md", "---\ntitle: Outside\n---\nhi\n")
	_, err = h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, FolderID: other.ID, FolderNote: true, Title: "X"})
	itIs(t, "index.md on disk", err, core.ErrExists)

	// An ordinary note titled index keeps the index-note name.
	plain := h.note(owner, sp, wiki.ID, "index", "")
	if plain.Path != "Wiki/index-note.md" {
		t.Fatalf("ordinary note titled index = %s", plain.Path)
	}

	// Replaying a create with the same id returns the stored note.
	third := h.folder(owner, sp, "", "Third")
	id := core.NewID()
	first, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{ID: id, SpaceID: sp.ID, FolderID: third.ID, FolderNote: true, Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{ID: id, SpaceID: sp.ID, FolderID: third.ID, FolderNote: true, Title: "T"})
	if err != nil || again.ID != first.ID || again.Path != "Third/index.md" {
		t.Fatalf("replay = %+v, %v", again, err)
	}

	_, err = h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, FolderNote: true, Title: "No folder"})
	itIs(t, "folder note without folderId", err, core.ErrInvalid)
	_, err = h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, FolderID: wiki.ID, Title: "No flag"})
	itIs(t, "folderId without folderNote", err, core.ErrInvalid)
	_, err = h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, FolderID: wiki.ID, ParentID: other.ID, FolderNote: true, Title: "Mixed"})
	itIs(t, "parentId differs from folderId", err, core.ErrInvalid)
	_, err = h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, FolderID: plain.ID, FolderNote: true, Title: "On a note"})
	itIs(t, "folderId names a note", err, core.ErrInvalid)
	_, err = h.app.CreateNote(h.ctx, owner, core.NoteInput{SpaceID: sp.ID, FolderID: core.NewID(), FolderNote: true, Title: "Missing"})
	itIs(t, "unknown folder", err, core.ErrNotFound)
	stranger := h.user("Stranger", false)
	fourth := h.folder(owner, sp, "", "Fourth")
	_, err = h.app.CreateNote(h.ctx, stranger, core.NoteInput{SpaceID: sp.ID, FolderID: fourth.ID, FolderNote: true, Title: "Stranger"})
	itIs(t, "stranger", err, core.ErrNotFound)
}

func TestFileQueues(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	h.writeDisk(sp, "found/words.txt", "harbour survey findings")
	h.writeDisk(sp, "found/big.txt", strings.Repeat("harbour ", 21<<20/8+1))
	h.rescan(sp)
	words, err := h.db.GetFileByPath(h.ctx, sp.ID, "found/words.txt")
	if err != nil || words.SHA256 != "" || words.Position != 0 {
		t.Fatalf("scanned file = %+v, %v", words, err)
	}
	h.app.drainFileQueues(h.ctx)
	words, _ = h.db.GetFileByPath(h.ctx, sp.ID, "found/words.txt")
	if words.SHA256 != itSHA("harbour survey findings") {
		t.Fatalf("hash not filled: %q", words.SHA256)
	}
	hits, err := h.db.SearchFiles(h.ctx, []string{sp.ID}, core.SearchQuery{Text: "findings"})
	if err != nil || len(hits) != 1 || hits[0].File.ID != words.ID {
		t.Fatalf("file search = %v, %v", hits, err)
	}
	if pending, _ := h.db.ListFilesNeedingText(h.ctx, 10); len(pending) != 0 {
		t.Fatalf("%d files still pending", len(pending))
	}
	big, _ := h.db.GetFileByPath(h.ctx, sp.ID, "found/big.txt")
	if hits, _ := h.db.SearchFiles(h.ctx, []string{sp.ID}, core.SearchQuery{Text: "harbour"}); len(hits) != 1 || hits[0].File.ID == big.ID {
		t.Fatalf("big file text indexed: %v", hits)
	}
	all, err := h.app.Search(h.ctx, owner, core.SearchQuery{Text: "findings"})
	if err != nil || len(all) != 1 || all[0].Kind != core.KindFile || all[0].File.URL == "" {
		t.Fatalf("app search = %+v, %v", all, err)
	}
}

func TestSyncPullItems(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	folder := h.folder(owner, sp, "", "shared")
	f := h.upload(owner, sp, folder.ID, "in.txt", "x")
	n := h.note(owner, sp, folder.ID, "Inside", "")
	outside := h.upload(owner, sp, "", "out.txt", "y")

	batch, err := h.app.SyncPull(h.ctx, owner, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(b *core.SyncBatch) []string {
		var out []string
		for _, x := range b.Folders {
			out = append(out, x.ID)
		}
		for _, x := range b.Files {
			out = append(out, x.ID)
		}
		for _, x := range b.Notes {
			out = append(out, x.ID)
		}
		sort.Strings(out)
		return out
	}
	want := []string{folder.ID, f.ID, n.ID, outside.ID}
	sort.Strings(want)
	if got := ids(batch); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pulled %v, want %v", got, want)
	}
	for _, id := range want {
		if !containsString(batch.LiveIDs, id) {
			t.Fatalf("liveIds lacks %s", id)
		}
	}
	if batch.Files[0].URL == "" {
		t.Fatal("pulled file not decorated")
	}

	guest := h.user("Guest", false)
	h.share(folder.ID, guest, core.AccessView, true)
	gb, err := h.app.SyncPull(h.ctx, guest, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{folder.ID, f.ID, n.ID}
	sort.Strings(want)
	if got := ids(gb); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("guest pulled %v, want %v", got, want)
	}
	if containsString(gb.LiveIDs, outside.ID) || !containsString(gb.LiveIDs, f.ID) {
		t.Fatalf("guest liveIds = %v", gb.LiveIDs)
	}
	if gb.AccessKey == "" || gb.AccessKey == batch.AccessKey {
		t.Fatal("access keys do not differ")
	}
	next, err := h.app.SyncPull(h.ctx, owner, batch.Rev, 100)
	if err != nil || len(next.Folders)+len(next.Files)+len(next.Notes) != 0 {
		t.Fatalf("second pull = %+v, %v", next, err)
	}
	if _, err := h.app.RenameFile(h.ctx, owner, f.ID, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	after, _ := h.app.SyncPull(h.ctx, owner, batch.Rev, 100)
	if len(after.Files) != 1 || after.Files[0].Name != "renamed.txt" {
		t.Fatalf("pull after rename = %+v", after.Files)
	}
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestItemEvents(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	ch, stop := h.app.events.add(nil)
	defer stop()
	folder := h.folder(owner, sp, "", "box")
	f := h.upload(owner, sp, "", "a.txt", "a")
	if _, err := h.app.MoveItem(h.ctx, owner, f.ID, folder.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := h.app.TrashItem(h.ctx, owner, folder.ID); err != nil {
		t.Fatal(err)
	}
	seen := map[string]core.Event{}
	for len(ch) > 0 {
		ev := <-ch
		seen[ev.Type+" "+ev.ItemID] = ev
	}
	for _, key := range []string{
		"folder.created " + folder.ID, "file.created " + f.ID, "item.moved " + f.ID,
		"file.updated " + f.ID, "folder.deleted " + folder.ID, "tree.changed " + folder.ID,
	} {
		ev, ok := seen[key]
		if !ok {
			t.Fatalf("missing event %s in %v", key, seen)
		}
		if ev.SpaceID != sp.ID || ev.Kind == "" {
			t.Fatalf("event %s = %+v", key, ev)
		}
	}
	if ev := seen["item.moved "+f.ID]; ev.Path != "box/a.txt" || ev.Kind != core.KindFile || ev.ActorID != owner.UserID() {
		t.Fatalf("move event = %+v", ev)
	}
}

func TestItemLookupsAndResolve(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	folder := h.folder(owner, sp, "", "Projects")
	pic := h.upload(owner, sp, folder.ID, "east wall.jpg", "jpg")
	doc := h.upload(owner, sp, "", "report.pdf", "%PDF-1.4")
	n := h.note(owner, sp, folder.ID, "Survey", "![[east wall.jpg]] [r](../report.pdf)")
	hidden := h.note(owner, sp, "", "Secret", "")

	for rel, id := range map[string]string{"Projects": folder.ID, "Projects/east wall.jpg": pic.ID, "Projects/survey": n.ID, "Projects/survey.md": n.ID} {
		it, err := h.app.GetItemByPath(h.ctx, owner, sp.Slug, rel)
		if err != nil || it.ID() != id {
			t.Fatalf("GetItemByPath %s = %+v, %v", rel, it, err)
		}
	}
	_, err := h.app.GetItemByPath(h.ctx, owner, sp.ID, "nope")
	itIs(t, "missing path", err, core.ErrNotFound)

	images, err := h.app.ListFiles(h.ctx, owner, sp.ID, core.FileQuery{Type: "image"})
	if err != nil || len(images) != 1 || images[0].ID != pic.ID {
		t.Fatalf("ListFiles image = %v, %v", images, err)
	}
	_, err = h.app.ListFiles(h.ctx, owner, sp.ID, core.FileQuery{Sort: "weird"})
	itIs(t, "bad sort", err, core.ErrInvalid)
	back, err := h.app.ItemBacklinks(h.ctx, owner, doc.ID)
	if err != nil || len(back) != 1 || back[0].FromNote.ID != n.ID {
		t.Fatalf("backlinks of the pdf = %v, %v", back, err)
	}

	guest := h.user("Guest", false)
	h.share(folder.ID, guest, core.AccessView, true)
	res, err := h.app.ResolveLinks(h.ctx, guest, core.ResolveRequest{
		SpaceID: sp.ID, FromNoteID: n.ID,
		Targets: []string{"east wall.jpg", "[r](../report.pdf)", "Secret", "Survey#Heading|label", "missing"},
	})
	if err != nil || len(res) != 5 {
		t.Fatalf("ResolveLinks = %v, %v", res, err)
	}
	if res[0].ID != pic.ID || res[0].Kind != core.KindFile || res[0].URL != "/s/"+sp.Slug+"/f/Projects/east%20wall.jpg" {
		t.Fatalf("resolve image = %+v", res[0])
	}
	if res[1].ID != "" || res[2].ID != "" || res[4].ID != "" {
		t.Fatalf("hidden targets resolved for the guest: %+v %+v %+v", res[1], res[2], res[4])
	}
	if res[3].ID != n.ID {
		t.Fatalf("resolve heading target = %+v", res[3])
	}
	_ = hidden
	all, _ := h.app.ResolveLinks(h.ctx, owner, core.ResolveRequest{SpaceID: sp.ID, FromNoteID: n.ID, Targets: []string{"[r](../report.pdf)"}})
	if all[0].ID != doc.ID {
		t.Fatalf("owner resolve = %+v", all[0])
	}

	// A folder shared without children is listed with its children, which
	// its holder cannot open.
	lister := h.user("Lister", false)
	h.share(folder.ID, lister, core.AccessView, false)
	view, _, err := h.app.GetFolder(h.ctx, lister, folder.ID)
	if err != nil || len(view.Children) != 2 {
		t.Fatalf("lister GetFolder = %v, %v", view, err)
	}
	_, _, err = h.app.GetFile(h.ctx, lister, pic.ID)
	itIs(t, "lister opens a child", err, core.ErrNotFound)
	if _, err := h.db.ItemShareGrantsForUser(h.ctx, lister.UserID()); err != nil {
		t.Fatal(err)
	}
	_ = ports.TextDone
}
