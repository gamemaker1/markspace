package module

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// ScheduleActor is the CreatedBy value of jobs the scheduler enqueues.
const ScheduleActor = "schedule"

// SchedulerOptions configures a Scheduler.
type SchedulerOptions struct {
	Engine *Engine
	// Store holds the last runs. The default is the engine's store.
	Store  JobStore
	Now    func() time.Time
	Logger *slog.Logger
}

// Scheduler enqueues jobs for the registered schedules.
type Scheduler struct {
	engine *Engine
	store  JobStore
	now    func() time.Time
	log    *slog.Logger

	mu        sync.Mutex
	specs     []ScheduleSpec
	firstSeen map[string]time.Time
}

// NewScheduler returns a scheduler with no schedules.
func NewScheduler(opts SchedulerOptions) *Scheduler {
	s := &Scheduler{
		engine:    opts.Engine,
		store:     opts.Store,
		now:       opts.Now,
		log:       opts.Logger,
		firstSeen: map[string]time.Time{},
	}
	if s.store == nil && s.engine != nil {
		s.store = s.engine.Store()
	}
	if s.now == nil {
		s.now = utcNow
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s
}

// SetSchedules replaces the schedules. The wiring step calls it with
// Registry.Schedules from a Registry.Watch callback.
func (s *Scheduler) SetSchedules(specs []ScheduleSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.specs = slices.Clone(specs)
	for kind := range s.firstSeen {
		if !slices.ContainsFunc(specs, func(sp ScheduleSpec) bool { return sp.JobKind == kind }) {
			delete(s.firstSeen, kind)
		}
	}
}

// JitterOffset returns a deterministic offset in [0, jitter) for a schedule
// period that starts at base.
func JitterOffset(kind string, base time.Time, jitter time.Duration) time.Duration {
	if jitter <= 0 {
		return 0
	}
	h := fnv.New64a()
	h.Write([]byte(kind))
	h.Write([]byte{0})
	if !base.IsZero() {
		h.Write([]byte(base.UTC().Format(time.RFC3339Nano)))
	}
	return time.Duration(h.Sum64() % uint64(jitter))
}

// NextRun returns when a schedule is next due.
func (s *Scheduler) NextRun(ctx context.Context, spec ScheduleSpec) (time.Time, error) {
	last, err := s.store.LastRun(ctx, spec.JobKind)
	if err != nil {
		return time.Time{}, err
	}
	if last.IsZero() {
		s.mu.Lock()
		first, ok := s.firstSeen[spec.JobKind]
		if !ok {
			first = s.now()
			s.firstSeen[spec.JobKind] = first
		}
		s.mu.Unlock()
		return first.Add(JitterOffset(spec.JobKind, time.Time{}, spec.Jitter)), nil
	}
	return last.Add(spec.Every + JitterOffset(spec.JobKind, last, spec.Jitter)), nil
}

// Tick enqueues a job for every due schedule and returns the kinds enqueued.
// A due schedule whose previous job is still queued, running or paused is
// skipped, and the skip counts as its run.
func (s *Scheduler) Tick(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	specs := slices.Clone(s.specs)
	s.mu.Unlock()
	now := s.now()
	var (
		enqueued []string
		errs     []error
	)
	for _, sp := range specs {
		next, err := s.NextRun(ctx, sp)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if now.Before(next) {
			continue
		}
		active, err := s.store.List(ctx, JobFilter{Kind: sp.JobKind, CreatedBy: ScheduleActor, Statuses: ActiveJobStatuses, Limit: 1})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(active) == 0 {
			if _, err := s.engine.Enqueue(ctx, EnqueueRequest{Kind: sp.JobKind, CreatedBy: ScheduleActor}); err != nil {
				errs = append(errs, err)
				continue
			}
			enqueued = append(enqueued, sp.JobKind)
		} else {
			s.log.Info("scheduled job skipped, previous run still active", "kind", sp.JobKind, "job", active[0].ID)
		}
		if err := s.store.SetLastRun(ctx, sp.JobKind, now); err != nil {
			errs = append(errs, err)
		}
	}
	return enqueued, errors.Join(errs...)
}

// Run calls Tick every interval until ctx ends. The default interval is 30 s.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("schedule tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
