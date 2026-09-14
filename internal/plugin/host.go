// Package plugin is the plugin host. It installs plugins from their
// manifests, adds each running plugin to the module registry as a module,
// resolves plugin and acting tokens for the application, delivers events
// through the persistent plugin_deliveries queue, checks plugin health, runs
// plugin jobs, and backs the host API with key-value, blob, secret, web fetch
// and notification services. docs/plugins.md specifies the protocol.
//
// Decisions where docs/plugins.md is open:
//   - A plugin is the module "plugin.<id>". Its commands, panels and importers
//     have the ids "<id>.<local id>", its job kinds "plugin.<id>.<kind>".
//   - Commands, panels and importers need the ui permission. Event delivery
//     needs the events permission.
//   - A schedule has a jitter of a tenth of its period, at most one hour.
//   - An event delivery batch uses the id of its first delivery as the
//     delivery id, and every attempt is signed with a fresh timestamp.
//   - A job started by a person gets an acting token scoped to the job. It
//     stays valid while the job runs, for at most 24 h, and is revoked when
//     the attempt ends.
//   - A plugin job that reports failed through the host API counts as a
//     failed attempt and retries like any other failure.
//   - Deleting a plugin's data on uninstall deletes the notes and files its
//     user created in its granted spaces, then its folders that are empty.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/fetch"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/sdk"
)

// Store is the index database the host uses. *index.DB implements it.
type Store interface {
	CreatePlugin(ctx context.Context, p *index.Plugin) error
	UpdatePlugin(ctx context.Context, p *index.Plugin) error
	SetPluginStatus(ctx context.Context, id, status string, now time.Time) error
	GetPlugin(ctx context.Context, id string) (*index.Plugin, error)
	GetPluginByTokenHash(ctx context.Context, hash string) (*index.Plugin, error)
	ListPlugins(ctx context.Context) ([]*index.Plugin, error)
	RecordPluginHealth(ctx context.Context, id string, ok bool, errText string, at time.Time) (*index.Plugin, error)

	CreateUser(ctx context.Context, u *core.User) error
	UpdateUser(ctx context.Context, u *core.User) error
	GetUser(ctx context.Context, id string) (*core.User, error)
	GetUserByEmail(ctx context.Context, email string) (*core.User, error)
	SetUserKind(ctx context.Context, userID, kind string) error
	UserKind(ctx context.Context, userID string) (string, error)
	ListMembershipsForUser(ctx context.Context, userID string) ([]*core.SpaceMember, error)
	GetSpace(ctx context.Context, id string) (*core.Space, error)

	SetPluginGrants(ctx context.Context, pluginID string, grants []index.PluginGrant) error
	ListPluginGrants(ctx context.Context, pluginID string) ([]index.PluginGrant, error)
	SetPluginSecret(ctx context.Context, pluginID, name, value string, now time.Time) error
	GetPluginSecret(ctx context.Context, pluginID, name string) (string, error)
	ListPluginSecrets(ctx context.Context, pluginID string) ([]index.PluginSecretInfo, error)
	DeletePluginSecret(ctx context.Context, pluginID, name string) error

	EnqueueDeliveries(ctx context.Context, ds []index.PluginDelivery, now time.Time) error
	ClaimDeliveries(ctx context.Context, pluginID string, now time.Time, limit int) ([]index.PluginDelivery, error)
	CompleteDeliveries(ctx context.Context, ids []string) error
	FailDeliveries(ctx context.Context, ids []string, lastStatus int, now time.Time) error
	ExpireDeliveries(ctx context.Context, now time.Time) (int, error)
	CountDeliveriesByPlugin(ctx context.Context) (map[string]int, error)
	NextDeliveryAt(ctx context.Context, pluginID string, now time.Time) (time.Time, error)

	PutKV(ctx context.Context, pluginID, key string, value []byte, now time.Time) error
	GetKV(ctx context.Context, pluginID, key string) (*index.KVEntry, error)
	DeleteKV(ctx context.Context, pluginID, key string) error
	ListKV(ctx context.Context, pluginID, prefix, cursor string, limit int) ([]index.KVEntry, string, error)
	CheckBlobQuota(ctx context.Context, pluginID, key string, size int64) error
	PutBlobMeta(ctx context.Context, m index.BlobMeta) error
	GetBlobMeta(ctx context.Context, pluginID, key string) (*index.BlobMeta, error)
	DeleteBlobMeta(ctx context.Context, pluginID, key string) error

	BeginIdempotent(ctx context.Context, pluginID, key, method, path string, now time.Time) (*index.IdempotentResponse, error)
	CompleteIdempotent(ctx context.Context, pluginID, key string, status int, body []byte) error
	ReleaseIdempotent(ctx context.Context, pluginID, key string) error
	PurgeIdempotency(ctx context.Context, now time.Time) (int, error)

	CreateActingToken(ctx context.Context, tok *index.ActingToken, now time.Time) error
	CreateJobActingToken(ctx context.Context, tok *index.ActingToken, expiresAt, now time.Time) error
	ResolveActingToken(ctx context.Context, hash string, now time.Time) (*index.ActingToken, error)
	RevokeInteractionTokens(ctx context.Context, pluginID, interaction string) (int, error)
	PurgeActingTokens(ctx context.Context, now time.Time) (int, error)

	UninstallPlugin(ctx context.Context, id string, opts index.UninstallOptions, now time.Time) error
	DeleteSchedules(ctx context.Context, kinds []string) error
	DeletePluginUsage(ctx context.Context, pluginID string) (int, error)
	AIUsageReport(ctx context.Context, q core.AIUsageQuery) ([]core.AIUsageRow, core.AIUsageRow, error)
}

