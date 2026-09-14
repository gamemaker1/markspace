package sync

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// syncPull serves GET /api/sync, the change feed offline clients replicate.
// since is the cursor from the previous page, or 0 for a full snapshot.
func (m *Module) syncPull(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var since int64
	if raw := q.Get("since"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			m.Fail(w, r, core.Invalid("since", "since must be a non-negative integer"))
			return
		}
		since = v
	}
	batch, err := m.Backend.SyncPull(r.Context(), api.ActorFrom(r.Context()), since, api.IntParam(q.Get("limit"), 200, 1000))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	api.WriteJSON(w, http.StatusOK, batch)
}

// events serves GET /api/events as a text/event-stream of core.Event values.
func (m *Module) events(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.StreamContext(r)
	defer cancel()
	actor := api.ActorFrom(ctx)
	ch, unsubscribe, err := m.Backend.Subscribe(ctx, actor)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	defer unsubscribe()

	stream := api.NewSSE(w)
	ticker := time.NewTicker(api.SSEKeepalive)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := stream.Send(ev.Type, ev); err != nil {
				m.Logger.Debug("event stream write failed", "err", err)
				return
			}
		case <-ticker.C:
			if err := stream.Ping(); err != nil {
				return
			}
		}
	}
}
