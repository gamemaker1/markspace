package module

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// JobHandlerSource looks up job handlers. *Registry implements it.
type JobHandlerSource interface {
	JobHandler(kind string) (JobRegistration, bool)
	JobKinds() []string
}

// EngineOptions configures an Engine. Zero values select the defaults.
type EngineOptions struct {
	Store    JobStore
	Handlers JobHandlerSource
	// Events receives job.updated events. Nil disables the events.
	Events Publisher
	// Workers is the number of worker goroutines. The default is 4.
	Workers int
	// Lease is the lease a claim and each progress update grant. The default
	// is 60 s.
	Lease time.Duration
	// Backoff lists the retry delays. The default is DefaultBackoff.
	Backoff []time.Duration
	// MaxQueued caps the queued, running and paused jobs of one module. The
	// default is 10 000.
	MaxQueued int
	// Retention is the age after which finished jobs are deleted. The default
	// is 30 days.
	Retention time.Duration
	// CleanupEvery is the minimum time between retention runs. The default is
	// 1 h.
	CleanupEvery time.Duration
	// Poll is the real-time interval of idle workers and of Tick. The default
	// is 1 s.
	Poll   time.Duration
	Now    func() time.Time
	Logger *slog.Logger
}

// Engine runs jobs from a JobStore with a pool of workers.
type Engine struct {
	store        JobStore
	handlers     JobHandlerSource
	events       Publisher
	workers      int
	lease        time.Duration
	backoff      []time.Duration
	maxQueued    int
	retention    time.Duration
	cleanupEvery time.Duration
	poll         time.Duration
	now          func() time.Time
	log          *slog.Logger

	mu          sync.Mutex
	running     map[runKey]context.CancelCauseFunc
	wakeCh      chan struct{}
	lastCleanup time.Time
	stop        context.CancelCauseFunc
	started     bool
	wg          sync.WaitGroup
}

type runKey struct {
	id      string
	attempt int
}

// NewEngine returns an engine. Call Start to run workers, or drive it with
// RunOnce and Tick in tests.
func NewEngine(opts EngineOptions) *Engine {
	e := &Engine{
		store:        opts.Store,
		handlers:     opts.Handlers,
		events:       opts.Events,
		workers:      opts.Workers,
		lease:        opts.Lease,
		backoff:      slices.Clone(opts.Backoff),
		maxQueued:    opts.MaxQueued,
		retention:    opts.Retention,
		cleanupEvery: opts.CleanupEvery,
		poll:         opts.Poll,
		now:          opts.Now,
		log:          opts.Logger,
		running:      map[runKey]context.CancelCauseFunc{},
		wakeCh:       make(chan struct{}),
	}
	if e.workers <= 0 {
		e.workers = DefaultWorkers
	}
	if e.lease <= 0 {
		e.lease = DefaultLease
	}
	if len(e.backoff) == 0 {
		e.backoff = slices.Clone(DefaultBackoff)
	}
	if e.maxQueued <= 0 {
		e.maxQueued = DefaultMaxQueued
	}
	if e.retention <= 0 {
		e.retention = DefaultRetention
	}
	if e.cleanupEvery <= 0 {
		e.cleanupEvery = time.Hour
	}
	if e.poll <= 0 {
		e.poll = time.Second
	}
	if e.now == nil {
		e.now = utcNow
	}
	if e.log == nil {
		e.log = slog.Default()
	}
	return e
}

// Store returns the engine's store.
func (e *Engine) Store() JobStore { return e.store }

// Start launches the workers and the maintenance loop. They stop when ctx is
// canceled or Stop is called.
func (e *Engine) Start(ctx context.Context) {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return
	}
	e.started = true
	ctx, cancel := context.WithCancelCause(ctx)
	e.stop = cancel
	e.mu.Unlock()
	for range e.workers {
		e.wg.Add(1)
		go e.worker(ctx)
	}
	e.wg.Add(1)
	go e.maintain(ctx)
}

