package app

import (
	"errors"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestPluginTokenResolution(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	pu := h.pluginUser("Wiki")
	id := &core.PluginIdentity{ID: "wiki", UserID: pu.ID, Status: core.PluginStatusActive, Grants: map[string]core.Role{sp.ID: core.RoleEditor}}

	const token = core.PluginTokenPrefix + "abc"
	if _, err := h.app.ActorFromAPIToken(h.ctx, token); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("no resolver: %v", err)
	}
	r := newFakeResolver()
	r.add(token, id)
	h.app.SetPluginResolver(r)

	actor, err := h.app.ActorFromAPIToken(h.ctx, token)
	if err != nil {
		t.Fatalf("resolve plugin token: %v", err)
	}
	if actor.User.ID != pu.ID || actor.Plugin == nil || actor.Plugin.ID != "wiki" || actor.Via != "" {
		t.Fatalf("actor = %+v", actor)
	}
	if _, err := h.app.ActorFromAPIToken(h.ctx, core.PluginTokenPrefix+"unknown"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("unknown token: %v", err)
	}
	for status, ok := range map[string]bool{
		core.PluginStatusActive: true, core.PluginStatusDown: true, core.PluginStatusNeedsApproval: true,
		core.PluginStatusPaused: false, core.PluginStatusUninstalled: false,
	} {
		r.setStatus("wiki", status)
		_, err := h.app.ActorFromAPIToken(h.ctx, token)
		if ok != (err == nil) || (!ok && !errors.Is(err, core.ErrUnauthorized)) {
			t.Errorf("status %s: err %v", status, err)
		}
	}
	r.setStatus("wiki", core.PluginStatusActive)

	// A database failure is not a credential problem.
	r.err = errors.New("disk on fire")
	if _, err := h.app.ActorFromAPIToken(h.ctx, token); err == nil || errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("resolver failure = %v, want a plain error", err)
	}
	r.err = nil

	// An inactive plugin user is refused.
	pu.IsActive = false
	if err := h.db.UpdateUser(h.ctx, pu); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.ActorFromAPIToken(h.ctx, token); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("inactive plugin user: %v", err)
	}
	pu.IsActive = true
	_ = h.db.UpdateUser(h.ctx, pu)

	// A plugin whose user is a person is refused.
	person := h.user("Person", false)
	r.add(core.PluginTokenPrefix+"person", &core.PluginIdentity{ID: "odd", UserID: person.User.ID, Status: core.PluginStatusActive})
	if _, err := h.app.ActorFromAPIToken(h.ctx, core.PluginTokenPrefix+"person"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("plugin with a person user: %v", err)
	}
}

func TestActingTokenResolution(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Docs")
	pu := h.pluginUser("Wiki")
	person := h.user("Person", false)
	r := newFakeResolver()
	id := &core.PluginIdentity{ID: "wiki", UserID: pu.ID, Status: core.PluginStatusActive, Grants: map[string]core.Role{sp.ID: core.RoleEditor}}
	r.add(core.PluginTokenPrefix+"p", id)
	const token = core.ActingTokenPrefix + "xyz"
	expires := h.clock.Now().Add(10 * time.Minute)
	r.acting[token] = &core.ActingToken{Plugin: id, UserID: person.User.ID, Interaction: "command:count", ExpiresAt: expires}

	if _, err := h.app.ActorFromAPIToken(h.ctx, token); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("no resolver: %v", err)
	}
	h.app.SetPluginResolver(r)
	actor, err := h.app.ActorFromAPIToken(h.ctx, token)
	if err != nil {
		t.Fatalf("resolve acting token: %v", err)
	}
	if actor.User.ID != person.User.ID || actor.Plugin.ID != "wiki" || actor.Via != "wiki" {
		t.Fatalf("acting actor = %+v", actor)
	}
	if _, err := h.app.ActorFromAPIToken(h.ctx, core.ActingTokenPrefix+"nope"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("unknown acting token: %v", err)
	}

	refused := func(what string) {
		t.Helper()
		if _, err := h.app.ActorFromAPIToken(h.ctx, token); !errors.Is(err, core.ErrUnauthorized) {
			t.Fatalf("%s: err %v, want unauthorized", what, err)
		}
	}
	accepted := func(what string) {
		t.Helper()
		if _, err := h.app.ActorFromAPIToken(h.ctx, token); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	for status, ok := range map[string]bool{
		core.PluginStatusDown: true, core.PluginStatusNeedsApproval: true,
		core.PluginStatusPaused: false, core.PluginStatusUninstalled: false,
	} {
		r.setStatus("wiki", status)
		if ok {
			accepted("status " + status)
		} else {
			refused("status " + status)
		}
	}
	r.setStatus("wiki", core.PluginStatusActive)

	h.clock.Advance(11 * time.Minute)
	refused("expired token")
	h.clock.Advance(-11 * time.Minute)
	accepted("unexpired token")

	r.interaction = false
	refused("invalid interaction")
	r.interaction = true

	pu.IsActive = false
	_ = h.db.UpdateUser(h.ctx, pu)
	refused("inactive plugin user")
	pu.IsActive = true
	_ = h.db.UpdateUser(h.ctx, pu)

	if err := h.db.SetUserKind(h.ctx, pu.ID, core.UserKindPerson); err != nil {
		t.Fatal(err)
	}
	refused("plugin user of kind person")
	_ = h.db.SetUserKind(h.ctx, pu.ID, core.UserKindPlugin)
	accepted("plugin user restored")

	person.User.IsActive = false
	_ = h.db.UpdateUser(h.ctx, person.User)
	refused("inactive person")
	person.User.IsActive = true
	_ = h.db.UpdateUser(h.ctx, person.User)

	if err := h.db.SetUserKind(h.ctx, person.User.ID, core.UserKindPlugin); err != nil {
		t.Fatal(err)
	}
	refused("person of kind plugin")
}

