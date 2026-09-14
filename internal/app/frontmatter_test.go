package app

import (
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestNoteExtraFrontmatter(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")

	n, err := h.app.CreateNote(h.ctx, owner, core.NoteInput{
		SpaceID: sp.ID, Title: "Source", Content: "body",
		Extra: map[string]any{"source": "https://example.test/a", "title": "Hijack", "id": "x", "ID": "y", "rating": 4},
	})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	disk, err := h.files.Read(sp.Slug, n.Path)
	if err != nil {
		t.Fatal(err)
	}
	if disk.Title != "Source" || disk.ID != n.ID {
		t.Fatalf("owned keys overwritten: title %q id %q", disk.Title, disk.ID)
	}
	if disk.Extra["source"] != "https://example.test/a" {
		t.Fatalf("extra source = %v, all %v", disk.Extra["source"], disk.Extra)
	}
	for _, k := range []string{"title", "id", "ID"} {
		if _, ok := disk.Extra[k]; ok {
			t.Fatalf("owned key %q stored as extra: %v", k, disk.Extra)
		}
	}
	if disk.Extra["rating"] == nil {
		t.Fatalf("rating lost: %v", disk.Extra)
	}

	// Merge adds and replaces, a nil value removes, and owned keys stay out.
	if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{
		Extra: map[string]any{"rating": nil, "status": "draft", "source": "https://example.test/b", "tags": []string{"x"}},
	}); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	disk, _ = h.files.Read(sp.Slug, n.Path)
	if _, ok := disk.Extra["rating"]; ok {
		t.Fatalf("rating not removed: %v", disk.Extra)
	}
	if disk.Extra["status"] != "draft" || disk.Extra["source"] != "https://example.test/b" {
		t.Fatalf("merge result: %v", disk.Extra)
	}
	if _, ok := disk.Extra["tags"]; ok || len(disk.Tags) != 0 {
		t.Fatalf("tags written through extra: extra %v tags %v", disk.Extra, disk.Tags)
	}
	if disk.Content != "body" {
		t.Fatalf("body changed: %q", disk.Content)
	}

	// An update without Extra keeps the keys, and GetNote returns them.
	body := "new body"
	if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: &body}); err != nil {
		t.Fatal(err)
	}
	got, _, err := h.app.GetNote(h.ctx, owner, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Extra["status"] != "draft" {
		t.Fatalf("GetNote extra = %v", got.Extra)
	}
}

func TestMergeExtra(t *testing.T) {
	if got := mergeExtra(nil, nil); got != nil {
		t.Fatalf("empty merge = %v", got)
	}
	cur := map[string]any{"a": 1}
	if got := mergeExtra(cur, map[string]any{"a": nil}); got != nil {
		t.Fatalf("removing the last key = %v, want nil", got)
	}
	if cur["a"] != 1 {
		t.Fatal("merge changed its input")
	}
	in := map[string]any{}
	for i := 0; i < maxExtraKeys+20; i++ {
		in[string(rune('a'+i%26))+string(rune('a'+(i/26)%26))] = i
	}
	if got := mergeExtra(nil, in); len(got) > maxExtraKeys {
		t.Fatalf("merge kept %d keys", len(got))
	}
	if got := mergeExtra(nil, map[string]any{" ": 1, "Updated_By": "x", "position": 3}); got != nil {
		t.Fatalf("owned or blank keys kept: %v", got)
	}
}
