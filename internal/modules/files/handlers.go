package files

import (
	"net/http"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// moveRequest is the body of POST /api/notes/{id}/move.
type moveRequest struct {
	ParentID string `json:"parentId,omitempty"`
	Position int    `json:"position,omitempty"`
}

// fileResponse is a file with the actor's resolved access folded in.
type fileResponse struct {
	*core.File
	Access *core.Grant `json:"access,omitempty"`
}

// uploadFile serves POST /api/files. The body is multipart. The text fields
// spaceId, parentId, id, onConflict and pasteFor must come before the part
// named "file", because the file part is streamed to the backend as it
// arrives and never buffered.
func (m *Module) uploadFile(w http.ResponseWriter, r *http.Request) {
	api.LimitUpload(w, r, m.MaxUpload)
	fields, part, err := api.UploadParts(r, "spaceId", "parentId", "id", "onConflict", "pasteFor")
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	defer part.Close()
	in := core.FileUpload{
		ID:         fields["id"],
		SpaceID:    fields["spaceId"],
		ParentID:   fields["parentId"],
		Name:       api.PartName(part),
		OnConflict: fields["onConflict"],
		PasteFor:   fields["pasteFor"],
	}
	file, err := m.Backend.UploadFile(r.Context(), api.ActorFrom(r.Context()), in, part)
	if err != nil {
		m.Fail(w, r, api.UploadErr(err))
		return
	}
	api.WriteJSON(w, http.StatusCreated, file)
}

// getFile serves GET /api/files/{id}.
func (m *Module) getFile(w http.ResponseWriter, r *http.Request) {
	file, grant, err := m.Backend.GetFile(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, fileResponse{File: file, Access: grant})
}

// renameFile serves PATCH /api/files/{id}.
func (m *Module) renameFile(w http.ResponseWriter, r *http.Request) {
	var in nameRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	file, err := m.Backend.RenameFile(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), in.Name)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, file)
}

// replaceFileContent serves PUT /api/files/{id}/content. The body is
// multipart, with the baseSha256 field before the part named "file". A
// baseSha256 that no longer matches answers 409.
func (m *Module) replaceFileContent(w http.ResponseWriter, r *http.Request) {
	api.LimitUpload(w, r, m.MaxUpload)
	fields, part, err := api.UploadParts(r, "baseSha256")
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	defer part.Close()
	in := core.FileReplace{Name: api.PartName(part), BaseSHA256: fields["baseSha256"]}
	file, err := m.Backend.ReplaceFile(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), in, part)
	if err != nil {
		m.Fail(w, r, api.UploadErr(err))
		return
	}
	api.WriteJSON(w, http.StatusOK, file)
}

// listFileVersions serves GET /api/files/{id}/versions.
func (m *Module) listFileVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := m.Backend.ListFileVersions(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, versions)
}

// restoreFileVersion serves POST /api/files/{id}/versions/{versionId}/restore.
func (m *Module) restoreFileVersion(w http.ResponseWriter, r *http.Request) {
	file, err := m.Backend.RestoreFileVersion(r.Context(), api.ActorFrom(r.Context()),
		r.PathValue("id"), r.PathValue("versionId"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, file)
}

// listSpaceFiles serves GET /api/spaces/{id}/files.
func (m *Module) listSpaceFiles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fq := core.FileQuery{
		Type:   q.Get("type"),
		Sort:   q.Get("sort"),
		Limit:  api.IntParam(q.Get("limit"), 0, 1000),
		Offset: api.IntParam(q.Get("offset"), 0, 0),
	}
	files, err := m.Backend.ListFiles(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), fq)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, files)
}
