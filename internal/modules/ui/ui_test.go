package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// fakeBackend answers GetItem and SpaceAccess. Every other method panics.
type fakeBackend struct {
	core.Backend
}

var items = map[string]*core.Item{
	"note1": {Kind: core.KindNote, Note: &core.Note{ID: "note1", SpaceID: "sp1"}, Access: core.AccessEdit},
	"note2": {Kind: core.KindNote, Note: &core.Note{ID: "note2", SpaceID: "sp1"}, Access: core.AccessView},
	"note3": {Kind: core.KindNote, Note: &core.Note{ID: "note3", SpaceID: "sp2"}, Access: core.AccessEdit},
	"file1": {Kind: core.KindFile, File: &core.File{ID: "file1", SpaceID: "sp1"}, Access: core.AccessView},
}

func (fakeBackend) GetItem(_ context.Context, _ *core.Actor, id string) (*core.Item, error) {
	it, ok := items[id]
	if !ok {
		return nil, core.ErrNotFound
	}
	cp := *it
	return &cp, nil
}

func (fakeBackend) SpaceAccess(_ context.Context, _ *core.Actor, spaceID string) (*core.Grant, error) {
	switch spaceID {
	case "sp1":
		return &core.Grant{Access: core.AccessEdit}, nil
	case "sp2":
		return &core.Grant{Access: core.AccessEdit}, nil
	case "sp3":
		return &core.Grant{Access: core.AccessView}, nil
	}
	return &core.Grant{Access: core.AccessNone}, nil
}

// availability hides testmod in sp2.
type availability struct{}

func (availability) ModuleAvailable(_ context.Context, moduleID, spaceID string) bool {
	return !(moduleID == "testmod" && spaceID == "sp2")
}

type runner struct {
	mu      sync.Mutex
	payload map[string]any
	req     module.CommandRequest
	resp    module.CommandResponse
}

func (r *runner) RunAction(_ context.Context, moduleID, actionID string, req module.CommandRequest, payload map[string]any) (module.CommandResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if moduleID != "testmod" || actionID != "go" {
		return module.CommandResponse{}, core.ErrNotFound
	}
	r.payload, r.req = payload, req
	return r.resp, nil
}

func node(t *testing.T, raw string) module.Node {
	t.Helper()
	n, err := module.ValidateUI(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("ValidateUI(%s): %v", raw, err)
	}
	return n
}

type stack struct {
	srv    *httptest.Server
	runner *runner
}

