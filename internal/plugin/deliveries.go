package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
)

// coreEvent converts a bus event for the access filter.
func coreEvent(e module.Event) core.Event {
	ev := core.Event{
		ID: e.ID, Type: e.Type, SpaceID: e.SpaceID, ItemID: e.ItemID, Kind: e.Kind,
		ActorID: e.ActorID, Path: e.Path, OldPath: e.OldPath, Data: e.Data, At: e.At.UnixMilli(),
	}
	if e.Kind == core.KindNote {
		ev.NoteID = e.ItemID
	}
	if e.Type == "plugin.notify" && len(e.Data) > 0 {
		var d struct {
			UserIDs []string `json:"userIds"`
		}
		if json.Unmarshal(e.Data, &d) == nil && len(d.UserIDs) > 0 {
			ev.Audience = d.UserIDs
		}
	}
	return ev
}

// onEvent is the bus handler of one plugin. It keeps events whose type
// matches the approved manifest's patterns and that the plugin may see, and
// queues them for delivery.
func (h *Host) onEvent(id string) module.EventHandler {
	return func(ctx context.Context, e module.Event) error {
		row, man, _, ok := h.snapshot(id)
		if !ok || row.Status == core.PluginStatusUninstalled || !slices.Contains(man.Permissions, "events") {
			return nil
		}
		if e.Type == plugins.SettingsUpdatedEvent {
			return nil
		}
		matched := false
		for _, p := range man.Events {
			if module.MatchEventType(p, e.Type) {
				matched = true
				break
			}
		}
		if !matched || !h.visible(ctx, id, e) {
			return nil
		}
		return h.enqueue(ctx, id, e)
	}
}

// visible applies the plugin's access filter to an event.
func (h *Host) visible(ctx context.Context, id string, e module.Event) bool {
	if h.opts.Filters == nil {
		_, _, ident, ok := h.snapshot(id)
		return ok && e.SpaceID != "" && ident.SpaceAccess(e.SpaceID) >= core.AccessView
	}
	h.mu.Lock()
	st := h.states[id]
	var f core.EventFilter
	if st != nil {
		f = st.filter
	}
	h.mu.Unlock()
	if st == nil {
		return false
	}
	if f == nil {
		actor, err := h.pluginActor(ctx, id)
		if err != nil {
			return false
		}
		if f, err = h.opts.Filters.NewEventFilter(ctx, actor); err != nil {
			h.log.Debug("plugin event filter unavailable", "plugin", id, "err", err)
			return false
		}
		h.mu.Lock()
		if st := h.states[id]; st != nil {
			st.filter = f
		}
		h.mu.Unlock()
	}
	return f.Allows(ctx, coreEvent(e))
}

// enqueue stores one event delivery and wakes the sender.
func (h *Host) enqueue(ctx context.Context, id string, e module.Event) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := h.store.EnqueueDeliveries(ctx, []index.PluginDelivery{{PluginID: id, Path: "/events", Body: body}}, h.now()); err != nil {
		return fmt.Errorf("enqueue delivery: %w", err)
	}
	h.wake(id)
	return nil
}

// wake nudges the sender of a plugin.
func (h *Host) wake(id string) {
	h.mu.Lock()
	st := h.states[id]
	h.mu.Unlock()
	if st == nil {
		return
	}
	select {
	case st.wake <- struct{}{}:
	default:
	}
}

// startSenderLocked starts the delivery loop of a plugin once the host runs.
func (h *Host) startSenderLocked(st *pluginState) {
	if h.runCtx == nil || st.stopSender != nil {
		return
	}
	ctx, cancel := context.WithCancel(h.runCtx)
	st.stopSender = cancel
	id, wake := st.row.ID, st.wake
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.sendLoop(ctx, id, wake)
	}()
}

// sendLoop delivers queued events until ctx ends. It sleeps until the next
// delivery is due, or until it is woken.
func (h *Host) sendLoop(ctx context.Context, id string, wake <-chan struct{}) {
	for {
		sent, next, err := h.DeliverOnce(ctx, id)
		if ctx.Err() != nil {
			return
		}
		if err == nil && sent > 0 {
			continue
		}
		wait := 30 * time.Second
		if err != nil {
			h.log.Debug("plugin delivery failed", "plugin", id, "err", err)
		}
		if !next.IsZero() {
			wait = min(max(next.Sub(h.now()), 50*time.Millisecond), wait)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-wake:
			t.Stop()
		case <-t.C:
		}
	}
}

