package module

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestRegistryDuplicatesNameBothModules(t *testing.T) {
	cases := []struct {
		name        string
		alpha, beta func(r *Registry)
		want        error
	}{
		{"job kind", func(r *Registry) { r.Job("shared.job", noopJob, JobOptions{}) },
			func(r *Registry) { r.Job("shared.job", noopJob, JobOptions{}) }, core.ErrExists},
		{"command", func(r *Registry) { r.Command(okCommand("shared.cmd")) },
			func(r *Registry) { r.Command(okCommand("shared.cmd")) }, core.ErrExists},
		{"panel", func(r *Registry) { r.Panel(okPanel("shared.panel")) },
			func(r *Registry) { r.Panel(okPanel("shared.panel")) }, core.ErrExists},
		{"importer", func(r *Registry) { r.Importer(okImporter("shared.import")) },
			func(r *Registry) { r.Importer(okImporter("shared.import")) }, core.ErrExists},
		{"AI tool", func(r *Registry) { r.AITool(okTool("shared_tool")) },
			func(r *Registry) { r.AITool(okTool("shared_tool")) }, core.ErrExists},
		{"API route", func(r *Registry) { r.API("GET /api/shared", noopHTTP) },
			func(r *Registry) { r.API("GET /api/shared", noopHTTP) }, core.ErrExists},
		{"API route after normalization", func(r *Registry) { r.API("GET /api/shared", noopHTTP) },
			func(r *Registry) { r.API("get \t /api/shared", noopHTTP) }, core.ErrExists},
		{"web route", func(r *Registry) { r.Web("GET /n/{id}", noopHTTP) },
			func(r *Registry) { r.Web("GET /n/{id}", noopHTTP) }, core.ErrExists},
		{"ServeMux wildcard conflict", func(r *Registry) { r.API("GET /api/notes/{id}", noopHTTP) },
			func(r *Registry) { r.API("GET /api/notes/{name}", noopHTTP) }, core.ErrConflict},
		{"ServeMux crossing wildcards", func(r *Registry) { r.API("GET /api/x/{a}/b", noopHTTP) },
			func(r *Registry) { r.API("GET /api/x/c/{b}", noopHTTP) }, core.ErrConflict},
		{"ServeMux method-less web route", func(r *Registry) { r.Web("/s/{slug}", noopHTTP) },
			func(r *Registry) { r.Web("/s/{name}", noopHTTP) }, core.ErrConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRegistry()
			mustAdd(t, r, builtin("alpha", c.alpha))
			err := r.Add(builtin("beta", c.beta))
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			msg := err.Error()
			if !strings.Contains(msg, `module "alpha"`) || !strings.Contains(msg, `module "beta"`) {
				t.Fatalf("error does not name both modules: %s", msg)
			}
			if _, ok := r.Module("beta"); ok {
				t.Fatalf("failed module was registered")
			}
		})
	}
}

