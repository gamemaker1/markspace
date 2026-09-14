package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// fakeResolver is a core.PluginResolver over in-memory maps.
type fakeResolver struct {
	mu          sync.Mutex
	tokens      map[string]*core.PluginIdentity // plugin token -> identity
	byID        map[string]*core.PluginIdentity
	acting      map[string]*core.ActingToken
	interaction bool
	err         error
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{
		tokens: map[string]*core.PluginIdentity{}, byID: map[string]*core.PluginIdentity{},
		acting: map[string]*core.ActingToken{}, interaction: true,
	}
}

func (r *fakeResolver) add(token string, id *core.PluginIdentity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens[token] = id
	r.byID[id.ID] = id
}

func (r *fakeResolver) setStatus(pluginID, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id := r.byID[pluginID]; id != nil {
		id.Status = status
	}
}

func (r *fakeResolver) setGrants(pluginID string, grants map[string]core.Role) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id := r.byID[pluginID]; id != nil {
		id.Grants = grants
	}
}

func (r *fakeResolver) ResolvePluginToken(_ context.Context, token string) (*core.PluginIdentity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	id, ok := r.tokens[token]
	if !ok {
		return nil, core.ErrUnauthorized
	}
	return cloneIdentity(id), nil
}

func (r *fakeResolver) ResolveActingToken(_ context.Context, token string) (*core.ActingToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	at, ok := r.acting[token]
	if !ok {
		return nil, core.ErrUnauthorized
	}
	cp := *at
	cp.Plugin = cloneIdentity(r.byID[at.Plugin.ID])
	return &cp, nil
}

func (r *fakeResolver) PluginIdentity(_ context.Context, pluginID string) (*core.PluginIdentity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byID[pluginID]
	if !ok || id.Status == core.PluginStatusUninstalled {
		return nil, core.ErrNotFound
	}
	return cloneIdentity(id), nil
}

func (r *fakeResolver) ValidInteraction(context.Context, string, string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.interaction
}

func cloneIdentity(id *core.PluginIdentity) *core.PluginIdentity {
	cp := *id
	cp.Grants = map[string]core.Role{}
	for k, v := range id.Grants {
		cp.Grants[k] = v
	}
	cp.Permissions = slices.Clone(id.Permissions)
	return &cp
}

// pluginUser creates an active user of kind plugin.
func (h *rharness) pluginUser(name string) *core.User {
	h.t.Helper()
	a := h.user(name, false)
	if err := h.db.SetUserKind(h.ctx, a.User.ID, core.UserKindPlugin); err != nil {
		h.t.Fatalf("set user kind: %v", err)
	}
	return a.User
}

// pluginActor returns a plugin token actor with the given grants.
func pluginActor(u *core.User, id string, grants map[string]core.Role) *core.Actor {
	return &core.Actor{User: u, Plugin: &core.PluginIdentity{
		ID: id, UserID: u.ID, Status: core.PluginStatusActive, Grants: grants,
		Permissions: []string{"items:read", "items:write", "suggestions"},
	}}
}

// actingActor returns an acting token actor for a person through a plugin.
func actingActor(person *core.User, plugin *core.Actor) *core.Actor {
	return &core.Actor{User: person, Plugin: plugin.Plugin, Via: plugin.Plugin.ID}
}

func accessOf(t *testing.T, h *rharness, actor *core.Actor, id string) core.Access {
	t.Helper()
	it, err := h.app.GetItem(h.ctx, actor, id)
	if errors.Is(err, core.ErrNotFound) {
		return core.AccessNone
	}
	if err != nil {
		t.Fatalf("GetItem(%s): %v", id, err)
	}
	return it.Access
}

