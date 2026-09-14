package app

// FuzzItemOperations drives random folder, note and file operations through
// the application over the real store and index, plus renames made directly
// on disk followed by a reconcile. After every step it checks the App
// invariants of docs/files.md "Testing": indexed items exist on disk, a reconcile
// finds nothing to change, ids are unique, parent chains end at the root, no
// two live siblings share a name ignoring case, and every link that resolved
// before a move or rename resolves to the same item after it.

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/links"
	"github.com/gamemaker1/markspace/internal/ports"
)

var (
	fzFolderNames = []string{"Docs", "docs", "Projects", "img", "a b", "Plan"}
	fzFileNames   = []string{"east wall.png", "report.pdf", "Report.PDF", "notes.txt", "x.png", "plan.txt"}
	fzTitles      = []string{"Harbour survey", "harbour-survey", "Plan", "Notes", "Index", "a b"}
	fzContents    = []string{"one", "two", "one", "\x89PNG\r\n\x1a\nrest", "%PDF-1.4\n%%EOF"}
)

// fzHarness is newRealHarness in a data directory whose path holds no "#".
// Fuzz seed names contain "#", which t.TempDir keeps, and the index opens its
// database through a file URI where "#" starts a fragment, so every seed
// would share one database.
func fzHarness(t *testing.T) *rharness {
	dir, err := os.MkdirTemp("", "fzitems")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return newRealHarness(&fzTB{T: t, dir: dir})
}

// fzTB hands newRealHarness a temporary directory without "#".
type fzTB struct {
	*testing.T
	dir string
	n   int
}

func (tb *fzTB) TempDir() string {
	tb.n++
	d := fmt.Sprintf("%s/h%d", tb.dir, tb.n)
	if err := os.MkdirAll(d, 0o755); err != nil {
		tb.Fatal(err)
	}
	return d
}

// fzWorld is the state of one fuzz run.
type fzWorld struct {
	t      *testing.T
	h      *rharness
	owner  *core.Actor
	spaces []*core.Space
	src    *byteSource
	log    []string
}

func FuzzItemOperations(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1, 0, 0, 1, 1, 0, 2, 0, 0, 0, 0, 3, 13, 0, 1, 5, 0, 0, 1})
	f.Add([]byte{1, 0, 2, 2, 0, 1, 1, 0, 13, 0, 3, 11, 0, 0, 1, 12, 0, 0, 2})
	f.Add([]byte("rename move copy trash restore delete external"))
	seq := make([]byte, 160)
	for i := range seq {
		seq[i] = byte(i*37 + 11)
	}
	f.Add(seq)
	f.Fuzz(func(t *testing.T, data []byte) { fzRun(t, data) })
}

// fzMaxItems caps the live items of a space, so repeated folder copies do
// not grow a run without bound.
const fzMaxItems = 40

// fzRun performs the operations encoded in data and checks the invariants
// after each one.
func fzRun(t *testing.T, data []byte) *fzWorld {
	if len(data) > 240 {
		data = data[:240]
	}
	h := fzHarness(t)
	owner := h.user("Owner", false)
	w := &fzWorld{t: t, h: h, owner: owner, src: &byteSource{data: data}}
	w.spaces = []*core.Space{h.space(owner, "Main"), h.space(owner, "Second")}
	for step := 0; step < 20 && w.src.pos < len(w.src.data); step++ {
		w.step()
		for _, sp := range w.spaces {
			w.checkSpace(sp)
		}
		w.checkIDs()
		h.clock.Advance(time.Second)
	}
	return w
}

// TestItemOperationsDoWork runs fixed pseudo-random sequences through the
// fuzz model and checks that renames, moves, copies, replaces, restores and
// external renames succeed often enough for the fuzz to test them.
func TestItemOperationsDoWork(t *testing.T) {
	counts := map[string]int{}
	state := uint32(7)
	for run := 0; run < 12; run++ {
		data := make([]byte, 160)
		for i := range data {
			state = state*1664525 + 1013904223
			data[i] = byte(state >> 24)
		}
		w := fzRun(t, data)
		for _, line := range w.log {
			if strings.HasPrefix(line, "external rename") {
				counts["external"]++
				continue
			}
			if !strings.HasSuffix(line, "-> <nil>") {
				continue
			}
			op, _, _ := strings.Cut(line, " ")
			counts[op]++
		}
	}
	for _, op := range []string{"create", "upload", "rename", "retitle", "move", "copy", "replace", "trash", "restore", "write", "external"} {
		if counts[op] == 0 {
			t.Errorf("no successful %s in the fixed runs: %v", op, counts)
		}
	}
}

