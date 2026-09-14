package module

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

const fuzzCap = 6

var fuzzKinds = []string{"m1.work", "m1.child", "m2.work"}

type fuzzHolder struct {
	id      string
	attempt int
}

// jobModel drives a MemJobStore through virtual workers and a real Engine
// with a manual clock, and checks the job invariants after every step.
type jobModel struct {
	t        *testing.T
	ctx      context.Context
	clock    *ManualClock
	store    *MemJobStore
	engine   *Engine
	workers  [3]*fuzzHolder
	zombies  []*fuzzHolder
	ids      []string
	seen     map[string]bool
	deleted  map[string]bool
	terminal map[string]JobStatus
	script   byte
}

func newJobModel(t *testing.T) *jobModel {
	m := &jobModel{
		t:        t,
		ctx:      context.Background(),
		clock:    NewManualClock(testBase),
		store:    NewMemJobStore(),
		seen:     map[string]bool{},
		deleted:  map[string]bool{},
		terminal: map[string]JobStatus{},
	}
	handler := func(ctx context.Context, j *Job, rt JobRuntime) (json.RawMessage, error) {
		return m.runScript(ctx, j, rt)
	}
	r := NewRegistry()
	mustAdd(t, r, New(Manifest{ID: "m1"}, func(r *Registry) error {
		r.Job("m1.work", handler, JobOptions{})
		r.Job("m1.child", handler, JobOptions{MaxAttempts: 2})
		return nil
	}))
	mustAdd(t, r, New(Manifest{ID: "m2"}, func(r *Registry) error {
		r.Job("m2.work", handler, JobOptions{MaxAttempts: 1})
		return nil
	}))
	m.engine = NewEngine(EngineOptions{
		Store: m.store, Handlers: r, Events: &recordingPublisher{}, MaxQueued: fuzzCap,
		Now: m.clock.Now, Logger: quietLogger(),
	})
	return m
}

func (m *jobModel) runScript(ctx context.Context, j *Job, rt JobRuntime) (json.RawMessage, error) {
	switch m.script % 6 {
	case 0:
		p := int64(1)
		if err := rt.Progress(ctx, JobProgress{Progress: &p}); err != nil {
			m.t.Fatalf("progress of the running job failed: %v", err)
		}
		return json.RawMessage(`{"ok":true}`), nil
	case 1:
		return nil, errors.New("retry me")
	case 2:
		return nil, Permanent(errors.New("bad input"))
	case 3:
		if _, err := rt.EnqueueChild(ctx, EnqueueRequest{Kind: "m1.child"}); err != nil && !errors.Is(err, ErrQueueFull) {
			m.t.Fatalf("enqueue child of the running job: %v", err)
		}
		return nil, nil
	case 4:
		if err := m.engine.Cancel(ctx, j.ID); err != nil {
			m.t.Fatalf("cancel of the running job: %v", err)
		}
		if !errors.Is(context.Cause(ctx), ErrJobCanceled) {
			m.t.Fatalf("cancel did not reach the handler context: cause %v", context.Cause(ctx))
		}
		return nil, ctx.Err()
	default:
		if _, err := m.engine.Pause(ctx, j.ID); err != nil {
			m.t.Fatalf("pause of the running job: %v", err)
		}
		return nil, errors.New("pause me")
	}
}

func (m *jobModel) pick(arg byte) string {
	if len(m.ids) == 0 {
		return core.NewID()
	}
	return m.ids[int(arg)%len(m.ids)]
}

func (m *jobModel) held(h *fuzzHolder) bool {
	j, err := m.store.Get(m.ctx, h.id)
	return err == nil && j.Status == JobRunning && j.Attempts == h.attempt
}

func moduleOfKind(kind string) string {
	if kind == "m2.work" {
		return "m2"
	}
	return "m1"
}

func (m *jobModel) enqueue(kind string, maxAttempts int, parent string) {
	before, _ := m.store.CountQueued(m.ctx, moduleOfKind(kind))
	var want error
	if parent != "" {
		p, err := m.store.Get(m.ctx, parent)
		switch {
		case err != nil:
			want = core.ErrNotFound
		case p.Status.Finished():
			want = core.ErrConflict
		}
	}
	if want == nil && before >= fuzzCap {
		want = ErrQueueFull
	}
	j, err := m.engine.Enqueue(m.ctx, EnqueueRequest{Kind: kind, MaxAttempts: maxAttempts, ParentID: parent})
	if want == nil {
		if err != nil {
			m.t.Fatalf("enqueue %s with %d active jobs: %v", kind, before, err)
		}
		m.remember(j.ID)
		return
	}
	if !errors.Is(err, want) {
		m.t.Fatalf("enqueue %s: got %v, want %v", kind, err, want)
	}
}