// Stop cancels running handlers with ErrEngineStopped and waits for the
// workers until ctx ends. An interrupted attempt is queued again at once.
func (e *Engine) Stop(ctx context.Context) error {
	e.mu.Lock()
	stop := e.stop
	e.mu.Unlock()
	if stop != nil {
		stop(ErrEngineStopped)
	}
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) worker(ctx context.Context) {
	defer e.wg.Done()
	for {
		wake := e.wakeChan()
		ran, err := e.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			e.log.Error("job claim failed", "err", err)
		}
		if ran {
			continue
		}
		t := time.NewTimer(e.poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-wake:
			t.Stop()
		case <-t.C:
		}
	}
}

func (e *Engine) maintain(ctx context.Context) {
	defer e.wg.Done()
	t := time.NewTicker(e.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.Tick(ctx); err != nil && ctx.Err() == nil {
				e.log.Error("job maintenance failed", "err", err)
			}
		}
	}
}

func (e *Engine) wakeChan() <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.wakeCh
}

func (e *Engine) notify() {
	e.mu.Lock()
	close(e.wakeCh)
	e.wakeCh = make(chan struct{})
	e.mu.Unlock()
}

// Enqueue creates a job of a registered kind. The module comes from the
// registration. It returns a wrapped core.ErrNotFound for an unknown kind and
// ErrQueueFull when the module is at its cap.
func (e *Engine) Enqueue(ctx context.Context, req EnqueueRequest) (*Job, error) {
	reg, ok := e.handlers.JobHandler(req.Kind)
	if !ok {
		return nil, fmt.Errorf("job kind %q: %w", req.Kind, core.ErrNotFound)
	}
	maxAttempts := req.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = reg.Options.MaxAttempts
	}
	title := req.Title
	if title == "" {
		title = reg.Options.Title
	}
	j := &Job{
		ID:          req.ID,
		Kind:        req.Kind,
		Module:      reg.Module,
		SpaceID:     req.SpaceID,
		CreatedBy:   req.CreatedBy,
		Title:       title,
		Payload:     req.Payload,
		MaxAttempts: maxAttempts,
		RunAfter:    req.RunAfter,
		ParentID:    req.ParentID,
		CreatedAt:   e.now(),
	}
	if err := e.store.Enqueue(ctx, j, e.maxQueued); err != nil {
		return nil, err
	}
	e.publish(j)
	if j.ParentID != "" {
		e.publishJob(ctx, j.ParentID)
	}
	e.notify()
	return j, nil
}

// Cancel cancels a job and its unfinished descendants, and cancels the
// contexts of their running handlers with ErrJobCanceled.
func (e *Engine) Cancel(ctx context.Context, id string) error {
	changed, err := e.store.Cancel(ctx, id, e.now())
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, j := range changed {
		ids[j.ID] = true
	}
	parents := map[string]bool{}
	for _, j := range changed {
		e.cancelRuns(j.ID, -1, ErrJobCanceled)
		e.publish(j)
		if j.ParentID != "" && !ids[j.ParentID] {
			parents[j.ParentID] = true
		}
	}
	for p := range parents {
		e.publishJob(ctx, p)
	}
	return nil
}

// CancelModule cancels every unfinished job of a module and returns the
// number of jobs canceled.
func (e *Engine) CancelModule(ctx context.Context, module string) (int, error) {
	jobs, err := e.store.List(ctx, JobFilter{Module: module, Statuses: ActiveJobStatuses})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, j := range jobs {
		changed, err := e.store.Cancel(ctx, j.ID, e.now())
		if err != nil {
			if errors.Is(err, core.ErrConflict) || errors.Is(err, core.ErrNotFound) {
				continue
			}
			return n, err
		}
		for _, c := range changed {
			e.cancelRuns(c.ID, -1, ErrJobCanceled)
			e.publish(c)
		}
		n += len(changed)
	}
	return n, nil
}

// Pause pauses a queued job or requests a pause of a running one.
func (e *Engine) Pause(ctx context.Context, id string) (*Job, error) {
	j, err := e.store.Pause(ctx, id, e.now())
	if err != nil {
		return nil, err
	}
	e.publish(j)
	return j, nil
}

