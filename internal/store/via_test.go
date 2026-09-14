package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdatedViaFrontmatter writes updated_via only when it is set, reads it
// back into the note, and carries it into a version.
func TestUpdatedViaFrontmatter(t *testing.T) {
	s := newTestStore(t)
	note := writeNote(t, s, "docs", "page.md", "Page", "body\n")
	full := filepath.Join(s.DataDir(), "spaces", "docs", note.Path)

	note.UpdatedBy, note.UpdatedVia = "0person", "bookmark-wiki"
	if err := s.Write("docs", note); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "updated_via: bookmark-wiki\n") {
		t.Fatalf("file has no updated_via:\n%s", raw)
	}
	back, err := s.Read("docs", note.Path)
	if err != nil || back.UpdatedVia != "bookmark-wiki" || back.Extra["updated_via"] != nil {
		t.Fatalf("read back = %+v, %v", back, err)
	}

	v, err := s.SaveVersion("0space", back, "")
	if err != nil || v.Via != "bookmark-wiki" || v.AuthorID != "0person" {
		t.Fatalf("SaveVersion = %+v, %v", v, err)
	}
	listed, err := s.ListVersions("0space", note.ID)
	if err != nil || len(listed) != 1 || listed[0].Via != "bookmark-wiki" {
		t.Fatalf("ListVersions = %+v, %v", listed, err)
	}
	read, err := s.ReadVersion("0space", note.ID, v.ID)
	if err != nil || read.Via != "bookmark-wiki" {
		t.Fatalf("ReadVersion = %+v, %v", read, err)
	}

	back.UpdatedVia = ""
	if err := s.Write("docs", back); err != nil {
		t.Fatalf("Write: %v", err)
	}
	raw, err = os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "updated_via") {
		t.Fatalf("plain write kept updated_via:\n%s", raw)
	}
}
