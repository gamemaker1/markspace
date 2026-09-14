package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

type fakePages struct{}

// RenderModulePage writes the body. The navigation of the spec goes to
// response headers, so tests can check the layout choice.
func (fakePages) RenderModulePage(w http.ResponseWriter, r *http.Request, spec api.PageSpec) {
	w.Header().Set("X-Page-Nav", spec.Nav)
	if spec.AdminNav {
		w.Header().Set("X-Admin-Nav", "true")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, string(spec.Body))
}

func (fakePages) RenderError(w http.ResponseWriter, r *http.Request, err error) {
	status := api.StatusFor(err)
	if status == 0 {
		status = 500
	}
	http.Error(w, err.Error(), status)
}

var (
	admin  = &core.Actor{User: &core.User{ID: "0admin0000000000000000000a", IsAdmin: true, IsActive: true}}
	alice  = &core.Actor{User: &core.User{ID: "0alice0000000000000000000a", IsActive: true}}
	bob    = &core.Actor{User: &core.User{ID: "0bob000000000000000000000b", IsActive: true}}
	plugin = &core.Actor{User: &core.User{ID: "0plug00000000000000000000p", IsActive: true},
		Plugin: &core.PluginIdentity{ID: "p", Status: core.PluginStatusActive}}
)

type stack struct {
	srv    *httptest.Server
	engine *module.Engine
	store  *module.MemJobStore
	clock  *module.ManualClock
	reg    *module.Registry
}

func newStack(t *testing.T) *stack {
	t.Helper()
	clock := module.NewManualClock(time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC))
	store := module.NewMemJobStore()
	reg := module.NewRegistry()
	engine := module.NewEngine(module.EngineOptions{Store: store, Handlers: reg, Now: clock.Now, Workers: 1})
	if err := reg.Add(New(Options{Engine: engine, Pages: fakePages{}, Now: clock.Now})); err != nil {
		t.Fatal(err)
	}
	work := module.New(module.Manifest{ID: "t", Name: "Test", Builtin: true}, func(r *module.Registry) error {
		r.Job("t.work", func(ctx context.Context, job *module.Job, rt module.JobRuntime) (json.RawMessage, error) {
			_ = rt.Log(ctx, "info", "worked")
			return nil, nil
		}, module.JobOptions{Title: "Work"})
		return nil
	})
	if err := reg.Add(work); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err := reg.Mount(mux, nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := &core.Actor{}
		switch r.Header.Get("X-Test-Actor") {
		case "admin":
			actor = admin
		case "alice":
			actor = alice
		case "bob":
			actor = bob
		case "plugin":
			actor = plugin
		}
		mux.ServeHTTP(w, r.WithContext(api.WithActor(r.Context(), actor)))
	}))
	t.Cleanup(srv.Close)
	return &stack{srv: srv, engine: engine, store: store, clock: clock, reg: reg}
}

func (s *stack) enqueue(t *testing.T, title, by string) *module.Job {
	t.Helper()
	j, err := s.engine.Enqueue(context.Background(), module.EnqueueRequest{Kind: "t.work", Title: title, CreatedBy: by})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (s *stack) do(t *testing.T, who, method, path string, body url.Values) (int, string, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(body.Encode())
	}
	req, _ := http.NewRequest(method, s.srv.URL+path, rd)
	req.Header.Set("X-Test-Actor", who)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("Location")
}

func ids(t *testing.T, body string) []string {
	t.Helper()
	var out listResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("list body %q: %v", body, err)
	}
	var list []string
	for _, v := range out.Items {
		list = append(list, v.Title)
	}
	return list
}