func TestRegistryDuplicatesInsideOneModule(t *testing.T) {
	cases := map[string]func(r *Registry){
		"command":  func(r *Registry) { r.Command(okCommand("a.cmd")); r.Command(okCommand("a.cmd")) },
		"job":      func(r *Registry) { r.Job("a.job", noopJob, JobOptions{}); r.Job("a.job", noopJob, JobOptions{}) },
		"panel":    func(r *Registry) { r.Panel(okPanel("a.p")); r.Panel(okPanel("a.p")) },
		"route":    func(r *Registry) { r.API("GET /api/a", noopHTTP); r.API("GET /api/a", noopHTTP) },
		"settings": func(r *Registry) { r.Settings(SettingsSchema{}); r.Settings(SettingsSchema{}) },
		"schedule": func(r *Registry) {
			r.Job("a.job", noopJob, JobOptions{})
			r.Schedule("a.job", time.Hour, 0)
			r.Schedule("a.job", 2*time.Hour, 0)
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			r := NewRegistry()
			err := r.Add(builtin("alpha", fn))
			if !errors.Is(err, core.ErrExists) || strings.Count(err.Error(), `module "alpha"`) != 2 {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestParseRoute(t *testing.T) {
	valid := []struct {
		pattern string
		web     bool
		want    string
	}{
		{"GET /api/notes", false, "GET /api/notes"},
		{"post /api/notes/{id}", false, "POST /api/notes/{id}"},
		{" DELETE\t/api/x/{rest...} ", false, "DELETE /api/x/{rest...}"},
		{"GET /api/x/{$}", false, "GET /api/x/{$}"},
		{"/", true, "/"},
		{"GET /n/{id}", true, "GET /n/{id}"},
		{"/s/{slug}/", true, "/s/{slug}/"},
	}
	for _, v := range valid {
		info, err := ParseRoute(v.pattern, v.web)
		if err != nil || info.Pattern != v.want {
			t.Errorf("ParseRoute(%q) = %q, %v, want %q", v.pattern, info.Pattern, err, v.want)
		}
	}
	invalid := []struct {
		pattern string
		web     bool
	}{
		{"/api/notes", false},
		{"GET /notes", false},
		{"GET /api", false},
		{"FETCH /api/x", false},
		{"GET api/x", false},
		{"GET /api/../x", false},
		{"GET /api//x", false},
		{"GET example.com/api/x", false},
		{"GET /api/{bad", false},
		{"GET /api/x extra", false},
		{"", false},
		{"/api/x", true},
		{"/api", true},
		{"GET /api/", true},
		{"/n/{a}/{a}", true},
	}
	for _, v := range invalid {
		if _, err := ParseRoute(v.pattern, v.web); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("ParseRoute(%q, web=%v) = %v, want ErrInvalid", v.pattern, v.web, err)
		}
	}
}

func TestRegistryNonConflictingRoutes(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, builtin("notes", func(r *Registry) {
		r.API("GET /api/notes/{id}", noopHTTP)
		r.API("POST /api/notes/{id}", noopHTTP)
	}))
	mustAdd(t, r, builtin("stars", func(r *Registry) {
		r.API("GET /api/notes/starred", noopHTTP)
		r.Web("GET /notes/{id}", noopHTTP)
	}))
	if got := len(r.Routes()); got != 4 {
		t.Fatalf("%d routes, want 4", got)
	}
}

func TestRegistryAddIsAtomic(t *testing.T) {
	r := NewRegistry()
	bad := Command{ID: "alpha.bad", Title: "Bad", Run: okCommand("x").Run}
	err := r.Add(builtin("alpha", func(r *Registry) {
		r.Command(okCommand("alpha.good"))
		r.Job("alpha.job", noopJob, JobOptions{})
		r.On("note.*", noopEvent)
		r.Command(bad)
	}))
	if !errors.Is(err, core.ErrInvalid) || !strings.Contains(err.Error(), "alpha.bad") {
		t.Fatalf("got %v", err)
	}
	if _, ok := r.LookupCommand("alpha.good"); ok {
		t.Fatalf("valid command of a failed module was committed")
	}
	if len(r.JobKinds()) != 0 || len(r.EventHandlers()) != 0 || len(r.Modules()) != 0 {
		t.Fatalf("failed module left registrations")
	}
	mustAdd(t, r, builtin("alpha", func(r *Registry) { r.Command(okCommand("alpha.good")) }))
}

func TestRegistryValidation(t *testing.T) {
	cases := map[string]func(r *Registry){
		"job kind":            func(r *Registry) { r.Job("Bad Kind", noopJob, JobOptions{}) },
		"nil job handler":     func(r *Registry) { r.Job("a.job", nil, JobOptions{}) },
		"job max attempts":    func(r *Registry) { r.Job("a.job", noopJob, JobOptions{MaxAttempts: 101}) },
		"command contexts":    func(r *Registry) { r.Command(okCommand("a.cmd", "desktop")) },
		"command no handler":  func(r *Registry) { c := okCommand("a.cmd"); c.Run = nil; r.Command(c) },
		"command shortcut":    func(r *Registry) { c := okCommand("a.cmd"); c.Shortcut = "Hyper-x"; r.Command(c) },
		"panel where":         func(r *Registry) { p := okPanel("a.p"); p.Where = "left"; r.Panel(p) },
		"panel renderer":      func(r *Registry) { p := okPanel("a.p"); p.Builtin = ""; r.Panel(p) },
		"tool name dot":       func(r *Registry) { r.AITool(okTool("a.tool")) },
		"tool schema":         func(r *Registry) { tl := okTool("tool"); tl.InputSchema = json.RawMessage(`[]`); r.AITool(tl) },
		"importer accept":     func(r *Registry) { i := okImporter("a.i"); i.Accept = []string{"html"}; r.Importer(i) },
		"schedule period":     func(r *Registry) { r.Job("a.job", noopJob, JobOptions{}); r.Schedule("a.job", time.Second, 0) },
		"schedule jitter":     func(r *Registry) { r.Job("a.job", noopJob, JobOptions{}); r.Schedule("a.job", time.Hour, 2*time.Hour) },
		"schedule other kind": func(r *Registry) { r.Schedule("b.job", time.Hour, 0) },
		"event pattern":       func(r *Registry) { r.On("Note.Created", noopEvent) },
		"event handler":       func(r *Registry) { r.On("note.created", nil) },
		"settings field":      func(r *Registry) { r.Settings(SettingsSchema{Fields: []Field{{Type: "password", Name: "p"}}}) },
		"route handler":       func(r *Registry) { r.API("GET /api/a", nil) },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			r := NewRegistry()
			if err := r.Add(builtin("alpha", fn)); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
	r := NewRegistry()
	if err := r.Add(New(Manifest{ID: "Bad"}, nil)); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("invalid manifest id: %v", err)
	}
	if err := r.Add(New(Manifest{ID: "p", Permissions: []string{"root"}}, nil)); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown permission: %v", err)
	}
	if err := r.Add(New(Manifest{ID: "p"}, func(*Registry) error { return errors.New("boom") })); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("register error not returned: %v", err)
	}
	if err := r.Add(New(Manifest{ID: "p"}, func(*Registry) error { panic("kaboom") })); err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("register panic not returned: %v", err)
	}
	mustAdd(t, r, New(Manifest{ID: "p"}, nil))
	if err := r.Add(New(Manifest{ID: "p"}, nil)); !errors.Is(err, core.ErrExists) {
		t.Fatalf("duplicate module: %v", err)
	}
}

