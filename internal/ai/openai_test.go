package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestOpenAICompleteReadsChoiceAndUsage(t *testing.T) {
	var body []byte
	var path, auth, extra string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		extra = r.Header.Get("X-Extra")
		body = readAll(t, r)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Hello there"}}],`+
			`"usage":{"prompt_tokens":11,"completion_tokens":3}}`)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL+"/v1", func(cfg *core.AIProviderConfig) {
		cfg.APIKey = "secret-key"
		cfg.Headers = map[string]string{"X-Extra": "yes"}
		cfg.MaxTokens = 128
	})
	msgs := []core.AIMessage{{Role: "user", Content: "hi"}}
	result, err := provider.Complete(context.Background(), msgs, core.AIOptions{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Text != "Hello there" {
		t.Fatalf("text = %q", result.Text)
	}
	if result.PromptTokens != 11 || result.CompletionTokens != 3 {
		t.Fatalf("usage = %d/%d", result.PromptTokens, result.CompletionTokens)
	}
	if result.Provider != "p1" || result.Model != "test-model" {
		t.Fatalf("result identity = %q %q", result.Provider, result.Model)
	}
	if path != "/v1/chat/completions" {
		t.Fatalf("path = %q", path)
	}
	if auth != "Bearer secret-key" {
		t.Fatalf("authorization = %q", auth)
	}
	if extra != "yes" {
		t.Fatalf("custom header = %q", extra)
	}
	var sent openaiChatRequest
	decodeBody(t, body, &sent)
	if sent.Model != "test-model" || sent.Stream || sent.MaxTokens != 128 {
		t.Fatalf("request = %+v", sent)
	}
	if len(sent.Messages) != 1 || sent.Messages[0].Content != "hi" {
		t.Fatalf("messages = %+v", sent.Messages)
	}
}

func TestOpenAIStreamSplitFrame(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = readAll(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, ": keepalive\n\n")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"Hello"}}]}`+"\n\n")
		flush(t, w)
		// One frame split across two writes.
		io.WriteString(w, `data: {"choices":[{"delta":{"content":" wor`)
		flush(t, w)
		io.WriteString(w, `ld"}}]}`+"\n\n")
		flush(t, w)
		io.WriteString(w, "data: [DONE]\n\n")
		flush(t, w)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
	ch, err := provider.Stream(context.Background(), []core.AIMessage{{Role: "user", Content: "hi"}}, core.AIOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	got := drain(t, ch)
	if got.err != nil {
		t.Fatalf("stream error: %v", got.err)
	}
	if got.text != "Hello world" {
		t.Fatalf("text = %q (deltas %q)", got.text, got.deltas)
	}
	if !got.done || got.count != len(got.deltas)+1 {
		t.Fatalf("expected one terminal chunk, got %+v", got)
	}
	var sent openaiChatRequest
	decodeBody(t, body, &sent)
	if !sent.Stream {
		t.Fatal("stream flag not set on the request")
	}
}

func TestOpenAIStreamToleratesPlainJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"one shot"}}]}`)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
	ch, err := provider.Stream(context.Background(), []core.AIMessage{{Role: "user", Content: "hi"}}, core.AIOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	got := drain(t, ch)
	if got.err != nil || got.text != "one shot" || !got.done {
		t.Fatalf("stream = %+v", got)
	}
}

func TestOpenAIStreamContextCancel(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"partial"}}]}`+"\n\n")
		flush(t, w)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := provider.Stream(ctx, []core.AIMessage{{Role: "user", Content: "hi"}}, core.AIOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	first, ok := <-ch
	if !ok || first.Delta != "partial" {
		t.Fatalf("first chunk = %+v (open %v)", first, ok)
	}
	cancel()
	got := drain(t, ch)
	if got.done {
		t.Fatal("canceled stream reported Done")
	}
}

func TestOpenAIEmbedOrdersByIndex(t *testing.T) {
	var body []byte
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body = readAll(t, r)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"index":1,"embedding":[0.5,0.25]},{"index":0,"embedding":[1,0]}]}`)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL, func(cfg *core.AIProviderConfig) {
		cfg.EmbedModel = "embed-model"
	})
	vectors, err := provider.Embed(context.Background(), []string{"a", "b"}, "")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if path != "/v1/embeddings" {
		t.Fatalf("path = %q", path)
	}
	if len(vectors) != 2 {
		t.Fatalf("vectors = %v", vectors)
	}
	if vectors[0][0] != 1 || vectors[0][1] != 0 {
		t.Fatalf("first vector = %v", vectors[0])
	}
	if vectors[1][0] != 0.5 || vectors[1][1] != 0.25 {
		t.Fatalf("second vector = %v", vectors[1])
	}
	var sent openaiEmbedRequest
	decodeBody(t, body, &sent)
	if sent.Model != "embed-model" || len(sent.Input) != 2 {
		t.Fatalf("request = %+v", sent)
	}
}

func TestOpenAIListModelsSorted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"zephyr"},{"id":"alpha"},{"id":""}]}`)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0] != "alpha" || models[1] != "zephyr" {
		t.Fatalf("models = %v", models)
	}
}

func TestOpenAIStatusMapping(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{"unauthorized", http.StatusUnauthorized, core.ErrUnauthorized},
		{"forbidden", http.StatusForbidden, core.ErrUnauthorized},
		{"rate limited", http.StatusTooManyRequests, core.ErrRateLimited},
		{"not found", http.StatusNotFound, core.ErrNotFound},
		{"too large", http.StatusRequestEntityTooLarge, core.ErrTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, "{\"error\":{\"message\":\"nope secret-key\"}}\n")
			}))
			defer server.Close()

			provider := testProvider(t, core.ProviderOpenAI, server.URL, func(cfg *core.AIProviderConfig) {
				cfg.APIKey = "secret-key"
			})
			_, err := provider.Complete(context.Background(), []core.AIMessage{{Role: "user", Content: "hi"}}, core.AIOptions{})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error %v does not match %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), "openai: ") || !strings.Contains(err.Error(), "from POST "+server.URL) {
				t.Fatalf("error text = %q", err.Error())
			}
			if strings.Contains(err.Error(), "secret-key") {
				t.Fatalf("error text leaked the api key: %q", err.Error())
			}
		})
	}
}

func TestOpenAIServerErrorHasNoSentinel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "upstream down")
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
	_, err := provider.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, sentinel := range []error{core.ErrUnauthorized, core.ErrNotFound, core.ErrRateLimited, core.ErrTooLarge} {
		if errors.Is(err, sentinel) {
			t.Fatalf("502 mapped to %v", sentinel)
		}
	}
	if !strings.Contains(err.Error(), "502 from GET") {
		t.Fatalf("error text = %q", err.Error())
	}
}

func TestOpenAIHealthFallsBackToCompletion(t *testing.T) {
	var completions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, "not found")
		case "/v1/chat/completions":
			completions.Add(1)
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
	if err := provider.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if completions.Load() != 1 {
		t.Fatalf("completion probes = %d", completions.Load())
	}
}
