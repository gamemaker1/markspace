package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// lookupLimit caps how many users a share picker may see at once.
const lookupLimit = 20

// defaultInviteTTL is how long an invite lasts when the caller gives no TTL.
const defaultInviteTTL = 7 * 24 * time.Hour

// adminMu serializes the account changes that can remove an active admin,
// UpdateUser and DeleteUser. Without it two admins demoting each other at the
// same moment would each see the other as the remaining admin. The lock is
// package level, so it covers one process, which is how Markspace runs.
var adminMu sync.Mutex

// ListUsers returns a page of accounts. Admin only.
func (a *App) ListUsers(ctx context.Context, actor *core.Actor, query string, limit, offset int) ([]*core.User, int, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	users, total, err := a.db.ListUsers(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	return users, total, nil
}

// GetUser returns one account. An admin gets the full record. Any other
// signed-in user gets only the id, name and avatar colour, which is what the
// interface needs to show who wrote or edited a note.
func (a *App) GetUser(ctx context.Context, actor *core.Actor, id string) (*core.User, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	u, err := a.db.GetUser(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load user %s: %w", id, err)
	}
	if u == nil {
		return nil, fmt.Errorf("user %s: %w", id, core.ErrNotFound)
	}
	if !actor.IsAdmin() && actor.UserID() != u.ID {
		return &core.User{ID: u.ID, Name: u.Name, AvatarColor: u.AvatarColor}, nil
	}
	return u, nil
}

// LookupUsers finds users by email or name prefix so a signed-in user can pick
// share recipients. It returns at most 20 rows carrying only the id, name,
// email and avatar colour.
func (a *App) LookupUsers(ctx context.Context, actor *core.Actor, query string, limit int) ([]*core.User, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > lookupLimit {
		limit = lookupLimit
	}
	users, _, err := a.db.ListUsers(ctx, strings.TrimSpace(query), limit, 0)
	if err != nil {
		return nil, fmt.Errorf("lookup users: %w", err)
	}
	out := make([]*core.User, 0, len(users))
	for _, u := range users {
		if !u.IsActive {
			continue
		}
		out = append(out, &core.User{
			ID:          u.ID,
			Name:        u.Name,
			Email:       u.Email,
			AvatarColor: u.AvatarColor,
		})
	}
	return out, nil
}

// CreateUser adds an account. Admin only.
func (a *App) CreateUser(ctx context.Context, actor *core.Actor, in core.UserInput) (*core.User, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if in.Email == nil || in.Password == nil {
		return nil, core.Invalid("email", "email and password are required")
	}
	email := a.hash.NormalizeEmail(*in.Email)
	if err := a.hash.ValidateEmail(email); err != nil {
		return nil, fmt.Errorf("email: %w", err)
	}
	if err := a.hash.ValidatePassword(*in.Password); err != nil {
		return nil, fmt.Errorf("password: %w", err)
	}
	existing, err := a.db.GetUserByEmail(ctx, email)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("lookup email: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("email %s: %w", email, core.ErrExists)
	}
	hashed, err := a.hash.Hash(*in.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}
	now := a.now()
	u := &core.User{
		ID:           core.NewID(),
		Email:        email,
		Name:         name,
		PasswordHash: hashed,
		IsAdmin:      in.IsAdmin != nil && *in.IsAdmin,
		IsActive:     in.IsActive == nil || *in.IsActive,
		AvatarColor:  avatarPalette[len(email)%len(avatarPalette)],
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := a.db.CreateUser(ctx, u); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	a.log.Info("user created by admin", "userId", u.ID, "by", actor.User.ID)
	return u, nil
}

// activeAdmins lists every active admin account.
func (a *App) activeAdmins(ctx context.Context) ([]*core.User, error) {
	const page = 200
	var out []*core.User
	for offset := 0; ; offset += page {
		users, total, err := a.db.ListUsers(ctx, "", page, offset)
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		for _, u := range users {
			if u.IsAdmin && u.IsActive {
				out = append(out, u)
			}
		}
		if len(users) < page || offset+len(users) >= total {
			return out, nil
		}
	}
}

// otherActiveAdmin returns an active admin other than userID, or nil when
// there is none.
func (a *App) otherActiveAdmin(ctx context.Context, userID string) (*core.User, error) {
	admins, err := a.activeAdmins(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range admins {
		if u.ID != userID {
			return u, nil
		}
	}
	return nil, nil
}

// lastAdminError is the refusal for a change that would leave no active admin.
func lastAdminError(u *core.User) error {
	return fmt.Errorf("%s is the last active admin, make another account an active admin first: %w", u.Email, core.ErrConflict)
}

// UpdateUser changes an account. Admin only. It refuses to demote or
// deactivate the last active admin, the actor included. Deactivating an
// account signs it out everywhere, and a password set by an admin signs out
// every session except the admin's own current one.
func (a *App) UpdateUser(ctx context.Context, actor *core.Actor, id string, in core.UserInput) (*core.User, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	adminMu.Lock()
	defer adminMu.Unlock()
	u, err := a.db.GetUser(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load user %s: %w", id, err)
	}
	if u == nil {
		return nil, fmt.Errorf("user %s: %w", id, core.ErrNotFound)
	}
	if (in.IsAdmin != nil && *in.IsAdmin) || in.Password != nil {
		if a.isPluginUser(ctx, u.ID) {
			return nil, core.Invalid("isAdmin", "a plugin user cannot be an admin or have a password")
		}
	}
	wasActive := u.IsActive
	wasActiveAdmin := u.IsAdmin && u.IsActive
	if in.Email != nil {
		email := a.hash.NormalizeEmail(*in.Email)
		if err := a.hash.ValidateEmail(email); err != nil {
			return nil, fmt.Errorf("email: %w", err)
		}
		if email != u.Email {
			other, err := a.db.GetUserByEmail(ctx, email)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				return nil, fmt.Errorf("lookup email: %w", err)
			}
			if other != nil && other.ID != u.ID {
				return nil, fmt.Errorf("email %s: %w", email, core.ErrExists)
			}
			u.Email = email
		}
	}
	if in.Name != nil {
		if name := strings.TrimSpace(*in.Name); name != "" {
			u.Name = name
		}
	}
	passwordSet := false
	if in.Password != nil {
		if err := a.hash.ValidatePassword(*in.Password); err != nil {
			return nil, fmt.Errorf("password: %w", err)
		}
		hashed, err := a.hash.Hash(*in.Password)
		if err != nil {
			return nil, fmt.Errorf("hash password: %w", err)
		}
		u.PasswordHash = hashed
		passwordSet = true
	}
	if in.IsAdmin != nil {
		u.IsAdmin = *in.IsAdmin
	}
	if in.IsActive != nil {
		u.IsActive = *in.IsActive
	}
	if wasActiveAdmin && !(u.IsAdmin && u.IsActive) {
		other, err := a.otherActiveAdmin(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		if other == nil {
			return nil, lastAdminError(u)
		}
	}
	u.UpdatedAt = a.now()
	if err := a.db.UpdateUser(ctx, u); err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}
	switch {
	case wasActive && !u.IsActive:
		if err := a.revokeSessions(ctx, u.ID, ""); err != nil {
			return nil, err
		}
		a.log.Info("user deactivated", "userId", u.ID, "by", actor.User.ID)
	case passwordSet:
		keep := ""
		if actor.UserID() == u.ID {
			keep = sessionOf(actor)
		}
		if err := a.revokeSessions(ctx, u.ID, keep); err != nil {
			return nil, err
		}
		a.log.Info("password reset by admin", "userId", u.ID, "by", actor.User.ID)
	}
	return u, nil
}

// DeleteUser removes an account. Admin only. It refuses to delete the last
// active admin, the actor included.
//
// A space the account owns alone passes to the acting admin, or to another
// active admin when admins delete themselves, so no space is left without an
// owner. Memberships, shares granted to the account, stars, sessions, API
// tokens and personal AI providers go with the account. What the account
// created for others stays: notes keep the author id, comments show a
// deleted-user author, shares and public links keep working, and an invite
// keeps working unless it grants admin, because an admin invite needs a
// creator who is still an active admin.
func (a *App) DeleteUser(ctx context.Context, actor *core.Actor, id string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	adminMu.Lock()
	defer adminMu.Unlock()
	u, err := a.db.GetUser(ctx, id)
	if err != nil {
		return fmt.Errorf("load user %s: %w", id, err)
	}
	if u == nil {
		return fmt.Errorf("user %s: %w", id, core.ErrNotFound)
	}
	if a.isPluginUser(ctx, u.ID) {
		return fmt.Errorf("user %s belongs to a plugin, uninstall the plugin instead: %w", id, core.ErrConflict)
	}
	other, err := a.otherActiveAdmin(ctx, id)
	if err != nil {
		return err
	}
	if u.IsAdmin && u.IsActive && other == nil {
		return lastAdminError(u)
	}
	heir := other
	if actor.UserID() != id {
		heir = actor.User
	}

	ownerMu.Lock()
	defer ownerMu.Unlock()
	members, err := a.db.ListMembershipsForUser(ctx, id)
	if err != nil {
		return fmt.Errorf("list memberships: %w", err)
	}
	if err := a.handOverSpaces(ctx, u, heir, members); err != nil {
		return err
	}
	for _, m := range members {
		if err := a.db.RemoveMember(ctx, m.SpaceID, id); err != nil {
			a.log.Warn("remove membership failed", "userId", id, "spaceId", m.SpaceID, "err", err)
		}
	}
	if shares, err := a.db.ListSharesForUser(ctx, id); err != nil {
		a.log.Warn("list shares failed", "userId", id, "err", err)
	} else {
		for _, s := range shares {
			if err := a.db.DeleteShare(ctx, s.ID); err != nil {
				a.log.Warn("delete share failed", "userId", id, "shareId", s.ID, "err", err)
			}
		}
	}
	if starred, err := a.db.ListStarred(ctx, id); err != nil {
		a.log.Warn("list stars failed", "userId", id, "err", err)
	} else {
		for _, n := range starred {
			if err := a.db.SetStar(ctx, id, n.ID, false); err != nil {
				a.log.Warn("clear star failed", "userId", id, "noteId", n.ID, "err", err)
			}
		}
	}
	if err := a.revokeSessions(ctx, id, ""); err != nil {
		a.log.Warn("revoke sessions failed", "userId", id, "err", err)
	}
	if tokens, err := a.db.ListAPITokens(ctx, id); err != nil {
		a.log.Warn("list api tokens failed", "userId", id, "err", err)
	} else {
		for _, t := range tokens {
			if err := a.db.DeleteAPIToken(ctx, t.ID, id); err != nil && !errors.Is(err, core.ErrNotFound) {
				a.log.Warn("delete api token failed", "userId", id, "tokenId", t.ID, "err", err)
			}
		}
	}
	if providers, err := a.db.ListAIProviders(ctx); err != nil {
		a.log.Warn("list ai providers failed", "userId", id, "err", err)
	} else {
		for _, p := range providers {
			if p.OwnerID != id {
				continue
			}
			if err := a.db.DeleteAIProvider(ctx, p.ID); err != nil {
				a.log.Warn("delete personal ai provider failed", "userId", id, "providerId", p.ID, "err", err)
				continue
			}
			if a.ai != nil {
				a.evictProvider(p.ID)
			}
		}
	}
	if err := a.db.DeleteAIBudget(ctx, core.BudgetUser, id); err != nil && !errors.Is(err, core.ErrNotFound) {
		a.log.Warn("delete ai budget failed", "userId", id, "err", err)
	}
	if err := a.db.DeleteUser(ctx, id); err != nil {
		return fmt.Errorf("delete user %s: %w", id, err)
	}
	a.log.Info("user deleted", "userId", id, "by", actor.User.ID)
	return nil
}

// handOverSpaces makes heir an owner of every space that u owns alone,
// raising an existing membership or adding a new one, so deleting u leaves no
// space without an owner. The caller holds ownerMu.
func (a *App) handOverSpaces(ctx context.Context, u, heir *core.User, members []*core.SpaceMember) error {
	for _, m := range members {
		if m.Role != core.RoleOwner {
			continue
		}
		all, err := a.db.ListMembers(ctx, m.SpaceID)
		if err != nil {
			return fmt.Errorf("list members: %w", err)
		}
		coOwned := false
		var heirRole core.Role
		for _, o := range all {
			if o.UserID == u.ID {
				continue
			}
			if o.Role == core.RoleOwner {
				coOwned = true
			}
			if heir != nil && o.UserID == heir.ID {
				heirRole = o.Role
			}
		}
		if coOwned {
			continue
		}
		if heir == nil {
			return fmt.Errorf("space %s has no other owner and no active admin can take it over: %w", m.SpaceID, core.ErrConflict)
		}
		if heirRole != "" {
			err = a.db.UpdateMember(ctx, m.SpaceID, heir.ID, core.RoleOwner)
		} else {
			err = a.db.AddMember(ctx, &core.SpaceMember{SpaceID: m.SpaceID, UserID: heir.ID, Role: core.RoleOwner, CreatedAt: a.now()})
		}
		if err != nil {
			return fmt.Errorf("hand over space %s: %w", m.SpaceID, err)
		}
		a.log.Info("space ownership transferred", "spaceId", m.SpaceID, "from", u.ID, "to", heir.ID)
	}
	return nil
}

// CreateInvite issues a signup invite. Admin only.
func (a *App) CreateInvite(ctx context.Context, actor *core.Actor, in core.InviteInput) (*core.Invite, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	email := a.hash.NormalizeEmail(in.Email)
	if email != "" {
		if err := a.hash.ValidateEmail(email); err != nil {
			return nil, fmt.Errorf("email: %w", err)
		}
	}
	if in.SpaceID != "" {
		if _, _, err := a.requireSpace(ctx, actor, in.SpaceID, core.AccessManage); err != nil {
			return nil, err
		}
		if in.Role != "" && !in.Role.Valid() {
			return nil, core.Invalid("role", "unknown role "+string(in.Role))
		}
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = defaultInviteTTL
	}
	now := a.now()
	inv := &core.Invite{
		ID:        core.NewID(),
		Email:     email,
		Token:     core.NewToken(16),
		SpaceID:   in.SpaceID,
		Role:      in.Role,
		IsAdmin:   in.IsAdmin,
		CreatedBy: actor.User.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if err := a.db.CreateInvite(ctx, inv); err != nil {
		return nil, fmt.Errorf("create invite: %w", err)
	}
	a.log.Info("invite created", "inviteId", inv.ID, "email", email, "by", actor.User.ID)
	return inv, nil
}

// ListInvites returns every invite. Admin only.
func (a *App) ListInvites(ctx context.Context, actor *core.Actor) ([]*core.Invite, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	out, err := a.db.ListInvites(ctx)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	return out, nil
}

// RevokeInvite deletes an invite. Admin only.
func (a *App) RevokeInvite(ctx context.Context, actor *core.Actor, id string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if err := a.db.DeleteInvite(ctx, id); err != nil {
		return fmt.Errorf("delete invite %s: %w", id, err)
	}
	return nil
}
