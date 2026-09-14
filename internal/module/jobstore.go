package module

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// JobStore persists jobs, job logs and schedule state. Every method is atomic.
// MemJobStore is the reference implementation, and every implementation must
// pass conformance.Run.
//
// Worker calls (RenewLease, Progress, Complete, Fail) carry the attempt number
// returned by Claim. They return ErrLeaseLost unless the job is running with
// that attempt number, and ErrNotFound (wrapped) for an unknown job.
//
// Times are stored in UTC. Returned jobs are copies with Children and
// ChildrenFinished computed.
type JobStore interface {
	// Enqueue validates j with ValidateNewJob, assigns an ID when empty, and
	// inserts it as queued. It returns core.ErrExists for a taken ID,
	// ErrQueueFull when maxQueued > 0 and the module already has maxQueued
	// queued, running or paused jobs, a wrapped core.ErrNotFound for a missing
	// parent, and core.ErrConflict for a finished parent. On success j holds
	// the stored job.
	Enqueue(ctx context.Context, j *Job, maxQueued int) error
	// Claim picks the queued job of one of kinds with the earliest RunAfter
	// not after now, then the earliest CreatedAt, then the smallest ID. It
	// marks it running, counts the attempt and sets the lease to now+lease.
	// It returns nil when no job is ready or kinds is empty.
	Claim(ctx context.Context, now time.Time, lease time.Duration, kinds []string) (*Job, error)
	// RenewLease sets the lease to now+lease.
	RenewLease(ctx context.Context, id string, attempt int, now time.Time, lease time.Duration) error
	// Progress applies a progress update and sets the lease to now+lease.
	// Negative values and messages over MaxJobMessage bytes return
	// core.ErrInvalid.
	Progress(ctx context.Context, id string, attempt int, p JobProgress, now time.Time, lease time.Duration) (*Job, error)
	// Log appends an entry to the log of an existing job.
	Log(ctx context.Context, e JobLogEntry) error
	// Logs returns the log of a job in append order.
	Logs(ctx context.Context, id string) ([]JobLogEntry, error)
	// Complete marks the attempt done with a result ({} when empty).
	Complete(ctx context.Context, id string, attempt int, result json.RawMessage, now time.Time) (*Job, error)
	// Fail ends the attempt with a message. A zero retryAt, or an attempt
	// equal to MaxAttempts, fails the job. Otherwise the job becomes paused
	// when a pause was requested, and queued with RunAfter = retryAt when not.
	Fail(ctx context.Context, id string, attempt int, message string, retryAt, now time.Time) (*Job, error)
	// RequeueExpired ends every running attempt whose lease is not after now.
	// Such a job fails when its attempts are used up, becomes paused when a
	// pause was requested, and becomes queued with RunAfter = now otherwise.
	// It returns the changed jobs sorted by ID, with Attempts still naming the
	// expired attempt.
	RequeueExpired(ctx context.Context, now time.Time) ([]*Job, error)
	// Cancel cancels a queued, running or paused job and every unfinished
	// descendant. It returns the changed jobs, the named job first. A job
	// that is already canceled returns no jobs and no error. A done or failed
	// job returns core.ErrConflict.
	Cancel(ctx context.Context, id string, now time.Time) ([]*Job, error)
	// Pause pauses a queued job, requests a pause of a running job, and
	// leaves a paused job unchanged. A finished job returns core.ErrConflict.
	Pause(ctx context.Context, id string, now time.Time) (*Job, error)
	// Resume queues a paused job with RunAfter = now, withdraws a pause
	// request of a running job, and leaves a queued job unchanged. A finished
	// job returns core.ErrConflict.
	Resume(ctx context.Context, id string, now time.Time) (*Job, error)
	// Get returns one job.
	Get(ctx context.Context, id string) (*Job, error)
	// List returns matching jobs, newest CreatedAt first, then larger ID first.
	List(ctx context.Context, f JobFilter) ([]*Job, error)
	// CountQueued counts the queued, running and paused jobs of a module.
	CountQueued(ctx context.Context, module string) (int, error)
	// DeleteFinishedBefore deletes every job tree whose jobs are all finished
	// with UpdatedAt before cutoff, with their logs, and returns the number of
	// jobs deleted.
	DeleteFinishedBefore(ctx context.Context, cutoff time.Time) (int, error)
	// ChildrenProgress returns the number of finished children and of all
	// children of a job.
	ChildrenProgress(ctx context.Context, parentID string) (finished, total int, err error)
	// LastRun returns the last run of a schedule, or the zero time.
	LastRun(ctx context.Context, kind string) (time.Time, error)
	// SetLastRun records the last run of a schedule.
	SetLastRun(ctx context.Context, kind string, t time.Time) error
}

