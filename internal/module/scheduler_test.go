package module

import (
	"context"
	"slices"
	"testing"
	"time"
)

func newScheduleFixture(t *testing.T, jitter time.Duration) (*ManualClock, *Registry, *MemJobStore, *Engine) {
	t.Helper()
	clock := NewManualClock(time.Date(2026, 9, 13, 3, 0, 0, 0, time.UTC))
	r := NewRegistry()
	mustAdd(t, r, plugin("wiki", func(r *Registry) {
		r.Job("wiki.lint", noopJob, JobOptions{})
		r.Schedule("wiki.lint", 24*time.Hour, jitter)
	}))
	store := NewMemJobStore()
	e := NewEngine(EngineOptions{Store: store, Handlers: r, Now: clock.Now, Logger: quietLogger()})
	return clock, r, store, e
}

func TestSchedulerDailyJob(t *testing.T) {
	clock, r, store, e := newScheduleFixture(t, 0)
	ctx := context.Background()
	s := NewScheduler(SchedulerOptions{Engine: e, Now: clock.Now, Logger: quietLogger()})
	s.SetSchedules(r.Schedules())
	tick := func(s *Scheduler, want ...string) {
		t.Helper()
		got, err := s.Tick(ctx)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("at %s: Tick = %v, %v, want %v", clock.Now(), got, err, want)
		}
	}
	run := func() {
		t.Helper()
		if ran, err := e.RunOnce(ctx); !ran || err != nil {
			t.Fatalf("RunOnce = %v, %v", ran, err)
		}
	}

	tick(s, "wiki.lint")
	jobs, _ := store.List(ctx, JobFilter{Kind: "wiki.lint"})
	if len(jobs) != 1 || jobs[0].CreatedBy != ScheduleActor || jobs[0].Module != "wiki" {
		t.Fatalf("scheduled job %+v", jobs)
	}
	tick(s)
	run()
	clock.Advance(23 * time.Hour)
	tick(s)
	clock.Advance(time.Hour)
	tick(s, "wiki.lint")

	clock.Advance(24 * time.Hour)
	tick(s)
	if lr, _ := store.LastRun(ctx, "wiki.lint"); !lr.Equal(clock.Now()) {
		t.Fatalf("skipped run not recorded: last run %s", lr)
	}
	if jobs, _ := store.List(ctx, JobFilter{Kind: "wiki.lint"}); len(jobs) != 2 {
		t.Fatalf("%d jobs, want 2", len(jobs))
	}
	run()
	clock.Advance(time.Hour)
	tick(s)

	restarted := NewScheduler(SchedulerOptions{Engine: e, Now: clock.Now, Logger: quietLogger()})
	restarted.SetSchedules(r.Schedules())
	clock.Advance(11 * time.Hour)
	tick(restarted)
	clock.Advance(12 * time.Hour)
	tick(restarted, "wiki.lint")

	restarted.SetSchedules(nil)
	clock.Advance(48 * time.Hour)
	tick(restarted)
}

func TestSchedulerJitter(t *testing.T) {
	clock, r, _, e := newScheduleFixture(t, time.Hour)
	ctx := context.Background()
	s := NewScheduler(SchedulerOptions{Engine: e, Now: clock.Now, Logger: quietLogger()})
	s.SetSchedules(r.Schedules())
	spec := r.Schedules()[0]

	first, err := s.NextRun(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	offset := JitterOffset("wiki.lint", time.Time{}, time.Hour)
	if !first.Equal(clock.Now().Add(offset)) {
		t.Fatalf("first run %s, want start plus %s", first, offset)
	}
	clock.Set(first.Add(-time.Nanosecond))
	if got, _ := s.Tick(ctx); len(got) != 0 && offset > 0 {
		t.Fatalf("ran before the jitter offset")
	}
	clock.Set(first)
	if got, _ := s.Tick(ctx); len(got) != 1 {
		t.Fatalf("did not run at the jitter offset")
	}
	if _, err := e.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	next, _ := s.NextRun(ctx, spec)
	want := first.Add(24*time.Hour + JitterOffset("wiki.lint", first, time.Hour))
	if !next.Equal(want) {
		t.Fatalf("next run %s, want %s", next, want)
	}
	clock.Set(next.Add(-time.Nanosecond))
	if got, _ := s.Tick(ctx); len(got) != 0 {
		t.Fatalf("ran before the next run")
	}
	clock.Set(next)
	if got, _ := s.Tick(ctx); len(got) != 1 {
		t.Fatalf("did not run at the next run")
	}

	distinct := map[time.Duration]bool{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 500 {
		b := base.Add(time.Duration(i) * 24 * time.Hour)
		off := JitterOffset("wiki.lint", b, time.Hour)
		if off < 0 || off >= time.Hour || off != JitterOffset("wiki.lint", b, time.Hour) {
			t.Fatalf("offset %s out of range or not deterministic", off)
		}
		distinct[off] = true
	}
	if len(distinct) < 400 {
		t.Fatalf("only %d distinct offsets in 500 periods", len(distinct))
	}
	if JitterOffset("wiki.lint", base, 0) != 0 {
		t.Fatalf("zero jitter must give a zero offset")
	}
}
