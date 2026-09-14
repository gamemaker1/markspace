package app

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/markdown"
	"github.com/gamemaker1/markspace/internal/ports"
)

func TestReconcileMatchesMovedFileByHash(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	a := h.folder(owner, sp, "", "a")
	wall := h.upload(owner, sp, a.ID, "wall.jpg", "wall bytes")
	linker := h.note(owner, sp, "", "Linker", "[w](a/wall.jpg) and ![[wall.jpg]]")

	h.renameDisk(sp, "a/wall.jpg", "b/wall.jpg")
	h.rescan(sp)
	got, err := h.db.GetFile(h.ctx, wall.ID)
	if err != nil || got.Path != "b/wall.jpg" || got.Deleted {
		t.Fatalf("moved file row = %+v, %v", got, err)
	}
	b, err := h.db.GetFolderByPath(h.ctx, sp.ID, "b")
	if err != nil || got.ParentID != b.ID || b.Position != 0 {
		t.Fatalf("new folder = %+v, %v", b, err)
	}
	itWant(t, "link after external move", itBody(t, h, sp, linker.ID), "[w](b/wall.jpg) and ![[wall.jpg]]")
	if _, err := h.db.GetFolder(h.ctx, a.ID); err != nil {
		t.Fatalf("empty folder a still on disk lost its row: %v", err)
	}
}

func TestReconcileFileTieBreaks(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	x := h.folder(owner, sp, "", "x")
	y := h.folder(owner, sp, "", "y")
	one := h.upload(owner, sp, x.ID, "one.txt", "same")
	two := h.upload(owner, sp, y.ID, "two.txt", "same")
	h.removeDisk(sp, "x/one.txt")
	h.renameDisk(sp, "y/two.txt", "x/two.txt")
	h.rescan(sp)
	if got, err := h.db.GetFile(h.ctx, two.ID); err != nil || got.Path != "x/two.txt" {
		t.Fatalf("same base name should win: %+v, %v", got, err)
	}
	if _, err := h.db.GetFile(h.ctx, one.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unmatched vanished file kept: %v", err)
	}

	p := h.folder(owner, sp, "", "p")
	q := h.folder(owner, sp, p.ID, "q")
	r := h.folder(owner, sp, "", "r")
	near := h.upload(owner, sp, q.ID, "same.txt", "twin")
	far := h.upload(owner, sp, r.ID, "same.txt", "twin")
	h.renameDisk(sp, "p/q/same.txt", "p/same.txt")
	h.removeDisk(sp, "r/same.txt")
	h.rescan(sp)
	if got, err := h.db.GetFile(h.ctx, near.ID); err != nil || got.Path != "p/same.txt" {
		t.Fatalf("nearest folder should win: %+v, %v", got, err)
	}
	if _, err := h.db.GetFile(h.ctx, far.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("far twin kept: %v", err)
	}
}

func TestReconcileMatchesMovedFolderByMajority(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	proj := h.folder(owner, sp, "", "proj")
	empty := h.folder(owner, sp, proj.ID, "empty")
	n := h.note(owner, sp, proj.ID, "Plan", "")
	f := h.upload(owner, sp, proj.ID, "data.csv", "a,b")
	root := h.note(owner, sp, "", "Root", "[[proj/plan]] and [d](proj/data.csv)")

	h.renameDisk(sp, "proj", "work")
	h.rescan(sp)
	for id, want := range map[string]string{proj.ID: "work", empty.ID: "work/empty", n.ID: "work/plan.md", f.ID: "work/data.csv"} {
		if got := h.item(id).Path(); got != want {
			t.Fatalf("item %s path = %s, want %s", id, got, want)
		}
	}
	itWant(t, "links after folder move", itBody(t, h, sp, root.ID), "[[work/plan]] and [d](work/data.csv)")

	old := h.folder(owner, sp, "", "old")
	var ids []string
	for _, name := range []string{"1.txt", "2.txt", "3.txt"} {
		ids = append(ids, h.upload(owner, sp, old.ID, name, "content "+name).ID)
	}
	h.renameDisk(sp, "old", "new")
	h.removeDisk(sp, "new/2.txt")
	h.removeDisk(sp, "new/3.txt")
	h.rescan(sp)
	if _, err := h.db.GetFolder(h.ctx, old.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("minority match kept the old folder: %v", err)
	}
	nf, err := h.db.GetFolderByPath(h.ctx, sp.ID, "new")
	if err != nil || nf.ID == old.ID {
		t.Fatalf("new folder = %+v, %v", nf, err)
	}
	if got, err := h.db.GetFile(h.ctx, ids[0]); err != nil || got.Path != "new/1.txt" || got.ParentID != nf.ID {
		t.Fatalf("surviving file = %+v, %v", got, err)
	}
}

