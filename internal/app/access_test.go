package app

import (
	"errors"
	"sort"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestItemGrant(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	guide := h.folder(owner, sp, "", "Guide")
	child := h.note(owner, sp, guide.ID, "Install", "")
	deep := h.folder(owner, sp, guide.ID, "Deep")
	grandchild := h.note(owner, sp, deep.ID, "Docker", "")
	pic := h.upload(owner, sp, deep.ID, "shot.png", "png")
	other := h.note(owner, sp, "", "Other", "")
	look := h.folder(owner, sp, "", "Guidebook")
	lookalike := h.note(owner, sp, look.ID, "Inside", "")
	manualNote := h.note(owner, sp, "", "Manual", "")
	manualPage := h.note(owner, sp, manualNote.ID, "Page", "")
	manualNote = h.row(manualNote.ID)
	if manualNote.Path != "manual/index.md" {
		t.Fatalf("folder note path = %s", manualNote.Path)
	}
	manual, err := h.db.GetFolderByPath(h.ctx, sp.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}

	editor := h.user("Editor", false)
	h.member(sp, editor, core.RoleEditor)
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)
	boosted := h.user("Boosted", false)
	h.member(sp, boosted, core.RoleViewer)
	h.share(child.ID, boosted, core.AccessEdit, false)
	admin := h.user("Admin", true)
	direct := h.user("Direct", false)
	h.share(child.ID, direct, core.AccessComment, false)
	tree := h.user("Tree", false)
	h.share(guide.ID, tree, core.AccessEdit, true)
	flat := h.user("Flat", false)
	h.share(guide.ID, flat, core.AccessEdit, false)
	folderNote := h.user("FolderNote", false)
	h.share(manualNote.ID, folderNote, core.AccessView, true)
	plainNote := h.user("PlainNote", false)
	h.share(other.ID, plainNote, core.AccessView, true)
	stranger := h.user("Stranger", false)

	link := &core.Actor{Link: &core.PublicLink{ID: "l1", NoteID: guide.ID, SpaceID: sp.ID, Access: core.AccessView}}
	linkKids := &core.Actor{Link: &core.PublicLink{ID: "l2", NoteID: guide.ID, SpaceID: sp.ID, Access: core.AccessManage, IncludeChildren: true}}

	cases := []struct {
		name   string
		actor  *core.Actor
		id     string
		want   core.Access
		source string
	}{
		{"space owner", owner, grandchild.ID, core.AccessManage, "space"},
		{"space editor", editor, other.ID, core.AccessEdit, "space"},
		{"space viewer on a file", viewer, pic.ID, core.AccessView, "space"},
		{"instance admin", admin, deep.ID, core.AccessManage, "admin"},
		{"direct share on the note", direct, child.ID, core.AccessComment, "share"},
		{"direct share does not reach the folder", direct, guide.ID, core.AccessNone, "none"},
		{"folder share covers a child note", tree, child.ID, core.AccessEdit, "inherited-share"},
		{"folder share covers a subfolder", tree, deep.ID, core.AccessEdit, "inherited-share"},
		{"folder share covers a grandchild", tree, grandchild.ID, core.AccessEdit, "inherited-share"},
		{"folder share covers a file", tree, pic.ID, core.AccessEdit, "inherited-share"},
		{"folder share on the folder itself", tree, guide.ID, core.AccessEdit, "share"},
		{"folder share skips a sibling", tree, other.ID, core.AccessNone, "none"},
		{"folder share skips a path lookalike", tree, lookalike.ID, core.AccessNone, "none"},
		{"share without children skips a child", flat, child.ID, core.AccessNone, "none"},
		{"share without children opens the folder", flat, guide.ID, core.AccessEdit, "share"},
		{"folder note share covers its folder", folderNote, manual.ID, core.AccessView, "inherited-share"},
		{"folder note share covers a sibling page", folderNote, manualPage.ID, core.AccessView, "inherited-share"},
		{"folder note share skips another folder", folderNote, guide.ID, core.AccessNone, "none"},
		{"plain note share has nothing below it", plainNote, lookalike.ID, core.AccessNone, "none"},
		{"share beats a weaker role", boosted, child.ID, core.AccessEdit, "share"},
		{"weaker share keeps the role", boosted, other.ID, core.AccessView, "space"},
		{"link on its target", link, guide.ID, core.AccessView, "link"},
		{"link without children skips a child", link, child.ID, core.AccessNone, "none"},
		{"link with children covers a file, capped", linkKids, pic.ID, core.AccessComment, "link"},
		{"link with children skips a sibling", linkKids, other.ID, core.AccessNone, "none"},
		{"stranger", stranger, other.ID, core.AccessNone, "none"},
		{"nil actor", nil, other.ID, core.AccessNone, "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it := h.item(tc.id)
			g, err := h.app.grants(tc.actor).item(h.ctx, refOf(it))
			if err != nil {
				t.Fatalf("item grant: %v", err)
			}
			if g == nil {
				t.Fatal("grant is nil")
			}
			if g.Access != tc.want || g.Source != tc.source {
				t.Fatalf("grant = %s/%s, want %s/%s", g.Access, g.Source, tc.want, tc.source)
			}
			if it.Note != nil {
				ng, err := h.app.noteGrant(h.ctx, tc.actor, it.Note)
				if err != nil || ng.Access != g.Access {
					t.Fatalf("noteGrant = %v, %v, want %s", grantAccess(ng), err, g.Access)
				}
			}
		})
	}

	t.Run("space grant of a link actor is none", func(t *testing.T) {
		g, err := h.app.spaceGrant(h.ctx, linkKids, sp.ID)
		if err != nil || g.Access != core.AccessNone {
			t.Fatalf("spaceGrant = %+v, %v", g, err)
		}
	})
	t.Run("space grant carries the role", func(t *testing.T) {
		g, err := h.app.spaceGrant(h.ctx, editor, sp.ID)
		if err != nil || g.Role != core.RoleEditor || g.Source != "space" {
			t.Fatalf("spaceGrant = %+v, %v", g, err)
		}
	})
}

