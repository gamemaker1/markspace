// Package conformance is the conformance suite for module.JobStore. Every
// store implementation runs it from its own tests:
//
//	func TestConformance(t *testing.T) {
//		conformance.Run(t, func() module.JobStore { return newStore(t) })
//	}
//
// newStore must return an empty store for every call.
package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// Base is the time the suite starts its clock at.
var Base = time.Date(2026, 9, 13, 9, 0, 0, 123456789, time.UTC)

const lease = time.Minute

// Run runs every conformance test against stores made by newStore.
func Run(t *testing.T, newStore func() module.JobStore) {
	tests := []struct {
		name string
		fn   func(t *testing.T, s module.JobStore)
	}{
		{"EnqueueDefaults", testEnqueueDefaults},
		{"EnqueueValidation", testEnqueueValidation},
		{"QueueCap", testQueueCap},
		{"ClaimOrder", testClaimOrder},
		{"ClaimKinds", testClaimKinds},
		{"FenceRejectsStaleAttempt", testFence},
		{"Progress", testProgress},
		{"FailRetryAndTerminal", testFail},
		{"PauseRunning", testPauseRunning},
		{"PauseResumeQueued", testPauseResumeQueued},
		{"RequeueExpired", testRequeueExpired},
		{"CancelCascade", testCancelCascade},
		{"ChildrenProgress", testChildrenProgress},
		{"ListFilterOrderLimit", testList},
		{"Logs", testLogs},
		{"DeleteFinishedBefore", testDeleteFinished},
		{"LastRun", testLastRun},
		{"ReturnsCopies", testCopies},
		{"ConcurrentClaims", testConcurrentClaims},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, newStore())
		})
	}
}

func newJob(kind, mod string, at time.Time) *module.Job {
	return &module.Job{Kind: kind, Module: mod, CreatedAt: at}
}

func enqueue(t *testing.T, s module.JobStore, j *module.Job) *module.Job {
	t.Helper()
	if err := s.Enqueue(context.Background(), j, 0); err != nil {
		t.Fatalf("enqueue %s: %v", j.Kind, err)
	}
	return j
}

func claim(t *testing.T, s module.JobStore, now time.Time, kinds ...string) *module.Job {
	t.Helper()
	j, err := s.Claim(context.Background(), now, lease, kinds)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	return j
}

func get(t *testing.T, s module.JobStore, id string) *module.Job {
	t.Helper()
	j, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return j
}

func wantErr(t *testing.T, err, target error, what string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: got error %v, want %v", what, err, target)
	}
}

func wantTime(t *testing.T, got, want time.Time, what string) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s: got %s, want %s", what, got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

func wantStatus(t *testing.T, j *module.Job, want module.JobStatus) {
	t.Helper()
	if j.Status != want {
		t.Fatalf("job %s: status %s, want %s", j.ID, j.Status, want)
	}
}

func testEnqueueDefaults(t *testing.T, s module.JobStore) {
	j := enqueue(t, s, &module.Job{Kind: "notes.reindex", Module: "notes", CreatedAt: Base, Title: "Reindex", SpaceID: core.NewID(), CreatedBy: "plugin:x"})
	if !core.ValidID(j.ID) {
		t.Fatalf("assigned id %q is not a core id", j.ID)
	}
	wantStatus(t, j, module.JobQueued)
	if j.MaxAttempts != module.DefaultMaxAttempts || j.Attempts != 0 {
		t.Fatalf("attempts %d/%d, want 0/%d", j.Attempts, j.MaxAttempts, module.DefaultMaxAttempts)
	}
	if string(j.Payload) != "{}" || string(j.Result) != "{}" {
		t.Fatalf("payload %s result %s, want {} and {}", j.Payload, j.Result)
	}
	wantTime(t, j.RunAfter, Base, "runAfter")
	wantTime(t, j.UpdatedAt, Base, "updatedAt")
	g := get(t, s, j.ID)
	if g.Title != "Reindex" || g.CreatedBy != "plugin:x" || g.SpaceID != j.SpaceID || g.Module != "notes" {
		t.Fatalf("stored job differs: %+v", g)
	}
	wantTime(t, g.CreatedAt, Base, "stored createdAt")
	if g.CreatedAt.Location() != time.UTC {
		t.Fatalf("createdAt is not UTC")
	}

	id := core.NewID()
	later := Base.Add(time.Hour)
	k := enqueue(t, s, &module.Job{ID: id, Kind: "notes.reindex", Module: "notes", CreatedAt: Base, RunAfter: later, MaxAttempts: 5, Payload: json.RawMessage(`{"a":1}`)})
	if k.ID != id || k.MaxAttempts != 5 {
		t.Fatalf("explicit id or maxAttempts lost: %+v", k)
	}
	wantTime(t, get(t, s, id).RunAfter, later, "explicit runAfter")
	var payload map[string]int
	if err := json.Unmarshal(get(t, s, id).Payload, &payload); err != nil || payload["a"] != 1 {
		t.Fatalf("payload not stored: %s", get(t, s, id).Payload)
	}
}

