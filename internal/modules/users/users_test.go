package users

import (
	"context"
	"net/http"
	"testing"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// usersBackend records the arguments of the user calls.
type usersBackend struct {
	*testkit.Fake
	query         string
	limit, offset int
	deleted       string
}

func (b *usersBackend) ListUsers(_ context.Context, _ *core.Actor, q string, limit, offset int) ([]*core.User, int, error) {
	b.query, b.limit, b.offset = q, limit, offset
	return []*core.User{{ID: "u2"}}, 7, nil
}

func (b *usersBackend) LookupUsers(_ context.Context, _ *core.Actor, q string, limit int) ([]*core.User, error) {
	b.query, b.limit = q, limit
	return nil, nil
}

func (b *usersBackend) DeleteUser(_ context.Context, _ *core.Actor, id string) error {
	b.deleted = id
	return nil
}

func TestUserRoutes(t *testing.T) {
	b := &usersBackend{Fake: testkit.SignedIn(true)}
	env := testkit.Env(b)
	h := testkit.Serve(t, env, New(env))

	rec := testkit.Do(t, h, "GET", "/api/users?q=ann&limit=500&offset=3", "", "s")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	if b.query != "ann" || b.limit != 200 || b.offset != 3 {
		t.Fatalf("list args = %q %d %d, want the 200 cap", b.query, b.limit, b.offset)
	}
	var env2 struct {
		Items []core.User `json:"items"`
		Total *int        `json:"total"`
	}
	testkit.DecodeBody(t, rec, &env2)
	if len(env2.Items) != 1 || env2.Total == nil || *env2.Total != 7 {
		t.Fatalf("list envelope = %s", rec.Body)
	}

	if rec := testkit.Do(t, h, "GET", "/api/users/lookup?q=bo", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous lookup = %d, want 401", rec.Code)
	}
	if rec := testkit.Do(t, h, "GET", "/api/users/lookup?q=bo&limit=99", "", "s"); rec.Code != http.StatusOK || b.limit != 50 {
		t.Fatalf("lookup = %d limit %d", rec.Code, b.limit)
	}
	if rec := testkit.Do(t, h, "DELETE", "/api/users/u9", "", "s"); rec.Code != http.StatusNoContent || b.deleted != "u9" {
		t.Fatalf("delete = %d %q", rec.Code, b.deleted)
	}
	if rec := testkit.Do(t, h, "POST", "/api/invites", `{"email":"x@y.test","bogus":1}`, "s"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invite with an unknown field = %d", rec.Code)
	}
}
