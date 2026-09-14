package ai

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// threadsBackend adds the conversation and image services to the fake
// backend.
type threadsBackend struct {
	*testkit.Fake
	mu       sync.Mutex
	calls    []string
	images   func(req core.AIImageGenerateRequest, progress func(core.AIImageProgress)) (*core.AIImageGenerateResult, error)
	imageReq core.AIImageGenerateRequest
}

func (b *threadsBackend) record(call string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, call)
}

func (b *threadsBackend) ListAIThreads(_ context.Context, actor *core.Actor, noteID string) ([]*core.AIThread, error) {
	if !actor.IsUser() {
		return nil, core.ErrUnauthorized
	}
	b.record("list " + noteID)
	return []*core.AIThread{{ID: "t1", UserID: actor.UserID(), NoteID: noteID, Messages: []*core.AIThreadMessage{}}}, nil
}

func (b *threadsBackend) CreateAIThread(_ context.Context, actor *core.Actor, in core.AIThreadInput) (*core.AIThread, error) {
	if !actor.IsUser() {
		return nil, core.ErrUnauthorized
	}
	b.record("create " + in.NoteID + " " + in.Title)
	return &core.AIThread{ID: "t1", UserID: actor.UserID(), NoteID: in.NoteID, Title: in.Title}, nil
}

func (b *threadsBackend) StreamAIThreadMessage(_ context.Context, actor *core.Actor, id string, in core.AIThreadMessageInput) (<-chan core.AIThreadChunk, error) {
	if !actor.IsUser() {
		return nil, core.ErrUnauthorized
	}
	if id != "t1" {
		return nil, core.ErrNotFound
	}
	b.record("stream " + id + " " + in.Content + " " + in.Scope)
	ch := make(chan core.AIThreadChunk, 3)
	ch <- core.AIThreadChunk{AIChunk: core.AIChunk{Delta: "The quay "}}
	ch <- core.AIThreadChunk{AIChunk: core.AIChunk{Delta: "is sound."}}
	ch <- core.AIThreadChunk{
		AIChunk: core.AIChunk{Done: true, Model: "chat", Provider: "local", Attempts: 2,
			Sources: []core.AISource{{Kind: core.ContextNote, NoteID: "n1", Title: "Survey"}}},
		Route:       core.RouteChat,
		UserMessage: &core.AIThreadMessage{ID: "m1", Role: core.AIRoleUser, Content: in.Content},
		Message:     &core.AIThreadMessage{ID: "m2", Role: core.AIRoleAssistant, Content: "The quay is sound."},
	}
	close(ch)
	return ch, nil
}

func (b *threadsBackend) SetAIMessageApplied(_ context.Context, actor *core.Actor, id, messageID string, applied bool) error {
	if id != "t1" {
		return core.ErrNotFound
	}
	if applied {
		b.record("applied " + id + " " + messageID)
	} else {
		b.record("unapplied " + id + " " + messageID)
	}
	return nil
}

func (b *threadsBackend) DeleteAIThread(_ context.Context, actor *core.Actor, id string) error {
	if id != "t1" {
		return core.ErrNotFound
	}
	b.record("delete " + id)
	return nil
}

func (b *threadsBackend) GenerateImages(_ context.Context, actor *core.Actor, req core.AIImageGenerateRequest, progress func(core.AIImageProgress)) (*core.AIImageGenerateResult, error) {
	b.imageReq = req
	return b.images(req, progress)
}

// newThreadsServer builds a server over a backend with the conversation and
// image services, signed in as u1.
func newThreadsServer(t *testing.T) (http.Handler, *threadsBackend) {
	t.Helper()
	b := &threadsBackend{Fake: testkit.SignedIn(false)}
	return serve(t, b), b
}

