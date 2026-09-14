package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// fakeHost records admin calls. Methods it does not override panic through
// the nil embedded interface, so a test that reaches them fails loudly.
type fakeHost struct {
	Host
	mu        sync.Mutex
	calls     []string
	plugins   []PluginView
	preview   Preview
	install   InstallInput
	result    InstallResult
	config    ConfigInput
	uninstall []string
	err       error
	// configErr and installErr fail only Configure and Install.
	configErr  error
	installErr error
}

func (h *fakeHost) record(format string, args ...any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, fmt.Sprintf(format, args...))
}

func (h *fakeHost) called(want string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if c == want {
			return true
		}
	}
	return false
}

func (h *fakeHost) List(ctx context.Context) ([]PluginView, error) {
	h.record("list")
	return h.plugins, h.err
}

func (h *fakeHost) Get(ctx context.Context, id string) (*PluginView, error) {
	h.record("get %s", id)
	if h.err != nil {
		return nil, h.err
	}
	for _, p := range h.plugins {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("plugin %s: %w", id, core.ErrNotFound)
}

func (h *fakeHost) Preview(ctx context.Context, baseURL string) (*Preview, error) {
	h.record("preview %s", baseURL)
	if h.err != nil {
		return nil, h.err
	}
	p := h.preview
	p.BaseURL = baseURL
	return &p, nil
}

func (h *fakeHost) Install(ctx context.Context, in InstallInput) (*InstallResult, error) {
	h.record("install %s", in.BaseURL)
	h.mu.Lock()
	h.install = in
	h.mu.Unlock()
	if h.installErr != nil {
		return nil, h.installErr
	}
	if h.err != nil {
		return nil, h.err
	}
	res := h.result
	return &res, nil
}

func (h *fakeHost) Configure(ctx context.Context, id string, in ConfigInput) (*PluginView, error) {
	h.record("configure %s", id)
	h.mu.Lock()
	h.config = in
	h.mu.Unlock()
	if h.configErr != nil {
		return nil, h.configErr
	}
	if h.err != nil {
		return nil, h.err
	}
	return &h.plugins[0], nil
}

func (h *fakeHost) SetPaused(ctx context.Context, id string, paused bool) (*PluginView, error) {
	h.record("paused %s %v", id, paused)
	if h.err != nil {
		return nil, h.err
	}
	return &h.plugins[0], nil
}

func (h *fakeHost) CheckManifest(ctx context.Context, id string) (*PluginView, error) {
	h.record("check %s", id)
	if h.err != nil {
		return nil, h.err
	}
	return &h.plugins[0], nil
}

func (h *fakeHost) Approve(ctx context.Context, id string) (*PluginView, error) {
	h.record("approve %s", id)
	if h.err != nil {
		return nil, h.err
	}
	return &h.plugins[0], nil
}

func (h *fakeHost) Uninstall(ctx context.Context, id string, deleteData bool) error {
	h.record("uninstall %s %v", id, deleteData)
	return h.err
}

type fakeBackend struct {
	core.Backend
	spaces []*core.Space
}

func (b *fakeBackend) ListSpaces(ctx context.Context, actor *core.Actor) ([]*core.Space, error) {
	return b.spaces, nil
}

// fakePages writes the page body, and the status of an error.
type fakePages struct{}

func (fakePages) RenderModulePage(w http.ResponseWriter, r *http.Request, spec api.PageSpec) {
	status := spec.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<title>"+spec.Title+"</title><h1>"+spec.Heading+"</h1>"+string(spec.Body)+strings.Join(spec.Scripts, " "))
}

func (fakePages) RenderError(w http.ResponseWriter, r *http.Request, err error) {
	status := api.StatusFor(err)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	http.Error(w, err.Error(), status)
}

var (
	actorAdmin  = &core.Actor{User: &core.User{ID: "u-admin", Name: "Ada", IsAdmin: true, IsActive: true}}
	actorPerson = &core.Actor{User: &core.User{ID: "u-bob", Name: "Bob", IsActive: true}}
	actorPlugin = &core.Actor{User: &core.User{ID: "u-plugin", IsAdmin: true, IsActive: true},
		Plugin: &core.PluginIdentity{ID: "word-count", UserID: "u-plugin", Status: core.PluginStatusActive}}
)

const settingsSchema = `[{"type":"space","name":"space","label":"Wiki space","required":true},
 {"type":"number","name":"limit","label":"Limit","value":5},
 {"type":"checkbox","name":"daily","label":"Run daily"},
 {"type":"select","name":"mode","label":"Mode","options":[{"value":"a","label":"A"},{"value":"b","label":"B"}]},
 {"type":"file","name":"skip","label":"Skipped"}]`

func newAdminServer(t *testing.T) (*httptest.Server, *fakeHost) {
	t.Helper()
	summary := ManifestSummary{
		ID: "word-count", Name: "Word count", Version: "0.1.0", Description: "Counts words.",
		Permissions: []string{"items:read", "storage"}, Events: []string{"note.updated"},
		Jobs:      []Contribution{{ID: "count-space", Title: "Count words in a space"}},
		Schedules: []Contribution{{ID: "count-space", Extra: "24h"}},
		Panels: []Contribution{{ID: "stats", Title: "Word statistics", Extra: "page"}, {ID: "side", Title: "Side", Extra: "rail"},
			{ID: "home", Title: "Word count", Extra: "page"}},
		Secrets:  []SecretView{{Name: "api_key", Label: "API key"}},
		Settings: json.RawMessage(settingsSchema),
	}
	host := &fakeHost{
		plugins: []PluginView{{
			ID: "word-count", Name: "Word count", Version: "0.1.0", BaseURL: "http://word-count:8090",
			Status: core.PluginStatusActive, Approved: summary,
			Grants:   []Grant{{SpaceID: "s1", SpaceName: "Docs", Role: core.RoleEditor}},
			Settings: json.RawMessage(`{"limit":7,"daily":true}`), Secrets: []SecretView{{Name: "api_key", Label: "API key", Set: true}},
			QueuedDeliveries: 3, AITokensMonth: 12345, AICostMicrosMonth: 1_250_000,
		}},
		preview: Preview{Manifest: summary, SHA256: "abc123"},
		result:  InstallResult{Plugin: PluginView{ID: "word-count", Name: "Word count"}, Delivered: true},
	}
	backend := &fakeBackend{spaces: []*core.Space{{ID: "s1", Name: "Docs"}, {ID: "s2", Name: "Team"}}}
	reg := module.NewRegistry()
	if err := reg.Add(New(Options{Host: host, Backend: backend, Pages: fakePages{}})); err != nil {
		t.Fatalf("add module: %v", err)
	}
	mux := http.NewServeMux()
	if err := reg.Mount(mux, nil); err != nil {
		t.Fatalf("mount: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var actor *core.Actor
		switch r.Header.Get("X-Test-Actor") {
		case "admin":
			actor = actorAdmin
		case "person":
			actor = actorPerson
		case "plugin":
			actor = actorPlugin
		default:
			actor = &core.Actor{}
		}
		mux.ServeHTTP(w, r.WithContext(api.WithActor(r.Context(), actor)))
	}))
	t.Cleanup(srv.Close)
	return srv, host
}

