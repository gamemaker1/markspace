package module

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// Registry schedule limits.
const (
	MinScheduleEvery = time.Minute
	MaxJobAttempts   = 100
)

// HTTPMethods lists the methods a route pattern may name.
var HTTPMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

// JobOptions configures a job kind.
type JobOptions struct {
	// Title is the default title of new jobs of this kind.
	Title string
	// MaxAttempts is the default attempt limit. 0 means 3.
	MaxAttempts int
}

// EventRegistration is one event handler added with Registry.On. Seq is unique
// for the life of the registry, so the bus can tell a re-added handler from
// the old one.
type EventRegistration struct {
	Seq     uint64
	Module  string
	Pattern string
	Handler EventHandler
}

// JobRegistration is one job kind added with Registry.Job.
type JobRegistration struct {
	Module  string
	Kind    string
	Handler JobHandler
	Options JobOptions
}

// ScheduleSpec is one schedule added with Registry.Schedule.
type ScheduleSpec struct {
	Module  string        `json:"module"`
	JobKind string        `json:"jobKind"`
	Every   time.Duration `json:"every"`
	Jitter  time.Duration `json:"jitter"`
}

// RouteInfo describes one route added with Registry.API or Registry.Web.
type RouteInfo struct {
	Module  string `json:"module"`
	Pattern string `json:"pattern"` // normalized, such as "GET /api/notes/{id}"
	Method  string `json:"method,omitempty"`
	Path    string `json:"path"`
	Web     bool   `json:"web"`
}

type route struct {
	RouteInfo
	handler http.HandlerFunc
}

// Registry collects the contributions of every module. Contributions are
// added only from Module.Register, through Registry.Add, on the goroutine that
// runs Register. A registration method called at any other time panics.
//
// Every registration method records problems instead of returning them. Add
// returns them all together and commits nothing for a module that has any.
type Registry struct {
	addMu sync.Mutex // serializes Add and Unregister

	mu        sync.RWMutex
	cur       *pending
	seq       uint64
	modules   map[string]*moduleEntry
	order     []string
	routes    []route
	events    []EventRegistration
	jobs      map[string]JobRegistration
	schedules map[string]ScheduleSpec
	commands  map[string]Command
	panels    map[string]Panel
	settings  map[string]SettingsSchema
	importers map[string]Importer
	tools     map[string]AITool
	mounted   bool
	watchers  []func()
}

type moduleEntry struct {
	manifest  Manifest
	hasRoutes bool
}

type pending struct {
	manifest  Manifest
	routes    []route
	events    []EventRegistration
	jobs      []JobRegistration
	schedules []ScheduleSpec
	commands  []Command
	panels    []Panel
	settings  []SettingsSchema
	importers []Importer
	tools     []AITool
	errs      []error
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		modules:   map[string]*moduleEntry{},
		jobs:      map[string]JobRegistration{},
		schedules: map[string]ScheduleSpec{},
		commands:  map[string]Command{},
		panels:    map[string]Panel{},
		settings:  map[string]SettingsSchema{},
		importers: map[string]Importer{},
		tools:     map[string]AITool{},
	}
}

func (p *pending) fail(sentinel error, format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf("module %q: %s: %w", p.manifest.ID, fmt.Sprintf(format, args...), sentinel))
}

func (p *pending) invalid(what, name string, err error) {
	p.errs = append(p.errs, fmt.Errorf("module %q: %s %q: %w", p.manifest.ID, what, name, err))
}

func dupErr(module, what, key, other string) error {
	return fmt.Errorf("module %q: %s %q already registered by module %q: %w", module, what, key, other, core.ErrExists)
}

func (r *Registry) stage(method string) *pending {
	r.mu.RLock()
	p := r.cur
	r.mu.RUnlock()
	if p == nil {
		panic("module: Registry." + method + " called outside Module.Register")
	}
	return p
}

