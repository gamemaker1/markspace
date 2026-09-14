package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestOllamaCompleteReadsMessageAndCounts(t *testing.T) {
	var body []byte
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body = readAll(t, r)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"message":{"role":"assistant","content":"done"},"done":true,`+
			`"prompt_eval_count":7,"eval_count":2}`)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOllama, server.URL, func(cfg *core.AIProviderConfig) {
		cfg.MaxTokens = 64
		cfg.Temperature = 0.3
	})
	result, err := provider.Complete(context.Background(), []core.AIMessage{{Role: "user", Content: "hi"}}, core.AIOptions{Stop: []string{"END"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Text != "done" || result.PromptTokens != 7 || result.CompletionTokens != 2 {
		t.Fatalf("result = %+v", result)
	}
	if path != "/api/chat" {
		t.Fatalf("path = %q", path)
	}
	var sent ollamaChatRequest
	decodeBody(t, body, &sent)
	if sent.Stream {
		t.Fatal("stream flag set on a non-streaming call")
	}
	if sent.Options.NumPredict != 64 || sent.Options.Temperature != 0.3 {
		t.Fatalf("options = %+v", sent.Options)
	}
	if len(sent.Options.Stop) != 1 || sent.Options.Stop[0] != "END" {
		t.Fatalf("stop = %v", sent.Options.Stop)
	}
}

func TestOllamaStreamNDJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"message":{"content":"Hel"},"done":false}`+"\n")
		flush(t, w)
		io.WriteString(w, `{"message":{"content":"lo"},"done":false}`+"\n")
		flush(t, w)
		io.WriteString(w, `{"message":{"content":""},"done":true,"prompt_eval_count":3,"eval_count":2}`+"\n")
		flush(t, w)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOllama, server.URL, nil)
	ch, err := provider.Stream(context.Background(), []core.AIMessage{{Role: "user", Content: "hi"}}, core.AIOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	got := drain(t, ch)
	if got.err != nil {
		t.Fatalf("stream error: %v", got.err)
	}
	if got.text != "Hello" || len(got.deltas) != 2 {
		t.Fatalf("stream = %+v", got)
	}
	if !got.done || got.count != 3 {
		t.Fatalf("expected two deltas and one terminal chunk, got %+v", got)
	}
}

func TestOllamaEmbedUsesNewEndpoint(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %q", r.URL.Path)
		}
		body = readAll(t, r)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"embeddings":[[1,0],[0,1]]}`)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOllama, server.URL, nil)
	vectors, err := provider.Embed(context.Background(), []string{"a", "b"}, "nomic-embed-text")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("vectors = %v", vectors)
	}
	var sent ollamaEmbedRequest
	decodeBody(t, body, &sent)
	if sent.Model != "nomic-embed-text" || len(sent.Input) != 2 {
		t.Fatalf("request = %+v", sent)
	}
}

func TestOllamaEmbedFallsBackOn404(t *testing.T) {
	var prompts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/embed":
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"404 page not found"}`)
		case "/api/embeddings":
			var sent ollamaLegacyEmbedRequest
			decodeBody(t, readAll(t, r), &sent)
			prompts = append(prompts, sent.Prompt)
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"embedding":[0.5,0.5]}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOllama, server.URL, nil)
	vectors, err := provider.Embed(context.Background(), []string{"first", "second"}, "")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vectors) != 2 {
		t.Fatalf("vectors = %v", vectors)
	}
	for _, vector := range vectors {
		if len(vector) != 2 || vector[0] != 0.5 {
			t.Fatalf("vector = %v", vector)
		}
	}
	if len(prompts) != 2 || prompts[0] != "first" || prompts[1] != "second" {
		t.Fatalf("legacy prompts = %v", prompts)
	}
}

func TestOllamaListModelsAndHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"models":[{"name":"qwen3:8b"},{"name":"llama3.2:3b"}]}`)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOllama, server.URL, nil)
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0] != "llama3.2:3b" || models[1] != "qwen3:8b" {
		t.Fatalf("models = %v", models)
	}
	if err := provider.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

func TestOllamaRateLimitMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, "slow down")
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderOllama, server.URL, nil)
	_, err := provider.Complete(context.Background(), []core.AIMessage{{Role: "user", Content: "hi"}}, core.AIOptions{})
	if !errors.Is(err, core.ErrRateLimited) {
		t.Fatalf("error = %v", err)
	}
}
