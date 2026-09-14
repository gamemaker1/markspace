package plugins

import (
	"log/slog"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// Options configures the module.
type Options struct {
	Host    Host
	Backend core.Backend
	// Pages renders /admin/plugins inside the application layout.
	Pages  api.PageRenderer
	Logger *slog.Logger
	Now    func() time.Time
}

// Module serves the host API, the admin API and the admin page.
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
	return &Module{opts: opts, log: opts.Logger}
}

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: ModuleID, Name: "Plugins", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	m.registerHostAPI(r)
	m.registerAdmin(r)
	return nil
}