// Add runs m.Register and commits its contributions when every one of them is
// valid and none conflicts with a registered module. Duplicate errors wrap
// core.ErrExists and name both modules. Route conflicts wrap core.ErrConflict.
// Validation errors wrap core.ErrInvalid.
func (r *Registry) Add(m Module) (err error) {
	r.addMu.Lock()
	defer r.addMu.Unlock()
	man := m.Manifest()
	if err := man.Validate(); err != nil {
		return fmt.Errorf("module %q: %w", man.ID, err)
	}
	man.Permissions = slices.Clone(man.Permissions)
	r.mu.Lock()
	if _, ok := r.modules[man.ID]; ok {
		r.mu.Unlock()
		return fmt.Errorf("module %q already registered: %w", man.ID, core.ErrExists)
	}
	p := &pending{manifest: man}
	r.cur = p
	r.mu.Unlock()

	regErr := callRegister(m, r)

	r.mu.Lock()
	r.cur = nil
	errs := p.errs
	if regErr != nil {
		errs = append([]error{fmt.Errorf("module %q: register: %w", man.ID, regErr)}, errs...)
	}
	if len(errs) == 0 {
		errs = r.conflictsLocked(p)
	}
	if len(errs) == 0 {
		r.commitLocked(p)
	}
	watchers := slices.Clone(r.watchers)
	r.mu.Unlock()
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	for _, w := range watchers {
		w()
	}
	return nil
}

func callRegister(m Module, r *Registry) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("register panicked: %v", v)
		}
	}()
	return m.Register(r)
}

// Watch adds a function called after every successful Add or Unregister. The
// wiring step uses it to refresh the bus handlers and the schedules. A watcher
// must not call Add or Unregister.
func (r *Registry) Watch(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.watchers = append(r.watchers, fn)
}

// API adds a JSON API route. The pattern must start with an HTTP method
// followed by " /api/", such as "GET /api/notes/{id}". Only built-in modules
// may add routes. The plugin host proxies plugin calls through its own routes.
func (r *Registry) API(pattern string, h http.HandlerFunc) { r.route("API", pattern, h, false) }

// Web adds a server page route outside /api/. Only built-in modules may add
// routes.
func (r *Registry) Web(pattern string, h http.HandlerFunc) { r.route("Web", pattern, h, true) }

func (r *Registry) route(method, pattern string, h http.HandlerFunc, web bool) {
	p := r.stage(method)
	if !p.manifest.Builtin {
		p.fail(core.ErrUnsupported, "route %q: only built-in modules may add routes", pattern)
		return
	}
	if h == nil {
		p.fail(core.ErrInvalid, "route %q: nil handler", pattern)
		return
	}
	info, err := ParseRoute(pattern, web)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("module %q: %w", p.manifest.ID, err))
		return
	}
	info.Module = p.manifest.ID
	p.routes = append(p.routes, route{RouteInfo: info, handler: h})
}

// ParseRoute validates and normalizes a route pattern. The method is upper
// cased and separated from the path by one space. API patterns need a method
// and a path under /api/. Web patterns must stay outside /api/. Host patterns
// and unclean paths are rejected.
func ParseRoute(pattern string, web bool) (RouteInfo, error) {
	bad := func(msg string) (RouteInfo, error) {
		return RouteInfo{}, fmt.Errorf("route %q: %s: %w", pattern, msg, core.ErrInvalid)
	}
	f := strings.Fields(pattern)
	var method, p string
	switch len(f) {
	case 1:
		p = f[0]
	case 2:
		method, p = strings.ToUpper(f[0]), f[1]
	default:
		return bad("a pattern is an optional method and a path")
	}
	if method != "" && !slices.Contains(HTTPMethods, method) {
		return bad("unknown method " + method)
	}
	if !strings.HasPrefix(p, "/") {
		return bad("the path must start with /")
	}
	clean := path.Clean(p)
	if strings.HasSuffix(p, "/") && clean != "/" {
		clean += "/"
	}
	if clean != p {
		return bad("the path is not clean")
	}
	underAPI := p == "/api" || strings.HasPrefix(p, "/api/")
	if !web {
		if method == "" {
			return bad("an API pattern must start with an HTTP method")
		}
		if !strings.HasPrefix(p, "/api/") {
			return bad("an API pattern must be under /api/")
		}
	} else if underAPI {
		return bad("a web pattern must not be under /api/")
	}
	norm := p
	if method != "" {
		norm = method + " " + p
	}
	if err := tryHandle(http.NewServeMux(), norm, http.NotFoundHandler()); err != nil {
		return bad(err.Error())
	}
	return RouteInfo{Pattern: norm, Method: method, Path: p, Web: web}, nil
}

