package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// maxLinkAccess is the strongest access a public link may carry.
const maxLinkAccess = core.AccessComment

// ListShares lists the user grants on an item of any kind. It needs
// AccessManage.
func (a *App) ListShares(ctx context.Context, actor *core.Actor, noteID string) ([]*core.Share, error) {
	if _, _, err := a.requireItemAny(ctx, actor, noteID, core.AccessManage); err != nil {
		return nil, err
	}
	shares, err := a.db.ListSharesForNote(ctx, noteID)
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	for _, s := range shares {
		if s.User == nil {
			if u, err := a.db.GetUser(ctx, s.UserID); err == nil && u != nil {
				s.User = publicUser(u)
			}
		}
	}
	return shares, nil
}

// publicUser keeps only the fields other users may see.
func publicUser(u *core.User) *core.User {
	return &core.User{ID: u.ID, Name: u.Name, Email: u.Email, AvatarColor: u.AvatarColor}
}

// CreateShare grants a user access to an item of any kind, by user id or
// email. It needs AccessManage. Sharing with oneself is invalid. A second
// share for the same user updates the existing one.
func (a *App) CreateShare(ctx context.Context, actor *core.Actor, in core.ShareInput) (*core.Share, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	it, _, err := a.requireItem(ctx, actor, in.NoteID, core.AccessManage)
	if err != nil {
		return nil, err
	}
	note := &core.Note{ID: it.ID(), SpaceID: it.SpaceID()}
	if in.Access < core.AccessView || in.Access > core.AccessManage {
		return nil, core.Invalid("access", "access must be view, comment, edit or manage")
	}
	var target *core.User
	switch {
	case in.UserID != "":
		target, err = a.db.GetUser(ctx, in.UserID)
	case strings.TrimSpace(in.Email) != "":
		target, err = a.db.GetUserByEmail(ctx, a.hash.NormalizeEmail(in.Email))
	default:
		return nil, core.Invalid("userId", "a user id or email is required")
	}
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load share recipient: %w", err)
	}
	if target == nil {
		return nil, fmt.Errorf("share recipient: %w", core.ErrNotFound)
	}
	if !target.IsActive {
		return nil, fmt.Errorf("share recipient %s is deactivated: %w", target.ID, core.ErrConflict)
	}
	if target.ID == actor.User.ID {
		return nil, core.Invalid("userId", "you cannot share a note with yourself")
	}
	if m, err := a.db.GetMember(ctx, note.SpaceID, target.ID); err == nil && m != nil {
		if m.Role.Access() >= in.Access {
			a.log.Info("share does not raise access of a space member",
				"noteId", note.ID, "userId", target.ID, "role", m.Role, "access", in.Access.String())
		}
	}

	existing, err := a.db.FindShare(ctx, note.ID, target.ID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("find share: %w", err)
	}
	if existing != nil {
		existing.Access = in.Access
		existing.IncludeChildren = in.IncludeChildren
		if err := a.db.UpdateShare(ctx, existing); err != nil {
			return nil, fmt.Errorf("update share: %w", err)
		}
		existing.User = publicUser(target)
		return existing, nil
	}
	s := &core.Share{
		ID:              core.NewID(),
		NoteID:          note.ID,
		SpaceID:         note.SpaceID,
		UserID:          target.ID,
		Access:          in.Access,
		IncludeChildren: in.IncludeChildren,
		CreatedBy:       actor.User.ID,
		CreatedAt:       a.now(),
	}
	if err := a.db.CreateItemShare(ctx, it.Kind, s); err != nil {
		return nil, fmt.Errorf("create share: %w", err)
	}
	s.User = publicUser(target)
	a.log.Info("note shared", "noteId", note.ID, "userId", target.ID, "access", in.Access.String(), "by", actor.User.ID)
	return s, nil
}

// shareForChange loads a share and checks AccessManage on its note.
func (a *App) shareForChange(ctx context.Context, actor *core.Actor, id string) (*core.Share, error) {
	s, err := a.db.GetShare(ctx, id)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load share: %w", err)
	}
	if s == nil {
		return nil, fmt.Errorf("share %s: %w", id, denied(actor))
	}
	if _, _, err := a.requireItemAny(ctx, actor, s.NoteID, core.AccessManage); err != nil {
		return nil, err
	}
	return s, nil
}

