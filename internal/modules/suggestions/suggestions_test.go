package suggestions

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// fakeBackend records suggestion calls. Every other method panics.
type fakeBackend struct {
	core.Backend
	mu       sync.Mutex
	statuses []string
	query    core.SuggestionQuery
	input    core.SuggestionInput
	editIDs  []string
	err      error
}

func (b *fakeBackend) set(f func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f()
}

func (b *fakeBackend) ListNoteSuggestions(_ context.Context, _ *core.Actor, noteID string, statuses []string) ([]*core.Suggestion, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.statuses = statuses
	if b.err != nil {
		return nil, b.err
	}
	return nil, nil
}

func (b *fakeBackend) CreateSuggestion(_ context.Context, _ *core.Actor, in core.SuggestionInput) (*core.Suggestion, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.input = in
	if b.err != nil {
		return nil, b.err
	}
	return &core.Suggestion{ID: "s1", NoteID: in.NoteID, Title: in.Title}, nil
}

func (b *fakeBackend) SuggestionInbox(_ context.Context, _ *core.Actor, q core.SuggestionQuery) ([]*core.Suggestion, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.query = q
	return []*core.Suggestion{{ID: "s1"}}, b.err
}

func (b *fakeBackend) GetSuggestion(_ context.Context, _ *core.Actor, id string) (*core.Suggestion, error) {
	if id != "s1" {
		return nil, core.ErrNotFound
	}
	return &core.Suggestion{ID: id}, nil
}

func (b *fakeBackend) AcceptSuggestion(_ context.Context, _ *core.Actor, id string, editIDs []string) (*core.SuggestionResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.editIDs = editIDs
	if b.err != nil {
		return nil, b.err
	}
	return &core.SuggestionResult{Suggestion: &core.Suggestion{ID: id, Status: "accepted"}}, nil
}

func (b *fakeBackend) DiscardSuggestion(_ context.Context, _ *core.Actor, id string, editIDs []string) (*core.Suggestion, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.editIDs = editIDs
	return &core.Suggestion{ID: id, Status: "discarded"}, b.err
}

func (b *fakeBackend) WithdrawSuggestion(_ context.Context, _ *core.Actor, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

func newServer(t *testing.T, b *fakeBackend) (*httptest.Server, *module.Registry) {
	t.Helper()
	reg := module.NewRegistry()
	if err := reg.Add(New(b, slog.New(slog.NewTextHandler(io.Discard, nil)))); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	wrap := func(_ string, h http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h(w, r.WithContext(api.WithActor(r.Context(), &core.Actor{User: &core.User{ID: "u1"}})))
		})
	}
	if err := reg.Mount(mux, wrap); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, reg
}

func call(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = bytes.NewBufferString(body)
	}
	req, _ := http.NewRequest(method, srv.URL+path, r)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 && json.Unmarshal(raw, &out) != nil {
		t.Fatalf("%s %s: non-JSON body %q", method, path, raw)
	}
	return resp.StatusCode, out
}

func TestSuggestionRoutes(t *testing.T) {
	b := &fakeBackend{}
	srv, reg := newServer(t, b)
	if p, ok := reg.LookupPanel("suggestions"); !ok || p.Builtin != "suggestions" {
		t.Fatalf("suggestions panel = %+v, %v", p, ok)
	}

	code, out := call(t, srv, "GET", "/api/notes/n1/suggestions?status=open,%20stale,", "")
	if code != 200 || out["items"] == nil || len(b.statuses) != 2 || b.statuses[1] != "stale" {
		t.Fatalf("list: %d %v statuses %v", code, out, b.statuses)
	}
	code, out = call(t, srv, "POST", "/api/notes/n1/suggestions",
		`{"title":"T","rationale":"R","edits":[{"anchor":{"exact":"a","start":0,"end":1},"text":"b"}]}`)
	if code != 201 || out["id"] != "s1" || b.input.NoteID != "n1" || len(b.input.Edits) != 1 || b.input.Edits[0].Text != "b" {
		t.Fatalf("create: %d %v %+v", code, out, b.input)
	}
	if code, _ := call(t, srv, "POST", "/api/notes/n1/suggestions", `{"title":"T","authorKind":"ai"}`); code != 400 {
		t.Fatalf("create with an unknown field: %d", code)
	}
	code, _ = call(t, srv, "GET", "/api/suggestions?space=sp&author=me&status=all&limit=7", "")
	if code != 200 || b.query != (core.SuggestionQuery{SpaceID: "sp", AuthorID: "me", Status: "all", Limit: 7}) {
		t.Fatalf("inbox: %d %+v", code, b.query)
	}
	call(t, srv, "GET", "/api/suggestions?limit=abc", "")
	if b.query.Limit != 0 {
		t.Fatalf("bad limit parsed as %d", b.query.Limit)
	}
	if code, _ := call(t, srv, "GET", "/api/suggestions/s1", ""); code != 200 {
		t.Fatalf("get: %d", code)
	}
	if code, _ := call(t, srv, "GET", "/api/suggestions/s2", ""); code != 404 {
		t.Fatalf("get unknown: %d", code)
	}

	code, out = call(t, srv, "POST", "/api/suggestions/s1/accept", "")
	if code != 200 || b.editIDs != nil || out["suggestion"].(map[string]any)["status"] != "accepted" {
		t.Fatalf("accept all: %d %v %v", code, out, b.editIDs)
	}
	if code, _ := call(t, srv, "POST", "/api/suggestions/s1/accept", `{"editIds":["e2"]}`); code != 200 || len(b.editIDs) != 1 {
		t.Fatalf("accept some: %d %v", code, b.editIDs)
	}
	if code, _ := call(t, srv, "POST", "/api/suggestions/s1/discard", `{"editIds":["e1","e3"]}`); code != 200 || len(b.editIDs) != 2 {
		t.Fatalf("discard: %d %v", code, b.editIDs)
	}
	if code, _ := call(t, srv, "POST", "/api/suggestions/s1/accept", `{"edits":[]}`); code != 400 {
		t.Fatalf("accept with a bad body: %d", code)
	}
	if code, _ := call(t, srv, "DELETE", "/api/suggestions/s1", ""); code != 204 {
		t.Fatalf("withdraw: %d", code)
	}

	for err, want := range map[error]int{
		core.ErrNotFound: 404, core.ErrForbidden: 403, core.ErrConflict: 409,
		core.ErrUnauthorized: 401, core.Invalid("edits", "bad"): 400, core.ErrUnsupported: 415,
	} {
		b.set(func() { b.err = err })
		for _, r := range [][3]string{
			{"POST", "/api/suggestions/s1/accept", ""},
			{"POST", "/api/notes/n1/suggestions", `{"title":"x"}`},
			{"DELETE", "/api/suggestions/s1", ""},
		} {
			code, out := call(t, srv, r[0], r[1], r[2])
			if code != want || out["error"] == nil {
				t.Errorf("%v on %s %s: %d %v, want %d", err, r[0], r[1], code, out, want)
			}
		}
	}
}