// Resume queues a paused job or withdraws a pause request.
func (e *Engine) Resume(ctx context.Context, id string) (*Job, error) {
	j, err := e.store.Resume(ctx, id, e.now())
	if err != nil {
		return nil, err
	}
	e.publish(j)
	e.notify()
	return j, nil
}

// Tick requeues jobs with expired leases, cancels their local handlers with
// ErrLeaseExpired, and deletes old finished jobs at most once per
// CleanupEvery. The maintenance loop calls it every Poll interval.
func (e *Engine) Tick(ctx context.Context) error {
	now := e.now()
	expired, err := e.store.RequeueExpired(ctx, now)
	if err != nil {
		return err
	}
	for _, j := range expired {
		e.cancelRuns(j.ID, j.Attempts, ErrLeaseExpired)
		e.publish(j)
		if j.ParentID != "" && j.Status.Finished() {
			e.publishJob(ctx, j.ParentID)
		}
		e.log.Warn("job lease expired", "job", j.ID, "kind", j.Kind, "attempt", j.Attempts, "status", j.Status)
	}
	if len(expired) > 0 {
		e.notify()
	}
	e.mu.Lock()
	due := e.lastCleanup.IsZero() || now.Sub(e.lastCleanup) >= e.cleanupEvery
	if due {
		e.lastCleanup = now
	}
	e.mu.Unlock()
	if due {
		n, err := e.store.DeleteFinishedBefore(ctx, now.Add(-e.retention))
		if err != nil {
			return err
		}
		if n > 0 {
			e.log.Info("deleted finished jobs", "count", n)
		}
	}
	return nil
}

// cancelRuns cancels local handlers of a job. attempt -1 matches every attempt.
func (e *Engine) cancelRuns(id string, attempt int, cause error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, cancel := range e.running {
		if k.id == id && (attempt < 0 || k.attempt == attempt) {
			cancel(cause)
		}
	}
}

// Running reports how many handlers run in this process.
func (e *Engine) Running() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.running)
}

// RunOnce claims one ready job and runs its handler on the calling goroutine.
// It reports whether a job was claimed.
func (e *Engine) RunOnce(ctx context.Context) (bool, error) {
	if ctx.Err() != nil {
		return false, nil
	}
	kinds := e.handlers.JobKinds()
	if len(kinds) == 0 {
		return false, nil
	}
	job, err := e.store.Claim(ctx, e.now(), e.lease, kinds)
	if err != nil || job == nil {
		return false, err
	}
	e.publish(job)
	reg, ok := e.handlers.JobHandler(job.Kind)
	if !ok {
		e.finish(ctx, job, nil, fmt.Errorf("no handler for job kind %q", job.Kind), nil)
		return true, nil
	}
	hctx, cancel := context.WithCancelCause(ctx)
	key := runKey{id: job.ID, attempt: job.Attempts}
	e.mu.Lock()
	e.running[key] = cancel
	e.mu.Unlock()

	rt := &jobRuntime{e: e, job: job.Clone(), cancel: cancel}
	result, herr := e.call(hctx, reg, job.Clone(), rt)

	e.mu.Lock()
	delete(e.running, key)
	e.mu.Unlock()
	cause := context.Cause(hctx)
	cancel(nil)
	e.finish(ctx, job, result, herr, cause)
	return true, nil
}

func (e *Engine) call(ctx context.Context, reg JobRegistration, job *Job, rt JobRuntime) (result json.RawMessage, err error) {
	defer func() {
		if v := recover(); v != nil {
			e.log.Error("job handler panicked", "job", job.ID, "kind", job.Kind, "module", reg.Module,
				"panic", fmt.Sprint(v), "stack", string(debug.Stack()))
			result, err = nil, fmt.Errorf("job handler panicked: %v", v)
		}
	}()
	return reg.Handler(ctx, job, rt)
}

