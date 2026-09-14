package store

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// noteText serializes a small note with the given id and body.
func noteText(t *testing.T, id, body string) string {
	t.Helper()
	data, err := serializeNote(&core.Note{ID: id, Title: "T " + id, Content: body})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// assertNoTemp fails when any temporary name is left below dir.
func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(full string, d os.DirEntry, err error) error {
		if err == nil && strings.Contains(d.Name(), ".tmp-") {
			t.Errorf("temporary entry left behind: %s", full)
		}
		return nil
	})
}

func TestScanTree(t *testing.T) {
	s := newTestStore(t)
	root := filepath.Join(s.SpacesDir(), "docs")
	mustMkdir(t, s, "docs", "Projects")
	mustMkdir(t, s, "docs", "Projects/Empty")
	mustMkdir(t, s, "docs", "attachments")
	mustSave(t, s, "docs", "Projects/index.md", noteText(t, "n1", "folder note\n"))
	mustSave(t, s, "docs", "top.md", "---\nid: n2\nparent: n9\n---\nbody\n")
	mustSave(t, s, "docs", "Projects/east wall.jpg", "JPEGDATA")
	mustSave(t, s, "docs", "attachments/x.bin", "\x00\x01")
	mustSave(t, s, "docs", "README", "plain text here\n")
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(content), fileMode); err != nil {
			t.Fatal(err)
		}
	}
	write("broken.md", "---\n: [unclosed\n---\nbody\n")
	write(".hidden.md", noteText(t, "h", "x"))
	write("a.md.tmp-1234", "x")
	write("~$lock.docx", "x")
	write("Thumbs.db", "x")
	write(" padded.txt", "x")
	if err := os.MkdirAll(filepath.Join(root, ".trash", "id1"), dirMode); err != nil {
		t.Fatal(err)
	}
	write(".trash/id1/gone.md", noteText(t, "g", "x"))
	big := strings.Repeat("a", maxNoteBytes+1)
	write("big.md", big)
	if err := os.Symlink(filepath.Join(root, "top.md"), filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(os.TempDir(), filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}

	res, err := s.ScanTree("docs")
	if err != nil {
		t.Fatalf("ScanTree: %v", err)
	}
	var folders, notes, files []string
	for _, f := range res.Folders {
		folders = append(folders, f.RelPath)
	}
	for _, n := range res.Notes {
		notes = append(notes, n.Note.Path+"@"+n.ParentRel+"#"+n.Note.ID+"^"+n.Note.ParentID)
	}
	mimes := map[string]string{}
	for _, f := range res.Files {
		files = append(files, f.RelPath)
		mimes[f.RelPath] = f.MIME
	}
	if got, want := strings.Join(folders, ","), "Projects,Projects/Empty,attachments"; got != want {
		t.Errorf("folders = %s, want %s", got, want)
	}
	if got, want := strings.Join(notes, ","), "Projects/index.md@Projects#n1^,top.md@#n2^"; got != want {
		t.Errorf("notes = %s, want %s", got, want)
	}
	if got, want := strings.Join(files, ","), "Projects/east wall.jpg,README,attachments/x.bin,big.md,broken.md"; got != want {
		t.Errorf("files = %s, want %s", got, want)
	}
	if mimes["Projects/east wall.jpg"] != "image/jpeg" || !strings.HasPrefix(mimes["README"], "text/plain") {
		t.Errorf("mime types = %v", mimes)
	}
	for _, f := range res.Files {
		if f.RelPath == "big.md" && f.Size != int64(len(big)) {
			t.Errorf("big.md size = %d", f.Size)
		}
	}
	if _, err := s.ScanTree("../x"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("ScanTree bad slug error = %v", err)
	}
}

func TestMakeDir(t *testing.T) {
	s := newTestStore(t)
	mustMkdir(t, s, "docs", "Projects")
	mustSave(t, s, "docs", "file.txt", "x")
	tests := []struct {
		rel string
		err error
	}{
		{"Projects/harbour", nil},
		{"New folder", nil},
		{"Projects", core.ErrExists},
		{"projects", core.ErrExists},
		{"FILE.TXT", core.ErrExists},
		{"Missing/child", core.ErrNotFound},
		{"file.txt/child", core.ErrNotFound},
		{".hidden", core.ErrInvalid},
		{"a/../b", core.ErrInvalid},
		{"", core.ErrInvalid},
	}
	for _, tt := range tests {
		if err := s.MakeDir("docs", tt.rel); !errors.Is(err, tt.err) {
			t.Errorf("MakeDir(%q) = %v, want %v", tt.rel, err, tt.err)
		}
	}
	if err := s.MakeDir("nospace", "x"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("MakeDir in missing space = %v", err)
	}
}

