package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/ports"
)

// newSuggestionHarness is the real harness with the suggestion store wired.
func newSuggestionHarness(t *testing.T) *rharness {
	h := newRealHarness(t)
	h.app.suggestions = h.db.Suggestions()
	return h
}

// anchorOf anchors an ASCII passage of body.
func anchorOf(t *testing.T, body, exact string) core.CommentAnchor {
	t.Helper()
	i := strings.Index(body, exact)
	if i < 0 {
		t.Fatalf("%q not in %q", exact, body)
	}
	end := i + len(exact)
	return core.CommentAnchor{Exact: exact, Prefix: body[max(0, i-32):i], Suffix: body[end:min(len(body), end+32)], Start: i, End: end}
}

func edit(t *testing.T, body, exact, text string) core.SuggestionEditInput {
	return core.SuggestionEditInput{Anchor: anchorOf(t, body, exact), Text: text}
}

func noteBody(t *testing.T, h *rharness, actor *core.Actor, id string) *core.Note {
	t.Helper()
	n, _, err := h.app.GetNote(h.ctx, actor, id)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	return n
}

const foxBody = "The quick brown fox jumps over the lazy dog."

func TestSuggestionCreateRules(t *testing.T) {
	h := newSuggestionHarness(t)
	owner := h.user("Owner", false)
	viewer := h.user("Viewer", false)
	commenter := h.user("Commenter", false)
	sp := h.space(owner, "Docs")
	h.member(sp, viewer, core.RoleViewer)
	h.member(sp, commenter, core.RoleCommenter)
	n := h.note(owner, sp, "", "Fox", foxBody)

	in := core.SuggestionInput{NoteID: n.ID, Title: "Colour", Edits: []core.SuggestionEditInput{edit(t, foxBody, "brown", "red")}}
	if _, err := h.app.CreateSuggestion(h.ctx, viewer, in); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("viewer create: %v", err)
	}
	outsider := h.user("Outsider", false)
	if _, err := h.app.CreateSuggestion(h.ctx, outsider, in); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("outsider create: %v", err)
	}
	s, err := h.app.CreateSuggestion(h.ctx, commenter, in)
	if err != nil {
		t.Fatalf("commenter create: %v", err)
	}
	if s.Status != module.SuggestionOpen || s.AuthorKind != module.AuthorUser || s.AuthorID != commenter.UserID() || len(s.Edits) != 1 || s.Edits[0].ID != "e1" {
		t.Fatalf("created = %+v", s)
	}

	orphan := in
	orphan.Edits = []core.SuggestionEditInput{{Anchor: core.CommentAnchor{Exact: "purple", Start: 4, End: 10}, Text: "x"}}
	if _, err := h.app.CreateSuggestion(h.ctx, commenter, orphan); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("orphan edit: %v", err)
	}
	overlap := in
	overlap.Edits = []core.SuggestionEditInput{edit(t, foxBody, "quick brown", "slow"), edit(t, foxBody, "brown fox", "cat")}
	if _, err := h.app.CreateSuggestion(h.ctx, commenter, overlap); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("overlapping edits: %v", err)
	}
	empty := in
	empty.Title = " "
	if _, err := h.app.CreateSuggestion(h.ctx, commenter, empty); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("empty title: %v", err)
	}

	// A plugin token writes as the plugin, an acting token as the person.
	pu := h.pluginUser("Wiki")
	plugin := pluginActor(pu, "wiki", map[string]core.Role{sp.ID: core.RoleCommenter})
	ps, err := h.app.CreateSuggestion(h.ctx, plugin, in)
	if err != nil || ps.AuthorKind != module.AuthorPlugin || ps.AuthorID != "wiki" {
		t.Fatalf("plugin create = %+v, %v", ps, err)
	}
	as, err := h.app.CreateSuggestion(h.ctx, actingActor(commenter.User, plugin), in)
	if err != nil || as.AuthorKind != module.AuthorUser || as.AuthorID != commenter.UserID() {
		t.Fatalf("acting create = %+v, %v", as, err)
	}
	viewerPlugin := pluginActor(pu, "wiki", map[string]core.Role{sp.ID: core.RoleViewer})
	if _, err := h.app.CreateSuggestion(h.ctx, viewerPlugin, in); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("viewer plugin create: %v", err)
	}
	if _, err := h.app.CreateSuggestion(h.ctx, actingActor(viewer.User, plugin), in); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("acting for a viewer: %v", err)
	}

	list, err := h.app.ListNoteSuggestions(h.ctx, viewer, n.ID, []string{"open"})
	if err != nil || len(list) != 3 {
		t.Fatalf("ListNoteSuggestions = %d, %v", len(list), err)
	}
	if _, err := h.app.ListNoteSuggestions(h.ctx, outsider, n.ID, nil); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("outsider list: %v", err)
	}
	if _, err := h.app.GetSuggestion(h.ctx, outsider, s.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("outsider get: %v", err)
	}
}

