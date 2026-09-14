package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/gamemaker1/markspace/internal/core"
)

// ownerMu serializes membership changes, so two owners stepping down at the
// same moment cannot each see the other as the remaining owner. Like adminMu
// it covers one process. When both are needed, adminMu is taken first.
var ownerMu sync.Mutex

// ListSpaces returns every space the actor may view, with MyRole set. A public
// link actor sees none.
func (a *App) ListSpaces(ctx context.Context, actor *core.Actor) ([]*core.Space, error) {
	if !actor.IsUser() {
		return nil, nil
	}
	if actor.Plugin != nil {
		return a.listPluginSpaces(ctx, actor)
	}
	if actor.IsAdmin() {
		spaces, err := a.db.ListSpaces(ctx)
		if err != nil {
			return nil, fmt.Errorf("list spaces: %w", err)
		}
		roles := map[string]core.Role{}
		if members, err := a.db.ListMembershipsForUser(ctx, actor.User.ID); err == nil {
			for _, m := range members {
				roles[m.SpaceID] = m.Role
			}
		}
		for _, s := range spaces {
			if r, ok := roles[s.ID]; ok {
				s.MyRole = r
			}
		}
		return spaces, nil
	}
	spaces, err := a.db.ListSpacesForUser(ctx, actor.User.ID)
	if err != nil {
		return nil, fmt.Errorf("list spaces for user: %w", err)
	}
	seen := make(map[string]bool, len(spaces))
	for _, s := range spaces {
		seen[s.ID] = true
	}
	// A note shared with the actor makes its space navigable, without a role.
	grants, err := a.db.ShareGrantsForUser(ctx, actor.User.ID)
	if err != nil {
		return nil, fmt.Errorf("load share grants: %w", err)
	}
	for _, g := range grants {
		if g.SpaceID == "" || seen[g.SpaceID] {
			continue
		}
		seen[g.SpaceID] = true
		sp, err := a.db.GetSpace(ctx, g.SpaceID)
		if err != nil || sp == nil {
			continue
		}
		spaces = append(spaces, sp)
	}
	return spaces, nil
}

// GetSpace resolves a space by id or slug and checks the actor may view it.
func (a *App) GetSpace(ctx context.Context, actor *core.Actor, idOrSlug string) (*core.Space, error) {
	if idOrSlug == "" {
		return nil, core.Invalid("space", "space id is required")
	}
	sp, err := a.db.GetSpace(ctx, idOrSlug)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load space: %w", err)
	}
	if sp == nil {
		sp, err = a.db.GetSpaceBySlug(ctx, idOrSlug)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return nil, fmt.Errorf("load space by slug: %w", err)
		}
	}
	if sp == nil {
		return nil, fmt.Errorf("space %s: %w", idOrSlug, denied(actor))
	}
	out, _, _, err := a.spaceView(ctx, actor, sp.ID)
	if err != nil {
		return nil, err
	}
	if actor.Plugin != nil {
		if g, err := a.spaceGrant(ctx, actor, sp.ID); err == nil {
			out.MyRole = g.Role
		}
	} else if actor.IsUser() {
		if m, err := a.db.GetMember(ctx, sp.ID, actor.User.ID); err == nil && m != nil {
			out.MyRole = m.Role
		}
	}
	return out, nil
}

// listPluginSpaces lists the spaces a plugin actor reaches: its granted
// spaces for a plugin token, and the granted spaces the person also reaches
// for an acting token. The role shown is the resolved one, never the plugin
// user's own membership.
func (a *App) listPluginSpaces(ctx context.Context, actor *core.Actor) ([]*core.Space, error) {
	ids, err := a.accessibleSpaceIDs(ctx, actor)
	if err != nil {
		return nil, err
	}
	out := []*core.Space{}
	for _, id := range ids {
		sp, err := a.db.GetSpace(ctx, id)
		if err != nil || sp == nil {
			continue
		}
		if g, err := a.spaceGrant(ctx, actor, id); err == nil {
			sp.MyRole = g.Role
		}
		out = append(out, sp)
	}
	return out, nil
}