// fail stops the run with the log of steps taken.
func (w *fzWorld) fail(format string, args ...any) {
	w.t.Helper()
	w.t.Fatalf("%s\nsteps:\n  %s", fmt.Sprintf(format, args...), strings.Join(w.log, "\n  "))
}

// allowed checks that an operation error is one the rules permit.
func (w *fzWorld) allowed(what string, err error) {
	w.t.Helper()
	w.log = append(w.log, fmt.Sprintf("%s -> %v", what, err))
	if err == nil {
		return
	}
	for _, ok := range []error{core.ErrExists, core.ErrInvalid, core.ErrConflict, core.ErrNotFound, core.ErrUnsupported} {
		if errors.Is(err, ok) {
			return
		}
	}
	w.fail("%s: unexpected error %v", what, err)
}

func (w *fzWorld) pick(list []string) string { return list[w.src.intn(len(list))] }

// rows returns the live rows of a space.
func (w *fzWorld) rows(sp *core.Space) *ports.TreeRows {
	w.t.Helper()
	rows, err := w.h.db.TreeItems(w.h.ctx, sp.ID)
	if err != nil {
		w.fail("tree items: %v", err)
	}
	return rows
}

// liveItems lists the live items of a space, optionally of some kinds.
func (w *fzWorld) liveItems(sp *core.Space, kinds ...core.ItemKind) []*core.Item {
	rows := w.rows(sp)
	want := func(k core.ItemKind) bool {
		if len(kinds) == 0 {
			return true
		}
		for _, x := range kinds {
			if x == k {
				return true
			}
		}
		return false
	}
	var out []*core.Item
	if want(core.KindFolder) {
		for _, f := range rows.Folders {
			out = append(out, &core.Item{Kind: core.KindFolder, Folder: f})
		}
	}
	if want(core.KindNote) {
		for _, n := range rows.Notes {
			out = append(out, &core.Item{Kind: core.KindNote, Note: n})
		}
	}
	if want(core.KindFile) {
		for _, f := range rows.Files {
			out = append(out, &core.Item{Kind: core.KindFile, File: f})
		}
	}
	return out
}

func (w *fzWorld) pickItem(sp *core.Space, kinds ...core.ItemKind) *core.Item {
	items := w.liveItems(sp, kinds...)
	if len(items) == 0 {
		return nil
	}
	return items[w.src.intn(len(items))]
}

// pickParent returns a live folder id of the space or "" for the root.
func (w *fzWorld) pickParent(sp *core.Space) string {
	folders := w.rows(sp).Folders
	i := w.src.intn(len(folders) + 1)
	if i == len(folders) {
		return ""
	}
	return folders[i].ID
}

// fzLinks records what every link of every live note resolved to.
type fzLinks map[string][]string

// linkState parses every live note body and resolves each link against the
// index snapshot. An unresolved link records "".
func (w *fzWorld) linkState(sp *core.Space) fzLinks {
	w.t.Helper()
	snap, rows, err := w.h.app.loadSnapshot(w.h.ctx, sp.ID)
	if err != nil {
		w.fail("snapshot: %v", err)
	}
	out := fzLinks{}
	for _, n := range rows.Notes {
		disk, err := w.h.files.Read(sp.Slug, n.Path)
		if err != nil {
			w.fail("read note %s: %v", n.Path, err)
		}
		var ids []string
		for _, l := range links.Parse(disk.Content) {
			if l.External {
				continue
			}
			id := ""
			if e := snap.Resolve(n.Path, l); e != nil {
				id = e.ID
			}
			ids = append(ids, id)
		}
		out[n.ID] = ids
	}
	return out
}