// tryHandle registers a pattern and turns a ServeMux panic into an error.
func tryHandle(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%v", v)
		}
	}()
	mux.Handle(pattern, h)
	return nil
}

// On adds an event handler. The pattern is "*", an exact event type, or a
// prefix ending in ".*".
func (r *Registry) On(eventType string, h EventHandler) {
	p := r.stage("On")
	if !ValidEventPattern(eventType) {
		p.fail(core.ErrInvalid, "event pattern %q: invalid pattern", eventType)
		return
	}
	if h == nil {
		p.fail(core.ErrInvalid, "event pattern %q: nil handler", eventType)
		return
	}
	p.events = append(p.events, EventRegistration{Module: p.manifest.ID, Pattern: eventType, Handler: h})
}

// Job adds a job kind.
func (r *Registry) Job(kind string, h JobHandler, opts JobOptions) {
	p := r.stage("Job")
	switch {
	case !ValidIdent(kind):
		p.fail(core.ErrInvalid, "job kind %q: invalid kind", kind)
		return
	case h == nil:
		p.fail(core.ErrInvalid, "job kind %q: nil handler", kind)
		return
	case opts.MaxAttempts < 0 || opts.MaxAttempts > MaxJobAttempts:
		p.fail(core.ErrInvalid, "job kind %q: maxAttempts must be 0 to %d", kind, MaxJobAttempts)
		return
	case len(opts.Title) > 200:
		p.fail(core.ErrInvalid, "job kind %q: title is longer than 200 bytes", kind)
		return
	}
	p.jobs = append(p.jobs, JobRegistration{Module: p.manifest.ID, Kind: kind, Handler: h, Options: opts})
}

// Schedule enqueues a job of the module's own kind every period, with a
// deterministic jitter in [0, jitter).
func (r *Registry) Schedule(jobKind string, every time.Duration, jitter time.Duration) {
	p := r.stage("Schedule")
	switch {
	case every < MinScheduleEvery:
		p.fail(core.ErrInvalid, "schedule %q: the period must be at least %s", jobKind, MinScheduleEvery)
		return
	case jitter < 0 || jitter > every:
		p.fail(core.ErrInvalid, "schedule %q: jitter must be between 0 and the period", jobKind)
		return
	}
	for _, s := range p.schedules {
		if s.JobKind == jobKind {
			p.errs = append(p.errs, dupErr(p.manifest.ID, "schedule", jobKind, p.manifest.ID))
			return
		}
	}
	p.schedules = append(p.schedules, ScheduleSpec{Module: p.manifest.ID, JobKind: jobKind, Every: every, Jitter: jitter})
}

// Command adds a command.
func (r *Registry) Command(c Command) {
	p := r.stage("Command")
	c.Module = p.manifest.ID
	c.Contexts = slices.Clone(c.Contexts)
	if err := c.Validate(); err != nil {
		p.invalid("command", c.ID, err)
		return
	}
	p.commands = append(p.commands, c)
}

// Panel adds a rail tab or page.
func (r *Registry) Panel(pn Panel) {
	p := r.stage("Panel")
	pn.Module = p.manifest.ID
	pn.Contexts = slices.Clone(pn.Contexts)
	if err := pn.Validate(); err != nil {
		p.invalid("panel", pn.ID, err)
		return
	}
	p.panels = append(p.panels, pn)
}

// Settings sets the module's settings schema. A module has at most one.
func (r *Registry) Settings(s SettingsSchema) {
	p := r.stage("Settings")
	if len(p.settings) > 0 {
		p.errs = append(p.errs, dupErr(p.manifest.ID, "settings", p.manifest.ID, p.manifest.ID))
		return
	}
	fields, err := canonicalFields(s.Fields)
	if err != nil {
		p.invalid("settings", p.manifest.ID, err)
		return
	}
	p.settings = append(p.settings, SettingsSchema{Module: p.manifest.ID, Fields: fields})
}