func TestAIThreadRoutes(t *testing.T) {
	h, b := newThreadsServer(t)

	rec := testkit.Do(t, h, "GET", "/api/ai/threads?noteId=n1", "", "s")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"items":[{"id":"t1"`) {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	rec = testkit.Do(t, h, "POST", "/api/ai/threads", `{"noteId":"n1","title":"Plan"}`, "s")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if rec = testkit.Do(t, h, "POST", "/api/ai/threads", `{"noteId":"n1","userId":"u2"}`, "s"); rec.Code != http.StatusBadRequest {
		t.Fatalf("create with an unknown field = %d", rec.Code)
	}

	rec = testkit.Do(t, h, "POST", "/api/ai/threads/t1/messages", `{"content":"Is it sound?","scope":"space","context":[{"kind":"note","noteId":"n1"}]}`, "s")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream = %d %s", rec.Code, rec.Body)
	}
	names, data := testkit.SSEEvents(rec.Body.String())
	if strings.Join(names, ",") != "delta,delta,done" {
		t.Fatalf("events = %v", names)
	}
	done := data[2]
	for _, want := range []string{`"done":true`, `"route":"chat"`, `"attempts":2`, `"provider":"local"`, `"userMessage":{"id":"m1"`, `"message":{"id":"m2"`, `"sources":[{"kind":"note"`} {
		if !strings.Contains(done, want) {
			t.Fatalf("done frame lacks %s: %s", want, done)
		}
	}
	if rec = testkit.Do(t, h, "POST", "/api/ai/threads/other/messages", `{"content":"x"}`, "s"); rec.Code != http.StatusNotFound {
		t.Fatalf("stream on another user's thread = %d", rec.Code)
	}

	if rec = testkit.Do(t, h, "PUT", "/api/ai/threads/t1/messages/m2/applied", `{"applied":true}`, "s"); rec.Code != http.StatusNoContent {
		t.Fatalf("applied = %d %s", rec.Code, rec.Body)
	}
	if rec = testkit.Do(t, h, "PUT", "/api/ai/threads/t1/messages/m2/applied", `{}`, "s"); rec.Code != http.StatusBadRequest {
		t.Fatalf("applied without a value = %d", rec.Code)
	}
	if rec = testkit.Do(t, h, "DELETE", "/api/ai/threads/t1", "", "s"); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	if rec = testkit.Do(t, h, "DELETE", "/api/ai/threads/zz", "", "s"); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing = %d", rec.Code)
	}
	want := "list n1,create n1 Plan,stream t1 Is it sound? space,applied t1 m2,delete t1"
	if got := strings.Join(b.calls, ","); got != want {
		t.Fatalf("calls = %s\nwant    %s", got, want)
	}

	anon := serve(t, &threadsBackend{Fake: &testkit.Fake{}})
	if rec = testkit.Do(t, anon, "GET", "/api/ai/threads?noteId=n1", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d", rec.Code)
	}
}

func TestAIImageStream(t *testing.T) {
	h, b := newThreadsServer(t)

	b.images = func(req core.AIImageGenerateRequest, progress func(core.AIImageProgress)) (*core.AIImageGenerateResult, error) {
		return nil, core.Invalid("prompt", "a prompt is required")
	}
	rec := testkit.Do(t, h, "POST", "/api/ai/image/stream", `{"prompt":""}`, "s")
	if rec.Code != http.StatusBadRequest || strings.Contains(rec.Header().Get("Content-Type"), "event-stream") {
		t.Fatalf("refused request = %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}

	b.images = func(req core.AIImageGenerateRequest, progress func(core.AIImageProgress)) (*core.AIImageGenerateResult, error) {
		pos := 1
		progress(core.AIImageProgress{Done: 0, Total: 2, QueuePosition: &pos})
		progress(core.AIImageProgress{Done: 1, Total: 2})
		progress(core.AIImageProgress{Done: 2, Total: 2})
		return &core.AIImageGenerateResult{
			Files:  []*core.File{},
			Images: []core.AIImageOutput{{DataURL: "data:image/png;base64,AAA"}, {DataURL: "data:image/png;base64,BBB"}},
			Usage:  core.AIImageUsage{ImagesOut: 2}, Provider: "pics", Model: "img-1", Route: core.RouteImageGenerate, Attempts: 1,
		}, nil
	}
	body := `{"spaceId":"s1","noteId":"n1","prompt":"A lighthouse","size":"1024x1024","n":2,"save":"none","route":"plugin.wiki.draw","pluginId":"someone-else","mask":"` + strings.Repeat("A", 3<<20) + `"}`
	rec = testkit.Do(t, h, "POST", "/api/ai/image/stream", body, "s")
	if rec.Code != http.StatusOK {
		t.Fatalf("stream = %d %.200s", rec.Code, rec.Body)
	}
	names, data := testkit.SSEEvents(rec.Body.String())
	if strings.Join(names, ",") != "progress,progress,progress,done" {
		t.Fatalf("events = %v", names)
	}
	if data[0] != `{"done":0,"total":2,"queuePosition":1}` || data[2] != `{"done":2,"total":2}` {
		t.Fatalf("progress frames = %v", data[:3])
	}
	for _, want := range []string{`"done":true`, `"files":[]`, `"images":[{"dataUrl":"data:image/png;base64,AAA"}`, `"usage":{"imagesIn":0,"imagesOut":2}`, `"route":"image.generate"`} {
		if !strings.Contains(data[3], want) {
			t.Fatalf("done frame lacks %s: %s", want, data[3])
		}
	}
	if b.imageReq.N != 2 || b.imageReq.NoteID != "n1" || b.imageReq.Save != "none" || len(b.imageReq.Mask) != 3<<20 {
		t.Fatalf("decoded request = %+v", b.imageReq.Prompt)
	}
	if b.imageReq.PluginID != "" || b.imageReq.Route != "plugin.wiki.draw" {
		t.Fatalf("pluginId %q was not cleared or route %q was lost", b.imageReq.PluginID, b.imageReq.Route)
	}

	b.images = func(req core.AIImageGenerateRequest, progress func(core.AIImageProgress)) (*core.AIImageGenerateResult, error) {
		progress(core.AIImageProgress{Total: 1})
		return nil, errors.New("disk exploded at /var/secret")
	}
	rec = testkit.Do(t, h, "POST", "/api/ai/image/stream", `{"prompt":"x"}`, "s")
	names, data = testkit.SSEEvents(rec.Body.String())
	if strings.Join(names, ",") != "progress,error" || data[1] != `{"error":"internal error"}` {
		t.Fatalf("late failure = %v %v", names, data)
	}

	b.images = func(req core.AIImageGenerateRequest, progress func(core.AIImageProgress)) (*core.AIImageGenerateResult, error) {
		progress(core.AIImageProgress{Total: 1})
		return nil, core.ErrQuotaExceeded
	}
	rec = testkit.Do(t, h, "POST", "/api/ai/image/stream", `{"prompt":"x"}`, "s")
	if names, _ = testkit.SSEEvents(rec.Body.String()); strings.Join(names, ",") != "progress,error" {
		t.Fatalf("mapped late failure = %v", names)
	}
	if rec = testkit.Do(t, h, "POST", "/api/ai/image/stream", `{"prompt":"x","surprise":1}`, "s"); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", rec.Code)
	}
}
