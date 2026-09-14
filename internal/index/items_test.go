package index

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// mustFolder creates a live folder.
func mustFolder(t *testing.T, db *DB, spaceID, parentID, relPath string) *core.Folder {
	t.Helper()
	f := &core.Folder{SpaceID: spaceID, ParentID: parentID, Path: relPath}
	if err := db.CreateFolder(context.Background(), f); err != nil {
		t.Fatalf("CreateFolder(%q): %v", relPath, err)
	}
	return f
}

// mustFile creates a live file.
func mustFile(t *testing.T, db *DB, spaceID, parentID, relPath, mime string, size int64) *core.File {
	t.Helper()
	f := &core.File{SpaceID: spaceID, ParentID: parentID, Path: relPath, MIME: mime, Size: size}
	if err := db.CreateFile(context.Background(), f); err != nil {
		t.Fatalf("CreateFile(%q): %v", relPath, err)
	}
	return f
}

// mustNoteAt upserts a live note at a given path.
func mustNoteAt(t *testing.T, db *DB, spaceID, parentID, relPath, title string, links ...ports.NoteLink) *core.Note {
	t.Helper()
	now := time.Now().UTC()
	n := &core.Note{
		ID: core.NewID(), SpaceID: spaceID, ParentID: parentID, Slug: pathWithoutExt(baseOf(relPath)),
		Path: relPath, Title: title, CreatedAt: now, UpdatedAt: now,
	}
	row := ports.NoteRow{Note: n, Plain: title, LinkRefs: links}
	if err := db.UpsertNote(context.Background(), row); err != nil {
		t.Fatalf("UpsertNote(%q): %v", relPath, err)
	}
	return n
}

// itemState is the stored placement of one item.
type itemState struct {
	kind      core.ItemKind
	parent    string
	path      string
	deleted   bool
	deletedAt string
	rev       int64
}

// stateOf reads an item's stored placement from any table.
func stateOf(t *testing.T, db *DB, id string) (itemState, bool) {
	t.Helper()
	var (
		s       itemState
		kind    string
		deleted int
	)
	err := db.SQL().QueryRow(`SELECT 'folder', parent_id, path, deleted, deleted_at, rev FROM folders WHERE id = ?1
		UNION ALL SELECT 'note', parent_id, path, deleted, deleted_at, rev FROM notes WHERE id = ?1
		UNION ALL SELECT 'file', parent_id, path, deleted, deleted_at, rev FROM files WHERE id = ?1`, id).
		Scan(&kind, &s.parent, &s.path, &deleted, &s.deletedAt, &s.rev)
	if err != nil {
		return s, false
	}
	s.kind = core.ItemKind(kind)
	s.deleted = deleted != 0
	return s, true
}

// maxRev reads the revision counter.
func maxRev(t *testing.T, db *DB) int64 {
	t.Helper()
	v, err := db.MaxNoteRev(context.Background())
	if err != nil {
		t.Fatalf("MaxNoteRev: %v", err)
	}
	return v
}

func TestFolderPathsAndUniqueness(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	sp := mustSpace(t, db, "docs")
	proj := mustFolder(t, db, sp.ID, "", "Projects")
	mustFolder(t, db, sp.ID, "", "Été")
	mustNoteAt(t, db, sp.ID, "", "readme.md", "Readme")

	got, err := db.GetFolderByPath(ctx, sp.ID, "projects")
	if err != nil || got.ID != proj.ID || got.Name != "Projects" || got.Rev == 0 {
		t.Fatalf("GetFolderByPath = %+v, %v", got, err)
	}

	tests := []struct {
		name string
		run  func() error
		want error
	}{
		{"folder with another case", func() error {
			return db.CreateFolder(ctx, &core.Folder{SpaceID: sp.ID, Path: "PROJECTS"})
		}, core.ErrExists},
		{"folder with Unicode case", func() error {
			return db.CreateFolder(ctx, &core.Folder{SpaceID: sp.ID, Path: "été"})
		}, core.ErrExists},
		{"file on a folder name", func() error {
			return db.CreateFile(ctx, &core.File{SpaceID: sp.ID, Path: "projects"})
		}, core.ErrExists},
		{"folder on a note file name", func() error {
			return db.CreateFolder(ctx, &core.Folder{SpaceID: sp.ID, Path: "readme.md"})
		}, core.ErrExists},
		{"note on a folder name", func() error {
			n := &core.Note{ID: core.NewID(), SpaceID: sp.ID, Path: "projects", Slug: "projects"}
			return db.UpsertNote(ctx, ports.NoteRow{Note: n})
		}, core.ErrExists},
		{"missing parent folder", func() error {
			return db.CreateFolder(ctx, &core.Folder{SpaceID: sp.ID, Path: "Nowhere/x"})
		}, core.ErrInvalid},
		{"wrong parent id", func() error {
			return db.CreateFolder(ctx, &core.Folder{SpaceID: sp.ID, ParentID: "other", Path: "Projects/x"})
		}, core.ErrInvalid},
		{"parent path in another case", func() error {
			return db.CreateFolder(ctx, &core.Folder{SpaceID: sp.ID, ParentID: proj.ID, Path: "projects/x"})
		}, core.ErrInvalid},
		{"root item with a parent", func() error {
			return db.CreateFolder(ctx, &core.Folder{SpaceID: sp.ID, ParentID: proj.ID, Path: "x"})
		}, core.ErrInvalid},
		{"name differs from path", func() error {
			return db.CreateFolder(ctx, &core.Folder{SpaceID: sp.ID, ParentID: proj.ID, Name: "y", Path: "Projects/x"})
		}, core.ErrInvalid},
		{"empty segment", func() error {
			return db.CreateFolder(ctx, &core.Folder{SpaceID: sp.ID, Path: "Projects//x"})
		}, core.ErrInvalid},
		{"dot segment", func() error {
			return db.CreateFile(ctx, &core.File{SpaceID: sp.ID, ParentID: proj.ID, Path: "Projects/.."})
		}, core.ErrInvalid},
		{"id held by a folder", func() error {
			return db.CreateFile(ctx, &core.File{ID: proj.ID, SpaceID: sp.ID, Path: "other.pdf"})
		}, core.ErrExists},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := maxRev(t, db)
			if err := tc.run(); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if after := maxRev(t, db); after != before {
				t.Fatalf("a refused write moved the counter from %d to %d", before, after)
			}
		})
	}

	// A trashed folder does not block a new live folder with the same name.
	if err := db.TrashFolder(ctx, proj.ID, time.Now(), "u"); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	fresh := mustFolder(t, db, sp.ID, "", "projects")
	if got, err := db.GetFolderByPath(ctx, sp.ID, "PROJECTS"); err != nil || got.ID != fresh.ID {
		t.Fatalf("GetFolderByPath after trash = %+v, %v", got, err)
	}
	live, _ := db.ListFolders(ctx, sp.ID, false)
	all, _ := db.ListFolders(ctx, sp.ID, true)
	if len(live) != 2 || len(all) != 3 {
		t.Fatalf("ListFolders live %d all %d, want 2 and 3", len(live), len(all))
	}

	siblings := []struct {
		parent, name, except string
		want                 bool
	}{
		{"", "PROJECTS", "", true},
		{"", "projects", fresh.ID, false},
		{"", "ÉTÉ", "", true},
		{"/", "README.MD", "", true},
		{"", "readme", "", false},
		{"Projects", "anything", "", false},
	}
	for _, s := range siblings {
		got, err := db.SiblingNameTaken(ctx, sp.ID, s.parent, s.name, s.except)
		if err != nil || got != s.want {
			t.Errorf("SiblingNameTaken(%q, %q, %q) = %v, %v, want %v", s.parent, s.name, s.except, got, err, s.want)
		}
	}
}

func TestFileRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	sp := mustSpace(t, db, "docs")
	media := mustFolder(t, db, sp.ID, "", "Media")
	jpg := mustFile(t, db, sp.ID, media.ID, "Media/east wall.jpg", "image/jpeg", 100)
	pdf := mustFile(t, db, sp.ID, media.ID, "Media/report.pdf", "application/pdf", 300)
	mustFile(t, db, sp.ID, "", "notes.txt", "text/plain", 50)

	textState := func(id string) string {
		var s string
		if err := db.SQL().QueryRow(`SELECT text_state FROM files WHERE id = ?`, id).Scan(&s); err != nil {
			t.Fatalf("text state: %v", err)
		}
		return s
	}
	if got, err := db.GetFileByPath(ctx, sp.ID, "media/EAST WALL.JPG"); err != nil || got.ID != jpg.ID || got.MIME != "image/jpeg" {
		t.Fatalf("GetFileByPath = %+v, %v", got, err)
	}
	if s := textState(jpg.ID); s != "pending" {
		t.Fatalf("new file text state %q, want pending", s)
	}
	if need, err := db.ListFilesNeedingText(ctx, 10); err != nil || len(need) != 3 {
		t.Fatalf("ListFilesNeedingText = %d, %v", len(need), err)
	}
	if need, err := db.ListFilesNeedingHash(ctx, 10); err != nil || len(need) != 3 {
		t.Fatalf("ListFilesNeedingHash = %d, %v", len(need), err)
	}

	// The first hash of unchanged content keeps the text state. New content
	// queues extraction again.
	if err := db.SetFileText(ctx, jpg.ID, "", ports.TextSkipped); err != nil {
		t.Fatalf("SetFileText: %v", err)
	}
	rev0 := maxRev(t, db)
	if err := db.SetFileHash(ctx, jpg.ID, "aa", 100, time.Now()); err != nil {
		t.Fatalf("SetFileHash: %v", err)
	}
	if s, st := textState(jpg.ID), mustState(t, db, jpg.ID); s != "skipped" || st.rev != rev0+1 {
		t.Fatalf("after first hash: state %q rev %d, want skipped and %d", s, st.rev, rev0+1)
	}
	if err := db.SetFileHash(ctx, jpg.ID, "bb", 100, time.Now()); err != nil {
		t.Fatalf("SetFileHash: %v", err)
	}
	if s := textState(jpg.ID); s != "pending" {
		t.Fatalf("after new content: state %q, want pending", s)
	}
	if err := db.SetFileText(ctx, jpg.ID, "", "bogus"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("SetFileText with a bad state gave %v", err)
	}
	if got, err := db.FindFilesByHash(ctx, sp.ID, "bb", 100); err != nil || len(got) != 1 || got[0].ID != jpg.ID {
		t.Fatalf("FindFilesByHash = %v, %v", got, err)
	}
	if got, _ := db.FindFilesByHash(ctx, sp.ID, "bb", 101); len(got) != 0 {
		t.Fatalf("FindFilesByHash with another size found %d", len(got))
	}

	names := func(files []*core.File) string {
		var out []string
		for _, f := range files {
			out = append(out, f.Name)
		}
		return strings.Join(out, ",")
	}
	lists := []struct {
		name string
		opts ports.FileListOptions
		want string
	}{
		{"by name", ports.FileListOptions{}, "east wall.jpg,notes.txt,report.pdf"},
		{"type without slash", ports.FileListOptions{Type: "image"}, "east wall.jpg"},
		{"type prefix", ports.FileListOptions{Type: "application/"}, "report.pdf"},
		{"parent", ports.FileListOptions{ParentID: media.ID}, "east wall.jpg,report.pdf"},
		{"root", ports.FileListOptions{HasParent: true}, "notes.txt"},
		{"size", ports.FileListOptions{Sort: "size"}, "report.pdf,east wall.jpg,notes.txt"},
		{"page", ports.FileListOptions{Limit: 1, Offset: 1}, "notes.txt"},
		{"type wildcard is literal", ports.FileListOptions{Type: "%"}, ""},
	}
	for _, tc := range lists {
		got, err := db.ListFiles(ctx, sp.ID, tc.opts)
		if err != nil || names(got) != tc.want {
			t.Errorf("ListFiles %s = %q, %v, want %q", tc.name, names(got), err, tc.want)
		}
	}

	// Rename keeps the id, takes a new revision and updates the search name.
	renamed := *pdf
	renamed.Path, renamed.Name = "Media/Report 2025.pdf", ""
	if err := db.UpdateFile(ctx, &renamed); err != nil {
		t.Fatalf("UpdateFile rename: %v", err)
	}
	if renamed.Rev <= pdf.Rev || renamed.Name != "Report 2025.pdf" {
		t.Fatalf("renamed file rev %d name %q", renamed.Rev, renamed.Name)
	}
	if hits, err := db.SearchFiles(ctx, []string{sp.ID}, core.SearchQuery{Text: "2025"}); err != nil || len(hits) != 1 {
		t.Fatalf("SearchFiles for the new name = %d, %v", len(hits), err)
	}
	clash := renamed
	clash.Path, clash.Name = "Media/EAST WALL.JPG", ""
	if err := db.UpdateFile(ctx, &clash); !errors.Is(err, core.ErrExists) {
		t.Fatalf("UpdateFile onto a sibling gave %v", err)
	}

	// Trash hides the file and frees its name. Restore refuses a taken path.
	if err := db.TrashFile(ctx, pdf.ID, time.Now(), "u"); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if n, _ := db.CountFiles(ctx); n != 2 {
		t.Fatalf("CountFiles = %d, want 2", n)
	}
	if got, _ := db.ListFiles(ctx, sp.ID, ports.FileListOptions{IncludeDeleted: true}); len(got) != 3 {
		t.Fatalf("ListFiles with trash = %d, want 3", len(got))
	}
	mustFile(t, db, sp.ID, media.ID, "Media/report 2025.pdf", "application/pdf", 1)
	if err := db.RestoreFile(ctx, pdf.ID, media.ID, ""); !errors.Is(err, core.ErrExists) {
		t.Fatalf("RestoreFile onto a taken path gave %v", err)
	}
	if err := db.RestoreFile(ctx, pdf.ID, media.ID, "Media/Report 2025 (1).pdf"); err != nil {
		t.Fatalf("RestoreFile to a free path: %v", err)
	}
	if got, err := db.GetFile(ctx, pdf.ID); err != nil || got.Deleted || got.Name != "Report 2025 (1).pdf" {
		t.Fatalf("restored file = %+v, %v", got, err)
	}
	if err := db.DeleteFile(ctx, pdf.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, err := db.GetFile(ctx, pdf.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetFile after delete gave %v", err)
	}
	if err := db.DeleteFile(ctx, pdf.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second DeleteFile gave %v", err)
	}
}