// MemJobStore is an in-memory JobStore protected by one mutex. It defines the
// reference semantics for other stores.
type MemJobStore struct {
	mu       sync.Mutex
	jobs     map[string]*Job
	children map[string][]string
	logs     map[string][]JobLogEntry
	lastRun  map[string]time.Time
}

// NewMemJobStore returns an empty store.
func NewMemJobStore() *MemJobStore {
	return &MemJobStore{
		jobs:     map[string]*Job{},
		children: map[string][]string{},
		logs:     map[string][]JobLogEntry{},
		lastRun:  map[string]time.Time{},
	}
}

var _ JobStore = (*MemJobStore)(nil)

func notFound(id string) error { return fmt.Errorf("job %q: %w", id, core.ErrNotFound) }

func (s *MemJobStore) view(j *Job) *Job {
	c := j.Clone()
	c.Children, c.ChildrenFinished = 0, 0
	for _, cid := range s.children[j.ID] {
		if ch, ok := s.jobs[cid]; ok {
			c.Children++
			if ch.Status.Finished() {
				c.ChildrenFinished++
			}
		}
	}
	return c
}

func (s *MemJobStore) countActiveLocked(module string) int {
	n := 0
	for _, j := range s.jobs {
		if j.Module == module && !j.Status.Finished() {
			n++
		}
	}
	return n
}

// Enqueue implements JobStore.
func (s *MemJobStore) Enqueue(ctx context.Context, j *Job, maxQueued int) error {
	stored := j.Clone()
	if err := ValidateNewJob(stored); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if stored.ID == "" {
		stored.ID = core.NewID()
		for s.jobs[stored.ID] != nil {
			stored.ID = core.NewID()
		}
	} else if _, ok := s.jobs[stored.ID]; ok {
		return fmt.Errorf("job %q: %w", stored.ID, core.ErrExists)
	}
	if stored.ParentID != "" {
		parent, ok := s.jobs[stored.ParentID]
		if !ok {
			return fmt.Errorf("parent job %q: %w", stored.ParentID, core.ErrNotFound)
		}
		if parent.Status.Finished() {
			return fmt.Errorf("parent job %q is %s: %w", stored.ParentID, parent.Status, core.ErrConflict)
		}
	}
	if maxQueued > 0 && s.countActiveLocked(stored.Module) >= maxQueued {
		return fmt.Errorf("module %q has %d queued jobs: %w", stored.Module, maxQueued, ErrQueueFull)
	}
	s.jobs[stored.ID] = stored
	if stored.ParentID != "" {
		s.children[stored.ParentID] = append(s.children[stored.ParentID], stored.ID)
	}
	*j = *s.view(stored)
	return nil
}

// Claim implements JobStore.
func (s *MemJobStore) Claim(ctx context.Context, now time.Time, lease time.Duration, kinds []string) (*Job, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *Job
	for _, j := range s.jobs {
		if j.Status != JobQueued || j.RunAfter.After(now) || j.Attempts >= j.MaxAttempts || !slices.Contains(kinds, j.Kind) {
			continue
		}
		if best == nil || claimOrder(j, best) < 0 {
			best = j
		}
	}
	if best == nil {
		return nil, nil
	}
	best.Status = JobRunning
	best.Attempts++
	best.LeaseUntil = now.Add(lease)
	best.UpdatedAt = now
	return s.view(best), nil
}

