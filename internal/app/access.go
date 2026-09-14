package app

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// noAccess is the grant returned when nothing grants the actor anything.
func noAccess() *core.Grant { return &core.Grant{Access: core.AccessNone, Source: "none"} }

// anonymous reports whether the actor is neither a signed-in user nor a public
// link holder. The web layer redirects such an actor to the login page.
func anonymous(actor *core.Actor) bool {
	return actor == nil || (actor.User == nil && actor.Link == nil)
}

// denied maps an empty grant onto the error the caller should see. An
// anonymous visitor gets ErrUnauthorized so the web layer can redirect to
// login. Anybody else gets ErrNotFound, so the reply does not leak the
// existence of the resource.
func denied(actor *core.Actor) error {
	if anonymous(actor) {
		return core.ErrUnauthorized
	}
	return core.ErrNotFound
}

// requireAdmin rejects anybody who is not a signed-in instance admin.
func requireAdmin(actor *core.Actor) error {
	if anonymous(actor) {
		return fmt.Errorf("admin required: %w", core.ErrUnauthorized)
	}
	if !actor.IsAdmin() {
		return fmt.Errorf("admin required: %w", core.ErrForbidden)
	}
	return nil
}

// requireUser rejects anybody who is not a signed-in user.
func requireUser(actor *core.Actor) error {
	if !actor.IsUser() {
		if anonymous(actor) {
			return fmt.Errorf("sign in required: %w", core.ErrUnauthorized)
		}
		return fmt.Errorf("sign in required: %w", core.ErrForbidden)
	}
	return nil
}

// grantCache resolves grants for one actor while reusing the membership,
// share and link target queries across many items.
type grantCache struct {
	app    *App
	actor  *core.Actor
	spaces map[string]*core.Grant
	shares []ports.ShareGrant
	loaded bool

	linkTarget itemRef
	linkOK     bool
	linkLoaded bool

	// person resolves the user side of an acting token.
	person *grantCache
}

// pluginGrant returns the grant a plugin's space role gives, capped at edit.
func pluginGrant(p *core.PluginIdentity, spaceID string) *core.Grant {
	acc := p.SpaceAccess(spaceID)
	if acc == core.AccessNone {
		return noAccess()
	}
	return &core.Grant{Access: acc, Source: "plugin", Role: p.Grants[spaceID]}
}

// personOf returns the person an acting token acts for, without the plugin.
func personOf(actor *core.Actor) *core.Actor { return &core.Actor{User: actor.User} }

// actingGrant intersects the person's grant with the plugin's grant. The
// role of the lower side is kept, and never a role above editor.
func actingGrant(user, plugin *core.Grant) *core.Grant {
	low := user
	if plugin.Access < user.Access {
		low = plugin
	}
	if low.Access == core.AccessNone {
		return noAccess()
	}
	g := &core.Grant{Access: low.Access, Source: "acting"}
	if low.Role.Access() <= core.AccessEdit {
		g.Role = low.Role
	}
	return g
}

// grants builds a grant cache for one actor.
func (a *App) grants(actor *core.Actor) *grantCache {
	return &grantCache{app: a, actor: actor, spaces: map[string]*core.Grant{}}
}

// space resolves the space grant, reusing an earlier answer for the same space.
func (c *grantCache) space(ctx context.Context, spaceID string) (*core.Grant, error) {
	if spaceID == "" || c.actor == nil {
		return noAccess(), nil
	}
	if g, ok := c.spaces[spaceID]; ok {
		return g, nil
	}
	g, err := c.app.loadSpaceGrant(ctx, c.actor, spaceID)
	if err != nil {
		return nil, err
	}
	c.spaces[spaceID] = g
	return g, nil
}

// shareGrants loads the actor's direct grants on items of every kind once.
func (c *grantCache) shareGrants(ctx context.Context) ([]ports.ShareGrant, error) {
	if c.loaded || !c.actor.IsUser() || (c.actor.Plugin != nil && c.actor.Via == "") {
		return c.shares, nil
	}
	list, err := c.app.db.ItemShareGrantsForUser(ctx, c.actor.User.ID)
	if err != nil {
		return nil, fmt.Errorf("load share grants: %w", err)
	}
	c.shares = list
	c.loaded = true
	return c.shares, nil
}

// note resolves the grant on a note.
func (c *grantCache) note(ctx context.Context, note *core.Note) (*core.Grant, error) {
	if note == nil {
		return noAccess(), nil
	}
	return c.item(ctx, noteRef(note))
}

