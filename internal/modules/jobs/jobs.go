// Package jobs is the built-in module for the job queue. It serves the job
// API, the /jobs page with live progress from the event stream, and the daily
// retention job that deletes finished jobs.
package jobs

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

//go:embed assets/*
var assets embed.FS

// Job kinds and limits.
const (
	RetentionKind     = "jobs.retention"
	defaultListLimit  = 100
	maxListLimit      = 500
	retentionInterval = 24 * time.Hour
	retentionJitter   = time.Hour
)

// Options configures the module.
type Options struct {
	Engine *module.Engine
	// Pages renders the /jobs page inside the application layout.
	Pages  api.PageRenderer
	Logger *slog.Logger
	Now    func() time.Time
	// Retention is the age after which finished jobs are deleted. The default
	// is 30 days.
	Retention time.Duration
}

// Module serves the job routes.
type Module struct {
	opts Options
	log  *slog.Logger
}

// New returns the module.
func New(opts Options) *Module {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Retention <= 0 {
		opts.Retention = module.DefaultRetention
	}
	return &Module{opts: opts, log: opts.Logger}
}

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "jobs", Name: "Jobs", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("GET /api/jobs", m.list)
	r.API("GET /api/jobs/{id}", m.get)
	r.API("POST /api/jobs/{id}/cancel", m.control("cancel"))
	r.API("POST /api/jobs/{id}/pause", m.control("pause"))
	r.API("POST /api/jobs/{id}/resume", m.control("resume"))
	r.Web("GET /jobs", m.page)
	r.Web("POST /jobs", m.pagePost)
	r.Web("GET /modules/jobs/jobs.js", m.script)
	r.Job(RetentionKind, m.retention, module.JobOptions{Title: "Delete finished jobs", MaxAttempts: 3})
	r.Schedule(RetentionKind, retentionInterval, retentionJitter)
	return nil
}

// retention deletes finished job trees older than the retention age.
func (m *Module) retention(ctx context.Context, job *module.Job, rt module.JobRuntime) (json.RawMessage, error) {
	n, err := m.opts.Engine.Store().DeleteFinishedBefore(ctx, m.opts.Now().Add(-m.opts.Retention))
	if err != nil {
		return nil, err
	}
	if n > 0 {
		_ = rt.Log(ctx, "info", fmt.Sprintf("deleted %d finished jobs", n))
	}
	return json.Marshal(map[string]int{"deleted": n})
}

func (m *Module) fail(w http.ResponseWriter, r *http.Request, err error) {
	api.WriteError(w, r, m.log, err)
}

// View is a job with the progress the page shows. A parent with no total of
// its own shows its finished children.
type View struct {
	*module.Job
	DisplayProgress int64 `json:"displayProgress"`
	DisplayTotal    int64 `json:"displayTotal"`
}

func viewOf(j *module.Job) View {
	p, t := j.DisplayProgress()
	return View{Job: j, DisplayProgress: p, DisplayTotal: t}
}

// person returns the signed-in person behind a request.
func person(r *http.Request) (*core.Actor, error) {
	actor := api.ActorFrom(r.Context())
	if !actor.IsPerson() {
		return nil, fmt.Errorf("sign in required: %w", core.ErrUnauthorized)
	}
	return actor, nil
}

// visible reports whether the actor may see a job: an admin sees every job,
// anyone else the jobs they started.
func visible(actor *core.Actor, j *module.Job) bool {
	return actor.IsAdmin() || (j.CreatedBy != "" && j.CreatedBy == actor.UserID())
}

// filterFrom reads the list filters of a request.
func filterFrom(actor *core.Actor, q map[string][]string) (module.JobFilter, error) {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	f := module.JobFilter{Kind: get("kind"), Module: get("module"), SpaceID: get("space"), Limit: defaultListLimit}
	for _, s := range strings.Split(get("status"), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		st := module.JobStatus(s)
		if !st.Valid() {
			return f, core.Invalid("status", fmt.Sprintf("unknown job status %q", s))
		}
		if !slices.Contains(f.Statuses, st) {
			f.Statuses = append(f.Statuses, st)
		}
	}
	if n, err := strconv.Atoi(get("limit")); err == nil && n > 0 {
		f.Limit = min(n, maxListLimit)
	}
	switch {
	case !actor.IsAdmin():
		f.CreatedBy = actor.UserID()
	case get("mine") == "1":
		f.CreatedBy = actor.UserID()
	case get("createdBy") != "":
		f.CreatedBy = get("createdBy")
	}
	return f, nil
}

type listResponse struct {
	Items []View `json:"items"`
}

// list serves GET /api/jobs?status=&kind=&module=&space=&limit=&mine=.
func (m *Module) list(w http.ResponseWriter, r *http.Request) {
	actor, err := person(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	f, err := filterFrom(actor, r.URL.Query())
	if err != nil {
		m.fail(w, r, err)
		return
	}
	jobs, err := m.opts.Engine.Store().List(r.Context(), f)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	out := listResponse{Items: []View{}}
	for _, j := range jobs {
		out.Items = append(out.Items, viewOf(j))
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// load reads a job the actor may see. A hidden job reads as not found.
func (m *Module) load(ctx context.Context, actor *core.Actor, id string) (*module.Job, error) {
	j, err := m.opts.Engine.Store().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !visible(actor, j) {
		return nil, fmt.Errorf("job %s: %w", id, core.ErrNotFound)
	}
	return j, nil
}

type detailResponse struct {
	Job View                 `json:"job"`
	Log []module.JobLogEntry `json:"log"`
}

// get serves GET /api/jobs/{id} with the job log.
func (m *Module) get(w http.ResponseWriter, r *http.Request) {
	actor, err := person(r)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	j, err := m.load(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		m.fail(w, r, err)
		return
	}
	logs, err := m.opts.Engine.Store().Logs(r.Context(), j.ID)
	if err != nil {
		m.fail(w, r, err)
		return
	}
	if logs == nil {
		logs = []module.JobLogEntry{}
	}
	api.WriteJSON(w, http.StatusOK, detailResponse{Job: viewOf(j), Log: logs})
}

// apply runs one control action on a job the actor may see.
func (m *Module) apply(ctx context.Context, actor *core.Actor, id, action string) (*module.Job, error) {
	if _, err := m.load(ctx, actor, id); err != nil {
		return nil, err
	}
	e := m.opts.Engine
	switch action {
	case "cancel":
		if err := e.Cancel(ctx, id); err != nil {
			return nil, err
		}
		return e.Store().Get(ctx, id)
	case "pause":
		return e.Pause(ctx, id)
	case "resume":
		return e.Resume(ctx, id)
	}
	return nil, core.Invalid("action", "unknown action")
}

// control serves POST /api/jobs/{id}/cancel, pause and resume.
func (m *Module) control(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, err := person(r)
		if err != nil {
			m.fail(w, r, err)
			return
		}
		j, err := m.apply(r.Context(), actor, r.PathValue("id"), action)
		if err != nil {
			m.fail(w, r, err)
			return
		}
		api.WriteJSON(w, http.StatusOK, viewOf(j))
	}
}

// errorOr maps a control error to a message for the page.
func errorOr(err error) string {
	switch {
	case errors.Is(err, core.ErrConflict):
		return "The job has already finished."
	case errors.Is(err, core.ErrNotFound):
		return "The job no longer exists."
	}
	return "The change could not be made."
}
