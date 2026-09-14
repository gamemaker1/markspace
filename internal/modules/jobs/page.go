package jobs

import (
	"bytes"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// pageTemplate is parsed with api.PageFuncs and executed with
// api.ExecutePage, which supplies the web server's octicon function.
var pageTemplate = template.Must(template.New("page.html").Funcs(api.PageFuncs()).Funcs(template.FuncMap{
	"dict":    dict,
	"relTime": relTime,
}).ParseFS(assets, "assets/page.html"))

// dict builds a map from alternating keys and values, for sub-templates.
func dict(kv ...any) map[string]any {
	out := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			out[k] = kv[i+1]
		}
	}
	return out
}

// relTime renders a Primer RelativeTime element with the date as fallback
// text. It matches the timeAgo template function of the web package.
func relTime(t time.Time) template.HTML {
	if t.IsZero() {
		return ""
	}
	t = t.UTC()
	return template.HTML(`<relative-time datetime="` + t.Format(time.RFC3339) + `" no-title>` +
		template.HTMLEscapeString(t.Format("Jan 2, 2006")) + `</relative-time>`)
}

// Page views of /jobs.
const (
	viewActive   = "active"
	viewFinished = "finished"
	viewAll      = "all"
)

var viewStatuses = map[string][]module.JobStatus{
	viewActive:   module.ActiveJobStatuses,
	viewFinished: {module.JobDone, module.JobFailed, module.JobCanceled},
	viewAll:      nil,
}

// rowView is one DataTable row of the page.
type rowView struct {
	View
	Label       string
	StatusLabel string
	// Tone is the Label variant of the status (docs/primer.md 5.8).
	Tone string
	// Percent is the rounded share of the total for the ProgressBar.
	Percent  int
	BarColor string
	HasTotal bool
	CanPause bool
	// CanResume is true for a paused job and for a running job whose pause
	// waits for its attempt to end.
	CanResume bool
	CanCancel bool
	// Actions counts the row actions. One action is a Button small, more
	// are an ActionMenu (docs/primer.md 5.2).
	Actions int
	LogURL  string
}

type pageData struct {
	Admin    bool
	View     string
	Views    []viewTab
	Filter   string
	Module   string
	Kind     string
	Query    string
	Rows     []rowView
	Notice   string
	Problem  string
	Everyone bool
	// Open is the row whose log the page shows in place, for use without
	// the page script. Log holds its entries.
	Open *rowView
	Log  []module.JobLogEntry
}

type viewTab struct {
	ID, Label, URL string
	Current        bool
}

var statusLabels = map[module.JobStatus]string{
	module.JobQueued: "Queued", module.JobRunning: "Running", module.JobPaused: "Paused",
	module.JobDone: "Done", module.JobFailed: "Failed", module.JobCanceled: "Canceled",
}

var statusTones = map[module.JobStatus]string{
	module.JobQueued: "secondary", module.JobRunning: "accent", module.JobPaused: "secondary",
	module.JobDone: "success", module.JobFailed: "danger", module.JobCanceled: "secondary",
}

// barColors gives the ProgressBar colour of a status. Other states use the
// accent. Success fills a bar only when the job is done.
var barColors = map[module.JobStatus]string{
	module.JobDone: "success", module.JobFailed: "danger", module.JobPaused: "neutral", module.JobCanceled: "neutral",
}

func rowOf(j *module.Job, logURL string) rowView {
	v := viewOf(j)
	label := strings.TrimSpace(j.Title)
	if label == "" {
		label = j.Kind
	}
	tone := statusTones[j.Status]
	if tone == "" {
		tone = "secondary"
	}
	color := barColors[j.Status]
	if color == "" {
		color = "accent"
	}
	row := rowView{
		View: v, Label: label, StatusLabel: statusLabels[j.Status], Tone: tone, BarColor: color,
		HasTotal:  v.DisplayTotal > 0,
		CanPause:  (j.Status == module.JobQueued || j.Status == module.JobRunning) && !j.PauseRequested,
		CanResume: j.Status == module.JobPaused || (j.Status == module.JobRunning && j.PauseRequested),
		CanCancel: !j.Status.Finished(),
		LogURL:    logURL,
	}
	if row.StatusLabel == "" {
		row.StatusLabel = string(j.Status)
	}
	if row.HasTotal {
		row.Percent = int(math.Round(math.Max(0, math.Min(100, float64(v.DisplayProgress)/float64(v.DisplayTotal)*100))))
	}
	row.Actions = 1
	for _, on := range []bool{row.CanPause, row.CanResume, row.CanCancel} {
		if on {
			row.Actions++
		}
	}
	return row
}