func TestPluginTokenAccess(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	a := h.space(owner, "Alpha")
	b := h.space(owner, "Beta")
	na := h.note(owner, a, "", "Alpha note", "alpha words")
	nb := h.note(owner, b, "", "Beta note", "beta words")
	pu := h.pluginUser("Wiki")
	plugin := pluginActor(pu, "wiki", map[string]core.Role{a.ID: core.RoleEditor})
	// A share and a membership of the plugin user must not count.
	h.share(nb.ID, &core.Actor{User: pu}, core.AccessManage, false)
	h.member(b, &core.Actor{User: pu}, core.RoleOwner)

	if got := accessOf(t, h, plugin, na.ID); got != core.AccessEdit {
		t.Fatalf("granted note access = %s, want edit", got)
	}
	if got := accessOf(t, h, plugin, nb.ID); got != core.AccessNone {
		t.Fatalf("ungranted note access = %s, want none", got)
	}
	if _, _, err := h.app.requireItem(h.ctx, plugin, na.ID, core.AccessManage); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("manage on granted note: %v, want forbidden", err)
	}
	if _, _, err := h.app.requireItem(h.ctx, plugin, na.ID, core.AccessEdit); err != nil {
		t.Fatalf("edit on granted note: %v", err)
	}
	if _, err := h.app.SpaceTree(h.ctx, plugin, b.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("SpaceTree of ungranted space: %v, want not found", err)
	}
	if _, err := h.app.SpaceTree(h.ctx, plugin, a.ID); err != nil {
		t.Fatalf("SpaceTree of granted space: %v", err)
	}
	ids, err := h.app.accessibleSpaceIDs(h.ctx, plugin)
	if err != nil || !slices.Equal(ids, []string{a.ID}) {
		t.Fatalf("accessibleSpaceIDs = %v, %v", ids, err)
	}
	spaces, err := h.app.ListSpaces(h.ctx, plugin)
	if err != nil {
		t.Fatalf("ListSpaces: %v", err)
	}
	for _, sp := range spaces {
		if sp.ID != a.ID {
			t.Fatalf("ListSpaces reveals %s", sp.Name)
		}
	}
	hits, err := h.app.Search(h.ctx, plugin, core.SearchQuery{Text: "words"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, hit := range hits {
		if hit.Note != nil && hit.Note.SpaceID != a.ID {
			t.Fatalf("Search reveals a note of %s", hit.Note.SpaceID)
		}
	}
	g, err := h.app.SpaceAccess(h.ctx, plugin, b.ID)
	if err != nil || g.Access != core.AccessNone {
		t.Fatalf("SpaceAccess of ungranted space = %+v, %v", g, err)
	}

	// Owner and admin roles in the identity grant nothing.
	for _, role := range []core.Role{core.RoleOwner, core.RoleAdmin, "bogus"} {
		p := pluginActor(pu, "wiki", map[string]core.Role{a.ID: role})
		if got := accessOf(t, h, p, na.ID); got != core.AccessNone {
			t.Fatalf("role %s gives %s", role, got)
		}
	}
	for role, want := range map[core.Role]core.Access{core.RoleViewer: core.AccessView, core.RoleCommenter: core.AccessComment} {
		p := pluginActor(pu, "wiki", map[string]core.Role{a.ID: role})
		if got := accessOf(t, h, p, na.ID); got != want {
			t.Fatalf("role %s gives %s, want %s", role, got, want)
		}
	}

	// A plugin user forced to admin in the database is still no admin.
	pu.IsAdmin = true
	if err := h.db.UpdateUser(h.ctx, pu); err != nil {
		t.Fatal(err)
	}
	stored, _ := h.db.GetUser(h.ctx, pu.ID)
	admin := pluginActor(stored, "wiki", map[string]core.Role{a.ID: core.RoleViewer})
	if admin.IsAdmin() {
		t.Fatal("plugin actor reports admin")
	}
	if err := requireAdmin(admin); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("requireAdmin = %v", err)
	}
	if _, _, err := h.app.ListUsers(h.ctx, admin, "", 10, 0); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("ListUsers as plugin = %v", err)
	}
	if got := accessOf(t, h, admin, nb.ID); got != core.AccessNone {
		t.Fatalf("admin plugin user reaches ungranted note: %s", got)
	}
	if err := h.app.DeleteSpace(h.ctx, admin, a.ID); err == nil {
		t.Fatal("plugin deleted a space")
	}
}

