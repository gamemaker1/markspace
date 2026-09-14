package plugin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gamemaker1/markspace/internal/auth"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/sdk"
)

var _ core.PluginResolver = (*Host)(nil)

// ResolvePluginToken implements core.PluginResolver.
func (h *Host) ResolvePluginToken(ctx context.Context, token string) (*core.PluginIdentity, error) {
	if !strings.HasPrefix(token, core.PluginTokenPrefix) {
		return nil, fmt.Errorf("not a plugin token: %w", core.ErrUnauthorized)
	}
	row, err := h.store.GetPluginByTokenHash(ctx, auth.HashToken(token))
	if errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("unknown plugin token: %w", core.ErrUnauthorized)
	}
	if err != nil {
		return nil, err
	}
	if row.Status == core.PluginStatusUninstalled {
		return nil, fmt.Errorf("plugin %s is uninstalled: %w", row.ID, core.ErrUnauthorized)
	}
	return h.identityOf(ctx, row)
}

// ResolveActingToken implements core.PluginResolver.
func (h *Host) ResolveActingToken(ctx context.Context, token string) (*core.ActingToken, error) {
	if !strings.HasPrefix(token, core.ActingTokenPrefix) {
		return nil, fmt.Errorf("not an acting token: %w", core.ErrUnauthorized)
	}
	tok, err := h.store.ResolveActingToken(ctx, auth.HashToken(token), h.now())
	if errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("unknown or expired acting token: %w", core.ErrUnauthorized)
	}
	if err != nil {
		return nil, err
	}
	row, err := h.store.GetPlugin(ctx, tok.PluginID)
	if errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("acting token plugin is gone: %w", core.ErrUnauthorized)
	}
	if err != nil {
		return nil, err
	}
	if row.Status == core.PluginStatusUninstalled {
		return nil, fmt.Errorf("plugin %s is uninstalled: %w", row.ID, core.ErrUnauthorized)
	}
	ident, err := h.identityOf(ctx, row)
	if err != nil {
		return nil, err
	}
	return &core.ActingToken{Plugin: ident, UserID: tok.UserID, Interaction: tok.Interaction, ExpiresAt: tok.ExpiresAt}, nil
}

// PluginIdentity implements core.PluginResolver.
func (h *Host) PluginIdentity(ctx context.Context, pluginID string) (*core.PluginIdentity, error) {
	row, err := h.store.GetPlugin(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	if row.Status == core.PluginStatusUninstalled {
		return nil, fmt.Errorf("plugin %s is uninstalled: %w", pluginID, core.ErrNotFound)
	}
	return h.identityOf(ctx, row)
}

// ValidInteraction implements core.PluginResolver. A command, panel or
// importer interaction must name a contribution of the approved manifest. An
// action names any valid action id. A job interaction needs a job of the
// plugin that has not finished.
func (h *Host) ValidInteraction(ctx context.Context, pluginID, interaction string) bool {
	kind, local, ok := strings.Cut(interaction, ":")
	if !ok || local == "" {
		return false
	}
	var man sdk.Manifest
	if _, m, _, loaded := h.snapshot(pluginID); loaded {
		man = m
	} else if kind != "job" {
		row, err := h.store.GetPlugin(ctx, pluginID)
		if err != nil {
			return false
		}
		if man, err = sdk.ParseManifest(row.ApprovedManifest); err != nil {
			return false
		}
	}
	switch kind {
	case "command":
		return slices.ContainsFunc(man.Commands, func(c sdk.ManifestCommand) bool { return c.ID == local })
	case "panel":
		return slices.ContainsFunc(man.Panels, func(p sdk.ManifestPanel) bool { return p.ID == local })
	case "import":
		return slices.ContainsFunc(man.Importers, func(i sdk.ManifestImporter) bool { return i.ID == local })
	case "action":
		return sdk.ValidLocalID(local)
	case "job":
		j, err := h.opts.Engine.Store().Get(ctx, local)
		return err == nil && j.Module == ModuleID(pluginID) && !j.Status.Finished()
	}
	return false
}