func TestRegistryRoutesAreBuiltinOnly(t *testing.T) {
	r := NewRegistry()
	err := r.Add(plugin("bookmark-wiki", func(r *Registry) { r.API("GET /api/wiki", noopHTTP) }))
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
}

func TestRegistryOutsideRegisterPanics(t *testing.T) {
	r := NewRegistry()
	defer func() {
		if recover() == nil {
			t.Fatalf("no panic")
		}
	}()
	r.Command(okCommand("a.cmd"))
}

func TestRegistryUnregister(t *testing.T) {
	r := NewRegistry()
	changes := 0
	r.Watch(func() { changes++ })
	register := func(r *Registry) {
		r.On("note.*", noopEvent)
		r.On("*", noopEvent)
		r.Job("bookmark-wiki.ingest", noopJob, JobOptions{MaxAttempts: 5})
		r.Job("bookmark-wiki.lint", noopJob, JobOptions{})
		r.Schedule("bookmark-wiki.lint", 24*time.Hour, time.Hour)
		r.Command(okCommand("bookmark-wiki.add-url", "global", "link"))
		r.Panel(okPanel("bookmark-wiki.wiki"))
		r.Settings(SettingsSchema{Fields: []Field{{Type: "space", Name: "space", Label: "Wiki space", Required: true}}})
		r.Importer(okImporter("bookmark-wiki.netscape"))
		r.AITool(okTool("wiki_lookup"))
	}
	mustAdd(t, r, builtin("notes", func(r *Registry) {
		r.API("GET /api/notes/{id}", noopHTTP)
		r.On("note.created", noopEvent)
		r.Command(okCommand("notes.new"))
	}))
	mustAdd(t, r, plugin("bookmark-wiki", register))
	firstSeqs := keysOf(r.EventHandlers(), func(e EventRegistration) string { return e.Module })
	if len(firstSeqs) != 3 || len(r.JobKinds()) != 2 || len(r.Schedules()) != 1 {
		t.Fatalf("registration incomplete: %v %v %v", firstSeqs, r.JobKinds(), r.Schedules())
	}
	all := r.Contributions(ContributionFilter{Access: core.AccessManage, Admin: true})
	if len(all.Commands) != 2 || len(all.Panels) != 1 || len(all.Importers) != 1 || len(all.AITools) != 1 || len(all.Settings) != 1 {
		t.Fatalf("contributions before unregister: %+v", all)
	}
	oldSeqs := r.EventHandlers()

	if err := r.Unregister("bookmark-wiki"); err != nil {
		t.Fatal(err)
	}
	if kinds := r.JobKinds(); len(kinds) != 0 {
		t.Fatalf("job kinds left: %v", kinds)
	}
	if _, ok := r.JobHandler("bookmark-wiki.ingest"); ok {
		t.Fatalf("job handler left")
	}
	if len(r.Schedules()) != 0 {
		t.Fatalf("schedules left")
	}
	if ev := r.EventHandlers(); len(ev) != 1 || ev[0].Module != "notes" {
		t.Fatalf("event handlers left: %+v", ev)
	}
	after := r.Contributions(ContributionFilter{Access: core.AccessManage, Admin: true})
	if len(after.Commands) != 1 || len(after.Panels) != 0 || len(after.Importers) != 0 || len(after.AITools) != 0 || len(after.Settings) != 0 {
		t.Fatalf("contributions after unregister: %+v", after)
	}
	for _, lookup := range []bool{
		func() bool { _, ok := r.LookupPanel("bookmark-wiki.wiki"); return ok }(),
		func() bool { _, ok := r.LookupImporter("bookmark-wiki.netscape"); return ok }(),
		func() bool { _, ok := r.LookupAITool("wiki_lookup"); return ok }(),
		func() bool { _, ok := r.LookupSettings("bookmark-wiki"); return ok }(),
		func() bool { _, ok := r.LookupCommand("bookmark-wiki.add-url"); return ok }(),
	} {
		if lookup {
			t.Fatalf("lookup still finds a removed contribution")
		}
	}
	if _, ok := r.Module("bookmark-wiki"); ok {
		t.Fatalf("module still registered")
	}

	mustAdd(t, r, plugin("bookmark-wiki", register))
	for _, e := range r.EventHandlers() {
		if e.Module == "bookmark-wiki" && slices.ContainsFunc(oldSeqs, func(o EventRegistration) bool { return o.Seq == e.Seq }) {
			t.Fatalf("re-added handler reuses seq %d", e.Seq)
		}
	}
	if err := r.Unregister("notes"); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("unregister module with routes: %v", err)
	}
	if err := r.Unregister("missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unregister unknown: %v", err)
	}
	if changes != 4 {
		t.Fatalf("watchers ran %d times, want 4", changes)
	}
}