func TestRequireNote(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Plan", "body")
	gone := h.note(owner, sp, "", "Gone", "body")
	if err := h.app.TrashNote(h.ctx, owner, gone.ID); err != nil {
		t.Fatalf("trash: %v", err)
	}
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)
	stranger := h.user("Stranger", false)
	otherLink := &core.Actor{Link: &core.PublicLink{NoteID: "elsewhere", SpaceID: sp.ID, Access: core.AccessView}}

	cases := []struct {
		name    string
		actor   *core.Actor
		id      string
		want    core.Access
		wantErr error
	}{
		{"owner edits", owner, n.ID, core.AccessEdit, nil},
		{"viewer views", viewer, n.ID, core.AccessView, nil},
		{"viewer edit is forbidden", viewer, n.ID, core.AccessEdit, core.ErrForbidden},
		{"stranger gets not found", stranger, n.ID, core.AccessView, core.ErrNotFound},
		{"nil actor is unauthorized", nil, n.ID, core.AccessView, core.ErrUnauthorized},
		{"empty actor is unauthorized", &core.Actor{}, n.ID, core.AccessView, core.ErrUnauthorized},
		{"link to another note gets not found", otherLink, n.ID, core.AccessView, core.ErrNotFound},
		{"missing note for a user", owner, "missing", core.AccessView, core.ErrNotFound},
		{"missing note for anonymous", nil, "missing", core.AccessView, core.ErrUnauthorized},
		{"trashed note is readable", owner, gone.ID, core.AccessView, nil},
		{"trashed note is not editable", owner, gone.ID, core.AccessEdit, core.ErrConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			note, grant, err := h.app.requireNote(h.ctx, tc.actor, tc.id, tc.want)
			item, igrant, ierr := h.app.requireItem(h.ctx, tc.actor, tc.id, tc.want)
			if tc.wantErr == nil {
				if err != nil || note == nil || grant == nil {
					t.Fatalf("requireNote = %v, %v, %v", note, grant, err)
				}
				if ierr != nil || item == nil || igrant.Access != grant.Access {
					t.Fatalf("requireItem = %v, %v, %v", item, igrant, ierr)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if !errors.Is(ierr, tc.wantErr) {
				t.Fatalf("requireItem err = %v, want %v", ierr, tc.wantErr)
			}
			if tc.wantErr == core.ErrNotFound && errors.Is(err, core.ErrForbidden) {
				t.Fatal("not found must not also be forbidden")
			}
		})
	}
}

func TestRequireSpace(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	viewer := h.user("Viewer", false)
	h.member(sp, viewer, core.RoleViewer)
	stranger := h.user("Stranger", false)
	admin := h.user("Admin", true)

	cases := []struct {
		name    string
		actor   *core.Actor
		want    core.Access
		wantErr error
	}{
		{"owner manages", owner, core.AccessManage, nil},
		{"admin manages", admin, core.AccessManage, nil},
		{"viewer views", viewer, core.AccessView, nil},
		{"viewer cannot manage", viewer, core.AccessManage, core.ErrForbidden},
		{"stranger gets not found", stranger, core.AccessView, core.ErrNotFound},
		{"anonymous is unauthorized", nil, core.AccessView, core.ErrUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := h.app.requireSpace(h.ctx, tc.actor, sp.ID, tc.want)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("err = %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRequireAdmin(t *testing.T) {
	h := newRealHarness(t)
	if err := requireAdmin(nil); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("nil actor: %v", err)
	}
	if err := requireAdmin(h.user("Normal", false)); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("normal user: %v", err)
	}
	if err := requireAdmin(h.user("Admin", true)); err != nil {
		t.Fatalf("admin: %v", err)
	}
}

func TestAccessibleSpaceIDs(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	a := h.space(owner, "Alpha")
	b := h.space(owner, "Beta")
	c := h.space(owner, "Gamma")
	d := h.space(owner, "Delta")
	shared := h.note(owner, b, "", "Shared", "")
	folder := h.folder(owner, d, "", "Box")
	reader := h.user("Reader", false)
	h.member(a, reader, core.RoleViewer)
	h.share(shared.ID, reader, core.AccessView, false)
	h.share(folder.ID, reader, core.AccessView, true)

	cases := []struct {
		name  string
		actor *core.Actor
		want  []string
	}{
		{"admin sees every space", h.user("Admin", true), []string{a.ID, b.ID, c.ID, d.ID}},
		{"member plus spaces of shared items", reader, []string{a.ID, b.ID, d.ID}},
		{"link actor sees none", &core.Actor{Link: &core.PublicLink{NoteID: shared.ID, SpaceID: b.ID}}, nil},
		{"anonymous sees none", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.app.accessibleSpaceIDs(h.ctx, tc.actor)
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if len(got) != len(want) {
				t.Fatalf("got %v, want %v", got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("got %v, want %v", got, want)
				}
			}
		})
	}
}