// item resolves the grant on an item as the maximum of the space grant, a
// direct share and a share on an ancestor folder, or on the folder note of
// an ancestor folder, that includes children. A public link actor is limited
// to the link target and, when the link includes children, what lies below it.
func (c *grantCache) item(ctx context.Context, it itemRef) (*core.Grant, error) {
	if it.id == "" || c.actor == nil {
		return noAccess(), nil
	}
	if c.actor.Link != nil && c.actor.User == nil {
		return c.linkGrant(ctx, it), nil
	}
	if c.actor.Plugin != nil && c.actor.User != nil {
		// A plugin reaches only its granted spaces, with no shares. An acting
		// token also needs the person's own access.
		pg := pluginGrant(c.actor.Plugin, it.spaceID)
		if pg.Access == core.AccessNone || c.actor.Via == "" {
			return pg, nil
		}
		if c.person == nil {
			c.person = c.app.grants(personOf(c.actor))
		}
		ug, err := c.person.item(ctx, it)
		if err != nil {
			return nil, err
		}
		return actingGrant(ug, pg), nil
	}
	best, err := c.space(ctx, it.spaceID)
	if err != nil {
		return nil, err
	}
	if !c.actor.IsUser() || best.Access.AtLeast(core.AccessManage) {
		// No share can raise manage access.
		return best, nil
	}
	shares, err := c.shareGrants(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range shares {
		var cand *core.Grant
		switch {
		case g.NoteID == it.id:
			cand = &core.Grant{Access: g.Access, Source: "share"}
		case g.IncludeChildren && g.SpaceID == it.spaceID && coversPath(g.Kind, g.NotePath, it.path):
			cand = &core.Grant{Access: g.Access, Source: "inherited-share"}
		}
		if cand != nil && cand.Access > best.Access {
			best = cand
		}
	}
	return best, nil
}

// linkGrant resolves the access a public link carries for one item. A link
// reaches no trashed item, nothing outside its own space, and never more than
// maxLinkAccess, whatever the stored row says.
func (c *grantCache) linkGrant(ctx context.Context, it itemRef) *core.Grant {
	link := c.actor.Link
	if link == nil || it.deleted || link.SpaceID != it.spaceID {
		return noAccess()
	}
	grant := &core.Grant{Access: clampLinkAccess(link.Access), Source: "link"}
	if link.NoteID == it.id {
		return grant
	}
	if !link.IncludeChildren {
		return noAccess()
	}
	if !c.linkLoaded {
		c.linkLoaded = true
		c.linkTarget, c.linkOK = c.app.linkTargetRef(ctx, link)
	}
	target := c.linkTarget
	if !c.linkOK || target.deleted || target.spaceID != it.spaceID {
		return noAccess()
	}
	if coversPath(target.kind, target.path, it.path) {
		return grant
	}
	return noAccess()
}

// linkTargetRef loads the item a public link points at.
func (a *App) linkTargetRef(ctx context.Context, link *core.PublicLink) (itemRef, bool) {
	it, err := a.loadItem(ctx, link.NoteID)
	if err != nil {
		return itemRef{}, false
	}
	if link.ID != "" {
		if kind, err := a.db.PublicLinkKind(ctx, link.ID); err == nil && kind != it.Kind {
			return itemRef{}, false
		}
	}
	return refOf(it), true
}

// coversPath reports whether a grant with children on the item of kind at
// root reaches the item at p. A folder covers everything below it. A folder
// note covers its folder and everything below that folder. Any other note or
// file has nothing below it.
func coversPath(kind core.ItemKind, root, p string) bool {
	if root == "" || p == "" || root == p {
		return false
	}
	switch kind {
	case core.KindFolder:
		return underPath(p, root)
	case core.KindNote:
		if !isFolderNotePath(root) {
			return false
		}
		dir := path.Dir(root)
		return p == dir || underPath(p, dir)
	}
	return false
}

// spaceGrant resolves what an actor may do in one space. An instance admin
// gets AccessManage. A member gets the access of their role. A public link
// actor gets nothing at the space level, because a link scopes to an item.
func (a *App) spaceGrant(ctx context.Context, actor *core.Actor, spaceID string) (*core.Grant, error) {
	return a.loadSpaceGrant(ctx, actor, spaceID)
}

// loadSpaceGrant is the uncached space grant lookup.
func (a *App) loadSpaceGrant(ctx context.Context, actor *core.Actor, spaceID string) (*core.Grant, error) {
	if spaceID == "" || actor == nil {
		return noAccess(), nil
	}
	if actor.Link != nil && actor.User == nil {
		return noAccess(), nil
	}
	if actor.Plugin != nil && actor.User != nil {
		pg := pluginGrant(actor.Plugin, spaceID)
		if pg.Access == core.AccessNone || actor.Via == "" {
			return pg, nil
		}
		ug, err := a.loadSpaceGrant(ctx, personOf(actor), spaceID)
		if err != nil {
			return nil, err
		}
		return actingGrant(ug, pg), nil
	}
	if actor.IsAdmin() {
		return &core.Grant{Access: core.AccessManage, Source: "admin"}, nil
	}
	if !actor.IsUser() {
		return noAccess(), nil
	}
	m, err := a.db.GetMember(ctx, spaceID, actor.User.ID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return noAccess(), nil
		}
		return nil, fmt.Errorf("load membership: %w", err)
	}
	if m == nil || !m.Role.Valid() {
		return noAccess(), nil
	}
	return &core.Grant{Access: m.Role.Access(), Source: "space", Role: m.Role}, nil
}

