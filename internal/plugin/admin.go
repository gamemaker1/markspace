package plugin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/auth"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
	"github.com/gamemaker1/markspace/sdk"
)

var _ plugins.Host = (*Host)(nil)

// maxSecretValue caps a stored secret value.
const maxSecretValue = 8 << 10

// List implements plugins.Host. Uninstalled plugins are left out.
func (h *Host) List(ctx context.Context) ([]plugins.PluginView, error) {
	rows, err := h.store.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := h.store.CountDeliveriesByPlugin(ctx)
	if err != nil {
		return nil, err
	}
	out := []plugins.PluginView{}
	for _, row := range rows {
		if row.Status == core.PluginStatusUninstalled {
			continue
		}
		v, err := h.view(ctx, row, counts[row.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Get implements plugins.Host.
func (h *Host) Get(ctx context.Context, id string) (*plugins.PluginView, error) {
	row, err := h.installed(ctx, id)
	if err != nil {
		return nil, err
	}
	counts, err := h.store.CountDeliveriesByPlugin(ctx)
	if err != nil {
		return nil, err
	}
	v, err := h.view(ctx, row, counts[row.ID])
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// installed reads a plugin that is not uninstalled.
func (h *Host) installed(ctx context.Context, id string) (*index.Plugin, error) {
	if !sdk.ValidPluginID(id) {
		return nil, fmt.Errorf("plugin %s: %w", id, core.ErrNotFound)
	}
	row, err := h.store.GetPlugin(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.Status == core.PluginStatusUninstalled {
		return nil, fmt.Errorf("plugin %s: %w", id, core.ErrNotFound)
	}
	return row, nil
}

// view builds the admin view of a plugin row.
func (h *Host) view(ctx context.Context, row *index.Plugin, queued int) (plugins.PluginView, error) {
	approved, _ := sdk.ParseManifest(row.ApprovedManifest)
	v := plugins.PluginView{
		ID: row.ID, Name: row.Name, Version: row.Version, BaseURL: row.BaseURL, Status: row.Status, UserID: row.UserID,
		Approved: summary(approved), Settings: row.Settings, QueuedDeliveries: queued,
		HealthFailures: row.HealthFailures, LastHealthAt: row.LastHealthAt, LastError: row.LastError,
		TokenPrefix: row.TokenPrefix, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Grants: []plugins.Grant{}, Secrets: []plugins.SecretView{},
	}
	if len(v.Settings) == 0 {
		v.Settings = json.RawMessage(`{}`)
	}
	if !bytes.Equal(row.Manifest, row.ApprovedManifest) {
		if pending, err := sdk.ParseManifest(row.Manifest); err == nil {
			s := summary(pending)
			v.Pending = &s
		}
	}
	grants, err := h.store.ListPluginGrants(ctx, row.ID)
	if err != nil {
		return v, err
	}
	for _, g := range grants {
		name := g.SpaceID
		if sp, err := h.store.GetSpace(ctx, g.SpaceID); err == nil && sp != nil {
			name = sp.Name
		}
		v.Grants = append(v.Grants, plugins.Grant{SpaceID: g.SpaceID, SpaceName: name, Role: core.Role(g.Role)})
	}
	sort.Slice(v.Grants, func(i, j int) bool {
		return strings.ToLower(v.Grants[i].SpaceName) < strings.ToLower(v.Grants[j].SpaceName)
	})
	stored, err := h.store.ListPluginSecrets(ctx, row.ID)
	if err != nil {
		return v, err
	}
	for _, s := range approved.Secrets {
		sv := plugins.SecretView{Name: s.Name, Label: s.Label}
		for _, st := range stored {
			if st.Name == s.Name {
				sv.Set, sv.UpdatedAt = true, st.UpdatedAt
			}
		}
		v.Secrets = append(v.Secrets, sv)
	}
	now := h.now()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if _, total, err := h.store.AIUsageReport(ctx, core.AIUsageQuery{From: month, To: now.Add(time.Second), PluginID: row.ID}); err == nil {
		v.AITokensMonth = total.InputTokens + total.OutputTokens
		v.AICostMicrosMonth = total.CostMicros
		v.AICallsMonth = total.Calls
	}
	return v, nil
}

// Preview implements plugins.Host.
func (h *Host) Preview(ctx context.Context, baseURL string) (*plugins.Preview, error) {
	raw, man, err := h.fetchManifest(ctx, baseURL)
	if err != nil {
		return nil, err
	}
	base, _ := normalizeBaseURL(baseURL)
	p := &plugins.Preview{BaseURL: base, Manifest: summary(man), SHA256: digest(raw)}
	if row, err := h.store.GetPlugin(ctx, man.ID); err == nil && row.Status != core.PluginStatusUninstalled {
		p.Installed = true
	}
	return p, nil
}

// grantsOf validates grants from the admin. A role above editor is refused.
func grantsOf(in []plugins.Grant) ([]index.PluginGrant, error) {
	out := make([]index.PluginGrant, 0, len(in))
	for i, g := range in {
		if g.Role == "" || g.Role == "none" {
			continue
		}
		if !core.ValidPluginRole(g.Role) {
			return nil, core.Invalid(fmt.Sprintf("grants[%d].role", i), "a plugin may be a viewer, commenter or editor")
		}
		out = append(out, index.PluginGrant{SpaceID: g.SpaceID, Role: string(g.Role)})
	}
	return out, nil
}

// checkSecrets accepts only secrets the manifest declares.
func checkSecrets(man sdk.Manifest, in map[string]string) error {
	for name, v := range in {
		if !slices.ContainsFunc(man.Secrets, func(s sdk.ManifestSecret) bool { return s.Name == name }) {
			return core.Invalid("secrets."+name, "the manifest declares no such secret")
		}
		if len(v) > maxSecretValue {
			return core.Invalid("secrets."+name, "is longer than 8 KiB")
		}
	}
	return nil
}

// newSecret returns 32 random bytes as padded standard base64.
func newSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("plugin: entropy source failed: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(b)
}

// Install implements plugins.Host. It fetches the manifest again and refuses
// one that changed since the preview.
func (h *Host) Install(ctx context.Context, in plugins.InstallInput) (*plugins.InstallResult, error) {
	raw, man, err := h.fetchManifest(ctx, in.BaseURL)
	if err != nil {
		return nil, err
	}
	if in.SHA256 != "" && digest(raw) != in.SHA256 {
		return nil, fmt.Errorf("the manifest changed since the review, check it again: %w", core.ErrConflict)
	}
	base, _ := normalizeBaseURL(in.BaseURL)
	existing, err := h.store.GetPlugin(ctx, man.ID)
	switch {
	case errors.Is(err, core.ErrNotFound):
		existing = nil
	case err != nil:
		return nil, err
	case existing.Status != core.PluginStatusUninstalled:
		return nil, fmt.Errorf("plugin %s is already installed: %w", man.ID, core.ErrExists)
	}
	if _, ok := h.opts.Registry.Module(ModuleID(man.ID)); ok {
		return nil, fmt.Errorf("module %s is already registered: %w", ModuleID(man.ID), core.ErrExists)
	}
	grants, err := grantsOf(in.Grants)
	if err != nil {
		return nil, err
	}
	if err := checkSecrets(man, in.Secrets); err != nil {
		return nil, err
	}
	settings, err := checkSettings(man, in.Settings, false)
	if err != nil {
		return nil, err
	}
	now := h.now()
	u, err := h.pluginUser(ctx, existing, man, now)
	if err != nil {
		return nil, err
	}
	token := core.PluginTokenPrefix + core.NewToken(25)
	secret := newSecret()
	row := &index.Plugin{
		ID: man.ID, Name: man.Name, Version: man.Version, BaseURL: base, Status: core.PluginStatusActive,
		Manifest: raw, ApprovedManifest: raw, UserID: u.ID, TokenHash: auth.HashToken(token), TokenPrefix: token[:12],
		SigningSecret: secret, Permissions: slices.Clone(man.Permissions), Settings: settings,
		CreatedAt: now, UpdatedAt: now,
	}
	if existing != nil {
		err = h.store.UpdatePlugin(ctx, row)
	} else {
		err = h.store.CreatePlugin(ctx, row)
	}
	if err != nil {
		return nil, fmt.Errorf("store plugin: %w", err)
	}
	if err := h.store.SetPluginGrants(ctx, man.ID, grants); err != nil {
		h.abandon(ctx, man.ID, u)
		return nil, err
	}
	for name, v := range in.Secrets {
		if v == "" {
			continue
		}
		if err := h.store.SetPluginSecret(ctx, man.ID, name, v, now); err != nil {
			h.abandon(ctx, man.ID, u)
			return nil, err
		}
	}
	stored, err := h.store.GetPlugin(ctx, man.ID)
	if err != nil {
		return nil, err
	}
	if err := h.activate(ctx, stored); err != nil {
		h.abandon(ctx, man.ID, u)
		return nil, err
	}
	if st := h.state(man.ID); st == nil || !st.registered {
		h.abandon(ctx, man.ID, u)
		return nil, fmt.Errorf("plugin %s could not be added to the registry: %w", man.ID, core.ErrConflict)
	}

	payload := sdk.Install{
		PluginID: man.ID, MarkspaceURL: h.opts.MarkspaceURL, Token: token, SigningSecret: secret,
		Permissions: slices.Clone(man.Permissions),
	}
	for _, g := range grants {
		payload.Grants = append(payload.Grants, sdk.Grant{SpaceID: g.SpaceID, Role: g.Role})
	}
	// A plugin installed before holds the secret of that install, so the call
	// is signed with it and the payload carries the new one.
	signer := stored
	if existing != nil && existing.SigningSecret != "" {
		prev := *stored
		prev.SigningSecret = existing.SigningSecret
		signer = &prev
	}
	res, callErr := h.call(ctx, signer, http.MethodPost, "/install", "", payload, h.opts.CallTimeout)
	delivered := callErr == nil && res.Status == http.StatusOK
	out := &plugins.InstallResult{Delivered: delivered}
	if !delivered {
		msg := "the plugin did not answer"
		switch {
		case callErr != nil:
			msg = callErr.Error()
		default:
			msg = "the plugin answered " + res.errorText()
		}
		out.InstallError = msg
		out.Token, out.SigningSecret, out.MarkspaceURL = token, secret, h.opts.MarkspaceURL
	}
	v, err := h.Get(ctx, man.ID)
	if err != nil {
		return nil, err
	}
	out.Plugin = *v
	h.log.Info("plugin installed", "plugin", man.ID, "version", man.Version, "delivered", delivered)
	return out, nil
}

// pluginEmail is the placeholder email of a plugin user. The invalid
// top-level domain never receives mail.
func pluginEmail(id string) string { return "plugin-" + id + "@plugins.invalid" }

// pluginUser returns the user of a plugin being installed: the user of an
// earlier install, made active again, or a new plugin-kind user.
func (h *Host) pluginUser(ctx context.Context, existing *index.Plugin, man sdk.Manifest, now time.Time) (*core.User, error) {
	if existing != nil && existing.UserID != "" {
		if u, err := h.store.GetUser(ctx, existing.UserID); err == nil && u != nil {
			u.IsActive, u.IsAdmin, u.Name, u.UpdatedAt = true, false, man.Name, now
			if err := h.store.UpdateUser(ctx, u); err != nil {
				return nil, fmt.Errorf("reactivate plugin user: %w", err)
			}
			return u, nil
		}
	}
	email := pluginEmail(man.ID)
	if other, err := h.store.GetUserByEmail(ctx, email); err == nil && other != nil {
		email = "plugin-" + man.ID + "-" + core.NewToken(5) + "@plugins.invalid"
	}
	u := &core.User{
		ID: core.NewID(), Email: email, Name: man.Name, IsActive: true, AvatarColor: "#475569",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := h.store.CreateUser(ctx, u); err != nil {
		return nil, fmt.Errorf("create plugin user: %w", err)
	}
	if err := h.store.SetUserKind(ctx, u.ID, core.UserKindPlugin); err != nil {
		return nil, fmt.Errorf("set plugin user kind: %w", err)
	}
	return u, nil
}

// abandon rolls back a failed install: the plugin is uninstalled and its user
// deactivated.
func (h *Host) abandon(ctx context.Context, id string, u *core.User) {
	h.deactivate(id)
	ctx = context.WithoutCancel(ctx)
	if err := h.store.UninstallPlugin(ctx, id, index.UninstallOptions{DeleteGrants: true, DeleteDeliveries: true, DeleteSecrets: true}, h.now()); err != nil {
		h.log.Warn("roll back plugin install failed", "plugin", id, "err", err)
	}
	if u != nil {
		u.IsActive = false
		if err := h.store.UpdateUser(ctx, u); err != nil {
			h.log.Warn("deactivate plugin user failed", "plugin", id, "err", err)
		}
	}
}

// Configure implements plugins.Host. A settings change reaches the plugin as
// a plugin.settings.updated event, whatever its event patterns.
func (h *Host) Configure(ctx context.Context, id string, in plugins.ConfigInput) (*plugins.PluginView, error) {
	row, err := h.installed(ctx, id)
	if err != nil {
		return nil, err
	}
	man, err := sdk.ParseManifest(row.ApprovedManifest)
	if err != nil {
		return nil, core.Invalid("manifest", err.Error())
	}
	if in.Grants != nil {
		grants, err := grantsOf(*in.Grants)
		if err != nil {
			return nil, err
		}
		if err := h.store.SetPluginGrants(ctx, id, grants); err != nil {
			return nil, err
		}
	}
	if in.Secrets != nil {
		if err := checkSecrets(man, in.Secrets); err != nil {
			return nil, err
		}
		for name, v := range in.Secrets {
			if v == "" {
				if err := h.store.DeletePluginSecret(ctx, id, name); err != nil && !errors.Is(err, core.ErrNotFound) {
					return nil, err
				}
				continue
			}
			if err := h.store.SetPluginSecret(ctx, id, name, v, h.now()); err != nil {
				return nil, err
			}
		}
	}
	var settings json.RawMessage
	if in.Settings != nil {
		if settings, err = checkSettings(man, in.Settings, false); err != nil {
			return nil, err
		}
		row.Settings = settings
		if err := h.store.UpdatePlugin(ctx, row); err != nil {
			return nil, err
		}
	}
	if err := h.reload(ctx, id); err != nil {
		return nil, err
	}
	if settings != nil {
		data, _ := json.Marshal(map[string]any{"pluginId": id, "settings": settings})
		ev := module.Event{ID: core.NewID(), Type: plugins.SettingsUpdatedEvent, Data: data, At: h.now()}
		if err := h.enqueue(ctx, id, ev); err != nil {
			h.log.Warn("queue settings event failed", "plugin", id, "err", err)
		}
	}
	return h.Get(ctx, id)
}

// SetPaused implements plugins.Host. A paused plugin keeps its queued
// deliveries, its tokens stop working and its contributions leave the
// registry.
func (h *Host) SetPaused(ctx context.Context, id string, paused bool) (*plugins.PluginView, error) {
	row, err := h.installed(ctx, id)
	if err != nil {
		return nil, err
	}
	switch {
	case paused && row.Status != core.PluginStatusPaused:
		if err := h.store.SetPluginStatus(ctx, id, core.PluginStatusPaused, h.now()); err != nil {
			return nil, err
		}
		if st := h.state(id); st != nil {
			h.unregister(st)
		}
	case !paused && row.Status == core.PluginStatusPaused:
		status := core.PluginStatusActive
		if !bytes.Equal(row.Manifest, row.ApprovedManifest) {
			status = core.PluginStatusNeedsApproval
		}
		if err := h.store.SetPluginStatus(ctx, id, status, h.now()); err != nil {
			return nil, err
		}
	}
	if err := h.reload(ctx, id); err != nil {
		return nil, err
	}
	if st := h.state(id); st != nil && !paused {
		if err := h.register(st); err != nil {
			return nil, err
		}
		h.wake(id)
	}
	return h.Get(ctx, id)
}

// CheckManifest implements plugins.Host.
func (h *Host) CheckManifest(ctx context.Context, id string) (*plugins.PluginView, error) {
	row, err := h.installed(ctx, id)
	if err != nil {
		return nil, err
	}
	raw, man, err := h.fetchManifest(ctx, row.BaseURL)
	if err != nil {
		return nil, err
	}
	if man.ID != id {
		return nil, core.Invalid("manifest", fmt.Sprintf("the manifest now names plugin %q", man.ID))
	}
	if bytes.Equal(raw, row.ApprovedManifest) {
		if !bytes.Equal(row.Manifest, row.ApprovedManifest) {
			row.Manifest = raw
			if row.Status == core.PluginStatusNeedsApproval {
				row.Status = core.PluginStatusActive
			}
			if err := h.store.UpdatePlugin(ctx, row); err != nil {
				return nil, err
			}
			if err := h.reload(ctx, id); err != nil {
				return nil, err
			}
		}
		return h.Get(ctx, id)
	}
	approved, err := sdk.ParseManifest(row.ApprovedManifest)
	if err == nil && asksForMore(approved, man) {
		row.Manifest = raw
		if row.Status != core.PluginStatusPaused {
			row.Status = core.PluginStatusNeedsApproval
		}
		if err := h.store.UpdatePlugin(ctx, row); err != nil {
			return nil, err
		}
		if err := h.reload(ctx, id); err != nil {
			return nil, err
		}
		h.log.Info("plugin manifest needs approval", "plugin", id, "version", man.Version)
		return h.Get(ctx, id)
	}
	return h.applyManifest(ctx, row, raw, man)
}

// Approve implements plugins.Host.
func (h *Host) Approve(ctx context.Context, id string) (*plugins.PluginView, error) {
	row, err := h.installed(ctx, id)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(row.Manifest, row.ApprovedManifest) {
		return h.Get(ctx, id)
	}
	man, err := sdk.ParseManifest(row.Manifest)
	if err != nil {
		return nil, core.Invalid("manifest", err.Error())
	}
	if err := h.validateModule(man); err != nil {
		return nil, err
	}
	return h.applyManifest(ctx, row, row.Manifest, man)
}

// applyManifest makes a manifest the approved one and registers the plugin's
// module again.
func (h *Host) applyManifest(ctx context.Context, row *index.Plugin, raw []byte, man sdk.Manifest) (*plugins.PluginView, error) {
	row.Manifest, row.ApprovedManifest = raw, raw
	row.Name, row.Version, row.Permissions = man.Name, man.Version, slices.Clone(man.Permissions)
	if row.Status == core.PluginStatusNeedsApproval {
		row.Status = core.PluginStatusActive
	}
	if err := h.store.UpdatePlugin(ctx, row); err != nil {
		return nil, err
	}
	st := h.state(row.ID)
	if st != nil {
		h.unregister(st)
	}
	if err := h.reload(ctx, row.ID); err != nil {
		return nil, err
	}
	if st != nil && core.PluginStatusRuns(row.Status) {
		if err := h.register(st); err != nil {
			return nil, err
		}
	}
	h.log.Info("plugin manifest applied", "plugin", row.ID, "version", man.Version)
	return h.Get(ctx, row.ID)
}

// blobDir is the blob directory of a plugin.
func (h *Host) blobDir(id string) string {
	return filepath.Join(h.opts.DataDir, "internal", "plugins", id, "blobs")
}

// Uninstall implements plugins.Host.
func (h *Host) Uninstall(ctx context.Context, id string, deleteData bool) error {
	row, err := h.installed(ctx, id)
	if err != nil {
		return err
	}
	man, _ := sdk.ParseManifest(row.ApprovedManifest)
	grants, err := h.store.ListPluginGrants(ctx, id)
	if err != nil {
		return err
	}
	h.deactivate(id)
	if _, err := h.opts.Engine.CancelModule(ctx, ModuleID(id)); err != nil {
		h.log.Warn("cancel plugin jobs failed", "plugin", id, "err", err)
	}
	var kinds []string
	for _, j := range man.Jobs {
		kinds = append(kinds, JobKind(id, j.Kind))
	}
	if err := h.store.DeleteSchedules(ctx, kinds); err != nil {
		return err
	}
	opts := index.UninstallOptions{DeleteKV: deleteData, DeleteBlobs: deleteData, DeleteGrants: true, DeleteDeliveries: true, DeleteSecrets: deleteData}
	if err := h.store.UninstallPlugin(ctx, id, opts, h.now()); err != nil {
		return err
	}
	if deleteData {
		if err := os.RemoveAll(filepath.Dir(h.blobDir(id))); err != nil {
			h.log.Warn("remove plugin blobs failed", "plugin", id, "err", err)
		}
		if _, err := h.store.DeletePluginUsage(ctx, id); err != nil {
			h.log.Warn("delete plugin usage failed", "plugin", id, "err", err)
		}
		h.deleteItems(ctx, row.UserID, grants)
	}
	if u, err := h.store.GetUser(ctx, row.UserID); err == nil && u != nil {
		u.IsActive, u.UpdatedAt = false, h.now()
		if err := h.store.UpdateUser(ctx, u); err != nil {
			h.log.Warn("deactivate plugin user failed", "plugin", id, "err", err)
		}
	}
	h.mu.Lock()
	delete(h.buckets, id)
	h.mu.Unlock()
	h.log.Info("plugin uninstalled", "plugin", id, "deleteData", deleteData)
	return nil
}

// systemActor administers every space. Uninstall uses it to delete a
// plugin's items.
func systemActor() *core.Actor {
	return &core.Actor{User: &core.User{ID: "plugin-host", Name: "Plugin host", IsAdmin: true, IsActive: true}}
}

// deleteItems deletes the notes and files a plugin user created in the
// plugin's spaces, then the folders it created that are left empty.
func (h *Host) deleteItems(ctx context.Context, userID string, grants []index.PluginGrant) {
	if userID == "" {
		return
	}
	sys := systemActor()
	for _, g := range grants {
		tree, err := h.opts.Backend.SpaceTree(ctx, sys, g.SpaceID)
		if err != nil {
			h.log.Warn("list plugin items failed", "space", g.SpaceID, "err", err)
			continue
		}
		type folderRef struct {
			id    string
			depth int
		}
		var leaves []string
		var folders []folderRef
		var walk func(items []*core.TreeItem, depth int)
		walk = func(items []*core.TreeItem, depth int) {
			for _, it := range items {
				switch {
				case it.Note != nil && it.Note.CreatedBy == userID:
					leaves = append(leaves, it.Note.ID)
				case it.File != nil && it.File.CreatedBy == userID:
					leaves = append(leaves, it.File.ID)
				case it.Folder != nil:
					if it.Folder.IndexNoteID != "" {
						if n, _, err := h.opts.Backend.GetNote(ctx, sys, it.Folder.IndexNoteID); err == nil && n.CreatedBy == userID {
							leaves = append(leaves, n.ID)
						}
					}
					if it.Folder.CreatedBy == userID {
						folders = append(folders, folderRef{it.Folder.ID, depth})
					}
				}
				walk(it.Children, depth+1)
			}
		}
		walk(tree, 0)
		for _, id := range leaves {
			if err := h.opts.Backend.DeleteItem(ctx, sys, id); err != nil && !errors.Is(err, core.ErrNotFound) {
				h.log.Warn("delete plugin item failed", "item", id, "err", err)
			}
		}
		sort.Slice(folders, func(i, j int) bool { return folders[i].depth > folders[j].depth })
		for _, f := range folders {
			item, _, err := h.opts.Backend.GetFolder(ctx, sys, f.id)
			if err != nil || len(item.Children) > 0 || (item.Folder != nil && item.Folder.IndexNoteID != "") {
				continue
			}
			if err := h.opts.Backend.DeleteItem(ctx, sys, f.id); err != nil && !errors.Is(err, core.ErrNotFound) {
				h.log.Warn("delete plugin folder failed", "folder", f.id, "err", err)
			}
		}
	}
}
