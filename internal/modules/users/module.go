// Package users is the built-in module for accounts and invites.
package users

import (
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/module"
)

// Module serves the user and invite routes.
type Module struct{ api.Env }

// New returns the module.
func New(env api.Env) *Module { return &Module{Env: env.WithDefaults()} }

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "users", Name: "Users", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("GET /api/users", m.listUsers)
	r.API("POST /api/users", m.createUser)
	r.API("GET /api/users/lookup", m.lookupUsers)
	r.API("GET /api/users/{id}", m.getUser)
	r.API("PATCH /api/users/{id}", m.updateUser)
	r.API("DELETE /api/users/{id}", m.deleteUser)
	r.API("GET /api/invites", m.listInvites)
	r.API("POST /api/invites", m.createInvite)
	r.API("DELETE /api/invites/{id}", m.revokeInvite)
	return nil
}
