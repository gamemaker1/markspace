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

// timingPlaceholder is hashed once and verified against on an unknown email,
// so a failed login costs the same whether or not the account exists.
const timingPlaceholder = "markspace-timing-placeholder"

// avatarPalette colours the generated avatar of a new account.
var avatarPalette = []string{"#a21caf", "#be185d", "#7e22ce", "#0f766e", "#c2410c", "#475569"}

// welcomeBody is the body of the note created with a personal space.
const welcomeBody = `Welcome to Markspace.

Every note here is one markdown file on disk. Edit it in this app or in your
own editor, both work.

- Use ` + "`[[wikilinks]]`" + ` to link notes.
- Use ` + "`#tags`" + ` anywhere in the body.
- Drop a file into the editor to attach it.
`

// NeedsSetup reports whether the instance has no users yet.
func (a *App) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := a.db.CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return n == 0, nil
}

// Signup creates an account. The first account on a fresh instance becomes an
// active admin and gets a personal space with a welcome note. Later accounts
// need either open signup or a valid invite.
func (a *App) Signup(ctx context.Context, in core.SignupInput) (*core.User, error) {
	email := a.hash.NormalizeEmail(in.Email)
	if err := a.hash.ValidateEmail(email); err != nil {
		return nil, fmt.Errorf("email: %w", err)
	}
	if err := a.hash.ValidatePassword(in.Password); err != nil {
		return nil, fmt.Errorf("password: %w", err)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}

	// The user count, the invite check, the insert and the invite consumption
	// run as one step, so two signups cannot both become the first admin or
	// both redeem one invite.
	signupMu.Lock()
	defer signupMu.Unlock()

	count, err := a.db.CountUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	first := count == 0

	var invite *core.Invite
	if !first {
		settings, err := a.settings(ctx)
		if err != nil {
			return nil, err
		}
		if token := strings.TrimSpace(in.InviteToken); token != "" {
			invite, err = a.usableInvite(ctx, token, email)
			if err != nil {
				return nil, err
			}
		} else if !settings.AllowSignup {
			return nil, fmt.Errorf("signup is closed: %w", core.ErrForbidden)
		}
	}

	if existing, err := a.db.GetUserByEmail(ctx, email); err == nil && existing != nil {
		return nil, fmt.Errorf("email %s: %w", email, core.ErrExists)
	} else if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("lookup email: %w", err)
	}

	hashed, err := a.hash.Hash(in.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	now := a.now()
	u := &core.User{
		ID:           core.NewID(),
		Email:        email,
		Name:         name,
		PasswordHash: hashed,
		IsAdmin:      first || (invite != nil && invite.IsAdmin),
		IsActive:     true,
		AvatarColor:  avatarPalette[len(email)%len(avatarPalette)],
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := a.db.CreateUser(ctx, u); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	if invite != nil {
		if err := a.db.MarkInviteUsed(ctx, invite.ID, now); err != nil {
			a.log.Warn("mark invite used failed", "inviteId", invite.ID, "err", err)
		}
		if invite.SpaceID != "" {
			role := invite.Role
			if !role.Valid() {
				role = core.RoleEditor
			}
			if _, err := a.space(ctx, invite.SpaceID); err != nil {
				// The space was deleted after the invite was issued. The account
				// stays, without a membership that points nowhere.
				a.log.Warn("invite space is gone, skipping membership", "inviteId", invite.ID, "spaceId", invite.SpaceID, "err", err)
			} else {
				m := &core.SpaceMember{SpaceID: invite.SpaceID, UserID: u.ID, Role: role, CreatedAt: now}
				if err := a.db.AddMember(ctx, m); err != nil {
					a.log.Warn("apply invite membership failed", "inviteId", invite.ID, "err", err)
				}
			}
		}
	}

	if first {
		if err := a.createPersonalSpace(ctx, u); err != nil {
			a.log.Warn("create personal space failed", "userId", u.ID, "err", err)
		}
	}
	a.log.Info("account created", "userId", u.ID, "email", email, "admin", u.IsAdmin, "first", first)
	return u, nil
}

// createPersonalSpace gives a new user a space of their own with one note.
func (a *App) createPersonalSpace(ctx context.Context, u *core.User) error {
	space, err := a.newSpace(ctx, u.Name, core.Slugify(u.Name), u.ID)
	if err != nil {
		return err
	}
	now := a.now()
	note := &core.Note{
		ID:        core.NewID(),
		SpaceID:   space.ID,
		Title:     "Welcome",
		Content:   welcomeBody,
		Position:  100,
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: u.ID,
		UpdatedBy: u.ID,
	}
	path, err := a.files.AllocNotePath(space.Slug, "", note.Title, "")
	if err != nil {
		return fmt.Errorf("allocate welcome path: %w", err)
	}
	note.Path = path
	note.Slug = pathSlug(path)
	if err := a.writeNote(space.Slug, note); err != nil {
		return err
	}
	return a.indexNote(ctx, space, note)
}

// Login verifies a password and returns the user plus the plaintext session
// token. An unknown email still runs a hash verification, so the reply time
// does not disclose whether the account exists.
func (a *App) Login(ctx context.Context, email, password, userAgent, ip string) (*core.User, string, error) {
	email = a.hash.NormalizeEmail(email)
	u, err := a.db.GetUserByEmail(ctx, email)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, "", fmt.Errorf("lookup email: %w", err)
	}
	if u == nil {
		if _, verr := a.hash.Verify(a.placeholderHash(), password); verr != nil {
			a.log.Debug("placeholder verify failed", "err", verr)
		}
		return nil, "", fmt.Errorf("login failed: %w", core.ErrUnauthorized)
	}
	ok, err := a.hash.Verify(u.PasswordHash, password)
	if err != nil {
		a.log.Warn("password verify failed", "userId", u.ID, "err", err)
		return nil, "", fmt.Errorf("login failed: %w", core.ErrUnauthorized)
	}
	if !ok {
		return nil, "", fmt.Errorf("login failed: %w", core.ErrUnauthorized)
	}
	if !u.IsActive {
		return nil, "", fmt.Errorf("account is disabled: %w", core.ErrUnauthorized)
	}
	if err := a.requireSignIn(ctx, u.ID); err != nil {
		return nil, "", err
	}

	now := a.now()
	plain, hash := a.hash.NewSessionToken()
	s := &core.Session{
		ID:        core.NewID(),
		UserID:    u.ID,
		TokenHash: hash,
		UserAgent: userAgent,
		IP:        ip,
		CreatedAt: now,
		ExpiresAt: now.Add(a.cfg.SessionTTL),
		LastSeen:  now,
	}
	if err := a.db.CreateSession(ctx, s); err != nil {
		return nil, "", fmt.Errorf("create session: %w", err)
	}
	if err := a.db.TouchUserLogin(ctx, u.ID, now); err != nil {
		a.log.Warn("touch last login failed", "userId", u.ID, "err", err)
	}
	a.log.Info("login", "userId", u.ID, "ip", ip)
	return u, plain, nil
}

