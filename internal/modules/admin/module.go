// Package admin is the built-in module for instance settings, statistics,
// the full reindex and the system API tokens.
package admin

import (
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/module"
)

// Module serves the admin routes.
type Module struct{ api.Env }

// New returns the module.
func New(env api.Env) *Module { return &Module{Env: env.WithDefaults()} }

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "admin", Name: "Administration", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("GET /api/settings", m.getSettings)
	r.API("PATCH /api/settings", m.updateSettings)
	r.API("GET /api/stats", m.stats)
	r.API("POST /api/reindex", m.reindex)
	r.API("GET /api/admin/tokens", m.listSystemTokens)
	r.API("DELETE /api/admin/tokens/{id}", m.revokeSystemToken)
	return nil
}