// noteGrant resolves what an actor may do with one note. The result is never
// nil.
func (a *App) noteGrant(ctx context.Context, actor *core.Actor, note *core.Note) (*core.Grant, error) {
	return a.grants(actor).note(ctx, note)
}

// requireNote loads a note, resolves the grant and enforces want. A trashed
// note stays readable at AccessView, and any stronger request fails with
// core.ErrConflict so a client cannot edit something that is in the trash.
func (a *App) requireNote(ctx context.Context, actor *core.Actor, noteID string, want core.Access) (*core.Note, *core.Grant, error) {
	note, grant, err := a.requireNoteAny(ctx, actor, noteID, want)
	if err != nil {
		return nil, nil, err
	}
	if note.Deleted && want > core.AccessView {
		return nil, nil, fmt.Errorf("note %s is in the trash: %w", noteID, core.ErrConflict)
	}
	return note, grant, nil
}

// requireNoteAny is requireNote without the trash check, for the operations
// that exist to act on trashed notes.
func (a *App) requireNoteAny(ctx context.Context, actor *core.Actor, noteID string, want core.Access) (*core.Note, *core.Grant, error) {
	if noteID == "" {
		return nil, nil, fmt.Errorf("note id is empty: %w", core.ErrInvalid)
	}
	note, err := a.db.GetNote(ctx, noteID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return nil, nil, fmt.Errorf("note %s: %w", noteID, denied(actor))
		}
		return nil, nil, fmt.Errorf("load note %s: %w", noteID, err)
	}
	if note == nil {
		return nil, nil, fmt.Errorf("note %s: %w", noteID, denied(actor))
	}
	grant, err := a.noteGrant(ctx, actor, note)
	if err != nil {
		return nil, nil, err
	}
	if grant.Access == core.AccessNone {
		return nil, nil, fmt.Errorf("note %s: %w", noteID, denied(actor))
	}
	if !grant.Access.AtLeast(want) {
		return nil, nil, fmt.Errorf("note %s needs %s access: %w", noteID, want, core.ErrForbidden)
	}
	return note, grant, nil
}

// requireSpace loads a space, resolves the grant and enforces want.
func (a *App) requireSpace(ctx context.Context, actor *core.Actor, spaceID string, want core.Access) (*core.Space, *core.Grant, error) {
	if spaceID == "" {
		return nil, nil, fmt.Errorf("space id is empty: %w", core.ErrInvalid)
	}
	sp, err := a.db.GetSpace(ctx, spaceID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return nil, nil, fmt.Errorf("space %s: %w", spaceID, denied(actor))
		}
		return nil, nil, fmt.Errorf("load space %s: %w", spaceID, err)
	}
	if sp == nil {
		return nil, nil, fmt.Errorf("space %s: %w", spaceID, denied(actor))
	}
	grant, err := a.spaceGrant(ctx, actor, sp.ID)
	if err != nil {
		return nil, nil, err
	}
	if grant.Access == core.AccessNone {
		return nil, nil, fmt.Errorf("space %s: %w", spaceID, denied(actor))
	}
	if !grant.Access.AtLeast(want) {
		return nil, nil, fmt.Errorf("space %s needs %s access: %w", spaceID, want, core.ErrForbidden)
	}
	return sp, grant, nil
}

