package notes

import (
	"net/http"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// listSpaceNotes serves GET /api/spaces/{id}/notes.
func (m *Module) listSpaceNotes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := core.NoteListOptions{
		Tag:    q.Get("tag"),
		Sort:   q.Get("sort"),
		Limit:  api.IntParam(q.Get("limit"), 100, 500),
		Offset: api.IntParam(q.Get("offset"), 0, 0),
	}
	if q.Has("parentId") {
		opts.ParentID = q.Get("parentId")
		opts.HasParent = true
	}
	notes, err := m.Backend.ListNotes(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), opts)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, notes)
}

// spaceTags serves GET /api/spaces/{id}/tags.
func (m *Module) spaceTags(w http.ResponseWriter, r *http.Request) {
	tags, err := m.Backend.ListTags(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, tags)
}

// spaceTrash serves GET /api/spaces/{id}/trash. It lists trashed notes, or
// trashed items of every kind with ?items=1.
func (m *Module) spaceTrash(w http.ResponseWriter, r *http.Request) {
	if api.BoolParam(r.URL.Query().Get("items")) {
		items, err := m.Backend.ListTrashItems(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
		if err != nil {
			m.Fail(w, r, err)
			return
		}
		api.WriteItems(w, items)
		return
	}
	notes, err := m.Backend.ListTrash(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, notes)
}

// emptyTrash serves POST /api/spaces/{id}/trash/empty.
func (m *Module) emptyTrash(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.EmptyTrash(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// noteResponse is a note with the actor's resolved access folded in, so the
// note fields stay at the top level of the object.
type noteResponse struct {
	*core.Note
	Access *core.Grant `json:"access,omitempty"`
}

// moveRequest is the body of POST /api/notes/{id}/move.
type moveRequest struct {
	ParentID string `json:"parentId,omitempty"`
	Position int    `json:"position,omitempty"`
}

// diffResponse is the body of the version diff route.
type diffResponse struct {
	Diff string `json:"diff"`
}

// createNote serves POST /api/notes.
func (m *Module) createNote(w http.ResponseWriter, r *http.Request) {
	var in core.NoteInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	note, err := m.Backend.CreateNote(r.Context(), api.ActorFrom(r.Context()), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, note)
}

// getNote serves GET /api/notes/{id}.
func (m *Module) getNote(w http.ResponseWriter, r *http.Request) {
	note, grant, err := m.Backend.GetNote(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, noteResponse{Note: note, Access: grant})
}

// patchNote serves PATCH /api/notes/{id}.
func (m *Module) patchNote(w http.ResponseWriter, r *http.Request) {
	var in core.NoteUpdate
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	note, err := m.Backend.UpdateNote(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, note)
}

// deleteNote serves DELETE /api/notes/{id}, the permanent delete.
func (m *Module) deleteNote(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.DeleteNote(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// moveNote serves POST /api/notes/{id}/move.
func (m *Module) moveNote(w http.ResponseWriter, r *http.Request) {
	var in moveRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	note, err := m.Backend.MoveNote(r.Context(), api.ActorFrom(r.Context()),
		r.PathValue("id"), in.ParentID, in.Position)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, note)
}

// duplicateNote serves POST /api/notes/{id}/duplicate.
func (m *Module) duplicateNote(w http.ResponseWriter, r *http.Request) {
	note, err := m.Backend.DuplicateNote(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, note)
}

// trashNote serves POST /api/notes/{id}/trash.
func (m *Module) trashNote(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.TrashNote(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// restoreNote serves POST /api/notes/{id}/restore.
func (m *Module) restoreNote(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.RestoreNote(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// setStar serves PUT /api/notes/{id}/star.
func (m *Module) setStar(w http.ResponseWriter, r *http.Request) {
	m.star(w, r, true)
}

// clearStar serves DELETE /api/notes/{id}/star.
func (m *Module) clearStar(w http.ResponseWriter, r *http.Request) {
	m.star(w, r, false)
}

// star applies one star state to the note named in the path.
func (m *Module) star(w http.ResponseWriter, r *http.Request, starred bool) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	if err := m.Backend.SetStar(r.Context(), actor, r.PathValue("id"), starred); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// backlinks serves GET /api/notes/{id}/backlinks.
func (m *Module) backlinks(w http.ResponseWriter, r *http.Request) {
	links, err := m.Backend.Backlinks(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, links)
}

// listVersions serves GET /api/notes/{id}/versions.
func (m *Module) listVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := m.Backend.ListVersions(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, versions)
}

// getVersion serves GET /api/notes/{id}/versions/{versionId}.
func (m *Module) getVersion(w http.ResponseWriter, r *http.Request) {
	version, err := m.Backend.GetVersion(r.Context(), api.ActorFrom(r.Context()),
		r.PathValue("id"), r.PathValue("versionId"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, version)
}

// diffVersion serves GET /api/notes/{id}/versions/{versionId}/diff.
func (m *Module) diffVersion(w http.ResponseWriter, r *http.Request) {
	html, err := m.Backend.DiffVersion(r.Context(), api.ActorFrom(r.Context()),
		r.PathValue("id"), r.PathValue("versionId"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, diffResponse{Diff: html})
}

// restoreVersion serves POST /api/notes/{id}/versions/{versionId}/restore.
func (m *Module) restoreVersion(w http.ResponseWriter, r *http.Request) {
	note, err := m.Backend.RestoreVersion(r.Context(), api.ActorFrom(r.Context()),
		r.PathValue("id"), r.PathValue("versionId"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, note)
}

// listStarred serves GET /api/starred.
func (m *Module) listStarred(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	notes, err := m.Backend.ListStarred(r.Context(), actor)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, notes)
}

// listRecent serves GET /api/recent.
func (m *Module) listRecent(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	if err := api.RequireUser(actor); err != nil {
		m.Fail(w, r, err)
		return
	}
	notes, err := m.Backend.RecentNotes(r.Context(), actor, api.IntParam(r.URL.Query().Get("limit"), 20, 100))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, notes)
}

// spaceTree serves GET /api/spaces/{id}/tree.
func (m *Module) spaceTree(w http.ResponseWriter, r *http.Request) {
	tree, err := m.Backend.SpaceTree(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, tree)
}

// renderRequest is the body of POST /api/render.
type renderRequest struct {
	SpaceID  string `json:"spaceId,omitempty"`
	Markdown string `json:"markdown"`
}

// render serves POST /api/render.
func (m *Module) render(w http.ResponseWriter, r *http.Request) {
	var in renderRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	out, err := m.Backend.RenderMarkdown(r.Context(), api.ActorFrom(r.Context()), in.SpaceID, in.Markdown)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}