func TestRegistryContributionsFilter(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, builtin("alpha", func(r *Registry) {
		view := okCommand("alpha.view", "note")
		view.MinAccess = core.AccessView
		edit := okCommand("alpha.edit", "note", "selection")
		edit.MinAccess = core.AccessEdit
		r.Command(view)
		r.Command(edit)
		r.Command(okCommand("alpha.global", "global"))
		b := okPanel("alpha.b")
		b.Order = 2
		a := okPanel("alpha.a")
		a.Order = 1
		a.Contexts = []string{"note", "space"}
		page := okPanel("alpha.page")
		page.Where, page.Contexts = "page", []string{"global"}
		r.Panel(b)
		r.Panel(a)
		r.Panel(page)
		imp := okImporter("alpha.import")
		imp.MinAccess = core.AccessEdit
		r.Importer(imp)
		tool := okTool("alpha_tool")
		tool.MinAccess = core.AccessView
		r.AITool(tool)
		r.Settings(SettingsSchema{Fields: []Field{{Type: "checkbox", Name: "on"}}})
	}))
	mustAdd(t, r, plugin("beta", func(r *Registry) { r.Command(okCommand("beta.x", "note")) }))

	ids := func(c Contributions) (cmds, panels []string) {
		for _, x := range c.Commands {
			cmds = append(cmds, x.ID)
		}
		for _, x := range c.Panels {
			panels = append(panels, x.ID)
		}
		return
	}
	check := func(f ContributionFilter, wantCmds, wantPanels []string, importers, tools, settings int) {
		t.Helper()
		c := r.Contributions(f)
		cmds, panels := ids(c)
		if !slices.Equal(cmds, wantCmds) || !slices.Equal(panels, wantPanels) ||
			len(c.Importers) != importers || len(c.AITools) != tools || len(c.Settings) != settings {
			t.Fatalf("filter %+v: commands %v panels %v importers %d tools %d settings %d",
				f, cmds, panels, len(c.Importers), len(c.AITools), len(c.Settings))
		}
	}
	check(ContributionFilter{Access: core.AccessView, Contexts: []string{"note"}},
		[]string{"alpha.view", "beta.x"}, []string{"alpha.a", "alpha.b"}, 0, 1, 0)
	check(ContributionFilter{Access: core.AccessEdit, Contexts: []string{"selection"}},
		[]string{"alpha.edit"}, nil, 1, 1, 0)
	check(ContributionFilter{Access: core.AccessNone, Contexts: []string{"global"}},
		[]string{"alpha.global"}, []string{"alpha.page"}, 0, 0, 0)
	check(ContributionFilter{Access: core.AccessManage, Admin: true, Enabled: func(m string) bool { return m != "beta" }},
		[]string{"alpha.edit", "alpha.global", "alpha.view"}, []string{"alpha.page", "alpha.a", "alpha.b"}, 1, 1, 1)
	check(ContributionFilter{Access: core.AccessManage, Enabled: func(string) bool { return false }}, nil, nil, 0, 0, 0)

	raw, err := json.Marshal(r.Contributions(ContributionFilter{Access: core.AccessView, Contexts: []string{"note"}}))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"minAccess":"view"`) || strings.Contains(s, "Run") || !strings.Contains(s, `"module":"beta"`) {
		t.Fatalf("contributions JSON: %s", s)
	}
}

func TestRegistryMount(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, builtin("notes", func(r *Registry) {
		r.API("GET /api/notes/{id}", func(w http.ResponseWriter, req *http.Request) {
			w.Write([]byte("note " + req.PathValue("id")))
		})
		r.Web("GET /n/{id}", func(w http.ResponseWriter, req *http.Request) { w.Write([]byte("page")) })
	}))
	mux := http.NewServeMux()
	err := r.Mount(mux, func(module string, h http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("X-Module", module)
			h(w, req)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/notes/abc", nil))
	if rec.Body.String() != "note abc" || rec.Header().Get("X-Module") != "notes" {
		t.Fatalf("mounted route: %q %v", rec.Body.String(), rec.Header())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/n/x", nil))
	if rec.Body.String() != "page" {
		t.Fatalf("web route: %q", rec.Body.String())
	}
	if err := r.Add(builtin("late", func(r *Registry) { r.API("GET /api/late", noopHTTP) })); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("route added after mount: %v", err)
	}
	mustAdd(t, r, plugin("late-plugin", func(r *Registry) { r.Command(okCommand("late.cmd")) }))

	taken := http.NewServeMux()
	taken.HandleFunc("GET /api/notes/{name}", noopHTTP)
	if err := r.Mount(taken, nil); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("mount into a conflicting mux: %v", err)
	}
}