func newStack(t *testing.T) *stack {
	t.Helper()
	reg := module.NewRegistry()
	r := &runner{resp: module.CommandResponse{Job: &module.JobRef{ID: "job1"}}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := reg.Add(New(Options{Registry: reg, Backend: fakeBackend{}, Actions: r, Availability: availability{}, Logger: logger})); err != nil {
		t.Fatalf("add ui: %v", err)
	}
	good := node(t, `{"type":"text","value":"hello"}`)
	leak := node(t, `{"type":"link","label":"secret","to":{"itemId":"hidden"}}`)
	visible := node(t, `{"type":"link","label":"fine","to":{"itemId":"note2"}}`)
	test := module.New(module.Manifest{ID: "testmod", Name: "Test"}, func(rg *module.Registry) error {
		rg.Command(module.Command{ID: "testmod.go", Title: "Go", Contexts: []string{"note"}, MinAccess: core.AccessEdit,
			Run: func(_ context.Context, req module.CommandRequest) (module.CommandResponse, error) {
				return module.CommandResponse{Toast: &module.Toast{Message: "ran for " + req.User.ID + " in " + req.Context.SpaceID}}, nil
			}})
		rg.Command(module.Command{ID: "testmod.bad", Title: "Bad", Contexts: []string{"note"},
			Run: func(context.Context, module.CommandRequest) (module.CommandResponse, error) {
				return module.CommandResponse{Toast: &module.Toast{Message: "x"}, Navigate: "/x"}, nil
			}})
		rg.Command(module.Command{ID: "testmod.global", Title: "Global", Contexts: []string{"global"},
			Run: func(context.Context, module.CommandRequest) (module.CommandResponse, error) {
				return module.CommandResponse{Navigate: "/s/x"}, nil
			}})
		rg.Panel(module.Panel{ID: "testmod.panel", Title: "Panel", Icon: "book", Where: "rail", Contexts: []string{"note"},
			Render: func(context.Context, module.CommandRequest) (module.Node, error) { return good, nil }})
		rg.Panel(module.Panel{ID: "testmod.leak", Title: "Leak", Icon: "book", Where: "rail", Contexts: []string{"note"},
			Render: func(context.Context, module.CommandRequest) (module.Node, error) { return leak, nil }})
		rg.Panel(module.Panel{ID: "testmod.linked", Title: "Linked", Icon: "book", Where: "rail", Contexts: []string{"note"},
			Render: func(context.Context, module.CommandRequest) (module.Node, error) { return visible, nil }})
		rg.Importer(module.Importer{ID: "testmod.imp", Title: "Import", Accept: []string{".txt"}, MinAccess: core.AccessEdit,
			Import: func(_ context.Context, req module.ImportRequest) (module.CommandResponse, error) {
				return module.CommandResponse{Job: &module.JobRef{ID: "import-" + req.FileID}}, nil
			}})
		return nil
	})
	if err := reg.Add(test); err != nil {
		t.Fatalf("add test module: %v", err)
	}
	mux := http.NewServeMux()
	wrap := func(_ string, h http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var actor *core.Actor
			switch r.Header.Get("X-Actor") {
			case "person":
				actor = &core.Actor{User: &core.User{ID: "u1", Name: "Uma"}}
			case "admin":
				actor = &core.Actor{User: &core.User{ID: "u2", IsAdmin: true}}
			case "plugin":
				actor = &core.Actor{User: &core.User{ID: "p1"}, Plugin: &core.PluginIdentity{ID: "wiki"}}
			default:
				actor = &core.Actor{}
			}
			h(w, r.WithContext(api.WithActor(r.Context(), actor)))
		})
	}
	if err := reg.Mount(mux, wrap); err != nil {
		t.Fatalf("mount: %v", err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &stack{srv: srv, runner: r}
}

func (s *stack) call(t *testing.T, actor, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, s.srv.URL+path, bytes.NewBufferString(body))
	req.Header.Set("X-Actor", actor)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: non-JSON body %q", method, path, raw)
		}
	}
	if resp.StatusCode >= 300 {
		if _, ok := out["error"]; !ok {
			t.Fatalf("%s %s: status %d without error body: %s", method, path, resp.StatusCode, raw)
		}
	}
	return resp.StatusCode, out
}

func ids(list any) []string {
	var out []string
	for _, v := range list.([]any) {
		out = append(out, v.(map[string]any)["id"].(string))
	}
	return out
}