func TestActingTokenAccess(t *testing.T) {
	h := newRealHarness(t)
	owner := h.user("Owner", false)
	a := h.space(owner, "Alpha")
	b := h.space(owner, "Beta")
	c := h.space(owner, "Gamma")
	na := h.note(owner, a, "", "Alpha note", "alpha")
	nb := h.note(owner, b, "", "Beta note", "beta")
	nc := h.note(owner, c, "", "Gamma note", "gamma")
	person := h.user("Person", false)
	h.member(a, person, core.RoleViewer)
	h.share(nb.ID, person, core.AccessEdit, false)
	pu := h.pluginUser("Wiki")
	plugin := pluginActor(pu, "wiki", map[string]core.Role{a.ID: core.RoleEditor, b.ID: core.RoleCommenter})
	acting := actingActor(person.User, plugin)

	for _, tc := range []struct {
		id   string
		want core.Access
	}{
		{na.ID, core.AccessView},    // person view, plugin edit
		{nb.ID, core.AccessComment}, // person share edit, plugin comment
		{nc.ID, core.AccessNone},    // neither
	} {
		if got := accessOf(t, h, acting, tc.id); got != tc.want {
			t.Errorf("acting access on %s = %s, want %s", tc.id, got, tc.want)
		}
	}
	if acting.IsAdmin() {
		t.Fatal("acting actor reports admin")
	}
	ids, _ := h.app.accessibleSpaceIDs(h.ctx, acting)
	slices.Sort(ids)
	want := []string{a.ID, b.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("acting accessibleSpaceIDs = %v, want %v", ids, want)
	}

	// An admin person gets at most the plugin grant.
	admin := h.user("Admin", true)
	viewer := pluginActor(pu, "wiki", map[string]core.Role{a.ID: core.RoleViewer})
	adminActing := actingActor(admin.User, viewer)
	if got := accessOf(t, h, adminActing, na.ID); got != core.AccessView {
		t.Fatalf("admin acting on granted note = %s, want view", got)
	}
	if got := accessOf(t, h, adminActing, nb.ID); got != core.AccessNone {
		t.Fatalf("admin acting on ungranted note = %s, want none", got)
	}
	spaces, _ := h.app.ListSpaces(h.ctx, adminActing)
	for _, sp := range spaces {
		if sp.ID != a.ID {
			t.Fatalf("admin acting ListSpaces reveals %s", sp.Name)
		}
	}
	editor := pluginActor(pu, "wiki", map[string]core.Role{a.ID: core.RoleEditor})
	ownerActing := actingActor(owner.User, editor)
	if err := h.app.DeleteSpace(h.ctx, ownerActing, a.ID); err == nil {
		t.Fatal("acting token deleted a space")
	}
	if _, err := h.app.AddMember(h.ctx, ownerActing, a.ID, admin.User.ID, core.RoleEditor); err == nil {
		t.Fatal("acting token added a member")
	}
	if _, err := h.app.CreateShare(h.ctx, ownerActing, core.ShareInput{NoteID: na.ID, UserID: admin.User.ID, Access: core.AccessView}); err == nil {
		t.Fatal("acting token created a share")
	}
	if got := accessOf(t, h, ownerActing, na.ID); got != core.AccessEdit {
		t.Fatalf("owner acting through editor plugin = %s, want edit", got)
	}
	g, err := h.app.SpaceAccess(h.ctx, ownerActing, a.ID)
	if err != nil || g.Access != core.AccessEdit || g.Role == core.RoleOwner {
		t.Fatalf("owner acting space grant = %+v, %v", g, err)
	}
	_ = time.Second
}
