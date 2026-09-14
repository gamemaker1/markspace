package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// loginRequest is the body of POST /api/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// passwordRequest is the body of POST /api/auth/password.
type passwordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// tokenRequest is the body of POST /api/tokens.
type tokenRequest struct {
	Name    string `json:"name"`
	TTLDays int    `json:"ttlDays,omitempty"`
}

// tokenResponse carries a freshly minted API token.
type tokenResponse struct {
	// Token is the plaintext credential.
	Token string `json:"token"`
	// Record is the stored token metadata.
	Record *core.APIToken `json:"record"`
}

// meResponse is the body of GET /api/auth/me.
type meResponse struct {
	User       *core.User     `json:"user"`
	NeedsSetup bool           `json:"needsSetup"`
	Settings   *core.Settings `json:"settings,omitempty"`
}

// signup serves POST /api/auth/signup. It creates the account and then logs
// the new user in, so the response already carries a session cookie.
func (m *Module) signup(w http.ResponseWriter, r *http.Request) {
	var in core.SignupInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	user, err := m.Backend.Signup(r.Context(), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	logged, token, err := m.Backend.Login(r.Context(), in.Email, in.Password,
		r.UserAgent(), m.Auth.ClientIP(r))
	if err != nil {
		// The account exists, so report it rather than failing the signup.
		m.Logger.Warn("signup could not start a session", "user", user.ID, "err", err)
		api.WriteJSON(w, http.StatusCreated, user)
		return
	}
	m.Auth.SetSessionCookie(w, token, m.SessionTTL)
	api.WriteJSON(w, http.StatusCreated, logged)
}

// login serves POST /api/auth/login. It sets the session cookie and returns
// the signed-in user.
func (m *Module) login(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	user, token, err := m.Backend.Login(r.Context(), in.Email, in.Password,
		r.UserAgent(), m.Auth.ClientIP(r))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	m.Auth.SetSessionCookie(w, token, m.SessionTTL)
	api.WriteJSON(w, http.StatusOK, user)
}

// logout serves POST /api/auth/logout. It deletes the session behind the
// cookie and clears the cookie.
func (m *Module) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(api.SessionCookie); err == nil && c.Value != "" {
		if err := m.Backend.Logout(r.Context(), c.Value); err != nil &&
			!errors.Is(err, core.ErrNotFound) && !errors.Is(err, core.ErrUnauthorized) {
			m.Fail(w, r, err)
			return
		}
	}
	m.Auth.ClearSessionCookie(w)
	api.WriteNoContent(w)
}

// me serves GET /api/auth/me. User is null for an anonymous actor, and
// settings are omitted when the actor may not read them.
func (m *Module) me(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	out := meResponse{}
	if actor.IsUser() {
		out.User = actor.User
	}
	needsSetup, err := m.Backend.NeedsSetup(r.Context())
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	out.NeedsSetup = needsSetup
	settings, err := m.Backend.GetSettings(r.Context(), actor)
	switch {
	case err == nil:
		out.Settings = settings
	case errors.Is(err, core.ErrForbidden), errors.Is(err, core.ErrUnauthorized):
		// Leave settings out for an actor that may not read them.
	default:
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// changePassword serves POST /api/auth/password.
func (m *Module) changePassword(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	var in passwordRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	if err := m.Backend.ChangePassword(r.Context(), actor, in.OldPassword, in.NewPassword); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// updateProfile serves PATCH /api/auth/profile.
func (m *Module) updateProfile(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	var in core.ProfileInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	user, err := m.Backend.UpdateProfile(r.Context(), actor, in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, user)
}

// listSessions serves GET /api/auth/sessions.
func (m *Module) listSessions(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	sessions, err := m.Backend.ListSessions(r.Context(), actor)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, sessions)
}

// revokeSession serves DELETE /api/auth/sessions/{id}.
func (m *Module) revokeSession(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	if err := m.Backend.RevokeSession(r.Context(), actor, r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// listTokens serves GET /api/tokens.
func (m *Module) listTokens(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	tokens, err := m.Backend.ListAPITokens(r.Context(), actor)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, tokens)
}

// createToken serves POST /api/tokens. The plaintext token in the response is
// shown once and is never retrievable again, because only its hash is stored.
func (m *Module) createToken(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	var in tokenRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	var ttl time.Duration
	if in.TTLDays > 0 {
		ttl = time.Duration(in.TTLDays) * 24 * time.Hour
	}
	record, plaintext, err := m.Backend.CreateAPIToken(r.Context(), actor, in.Name, ttl)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, tokenResponse{Token: plaintext, Record: record})
}

// revokeToken serves DELETE /api/tokens/{id}.
func (m *Module) revokeToken(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	if err := m.Backend.RevokeAPIToken(r.Context(), actor, r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}
