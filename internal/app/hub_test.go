package app

import (
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestHubDropsInsteadOfBlocking(t *testing.T) {
	hb := newHub()
	ch, cancel := hb.add(nil)
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < eventBuffer+36; i++ {
			hb.publish(core.Event{Type: "note.updated", SpaceID: "s1"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a slow subscriber")
	}
	if len(ch) != eventBuffer {
		t.Fatalf("buffered = %d, want %d", len(ch), eventBuffer)
	}
	hb.mu.Lock()
	var dropped int
	for _, s := range hb.subs {
		dropped = s.dropped
	}
	hb.mu.Unlock()
	if dropped != 36 {
		t.Fatalf("dropped = %d, want 36", dropped)
	}
}

func TestHubFiltersBySpaceAndUnsubscribes(t *testing.T) {
	hb := newHub()
	ch, cancel := hb.add(map[string]bool{"s1": true})
	hb.publish(core.Event{Type: "tree.changed", SpaceID: "s2"})
	hb.publish(core.Event{Type: "tree.changed", SpaceID: "s1"})
	if len(ch) != 1 {
		t.Fatalf("buffered = %d, want 1", len(ch))
	}
	if ev := <-ch; ev.SpaceID != "s1" {
		t.Fatalf("event = %+v", ev)
	}
	cancel()
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel open after unsubscribe")
	}
	if hb.count() != 0 {
		t.Fatalf("subscribers = %d", hb.count())
	}
	hb.publish(core.Event{Type: "tree.changed", SpaceID: "s1"})
}

func TestSubscribeFiltersByAccess(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	mine := h.space(owner, "Mine")
	other := h.user("Other", false)
	theirs := h.space(other, "Theirs")

	events, cancel, err := h.app.Subscribe(h.ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	h.note(other, theirs, "", "Hidden", "")
	h.note(owner, mine, "", "Visible", "")

	// Delivery runs on the subscription goroutine, so collect for a moment.
	got := map[string]bool{}
	deadline := time.After(300 * time.Millisecond)
collect:
	for {
		select {
		case ev := <-events:
			got[ev.SpaceID] = true
		case <-deadline:
			break collect
		}
	}
	if got[theirs.ID] || !got[mine.ID] {
		t.Fatalf("event spaces = %v", got)
	}
	if _, _, err := h.app.Subscribe(h.ctx, nil); err == nil {
		t.Fatal("anonymous subscribe allowed")
	}
}