func TestSuggestionInbox(t *testing.T) {
	h := newSuggestionHarness(t)
	owner := h.user("Owner", false)
	editor := h.user("Editor", false)
	commenter := h.user("Commenter", false)
	a := h.space(owner, "Alpha")
	b := h.space(owner, "Beta")
	h.member(a, editor, core.RoleEditor)
	h.member(a, commenter, core.RoleCommenter)
	na := h.note(owner, a, "", "A", foxBody)
	nb := h.note(owner, b, "", "B", foxBody)
	in := func(id string) core.SuggestionInput {
		return core.SuggestionInput{NoteID: id, Title: "t", Edits: []core.SuggestionEditInput{edit(t, foxBody, "lazy", "sleepy")}}
	}
	mine, err := h.app.CreateSuggestion(h.ctx, commenter, in(na.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.CreateSuggestion(h.ctx, owner, in(nb.ID)); err != nil {
		t.Fatal(err)
	}
	pu := h.pluginUser("Wiki")
	plugin := pluginActor(pu, "wiki", map[string]core.Role{a.ID: core.RoleCommenter})
	ps, err := h.app.CreateSuggestion(h.ctx, plugin, in(na.ID))
	if err != nil {
		t.Fatal(err)
	}

	ids := func(list []*core.Suggestion) map[string]bool {
		out := map[string]bool{}
		for _, s := range list {
			out[s.ID] = true
		}
		return out
	}
	got, err := h.app.SuggestionInbox(h.ctx, editor, core.SuggestionQuery{})
	if err != nil || len(got) != 2 || !ids(got)[mine.ID] || !ids(got)[ps.ID] {
		t.Fatalf("editor inbox = %d, %v", len(got), err)
	}
	if got, _ := h.app.SuggestionInbox(h.ctx, owner, core.SuggestionQuery{SpaceID: b.ID}); len(got) != 1 || got[0].NoteID != nb.ID {
		t.Fatalf("owner inbox of B = %+v", got)
	}
	if got, _ := h.app.SuggestionInbox(h.ctx, editor, core.SuggestionQuery{SpaceID: b.ID}); len(got) != 0 {
		t.Fatalf("editor inbox of an unreachable space = %d", len(got))
	}
	if got, _ := h.app.SuggestionInbox(h.ctx, commenter, core.SuggestionQuery{}); len(got) != 0 {
		t.Fatalf("commenter inbox needs edit access, got %d", len(got))
	}
	if got, _ := h.app.SuggestionInbox(h.ctx, commenter, core.SuggestionQuery{AuthorID: core.SuggestionAuthorMe}); len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("commenter own suggestions = %+v", got)
	}
	if got, _ := h.app.SuggestionInbox(h.ctx, plugin, core.SuggestionQuery{AuthorID: core.SuggestionAuthorMe, NoteID: na.ID}); len(got) != 1 || got[0].ID != ps.ID {
		t.Fatalf("plugin own suggestions = %+v", got)
	}
	if got, _ := h.app.SuggestionInbox(h.ctx, editor, core.SuggestionQuery{AuthorID: commenter.UserID()}); len(got) != 1 {
		t.Fatalf("author filter = %d", len(got))
	}
	if _, err := h.app.DiscardSuggestion(h.ctx, editor, mine.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.app.SuggestionInbox(h.ctx, editor, core.SuggestionQuery{Status: "discarded"}); len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("discarded inbox = %+v", got)
	}
	if got, _ := h.app.SuggestionInbox(h.ctx, editor, core.SuggestionQuery{Status: "all"}); len(got) != 2 {
		t.Fatalf("all statuses = %d", len(got))
	}
	if _, err := h.app.SuggestionInbox(h.ctx, editor, core.SuggestionQuery{Status: "bogus"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bogus status: %v", err)
	}
}

func TestSuggestionAcceptAndDiscard(t *testing.T) {
	h := newSuggestionHarness(t)
	owner := h.user("Owner", false)
	commenter := h.user("Commenter", false)
	sp := h.space(owner, "Docs")
	h.member(sp, commenter, core.RoleCommenter)
	n := h.note(owner, sp, "", "Fox", foxBody)

	events, stop, err := h.app.Subscribe(h.ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	s, err := h.app.CreateSuggestion(h.ctx, commenter, core.SuggestionInput{NoteID: n.ID, Title: "Two", Edits: []core.SuggestionEditInput{
		edit(t, foxBody, "brown", "red"), edit(t, foxBody, "lazy", "sleepy"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	waitSuggestionEvent(t, events, "suggestion.created", s.ID)

	if _, err := h.app.AcceptSuggestion(h.ctx, commenter, s.ID, nil); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("commenter accept: %v", err)
	}
	before, _ := h.app.ListVersions(h.ctx, owner, n.ID)
	h.clock.Advance(time.Second)
	res, err := h.app.AcceptSuggestion(h.ctx, owner, s.ID, []string{"e1"})
	if err != nil {
		t.Fatalf("accept e1: %v", err)
	}
	if res.Note == nil || res.Note.Content != "The quick red fox jumps over the lazy dog." {
		t.Fatalf("note after accept = %+v", res.Note)
	}
	if res.Suggestion.Status != module.SuggestionOpen || res.Suggestion.Edits[0].Status != module.EditAccepted {
		t.Fatalf("suggestion after partial accept = %+v", res.Suggestion)
	}
	after, _ := h.app.ListVersions(h.ctx, owner, n.ID)
	if len(after) != len(before)+1 {
		t.Fatalf("versions %d -> %d, want one more", len(before), len(after))
	}
	waitSuggestionEvent(t, events, "suggestion.accepted", s.ID)
	if disk := h.body(sp, n.Path); disk != res.Note.Content {
		t.Fatalf("disk body = %q", disk)
	}
	if _, err := h.app.AcceptSuggestion(h.ctx, owner, s.ID, []string{"e1"}); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("accept of an accepted edit: %v", err)
	}
	d, err := h.app.DiscardSuggestion(h.ctx, owner, s.ID, nil)
	if err != nil || d.Status != module.SuggestionPartial || d.ResolvedBy != owner.UserID() {
		t.Fatalf("discard rest = %+v, %v", d, err)
	}
	waitSuggestionEvent(t, events, "suggestion.discarded", s.ID)
	if _, err := h.app.AcceptSuggestion(h.ctx, owner, s.ID, nil); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("accept of a resolved suggestion: %v", err)
	}
	if _, err := h.app.DiscardSuggestion(h.ctx, owner, s.ID, nil); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("discard of a resolved suggestion: %v", err)
	}

	// Accept all at once.
	body := noteBody(t, h, owner, n.ID).Content
	all, err := h.app.CreateSuggestion(h.ctx, commenter, core.SuggestionInput{NoteID: n.ID, Title: "All", Edits: []core.SuggestionEditInput{
		edit(t, body, "quick", "slow"), edit(t, body, "dog.", "cat."),
	}})
	if err != nil {
		t.Fatal(err)
	}
	res, err = h.app.AcceptSuggestion(h.ctx, owner, all.ID, nil)
	if err != nil || res.Suggestion.Status != module.SuggestionAccepted || res.Note.Content != "The slow red fox jumps over the lazy cat." {
		t.Fatalf("accept all = %+v, %v", res, err)
	}
	if _, err := h.app.AcceptSuggestion(h.ctx, owner, all.ID, nil); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("second accept: %v", err)
	}
}

func waitSuggestionEvent(t *testing.T, events <-chan core.Event, typ, id string) {
	t.Helper()
	for {
		ev, ok, _ := nextEvent(events)
		if !ok {
			t.Fatalf("no %s event for %s", typ, id)
		}
		if ev.Type != typ {
			continue
		}
		var d struct {
			SuggestionID string `json:"suggestionId"`
		}
		if json.Unmarshal(ev.Data, &d) == nil && d.SuggestionID == id {
			return
		}
	}
}

func TestSuggestionRemapAndStale(t *testing.T) {
	h := newSuggestionHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Fox", foxBody)
	s, err := h.app.CreateSuggestion(h.ctx, owner, core.SuggestionInput{NoteID: n.ID, Title: "Colour", Edits: []core.SuggestionEditInput{
		edit(t, foxBody, "brown", "red"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := h.app.CreateSuggestion(h.ctx, owner, core.SuggestionInput{NoteID: n.ID, Title: "Dog", Edits: []core.SuggestionEditInput{
		edit(t, foxBody, "lazy dog", "cat"),
	}})
	if err != nil {
		t.Fatal(err)
	}

	prefixed := "Hello there. The quick brown fox jumps over."
	h.clock.Advance(time.Second)
	if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: &prefixed}); err != nil {
		t.Fatal(err)
	}
	remapped, err := h.app.GetSuggestion(h.ctx, owner, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a := remapped.Edits[0].Anchor; a.Start != strings.Index(prefixed, "brown") || a.Exact != "brown" {
		t.Fatalf("anchor after edit = %+v", a)
	}
	stale, _ := h.app.GetSuggestion(h.ctx, owner, gone.ID)
	if stale.Status != module.SuggestionStale || stale.Edits[0].Status != module.EditOrphaned {
		t.Fatalf("suggestion on a deleted passage = %+v", stale)
	}
	res, err := h.app.AcceptSuggestion(h.ctx, owner, s.ID, nil)
	if err != nil || res.Note.Content != "Hello there. The quick red fox jumps over." {
		t.Fatalf("accept after remap = %+v, %v", res, err)
	}

	// Restoring the passage brings the stale suggestion back.
	h.clock.Advance(time.Second)
	restored := res.Note.Content + " The lazy dog."
	if _, err := h.app.UpdateNote(h.ctx, owner, n.ID, core.NoteUpdate{Content: &restored}); err != nil {
		t.Fatal(err)
	}
	back, _ := h.app.GetSuggestion(h.ctx, owner, gone.ID)
	if back.Status != module.SuggestionOpen {
		t.Fatalf("suggestion after the passage returned = %+v", back)
	}
}

// conflictStore makes the first UpdateSuggestion of one id lose a race
// against another writer.
type conflictStore struct {
	ports.SuggestionDB
	mu      sync.Mutex
	id      string
	raced   bool
	updates int
}

func (c *conflictStore) UpdateSuggestion(ctx context.Context, s *ports.StoredSuggestion, now time.Time) error {
	c.mu.Lock()
	race := s.ID == c.id && !c.raced
	c.raced = c.raced || race
	c.updates++
	c.mu.Unlock()
	if race {
		other, err := c.SuggestionDB.GetSuggestion(ctx, s.ID)
		if err != nil {
			return err
		}
		if err := c.SuggestionDB.UpdateSuggestion(ctx, other, now); err != nil {
			return err
		}
	}
	return c.SuggestionDB.UpdateSuggestion(ctx, s, now)
}

func TestSuggestionAcceptSurvivesConcurrentWrite(t *testing.T) {
	h := newSuggestionHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	n := h.note(owner, sp, "", "Fox", foxBody)
	s, err := h.app.CreateSuggestion(h.ctx, owner, core.SuggestionInput{NoteID: n.ID, Title: "Two", Edits: []core.SuggestionEditInput{
		edit(t, foxBody, "brown", "red"), edit(t, foxBody, "lazy", "sleepy"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	store := &conflictStore{SuggestionDB: h.db.Suggestions(), id: s.ID}
	h.app.suggestions = store
	res, err := h.app.AcceptSuggestion(h.ctx, owner, s.ID, []string{"e2"})
	if err != nil {
		t.Fatalf("accept with a concurrent write: %v", err)
	}
	if !store.raced {
		t.Fatal("the race did not happen")
	}
	stored, err := h.db.Suggestions().GetSuggestion(h.ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Edits[1].Status != module.EditAccepted || stored.Edits[0].Status != module.EditOpen || stored.Version < 3 {
		t.Fatalf("stored after retry = %+v (version %d)", stored.Edits, stored.Version)
	}
	if res.Note.Content != "The quick brown fox jumps over the sleepy dog." {
		t.Fatalf("body = %q", res.Note.Content)
	}
	// The open edit still applies at its place after the retry.
	res, err = h.app.AcceptSuggestion(h.ctx, owner, s.ID, nil)
	if err != nil || res.Note.Content != "The quick red fox jumps over the sleepy dog." || res.Suggestion.Status != module.SuggestionAccepted {
		t.Fatalf("accept rest = %+v, %v", res, err)
	}
}

func TestSuggestionWithdraw(t *testing.T) {
	h := newSuggestionHarness(t)
	owner := h.user("Owner", false)
	commenter := h.user("Commenter", false)
	sp := h.space(owner, "Docs")
	h.member(sp, commenter, core.RoleCommenter)
	n := h.note(owner, sp, "", "Fox", foxBody)
	in := core.SuggestionInput{NoteID: n.ID, Title: "t", Edits: []core.SuggestionEditInput{edit(t, foxBody, "fox", "cat")}}
	mine, _ := h.app.CreateSuggestion(h.ctx, commenter, in)
	pu := h.pluginUser("Wiki")
	plugin := pluginActor(pu, "wiki", map[string]core.Role{sp.ID: core.RoleCommenter})
	ps, err := h.app.CreateSuggestion(h.ctx, plugin, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.app.WithdrawSuggestion(h.ctx, owner, mine.ID); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("owner withdraws another's: %v", err)
	}
	if err := h.app.WithdrawSuggestion(h.ctx, commenter, ps.ID); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("person withdraws a plugin suggestion: %v", err)
	}
	if err := h.app.WithdrawSuggestion(h.ctx, actingActor(commenter.User, plugin), ps.ID); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("acting token withdraws a plugin suggestion: %v", err)
	}
	other := pluginActor(pu, "other", map[string]core.Role{sp.ID: core.RoleCommenter})
	if err := h.app.WithdrawSuggestion(h.ctx, other, ps.ID); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("another plugin withdraws: %v", err)
	}
	if err := h.app.WithdrawSuggestion(h.ctx, plugin, ps.ID); err != nil {
		t.Fatalf("plugin withdraws its own: %v", err)
	}
	if err := h.app.WithdrawSuggestion(h.ctx, commenter, mine.ID); err != nil {
		t.Fatalf("author withdraws: %v", err)
	}
	if _, err := h.app.GetSuggestion(h.ctx, owner, mine.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("withdrawn suggestion still readable: %v", err)
	}
}

func TestSuggestionsDeletedWithNoteFolderAndSpace(t *testing.T) {
	h := newSuggestionHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	folder := h.folder(owner, sp, "", "Box")
	loose := h.note(owner, sp, "", "Loose", foxBody)
	inside := h.note(owner, sp, folder.ID, "Inside", foxBody)
	other := h.space(owner, "Other")
	far := h.note(owner, other, "", "Far", foxBody)
	create := func(id string) *core.Suggestion {
		s, err := h.app.CreateSuggestion(h.ctx, owner, core.SuggestionInput{NoteID: id, Title: "t", Edits: []core.SuggestionEditInput{edit(t, foxBody, "fox", "cat")}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	sl, si, sf := create(loose.ID), create(inside.ID), create(far.ID)
	store := h.db.Suggestions()
	exists := func(id string) bool {
		_, err := store.GetSuggestion(h.ctx, id)
		return err == nil
	}
	if err := h.app.DeleteItem(h.ctx, owner, loose.ID); err != nil {
		t.Fatal(err)
	}
	if exists(sl.ID) {
		t.Fatal("suggestion of a deleted note remains")
	}
	if err := h.app.DeleteItem(h.ctx, owner, folder.ID); err != nil {
		t.Fatal(err)
	}
	if exists(si.ID) {
		t.Fatal("suggestion of a note in a deleted folder remains")
	}
	if !exists(sf.ID) {
		t.Fatal("suggestion in another space was removed")
	}
	if err := h.app.DeleteSpace(h.ctx, owner, other.ID); err != nil {
		t.Fatal(err)
	}
	if exists(sf.ID) {
		t.Fatal("suggestion of a deleted space remains")
	}
}

func TestSuggestionsUnavailableWithoutStore(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	if _, err := h.app.SuggestionInbox(h.ctx, owner, core.SuggestionQuery{}); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("inbox without store: %v", err)
	}
}