// UpdateShare changes the access of a share. It needs AccessManage.
func (a *App) UpdateShare(ctx context.Context, actor *core.Actor, id string, access core.Access, includeChildren bool) (*core.Share, error) {
	s, err := a.shareForChange(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if access < core.AccessView || access > core.AccessManage {
		return nil, core.Invalid("access", "access must be view, comment, edit or manage")
	}
	s.Access = access
	s.IncludeChildren = includeChildren
	if err := a.db.UpdateShare(ctx, s); err != nil {
		return nil, fmt.Errorf("update share: %w", err)
	}
	return s, nil
}

// DeleteShare revokes a share. It needs AccessManage.
func (a *App) DeleteShare(ctx context.Context, actor *core.Actor, id string) error {
	s, err := a.shareForChange(ctx, actor, id)
	if err != nil {
		return err
	}
	if err := a.db.DeleteShare(ctx, s.ID); err != nil {
		return fmt.Errorf("delete share: %w", err)
	}
	return nil
}

// ListSharedWithMe returns the shares granted to the actor with their notes.
func (a *App) ListSharedWithMe(ctx context.Context, actor *core.Actor) ([]*core.Share, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	shares, err := a.db.ListSharesForUser(ctx, actor.User.ID)
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	out := make([]*core.Share, 0, len(shares))
	for _, s := range shares {
		if s.Note == nil {
			n, err := a.db.GetNote(ctx, s.NoteID)
			if err != nil || n == nil {
				continue
			}
			s.Note = n
		}
		if s.Note.Deleted {
			continue
		}
		if sp, err := a.db.GetSpace(ctx, s.Note.SpaceID); err == nil && sp != nil {
			s.Note.SpaceSlug = sp.Slug
		}
		out = append(out, s)
	}
	return out, nil
}

// ListPublicLinks lists the public links of an item. It needs AccessManage.
func (a *App) ListPublicLinks(ctx context.Context, actor *core.Actor, noteID string) ([]*core.PublicLink, error) {
	if _, _, err := a.requireItemAny(ctx, actor, noteID, core.AccessManage); err != nil {
		return nil, err
	}
	links, err := a.db.ListPublicLinks(ctx, noteID)
	if err != nil {
		return nil, fmt.Errorf("list public links: %w", err)
	}
	for _, l := range links {
		l.HasPassword = l.PasswordHash != "" || l.HasPassword
	}
	return links, nil
}

// clampLinkAccess limits public link access to AccessComment and defaults an
// empty value to AccessView.
func clampLinkAccess(in core.Access) core.Access {
	if in <= core.AccessNone {
		return core.AccessView
	}
	if in > maxLinkAccess {
		return maxLinkAccess
	}
	return in
}

// CreatePublicLink issues an anonymous link to an item of any kind. It needs
// AccessManage. Access is capped at AccessComment.
func (a *App) CreatePublicLink(ctx context.Context, actor *core.Actor, in core.PublicLinkInput) (*core.PublicLink, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	it, _, err := a.requireItem(ctx, actor, in.NoteID, core.AccessManage)
	if err != nil {
		return nil, err
	}
	note := &core.Note{ID: it.ID(), SpaceID: it.SpaceID()}
	l := &core.PublicLink{
		ID:              core.NewID(),
		Token:           core.NewToken(16),
		NoteID:          note.ID,
		SpaceID:         note.SpaceID,
		Access:          clampLinkAccess(in.Access),
		IncludeChildren: in.IncludeChildren,
		ExpiresAt:       in.ExpiresAt,
		CreatedBy:       actor.User.ID,
		CreatedAt:       a.now(),
	}
	if in.Password != nil && *in.Password != "" {
		h, err := a.hash.Hash(*in.Password)
		if err != nil {
			return nil, fmt.Errorf("hash link password: %w", err)
		}
		l.PasswordHash = h
		l.HasPassword = true
	}
	if err := a.db.CreateItemPublicLink(ctx, it.Kind, l); err != nil {
		return nil, fmt.Errorf("create public link: %w", err)
	}
	a.log.Info("public link created", "linkId", l.ID, "noteId", note.ID, "access", l.Access.String(), "by", actor.User.ID)
	return l, nil
}

// UpdatePublicLink changes a public link. It needs AccessManage. A nil
// password keeps the current one and an empty password removes it.
func (a *App) UpdatePublicLink(ctx context.Context, actor *core.Actor, id string, in core.PublicLinkInput) (*core.PublicLink, error) {
	l, err := a.db.GetPublicLink(ctx, id)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load public link: %w", err)
	}
	if l == nil {
		return nil, fmt.Errorf("public link %s: %w", id, denied(actor))
	}
	if _, _, err := a.requireItemAny(ctx, actor, l.NoteID, core.AccessManage); err != nil {
		return nil, err
	}
	l.Access = clampLinkAccess(in.Access)
	l.IncludeChildren = in.IncludeChildren
	l.ExpiresAt = in.ExpiresAt
	if in.Password != nil {
		if *in.Password == "" {
			l.PasswordHash = ""
		} else {
			h, err := a.hash.Hash(*in.Password)
			if err != nil {
				return nil, fmt.Errorf("hash link password: %w", err)
			}
			l.PasswordHash = h
		}
	}
	l.HasPassword = l.PasswordHash != ""
	if err := a.db.UpdatePublicLink(ctx, l); err != nil {
		return nil, fmt.Errorf("update public link: %w", err)
	}
	return l, nil
}