func has(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

func TestContributions(t *testing.T) {
	s := newStack(t)
	for _, actor := range []string{"anon", "plugin"} {
		if code, _ := s.call(t, actor, "GET", "/api/ui/contributions", ""); code != http.StatusUnauthorized {
			t.Fatalf("%s contributions: %d", actor, code)
		}
	}
	code, out := s.call(t, "person", "GET", "/api/ui/contributions?itemId=note1", "")
	if code != 200 {
		t.Fatalf("contributions: %d %v", code, out)
	}
	cmds, panels := ids(out["commands"]), ids(out["panels"])
	for _, want := range []string{"testmod.go", "testmod.bad", "testmod.global"} {
		if !has(cmds, want) {
			t.Errorf("commands %v lack %s", cmds, want)
		}
	}
	for _, want := range []string{"outline", "comments", "history", "share", "files", "ai", "info", "testmod.panel"} {
		if !has(panels, want) {
			t.Errorf("panels %v lack %s", panels, want)
		}
	}
	if len(ids(out["importers"])) != 1 {
		t.Errorf("importers = %v", out["importers"])
	}
	if out["settings"] != nil {
		t.Errorf("a person gets settings: %v", out["settings"])
	}

	// View access hides the edit command, a folder hides note panels.
	_, out = s.call(t, "person", "GET", "/api/ui/contributions?itemId=note2", "")
	if has(ids(out["commands"]), "testmod.go") {
		t.Error("edit command offered with view access")
	}
	_, out = s.call(t, "person", "GET", "/api/ui/contributions?itemId=file1", "")
	if p := ids(out["panels"]); has(p, "outline") || !has(p, "comments") {
		t.Errorf("file panels = %v", p)
	}
	// The unavailable module is hidden in sp2.
	_, out = s.call(t, "person", "GET", "/api/ui/contributions?itemId=note3", "")
	if c := ids(out["commands"]); len(c) != 0 {
		t.Errorf("unavailable module commands = %v", c)
	}
	// The global context has only global commands.
	_, out = s.call(t, "person", "GET", "/api/ui/contributions", "")
	if c := ids(out["commands"]); len(c) != 1 || c[0] != "testmod.global" {
		t.Errorf("global commands = %v", c)
	}
	if code, _ := s.call(t, "person", "GET", "/api/ui/contributions?itemId=hidden", ""); code != 404 {
		t.Errorf("hidden item contributions: %d", code)
	}
	if code, _ := s.call(t, "person", "GET", "/api/ui/contributions?spaceId=nope", ""); code != 404 {
		t.Errorf("unknown space contributions: %d", code)
	}
	if code, _ := s.call(t, "person", "GET", "/api/ui/contributions?kind=note", ""); code != 400 {
		t.Errorf("kind without item: %d", code)
	}
}

func TestCommandRoute(t *testing.T) {
	s := newStack(t)
	code, out := s.call(t, "person", "POST", "/api/commands/testmod.go", `{"context":{"itemId":"note1","spaceId":"forged"}}`)
	if code != 200 || out["toast"].(map[string]any)["message"] != "ran for u1 in sp1" {
		t.Fatalf("command: %d %v", code, out)
	}
	for _, tc := range []struct {
		actor, path, body string
		want              int
	}{
		{"anon", "/api/commands/testmod.go", `{"context":{"itemId":"note1"}}`, 401},
		{"plugin", "/api/commands/testmod.go", `{"context":{"itemId":"note1"}}`, 401},
		{"person", "/api/commands/testmod.go", `{"context":{"itemId":"note2"}}`, 403},
		{"person", "/api/commands/testmod.go", `{"context":{"itemId":"hidden"}}`, 404},
		{"person", "/api/commands/testmod.go", `{"context":{"itemId":"note3"}}`, 404},
		{"person", "/api/commands/nope", `{"context":{}}`, 404},
		{"person", "/api/commands/testmod.bad", `{"context":{"itemId":"note1"}}`, 503},
		{"person", "/api/commands/testmod.go", `{"context":{"itemId":"note1"},"extra":1}`, 400},
		{"person", "/api/commands/testmod.go", `{"context":{"itemId":"note1","imageFileId":"note2"}}`, 400},
	} {
		if code, out := s.call(t, tc.actor, "POST", tc.path, tc.body); code != tc.want {
			t.Errorf("%s %s %s: %d %v, want %d", tc.actor, tc.path, tc.body, code, out, tc.want)
		}
	}
}

func TestPanelRoute(t *testing.T) {
	s := newStack(t)
	code, out := s.call(t, "person", "POST", "/api/panels/testmod.panel", `{"context":{"itemId":"note2"}}`)
	if code != 200 || out["ui"].(map[string]any)["type"] != "text" {
		t.Fatalf("panel: %d %v", code, out)
	}
	if code, out := s.call(t, "person", "POST", "/api/panels/testmod.linked", `{"context":{"itemId":"note2"}}`); code != 200 {
		t.Fatalf("panel with a visible link: %d %v", code, out)
	}
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/api/panels/outline", `{"context":{"itemId":"note1"}}`, 400},
		{"/api/panels/testmod.leak", `{"context":{"itemId":"note1"}}`, 503},
		{"/api/panels/nope", `{}`, 404},
		{"/api/panels/testmod.panel", `{"context":{"itemId":"note3"}}`, 404},
	} {
		if code, out := s.call(t, "person", "POST", tc.path, tc.body); code != tc.want {
			t.Errorf("%s: %d %v, want %d", tc.path, code, out, tc.want)
		}
	}
}

