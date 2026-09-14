package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// apiError is the JSON error body described in docs/routes.md.
type apiError struct {
	Error string `json:"error"`
	Field string `json:"field,omitempty"`
}

// mustConflict checks for a 409 with a JSON error body.
func (h *harness) mustConflict(method, path string, body any, what string) {
	h.t.Helper()
	var e apiError
	resp := h.do(method, path, body, &e)
	h.mustStatus(resp, http.StatusConflict, what)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		h.t.Fatalf("%s: content type %q, want JSON", what, ct)
	}
	if e.Error == "" {
		h.t.Fatalf("%s: empty error message", what)
	}
}

// TestLastAdminAndOwnerOverHTTP covers the reported bug and its relatives end
// to end: the last admin cannot be deleted, demoted or deactivated, the last
// owner cannot be removed or demoted, and deleting a sole owner hands the
// space to the admin.
func TestLastAdminAndOwnerOverHTTP(t *testing.T) {
	h := newHarness(t)
	var admin core.User
	h.mustStatus(h.do("POST", "/api/auth/signup", map[string]string{
		"email": "admin@example.com", "name": "Ada Admin", "password": "correct horse battery",
	}, &admin), http.StatusCreated, "signup")
	var spaces struct{ Items []*core.Space }
	h.mustStatus(h.do("GET", "/api/spaces", nil, &spaces), http.StatusOK, "list spaces")
	if len(spaces.Items) == 0 {
		t.Fatal("first user has no personal space")
	}
	home := spaces.Items[0]

	h.mustConflict("DELETE", "/api/users/"+admin.ID, nil, "delete last admin")
	h.mustConflict("PATCH", "/api/users/"+admin.ID, map[string]any{"isAdmin": false}, "demote last admin")
	h.mustConflict("PATCH", "/api/users/"+admin.ID, map[string]any{"isActive": false}, "deactivate last admin")
	var still core.User
	h.mustStatus(h.do("GET", "/api/users/"+admin.ID, nil, &still), http.StatusOK, "admin after refusals")
	if !still.IsAdmin || !still.IsActive {
		t.Fatalf("refused requests changed the admin: %+v", still)
	}

	h.mustConflict("DELETE", "/api/spaces/"+home.ID+"/members/"+admin.ID, nil, "remove last owner")
	h.mustConflict("PATCH", "/api/spaces/"+home.ID+"/members/"+admin.ID, map[string]any{"role": "editor"}, "demote last owner")

	// Bob owns a space alone. Deleting him hands it to the admin.
	var bob core.User
	h.mustStatus(h.do("POST", "/api/users", core.UserInput{
		Email: ptr("bob@example.com"), Name: ptr("Bob"), Password: ptr("another good password"),
	}, &bob), http.StatusCreated, "create bob")
	b := h.as("bob@example.com", "another good password")
	var bobSpace core.Space
	b.mustStatus(b.do("POST", "/api/spaces", core.SpaceInput{Name: ptr("Bob notes")}, &bobSpace), http.StatusCreated, "bob creates a space")

	h.mustStatus(h.do("DELETE", "/api/users/"+bob.ID, nil, nil), http.StatusNoContent, "delete bob")
	var members struct{ Items []*core.SpaceMember }
	h.mustStatus(h.do("GET", "/api/spaces/"+bobSpace.ID+"/members", nil, &members), http.StatusOK, "members after delete")
	if len(members.Items) != 1 || members.Items[0].UserID != admin.ID || members.Items[0].Role != core.RoleOwner {
		t.Fatalf("members of the deleted user's space = %+v, want the admin as owner", members.Items)
	}
	var me struct {
		User *core.User `json:"user"`
	}
	b.do("GET", "/api/auth/me", nil, &me)
	if me.User != nil {
		t.Fatal("the deleted user's session still resolves")
	}

	// With a second active admin, the first may step down.
	var carol core.User
	h.mustStatus(h.do("POST", "/api/users", core.UserInput{
		Email: ptr("carol@example.com"), Password: ptr("a third good password"), IsAdmin: ptr(true),
	}, &carol), http.StatusCreated, "create second admin")
	h.mustStatus(h.do("PATCH", "/api/users/"+admin.ID, map[string]any{"isAdmin": false}, nil), http.StatusOK, "demote with another admin")
}

// TestTrashedTitleCanBeReusedOverHTTP covers a trashed note keeping its path
// in the index. A new note with the same title used to fail with 409 after
// its file was already written.
func TestTrashedTitleCanBeReusedOverHTTP(t *testing.T) {
	h := newHarness(t)
	h.mustStatus(h.do("POST", "/api/auth/signup", map[string]string{
		"email": "admin@example.com", "name": "Ada Admin", "password": "correct horse battery",
	}, nil), http.StatusCreated, "signup")
	var spaces struct{ Items []*core.Space }
	h.do("GET", "/api/spaces", nil, &spaces)
	space := spaces.Items[0]

	var plan, fresh, other core.Note
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{SpaceID: space.ID, Title: "Plan", Content: "old\n"}, &plan), http.StatusCreated, "create plan")
	h.mustStatus(h.do("POST", "/api/notes/"+plan.ID+"/trash", nil, nil), http.StatusNoContent, "trash plan")
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{SpaceID: space.ID, Title: "Plan", Content: "new\n"}, &fresh), http.StatusCreated, "create with the trashed title")
	// A trashed note leaves its path, so the new note may take it. The
	// restore below then picks a free name instead.
	h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{SpaceID: space.ID, Title: "Other"}, &other), http.StatusCreated, "create other")
	h.mustStatus(h.do("PATCH", "/api/notes/"+other.ID, core.NoteUpdate{Title: ptr("Plan")}, &other), http.StatusOK, "rename to the trashed title")
	h.mustStatus(h.do("POST", "/api/notes/"+plan.ID+"/restore", nil, nil), http.StatusNoContent, "restore plan")

	var got core.Note
	h.mustStatus(h.do("GET", "/api/notes/"+plan.ID, nil, &got), http.StatusOK, "read restored plan")
	if got.Path == other.Path || got.Path == fresh.Path || !strings.Contains(got.Content, "old") {
		t.Fatalf("restored plan = %s %q, collides with %s or %s", got.Path, got.Content, other.Path, fresh.Path)
	}
	var tree struct{ Items []*core.TreeItem }
	h.mustStatus(h.do("GET", "/api/spaces/"+space.ID+"/tree", nil, &tree), http.StatusOK, "tree")
	entries, err := os.ReadDir(filepath.Join(h.cfg.SpacesDir(), space.Slug))
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			files++
		}
	}
	notes := 0
	for _, it := range tree.Items {
		if it.Note != nil {
			notes++
		}
	}
	if files != notes {
		t.Fatalf("%d note files on disk but %d notes in the tree: an orphan file was left", files, notes)
	}
}