// placeholderHash returns a stored-shaped hash to compare against on an
// unknown email. It is computed once.
func (a *App) placeholderHash() string {
	a.dummyOnce.Do(func() {
		h, err := a.hash.Hash(timingPlaceholder)
		if err != nil {
			a.log.Warn("build placeholder hash failed", "err", err)
			return
		}
		a.dummyHash = h
	})
	return a.dummyHash
}

// Logout deletes the session behind the plaintext token.
func (a *App) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	if err := a.db.DeleteSessionByHash(ctx, a.hash.HashToken(sessionToken)); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// ActorFromSession resolves a session cookie into an actor. An unknown,
// expired or disabled session yields core.ErrUnauthorized.
func (a *App) ActorFromSession(ctx context.Context, sessionToken string) (*core.Actor, error) {
	if sessionToken == "" {
		return nil, fmt.Errorf("no session: %w", core.ErrUnauthorized)
	}
	s, err := a.db.GetSessionByHash(ctx, a.hash.HashToken(sessionToken))
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load session: %w", err)
	}
	if s == nil {
		return nil, fmt.Errorf("unknown session: %w", core.ErrUnauthorized)
	}
	now := a.now()
	if now.After(s.ExpiresAt) {
		if err := a.db.DeleteSession(ctx, s.ID); err != nil {
			a.log.Debug("delete expired session failed", "sessionId", s.ID, "err", err)
		}
		return nil, fmt.Errorf("session expired: %w", core.ErrUnauthorized)
	}
	u, err := a.db.GetUser(ctx, s.UserID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load session user: %w", err)
	}
	if u == nil {
		return nil, fmt.Errorf("session user is gone: %w", core.ErrUnauthorized)
	}
	if !u.IsActive {
		return nil, fmt.Errorf("account is disabled: %w", core.ErrUnauthorized)
	}
	if err := a.requireSignIn(ctx, u.ID); err != nil {
		return nil, err
	}
	if now.Sub(s.LastSeen) > time.Minute {
		if err := a.db.TouchSession(ctx, s.ID, now); err != nil {
			a.log.Debug("touch session failed", "sessionId", s.ID, "err", err)
		}
	}
	return &core.Actor{User: u, Session: s}, nil
}