func (e *Engine) finish(ctx context.Context, job *Job, result json.RawMessage, herr, cause error) {
	switch {
	case errors.Is(cause, ErrJobCanceled), errors.Is(cause, ErrLeaseExpired), errors.Is(cause, ErrLeaseLost):
		return
	}
	sctx := context.WithoutCancel(ctx)
	now := e.now()
	var (
		j   *Job
		err error
	)
	switch {
	case herr == nil:
		j, err = e.store.Complete(sctx, job.ID, job.Attempts, result, now)
	case errors.Is(cause, ErrEngineStopped):
		j, err = e.store.Fail(sctx, job.ID, job.Attempts, "interrupted by shutdown", now, now)
	case IsPermanent(herr):
		j, err = e.store.Fail(sctx, job.ID, job.Attempts, herr.Error(), time.Time{}, now)
	default:
		j, err = e.store.Fail(sctx, job.ID, job.Attempts, herr.Error(), now.Add(RetryDelay(e.backoff, job.Attempts)), now)
	}
	if err != nil {
		if errors.Is(err, core.ErrConflict) {
			e.log.Debug("job attempt ended after losing its lease", "job", job.ID, "attempt", job.Attempts)
		} else {
			e.log.Error("job state update failed", "job", job.ID, "attempt", job.Attempts, "err", err)
		}
		return
	}
	if herr != nil {
		e.log.Warn("job attempt failed", "job", j.ID, "kind", j.Kind, "module", j.Module,
			"attempt", job.Attempts, "status", j.Status, "err", herr)
	}
	e.publish(j)
	if j.ParentID != "" && j.Status.Finished() {
		e.publishJob(sctx, j.ParentID)
	}
}

func (e *Engine) publishJob(ctx context.Context, id string) {
	if e.events == nil {
		return
	}
	j, err := e.store.Get(ctx, id)
	if err != nil {
		return
	}
	e.publish(j)
}

// JobUpdatedData is the data of a job.updated event. Progress and Total are
// the display progress, so a parent without its own total counts finished
// children. Message is always present, and an empty string clears the
// message a client shows.
type JobUpdatedData struct {
	JobID    string    `json:"jobId"`
	Status   JobStatus `json:"status"`
	Title    string    `json:"title"`
	Message  string    `json:"message"`
	Progress int64     `json:"progress"`
	Total    int64     `json:"total"`
}

func (e *Engine) publish(j *Job) {
	if e.events == nil || j == nil {
		return
	}
	p, t := j.DisplayProgress()
	data, _ := json.Marshal(JobUpdatedData{JobID: j.ID, Status: j.Status, Title: j.Title, Message: j.Message, Progress: p, Total: t})
	ev := Event{ID: core.NewID(), Type: EventJobUpdated, SpaceID: j.SpaceID, Data: data, At: e.now()}
	if core.ValidID(j.CreatedBy) {
		ev.ActorID = j.CreatedBy
	}
	e.events.Publish(ev)
}

type jobRuntime struct {
	e      *Engine
	job    *Job
	cancel context.CancelCauseFunc
}

func (rt *jobRuntime) Progress(ctx context.Context, p JobProgress) error {
	j, err := rt.e.store.Progress(ctx, rt.job.ID, rt.job.Attempts, p, rt.e.now(), rt.e.lease)
	if err != nil {
		if errors.Is(err, ErrLeaseLost) {
			rt.lost(ctx)
		}
		return err
	}
	rt.e.publish(j)
	return nil
}

func (rt *jobRuntime) lost(ctx context.Context) {
	j, err := rt.e.store.Get(context.WithoutCancel(ctx), rt.job.ID)
	if err == nil && j.Status == JobCanceled {
		rt.cancel(ErrJobCanceled)
		return
	}
	rt.cancel(ErrLeaseLost)
}

func (rt *jobRuntime) Log(ctx context.Context, level, message string) error {
	return rt.e.store.Log(ctx, JobLogEntry{JobID: rt.job.ID, At: rt.e.now(), Level: level, Message: truncate(message, MaxJobLogMessage)})
}

func (rt *jobRuntime) EnqueueChild(ctx context.Context, req EnqueueRequest) (*Job, error) {
	req.ParentID = rt.job.ID
	if req.SpaceID == "" {
		req.SpaceID = rt.job.SpaceID
	}
	if req.CreatedBy == "" {
		req.CreatedBy = rt.job.CreatedBy
	}
	return rt.e.Enqueue(ctx, req)
}