func testEnqueueValidation(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	wantErr(t, s.Enqueue(ctx, newJob("Bad Kind", "notes", Base), 0), core.ErrInvalid, "invalid kind")
	wantErr(t, s.Enqueue(ctx, newJob("notes.x", "", Base), 0), core.ErrInvalid, "empty module")
	wantErr(t, s.Enqueue(ctx, &module.Job{Kind: "notes.x", Module: "notes"}, 0), core.ErrInvalid, "missing createdAt")
	wantErr(t, s.Enqueue(ctx, &module.Job{Kind: "notes.x", Module: "notes", CreatedAt: Base, Payload: json.RawMessage(`{`)}, 0), core.ErrInvalid, "bad payload")
	wantErr(t, s.Enqueue(ctx, &module.Job{Kind: "notes.x", Module: "notes", CreatedAt: Base, MaxAttempts: module.MaxJobAttempts + 1}, 0), core.ErrInvalid, "maxAttempts")
	big := json.RawMessage(`"` + strings.Repeat("a", module.MaxJobPayload) + `"`)
	wantErr(t, s.Enqueue(ctx, &module.Job{Kind: "notes.x", Module: "notes", CreatedAt: Base, Payload: big}, 0), core.ErrTooLarge, "large payload")

	j := enqueue(t, s, newJob("notes.x", "notes", Base))
	wantErr(t, s.Enqueue(ctx, &module.Job{ID: j.ID, Kind: "notes.x", Module: "notes", CreatedAt: Base}, 0), core.ErrExists, "duplicate id")
	wantErr(t, s.Enqueue(ctx, &module.Job{Kind: "notes.x", Module: "notes", CreatedAt: Base, ParentID: core.NewID()}, 0), core.ErrNotFound, "missing parent")

	c := claim(t, s, Base, "notes.x")
	if _, err := s.Complete(ctx, c.ID, c.Attempts, nil, Base); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.Enqueue(ctx, &module.Job{Kind: "notes.x", Module: "notes", CreatedAt: Base, ParentID: c.ID}, 0), core.ErrConflict, "finished parent")
	list, _ := s.List(ctx, module.JobFilter{})
	if len(list) != 1 {
		t.Fatalf("failed enqueues left %d jobs, want 1", len(list))
	}
}

func testQueueCap(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	for i := range 2 {
		if err := s.Enqueue(ctx, newJob("a.x", "a", Base.Add(time.Duration(i))), 2); err != nil {
			t.Fatal(err)
		}
	}
	err := s.Enqueue(ctx, newJob("a.x", "a", Base), 2)
	wantErr(t, err, module.ErrQueueFull, "third job")
	wantErr(t, err, core.ErrRateLimited, "third job")
	if err := s.Enqueue(ctx, newJob("b.x", "b", Base), 2); err != nil {
		t.Fatalf("other module blocked: %v", err)
	}
	c := claim(t, s, Base, "a.x")
	wantErr(t, s.Enqueue(ctx, newJob("a.x", "a", Base), 2), module.ErrQueueFull, "running job counts")
	if n, _ := s.CountQueued(ctx, "a"); n != 2 {
		t.Fatalf("CountQueued = %d, want 2", n)
	}
	if _, err := s.Complete(ctx, c.ID, c.Attempts, nil, Base); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(ctx, newJob("a.x", "a", Base), 2); err != nil {
		t.Fatalf("finished job still counts: %v", err)
	}
	list, _ := s.List(ctx, module.JobFilter{Module: "a", Statuses: []module.JobStatus{module.JobQueued}})
	if _, err := s.Pause(ctx, list[0].ID, Base); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.Enqueue(ctx, newJob("a.x", "a", Base), 2), module.ErrQueueFull, "paused job counts")
	if err := s.Enqueue(ctx, newJob("a.x", "a", Base), 0); err != nil {
		t.Fatalf("maxQueued 0 must not cap: %v", err)
	}
}