func (m *jobModel) remember(id string) {
	if !m.seen[id] {
		m.seen[id] = true
		m.ids = append(m.ids, id)
	}
}

// workerCall runs a fenced store call and checks that it succeeds exactly when
// the worker still holds the job.
func (m *jobModel) workerCall(w int, call func(h *fuzzHolder) error) {
	h := m.workers[w]
	if h == nil {
		return
	}
	want := m.held(h)
	err := call(h)
	if want != (err == nil) {
		m.t.Fatalf("worker %d on %s attempt %d: held=%v but call returned %v", w, h.id, h.attempt, want, err)
	}
	if err != nil && !errors.Is(err, ErrLeaseLost) {
		// Cleanup can delete a canceled job while a worker still holds its claim. The JobStore contract returns a
		// wrapped ErrNotFound for an unknown job.
		_, getErr := m.store.Get(m.ctx, h.id)
		if !errors.Is(err, core.ErrNotFound) || !errors.Is(getErr, core.ErrNotFound) {
			m.t.Fatalf("stale worker call returned %v, want ErrLeaseLost, or ErrNotFound for a deleted job", err)
		}
	}
}

func (m *jobModel) step(op, arg byte) {
	ctx := m.ctx
	now := m.clock.Now()
	w := int(arg) % len(m.workers)
	switch op % 14 {
	case 0:
		m.enqueue(fuzzKinds[int(arg)%len(fuzzKinds)], int(arg>>2)%4, "")
	case 1:
		if h := m.workers[w]; h != nil {
			// The worker crashes and keeps nothing but its stale claim.
			m.zombies = append(m.zombies, h)
			m.workers[w] = nil
			return
		}
		j, err := m.store.Claim(ctx, now, DefaultLease, fuzzKinds)
		if err != nil {
			m.t.Fatal(err)
		}
		if j != nil {
			if j.Status != JobRunning || j.Attempts < 1 || j.Attempts > j.MaxAttempts {
				m.t.Fatalf("claimed %+v", j)
			}
			m.workers[w] = &fuzzHolder{id: j.ID, attempt: j.Attempts}
		}
	case 2:
		m.workerCall(w, func(h *fuzzHolder) error {
			p := int64(arg)
			up := JobProgress{Progress: &p}
			if arg&1 == 1 {
				total := int64(arg >> 1)
				up.Total = &total
			}
			_, err := m.store.Progress(ctx, h.id, h.attempt, up, now, DefaultLease)
			if err != nil {
				m.workers[w] = nil
			}
			return err
		})
	case 3:
		m.workerCall(w, func(h *fuzzHolder) error {
			m.workers[w] = nil
			_, err := m.store.Complete(ctx, h.id, h.attempt, nil, now)
			return err
		})
	case 4:
		m.workerCall(w, func(h *fuzzHolder) error {
			m.workers[w] = nil
			_, err := m.store.Fail(ctx, h.id, h.attempt, "retry", now.Add(RetryDelay(nil, h.attempt)), now)
			return err
		})
	case 5:
		m.workerCall(w, func(h *fuzzHolder) error {
			m.workers[w] = nil
			_, err := m.store.Fail(ctx, h.id, h.attempt, "permanent", time.Time{}, now)
			return err
		})
	case 6:
		m.clock.Advance(time.Duration(arg) * 5 * time.Second)
	case 7:
		if err := m.engine.Tick(ctx); err != nil {
			m.t.Fatal(err)
		}
	case 8:
		id := m.pick(arg)
		before, getErr := m.store.Get(ctx, id)
		err := m.engine.Cancel(ctx, id)
		switch {
		case getErr != nil:
			if !errors.Is(err, core.ErrNotFound) {
				m.t.Fatalf("cancel missing job: %v", err)
			}
		case before.Status == JobDone || before.Status == JobFailed:
			if !errors.Is(err, core.ErrConflict) {
				m.t.Fatalf("cancel %s job: %v", before.Status, err)
			}
		default:
			if err != nil {
				m.t.Fatalf("cancel %s job: %v", before.Status, err)
			}
			if after, _ := m.store.Get(ctx, id); after.Status != JobCanceled {
				m.t.Fatalf("canceled job is %s", after.Status)
			}
		}
	case 9, 10:
		id := m.pick(arg)
		before, getErr := m.store.Get(ctx, id)
		var (
			after *Job
			err   error
		)
		if op%14 == 9 {
			after, err = m.engine.Pause(ctx, id)
		} else {
			after, err = m.engine.Resume(ctx, id)
		}
		switch {
		case getErr != nil:
			if !errors.Is(err, core.ErrNotFound) {
				m.t.Fatalf("pause or resume of a missing job: %v", err)
			}
		case before.Status.Finished():
			if !errors.Is(err, core.ErrConflict) {
				m.t.Fatalf("pause or resume of a %s job: %v", before.Status, err)
			}
		case err != nil:
			m.t.Fatalf("pause or resume of a %s job: %v", before.Status, err)
		case op%14 == 9 && before.Status == JobQueued && after.Status != JobPaused,
			op%14 == 9 && before.Status == JobRunning && !after.PauseRequested,
			op%14 == 10 && before.Status == JobPaused && after.Status != JobQueued,
			op%14 == 10 && before.Status == JobRunning && after.PauseRequested:
			m.t.Fatalf("pause or resume moved %s to %+v", before.Status, after)
		}
	case 11:
		if h := m.workers[w]; h != nil {
			m.enqueue("m1.child", 0, h.id)
		}
	case 12:
		cutoff := now.Add(-time.Duration(arg) * time.Minute)
		n, err := m.store.DeleteFinishedBefore(ctx, cutoff)
		if err != nil {
			m.t.Fatal(err)
		}
		gone := 0
		for _, id := range m.ids {
			if m.deleted[id] {
				continue
			}
			if _, err := m.store.Get(ctx, id); errors.Is(err, core.ErrNotFound) {
				if _, ok := m.terminal[id]; !ok {
					m.t.Fatalf("cleanup deleted unfinished job %s", id)
				}
				m.deleted[id] = true
				gone++
			}
		}
		if gone != n {
			m.t.Fatalf("cleanup reported %d deletions, %d jobs disappeared", n, gone)
		}
	case 13:
		m.script = arg
		if _, err := m.engine.RunOnce(ctx); err != nil {
			m.t.Fatal(err)
		}
	}
}

