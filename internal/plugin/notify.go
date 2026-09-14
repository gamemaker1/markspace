package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
)

// Notification and event limits.
const (
	maxNotifyMessage = 1000
	maxNotifyUsers   = 100
	maxEventData     = 64 << 10
)

// NotifyEvent is the type of a plugin notification.
const NotifyEvent = "plugin.notify"

// validLink accepts an application path or an http or https URL.
func validLink(s string) bool {
	if s == "" {
		return true
	}
	if strings.HasPrefix(s, "/") {
		return !strings.HasPrefix(s, "//") && !strings.HasPrefix(s, "/\\") && len(s) <= 2048
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

// spaceVisible requires the actor to view a space. With an acting token both
// the person and the plugin must view it.
func (h *Host) spaceVisible(ctx context.Context, actor *core.Actor, spaceID string) error {
	g, err := h.opts.Backend.SpaceAccess(ctx, actor, spaceID)
	if err != nil {
		return err
	}
	if g == nil || g.Access < core.AccessView {
		return fmt.Errorf("space %s: %w", spaceID, core.ErrNotFound)
	}
	return nil
}

// Notify implements plugins.Host. It publishes a plugin.notify event that the
// client shows in the status line. A plugin may notify the members of its
// granted spaces, and an acting token its person.
func (h *Host) Notify(ctx context.Context, actor *core.Actor, in plugins.Notification) error {
	pid, err := pluginOf(actor)
	if err != nil {
		return err
	}
	msg := strings.TrimSpace(in.Message)
	switch {
	case msg == "" || len(msg) > maxNotifyMessage:
		return core.Invalid("message", fmt.Sprintf("must be 1 to %d bytes", maxNotifyMessage))
	case !validLink(in.Link):
		return core.Invalid("link", "must be an application path or an http or https URL")
	case len(in.UserIDs) > maxNotifyUsers:
		return core.Invalid("userIds", fmt.Sprintf("at most %d users", maxNotifyUsers))
	}
	if in.SpaceID != "" {
		if err := h.spaceVisible(ctx, actor, in.SpaceID); err != nil {
			return err
		}
	}
	var users []string
	for _, uid := range in.UserIDs {
		if slices.Contains(users, uid) {
			continue
		}
		if err := h.reachable(ctx, actor, uid); err != nil {
			return err
		}
		users = append(users, uid)
	}
	if in.SpaceID == "" && len(users) == 0 {
		if actor.Via == "" {
			return core.Invalid("userIds", "name users or a space")
		}
		users = []string{actor.UserID()}
	}
	name := pid
	if row, _, _, ok := h.snapshot(pid); ok {
		name = row.Name
	}
	data, _ := json.Marshal(map[string]any{
		"pluginId": pid, "pluginName": name, "message": msg, "link": in.Link, "userIds": users,
	})
	h.opts.Bus.Publish(module.Event{
		ID: core.NewID(), Type: NotifyEvent, SpaceID: in.SpaceID, ActorID: actor.UserID(), Data: data, At: h.now(),
	})
	return nil
}

// reachable reports whether a plugin may notify a user: the person of its
// acting token, or an active member of a space the plugin is granted.
func (h *Host) reachable(ctx context.Context, actor *core.Actor, userID string) error {
	notFound := fmt.Errorf("user %s: %w", userID, core.ErrNotFound)
	if actor.Via != "" && userID == actor.UserID() {
		return nil
	}
	u, err := h.store.GetUser(ctx, userID)
	if err != nil || u == nil || !u.IsActive {
		return notFound
	}
	members, err := h.store.ListMembershipsForUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, m := range members {
		if actor.Plugin.SpaceAccess(m.SpaceID) >= core.AccessView {
			return nil
		}
	}
	return notFound
}

// Emit implements plugins.Host. The type must be plugin.<id>.<name> of the
// actor's plugin.
func (h *Host) Emit(ctx context.Context, actor *core.Actor, typ, spaceID string, data json.RawMessage) error {
	pid, err := pluginOf(actor)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(typ, "plugin."+pid+".") || !module.ValidEventType(typ) || typ == plugins.SettingsUpdatedEvent {
		return core.Invalid("type", "must be plugin.<id>.<name> for this plugin")
	}
	if len(data) > maxEventData {
		return fmt.Errorf("event data is larger than 64 KiB: %w", core.ErrTooLarge)
	}
	if len(data) > 0 && !json.Valid(data) {
		return core.Invalid("data", "must be JSON")
	}
	if spaceID != "" {
		if err := h.spaceVisible(ctx, actor, spaceID); err != nil {
			return err
		}
	}
	h.opts.Bus.Publish(module.Event{
		ID: core.NewID(), Type: typ, SpaceID: spaceID, ActorID: actor.UserID(), Data: data, At: h.now(),
	})
	return nil
}