func TestJobsAPIVisibility(t *testing.T) {
	s := newStack(t)
	a := s.enqueue(t, "alice job", alice.UserID())
	b := s.enqueue(t, "bob job", bob.UserID())
	s.enqueue(t, "scheduled job", module.ScheduleActor)

	for _, who := range []string{"anon", "plugin"} {
		if st, _, _ := s.do(t, who, "GET", "/api/jobs", nil); st != 401 {
			t.Errorf("%s list: %d", who, st)
		}
	}
	_, body, _ := s.do(t, "alice", "GET", "/api/jobs", nil)
	if got := ids(t, body); len(got) != 1 || got[0] != "alice job" {
		t.Errorf("alice sees %v", got)
	}
	_, body, _ = s.do(t, "admin", "GET", "/api/jobs", nil)
	if got := ids(t, body); len(got) != 3 {
		t.Errorf("admin sees %v", got)
	}
	_, body, _ = s.do(t, "admin", "GET", "/api/jobs?mine=1", nil)
	if got := ids(t, body); len(got) != 0 {
		t.Errorf("admin mine sees %v", got)
	}
	if st, _, _ := s.do(t, "alice", "GET", "/api/jobs?status=bogus", nil); st != 400 {
		t.Errorf("bad status filter: %d", st)
	}

	if st, _, _ := s.do(t, "alice", "GET", "/api/jobs/"+b.ID, nil); st != 404 {
		t.Errorf("alice reads bob's job: %d", st)
	}
	for _, action := range []string{"cancel", "pause", "resume"} {
		if st, _, _ := s.do(t, "alice", "POST", "/api/jobs/"+b.ID+"/"+action, nil); st != 404 {
			t.Errorf("alice %s bob's job: %d", action, st)
		}
	}
	st, body, _ := s.do(t, "alice", "GET", "/api/jobs/"+a.ID, nil)
	var detail detailResponse
	if st != 200 || json.Unmarshal([]byte(body), &detail) != nil || detail.Job.ID != a.ID || detail.Log == nil {
		t.Errorf("get own job: %d %s", st, body)
	}

	check := func(action string, want module.JobStatus) {
		t.Helper()
		st, body, _ := s.do(t, "alice", "POST", "/api/jobs/"+a.ID+"/"+action, nil)
		var v View
		if st != 200 || json.Unmarshal([]byte(body), &v) != nil || v.Status != want {
			t.Errorf("%s: %d %s", action, st, body)
		}
	}
	check("pause", module.JobPaused)
	check("resume", module.JobQueued)
	check("cancel", module.JobCanceled)
	if st, _, _ := s.do(t, "alice", "POST", "/api/jobs/"+a.ID+"/pause", nil); st != 409 {
		t.Errorf("pause canceled job: %d", st)
	}
	if st, _, _ := s.do(t, "admin", "POST", "/api/jobs/"+b.ID+"/cancel", nil); st != 200 {
		t.Errorf("admin cancels bob's job: %d", st)
	}
}

