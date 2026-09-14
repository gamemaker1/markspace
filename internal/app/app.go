// Package app implements core.Backend on top of the four ports: the markdown
// file store, the index database, the markdown renderer and the AI factory.
//
// Markdown files are the source of truth for note bodies. The index database
// holds derived fields plus the state that has no file representation, such as
// accounts, shares and comments.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/config"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/ports"
)

// Hasher hashes and verifies passwords.
type Hasher interface {
	Hash(plain string) (string, error)
	Verify(encoded, plain string) (bool, error)
	HashToken(plain string) string
	NewSessionToken() (plaintext, hash string)
	NewAPIToken() (plaintext, hash, prefix string)
	ValidatePassword(plain string) error
	NormalizeEmail(s string) string
	ValidateEmail(s string) error
}

// Deps are the collaborators App needs. Files, DB, Renderer, Hasher, Config
// and Logger are required. A nil AI disables the AI service, which then
// returns core.ErrUnsupported.
type Deps struct {
	Files    ports.Files
	DB       ports.DB
	Renderer ports.Renderer
	AI       ports.AIFactory
	// PriceFetcher downloads the public price catalogs. Nil turns the
	// downloads off, and stored copies still apply.
	PriceFetcher ports.CatalogFetcher
	Hasher       Hasher
	Config       *config.Config
	Logger       *slog.Logger
	// Now is injectable for tests. Default time.Now.
	Now func() time.Time

	// Suggestions stores suggested edits. Nil turns the suggestion service
	// off, and its methods return core.ErrUnsupported.
	Suggestions ports.SuggestionDB
	// Bus, Jobs and Scheduler are the module engine from internal/module.
	// Start runs them and Close stops them. Each may be nil.
	Bus       *module.Bus
	Jobs      *module.Engine
	Scheduler *module.Scheduler
	// ScheduleEvery is the scheduler tick. Zero means 30 s.
	ScheduleEvery time.Duration
	// EventSink receives every event the application publishes, after the
	// browser event streams got it. The wiring step feeds the module bus
	// with it.
	EventSink func(core.Event)
}

// Service is a background component that runs while the application runs,
// such as the plugin host. Start must not block.
type Service interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// App implements core.Backend.
type App struct {
	files  ports.Files
	db     ports.DB
	rend   ports.Renderer
	ai     ports.AIFactory
	prices ports.CatalogFetcher
	hash   Hasher
	cfg    *config.Config
	log    *slog.Logger
	now    func() time.Time

	events *hub

	// state holds provider failures, listing outcomes and the parsed price
	// catalogs.
	state aiState
	// bg is the context of background work started by requests, canceled by
	// Close.
	bg       context.Context
	bgCancel context.CancelFunc

	startMu sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// writes records paths the application itself wrote recently, so the
	// watcher can ignore its own effects. A key ending in "/" covers a whole
	// subtree.
	writeMu sync.Mutex
	writes  map[string]time.Time

	// locks serializes structural writes and scans per space.
	locksMu sync.Mutex
	locks   map[string]*sync.Mutex

	// Scheduled rescans, and the items the watcher saw vanish.
	scanMu     sync.Mutex
	scanTimers map[string]*time.Timer
	scanDue    map[string]time.Time
	scanClosed bool
	missing    map[string]time.Time
	runCtx     context.Context

	// queueKick wakes the hash and text extraction queues.
	queueKick chan struct{}

	// dummy is a password hash used to keep the login path constant time for
	// an unknown email address.
	dummyOnce sync.Once
	dummyHash string

	statMu    sync.Mutex
	indexedAt time.Time

	suggestions   ports.SuggestionDB
	bus           *module.Bus
	jobs          *module.Engine
	scheduler     *module.Scheduler
	scheduleEvery time.Duration
	sink          func(core.Event)

	pluginMu sync.RWMutex
	plugins  core.PluginResolver

	servicesMu sync.Mutex
	services   []Service
	started    []Service
}

var _ core.Backend = (*App)(nil)

