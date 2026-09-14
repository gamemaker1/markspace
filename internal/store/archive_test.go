package store

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// zipOf builds a zip from ordered name and content pairs. A name ending in
// "/" is a directory entry and a name starting with "@" is a symlink entry.
func zipOf(t *testing.T, entries ...[2]string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e[0], Method: zip.Deflate}
		if strings.HasPrefix(e[0], "@") {
			hdr.Name = e[0][1:]
			hdr.SetMode(fs.ModeSymlink | 0o777)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func TestImportArchive(t *testing.T) {
	s := newTestStore(t)
	mustMkdir(t, s, "docs", "Projects")
	mustSave(t, s, "docs", "report.pdf", "old")
	mustSave(t, s, "docs", "Media", "a file named like a folder")
	writeNote(t, s, "docs", "taken.md", "Taken", "original\n")

	zr := zipOf(t,
		[2]string{"report.pdf", "new"},
		[2]string{"projects/plan.markdown", "# Plan\n"},
		[2]string{"media/clip.mp4", "MP4"},
		[2]string{"Empty/", ""},
		[2]string{"taken.md", "---\ntitle: Second\n---\nsecond\n"},
		[2]string{"../escape.txt", "x"},
		[2]string{"a/../../escape2.txt", "x"},
		[2]string{".git/config", "x"},
		[2]string{"__MACOSX/._report.pdf", "x"},
		[2]string{" padded.txt", "x"},
		[2]string{`win\path\file.txt`, "backslashes"},
		[2]string{"@link", "/etc/passwd"},
	)
	res, err := s.ImportArchive("docs", zr, int64(zr.Len()))
	if err != nil {
		t.Fatalf("ImportArchive: %v", err)
	}
	var notes, files []string
	for _, n := range res.Notes {
		notes = append(notes, n.RelPath)
	}
	for _, f := range res.Files {
		files = append(files, f.RelPath)
	}
	sort.Strings(notes)
	sort.Strings(files)
	if got, want := strings.Join(notes, ","), "Projects/plan.md,taken-2.md"; got != want {
		t.Errorf("notes = %s, want %s", got, want)
	}
	if got, want := strings.Join(files, ","), "media (2)/clip.mp4,report (2).pdf,win/path/file.txt"; got != want {
		t.Errorf("files = %s, want %s", got, want)
	}
	if got, want := strings.Join(res.Folders, ","), "media (2),Empty,win,win/path"; got != want {
		t.Errorf("folders = %s, want %s", got, want)
	}
	old, _ := os.ReadFile(filepath.Join(s.SpacesDir(), "docs", "report.pdf"))
	if string(old) != "old" {
		t.Errorf("existing file overwritten: %q", old)
	}
	for _, name := range treeOf(t, s.DataDir()) {
		if strings.Contains(name, "escape") || strings.Contains(name, "config") || strings.Contains(name, "link") {
			t.Errorf("unsafe entry landed on disk: %q", name)
		}
	}
	assertNoTemp(t, s.DataDir())
}

func TestImportArchiveLimits(t *testing.T) {
	t.Run("oversized file is skipped", func(t *testing.T) {
		setLimit(t, &maxImportFileBytes, 10)
		s, _ := limitedStore(t)
		zr := zipOf(t, [2]string{"big.bin", strings.Repeat("x", 11)}, [2]string{"small.bin", "ok"})
		res, err := s.ImportArchive("docs", zr, int64(zr.Len()))
		if err != nil || len(res.Files) != 1 || res.Files[0].RelPath != "small.bin" {
			t.Fatalf("ImportArchive = %+v, %v", res, err)
		}
	})
	t.Run("total budget stops the import", func(t *testing.T) {
		setLimit(t, &maxImportTotalBytes, 15)
		s, _ := limitedStore(t)
		zr := zipOf(t, [2]string{"a.bin", strings.Repeat("a", 10)}, [2]string{"b.bin", strings.Repeat("b", 10)})
		res, err := s.ImportArchive("docs", zr, int64(zr.Len()))
		if !errors.Is(err, core.ErrTooLarge) || len(res.Files) != 1 {
			t.Fatalf("ImportArchive = %+v, %v", res, err)
		}
		assertNoTemp(t, s.DataDir())
	})
	t.Run("entry count", func(t *testing.T) {
		setLimit(t, &maxImportEntries, 2)
		s, _ := limitedStore(t)
		zr := zipOf(t, [2]string{"a", "1"}, [2]string{"b", "2"}, [2]string{"c", "3"})
		if _, err := s.ImportArchive("docs", zr, int64(zr.Len())); !errors.Is(err, core.ErrTooLarge) {
			t.Fatalf("ImportArchive = %v", err)
		}
	})
	t.Run("junk", func(t *testing.T) {
		s, _ := limitedStore(t)
		data := []byte("not a zip")
		if _, err := s.ImportArchive("docs", bytes.NewReader(data), int64(len(data))); !errors.Is(err, core.ErrInvalid) {
			t.Fatalf("ImportArchive junk = %v", err)
		}
		if _, err := s.ImportArchive("missing", bytes.NewReader(data), int64(len(data))); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("ImportArchive missing space = %v", err)
		}
	})
}

// FuzzImportArchive checks that nothing is written outside the space and
// that every returned path is a valid entry path.
func FuzzImportArchive(f *testing.F) {
	f.Add("a.md", []byte("# A"), "dir/x.png", []byte("img"))
	f.Add("../evil.md", []byte("x"), "a/../../evil.png", []byte("x"))
	f.Add("A/b.txt", []byte("x"), "a/B.TXT", []byte("y"))
	f.Add("x/", []byte(""), "x", []byte("file"))
	f.Fuzz(func(t *testing.T, name1 string, body1 []byte, name2 string, body2 []byte) {
		parent := t.TempDir()
		s, err := New(filepath.Join(parent, "data"), slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.EnsureSpace("docs"); err != nil {
			t.Fatal(err)
		}
		spaceDir := filepath.Join(s.SpacesDir(), "docs")
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, e := range []struct {
			name string
			body []byte
		}{{name1, body1}, {name2, body2}} {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate})
			if err != nil {
				continue
			}
			w.Write(e.body)
		}
		zw.Close()
		before := listTree(parent)
		res, _ := s.ImportArchive("docs", bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if res != nil {
			for _, n := range res.Notes {
				if err := validEntryPath(n.RelPath); err != nil {
					t.Fatalf("note path %q: %v", n.RelPath, err)
				}
			}
			for _, fl := range res.Files {
				if err := validEntryPath(fl.RelPath); err != nil {
					t.Fatalf("file path %q: %v", fl.RelPath, err)
				}
			}
		}
		for full := range listTree(parent) {
			if !before[full] && !withinDir(spaceDir, full) {
				t.Fatalf("import wrote %q outside the space dir", full)
			}
		}
		assertNoCaseCollisions(t, spaceDir)
	})
}

// assertNoCaseCollisions fails when two entries of one directory below root
// differ only by case.
func assertNoCaseCollisions(t *testing.T, root string) {
	t.Helper()
	_ = filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		entries, err := os.ReadDir(full)
		if err != nil {
			return nil
		}
		seen := map[string]string{}
		for _, e := range entries {
			key := foldKey(e.Name())
			if other, ok := seen[key]; ok {
				t.Fatalf("case collision in %s: %q and %q", full, other, e.Name())
			}
			seen[key] = e.Name()
		}
		return nil
	})
}
