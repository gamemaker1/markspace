// Package comments is the built-in module for comment threads on notes,
// folders and files.
package comments

import (
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/module"
)

// Module serves the comment routes.
type Module struct{ api.Env }

// New returns the module.
func New(env api.Env) *Module { return &Module{Env: env.WithDefaults()} }

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "comments", Name: "Comments", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("GET /api/notes/{id}/comments", m.listComments)
	r.API("POST /api/notes/{id}/comments", m.createComment)
	r.API("GET /api/items/{id}/comments", m.listComments)
	r.API("POST /api/items/{id}/comments", m.createComment)
	r.API("PATCH /api/comments/{id}", m.updateComment)
	r.API("DELETE /api/comments/{id}", m.deleteComment)
	r.API("POST /api/comments/{id}/resolve", m.resolveComment)
	return nil
}