// New validates deps and builds an App. It starts no goroutine. Call Start to
// run the startup reconciliation and the file watcher.
func New(deps Deps) (*App, error) {
	if deps.Files == nil {
		return nil, fmt.Errorf("app: Files is required: %w", core.ErrInvalid)
	}
	if deps.DB == nil {
		return nil, fmt.Errorf("app: DB is required: %w", core.ErrInvalid)
	}
	if deps.Renderer == nil {
		return nil, fmt.Errorf("app: Renderer is required: %w", core.ErrInvalid)
	}
	if deps.Config == nil {
		return nil, fmt.Errorf("app: Config is required: %w", core.ErrInvalid)
	}
	if deps.Logger == nil {
		return nil, fmt.Errorf("app: Logger is required: %w", core.ErrInvalid)
	}
	if deps.Hasher == nil {
		return nil, fmt.Errorf("app: Hasher is required, supply an adapter over internal/auth: %w", core.ErrInvalid)
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	a := &App{
		files:      deps.Files,
		db:         deps.DB,
		rend:       deps.Renderer,
		ai:         deps.AI,
		prices:     deps.PriceFetcher,
		hash:       deps.Hasher,
		cfg:        deps.Config,
		log:        deps.Logger,
		now:        func() time.Time { return now().UTC() },
		events:     newHub(),
		writes:     make(map[string]time.Time),
		locks:      make(map[string]*sync.Mutex),
		scanTimers: make(map[string]*time.Timer),
		scanDue:    make(map[string]time.Time),
		missing:    make(map[string]time.Time),
		queueKick:  make(chan struct{}, 1),

		suggestions:   deps.Suggestions,
		bus:           deps.Bus,
		jobs:          deps.Jobs,
		scheduler:     deps.Scheduler,
		scheduleEvery: deps.ScheduleEvery,
		sink:          deps.EventSink,
	}
	a.bg, a.bgCancel = context.WithCancel(context.Background())
	return a, nil
}

// Start runs the startup reconciliation, the AI usage retention job and,
// when Config.WatchFiles is true, the file watcher goroutine. Calling it twice
// is an error.
func (a *App) Start(ctx context.Context) error {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	if a.cancel != nil {
		return fmt.Errorf("app: already started: %w", core.ErrConflict)
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	a.cancel = cancel
	a.scanMu.Lock()
	a.runCtx = runCtx
	a.scanMu.Unlock()

	if _, err := a.reconcile(ctx); err != nil {
		cancel()
		a.cancel = nil
		return fmt.Errorf("startup reconcile: %w", err)
	}
	if a.cfg.WatchFiles {
		ch, err := a.files.Watch(runCtx)
		if err != nil {
			a.log.Warn("file watcher unavailable", "err", err)
		} else {
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				a.watch(runCtx, ch)
			}()
		}
	}
	a.wg.Add(2)
	go func() {
		defer a.wg.Done()
		a.usageRetentionLoop(runCtx)
	}()
	go func() {
		defer a.wg.Done()
		a.catalogLoop(runCtx)
	}()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.fileQueueLoop(runCtx)
	}()
	if a.jobs != nil {
		a.jobs.Start(runCtx)
	}
	if a.scheduler != nil {
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			a.scheduler.Run(runCtx, a.scheduleEvery)
		}()
	}
	a.servicesMu.Lock()
	services := append([]Service(nil), a.services...)
	a.servicesMu.Unlock()
	for _, svc := range services {
		if err := svc.Start(runCtx); err != nil {
			return fmt.Errorf("start service: %w", err)
		}
		a.servicesMu.Lock()
		a.started = append(a.started, svc)
		a.servicesMu.Unlock()
	}
	return nil
}

// AddService registers a background component that Start starts after the
// module engine and Close stops before it. Call it before Start.
func (a *App) AddService(svc Service) {
	a.servicesMu.Lock()
	defer a.servicesMu.Unlock()
	a.services = append(a.services, svc)
}

