package app

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

func TestReconcile(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", true)
	sp := h.space(owner, "Docs")
	one := h.note(owner, sp, "", "One", "original")
	two := h.note(owner, sp, "", "Two", "doomed")

	if n, err := h.app.reconcile(h.ctx); err != nil || n != 0 {
		t.Fatalf("clean reconcile = %d, %v", n, err)
	}

	threeID := core.NewID()
	h.writeDisk(sp, "three.md", "---\nid: "+threeID+"\ntitle: Three\n---\nnew file\n")
	edited, err := h.files.Read(sp.Slug, one.Path)
	if err != nil {
		t.Fatal(err)
	}
	edited.Content = "edited outside"
	if err := h.files.Write(sp.Slug, edited); err != nil {
		t.Fatal(err)
	}
	h.removeDisk(sp, two.Path)
	kidID := core.NewID()
	h.writeDisk(sp, "one/kid.md", "---\nid: "+kidID+"\ntitle: Kid\nparent: stale-parent\n---\nkid body\n")

	n, err := h.app.reconcile(h.ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// three.md, the edited one.md, the folder one and one/kid.md.
	if n != 4 {
		t.Fatalf("indexed = %d, want 4", n)
	}
	if row := h.row(threeID); row.SpaceID != sp.ID || row.Path != "three.md" {
		t.Fatalf("new file row = %+v", row)
	}
	if row := h.row(one.ID); row.Excerpt != "edited outside" {
		t.Fatalf("edited row excerpt = %q", row.Excerpt)
	}
	if _, err := h.db.GetNote(h.ctx, two.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("deleted file row kept: %v", err)
	}
	folder, err := h.db.GetFolderByPath(h.ctx, sp.ID, "one")
	if err != nil || folder == nil {
		t.Fatalf("folder row = %v, %v", folder, err)
	}
	if row := h.row(kidID); row.ParentID != folder.ID {
		t.Fatalf("kid parent = %q, want the folder %q", row.ParentID, folder.ID)
	}
	// A parent key in frontmatter is ignored, and a rewrite drops it.
	kid, err := h.files.Read(sp.Slug, "one/kid.md")
	if err != nil {
		t.Fatal(err)
	}
	if kid.ParentID != "" || kid.Extra["parent"] != nil {
		t.Fatalf("parent key read into the note: %+v", kid)
	}
	if _, err := h.app.UpdateNote(h.ctx, owner, kidID, core.NoteUpdate{Content: ptrTo("kid body 2")}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(h.diskPath(sp, "one/kid.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "parent:") {
		t.Fatalf("parent key written back to disk:\n%s", raw)
	}

	if n, err := h.app.reconcile(h.ctx); err != nil || n != 0 {
		t.Fatalf("second reconcile = %d, %v", n, err)
	}
}

func TestReconcileAdoptsDirectoryAndRebuildsTrash(t *testing.T) {
	h := newRealHarness(t)
	admin := h.user("Admin", true)
	if err := h.files.EnsureSpace("imported"); err != nil {
		t.Fatal(err)
	}
	impID := core.NewID()
	if err := h.files.Write("imported", &core.Note{ID: impID, Title: "Imported", Path: "imported.md", Content: "hi"}); err != nil {
		t.Fatal(err)
	}

	sp := h.space(admin, "Docs")
	gone := h.note(admin, sp, "", "Gone", "bye")
	if err := h.app.TrashNote(h.ctx, admin, gone.ID); err != nil {
		t.Fatal(err)
	}
	// Simulate a lost index row for the trashed note.
	if err := h.db.DeleteNoteRow(h.ctx, gone.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := h.app.reconcile(h.ctx); err != nil {
		t.Fatal(err)
	}
	adopted, err := h.db.GetSpaceBySlug(h.ctx, "imported")
	if err != nil || adopted.CreatedBy != admin.UserID() {
		t.Fatalf("adopted space = %+v, %v", adopted, err)
	}
	if row := h.row(impID); row.SpaceID != adopted.ID {
		t.Fatalf("adopted note row = %+v", row)
	}
	row := h.row(gone.ID)
	if !row.Deleted || row.Path != "gone.md" {
		t.Fatalf("rebuilt trash row = %+v", row)
	}
	if err := h.app.RestoreNote(h.ctx, admin, gone.ID); err != nil {
		t.Fatalf("restore rebuilt row: %v", err)
	}
	if !h.exists(sp, "gone.md") {
		t.Fatal("restore did not bring the file back")
	}
	if row := h.row(gone.ID); row.Deleted || row.Path != "gone.md" {
		t.Fatalf("restored row = %+v", row)
	}
}

// mxWaitEvent reads events until one of the type for the id arrives.
func mxWaitEvent(t *testing.T, events <-chan core.Event, typ, id string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Type == typ && (ev.ItemID == id || ev.NoteID == id) {
				return
			}
		case <-deadline:
			t.Fatalf("no %s event for %s", typ, id)
		}
	}
}

func TestWatcherEvents(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "v1")
	events, cancel, err := h.app.Subscribe(h.ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	// The application's own write is ignored.
	before := h.row(n.ID).Rev
	h.app.handleFileEvent(h.ctx, ports.FileEvent{SpaceSlug: sp.Slug, RelPath: n.Path, Op: "write", Kind: core.KindNote})
	if h.row(n.ID).Rev != before || h.app.scanPending(sp.Slug) {
		t.Fatal("own write was acted on")
	}

	// An outside edit is indexed at once.
	h.clock.Advance(3 * time.Second)
	edited, err := h.files.Read(sp.Slug, n.Path)
	if err != nil {
		t.Fatal(err)
	}
	edited.Content = "v2 from editor"
	if err := h.files.Write(sp.Slug, edited); err != nil {
		t.Fatal(err)
	}
	h.app.handleFileEvent(h.ctx, ports.FileEvent{SpaceSlug: sp.Slug, RelPath: n.Path, Op: "write", Kind: core.KindNote})
	if row := h.row(n.ID); row.Excerpt != "v2 from editor" {
		t.Fatalf("external edit not indexed: %q", row.Excerpt)
	}
	mxWaitEvent(t, events, "note.updated", n.ID)

	// A file renamed outside keeps its id, and links follow it.
	chart := h.upload(owner, sp, "", "chart.png", "not really a png")
	linker := h.note(owner, sp, "", "Linker", "![](chart.png)\n")
	h.clock.Advance(3 * time.Second)
	h.renameDisk(sp, "chart.png", "graph.png")
	h.app.handleFileEvent(h.ctx, ports.FileEvent{SpaceSlug: sp.Slug, RelPath: "chart.png", Op: "remove", Kind: core.KindFile})
	h.app.handleFileEvent(h.ctx, ports.FileEvent{SpaceSlug: sp.Slug, RelPath: "graph.png", Op: "write", Kind: core.KindFile})
	if !h.app.scanPending(sp.Slug) {
		t.Fatal("file events scheduled no rescan")
	}
	h.app.flushScans(h.ctx)
	if got := h.item(chart.ID).File; got.Path != "graph.png" || got.Deleted {
		t.Fatalf("renamed file row = %+v", got)
	}
	if body := h.body(sp, linker.Path); !strings.Contains(body, "graph.png") {
		t.Fatalf("link not rewritten: %q", body)
	}

	// A removed note keeps its row for the rename window, then goes.
	h.removeDisk(sp, n.Path)
	h.app.handleFileEvent(h.ctx, ports.FileEvent{SpaceSlug: sp.Slug, RelPath: n.Path, Op: "remove", Kind: core.KindNote})
	h.app.flushScans(h.ctx)
	if _, err := h.db.GetNote(h.ctx, n.ID); err != nil {
		t.Fatalf("row dropped inside the rename window: %v", err)
	}
	h.clock.Advance(renameWindow + time.Second)
	h.app.flushScans(h.ctx)
	if _, err := h.db.GetNote(h.ctx, n.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("removed file row kept after the window: %v", err)
	}
}

func TestStartRunsWatcher(t *testing.T) {
	h := newRealHarness(t)
	h.cfg.WatchFiles = true
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "v1")
	if err := h.app.Start(h.ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := h.app.Start(h.ctx); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("second Start err = %v", err)
	}
	h.clock.Advance(3 * time.Second)
	edited, err := h.files.Read(sp.Slug, n.Path)
	if err != nil {
		t.Fatal(err)
	}
	edited.Content = "watched"
	if err := h.files.Write(sp.Slug, edited); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if row, err := h.db.GetNote(h.ctx, n.ID); err == nil && row.Excerpt == "watched" {
			if err := h.app.Close(); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watcher did not reindex")
}

// mxHas reports whether list holds id.
func mxHas(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// TestSyncFeedItemsAndAccessKey checks folders and files in the feed and that
// a share on a folder or a file changes the access key.
func TestSyncFeedItemsAndAccessKey(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	guest := h.user("Guest", false)
	sp := h.space(owner, "Docs")
	shared := h.folder(owner, sp, "", "Shared")
	inside := h.upload(owner, sp, shared.ID, "a.txt", "a")
	lone := h.upload(owner, sp, "", "lone.txt", "b")
	hidden := h.note(owner, sp, "", "Hidden", "h")

	first, err := h.app.SyncPull(h.ctx, guest, 0, 0)
	if err != nil {
		t.Fatalf("SyncPull: %v", err)
	}
	if len(first.Folders)+len(first.Files)+len(first.Notes) != 0 || len(first.LiveIDs) != 0 {
		t.Fatalf("guest saw items before any share: %+v", first)
	}

	h.share(shared.ID, guest, core.AccessView, true)
	second, err := h.app.SyncPull(h.ctx, guest, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if second.AccessKey == first.AccessKey {
		t.Fatal("a folder share did not change the access key")
	}
	if len(second.Folders) != 1 || second.Folders[0].ID != shared.ID {
		t.Fatalf("folders = %+v", second.Folders)
	}
	if len(second.Files) != 1 || second.Files[0].ID != inside.ID || second.Files[0].URL == "" {
		t.Fatalf("files = %+v", second.Files)
	}
	if len(second.Notes) != 0 {
		t.Fatalf("notes leaked: %+v", second.Notes)
	}
	if !mxHas(second.LiveIDs, shared.ID) || !mxHas(second.LiveIDs, inside.ID) || mxHas(second.LiveIDs, lone.ID) || mxHas(second.LiveIDs, hidden.ID) {
		t.Fatalf("live ids = %v", second.LiveIDs)
	}

	h.share(lone.ID, guest, core.AccessView, false)
	third, err := h.app.SyncPull(h.ctx, guest, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if third.AccessKey == second.AccessKey {
		t.Fatal("a file share did not change the access key")
	}
	if len(third.Files) != 2 || !mxHas(third.LiveIDs, lone.ID) {
		t.Fatalf("files after the file share = %+v, live %v", third.Files, third.LiveIDs)
	}

	all, err := h.app.SyncPull(h.ctx, owner, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Folders) != 1 || len(all.Files) != 2 || len(all.Notes) != 1 {
		t.Fatalf("owner feed = %d folders, %d files, %d notes", len(all.Folders), len(all.Files), len(all.Notes))
	}
	for _, id := range []string{shared.ID, inside.ID, lone.ID, hidden.ID} {
		if !mxHas(all.LiveIDs, id) {
			t.Fatalf("owner live ids lack %s: %v", id, all.LiveIDs)
		}
	}

	// A cursor pull returns only what changed after it.
	cursor := all.Rev
	h.upload(owner, sp, shared.ID, "b.txt", "c")
	next, err := h.app.SyncPull(h.ctx, owner, cursor, 0)
	if err != nil || len(next.Files) != 1 || len(next.Folders) != 0 || next.Files[0].Name != "b.txt" {
		t.Fatalf("incremental pull = %+v, %v", next, err)
	}
}
