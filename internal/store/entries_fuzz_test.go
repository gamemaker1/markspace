package store

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// The stateful entry fuzz applies random operations to a real store and to an
// in-memory model and checks after every step that the two agree.

// modelNode is one entry of the model. A regular entry whose name ends in
// ".md" holds a note and every other regular entry holds file bytes.
type modelNode struct {
	dir    bool
	data   string
	noteID string
}

// modelTrash is one trashed item of the model. tree holds the entry at "" and
// its descendants at paths relative to it.
type modelTrash struct {
	id   string
	base string
	kind core.ItemKind
	from string
	tree map[string]*modelNode
}

type entryModel struct {
	spaces map[string]map[string]*modelNode
	trash  map[string]map[string]*modelTrash
}

// fuzzNames is the name pool. It holds case variants on purpose.
var fuzzNames = []string{"a", "A", "b.txt", "B.TXT", "n.md", "N.md", "dir", "Dir", "ä", "Ä", "c d.bin", "k.md"}

// fuzzSpaces are the two spaces a program works on.
var fuzzSpaces = []string{"one", "two"}

func (m *entryModel) exists(space, rel string) *modelNode { return m.spaces[space][rel] }

func (m *entryModel) isFolder(space, rel string) bool {
	if rel == "" {
		return true
	}
	n := m.exists(space, rel)
	return n != nil && n.dir
}

// taken reports whether a child of folder other than except equals name
// ignoring case.
func (m *entryModel) taken(space, folder, name, except string) bool {
	key := foldKey(name)
	for rel := range m.spaces[space] {
		if rel != except && parentRel(rel) == folder && foldKey(path.Base(rel)) == key {
			return true
		}
	}
	return false
}

// subtree returns rel and everything below it with paths relative to rel.
func (m *entryModel) subtree(space, rel string) map[string]*modelNode {
	out := map[string]*modelNode{}
	for p, n := range m.spaces[space] {
		switch {
		case p == rel:
			out[""] = n
		case strings.HasPrefix(p, rel+"/"):
			out[strings.TrimPrefix(p, rel+"/")] = n
		}
	}
	return out
}

func (m *entryModel) removeSubtree(space, rel string) {
	for p := range m.spaces[space] {
		if p == rel || strings.HasPrefix(p, rel+"/") {
			delete(m.spaces[space], p)
		}
	}
}

func (m *entryModel) place(space, rel string, tree map[string]*modelNode) {
	for p, n := range tree {
		m.spaces[space][under(rel, p)] = n
	}
}

// under joins a subtree path onto the path of its root, where "" is the root
// itself.
func under(root, p string) string {
	if p == "" {
		return root
	}
	return root + "/" + p
}

