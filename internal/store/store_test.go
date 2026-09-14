package store

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// newTestStore builds a store in a temporary directory with one space.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.EnsureSpace("docs"); err != nil {
		t.Fatalf("EnsureSpace: %v", err)
	}
	return s
}

// writeNote writes a note with a body and no extra frontmatter.
func writeNote(t *testing.T, s *Store, space, relPath, title, body string) *core.Note {
	t.Helper()
	note := &core.Note{
		ID:        core.NewID(),
		Path:      relPath,
		Slug:      baseName(relPath),
		Title:     title,
		Content:   body,
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	if err := s.Write(space, note); err != nil {
		t.Fatalf("Write %q: %v", relPath, err)
	}
	return note
}

// treeOf lists every file below dir as slash separated relative paths.
func treeOf(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		if d.IsDir() {
			name += "/"
		}
		out = append(out, name)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %q: %v", dir, err)
	}
	sort.Strings(out)
	return out
}

func TestNewCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, want := range []string{
		filepath.Join(dir, "spaces"),
		filepath.Join(dir, "internal", "history"),
		filepath.Join(dir, "deleted"),
	} {
		info, err := os.Stat(want)
		if err != nil || !info.IsDir() {
			t.Errorf("missing directory %q: %v", want, err)
		}
	}
	if s.SpacesDir() != filepath.Join(dir, "spaces") {
		t.Errorf("SpacesDir = %q", s.SpacesDir())
	}
	if _, err := New("", nil); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("New(\"\") error = %v, want ErrInvalid", err)
	}
}

func TestValidRelPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"simple", "a.md", false},
		{"nested", "a/b/c.md", false},
		{"space in name", "my notes.md", false},
		{"empty", "", true},
		{"no extension", "a", true},
		{"wrong extension", "a.txt", true},
		{"absolute", "/a.md", true},
		{"parent escape", "../a.md", true},
		{"nested parent escape", "a/../../b.md", true},
		{"dot segment", "./a.md", true},
		{"dot dir", ".trash/x/a.md", true},
		{"hidden file", "a/.hidden.md", true},
		{"backslash", "a\\b.md", true},
		{"nul byte", "a\x00.md", true},
		{"empty segment", "a//b.md", true},
		{"padded segment", "a /b.md", true},
		{"extension only", "a/.md", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validRelPath(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validRelPath(%q) = nil, want error", tt.path)
				}
				if !errors.Is(err, core.ErrInvalid) {
					t.Fatalf("validRelPath(%q) error = %v, want ErrInvalid", tt.path, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validRelPath(%q) = %v", tt.path, err)
			}
		})
	}
}

func TestRejectedPathsReachEveryMethod(t *testing.T) {
	s := newTestStore(t)
	bad := "../escape.md"
	if _, err := s.Read("docs", bad); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("Read error = %v, want ErrInvalid", err)
	}
	if err := s.Write("docs", &core.Note{Path: bad}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("Write error = %v, want ErrInvalid", err)
	}
	if _, _, err := s.Stat("docs", bad); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("Stat error = %v, want ErrInvalid", err)
	}
	if s.Exists("docs", bad) {
		t.Error("Exists reported true for an invalid path")
	}
	if _, err := s.Read("../etc", "a.md"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("Read with bad space error = %v, want ErrInvalid", err)
	}
}

func TestValidSlug(t *testing.T) {
	tests := []struct {
		slug    string
		wantErr bool
	}{
		{"docs", false},
		{"my-space", false},
		{"My Space", false},
		{"", true},
		{".", true},
		{"..", true},
		{".hidden", true},
		{"a/b", true},
		{`a\b`, true},
		{"a\x00", true},
		{" padded ", true},
	}
	for _, tt := range tests {
		err := validSlug(tt.slug)
		if tt.wantErr && !errors.Is(err, core.ErrInvalid) {
			t.Errorf("validSlug(%q) = %v, want ErrInvalid", tt.slug, err)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("validSlug(%q) = %v", tt.slug, err)
		}
	}
}

func TestWriteReadStatExists(t *testing.T) {
	s := newTestStore(t)
	note := writeNote(t, s, "docs", "guide.md", "Guide", "# Guide\n\nHello\n")
	if !s.Exists("docs", "guide.md") {
		t.Fatal("Exists = false after Write")
	}
	got, err := s.Read("docs", "guide.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ID != note.ID || got.Title != "Guide" || got.Content != "# Guide\n\nHello\n" {
		t.Fatalf("Read gave %+v", got)
	}
	if got.SpaceSlug != "docs" || got.Slug != "guide" || got.Path != "guide.md" {
		t.Fatalf("Read identity fields = %q %q %q", got.SpaceSlug, got.Slug, got.Path)
	}
	modTime, size, err := s.Stat("docs", "guide.md")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if size == 0 || modTime.IsZero() {
		t.Fatalf("Stat = %v %d", modTime, size)
	}
	if _, err := s.Read("docs", "missing.md"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Read missing error = %v, want ErrNotFound", err)
	}
	if _, _, err := s.Stat("docs", "missing.md"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Stat missing error = %v, want ErrNotFound", err)
	}
	info, err := os.Stat(filepath.Join(s.SpacesDir(), "docs", "guide.md"))
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("file mode = %v, want 0644", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Join(s.SpacesDir(), "docs"))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temporary file left behind: %q", e.Name())
		}
	}
}

func TestSpaceLifecycle(t *testing.T) {
	s := newTestStore(t)
	writeNote(t, s, "docs", "a.md", "A", "a\n")
	if err := s.EnsureSpace("notes"); err != nil {
		t.Fatalf("EnsureSpace: %v", err)
	}
	dirs, err := s.ListSpaceDirs()
	if err != nil {
		t.Fatalf("ListSpaceDirs: %v", err)
	}
	if strings.Join(dirs, ",") != "docs,notes" {
		t.Fatalf("ListSpaceDirs = %v", dirs)
	}
	if err := s.RenameSpace("docs", "guides"); err != nil {
		t.Fatalf("RenameSpace: %v", err)
	}
	if !s.Exists("guides", "a.md") {
		t.Fatal("note missing after RenameSpace")
	}
	if err := s.RenameSpace("guides", "notes"); !errors.Is(err, core.ErrExists) {
		t.Fatalf("RenameSpace onto existing error = %v, want ErrExists", err)
	}
	if err := s.RenameSpace("missing", "other"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("RenameSpace missing error = %v, want ErrNotFound", err)
	}
	if err := s.ArchiveSpace("guides"); err != nil {
		t.Fatalf("ArchiveSpace: %v", err)
	}
	dirs, err = s.ListSpaceDirs()
	if err != nil {
		t.Fatalf("ListSpaceDirs: %v", err)
	}
	if strings.Join(dirs, ",") != "notes" {
		t.Fatalf("ListSpaceDirs after archive = %v", dirs)
	}
	archived := treeOf(t, filepath.Join(s.DataDir(), "deleted"))
	found := false
	for _, name := range archived {
		if strings.HasSuffix(name, "/a.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("archived tree = %v, want an a.md entry", archived)
	}
	if err := s.ArchiveSpace("missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("ArchiveSpace missing error = %v, want ErrNotFound", err)
	}
}

func TestDiskUsage(t *testing.T) {
	s := newTestStore(t)
	before, err := s.DiskUsage()
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	writeNote(t, s, "docs", "a.md", "A", strings.Repeat("x", 4096))
	after, err := s.DiskUsage()
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	if after < before+4096 {
		t.Fatalf("DiskUsage = %d, want at least %d", after, before+4096)
	}
}
