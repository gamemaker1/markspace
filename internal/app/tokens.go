package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// EnsureSystemToken keeps one system API token named name and returns it.
// current is the plaintext the caller holds, or "". When current matches the
// stored token of that name, plain is "" and nothing changes. Otherwise every
// system token of that name is deleted and a new one is issued, and plain is
// its plaintext. Only the hash is stored. The token never expires.
//
// It has no actor because the server calls it at start, before any request.
func (a *App) EnsureSystemToken(ctx context.Context, name, current string) (plain string, token *core.APIToken, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, core.Invalid("name", "token name is required")
	}
	tokens, err := a.db.ListSystemAPITokens(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("list system tokens: %w", err)
	}
	if current != "" {
		hash := a.hash.HashToken(current)
		for _, t := range tokens {
			if t.Name == name && t.TokenHash == hash {
				return "", t, nil
			}
		}
	}
	for _, t := range tokens {
		if t.Name != name {
			continue
		}
		if err := a.db.DeleteSystemAPIToken(ctx, t.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			return "", nil, fmt.Errorf("delete old system token: %w", err)
		}
	}
	plain, hash, prefix := a.hash.NewAPIToken()
	token = &core.APIToken{
		ID:        core.NewID(),
		Name:      name,
		TokenHash: hash,
		Prefix:    prefix,
		CreatedAt: a.now(),
	}
	if err := a.db.CreateAPIToken(ctx, token); err != nil {
		return "", nil, fmt.Errorf("create system token: %w", err)
	}
	return plain, token, nil
}

// ListSystemTokens returns the API tokens that belong to no user. Admin only.
func (a *App) ListSystemTokens(ctx context.Context, actor *core.Actor) ([]*core.APIToken, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	out, err := a.db.ListSystemAPITokens(ctx)
	if err != nil {
		return nil, fmt.Errorf("list system tokens: %w", err)
	}
	return out, nil
}

// RevokeSystemToken deletes one system API token. Admin only. The server
// issues a new token of the same name at its next start.
func (a *App) RevokeSystemToken(ctx context.Context, actor *core.Actor, id string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if err := a.db.DeleteSystemAPIToken(ctx, id); err != nil {
		return fmt.Errorf("delete system token %s: %w", id, err)
	}
	a.log.Info("system api token revoked", "tokenId", id, "by", actor.User.ID)
	return nil
}
