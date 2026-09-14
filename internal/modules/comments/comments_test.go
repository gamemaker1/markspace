package comments

import (
	"context"
	"net/http"
	"testing"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// commentsBackend records the comment calls.
type commentsBackend struct {
	*testkit.Fake
	listed   []string
	created  []core.CommentInput
	resolved bool
}

func (b *commentsBackend) ListComments(_ context.Context, _ *core.Actor, id string) ([]*core.Comment, error) {
	b.listed = append(b.listed, id)
	return nil, nil
}

func (b *commentsBackend) CreateComment(_ context.Context, _ *core.Actor, in core.CommentInput) (*core.Comment, error) {
	b.created = append(b.created, in)
	return &core.Comment{ID: "c1", NoteID: in.NoteID, Body: in.Body}, nil
}

func (b *commentsBackend) ResolveComment(_ context.Context, _ *core.Actor, id string, resolved bool) (*core.Comment, error) {
	b.resolved = resolved
	return &core.Comment{ID: id}, nil
}

func TestCommentRoutes(t *testing.T) {
	b := &commentsBackend{Fake: testkit.SignedIn(false)}
	env := testkit.Env(b)
	h := testkit.Serve(t, env, New(env))

	for _, p := range []string{"/api/notes/n1/comments", "/api/items/f1/comments"} {
		if rec := testkit.Do(t, h, "GET", p, "", "s"); rec.Code != http.StatusOK || rec.Body.String() != "{\"items\":[]}\n" {
			t.Fatalf("list %s = %d %s", p, rec.Code, rec.Body)
		}
	}
	rec := testkit.Do(t, h, "POST", "/api/items/f1/comments", `{"noteId":"other","body":"Looks good"}`, "s")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if len(b.listed) != 2 || b.listed[1] != "f1" || len(b.created) != 1 || b.created[0].NoteID != "f1" || b.created[0].Body != "Looks good" {
		t.Fatalf("calls = %v %+v, the path id must win over the body", b.listed, b.created)
	}
	if rec := testkit.Do(t, h, "POST", "/api/comments/c1/resolve", `{"resolved":true}`, "s"); rec.Code != http.StatusOK || !b.resolved {
		t.Fatalf("resolve = %d", rec.Code)
	}
}