// Importer adds an importer.
func (r *Registry) Importer(i Importer) {
	p := r.stage("Importer")
	i.Module = p.manifest.ID
	i.Accept = slices.Clone(i.Accept)
	if err := i.Validate(); err != nil {
		p.invalid("importer", i.ID, err)
		return
	}
	p.importers = append(p.importers, i)
}

// AITool adds a tool for AI runs.
func (r *Registry) AITool(t AITool) {
	p := r.stage("AITool")
	t.Module = p.manifest.ID
	if err := t.Validate(); err != nil {
		p.invalid("AI tool", t.Name, err)
		return
	}
	if len(t.InputSchema) == 0 {
		t.InputSchema = json.RawMessage(`{"type":"object"}`)
	} else {
		t.InputSchema = slices.Clone(t.InputSchema)
	}
	p.tools = append(p.tools, t)
}

func checkUnique(errs []error, module, what string, keys []string, owner func(string) (string, bool)) []error {
	seen := map[string]bool{}
	for _, k := range keys {
		if other, ok := owner(k); ok {
			errs = append(errs, dupErr(module, what, k, other))
			continue
		}
		if seen[k] {
			errs = append(errs, dupErr(module, what, k, module))
			continue
		}
		seen[k] = true
	}
	return errs
}

func keysOf[T any](items []T, key func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = key(it)
	}
	return out
}

func ownerIn[T any](m map[string]T, module func(T) string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		if !ok {
			return "", false
		}
		return module(v), true
	}
}

func (r *Registry) conflictsLocked(p *pending) []error {
	var errs []error
	id := p.manifest.ID
	if len(p.routes) > 0 {
		if r.mounted {
			errs = append(errs, fmt.Errorf("module %q: routes are already mounted: %w", id, core.ErrConflict))
		}
		errs = append(errs, routeConflicts(r.routes, p.routes)...)
	}
	errs = checkUnique(errs, id, "job kind", keysOf(p.jobs, func(j JobRegistration) string { return j.Kind }),
		ownerIn(r.jobs, func(j JobRegistration) string { return j.Module }))
	for _, s := range p.schedules {
		if !slices.ContainsFunc(p.jobs, func(j JobRegistration) bool { return j.Kind == s.JobKind }) {
			errs = append(errs, fmt.Errorf("module %q: schedule %q: the module registers no job of that kind: %w", id, s.JobKind, core.ErrInvalid))
		}
	}
	errs = checkUnique(errs, id, "command", keysOf(p.commands, func(c Command) string { return c.ID }),
		ownerIn(r.commands, func(c Command) string { return c.Module }))
	errs = checkUnique(errs, id, "panel", keysOf(p.panels, func(c Panel) string { return c.ID }),
		ownerIn(r.panels, func(c Panel) string { return c.Module }))
	errs = checkUnique(errs, id, "importer", keysOf(p.importers, func(c Importer) string { return c.ID }),
		ownerIn(r.importers, func(c Importer) string { return c.Module }))
	errs = checkUnique(errs, id, "AI tool", keysOf(p.tools, func(c AITool) string { return c.Name }),
		ownerIn(r.tools, func(c AITool) string { return c.Module }))
	return errs
}