func testClaimOrder(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	late := enqueue(t, s, &module.Job{Kind: "a.x", Module: "a", CreatedAt: Base, RunAfter: Base.Add(2 * time.Minute)})
	second := enqueue(t, s, &module.Job{Kind: "a.x", Module: "a", CreatedAt: Base.Add(time.Second), RunAfter: Base})
	first := enqueue(t, s, &module.Job{Kind: "a.x", Module: "a", CreatedAt: Base.Add(-time.Second), RunAfter: Base})
	idA := "0" + strings.Repeat("a", 25)
	idB := "0" + strings.Repeat("b", 25)
	tieB := enqueue(t, s, &module.Job{ID: idB, Kind: "a.x", Module: "a", CreatedAt: Base.Add(2 * time.Second), RunAfter: Base})
	tieA := enqueue(t, s, &module.Job{ID: idA, Kind: "a.x", Module: "a", CreatedAt: Base.Add(2 * time.Second), RunAfter: Base})

	now := Base.Add(time.Minute)
	for _, want := range []*module.Job{first, second, tieA, tieB} {
		c := claim(t, s, now, "a.x")
		if c == nil || c.ID != want.ID {
			t.Fatalf("claimed %v, want %s", c, want.ID)
		}
		wantStatus(t, c, module.JobRunning)
		if c.Attempts != 1 {
			t.Fatalf("attempts %d, want 1", c.Attempts)
		}
		wantTime(t, c.LeaseUntil, now.Add(lease), "lease")
		wantTime(t, c.UpdatedAt, now, "updatedAt")
	}
	if c := claim(t, s, now, "a.x"); c != nil {
		t.Fatalf("claimed future job %s", c.ID)
	}
	if c := claim(t, s, Base.Add(2*time.Minute), "a.x"); c == nil || c.ID != late.ID {
		t.Fatalf("job due exactly at now not claimed: %v", c)
	}
	if c, err := s.Claim(ctx, now, lease, nil); c != nil || err != nil {
		t.Fatalf("empty kinds claimed %v, %v", c, err)
	}
}

func testClaimKinds(t *testing.T, s module.JobStore) {
	a := enqueue(t, s, newJob("a.x", "a", Base))
	b := enqueue(t, s, newJob("b.x", "b", Base.Add(-time.Hour)))
	if c := claim(t, s, Base, "a.x", "c.x"); c == nil || c.ID != a.ID {
		t.Fatalf("claimed %v, want %s", c, a.ID)
	}
	if c := claim(t, s, Base, "a.x"); c != nil {
		t.Fatalf("claimed %s of another kind", c.ID)
	}
	if c := claim(t, s, Base, "b.x"); c == nil || c.ID != b.ID {
		t.Fatalf("claimed %v, want %s", c, b.ID)
	}
}

func testFence(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	j := enqueue(t, s, newJob("a.x", "a", Base))
	c1 := claim(t, s, Base, "a.x")
	expired, err := s.RequeueExpired(ctx, Base.Add(lease))
	if err != nil || len(expired) != 1 || expired[0].Status != module.JobQueued || expired[0].Attempts != 1 {
		t.Fatalf("requeue: %v %v", expired, err)
	}
	c2 := claim(t, s, Base.Add(lease), "a.x")
	if c2 == nil || c2.Attempts != 2 {
		t.Fatalf("reclaim: %v", c2)
	}
	now := Base.Add(lease)
	p := int64(1)
	wantErr(t, s.RenewLease(ctx, j.ID, c1.Attempts, now, lease), module.ErrLeaseLost, "stale renew")
	_, err = s.Progress(ctx, j.ID, c1.Attempts, module.JobProgress{Progress: &p}, now, lease)
	wantErr(t, err, module.ErrLeaseLost, "stale progress")
	_, err = s.Complete(ctx, j.ID, c1.Attempts, nil, now)
	wantErr(t, err, module.ErrLeaseLost, "stale complete")
	wantErr(t, err, core.ErrConflict, "stale complete")
	_, err = s.Fail(ctx, j.ID, c1.Attempts, "x", now, now)
	wantErr(t, err, module.ErrLeaseLost, "stale fail")
	if g := get(t, s, j.ID); g.Status != module.JobRunning || g.Attempts != 2 || g.Progress != 0 {
		t.Fatalf("stale calls changed the job: %+v", g)
	}
	if err := s.RenewLease(ctx, j.ID, c2.Attempts, now.Add(time.Second), lease); err != nil {
		t.Fatal(err)
	}
	wantTime(t, get(t, s, j.ID).LeaseUntil, now.Add(time.Second+lease), "renewed lease")
	done, err := s.Complete(ctx, j.ID, c2.Attempts, json.RawMessage(`{"ok":true}`), now)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, done, module.JobDone)
	if !done.LeaseUntil.IsZero() {
		t.Fatalf("done job keeps a lease")
	}
	_, err = s.Complete(ctx, j.ID, c2.Attempts, nil, now)
	wantErr(t, err, module.ErrLeaseLost, "second complete")
	wantErr(t, s.RenewLease(ctx, core.NewID(), 1, now, lease), core.ErrNotFound, "unknown job")
}

