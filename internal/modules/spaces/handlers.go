package spaces

import (
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// memberRequest is the body of the space member routes. Either userId or
// email identifies the account.
type memberRequest struct {
	UserID string    `json:"userId,omitempty"`
	Email  string    `json:"email,omitempty"`
	Role   core.Role `json:"role"`
}

// roleRequest is the body of PATCH /api/spaces/{id}/members/{userId}.
type roleRequest struct {
	Role core.Role `json:"role"`
}

// listSpaces serves GET /api/spaces.
func (m *Module) listSpaces(w http.ResponseWriter, r *http.Request) {
	spaces, err := m.Backend.ListSpaces(r.Context(), api.ActorFrom(r.Context()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, spaces)
}

// createSpace serves POST /api/spaces.
func (m *Module) createSpace(w http.ResponseWriter, r *http.Request) {
	var in core.SpaceInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	space, err := m.Backend.CreateSpace(r.Context(), api.ActorFrom(r.Context()), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, space)
}

// getSpace serves GET /api/spaces/{id}, which accepts an id or a slug.
func (m *Module) getSpace(w http.ResponseWriter, r *http.Request) {
	space, err := m.Backend.GetSpace(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, space)
}

// updateSpace serves PATCH /api/spaces/{id}.
func (m *Module) updateSpace(w http.ResponseWriter, r *http.Request) {
	var in core.SpaceInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	space, err := m.Backend.UpdateSpace(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, space)
}

// deleteSpace serves DELETE /api/spaces/{id}.
func (m *Module) deleteSpace(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.DeleteSpace(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// listMembers serves GET /api/spaces/{id}/members.
func (m *Module) listMembers(w http.ResponseWriter, r *http.Request) {
	members, err := m.Backend.ListMembers(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, members)
}

// addMember serves POST /api/spaces/{id}/members. A body that names an email
// instead of a user id is resolved with LookupUsers first, because
// Backend.AddMember takes only a user id.
func (m *Module) addMember(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	var in memberRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	userID := in.UserID
	if userID == "" {
		id, err := m.resolveEmail(r, actor, in.Email)
		if err != nil {
			m.Fail(w, r, err)
			return
		}
		userID = id
	}
	member, err := m.Backend.AddMember(r.Context(), actor, r.PathValue("id"), userID, in.Role)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, member)
}

// resolveEmail turns an email address into a user id.
func (m *Module) resolveEmail(r *http.Request, actor *core.Actor, email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", core.Invalid("userId", "userId or email is required")
	}
	users, err := m.Backend.LookupUsers(r.Context(), actor, email, 20)
	if err != nil {
		return "", err
	}
	for _, u := range users {
		if strings.EqualFold(u.Email, email) {
			return u.ID, nil
		}
	}
	return "", core.Invalid("email", "no user with that email")
}

// updateMember serves PATCH /api/spaces/{id}/members/{userId}.
func (m *Module) updateMember(w http.ResponseWriter, r *http.Request) {
	var in roleRequest
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	member, err := m.Backend.UpdateMember(r.Context(), api.ActorFrom(r.Context()),
		r.PathValue("id"), r.PathValue("userId"), in.Role)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, member)
}

// removeMember serves DELETE /api/spaces/{id}/members/{userId}.
func (m *Module) removeMember(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.RemoveMember(r.Context(), api.ActorFrom(r.Context()),
		r.PathValue("id"), r.PathValue("userId")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// importedResponse reports how many notes an import wrote.
type importedResponse struct {
	Imported int `json:"imported"`
}

// exportSpace serves GET /api/spaces/{id}/export and streams a zip archive.
func (m *Module) exportSpace(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	id := r.PathValue("id")
	space, err := m.Backend.GetSpace(r.Context(), actor, id)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+api.SafeFilename(space.Slug+".zip")+`"`)
	w.WriteHeader(http.StatusOK)
	if err := m.Backend.ExportSpace(r.Context(), actor, space.ID, w); err != nil {
		// The status line is already on the wire, so the only option left is
		// to log the failure and truncate the archive.
		m.Logger.Error("space export failed", "space", space.ID, "err", err)
	}
}

// importSpace serves POST /api/spaces/{id}/import. The uploaded part is
// copied to a temporary file first, because the backend needs a reader that
// supports random access and a known size.
func (m *Module) importSpace(w http.ResponseWriter, r *http.Request) {
	actor := api.ActorFrom(r.Context())
	spaceID := r.PathValue("id")

	r.Body = http.MaxBytesReader(w, r.Body, m.MaxUpload+1024)
	if err := r.ParseMultipartForm(api.MultipartMemory); err != nil {
		m.Fail(w, r, core.Invalid("file", "could not read the multipart body"))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	header, err := api.FirstFile(r.MultipartForm, "file")
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	part, err := header.Open()
	if err != nil {
		m.Fail(w, r, core.Invalid("file", "could not open the uploaded part"))
		return
	}
	defer part.Close()

	tmp, err := os.CreateTemp("", "markspace-import-*")
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	defer func() {
		name := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(name)
	}()

	size, err := io.Copy(tmp, part)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	if size == 0 {
		m.Fail(w, r, core.Invalid("file", "the uploaded file is empty"))
		return
	}
	n, err := m.Backend.ImportSpace(r.Context(), actor, spaceID, tmp, size)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, importedResponse{Imported: n})
}
