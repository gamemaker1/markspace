// Package system is the built-in module for the instance status and the
// backup download. The markspace command line uses both through the Markspace
// CLI token, and an admin session may call them too.
package system

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
)

// JobCounter counts the jobs of each status. index.JobStore implements it.
type JobCounter interface {
	CountByStatus(ctx context.Context) (map[module.JobStatus]int, error)
}

// PluginLister lists the installed plugins. plugin.Host implements it.
type PluginLister interface {
	List(ctx context.Context) ([]plugins.PluginView, error)
}

// Options configures the module.
type Options struct {
	Backend core.Backend
	// Jobs and Plugins may be nil. The status then reports no jobs or
	// plugins.
	Jobs    JobCounter
	Plugins PluginLister
	// DB is the index database the backup snapshots.
	DB *sql.DB
	// Listen, DataDir and DBPath come from the configuration.
	Listen  string
	DataDir string
	DBPath  string
	// Skip lists names relative to DataDir that the backup leaves out, such
	// as the CLI token file.
	Skip    []string
	Version string
	Started time.Time
	Logger  *slog.Logger
	Now     func() time.Time
}

// Module serves the status and backup routes.
type Module struct {
	opts Options
	log  *slog.Logger
}

// New returns the module.
func New(opts Options) *Module {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Started.IsZero() {
		opts.Started = opts.Now()
	}
	return &Module{opts: opts, log: opts.Logger}
}

// Manifest implements module.Module.
func (m *Module) Manifest() module.Manifest {
	return module.Manifest{ID: "system", Name: "System", Builtin: true}
}

// Register implements module.Module.
func (m *Module) Register(r *module.Registry) error {
	r.API("GET /api/admin/status", m.status)
	r.API("GET /api/admin/backup", m.backup)
	return nil
}
