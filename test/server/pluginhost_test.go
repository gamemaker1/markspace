package server

// This file builds the whole stack with the module engine and the plugin
// host, as cmd/markspace wires it, and a test plugin on sdk in an
// httptest server. The plugin tests and the host API tests use it.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/ai"
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/app"
	"github.com/gamemaker1/markspace/internal/config"
	"github.com/gamemaker1/markspace/internal/control"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/markdown"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/modules/system"
	"github.com/gamemaker1/markspace/internal/plugin"
	"github.com/gamemaker1/markspace/internal/store"
	"github.com/gamemaker1/markspace/internal/web"
	"github.com/gamemaker1/markspace/internal/wiring"
	"github.com/gamemaker1/markspace/sdk"
	"github.com/gamemaker1/markspace/sdk/ui"
)

// pluginStack is a running server with the plugin host.
type pluginStack struct {
	t        testing.TB
	cfg      *config.Config
	srv      *httptest.Server
	app      *app.App
	db       *index.DB
	parts    *wiring.Parts
	host     *plugin.Host
	clock    *module.ManualClock
	adminTok string
	adminID  string
	client   *http.Client
}

// newPluginStack builds the stack. The plugin host runs without its loops,
// so a test drives deliveries and health checks, and its clock is manual.
// The job engine and the scheduler share that clock.
func newPluginStack(tb testing.TB) *pluginStack {
	tb.Helper()
	dir, err := os.MkdirTemp("", "markspace-plugins-")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { os.RemoveAll(dir) })
	tb.Setenv("MARKSPACE_DATA_DIR", dir)
	tb.Setenv("MARKSPACE_AI_ENABLED", "false")
	cfg, err := config.Load("")
	if err != nil {
		tb.Fatalf("config: %v", err)
	}
	cfg.WatchFiles = false
	// The listener exists before the host is built, so plugins call back to
	// this server and never to a default address.
	var handler http.Handler = http.NotFoundHandler()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	tb.Cleanup(srv.Close)
	cfg.Plugins.MarkspaceURL = "http://" + srv.Listener.Addr().String()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	if os.Getenv("PLUGIN_TEST_LOG") != "" {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	files, err := store.New(cfg.DataDir, logger)
	if err != nil {
		tb.Fatalf("store: %v", err)
	}
	db, err := index.Open(cfg.DBPath, logger)
	if err != nil {
		tb.Fatalf("index: %v", err)
	}
	tb.Cleanup(func() { db.Close() })

	clock := module.NewManualClock(time.Now())
	parts := wiring.NewEngine(cfg, db, logger, wiring.EngineOptions{Now: clock.Now})
	renderer := markdown.New(logger)
	backend, err := app.New(app.Deps{
		Files: files, DB: db, Renderer: renderer, AI: ai.NewRegistry(logger, cfg.AI.ContextChunks),
		Hasher: hasher{}, Config: cfg, Logger: logger,
		Suggestions: db.Suggestions(), Bus: parts.Bus, Jobs: parts.Jobs, Scheduler: parts.Scheduler,
		ScheduleEvery: time.Hour,
		EventSink:     func(ev core.Event) { parts.Bus.Publish(wiring.ModuleEvent(ev)) },
	})
	if err != nil {
		tb.Fatalf("app: %v", err)
	}
	parts.Bridge(backend)
	authn := &api.Authenticator{Backend: backend, Logger: logger}
	pages := &wiring.PageRenderer{}
	if err := parts.AddBuiltins(wiring.NewEnv(cfg, backend, authn, logger), pages); err != nil {
		tb.Fatalf("modules: %v", err)
	}
	host, err := parts.AddPlugins(cfg, db, backend, pages, logger, wiring.HostOptions{
		NoService: true,
		Adjust: func(o *plugin.Options) {
			o.Now = clock.Now
			o.FetchAllowPrivate = true
		},
	})
	if err != nil {
		tb.Fatalf("plugin host: %v", err)
	}
	if err := parts.AddUI(backend, host, host, logger); err != nil {
		tb.Fatalf("ui module: %v", err)
	}
	if err := parts.Registry.Add(system.New(system.Options{
		Backend: backend, Jobs: index.NewJobStore(db), Plugins: host, DB: db.SQL(),
		Listen: cfg.Listen, DataDir: cfg.DataDir, DBPath: cfg.DBPath,
		Skip: []string{control.TokenFile}, Version: "test", Logger: logger,
	})); err != nil {
		tb.Fatalf("system module: %v", err)
	}
	if err := backend.Start(context.Background()); err != nil {
		tb.Fatalf("start: %v", err)
	}
	tb.Cleanup(func() { backend.Close() })

	apiSrv, err := api.NewServer(api.Options{Logger: logger, Auth: authn, Mount: parts.Mount})
	if err != nil {
		tb.Fatalf("api: %v", err)
	}
	webSrv, err := web.NewServer(web.Options{
		Backend: backend, Logger: logger, Auth: authn, SessionTTL: cfg.SessionTTL, InstanceName: "Test",
		MaxUpload: cfg.MaxUploadBytes(), MarkdownCSS: renderer.CSS(), Mount: parts.Mount,
	})
	if err != nil {
		tb.Fatalf("web: %v", err)
	}
	pages.Set(webSrv)
	mux := http.NewServeMux()
	mux.Handle("/api/", apiSrv.Handler())
	mux.Handle("/", webSrv.Handler())
	handler = api.Chain(mux, api.Recover(logger))
	srv.Start()

	s := &pluginStack{t: tb, cfg: cfg, srv: srv, app: backend, db: db, parts: parts, host: host, clock: clock,
		client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	var admin core.User
	s.call("", "POST", "/api/auth/signup", map[string]string{
		"email": "admin@example.com", "name": "Admin", "password": "correct horse battery",
	}, &admin, 201)
	s.adminID = admin.ID
	s.adminTok = s.tokenFor("admin@example.com", "correct horse battery")
	return s
}

// tokenFor logs in and creates an API token.
func (s *pluginStack) tokenFor(email, password string) string {
	s.t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := s.client.Post(s.srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		s.t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == api.SessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		s.t.Fatalf("login %s: no session cookie, status %d", email, resp.StatusCode)
	}
	req, _ := http.NewRequest("POST", s.srv.URL+"/api/tokens", strings.NewReader(`{"name":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	resp, err = s.client.Do(req)
	if err != nil {
		s.t.Fatalf("token: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Token == "" {
		s.t.Fatalf("token: status %d, %v", resp.StatusCode, err)
	}
	return out.Token
}

// request sends a request and returns the status and body.
func (s *pluginStack) request(token, method, path string, body any, header ...string) (int, []byte, http.Header) {
	s.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			s.t.Fatalf("marshal: %v", err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, s.srv.URL+path, rd)
	if err != nil {
		s.t.Fatalf("request: %v", err)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, resp.Header
}

// call sends a request, requires the status and decodes the body into out.
func (s *pluginStack) call(token, method, path string, body, out any, want int) {
	s.t.Helper()
	status, data, _ := s.request(token, method, path, body)
	if status != want {
		s.t.Fatalf("%s %s: status %d, want %d: %s", method, path, status, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			s.t.Fatalf("%s %s: decode %s: %v", method, path, data, err)
		}
	}
}

// space creates a space as the admin.
func (s *pluginStack) space(name string) *core.Space {
	s.t.Helper()
	var sp core.Space
	s.call(s.adminTok, "POST", "/api/spaces", core.SpaceInput{Name: ptr(name)}, &sp, 201)
	return &sp
}

// note creates a note as the admin.
func (s *pluginStack) note(spaceID, title, content string) *core.Note {
	s.t.Helper()
	var n core.Note
	s.call(s.adminTok, "POST", "/api/notes", core.NoteInput{SpaceID: spaceID, Title: title, Content: content}, &n, 201)
	return &n
}

// testManifest is the manifest of the test plugin.
func testManifest(id string, permissions ...string) sdk.Manifest {
	if len(permissions) == 0 {
		permissions = []string{"items:read", "items:write", "links:read", "search", "comments", "suggestions",
			"jobs", "storage", "ui", "events", "web:fetch"}
	}
	return sdk.Manifest{
		Protocol: 1, ID: id, Name: "Test plugin", Version: "1.0.0", Description: "A plugin for tests.",
		Permissions: permissions,
		Events:      []string{"note.*", "suggestion.*"},
		Jobs:        []sdk.ManifestJob{{Kind: "slow", Title: "Slow job", MaxAttempts: 2}, {Kind: "tick", Title: "Tick"}},
		Schedules:   []sdk.ManifestSchedule{{Job: "tick", Every: "1h"}},
		Commands:    []sdk.ManifestCommand{{ID: "hello", Title: "Say hello", Contexts: []string{"note"}}},
		Panels:      []sdk.ManifestPanel{{ID: "stats", Title: "Stats", Icon: "hash", Where: "rail", Contexts: []string{"note"}}},
		Importers:   []sdk.ManifestImporter{{ID: "text", Title: "Text", Accept: []string{".txt"}}},
		Settings:    &sdk.ManifestSettings{Fields: []ui.Node{{"type": "text", "name": "greeting", "label": "Greeting"}}},
		Secrets:     []sdk.ManifestSecret{{Name: "api_key", Label: "API key"}},
	}
}

// testPlugin is a plugin built on sdk that records what it sees.
type testPlugin struct {
	t   testing.TB
	sdk *sdk.Plugin
	srv *httptest.Server
	dir string

	mu        sync.Mutex
	events    []sdk.Event
	batches   int
	jobs      []string
	lastToken string

	// failEvents answers POST /events with 503 while set.
	failEvents atomic.Bool
	// failHealth answers GET /health with 500 while set.
	failHealth atomic.Bool
	// slowDone is closed by the slow job once it finished.
	slowDone chan struct{}
	// release lets the slow job finish.
	release chan struct{}
}

// newTestPlugin starts the test plugin.
func newTestPlugin(tb testing.TB, man sdk.Manifest) *testPlugin {
	tb.Helper()
	dir, err := os.MkdirTemp("", "markspace-plugin-data-")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { os.RemoveAll(dir) })
	p, err := sdk.New(man)
	if err != nil {
		tb.Fatalf("plugin: %v", err)
	}
	tp := &testPlugin{t: tb, sdk: p, dir: dir, slowDone: make(chan struct{}), release: make(chan struct{})}
	p.DataDir = dir
	p.JobWait = 50 * time.Millisecond
	p.HeartbeatEvery = 20 * time.Millisecond
	p.Getenv = func(string) string { return "" }
	p.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	declared := func(list []string, id string) bool {
		for _, x := range list {
			if x == id {
				return true
			}
		}
		return false
	}
	var cmds, panels, importers, jobs []string
	for _, c := range man.Commands {
		cmds = append(cmds, c.ID)
	}
	for _, x := range man.Panels {
		panels = append(panels, x.ID)
	}
	for _, x := range man.Importers {
		importers = append(importers, x.ID)
	}
	for _, x := range man.Jobs {
		jobs = append(jobs, x.Kind)
	}
	command := func(id string, h sdk.CommandHandler) {
		if declared(cmds, id) {
			p.Command(id, h)
		}
	}
	panel := func(id string, h sdk.PanelHandler) {
		if declared(panels, id) {
			p.Panel(id, h)
		}
	}
	importer := func(id string, h sdk.ImportHandler) {
		if declared(importers, id) {
			p.Importer(id, h)
		}
	}
	job := func(kind string, h sdk.JobHandler) {
		if declared(jobs, kind) {
			p.Job(kind, h)
		}
	}

	p.On("note.*", func(ctx context.Context, b *sdk.EventBatch) error {
		tp.mu.Lock()
		defer tp.mu.Unlock()
		tp.events = append(tp.events, b.Events...)
		tp.batches++
		return nil
	})
	command("hello", func(ctx context.Context, c *sdk.CommandContext) (sdk.Response, error) {
		tp.mu.Lock()
		tp.lastToken = c.Host.Token
		tp.mu.Unlock()
		item, err := c.Host.GetItem(ctx, c.ItemID())
		if err != nil {
			return nil, err
		}
		note, _ := item["note"].(map[string]any)
		return sdk.Toast(fmt.Sprintf("Hello %v from %s", note["title"], c.User.Name), "success"), nil
	})
	panel("stats", func(ctx context.Context, c *sdk.CommandContext) (ui.Node, error) {
		return ui.Stack(
			ui.Section("Note", ui.Text("Words: 3"), ui.LinkItem("Open", c.ItemID())),
			ui.Button("Echo", ui.Act("echo", map[string]any{"value": 42})),
		), nil
	})
	p.Action("echo", func(ctx context.Context, a *sdk.ActionContext) (sdk.Response, error) {
		return sdk.Toast(fmt.Sprintf("echo %v for %s", a.Payload["value"], a.ItemID()), "info"), nil
	})
	importer("text", func(ctx context.Context, c *sdk.ImportContext) (sdk.Response, error) {
		return sdk.Toast("imported "+c.FileID, "info"), nil
	})
	job("slow", func(ctx context.Context, j *sdk.JobContext) (any, error) {
		tp.mu.Lock()
		tp.jobs = append(tp.jobs, "slow:"+j.Job.ID)
		tp.mu.Unlock()
		for i := 1; i <= 3; i++ {
			if err := j.Progress(ctx, i, 3, fmt.Sprintf("step %d", i)); err != nil {
				return nil, err
			}
			if i == 2 {
				select {
				case <-tp.release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
		_ = j.Log(ctx, "info", "slow job finished")
		defer close(tp.slowDone)
		return map[string]any{"steps": 3}, nil
	})
	job("tick", func(ctx context.Context, j *sdk.JobContext) (any, error) {
		tp.mu.Lock()
		tp.jobs = append(tp.jobs, "tick:"+j.Job.ID)
		tp.mu.Unlock()
		return map[string]any{"ticked": true}, nil
	})

	handler := p.Handler()
	tp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/events" && tp.failEvents.Load():
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"busy"}`))
			return
		case r.URL.Path == "/health" && tp.failHealth.Load():
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	tb.Cleanup(tp.srv.Close)
	return tp
}

// eventTypes returns the types of the events received so far.
func (tp *testPlugin) eventTypes() []string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	out := make([]string, 0, len(tp.events))
	for _, e := range tp.events {
		out = append(out, e.Type+":"+e.ItemID)
	}
	return out
}

