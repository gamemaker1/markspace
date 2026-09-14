// Package wiring assembles the module engine, the built-in modules and the
// plugin host around an application, for cmd/markspace and the end-to-end
// tests.
package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/app"
	"github.com/gamemaker1/markspace/internal/config"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/modules/admin"
	aimod "github.com/gamemaker1/markspace/internal/modules/ai"
	authmod "github.com/gamemaker1/markspace/internal/modules/auth"
	"github.com/gamemaker1/markspace/internal/modules/comments"
	"github.com/gamemaker1/markspace/internal/modules/files"
	"github.com/gamemaker1/markspace/internal/modules/jobs"
	"github.com/gamemaker1/markspace/internal/modules/notes"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
	"github.com/gamemaker1/markspace/internal/modules/search"
	"github.com/gamemaker1/markspace/internal/modules/sharing"
	"github.com/gamemaker1/markspace/internal/modules/spaces"
	"github.com/gamemaker1/markspace/internal/modules/suggestions"
	syncmod "github.com/gamemaker1/markspace/internal/modules/sync"
	"github.com/gamemaker1/markspace/internal/modules/ui"
	"github.com/gamemaker1/markspace/internal/modules/users"
	"github.com/gamemaker1/markspace/internal/plugin"
)

// Parts holds the module engine of one process.
type Parts struct {
	Registry  *module.Registry
	Bus       *module.Bus
	Jobs      *module.Engine
	Scheduler *module.Scheduler
	Host      *plugin.Host
	log       *slog.Logger
}

// EngineOptions adjusts the engine for tests. Zero values keep the defaults.
type EngineOptions struct {
	Now func() time.Time
}

// NewEngine builds the registry, the event bus, the job engine over the index
// job store and the scheduler. The registry keeps the bus handlers and the
// schedules in step with the registered modules.
func NewEngine(cfg *config.Config, db *index.DB, logger *slog.Logger, opts ...EngineOptions) *Parts {
	var o EngineOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	log := logger.With("component", "modules")
	registry := module.NewRegistry()
	bus := module.NewBus(module.BusOptions{Logger: log})
	engine := module.NewEngine(module.EngineOptions{
		Store:    index.NewJobStore(db),
		Handlers: registry,
		Events:   bus,
		Workers:  cfg.Jobs.Workers,
		Logger:   log,
		Now:      o.Now,
		// Retention runs as the daily jobs.retention job. The engine's own
		// cleanup then runs once, at the first tick after start.
		CleanupEvery: 100 * 365 * 24 * time.Hour,
	})
	scheduler := module.NewScheduler(module.SchedulerOptions{Engine: engine, Logger: log, Now: o.Now})
	registry.Watch(func() {
		bus.SetHandlers(registry.EventHandlers())
		scheduler.SetSchedules(registry.Schedules())
	})
	return &Parts{Registry: registry, Bus: bus, Jobs: engine, Scheduler: scheduler, log: log}
}

// ModuleEvent converts an application event into a bus event.
func ModuleEvent(ev core.Event) module.Event {
	id := ev.ID
	if id == "" {
		id = core.NewID()
	}
	var at time.Time
	if ev.At != 0 {
		at = time.UnixMilli(ev.At).UTC()
	}
	return module.Event{
		ID: id, Type: ev.Type, SpaceID: ev.SpaceID, ItemID: ev.ItemID, Kind: ev.Kind,
		ActorID: ev.ActorID, Path: ev.Path, OldPath: ev.OldPath, Data: ev.Data, At: at,
	}
}

// CoreEvent converts a bus event into an application event. A job update
// reaches the person who started the job, and a plugin notification the
// users it names.
func CoreEvent(e module.Event) core.Event {
	ev := core.Event{
		ID: e.ID, Type: e.Type, SpaceID: e.SpaceID, ItemID: e.ItemID, Kind: e.Kind,
		ActorID: e.ActorID, Path: e.Path, OldPath: e.OldPath, Data: e.Data, At: e.At.UnixMilli(),
	}
	if e.Kind == core.KindNote {
		ev.NoteID = e.ItemID
	}
	if e.Type == "plugin.notify" && len(e.Data) > 0 {
		var d struct {
			UserIDs []string `json:"userIds"`
		}
		if json.Unmarshal(e.Data, &d) == nil && len(d.UserIDs) > 0 {
			ev.Audience = d.UserIDs
		}
	}
	return ev
}

// Bridge forwards the bus events that start outside the application, job
// updates and plugin events, to the browser event streams.
func (e *Parts) Bridge(backend *app.App) {
	forward := func(_ context.Context, ev module.Event) error {
		backend.Emit(CoreEvent(ev))
		return nil
	}
	e.Bus.Handle("bridge", "job.*", forward)
	e.Bus.Handle("bridge", "plugin.*", forward)
}

// NewEnv builds the handler environment of the built-in modules from the
// configuration.
func NewEnv(cfg *config.Config, backend core.Backend, auth *api.Authenticator, logger *slog.Logger) api.Env {
	return api.Env{
		Backend:    backend,
		Logger:     logger.With("component", "api"),
		Auth:       auth,
		SessionTTL: cfg.SessionTTL,
		MaxUpload:  cfg.MaxUploadBytes(),
		BaseURL:    cfg.BaseURL,
	}.WithDefaults()
}