func testProgress(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	enqueue(t, s, newJob("a.x", "a", Base))
	c := claim(t, s, Base, "a.x")
	p, total, msg := int64(3), int64(10), "three of ten"
	now := Base.Add(30 * time.Second)
	j, err := s.Progress(ctx, c.ID, c.Attempts, module.JobProgress{Progress: &p, Total: &total, Message: &msg}, now, lease)
	if err != nil {
		t.Fatal(err)
	}
	if j.Progress != 3 || j.Total != 10 || j.Message != msg {
		t.Fatalf("progress not applied: %+v", j)
	}
	wantTime(t, j.LeaseUntil, now.Add(lease), "lease after progress")
	p2 := int64(4)
	j, err = s.Progress(ctx, c.ID, c.Attempts, module.JobProgress{Progress: &p2}, now, lease)
	if err != nil || j.Total != 10 || j.Message != msg || j.Progress != 4 {
		t.Fatalf("nil fields must stay: %+v %v", j, err)
	}
	neg := int64(-1)
	_, err = s.Progress(ctx, c.ID, c.Attempts, module.JobProgress{Progress: &neg}, now, lease)
	wantErr(t, err, core.ErrInvalid, "negative progress")
	long := strings.Repeat("m", module.MaxJobMessage+1)
	_, err = s.Progress(ctx, c.ID, c.Attempts, module.JobProgress{Message: &long}, now, lease)
	wantErr(t, err, core.ErrInvalid, "long message")
}

func testFail(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	j := enqueue(t, s, &module.Job{Kind: "a.x", Module: "a", CreatedAt: Base, MaxAttempts: 2})
	c := claim(t, s, Base, "a.x")
	retry := Base.Add(30 * time.Second)
	f, err := s.Fail(ctx, j.ID, c.Attempts, "boom", retry, Base)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, f, module.JobQueued)
	wantTime(t, f.RunAfter, retry, "retry runAfter")
	if f.Message != "boom" || !f.LeaseUntil.IsZero() {
		t.Fatalf("fail fields: %+v", f)
	}
	if claim(t, s, retry.Add(-time.Nanosecond), "a.x") != nil {
		t.Fatalf("claimed before retry time")
	}
	c = claim(t, s, retry, "a.x")
	f, err = s.Fail(ctx, j.ID, c.Attempts, "boom again", retry.Add(time.Minute), retry)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, f, module.JobFailed)
	if f.Attempts != 2 {
		t.Fatalf("attempts %d, want 2", f.Attempts)
	}

	k := enqueue(t, s, newJob("b.x", "b", Base))
	c = claim(t, s, Base, "b.x")
	f, err = s.Fail(ctx, k.ID, c.Attempts, "permanent", time.Time{}, Base)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, f, module.JobFailed)
	if f.Attempts != 1 {
		t.Fatalf("permanent failure attempts %d, want 1", f.Attempts)
	}
}

