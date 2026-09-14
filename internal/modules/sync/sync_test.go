package sync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// serve builds the API handler over b with this module registered.
func serve(t *testing.T, b core.Backend) http.Handler {
	t.Helper()
	env := testkit.Env(b)
	return testkit.Serve(t, env, New(env))
}

func TestEventsStreamEmitsEventAndStopsOnCancel(t *testing.T) {
	events := make(chan core.Event, 1)
	unsubscribed := make(chan struct{})
	fake := &testkit.Fake{
		SubscribeFn: func(context.Context, *core.Actor) (<-chan core.Event, func(), error) {
			return events, func() { close(unsubscribed) }, nil
		},
	}
	h := serve(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()

	events <- core.Event{Type: "note.updated", NoteID: "n1", SpaceID: "sp1", At: 42}
	// Give the handler a chance to write before the context is cancelled.
	deadline := time.Now().Add(2 * time.Second)
	for len(events) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after the context was cancelled")
	}
	select {
	case <-unsubscribed:
	default:
		t.Error("unsubscribe was not called")
	}

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: note.updated\n") {
		t.Errorf("body has no event line: %q", body)
	}
	if !strings.Contains(body, `"noteId":"n1"`) {
		t.Errorf("body has no event payload: %q", body)
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Errorf("event frame is not terminated: %q", body)
	}
}

func TestSyncPullParameters(t *testing.T) {
	var gotSince int64
	var gotLimit int
	fake := &testkit.Fake{
		SyncPullFn: func(_ context.Context, _ *core.Actor, since int64, limit int) (*core.SyncBatch, error) {
			gotSince, gotLimit = since, limit
			return &core.SyncBatch{Rev: 42, Notes: []*core.SyncNote{}}, nil
		},
	}
	h := serve(t, fake)

	rec := testkit.Do(t, h, "GET", "/api/sync", "", "")
	if rec.Code != http.StatusOK || gotSince != 0 || gotLimit != 200 {
		t.Fatalf("defaults: status %d since %d limit %d", rec.Code, gotSince, gotLimit)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var body map[string]any
	testkit.DecodeBody(t, rec, &body)
	for _, key := range []string{"rev", "more", "notes", "liveIds", "starredIds", "accessKey"} {
		if _, ok := body[key]; !ok {
			t.Errorf("response lacks %q: %v", key, body)
		}
	}

	testkit.Do(t, h, "GET", "/api/sync?since=17&limit=5000", "", "")
	if gotSince != 17 || gotLimit != 1000 {
		t.Fatalf("since %d limit %d, want 17 and the 1000 cap", gotSince, gotLimit)
	}
	for _, bad := range []string{"-1", "abc", "1.5"} {
		rec := testkit.Do(t, h, "GET", "/api/sync?since="+bad, "", "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("since=%s status %d, want 400", bad, rec.Code)
		}
	}
}