var _ Store = (*index.DB)(nil)

// EventFilters builds the access filter of a plugin actor. *app.App
// implements it.
type EventFilters interface {
	NewEventFilter(ctx context.Context, actor *core.Actor) (core.EventFilter, error)
}

// Timeouts and intervals from docs/plugins.md.
const (
	DefaultCallTimeout   = 10 * time.Second
	DefaultActionTimeout = 30 * time.Second
	DefaultJobTimeout    = 30 * time.Second
	DefaultHealthEvery   = 60 * time.Second
	DefaultExpireEvery   = 10 * time.Minute
	DefaultPurgeEvery    = time.Hour
	manifestTimeout      = 10 * time.Second
	maxManifestBytes     = 1 << 20
	maxResponseBytes     = 4 << 20
	fetchTimeout         = 20 * time.Second
	fetchMaxBytes        = 10 << 20
)

// Options configures a Host. Store, Backend, Registry, Engine and Bus are
// required.
type Options struct {
	Store    Store
	Backend  core.Backend
	Registry *module.Registry
	Engine   *module.Engine
	Bus      *module.Bus
	// Filters builds the grant filter of event deliveries. Nil delivers
	// events of granted spaces by space id alone.
	Filters EventFilters
	// DataDir is the data directory. Blobs live under
	// <DataDir>/internal/plugins/<id>/blobs/.
	DataDir string
	// MarkspaceURL is the address sent to plugins in the install payload.
	MarkspaceURL string
	// Client calls plugins and fetches manifests. It may reach private
	// addresses. Nil means a client with no global timeout.
	Client *http.Client
	// FetchAllowPrivate lets the host API web fetch reach private addresses.
	// Tests use it.
	FetchAllowPrivate bool
	// Now is the clock of queues, tokens and health. Signatures always use
	// the real clock, because the plugin checks them against its own.
	Now    func() time.Time
	Logger *slog.Logger

	CallTimeout   time.Duration
	ActionTimeout time.Duration
	JobTimeout    time.Duration
	HealthEvery   time.Duration
	ExpireEvery   time.Duration
	PurgeEvery    time.Duration
	// RateLimit and Burst set the host API token bucket per plugin. The
	// defaults are 20 per second and 100.
	RateLimit float64
	Burst     int
	// FetchHostInterval is the minimum time between two web fetches to one
	// host. The default is 1 s.
	FetchHostInterval time.Duration
}