// mustState reads an item state or fails.
func mustState(t *testing.T, db *DB, id string) itemState {
	t.Helper()
	s, ok := stateOf(t, db, id)
	if !ok {
		t.Fatalf("item %s is missing", id)
	}
	return s
}

func TestMoveFolderRewritesSubtree(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	sp := mustSpace(t, db, "docs")
	a := mustFolder(t, db, sp.ID, "", "A")
	idx := mustNoteAt(t, db, sp.ID, a.ID, "A/index.md", "A")
	b := mustFolder(t, db, sp.ID, a.ID, "A/B")
	c := mustFile(t, db, sp.ID, b.ID, "A/B/c.pdf", "application/pdf", 1)
	d := mustNoteAt(t, db, sp.ID, b.ID, "A/B/d.md", "D")
	e := mustFile(t, db, sp.ID, a.ID, "A/e.jpg", "image/jpeg", 1)
	old := mustFile(t, db, sp.ID, a.ID, "A/old.pdf", "application/pdf", 1)
	if err := db.TrashFile(ctx, old.ID, time.Now(), "u"); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	x := mustFolder(t, db, sp.ID, "", "X")
	mustFolder(t, db, sp.ID, "", "Y")
	mustFile(t, db, sp.ID, "", "z.pdf", "application/pdf", 1)

	before := maxRev(t, db)
	if err := db.MoveFolder(ctx, a.ID, x.ID, "X/A2"); err != nil {
		t.Fatalf("MoveFolder: %v", err)
	}
	after := maxRev(t, db)
	want := map[string]string{
		a.ID: "X/A2", idx.ID: "X/A2/index.md", b.ID: "X/A2/B", c.ID: "X/A2/B/c.pdf",
		d.ID: "X/A2/B/d.md", e.ID: "X/A2/e.jpg", old.ID: "X/A2/old.pdf",
	}
	if after-before != int64(len(want)) {
		t.Fatalf("the move used %d revisions, want %d", after-before, len(want))
	}
	seen := map[int64]bool{}
	for id, p := range want {
		st := mustState(t, db, id)
		if st.path != p {
			t.Errorf("%s path %q, want %q", st.kind, st.path, p)
		}
		if st.rev <= before || st.rev > after || seen[st.rev] {
			t.Errorf("%s at %q has rev %d, want a fresh one in (%d, %d]", st.kind, p, st.rev, before, after)
		}
		seen[st.rev] = true
	}
	if st := mustState(t, db, a.ID); st.parent != x.ID {
		t.Fatalf("moved folder parent %q, want %q", st.parent, x.ID)
	}
	if st := mustState(t, db, c.ID); st.parent != b.ID {
		t.Fatalf("descendant parent changed to %q", st.parent)
	}
	if st := mustState(t, db, old.ID); !st.deleted {
		t.Fatalf("a trashed descendant was restored by the move")
	}

	refused := []struct {
		name           string
		parent, target string
		want           error
	}{
		{"into a child", b.ID, "X/A2/B/A2", core.ErrInvalid},
		{"into itself", a.ID, "X/A2/A2", core.ErrInvalid},
		{"onto a folder", "", "y", core.ErrExists},
		{"onto a file", "", "Z.PDF", core.ErrExists},
		{"parent mismatch", x.ID, "A2", core.ErrInvalid},
	}
	for _, tc := range refused {
		rev := maxRev(t, db)
		if err := db.MoveFolder(ctx, a.ID, tc.parent, tc.target); !errors.Is(err, tc.want) {
			t.Errorf("MoveFolder %s gave %v, want %v", tc.name, err, tc.want)
		}
		if maxRev(t, db) != rev || mustState(t, db, c.ID).path != "X/A2/B/c.pdf" {
			t.Errorf("MoveFolder %s changed state", tc.name)
		}
	}

	// A case-only rename is a move.
	if err := db.MoveFolder(ctx, a.ID, x.ID, "X/a2"); err != nil {
		t.Fatalf("case rename: %v", err)
	}
	if p := mustState(t, db, d.ID).path; p != "X/a2/B/d.md" {
		t.Fatalf("after case rename note path %q", p)
	}

	// UpdateFolder with the same path touches only the folder row.
	f, _ := db.GetFolder(ctx, a.ID)
	f.Position, f.IndexNoteID = 7, idx.ID
	rev := maxRev(t, db)
	if err := db.UpdateFolder(ctx, f); err != nil {
		t.Fatalf("UpdateFolder: %v", err)
	}
	if got, _ := db.GetFolder(ctx, a.ID); got.Position != 7 || got.IndexNoteID != idx.ID || got.Rev != rev+1 {
		t.Fatalf("UpdateFolder stored %+v", got)
	}
	if maxRev(t, db) != rev+1 {
		t.Fatalf("UpdateFolder without a move used %d revisions", maxRev(t, db)-rev)
	}
	f.Path, f.Name = "Y2", ""
	f.ParentID = ""
	if err := db.UpdateFolder(ctx, f); err != nil {
		t.Fatalf("UpdateFolder move: %v", err)
	}
	if p := mustState(t, db, c.ID).path; p != "Y2/B/c.pdf" {
		t.Fatalf("UpdateFolder move left %q", p)
	}
}