// compareLinks checks that each link that resolved before still resolves to
// the same item, when both the note and the target are still live. ignore
// names an item created by the operation, which may take over a name.
func (w *fzWorld) compareLinks(sp *core.Space, before fzLinks, what, ignore string) {
	w.t.Helper()
	after := w.linkState(sp)
	live := map[string]bool{}
	for _, it := range w.liveItems(sp) {
		live[it.ID()] = true
	}
	for noteID, old := range before {
		now, ok := after[noteID]
		if !ok {
			continue
		}
		if len(now) != len(old) {
			w.fail("%s: note %s had %d links, now %d", what, noteID, len(old), len(now))
		}
		for i, id := range old {
			if id == "" || !live[id] || now[i] == ignore && ignore != "" {
				continue
			}
			if now[i] != id {
				w.fail("%s: note %s link %d resolved to %s, now %q", what, noteID, i, id, now[i])
			}
		}
	}
}

// fzRel returns the relative path from folder dir to entry path p.
func fzRel(dir, p string) string {
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(s, "/")
	}
	ds, ps := split(dir), split(p)
	c := 0
	for c < len(ds) && c < len(ps) && ds[c] == ps[c] {
		c++
	}
	var parts []string
	for range ds[c:] {
		parts = append(parts, "..")
	}
	return strings.Join(append(parts, ps[c:]...), "/")
}