func (m *jobModel) check() {
	ctx := m.ctx
	all, err := m.store.List(ctx, JobFilter{})
	if err != nil {
		m.t.Fatal(err)
	}
	byID := map[string]*Job{}
	for _, j := range all {
		byID[j.ID] = j
		m.remember(j.ID)
	}

	// Every job is in exactly one state.
	count := 0
	for _, st := range JobStatuses {
		list, _ := m.store.List(ctx, JobFilter{Statuses: []JobStatus{st}})
		for _, j := range list {
			if j.Status != st {
				m.t.Fatalf("status filter %s returned a %s job", st, j.Status)
			}
		}
		count += len(list)
	}
	if count != len(all) {
		m.t.Fatalf("status lists hold %d jobs, the store holds %d", count, len(all))
	}

	for id := range m.deleted {
		if byID[id] != nil {
			m.t.Fatalf("deleted job %s came back", id)
		}
	}
	for _, id := range m.ids {
		if byID[id] == nil && !m.deleted[id] {
			m.t.Fatalf("job %s disappeared without cleanup", id)
		}
	}

	active := map[string]int{}
	children := map[string][]*Job{}
	for _, j := range all {
		if !j.Status.Valid() {
			m.t.Fatalf("job %s has status %q", j.ID, j.Status)
		}
		if j.Attempts < 0 || j.Attempts > j.MaxAttempts {
			m.t.Fatalf("job %s has %d of %d attempts", j.ID, j.Attempts, j.MaxAttempts)
		}
		if (j.Status == JobQueued || j.Status == JobPaused) && j.Attempts >= j.MaxAttempts {
			m.t.Fatalf("%s job %s has no attempts left", j.Status, j.ID)
		}
		if j.Status == JobRunning {
			if j.LeaseUntil.IsZero() {
				m.t.Fatalf("running job %s has no lease", j.ID)
			}
		} else if !j.LeaseUntil.IsZero() || j.PauseRequested {
			m.t.Fatalf("%s job %s keeps a lease or pause request", j.Status, j.ID)
		}
		if prev, ok := m.terminal[j.ID]; ok && prev != j.Status {
			m.t.Fatalf("job %s left terminal state %s for %s", j.ID, prev, j.Status)
		}
		if j.Status.Finished() {
			m.terminal[j.ID] = j.Status
		} else {
			active[j.Module]++
		}
		if j.ParentID != "" {
			if byID[j.ParentID] == nil {
				m.t.Fatalf("job %s lost its parent %s", j.ID, j.ParentID)
			}
			children[j.ParentID] = append(children[j.ParentID], j)
		}
	}
	for _, mod := range []string{"m1", "m2"} {
		if active[mod] > fuzzCap {
			m.t.Fatalf("module %s has %d active jobs, cap %d", mod, active[mod], fuzzCap)
		}
		if n, _ := m.store.CountQueued(ctx, mod); n != active[mod] {
			m.t.Fatalf("CountQueued(%s) = %d, want %d", mod, n, active[mod])
		}
	}
	for _, j := range all {
		kids := children[j.ID]
		finished := 0
		for _, k := range kids {
			if k.Status.Finished() {
				finished++
			} else if j.Status == JobCanceled {
				m.t.Fatalf("canceled job %s has %s child %s", j.ID, k.Status, k.ID)
			}
		}
		if j.Children != len(kids) || j.ChildrenFinished != finished {
			m.t.Fatalf("job %s reports %d/%d children, want %d/%d", j.ID, j.ChildrenFinished, j.Children, finished, len(kids))
		}
		if j.Total == 0 && len(kids) > 0 {
			if p, total := j.DisplayProgress(); p != int64(finished) || total != int64(len(kids)) {
				m.t.Fatalf("job %s progress %d/%d, want %d/%d", j.ID, p, total, finished, len(kids))
			}
		}
	}

	// No job is held by two workers, and every running job has one holder.
	holders := make([]*fuzzHolder, 0, len(m.workers)+len(m.zombies))
	for _, h := range m.workers {
		if h != nil {
			holders = append(holders, h)
		}
	}
	holders = append(holders, m.zombies...)
	valid := map[string]int{}
	for _, h := range holders {
		if m.held(h) {
			valid[h.id]++
		}
	}
	for id, n := range valid {
		if n > 1 {
			m.t.Fatalf("job %s is held by %d workers", id, n)
		}
	}
	for _, j := range all {
		if j.Status == JobRunning && valid[j.ID] != 1 {
			m.t.Fatalf("running job %s has %d holders", j.ID, valid[j.ID])
		}
	}
	live := m.zombies[:0]
	for _, z := range m.zombies {
		if m.held(z) {
			live = append(live, z)
		}
	}
	m.zombies = live
}

