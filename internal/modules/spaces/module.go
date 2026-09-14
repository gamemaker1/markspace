// Package spaces is the built-in module for spaces, their members, and space
// export and import.
package spaces

import (
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/module"
)

// Module serves the space routes.
type Module struct{ api.Env }

// New returns the module.
func New(env api.Env) *Module { return &Module{Env: env.WithDefaults()} }

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "spaces", Name: "Spaces", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("GET /api/spaces", m.listSpaces)
	r.API("POST /api/spaces", m.createSpace)
	r.API("GET /api/spaces/{id}", m.getSpace)
	r.API("PATCH /api/spaces/{id}", m.updateSpace)
	r.API("DELETE /api/spaces/{id}", m.deleteSpace)
	r.API("GET /api/spaces/{id}/members", m.listMembers)
	r.API("POST /api/spaces/{id}/members", m.addMember)
	r.API("PATCH /api/spaces/{id}/members/{userId}", m.updateMember)
	r.API("DELETE /api/spaces/{id}/members/{userId}", m.removeMember)
	r.API("GET /api/spaces/{id}/export", m.exportSpace)
	r.API("POST /api/spaces/{id}/import", m.importSpace)
	return nil
}