func claimOrder(a, b *Job) int {
	if c := a.RunAfter.Compare(b.RunAfter); c != 0 {
		return c
	}
	if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

func (s *MemJobStore) held(id string, attempt int) (*Job, error) {
	j, ok := s.jobs[id]
	if !ok {
		return nil, notFound(id)
	}
	if j.Status != JobRunning || j.Attempts != attempt {
		return nil, fmt.Errorf("job %q attempt %d: %w", id, attempt, ErrLeaseLost)
	}
	return j, nil
}

// RenewLease implements JobStore.
func (s *MemJobStore) RenewLease(ctx context.Context, id string, attempt int, now time.Time, lease time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.held(id, attempt)
	if err != nil {
		return err
	}
	j.LeaseUntil = now.UTC().Add(lease)
	j.UpdatedAt = now.UTC()
	return nil
}

// Progress implements JobStore.
func (s *MemJobStore) Progress(ctx context.Context, id string, attempt int, p JobProgress, now time.Time, lease time.Duration) (*Job, error) {
	switch {
	case p.Progress != nil && *p.Progress < 0:
		return nil, core.Invalid("progress", "must not be negative")
	case p.Total != nil && *p.Total < 0:
		return nil, core.Invalid("total", "must not be negative")
	case p.Message != nil && len(*p.Message) > MaxJobMessage:
		return nil, core.Invalid("message", fmt.Sprintf("is longer than %d bytes", MaxJobMessage))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.held(id, attempt)
	if err != nil {
		return nil, err
	}
	if p.Progress != nil {
		j.Progress = *p.Progress
	}
	if p.Total != nil {
		j.Total = *p.Total
	}
	if p.Message != nil {
		j.Message = *p.Message
	}
	j.LeaseUntil = now.UTC().Add(lease)
	j.UpdatedAt = now.UTC()
	return s.view(j), nil
}

// Log implements JobStore.
func (s *MemJobStore) Log(ctx context.Context, e JobLogEntry) error {
	switch {
	case !slices.Contains(JobLogLevels, e.Level):
		return core.Invalid("level", fmt.Sprintf("unknown level %q", e.Level))
	case len(e.Message) > MaxJobLogMessage:
		return core.Invalid("message", fmt.Sprintf("is longer than %d bytes", MaxJobLogMessage))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[e.JobID]; !ok {
		return notFound(e.JobID)
	}
	e.At = e.At.UTC()
	s.logs[e.JobID] = append(s.logs[e.JobID], e)
	return nil
}

// Logs implements JobStore.
func (s *MemJobStore) Logs(ctx context.Context, id string) ([]JobLogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return nil, notFound(id)
	}
	return slices.Clone(s.logs[id]), nil
}

// Complete implements JobStore.
func (s *MemJobStore) Complete(ctx context.Context, id string, attempt int, result json.RawMessage, now time.Time) (*Job, error) {
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	} else if !json.Valid(result) {
		return nil, core.Invalid("result", "must be valid JSON")
	} else if len(result) > MaxJobPayload {
		return nil, fmt.Errorf("job result exceeds %d bytes: %w", MaxJobPayload, core.ErrTooLarge)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.held(id, attempt)
	if err != nil {
		return nil, err
	}
	j.Status = JobDone
	j.Result = slices.Clone(result)
	j.LeaseUntil = time.Time{}
	j.PauseRequested = false
	j.UpdatedAt = now.UTC()
	return s.view(j), nil
}

// Fail implements JobStore.
func (s *MemJobStore) Fail(ctx context.Context, id string, attempt int, message string, retryAt, now time.Time) (*Job, error) {
	message = truncate(message, MaxJobMessage)
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.held(id, attempt)
	if err != nil {
		return nil, err
	}
	j.Message = message
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = now.UTC()
	switch {
	case retryAt.IsZero() || j.Attempts >= j.MaxAttempts:
		j.Status = JobFailed
		j.PauseRequested = false
	case j.PauseRequested:
		j.Status = JobPaused
		j.PauseRequested = false
		j.RunAfter = retryAt.UTC()
	default:
		j.Status = JobQueued
		j.RunAfter = retryAt.UTC()
	}
	return s.view(j), nil
}

// RequeueExpired implements JobStore.
func (s *MemJobStore) RequeueExpired(ctx context.Context, now time.Time) ([]*Job, error) {
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Job
	for _, j := range s.jobs {
		if j.Status != JobRunning || j.LeaseUntil.After(now) {
			continue
		}
		j.LeaseUntil = time.Time{}
		j.UpdatedAt = now
		switch {
		case j.Attempts >= j.MaxAttempts:
			j.Status = JobFailed
			j.Message = "lease expired"
		case j.PauseRequested:
			j.Status = JobPaused
			j.RunAfter = now
		default:
			j.Status = JobQueued
			j.RunAfter = now
		}
		j.PauseRequested = false
		out = append(out, j)
	}
	slices.SortFunc(out, func(a, b *Job) int { return cmp.Compare(a.ID, b.ID) })
	views := make([]*Job, len(out))
	for i, j := range out {
		views[i] = s.view(j)
	}
	return views, nil
}

// Cancel implements JobStore.
func (s *MemJobStore) Cancel(ctx context.Context, id string, now time.Time) ([]*Job, error) {
	now = now.UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, notFound(id)
	}
	switch j.Status {
	case JobCanceled:
		return nil, nil
	case JobDone, JobFailed:
		return nil, fmt.Errorf("job %q is %s: %w", id, j.Status, core.ErrConflict)
	}
	var changed []*Job
	queue := []string{id}
	for len(queue) > 0 {
		cur := s.jobs[queue[0]]
		queue = queue[1:]
		if cur == nil {
			continue
		}
		if !cur.Status.Finished() {
			cur.Status = JobCanceled
			cur.LeaseUntil = time.Time{}
			cur.PauseRequested = false
			cur.UpdatedAt = now
			changed = append(changed, cur)
		}
		queue = append(queue, s.children[cur.ID]...)
	}
	views := make([]*Job, len(changed))
	for i, c := range changed {
		views[i] = s.view(c)
	}
	return views, nil
}

// Pause implements JobStore.
func (s *MemJobStore) Pause(ctx context.Context, id string, now time.Time) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, notFound(id)
	}
	switch j.Status {
	case JobQueued:
		j.Status = JobPaused
		j.UpdatedAt = now.UTC()
	case JobRunning:
		if !j.PauseRequested {
			j.PauseRequested = true
			j.UpdatedAt = now.UTC()
		}
	case JobPaused:
	default:
		return nil, fmt.Errorf("job %q is %s: %w", id, j.Status, core.ErrConflict)
	}
	return s.view(j), nil
}