func FuzzJobEngine(f *testing.F) {
	seeds := [][]byte{
		{0, 0, 1, 0, 2, 5, 3, 0, 13, 0},
		{0, 0, 0, 4, 0, 8, 0, 12, 0, 16, 0, 20, 0, 24, 0, 28, 1, 0, 1, 1, 1, 2},
		{0, 0, 1, 0, 9, 0, 4, 0, 10, 0, 6, 10, 1, 0, 3, 0},
		{0, 0, 1, 0, 1, 0, 6, 13, 7, 0, 1, 1, 3, 1, 6, 200, 7, 0},
		{0, 0, 1, 0, 11, 0, 11, 0, 11, 0, 8, 0, 13, 0, 13, 1, 12, 0},
		{0, 0, 13, 3, 13, 4, 13, 5, 0, 1, 13, 3, 13, 0, 13, 0, 6, 255, 12, 0},
		{0, 2, 13, 1, 6, 6, 13, 1, 6, 24, 13, 1, 6, 120, 13, 1, 12, 1},
		{0, 1, 1, 1, 2, 1, 1, 1, 6, 13, 7, 0, 1, 2, 5, 2, 9, 0, 10, 0, 12, 0},
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ops []byte) {
		m := newJobModel(t)
		for i := 0; i+1 < len(ops) && i < 800; i += 2 {
			m.step(ops[i], ops[i+1])
			m.check()
		}
	})
}