// Host runs installed plugins.
type Host struct {
	opts    Options
	store   Store
	log     *slog.Logger
	now     func() time.Time
	client  *http.Client
	fetcher *fetch.Fetcher
	robotsF *fetch.Fetcher

	mu      sync.Mutex
	states  map[string]*pluginState
	waiters map[string]*jobWaiter
	buckets map[string]*bucket
	hosts   map[string]time.Time
	robots  map[string]*robotsEntry
	runCtx  context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// pluginState is one loaded plugin.
type pluginState struct {
	row        *index.Plugin
	manifest   sdk.Manifest // the approved manifest
	identity   *core.PluginIdentity
	registered bool
	unsub      func()
	wake       chan struct{}
	stopSender context.CancelFunc
	filter     core.EventFilter
}

// ModuleID returns the registry module id of a plugin.
func ModuleID(pluginID string) string { return "plugin." + pluginID }

// JobKind returns the registry job kind of a plugin job.
func JobKind(pluginID, kind string) string { return "plugin." + pluginID + "." + kind }

// New returns a host. Call Start to load the installed plugins and run the
// delivery, health and maintenance loops.
func New(opts Options) (*Host, error) {
	switch {
	case opts.Store == nil:
		return nil, errors.New("plugin: Store is required")
	case opts.Backend == nil:
		return nil, errors.New("plugin: Backend is required")
	case opts.Registry == nil:
		return nil, errors.New("plugin: Registry is required")
	case opts.Engine == nil:
		return nil, errors.New("plugin: Engine is required")
	case opts.Bus == nil:
		return nil, errors.New("plugin: Bus is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Client == nil {
		opts.Client = &http.Client{}
	}
	defaults := []struct {
		v *time.Duration
		d time.Duration
	}{
		{&opts.CallTimeout, DefaultCallTimeout}, {&opts.ActionTimeout, DefaultActionTimeout},
		{&opts.JobTimeout, DefaultJobTimeout}, {&opts.HealthEvery, DefaultHealthEvery},
		{&opts.ExpireEvery, DefaultExpireEvery}, {&opts.PurgeEvery, DefaultPurgeEvery},
		{&opts.FetchHostInterval, time.Second},
	}
	for _, d := range defaults {
		if *d.v <= 0 {
			*d.v = d.d
		}
	}
	if opts.RateLimit <= 0 {
		opts.RateLimit = 20
	}
	if opts.Burst <= 0 {
		opts.Burst = 100
	}
	h := &Host{
		opts:    opts,
		store:   opts.Store,
		log:     opts.Logger,
		now:     func() time.Time { return opts.Now().UTC() },
		client:  opts.Client,
		states:  map[string]*pluginState{},
		waiters: map[string]*jobWaiter{},
		buckets: map[string]*bucket{},
		hosts:   map[string]time.Time{},
		robots:  map[string]*robotsEntry{},
	}
	h.fetcher = fetch.New(fetch.Options{
		AllowPrivate: opts.FetchAllowPrivate, Timeout: fetchTimeout, MaxBytes: fetchMaxBytes, AnyType: true,
		KeepErrorStatus: true, UserAgent: "Markspace/1 (plugin fetcher)",
	})
	h.robotsF = fetch.New(fetch.Options{
		AllowPrivate: opts.FetchAllowPrivate, Timeout: 10 * time.Second, MaxBytes: 512 << 10, AnyType: true,
		UserAgent: "Markspace/1 (plugin fetcher)",
	})
	return h, nil
}

// Start loads every installed plugin and starts the loops. It implements
// app.Service.
func (h *Host) Start(ctx context.Context) error {
	h.mu.Lock()
	if h.runCtx != nil {
		h.mu.Unlock()
		return nil
	}
	h.runCtx, h.cancel = context.WithCancel(context.WithoutCancel(ctx))
	h.mu.Unlock()
	if err := h.Load(ctx); err != nil {
		return err
	}
	h.mu.Lock()
	for _, st := range h.states {
		h.startSenderLocked(st)
	}
	h.mu.Unlock()
	h.loop(h.opts.HealthEvery, func(ctx context.Context) { h.CheckHealth(ctx) })
	h.loop(h.opts.ExpireEvery, func(ctx context.Context) { h.ExpireDeliveries(ctx) })
	h.loop(h.opts.PurgeEvery, func(ctx context.Context) { h.Purge(ctx) })
	return nil
}

// loop runs fn every interval until the host stops.
func (h *Host) loop(every time.Duration, fn func(context.Context)) {
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-h.runCtx.Done():
				return
			case <-t.C:
				fn(h.runCtx)
			}
		}
	}()
}