// Close stops the watcher goroutine and waits for it. It leaves the ports open
// for their owners to close.
func (a *App) Close() error {
	a.startMu.Lock()
	cancel := a.cancel
	a.cancel = nil
	a.startMu.Unlock()
	a.stopScans()
	a.servicesMu.Lock()
	started := a.started
	a.started = nil
	a.servicesMu.Unlock()
	for i := len(started) - 1; i >= 0; i-- {
		stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		if err := started[i].Stop(stopCtx); err != nil {
			a.log.Warn("stop service failed", "err", err)
		}
		stop()
	}
	if cancel != nil {
		cancel()
	}
	if a.jobs != nil {
		stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		if err := a.jobs.Stop(stopCtx); err != nil {
			a.log.Warn("stop job engine failed", "err", err)
		}
		stop()
	}
	a.bgCancel()
	a.wg.Wait()
	if a.bus != nil {
		if err := a.bus.Close(); err != nil {
			a.log.Warn("close event bus", "err", err)
		}
	}
	a.events.close()
	return nil
}

// space loads a space row by id.
func (a *App) space(ctx context.Context, id string) (*core.Space, error) {
	if id == "" {
		return nil, fmt.Errorf("space id is empty: %w", core.ErrInvalid)
	}
	sp, err := a.db.GetSpace(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load space %s: %w", id, err)
	}
	if sp == nil {
		return nil, fmt.Errorf("load space %s: %w", id, core.ErrNotFound)
	}
	return sp, nil
}

// settings merges the configuration defaults with the stored settings row.
func (a *App) settings(ctx context.Context) (*core.Settings, error) {
	out := &core.Settings{
		InstanceName:      a.cfg.InstanceName,
		AllowSignup:       a.cfg.AllowSignup,
		AIEnabled:         a.cfg.AI.Enabled,
		AIAllowUserKeys:   a.cfg.AI.AllowUserKeys,
		AIAllowUserRoutes: true,
	}
	row, err := a.db.GetSettings(ctx)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	if row == nil || row.UpdatedAt.IsZero() {
		return out, nil
	}
	// A saved row is authoritative for the flags it carries. Empty strings
	// still fall back to the configuration file.
	out.AllowSignup = row.AllowSignup
	out.AIEnabled = row.AIEnabled
	out.AIAllowUserKeys = row.AIAllowUserKeys
	out.AIAllowUserRoutes = row.AIAllowUserRoutes
	out.UpdatedAt = row.UpdatedAt
	out.DefaultSpaceID = row.DefaultSpaceID
	out.AIMonthlyTokenBudget = row.AIMonthlyTokenBudget
	out.AIMonthlyCostBudget = row.AIMonthlyCostBudget
	if row.InstanceName != "" {
		out.InstanceName = row.InstanceName
	}
	return out, nil
}

// publish sends one event about a note, or about a space when noteID is
// empty, to the hub. It never blocks.
func (a *App) publish(typ, spaceID, noteID, actorID, path string) {
	ev := core.Event{
		Type:    typ,
		SpaceID: spaceID,
		NoteID:  noteID,
		ActorID: actorID,
		Path:    path,
		At:      a.now().UnixMilli(),
	}
	if noteID != "" {
		ev.ItemID, ev.Kind = noteID, core.KindNote
	}
	a.emit(ev)
}

// emit sends an application event to the browser event streams and to the
// event sink.
func (a *App) emit(ev core.Event) {
	a.events.publish(ev)
	if a.sink != nil {
		a.sink(ev)
	}
}

// Emit publishes an event that started outside the application, such as a
// job update or a plugin notification, to the browser event streams only.
// The event sink does not see it, so a bus that forwards events here does not
// loop.
func (a *App) Emit(ev core.Event) {
	if ev.At == 0 {
		ev.At = a.now().UnixMilli()
	}
	a.events.publish(ev)
}

// markWrite records that the application wrote a note file, so the watcher can
// ignore the resulting event.
func (a *App) markWrite(spaceSlug, relPath string) {
	now := a.now()
	key := spaceSlug + "/" + relPath
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	for k, t := range a.writes {
		if now.Sub(t) > 2*time.Second {
			delete(a.writes, k)
		}
	}
	a.writes[key] = now
}

// markTree records that the application moved, trashed or created the entry
// at relPath, which covers every event below it too.
func (a *App) markTree(spaceSlug, relPath string) {
	a.markWrite(spaceSlug, relPath)
	a.markWrite(spaceSlug, relPath+"/")
}

