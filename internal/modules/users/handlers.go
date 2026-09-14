package users

import (
	"net/http"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// listUsers serves GET /api/users.
func (m *Module) listUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := api.IntParam(q.Get("limit"), 50, 200)
	offset := api.IntParam(q.Get("offset"), 0, 0)
	users, total, err := m.Backend.ListUsers(r.Context(), api.ActorFrom(r.Context()), q.Get("q"), limit, offset)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItemsTotal(w, users, total)
}

// createUser serves POST /api/users.
func (m *Module) createUser(w http.ResponseWriter, r *http.Request) {
	var in core.UserInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	user, err := m.Backend.CreateUser(r.Context(), api.ActorFrom(r.Context()), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, user)
}

// lookupUsers serves GET /api/users/lookup, the share recipient picker.
func (m *Module) lookupUsers(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	q := r.URL.Query()
	users, err := m.Backend.LookupUsers(r.Context(), actor, q.Get("q"), api.IntParam(q.Get("limit"), 10, 50))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, users)
}

// getUser serves GET /api/users/{id}.
func (m *Module) getUser(w http.ResponseWriter, r *http.Request) {
	user, err := m.Backend.GetUser(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, user)
}

// updateUser serves PATCH /api/users/{id}.
func (m *Module) updateUser(w http.ResponseWriter, r *http.Request) {
	var in core.UserInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	user, err := m.Backend.UpdateUser(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, user)
}

// deleteUser serves DELETE /api/users/{id}.
func (m *Module) deleteUser(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.DeleteUser(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// listInvites serves GET /api/invites.
func (m *Module) listInvites(w http.ResponseWriter, r *http.Request) {
	invites, err := m.Backend.ListInvites(r.Context(), api.ActorFrom(r.Context()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, invites)
}

// createInvite serves POST /api/invites.
func (m *Module) createInvite(w http.ResponseWriter, r *http.Request) {
	var in core.InviteInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	invite, err := m.Backend.CreateInvite(r.Context(), api.ActorFrom(r.Context()), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, invite)
}

// revokeInvite serves DELETE /api/invites/{id}.
func (m *Module) revokeInvite(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.RevokeInvite(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}
