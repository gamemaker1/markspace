package app

import (
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// itBody reads the current body of a note by id.
func itBody(t *testing.T, h *rharness, sp *core.Space, id string) string {
	t.Helper()
	return h.body(sp, h.row(id).Path)
}

// itLabels lists the labels of a note's versions.
func itLabels(t *testing.T, h *rharness, sp *core.Space, id string) []string {
	t.Helper()
	versions, err := h.files.ListVersions(sp.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range versions {
		out = append(out, v.Label)
	}
	return out
}

func itWant(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s:\n got  %q\n want %q", what, got, want)
	}
}

func TestRewriteRenameFile(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	photos := h.folder(owner, sp, "", "photos")
	wall := h.upload(owner, sp, photos.ID, "wall.jpg", "jpg")
	n := h.note(owner, sp, "", "Page", "![[wall.jpg]] and [w](photos/wall.jpg) and [[photos/wall.jpg|the wall]]")
	untouched := h.note(owner, sp, "", "Other", "[[Page]] only")

	if _, err := h.app.RenameFile(h.ctx, owner, wall.ID, "east wall.jpg"); err != nil {
		t.Fatal(err)
	}
	itWant(t, "renamed file links", itBody(t, h, sp, n.ID),
		"![[east wall.jpg]] and [w](<photos/east wall.jpg>) and [[photos/east wall.jpg|the wall]]")
	if labels := itLabels(t, h, sp, n.ID); len(labels) != 1 || labels[0] != linkRewriteLabel {
		t.Fatalf("versions = %v", labels)
	}
	itWant(t, "untouched note", itBody(t, h, sp, untouched.ID), "[[Page]] only")
	if len(itLabels(t, h, sp, untouched.ID)) != 0 {
		t.Fatal("untouched note got a version")
	}
	back, err := h.app.ItemBacklinks(h.ctx, owner, wall.ID)
	if err != nil || len(back) != 1 || back[0].FromNote.ID != n.ID {
		t.Fatalf("backlinks after rewrite = %v, %v", back, err)
	}
}

func TestRewriteRenameNoteByTitle(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	proj := h.folder(owner, sp, "", "proj")
	survey := h.note(owner, sp, proj.ID, "Harbour survey", "")
	title := h.note(owner, sp, "", "By title", "[[Harbour survey]]")
	byPath := h.note(owner, sp, "", "By path", "[[proj/harbour-survey]] and [s](proj/harbour-survey.md#Findings)")

	newTitle := "Findings"
	got, err := h.app.UpdateNote(h.ctx, owner, survey.ID, core.NoteUpdate{Title: &newTitle})
	if err != nil || got.Path != "proj/findings.md" {
		t.Fatalf("retitle = %+v, %v", got, err)
	}
	itWant(t, "path links", itBody(t, h, sp, byPath.ID), "[[proj/findings]] and [s](proj/findings.md#Findings)")
	// Whatever the rewrite does to the name wikilink, it keeps reaching the
	// note after the title and the file name change.
	body := itBody(t, h, sp, title.ID)
	snap, _, err := h.app.loadSnapshot(h.ctx, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e := resolveTargetText(snap, h.row(title.ID).Path, body); e == nil || e.ID != survey.ID {
		t.Fatalf("title link %q no longer reaches the note", body)
	}
}

func TestRewriteMoveNoteAndItsOwnLinks(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	h.upload(owner, sp, "", "report.pdf", "%PDF")
	h.upload(owner, sp, "", "pic.png", "png")
	docs := h.folder(owner, sp, "", "docs")
	plan := h.note(owner, sp, "", "Plan", "[r](report.pdf) and ![[pic.png]] and [abs](/report.pdf)")
	linker := h.note(owner, sp, "", "Linker", "[p](plan.md) and [[Plan]]")

	if _, err := h.app.MoveItem(h.ctx, owner, plan.ID, docs.ID, 0); err != nil {
		t.Fatal(err)
	}
	itWant(t, "own links", itBody(t, h, sp, plan.ID), "[r](../report.pdf) and ![[pic.png]] and [abs](/report.pdf)")
	itWant(t, "incoming links", itBody(t, h, sp, linker.ID), "[p](docs/plan.md) and [[Plan]]")
	if labels := itLabels(t, h, sp, linker.ID); len(labels) != 1 || labels[0] != linkRewriteLabel {
		t.Fatalf("linker versions = %v", labels)
	}
}

func TestRewriteMoveAndRenameFolder(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	h.upload(owner, sp, "", "top.pdf", "%PDF")
	a := h.folder(owner, sp, "", "a")
	x := h.note(owner, sp, a.ID, "X", "[y](y.md) and [t](../top.pdf) and [[a/y]]")
	h.note(owner, sp, a.ID, "Y", "")
	root := h.note(owner, sp, "", "Root", "[x](a/x.md) and [[a/y]]")
	deep := h.folder(owner, sp, "", "deep")

	if _, err := h.app.RenameFolder(h.ctx, owner, a.ID, "b"); err != nil {
		t.Fatal(err)
	}
	itWant(t, "outside after rename", itBody(t, h, sp, root.ID), "[x](b/x.md) and [[b/y]]")
	itWant(t, "inside after rename", itBody(t, h, sp, x.ID), "[y](y.md) and [t](../top.pdf) and [[b/y]]")

	if _, err := h.app.MoveItem(h.ctx, owner, a.ID, deep.ID, 0); err != nil {
		t.Fatal(err)
	}
	// [[b/y]] still resolves by its path suffix, so it stays as written.
	itWant(t, "outside after move", itBody(t, h, sp, root.ID), "[x](deep/b/x.md) and [[b/y]]")
	itWant(t, "inside after move", itBody(t, h, sp, x.ID), "[y](y.md) and [t](../../top.pdf) and [[b/y]]")
}

func TestRewriteSkipsNotesWithoutEditAccess(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	shared := h.folder(owner, sp, "", "shared")
	img := h.upload(owner, sp, shared.ID, "s.png", "png")
	inside := h.note(owner, sp, shared.ID, "In", "![[shared/s.png]]")
	outside := h.note(owner, sp, "", "Out", "[i](shared/s.png)")
	guest := h.user("Guest", false)
	h.share(shared.ID, guest, core.AccessEdit, true)

	if _, err := h.app.RenameFile(h.ctx, guest, img.ID, "t.png"); err != nil {
		t.Fatal(err)
	}
	itWant(t, "note the guest can edit", itBody(t, h, sp, inside.ID), "![[shared/t.png]]")
	itWant(t, "note the guest cannot edit", itBody(t, h, sp, outside.ID), "[i](shared/s.png)")
	if len(itLabels(t, h, sp, outside.ID)) != 0 {
		t.Fatal("skipped note got a version")
	}
	if row := h.row(inside.ID); row.UpdatedBy != guest.UserID() {
		t.Fatalf("rewritten note updated by %q", row.UpdatedBy)
	}
}

func TestRenderResolvesItems(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	wall := h.upload(owner, sp, "", "wall.png", itPNG(t, 4, 4))
	report := h.upload(owner, sp, "", "report.pdf", "%PDF-1.4")
	h.note(owner, sp, "", "Top", "")
	h.note(owner, sp, "", "Secret", "")
	notes := h.folder(owner, sp, "", "notes")
	plan := h.note(owner, sp, notes.ID, "Plan", "![[wall.png]]\n\n![[report.pdf]]\n\n[[Secret]]\n\n[rel](../top.md)\n\n![img](../wall.png)")

	full, _, err := h.app.GetNote(h.ctx, owner, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := h.app.RenderNote(h.ctx, owner, full)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`src="/files/` + sp.Slug + `/` + wall.ID + `/wall.png"`,
		`file-card`,
		`href="/s/` + sp.Slug + `/f/report.pdf"`,
		`href="/s/` + sp.Slug + `/n/top"`,
		`href="/s/` + sp.Slug + `/n/secret"`,
	} {
		if !strings.Contains(out.HTML, want) {
			t.Fatalf("owner render lacks %s:\n%s", want, out.HTML)
		}
	}
	found := false
	for _, l := range out.Links {
		if l.ItemID == report.ID && l.Kind == core.KindFile {
			found = true
		}
	}
	if !found {
		t.Fatalf("rendered links = %+v", out.Links)
	}

	guest := h.user("Guest", false)
	h.share(plan.ID, guest, core.AccessView, false)
	gout, err := h.app.RenderNote(h.ctx, guest, full)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{wall.ID, "/n/secret", "/f/report.pdf", "/n/top"} {
		if strings.Contains(gout.HTML, leak) {
			t.Fatalf("guest render reveals %s:\n%s", leak, gout.HTML)
		}
	}
	if !strings.Contains(gout.HTML, "wikilink-new") {
		t.Fatalf("hidden wikilink not dangling:\n%s", gout.HTML)
	}

	pub := h.folder(owner, sp, "", "pub")
	pic := h.upload(owner, sp, pub.ID, "pic.png", itPNG(t, 2, 2))
	page := h.note(owner, sp, pub.ID, "Page", "![[pic.png]] [[Secret]]")
	link, err := h.app.CreatePublicLink(h.ctx, owner, core.PublicLinkInput{NoteID: pub.ID, IncludeChildren: true})
	if err != nil {
		t.Fatal(err)
	}
	linkActor, item, err := h.app.ResolvePublicItem(h.ctx, link.Token, "")
	if err != nil || item.Folder == nil || item.Folder.ID != pub.ID {
		t.Fatalf("ResolvePublicItem = %+v, %v", item, err)
	}
	pageNote, _, err := h.app.GetNote(h.ctx, linkActor, page.ID)
	if err != nil {
		t.Fatalf("link actor reads a child note: %v", err)
	}
	lout, err := h.app.RenderNote(h.ctx, linkActor, pageNote)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lout.HTML, `/p/`+link.Token+`/raw/`+pic.ID+`/pic.png`) || strings.Contains(lout.HTML, "/n/secret") {
		t.Fatalf("link actor render:\n%s", lout.HTML)
	}
	if _, _, err := h.app.ResolvePublicLink(h.ctx, link.Token, ""); err == nil {
		t.Fatal("note-only resolve accepted a folder link")
	}
}