// spaceView resolves a space for a listing. It admits a member, and it also
// admits an actor whose only access is a share on an item inside the space,
// in which case the grant is AccessNone and the caller filters per item. The
// returned share grants are the actor's grants inside this space.
func (a *App) spaceView(ctx context.Context, actor *core.Actor, spaceID string) (*core.Space, *core.Grant, []ports.ShareGrant, error) {
	if spaceID == "" {
		return nil, nil, nil, fmt.Errorf("space id is empty: %w", core.ErrInvalid)
	}
	sp, err := a.db.GetSpace(ctx, spaceID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return nil, nil, nil, fmt.Errorf("space %s: %w", spaceID, denied(actor))
		}
		return nil, nil, nil, fmt.Errorf("load space %s: %w", spaceID, err)
	}
	if sp == nil {
		return nil, nil, nil, fmt.Errorf("space %s: %w", spaceID, denied(actor))
	}
	grant, err := a.spaceGrant(ctx, actor, sp.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	if grant.Access.AtLeast(core.AccessView) {
		return sp, grant, nil, nil
	}
	var mine []ports.ShareGrant
	// A plugin token has no shares. An acting token uses the person's shares
	// inside a space the plugin may view.
	if actor.IsUser() && (actor.Plugin == nil || (actor.Via != "" && actor.Plugin.SpaceAccess(sp.ID) >= core.AccessView)) {
		all, err := a.db.ItemShareGrantsForUser(ctx, actor.User.ID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("load share grants: %w", err)
		}
		for _, g := range all {
			if g.SpaceID == sp.ID && g.Access.AtLeast(core.AccessView) {
				mine = append(mine, g)
			}
		}
	}
	if actor != nil && actor.Link != nil && actor.User == nil && actor.Link.SpaceID == sp.ID {
		// A link actor may list only inside its own link scope.
		return sp, noAccess(), nil, nil
	}
	if len(mine) == 0 {
		return nil, nil, nil, fmt.Errorf("space %s: %w", spaceID, denied(actor))
	}
	return sp, noAccess(), mine, nil
}

// accessibleSpaceIDs lists the spaces the actor may read from. An instance
// admin sees every space. A normal user sees their member spaces plus the
// spaces holding items shared with them. A public link actor sees none.
func (a *App) accessibleSpaceIDs(ctx context.Context, actor *core.Actor) ([]string, error) {
	if actor == nil || (actor.User == nil) {
		return nil, nil
	}
	if actor.Plugin != nil {
		granted := actor.Plugin.SpaceIDs()
		if actor.Via == "" {
			return granted, nil
		}
		mine, err := a.accessibleSpaceIDs(ctx, personOf(actor))
		if err != nil {
			return nil, err
		}
		allowed := make(map[string]bool, len(granted))
		for _, id := range granted {
			allowed[id] = true
		}
		out := []string{}
		for _, id := range mine {
			if allowed[id] {
				out = append(out, id)
			}
		}
		return out, nil
	}
	if actor.IsAdmin() {
		spaces, err := a.db.ListSpaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("list spaces: %w", err)
		}
		out := make([]string, 0, len(spaces))
		for _, s := range spaces {
			out = append(out, s.ID)
		}
		return out, nil
	}
	seen := map[string]bool{}
	out := []string{}
	members, err := a.db.ListMembershipsForUser(ctx, actor.User.ID)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	for _, m := range members {
		if !seen[m.SpaceID] {
			seen[m.SpaceID] = true
			out = append(out, m.SpaceID)
		}
	}
	grants, err := a.db.ItemShareGrantsForUser(ctx, actor.User.ID)
	if err != nil {
		return nil, fmt.Errorf("load share grants: %w", err)
	}
	for _, g := range grants {
		if g.SpaceID != "" && !seen[g.SpaceID] {
			seen[g.SpaceID] = true
			out = append(out, g.SpaceID)
		}
	}
	return out, nil
}

// visibleNotes keeps the notes the actor may view. It skips the per-note work
// when the space grant already covers everything.
func (a *App) visibleNotes(ctx context.Context, actor *core.Actor, grant *core.Grant, notes []*core.Note) ([]*core.Note, error) {
	if grant != nil && grant.Access.AtLeast(core.AccessView) {
		return notes, nil
	}
	cache := a.grants(actor)
	out := make([]*core.Note, 0, len(notes))
	for _, n := range notes {
		g, err := cache.note(ctx, n)
		if err != nil {
			return nil, err
		}
		if g.Access.AtLeast(core.AccessView) {
			out = append(out, n)
		}
	}
	return out, nil
}

// intersect returns the members of want that appear in have, or all of have
// when want is empty.
func intersect(have, want []string) []string {
	if len(want) == 0 {
		return have
	}
	set := make(map[string]bool, len(have))
	for _, h := range have {
		set[h] = true
	}
	out := make([]string, 0, len(want))
	for _, w := range want {
		if set[w] {
			out = append(out, w)
		}
	}
	return out
}

// hasPathPrefix reports whether p starts with prefix.
func hasPathPrefix(p, prefix string) bool { return strings.HasPrefix(p, prefix) }
