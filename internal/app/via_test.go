package app

import (
	"os"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// TestActingWriteRecordsVia checks that a write through an acting token names
// the person as author and the plugin as via, in the index row, the
// frontmatter and the version, and that a later plain write clears it.
func TestActingWriteRecordsVia(t *testing.T) {
	h := newRealHarness(t)
	person := h.user("Person", false)
	sp := h.space(person, "Alpha")
	n := h.note(person, sp, "", "Page", "first body")
	if n.UpdatedVia != "" {
		t.Fatalf("new note via = %q", n.UpdatedVia)
	}
	pu := h.pluginUser("Wiki")
	acting := actingActor(person.User, pluginActor(pu, "wiki", map[string]core.Role{sp.ID: core.RoleEditor}))

	body := "second body from the plugin"
	updated, err := h.app.UpdateNote(h.ctx, acting, n.ID, core.NoteUpdate{Content: &body, Snapshot: true})
	if err != nil {
		t.Fatalf("acting UpdateNote: %v", err)
	}
	if updated.UpdatedBy != person.User.ID || updated.UpdatedVia != "wiki" {
		t.Fatalf("acting update = by %q via %q", updated.UpdatedBy, updated.UpdatedVia)
	}
	if row := h.row(n.ID); row.UpdatedBy != person.User.ID || row.UpdatedVia != "wiki" {
		t.Fatalf("row = by %q via %q", row.UpdatedBy, row.UpdatedVia)
	}
	raw, err := os.ReadFile(h.diskPath(sp, updated.Path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "updated_by: "+person.User.ID+"\n") || !strings.Contains(string(raw), "updated_via: wiki\n") {
		t.Fatalf("frontmatter after acting write:\n%s", raw)
	}

	body = "third body from the person"
	if _, err := h.app.UpdateNote(h.ctx, person, n.ID, core.NoteUpdate{Content: &body, Snapshot: true}); err != nil {
		t.Fatalf("person UpdateNote: %v", err)
	}
	if row := h.row(n.ID); row.UpdatedBy != person.User.ID || row.UpdatedVia != "" {
		t.Fatalf("row after plain write = by %q via %q", row.UpdatedBy, row.UpdatedVia)
	}
	raw, err = os.ReadFile(h.diskPath(sp, updated.Path))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "updated_via") {
		t.Fatalf("plain write kept updated_via:\n%s", raw)
	}

	versions, err := h.app.ListVersions(h.ctx, person, n.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions = %+v, %v", versions, err)
	}
	// Newest first: the snapshot of the acting write, then the original body.
	if versions[0].AuthorID != person.User.ID || versions[0].Via != "wiki" {
		t.Fatalf("acting version = author %q via %q", versions[0].AuthorID, versions[0].Via)
	}
	if versions[1].AuthorID != person.User.ID || versions[1].Via != "" {
		t.Fatalf("first version = author %q via %q", versions[1].AuthorID, versions[1].Via)
	}
	v, err := h.app.GetVersion(h.ctx, person, n.ID, versions[0].ID)
	if err != nil || v.Via != "wiki" || v.Content != "second body from the plugin" {
		t.Fatalf("GetVersion = %+v, %v", v, err)
	}
}