// Resume implements JobStore.
func (s *MemJobStore) Resume(ctx context.Context, id string, now time.Time) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, notFound(id)
	}
	switch j.Status {
	case JobPaused:
		j.Status = JobQueued
		j.RunAfter = now.UTC()
		j.UpdatedAt = now.UTC()
	case JobRunning:
		if j.PauseRequested {
			j.PauseRequested = false
			j.UpdatedAt = now.UTC()
		}
	case JobQueued:
	default:
		return nil, fmt.Errorf("job %q is %s: %w", id, j.Status, core.ErrConflict)
	}
	return s.view(j), nil
}

// Get implements JobStore.
func (s *MemJobStore) Get(ctx context.Context, id string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, notFound(id)
	}
	return s.view(j), nil
}

// List implements JobStore.
func (s *MemJobStore) List(ctx context.Context, f JobFilter) ([]*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Job
	for _, j := range s.jobs {
		if f.Matches(j) {
			out = append(out, j)
		}
	}
	slices.SortFunc(out, func(a, b *Job) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	views := make([]*Job, len(out))
	for i, j := range out {
		views[i] = s.view(j)
	}
	return views, nil
}

// CountQueued implements JobStore.
func (s *MemJobStore) CountQueued(ctx context.Context, module string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.countActiveLocked(module), nil
}

// DeleteFinishedBefore implements JobStore.
func (s *MemJobStore) DeleteFinishedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for id, j := range s.jobs {
		if j.ParentID != "" && s.jobs[j.ParentID] != nil {
			continue
		}
		tree := []string{id}
		ok := true
		for i := 0; i < len(tree) && ok; i++ {
			t := s.jobs[tree[i]]
			if !t.Status.Finished() || !t.UpdatedAt.Before(cutoff) {
				ok = false
			}
			for _, cid := range s.children[t.ID] {
				if s.jobs[cid] != nil {
					tree = append(tree, cid)
				}
			}
		}
		if !ok {
			continue
		}
		for _, tid := range tree {
			t := s.jobs[tid]
			delete(s.jobs, tid)
			delete(s.logs, tid)
			delete(s.children, tid)
			if t.ParentID != "" {
				s.children[t.ParentID] = slices.DeleteFunc(s.children[t.ParentID], func(c string) bool { return c == tid })
				if len(s.children[t.ParentID]) == 0 {
					delete(s.children, t.ParentID)
				}
			}
			deleted++
		}
	}
	return deleted, nil
}

// ChildrenProgress implements JobStore.
func (s *MemJobStore) ChildrenProgress(ctx context.Context, parentID string) (finished, total int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[parentID]
	if !ok {
		return 0, 0, notFound(parentID)
	}
	v := s.view(j)
	return v.ChildrenFinished, v.Children, nil
}

// LastRun implements JobStore.
func (s *MemJobStore) LastRun(ctx context.Context, kind string) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRun[kind], nil
}

// SetLastRun implements JobStore.
func (s *MemJobStore) SetLastRun(ctx context.Context, kind string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRun[kind] = t.UTC()
	return nil
}

// truncate cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}
