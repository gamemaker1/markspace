package system

import (
	"net/http"
	"sort"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/module"
)

// Status is the body of GET /api/admin/status.
type Status struct {
	Version       string    `json:"version"`
	StartedAt     time.Time `json:"startedAt"`
	UptimeSeconds int64     `json:"uptimeSeconds"`
	Listen        string    `json:"listen"`
	DataDir       string    `json:"dataDir"`
	Users         int       `json:"users"`
	Spaces        int       `json:"spaces"`
	Notes         int       `json:"notes"`
	Files         int       `json:"files"`
	// Jobs maps every job status to its job count.
	Jobs    map[string]int `json:"jobs"`
	Plugins []PluginStatus `json:"plugins"`
}

// PluginStatus is the health of one installed plugin.
type PluginStatus struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Status is active, down, paused or needs_approval.
	Status         string    `json:"status"`
	HealthFailures int       `json:"healthFailures"`
	LastHealthAt   time.Time `json:"lastHealthAt,omitzero"`
	LastError      string    `json:"lastError,omitempty"`
}

// status serves GET /api/admin/status.
func (m *Module) status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := api.ActorFrom(ctx)
	if err := api.RequireAdmin(actor); err != nil {
		api.WriteError(w, r, m.log, err)
		return
	}
	stats, err := m.opts.Backend.Stats(ctx, actor)
	if err != nil {
		api.WriteError(w, r, m.log, err)
		return
	}
	now := m.opts.Now()
	out := Status{
		Version:       m.opts.Version,
		StartedAt:     m.opts.Started.UTC(),
		UptimeSeconds: int64(now.Sub(m.opts.Started) / time.Second),
		Listen:        m.opts.Listen,
		DataDir:       m.opts.DataDir,
		Users:         stats.Users,
		Spaces:        stats.Spaces,
		Notes:         stats.Notes,
		Files:         stats.Files,
		Jobs:          map[string]int{},
		Plugins:       []PluginStatus{},
	}
	for _, st := range module.JobStatuses {
		out.Jobs[string(st)] = 0
	}
	if m.opts.Jobs != nil {
		counts, err := m.opts.Jobs.CountByStatus(ctx)
		if err != nil {
			api.WriteError(w, r, m.log, err)
			return
		}
		for st, n := range counts {
			out.Jobs[string(st)] = n
		}
	}
	if m.opts.Plugins != nil {
		views, err := m.opts.Plugins.List(ctx)
		if err != nil {
			api.WriteError(w, r, m.log, err)
			return
		}
		for _, v := range views {
			out.Plugins = append(out.Plugins, PluginStatus{
				ID: v.ID, Name: v.Name, Version: v.Version, Status: v.Status,
				HealthFailures: v.HealthFailures, LastHealthAt: v.LastHealthAt, LastError: v.LastError,
			})
		}
		sort.Slice(out.Plugins, func(i, j int) bool { return out.Plugins[i].ID < out.Plugins[j].ID })
	}
	api.WriteJSON(w, http.StatusOK, out)
}