// page serves GET /jobs?view=active|finished|all&module=&kind=&open=
// &notice=&problem=. The layout itself reads error and flash, so the page
// uses its own keys for messages.
func (m *Module) page(w http.ResponseWriter, r *http.Request) {
	actor, err := person(r)
	if err != nil {
		m.opts.Pages.RenderError(w, r, err)
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	view := q.Get("view")
	if _, ok := viewStatuses[view]; !ok {
		view = viewActive
	}
	f := module.JobFilter{Module: strings.TrimSpace(q.Get("module")), Kind: strings.TrimSpace(q.Get("kind")),
		Statuses: viewStatuses[view], Limit: defaultListLimit}
	if !actor.IsAdmin() {
		f.CreatedBy = actor.UserID()
	}
	jobs, err := m.opts.Engine.Store().List(ctx, f)
	if err != nil {
		m.opts.Pages.RenderError(w, r, err)
		return
	}
	extra := url.Values{}
	filter := ""
	if f.Module != "" {
		extra.Set("module", f.Module)
		filter = f.Module
	}
	if f.Kind != "" {
		extra.Set("kind", f.Kind)
		filter = f.Kind
	}
	d := pageData{Admin: actor.IsAdmin(), View: view, Filter: filter, Module: f.Module, Kind: f.Kind, Query: extra.Encode(),
		Notice: q.Get("notice"), Problem: q.Get("problem"), Everyone: actor.IsAdmin()}
	for _, t := range []struct{ id, label string }{{viewActive, "Active"}, {viewFinished, "Finished"}, {viewAll, "All"}} {
		u := url.Values{"view": {t.id}}
		for k, v := range extra {
			u[k] = v
		}
		d.Views = append(d.Views, viewTab{ID: t.id, Label: t.label, URL: "/jobs?" + u.Encode(), Current: t.id == view})
	}
	for _, j := range jobs {
		u := url.Values{"view": {view}, "open": {j.ID}}
		for k, v := range extra {
			u[k] = v
		}
		row := rowOf(j, "/jobs?"+u.Encode()+"#job-log")
		if q.Get("open") == j.ID {
			open := row
			d.Open = &open
			d.Log, _ = m.opts.Engine.Store().Logs(ctx, j.ID)
		}
		d.Rows = append(d.Rows, row)
	}
	var buf bytes.Buffer
	if err := api.ExecutePage(&buf, pageTemplate, m.opts.Pages, d); err != nil {
		m.opts.Pages.RenderError(w, r, err)
		return
	}
	spec := api.PageSpec{
		Title: "Jobs", Nav: "jobs", Body: template.HTML(buf.String()),
		Scripts: []string{"/modules/jobs/jobs.js"},
	}
	// An admin reaches the page from the administration NavList, so it shows
	// in the administration layout.
	if actor.IsAdmin() {
		spec.Nav, spec.AdminNav = "admin", true
	}
	m.opts.Pages.RenderModulePage(w, r, spec)
}

// pageNotices are the status announcements after a row action.
var pageNotices = map[string]string{"cancel": "Job canceled.", "pause": "Job paused.", "resume": "Job resumed."}

// pagePost serves POST /jobs, the form fallback of the row actions. The
// Cancel button asks through a ConfirmationDialog before it submits.
func (m *Module) pagePost(w http.ResponseWriter, r *http.Request) {
	actor, err := person(r)
	if err != nil {
		m.opts.Pages.RenderError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		m.opts.Pages.RenderError(w, r, core.Invalid("body", "the form could not be read"))
		return
	}
	back := url.Values{}
	for _, k := range []string{"view", "module", "kind"} {
		if v := r.PostFormValue(k); v != "" {
			back.Set(k, v)
		}
	}
	id, action := r.PostFormValue("id"), r.PostFormValue("action")
	if _, err := m.apply(r.Context(), actor, id, action); err != nil {
		back.Set("problem", errorOr(err))
	} else {
		back.Set("notice", pageNotices[action])
	}
	http.Redirect(w, r, "/jobs?"+back.Encode(), http.StatusSeeOther)
}

// script serves the page script.
func (m *Module) script(w http.ResponseWriter, r *http.Request) {
	b, err := assets.ReadFile("assets/jobs.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}