func testPauseRunning(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	a := enqueue(t, s, newJob("a.x", "a", Base))
	c := claim(t, s, Base, "a.x")
	p, err := s.Pause(ctx, a.ID, Base)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != module.JobRunning || !p.PauseRequested {
		t.Fatalf("pause of running job: %+v", p)
	}
	f, err := s.Fail(ctx, a.ID, c.Attempts, "x", Base.Add(time.Minute), Base)
	if err != nil {
		t.Fatal(err)
	}
	if f.Status != module.JobPaused || f.PauseRequested {
		t.Fatalf("failed attempt with pause request: %+v", f)
	}
	if claim(t, s, Base.Add(time.Hour), "a.x") != nil {
		t.Fatalf("claimed a paused job")
	}
	r, err := s.Resume(ctx, a.ID, Base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, r, module.JobQueued)
	wantTime(t, r.RunAfter, Base.Add(time.Hour), "resume runAfter")

	b := enqueue(t, s, newJob("b.x", "b", Base))
	c = claim(t, s, Base, "b.x")
	if _, err := s.Pause(ctx, b.ID, Base); err != nil {
		t.Fatal(err)
	}
	done, err := s.Complete(ctx, b.ID, c.Attempts, nil, Base)
	if err != nil || done.Status != module.JobDone || done.PauseRequested {
		t.Fatalf("complete with pause request: %+v %v", done, err)
	}

	d := enqueue(t, s, newJob("d.x", "d", Base))
	c = claim(t, s, Base, "d.x")
	if _, err := s.Pause(ctx, d.ID, Base); err != nil {
		t.Fatal(err)
	}
	r, err = s.Resume(ctx, d.ID, Base)
	if err != nil || r.Status != module.JobRunning || r.PauseRequested {
		t.Fatalf("resume must withdraw the request: %+v %v", r, err)
	}
	f, err = s.Fail(ctx, d.ID, c.Attempts, "x", Base.Add(time.Minute), Base)
	if err != nil || f.Status != module.JobQueued {
		t.Fatalf("withdrawn pause still applied: %+v %v", f, err)
	}
}

func testPauseResumeQueued(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	j := enqueue(t, s, newJob("a.x", "a", Base))
	p, err := s.Pause(ctx, j.ID, Base)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, p, module.JobPaused)
	if p, err = s.Pause(ctx, j.ID, Base); err != nil || p.Status != module.JobPaused {
		t.Fatalf("pause twice: %+v %v", p, err)
	}
	r, err := s.Resume(ctx, j.ID, Base)
	if err != nil {
		t.Fatal(err)
	}
	wantStatus(t, r, module.JobQueued)
	if r, err = s.Resume(ctx, j.ID, Base); err != nil || r.Status != module.JobQueued {
		t.Fatalf("resume queued: %+v %v", r, err)
	}
	c := claim(t, s, Base, "a.x")
	if _, err := s.Complete(ctx, c.ID, c.Attempts, nil, Base); err != nil {
		t.Fatal(err)
	}
	_, err = s.Pause(ctx, j.ID, Base)
	wantErr(t, err, core.ErrConflict, "pause done job")
	_, err = s.Resume(ctx, j.ID, Base)
	wantErr(t, err, core.ErrConflict, "resume done job")
	_, err = s.Pause(ctx, core.NewID(), Base)
	wantErr(t, err, core.ErrNotFound, "pause unknown job")
}

func testRequeueExpired(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	idLow := "0" + strings.Repeat("1", 25)
	idMid := "0" + strings.Repeat("2", 25)
	idHigh := "0" + strings.Repeat("3", 25)
	enqueue(t, s, &module.Job{ID: idHigh, Kind: "a.x", Module: "a", CreatedAt: Base, MaxAttempts: 1})
	enqueue(t, s, &module.Job{ID: idLow, Kind: "a.x", Module: "a", CreatedAt: Base.Add(time.Second)})
	enqueue(t, s, &module.Job{ID: idMid, Kind: "a.x", Module: "a", CreatedAt: Base.Add(2 * time.Second)})
	fresh := enqueue(t, s, &module.Job{Kind: "a.x", Module: "a", CreatedAt: Base.Add(3 * time.Second)})
	early := Base.Add(2 * time.Second)
	for range 3 {
		if claim(t, s, early, "a.x") == nil {
			t.Fatalf("no job ready at %s", early)
		}
	}
	if _, err := s.Pause(ctx, idMid, early); err != nil {
		t.Fatal(err)
	}
	c := claim(t, s, Base.Add(30*time.Second), "a.x")
	if c == nil || c.ID != fresh.ID {
		t.Fatalf("claimed %v, want %s", c, fresh.ID)
	}
	if out, err := s.RequeueExpired(ctx, early.Add(lease-time.Nanosecond)); err != nil || len(out) != 0 {
		t.Fatalf("requeued before expiry: %v %v", out, err)
	}
	now := early.Add(lease)
	out, err := s.RequeueExpired(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].ID != idLow || out[1].ID != idMid || out[2].ID != idHigh {
		t.Fatalf("requeued %v, want the three expired jobs sorted by id", out)
	}
	wantStatus(t, out[0], module.JobQueued)
	wantTime(t, out[0].RunAfter, now, "requeued runAfter")
	wantStatus(t, out[1], module.JobPaused)
	if out[1].PauseRequested {
		t.Fatalf("pause request kept after expiry")
	}
	wantStatus(t, out[2], module.JobFailed)
	for _, j := range out {
		if j.Attempts != 1 || !j.LeaseUntil.IsZero() {
			t.Fatalf("expired job %s: attempts %d lease %s", j.ID, j.Attempts, j.LeaseUntil)
		}
	}
	wantStatus(t, get(t, s, fresh.ID), module.JobRunning)
}

