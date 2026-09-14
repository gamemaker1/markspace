package store

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// zipNames lists the entry names of a zip archive.
func zipNames(t *testing.T, data []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	var out []string
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

// zipEntry returns the bytes of one zip entry.
func zipEntry(t *testing.T, data []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %q: %v", name, err)
		}
		defer rc.Close()
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read entry %q: %v", name, err)
		}
		return buf.String()
	}
	t.Fatalf("entry %q not found in %v", name, zipNames(t, data))
	return ""
}

// notesOf pulls the notes out of a scan result.
func notesOf(scanned []ports.ScannedNote) []*core.Note {
	out := make([]*core.Note, 0, len(scanned))
	for _, sn := range scanned {
		out = append(out, sn.Note)
	}
	return out
}

func TestExportThenImportRoundTrip(t *testing.T) {
	s := newTestStore(t)
	writeNote(t, s, "docs", "guide.md", "Guide", "# Guide\n\nSee ![shot](guide/photo.png)\n")
	writeNote(t, s, "docs", "guide/install.md", "Install Guide", "child\n")
	writeNote(t, s, "docs", "guide/install/docker.md", "Docker Notes", "grandchild\n")
	if _, err := s.SaveFile("docs", "guide/photo.png", strings.NewReader("PNGDATA"), 1024); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	if err := s.MakeDir("docs", "Empty folder"); err != nil {
		t.Fatalf("MakeDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.SpacesDir(), "docs", "guide", "Thumbs.db"), []byte("x"), fileMode); err != nil {
		t.Fatal(err)
	}

	res, err := s.ScanTree("docs")
	if err != nil {
		t.Fatalf("ScanTree: %v", err)
	}
	var buf bytes.Buffer
	if err := s.ExportSpace("docs", notesOf(res.Notes), &buf); err != nil {
		t.Fatalf("ExportSpace: %v", err)
	}
	names := zipNames(t, buf.Bytes())
	want := []string{"Empty folder/", "guide.md", "guide/install.md", "guide/install/docker.md", "guide/photo.png"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("zip entries = %v, want %v", names, want)
	}
	body := zipEntry(t, buf.Bytes(), "guide.md")
	if !strings.Contains(body, "![shot](guide/photo.png)") {
		t.Fatalf("note body changed in the export:\n%s", body)
	}
	if strings.Contains(body, "_parent_path") || strings.Contains(body, "parent:") {
		t.Fatalf("parent key leaked into the export:\n%s", body)
	}

	if err := s.EnsureSpace("copy"); err != nil {
		t.Fatalf("EnsureSpace: %v", err)
	}
	imported, err := s.ImportArchive("copy", bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("ImportArchive: %v", err)
	}
	if len(imported.Notes) != 3 || len(imported.Files) != 1 {
		t.Fatalf("ImportArchive wrote %d notes and %d files, want 3 and 1", len(imported.Notes), len(imported.Files))
	}
	gotTitles := map[string]string{}
	for _, in := range imported.Notes {
		gotTitles[in.RelPath] = in.Note.Title
		if in.Note.ID == "" {
			t.Errorf("%q has no id", in.RelPath)
		}
	}
	wantTitles := map[string]string{
		"guide.md":                "Guide",
		"guide/install.md":        "Install Guide",
		"guide/install/docker.md": "Docker Notes",
	}
	for path, title := range wantTitles {
		if gotTitles[path] != title {
			t.Errorf("imported %q title = %q, want %q", path, gotTitles[path], title)
		}
	}
	file := imported.Files[0]
	if file.RelPath != "guide/photo.png" || file.Size != 7 || file.MIME != "image/png" || len(file.SHA256) != 64 {
		t.Fatalf("imported file = %+v", file)
	}
	copyTree := treeOf(t, filepath.Join(s.SpacesDir(), "copy"))
	wantTree := []string{
		"Empty folder/", "guide.md", "guide/", "guide/install.md", "guide/install/", "guide/install/docker.md", "guide/photo.png",
	}
	if strings.Join(copyTree, ",") != strings.Join(wantTree, ",") {
		t.Fatalf("imported tree = %v, want %v", copyTree, wantTree)
	}
	data, err := os.ReadFile(filepath.Join(s.SpacesDir(), "copy", "guide", "photo.png"))
	if err != nil || string(data) != "PNGDATA" {
		t.Fatalf("imported file = %q, %v", data, err)
	}
}

func TestImportMarkdown(t *testing.T) {
	s := newTestStore(t)
	first, err := s.ImportMarkdown("docs", "My Notes.md", []byte("# My Notes\n\nbody\n"))
	if err != nil {
		t.Fatalf("ImportMarkdown: %v", err)
	}
	if first.RelPath != "my-notes.md" {
		t.Fatalf("RelPath = %q, want my-notes.md", first.RelPath)
	}
	if first.Note.Title != "My Notes" || first.Note.ID == "" {
		t.Fatalf("note = %+v", first.Note)
	}
	second, err := s.ImportMarkdown("docs", "My Notes.md", []byte("# My Notes\n\nagain\n"))
	if err != nil {
		t.Fatalf("ImportMarkdown: %v", err)
	}
	if second.RelPath != "my-notes-2.md" {
		t.Fatalf("second RelPath = %q, want my-notes-2.md", second.RelPath)
	}
	third, err := s.ImportMarkdown("docs", "../../etc/passwd", []byte("no frontmatter\n"))
	if err != nil {
		t.Fatalf("ImportMarkdown with a hostile name: %v", err)
	}
	if strings.Contains(third.RelPath, "..") || !strings.HasSuffix(third.RelPath, mdExt) {
		t.Fatalf("RelPath = %q", third.RelPath)
	}
}
