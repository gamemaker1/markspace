package files

import (
	"net/http"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// copyRequest is the body of POST /api/items/{id}/copy.
type copyRequest struct {
	SpaceID  string `json:"spaceId,omitempty"`
	ParentID string `json:"parentId,omitempty"`
	Name     string `json:"name,omitempty"`
}

// getItem serves GET /api/items/{id}.
func (m *Module) getItem(w http.ResponseWriter, r *http.Request) {
	item, err := m.Backend.GetItem(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, item)
}

// moveItem serves POST /api/items/{id}/move.
func (m *Module) moveItem(w http.ResponseWriter, r *http.Request) {
	var in moveRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	item, err := m.Backend.MoveItem(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), in.ParentID, in.Position)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, item)
}

// copyItem serves POST /api/items/{id}/copy.
func (m *Module) copyItem(w http.ResponseWriter, r *http.Request) {
	var in copyRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	item, err := m.Backend.CopyItem(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"),
		core.CopyInput{SpaceID: in.SpaceID, ParentID: in.ParentID, Name: in.Name})
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, item)
}

// trashItem serves POST /api/items/{id}/trash.
func (m *Module) trashItem(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.TrashItem(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// restoreItem serves POST /api/items/{id}/restore.
func (m *Module) restoreItem(w http.ResponseWriter, r *http.Request) {
	item, err := m.Backend.RestoreItem(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, item)
}

// deleteItem serves DELETE /api/items/{id}, the permanent delete.
func (m *Module) deleteItem(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.DeleteItem(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// itemBacklinks serves GET /api/files/{id}/backlinks and
// GET /api/items/{id}/backlinks.
func (m *Module) itemBacklinks(w http.ResponseWriter, r *http.Request) {
	links, err := m.Backend.ItemBacklinks(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, links)
}

// resolveLinks serves POST /api/resolve.
func (m *Module) resolveLinks(w http.ResponseWriter, r *http.Request) {
	var in core.ResolveRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	out, err := m.Backend.ResolveLinks(r.Context(), api.ActorFrom(r.Context()), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, out)
}
