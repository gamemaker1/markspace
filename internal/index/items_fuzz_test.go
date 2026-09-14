package index

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// Names the harness draws from. Folder names collide with each other by
// ASCII and Unicode case, and with note and file names. Note names stay
// ASCII, because note-to-note uniqueness is the job of the SQLite index,
// which folds ASCII only.
var (
	fuzzFolderNames = []string{"a", "A", "b", "é", "É", "a.md", "A.pdf"}
	fuzzNoteNames   = []string{"a.md", "B.md", "b.md"}
	fuzzFileNames   = []string{"a.pdf", "B.pdf", "b.pdf", "é.pdf", "É.pdf"}
)

// harnessItem is one stored row as the harness sees it.
type harnessItem struct {
	kind      core.ItemKind
	id        string
	space     string
	parent    string
	path      string
	deleted   bool
	deletedAt string
	rev       int64
}

// itemHarness runs a byte-driven sequence of item operations against one
// database and checks invariants after each step.
type itemHarness struct {
	t      *testing.T
	db     *DB
	ctx    context.Context
	spaces []string
	data   []byte
	pos    int
	clock  time.Time
}

// next returns the next input byte, or 0 once the input is used up.
func (h *itemHarness) next() int {
	if h.pos >= len(h.data) {
		return 0
	}
	b := h.data[h.pos]
	h.pos++
	return int(b)
}

// tick returns a fresh time, so every trash has its own deleted_at.
func (h *itemHarness) tick() time.Time {
	h.clock = h.clock.Add(time.Millisecond)
	return h.clock
}

// items reads every row in the harness spaces.
func (h *itemHarness) items() map[string]harnessItem {
	h.t.Helper()
	list := `(` + placeholders(len(h.spaces)) + `)`
	args := stringArgs(h.spaces)
	all := append(append(append([]any{}, args...), args...), args...)
	rows, err := h.db.SQL().Query(`SELECT 'folder', id, space_id, parent_id, path, deleted, deleted_at, rev FROM folders WHERE space_id IN `+list+`
		UNION ALL SELECT 'note', id, space_id, parent_id, path, deleted, deleted_at, rev FROM notes WHERE space_id IN `+list+`
		UNION ALL SELECT 'file', id, space_id, parent_id, path, deleted, deleted_at, rev FROM files WHERE space_id IN `+list, all...)
	if err != nil {
		h.t.Fatalf("read items: %v", err)
	}
	defer rows.Close()
	out := map[string]harnessItem{}
	for rows.Next() {
		var (
			it      harnessItem
			kind    string
			deleted int
		)
		if err := rows.Scan(&kind, &it.id, &it.space, &it.parent, &it.path, &deleted, &it.deletedAt, &it.rev); err != nil {
			h.t.Fatalf("read items: %v", err)
		}
		it.kind = core.ItemKind(kind)
		it.deleted = deleted != 0
		out[it.id] = it
	}
	if err := rows.Err(); err != nil {
		h.t.Fatalf("read items: %v", err)
	}
	return out
}

