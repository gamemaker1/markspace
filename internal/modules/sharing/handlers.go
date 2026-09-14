package sharing

import (
	"net/http"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// shareUpdateRequest is the body of PATCH /api/shares/{id}.
type shareUpdateRequest struct {
	Access          core.Access `json:"access"`
	IncludeChildren bool        `json:"includeChildren,omitempty"`
}

// listShares serves GET /api/notes/{id}/shares.
func (m *Module) listShares(w http.ResponseWriter, r *http.Request) {
	shares, err := m.Backend.ListShares(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, shares)
}

// createShare serves POST /api/notes/{id}/shares. The path always wins over a
// noteId in the body.
func (m *Module) createShare(w http.ResponseWriter, r *http.Request) {
	var in core.ShareInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	in.NoteID = r.PathValue("id")
	share, err := m.Backend.CreateShare(r.Context(), api.ActorFrom(r.Context()), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, share)
}

// updateShare serves PATCH /api/shares/{id}.
func (m *Module) updateShare(w http.ResponseWriter, r *http.Request) {
	var in shareUpdateRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	share, err := m.Backend.UpdateShare(r.Context(), api.ActorFrom(r.Context()),
		r.PathValue("id"), in.Access, in.IncludeChildren)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, share)
}

// deleteShare serves DELETE /api/shares/{id}.
func (m *Module) deleteShare(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.DeleteShare(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// listSharedWithMe serves GET /api/shares/mine.
func (m *Module) listSharedWithMe(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	shares, err := m.Backend.ListSharedWithMe(r.Context(), actor)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, shares)
}

// listLinks serves GET /api/notes/{id}/links.
func (m *Module) listLinks(w http.ResponseWriter, r *http.Request) {
	links, err := m.Backend.ListPublicLinks(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, links)
}

// createLink serves POST /api/notes/{id}/links. The path always wins over a
// noteId in the body.
func (m *Module) createLink(w http.ResponseWriter, r *http.Request) {
	var in core.PublicLinkInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	in.NoteID = r.PathValue("id")
	link, err := m.Backend.CreatePublicLink(r.Context(), api.ActorFrom(r.Context()), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, link)
}

// updateLink serves PATCH /api/links/{id}.
func (m *Module) updateLink(w http.ResponseWriter, r *http.Request) {
	var in core.PublicLinkInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	link, err := m.Backend.UpdatePublicLink(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, link)
}

// deleteLink serves DELETE /api/links/{id}.
func (m *Module) deleteLink(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.DeletePublicLink(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}
