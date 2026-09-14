package spaces

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// serve builds the API handler over b with this module registered.
func serve(t *testing.T, b core.Backend) http.Handler {
	t.Helper()
	env := testkit.Env(b)
	return testkit.Serve(t, env, New(env))
}

func TestExportSpaceHeadersAndBody(t *testing.T) {
	fake := &testkit.Fake{
		GetSpaceFn: func(context.Context, *core.Actor, string) (*core.Space, error) {
			return &core.Space{ID: "sp1", Slug: "handbook"}, nil
		},
		ExportSpaceFn: func(_ context.Context, _ *core.Actor, spaceID string, w io.Writer) error {
			if spaceID != "sp1" {
				t.Errorf("space id = %q, want sp1", spaceID)
			}
			_, err := w.Write([]byte("PK\x03\x04zip"))
			return err
		},
	}
	rec := testkit.Do(t, serve(t, fake), "GET", "/api/spaces/handbook/export", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="handbook.zip"` {
		t.Errorf("content disposition = %q", cd)
	}
	if rec.Body.String() != "PK\x03\x04zip" {
		t.Errorf("body = %q", rec.Body.String())
	}
}