func TestReconcileCaseDuplicatesAndRemovals(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	docs := h.folder(owner, sp, "", "Docs")
	n := h.note(owner, sp, "", "Plan", "body")
	gone := h.upload(owner, sp, "", "gone.txt", "x")
	goneNote := h.note(owner, sp, "", "Gone", "")
	goneFolder := h.folder(owner, sp, "", "gone-folder")

	h.renameDisk(sp, "Docs", "docs")
	data, err := os.ReadFile(h.diskPath(sp, "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	h.writeDisk(sp, "copy.md", string(data))
	h.removeDisk(sp, "gone.txt")
	h.removeDisk(sp, "gone.md")
	h.removeDisk(sp, "gone-folder")
	h.rescan(sp)

	if got := h.item(docs.ID).Path(); got != "docs" {
		t.Fatalf("case-only rename path = %s", got)
	}
	if got := h.row(n.ID); got.Path != "plan.md" {
		t.Fatalf("original note moved to %s", got.Path)
	}
	copyRow, err := h.db.GetNoteByPath(h.ctx, sp.ID, "copy.md")
	if err != nil || copyRow.ID == n.ID {
		t.Fatalf("copy row = %+v, %v", copyRow, err)
	}
	if disk, _ := h.files.Read(sp.Slug, "copy.md"); disk.ID != copyRow.ID {
		t.Fatalf("copy frontmatter id = %s, row %s", disk.ID, copyRow.ID)
	}
	for _, id := range []string{gone.ID, goneNote.ID, goneFolder.ID} {
		if _, _, err := h.db.ItemKind(h.ctx, id); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("vanished item %s kept: %v", id, err)
		}
	}
}

func TestWatcherRenameWindowAndSelfWrites(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	f := h.upload(owner, sp, "", "a.txt", "keep me")

	// The app's own write is ignored.
	h.app.handleFileEvent(h.ctx, ports.FileEvent{SpaceSlug: sp.Slug, RelPath: "a.txt", Op: "write", Kind: core.KindFile})
	if h.app.scanPending(sp.Slug) {
		t.Fatal("self write scheduled a scan")
	}
	h.clock.Advance(3 * time.Second)

	outside := filepath.Join(t.TempDir(), "a.txt")
	if err := os.Rename(h.diskPath(sp, "a.txt"), outside); err != nil {
		t.Fatal(err)
	}
	h.app.handleFileEvent(h.ctx, ports.FileEvent{SpaceSlug: sp.Slug, RelPath: "a.txt", Op: "remove", Kind: core.KindFile})
	if !h.app.scanPending(sp.Slug) {
		t.Fatal("remove did not schedule a scan")
	}
	h.app.flushScans(h.ctx)
	if got, err := h.db.GetFile(h.ctx, f.ID); err != nil || got.Path != "a.txt" {
		t.Fatalf("vanished file dropped inside the window: %+v, %v", got, err)
	}

	h.clock.Advance(5 * time.Second)
	if err := os.Rename(outside, h.diskPath(sp, "b.txt")); err != nil {
		t.Fatal(err)
	}
	h.app.handleFileEvent(h.ctx, ports.FileEvent{SpaceSlug: sp.Slug, RelPath: "b.txt", Op: "write", Kind: core.KindFile})
	h.app.flushScans(h.ctx)
	if got, err := h.db.GetFile(h.ctx, f.ID); err != nil || got.Path != "b.txt" {
		t.Fatalf("file renamed within the window = %+v, %v", got, err)
	}

	h.removeDisk(sp, "b.txt")
	h.app.handleFileEvent(h.ctx, ports.FileEvent{SpaceSlug: sp.Slug, RelPath: "b.txt", Op: "remove", Kind: core.KindFile})
	h.app.flushScans(h.ctx)
	if _, err := h.db.GetFile(h.ctx, f.ID); err != nil {
		t.Fatalf("dropped before the window ended: %v", err)
	}
	h.clock.Advance(11 * time.Second)
	h.app.flushScans(h.ctx)
	if _, err := h.db.GetFile(h.ctx, f.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("file kept after the window: %v", err)
	}

	// A note written outside is indexed at once, and a move keeps its id and
	// rewrites the links to it.
	docs := h.folder(owner, sp, "", "docs")
	linker := h.note(owner, sp, "", "Linker", "[e](ext.md)")
	h.clock.Advance(3 * time.Second)
	id := core.NewID()
	h.writeDisk(sp, "ext.md", "---\nid: "+id+"\ntitle: External\n---\n[l](linker.md)")
	h.app.handleFileEvent(h.ctx, ports.FileEvent{SpaceSlug: sp.Slug, RelPath: "ext.md", Op: "write", Kind: core.KindNote})
	if row, err := h.db.GetNote(h.ctx, id); err != nil || row.Path != "ext.md" || h.app.scanPending(sp.Slug) {
		t.Fatalf("external note = %+v, %v", row, err)
	}
	h.clock.Advance(3 * time.Second)
	h.renameDisk(sp, "ext.md", "docs/ext.md")
	h.app.handleFileEvent(h.ctx, ports.FileEvent{SpaceSlug: sp.Slug, RelPath: "docs/ext.md", Op: "write", Kind: core.KindNote})
	row, err := h.db.GetNote(h.ctx, id)
	if err != nil || row.Path != "docs/ext.md" || row.ParentID != docs.ID {
		t.Fatalf("moved external note = %+v, %v", row, err)
	}
	itWant(t, "link to the moved note", itBody(t, h, sp, linker.ID), "[e](docs/ext.md)")
	itWant(t, "moved note's own link", h.body(sp, "docs/ext.md"), "[l](../linker.md)")
}

func TestReconcileRebuildsTrashOnFreshIndex(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	folder := h.folder(owner, sp, "", "box")
	h.upload(owner, sp, folder.ID, "in.txt", "in")
	n := h.note(owner, sp, "", "Plan", "trashed body")
	f := h.upload(owner, sp, "", "f.txt", "f")
	live := h.note(owner, sp, "", "Live", "")
	for _, id := range []string{folder.ID, n.ID, f.ID} {
		if err := h.app.TrashItem(h.ctx, owner, id); err != nil {
			t.Fatal(err)
		}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := index.Open(filepath.Join(t.TempDir(), "fresh.db"), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fresh, err := New(Deps{Files: h.files, DB: db, Renderer: markdown.New(logger), Hasher: fakeHasher{}, Config: h.cfg, Logger: logger, Now: h.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err := fresh.reconcile(h.ctx); err != nil {
		t.Fatal(err)
	}
	for id, kind := range map[string]core.ItemKind{folder.ID: core.KindFolder, n.ID: core.KindNote, f.ID: core.KindFile} {
		it, err := fresh.loadItem(h.ctx, id)
		if err != nil || it.Kind != kind || !itemDeleted(it) {
			t.Fatalf("rebuilt %s = %+v, %v", kind, it, err)
		}
	}
	if row, err := db.GetNote(h.ctx, n.ID); err != nil || row.Path != "plan.md" {
		t.Fatalf("rebuilt note path = %+v, %v", row, err)
	}
	if row, err := db.GetNote(h.ctx, live.ID); err != nil || row.Deleted {
		t.Fatalf("live note = %+v, %v", row, err)
	}
	space, err := db.GetSpaceBySlug(h.ctx, sp.Slug)
	if err != nil {
		t.Fatal(err)
	}
	fr := &core.Space{ID: space.ID, Slug: space.Slug}
	if body, ok := fresh.trashedNoteBody(fr, &core.Note{ID: n.ID}); !ok || body != "trashed body" {
		t.Fatalf("trashed body = %q, %v", body, ok)
	}
}
