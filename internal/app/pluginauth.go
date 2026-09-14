package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// SetPluginResolver installs the plugin host's credential resolver. Without
// one, plugin and acting tokens are refused.
func (a *App) SetPluginResolver(r core.PluginResolver) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	a.plugins = r
}

func (a *App) pluginResolver() core.PluginResolver {
	a.pluginMu.RLock()
	defer a.pluginMu.RUnlock()
	return a.plugins
}

// userKinds returns the account kind port of the database, when it has one.
func (a *App) userKinds() ports.UserKinds {
	k, _ := a.db.(ports.UserKinds)
	return k
}

// requireSignIn refuses a user whose kind cannot sign in, such as a plugin
// user. A database without user kinds treats every user as a person.
func (a *App) requireSignIn(ctx context.Context, userID string) error {
	k := a.userKinds()
	if k == nil {
		return nil
	}
	ok, err := k.UserCanSignIn(ctx, userID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return fmt.Errorf("user %s is gone: %w", userID, core.ErrUnauthorized)
		}
		return fmt.Errorf("check user kind: %w", err)
	}
	if !ok {
		return fmt.Errorf("this account cannot sign in: %w", core.ErrUnauthorized)
	}
	return nil
}

// isPluginUser reports whether a user row belongs to a plugin.
func (a *App) isPluginUser(ctx context.Context, userID string) bool {
	k := a.userKinds()
	if k == nil {
		return false
	}
	kind, err := k.UserKind(ctx, userID)
	return err == nil && kind == core.UserKindPlugin
}

// activeUser loads an active user or returns core.ErrUnauthorized.
func (a *App) activeUser(ctx context.Context, userID string) (*core.User, error) {
	u, err := a.db.GetUser(ctx, userID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load user: %w", err)
	}
	if u == nil {
		return nil, fmt.Errorf("user is gone: %w", core.ErrUnauthorized)
	}
	if !u.IsActive {
		return nil, fmt.Errorf("account is disabled: %w", core.ErrUnauthorized)
	}
	return u, nil
}

// unauthorized keeps a resolver failure that is not a credential problem, so
// a database error does not read as a bad token.
func unauthorized(what string, err error) error {
	if err == nil || errors.Is(err, core.ErrUnauthorized) || errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("%s: %w", what, core.ErrUnauthorized)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// actorFromPluginToken resolves a plugin token. The plugin must be running,
// and its user must be an active plugin user.
func (a *App) actorFromPluginToken(ctx context.Context, token string) (*core.Actor, error) {
	r := a.pluginResolver()
	if r == nil {
		return nil, fmt.Errorf("plugin tokens are not accepted: %w", core.ErrUnauthorized)
	}
	id, err := r.ResolvePluginToken(ctx, token)
	if err != nil || id == nil {
		return nil, unauthorized("plugin token", err)
	}
	if !core.PluginStatusRuns(id.Status) {
		return nil, fmt.Errorf("plugin %s is %s: %w", id.ID, id.Status, core.ErrUnauthorized)
	}
	u, err := a.pluginUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return &core.Actor{User: u, Plugin: id}, nil
}

// pluginUser loads the active plugin-kind user of a plugin.
func (a *App) pluginUser(ctx context.Context, id *core.PluginIdentity) (*core.User, error) {
	u, err := a.activeUser(ctx, id.UserID)
	if err != nil {
		return nil, fmt.Errorf("plugin %s user: %w", id.ID, err)
	}
	if k := a.userKinds(); k != nil {
		kind, err := k.UserKind(ctx, u.ID)
		if err != nil {
			return nil, unauthorized("plugin user kind", err)
		}
		if kind != core.UserKindPlugin {
			return nil, fmt.Errorf("plugin %s user is not a plugin user: %w", id.ID, core.ErrUnauthorized)
		}
	}
	return u, nil
}

// actorFromActingToken resolves an acting token. The token must be unexpired
// and its interaction must still be valid, the plugin must be running with an
// active plugin user, and the person must be active and able to sign in.
func (a *App) actorFromActingToken(ctx context.Context, token string) (*core.Actor, error) {
	r := a.pluginResolver()
	if r == nil {
		return nil, fmt.Errorf("acting tokens are not accepted: %w", core.ErrUnauthorized)
	}
	at, err := r.ResolveActingToken(ctx, token)
	if err != nil || at == nil || at.Plugin == nil {
		return nil, unauthorized("acting token", err)
	}
	if !a.now().Before(at.ExpiresAt) {
		return nil, fmt.Errorf("acting token expired: %w", core.ErrUnauthorized)
	}
	if !core.PluginStatusRuns(at.Plugin.Status) {
		return nil, fmt.Errorf("plugin %s is %s: %w", at.Plugin.ID, at.Plugin.Status, core.ErrUnauthorized)
	}
	if !r.ValidInteraction(ctx, at.Plugin.ID, at.Interaction) {
		return nil, fmt.Errorf("acting token interaction %q is no longer valid: %w", at.Interaction, core.ErrUnauthorized)
	}
	if _, err := a.pluginUser(ctx, at.Plugin); err != nil {
		return nil, err
	}
	u, err := a.activeUser(ctx, at.UserID)
	if err != nil {
		return nil, err
	}
	if err := a.requireSignIn(ctx, u.ID); err != nil {
		return nil, err
	}
	return &core.Actor{User: u, Plugin: at.Plugin, Via: at.Plugin.ID}, nil
}