func TestJobsPage(t *testing.T) {
	s := newStack(t)
	a := s.enqueue(t, "alice job", alice.UserID())
	s.enqueue(t, "bob job", bob.UserID())

	if st, _, _ := s.do(t, "anon", "GET", "/jobs", nil); st != 401 {
		t.Errorf("anonymous page: %d", st)
	}
	st, body, _ := s.do(t, "alice", "GET", "/jobs", nil)
	if st != 200 || !strings.Contains(body, "alice job") || strings.Contains(body, "bob job") {
		t.Fatalf("alice page: %d %s", st, body)
	}
	for _, want := range []string{
		`data-job-id="` + a.ID + `"`, `<h1 class="page-header-title" tabindex="-1">Jobs</h1>`, `aria-current="page">Active`,
		"Background jobs you started.", `<span class="ui-label ui-label-secondary">Queued</span>`,
		`popovertarget="job-menu-` + a.ID + `" aria-label="Actions: alice job"`, `value="pause">Pause</button>`,
		`data-confirm="Cancel alice job?"`, `>Cancel job</button>`, `role="region" tabindex="0" aria-label="Jobs table"`,
		`<th scope="row">`, "View log",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, unwanted := range []string{"data-two-step", "cancel-ask", " title=", " disabled", `href="/admin"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("member page has %q", unwanted)
		}
	}
	req, _ := http.NewRequest("GET", s.srv.URL+"/jobs", nil)
	req.Header.Set("X-Test-Actor", "alice")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.Header.Get("X-Admin-Nav") != "" || resp.Header.Get("X-Page-Nav") != "jobs" {
		t.Errorf("member layout: %v %v", err, resp.Header)
	} else {
		resp.Body.Close()
	}
	_, body, _ = s.do(t, "admin", "GET", "/jobs", nil)
	if !strings.Contains(body, "alice job") || !strings.Contains(body, "bob job") || !strings.Contains(body, `href="/admin"`) {
		t.Error("admin page does not show every job with the parent link")
	}
	req.Header.Set("X-Test-Actor", "admin")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.Header.Get("X-Admin-Nav") != "true" || resp.Header.Get("X-Page-Nav") != "admin" {
		t.Errorf("admin layout: %v %v", err, resp.Header)
	} else {
		resp.Body.Close()
	}
	_, body, _ = s.do(t, "bob", "GET", "/jobs?view=finished", nil)
	if !strings.Contains(body, "No finished jobs") || !strings.Contains(body, `data-job-table hidden`) {
		t.Error("an empty view does not show the Blankslate")
	}
	_, body, _ = s.do(t, "admin", "GET", "/jobs?module=other", nil)
	if strings.Contains(body, "alice job") || !strings.Contains(body, "Showing other") {
		t.Error("module filter does not apply")
	}

	st, _, loc := s.do(t, "alice", "POST", "/jobs", url.Values{"action": {"pause"}, "id": {a.ID}, "view": {"active"}, "module": {"t"}})
	if st != 303 || !strings.Contains(loc, "notice=") || !strings.Contains(loc, "module=t") {
		t.Errorf("pause post: %d %s", st, loc)
	}
	if j, _ := s.store.Get(context.Background(), a.ID); j.Status != module.JobPaused {
		t.Errorf("status after pause %s", j.Status)
	}
	_, body, _ = s.do(t, "alice", "GET", "/jobs", nil)
	if !strings.Contains(body, `value="resume"`) {
		t.Error("paused job has no Resume")
	}

	// The two-step Cancel is gone. The ConfirmationDialog asks in the browser.
	_, _, loc = s.do(t, "alice", "POST", "/jobs", url.Values{"action": {"cancel-ask"}, "id": {a.ID}})
	if !strings.Contains(loc, "problem=") {
		t.Errorf("cancel-ask is still an action: %s", loc)
	}
	if j, _ := s.store.Get(context.Background(), a.ID); j.Status == module.JobCanceled {
		t.Fatal("an unknown action canceled the job")
	}
	st, _, loc = s.do(t, "alice", "POST", "/jobs", url.Values{"action": {"cancel"}, "id": {a.ID}, "view": {"active"}})
	if st != 303 || !strings.Contains(loc, "notice=Job+canceled.") {
		t.Errorf("cancel post: %d %s", st, loc)
	}
	if j, _ := s.store.Get(context.Background(), a.ID); j.Status != module.JobCanceled {
		t.Errorf("status after cancel %s", j.Status)
	}
	_, body, _ = s.do(t, "alice", "GET", loc, nil)
	if !strings.Contains(body, `data-job-notice>Job canceled.</p>`) {
		t.Error("the result of the action is not announced")
	}
	_, _, loc = s.do(t, "bob", "POST", "/jobs", url.Values{"action": {"cancel"}, "id": {a.ID}})
	if !strings.Contains(loc, "problem=") {
		t.Errorf("bob cancels alice's job: %s", loc)
	}
	_, body, _ = s.do(t, "bob", "GET", loc, nil)
	if !strings.Contains(body, `class="banner banner-critical"`) {
		t.Error("a failed action shows no critical Banner")
	}

	_, body, _ = s.do(t, "alice", "GET", "/jobs?view=finished", nil)
	if !strings.Contains(body, `<span class="ui-label ui-label-secondary">Canceled</span>`) || !strings.Contains(body, `class="btn btn-sm" href="/jobs?open=`+a.ID) || strings.Contains(body, "job-menu-"+a.ID) {
		t.Error("a finished job does not show View log as its single action")
	}
	_, body, _ = s.do(t, "alice", "GET", "/jobs?view=finished&open="+a.ID, nil)
	if !strings.Contains(body, `id="job-log"`) || !strings.Contains(body, "Log of alice job") {
		t.Error("finished view with an open log does not show the log")
	}
	if st, body, _ := s.do(t, "anon", "GET", "/modules/jobs/jobs.js", nil); st != 200 || !strings.Contains(body, "job.updated") {
		t.Errorf("script: %d", st)
	}
}

func TestRetentionDeletesOldFinishedJobs(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	old := s.enqueue(t, "old", alice.UserID())
	if ran, err := s.engine.RunOnce(ctx); !ran || err != nil {
		t.Fatalf("run old job: %v %v", ran, err)
	}
	if j, _ := s.store.Get(ctx, old.ID); j.Status != module.JobDone {
		t.Fatalf("old job status %s", j.Status)
	}
	s.clock.Advance(31 * 24 * time.Hour)
	recent := s.enqueue(t, "recent", alice.UserID())
	if _, err := s.engine.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.reg.JobHandler(RetentionKind); !ok {
		t.Fatal("retention job not registered")
	}
	if len(s.reg.Schedules()) != 1 || s.reg.Schedules()[0].Every != 24*time.Hour {
		t.Errorf("schedules %+v", s.reg.Schedules())
	}
	ret, err := s.engine.Enqueue(ctx, module.EnqueueRequest{Kind: RetentionKind, CreatedBy: module.ScheduleActor})
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := s.engine.RunOnce(ctx); !ran || err != nil {
		t.Fatalf("run retention: %v %v", ran, err)
	}
	if _, err := s.store.Get(ctx, old.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("old finished job still stored: %v", err)
	}
	if j, err := s.store.Get(ctx, recent.ID); err != nil || j.Status != module.JobDone {
		t.Errorf("recent job: %v %v", j, err)
	}
	if j, _ := s.store.Get(ctx, ret.ID); j.Status != module.JobDone || !strings.Contains(string(j.Result), `"deleted":1`) {
		t.Errorf("retention result %s %s", j.Status, j.Result)
	}
}

func (fakePages) Octicon(name string, size ...int) (template.HTML, error) {
	return template.HTML(`<svg class="octicon octicon-` + template.HTMLEscapeString(name) + `"></svg>`), nil
}