// selfWrote reports whether the application wrote this path, or a folder
// holding it, in the last two seconds.
func (a *App) selfWrote(spaceSlug, relPath string) bool {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	now := a.now()
	fresh := func(key string) bool {
		t, ok := a.writes[key]
		if !ok {
			return false
		}
		if now.Sub(t) > 2*time.Second {
			delete(a.writes, key)
			return false
		}
		return true
	}
	if fresh(spaceSlug + "/" + relPath) {
		return true
	}
	for dir := relPath; dir != ""; dir = dirOf(dir) {
		if fresh(spaceSlug + "/" + dir + "/") {
			return true
		}
	}
	return false
}

// writeNote writes a note file and records the write for the watcher.
func (a *App) writeNote(spaceSlug string, note *core.Note) error {
	if err := a.files.Write(spaceSlug, note); err != nil {
		return fmt.Errorf("write note %s: %w", note.Path, err)
	}
	a.markWrite(spaceSlug, note.Path)
	return nil
}

// readDisk reads the note file, which is the source of truth. A missing file
// drops the stale index row and yields core.ErrNotFound.
func (a *App) readDisk(ctx context.Context, space *core.Space, note *core.Note) (*core.Note, error) {
	disk, err := a.files.Read(space.Slug, note.Path)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			a.log.Warn("index row without file, dropping row",
				"noteId", note.ID, "space", space.Slug, "path", note.Path)
			if derr := a.db.DeleteNoteRow(ctx, note.ID); derr != nil {
				a.log.Warn("drop stale index row failed", "noteId", note.ID, "err", derr)
			}
			return nil, fmt.Errorf("read note %s: %w", note.Path, core.ErrNotFound)
		}
		return nil, fmt.Errorf("read note %s: %w", note.Path, err)
	}
	if disk == nil {
		return nil, fmt.Errorf("read note %s: %w", note.Path, core.ErrNotFound)
	}
	return disk, nil
}

// loadBody copies the body from disk onto an index row for display.
func (a *App) loadBody(ctx context.Context, space *core.Space, note *core.Note) error {
	disk, err := a.readDisk(ctx, space, note)
	if err != nil {
		return err
	}
	note.Content = disk.Content
	if note.Extra == nil {
		note.Extra = disk.Extra
	}
	return nil
}

// loadForWrite copies the fields the file owns, which are the body, the
// frontmatter tags and the extra keys, onto an index row. A following write
// then does not bake derived index fields such as inline hashtags into the
// frontmatter.
func (a *App) loadForWrite(ctx context.Context, space *core.Space, note *core.Note) error {
	disk, err := a.readDisk(ctx, space, note)
	if err != nil {
		return err
	}
	note.Content = disk.Content
	note.Tags = disk.Tags
	note.Extra = disk.Extra
	return nil
}

// decorate fills the display-only fields of a note for one actor.
func (a *App) decorate(ctx context.Context, actor *core.Actor, space *core.Space, note *core.Note) {
	if space != nil {
		note.SpaceSlug = space.Slug
	}
	if actor.IsUser() {
		starred, err := a.db.IsStarred(ctx, actor.User.ID, note.ID)
		if err != nil {
			a.log.Debug("star lookup failed", "noteId", note.ID, "err", err)
			return
		}
		note.Starred = starred
	}
}

// mergeTags combines explicit tags with inline hashtags, case insensitively,
// keeping the first spelling of each tag.
func mergeTags(explicit, inline []string) []string {
	seen := make(map[string]bool, len(explicit)+len(inline))
	out := make([]string, 0, len(explicit)+len(inline))
	add := func(list []string) {
		for _, t := range list {
			t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#"))
			if t == "" {
				continue
			}
			key := strings.ToLower(t)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, t)
		}
	}
	add(explicit)
	add(inline)
	if len(out) == 0 {
		return nil
	}
	return out
}

// pathSlug returns the note slug encoded in a relative path.
func pathSlug(relPath string) string {
	base := relPath
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".md")
}

// extraString reads a string value out of the frontmatter extras.
func extraString(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	v, ok := extra[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}
