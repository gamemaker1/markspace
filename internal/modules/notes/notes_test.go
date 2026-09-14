package notes

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// serve builds the API handler over b with this module registered.
func serve(t *testing.T, b core.Backend) http.Handler {
	t.Helper()
	env := testkit.Env(b)
	return testkit.Serve(t, env, New(env))
}

func TestValidationErrorIs400WithField(t *testing.T) {
	fake := &testkit.Fake{
		CreateNoteFn: func(context.Context, *core.Actor, core.NoteInput) (*core.Note, error) {
			return nil, core.Invalid("title", "title is required")
		},
	}
	rec := testkit.Do(t, serve(t, fake), "POST", "/api/notes", `{"spaceId":"sp1"}`, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	var body api.ErrorBody
	testkit.DecodeBody(t, rec, &body)
	if body.Field != "title" {
		t.Errorf("field = %q, want title", body.Field)
	}
	if body.Error != "title is required" {
		t.Errorf("error = %q, want title is required", body.Error)
	}
}

func TestNotFoundIs404(t *testing.T) {
	fake := &testkit.Fake{
		GetNoteFn: func(context.Context, *core.Actor, string) (*core.Note, *core.Grant, error) {
			return nil, nil, fmt.Errorf("load note: %w", core.ErrNotFound)
		},
	}
	rec := testkit.Do(t, serve(t, fake), "GET", "/api/notes/n1", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
}

func TestConflictIs409(t *testing.T) {
	fake := &testkit.Fake{
		UpdateNoteFn: func(context.Context, *core.Actor, string, core.NoteUpdate) (*core.Note, error) {
			return nil, fmt.Errorf("stale write: %w", core.ErrConflict)
		},
	}
	rec := testkit.Do(t, serve(t, fake), "PATCH", "/api/notes/n1", `{"title":"x"}`, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
	}
}

func TestUnknownFieldIs400(t *testing.T) {
	fake := &testkit.Fake{
		CreateNoteFn: func(context.Context, *core.Actor, core.NoteInput) (*core.Note, error) {
			t.Fatal("backend must not be called for a bad body")
			return nil, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "POST", "/api/notes", `{"spaceId":"sp1","nope":1}`, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
	}
	var body api.ErrorBody
	testkit.DecodeBody(t, rec, &body)
	if body.Field != "body" {
		t.Errorf("field = %q, want body", body.Field)
	}
}

func TestUnmappedErrorIs500WithoutDetail(t *testing.T) {
	fake := &testkit.Fake{
		CreateNoteFn: func(context.Context, *core.Actor, core.NoteInput) (*core.Note, error) {
			return nil, fmt.Errorf("disk on fire at /secret/path")
		},
	}
	rec := testkit.Do(t, serve(t, fake), "POST", "/api/notes", `{"spaceId":"sp1"}`, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("body leaks the internal message: %s", rec.Body)
	}
	var body api.ErrorBody
	testkit.DecodeBody(t, rec, &body)
	if body.Error != "internal error" {
		t.Errorf("error = %q, want internal error", body.Error)
	}
}

func TestCreateNoteHappyPath(t *testing.T) {
	var got core.NoteInput
	fake := &testkit.Fake{
		CreateNoteFn: func(_ context.Context, _ *core.Actor, in core.NoteInput) (*core.Note, error) {
			got = in
			return &core.Note{ID: "n1", SpaceID: in.SpaceID, Title: in.Title, Path: "welcome"}, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "POST", "/api/notes",
		`{"spaceId":"sp1","title":"Welcome","tags":["a","b"]}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	if got.SpaceID != "sp1" || got.Title != "Welcome" {
		t.Errorf("input = %+v", got)
	}
	if !reflect.DeepEqual(got.Tags, []string{"a", "b"}) {
		t.Errorf("tags = %v", got.Tags)
	}
	var note core.Note
	testkit.DecodeBody(t, rec, &note)
	if note.ID != "n1" {
		t.Errorf("id = %q, want n1", note.ID)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
}

func TestPatchNoteHappyPath(t *testing.T) {
	var gotID string
	var gotUpdate core.NoteUpdate
	fake := &testkit.Fake{
		UpdateNoteFn: func(_ context.Context, _ *core.Actor, id string, in core.NoteUpdate) (*core.Note, error) {
			gotID, gotUpdate = id, in
			return &core.Note{ID: id, Title: *in.Title}, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "PATCH", "/api/notes/n7",
		`{"title":"Renamed","content":"body text"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if gotID != "n7" {
		t.Errorf("id = %q, want n7", gotID)
	}
	if gotUpdate.Title == nil || *gotUpdate.Title != "Renamed" {
		t.Errorf("title = %v", gotUpdate.Title)
	}
	if gotUpdate.Content == nil || *gotUpdate.Content != "body text" {
		t.Errorf("content = %v", gotUpdate.Content)
	}
	var note core.Note
	testkit.DecodeBody(t, rec, &note)
	if note.Title != "Renamed" {
		t.Errorf("title = %q", note.Title)
	}
}

func TestGetNoteIncludesAccess(t *testing.T) {
	fake := &testkit.Fake{
		GetNoteFn: func(context.Context, *core.Actor, string) (*core.Note, *core.Grant, error) {
			return &core.Note{ID: "n1", Title: "T"},
				&core.Grant{Access: core.AccessEdit, Source: "space", Role: core.RoleEditor}, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "GET", "/api/notes/n1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var body struct {
		ID     string `json:"id"`
		Access struct {
			Access string `json:"access"`
			Source string `json:"source"`
		} `json:"access"`
	}
	testkit.DecodeBody(t, rec, &body)
	if body.ID != "n1" {
		t.Errorf("id = %q, want n1", body.ID)
	}
	if body.Access.Access != "edit" || body.Access.Source != "space" {
		t.Errorf("access = %+v", body.Access)
	}
}

func TestSpaceTreeEnvelope(t *testing.T) {
	fake := &testkit.Fake{
		SpaceTreeFn: func(_ context.Context, _ *core.Actor, spaceID string) ([]*core.TreeItem, error) {
			if spaceID != "sp1" {
				t.Errorf("space = %q", spaceID)
			}
			return []*core.TreeItem{{
				Kind:   core.KindFolder,
				Folder: &core.Folder{ID: "fo1", Name: "Projects", Path: "Projects"},
				Children: []*core.TreeItem{
					{Kind: core.KindFile, File: &core.File{ID: "f1", Name: "a.pdf", Path: "Projects/a.pdf"}},
				},
			}}, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "GET", "/api/spaces/sp1/tree", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var env struct {
		Items []*core.TreeItem `json:"items"`
	}
	testkit.DecodeBody(t, rec, &env)
	if len(env.Items) != 1 || env.Items[0].Folder.ID != "fo1" || len(env.Items[0].Children) != 1 ||
		env.Items[0].Children[0].File.ID != "f1" {
		t.Errorf("tree = %s", rec.Body)
	}
	empty := &testkit.Fake{SpaceTreeFn: func(context.Context, *core.Actor, string) ([]*core.TreeItem, error) { return nil, nil }}
	if rec := testkit.Do(t, serve(t, empty), "GET", "/api/spaces/sp1/tree", "", ""); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("empty tree body = %s", rec.Body)
	}
}

func TestStarRoutes(t *testing.T) {
	fake := &testkit.Fake{
		ActorFromSessionFn: func(context.Context, string) (*core.Actor, error) {
			return &core.Actor{User: &core.User{ID: "u1"}}, nil
		},
	}
	// SetStar is not overridden, so the stub answers ErrUnsupported, which
	// proves the route reached the backend with an authenticated actor.
	for _, method := range []string{"PUT", "DELETE"} {
		rec := testkit.Do(t, serve(t, fake), method, "/api/notes/n1/star", "", "sess")
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%s status = %d, want 415: %s", method, rec.Code, rec.Body)
		}
	}
	// Without a session the handler rejects the actor before the backend.
	rec := testkit.Do(t, serve(t, &testkit.Fake{}), "PUT", "/api/notes/n1/star", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous status = %d, want 401", rec.Code)
	}
}