// sorted returns the items ordered by id, so a pick is deterministic.
func sorted(items map[string]harnessItem, keep func(harnessItem) bool) []harnessItem {
	var out []harnessItem
	for _, it := range items {
		if keep(it) {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// pick chooses one matching item.
func (h *itemHarness) pick(items map[string]harnessItem, keep func(harnessItem) bool) (harnessItem, bool) {
	c := sorted(items, keep)
	if len(c) == 0 {
		return harnessItem{}, false
	}
	return c[h.next()%len(c)], true
}

// pickParent chooses a live folder in the space, or the root.
func (h *itemHarness) pickParent(items map[string]harnessItem, space string) (id, dir string) {
	c := sorted(items, func(it harnessItem) bool {
		return it.kind == core.KindFolder && !it.deleted && it.space == space
	})
	i := h.next() % (len(c) + 1)
	if i == len(c) {
		return "", ""
	}
	return c[i].id, c[i].path
}

// step runs one operation chosen through ops. It returns the error of an
// allowed refusal, and fails the test on any other error.
func (h *itemHarness) step(ops []int) (string, error) {
	items := h.items()
	op := ops[h.next()%len(ops)]
	ctx, db := h.ctx, h.db
	allow := func(err error, allowed ...error) error {
		if err == nil {
			return nil
		}
		for _, a := range allowed {
			if errors.Is(err, a) {
				return err
			}
		}
		h.t.Fatalf("op %d: unexpected error: %v", op, err)
		return err
	}
	space := h.spaces[h.next()%len(h.spaces)]
	switch op {
	case 0:
		parent, dir := h.pickParent(items, space)
		p := joinPath(dir, fuzzFolderNames[h.next()%len(fuzzFolderNames)])
		return "create folder " + p, allow(db.CreateFolder(ctx, &core.Folder{SpaceID: space, ParentID: parent, Path: p}), core.ErrExists)
	case 1:
		parent, dir := h.pickParent(items, space)
		p := joinPath(dir, fuzzNoteNames[h.next()%len(fuzzNoteNames)])
		n := &core.Note{ID: core.NewID(), SpaceID: space, ParentID: parent, Path: p, Slug: pathWithoutExt(baseOf(p)), Title: baseOf(p)}
		return "create note " + p, allow(db.UpsertNote(ctx, ports.NoteRow{Note: n}), core.ErrExists)
	case 2:
		parent, dir := h.pickParent(items, space)
		p := joinPath(dir, fuzzFileNames[h.next()%len(fuzzFileNames)])
		return "create file " + p, allow(db.CreateFile(ctx, &core.File{SpaceID: space, ParentID: parent, Path: p}), core.ErrExists)
	case 3:
		it, ok := h.pick(items, func(it harnessItem) bool { return it.kind == core.KindFile && !it.deleted })
		if !ok {
			return "no file to move", nil
		}
		f, err := db.GetFile(ctx, it.id)
		if err != nil {
			h.t.Fatalf("GetFile: %v", err)
		}
		parent, dir := h.pickParent(items, it.space)
		f.ParentID, f.Path, f.Name = parent, joinPath(dir, fuzzFileNames[h.next()%len(fuzzFileNames)]), ""
		return "move file to " + f.Path, allow(db.UpdateFile(ctx, f), core.ErrExists)
	case 4:
		it, ok := h.pick(items, func(it harnessItem) bool { return it.kind == core.KindFolder && !it.deleted })
		if !ok {
			return "no folder to move", nil
		}
		parent, dir := h.pickParent(items, it.space)
		p := joinPath(dir, fuzzFolderNames[h.next()%len(fuzzFolderNames)])
		err := db.MoveFolder(ctx, it.id, parent, p)
		inside := dir == it.path || strings.HasPrefix(dir, it.path+"/")
		if errors.Is(err, core.ErrInvalid) != inside {
			h.t.Fatalf("MoveFolder %q to %q: error %v, into itself %v", it.path, p, err, inside)
		}
		return fmt.Sprintf("move folder %q to %q", it.path, p), allow(err, core.ErrExists, core.ErrInvalid)
	case 5:
		it, ok := h.pick(items, func(it harnessItem) bool { return it.kind == core.KindNote && !it.deleted })
		if !ok {
			return "no note to move", nil
		}
		parent, dir := h.pickParent(items, it.space)
		p := joinPath(dir, fuzzNoteNames[h.next()%len(fuzzNoteNames)])
		return "move note to " + p, allow(db.SetNotePath(ctx, it.id, p, pathWithoutExt(baseOf(p)), parent), core.ErrExists)
	case 6:
		it, ok := h.pick(items, func(it harnessItem) bool { return !it.deleted })
		if !ok {
			return "nothing to trash", nil
		}
		at := h.tick()
		var err error
		switch it.kind {
		case core.KindFolder:
			err = db.TrashFolder(ctx, it.id, at, "u")
		case core.KindFile:
			err = db.TrashFile(ctx, it.id, at, "u")
		default:
			err = db.SetNoteDeleted(ctx, it.id, true, &at, "u")
		}
		return fmt.Sprintf("trash %s %q", it.kind, it.path), allow(err)
	case 7:
		it, ok := h.pick(items, func(it harnessItem) bool { return it.deleted })
		if !ok {
			return "nothing to restore", nil
		}
		// Restore to the old place when its parent folder is live and still at
		// the parent directory, and to the root otherwise.
		parent, p := "", baseOf(it.path)
		if par, ok := items[it.parent]; ok && par.kind == core.KindFolder && !par.deleted && par.path == dirOf(it.path) {
			parent, p = it.parent, it.path
		}
		var err error
		switch it.kind {
		case core.KindFolder:
			err = db.RestoreFolder(ctx, it.id, parent, p)
		case core.KindFile:
			err = db.RestoreFile(ctx, it.id, parent, p)
		default:
			if p != it.path || parent != it.parent {
				// A trashed note moves first. The restore is a later step.
				return "place trashed note at " + p, allow(db.SetNotePath(ctx, it.id, p, pathWithoutExt(baseOf(p)), parent))
			}
			err = db.SetNoteDeleted(ctx, it.id, false, nil, "")
		}
		return fmt.Sprintf("restore %s %q to %q", it.kind, it.path, p), allow(err, core.ErrExists)
	case 8:
		it, ok := h.pick(items, func(harnessItem) bool { return true })
		if !ok {
			return "nothing to delete", nil
		}
		var err error
		switch it.kind {
		case core.KindFolder:
			err = db.DeleteFolder(ctx, it.id)
		case core.KindFile:
			err = db.DeleteFile(ctx, it.id)
		default:
			err = db.DeleteNoteRow(ctx, it.id)
		}
		return fmt.Sprintf("delete %s %q", it.kind, it.path), allow(err)
	case 9:
		it, ok := h.pick(items, func(it harnessItem) bool { return it.kind == core.KindFolder })
		if !ok {
			return "no folder to update", nil
		}
		f, err := db.GetFolder(ctx, it.id)
		if err != nil {
			h.t.Fatalf("GetFolder: %v", err)
		}
		f.Position++
		return "update folder " + f.Path, allow(db.UpdateFolder(ctx, f))
	default:
		it, ok := h.pick(items, func(it harnessItem) bool { return it.kind == core.KindFile })
		if !ok {
			return "no file to hash", nil
		}
		return "hash file " + it.path, allow(db.SetFileHash(ctx, it.id, fmt.Sprintf("%02x", h.next()), int64(h.next()), h.tick()))
	}
}

// checkRevisions verifies the revision rules for one step.
func (h *itemHarness) checkRevisions(desc string, stepErr error, before, after map[string]harnessItem, c0, c1 int64) {
	h.t.Helper()
	if stepErr != nil {
		if c1 != c0 {
			h.t.Fatalf("%s: refused with %v but moved the counter %d -> %d", desc, stepErr, c0, c1)
		}
		if len(before) != len(after) {
			h.t.Fatalf("%s: refused with %v but changed the row count", desc, stepErr)
		}
		for id, b := range before {
			if after[id] != b {
				h.t.Fatalf("%s: refused with %v but changed %+v to %+v", desc, stepErr, b, after[id])
			}
		}
	}
	var fresh []int64
	for id, a := range after {
		b, existed := before[id]
		if existed && b.rev == a.rev {
			if b != a {
				h.t.Fatalf("%s: %s changed from %+v to %+v without a new revision", desc, a.kind, b, a)
			}
			continue
		}
		if a.rev <= c0 || a.rev > c1 {
			h.t.Fatalf("%s: %s %q got rev %d outside (%d, %d]", desc, a.kind, a.path, a.rev, c0, c1)
		}
		fresh = append(fresh, a.rev)
	}
	// Every revision the step drew belongs to exactly one surviving or
	// deleted row, so the fresh revisions are a contiguous run unless a row
	// that took one was deleted in the same step, which no operation does.
	sort.Slice(fresh, func(i, j int) bool { return fresh[i] < fresh[j] })
	if int64(len(fresh)) != c1-c0 {
		h.t.Fatalf("%s: %d rows took fresh revisions, counter moved by %d", desc, len(fresh), c1-c0)
	}
	for i, r := range fresh {
		if r != c0+int64(i)+1 {
			h.t.Fatalf("%s: fresh revisions %v are not the run after %d", desc, fresh, c0)
		}
	}
	seen := map[int64]string{}
	for id, a := range after {
		if other, dup := seen[a.rev]; dup {
			h.t.Fatalf("%s: rev %d held by %s and %s", desc, a.rev, other, id)
		}
		seen[a.rev] = id
	}

	full, err := h.db.ItemsChangedSince(h.ctx, h.spaces, nil, 0, c1, 1<<20)
	if err != nil {
		h.t.Fatalf("ItemsChangedSince: %v", err)
	}
	if len(full) != len(after) {
		h.t.Fatalf("%s: feed has %d items, index has %d", desc, len(full), len(after))
	}
	var last int64
	got := map[string]bool{}
	for _, it := range full {
		id, rev := treeItemID(it), treeItemRev(it)
		if rev <= last {
			h.t.Fatalf("%s: feed revisions not increasing at %d after %d", desc, rev, last)
		}
		last = rev
		if got[id] || after[id].rev != rev || after[id].kind != it.Kind {
			h.t.Fatalf("%s: feed item %s rev %d does not match the index", desc, id, rev)
		}
		got[id] = true
	}

	var paged []*core.TreeItem
	since := int64(0)
	for guard := 0; guard <= len(full)+1; guard++ {
		page, err := h.db.ItemsChangedSince(h.ctx, h.spaces, nil, since, c1, 1+h.next()%8)
		if err != nil {
			h.t.Fatalf("ItemsChangedSince page: %v", err)
		}
		if len(page) == 0 {
			break
		}
		paged = append(paged, page...)
		since = treeItemRev(page[len(page)-1])
	}
	if len(paged) != len(full) {
		h.t.Fatalf("%s: paging returned %d items, one page %d", desc, len(paged), len(full))
	}
	for i := range full {
		if treeItemID(paged[i]) != treeItemID(full[i]) {
			h.t.Fatalf("%s: paged feed differs at %d", desc, i)
		}
	}
}

// treeItemID returns the id of whichever item is set.
func treeItemID(it *core.TreeItem) string {
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

// checkTree verifies the placement rules over live items.
func (h *itemHarness) checkTree(desc string, items map[string]harnessItem) {
	h.t.Helper()
	folderAt := map[string]string{}
	for _, it := range items {
		if it.kind == core.KindFolder && !it.deleted {
			folderAt[it.space+"\x00"+it.path] = it.id
		}
	}
	siblings := map[string][]harnessItem{}
	for _, it := range items {
		if it.deleted {
			continue
		}
		dir := dirOf(it.path)
		if dir == "" {
			if it.parent != "" {
				h.t.Fatalf("%s: root %s %q has parent %q", desc, it.kind, it.path, it.parent)
			}
		} else {
			id, ok := folderAt[it.space+"\x00"+dir]
			if !ok || id != it.parent {
				h.t.Fatalf("%s: %s %q has parent %q, live folder at %q is %q", desc, it.kind, it.path, it.parent, dir, id)
			}
			if !strings.HasPrefix(it.path, items[it.parent].path+"/") {
				h.t.Fatalf("%s: %s %q is not below its parent %q", desc, it.kind, it.path, items[it.parent].path)
			}
		}
		key := it.space + "\x00" + dir
		for _, s := range siblings[key] {
			if strings.EqualFold(baseOf(s.path), baseOf(it.path)) {
				h.t.Fatalf("%s: live siblings %s %q and %s %q collide", desc, s.kind, s.path, it.kind, it.path)
			}
		}
		siblings[key] = append(siblings[key], it)
	}
}

// fuzzItemSeeds returns deterministic pseudo-random inputs.
func fuzzItemSeeds() [][]byte {
	var out [][]byte
	x := uint32(2463534242)
	for s := 0; s < 12; s++ {
		b := make([]byte, 160+s*20)
		for i := range b {
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			b[i] = byte(x)
		}
		out = append(out, b)
	}
	return append(out, []byte{}, []byte{0, 0, 0, 0, 0, 0, 2, 0, 1, 4, 0, 0, 0, 6, 0, 0, 7, 0, 0, 8})
}

// runItemFuzz opens one database for the fuzz worker and runs each input in
// two fresh spaces.
func runItemFuzz(f *testing.F, ops []int, revisions, tree bool) {
	for _, seed := range fuzzItemSeeds() {
		f.Add(seed)
	}
	db, err := Open(filepath.Join(f.TempDir(), "index.db"), testLogger())
	if err != nil {
		f.Fatalf("Open: %v", err)
	}
	f.Cleanup(func() { db.Close() })
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 300 {
			data = data[:300]
		}
		ctx := context.Background()
		h := &itemHarness{t: t, db: db, ctx: ctx, data: data, clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		for i := 0; i < 2; i++ {
			s := &core.Space{Slug: "s" + core.NewID(), Name: "fuzz"}
			if err := db.CreateSpace(ctx, s); err != nil {
				t.Fatalf("CreateSpace: %v", err)
			}
			h.spaces = append(h.spaces, s.ID)
		}
		for steps := 0; steps < 40 && h.pos < len(h.data); steps++ {
			before := h.items()
			c0, err := db.MaxNoteRev(ctx)
			if err != nil {
				t.Fatalf("MaxNoteRev: %v", err)
			}
			desc, stepErr := h.step(ops)
			desc = fmt.Sprintf("step %d %s", steps, desc)
			after := h.items()
			c1, err := db.MaxNoteRev(ctx)
			if err != nil {
				t.Fatalf("MaxNoteRev: %v", err)
			}
			if revisions {
				h.checkRevisions(desc, stepErr, before, after, c0, c1)
			}
			if tree {
				h.checkTree(desc, after)
			}
		}
	})
}

// FuzzItemRevisions runs random create, update, move, trash, restore and
// delete sequences across folders, notes and files. After each step the
// revisions a write drew are the run right after the old counter, no two
// rows share a revision, a row never changes without a new revision, the
// feed from 0 returns every row once in revision order, and paging with
// random limits returns the same sequence.
func FuzzItemRevisions(f *testing.F) {
	runItemFuzz(f, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, true, true)
}

// FuzzTreePaths builds random folder trees and moves them. After each step
// every live item sits below the live folder its parent id names, that
// folder is the one at the parent directory, and no two live siblings have
// names equal under case folding.
func FuzzTreePaths(f *testing.F) {
	runItemFuzz(f, []int{0, 0, 0, 4, 4, 4, 4, 3, 5, 2, 1, 6, 7}, false, true)
}
