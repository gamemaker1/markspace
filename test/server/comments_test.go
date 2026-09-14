package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/gamemaker1/markspace/internal/anchor"
	"github.com/gamemaker1/markspace/internal/core"
)

// utf16Offset returns the UTF-16 offset of the first occurrence of sub in s.
func utf16Offset(t *testing.T, s, sub string) int {
	t.Helper()
	i := strings.Index(s, sub)
	if i < 0 {
		t.Fatalf("%q not in %q", sub, s)
	}
	return len(utf16.Encode([]rune(s[:i])))
}

// anchorRoots lists the root comments of a note.
func (h *harness) anchorRoots(noteID string) []*core.Comment {
	h.t.Helper()
	var out struct{ Items []*core.Comment }
	h.mustStatus(h.do("GET", "/api/notes/"+noteID+"/comments", nil, &out), 200, "list comments")
	return out.Items
}

// anchorRoot lists the comments of a note and returns one root.
func (h *harness) anchorRoot(noteID, id string) *core.Comment {
	h.t.Helper()
	for _, c := range h.anchorRoots(noteID) {
		if c.ID == id {
			return c
		}
	}
	h.t.Fatalf("comment %s not listed", id)
	return nil
}

// noteContent reads the current body of a note through the API.
func (h *harness) noteContent(noteID string) string {
	h.t.Helper()
	var got core.Note
	h.mustStatus(h.do("GET", "/api/notes/"+noteID, nil, &got), 200, "get note")
	return got.Content
}

