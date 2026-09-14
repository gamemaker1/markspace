package ai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// serve builds the API handler over b with this module registered.
func serve(t *testing.T, b core.Backend) http.Handler {
	t.Helper()
	env := testkit.Env(b)
	return testkit.Serve(t, env, New(env))
}

func TestAIStreamEmitsDeltaThenDone(t *testing.T) {
	chunks := make(chan core.AIChunk, 3)
	chunks <- core.AIChunk{Delta: "Hello "}
	chunks <- core.AIChunk{Delta: "world"}
	chunks <- core.AIChunk{Done: true}
	close(chunks)

	var gotReq core.AIRequest
	fake := &testkit.Fake{
		StreamAIFn: func(_ context.Context, _ *core.Actor, req core.AIRequest) (<-chan core.AIChunk, error) {
			gotReq = req
			return chunks, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "POST", "/api/ai/stream",
		`{"task":"summarize","text":"long text"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if gotReq.Task != core.TaskSummarize || gotReq.Text != "long text" {
		t.Errorf("request = %+v", gotReq)
	}
	body := rec.Body.String()
	want := "event: delta\ndata: {\"delta\":\"Hello \"}\n\n" +
		"event: delta\ndata: {\"delta\":\"world\"}\n\n" +
		"event: done\ndata: {\"done\":true}\n\n"
	if body != want {
		t.Errorf("body = %q\nwant  %q", body, want)
	}
}

func TestAIStreamEmitsErrorFrame(t *testing.T) {
	chunks := make(chan core.AIChunk, 1)
	chunks <- core.AIChunk{Err: fmt.Errorf("provider refused")}
	close(chunks)
	fake := &testkit.Fake{
		StreamAIFn: func(context.Context, *core.Actor, core.AIRequest) (<-chan core.AIChunk, error) {
			return chunks, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "POST", "/api/ai/stream", `{"task":"ask"}`, "")
	if !strings.Contains(rec.Body.String(), "event: error\ndata: {\"error\":\"provider refused\"}") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestAIProviderAcceptsAPIKeyAndNeverReturnsIt(t *testing.T) {
	var got []*core.AIProviderConfig
	fake := &testkit.Fake{
		SaveAIProviderFn: func(_ context.Context, _ *core.Actor, cfg *core.AIProviderConfig) (*core.AIProviderConfig, error) {
			got = append(got, cfg)
			out := *cfg
			out.HasAPIKey = cfg.APIKey != ""
			out.APIKey = cfg.APIKey // a careless backend: the handler must still not leak it
			return &out, nil
		},
	}
	h := serve(t, fake)

	rec := testkit.Do(t, h, "POST", "/api/ai/providers",
		`{"id":"local","kind":"openai","baseUrl":"http://127.0.0.1:8000/v1","model":"m","apiKey":"sk-secret"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body)
	}
	if len(got) != 1 || got[0].APIKey != "sk-secret" {
		t.Fatalf("backend did not receive the API key: %+v", got)
	}
	if !got[0].Enabled {
		t.Fatal("a provider created without an enabled flag should be enabled")
	}
	if strings.Contains(rec.Body.String(), "sk-secret") {
		t.Fatalf("create response leaks the key: %s", rec.Body)
	}

	rec = testkit.Do(t, h, "PATCH", "/api/ai/providers/from-path",
		`{"id":"from-body","baseUrl":"http://x","apiKey":"sk-two"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d: %s", rec.Code, rec.Body)
	}
	if got[1].ID != "from-path" || got[1].APIKey != "sk-two" {
		t.Fatalf("update forwarded id %q key %q", got[1].ID, got[1].APIKey)
	}
	if strings.Contains(rec.Body.String(), "sk-two") {
		t.Fatalf("update response leaks the key: %s", rec.Body)
	}
}

func TestAIProviderEnabledFlag(t *testing.T) {
	var got *core.AIProviderConfig
	fake := &testkit.Fake{
		SaveAIProviderFn: func(_ context.Context, _ *core.Actor, cfg *core.AIProviderConfig) (*core.AIProviderConfig, error) {
			got = cfg
			return cfg, nil
		},
	}
	h := serve(t, fake)

	testkit.Do(t, h, "POST", "/api/ai/providers", `{"id":"a","baseUrl":"http://x","enabled":false}`, "")
	if got == nil || got.Enabled {
		t.Fatalf("explicit enabled=false was not honoured: %+v", got)
	}

	// With no stored provider to read, an update without the flag enables it.
	testkit.Do(t, h, "PATCH", "/api/ai/providers/a", `{"baseUrl":"http://y"}`, "")
	if got == nil || !got.Enabled {
		t.Fatalf("update without enabled flag = %+v, want enabled", got)
	}
}

func TestAIStreamDoneCarriesSources(t *testing.T) {
	chunks := make(chan core.AIChunk, 2)
	chunks <- core.AIChunk{Delta: "ok"}
	chunks <- core.AIChunk{Done: true, Model: "m", Sources: []core.AISource{
		{Kind: core.ContextNote, NoteID: "n1", Title: "Plan", Path: "plan.md", Truncated: true},
		{Kind: core.ContextURL, URL: "http://10.0.0.1/", Skipped: "private address not allowed"},
	}}
	close(chunks)
	fake := &testkit.Fake{
		StreamAIFn: func(context.Context, *core.Actor, core.AIRequest) (<-chan core.AIChunk, error) {
			return chunks, nil
		},
	}
	rec := testkit.Do(t, serve(t, fake), "POST", "/api/ai/stream", `{"task":"summarize"}`, "")
	want := "event: delta\ndata: {\"delta\":\"ok\"}\n\n" +
		"event: done\ndata: {\"done\":true,\"model\":\"m\",\"sources\":[" +
		"{\"kind\":\"note\",\"noteId\":\"n1\",\"title\":\"Plan\",\"path\":\"plan.md\",\"truncated\":true}," +
		"{\"kind\":\"url\",\"url\":\"http://10.0.0.1/\",\"title\":\"\",\"skipped\":\"private address not allowed\"}]}\n\n"
	if body := rec.Body.String(); body != want {
		t.Errorf("body = %q\nwant  %q", body, want)
	}
}
