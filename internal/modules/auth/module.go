// Package auth is the built-in module for sign-in: signup, login, logout,
// the current account, password and profile changes, sessions and API
// tokens.
package auth

import (
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/module"
)

// Module serves the auth routes.
type Module struct{ api.Env }

// New returns the module.
func New(env api.Env) *Module { return &Module{Env: env.WithDefaults()} }

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "auth", Name: "Accounts", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("POST /api/auth/signup", m.signup)
	r.API("POST /api/auth/login", m.login)
	r.API("POST /api/auth/logout", m.logout)
	r.API("GET /api/auth/me", m.me)
	r.API("POST /api/auth/password", m.changePassword)
	r.API("PATCH /api/auth/profile", m.updateProfile)
	r.API("GET /api/auth/sessions", m.listSessions)
	r.API("DELETE /api/auth/sessions/{id}", m.revokeSession)
	r.API("GET /api/tokens", m.listTokens)
	r.API("POST /api/tokens", m.createToken)
	r.API("DELETE /api/tokens/{id}", m.revokeToken)
	return nil
}
