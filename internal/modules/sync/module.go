// Package sync is the built-in module for the sync feed that offline clients
// replicate and for the event stream.
package sync

import (
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/module"
)

// Module serves the sync feed and the event stream.
type Module struct{ api.Env }

// New returns the module.
func New(env api.Env) *Module { return &Module{Env: env.WithDefaults()} }

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "sync", Name: "Sync", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("GET /api/sync", m.syncPull)
	r.API("GET /api/events", m.events)
	return nil
}