func testCancelCascade(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	parent := enqueue(t, s, newJob("a.x", "a", Base))
	claim(t, s, Base, "a.x")
	child1 := enqueue(t, s, &module.Job{Kind: "a.c", Module: "a", CreatedAt: Base, ParentID: parent.ID})
	child2 := enqueue(t, s, &module.Job{Kind: "a.c", Module: "a", CreatedAt: Base.Add(time.Second), ParentID: parent.ID})
	grand := enqueue(t, s, &module.Job{Kind: "a.g", Module: "a", CreatedAt: Base, ParentID: child2.ID})
	c := claim(t, s, Base, "a.c")
	if c.ID != child1.ID {
		t.Fatalf("claimed %s, want %s", c.ID, child1.ID)
	}
	if _, err := s.Complete(ctx, child1.ID, c.Attempts, nil, Base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pause(ctx, grand.ID, Base); err != nil {
		t.Fatal(err)
	}
	later := Base.Add(time.Minute)
	changed, err := s.Cancel(ctx, parent.ID, later)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, j := range changed {
		ids = append(ids, j.ID)
		wantStatus(t, j, module.JobCanceled)
		wantTime(t, j.UpdatedAt, later, "canceled updatedAt")
	}
	if len(ids) != 3 || ids[0] != parent.ID || !slices.Contains(ids, child2.ID) || !slices.Contains(ids, grand.ID) {
		t.Fatalf("canceled %v, want parent first, then child2 and grandchild", ids)
	}
	wantStatus(t, get(t, s, child1.ID), module.JobDone)
	if again, err := s.Cancel(ctx, parent.ID, later); err != nil || len(again) != 0 {
		t.Fatalf("cancel twice: %v %v", again, err)
	}
	_, err = s.Cancel(ctx, child1.ID, later)
	wantErr(t, err, core.ErrConflict, "cancel done job")
	_, err = s.Cancel(ctx, core.NewID(), later)
	wantErr(t, err, core.ErrNotFound, "cancel unknown job")
	_, err = s.Complete(ctx, parent.ID, 1, nil, later)
	wantErr(t, err, module.ErrLeaseLost, "complete canceled job")
}

func testChildrenProgress(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	parent := enqueue(t, s, newJob("a.x", "a", Base))
	if f, n, err := s.ChildrenProgress(ctx, parent.ID); err != nil || f != 0 || n != 0 {
		t.Fatalf("no children: %d/%d %v", f, n, err)
	}
	var kids []*module.Job
	for i := range 3 {
		kids = append(kids, enqueue(t, s, &module.Job{Kind: "a.c", Module: "a", CreatedAt: Base.Add(time.Duration(i)), ParentID: parent.ID}))
	}
	c := claim(t, s, Base, "a.c")
	if _, err := s.Complete(ctx, c.ID, c.Attempts, nil, Base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(ctx, kids[2].ID, Base); err != nil {
		t.Fatal(err)
	}
	f, n, err := s.ChildrenProgress(ctx, parent.ID)
	if err != nil || f != 2 || n != 3 {
		t.Fatalf("children progress %d/%d %v, want 2/3", f, n, err)
	}
	g := get(t, s, parent.ID)
	if g.Children != 3 || g.ChildrenFinished != 2 {
		t.Fatalf("computed fields %d/%d, want 2/3", g.ChildrenFinished, g.Children)
	}
	if p, total := g.DisplayProgress(); p != 2 || total != 3 {
		t.Fatalf("display progress %d/%d, want 2/3", p, total)
	}
	_, _, err = s.ChildrenProgress(ctx, core.NewID())
	wantErr(t, err, core.ErrNotFound, "unknown parent")
}

func testList(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	space := core.NewID()
	var all []*module.Job
	for i := range 6 {
		mod := "a"
		if i%2 == 1 {
			mod = "b"
		}
		j := &module.Job{Kind: mod + ".x", Module: mod, CreatedAt: Base.Add(time.Duration(i) * time.Second), CreatedBy: fmt.Sprint("u", i%3)}
		if i < 3 {
			j.SpaceID = space
		}
		all = append(all, enqueue(t, s, j))
	}
	c := claim(t, s, Base.Add(time.Hour), "a.x")
	list, err := s.List(ctx, module.JobFilter{})
	if err != nil || len(list) != 6 {
		t.Fatalf("list all: %d %v", len(list), err)
	}
	for i := range list {
		if list[i].ID != all[5-i].ID {
			t.Fatalf("list order: position %d is %s, want %s", i, list[i].ID, all[5-i].ID)
		}
	}
	check := func(f module.JobFilter, want ...*module.Job) {
		t.Helper()
		got, err := s.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var g, w []string
		for _, j := range got {
			g = append(g, j.ID)
		}
		for _, j := range want {
			w = append(w, j.ID)
		}
		if !slices.Equal(g, w) {
			t.Fatalf("filter %+v: got %v, want %v", f, g, w)
		}
	}
	check(module.JobFilter{Module: "b"}, all[5], all[3], all[1])
	check(module.JobFilter{Kind: "a.x", Limit: 2}, all[4], all[2])
	check(module.JobFilter{SpaceID: space}, all[2], all[1], all[0])
	check(module.JobFilter{CreatedBy: "u1"}, all[4], all[1])
	check(module.JobFilter{Statuses: []module.JobStatus{module.JobRunning}}, get(t, s, c.ID))
	check(module.JobFilter{Module: "a", Statuses: []module.JobStatus{module.JobQueued, module.JobPaused}}, all[4], all[2])
	child := enqueue(t, s, &module.Job{Kind: "a.x", Module: "a", CreatedAt: Base, ParentID: c.ID})
	check(module.JobFilter{ParentID: c.ID}, child)
}

func testLogs(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	j := enqueue(t, s, newJob("a.x", "a", Base))
	for i, level := range []string{"info", "warn", "error", "debug"} {
		if err := s.Log(ctx, module.JobLogEntry{JobID: j.ID, At: Base.Add(time.Duration(i)), Level: level, Message: fmt.Sprint("line ", i)}); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := s.Logs(ctx, j.ID)
	if err != nil || len(logs) != 4 {
		t.Fatalf("logs: %v %v", logs, err)
	}
	for i, l := range logs {
		if l.Message != fmt.Sprint("line ", i) || l.JobID != j.ID {
			t.Fatalf("log %d: %+v", i, l)
		}
		wantTime(t, l.At, Base.Add(time.Duration(i)), "log time")
	}
	wantErr(t, s.Log(ctx, module.JobLogEntry{JobID: j.ID, At: Base, Level: "loud", Message: "x"}), core.ErrInvalid, "bad level")
	wantErr(t, s.Log(ctx, module.JobLogEntry{JobID: j.ID, At: Base, Level: "info", Message: strings.Repeat("x", module.MaxJobLogMessage+1)}), core.ErrInvalid, "long message")
	wantErr(t, s.Log(ctx, module.JobLogEntry{JobID: core.NewID(), At: Base, Level: "info", Message: "x"}), core.ErrNotFound, "unknown job")
	_, err = s.Logs(ctx, core.NewID())
	wantErr(t, err, core.ErrNotFound, "logs of unknown job")
}

func finish(t *testing.T, s module.JobStore, kind string, at time.Time) {
	t.Helper()
	c := claim(t, s, at, kind)
	if c == nil {
		t.Fatalf("no %s job to finish", kind)
	}
	if _, err := s.Complete(context.Background(), c.ID, c.Attempts, nil, at); err != nil {
		t.Fatal(err)
	}
}

func testDeleteFinished(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	old := Base.Add(-40 * 24 * time.Hour)
	cutoff := Base.Add(-30 * 24 * time.Hour)

	lone := enqueue(t, s, newJob("old.lone", "a", old))
	finish(t, s, "old.lone", old)
	if err := s.Log(ctx, module.JobLogEntry{JobID: lone.ID, At: old, Level: "info", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	recent := enqueue(t, s, newJob("recent", "a", Base))
	finish(t, s, "recent", Base)
	pending := enqueue(t, s, newJob("pending", "a", old))

	treeRoot := enqueue(t, s, newJob("tree.root", "a", old))
	claimedRoot := claim(t, s, old, "tree.root")
	treeKid := enqueue(t, s, &module.Job{Kind: "tree.kid", Module: "a", CreatedAt: old, ParentID: treeRoot.ID})
	if _, err := s.Complete(ctx, treeRoot.ID, claimedRoot.Attempts, nil, old); err != nil {
		t.Fatal(err)
	}
	finish(t, s, "tree.kid", old)

	busyRoot := enqueue(t, s, newJob("busy.root", "a", old))
	claimedBusy := claim(t, s, old, "busy.root")
	busyKid := enqueue(t, s, &module.Job{Kind: "busy.kid", Module: "a", CreatedAt: old, ParentID: busyRoot.ID})
	if _, err := s.Complete(ctx, busyRoot.ID, claimedBusy.Attempts, nil, old); err != nil {
		t.Fatal(err)
	}

	newRoot := enqueue(t, s, newJob("new.root", "a", old))
	claimedNew := claim(t, s, old, "new.root")
	newKid := enqueue(t, s, &module.Job{Kind: "new.kid", Module: "a", CreatedAt: old, ParentID: newRoot.ID})
	if _, err := s.Complete(ctx, newRoot.ID, claimedNew.Attempts, nil, old); err != nil {
		t.Fatal(err)
	}
	finish(t, s, "new.kid", Base)

	n, err := s.DeleteFinishedBefore(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("deleted %d jobs, want 3", n)
	}
	for _, id := range []string{lone.ID, treeRoot.ID, treeKid.ID} {
		_, err := s.Get(ctx, id)
		wantErr(t, err, core.ErrNotFound, "deleted job")
	}
	_, err = s.Logs(ctx, lone.ID)
	wantErr(t, err, core.ErrNotFound, "logs of deleted job")
	for _, id := range []string{recent.ID, pending.ID, busyRoot.ID, busyKid.ID, newRoot.ID, newKid.ID} {
		get(t, s, id)
	}
	if g := get(t, s, busyRoot.ID); g.Children != 1 {
		t.Fatalf("kept parent lost its child count: %+v", g)
	}
	if n, err := s.DeleteFinishedBefore(ctx, cutoff); err != nil || n != 0 {
		t.Fatalf("second delete: %d %v", n, err)
	}
}

func testLastRun(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	if lr, err := s.LastRun(ctx, "a.lint"); err != nil || !lr.IsZero() {
		t.Fatalf("initial last run %s %v", lr, err)
	}
	if err := s.SetLastRun(ctx, "a.lint", Base); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastRun(ctx, "a.lint", Base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	lr, err := s.LastRun(ctx, "a.lint")
	if err != nil {
		t.Fatal(err)
	}
	wantTime(t, lr, Base.Add(time.Hour), "last run")
	if lr, _ := s.LastRun(ctx, "b.lint"); !lr.IsZero() {
		t.Fatalf("last run leaked across kinds")
	}
}

func testCopies(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	j := enqueue(t, s, &module.Job{Kind: "a.x", Module: "a", CreatedAt: Base, Payload: json.RawMessage(`{"k":"v"}`)})
	j.Payload[2] = 'X'
	j.Status = module.JobDone
	g := get(t, s, j.ID)
	if string(g.Payload) != `{"k":"v"}` || g.Status != module.JobQueued {
		t.Fatalf("store shares memory with the enqueued job: %+v", g)
	}
	g.Payload[2] = 'Y'
	list, _ := s.List(ctx, module.JobFilter{})
	if string(list[0].Payload) != `{"k":"v"}` {
		t.Fatalf("store shares memory with Get results")
	}
}

func testConcurrentClaims(t *testing.T, s module.JobStore) {
	ctx := context.Background()
	const jobs = 60
	for i := range jobs {
		enqueue(t, s, newJob("a.x", "a", Base.Add(time.Duration(i))))
	}
	var (
		mu   sync.Mutex
		seen = map[string]int{}
		wg   sync.WaitGroup
	)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			for {
				c, err := s.Claim(ctx, Base.Add(time.Minute), lease, []string{"a.x"})
				if err != nil {
					errs <- err
					return
				}
				if c == nil {
					return
				}
				mu.Lock()
				seen[c.ID]++
				mu.Unlock()
				if _, err := s.Complete(ctx, c.ID, c.Attempts, nil, Base.Add(time.Minute)); err != nil {
					errs <- err
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
	if len(seen) != jobs {
		t.Fatalf("claimed %d distinct jobs, want %d", len(seen), jobs)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %s claimed %d times", id, n)
		}
	}
}