// slugFromInput normalizes a slug a caller typed. Input without a letter or a
// digit is refused, because Slugify would turn it into "untitled".
func slugFromInput(raw string) (string, error) {
	if strings.IndexFunc(raw, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0 {
		return "", core.Invalid("slug", "the slug needs at least one letter or digit")
	}
	return core.Slugify(raw), nil
}

// CreateSpace makes a space, its directory and an owner membership for the
// caller. A blank slug falls back to the name, and a taken slug gets a numeric
// suffix.
func (a *App) CreateSpace(ctx context.Context, actor *core.Actor, in core.SpaceInput) (*core.Space, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if name == "" {
		return nil, core.Invalid("name", "space name is required")
	}
	slug := ""
	if in.Slug != nil && strings.TrimSpace(*in.Slug) != "" {
		s, err := slugFromInput(*in.Slug)
		if err != nil {
			return nil, err
		}
		slug = s
	}
	if slug == "" {
		slug = core.Slugify(name)
	}
	sp, err := a.newSpace(ctx, name, slug, actor.User.ID)
	if err != nil {
		return nil, err
	}
	if in.Description != nil || in.Icon != nil {
		if in.Description != nil {
			sp.Description = strings.TrimSpace(*in.Description)
		}
		if in.Icon != nil {
			sp.Icon = strings.TrimSpace(*in.Icon)
		}
		if err := a.db.UpdateSpace(ctx, sp); err != nil {
			return nil, fmt.Errorf("update new space: %w", err)
		}
	}
	sp.MyRole = core.RoleOwner
	return sp, nil
}

// newSpace creates the directory, the space row and the owner membership.
func (a *App) newSpace(ctx context.Context, name, slug, ownerID string) (*core.Space, error) {
	slug, err := a.uniqueSlug(ctx, slug, "")
	if err != nil {
		return nil, err
	}
	if err := a.files.EnsureSpace(slug); err != nil {
		return nil, fmt.Errorf("create space directory %s: %w", slug, err)
	}
	now := a.now()
	sp := &core.Space{
		ID:        core.NewID(),
		Slug:      slug,
		Name:      name,
		CreatedBy: ownerID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := a.db.CreateSpace(ctx, sp); err != nil {
		return nil, fmt.Errorf("create space: %w", err)
	}
	if ownerID != "" {
		m := &core.SpaceMember{SpaceID: sp.ID, UserID: ownerID, Role: core.RoleOwner, CreatedAt: now}
		if err := a.db.AddMember(ctx, m); err != nil {
			return nil, fmt.Errorf("add space owner: %w", err)
		}
	}
	a.log.Info("space created", "spaceId", sp.ID, "slug", slug, "owner", ownerID)
	return sp, nil
}

// uniqueSlug suffixes a slug until no other space holds it.
func (a *App) uniqueSlug(ctx context.Context, slug, exceptID string) (string, error) {
	if slug == "" {
		slug = "space"
	}
	candidate := slug
	for i := 2; i < 1000; i++ {
		other, err := a.db.GetSpaceBySlug(ctx, candidate)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return "", fmt.Errorf("check space slug: %w", err)
		}
		if other == nil || other.ID == exceptID {
			return candidate, nil
		}
		candidate = slug + "-" + strconv.Itoa(i)
	}
	return "", fmt.Errorf("space slug %s: %w", slug, core.ErrExists)
}

// UpdateSpace changes a space. A changed slug renames the directory. A slug
// that another space holds fails with ErrExists instead of gaining a suffix,
// because the caller asked for that exact address.
func (a *App) UpdateSpace(ctx context.Context, actor *core.Actor, id string, in core.SpaceInput) (*core.Space, error) {
	sp, _, err := a.requireSpace(ctx, actor, id, core.AccessManage)
	if err != nil {
		return nil, err
	}
	oldSlug := sp.Slug
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, core.Invalid("name", "space name is required")
		}
		sp.Name = name
	}
	if in.Description != nil {
		sp.Description = strings.TrimSpace(*in.Description)
	}
	if in.Icon != nil {
		sp.Icon = strings.TrimSpace(*in.Icon)
	}
	if in.PasteFolder != nil {
		folder, err := a.cleanPasteFolder(*in.PasteFolder)
		if err != nil {
			return nil, err
		}
		sp.PasteFolder = folder
	}
	newSlug := oldSlug
	if in.Slug != nil {
		want, err := slugFromInput(*in.Slug)
		if err != nil {
			return nil, err
		}
		if want != oldSlug {
			other, err := a.db.GetSpaceBySlug(ctx, want)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return nil, fmt.Errorf("check space slug: %w", err)
			}
			if other != nil && other.ID != sp.ID {
				return nil, fmt.Errorf("space slug %s is taken: %w", want, core.ErrExists)
			}
			newSlug = want
		}
	}
	if newSlug != oldSlug {
		if err := a.files.RenameSpace(oldSlug, newSlug); err != nil {
			return nil, fmt.Errorf("rename space directory: %w", err)
		}
		sp.Slug = newSlug
	}
	sp.UpdatedAt = a.now()
	if err := a.db.UpdateSpace(ctx, sp); err != nil {
		if sp.Slug != oldSlug {
			if rerr := a.files.RenameSpace(sp.Slug, oldSlug); rerr != nil {
				a.log.Warn("undo space directory rename failed", "spaceId", sp.ID, "from", sp.Slug, "to", oldSlug, "err", rerr)
			}
		}
		return nil, fmt.Errorf("update space: %w", err)
	}
	if sp.Slug != oldSlug {
		a.log.Info("space renamed", "spaceId", sp.ID, "from", oldSlug, "to", sp.Slug)
		a.publish("tree.changed", sp.ID, "", actor.UserID(), "")
	}
	return sp, nil
}

