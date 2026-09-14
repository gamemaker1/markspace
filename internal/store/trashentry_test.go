package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestTrashAndRestoreEntries(t *testing.T) {
	s := newTestStore(t)
	mustMkdir(t, s, "docs", "Projects")
	mustMkdir(t, s, "docs", "Projects/harbour")
	mustSave(t, s, "docs", "Projects/harbour/index.md", noteText(t, "n1", "harbour\n"))
	mustSave(t, s, "docs", "Projects/plan.md", noteText(t, "n2", "plan\n"))
	mustSave(t, s, "docs", "Projects/photo.jpg", "JPEG")

	folderTrash, err := s.TrashEntry("docs", "Projects/harbour", "f1", "u1")
	if err != nil || folderTrash != ".trash/f1/harbour" {
		t.Fatalf("TrashEntry folder = %q, %v", folderTrash, err)
	}
	fileTrash, err := s.TrashEntry("docs", "Projects/photo.jpg", "file1", "u2")
	if err != nil {
		t.Fatalf("TrashEntry file: %v", err)
	}
	noteTrash, err := s.TrashEntry("docs", "Projects/plan.md", "n2", "u3")
	if err != nil {
		t.Fatalf("TrashEntry note: %v", err)
	}
	if got := treeOf(t, filepath.Join(s.SpacesDir(), "docs")); !strings.HasPrefix(strings.Join(got, ","), "Projects/,") || len(got) != 1+len(treeOf(t, filepath.Join(s.SpacesDir(), "docs", ".trash")))+1 {
		// Only Projects and the trash remain at the top.
		live := []string{}
		for _, p := range got {
			if !strings.HasPrefix(p, ".trash") {
				live = append(live, p)
			}
		}
		if strings.Join(live, ",") != "Projects/" {
			t.Fatalf("live tree = %v", live)
		}
	}
	trashed, err := s.Read("docs", "Projects/plan.md")
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("note still live: %+v, %v", trashed, err)
	}

	entries, err := s.ScanTrashEntries("docs")
	if err != nil || len(entries) != 3 {
		t.Fatalf("ScanTrashEntries = %+v, %v", entries, err)
	}
	byID := map[string]int{}
	for i, e := range entries {
		byID[e.ID] = i
	}
	folder := entries[byID["f1"]]
	if folder.Kind != core.KindFolder || folder.TrashedFrom != "Projects/harbour" || folder.DeletedBy != "u1" || folder.Size == 0 || folder.DeletedAt.IsZero() {
		t.Fatalf("folder entry = %+v", folder)
	}
	file := entries[byID["file1"]]
	if file.Kind != core.KindFile || file.TrashRel != fileTrash || file.Size != 4 || file.DeletedBy != "u2" {
		t.Fatalf("file entry = %+v", file)
	}
	note := entries[byID["n2"]]
	if note.Kind != core.KindNote || note.Note == nil || note.Note.ID != "n2" || !note.Note.Deleted || note.Note.DeletedBy != "u3" || note.TrashedFrom != "Projects/plan.md" {
		t.Fatalf("note entry = %+v", note)
	}
	raw, _ := os.ReadFile(filepath.Join(s.SpacesDir(), "docs", filepath.FromSlash(noteTrash)))
	if !strings.Contains(string(raw), "trashed_from: Projects/plan.md") || !strings.Contains(string(raw), "deleted: true") {
		t.Fatalf("trashed note frontmatter:\n%s", raw)
	}

	errTests := []struct {
		name, trashRel, target string
		err                    error
	}{
		{"parent missing", folderTrash, "Gone/harbour", core.ErrNotFound},
		{"collision", fileTrash, "PROJECTS", core.ErrExists},
		{"missing slot", ".trash/zzz/harbour", "harbour", core.ErrNotFound},
		{"not a trash path", "Projects", "x", core.ErrInvalid},
		{"escaping trash path", ".trash/../Projects", "x", core.ErrInvalid},
		{"bad target", fileTrash, "../x", core.ErrInvalid},
	}
	for _, tt := range errTests {
		if err := s.RestoreEntry("docs", tt.trashRel, tt.target); !errors.Is(err, tt.err) {
			t.Errorf("RestoreEntry %s = %v, want %v", tt.name, err, tt.err)
		}
	}

	if err := s.RestoreEntry("docs", folderTrash, "Projects/harbour"); err != nil {
		t.Fatalf("RestoreEntry folder: %v", err)
	}
	if err := s.RestoreEntry("docs", noteTrash, "restored-plan.md"); err != nil {
		t.Fatalf("RestoreEntry note: %v", err)
	}
	restored, err := s.Read("docs", "restored-plan.md")
	if err != nil || restored.ID != "n2" || restored.Deleted || restored.DeletedAt != nil || restored.Content != "plan\n" {
		t.Fatalf("restored note = %+v, %v", restored, err)
	}
	if _, ok := restored.Extra[keyTrashedFrom]; ok {
		t.Fatalf("trashed_from kept: %+v", restored.Extra)
	}
	if n, err := s.Read("docs", "Projects/harbour/index.md"); err != nil || n.ID != "n1" {
		t.Fatalf("restored folder note = %+v, %v", n, err)
	}
	if err := s.PurgeTrashEntry("docs", fileTrash); err != nil {
		t.Fatalf("PurgeTrashEntry: %v", err)
	}
	if entries, err := s.ScanTrashEntries("docs"); err != nil || len(entries) != 0 {
		t.Fatalf("trash after purge = %+v, %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(s.SpacesDir(), "docs", ".trash")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty trash root kept: %v", err)
	}
}