func TestTrashRestoreSubtree(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	sp := mustSpace(t, db, "docs")
	a := mustFolder(t, db, sp.ID, "", "A")
	b := mustFolder(t, db, sp.ID, a.ID, "A/B")
	c := mustFile(t, db, sp.ID, b.ID, "A/B/c.pdf", "application/pdf", 1)
	d := mustNoteAt(t, db, sp.ID, a.ID, "A/d.md", "D")
	solo := mustFile(t, db, sp.ID, a.ID, "A/solo.pdf", "application/pdf", 1)

	t1 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	if err := db.TrashFile(ctx, solo.ID, t1, "u"); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	before := maxRev(t, db)
	if err := db.TrashFolder(ctx, a.ID, t2, "u"); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if got := maxRev(t, db) - before; got != 4 {
		t.Fatalf("TrashFolder used %d revisions, want 4", got)
	}
	for _, id := range []string{a.ID, b.ID, c.ID, d.ID} {
		if st := mustState(t, db, id); !st.deleted || st.deletedAt != fmtTime(t2) || st.rev <= before {
			t.Errorf("%s %q after trash: %+v", st.kind, st.path, st)
		}
	}
	if st := mustState(t, db, solo.ID); st.deletedAt != fmtTime(t1) {
		t.Fatalf("separately trashed file got deleted_at %q", st.deletedAt)
	}
	rev := maxRev(t, db)
	if err := db.TrashFolder(ctx, a.ID, time.Now(), "u"); err != nil || maxRev(t, db) != rev {
		t.Fatalf("second TrashFolder: %v, counter moved %d", err, maxRev(t, db)-rev)
	}

	// A new live folder takes the name while the old one is in the trash.
	fresh := mustFolder(t, db, sp.ID, "", "a")
	freshFile := mustFile(t, db, sp.ID, fresh.ID, "a/c.pdf", "application/pdf", 1)
	freshRev := mustState(t, db, freshFile.ID).rev

	if err := db.RestoreFolder(ctx, a.ID, "", ""); !errors.Is(err, core.ErrExists) {
		t.Fatalf("RestoreFolder onto a live name gave %v", err)
	}
	if err := db.RestoreFolder(ctx, a.ID, "", "A (1)"); err != nil {
		t.Fatalf("RestoreFolder: %v", err)
	}
	want := map[string]struct {
		path    string
		deleted bool
	}{
		a.ID:    {"A (1)", false},
		b.ID:    {"A (1)/B", false},
		c.ID:    {"A (1)/B/c.pdf", false},
		d.ID:    {"A (1)/d.md", false},
		solo.ID: {"A (1)/solo.pdf", true},
	}
	for id, w := range want {
		st := mustState(t, db, id)
		if st.path != w.path || st.deleted != w.deleted {
			t.Errorf("%s after restore: path %q deleted %v, want %q %v", st.kind, st.path, st.deleted, w.path, w.deleted)
		}
		if !st.deleted && st.deletedAt != "" {
			t.Errorf("%s kept deleted_at %q", st.kind, st.deletedAt)
		}
	}
	if st := mustState(t, db, freshFile.ID); st.path != "a/c.pdf" || st.rev != freshRev {
		t.Fatalf("restore touched the unrelated live subtree: %+v", st)
	}
	rev = maxRev(t, db)
	if err := db.RestoreFolder(ctx, a.ID, "", "elsewhere"); err != nil || maxRev(t, db) != rev {
		t.Fatalf("RestoreFolder on a live folder: %v, counter moved", err)
	}
	if err := db.RestoreFile(ctx, solo.ID, a.ID, ""); err != nil {
		t.Fatalf("RestoreFile below the restored folder: %v", err)
	}
	if st := mustState(t, db, solo.ID); st.deleted || st.path != "A (1)/solo.pdf" {
		t.Fatalf("restored solo file: %+v", st)
	}
}