// DeleteSpace archives the directory and removes the space record. The caller
// must be a space owner or an instance admin. A default space setting that
// points at the space is cleared.
func (a *App) DeleteSpace(ctx context.Context, actor *core.Actor, id string) error {
	sp, grant, err := a.requireSpace(ctx, actor, id, core.AccessManage)
	if err != nil {
		return err
	}
	if grant.Source != "admin" && grant.Role != core.RoleOwner {
		return fmt.Errorf("space %s needs an owner: %w", id, core.ErrForbidden)
	}
	if err := a.files.ArchiveSpace(sp.Slug); err != nil {
		return fmt.Errorf("archive space directory: %w", err)
	}
	if err := a.db.DeleteSpace(ctx, sp.ID); err != nil {
		return fmt.Errorf("delete space: %w", err)
	}
	if a.suggestions != nil {
		if _, err := a.suggestions.DeleteSpaceSuggestions(ctx, sp.ID); err != nil {
			a.log.Warn("delete suggestions of deleted space failed", "spaceId", sp.ID, "err", err)
		}
	}
	a.clearDefaultSpace(ctx, sp.ID)
	a.log.Info("space deleted", "spaceId", sp.ID, "slug", sp.Slug, "by", actor.UserID())
	a.publish("tree.changed", sp.ID, "", actor.UserID(), "")
	return nil
}

// clearDefaultSpace drops the default space setting when it names spaceID.
func (a *App) clearDefaultSpace(ctx context.Context, spaceID string) {
	row, err := a.db.GetSettings(ctx)
	if err != nil || row == nil || row.DefaultSpaceID != spaceID {
		return
	}
	row.DefaultSpaceID = ""
	row.UpdatedAt = a.now()
	if err := a.db.SaveSettings(ctx, row); err != nil {
		a.log.Warn("clear default space failed", "spaceId", spaceID, "err", err)
	}
}

