package app

import (
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestEventFilterPluginActor(t *testing.T) {
	old := eventFreshness
	eventFreshness = 0
	t.Cleanup(func() { eventFreshness = old })

	h := newRealHarness(t)
	owner := h.user("Owner", false)
	a := h.space(owner, "Alpha")
	b := h.space(owner, "Beta")
	na := h.note(owner, a, "", "A", "a")
	nb := h.note(owner, b, "", "B", "b")
	pu := h.pluginUser("Wiki")
	plugin := pluginActor(pu, "wiki", map[string]core.Role{a.ID: core.RoleViewer})
	r := newFakeResolver()
	r.add(core.PluginTokenPrefix+"t", plugin.Plugin)
	h.app.SetPluginResolver(r)

	f, err := h.app.NewEventFilter(h.ctx, plugin)
	if err != nil {
		t.Fatalf("NewEventFilter: %v", err)
	}
	check := func(what string, ev core.Event, want bool) {
		t.Helper()
		if got := f.Allows(h.ctx, ev); got != want {
			t.Errorf("%s: allows = %v, want %v", what, got, want)
		}
	}
	noteEv := func(sp *core.Space, n *core.Note) core.Event {
		return core.Event{Type: "note.updated", SpaceID: sp.ID, NoteID: n.ID, ItemID: n.ID, Kind: core.KindNote, Path: n.Path}
	}
	check("granted space", noteEv(a, na), true)
	check("ungranted space", noteEv(b, nb), false)
	check("global item event", core.Event{Type: "note.updated", ItemID: na.ID}, false)
	check("global event without item", core.Event{Type: "plugin.other.ping"}, true)
	check("audience without the plugin user", core.Event{Type: "plugin.notify", SpaceID: a.ID, Audience: []string{owner.UserID()}}, false)
	check("audience with the plugin user", core.Event{Type: "plugin.notify", SpaceID: a.ID, Audience: []string{pu.ID}}, true)
	check("job of another user", core.Event{Type: "job.updated", SpaceID: a.ID, ActorID: owner.UserID()}, false)

	// A grant change shows after a refresh.
	r.setGrants("wiki", map[string]core.Role{b.ID: core.RoleEditor})
	check("granted space after the grant moved", noteEv(a, na), false)
	check("new grant", noteEv(b, nb), true)

	for _, status := range []string{core.PluginStatusPaused, core.PluginStatusUninstalled} {
		r.setStatus("wiki", status)
		check("plugin "+status, noteEv(b, nb), false)
	}
	r.setStatus("wiki", core.PluginStatusActive)
	check("plugin active again", noteEv(b, nb), true)
}

func TestEventFilterJobsAndAudience(t *testing.T) {
	old := eventFreshness
	eventFreshness = 0
	t.Cleanup(func() { eventFreshness = old })

	h := newRealHarness(t)
	admin := h.user("Admin", true)
	alice := h.user("Alice", false)
	bob := h.user("Bob", false)
	sp := h.space(alice, "Docs")
	h.member(sp, bob, core.RoleEditor)

	job := core.Event{Type: "job.updated", SpaceID: sp.ID, ActorID: alice.UserID()}
	plugJob := core.Event{Type: "job.updated", SpaceID: sp.ID}
	for _, tc := range []struct {
		actor *core.Actor
		ev    core.Event
		want  bool
	}{
		{admin, job, true}, {alice, job, true}, {bob, job, false},
		{admin, plugJob, true}, {alice, plugJob, false},
		{bob, core.Event{Type: "plugin.notify", SpaceID: sp.ID, Audience: []string{alice.UserID()}}, false},
		{alice, core.Event{Type: "plugin.notify", SpaceID: sp.ID, Audience: []string{alice.UserID()}}, true},
		{admin, core.Event{Type: "plugin.notify", Audience: []string{alice.UserID()}}, false},
		{bob, core.Event{Type: "plugin.notify", SpaceID: sp.ID}, true},
	} {
		f, err := h.app.NewEventFilter(h.ctx, tc.actor)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.Allows(h.ctx, tc.ev); got != tc.want {
			t.Errorf("%s sees %+v = %v, want %v", tc.actor.User.Name, tc.ev, got, tc.want)
		}
	}

	// The event stream applies the same rules to a plugin subscriber.
	pu := h.pluginUser("Wiki")
	plugin := pluginActor(pu, "wiki", map[string]core.Role{sp.ID: core.RoleViewer})
	r := newFakeResolver()
	r.add(core.PluginTokenPrefix+"t", plugin.Plugin)
	h.app.SetPluginResolver(r)
	events, stop, err := h.app.Subscribe(h.ctx, plugin)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer stop()
	other := h.space(alice, "Other")
	hidden := h.note(alice, other, "", "Hidden", "h")
	shown := h.note(alice, sp, "", "Shown", "s")
	h.app.publish("note.updated", other.ID, hidden.ID, alice.UserID(), hidden.Path)
	h.app.publish("note.updated", sp.ID, shown.ID, alice.UserID(), shown.Path)
	for {
		ev, ok, _ := nextEvent(events)
		if !ok {
			t.Fatal("the granted note event did not arrive")
		}
		if ev.SpaceID != sp.ID {
			t.Fatalf("plugin stream leaked %+v", ev)
		}
		if ev.NoteID == shown.ID && ev.Type == "note.updated" {
			break
		}
	}
	// Uninstall closes the stream.
	r.setStatus("wiki", core.PluginStatusUninstalled)
	h.app.publish("note.updated", sp.ID, shown.ID, alice.UserID(), shown.Path)
	for {
		ev, ok, closed := nextEvent(events)
		if closed {
			break
		}
		if !ok {
			t.Fatal("stream stayed open after uninstall")
		}
		if ev.NoteID == shown.ID && ev.Type == "note.updated" {
			t.Fatalf("event after uninstall: %+v", ev)
		}
	}
}