func TestTrashEntryErrors(t *testing.T) {
	s := newTestStore(t)
	mustSave(t, s, "docs", "a.txt", "a")
	mustSave(t, s, "docs", "b.txt", "b")
	if _, err := s.TrashEntry("docs", "a.txt", "id1", ""); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		rel, id string
		err     error
	}{
		{"b.txt", "id1", core.ErrExists},
		{"missing.txt", "id2", core.ErrNotFound},
		{"b.txt", "", core.ErrInvalid},
		{"b.txt", "../x", core.ErrInvalid},
		{".trash/id1/a.txt", "id3", core.ErrInvalid},
	}
	for _, tt := range tests {
		if _, err := s.TrashEntry("docs", tt.rel, tt.id, ""); !errors.Is(err, tt.err) {
			t.Errorf("TrashEntry(%q, %q) = %v, want %v", tt.rel, tt.id, err, tt.err)
		}
	}
}

func TestTrashSlotWithOnlySidecarIsReusable(t *testing.T) {
	s := newTestStore(t)
	mustSave(t, s, "docs", "a.txt", "a")
	slot := filepath.Join(s.SpacesDir(), "docs", ".trash", "id1")
	if err := os.MkdirAll(slot, dirMode); err != nil {
		t.Fatal(err)
	}
	// A crash between the sidecar write and the rename leaves this state.
	if err := writeTrashMeta(slot, trashMeta{Kind: core.KindFile, Name: "a.txt", TrashedFrom: "a.txt"}); err != nil {
		t.Fatal(err)
	}
	if entries, err := s.ScanTrashEntries("docs"); err != nil || len(entries) != 0 {
		t.Fatalf("incomplete slot listed: %+v, %v", entries, err)
	}
	if _, err := s.TrashEntry("docs", "a.txt", "id1", "u"); err != nil {
		t.Fatalf("TrashEntry into sidecar-only slot: %v", err)
	}
	if entries, err := s.ScanTrashEntries("docs"); err != nil || len(entries) != 1 || entries[0].DeletedBy != "u" {
		t.Fatalf("ScanTrashEntries = %+v, %v", entries, err)
	}
}

func TestEmptyTrashRemovesEverySlot(t *testing.T) {
	s := newTestStore(t)
	writeNote(t, s, "docs", "a.md", "A", "a\n")
	mustMkdir(t, s, "docs", "Folder")
	mustSave(t, s, "docs", "Folder/x.bin", "x")
	note, err := s.Read("docs", "a.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrashEntry("docs", "a.md", note.ID, "u"); err != nil {
		t.Fatalf("TrashEntry note: %v", err)
	}
	if _, err := s.TrashEntry("docs", "Folder", "0folder", "u"); err != nil {
		t.Fatalf("TrashEntry folder: %v", err)
	}
	if entries, err := s.ScanTrashEntries("docs"); err != nil || len(entries) != 2 {
		t.Fatalf("ScanTrashEntries = %+v, %v", entries, err)
	}
	if err := s.EmptyTrash("docs"); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	if entries, err := s.ScanTrashEntries("docs"); err != nil || len(entries) != 0 {
		t.Fatalf("ScanTrashEntries after EmptyTrash = %+v, %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(s.SpacesDir(), "docs", trashDirName)); !os.IsNotExist(err) {
		t.Fatalf("trash directory survived: %v", err)
	}
}