// Stop stops the loops and the senders and removes the event subscriptions.
// It implements app.Service.
func (h *Host) Stop(ctx context.Context) error {
	h.mu.Lock()
	cancel := h.cancel
	for _, st := range h.states {
		if st.unsub != nil {
			st.unsub()
			st.unsub = nil
		}
	}
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Load activates every plugin that is not uninstalled. A plugin whose stored
// manifest no longer loads is logged and skipped.
func (h *Host) Load(ctx context.Context) error {
	rows, err := h.store.ListPlugins(ctx)
	if err != nil {
		return fmt.Errorf("list plugins: %w", err)
	}
	for _, row := range rows {
		if row.Status == core.PluginStatusUninstalled {
			continue
		}
		h.mu.Lock()
		_, loaded := h.states[row.ID]
		h.mu.Unlock()
		if loaded {
			continue
		}
		if err := h.activate(ctx, row); err != nil {
			h.log.Error("load plugin failed", "plugin", row.ID, "err", err)
		}
	}
	return nil
}

// activate loads a plugin row into memory, subscribes it to events, and adds
// its module when its status lets it run.
func (h *Host) activate(ctx context.Context, row *index.Plugin) error {
	man, err := sdk.ParseManifest(row.ApprovedManifest)
	if err != nil {
		return fmt.Errorf("approved manifest: %w", err)
	}
	id, err := h.identityOf(ctx, row)
	if err != nil {
		return err
	}
	st := &pluginState{row: row, manifest: man, identity: id, wake: make(chan struct{}, 1)}
	h.mu.Lock()
	if old := h.states[row.ID]; old != nil {
		h.mu.Unlock()
		return fmt.Errorf("plugin %s is already loaded: %w", row.ID, core.ErrConflict)
	}
	h.states[row.ID] = st
	h.mu.Unlock()
	st.unsub = h.opts.Bus.Handle(ModuleID(row.ID), "*", h.onEvent(row.ID))
	if core.PluginStatusRuns(row.Status) {
		if err := h.register(st); err != nil {
			h.log.Error("register plugin module failed", "plugin", row.ID, "err", err)
		}
	}
	h.mu.Lock()
	h.startSenderLocked(st)
	h.mu.Unlock()
	return nil
}

// deactivate removes a plugin from memory, the registry and the bus.
func (h *Host) deactivate(id string) {
	h.mu.Lock()
	st := h.states[id]
	delete(h.states, id)
	h.mu.Unlock()
	if st == nil {
		return
	}
	h.unregister(st)
	if st.unsub != nil {
		st.unsub()
	}
	if st.stopSender != nil {
		st.stopSender()
	}
}

// register adds the plugin's module to the registry.
func (h *Host) register(st *pluginState) error {
	h.mu.Lock()
	if st.registered {
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()
	if err := h.opts.Registry.Add(h.buildModule(st.row.ID, st.manifest)); err != nil {
		return err
	}
	h.mu.Lock()
	st.registered = true
	h.mu.Unlock()
	return nil
}

// unregister removes the plugin's module from the registry.
func (h *Host) unregister(st *pluginState) {
	h.mu.Lock()
	was := st.registered
	st.registered = false
	h.mu.Unlock()
	if !was {
		return
	}
	if err := h.opts.Registry.Unregister(ModuleID(st.row.ID)); err != nil && !errors.Is(err, core.ErrNotFound) {
		h.log.Warn("unregister plugin module failed", "plugin", st.row.ID, "err", err)
	}
}

// state returns a loaded plugin.
func (h *Host) state(id string) *pluginState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.states[id]
}

// snapshot returns copies of the fields callers read without the lock.
func (h *Host) snapshot(id string) (*index.Plugin, sdk.Manifest, *core.PluginIdentity, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.states[id]
	if st == nil {
		return nil, sdk.Manifest{}, nil, false
	}
	row := *st.row
	return &row, st.manifest, st.identity, true
}

// reload rereads a loaded plugin's row and grants, so identity, status and
// filters follow a change.
func (h *Host) reload(ctx context.Context, id string) error {
	row, err := h.store.GetPlugin(ctx, id)
	if err != nil {
		return err
	}
	ident, err := h.identityOf(ctx, row)
	if err != nil {
		return err
	}
	man, err := sdk.ParseManifest(row.ApprovedManifest)
	if err != nil {
		return fmt.Errorf("approved manifest: %w", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.states[id]
	if st == nil {
		return nil
	}
	st.row, st.identity, st.manifest, st.filter = row, ident, man, nil
	return nil
}

// identityOf builds the identity of a plugin row from its grants.
func (h *Host) identityOf(ctx context.Context, row *index.Plugin) (*core.PluginIdentity, error) {
	grants, err := h.store.ListPluginGrants(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	id := &core.PluginIdentity{
		ID: row.ID, Name: row.Name, UserID: row.UserID, Status: row.Status,
		Permissions: append([]string(nil), row.Permissions...), Grants: map[string]core.Role{},
	}
	for _, g := range grants {
		id.Grants[g.SpaceID] = core.Role(g.Role)
	}
	sort.Strings(id.Permissions)
	return id, nil
}

// pluginActor returns the actor of a plugin token for a loaded plugin.
func (h *Host) pluginActor(ctx context.Context, id string) (*core.Actor, error) {
	row, _, ident, ok := h.snapshot(id)
	if !ok {
		return nil, fmt.Errorf("plugin %s: %w", id, core.ErrNotFound)
	}
	u, err := h.store.GetUser(ctx, row.UserID)
	if err != nil {
		return nil, fmt.Errorf("plugin user: %w", err)
	}
	return &core.Actor{User: u, Plugin: ident}, nil
}

// pluginOf returns the plugin id of a plugin actor.
func pluginOf(actor *core.Actor) (string, error) {
	if actor == nil || actor.Plugin == nil {
		return "", fmt.Errorf("a plugin token is required: %w", core.ErrUnauthorized)
	}
	return actor.Plugin.ID, nil
}
