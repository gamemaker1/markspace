package files

import (
	"net/http"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// nameRequest is the body of the folder and file rename routes.
type nameRequest struct {
	Name string `json:"name"`
}

// folderResponse is a folder with its direct children and the actor's
// resolved access.
type folderResponse struct {
	*core.TreeItem
	Access *core.Grant `json:"access,omitempty"`
}

// createFolder serves POST /api/folders.
func (m *Module) createFolder(w http.ResponseWriter, r *http.Request) {
	var in core.FolderInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	folder, err := m.Backend.CreateFolder(r.Context(), api.ActorFrom(r.Context()), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, folder)
}

// getFolder serves GET /api/folders/{id}.
func (m *Module) getFolder(w http.ResponseWriter, r *http.Request) {
	item, grant, err := m.Backend.GetFolder(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, folderResponse{TreeItem: item, Access: grant})
}

// renameFolder serves PATCH /api/folders/{id}.
func (m *Module) renameFolder(w http.ResponseWriter, r *http.Request) {
	var in nameRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	folder, err := m.Backend.RenameFolder(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), in.Name)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, folder)
}