// TestAnchoredComments follows anchored comments through edits made in the
// application and outside it, over HTTP with the real stack.
func TestAnchoredComments(t *testing.T) {
	h := newHarness(t)
	h.mustStatus(h.do("POST", "/api/auth/signup", map[string]string{
		"email": "admin@example.com", "name": "Ada", "password": "correct horse battery",
	}, nil), 201, "signup")
	var spaces struct{ Items []*core.Space }
	h.do("GET", "/api/spaces", nil, &spaces)
	space := spaces.Items[0]

	var note core.Note
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{
		SpaceID: space.ID, Title: "Anchors",
		Content: "# Plan\n\nShip the sync engine today.\n\nKeep the notes short.\n",
	}, &note), 201, "create note")
	body := h.noteContent(note.ID)

	comment := func(what, text string, a *core.CommentAnchor, parent string) *core.Comment {
		t.Helper()
		var c core.Comment
		h.mustStatus(h.do("POST", "/api/notes/"+note.ID+"/comments", core.CommentInput{
			Body: text, Anchor: a, ParentID: parent,
		}, &c), 201, what)
		return &c
	}
	anchorOn := func(body, sub string) *core.CommentAnchor {
		t.Helper()
		start := utf16Offset(t, body, sub)
		a, ok := anchor.Make(body, start, start+len(utf16.Encode([]rune(sub))))
		if !ok {
			t.Fatalf("no anchor for %q", sub)
		}
		return a
	}
	patch := func(what, content string) string {
		t.Helper()
		h.mustStatus(h.do("PATCH", "/api/notes/"+note.ID, core.NoteUpdate{Content: &content}, nil), 200, what)
		return h.noteContent(note.ID)
	}
	expect := func(what string, c *core.Comment, state, exact string, start int) {
		t.Helper()
		if c.AnchorState != state || c.Anchor == nil || c.Anchor.Exact != exact || c.Anchor.Start != start {
			t.Fatalf("%s: anchor %+v, state %q; want %q at %d, state %q", what, c.Anchor, c.AnchorState, exact, start, state)
		}
		if start >= 0 && c.Anchor.End != start+len(utf16.Encode([]rune(exact))) {
			t.Fatalf("%s: end %d does not match the exact text", what, c.Anchor.End)
		}
	}

	// A comment anchored to a word.
	word := comment("anchor a word", "Which engine?", anchorOn(body, "engine"), "")
	expect("created word comment", word, core.AnchorExact, "engine", utf16Offset(t, body, "engine"))
	passage := comment("anchor a passage", "Too strict", anchorOn(body, "Keep the notes short"), "")

	// Text inserted before the word shifts the stored offsets.
	body = patch("insert before", "Intro paragraph.\n\n"+body)
	expect("after insertion before", h.anchorRoot(note.ID, word.ID), core.AnchorExact, "engine", utf16Offset(t, body, "engine"))

	// Editing a word inside a passage keeps the comment on the edited text.
	body = patch("edit inside", strings.Replace(body, "notes", "entries", 1))
	expect("after editing inside", h.anchorRoot(note.ID, passage.ID), core.AnchorExact,
		"Keep the entries short", utf16Offset(t, body, "Keep the entries short"))

	// Deleting the passage orphans the comment and keeps its quote.
	body = patch("delete passage", strings.Replace(body, "sync engine ", "", 1))
	expect("after deleting the passage", h.anchorRoot(note.ID, word.ID), core.AnchorOrphaned, "engine", -1)

	// An edit outside the application is resolved on listing and written back.
	onDisk := filepath.Join(h.cfg.SpacesDir(), space.Slug, note.Path)
	raw, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("read note file: %v", err)
	}
	edited := strings.Replace(string(raw), "Intro paragraph.", "Written outside.\n\nIntro paragraph.", 1)
	if edited == string(raw) {
		t.Fatalf("note file lacks the intro:\n%s", raw)
	}
	if err := os.WriteFile(onDisk, []byte(edited), 0o644); err != nil {
		t.Fatalf("write note file: %v", err)
	}
	h.mustStatus(h.do("POST", "/api/reindex", nil, nil), 200, "reindex")
	body = h.noteContent(note.ID)
	want := utf16Offset(t, body, "Keep the entries short")
	expect("first listing after the outside edit", h.anchorRoot(note.ID, passage.ID), core.AnchorMoved, "Keep the entries short", want)
	expect("second listing after the outside edit", h.anchorRoot(note.ID, passage.ID), core.AnchorExact, "Keep the entries short", want)

	// A reply may not carry an anchor, and the text must be in the note.
	h.mustStatus(h.do("POST", "/api/notes/"+note.ID+"/comments", core.CommentInput{
		Body: "reply", ParentID: passage.ID, Anchor: anchorOn(body, "Plan"),
	}, nil), 400, "reply with an anchor")
	h.mustStatus(h.do("POST", "/api/notes/"+note.ID+"/comments", core.CommentInput{
		Body: "ghost", Anchor: &core.CommentAnchor{Exact: "warp drive", Start: 0, End: 10},
	}, nil), 400, "anchor text not in the note")
	comment("plain reply", "Agreed", nil, passage.ID)

	// An emoji before the passage shifts it by two UTF-16 units.
	before := h.anchorRoot(note.ID, passage.ID).Anchor.Start
	body = patch("emoji before", "\U0001F600"+body)
	expect("after the emoji", h.anchorRoot(note.ID, passage.ID), core.AnchorExact, "Keep the entries short", before+2)

	// Roots: anchored threads by position, then general comments, then
	// orphaned threads.
	general := comment("general comment", "Looks good overall", nil, "")
	heading := comment("anchor the heading", "Rename?", anchorOn(body, "Written outside"), "")
	roots := h.anchorRoots(note.ID)
	wantOrder := []string{heading.ID, passage.ID, general.ID, word.ID}
	if len(roots) != len(wantOrder) {
		t.Fatalf("%d roots, want %d", len(roots), len(wantOrder))
	}
	for i, id := range wantOrder {
		if roots[i].ID != id {
			t.Fatalf("root %d is %q, want order heading, passage, general, orphaned word", i, roots[i].Body)
		}
	}
	if len(roots[1].Children) != 1 || roots[1].Children[0].Anchor != nil {
		t.Fatalf("passage thread children %+v, want one reply without an anchor", roots[1].Children)
	}
}