func TestPluginUsersCannotSignIn(t *testing.T) {
	h := newRealHarness(t)
	admin := h.user("Admin", true)
	pu := h.pluginUser("Wiki") // has a valid password hash from the harness

	if _, _, err := h.app.Login(h.ctx, pu.Email, "password123", "ua", "127.0.0.1"); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("Login of plugin user: %v", err)
	}
	// A person with the same password signs in, so only the kind refuses.
	if _, _, err := h.app.Login(h.ctx, admin.User.Email, "password123", "ua", "127.0.0.1"); err != nil {
		t.Fatalf("Login of person: %v", err)
	}

	now := h.clock.Now()
	plainSession := "session-plugin"
	if err := h.db.CreateSession(h.ctx, &core.Session{
		ID: core.NewID(), UserID: pu.ID, TokenHash: fakeHasher{}.HashToken(plainSession),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeen: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.ActorFromSession(h.ctx, plainSession); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("session of plugin user: %v", err)
	}
	plainToken := "msk_pluginuser"
	if err := h.db.CreateAPIToken(h.ctx, &core.APIToken{
		ID: core.NewID(), UserID: pu.ID, Name: "x", TokenHash: fakeHasher{}.HashToken(plainToken), Prefix: "msk_plu", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.ActorFromAPIToken(h.ctx, plainToken); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("API token of plugin user: %v", err)
	}

	pluginToken := pluginActor(pu, "wiki", nil)
	if _, _, err := h.app.CreateAPIToken(h.ctx, pluginToken, "x", 0); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("CreateAPIToken as plugin: %v", err)
	}
	if _, _, err := h.app.CreateAPIToken(h.ctx, &core.Actor{User: pu}, "x", 0); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("CreateAPIToken for plugin user: %v", err)
	}
	person := h.user("Person", false)
	acting := actingActor(person.User, pluginActor(pu, "wiki", nil))
	if _, _, err := h.app.CreateAPIToken(h.ctx, acting, "x", 0); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("CreateAPIToken with acting token: %v", err)
	}

	yes := true
	if _, err := h.app.UpdateUser(h.ctx, admin, pu.ID, core.UserInput{IsAdmin: &yes}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("promote plugin user: %v", err)
	}
	pw := "another password"
	if _, err := h.app.UpdateUser(h.ctx, admin, pu.ID, core.UserInput{Password: &pw}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("set plugin user password: %v", err)
	}
	no := false
	if _, err := h.app.UpdateUser(h.ctx, admin, pu.ID, core.UserInput{IsActive: &no}); err != nil {
		t.Fatalf("deactivate plugin user: %v", err)
	}
	if err := h.app.DeleteUser(h.ctx, admin, pu.ID); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("delete plugin user: %v", err)
	}
}