// countRows counts rows of a table whose column holds one of the ids.
func countRows(t *testing.T, db *DB, table, column string, ids ...string) int {
	t.Helper()
	var n int
	err := db.SQL().QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+column+` IN (`+placeholders(len(ids))+`)`,
		stringArgs(ids)...).Scan(&n)
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestDeleteCascades(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	sp := mustSpace(t, db, "docs")
	u := mustUser(t, db, "guest@example.com")
	f := mustFolder(t, db, sp.ID, "", "F")
	n := mustNoteAt(t, db, sp.ID, f.ID, "F/n.md", "Inner note")
	file := mustFile(t, db, sp.ID, f.ID, "F/f.pdf", "application/pdf", 1)
	s := mustFolder(t, db, sp.ID, f.ID, "F/S")
	g := mustFile(t, db, sp.ID, s.ID, "F/S/g.pdf", "application/pdf", 1)
	kept := mustFile(t, db, sp.ID, f.ID, "F/t.pdf", "application/pdf", 1)
	out := mustNoteAt(t, db, sp.ID, "", "out.md", "Out",
		ports.NoteLink{Target: "F/f.pdf", Form: ports.LinkMarkdown},
		ports.NoteLink{Target: "Inner note", Form: ports.LinkWikilink},
		ports.NoteLink{Target: "F/S/g.pdf", Form: ports.LinkPathWikilink})

	targets := map[string]core.ItemKind{f.ID: core.KindFolder, n.ID: core.KindNote, file.ID: core.KindFile, g.ID: core.KindFile}
	var gone []string
	for id, kind := range targets {
		gone = append(gone, id)
		if err := db.CreateItemShare(ctx, kind, &core.Share{NoteID: id, SpaceID: sp.ID, UserID: u.ID, Access: core.AccessView}); err != nil {
			t.Fatalf("CreateItemShare: %v", err)
		}
		if err := db.CreateItemPublicLink(ctx, kind, &core.PublicLink{Token: "tok" + id, NoteID: id, SpaceID: sp.ID}); err != nil {
			t.Fatalf("CreateItemPublicLink: %v", err)
		}
		if err := db.CreateItemComment(ctx, kind, &core.Comment{NoteID: id, SpaceID: sp.ID, UserID: u.ID, Body: "x"}); err != nil {
			t.Fatalf("CreateItemComment: %v", err)
		}
	}
	if err := db.SetStar(ctx, u.ID, n.ID, true); err != nil {
		t.Fatalf("SetStar: %v", err)
	}
	if err := db.CreateShare(ctx, &core.Share{NoteID: out.ID, SpaceID: sp.ID, UserID: u.ID}); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	links, _ := db.OutboundLinks(ctx, out.ID)
	for _, l := range links {
		if l.TargetID == "" {
			t.Fatalf("link %q did not resolve before the delete", l.Target)
		}
	}

	t1 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	if err := db.TrashFile(ctx, kept.ID, t1, "u"); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := db.TrashFolder(ctx, f.ID, t1.Add(time.Minute), "u"); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if err := db.DeleteFolder(ctx, f.ID); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	for _, id := range append(gone, s.ID) {
		if st, ok := stateOf(t, db, id); ok {
			t.Errorf("%s %q survived the delete", st.kind, st.path)
		}
	}
	if _, ok := stateOf(t, db, kept.ID); !ok {
		t.Fatalf("a file trashed on its own was deleted with the folder")
	}
	for _, table := range []string{"shares", "public_links", "comments", "stars"} {
		if c := countRows(t, db, table, "note_id", gone...); c != 0 {
			t.Errorf("%d %s rows still target deleted items", c, table)
		}
	}
	if c := countRows(t, db, "shares", "note_id", out.ID); c != 1 {
		t.Fatalf("the share on the outside note is gone")
	}
	if c := countRows(t, db, "files_fts", "file_id", file.ID, g.ID); c != 0 {
		t.Fatalf("%d file text rows survived", c)
	}
	if c := countRows(t, db, "notes_fts", "note_id", n.ID); c != 0 {
		t.Fatalf("the note text row survived")
	}
	links, _ = db.OutboundLinks(ctx, out.ID)
	if len(links) != 3 {
		t.Fatalf("outside note has %d links, want 3", len(links))
	}
	for _, l := range links {
		if l.TargetID != "" || l.TargetKind != "" {
			t.Errorf("link %q still points at %s %q", l.Target, l.TargetKind, l.TargetID)
		}
	}

	// A new file at the old path picks the markdown link up again.
	f2 := mustFolder(t, db, sp.ID, "", "F")
	file2 := mustFile(t, db, sp.ID, f2.ID, "F/f.pdf", "application/pdf", 1)
	back, err := db.BacklinksTo(ctx, file2.ID)
	if err != nil || len(back) != 1 || back[0].FromNote.ID != out.ID {
		t.Fatalf("BacklinksTo recreated file = %v, %v", back, err)
	}

	// DeleteFile cascades the same way.
	if err := db.CreateItemComment(ctx, core.KindFile, &core.Comment{NoteID: file2.ID, SpaceID: sp.ID, Body: "y"}); err != nil {
		t.Fatalf("CreateItemComment: %v", err)
	}
	if err := db.DeleteFile(ctx, file2.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if c := countRows(t, db, "comments", "note_id", file2.ID); c != 0 {
		t.Fatalf("DeleteFile left %d comments", c)
	}
	if back, _ := db.BacklinksTo(ctx, file2.ID); len(back) != 0 {
		t.Fatalf("DeleteFile left a backlink")
	}

	// Deleting a live folder removes its live subtree only.
	lf := mustFolder(t, db, sp.ID, "", "Live")
	ln := mustNoteAt(t, db, sp.ID, lf.ID, "Live/x.md", "X")
	lt := mustFile(t, db, sp.ID, lf.ID, "Live/t.pdf", "application/pdf", 1)
	if err := db.TrashFile(ctx, lt.ID, time.Now(), "u"); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := db.DeleteFolder(ctx, lf.ID); err != nil {
		t.Fatalf("DeleteFolder live: %v", err)
	}
	if _, ok := stateOf(t, db, ln.ID); ok {
		t.Fatalf("live note survived its folder")
	}
	if _, ok := stateOf(t, db, lt.ID); !ok {
		t.Fatalf("trashed file was deleted with the live folder")
	}
}

func TestItemLookupsAndFeed(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	s1 := mustSpace(t, db, "one")
	s2 := mustSpace(t, db, "two")
	a := mustFolder(t, db, s1.ID, "", "A")
	b := mustFolder(t, db, s1.ID, a.ID, "A/B")
	n := mustNoteAt(t, db, s1.ID, a.ID, "A/n.md", "N")
	f := mustFile(t, db, s1.ID, b.ID, "A/B/c.pdf", "application/pdf", 1)
	other := mustFile(t, db, s2.ID, "", "other.pdf", "application/pdf", 1)

	for id, want := range map[string]core.ItemKind{a.ID: core.KindFolder, n.ID: core.KindNote, f.ID: core.KindFile} {
		kind, space, err := db.ItemKind(ctx, id)
		if err != nil || kind != want || space != s1.ID {
			t.Errorf("ItemKind(%s) = %s, %s, %v", id, kind, space, err)
		}
	}
	if _, _, err := db.ItemKind(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("ItemKind of a missing id gave %v", err)
	}

	tree, err := db.TreeItems(ctx, s1.ID)
	if err != nil || len(tree.Folders) != 2 || len(tree.Notes) != 1 || len(tree.Files) != 1 {
		t.Fatalf("TreeItems = %+v, %v", tree, err)
	}
	if tree.Folders[0].Path != "A" || tree.Folders[1].Path != "A/B" {
		t.Fatalf("TreeItems folders not sorted by path")
	}

	if got, _ := db.Ancestors(ctx, s1.ID, "A/B/c.pdf"); strings.Join(got, ",") != b.ID+","+a.ID {
		t.Fatalf("Ancestors = %v", got)
	}
	if got, _ := db.Ancestors(ctx, s1.ID, "c.pdf"); len(got) != 0 {
		t.Fatalf("Ancestors at the root = %v", got)
	}
	if err := db.TrashFolder(ctx, b.ID, time.Now(), "u"); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if got, _ := db.Ancestors(ctx, s1.ID, "A/B/c.pdf"); strings.Join(got, ",") != b.ID+","+a.ID {
		t.Fatalf("Ancestors through a trashed folder = %v", got)
	}
	b2 := mustFolder(t, db, s1.ID, a.ID, "A/b")
	if got, _ := db.Ancestors(ctx, s1.ID, "A/B/c.pdf"); strings.Join(got, ",") != b2.ID+","+a.ID {
		t.Fatalf("Ancestors did not prefer the live folder: %v", got)
	}

	top := maxRev(t, db)
	revs := func(items []*core.TreeItem) []int64 {
		var out []int64
		for _, it := range items {
			out = append(out, treeItemRev(it))
		}
		return out
	}
	all, err := db.ItemsChangedSince(ctx, []string{s1.ID}, nil, 0, top, 100)
	if err != nil || len(all) != 5 {
		t.Fatalf("ItemsChangedSince space one = %d items, %v", len(all), err)
	}
	if !sort.SliceIsSorted(all, func(i, j int) bool { return treeItemRev(all[i]) < treeItemRev(all[j]) }) {
		t.Fatalf("feed not in revision order: %v", revs(all))
	}
	for _, it := range all {
		set := 0
		for _, ok := range []bool{it.Folder != nil, it.Note != nil, it.File != nil} {
			if ok {
				set++
			}
		}
		if set != 1 {
			t.Fatalf("tree item of kind %s has %d payloads", it.Kind, set)
		}
	}
	withOther, _ := db.ItemsChangedSince(ctx, []string{s1.ID}, []string{other.ID}, 0, top, 100)
	if len(withOther) != 6 {
		t.Fatalf("feed with an extra id = %d items, want 6", len(withOther))
	}
	page, _ := db.ItemsChangedSince(ctx, []string{s1.ID}, nil, 0, top, 2)
	if len(page) != 2 || treeItemRev(page[0]) != treeItemRev(all[0]) || treeItemRev(page[1]) != treeItemRev(all[1]) {
		t.Fatalf("limited feed = %v, want the first two of %v", revs(page), revs(all))
	}
	ceiling := treeItemRev(all[2])
	if got, _ := db.ItemsChangedSince(ctx, []string{s1.ID}, nil, treeItemRev(all[0]), ceiling, 100); len(got) != 2 {
		t.Fatalf("windowed feed = %v", revs(got))
	}
	if got, err := db.ItemsChangedSince(ctx, nil, nil, 0, top, 10); err != nil || got != nil {
		t.Fatalf("empty scope = %v, %v", got, err)
	}
	ids, err := db.ItemIDsInSpaces(ctx, []string{s1.ID, s2.ID})
	if err != nil || len(ids) != 6 || !sort.StringsAreSorted(ids) {
		t.Fatalf("ItemIDsInSpaces = %v, %v", ids, err)
	}
}

func TestItemLinks(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	sp := mustSpace(t, db, "docs")
	elsewhere := mustSpace(t, db, "elsewhere")
	proj := mustFolder(t, db, sp.ID, "", "Projects")
	media := mustFolder(t, db, sp.ID, "", "Media")
	projMedia := mustFolder(t, db, sp.ID, proj.ID, "Projects/Media")
	rootJpg := mustFile(t, db, sp.ID, media.ID, "Media/east wall.jpg", "image/jpeg", 1)
	projJpg := mustFile(t, db, sp.ID, projMedia.ID, "Projects/Media/east wall.jpg", "image/jpeg", 1)
	foreign := mustFile(t, db, elsewhere.ID, "", "x.pdf", "application/pdf", 1)

	plan := mustNoteAt(t, db, sp.ID, proj.ID, "Projects/plan.md", "Plan",
		ports.NoteLink{Target: "../Media/east%20wall.jpg", Form: ports.LinkMarkdownImage},
		ports.NoteLink{Target: "east wall.jpg", Form: ports.LinkWikilink, Embed: true},
		ports.NoteLink{Target: "Media/east wall.jpg", Form: ports.LinkPathWikilink},
		ports.NoteLink{Target: "<report.pdf>", Form: ports.LinkMarkdown},
		ports.NoteLink{Target: "Guide", Form: ports.LinkWikilink, Heading: "Setup"},
		ports.NoteLink{Target: "Media", Form: ports.LinkWikilink},
		ports.NoteLink{Target: "x.pdf", Form: ports.LinkWikilink, TargetID: foreign.ID},
		ports.NoteLink{Target: "B/deep.pdf", Form: ports.LinkPathWikilink},
		ports.NoteLink{Target: "Later", Form: ports.LinkWikilink},
		ports.NoteLink{Target: "/Media/east wall.jpg", Form: ports.LinkMarkdown},
	)

	want := []ports.NoteLink{
		{Target: "Media/east wall.jpg", Form: ports.LinkMarkdownImage, TargetID: rootJpg.ID, TargetKind: core.KindFile},
		{Target: "east wall.jpg", Form: ports.LinkWikilink, Embed: true, TargetID: projJpg.ID, TargetKind: core.KindFile},
		{Target: "Media/east wall.jpg", Form: ports.LinkPathWikilink, TargetID: rootJpg.ID, TargetKind: core.KindFile},
		{Target: "Projects/report.pdf", Form: ports.LinkMarkdown},
		{Target: "Guide", Form: ports.LinkWikilink, Heading: "Setup"},
		{Target: "Media", Form: ports.LinkWikilink, TargetID: projMedia.ID, TargetKind: core.KindFolder},
		{Target: "x.pdf", Form: ports.LinkWikilink},
		{Target: "B/deep.pdf", Form: ports.LinkPathWikilink},
		{Target: "Later", Form: ports.LinkWikilink},
	}
	check := func(label string) {
		t.Helper()
		got, err := db.OutboundLinks(ctx, plan.ID)
		if err != nil || len(got) != len(want) {
			t.Fatalf("%s: OutboundLinks = %d links, %v, want %d", label, len(got), err, len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: link %d = %+v, want %+v", label, i, got[i], want[i])
			}
		}
	}
	// The last markdown link names the same path as the first but in another
	// form, so it is kept as a separate row only when the form differs. Here
	// the form is md-link, which differs from md-image.
	want = append(want, ports.NoteLink{Target: "Media/east wall.jpg", Form: ports.LinkMarkdown, TargetID: rootJpg.ID, TargetKind: core.KindFile})
	check("initial")

	if back, _ := db.BacklinksTo(ctx, projJpg.ID); len(back) != 1 || back[0].FromNote.ID != plan.ID {
		t.Fatalf("BacklinksTo file = %v", back)
	}
	if got, _ := db.NotesLinkingToIDs(ctx, []string{rootJpg.ID, projJpg.ID, "missing"}); len(got) != 1 || got[0].ID != plan.ID {
		t.Fatalf("NotesLinkingToIDs = %v", got)
	}

	// Items created later resolve the links waiting for them.
	report := mustFile(t, db, sp.ID, proj.ID, "Projects/report.pdf", "application/pdf", 1)
	want[3].TargetID, want[3].TargetKind = report.ID, core.KindFile
	guide := mustNoteAt(t, db, sp.ID, "", "guide.md", "Guide")
	want[4].TargetID, want[4].TargetKind = guide.ID, core.KindNote
	b := mustFolder(t, db, sp.ID, proj.ID, "Projects/B")
	deep := mustFile(t, db, sp.ID, b.ID, "Projects/B/deep.pdf", "application/pdf", 1)
	want[7].TargetID, want[7].TargetKind = deep.ID, core.KindFile
	later := mustFolder(t, db, sp.ID, "", "Later")
	want[8].TargetID, want[8].TargetKind = later.ID, core.KindFolder
	check("after creates")

	// A folder with a folder note resolves a name link to the note.
	docs := mustFolder(t, db, sp.ID, "", "Docs")
	docsIdx := mustNoteAt(t, db, sp.ID, docs.ID, "Docs/index.md", "Documentation")
	docs.IndexNoteID = docsIdx.ID
	if err := db.UpdateFolder(ctx, docs); err != nil {
		t.Fatalf("UpdateFolder: %v", err)
	}
	reader := mustNoteAt(t, db, sp.ID, "", "reader.md", "Reader",
		ports.NoteLink{Target: "Docs", Form: ports.LinkWikilink},
		ports.NoteLink{Target: "Documentation", Form: ports.LinkWikilink})
	links, _ := db.OutboundLinks(ctx, reader.ID)
	for _, l := range links {
		if l.TargetID != docsIdx.ID {
			t.Errorf("link %q resolved to %q, want the folder note", l.Target, l.TargetID)
		}
	}

	if err := db.ResolveDanglingLinks(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("ResolveDanglingLinks of a missing id gave %v", err)
	}
	if err := db.ResolveDanglingLinks(ctx, report.ID); err != nil {
		t.Fatalf("ResolveDanglingLinks: %v", err)
	}
}

func TestMarkdownLinkPath(t *testing.T) {
	tests := []struct{ note, target, want string }{
		{"a/b/n.md", "c.pdf", "a/b/c.pdf"},
		{"a/b/n.md", "../c.pdf", "a/c.pdf"},
		{"a/b/n.md", "/c.pdf", "c.pdf"},
		{"n.md", "./x%20y.png", "x y.png"},
		{"n.md", "<x (1).png>", "x (1).png"},
		{"n.md", "x.md#part", "x.md"},
		{"n.md", "../../up.pdf", "../../up.pdf"},
		{"n.md", "#only", ""},
		{"n.md", "bad%zz.pdf", "bad%zz.pdf"},
	}
	for _, tc := range tests {
		if got := markdownLinkPath(tc.note, tc.target); got != tc.want {
			t.Errorf("markdownLinkPath(%q, %q) = %q, want %q", tc.note, tc.target, got, tc.want)
		}
	}
}

func TestItemSharesLinksAndComments(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	sp := mustSpace(t, db, "docs")
	u := mustUser(t, db, "guest@example.com")
	f := mustFolder(t, db, sp.ID, "", "F")
	file := mustFile(t, db, sp.ID, f.ID, "F/a.pdf", "application/pdf", 1)
	n := mustNoteAt(t, db, sp.ID, "", "n.md", "N")

	if err := db.CreateItemShare(ctx, core.KindFolder, &core.Share{NoteID: f.ID, SpaceID: sp.ID, UserID: u.ID, Access: core.AccessView, IncludeChildren: true}); err != nil {
		t.Fatalf("CreateItemShare folder: %v", err)
	}
	if err := db.CreateItemShare(ctx, core.KindFile, &core.Share{NoteID: file.ID, SpaceID: sp.ID, UserID: u.ID, Access: core.AccessComment}); err != nil {
		t.Fatalf("CreateItemShare file: %v", err)
	}
	if err := db.CreateShare(ctx, &core.Share{NoteID: n.ID, SpaceID: sp.ID, UserID: u.ID, Access: core.AccessEdit}); err != nil {
		t.Fatalf("CreateShare: %v", err)
	}
	if err := db.CreateItemShare(ctx, "bogus", &core.Share{NoteID: n.ID}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("CreateItemShare with a bad kind gave %v", err)
	}
	if err := db.CreateItemShare(ctx, core.KindFolder, &core.Share{NoteID: f.ID, SpaceID: sp.ID, UserID: u.ID}); !errors.Is(err, core.ErrExists) {
		t.Fatalf("duplicate item share gave %v", err)
	}
	if got, _ := db.ListItemShares(ctx, core.KindFolder, f.ID); len(got) != 1 {
		t.Fatalf("ListItemShares folder = %d", len(got))
	}
	if got, _ := db.ListItemShares(ctx, core.KindNote, f.ID); len(got) != 0 {
		t.Fatalf("ListItemShares with the wrong kind = %d", len(got))
	}
	grants, err := db.ItemShareGrantsForUser(ctx, u.ID)
	if err != nil || len(grants) != 3 {
		t.Fatalf("ItemShareGrantsForUser = %v, %v", grants, err)
	}
	wantGrants := []ports.ShareGrant{
		{NoteID: f.ID, SpaceID: sp.ID, NotePath: "F", Access: core.AccessView, IncludeChildren: true, Kind: core.KindFolder},
		{NoteID: file.ID, SpaceID: sp.ID, NotePath: "F/a.pdf", Access: core.AccessComment, Kind: core.KindFile},
		{NoteID: n.ID, SpaceID: sp.ID, NotePath: "n.md", Access: core.AccessEdit, Kind: core.KindNote},
	}
	for i := range wantGrants {
		if grants[i] != wantGrants[i] {
			t.Errorf("grant %d = %+v, want %+v", i, grants[i], wantGrants[i])
		}
	}
	if old, _ := db.ShareGrantsForUser(ctx, u.ID); len(old) != 1 || old[0].NoteID != n.ID {
		t.Fatalf("ShareGrantsForUser returned non-note grants: %v", old)
	}

	pl := &core.PublicLink{Token: "file-token", NoteID: file.ID, SpaceID: sp.ID, Access: core.AccessView}
	if err := db.CreateItemPublicLink(ctx, core.KindFile, pl); err != nil {
		t.Fatalf("CreateItemPublicLink: %v", err)
	}
	legacy := &core.PublicLink{Token: "note-token", NoteID: n.ID, SpaceID: sp.ID}
	if err := db.CreatePublicLink(ctx, legacy); err != nil {
		t.Fatalf("CreatePublicLink: %v", err)
	}
	if k, err := db.PublicLinkKind(ctx, pl.ID); err != nil || k != core.KindFile {
		t.Fatalf("PublicLinkKind file = %q, %v", k, err)
	}
	if k, err := db.PublicLinkKind(ctx, legacy.ID); err != nil || k != core.KindNote {
		t.Fatalf("PublicLinkKind note = %q, %v", k, err)
	}
	if got, _ := db.ListItemPublicLinks(ctx, core.KindFile, file.ID); len(got) != 1 || got[0].Token != "file-token" {
		t.Fatalf("ListItemPublicLinks = %v", got)
	}

	if err := db.CreateItemComment(ctx, core.KindFolder, &core.Comment{NoteID: f.ID, SpaceID: sp.ID, UserID: u.ID, Body: "hi"}); err != nil {
		t.Fatalf("CreateItemComment: %v", err)
	}
	if got, _ := db.ListItemComments(ctx, core.KindFolder, f.ID); len(got) != 1 || got[0].Body != "hi" {
		t.Fatalf("ListItemComments = %v", got)
	}
	if got, _ := db.ListComments(ctx, f.ID); len(got) != 1 {
		t.Fatalf("ListComments by id = %d", len(got))
	}
}

func TestSearchFiles(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	sp := mustSpace(t, db, "docs")
	other := mustSpace(t, db, "other")
	u := mustUser(t, db, "author@example.com")

	type fileText struct {
		path, body string
		trash      bool
	}
	byName := map[string]*core.File{}
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i, ft := range []fileText{
		{"report.pdf", "Quarterly revenue <script>alert(1)</script> grew", false},
		{"east wall.jpg", "", false},
		{"notes.txt", "revenue plan \uE000bad\uE001 marker", false},
		{"old revenue.pdf", "revenue", true},
	} {
		f := &core.File{SpaceID: sp.ID, Path: ft.path, MIME: "application/pdf", CreatedBy: u.ID,
			UpdatedAt: base.Add(time.Duration(i) * time.Hour)}
		if err := db.CreateFile(ctx, f); err != nil {
			t.Fatalf("CreateFile: %v", err)
		}
		if err := db.SetFileText(ctx, f.ID, ft.body, ports.TextDone); err != nil {
			t.Fatalf("SetFileText: %v", err)
		}
		if ft.trash {
			if err := db.TrashFile(ctx, f.ID, time.Now(), "u"); err != nil {
				t.Fatalf("TrashFile: %v", err)
			}
		}
		byName[ft.path] = f
	}

	names := func(hits []*ports.FileHit) string {
		var out []string
		for _, h := range hits {
			out = append(out, h.File.Name)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	tests := []struct {
		name   string
		spaces []string
		q      core.SearchQuery
		want   string
	}{
		{"name hit", []string{sp.ID}, core.SearchQuery{Text: "east"}, "east wall.jpg"},
		{"prefix on the last term", []string{sp.ID}, core.SearchQuery{Text: "quart"}, "report.pdf"},
		{"body hit", []string{sp.ID}, core.SearchQuery{Text: "quarterly"}, "report.pdf"},
		{"trash excluded", []string{sp.ID}, core.SearchQuery{Text: "old"}, ""},
		{"trash included", []string{sp.ID}, core.SearchQuery{Text: "old", IncludeTrash: true}, "old revenue.pdf"},
		{"any-term fallback", []string{sp.ID}, core.SearchQuery{Text: "quarterly zebra"}, "report.pdf"},
		{"stopwords dropped in fallback", []string{sp.ID}, core.SearchQuery{Text: "the revenue"}, "notes.txt,report.pdf"},
		{"tags match no file", []string{sp.ID}, core.SearchQuery{Text: "revenue", Tags: []string{"x"}}, ""},
		{"recency listing", []string{sp.ID}, core.SearchQuery{Text: "  "}, "east wall.jpg,notes.txt,report.pdf"},
		{"space narrowing", []string{sp.ID}, core.SearchQuery{Text: "revenue", SpaceIDs: []string{other.ID}}, ""},
		{"no spaces", nil, core.SearchQuery{Text: "revenue"}, ""},
		{"author", []string{sp.ID}, core.SearchQuery{Text: "revenue", AuthorID: "someone"}, ""},
		{"operators are terms", []string{sp.ID}, core.SearchQuery{Text: `"east" OR (wall`}, "east wall.jpg"},
		{"NUL", []string{sp.ID}, core.SearchQuery{Text: "\x00"}, "east wall.jpg,notes.txt,report.pdf"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := db.SearchFiles(ctx, tc.spaces, tc.q)
			if err != nil {
				t.Fatalf("SearchFiles: %v", err)
			}
			if got := names(hits); got != tc.want {
				t.Fatalf("hits %q, want %q", got, tc.want)
			}
		})
	}

	hits, _ := db.SearchFiles(ctx, []string{sp.ID}, core.SearchQuery{Text: "Quarterly"})
	snip := hits[0].Snippet
	if strings.Count(snip, "<mark>") != 1 || strings.Count(snip, "</mark>") != 1 || !strings.Contains(snip, "<mark>Quarterly</mark>") {
		t.Fatalf("snippet marks: %q", snip)
	}
	plain := strings.NewReplacer("<mark>", "", "</mark>", "").Replace(snip)
	if strings.ContainsAny(plain, "<>") || !strings.Contains(plain, "&lt;script&gt;") {
		t.Fatalf("snippet not escaped: %q", snip)
	}
	if hits[0].Score <= 0 {
		t.Fatalf("score %v, want positive", hits[0].Score)
	}

	hits, _ = db.SearchFiles(ctx, []string{sp.ID}, core.SearchQuery{Text: "bad"})
	if len(hits) != 1 || strings.ContainsAny(hits[0].Snippet, "\uE000\uE001") || strings.Count(hits[0].Snippet, "<mark>") != 1 {
		t.Fatalf("stored markers leaked into the snippet: %v", hits)
	}

	hits, _ = db.SearchFiles(ctx, []string{sp.ID}, core.SearchQuery{Text: "east"})
	if len(hits) != 1 || hits[0].Snippet != "<mark>east</mark> wall.jpg" {
		t.Fatalf("name snippet = %v", hits)
	}

	recent, _ := db.SearchFiles(ctx, []string{sp.ID}, core.SearchQuery{})
	if len(recent) != 3 || recent[0].File.ID != byName["notes.txt"].ID {
		t.Fatalf("recency order starts with %v", recent)
	}
}

func TestNotesLivePathUniqueness(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	sp := mustSpace(t, db, "docs")
	n1 := mustNoteAt(t, db, sp.ID, "", "Guide.md", "Guide")
	at := time.Now()
	if err := db.SetNoteDeleted(ctx, n1.ID, true, &at, "u"); err != nil {
		t.Fatalf("SetNoteDeleted: %v", err)
	}
	n2 := mustNoteAt(t, db, sp.ID, "", "guide.md", "Guide again")
	if err := db.SetNoteDeleted(ctx, n1.ID, false, nil, ""); !errors.Is(err, core.ErrExists) {
		t.Fatalf("restoring onto a live note gave %v", err)
	}
	n3 := &core.Note{ID: core.NewID(), SpaceID: sp.ID, Path: "GUIDE.md", Slug: "GUIDE"}
	if err := db.UpsertNote(ctx, ports.NoteRow{Note: n3}); !errors.Is(err, core.ErrExists) {
		t.Fatalf("a case variant of a live note gave %v", err)
	}

	mustFolder(t, db, sp.ID, "", "X.MD")
	if err := db.SetNotePath(ctx, n2.ID, "x.md", "x", ""); !errors.Is(err, core.ErrExists) {
		t.Fatalf("moving a note onto a folder gave %v", err)
	}
	if err := db.SetNotePath(ctx, n1.ID, "x.md", "x", ""); err != nil {
		t.Fatalf("moving a trashed note: %v", err)
	}
	if err := db.SetNoteDeleted(ctx, n1.ID, false, nil, ""); !errors.Is(err, core.ErrExists) {
		t.Fatalf("restoring a note onto a folder gave %v", err)
	}
}