// routeConflicts checks new routes against existing ones and each other: first
// for equal normalized patterns, then by registering them into a throwaway
// ServeMux, which panics on patterns that match the same requests with no
// precedence between them.
func routeConflicts(existing, added []route) []error {
	var errs []error
	owner := map[string]string{}
	mux := http.NewServeMux()
	accepted := slices.Clone(existing)
	for _, rt := range existing {
		owner[rt.Pattern] = rt.Module
		_ = tryHandle(mux, rt.Pattern, http.NotFoundHandler())
	}
	for _, rt := range added {
		if other, ok := owner[rt.Pattern]; ok {
			errs = append(errs, dupErr(rt.Module, "route", rt.Pattern, other))
			continue
		}
		if err := tryHandle(mux, rt.Pattern, http.NotFoundHandler()); err != nil {
			other := route{}
			for _, a := range accepted {
				if tryHandle(singleMux(a.Pattern), rt.Pattern, http.NotFoundHandler()) != nil {
					other = a
					break
				}
			}
			errs = append(errs, fmt.Errorf("module %q: route %q conflicts with route %q of module %q: %w",
				rt.Module, rt.Pattern, other.Pattern, other.Module, core.ErrConflict))
			continue
		}
		owner[rt.Pattern] = rt.Module
		accepted = append(accepted, rt)
	}
	return errs
}

func singleMux(pattern string) *http.ServeMux {
	m := http.NewServeMux()
	_ = tryHandle(m, pattern, http.NotFoundHandler())
	return m
}

func (r *Registry) commitLocked(p *pending) {
	id := p.manifest.ID
	r.modules[id] = &moduleEntry{manifest: p.manifest, hasRoutes: len(p.routes) > 0}
	r.order = append(r.order, id)
	r.routes = append(r.routes, p.routes...)
	for _, e := range p.events {
		r.seq++
		e.Seq = r.seq
		r.events = append(r.events, e)
	}
	for _, j := range p.jobs {
		r.jobs[j.Kind] = j
	}
	for _, s := range p.schedules {
		r.schedules[s.JobKind] = s
	}
	for _, c := range p.commands {
		r.commands[c.ID] = c
	}
	for _, pn := range p.panels {
		r.panels[pn.ID] = pn
	}
	for _, s := range p.settings {
		r.settings[id] = s
	}
	for _, i := range p.importers {
		r.importers[i.ID] = i
	}
	for _, t := range p.tools {
		r.tools[t.Name] = t
	}
}

// Unregister removes a module and every contribution it made, so a plugin can
// be paused, uninstalled or added again with a new manifest. It returns
// core.ErrNotFound for an unknown module and core.ErrUnsupported for a module
// with routes, because ServeMux routes cannot be removed.
func (r *Registry) Unregister(moduleID string) error {
	r.addMu.Lock()
	defer r.addMu.Unlock()
	r.mu.Lock()
	entry, ok := r.modules[moduleID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("module %q: %w", moduleID, core.ErrNotFound)
	}
	if entry.hasRoutes {
		r.mu.Unlock()
		return fmt.Errorf("module %q has routes and cannot be unregistered: %w", moduleID, core.ErrUnsupported)
	}
	delete(r.modules, moduleID)
	r.order = slices.DeleteFunc(r.order, func(s string) bool { return s == moduleID })
	r.events = slices.DeleteFunc(r.events, func(e EventRegistration) bool { return e.Module == moduleID })
	deleteOwned(r.jobs, moduleID, func(v JobRegistration) string { return v.Module })
	deleteOwned(r.schedules, moduleID, func(v ScheduleSpec) string { return v.Module })
	deleteOwned(r.commands, moduleID, func(v Command) string { return v.Module })
	deleteOwned(r.panels, moduleID, func(v Panel) string { return v.Module })
	deleteOwned(r.settings, moduleID, func(v SettingsSchema) string { return v.Module })
	deleteOwned(r.importers, moduleID, func(v Importer) string { return v.Module })
	deleteOwned(r.tools, moduleID, func(v AITool) string { return v.Module })
	watchers := slices.Clone(r.watchers)
	r.mu.Unlock()
	for _, w := range watchers {
		w()
	}
	return nil
}

func deleteOwned[T any](m map[string]T, module string, owner func(T) string) {
	for k, v := range m {
		if owner(v) == module {
			delete(m, k)
		}
	}
}