// ActorFromAPIToken resolves a bearer token into an actor.
func (a *App) ActorFromAPIToken(ctx context.Context, token string) (*core.Actor, error) {
	if token == "" {
		return nil, fmt.Errorf("no token: %w", core.ErrUnauthorized)
	}
	switch {
	case strings.HasPrefix(token, core.PluginTokenPrefix):
		return a.actorFromPluginToken(ctx, token)
	case strings.HasPrefix(token, core.ActingTokenPrefix):
		return a.actorFromActingToken(ctx, token)
	}
	t, err := a.db.GetAPITokenByHash(ctx, a.hash.HashToken(token))
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load api token: %w", err)
	}
	if t == nil {
		return nil, fmt.Errorf("unknown token: %w", core.ErrUnauthorized)
	}
	now := a.now()
	if t.ExpiresAt != nil && now.After(*t.ExpiresAt) {
		return nil, fmt.Errorf("token expired: %w", core.ErrUnauthorized)
	}
	if t.IsSystem() {
		a.touchAPIToken(ctx, t, now)
		return core.SystemActor(t), nil
	}
	u, err := a.db.GetUser(ctx, t.UserID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load token user: %w", err)
	}
	if u == nil {
		return nil, fmt.Errorf("token user is gone: %w", core.ErrUnauthorized)
	}
	if !u.IsActive {
		return nil, fmt.Errorf("account is disabled: %w", core.ErrUnauthorized)
	}
	if err := a.requireSignIn(ctx, u.ID); err != nil {
		return nil, err
	}
	a.touchAPIToken(ctx, t, now)
	return &core.Actor{User: u, Token: t}, nil
}

// touchAPIToken records the use of a token at most once a minute.
func (a *App) touchAPIToken(ctx context.Context, t *core.APIToken, now time.Time) {
	if t.LastUsed == nil || now.Sub(*t.LastUsed) > time.Minute {
		if err := a.db.TouchAPIToken(ctx, t.ID, now); err != nil {
			a.log.Debug("touch api token failed", "tokenId", t.ID, "err", err)
		}
	}
}

// ChangePassword replaces the actor's own password after verifying the old
// one, then signs out every other session of the account. The session the
// request came through stays signed in. A request made with an API token has
// no session, so every session ends.
func (a *App) ChangePassword(ctx context.Context, actor *core.Actor, oldPassword, newPassword string) error {
	if err := requireUser(actor); err != nil {
		return err
	}
	u, err := a.db.GetUser(ctx, actor.User.ID)
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	ok, err := a.hash.Verify(u.PasswordHash, oldPassword)
	if err != nil || !ok {
		// ErrInvalid, not ErrUnauthorized: the caller is signed in, and a 401
		// would read as a lost session.
		return core.Invalid("oldPassword", "the current password is wrong")
	}
	if err := a.hash.ValidatePassword(newPassword); err != nil {
		return fmt.Errorf("password: %w", err)
	}
	hashed, err := a.hash.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	u.PasswordHash = hashed
	u.UpdatedAt = a.now()
	if err := a.db.UpdateUser(ctx, u); err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	if err := a.revokeSessions(ctx, u.ID, sessionOf(actor)); err != nil {
		return err
	}
	a.log.Info("password changed", "userId", u.ID)
	return nil
}

// revokeSessions deletes every session of a user except keepID.
func (a *App) revokeSessions(ctx context.Context, userID, keepID string) error {
	sessions, err := a.db.ListSessions(ctx, userID)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	for _, s := range sessions {
		if s.ID == keepID {
			continue
		}
		if err := a.db.DeleteSession(ctx, s.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return fmt.Errorf("revoke session: %w", err)
		}
	}
	return nil
}

// sessionOf returns the id of the session behind an actor, or "" when the
// actor did not authenticate with a session cookie.
func sessionOf(actor *core.Actor) string {
	if actor == nil || actor.Session == nil {
		return ""
	}
	return actor.Session.ID
}

// signupMu serializes Signup within one process.
var signupMu sync.Mutex