func TestMoveEntry(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		err      error
		want     []string
	}{
		{"rename file", "a/one.txt", "a/uno.txt", nil, []string{"a/", "a/sub/", "a/sub/n.md", "a/uno.txt", "b/", "file.bin"}},
		{"case only", "a/one.txt", "a/ONE.txt", nil, []string{"a/", "a/ONE.txt", "a/sub/", "a/sub/n.md", "b/", "file.bin"}},
		{"into new nested folders", "file.bin", "x/y/file.bin", nil, []string{"a/", "a/one.txt", "a/sub/", "a/sub/n.md", "b/", "x/", "x/y/", "x/y/file.bin"}},
		{"folder keeps subtree and leaves empty parent", "a/sub", "b/sub", nil, []string{"a/", "a/one.txt", "b/", "b/sub/", "b/sub/n.md", "file.bin"}},
		{"folder into itself", "a", "a/sub/a", core.ErrInvalid, nil},
		{"folder into case variant of itself", "a", "A/x", core.ErrInvalid, nil},
		{"same path", "a", "a", nil, []string{"a/", "a/one.txt", "a/sub/", "a/sub/n.md", "b/", "file.bin"}},
		{"collision", "file.bin", "a/ONE.TXT", core.ErrExists, nil},
		{"folder name taken by file", "file.bin", "file.bin/x", core.ErrExists, nil},
		{"new parent collides by case", "file.bin", "B/c/file.bin", core.ErrExists, nil},
		{"missing", "nope", "a/nope", core.ErrNotFound, nil},
		{"invalid", "a", "../a", core.ErrInvalid, nil},
		{"dot target", "a", ".trash/a", core.ErrInvalid, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			mustMkdir(t, s, "docs", "a")
			mustMkdir(t, s, "docs", "a/sub")
			mustMkdir(t, s, "docs", "b")
			mustSave(t, s, "docs", "a/one.txt", "1")
			mustSave(t, s, "docs", "a/sub/n.md", noteText(t, "n", "x"))
			mustSave(t, s, "docs", "file.bin", "bin")
			before := treeOf(t, filepath.Join(s.SpacesDir(), "docs"))
			err := s.MoveEntry("docs", tt.old, tt.new)
			if !errors.Is(err, tt.err) {
				t.Fatalf("MoveEntry(%q, %q) = %v, want %v", tt.old, tt.new, err, tt.err)
			}
			got := treeOf(t, filepath.Join(s.SpacesDir(), "docs"))
			want := tt.want
			if err != nil {
				want = before
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("tree = %v, want %v", got, want)
			}
		})
	}
}

