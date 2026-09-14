package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// adminBackend answers the settings, stats and reindex calls.
type adminBackend struct {
	*testkit.Fake
	saved *core.Settings
}

func (b *adminBackend) UpdateSettings(_ context.Context, actor *core.Actor, in *core.Settings) (*core.Settings, error) {
	if !actor.IsAdmin() {
		return nil, core.ErrForbidden
	}
	b.saved = in
	return in, nil
}

func (b *adminBackend) Reindex(context.Context, *core.Actor) (int, error) { return 12, nil }

func TestAdminRoutes(t *testing.T) {
	b := &adminBackend{Fake: testkit.SignedIn(true)}
	env := testkit.Env(b)
	h := testkit.Serve(t, env, New(env))

	rec := testkit.Do(t, h, "PATCH", "/api/settings", `{"instanceName":"Harbour"}`, "s")
	if rec.Code != http.StatusOK || b.saved == nil || b.saved.InstanceName != "Harbour" {
		t.Fatalf("settings = %d %s", rec.Code, rec.Body)
	}
	if rec := testkit.Do(t, h, "POST", "/api/reindex", "", "s"); rec.Code != http.StatusOK || rec.Body.String() != "{\"indexed\":12}\n" {
		t.Fatalf("reindex = %d %s", rec.Code, rec.Body)
	}
	// Stats is not overridden, so the stub's ErrUnsupported proves the route
	// reached the backend.
	if rec := testkit.Do(t, h, "GET", "/api/stats", "", "s"); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("stats = %d", rec.Code)
	}

	user := &adminBackend{Fake: testkit.SignedIn(false)}
	uenv := testkit.Env(user)
	uh := testkit.Serve(t, uenv, New(uenv))
	if rec := testkit.Do(t, uh, "PATCH", "/api/settings", `{"instanceName":"x"}`, "s"); rec.Code != http.StatusForbidden {
		t.Fatalf("settings as a user = %d, want 403", rec.Code)
	}
}
