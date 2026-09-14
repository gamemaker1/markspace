package sharing

import (
	"context"
	"net/http"
	"testing"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// sharingBackend records the share and link calls.
type sharingBackend struct {
	*testkit.Fake
	share   core.ShareInput
	update  core.Access
	subtree bool
	link    core.PublicLinkInput
}

func (b *sharingBackend) CreateShare(_ context.Context, _ *core.Actor, in core.ShareInput) (*core.Share, error) {
	b.share = in
	return &core.Share{ID: "s1"}, nil
}

func (b *sharingBackend) UpdateShare(_ context.Context, _ *core.Actor, id string, access core.Access, includeChildren bool) (*core.Share, error) {
	b.update, b.subtree = access, includeChildren
	return &core.Share{ID: id}, nil
}

func (b *sharingBackend) CreatePublicLink(_ context.Context, _ *core.Actor, in core.PublicLinkInput) (*core.PublicLink, error) {
	b.link = in
	return &core.PublicLink{ID: "l1"}, nil
}

func TestSharingRoutes(t *testing.T) {
	b := &sharingBackend{Fake: testkit.SignedIn(false)}
	env := testkit.Env(b)
	h := testkit.Serve(t, env, New(env))

	if rec := testkit.Do(t, h, "POST", "/api/items/fo1/shares", `{"noteId":"x","userId":"u2","access":"view"}`, "s"); rec.Code != http.StatusCreated || b.share.NoteID != "fo1" {
		t.Fatalf("share = %d %+v", rec.Code, b.share)
	}
	if rec := testkit.Do(t, h, "PATCH", "/api/shares/s1", `{"access":"edit","includeChildren":true}`, "s"); rec.Code != http.StatusOK || b.update != core.AccessEdit || !b.subtree {
		t.Fatalf("update = %d %s %v", rec.Code, b.update, b.subtree)
	}
	if rec := testkit.Do(t, h, "POST", "/api/notes/n1/links", `{"noteId":"x"}`, "s"); rec.Code != http.StatusCreated || b.link.NoteID != "n1" {
		t.Fatalf("link = %d %+v", rec.Code, b.link)
	}
	if rec := testkit.Do(t, h, "GET", "/api/shares/mine", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous shares/mine = %d, want 401", rec.Code)
	}
}
