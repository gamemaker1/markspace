package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// slugPattern matches the address of a space.
var slugPattern = regexp.MustCompile(`^[a-z0-9_-]*$`)

// handleSpaces renders a card for every space the actor may view, with the
// create form underneath.
func (s *Server) handleSpaces(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	p := s.newPage(r, "Spaces", "spaces")
	p.Flash = r.URL.Query().Get("flash")
	s.Render(w, r, http.StatusOK, "spaces", p)
}

// handleSpacesPost creates a space and redirects to its home page. A refused
// form renders again with the entered values.
func (s *Server) handleSpacesPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	form := newForm("space", r)
	in := core.SpaceInput{}
	if v := form.value("name"); v != "" {
		in.Name = &v
	} else {
		form.add("name", "Enter a name")
	}
	if v := form.value("slug"); v != "" {
		if !slugPattern.MatchString(v) {
			form.add("slug", "The address can use lowercase letters, digits and dashes")
		}
		in.Slug = &v
	}
	if v := form.value("description"); v != "" {
		in.Description = &v
	}
	if v := form.value("icon"); v != "" {
		in.Icon = &v
	}
	var err error
	if !form.invalid() {
		var space *core.Space
		space, err = s.backend.CreateSpace(r.Context(), actor, in)
		if err == nil {
			http.Redirect(w, r, spaceURL(space), http.StatusSeeOther)
			return
		}
		if hardFailure(err) {
			s.fail(w, r, err)
			return
		}
		if errors.Is(err, core.ErrExists) && fieldOf(err) == "" {
			form.add("slug", "Another space uses this address")
		} else {
			form.fail(err, "The space could not be created.", nil)
		}
	}
	p := s.newPage(r, "Spaces", "spaces")
	p.Set(formDataKey, form)
	s.Render(w, r, form.status(err), "spaces", p)
}

// handleSpaceSettings renders the space settings page. The member table,
// import and delete appear only with manage access.
func (s *Server) handleSpaceSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	s.renderSpaceSettings(w, r, nil, http.StatusOK)
}

// renderSpaceSettings renders the settings page of the space in the path,
// with a refused form when form is not nil.
func (s *Server) renderSpaceSettings(w http.ResponseWriter, r *http.Request, form *formState, status int) {
	actor := currentActor(r)
	p := s.newPage(r, "Space settings", "settings")
	if err := s.attachSpace(r, p, r.PathValue("space")); err != nil {
		s.fail(w, r, err)
		return
	}
	p.Title = p.Space.Name + " settings"
	p.Flash = r.URL.Query().Get("flash")
	if p.SpaceAccess.AtLeast(core.AccessManage) {
		if members, err := s.backend.ListMembers(r.Context(), actor, p.Space.ID); err == nil {
			p.Set("Members", members)
		}
	}
	p.Set("Roles", core.Roles)
	if form != nil {
		p.Set(formDataKey, form)
	}
	s.Render(w, r, status, "space/settings", p)
}