// ListMembers returns the membership of a space.
func (a *App) ListMembers(ctx context.Context, actor *core.Actor, spaceID string) ([]*core.SpaceMember, error) {
	if _, _, err := a.requireSpace(ctx, actor, spaceID, core.AccessView); err != nil {
		return nil, err
	}
	members, err := a.db.ListMembers(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	for _, m := range members {
		if m.User != nil {
			continue
		}
		if u, err := a.db.GetUser(ctx, m.UserID); err == nil {
			m.User = u
		}
	}
	return members, nil
}

// requireOwnerPower admits a space owner or an instance admin. Only they may
// grant the owner role, or change or remove a member who holds it. A space
// admin manages everyone else.
func (a *App) requireOwnerPower(ctx context.Context, actor *core.Actor, spaceID string) error {
	g, err := a.spaceGrant(ctx, actor, spaceID)
	if err != nil {
		return err
	}
	if g.Source == "admin" || g.Role == core.RoleOwner {
		return nil
	}
	return fmt.Errorf("only a space owner or an instance admin can grant or revoke the owner role: %w", core.ErrForbidden)
}

// AddMember grants an active user a role in a space.
func (a *App) AddMember(ctx context.Context, actor *core.Actor, spaceID, userID string, role core.Role) (*core.SpaceMember, error) {
	if _, _, err := a.requireSpace(ctx, actor, spaceID, core.AccessManage); err != nil {
		return nil, err
	}
	if !role.Valid() {
		return nil, core.Invalid("role", "unknown role "+string(role))
	}
	ownerMu.Lock()
	defer ownerMu.Unlock()
	if role == core.RoleOwner {
		if err := a.requireOwnerPower(ctx, actor, spaceID); err != nil {
			return nil, err
		}
	}
	u, err := a.db.GetUser(ctx, userID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load user: %w", err)
	}
	if u == nil {
		return nil, fmt.Errorf("user %s: %w", userID, core.ErrNotFound)
	}
	if !u.IsActive {
		return nil, fmt.Errorf("user %s is deactivated: %w", userID, core.ErrConflict)
	}
	if existing, err := a.db.GetMember(ctx, spaceID, userID); err == nil && existing != nil {
		return nil, fmt.Errorf("user %s is already a member: %w", userID, core.ErrExists)
	}
	m := &core.SpaceMember{SpaceID: spaceID, UserID: userID, Role: role, CreatedAt: a.now(), User: u}
	if err := a.db.AddMember(ctx, m); err != nil {
		return nil, fmt.Errorf("add member: %w", err)
	}
	a.log.Info("member added", "spaceId", spaceID, "userId", userID, "role", role, "by", actor.UserID())
	return m, nil
}

// UpdateMember changes the role of a member. Only an owner or an instance
// admin may grant the owner role or change an owner, and the last owner
// cannot be demoted.
func (a *App) UpdateMember(ctx context.Context, actor *core.Actor, spaceID, userID string, role core.Role) (*core.SpaceMember, error) {
	if _, _, err := a.requireSpace(ctx, actor, spaceID, core.AccessManage); err != nil {
		return nil, err
	}
	if !role.Valid() {
		return nil, core.Invalid("role", "unknown role "+string(role))
	}
	ownerMu.Lock()
	defer ownerMu.Unlock()
	m, err := a.db.GetMember(ctx, spaceID, userID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load member: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("member %s: %w", userID, core.ErrNotFound)
	}
	if role == core.RoleOwner || m.Role == core.RoleOwner {
		if err := a.requireOwnerPower(ctx, actor, spaceID); err != nil {
			return nil, err
		}
	}
	if m.Role == core.RoleOwner && role != core.RoleOwner {
		if err := a.checkLastOwner(ctx, spaceID, userID); err != nil {
			return nil, err
		}
	}
	if err := a.db.UpdateMember(ctx, spaceID, userID, role); err != nil {
		return nil, fmt.Errorf("update member: %w", err)
	}
	m.Role = role
	if m.User == nil {
		if u, err := a.db.GetUser(ctx, userID); err == nil {
			m.User = u
		}
	}
	return m, nil
}

// RemoveMember drops a membership. Only an owner or an instance admin may
// remove an owner, and the last owner cannot be removed.
func (a *App) RemoveMember(ctx context.Context, actor *core.Actor, spaceID, userID string) error {
	if _, _, err := a.requireSpace(ctx, actor, spaceID, core.AccessManage); err != nil {
		return err
	}
	ownerMu.Lock()
	defer ownerMu.Unlock()
	m, err := a.db.GetMember(ctx, spaceID, userID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("load member: %w", err)
	}
	if m == nil {
		return fmt.Errorf("member %s: %w", userID, core.ErrNotFound)
	}
	if m.Role == core.RoleOwner {
		if err := a.requireOwnerPower(ctx, actor, spaceID); err != nil {
			return err
		}
		if err := a.checkLastOwner(ctx, spaceID, userID); err != nil {
			return err
		}
	}
	if err := a.db.RemoveMember(ctx, spaceID, userID); err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	a.log.Info("member removed", "spaceId", spaceID, "userId", userID, "by", actor.UserID())
	return nil
}

// checkLastOwner refuses to leave a space without an owner. An owner counts
// whether or not the account is active, because an admin can reactivate it.
func (a *App) checkLastOwner(ctx context.Context, spaceID, userID string) error {
	members, err := a.db.ListMembers(ctx, spaceID)
	if err != nil {
		return fmt.Errorf("list members: %w", err)
	}
	for _, other := range members {
		if other.UserID != userID && other.Role == core.RoleOwner {
			return nil
		}
	}
	return fmt.Errorf("space %s would have no owner, make another member an owner first: %w", spaceID, core.ErrConflict)
}

// SpaceAccess resolves what the actor may do in one space.
func (a *App) SpaceAccess(ctx context.Context, actor *core.Actor, spaceID string) (*core.Grant, error) {
	grant, err := a.spaceGrant(ctx, actor, spaceID)
	if err != nil {
		return nil, err
	}
	return grant, nil
}

// cleanPasteFolder checks a paste folder setting: a relative folder path
// whose segments pass the name rules, or "" for the note's own folder.
func (a *App) cleanPasteFolder(v string) (string, error) {
	v = strings.Trim(strings.TrimSpace(v), "/")
	if v == "" {
		return "", nil
	}
	segs := strings.Split(v, "/")
	if len(segs) > 8 {
		return "", core.Invalid("pasteFolder", "the paste folder may be at most 8 folders deep")
	}
	for _, seg := range segs {
		if err := a.files.ValidName(seg); err != nil {
			return "", core.Invalid("pasteFolder", "each folder name must follow the name rules")
		}
	}
	return v, nil
}