func TestActionRoute(t *testing.T) {
	s := newStack(t)
	code, out := s.call(t, "person", "POST", "/api/modules/testmod/actions/go",
		`{"context":{"itemId":"note2"},"values":{"q":"x"},"payload":{"n":1}}`)
	if code != 200 || out["job"].(map[string]any)["id"] != "job1" {
		t.Fatalf("action: %d %v", code, out)
	}
	if s.runner.payload["n"] != float64(1) || s.runner.req.Values["q"] != "x" || s.runner.req.User.ID != "u1" || s.runner.req.Context.SpaceID != "sp1" {
		t.Fatalf("runner saw %+v %+v", s.runner.payload, s.runner.req)
	}
	s.runner.resp = module.CommandResponse{UI: &module.Node{Type: "link", Label: "x", To: &module.LinkTarget{ItemID: "hidden"}}}
	if code, _ := s.call(t, "person", "POST", "/api/modules/testmod/actions/go", `{"context":{"itemId":"note2"}}`); code != 503 {
		t.Errorf("action with a hidden link: %d", code)
	}
	s.runner.resp = module.CommandResponse{}
	if code, _ := s.call(t, "person", "POST", "/api/modules/testmod/actions/go", `{}`); code != 503 {
		t.Errorf("empty action response: %d", code)
	}
	for _, tc := range []struct {
		actor, path, body string
		want              int
	}{
		{"anon", "/api/modules/testmod/actions/go", `{}`, 401},
		{"person", "/api/modules/unknown/actions/go", `{}`, 404},
		{"person", "/api/modules/testmod/actions/Bad!", `{}`, 404},
		{"person", "/api/modules/testmod/actions/go", `{"context":{"itemId":"note3"}}`, 404},
		{"person", "/api/modules/testmod/actions/go", `{"context":{"itemId":"hidden"}}`, 404},
	} {
		if code, out := s.call(t, tc.actor, "POST", tc.path, tc.body); code != tc.want {
			t.Errorf("%s %s: %d %v, want %d", tc.actor, tc.path, code, out, tc.want)
		}
	}
}

func TestImportRoute(t *testing.T) {
	s := newStack(t)
	code, out := s.call(t, "person", "POST", "/api/import/testmod.imp", `{"fileId":"file1","spaceId":"sp1"}`)
	if code != 200 || out["job"].(map[string]any)["id"] != "import-file1" {
		t.Fatalf("import: %d %v", code, out)
	}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"fileId":"file1","spaceId":"sp3"}`, 403},
		{`{"fileId":"file1","spaceId":"sp2"}`, 404},
		{`{"fileId":"note1","spaceId":"sp1"}`, 400},
		{`{"fileId":"hidden","spaceId":"sp1"}`, 404},
		{`{"fileId":"file1"}`, 400},
		{`{"fileId":"file1","spaceId":"none"}`, 404},
		{`not json`, 400},
	} {
		if code, out := s.call(t, "person", "POST", "/api/import/testmod.imp", tc.body); code != tc.want {
			t.Errorf("%s: %d %v, want %d", tc.body, code, out, tc.want)
		}
	}
	if code, _ := s.call(t, "person", "POST", "/api/import/nope", `{"fileId":"file1","spaceId":"sp1"}`); code != 404 {
		t.Errorf("unknown importer: %d", code)
	}
	if !strings.HasPrefix(s.srv.URL, "http") {
		t.Fatal("server")
	}
}