type result struct {
	status   int
	body     string
	location string
}

func call(t *testing.T, srv *httptest.Server, who, method, path, contentType, body string) result {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Actor", who)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return result{status: resp.StatusCode, body: string(b), location: resp.Header.Get("Location")}
}

func form(t *testing.T, srv *httptest.Server, who string, v url.Values) result {
	t.Helper()
	return call(t, srv, who, "POST", "/admin/plugins", "application/x-www-form-urlencoded", v.Encode())
}

func TestAdminAPIAccess(t *testing.T) {
	srv, host := newAdminServer(t)
	routes := []struct {
		method, path, body string
		want               int
		call               string
	}{
		{"GET", "/api/admin/plugins", "", 200, "list"},
		{"GET", "/api/admin/plugins/word-count", "", 200, "get word-count"},
		{"POST", "/api/admin/plugins/preview", `{"baseUrl":"http://word-count:8090"}`, 200, "preview http://word-count:8090"},
		{"POST", "/api/admin/plugins", `{"baseUrl":"http://word-count:8090","sha256":"abc123","grants":[{"spaceId":"s1","role":"editor"}]}`, 201, "install http://word-count:8090"},
		{"PATCH", "/api/admin/plugins/word-count", `{"settings":{"limit":3}}`, 200, "configure word-count"},
		{"POST", "/api/admin/plugins/word-count/pause", "", 200, "paused word-count true"},
		{"POST", "/api/admin/plugins/word-count/resume", "", 200, "paused word-count false"},
		{"POST", "/api/admin/plugins/word-count/check", "", 200, "check word-count"},
		{"POST", "/api/admin/plugins/word-count/approve", "", 200, "approve word-count"},
		{"POST", "/api/admin/plugins/word-count/uninstall", `{"deleteData":true}`, 204, "uninstall word-count true"},
	}
	for _, rt := range routes {
		for who, want := range map[string]int{"anon": 401, "person": 403, "plugin": 401} {
			res := call(t, srv, who, rt.method, rt.path, "application/json", rt.body)
			if res.status != want {
				t.Errorf("%s %s as %s: status %d, want %d (%s)", rt.method, rt.path, who, res.status, want, res.body)
			}
			var e struct {
				Error string `json:"error"`
			}
			if json.Unmarshal([]byte(res.body), &e) != nil || e.Error == "" {
				t.Errorf("%s %s as %s: no JSON error body: %q", rt.method, rt.path, who, res.body)
			}
		}
		if host.called(rt.call) {
			t.Fatalf("%s %s reached the host before an admin called it", rt.method, rt.path)
		}
		res := call(t, srv, "admin", rt.method, rt.path, "application/json", rt.body)
		if res.status != rt.want {
			t.Errorf("%s %s as admin: status %d, want %d (%s)", rt.method, rt.path, res.status, rt.want, res.body)
		}
		if !host.called(rt.call) {
			t.Errorf("%s %s as admin: host call %q missing, got %v", rt.method, rt.path, rt.call, host.calls)
		}
	}
	if host.install.SHA256 != "abc123" || len(host.install.Grants) != 1 || host.install.Grants[0].Role != core.RoleEditor {
		t.Errorf("install input = %+v", host.install)
	}
	var list pluginList
	res := call(t, srv, "admin", "GET", "/api/admin/plugins", "", "")
	if err := json.Unmarshal([]byte(res.body), &list); err != nil || len(list.Items) != 1 || list.Items[0].ID != "word-count" {
		t.Errorf("list body = %s (%v)", res.body, err)
	}
}

