package sdk

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/sdk/ui"
)

type recorded struct {
	Method, Path, Auth string
	Body               map[string]any
}

type fakeHost struct {
	srv      *httptest.Server
	mu       sync.Mutex
	reqs     []recorded
	canceled atomic.Bool
}

func newFakeHost(t *testing.T) *fakeHost {
	fh := &fakeHost{}
	fh.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		fh.mu.Lock()
		fh.reqs = append(fh.reqs, recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), body})
		fh.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/plugin/v1/items/"):
			w.Write([]byte(`{"kind":"note","note":{"id":"n1","content":"one two three"}}`))
		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/api/plugin/v1/jobs/"):
			if fh.canceled.Load() {
				w.Write([]byte(`{"status":"canceled"}`))
				return
			}
			w.Write([]byte(`{"id":"j1","kind":"count-space","status":"running"}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(fh.srv.Close)
	return fh
}

func (fh *fakeHost) requests() []recorded {
	fh.mu.Lock()
	defer fh.mu.Unlock()
	return append([]recorded(nil), fh.reqs...)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var testSecret = bytes.Repeat([]byte{7}, 32)

type harness struct {
	t      *testing.T
	p      *Plugin
	h      http.Handler
	clock  *clock
	env    map[string]string
	host   *fakeHost
	nextID atomic.Int64
}

func newHarness(t *testing.T, env map[string]string, setup func(p *Plugin)) *harness {
	t.Helper()
	p, err := New(testManifest())
	if err != nil {
		t.Fatal(err)
	}
	hs := &harness{t: t, p: p, clock: &clock{t: time.Unix(1789300000, 0)}, env: env, host: newFakeHost(t)}
	p.DataDir = t.TempDir()
	p.Now = hs.clock.Now
	p.Getenv = func(k string) string { return hs.env[k] }
	p.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	if setup != nil {
		setup(p)
	}
	hs.h = p.Handler()
	return hs
}

func (hs *harness) installBody(token string) []byte {
	b, _ := json.Marshal(map[string]any{
		"pluginId": "word-count", "markspaceUrl": hs.host.srv.URL, "token": token,
		"signingSecret": base64.StdEncoding.EncodeToString(testSecret),
		"grants":        []map[string]string{{"spaceId": "s1", "role": "editor"}},
	})
	return b
}

func (hs *harness) do(method, path string, body []byte, sign bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if sign {
		// Each signed request gets a new second so equal bodies do not
		// repeat a digest.
		hs.clock.Add(time.Second)
		SignRequest(req, testSecret, "word-count", fmt.Sprintf("d%d", hs.nextID.Add(1)), body, hs.clock.Now())
	}
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	return rec
}

func (hs *harness) post(path string, v any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(v)
	return hs.do("POST", path, b, true)
}

func (hs *harness) install() {
	hs.t.Helper()
	if rec := hs.do("POST", "/install", hs.installBody("mspl_one"), false); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		hs.t.Fatalf("install %d %s", rec.Code, rec.Body)
	}
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}
	return m
}

func TestManifestAndHealth(t *testing.T) {
	hs := newHarness(t, nil, nil)
	rec := hs.do("GET", "/manifest.json", nil, false)
	if rec.Code != 200 || decode(t, rec)["id"] != "word-count" {
		t.Fatalf("manifest %d %s", rec.Code, rec.Body)
	}
	rec = hs.do("GET", "/health", nil, false)
	if m := decode(t, rec); m["status"] != "ok" || m["version"] != "0.1.0" {
		t.Fatalf("health %s", rec.Body)
	}
	if rec := hs.do("POST", "/health", nil, false); rec.Code != 405 {
		t.Fatalf("POST /health %d", rec.Code)
	}
	if rec := hs.post("/events", map[string]any{"events": []any{}}); rec.Code != 503 {
		t.Fatalf("events before install %d", rec.Code)
	}
}

func TestInstallFlow(t *testing.T) {
	hs := newHarness(t, map[string]string{}, nil)
	hs.install()
	fi, err := os.Stat(filepath.Join(hs.p.DataDir, InstallFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("install.json %v %v", fi, err)
	}
	in, err := LoadInstall(hs.p.DataDir)
	if err != nil || in.Token != "mspl_one" || in.Grants[0].Role != "editor" {
		t.Fatalf("load %+v %v", in, err)
	}
	if rec := hs.do("POST", "/install", hs.installBody("mspl_two"), false); rec.Code != 401 {
		t.Fatalf("unsigned reinstall %d", rec.Code)
	}
	if rec := hs.do("POST", "/install", hs.installBody("mspl_two"), true); rec.Code != 200 {
		t.Fatalf("signed reinstall %d %s", rec.Code, rec.Body)
	}
	if hs.p.Credentials().Token != "mspl_two" {
		t.Fatal("reinstall not applied")
	}
	bad, _ := json.Marshal(map[string]any{"pluginId": "other", "markspaceUrl": "http://x", "token": "t", "signingSecret": base64.StdEncoding.EncodeToString(testSecret)})
	rec := hs.do("POST", "/install", bad, true)
	if m := decode(t, rec); rec.Code != 400 || m["code"] != "wrong_plugin" || m["field"] != "$.pluginId" {
		t.Fatalf("bad install %d %s", rec.Code, rec.Body)
	}
}

func TestInstallWindowAndEnv(t *testing.T) {
	hs := newHarness(t, nil, func(p *Plugin) { p.InstallWindow = time.Minute })
	hs.clock.Add(61 * time.Second)
	if rec := hs.do("POST", "/install", hs.installBody("mspl_one"), false); rec.Code != 403 {
		t.Fatalf("after window %d", rec.Code)
	}
	hs = newHarness(t, map[string]string{EnvToken: "mspl_env"}, nil)
	if rec := hs.do("POST", "/install", hs.installBody("mspl_one"), false); rec.Code != 403 {
		t.Fatalf("env token %d", rec.Code)
	}
	hs = newHarness(t, map[string]string{EnvInstallWindow: "30"}, nil)
	hs.clock.Add(31 * time.Second)
	if rec := hs.do("POST", "/install", hs.installBody("mspl_one"), false); rec.Code != 403 {
		t.Fatalf("env window of 30 s after 31 s %d", rec.Code)
	}
	hs = newHarness(t, map[string]string{EnvInstallWindow: "0"}, nil)
	hs.clock.Add(30 * 24 * time.Hour)
	if rec := hs.do("POST", "/install", hs.installBody("mspl_one"), false); rec.Code != 200 {
		t.Fatalf("env window of 0 after 30 days %d %s", rec.Code, rec.Body)
	}
}

func TestInstallWindowFromEnv(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := map[string]time.Duration{
		"":     DefaultInstallWindow,
		"0":    -1,
		"90":   90 * time.Second,
		"1.5":  1500 * time.Millisecond,
		"-1":   DefaultInstallWindow,
		"ten":  DefaultInstallWindow,
		"inf":  DefaultInstallWindow,
		"NaN":  DefaultInstallWindow,
		" 45 ": 45 * time.Second,
	}
	for raw, want := range cases {
		got := installWindowFromEnv(func(string) string { return raw }, logger)
		if got != want {
			t.Errorf("%q: got %v, want %v", raw, got, want)
		}
	}
}

func TestCredentialPrecedence(t *testing.T) {
	dir := t.TempDir()
	fileSecret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	envSecret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	b, _ := json.Marshal(Install{PluginID: "word-count", MarkspaceURL: "http://file", Token: "file", SigningSecret: fileSecret})
	if err := SaveInstall(dir, b); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	c, err := ResolveCredentials(dir, get)
	if err != nil || c.Token != "file" || c.MarkspaceURL != "http://file" || c.Secret[0] != 1 {
		t.Fatalf("file only %+v %v", c, err)
	}
	env[EnvToken] = "env"
	c, _ = ResolveCredentials(dir, get)
	if c.Token != "env" || c.MarkspaceURL != "http://file" || c.Secret[0] != 1 {
		t.Fatalf("token override %+v", c)
	}
	env[EnvSigningSecret] = envSecret
	env[EnvMarkspaceURL] = "http://env"
	c, _ = ResolveCredentials(dir, get)
	if c.MarkspaceURL != "http://env" || c.Secret[0] != 2 {
		t.Fatalf("full override %+v", c)
	}
	env[EnvSigningSecret] = "short"
	if _, err := ResolveCredentials(dir, get); err == nil {
		t.Fatal("bad env secret accepted")
	}
	c, err = ResolveCredentials(t.TempDir(), func(string) string { return "" })
	if err != nil || c.Secret != nil || c.Install != nil {
		t.Fatalf("empty %+v %v", c, err)
	}
	if DefaultDataDir(func(string) string { return "" }) != "/data" || DefaultDataDir(func(string) string { return "/x" }) != "/x" {
		t.Fatal("default data dir")
	}
}

func TestSignatureRejections(t *testing.T) {
	hs := newHarness(t, nil, nil)
	hs.install()
	body := []byte(`{"events":[]}`)
	req := httptest.NewRequest("POST", "/events", bytes.NewReader(body))
	SignRequest(req, bytes.Repeat([]byte{9}, 32), "word-count", "d1", body, hs.clock.Now())
	rec := httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	if m := decode(t, rec); rec.Code != 401 || m["code"] != "mismatch" {
		t.Fatalf("wrong secret %d %s", rec.Code, rec.Body)
	}
	send := func(delivery, plugin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/events", bytes.NewReader(body))
		SignRequest(req, testSecret, plugin, delivery, body, hs.clock.Now())
		rec := httptest.NewRecorder()
		hs.h.ServeHTTP(rec, req)
		return rec
	}
	if rec := send("d1", "word-count"); rec.Code != 204 {
		t.Fatalf("first %d %s", rec.Code, rec.Body)
	}
	if rec := send("d1", "word-count"); rec.Code != 401 || decode(t, rec)["code"] != "replay" {
		t.Fatalf("replay %d %s", rec.Code, rec.Body)
	}
	tampered := func(path, signedPath, delivery, signedDelivery string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, bytes.NewReader(body))
		req.Header.Set(HeaderDelivery, delivery)
		req.Header.Set(HeaderSignature, Sign(testSecret, hs.clock.Now(), "POST", signedPath, signedDelivery, body))
		rec := httptest.NewRecorder()
		hs.h.ServeHTTP(rec, req)
		return rec
	}
	if rec := tampered("/commands/count", "/events", "d5", "d5"); rec.Code != 401 || decode(t, rec)["code"] != "mismatch" {
		t.Fatalf("body sent to another route %d %s", rec.Code, rec.Body)
	}
	if rec := tampered("/events", "/events", "d6", "d5"); rec.Code != 401 || decode(t, rec)["code"] != "mismatch" {
		t.Fatalf("changed delivery id %d %s", rec.Code, rec.Body)
	}
	hs.clock.Add(time.Second)
	if rec := send("d1", "word-count"); rec.Code != 204 {
		t.Fatalf("retry of d1 at a new timestamp %d %s", rec.Code, rec.Body)
	}
	if rec := send("d2", "bookmark-wiki"); rec.Code != 401 || decode(t, rec)["code"] != "wrong_plugin" {
		t.Fatalf("wrong plugin %d %s", rec.Code, rec.Body)
	}
	if rec := send("", "word-count"); rec.Code != 401 || decode(t, rec)["code"] != "missing_delivery" {
		t.Fatalf("missing delivery %d %s", rec.Code, rec.Body)
	}
	hs.clock.Add(-10 * time.Minute)
	req = httptest.NewRequest("POST", "/events", bytes.NewReader(body))
	SignRequest(req, testSecret, "word-count", "d3", body, hs.clock.Now().Add(10*time.Minute))
	rec = httptest.NewRecorder()
	hs.h.ServeHTTP(rec, req)
	if rec.Code != 401 || decode(t, rec)["code"] != "expired" {
		t.Fatalf("expired %d %s", rec.Code, rec.Body)
	}
}

func TestEvents(t *testing.T) {
	var mu sync.Mutex
	got := map[string][]string{}
	fail := atomic.Bool{}
	hs := newHarness(t, nil, func(p *Plugin) {
		for _, pat := range []string{"note.*", "file.created", "*"} {
			pat := pat
			p.On(pat, func(ctx context.Context, b *EventBatch) error {
				if b.Host == nil || b.Host.Token != "mspl_one" {
					t.Errorf("event host %+v", b.Host)
				}
				mu.Lock()
				for _, e := range b.Events {
					got[pat] = append(got[pat], e.Type)
				}
				mu.Unlock()
				if fail.Load() && pat == "*" {
					panic("boom")
				}
				return nil
			})
		}
	})
	hs.install()
	events := map[string]any{"events": []map[string]any{
		{"id": "e1", "type": "note.updated", "itemId": "n1", "at": "2026-09-13T11:00:00Z"},
		{"id": "e2", "type": "file.created"},
		{"id": "e3", "type": "space.created"},
	}}
	if rec := hs.post("/events", events); rec.Code != 204 {
		t.Fatalf("events %d %s", rec.Code, rec.Body)
	}
	if strings.Join(got["note.*"], ",") != "note.updated" || strings.Join(got["file.created"], ",") != "file.created" || len(got["*"]) != 3 {
		t.Fatalf("dispatch %v", got)
	}
	fail.Store(true)
	if rec := hs.post("/events", events); rec.Code != 500 {
		t.Fatalf("panicking handler %d", rec.Code)
	}
}

func TestJobSync(t *testing.T) {
	hs := newHarness(t, nil, func(p *Plugin) {
		p.Job("count-space", func(ctx context.Context, j *JobContext) (any, error) {
			var payload struct{ SpaceID string }
			if err := j.DecodePayload(&payload); err != nil {
				return nil, err
			}
			if j.Job.ID == "j1" && (j.User == nil || j.User.Name != "Ada" || j.Job.Title != "Count words" || j.Job.SpaceID != "s1") {
				t.Errorf("job context %+v user %+v", j.Job, j.User)
			}
			if j.Job.ID == "j2" && (j.User != nil || j.Host.Token != "mspl_one") {
				t.Errorf("job without acting token: user %+v token %q", j.User, j.Host.Token)
			}
			if payload.SpaceID == "fail" {
				return nil, errors.New("space gone")
			}
			if err := j.Progress(ctx, 1, 2, "counting"); err != nil {
				return nil, err
			}
			if j.Canceled() {
				return nil, ErrJobCanceled
			}
			if err := j.Log(ctx, "info", "done"); err != nil {
				return nil, err
			}
			return map[string]int{"words": 42}, nil
		})
	})
	hs.install()
	rec := hs.post("/jobs/count-space", map[string]any{"job": map[string]any{"id": "j1", "kind": "count-space", "title": "Count words", "spaceId": "s1", "payload": map[string]any{"spaceId": "s1"}, "attempt": 1}, "actingToken": "job_tok", "user": map[string]any{"id": "u1", "name": "Ada"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"words":42`) {
		t.Fatalf("job %d %s", rec.Code, rec.Body)
	}
	reqs := hs.host.requests()
	if len(reqs) != 2 || reqs[0].Method != "PATCH" || reqs[0].Path != "/api/plugin/v1/jobs/j1" || reqs[0].Auth != "Bearer job_tok" || reqs[1].Path != "/api/plugin/v1/jobs/j1/log" {
		t.Fatalf("host requests %+v", reqs)
	}
	rec = hs.post("/jobs/count-space", map[string]any{"job": map[string]any{"id": "j2", "payload": map[string]any{"spaceId": "fail"}}})
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "space gone") {
		t.Fatalf("failing job %d %s", rec.Code, rec.Body)
	}
	hs.host.canceled.Store(true)
	rec = hs.post("/jobs/count-space", map[string]any{"job": map[string]any{"id": "j3", "payload": map[string]any{"spaceId": "s1"}}})
	if rec.Code != 409 {
		t.Fatalf("canceled job %d %s", rec.Code, rec.Body)
	}
	if rec := hs.post("/jobs/nope", map[string]any{"job": map[string]any{"id": "j4"}}); rec.Code != 404 {
		t.Fatalf("unknown kind %d", rec.Code)
	}
	if rec := hs.post("/jobs/count-space", map[string]any{"job": map[string]any{"id": "../x"}}); rec.Code != 400 {
		t.Fatalf("bad job id %d", rec.Code)
	}
}