// folders lists the root and every folder of a space, sorted.
func (m *entryModel) folders(space string) []string {
	out := []string{""}
	for rel, n := range m.spaces[space] {
		if n.dir {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// entries lists every entry of a space, sorted.
func (m *entryModel) entries(space string) []string {
	var out []string
	for rel := range m.spaces[space] {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// programReader yields bytes of a fuzz program and reports exhaustion.
type programReader struct {
	data []byte
	pos  int
}

func (p *programReader) next() (byte, bool) {
	if p.pos >= len(p.data) {
		return 0, false
	}
	b := p.data[p.pos]
	p.pos++
	return b, true
}

func (p *programReader) pick(n int) (int, bool) {
	b, ok := p.next()
	if !ok || n == 0 {
		return 0, false
	}
	return int(b) % n, true
}

func FuzzEntryOps(f *testing.F) {
	f.Add([]byte{0, 0, 6, 1, 0, 2, 0, 0, 1, 2, 3, 2, 0, 1, 0, 0, 5, 4, 0, 0, 5, 0, 0, 0, 1})
	f.Add([]byte{0, 0, 0, 6, 1, 0, 1, 6, 0, 1, 0, 0, 0, 4, 3, 0, 0, 1, 1, 0, 0, 2, 0, 3, 0, 1, 7, 0})
	f.Add([]byte("move a folder into a case variant of itself and back again"))
	f.Add(bytes.Repeat([]byte{1, 0, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, 8))
	f.Fuzz(func(t *testing.T, program []byte) {
		runEntryProgram(t, program)
	})
}

// TestEntryOpsRandomPrograms runs generated programs on every test run, so
// the model check covers more than the fuzz seeds without -fuzz.
func TestEntryOpsRandomPrograms(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 13))
	runs := 150
	if testing.Short() {
		runs = 20
	}
	for i := 0; i < runs; i++ {
		program := make([]byte, 60+rng.IntN(240))
		for j := range program {
			program[j] = byte(rng.IntN(256))
		}
		t.Run(fmt.Sprint(i), func(t *testing.T) { runEntryProgram(t, program) })
	}
}

func runEntryProgram(t *testing.T, program []byte) {
	s, err := New(t.TempDir(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	m := &entryModel{spaces: map[string]map[string]*modelNode{}, trash: map[string]map[string]*modelTrash{}}
	for _, sp := range fuzzSpaces {
		if err := s.EnsureSpace(sp); err != nil {
			t.Fatal(err)
		}
		m.spaces[sp] = map[string]*modelNode{}
		m.trash[sp] = map[string]*modelTrash{}
	}
	p := &programReader{data: program}
	seq := 0
	for step := 0; step < 80; step++ {
		opByte, ok := p.next()
		if !ok {
			return
		}
		spIdx, ok := p.pick(len(fuzzSpaces))
		if !ok {
			return
		}
		space := fuzzSpaces[spIdx]
		seq++
		desc, done := applyEntryOp(t, s, m, p, int(opByte)%7, space, seq)
		if done {
			return
		}
		checkEntryModel(t, s, m, desc)
	}
}

// pickTarget chooses a folder and a name from the pool. Regular entries keep
// their ".md" status so the kind of the moved content never changes.
func pickTarget(p *programReader, m *entryModel, space string, sourceIsNote, sourceIsDir bool) (string, bool) {
	folders := m.folders(space)
	fi, ok := p.pick(len(folders) + 2)
	if !ok {
		return "", false
	}
	var names []string
	for _, n := range fuzzNames {
		isMD := strings.HasSuffix(n, mdExt)
		if (sourceIsDir && !isMD) || (!sourceIsDir && isMD == sourceIsNote) {
			names = append(names, n)
		}
	}
	ni, ok := p.pick(len(names))
	if !ok {
		return "", false
	}
	folder := ""
	switch {
	case fi < len(folders):
		folder = folders[fi]
	default:
		// A folder that may not exist yet.
		folder = joinRel(folders[fi%len(folders)], fuzzNames[(fi+ni)%len(fuzzNames)])
	}
	return joinRel(folder, names[ni]), true
}

// applyEntryOp runs one operation on the store and the model and compares
// the error. It returns a description of the step, and true when the
// program ran out of bytes.
func applyEntryOp(t *testing.T, s *Store, m *entryModel, p *programReader, op int, space string, seq int) (string, bool) {
	t.Helper()
	check := func(desc string, got, want error) {
		t.Helper()
		if want == nil && got != nil || want != nil && !errors.Is(got, want) {
			t.Fatalf("%s: error = %v, want %v", desc, got, want)
		}
	}
	switch op {
	case 0, 1: // MakeDir or SaveFile
		isDir := op == 0
		ni, ok := p.pick(len(fuzzNames))
		if !ok {
			return "", true
		}
		name := fuzzNames[ni]
		if isDir && strings.HasSuffix(name, mdExt) {
			name = strings.TrimSuffix(name, mdExt)
		}
		folders := m.folders(space)
		fi, ok := p.pick(len(folders) + 1)
		if !ok {
			return "", true
		}
		folder := "missing"
		if fi < len(folders) {
			folder = folders[fi]
		}
		rel := joinRel(folder, name)
		var want error
		switch {
		case !m.isFolder(space, folder):
			want = core.ErrNotFound
		case m.taken(space, folder, name, ""):
			want = core.ErrExists
		}
		if isDir {
			desc := fmt.Sprintf("MakeDir(%s, %q)", space, rel)
			check(desc, s.MakeDir(space, rel), want)
			if want == nil {
				m.spaces[space][rel] = &modelNode{dir: true}
			}
			return desc, false
		}
		node := &modelNode{data: fmt.Sprintf("file %d\n", seq)}
		content := node.data
		if strings.HasSuffix(name, mdExt) {
			node.noteID = core.NewID()
			node.data = fmt.Sprintf("body %d\n", seq)
			content = noteText(t, node.noteID, node.data)
		}
		desc := fmt.Sprintf("SaveFile(%s, %q)", space, rel)
		_, err := s.SaveFile(space, rel, strings.NewReader(content), 1<<20)
		check(desc, err, want)
		if want == nil {
			m.spaces[space][rel] = node
		}
		return desc, false

	case 2, 3, 4, 6: // MoveEntry, CopyEntry, TrashEntry, RemoveEntry
		all := m.entries(space)
		si, ok := p.pick(len(all) + 1)
		if !ok {
			return "", true
		}
		src := "nope"
		var srcNode *modelNode
		if si < len(all) {
			src = all[si]
			srcNode = m.exists(space, src)
		}
		isDir := srcNode != nil && srcNode.dir
		isNote := strings.HasSuffix(src, mdExt)
		switch op {
		case 2:
			dst, ok := pickTarget(p, m, space, isNote, isDir)
			if !ok {
				return "", true
			}
			desc := fmt.Sprintf("MoveEntry(%s, %q, %q)", space, src, dst)
			want, create := m.predictMove(space, src, dst, srcNode)
			check(desc, s.MoveEntry(space, src, dst), want)
			if want == nil && src != dst {
				for _, dir := range create {
					m.spaces[space][dir] = &modelNode{dir: true}
				}
				tree := m.subtree(space, src)
				m.removeSubtree(space, src)
				m.place(space, dst, tree)
			}
			return desc, false
		case 3:
			di, ok := p.pick(len(fuzzSpaces))
			if !ok {
				return "", true
			}
			dstSpace := fuzzSpaces[di]
			dst, ok := pickTarget(p, m, dstSpace, isNote, isDir)
			if !ok {
				return "", true
			}
			desc := fmt.Sprintf("CopyEntry(%s, %q, %s, %q)", space, src, dstSpace, dst)
			var want error
			dstFolder := parentRel(dst)
			switch {
			case srcNode == nil:
				want = core.ErrNotFound
			case space == dstSpace && isDir && (strings.EqualFold(dst, src) || foldHasPrefix(dst, src+"/")):
				want = core.ErrInvalid
			case !m.isFolder(dstSpace, dstFolder):
				want = core.ErrNotFound
			case m.taken(dstSpace, dstFolder, path.Base(dst), ""):
				want = core.ErrExists
			}
			copied, err := s.CopyEntry(space, src, dstSpace, dst, func(string) string { return core.NewID() })
			check(desc, err, want)
			if want != nil {
				return desc, false
			}
			tree := m.subtree(space, src)
			newIDs := map[string]string{}
			for _, c := range copied {
				if c.Kind == core.KindNote {
					if c.NewID == "" || c.NewID == c.OldID {
						t.Fatalf("%s: note %q kept id %q", desc, c.OldRel, c.OldID)
					}
					newIDs[c.NewRel] = c.NewID
				}
			}
			if len(copied) != len(tree) {
				t.Fatalf("%s: copied %d entries, model has %d", desc, len(copied), len(tree))
			}
			for rel, n := range tree {
				cp := *n
				if n.noteID != "" {
					id, ok := newIDs[under(dst, rel)]
					if !ok {
						t.Fatalf("%s: no new id for %q", desc, under(dst, rel))
					}
					cp.noteID = id
				}
				m.spaces[dstSpace][under(dst, rel)] = &cp
			}
			return desc, false
		case 4:
			id := core.NewID()
			desc := fmt.Sprintf("TrashEntry(%s, %q, %s)", space, src, id)
			trashRel, err := s.TrashEntry(space, src, id, "fuzzer")
			if srcNode == nil {
				check(desc, err, core.ErrNotFound)
				return desc, false
			}
			check(desc, err, nil)
			kind := core.KindFile
			switch {
			case isDir:
				kind = core.KindFolder
			case isNote:
				kind = core.KindNote
			}
			if trashRel != trashDirName+"/"+id+"/"+path.Base(src) {
				t.Fatalf("%s: trash path %q", desc, trashRel)
			}
			m.trash[space][id] = &modelTrash{id: id, base: path.Base(src), kind: kind, from: src, tree: m.subtree(space, src)}
			m.removeSubtree(space, src)
			return desc, false
		default:
			desc := fmt.Sprintf("RemoveEntry(%s, %q)", space, src)
			var want error
			if srcNode == nil {
				want = core.ErrNotFound
			}
			check(desc, s.RemoveEntry(space, src), want)
			if want == nil {
				m.removeSubtree(space, src)
			}
			return desc, false
		}

	default: // RestoreEntry
		var ids []string
		for id := range m.trash[space] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		ti, ok := p.pick(len(ids) + 1)
		if !ok {
			return "", true
		}
		if ti == len(ids) {
			desc := fmt.Sprintf("RestoreEntry(%s, missing)", space)
			check(desc, s.RestoreEntry(space, ".trash/"+core.NewID()+"/x", "x"), core.ErrNotFound)
			return desc, false
		}
		item := m.trash[space][ids[ti]]
		target, ok := pickTarget(p, m, space, item.kind == core.KindNote, item.kind == core.KindFolder)
		if !ok {
			return "", true
		}
		desc := fmt.Sprintf("RestoreEntry(%s, %s/%s, %q)", space, item.id, item.base, target)
		var want error
		folder := parentRel(target)
		switch {
		case !m.isFolder(space, folder):
			want = core.ErrNotFound
		case m.taken(space, folder, path.Base(target), ""):
			want = core.ErrExists
		}
		check(desc, s.RestoreEntry(space, trashDirName+"/"+item.id+"/"+item.base, target), want)
		if want == nil {
			m.place(space, target, item.tree)
			delete(m.trash[space], item.id)
		}
		return desc, false
	}
}

// predictMove mirrors the checks of MoveEntry and returns the expected error
// and the folders the move creates.
func (m *entryModel) predictMove(space, src, dst string, srcNode *modelNode) (error, []string) {
	if srcNode == nil {
		return core.ErrNotFound, nil
	}
	if src == dst {
		return nil, nil
	}
	if srcNode.dir && foldHasPrefix(dst, src+"/") {
		return core.ErrInvalid, nil
	}
	var create []string
	cur := ""
	if folder := parentRel(dst); folder != "" {
		for _, seg := range strings.Split(folder, "/") {
			next := joinRel(cur, seg)
			if len(create) > 0 {
				create = append(create, next)
				cur = next
				continue
			}
			switch n := m.exists(space, next); {
			case n != nil && n.dir:
			case n != nil:
				return core.ErrExists, nil
			default:
				if m.taken(space, cur, seg, "") {
					return core.ErrExists, nil
				}
				create = append(create, next)
			}
			cur = next
		}
	}
	if len(create) == 0 && m.taken(space, parentRel(dst), path.Base(dst), src) {
		return core.ErrExists, nil
	}
	return nil, create
}

// checkEntryModel compares the disk with the model after one step.
func checkEntryModel(t *testing.T, s *Store, m *entryModel, desc string) {
	t.Helper()
	for _, space := range fuzzSpaces {
		root := filepath.Join(s.SpacesDir(), space)
		disk := readDiskTree(t, root, true)
		compareTrees(t, desc, space, disk, m.spaces[space])
		assertNoCaseCollisions(t, root)

		listed, err := s.ScanTrashEntries(space)
		if err != nil {
			t.Fatalf("%s: ScanTrashEntries: %v", desc, err)
		}
		if len(listed) != len(m.trash[space]) {
			t.Fatalf("%s: trash of %s lists %d items, model has %d", desc, space, len(listed), len(m.trash[space]))
		}
		for _, e := range listed {
			item, ok := m.trash[space][e.ID]
			if !ok || e.Kind != item.kind || e.TrashedFrom != item.from || e.DeletedBy != "fuzzer" {
				t.Fatalf("%s: trash entry %+v, model %+v", desc, e, item)
			}
			slot := readDiskTree(t, filepath.Join(root, trashDirName, e.ID, item.base), false)
			compareTrees(t, desc, space+" trash "+e.ID, slot, item.tree)
		}
	}
	_ = filepath.WalkDir(s.DataDir(), func(full string, d fs.DirEntry, err error) error {
		if err == nil && strings.Contains(d.Name(), ".tmp-") {
			t.Fatalf("%s: temporary entry left behind: %s", desc, full)
		}
		return nil
	})
}

// readDiskTree reads an entry tree. With skipTrash it reads a space root and
// leaves out the trash. Otherwise root is one entry, stored at "".
func readDiskTree(t *testing.T, root string, skipTrash bool) map[string]*modelNode {
	t.Helper()
	out := map[string]*modelNode{}
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, full)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			if skipTrash {
				return nil
			}
			rel = ""
		}
		if skipTrash && rel == trashDirName {
			return fs.SkipDir
		}
		if d.IsDir() {
			out[rel] = &modelNode{dir: true}
			return nil
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		node := &modelNode{data: string(data)}
		if strings.HasSuffix(d.Name(), mdExt) {
			note, _, err := parseNote(data, "x.md", time.Time{})
			if err != nil {
				t.Fatalf("unparseable note %s: %v", full, err)
			}
			node.data = note.Content
			node.noteID = note.ID
		}
		out[rel] = node
		return nil
	})
	if err != nil {
		t.Fatalf("read tree %s: %v", root, err)
	}
	return out
}

func compareTrees(t *testing.T, desc, where string, disk, model map[string]*modelNode) {
	t.Helper()
	for rel, n := range model {
		d, ok := disk[rel]
		if !ok {
			t.Fatalf("%s: %s: %q in model, missing on disk", desc, where, rel)
		}
		if d.dir != n.dir || d.data != n.data || d.noteID != n.noteID {
			t.Fatalf("%s: %s: %q on disk %+v, model %+v", desc, where, rel, *d, *n)
		}
	}
	for rel := range disk {
		if _, ok := model[rel]; !ok {
			t.Fatalf("%s: %s: %q on disk, missing in model", desc, where, rel)
		}
	}
}
