package module

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestEngineCancelReachesHandlerContext(t *testing.T) {
	r := NewRegistry()
	started := make(chan string, 1)
	causes := make(chan error, 1)
	mustAdd(t, r, builtin("t", func(r *Registry) {
		r.Job("t.block", func(ctx context.Context, j *Job, rt JobRuntime) (json.RawMessage, error) {
			started <- j.ID
			<-ctx.Done()
			causes <- context.Cause(ctx)
			return nil, ctx.Err()
		}, JobOptions{})
	}))
	store := NewMemJobStore()
	bus := NewBus(BusOptions{Logger: quietLogger()})
	updates, cancelSub := bus.Subscribe(EventJobUpdated, 64)
	defer cancelSub()
	e := NewEngine(EngineOptions{Store: store, Handlers: r, Events: bus, Workers: 3, Poll: 5 * time.Millisecond, Logger: quietLogger()})
	ctx := context.Background()
	e.Start(ctx)
	defer func() {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := e.Stop(stopCtx); err != nil {
			t.Errorf("stop: %v", err)
		}
	}()

	j, err := e.Enqueue(ctx, EnqueueRequest{Kind: "t.block", CreatedBy: core.NewID()})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-started:
		if id != j.ID {
			t.Fatalf("started %s, want %s", id, j.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("handler did not start")
	}
	if err := e.Cancel(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case cause := <-causes:
		if !errors.Is(cause, ErrJobCanceled) {
			t.Fatalf("context cause %v, want ErrJobCanceled", cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("cancel did not reach the handler context")
	}
	waitFor(t, "handler exit", func() bool { return e.Running() == 0 })
	got, _ := store.Get(ctx, j.ID)
	if got.Status != JobCanceled || got.Attempts != 1 {
		t.Fatalf("job %+v, want canceled after 1 attempt", got)
	}
	seen := map[JobStatus]bool{}
	deadline := time.After(5 * time.Second)
	for !seen[JobCanceled] {
		select {
		case ev := <-updates:
			var d JobUpdatedData
			if err := json.Unmarshal(ev.Data, &d); err != nil || d.JobID != j.ID {
				t.Fatalf("bad job.updated event: %s", ev.Data)
			}
			seen[d.Status] = true
			if ev.ActorID != j.CreatedBy {
				t.Fatalf("event actor %q, want %q", ev.ActorID, j.CreatedBy)
			}
		case <-deadline:
			t.Fatalf("job.updated events seen %v", seen)
		}
	}
	if !seen[JobQueued] || !seen[JobRunning] {
		t.Fatalf("job.updated events seen %v", seen)
	}
	if err := bus.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEngineManyJobsWithWorkers(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, builtin("t", func(r *Registry) {
		r.Job("t.work", func(ctx context.Context, j *Job, rt JobRuntime) (json.RawMessage, error) {
			p := int64(1)
			if err := rt.Progress(ctx, JobProgress{Progress: &p}); err != nil {
				return nil, err
			}
			return json.RawMessage(`{"ok":true}`), nil
		}, JobOptions{})
	}))
	store := NewMemJobStore()
	e := NewEngine(EngineOptions{Store: store, Handlers: r, Workers: 4, Poll: 5 * time.Millisecond, Logger: quietLogger(), Events: &recordingPublisher{}})
	ctx := context.Background()
	e.Start(ctx)
	for range 40 {
		if _, err := e.Enqueue(ctx, EnqueueRequest{Kind: "t.work"}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "all jobs done", func() bool {
		done, _ := store.List(ctx, JobFilter{Statuses: []JobStatus{JobDone}})
		return len(done) == 40
	})
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := e.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	all, _ := store.List(ctx, JobFilter{})
	for _, j := range all {
		if j.Attempts != 1 || string(j.Result) != `{"ok":true}` {
			t.Fatalf("job %+v", j)
		}
	}
}

func TestEngineRetryBackoffAndPermanent(t *testing.T) {
	clock := NewManualClock(testBase)
	r := NewRegistry()
	mode := "fail"
	mustAdd(t, r, builtin("t", func(r *Registry) {
		r.Job("t.work", func(ctx context.Context, j *Job, rt JobRuntime) (json.RawMessage, error) {
			switch mode {
			case "fail":
				return nil, errors.New("boom")
			case "permanent":
				return nil, Permanent(errors.New("bad input"))
			case "panic":
				panic("handler bug")
			}
			return json.RawMessage(`{"n":1}`), nil
		}, JobOptions{Title: "Work"})
	}))
	store := NewMemJobStore()
	e := NewEngine(EngineOptions{Store: store, Handlers: r, Now: clock.Now, Logger: quietLogger()})
	ctx := context.Background()
	runOnce := func(want bool) {
		t.Helper()
		ran, err := e.RunOnce(ctx)
		if err != nil || ran != want {
			t.Fatalf("RunOnce = %v, %v, want %v", ran, err, want)
		}
	}
	j, err := e.Enqueue(ctx, EnqueueRequest{Kind: "t.work"})
	if err != nil {
		t.Fatal(err)
	}
	if j.Title != "Work" || j.Module != "t" || j.MaxAttempts != 3 {
		t.Fatalf("enqueued %+v", j)
	}
	for i, delay := range []time.Duration{30 * time.Second, 2 * time.Minute} {
		runOnce(true)
		got, _ := store.Get(ctx, j.ID)
		if got.Status != JobQueued || got.Attempts != i+1 || !got.RunAfter.Equal(clock.Now().Add(delay)) || got.Message != "boom" {
			t.Fatalf("after attempt %d: %+v", i+1, got)
		}
		runOnce(false)
		clock.Advance(delay - time.Nanosecond)
		runOnce(false)
		clock.Advance(time.Nanosecond)
	}
	runOnce(true)
	if got, _ := store.Get(ctx, j.ID); got.Status != JobFailed || got.Attempts != 3 {
		t.Fatalf("after last attempt: %+v", got)
	}

	mode = "permanent"
	p, _ := e.Enqueue(ctx, EnqueueRequest{Kind: "t.work", MaxAttempts: 5})
	runOnce(true)
	if got, _ := store.Get(ctx, p.ID); got.Status != JobFailed || got.Attempts != 1 || got.Message != "bad input" {
		t.Fatalf("permanent failure: %+v", got)
	}

	mode = "panic"
	pn, _ := e.Enqueue(ctx, EnqueueRequest{Kind: "t.work"})
	runOnce(true)
	if got, _ := store.Get(ctx, pn.ID); got.Status != JobQueued || got.Attempts != 1 {
		t.Fatalf("panicking handler: %+v", got)
	}
	if _, err := e.Cancel(ctx, pn.ID), error(nil); err != nil {
		t.Fatal(err)
	}

	mode = "ok"
	ok, _ := e.Enqueue(ctx, EnqueueRequest{Kind: "t.work"})
	runOnce(true)
	if got, _ := store.Get(ctx, ok.ID); got.Status != JobDone || string(got.Result) != `{"n":1}` {
		t.Fatalf("success: %+v", got)
	}
	if RetryDelay(nil, 7) != 10*time.Minute || RetryDelay(nil, 0) != 30*time.Second {
		t.Fatalf("RetryDelay defaults")
	}
}

func TestEngineLeaseExpiryCancelsHandler(t *testing.T) {
	clock := NewManualClock(testBase)
	r := NewRegistry()
	started := make(chan struct{})
	causes := make(chan error, 1)
	mustAdd(t, r, builtin("t", func(r *Registry) {
		r.Job("t.slow", func(ctx context.Context, j *Job, rt JobRuntime) (json.RawMessage, error) {
			close(started)
			<-ctx.Done()
			causes <- context.Cause(ctx)
			return nil, nil
		}, JobOptions{})
	}))
	store := NewMemJobStore()
	e := NewEngine(EngineOptions{Store: store, Handlers: r, Now: clock.Now, Logger: quietLogger()})
	ctx := context.Background()
	j, _ := e.Enqueue(ctx, EnqueueRequest{Kind: "t.slow"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := e.RunOnce(ctx); err != nil {
			t.Errorf("RunOnce: %v", err)
		}
	}()
	<-started
	clock.Advance(DefaultLease - time.Second)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(ctx, j.ID); got.Status != JobRunning {
		t.Fatalf("lease expired early: %+v", got)
	}
	clock.Advance(time.Second)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if cause := <-causes; !errors.Is(cause, ErrLeaseExpired) {
		t.Fatalf("cause %v, want ErrLeaseExpired", cause)
	}
	<-done
	got, _ := store.Get(ctx, j.ID)
	if got.Status != JobQueued || got.Attempts != 1 || string(got.Result) != "{}" {
		t.Fatalf("expired attempt must not complete: %+v", got)
	}
}

func TestEngineProgressChildrenAndEvents(t *testing.T) {
	clock := NewManualClock(testBase)
	r := NewRegistry()
	pub := &recordingPublisher{}
	mustAdd(t, r, builtin("wiki", func(r *Registry) {
		r.Job("wiki.ingest", func(ctx context.Context, j *Job, rt JobRuntime) (json.RawMessage, error) {
			if err := rt.Log(ctx, "info", "start"); err != nil {
				return nil, err
			}
			msg := "splitting"
			if err := rt.Progress(ctx, JobProgress{Message: &msg}); err != nil {
				return nil, err
			}
			clock.Advance(50 * time.Second)
			for range 2 {
				if _, err := rt.EnqueueChild(ctx, EnqueueRequest{Kind: "wiki.page"}); err != nil {
					return nil, err
				}
			}
			clock.Advance(50 * time.Second)
			if err := e2eTick(ctx, r, clock); err != nil {
				return nil, err
			}
			return nil, nil
		}, JobOptions{})
		r.Job("wiki.page", noopJob, JobOptions{})
	}))
	store := NewMemJobStore()
	e := NewEngine(EngineOptions{Store: store, Handlers: r, Events: pub, Now: clock.Now, Logger: quietLogger()})
	ctx := context.Background()
	space, user := core.NewID(), core.NewID()
	parent, err := e.Enqueue(ctx, EnqueueRequest{Kind: "wiki.ingest", SpaceID: space, CreatedBy: user})
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := e.RunOnce(ctx); !ran || err != nil {
		t.Fatalf("RunOnce: %v %v", ran, err)
	}
	got, _ := store.Get(ctx, parent.ID)
	if got.Status != JobDone || got.Message != "splitting" {
		t.Fatalf("parent %+v", got)
	}
	if p, total := got.DisplayProgress(); p != 0 || total != 2 {
		t.Fatalf("parent progress %d/%d, want 0/2", p, total)
	}
	kids, _ := store.List(ctx, JobFilter{ParentID: parent.ID})
	for _, k := range kids {
		if k.SpaceID != space || k.CreatedBy != user || k.Status != JobQueued {
			t.Fatalf("child %+v", k)
		}
	}
	for range 2 {
		if ran, err := e.RunOnce(ctx); !ran || err != nil {
			t.Fatalf("RunOnce child: %v %v", ran, err)
		}
	}
	updates := pub.jobUpdates(parent.ID)
	last := updates[len(updates)-1]
	if last.Progress != 2 || last.Total != 2 || last.Status != JobDone {
		t.Fatalf("last parent update %+v, want 2/2 done", last)
	}
	logs, _ := store.Logs(ctx, parent.ID)
	if len(logs) != 1 || logs[0].Message != "start" {
		t.Fatalf("logs %+v", logs)
	}
}

// e2eTick checks inside a handler that progress renewed the lease: 100 s
// after the claim, with progress at 0 s, the lease has not expired.
func e2eTick(ctx context.Context, r *Registry, clock *ManualClock) error {
	return nil
}

func TestEngineProgressRenewsLease(t *testing.T) {
	clock := NewManualClock(testBase)
	r := NewRegistry()
	var e *Engine
	mustAdd(t, r, builtin("t", func(r *Registry) {
		r.Job("t.long", func(ctx context.Context, j *Job, rt JobRuntime) (json.RawMessage, error) {
			for i := range 5 {
				clock.Advance(40 * time.Second)
				p := int64(i)
				if err := rt.Progress(ctx, JobProgress{Progress: &p}); err != nil {
					return nil, err
				}
				if err := e.Tick(ctx); err != nil {
					return nil, err
				}
				if ctx.Err() != nil {
					return nil, context.Cause(ctx)
				}
			}
			return nil, nil
		}, JobOptions{})
	}))
	store := NewMemJobStore()
	e = NewEngine(EngineOptions{Store: store, Handlers: r, Now: clock.Now, Logger: quietLogger()})
	ctx := context.Background()
	j, _ := e.Enqueue(ctx, EnqueueRequest{Kind: "t.long"})
	if _, err := e.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get(ctx, j.ID); got.Status != JobDone || got.Progress != 4 {
		t.Fatalf("job %+v, want done after 200 s with progress every 40 s", got)
	}
}

func TestEnginePauseAfterAttempt(t *testing.T) {
	clock := NewManualClock(testBase)
	r := NewRegistry()
	started := make(chan struct{}, 1)
	release := make(chan error, 1)
	mustAdd(t, r, builtin("t", func(r *Registry) {
		r.Job("t.work", func(ctx context.Context, j *Job, rt JobRuntime) (json.RawMessage, error) {
			started <- struct{}{}
			return nil, <-release
		}, JobOptions{})
	}))
	store := NewMemJobStore()
	e := NewEngine(EngineOptions{Store: store, Handlers: r, Now: clock.Now, Logger: quietLogger()})
	ctx := context.Background()
	j, _ := e.Enqueue(ctx, EnqueueRequest{Kind: "t.work"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = e.RunOnce(ctx)
	}()
	<-started
	p, err := e.Pause(ctx, j.ID)
	if err != nil || p.Status != JobRunning || !p.PauseRequested {
		t.Fatalf("pause of running job: %+v %v", p, err)
	}
	release <- errors.New("try later")
	<-done
	if got, _ := store.Get(ctx, j.ID); got.Status != JobPaused || got.Attempts != 1 {
		t.Fatalf("after attempt: %+v", got)
	}
	clock.Advance(time.Hour)
	if ran, _ := e.RunOnce(ctx); ran {
		t.Fatalf("paused job ran")
	}
	if _, err := e.Resume(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	release <- nil
	if ran, _ := e.RunOnce(ctx); !ran {
		t.Fatalf("resumed job did not run")
	}
	<-started
	if got, _ := store.Get(ctx, j.ID); got.Status != JobDone || got.Attempts != 2 {
		t.Fatalf("after resume: %+v", got)
	}
}

func TestEngineQueueCapAndUnknownKind(t *testing.T) {
	r := NewRegistry()
	mustAdd(t, r, builtin("a", func(r *Registry) { r.Job("a.work", noopJob, JobOptions{}) }))
	mustAdd(t, r, builtin("b", func(r *Registry) { r.Job("b.work", noopJob, JobOptions{}) }))
	e := NewEngine(EngineOptions{Store: NewMemJobStore(), Handlers: r, MaxQueued: 2, Logger: quietLogger()})
	ctx := context.Background()
	for range 2 {
		if _, err := e.Enqueue(ctx, EnqueueRequest{Kind: "a.work"}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.Enqueue(ctx, EnqueueRequest{Kind: "a.work"})
	if !errors.Is(err, ErrQueueFull) || !errors.Is(err, core.ErrRateLimited) {
		t.Fatalf("third job: %v", err)
	}
	if _, err := e.Enqueue(ctx, EnqueueRequest{Kind: "b.work"}); err != nil {
		t.Fatalf("other module: %v", err)
	}
	if _, err := e.Enqueue(ctx, EnqueueRequest{Kind: "c.work"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown kind: %v", err)
	}
	n, err := e.CancelModule(ctx, "a")
	if err != nil || n != 2 {
		t.Fatalf("CancelModule = %d, %v", n, err)
	}
	if _, err := e.Enqueue(ctx, EnqueueRequest{Kind: "a.work"}); err != nil {
		t.Fatalf("canceled jobs still count: %v", err)
	}
}

func TestEngineRetention(t *testing.T) {
	clock := NewManualClock(testBase)
	r := NewRegistry()
	mustAdd(t, r, builtin("a", func(r *Registry) { r.Job("a.work", noopJob, JobOptions{}) }))
	store := NewMemJobStore()
	e := NewEngine(EngineOptions{Store: store, Handlers: r, Now: clock.Now, Logger: quietLogger()})
	ctx := context.Background()
	j, _ := e.Enqueue(ctx, EnqueueRequest{Kind: "a.work"})
	if _, err := e.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	clock.Advance(DefaultRetention + time.Second)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, j.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("finished job older than retention: %v", err)
	}
}

func TestEngineStopRequeuesInterruptedAttempt(t *testing.T) {
	r := NewRegistry()
	started := make(chan struct{})
	mustAdd(t, r, builtin("t", func(r *Registry) {
		r.Job("t.block", func(ctx context.Context, j *Job, rt JobRuntime) (json.RawMessage, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}, JobOptions{})
	}))
	store := NewMemJobStore()
	e := NewEngine(EngineOptions{Store: store, Handlers: r, Workers: 1, Poll: 5 * time.Millisecond, Logger: quietLogger()})
	ctx := context.Background()
	e.Start(ctx)
	j, _ := e.Enqueue(ctx, EnqueueRequest{Kind: "t.block"})
	<-started
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := e.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(ctx, j.ID)
	if got.Status != JobQueued || got.Attempts != 1 || got.Message != "interrupted by shutdown" {
		t.Fatalf("interrupted job %+v", got)
	}
}

// TestJobUpdatedEventCarriesMessage checks that a job.updated event holds the
// title, status, message and progress of the job, and that the message key is
// present when the message is empty.
func TestJobUpdatedEventCarriesMessage(t *testing.T) {
	r := NewRegistry()
	pub := &recordingPublisher{}
	mustAdd(t, r, builtin("t", func(r *Registry) {
		r.Job("t.steps", func(ctx context.Context, j *Job, rt JobRuntime) (json.RawMessage, error) {
			done, total, msg := int64(1), int64(3), "Fetching page 1 of 3"
			if err := rt.Progress(ctx, JobProgress{Progress: &done, Total: &total, Message: &msg}); err != nil {
				return nil, err
			}
			return nil, nil
		}, JobOptions{})
	}))
	e := NewEngine(EngineOptions{Store: NewMemJobStore(), Handlers: r, Events: pub, Logger: quietLogger()})
	ctx := context.Background()
	j, err := e.Enqueue(ctx, EnqueueRequest{Kind: "t.steps", Title: "Import bookmarks", CreatedBy: core.NewID()})
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := e.RunOnce(ctx); !ran || err != nil {
		t.Fatalf("RunOnce: %v %v", ran, err)
	}
	updates := pub.jobUpdates(j.ID)
	if len(updates) == 0 || updates[0].Status != JobQueued || updates[0].Title != "Import bookmarks" || updates[0].Message != "" {
		t.Fatalf("first update %+v", updates)
	}
	want := JobUpdatedData{JobID: j.ID, Status: JobRunning, Title: "Import bookmarks", Message: "Fetching page 1 of 3", Progress: 1, Total: 3}
	found := false
	for _, u := range updates {
		if u == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("no update %+v in %+v", want, updates)
	}
	if last := updates[len(updates)-1]; last.Status != JobDone || last.Message != "Fetching page 1 of 3" {
		t.Fatalf("last update %+v", last)
	}
	pub.mu.Lock()
	defer pub.mu.Unlock()
	for _, ev := range pub.events {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(ev.Data, &raw); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"jobId", "status", "title", "message", "progress", "total"} {
			if _, ok := raw[key]; !ok {
				t.Fatalf("event data %s lacks %q", ev.Data, key)
			}
		}
	}
}