// usableInvite loads the invite for a signup and checks that it is unused,
// unexpired and meant for this email. An admin invite also needs a creator
// who is still an active admin, so demoting or deleting an admin retires the
// admin invites they issued.
func (a *App) usableInvite(ctx context.Context, token, email string) (*core.Invite, error) {
	invite, err := a.db.GetInviteByToken(ctx, token)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load invite: %w", err)
	}
	if invite == nil || invite.UsedAt != nil || a.now().After(invite.ExpiresAt) {
		return nil, fmt.Errorf("invite is not usable: %w", core.ErrInvalid)
	}
	if invite.Email != "" && !strings.EqualFold(a.hash.NormalizeEmail(invite.Email), email) {
		return nil, fmt.Errorf("invite is for another email address: %w", core.ErrInvalid)
	}
	if invite.IsAdmin {
		creator, err := a.db.GetUser(ctx, invite.CreatedBy)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			return nil, fmt.Errorf("load invite creator: %w", err)
		}
		if creator == nil || !creator.IsAdmin || !creator.IsActive {
			return nil, fmt.Errorf("admin invite is not usable, its creator is no longer an active admin: %w", core.ErrInvalid)
		}
	}
	return invite, nil
}

// UpdateProfile changes the actor's own account fields.
func (a *App) UpdateProfile(ctx context.Context, actor *core.Actor, in core.ProfileInput) (*core.User, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	u, err := a.db.GetUser(ctx, actor.User.ID)
	if err != nil {
		return nil, fmt.Errorf("load user: %w", err)
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, core.Invalid("name", "name is required")
		}
		u.Name = name
	}
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
			if other != nil {
				return nil, fmt.Errorf("email %s: %w", email, core.ErrExists)
			}
			u.Email = email
		}
	}
	if in.Locale != nil {
		u.Locale = strings.TrimSpace(*in.Locale)
	}
	if in.Timezone != nil {
		u.Timezone = strings.TrimSpace(*in.Timezone)
	}
	if in.AvatarColor != nil {
		u.AvatarColor = strings.TrimSpace(*in.AvatarColor)
	}
	u.UpdatedAt = a.now()
	if err := a.db.UpdateUser(ctx, u); err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}
	return u, nil
}

// ListSessions returns the actor's own sessions.
func (a *App) ListSessions(ctx context.Context, actor *core.Actor) ([]*core.Session, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	out, err := a.db.ListSessions(ctx, actor.User.ID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return out, nil
}

// RevokeSession deletes one of the actor's own sessions.
func (a *App) RevokeSession(ctx context.Context, actor *core.Actor, sessionID string) error {
	if err := requireUser(actor); err != nil {
		return err
	}
	sessions, err := a.db.ListSessions(ctx, actor.User.ID)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	for _, s := range sessions {
		if s.ID == sessionID {
			if err := a.db.DeleteSession(ctx, sessionID); err != nil {
				return fmt.Errorf("delete session: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("session %s: %w", sessionID, core.ErrNotFound)
}

// CreateAPIToken issues a bearer token for the actor and returns the record
// plus the plaintext token, which is never stored.
func (a *App) CreateAPIToken(ctx context.Context, actor *core.Actor, name string, ttl time.Duration) (*core.APIToken, string, error) {
	if err := requireUser(actor); err != nil {
		return nil, "", err
	}
	if actor.IsPlugin() {
		return nil, "", fmt.Errorf("a plugin cannot create API tokens: %w", core.ErrForbidden)
	}
	if err := a.requireSignIn(ctx, actor.User.ID); err != nil {
		return nil, "", fmt.Errorf("%w: %w", err, core.ErrForbidden)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", core.Invalid("name", "token name is required")
	}
	plain, hash, prefix := a.hash.NewAPIToken()
	now := a.now()
	t := &core.APIToken{
		ID:        core.NewID(),
		UserID:    actor.User.ID,
		Name:      name,
		TokenHash: hash,
		Prefix:    prefix,
		CreatedAt: now,
	}
	if ttl > 0 {
		exp := now.Add(ttl)
		t.ExpiresAt = &exp
	}
	if err := a.db.CreateAPIToken(ctx, t); err != nil {
		return nil, "", fmt.Errorf("create api token: %w", err)
	}
	a.log.Info("api token created", "userId", actor.User.ID, "tokenId", t.ID)
	return t, plain, nil
}

// ListAPITokens returns the actor's own tokens.
func (a *App) ListAPITokens(ctx context.Context, actor *core.Actor) ([]*core.APIToken, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	out, err := a.db.ListAPITokens(ctx, actor.User.ID)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	return out, nil
}

// RevokeAPIToken deletes one of the actor's own tokens.
func (a *App) RevokeAPIToken(ctx context.Context, actor *core.Actor, id string) error {
	if err := requireUser(actor); err != nil {
		return err
	}
	if err := a.db.DeleteAPIToken(ctx, id, actor.User.ID); err != nil {
		return fmt.Errorf("delete api token: %w", err)
	}
	return nil
}
