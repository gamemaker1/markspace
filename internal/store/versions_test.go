package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// saveVersions writes n snapshots of one note and returns their ids, oldest
// first.
func saveVersions(t *testing.T, s *Store, spaceID string, note *core.Note, n int) []string {
	t.Helper()
	var ids []string
	for i := 0; i < n; i++ {
		note.Content = strings.Repeat("x", i+1) + "\n"
		v, err := s.SaveVersion(spaceID, note, "")
		if err != nil {
			t.Fatalf("SaveVersion: %v", err)
		}
		ids = append(ids, v.ID)
	}
	return ids
}

func TestVersionSaveListReadPrune(t *testing.T) {
	s := newTestStore(t)
	note := writeNote(t, s, "docs", "guide.md", "Guide", "first\n")
	note.CreatedBy = "0author"
	note.UpdatedBy = "0editor"
	note.Extra = map[string]any{"custom": "kept"}

	labelled, err := s.SaveVersion("0space", note, "before edit")
	if err != nil {
		t.Fatalf("SaveVersion: %v", err)
	}
	if labelled.Label != "before edit" || labelled.NoteID != note.ID || labelled.SpaceID != "0space" {
		t.Fatalf("version = %+v", labelled)
	}
	if labelled.Title != "Guide" || labelled.AuthorID != "0editor" || labelled.Size == 0 {
		t.Fatalf("version = %+v", labelled)
	}
	if labelled.CreatedAt.IsZero() {
		t.Fatal("version CreatedAt is zero")
	}
	full := filepath.Join(s.DataDir(), "internal", "history", "0space", note.ID, labelled.ID+mdExt)
	raw, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("snapshot file: %v", err)
	}
	if !strings.Contains(string(raw), "label: before edit") {
		t.Fatalf("snapshot has no label:\n%s", raw)
	}
	if !strings.Contains(string(raw), "id: "+note.ID) {
		t.Fatalf("snapshot has no frontmatter id:\n%s", raw)
	}

	// The label lives in the snapshot only.
	onDisk, err := os.ReadFile(filepath.Join(s.SpacesDir(), "docs", "guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), "label:") {
		t.Fatal("label leaked into the note file")
	}
	if _, ok := note.Extra[keyLabel]; ok {
		t.Fatal("label leaked into note.Extra")
	}

	ids := saveVersions(t, s, "0space", note, 3)
	all := append([]string{labelled.ID}, ids...)

	list, err := s.ListVersions("0space", note.ID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("ListVersions returned %d, want 4", len(list))
	}
	for i, v := range list {
		want := all[len(all)-1-i]
		if v.ID != want {
			t.Fatalf("ListVersions[%d].ID = %q, want %q", i, v.ID, want)
		}
		if v.Content != "" {
			t.Errorf("ListVersions[%d] carries content", i)
		}
		if v.Title != "Guide" || v.AuthorID != "0editor" {
			t.Errorf("ListVersions[%d] header = %q %q", i, v.Title, v.AuthorID)
		}
		if v.Size == 0 {
			t.Errorf("ListVersions[%d] has no size", i)
		}
	}
	if list[3].Label != "before edit" {
		t.Errorf("oldest label = %q", list[3].Label)
	}

	read, err := s.ReadVersion("0space", note.ID, all[3])
	if err != nil {
		t.Fatalf("ReadVersion: %v", err)
	}
	if read.Content != "xxx\n" {
		t.Fatalf("ReadVersion content = %q", read.Content)
	}
	if read.Title != "Guide" || read.AuthorID != "0editor" {
		t.Fatalf("ReadVersion header = %q %q", read.Title, read.AuthorID)
	}
	if _, err := s.ReadVersion("0space", note.ID, "999"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("ReadVersion missing error = %v, want ErrNotFound", err)
	}

	if err := s.PruneVersions("0space", note.ID, 2); err != nil {
		t.Fatalf("PruneVersions: %v", err)
	}
	after, err := s.ListVersions("0space", note.ID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("ListVersions after prune returned %d, want 2", len(after))
	}
	if after[0].ID != all[3] || after[1].ID != all[2] {
		t.Fatalf("prune kept %q and %q, want %q and %q", after[0].ID, after[1].ID, all[3], all[2])
	}
	if err := s.PruneVersions("0space", note.ID, 10); err != nil {
		t.Fatalf("PruneVersions with a high keep: %v", err)
	}
	if err := s.PruneVersions("0space", note.ID, 0); err != nil {
		t.Fatalf("PruneVersions 0: %v", err)
	}
	empty, err := s.ListVersions("0space", note.ID)
	if err != nil || len(empty) != 0 {
		t.Fatalf("ListVersions = %v, %v", empty, err)
	}
}

func TestVersionsWithNoHistory(t *testing.T) {
	s := newTestStore(t)
	list, err := s.ListVersions("0space", "0note")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ListVersions = %v", list)
	}
	if err := s.PruneVersions("0space", "0note", 3); err != nil {
		t.Fatalf("PruneVersions: %v", err)
	}
	if _, err := s.ReadVersion("0space", "0note", "1"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("ReadVersion error = %v, want ErrNotFound", err)
	}
	if _, err := s.SaveVersion("", &core.Note{ID: "0note"}, ""); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("SaveVersion with no space error = %v, want ErrInvalid", err)
	}
	if _, err := s.SaveVersion("0space", nil, ""); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("SaveVersion with nil note error = %v, want ErrInvalid", err)
	}
	if _, err := s.ReadVersion("0space", "0note", "../../escape"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("ReadVersion with a traversing id error = %v, want ErrInvalid", err)
	}
}
