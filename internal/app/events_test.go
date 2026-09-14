package app

import (
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// nextEvent waits briefly for one event. ok is false on timeout, and closed
// is true when the channel closed.
func nextEvent(ch <-chan core.Event) (ev core.Event, ok, closed bool) {
	select {
	case ev, open := <-ch:
		return ev, open, !open
	case <-time.After(300 * time.Millisecond):
		return core.Event{}, false, false
	}
}

func TestEventStreamFiltersSharedOnlySpaces(t *testing.T) {
	old := eventFreshness
	eventFreshness = 0
	t.Cleanup(func() { eventFreshness = old })

	h := newRealHarness(t)
	owner := h.user("Owner", false)
	guest := h.user("Guest", false)
	sp := h.space(owner, "Docs")
	shared := h.note(owner, sp, "", "Shared", "s")
	hidden := h.note(owner, sp, "", "Hidden", "h")
	h.share(shared.ID, guest, core.AccessView, false)

	events, stop, err := h.app.Subscribe(h.ctx, guest)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stop()

	h.app.publish("note.updated", sp.ID, hidden.ID, owner.UserID(), hidden.Path)
	h.app.publish("note.updated", sp.ID, shared.ID, owner.UserID(), shared.Path)
	ev, ok, _ := nextEvent(events)
	if !ok || ev.NoteID != shared.ID {
		t.Fatalf("first delivered event = %+v, %v; want the shared note only", ev, ok)
	}
	if ev, ok, _ := nextEvent(events); ok {
		t.Fatalf("hidden note event leaked: %+v", ev)
	}

	// Revoking the share stops events for that note on the next delivery.
	shares, err := h.db.ListSharesForNote(h.ctx, shared.ID)
	if err != nil || len(shares) != 1 {
		t.Fatalf("list shares = %v, %v", shares, err)
	}
	if err := h.db.DeleteShare(h.ctx, shares[0].ID); err != nil {
		t.Fatal(err)
	}
	h.app.publish("note.updated", sp.ID, shared.ID, owner.UserID(), shared.Path)
	if ev, ok, _ := nextEvent(events); ok {
		t.Fatalf("event delivered after the share was revoked: %+v", ev)
	}
}

// TestEventStreamItemsForShareOnlySubscriber checks folder, file and move
// events for a user who reaches the space through one folder share.
func TestEventStreamItemsForShareOnlySubscriber(t *testing.T) {
	old := eventFreshness
	eventFreshness = 0
	t.Cleanup(func() { eventFreshness = old })

	h := newRealHarness(t)
	owner := h.user("Owner", false)
	guest := h.user("Guest", false)
	sp := h.space(owner, "Docs")
	shared := h.folder(owner, sp, "", "Shared")
	hidden := h.folder(owner, sp, "", "Hidden")
	h.share(shared.ID, guest, core.AccessView, true)

	events, stop, err := h.app.Subscribe(h.ctx, guest)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stop()

	sub := h.folder(owner, sp, shared.ID, "Sub")
	inShared := h.upload(owner, sp, shared.ID, "a.txt", "a")
	inHidden := h.upload(owner, sp, hidden.ID, "b.txt", "b")
	page := h.note(owner, sp, shared.ID, "Page", "p")
	if _, err := h.app.MoveItem(h.ctx, owner, page.ID, sub.ID, 0); err != nil {
		t.Fatalf("MoveItem: %v", err)
	}
	if _, err := h.app.RenameFolder(h.ctx, owner, hidden.ID, "Hidden two"); err != nil {
		t.Fatalf("RenameFolder: %v", err)
	}
	if _, err := h.app.RenameFile(h.ctx, owner, inShared.ID, "c.txt"); err != nil {
		t.Fatalf("RenameFile: %v", err)
	}

	seen := map[string]map[string]bool{}
	deadline := time.After(700 * time.Millisecond)
collect:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break collect
			}
			id := ev.ItemID
			if id == "" {
				id = ev.NoteID
			}
			if seen[id] == nil {
				seen[id] = map[string]bool{}
			}
			seen[id][ev.Type] = true
		case <-deadline:
			break collect
		}
	}
	for _, want := range []struct {
		id, typ string
	}{
		{sub.ID, "folder.created"},
		{inShared.ID, "file.created"},
		{inShared.ID, "file.updated"},
		{page.ID, "note.created"},
		{page.ID, "item.moved"},
	} {
		if !seen[want.id][want.typ] {
			t.Errorf("missing %s for %s, saw %v", want.typ, want.id, seen)
		}
	}
	for _, id := range []string{hidden.ID, inHidden.ID} {
		if len(seen[id]) > 0 {
			t.Errorf("hidden item %s leaked events %v", id, seen[id])
		}
	}
}

func TestEventStreamClosesOnDeactivation(t *testing.T) {
	old := eventFreshness
	eventFreshness = 0
	t.Cleanup(func() { eventFreshness = old })

	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	note := h.note(owner, sp, "", "Page", "p")

	events, stop, err := h.app.Subscribe(h.ctx, owner)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stop()
	h.app.publish("note.updated", sp.ID, note.ID, owner.UserID(), note.Path)
	if _, ok, _ := nextEvent(events); !ok {
		t.Fatal("active member missed an event")
	}

	u := *owner.User
	u.IsActive = false
	if err := h.db.UpdateUser(h.ctx, &u); err != nil {
		t.Fatal(err)
	}
	h.app.publish("note.updated", sp.ID, note.ID, owner.UserID(), note.Path)
	if _, _, closed := nextEvent(events); !closed {
		t.Fatal("stream stayed open after the account was deactivated")
	}
}

func TestEventStreamClosesWhenSessionIsRevoked(t *testing.T) {
	old := eventFreshness
	eventFreshness = 0
	t.Cleanup(func() { eventFreshness = old })

	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	note := h.note(owner, sp, "", "Page", "p")

	sess := &core.Session{ID: core.NewID(), UserID: owner.UserID(), TokenHash: "hash-1",
		ExpiresAt: h.clock.Now().Add(time.Hour), CreatedAt: h.clock.Now(), LastSeen: h.clock.Now()}
	if err := h.db.CreateSession(h.ctx, sess); err != nil {
		t.Fatal(err)
	}
	actor := &core.Actor{User: owner.User, Session: sess}
	events, stop, err := h.app.Subscribe(h.ctx, actor)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stop()

	if err := h.db.DeleteSession(h.ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	h.app.publish("note.updated", sp.ID, note.ID, owner.UserID(), note.Path)
	if _, _, closed := nextEvent(events); !closed {
		t.Fatal("stream stayed open after its session was revoked")
	}
}

func TestSubscribeRefusesInactiveUser(t *testing.T) {
	h := newHarness(t)
	user := h.user("Gone", false)
	u := *user.User
	u.IsActive = false
	if err := h.db.UpdateUser(h.ctx, &u); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.app.Subscribe(h.ctx, user); err == nil {
		t.Fatal("inactive user subscribed")
	}
}
