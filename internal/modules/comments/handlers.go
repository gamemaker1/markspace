package comments

import (
	"net/http"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// commentBodyRequest is the body of PATCH /api/comments/{id}.
type commentBodyRequest struct {
	Body string `json:"body"`
}

// resolveRequest is the body of POST /api/comments/{id}/resolve.
type resolveRequest struct {
	Resolved bool `json:"resolved"`
}

// listComments serves GET /api/notes/{id}/comments.
func (m *Module) listComments(w http.ResponseWriter, r *http.Request) {
	comments, err := m.Backend.ListComments(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, comments)
}

// createComment serves POST /api/notes/{id}/comments. The path always wins
// over a noteId in the body.
func (m *Module) createComment(w http.ResponseWriter, r *http.Request) {
	var in core.CommentInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	in.NoteID = r.PathValue("id")
	comment, err := m.Backend.CreateComment(r.Context(), api.ActorFrom(r.Context()), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, comment)
}

// updateComment serves PATCH /api/comments/{id}.
func (m *Module) updateComment(w http.ResponseWriter, r *http.Request) {
	var in commentBodyRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	comment, err := m.Backend.UpdateComment(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), in.Body)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, comment)
}

// deleteComment serves DELETE /api/comments/{id}.
func (m *Module) deleteComment(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.DeleteComment(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// resolveComment serves POST /api/comments/{id}/resolve.
func (m *Module) resolveComment(w http.ResponseWriter, r *http.Request) {
	var in resolveRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	comment, err := m.Backend.ResolveComment(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), in.Resolved)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, comment)
}
