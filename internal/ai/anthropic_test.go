package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestAnthropicCompleteHoistsSystemAndMergesRoles(t *testing.T) {
	var body []byte
	var path, key, version string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		key = r.Header.Get("x-api-key")
		version = r.Header.Get("anthropic-version")
		body = readAll(t, r)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"content":[{"type":"text","text":"one "},{"type":"thinking","thinking":"skip"},`+
			`{"type":"text","text":"two"}],"usage":{"input_tokens":9,"output_tokens":4}}`)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderAnthropic, server.URL, func(cfg *core.AIProviderConfig) {
		cfg.APIKey = "anthropic-key"
	})
	msgs := []core.AIMessage{
		{Role: "system", Content: "rule one"},
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
		{Role: "system", Content: "rule two"},
		{Role: "assistant", Content: "answer"},
		{Role: "tool", Content: "treated as user"},
	}
	result, err := provider.Complete(context.Background(), msgs, core.AIOptions{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Text != "one two" {
		t.Fatalf("text = %q", result.Text)
	}
	if result.PromptTokens != 9 || result.CompletionTokens != 4 {
		t.Fatalf("usage = %d/%d", result.PromptTokens, result.CompletionTokens)
	}
	if path != "/v1/messages" {
		t.Fatalf("path = %q", path)
	}
	if key != "anthropic-key" || version != anthropicVersion {
		t.Fatalf("headers = %q %q", key, version)
	}

	var sent anthropicChatRequest
	decodeBody(t, body, &sent)
	if sent.System != "rule one\n\nrule two" {
		t.Fatalf("system = %q", sent.System)
	}
	if sent.MaxTokens != anthropicDefaultMaxTokens {
		t.Fatalf("max_tokens = %d", sent.MaxTokens)
	}
	want := []anthropicMessage{
		{Role: "user", Content: "first\n\nsecond"},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "treated as user"},
	}
	if len(sent.Messages) != len(want) {
		t.Fatalf("messages = %+v", sent.Messages)
	}
	for i := range want {
		if !reflect.DeepEqual(sent.Messages[i], want[i]) {
			t.Fatalf("message %d = %+v, want %+v", i, sent.Messages[i], want[i])
		}
	}
}

func TestSplitSystem(t *testing.T) {
	tests := []struct {
		name       string
		in         []core.AIMessage
		wantSystem string
		wantTurns  []core.AIMessage
	}{
		{
			name:       "no system message",
			in:         []core.AIMessage{{Role: "user", Content: "a"}},
			wantSystem: "",
			wantTurns:  []core.AIMessage{{Role: "user", Content: "a"}},
		},
		{
			name: "three user turns merge into one",
			in: []core.AIMessage{
				{Role: "user", Content: "a"},
				{Role: "user", Content: "b"},
				{Role: "user", Content: "c"},
			},
			wantSystem: "",
			wantTurns:  []core.AIMessage{{Role: "user", Content: "a\n\nb\n\nc"}},
		},
		{
			name: "blank system message dropped",
			in: []core.AIMessage{
				{Role: "system", Content: "  "},
				{Role: "user", Content: "a"},
			},
			wantSystem: "",
			wantTurns:  []core.AIMessage{{Role: "user", Content: "a"}},
		},
		{
			name:       "only a system message leaves no turns",
			in:         []core.AIMessage{{Role: "system", Content: "s"}},
			wantSystem: "s",
			wantTurns:  nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			system, turns := splitSystem(tc.in)
			if system != tc.wantSystem {
				t.Fatalf("system = %q, want %q", system, tc.wantSystem)
			}
			if len(turns) != len(tc.wantTurns) {
				t.Fatalf("turns = %+v, want %+v", turns, tc.wantTurns)
			}
			for i := range tc.wantTurns {
				if !reflect.DeepEqual(turns[i], tc.wantTurns[i]) {
					t.Fatalf("turn %d = %+v, want %+v", i, turns[i], tc.wantTurns[i])
				}
			}
		})
	}
}

func TestAnthropicCompleteRejectsEmptyTranscript(t *testing.T) {
	provider := testProvider(t, core.ProviderAnthropic, "https://example.invalid", nil)
	_, err := provider.Complete(context.Background(), []core.AIMessage{{Role: "system", Content: "s"}}, core.AIOptions{})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("error = %v", err)
	}
}

func TestAnthropicStreamDeltas(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = readAll(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
		io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
		flush(t, w)
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hel\"}}\n\n")
		flush(t, w)
		// One frame split across two writes.
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_")
		flush(t, w)
		io.WriteString(w, "delta\",\"text\":\"lo\"}}\n\n")
		flush(t, w)
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flush(t, w)
		io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"after stop\"}}\n\n")
		flush(t, w)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderAnthropic, server.URL, nil)
	ch, err := provider.Stream(context.Background(), []core.AIMessage{{Role: "user", Content: "hi"}}, core.AIOptions{MaxTokens: 16})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	got := drain(t, ch)
	if got.err != nil {
		t.Fatalf("stream error: %v", got.err)
	}
	if got.text != "Hello" || !got.done || got.count != 3 {
		t.Fatalf("stream = %+v", got)
	}
	var sent anthropicChatRequest
	decodeBody(t, body, &sent)
	if !sent.Stream || sent.MaxTokens != 16 {
		t.Fatalf("request = %+v", sent)
	}
}

func TestAnthropicStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"overloaded\"}}\n\n")
		flush(t, w)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderAnthropic, server.URL, nil)
	ch, err := provider.Stream(context.Background(), []core.AIMessage{{Role: "user", Content: "hi"}}, core.AIOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	got := drain(t, ch)
	if got.err == nil || got.done {
		t.Fatalf("stream = %+v", got)
	}
}

func TestAnthropicEmbedUnsupported(t *testing.T) {
	provider := testProvider(t, core.ProviderAnthropic, "https://example.invalid", nil)
	_, err := provider.Embed(context.Background(), []string{"a"}, "")
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("error = %v", err)
	}
}

func TestAnthropicListModelsFallsBackToConfiguredModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "bad key")
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderAnthropic, server.URL, nil)
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0] != "test-model" {
		t.Fatalf("models = %v", models)
	}
	if err := provider.Health(context.Background()); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("Health error = %v", err)
	}
}

func TestAnthropicListModelsSorted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"id":"claude-b"},{"id":"claude-a"}]}`)
	}))
	defer server.Close()

	provider := testProvider(t, core.ProviderAnthropic, server.URL, nil)
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0] != "claude-a" || models[1] != "claude-b" {
		t.Fatalf("models = %v", models)
	}
}