func TestAdminAPIErrors(t *testing.T) {
	srv, host := newAdminServer(t)
	cases := []struct {
		name, method, path, body string
		err                      error
		want                     int
	}{
		{"unknown plugin", "GET", "/api/admin/plugins/nope", "", nil, 404},
		{"missing base URL", "POST", "/api/admin/plugins/preview", `{"baseUrl":" "}`, nil, 400},
		{"unknown field", "POST", "/api/admin/plugins/preview", `{"url":"x"}`, nil, 400},
		{"install refused", "POST", "/api/admin/plugins", `{"baseUrl":"http://x"}`, core.Invalid("grants", "bad role"), 400},
		{"manifest changed", "POST", "/api/admin/plugins", `{"baseUrl":"http://x","sha256":"old"}`, fmt.Errorf("manifest changed: %w", core.ErrConflict), 409},
		{"host down", "POST", "/api/admin/plugins/word-count/check", "", fmt.Errorf("fetch: %w", core.ErrUnavailable), 503},
		{"host failure", "POST", "/api/admin/plugins/word-count/pause", "", fmt.Errorf("disk on fire"), 500},
	}
	for _, c := range cases {
		host.err = c.err
		res := call(t, srv, "admin", c.method, c.path, "application/json", c.body)
		if res.status != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, res.status, c.want, res.body)
		}
		if c.want == 500 && strings.Contains(res.body, "disk on fire") {
			t.Errorf("%s: internal error text leaked: %s", c.name, res.body)
		}
	}
}

