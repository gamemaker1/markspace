// Package sharing is the built-in module for shares with users and public
// links.
package sharing

import (
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/module"
)

// Module serves the share and public link routes.
type Module struct{ api.Env }

// New returns the module.
func New(env api.Env) *Module { return &Module{Env: env.WithDefaults()} }

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "sharing", Name: "Sharing", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("GET /api/notes/{id}/shares", m.listShares)
	r.API("POST /api/notes/{id}/shares", m.createShare)
	r.API("GET /api/items/{id}/shares", m.listShares)
	r.API("POST /api/items/{id}/shares", m.createShare)
	r.API("PATCH /api/shares/{id}", m.updateShare)
	r.API("DELETE /api/shares/{id}", m.deleteShare)
	r.API("GET /api/shares/mine", m.listSharedWithMe)
	r.API("GET /api/notes/{id}/links", m.listLinks)
	r.API("POST /api/notes/{id}/links", m.createLink)
	r.API("GET /api/items/{id}/links", m.listLinks)
	r.API("POST /api/items/{id}/links", m.createLink)
	r.API("PATCH /api/links/{id}", m.updateLink)
	r.API("DELETE /api/links/{id}", m.deleteLink)
	return nil
}