// DeliverOnce claims up to 100 due deliveries of a plugin and posts them as
// one /events batch. It does nothing while the plugin is down, paused or
// uninstalled. It returns the number delivered and, when nothing was due, the
// time the next delivery is due.
func (h *Host) DeliverOnce(ctx context.Context, id string) (int, time.Time, error) {
	row, _, _, ok := h.snapshot(id)
	if !ok || (row.Status != core.PluginStatusActive && row.Status != core.PluginStatusNeedsApproval) {
		return 0, time.Time{}, nil
	}
	now := h.now()
	ds, err := h.store.ClaimDeliveries(ctx, id, now, index.MaxDeliveryBatch)
	if err != nil {
		return 0, time.Time{}, err
	}
	if len(ds) == 0 {
		next, err := h.store.NextDeliveryAt(ctx, id, now)
		return 0, next, err
	}
	events := make([]json.RawMessage, 0, len(ds))
	ids := make([]string, 0, len(ds))
	for _, d := range ds {
		ids = append(ids, d.ID)
		if json.Valid(d.Body) {
			events = append(events, json.RawMessage(d.Body))
		}
	}
	res, err := h.call(ctx, row, http.MethodPost, "/events", ds[0].ID, map[string]any{"events": events}, h.opts.CallTimeout)
	if err == nil && res.Status >= 200 && res.Status < 300 {
		if err := h.store.CompleteDeliveries(context.WithoutCancel(ctx), ids); err != nil {
			return 0, time.Time{}, err
		}
		return len(ids), time.Time{}, nil
	}
	if err == nil {
		err = fmt.Errorf("plugin answered %s", res.errorText())
	}
	if ferr := h.store.FailDeliveries(context.WithoutCancel(ctx), ids, res.Status, h.now()); ferr != nil {
		return 0, time.Time{}, ferr
	}
	return 0, time.Time{}, err
}

// ExpireDeliveries deletes deliveries older than 24 hours.
func (h *Host) ExpireDeliveries(ctx context.Context) int {
	n, err := h.store.ExpireDeliveries(ctx, h.now())
	if err != nil {
		h.log.Warn("expire plugin deliveries failed", "err", err)
		return 0
	}
	if n > 0 {
		h.log.Info("expired plugin deliveries", "count", n)
	}
	return n
}

// Purge deletes old idempotency keys and expired acting tokens.
func (h *Host) Purge(ctx context.Context) {
	if _, err := h.store.PurgeIdempotency(ctx, h.now()); err != nil {
		h.log.Warn("purge idempotency keys failed", "err", err)
	}
	if _, err := h.store.PurgeActingTokens(ctx, h.now()); err != nil {
		h.log.Warn("purge acting tokens failed", "err", err)
	}
}

// CheckHealth calls GET /health on every plugin that runs or is down. Three
// failures in a row mark a plugin down, and a success marks it active.
func (h *Host) CheckHealth(ctx context.Context) {
	h.mu.Lock()
	var ids []string
	for id, st := range h.states {
		if core.PluginStatusRuns(st.row.Status) {
			ids = append(ids, id)
		}
	}
	h.mu.Unlock()
	slices.Sort(ids)
	for _, id := range ids {
		h.checkOne(ctx, id)
	}
}

// checkOne checks the health of one plugin and records the outcome.
func (h *Host) checkOne(ctx context.Context, id string) {
	row, _, _, ok := h.snapshot(id)
	if !ok {
		return
	}
	res, err := h.call(ctx, row, http.MethodGet, "/health", "", nil, h.opts.CallTimeout)
	healthy := err == nil && res.Status == http.StatusOK
	errText := ""
	switch {
	case err != nil:
		errText = err.Error()
	case !healthy:
		errText = "health check answered " + res.errorText()
	}
	updated, rerr := h.store.RecordPluginHealth(ctx, id, healthy, errText, h.now())
	if rerr != nil {
		h.log.Warn("record plugin health failed", "plugin", id, "err", rerr)
		return
	}
	h.mu.Lock()
	st := h.states[id]
	var was string
	if st != nil {
		was = st.row.Status
		st.row = updated
		if st.identity != nil {
			ident := *st.identity
			ident.Status = updated.Status
			st.identity = &ident
		}
	}
	h.mu.Unlock()
	if was != updated.Status {
		h.log.Info("plugin health changed", "plugin", id, "from", was, "to", updated.Status, "err", errText)
	}
	if updated.Status == core.PluginStatusActive && was == core.PluginStatusDown {
		h.wake(id)
	}
}