func TestAdminPageRenders(t *testing.T) {
	srv, _ := newAdminServer(t)
	res := call(t, srv, "admin", "GET", "/admin/plugins", "", "")
	if res.status != 200 {
		t.Fatalf("status %d: %s", res.status, res.body)
	}
	for _, want := range []string{
		"Word count", "http://word-count:8090", `class="ui-label ui-label-success">Up<`, "Docs (Editor)", ">3<", "12,345 tokens, $1.25",
		`name="setting.limit" value="7"`, `name="setting.daily" value="1" checked`, `name="grant.s2"`,
		"Check for manifest changes", "/jobs?module=plugin.word-count", `aria-label="Actions: Word count"`,
		`data-dialog-open="plugin-settings-word-count"`, `data-dialog-open="plugin-secrets-word-count"`,
		`data-plugin-uninstall="plugin-uninstall-word-count"`, `value="delete"`, `role="region" tabindex="0"`,
		"Add plugin", `name="baseUrl"`, `name="secret.api_key"`, "A value is stored.", "/modules/plugins/admin.js",
	} {
		if !strings.Contains(res.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, unwanted := range []string{"setting.skip", "data-dialog-autoopen", "data-two-step", " title=", " disabled"} {
		if strings.Contains(res.body, unwanted) {
			t.Errorf("page contains %q", unwanted)
		}
	}
	if got := call(t, srv, "person", "GET", "/admin/plugins", "", ""); got.status != 403 {
		t.Errorf("person: status %d", got.status)
	}
	if got := call(t, srv, "anon", "GET", "/admin/plugins", "", ""); got.status != 401 {
		t.Errorf("anonymous: status %d", got.status)
	}
	if got := form(t, srv, "person", url.Values{"action": {"pause"}, "id": {"word-count"}}); got.status != 403 {
		t.Errorf("person post: status %d", got.status)
	}
	if got := call(t, srv, "anon", "GET", "/modules/plugins/admin.js", "", ""); got.status != 200 || !strings.Contains(got.body, "data-plugins-page") {
		t.Errorf("script: status %d", got.status)
	}
}

func TestAdminPageInstallSteps(t *testing.T) {
	srv, host := newAdminServer(t)
	res := form(t, srv, "admin", url.Values{"action": {"preview"}, "baseUrl": {""}})
	if res.status != 400 || !strings.Contains(res.body, "Enter the address") {
		t.Errorf("empty URL: %d %s", res.status, res.body)
	}

	res = form(t, srv, "admin", url.Values{"action": {"preview"}, "baseUrl": {"http://word-count:8090"}})
	for _, want := range []string{"Word count 0.1.0", "Counts words.", "items:read", permissionText["storage"], "note.updated",
		"Count words in a space", "every 24h", `value="continue"`, `name="sha256" value="abc123"`, "Step 2 of 4"} {
		if !strings.Contains(res.body, want) {
			t.Errorf("review lacks %q", want)
		}
	}
	if tag := openTag(res.body, "add-plugin"); !strings.Contains(tag, "data-dialog-autoopen") || !strings.Contains(tag, `data-add-step="2"`) {
		t.Errorf("review does not open in its Dialog: %s", tag)
	}

	back := form(t, srv, "admin", url.Values{"action": {"back"}, "baseUrl": {"http://word-count:8090"}})
	if !strings.Contains(back.body, `value="http://word-count:8090"`) || !strings.Contains(back.body, `value="preview"`) {
		t.Error("back does not return to the address step with the URL")
	}

	res = form(t, srv, "admin", url.Values{"action": {"continue"}, "baseUrl": {"http://word-count:8090"}, "sha256": {"abc123"}})
	for _, want := range []string{`name="grant.s1"`, `name="grant.s2"`, `name="secret.api_key"`, `name="setting.space"`,
		`<option value="s2">Team</option>`, `value="confirm"`, "Step 3 of 4"} {
		if !strings.Contains(res.body, want) {
			t.Errorf("step 3 lacks %q", want)
		}
	}

	choices := url.Values{"baseUrl": {"http://word-count:8090"}, "sha256": {"abc123"},
		"grant.s1": {"editor"}, "grant.s2": {""}, "secret.api_key": {"k3y"},
		"setting.space": {"s1"}, "setting.limit": {"12"}, "present.daily": {"1"}, "setting.mode": {"b"}}
	step4 := url.Values{"action": {"confirm"}}
	for k, v := range choices {
		step4[k] = v
	}
	res = form(t, srv, "admin", step4)
	for _, want := range []string{"Step 4 of 4", `type="hidden" name="grant.s1" value="editor"`, `type="hidden" name="secret.api_key" value="k3y"`,
		"Docs (Editor)", "Provided", "<dt>Wiki space</dt><dd>Docs</dd>", "<dt>Run daily</dt><dd>Off</dd>", `value="install"`, `value="choices"`} {
		if !strings.Contains(res.body, want) {
			t.Errorf("step 4 lacks %q", want)
		}
	}
	back4 := url.Values{"action": {"choices"}}
	for k, v := range choices {
		back4[k] = v
	}
	res = form(t, srv, "admin", back4)
	if !strings.Contains(res.body, "Step 3 of 4") || !strings.Contains(res.body, `<option value="editor" selected>`) || !strings.Contains(res.body, `value="k3y"`) {
		t.Error("back from step 4 does not keep the choices")
	}

	res = form(t, srv, "admin", url.Values{"action": {"continue"}, "baseUrl": {"http://word-count:8090"}, "sha256": {"stale"}})
	if !strings.Contains(res.body, "manifest changed") || !strings.Contains(res.body, `value="continue"`) {
		t.Error("a changed manifest does not return to the review")
	}

	res = form(t, srv, "admin", url.Values{
		"action": {"install"}, "baseUrl": {"http://word-count:8090"}, "sha256": {"abc123"},
		"grant.s1": {"editor"}, "grant.s2": {"owner"}, "secret.api_key": {"k3y"},
		"setting.space": {"s1"}, "setting.limit": {"12"}, "present.daily": {"1"}, "setting.mode": {"b"},
	})
	if res.status != 303 || !strings.Contains(res.location, "announce=") {
		t.Fatalf("install: %d %s", res.status, res.location)
	}
	in := host.install
	if in.SHA256 != "abc123" || len(in.Grants) != 1 || in.Grants[0].SpaceID != "s1" || in.Grants[0].Role != core.RoleEditor {
		t.Errorf("grants = %+v", in.Grants)
	}
	if in.Secrets["api_key"] != "k3y" {
		t.Errorf("secrets = %v", in.Secrets)
	}
	if in.Settings["limit"] != 12.0 || in.Settings["daily"] != false || in.Settings["space"] != "s1" || in.Settings["mode"] != "b" {
		t.Errorf("settings = %v", in.Settings)
	}
	if _, ok := in.Settings["skip"]; ok {
		t.Error("file setting sent")
	}

	host.result = InstallResult{Plugin: PluginView{ID: "word-count", Name: "Word count"}, InstallError: "connection refused",
		Token: "mspl_secret", SigningSecret: "c2VjcmV0", MarkspaceURL: "http://markspace:8080"}
	res = form(t, srv, "admin", url.Values{"action": {"install"}, "baseUrl": {"http://word-count:8090"}, "sha256": {"abc123"}})
	for _, want := range []string{"mspl_secret", "c2VjcmV0", "http://markspace:8080", "MARKSPACE_SIGNING_SECRET", "connection refused",
		`data-copy="mspl_secret"`, `aria-label="Copy MARKSPACE_TOKEN"`, "Copy your plugin credentials"} {
		if !strings.Contains(res.body, want) {
			t.Errorf("credentials lack %q", want)
		}
	}
	if tag := openTag(res.body, "plugin-credentials"); !strings.Contains(tag, "data-dialog-autoopen") {
		t.Errorf("credentials do not open in a Dialog: %s", tag)
	}
	if res.status != 201 {
		t.Errorf("credentials status %d", res.status)
	}

	host.installErr = core.Invalid("grants", "choose at least one space")
	res = form(t, srv, "admin", url.Values{"action": {"install"}, "baseUrl": {"http://word-count:8090"}, "sha256": {"abc123"}, "grant.s2": {"viewer"}})
	host.installErr = nil
	if !strings.Contains(res.body, "Choose at least one space.") || !strings.Contains(res.body, "data-dialog-error") || !strings.Contains(res.body, "Step 3 of 4") {
		t.Error("an install error is not shown in the step 3 Dialog")
	}
	if !strings.Contains(res.body, `<option value="viewer" selected>`) {
		t.Error("a refused install loses the chosen spaces")
	}
}

// openTag returns the opening tag of the element with the id.
func openTag(body, id string) string {
	i := strings.Index(body, `id="`+id+`"`)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(body[:i], "<")
	end := strings.Index(body[i:], ">")
	return body[start : i+end+1]
}

func TestAdminPageRowActions(t *testing.T) {
	srv, host := newAdminServer(t)
	for action, key := range map[string]string{"pause": "announce=", "resume": "announce=", "check": "notice=", "approve": "announce="} {
		res := form(t, srv, "admin", url.Values{"action": {action}, "id": {"word-count"}})
		if res.status != 303 || !strings.Contains(res.location, key) {
			t.Errorf("%s: %d %s", action, res.status, res.location)
		}
	}
	for _, want := range []string{"paused word-count true", "paused word-count false", "check word-count", "approve word-count"} {
		if !host.called(want) {
			t.Errorf("missing call %q", want)
		}
	}
	host.err = fmt.Errorf("unreachable: %w", core.ErrUnavailable)
	if res := form(t, srv, "admin", url.Values{"action": {"check"}, "id": {"word-count"}}); !strings.Contains(res.location, "problem=") {
		t.Errorf("error redirect %s", res.location)
	}
	host.err = nil

	res := form(t, srv, "admin", url.Values{"action": {"settings"}, "id": {"word-count"}, "grant.s1": {"viewer"}, "grant.s2": {""},
		"setting.limit": {"4"}, "present.daily": {"1"}, "setting.daily": {"1"}})
	if res.status != 303 {
		t.Fatalf("settings: %d", res.status)
	}
	if g := host.config.Grants; g == nil || len(*g) != 1 || (*g)[0].Role != core.RoleViewer {
		t.Errorf("config grants = %+v", g)
	}
	if host.config.Settings["limit"] != 4.0 || host.config.Settings["daily"] != true {
		t.Errorf("config settings = %v", host.config.Settings)
	}

	form(t, srv, "admin", url.Values{"action": {"secrets"}, "id": {"word-count"}, "secret.api_key": {""}})
	if len(host.config.Secrets) != 0 {
		t.Errorf("an empty secret was sent: %v", host.config.Secrets)
	}
	form(t, srv, "admin", url.Values{"action": {"secrets"}, "id": {"word-count"}, "secret.api_key": {"new"}})
	if host.config.Secrets["api_key"] != "new" {
		t.Errorf("secrets = %v", host.config.Secrets)
	}

	open := call(t, srv, "admin", "GET", "/admin/plugins?open=word-count&panel=secrets", "", "")
	if !strings.Contains(openTag(open.body, "plugin-secrets-word-count"), "data-dialog-autoopen") ||
		!strings.Contains(openTag(open.body, "plugin-settings-word-count"), " hidden") {
		t.Error("the open panel parameter does not open exactly the secrets Dialog")
	}

	ask := form(t, srv, "admin", url.Values{"action": {"uninstall-ask"}, "id": {"word-count"}})
	if ask.status != 303 || !strings.Contains(ask.location, "confirm=word-count") {
		t.Fatalf("uninstall-ask: %d %s", ask.status, ask.location)
	}
	if host.called("uninstall word-count false") || host.called("uninstall word-count true") {
		t.Fatal("the first step uninstalled the plugin")
	}
	confirm := call(t, srv, "admin", "GET", ask.location, "", "")
	if tag := openTag(confirm.body, "plugin-uninstall-word-count"); !strings.Contains(tag, "data-dialog-autoopen") ||
		!strings.Contains(tag, `data-dialog-title="Uninstall Word count?"`) || !strings.Contains(confirm.body, `value="uninstall">Uninstall</button>`) {
		t.Errorf("the confirmation does not open: %s", tag)
	}
	if res := form(t, srv, "admin", url.Values{"action": {"uninstall"}, "id": {"word-count"}, "data": {"keep"}}); res.status != 303 {
		t.Errorf("uninstall keep: %d", res.status)
	}
	if !host.called("uninstall word-count false") {
		t.Error("keep did not uninstall with deleteData false")
	}
	form(t, srv, "admin", url.Values{"action": {"uninstall"}, "id": {"word-count"}, "data": {"delete"}})
	if !host.called("uninstall word-count true") {
		t.Error("delete did not uninstall with deleteData true")
	}
	host.err = fmt.Errorf("busy: %w", core.ErrConflict)
	if res := form(t, srv, "admin", url.Values{"action": {"uninstall"}, "id": {"word-count"}, "data": {"keep"}}); !strings.Contains(res.location, "panel=uninstall") {
		t.Errorf("a failed uninstall does not reopen its Dialog: %s", res.location)
	}
	host.err = nil
}

func TestAdminPageSettingsFailureReopensDialog(t *testing.T) {
	srv, host := newAdminServer(t)
	host.configErr = core.Invalid("limit", "enter a number of 1 or more")
	res := form(t, srv, "admin", url.Values{"action": {"settings"}, "id": {"word-count"}, "setting.limit": {"0"}})
	host.configErr = nil
	if res.status != 303 || !strings.Contains(res.location, "open=word-count") || !strings.Contains(res.location, "panel=settings") {
		t.Fatalf("settings failure: %d %s", res.status, res.location)
	}
	page := call(t, srv, "admin", "GET", res.location, "", "")
	if tag := openTag(page.body, "plugin-settings-word-count"); !strings.Contains(tag, "data-dialog-autoopen") {
		t.Errorf("the settings Dialog does not reopen: %s", tag)
	}
	i := strings.Index(page.body, `id="plugin-settings-word-count"`)
	if i < 0 || !strings.Contains(page.body[i:], "data-dialog-error") || !strings.Contains(page.body[i:], "Enter a number of 1 or more.") {
		t.Error("the settings Dialog does not show the error")
	}
	if strings.Contains(page.body, "data-plugin-banner") {
		t.Error("the error also shows above the page")
	}
}

func TestUsageText(t *testing.T) {
	for _, c := range []struct {
		tokens, micros int64
		want           string
	}{
		{0, 0, "None"}, {1500, 0, "1,500 tokens, $0.00"}, {10, 5000, "10 tokens, under $0.01"}, {1234567, 20_000_000, "1,234,567 tokens, $20.00"},
	} {
		if got := usageText(c.tokens, c.micros); got != c.want {
			t.Errorf("usageText(%d, %d) = %q, want %q", c.tokens, c.micros, got, c.want)
		}
	}
}

func TestPluginPageRoute(t *testing.T) {
	srv, _ := newAdminServer(t)
	cases := []struct{ panel, title, heading string }{
		{"word-count.stats", "Word statistics", "Word count"},
		{"word-count.home", "", "Word count"},
		{"word-count.side", "", ""},
		{"word-count.missing", "", ""},
		{"other.stats", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		res := call(t, srv, "person", "GET", "/plugins/page?panel="+url.QueryEscape(c.panel)+"&spaceId=s1", "", "")
		want := "<title>" + c.title + "</title><h1>" + c.heading + "</h1>"
		if res.status != 200 || !strings.HasPrefix(res.body, want) {
			t.Errorf("panel %q: %d %q, want prefix %q", c.panel, res.status, res.body, want)
		}
	}
	if res := call(t, srv, "anon", "GET", "/plugins/page?panel=word-count.stats", "", ""); res.status != 401 {
		t.Errorf("anonymous: status %d", res.status)
	}
	admin := call(t, srv, "admin", "GET", "/admin/plugins", "", "")
	if !strings.Contains(admin.body, `href="/plugins/page?panel=word-count.stats">Word statistics</a>`) ||
		!strings.Contains(admin.body, `href="/plugins/page?panel=word-count.home">Word count</a>`) ||
		strings.Contains(admin.body, "panel=word-count.side") {
		t.Error("the row menu does not link exactly the page panels")
	}
}

func (fakePages) Octicon(name string, size ...int) (template.HTML, error) {
	return template.HTML(`<svg class="octicon octicon-` + template.HTMLEscapeString(name) + `"></svg>`), nil
}
