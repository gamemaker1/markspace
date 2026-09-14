package index

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestAIThreadsRoundTrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	ann := mustUser(t, db, "ann@example.test")
	bob := mustUser(t, db, "bob@example.test")
	t0 := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

	th := &core.AIThread{UserID: ann.ID, NoteID: "n1", SpaceID: "s1", Title: "Plan", CreatedAt: t0}
	if err := db.CreateAIThread(ctx, th); err != nil {
		t.Fatal(err)
	}
	if th.ID == "" || !th.UpdatedAt.Equal(t0) {
		t.Fatalf("thread = %+v", th)
	}
	other := &core.AIThread{UserID: bob.ID, NoteID: "n1", CreatedAt: t0}
	if err := db.CreateAIThread(ctx, other); err != nil {
		t.Fatal(err)
	}

	q := &core.AIThreadMessage{ThreadID: th.ID, Role: core.AIRoleUser, Content: "What changed?",
		Context: []core.AIContextRef{{Kind: core.ContextNote, NoteID: "n1"}}, CreatedAt: t0.Add(time.Minute)}
	a := &core.AIThreadMessage{ThreadID: th.ID, Role: core.AIRoleAssistant, Content: "The date [1].",
		Sources: []core.AISource{{Kind: core.ContextNote, NoteID: "n1", Title: "Plan"}}, CreatedAt: t0.Add(time.Minute)}
	for _, m := range []*core.AIThreadMessage{q, a} {
		if err := db.AddAIMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.GetAIThread(ctx, th.ID)
	if err != nil || !got.UpdatedAt.Equal(t0.Add(time.Minute)) || got.UserID != ann.ID || got.Title != "Plan" {
		t.Fatalf("thread after messages = %+v, %v", got, err)
	}
	msgs, err := db.ListAIMessages(ctx, th.ID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("messages = %+v, %v", msgs, err)
	}
	if msgs[0].ID != q.ID || msgs[1].ID != a.ID || msgs[0].Context[0].NoteID != "n1" || msgs[1].Sources[0].Title != "Plan" {
		t.Fatalf("messages out of order or lost fields: %+v %+v", msgs[0], msgs[1])
	}
	if msgs[0].Sources == nil || msgs[1].Context == nil {
		t.Fatal("empty arrays came back nil")
	}

	// An older message does not move updated_at back.
	early := &core.AIThreadMessage{ThreadID: th.ID, Role: core.AIRoleUser, Content: "late write", CreatedAt: t0}
	if err := db.AddAIMessage(ctx, early); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetAIThread(ctx, th.ID); !got.UpdatedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("updated_at moved back to %v", got.UpdatedAt)
	}

	list, err := db.ListAIThreads(ctx, ann.ID, "n1", time.Time{})
	if err != nil || len(list) != 1 || list[0].ID != th.ID {
		t.Fatalf("ann's threads = %+v, %v", list, err)
	}
	if list, _ := db.ListAIThreads(ctx, ann.ID, "n1", t0.Add(2*time.Minute)); len(list) != 0 {
		t.Fatalf("since filter kept %+v", list)
	}
	if list, _ := db.ListAIThreads(ctx, ann.ID, "n2", time.Time{}); len(list) != 0 {
		t.Fatalf("other note listed %+v", list)
	}

	if err := db.SetAIMessageApplied(ctx, th.ID, a.ID, true); err != nil {
		t.Fatal(err)
	}
	msgs, _ = db.ListAIMessages(ctx, th.ID)
	for _, m := range msgs {
		if m.Applied != (m.ID == a.ID) {
			t.Fatalf("message %s applied = %v", m.Content, m.Applied)
		}
	}
	if err := db.SetAIMessageApplied(ctx, other.ID, a.ID, true); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("applied through another thread = %v", err)
	}

	if err := db.AddAIMessage(ctx, &core.AIThreadMessage{ThreadID: "missing", Role: core.AIRoleUser}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("message on a missing thread = %v", err)
	}
	if err := db.AddAIMessage(ctx, &core.AIThreadMessage{ThreadID: th.ID, Role: "system"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("system role = %v", err)
	}

	if err := db.DeleteAIThread(ctx, th.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAIThread(ctx, th.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("deleted thread = %v", err)
	}
	if msgs, _ := db.ListAIMessages(ctx, th.ID); len(msgs) != 0 {
		t.Fatalf("messages survived their thread: %+v", msgs)
	}
	if err := db.DeleteAIThread(ctx, th.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestDeleteAIThreadsBeforeAndUserCascade(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	ann := mustUser(t, db, "ann@example.test")
	bob := mustUser(t, db, "bob@example.test")
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	cutoff := now.Add(-core.AIThreadRetention)
	mk := func(user string, updated time.Time) *core.AIThread {
		th := &core.AIThread{UserID: user, NoteID: "n", CreatedAt: updated.Add(-time.Hour), UpdatedAt: updated}
		if err := db.CreateAIThread(ctx, th); err != nil {
			t.Fatal(err)
		}
		return th
	}
	annOld := mk(ann.ID, cutoff.Add(-time.Second))
	annEdge := mk(ann.ID, cutoff)
	bobOld := mk(bob.ID, cutoff.Add(-24*time.Hour))
	bobNew := mk(bob.ID, now)

	n, err := db.DeleteAIThreadsBefore(ctx, ann.ID, cutoff)
	if err != nil || n != 1 {
		t.Fatalf("ann purge = %d, %v", n, err)
	}
	if _, err := db.GetAIThread(ctx, annOld.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("ann's old thread survived")
	}
	if _, err := db.GetAIThread(ctx, bobOld.ID); err != nil {
		t.Fatal("a purge for ann deleted bob's thread")
	}
	if _, err := db.GetAIThread(ctx, annEdge.ID); err != nil {
		t.Fatal("a thread active exactly at the cutoff was deleted")
	}
	if n, err := db.DeleteAIThreadsBefore(ctx, "", cutoff); err != nil || n != 1 {
		t.Fatalf("global purge = %d, %v", n, err)
	}
	if _, err := db.GetAIThread(ctx, bobNew.ID); err != nil {
		t.Fatal("a recent thread was deleted")
	}

	if _, err := db.sql.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAIThread(ctx, bobNew.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("deleting a user kept their threads")
	}
	if err := db.CreateAIThread(ctx, &core.AIThread{UserID: "nobody"}); !errors.Is(err, core.ErrExists) {
		t.Fatalf("thread for an unknown user = %v", err)
	}
}