// DeletePublicLink revokes a public link. It needs AccessManage.
func (a *App) DeletePublicLink(ctx context.Context, actor *core.Actor, id string) error {
	l, err := a.db.GetPublicLink(ctx, id)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("load public link: %w", err)
	}
	if l == nil {
		return fmt.Errorf("public link %s: %w", id, denied(actor))
	}
	if _, _, err := a.requireItemAny(ctx, actor, l.NoteID, core.AccessManage); err != nil {
		return err
	}
	if err := a.db.DeletePublicLink(ctx, l.ID); err != nil {
		return fmt.Errorf("delete public link: %w", err)
	}
	return nil
}

// resolveLinkActor validates a token and optional password, and returns an
// anonymous actor scoped to the link. A missing or wrong password yields
// core.ErrUnauthorized. An unknown or expired token yields core.ErrNotFound.
func (a *App) resolveLinkActor(ctx context.Context, token, password string) (*core.Actor, error) {
	if token == "" {
		return nil, fmt.Errorf("public link: %w", core.ErrNotFound)
	}
	l, err := a.db.GetPublicLinkByToken(ctx, token)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load public link: %w", err)
	}
	if l == nil || l.Token != token {
		return nil, fmt.Errorf("public link: %w", core.ErrNotFound)
	}
	if l.Expired(a.now()) {
		return nil, fmt.Errorf("public link expired: %w", core.ErrNotFound)
	}
	if l.PasswordHash != "" {
		if password == "" {
			return nil, fmt.Errorf("public link needs a password: %w", core.ErrUnauthorized)
		}
		ok, err := a.hash.Verify(l.PasswordHash, password)
		if err != nil || !ok {
			return nil, fmt.Errorf("public link password is wrong: %w", core.ErrUnauthorized)
		}
	}
	l.HasPassword = l.PasswordHash != ""
	l.Access = clampLinkAccess(l.Access)
	return &core.Actor{Link: l}, nil
}

// ResolvePublicLink validates a token and optional password, and returns an
// anonymous actor scoped to the link plus the target note with its body. A
// link on a folder or file returns core.ErrNotFound, see ResolvePublicItem.
func (a *App) ResolvePublicLink(ctx context.Context, token, password string) (*core.Actor, *core.Note, error) {
	actor, it, err := a.ResolvePublicItem(ctx, token, password)
	if err != nil {
		return nil, nil, err
	}
	if it.Note == nil {
		return nil, nil, fmt.Errorf("public link target is not a note: %w", core.ErrNotFound)
	}
	return actor, it.Note, nil
}

// ResolvePublicItem validates a token and returns the link actor and the item
// the link targets. A note carries its body. A trashed target is not found.
func (a *App) ResolvePublicItem(ctx context.Context, token, password string) (*core.Actor, *core.Item, error) {
	actor, err := a.resolveLinkActor(ctx, token, password)
	if err != nil {
		return nil, nil, err
	}
	it, err := a.GetItem(ctx, actor, actor.Link.NoteID)
	if err != nil {
		return nil, nil, err
	}
	if itemDeleted(it) {
		return nil, nil, fmt.Errorf("public link target is in the trash: %w", core.ErrNotFound)
	}
	if it.Note != nil {
		note, _, err := a.GetNote(ctx, actor, it.Note.ID)
		if err != nil {
			return nil, nil, err
		}
		it.Note = note
	}
	if err := a.db.IncrementLinkViews(ctx, actor.Link.ID); err != nil {
		a.log.Debug("increment link views failed", "linkId", actor.Link.ID, "err", err)
	}
	return actor, it, nil
}