func TestCopyEntry(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureSpace("other"); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, s, "docs", "Projects")
	mustMkdir(t, s, "docs", "Projects/Empty")
	mustSave(t, s, "docs", "Projects/index.md", noteText(t, "n1", "folder note\n"))
	mustSave(t, s, "docs", "Projects/plan.md", noteText(t, "n2", "plan\n"))
	mustSave(t, s, "docs", "Projects/photo.jpg", "JPEG")
	mustSave(t, s, "docs", "Projects/broken.md", "---\n: [\n---\n")
	if err := os.WriteFile(filepath.Join(s.SpacesDir(), "docs", "Projects", ".DS_Store"), []byte("x"), fileMode); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	copied, err := s.CopyEntry("docs", "Projects", "other", "Projects copy", func(old string) string {
		seen[old] = true
		return "new-" + old
	})
	if err != nil {
		t.Fatalf("CopyEntry: %v", err)
	}
	var rows []string
	for _, c := range copied {
		rows = append(rows, string(c.Kind)+":"+c.OldRel+">"+c.NewRel+":"+c.OldID+">"+c.NewID)
	}
	sort.Strings(rows)
	want := []string{
		"file:Projects/broken.md>Projects copy/broken.md:>",
		"file:Projects/photo.jpg>Projects copy/photo.jpg:>",
		"folder:Projects/Empty>Projects copy/Empty:>",
		"folder:Projects>Projects copy:>",
		"note:Projects/index.md>Projects copy/index.md:n1>new-n1",
		"note:Projects/plan.md>Projects copy/plan.md:n2>new-n2",
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("copied =\n%s\nwant\n%s", strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
	if !seen["n1"] || !seen["n2"] {
		t.Fatalf("callback saw %v", seen)
	}
	note, err := s.Read("other", "Projects copy/plan.md")
	if err != nil || note.ID != "new-n2" || note.Content != "plan\n" {
		t.Fatalf("copied note = %+v, %v", note, err)
	}
	if got := treeOf(t, filepath.Join(s.SpacesDir(), "other")); strings.Join(got, ",") !=
		"Projects copy/,Projects copy/Empty/,Projects copy/broken.md,Projects copy/index.md,Projects copy/photo.jpg,Projects copy/plan.md" {
		t.Fatalf("copied tree = %v", got)
	}
	broken, _ := os.ReadFile(filepath.Join(s.SpacesDir(), "other", "Projects copy", "broken.md"))
	if string(broken) != "---\n: [\n---\n" {
		t.Fatalf("unparseable note changed: %q", broken)
	}

	single, err := s.CopyEntry("docs", "Projects/plan.md", "docs", "plan copy.md", nil)
	if err != nil || len(single) != 1 || single[0].NewID == "n2" || !core.ValidID(single[0].NewID) {
		t.Fatalf("CopyEntry note with nil callback = %+v, %v", single, err)
	}

	errTests := []struct {
		src, dstSpace, dst string
		err                error
	}{
		{"Projects", "docs", "Projects/Inner", core.ErrInvalid},
		{"Projects", "docs", "projects", core.ErrInvalid},
		{"Projects", "docs", "PLAN COPY.md", core.ErrExists},
		{"Projects/photo.jpg", "docs", "Missing/photo.jpg", core.ErrNotFound},
		{"missing", "docs", "x", core.ErrNotFound},
		{"Projects", "other", "Projects copy", core.ErrExists},
		{"Projects", "docs", ".x", core.ErrInvalid},
	}
	for _, tt := range errTests {
		if _, err := s.CopyEntry("docs", tt.src, tt.dstSpace, tt.dst, nil); !errors.Is(err, tt.err) {
			t.Errorf("CopyEntry(%q, %q, %q) = %v, want %v", tt.src, tt.dstSpace, tt.dst, err, tt.err)
		}
	}
	assertNoTemp(t, s.DataDir())
}

func TestRemoveEntry(t *testing.T) {
	s := newTestStore(t)
	mustMkdir(t, s, "docs", "a")
	mustSave(t, s, "docs", "a/x.txt", "x")
	if err := s.RemoveEntry("docs", "a/x.txt"); err != nil {
		t.Fatalf("RemoveEntry file: %v", err)
	}
	if got := treeOf(t, filepath.Join(s.SpacesDir(), "docs")); strings.Join(got, ",") != "a/" {
		t.Fatalf("empty folder was pruned: %v", got)
	}
	mustSave(t, s, "docs", "a/y.txt", "y")
	if err := s.RemoveEntry("docs", "a"); err != nil {
		t.Fatalf("RemoveEntry folder: %v", err)
	}
	for _, tt := range []struct {
		rel string
		err error
	}{{"a", core.ErrNotFound}, {"a/b", core.ErrNotFound}, {"../docs", core.ErrInvalid}, {"", core.ErrInvalid}} {
		if err := s.RemoveEntry("docs", tt.rel); !errors.Is(err, tt.err) {
			t.Errorf("RemoveEntry(%q) = %v, want %v", tt.rel, err, tt.err)
		}
	}
}

func TestEntryOpsRefuseSymlinkParents(t *testing.T) {
	s := newTestStore(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.SpacesDir(), "docs", "escape")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.OpenFile("docs", "escape/secret.txt"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("OpenFile through symlink = %v", err)
	}
	if _, err := s.SaveFile("docs", "escape/new.txt", strings.NewReader("x"), 0); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("SaveFile through symlink = %v", err)
	}
	if err := s.RemoveEntry("docs", "escape/secret.txt"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("RemoveEntry through symlink = %v", err)
	}
	if _, _, err := s.OpenFile("docs", "escape"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("OpenFile of symlink = %v", err)
	}
	if err := s.MoveEntry("docs", "escape", "moved"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("MoveEntry of symlink = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Errorf("outside file touched: %v", err)
	}
}

func TestStatEntry(t *testing.T) {
	s := newTestStore(t)
	mustSave(t, s, "docs", "a.txt", "abc")
	info, err := s.StatEntry("docs", "a.txt")
	if err != nil || info.Size() != 3 || info.IsDir() {
		t.Fatalf("StatEntry = %v, %v", info, err)
	}
	if _, err := s.StatEntry("docs", "b.txt"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("StatEntry missing = %v", err)
	}
}

// Compile-time check that the scan result type is what ScanTree returns.
var _ = func(s *Store) (ports.ScanResult, error) { return s.ScanTree("docs") }