// CoreModules returns the built-in modules that need only the handler
// environment, in registration order: auth, users, spaces, notes, files,
// comments, sharing, search, sync, ai, suggestions and admin.
func CoreModules(env api.Env) []module.Module {
	env = env.WithDefaults()
	return []module.Module{
		authmod.New(env),
		users.New(env),
		spaces.New(env),
		notes.New(env),
		files.New(env),
		comments.New(env),
		sharing.New(env),
		search.New(env),
		syncmod.New(env),
		aimod.New(env),
		suggestions.New(env.Backend, env.Logger.With("module", "suggestions")),
		admin.New(env),
	}
}

// MountCore registers the core modules in a new registry and returns its
// Mount function. Servers without the job engine and the plugin host use it.
func MountCore(env api.Env) (func(*http.ServeMux) error, error) {
	reg := module.NewRegistry()
	for _, m := range CoreModules(env) {
		if err := reg.Add(m); err != nil {
			return nil, err
		}
	}
	return func(mux *http.ServeMux) error { return reg.Mount(mux, nil) }, nil
}

// AddBuiltins registers the core modules, the jobs module and any extra
// modules.
func (e *Parts) AddBuiltins(env api.Env, pages api.PageRenderer, extra ...module.Module) error {
	env = env.WithDefaults()
	mods := CoreModules(env)
	mods = append(mods, jobs.New(jobs.Options{Engine: e.Jobs, Pages: pages, Logger: env.Logger.With("module", "jobs")}))
	mods = append(mods, extra...)
	for _, m := range mods {
		if err := e.Registry.Add(m); err != nil {
			return err
		}
	}
	return nil
}

// AddUI registers the ui module with the plugin host's action runner and
// availability, which may be nil.
func (e *Parts) AddUI(backend *app.App, actions ui.ActionRunner, avail ui.Availability, logger *slog.Logger) error {
	return e.Registry.Add(ui.New(ui.Options{
		Registry: e.Registry, Backend: backend, Actions: actions, Availability: avail,
		Logger: logger.With("module", "ui"),
	}))
}

// Mount registers the module routes into a mux.
func (e *Parts) Mount(mux *http.ServeMux) error {
	return e.Registry.Mount(mux, nil)
}

// PageRenderer forwards module pages to the web server, which is built after
// the modules.
type PageRenderer struct {
	mu sync.RWMutex
	r  api.PageRenderer
}

// Set installs the web server once it exists.
func (p *PageRenderer) Set(r api.PageRenderer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.r = r
}

func (p *PageRenderer) get() api.PageRenderer {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.r
}

// RenderModulePage implements api.PageRenderer.
func (p *PageRenderer) RenderModulePage(w http.ResponseWriter, r *http.Request, spec api.PageSpec) {
	if inner := p.get(); inner != nil {
		inner.RenderModulePage(w, r, spec)
		return
	}
	http.Error(w, "pages are not ready", http.StatusServiceUnavailable)
}

// RenderError implements api.PageRenderer.
func (p *PageRenderer) RenderError(w http.ResponseWriter, r *http.Request, err error) {
	if inner := p.get(); inner != nil {
		inner.RenderError(w, r, err)
		return
	}
	http.Error(w, "pages are not ready", http.StatusServiceUnavailable)
}

// Octicon implements api.PageRenderer.
func (p *PageRenderer) Octicon(name string, size ...int) (template.HTML, error) {
	if inner := p.get(); inner != nil {
		return inner.Octicon(name, size...)
	}
	return "", errors.New("octicon: pages are not ready")
}

// MarkspaceURL is the address plugins use to reach this server: the plugins
// configuration, then the base URL, then the listen port on 127.0.0.1.
func MarkspaceURL(cfg *config.Config) string {
	if cfg.Plugins.MarkspaceURL != "" {
		return cfg.Plugins.MarkspaceURL
	}
	if cfg.BaseURL != "" {
		return cfg.BaseURL
	}
	listen := cfg.Listen
	if i := strings.LastIndex(listen, ":"); i >= 0 {
		host := listen[:i]
		if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
			host = "127.0.0.1"
		}
		return "http://" + host + listen[i:]
	}
	return "http://127.0.0.1:8080"
}

// HostOptions adjusts the plugin host for tests.
type HostOptions struct {
	// Adjust changes the host options before the host is built.
	Adjust func(*plugin.Options)
	// NoService leaves the host out of the application's services, so its
	// delivery, health and maintenance loops do not run. Tests drive them.
	NoService bool
}

// AddPlugins builds the plugin host, installs it as the application's plugin
// resolver and a background service, and registers the plugins module.
func (e *Parts) AddPlugins(cfg *config.Config, db *index.DB, backend *app.App, pages api.PageRenderer, logger *slog.Logger, opts ...HostOptions) (*plugin.Host, error) {
	var o HostOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	popts := plugin.Options{
		Store: db, Backend: backend, Registry: e.Registry, Engine: e.Jobs, Bus: e.Bus, Filters: backend,
		DataDir: cfg.DataDir, MarkspaceURL: MarkspaceURL(cfg), Logger: logger.With("component", "plugins"),
		FetchAllowPrivate: cfg.Plugins.AllowPrivateURLs,
	}
	if o.Adjust != nil {
		o.Adjust(&popts)
	}
	host, err := plugin.New(popts)
	if err != nil {
		return nil, err
	}
	e.Host = host
	backend.SetPluginResolver(host)
	if !o.NoService {
		backend.AddService(host)
	}
	mod := plugins.New(plugins.Options{Host: host, Backend: backend, Pages: pages, Logger: logger.With("module", "plugins")})
	if err := e.Registry.Add(mod); err != nil {
		return nil, err
	}
	return host, nil
}