// install previews and installs a plugin through the admin API.
func (s *pluginStack) install(baseURL string, grants []map[string]string) map[string]any {
	s.t.Helper()
	var preview struct {
		BaseURL string `json:"baseUrl"`
		SHA256  string `json:"sha256"`
	}
	s.call(s.adminTok, "POST", "/api/admin/plugins/preview", map[string]string{"baseUrl": baseURL}, &preview, 200)
	var result map[string]any
	s.call(s.adminTok, "POST", "/api/admin/plugins", map[string]any{
		"baseUrl": preview.BaseURL, "sha256": preview.SHA256, "grants": grants,
		"secrets": map[string]string{"api_key": "k-123"}, "settings": map[string]any{"greeting": "hi"},
	}, &result, 201)
	return result
}

// pluginToken returns the plugin token the test plugin stored at install.
func (tp *testPlugin) pluginToken() string {
	tp.t.Helper()
	c := tp.sdk.Credentials()
	if c.Token == "" {
		tp.t.Fatal("the plugin holds no token")
	}
	return c.Token
}

// waitFor polls cond for up to 10 s.
func waitFor(tb testing.TB, what string, cond func() bool) {
	tb.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	tb.Fatalf("timed out waiting for %s", what)
}

// errBody decodes an error body.
func errBody(data []byte) string {
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(data, &e) != nil {
		return string(data)
	}
	return e.Error + "|" + e.Code
}

var errSkip = errors.New("skip")