// handleSpaceSettingsPost applies one space settings action: the details
// form, a member change, an import or deleting the space.
func (s *Server) handleSpaceSettingsPost(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if err := s.formValues(r); err != nil {
		s.fail(w, r, core.Invalid("form", "the form could not be read"))
		return
	}
	ctx := r.Context()
	space, err := s.backend.GetSpace(ctx, actor, r.PathValue("space"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	settingsURL := spaceURL(space) + "/settings"

	switch r.PostFormValue("action") {
	case "member-add":
		s.addSpaceMember(w, r, actor, space)
	case "member-role":
		role := core.Role(strings.TrimSpace(r.PostFormValue("role")))
		if _, err := s.backend.UpdateMember(ctx, actor, space.ID, r.PostFormValue("userId"), role); err != nil {
			s.formFail(w, r, settingsURL+"#members", err, "The role could not be changed.")
			return
		}
		http.Redirect(w, r, settingsURL+"#members", http.StatusSeeOther)
	case "member-remove":
		userID := strings.TrimSpace(r.PostFormValue("userId"))
		if err := s.backend.RemoveMember(ctx, actor, space.ID, userID); err != nil {
			s.formFail(w, r, settingsURL+"#members", err, "The member could not be removed.")
			return
		}
		// With scripting on, the page script offers Undo on the next page.
		// Without it, the page shows an Undo form for the removed member.
		if r.PostFormValue("noscript") == "1" {
			v := url.Values{"removed": {userID}, "role": {strings.TrimSpace(r.PostFormValue("role"))}}
			http.Redirect(w, r, withQuery(settingsURL+"#members", v), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, settingsURL+"#members", http.StatusSeeOther)
	case "import":
		s.importIntoSpace(w, r, actor, space)
	case "delete":
		if needsConfirm(r) {
			s.renderConfirm(w, r, "settings", space, &confirmView{
				Title:        "Delete " + space.Name + "?",
				Body:         "Every member loses access at once. The files move to the deleted archive on disk, where an administrator can still recover them.",
				ConfirmLabel: "Delete space",
				Danger:       true,
				Action:       settingsURL,
				CancelURL:    settingsURL,
				Fields:       confirmFields(r.PostForm),
			})
			return
		}
		if err := s.backend.DeleteSpace(ctx, actor, space.ID); err != nil {
			s.formFail(w, r, settingsURL, err, "The space could not be deleted.")
			return
		}
		http.Redirect(w, r, "/spaces?flash="+queryValue(space.Name+" was deleted."), http.StatusSeeOther)
	default:
		s.saveSpaceDetails(w, r, actor, space)
	}
}

// saveSpaceDetails checks and saves the details form. An unchanged form
// saves nothing and shows nothing.
func (s *Server) saveSpaceDetails(w http.ResponseWriter, r *http.Request, actor *core.Actor, space *core.Space) {
	form := newForm("details", r)
	in := core.SpaceInput{}
	changed := false
	if v := form.value("name"); v == "" {
		form.add("name", "Enter a name")
	} else if v != space.Name {
		in.Name = &v
		changed = true
	}
	for _, f := range []struct {
		field   string
		current string
		dst     **string
	}{
		{"description", space.Description, &in.Description},
		{"icon", space.Icon, &in.Icon},
	} {
		if !r.PostForm.Has(f.field) {
			continue
		}
		if v := form.value(f.field); v != f.current {
			*f.dst = &v
			changed = true
		}
	}
	if form.invalid() {
		s.renderSpaceSettings(w, r, form, http.StatusBadRequest)
		return
	}
	settingsURL := spaceURL(space) + "/settings"
	if !changed {
		http.Redirect(w, r, settingsURL, http.StatusSeeOther)
		return
	}
	updated, err := s.backend.UpdateSpace(r.Context(), actor, space.ID, in)
	if err != nil {
		if hardFailure(err) {
			s.fail(w, r, err)
			return
		}
		form.fail(err, "The details could not be saved.", nil)
		s.renderSpaceSettings(w, r, form, form.status(err))
		return
	}
	if updated != nil {
		settingsURL = spaceURL(updated) + "/settings"
	}
	http.Redirect(w, r, settingsURL+"?saved=details#details", http.StatusSeeOther)
}

// addSpaceMember resolves an email to a user and grants it a role. The new
// row is visible in the member table, so no message shows.
func (s *Server) addSpaceMember(w http.ResponseWriter, r *http.Request, actor *core.Actor, space *core.Space) {
	ctx := r.Context()
	form := newForm("member", r)
	role := core.Role(form.value("role"))
	if !role.Valid() {
		role = core.RoleEditor
	}
	userID := form.value("userId")
	if userID == "" {
		email := form.value("email")
		switch {
		case email == "":
			form.add("email", "Enter the email address of the person to add")
		case !strings.Contains(email, "@"):
			form.add("email", "Enter an email address such as ada@example.com")
		default:
			found, err := s.backend.LookupUsers(ctx, actor, email, 5)
			if err != nil && hardFailure(err) {
				s.fail(w, r, err)
				return
			}
			for _, u := range found {
				if strings.EqualFold(u.Email, email) {
					userID = u.ID
					break
				}
			}
			if userID == "" {
				form.add("email", "No account uses "+email+". The person needs an account first")
			}
		}
	}
	var err error
	if !form.invalid() {
		if _, err = s.backend.AddMember(ctx, actor, space.ID, userID, role); err == nil {
			http.Redirect(w, r, spaceURL(space)+"/settings#members", http.StatusSeeOther)
			return
		}
		if hardFailure(err) {
			s.fail(w, r, err)
			return
		}
		if (errors.Is(err, core.ErrExists) || errors.Is(err, core.ErrConflict)) && fieldOf(err) == "" {
			form.add("email", "That person is already a member")
		} else {
			form.fail(err, "The member could not be added.", map[string]string{"userId": "email"})
		}
	}
	s.renderSpaceSettings(w, r, form, form.status(err))
}

// importIntoSpace reads an uploaded zip or markdown file into the space. The
// imported notes are not visible on the settings page, so a Banner reports
// the count.
func (s *Server) importIntoSpace(w http.ResponseWriter, r *http.Request, actor *core.Actor, space *core.Space) {
	form := newForm("import", r)
	form.values.Set("file", "")
	file, header, err := r.FormFile("file")
	if err != nil {
		form.add("file", "Choose a zip or markdown file to import")
		s.renderSpaceSettings(w, r, form, http.StatusBadRequest)
		return
	}
	defer file.Close()
	n, err := s.backend.ImportSpace(r.Context(), actor, space.ID, file, header.Size)
	if err != nil {
		if hardFailure(err) {
			s.fail(w, r, err)
			return
		}
		form.fail(err, "The file could not be imported.", nil)
		if !form.invalid() || form.message != "" {
			form.problems, form.message = nil, ""
			form.add("file", authMessage(err, "The file could not be imported."))
		}
		s.renderSpaceSettings(w, r, form, form.status(err))
		return
	}
	flash := pluralize(n, "note imported.", "notes imported.")
	http.Redirect(w, r, spaceURL(space)+"/settings?flash="+queryValue(flash)+"#import", http.StatusSeeOther)
}

// trashConfirm describes the confirmation page of a destructive trash
// action: "empty" or "delete" with a note id. It returns core.ErrNotFound
// for a note that is not in the trash and an invalid error for any other
// action. The trash POST handler renders it when needsConfirm reports that
// scripting is off.
func (s *Server) trashConfirm(ctx context.Context, actor *core.Actor, space *core.Space, action, noteID string) (*confirmView, error) {
	trashURL := spaceURL(space) + "/trash"
	c := &confirmView{Danger: true, Action: trashURL, CancelURL: trashURL}
	switch action {
	case "empty":
		c.Title = "Empty the trash?"
		c.Body = "Every page in the trash is deleted forever. This cannot be undone."
		c.ConfirmLabel = "Empty trash"
		c.Fields = []confirmField{{Name: "action", Value: "empty"}}
	case "delete":
		id := strings.TrimSpace(noteID)
		notes, err := s.backend.ListTrash(ctx, actor, space.ID)
		if err != nil {
			return nil, err
		}
		title := ""
		found := false
		for _, n := range notes {
			if n.ID == id {
				title, found = n.Title, true
			}
		}
		if !found {
			return nil, core.ErrNotFound
		}
		c.Title = "Delete " + title + " forever?"
		c.Body = "The page cannot be restored afterwards."
		c.ConfirmLabel = "Delete forever"
		c.Fields = []confirmField{{Name: "action", Value: "delete"}, {Name: "noteId", Value: id}}
	default:
		return nil, core.Invalid("action", "unknown trash action")
	}
	return c, nil
}

// queryValue escapes a flash message for a redirect query string.
func queryValue(v string) string { return url.QueryEscape(v) }