// Mount registers every route into mux. When wrap is not nil, each handler is
// replaced by wrap(module, handler), which the wiring step uses for
// authentication and error mapping. After Mount, Add refuses modules with
// routes. Mount may be called for more than one mux.
func (r *Registry) Mount(mux *http.ServeMux, wrap func(module string, h http.HandlerFunc) http.Handler) error {
	r.mu.Lock()
	r.mounted = true
	routes := slices.Clone(r.routes)
	r.mu.Unlock()
	for _, rt := range routes {
		var h http.Handler = rt.handler
		if wrap != nil {
			h = wrap(rt.Module, rt.handler)
		}
		if err := tryHandle(mux, rt.Pattern, h); err != nil {
			return fmt.Errorf("module %q: mount %q: %v: %w", rt.Module, rt.Pattern, err, core.ErrConflict)
		}
	}
	return nil
}

// Modules returns the manifests in registration order.
func (r *Registry) Modules() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Manifest, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.modules[id].manifest)
	}
	return out
}

// Module returns the manifest of a registered module.
func (r *Registry) Module(id string) (Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.modules[id]
	if !ok {
		return Manifest{}, false
	}
	return e.manifest, true
}

// Routes returns the routes in registration order.
func (r *Registry) Routes() []RouteInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RouteInfo, len(r.routes))
	for i, rt := range r.routes {
		out[i] = rt.RouteInfo
	}
	return out
}

// EventHandlers returns the event handlers in registration order.
func (r *Registry) EventHandlers() []EventRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.events)
}

// JobHandlers returns the job kinds sorted by kind.
func (r *Registry) JobHandlers() []JobRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedValues(r.jobs)
}

// JobHandler returns the registration of one job kind.
func (r *Registry) JobHandler(kind string) (JobRegistration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.jobs[kind]
	return j, ok
}

// JobKinds returns the registered job kinds in byte order.
func (r *Registry) JobKinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedKeys(r.jobs)
}

// Schedules returns the schedules sorted by job kind.
func (r *Registry) Schedules() []ScheduleSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedValues(r.schedules)
}

// LookupCommand returns a command by id.
func (r *Registry) LookupCommand(id string) (Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.commands[id]
	return c, ok
}

// LookupPanel returns a panel by id.
func (r *Registry) LookupPanel(id string) (Panel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.panels[id]
	return p, ok
}

// LookupImporter returns an importer by id.
func (r *Registry) LookupImporter(id string) (Importer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	i, ok := r.importers[id]
	return i, ok
}

// LookupAITool returns an AI tool by name.
func (r *Registry) LookupAITool(name string) (AITool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// LookupSettings returns the settings schema of a module.
func (r *Registry) LookupSettings(moduleID string) (SettingsSchema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.settings[moduleID]
	return s, ok
}

// Contributions returns the contributions the filter allows. Commands,
// importers and AI tools are sorted by id or name. Panels are sorted by Order
// and then id. Settings are included for admins only, sorted by module. The
// returned values share slices with the registry and must not be modified.
func (r *Registry) Contributions(f ContributionFilter) Contributions {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := Contributions{Commands: []Command{}, Panels: []Panel{}, Importers: []Importer{}, AITools: []AITool{}}
	for _, c := range sortedValues(r.commands) {
		if f.allows(c.Module, c.MinAccess, c.Contexts) {
			out.Commands = append(out.Commands, c)
		}
	}
	for _, p := range sortedValues(r.panels) {
		if f.allows(p.Module, p.MinAccess, p.Contexts) {
			out.Panels = append(out.Panels, p)
		}
	}
	sort.SliceStable(out.Panels, func(i, j int) bool { return out.Panels[i].Order < out.Panels[j].Order })
	for _, i := range sortedValues(r.importers) {
		if f.allows(i.Module, i.MinAccess, nil) {
			out.Importers = append(out.Importers, i)
		}
	}
	for _, t := range sortedValues(r.tools) {
		if f.allows(t.Module, t.MinAccess, nil) {
			out.AITools = append(out.AITools, t)
		}
	}
	if f.Admin {
		for _, s := range sortedValues(r.settings) {
			if f.Enabled == nil || f.Enabled(s.Module) {
				out.Settings = append(out.Settings, s)
			}
		}
	}
	return out
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedValues[T any](m map[string]T) []T {
	out := make([]T, 0, len(m))
	for _, k := range sortedKeys(m) {
		out = append(out, m[k])
	}
	return out
}