// body builds a note body with links to items of the space, as seen from a
// note at notePath.
func (w *fzWorld) body(sp *core.Space, notePath string) string {
	items := w.liveItems(sp)
	var b strings.Builder
	b.WriteString("Links:\n\n")
	n := w.src.intn(5)
	for i := 0; i < n; i++ {
		b.WriteString("- ")
		if len(items) == 0 || w.src.intn(8) == 0 {
			b.WriteString("[[missing thing]]\n")
			continue
		}
		it := items[w.src.intn(len(items))]
		p := it.Path()
		name := baseOf(p)
		switch w.src.intn(5) {
		case 0:
			switch {
			case it.File != nil:
				fmt.Fprintf(&b, "[[%s]]", name)
			case it.Note != nil:
				fmt.Fprintf(&b, "[[%s]]", it.Note.Title)
			default:
				fmt.Fprintf(&b, "[[%s]]", p)
			}
		case 1:
			fmt.Fprintf(&b, "[[%s|label]]", strings.TrimSuffix(p, ".md"))
		case 2:
			fmt.Fprintf(&b, "[text](<%s>)", fzRel(dirOf(notePath), p))
		case 3:
			if it.File != nil {
				fmt.Fprintf(&b, "![img](<%s>)", fzRel(dirOf(notePath), p))
			} else {
				fmt.Fprintf(&b, "[root](</%s>)", p)
			}
		default:
			if it.Folder != nil {
				fmt.Fprintf(&b, "[[%s]]", p)
			} else {
				fmt.Fprintf(&b, "![[%s]]", name)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// step performs one random operation.
func (w *fzWorld) step() {
	h, ctx, owner := w.h, w.h.ctx, w.owner
	sp := w.spaces[0]
	if w.src.intn(6) == 0 {
		sp = w.spaces[1]
	}
	op := w.src.intn(15)
	if op <= 2 && len(w.liveItems(sp)) > fzMaxItems {
		op = 9
	}
	switch op {
	case 0: // create a note at the root, in a folder, or inside a note
		before := w.linkState(sp)
		parent := ""
		converted := false
		switch w.src.intn(3) {
		case 1:
			parent = w.pickParent(sp)
		case 2:
			if it := w.pickItem(sp, core.KindNote); it != nil {
				parent, converted = it.ID(), true
			}
		}
		n, err := h.app.CreateNote(ctx, owner, core.NoteInput{SpaceID: sp.ID, ParentID: parent, Title: w.pick(fzTitles)})
		w.allowed("create note in "+parent, err)
		if converted && err == nil {
			w.compareLinks(sp, before, "convert to folder note", n.ID)
		}
	case 1:
		_, err := h.app.CreateFolder(ctx, owner, core.FolderInput{SpaceID: sp.ID, ParentID: w.pickParent(sp), Name: w.pick(fzFolderNames)})
		w.allowed("create folder", err)
	case 2:
		conflict := []string{"", core.OnConflictRename, core.OnConflictReplace}[w.src.intn(3)]
		name := w.pick(fzFileNames)
		_, err := h.app.UploadFile(ctx, owner, core.FileUpload{SpaceID: sp.ID, ParentID: w.pickParent(sp), Name: name, OnConflict: conflict},
			strings.NewReader(w.pick(fzContents)))
		w.allowed("upload "+name+" "+conflict, err)
	case 3:
		it := w.pickItem(sp, core.KindFolder, core.KindFile)
		if it == nil {
			return
		}
		name := w.pick(fzFolderNames)
		if it.File != nil {
			name = w.pick(fzFileNames)
		}
		before := w.linkState(sp)
		var err error
		if it.Folder != nil {
			_, err = h.app.RenameFolder(ctx, owner, it.ID(), name)
		} else {
			_, err = h.app.RenameFile(ctx, owner, it.ID(), name)
		}
		w.allowed("rename "+it.Path()+" to "+name, err)
		w.compareLinks(sp, before, "rename "+it.Path(), "")
	case 4:
		it := w.pickItem(sp, core.KindNote)
		if it == nil {
			return
		}
		before := w.linkState(sp)
		title := w.pick(fzTitles)
		_, err := h.app.UpdateNote(ctx, owner, it.ID(), core.NoteUpdate{Title: &title})
		w.allowed("retitle "+it.Path()+" to "+title, err)
		w.compareLinks(sp, before, "retitle "+it.Path(), "")
	case 5, 6:
		it := w.pickItem(sp)
		if it == nil {
			return
		}
		parent := w.pickParent(sp)
		before := w.linkState(sp)
		_, err := h.app.MoveItem(ctx, owner, it.ID(), parent, w.src.intn(3)*100)
		w.allowed("move "+it.Path()+" into "+parent, err)
		w.compareLinks(sp, before, "move "+it.Path(), "")
	case 7:
		it := w.pickItem(sp)
		if it == nil || len(w.liveItems(w.spaces[0]))+len(w.liveItems(w.spaces[1])) > 2*fzMaxItems {
			return
		}
		dst := w.spaces[w.src.intn(2)]
		_, err := h.app.CopyItem(ctx, owner, it.ID(), core.CopyInput{SpaceID: dst.ID, ParentID: w.pickParent(dst)})
		w.allowed("copy "+it.Path()+" to "+dst.Slug, err)
	case 8:
		it := w.pickItem(sp, core.KindFile)
		if it == nil {
			return
		}
		base := "0000000000000000000000000000000000000000000000000000000000000000"
		if w.src.intn(3) > 0 {
			sha, err := h.files.HashFile(sp.Slug, it.Path())
			if err != nil {
				w.fail("hash %s: %v", it.Path(), err)
			}
			base = sha
		}
		before := w.linkState(sp)
		_, err := h.app.ReplaceFile(ctx, owner, it.ID(), core.FileReplace{Name: w.pick(fzFileNames), BaseSHA256: base},
			strings.NewReader(w.pick(fzContents)))
		if base[0] == '0' && base == strings.Repeat("0", 64) && !errors.Is(err, core.ErrConflict) {
			w.fail("replace %s with a stale hash: err = %v, want conflict", it.Path(), err)
		}
		w.allowed("replace "+it.Path(), err)
		w.compareLinks(sp, before, "replace "+it.Path(), "")
	case 9:
		if it := w.pickItem(sp); it != nil {
			w.allowed("trash "+it.Path(), h.app.TrashItem(ctx, owner, it.ID()))
		}
	case 10:
		trash, err := h.app.ListTrashItems(ctx, owner, sp.ID)
		if err != nil {
			w.fail("list trash: %v", err)
		}
		if len(trash) == 0 {
			return
		}
		id := treeID(trash[w.src.intn(len(trash))])
		_, err = h.app.RestoreItem(ctx, owner, id)
		w.allowed("restore "+id, err)
	case 11:
		if w.src.intn(2) == 0 {
			if it := w.pickItem(sp); it != nil {
				w.allowed("delete "+it.Path(), h.app.DeleteItem(ctx, owner, it.ID()))
			}
			return
		}
		trash, err := h.app.ListTrashItems(ctx, owner, sp.ID)
		if err != nil || len(trash) == 0 {
			return
		}
		id := treeID(trash[w.src.intn(len(trash))])
		w.allowed("delete trashed "+id, h.app.DeleteItem(ctx, owner, id))
	case 12, 13:
		it := w.pickItem(sp, core.KindNote)
		if it == nil {
			return
		}
		content := w.body(sp, it.Path())
		_, err := h.app.UpdateNote(ctx, owner, it.ID(), core.NoteUpdate{Content: &content})
		w.allowed("write links into "+it.Path(), err)
	default:
		w.external(sp)
	}
}

// external renames or moves one entry directly on disk, then reconciles.
func (w *fzWorld) external(sp *core.Space) {
	h := w.h
	w.h.app.drainFileQueues(h.ctx)
	rows := w.rows(sp)
	var (
		from    string
		isDir   bool
		name    string
		targets []string
	)
	switch w.src.intn(3) {
	case 0:
		if len(rows.Files) == 0 {
			return
		}
		f := rows.Files[w.src.intn(len(rows.Files))]
		from, name = f.Path, w.pick(append([]string{f.Name}, fzFileNames...))
	case 1:
		if len(rows.Notes) == 0 {
			return
		}
		n := rows.Notes[w.src.intn(len(rows.Notes))]
		from, name = n.Path, baseOf(n.Path)
	default:
		var cands []*core.Folder
		for _, f := range rows.Folders {
			known := false
			for _, n := range rows.Notes {
				known = known || underPath(n.Path, f.Path)
			}
			for _, x := range rows.Files {
				known = known || underPath(x.Path, f.Path)
			}
			if known {
				cands = append(cands, f)
			}
		}
		if len(cands) == 0 {
			return
		}
		f := cands[w.src.intn(len(cands))]
		from, name, isDir = f.Path, w.pick(append([]string{f.Name}, fzFolderNames...)), true
	}
	targets = append(targets, "")
	for _, f := range rows.Folders {
		if !isDir || (f.Path != from && !underPath(f.Path, from)) {
			targets = append(targets, f.Path)
		}
	}
	dir := targets[w.src.intn(len(targets))]
	to := joinRel(dir, name)
	if to == from {
		return
	}
	if _, taken := h.files.CaseCollision(sp.Slug, dir, name, from); taken {
		return
	}
	before := w.linkState(sp)
	w.log = append(w.log, "external rename "+from+" to "+to)
	h.renameDisk(sp, from, to)
	if _, _, err := h.app.reconcileSpace(h.ctx, sp, false); err != nil {
		w.fail("reconcile after external rename: %v", err)
	}
	w.compareLinks(sp, before, "external rename "+from+" to "+to, "")
}

// fzRow is the identity of one live row.
type fzRow struct {
	kind   core.ItemKind
	path   string
	parent string
}

func (w *fzWorld) rowMap(sp *core.Space) map[string]fzRow {
	rows := w.rows(sp)
	out := map[string]fzRow{}
	for _, f := range rows.Folders {
		out[f.ID] = fzRow{core.KindFolder, f.Path, f.ParentID}
	}
	for _, n := range rows.Notes {
		out[n.ID] = fzRow{core.KindNote, n.Path, n.ParentID}
	}
	for _, f := range rows.Files {
		out[f.ID] = fzRow{core.KindFile, f.Path, f.ParentID}
	}
	return out
}

// checkSpace verifies the disk and index invariants of one space.
func (w *fzWorld) checkSpace(sp *core.Space) {
	w.t.Helper()
	h := w.h
	before := w.rowMap(sp)

	// Every indexed live item exists on disk with its kind.
	folderIDs := map[string]string{}
	for id, r := range before {
		if r.kind == core.KindFolder {
			folderIDs[r.path] = id
		}
	}
	for id, r := range before {
		info, err := h.files.StatEntry(sp.Slug, r.path)
		if err != nil {
			w.fail("%s %s at %s is not on disk: %v", r.kind, id, r.path, err)
		}
		if info.IsDir() != (r.kind == core.KindFolder) {
			w.fail("%s %s at %s has the wrong kind on disk", r.kind, id, r.path)
		}
		if r.kind == core.KindNote {
			n, err := h.files.Read(sp.Slug, r.path)
			if err != nil || n.ID != id {
				w.fail("note file %s holds id %v, want %s (%v)", r.path, n, id, err)
			}
		}
		// The parent id names the folder at the parent path, so parent
		// chains end at the root.
		if want := folderIDs[dirOf(r.path)]; r.parent != want {
			w.fail("%s %s at %s has parent %q, want %q", r.kind, id, r.path, r.parent, want)
		}
		cur, hops := r, 0
		for cur.parent != "" {
			next, ok := before[cur.parent]
			if !ok || next.kind != core.KindFolder || hops > 64 {
				w.fail("parent chain of %s breaks at %q", id, cur.parent)
			}
			cur, hops = next, hops+1
		}
	}

	// No two live siblings share a name ignoring case, in the index.
	seen := map[string]string{}
	for id, r := range before {
		key := strings.ToLower(r.path)
		if other, dup := seen[key]; dup {
			w.fail("items %s and %s share the path %s ignoring case", id, other, r.path)
		}
		seen[key] = id
	}

	// Every entry on disk is indexed, and a reconcile changes nothing.
	scan, err := h.files.ScanTree(sp.Slug)
	if err != nil {
		w.fail("scan: %v", err)
	}
	var diskPaths []string
	for _, f := range scan.Folders {
		diskPaths = append(diskPaths, f.RelPath)
		if id, ok := folderIDs[f.RelPath]; !ok || before[id].kind != core.KindFolder {
			w.fail("folder %s on disk is not indexed", f.RelPath)
		}
	}
	for _, n := range scan.Notes {
		diskPaths = append(diskPaths, n.Note.Path)
		if r, ok := before[n.Note.ID]; !ok || r.path != n.Note.Path {
			w.fail("note %s (%s) on disk is not indexed there", n.Note.Path, n.Note.ID)
		}
	}
	files := map[string]bool{}
	for _, r := range before {
		if r.kind == core.KindFile {
			files[r.path] = true
		}
	}
	for _, f := range scan.Files {
		diskPaths = append(diskPaths, f.RelPath)
		if !files[f.RelPath] {
			w.fail("file %s on disk is not indexed", f.RelPath)
		}
	}
	if len(diskPaths) != len(before) {
		sort.Strings(diskPaths)
		w.fail("disk holds %d entries, index %d live items: %v", len(diskPaths), len(before), diskPaths)
	}
	lower := map[string]string{}
	for _, p := range diskPaths {
		if other, dup := lower[strings.ToLower(p)]; dup {
			w.fail("disk entries %s and %s differ only by case", p, other)
		}
		lower[strings.ToLower(p)] = p
	}
	if _, _, err := h.app.reconcileSpace(h.ctx, sp, false); err != nil {
		w.fail("reconcile: %v", err)
	}
	after := w.rowMap(sp)
	if len(after) != len(before) {
		w.fail("reconcile changed the live item count from %d to %d", len(before), len(after))
	}
	for id, r := range before {
		if after[id] != r {
			w.fail("reconcile changed %s from %+v to %+v", id, r, after[id])
		}
	}
}

// checkIDs verifies that no id repeats across kinds and spaces, trashed
// items included.
func (w *fzWorld) checkIDs() {
	w.t.Helper()
	ctx := w.h.ctx
	seen := map[string]string{}
	add := func(id, what string) {
		if prev, dup := seen[id]; dup {
			w.fail("id %s is used by %s and %s", id, prev, what)
		}
		seen[id] = what
	}
	for _, sp := range w.spaces {
		folders, err := w.h.db.ListFolders(ctx, sp.ID, true)
		if err != nil {
			w.fail("list folders: %v", err)
		}
		for _, f := range folders {
			add(f.ID, "folder "+sp.Slug+"/"+f.Path)
		}
		notes, err := w.h.db.ListNotes(ctx, sp.ID, core.NoteListOptions{IncludeTrash: true})
		if err != nil {
			w.fail("list notes: %v", err)
		}
		for _, n := range notes {
			add(n.ID, "note "+sp.Slug+"/"+n.Path)
		}
		files, err := w.h.db.ListFiles(ctx, sp.ID, ports.FileListOptions{IncludeDeleted: true})
		if err != nil {
			w.fail("list files: %v", err)
		}
		for _, f := range files {
			add(f.ID, "file "+sp.Slug+"/"+f.Path)
		}
	}
}