func TestJobBackground(t *testing.T) {
	release := make(chan struct{})
	fail := atomic.Bool{}
	hs := newHarness(t, nil, func(p *Plugin) {
		p.JobWait = 30 * time.Millisecond
		p.HeartbeatEvery = 20 * time.Millisecond
		p.Job("slow", func(ctx context.Context, j *JobContext) (any, error) {
			<-release
			if fail.Load() {
				return nil, errors.New("fetch failed")
			}
			return map[string]any{"notes": 3}, nil
		})
	})
	hs.install()
	rec := hs.post("/jobs/slow", map[string]any{"job": map[string]any{"id": "j1", "kind": "slow", "attempt": 1}})
	if rec.Code != 202 {
		t.Fatalf("slow job %d %s", rec.Code, rec.Body)
	}
	if rec := hs.post("/jobs/slow", map[string]any{"job": map[string]any{"id": "j1", "kind": "slow", "attempt": 1}}); rec.Code != 202 {
		t.Fatalf("duplicate job %d", rec.Code)
	}
	time.Sleep(80 * time.Millisecond)
	close(release)
	hs.p.wg.Wait()
	var heartbeats, done int
	for _, r := range hs.host.requests() {
		if r.Method != "PATCH" || r.Path != "/api/plugin/v1/jobs/j1" {
			continue
		}
		switch r.Body["status"] {
		case nil:
			if _, ok := r.Body["progress"]; ok {
				heartbeats++
			}
		case "done":
			done++
			if res, _ := r.Body["result"].(map[string]any); res["notes"] != float64(3) {
				t.Errorf("result %v", r.Body)
			}
		}
	}
	if heartbeats == 0 || done != 1 {
		t.Fatalf("heartbeats %d done %d: %+v", heartbeats, done, hs.host.requests())
	}

	release = make(chan struct{})
	fail.Store(true)
	if rec := hs.post("/jobs/slow", map[string]any{"job": map[string]any{"id": "j2", "kind": "slow"}}); rec.Code != 202 {
		t.Fatalf("slow job 2 %d", rec.Code)
	}
	close(release)
	hs.p.wg.Wait()
	failed := false
	for _, r := range hs.host.requests() {
		if r.Path == "/api/plugin/v1/jobs/j2" && r.Body["status"] == "failed" && r.Body["message"] == "fetch failed" {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("no failed PATCH: %+v", hs.host.requests())
	}
}

func TestInteractions(t *testing.T) {
	hs := newHarness(t, nil, func(p *Plugin) {
		p.Command("count", func(ctx context.Context, c *CommandContext) (Response, error) {
			item, err := c.Host.GetItem(ctx, c.ItemID())
			if err != nil {
				return nil, err
			}
			note := item["note"].(map[string]any)
			n := len(strings.Fields(note["content"].(string)))
			if c.User.Name != "Ada" || c.Kind() != "note" || c.SpaceID() != "s1" {
				t.Errorf("context %+v", c)
			}
			return Toast(strings.Repeat("w", n), "info"), nil
		})
		p.Command("bad", func(ctx context.Context, c *CommandContext) (Response, error) {
			return Response{"toast": map[string]any{"message": ""}}, nil
		})
		p.Panel("counts", func(ctx context.Context, c *CommandContext) (ui.Node, error) {
			return ui.Section("Word count", ui.Text("3 words")), nil
		})
		p.Action("count-space", func(ctx context.Context, a *ActionContext) (Response, error) {
			if a.Values["mode"] != "fast" || a.Payload["spaceId"] != "s1" {
				t.Errorf("action %+v", a)
			}
			return JobResponse("j9"), nil
		})
		p.Importer("text", func(ctx context.Context, c *ImportContext) (Response, error) {
			if c.FileID != "f1" || c.SpaceID != "s1" || c.Host.Token != "act_imp" {
				t.Errorf("import %+v", c)
			}
			return OpenPanel("word-count.counts", ""), nil
		})
	})
	hs.install()
	ctx := map[string]any{"spaceId": "s1", "itemId": "n1", "kind": "note"}
	user := map[string]any{"id": "u1", "name": "Ada"}
	rec := hs.post("/commands/count", map[string]any{"context": ctx, "actingToken": "act_1", "user": user})
	if rec.Code != 200 || decode(t, rec)["toast"].(map[string]any)["message"] != "www" {
		t.Fatalf("command %d %s", rec.Code, rec.Body)
	}
	if reqs := hs.host.requests(); len(reqs) != 1 || reqs[0].Auth != "Bearer act_1" || reqs[0].Path != "/api/plugin/v1/items/n1" {
		t.Fatalf("acting token not used: %+v", reqs)
	}
	if rec := hs.post("/commands/count", map[string]any{"context": ctx, "user": user}); rec.Code != 400 {
		t.Fatalf("missing acting token %d", rec.Code)
	}
	rec = hs.post("/commands/bad", map[string]any{"context": ctx, "actingToken": "a", "user": user})
	if m := decode(t, rec); rec.Code != 500 || m["code"] != "invalid_field" || m["field"] != "$.toast.message" {
		t.Fatalf("invalid response %d %s", rec.Code, rec.Body)
	}
	rec = hs.post("/panels/counts", map[string]any{"context": ctx, "actingToken": "a", "user": user})
	if m := decode(t, rec); rec.Code != 200 || m["ui"].(map[string]any)["type"] != "section" {
		t.Fatalf("panel %d %s", rec.Code, rec.Body)
	}
	rec = hs.post("/actions/count-space", map[string]any{"context": ctx, "values": map[string]any{"mode": "fast"}, "payload": map[string]any{"spaceId": "s1"}, "actingToken": "a", "user": user})
	if m := decode(t, rec); rec.Code != 200 || m["job"].(map[string]any)["id"] != "j9" {
		t.Fatalf("action %d %s", rec.Code, rec.Body)
	}
	rec = hs.post("/import/text", map[string]any{"fileId": "f1", "spaceId": "s1", "actingToken": "act_imp", "user": user})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "openPanel") {
		t.Fatalf("import %d %s", rec.Code, rec.Body)
	}
	if rec := hs.post("/panels/nope", map[string]any{"actingToken": "a"}); rec.Code != 404 {
		t.Fatalf("unknown panel %d", rec.Code)
	}
}

func TestRegistrationPanics(t *testing.T) {
	p, _ := New(testManifest())
	for name, f := range map[string]func(){
		"undeclared command": func() { p.Command("nope", nil) },
		"invalid action":     func() { p.Action("Bad", nil) },
		"undeclared job":     func() { p.Job("nope", nil) },
		"bad pattern":        func() { p.On("Note?", nil) },
		"duplicate panel": func() {
			p.Panel("counts", nil)
			p.Panel("counts", nil)
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s did not panic", name)
				}
			}()
			f()
		}()
	}
}
