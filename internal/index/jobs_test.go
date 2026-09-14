package index

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/module/conformance"
)

func TestJobStoreConformance(t *testing.T) {
	conformance.Run(t, func() module.JobStore { return NewJobStore(newTestDB(t)) })
}

// TestJobStoreConcurrentClaimsRace claims 500 jobs from 8 goroutines. Some
// attempts fail and requeue at once, so jobs are claimed again while other
// workers run. It checks that no attempt is claimed twice, that no job runs
// in two workers at once, and that a finished attempt is fenced off.
func TestJobStoreConcurrentClaimsRace(t *testing.T) {
	ctx := context.Background()
	s := NewJobStore(newTestDB(t))
	const jobs, workers = 500, 8
	base := conformance.Base
	for i := range jobs {
		if err := s.Enqueue(ctx, &module.Job{Kind: "a.x", Module: "a", CreatedAt: base.Add(time.Duration(i))}, 0); err != nil {
			t.Fatal(err)
		}
	}
	now := base.Add(time.Minute)
	type attemptKey struct {
		id      string
		attempt int
	}
	var (
		mu      sync.Mutex
		claimed = map[attemptKey]bool{}
		running = map[string]bool{}
		claims  = map[string]int{}
		done    atomic.Int64
		failed  atomic.Bool
		wg      sync.WaitGroup
	)
	errs := make(chan error, workers)
	fail := func(err error) {
		failed.Store(true)
		select {
		case errs <- err:
		default:
		}
	}
	for range workers {
		wg.Go(func() {
			for done.Load() < jobs && !failed.Load() {
				c, err := s.Claim(ctx, now, time.Minute, []string{"a.x"})
				if err != nil {
					fail(err)
					return
				}
				if c == nil {
					runtime.Gosched()
					continue
				}
				k := attemptKey{c.ID, c.Attempts}
				mu.Lock()
				twice, busy := claimed[k], running[c.ID]
				claimed[k], running[c.ID] = true, true
				claims[c.ID]++
				mu.Unlock()
				if twice || busy {
					fail(fmt.Errorf("job %s attempt %d: claimed twice %v, running elsewhere %v", c.ID, c.Attempts, twice, busy))
					return
				}
				retry := c.Attempts < c.MaxAttempts && (int(c.ID[len(c.ID)-1])+c.Attempts)%3 == 0
				mu.Lock()
				delete(running, c.ID)
				mu.Unlock()
				if retry {
					f, err := s.Fail(ctx, c.ID, c.Attempts, "retry", now, now)
					if err != nil || f.Status != module.JobQueued {
						fail(fmt.Errorf("fail %s: %v %v", c.ID, f, err))
						return
					}
				} else {
					if _, err := s.Complete(ctx, c.ID, c.Attempts, nil, now); err != nil {
						fail(fmt.Errorf("complete %s: %w", c.ID, err))
						return
					}
					done.Add(1)
				}
				if _, err := s.Complete(ctx, c.ID, c.Attempts, nil, now); !errors.Is(err, module.ErrLeaseLost) {
					fail(fmt.Errorf("stale complete of %s attempt %d: %v", c.ID, c.Attempts, err))
					return
				}
				if err := s.RenewLease(ctx, c.ID, c.Attempts, now, time.Minute); !errors.Is(err, module.ErrLeaseLost) {
					fail(fmt.Errorf("stale renew of %s attempt %d: %v", c.ID, c.Attempts, err))
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	list, err := s.List(ctx, module.JobFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != jobs {
		t.Fatalf("listed %d jobs, want %d", len(list), jobs)
	}
	retried := 0
	for _, j := range list {
		if j.Status != module.JobDone {
			t.Fatalf("job %s ended %s", j.ID, j.Status)
		}
		if j.Attempts != claims[j.ID] || j.Attempts > j.MaxAttempts {
			t.Fatalf("job %s: %d attempts stored, %d claims seen, max %d", j.ID, j.Attempts, claims[j.ID], j.MaxAttempts)
		}
		if j.Attempts > 1 {
			retried++
		}
	}
	if retried == 0 {
		t.Fatalf("no job was retried, so the test did not reclaim any job")
	}
}

func TestJobStoreDeletesManyFinishedTrees(t *testing.T) {
	ctx := context.Background()
	s := NewJobStore(newTestDB(t))
	old := conformance.Base.Add(-40 * 24 * time.Hour)
	const n = deleteChunk + 100
	for i := range n {
		if err := s.Enqueue(ctx, &module.Job{Kind: "a.x", Module: "a", CreatedAt: old.Add(time.Duration(i))}, 0); err != nil {
			t.Fatal(err)
		}
		c, err := s.Claim(ctx, old.Add(time.Hour), time.Minute, []string{"a.x"})
		if err != nil || c == nil {
			t.Fatalf("claim: %v %v", c, err)
		}
		if err := s.Log(ctx, module.JobLogEntry{JobID: c.ID, At: old, Level: "info", Message: "x"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Complete(ctx, c.ID, c.Attempts, nil, old.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := s.DeleteFinishedBefore(ctx, conformance.Base)
	if err != nil || deleted != n {
		t.Fatalf("deleted %d, %v, want %d", deleted, err, n)
	}
	var logs int
	if err := s.d.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM job_log`).Scan(&logs); err != nil || logs != 0 {
		t.Fatalf("job_log rows left: %d %v", logs, err)
	}
}

func TestJobStoreLogsKeepAppendOrderAndStoreFixedWidthTimes(t *testing.T) {
	ctx := context.Background()
	s := NewJobStore(newTestDB(t))
	j := &module.Job{Kind: "a.x", Module: "a", CreatedAt: time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)}
	if err := s.Enqueue(ctx, j, 0); err != nil {
		t.Fatal(err)
	}
	for i, at := range []time.Time{j.CreatedAt.Add(time.Hour), j.CreatedAt} {
		if err := s.Log(ctx, module.JobLogEntry{JobID: j.ID, At: at, Level: "info", Message: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := s.Logs(ctx, j.ID)
	if err != nil || len(logs) != 2 || logs[0].Message != "0" || logs[1].Message != "1" {
		t.Fatalf("logs out of append order: %+v %v", logs, err)
	}
	var created string
	if err := s.d.sql.QueryRowContext(ctx, `SELECT created_at FROM jobs WHERE id = ?`, j.ID).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != "2026-09-13T09:00:00.000000000Z" {
		t.Fatalf("stored created_at %q, want nine fractional digits", created)
	}
}
