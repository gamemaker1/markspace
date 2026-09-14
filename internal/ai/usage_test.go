package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// frameServer answers every request with the frames in order, flushing after
// each one, and stores the last request body in captured when it is non-nil.
func frameServer(t *testing.T, contentType string, frames []string, captured *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAll(t, r)
		if captured != nil {
			*captured = body
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		for _, frame := range frames {
			io.WriteString(w, frame)
			flush(t, w)
		}
	}))
}

// streamAll runs one Stream call and drains it.
func streamAll(t *testing.T, provider core.AIProvider, opts core.AIOptions) drained {
	t.Helper()
	ch, err := provider.Stream(context.Background(), []core.AIMessage{{Role: "user", Content: "hi"}}, opts)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	got := drain(t, ch)
	if got.err != nil {
		t.Fatalf("stream error: %v", got.err)
	}
	if !got.done {
		t.Fatalf("stream ended without Done: %+v", got)
	}
	return got
}

// assertFinal checks the terminal chunk of a completed stream.
func assertFinal(t *testing.T, got drained, model string, prompt, completion int) {
	t.Helper()
	final := got.final
	if !final.Done || final.Err != nil || final.Delta != "" {
		t.Fatalf("final chunk = %+v", final)
	}
	if final.Model != model {
		t.Fatalf("final model = %q, want %q", final.Model, model)
	}
	if final.PromptTokens != prompt || final.CompletionTokens != completion {
		t.Fatalf("final usage = %d/%d, want %d/%d", final.PromptTokens, final.CompletionTokens, prompt, completion)
	}
}

func TestOpenAIStreamUsageOnFinalChunk(t *testing.T) {
	var body []byte
	server := frameServer(t, "text/event-stream", []string{
		`data: {"choices":[{"delta":{"content":"Hi"}}]}` + "\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n",
		// The usage frame is split across two writes and carries no choice.
		`data: {"choices":[],"usage":{"prompt_tokens":12,`,
		`"completion_tokens":5}}` + "\n\n",
		"data: [DONE]\n\n",
	}, &body)
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
	got := streamAll(t, provider, core.AIOptions{})
	if got.text != "Hi" {
		t.Fatalf("text = %q", got.text)
	}
	assertFinal(t, got, "test-model", 12, 5)

	var sent openaiChatRequest
	decodeBody(t, body, &sent)
	if sent.StreamOptions == nil || !sent.StreamOptions.IncludeUsage {
		t.Fatalf("stream_options = %+v", sent.StreamOptions)
	}
}

func TestOpenAIStreamWithoutUsageLeavesZeros(t *testing.T) {
	server := frameServer(t, "text/event-stream", []string{
		`data: {"choices":[{"delta":{"content":"Hi"}}]}` + "\n\n",
		"data: [DONE]\n\n",
	}, nil)
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
	got := streamAll(t, provider, core.AIOptions{Model: "override-model"})
	assertFinal(t, got, "override-model", 0, 0)
}

func TestOpenAIStreamEOFWithoutDoneKeepsUsage(t *testing.T) {
	server := frameServer(t, "text/event-stream", []string{
		`data: {"choices":[{"delta":{"content":"Hi"}}],"usage":{"prompt_tokens":4,"completion_tokens":1}}` + "\n\n",
	}, nil)
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
	assertFinal(t, streamAll(t, provider, core.AIOptions{}), "test-model", 4, 1)
}

func TestOpenAIPlainBodyStreamUsage(t *testing.T) {
	server := frameServer(t, "application/json", []string{
		`{"choices":[{"message":{"content":"one shot"}}],"usage":{"prompt_tokens":8,"completion_tokens":2}}`,
	}, nil)
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
	got := streamAll(t, provider, core.AIOptions{})
	if got.text != "one shot" {
		t.Fatalf("text = %q", got.text)
	}
	assertFinal(t, got, "test-model", 8, 2)
}

// requestLog records request bodies from test handler goroutines.
type requestLog struct {
	mu     sync.Mutex
	bodies [][]byte
}

// add records one request body.
func (l *requestLog) add(body []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bodies = append(l.bodies, body)
}

// all returns the recorded bodies in arrival order.
func (l *requestLog) all() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][]byte(nil), l.bodies...)
}

// hasStreamOptions reports whether a chat request body carries the
// stream_options key at all.
func hasStreamOptions(t *testing.T, body []byte) bool {
	t.Helper()
	var fields map[string]json.RawMessage
	decodeBody(t, body, &fields)
	_, ok := fields["stream_options"]
	return ok
}

// streamOptionsServer rejects every request whose body carries stream_options
// with status and errBody, and streams a short answer to every other request.
func streamOptionsServer(t *testing.T, log *requestLog, status int, errBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readAll(t, r)
		log.add(body)
		if hasStreamOptions(t, body) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			io.WriteString(w, errBody)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"Hi"}}]}`+"\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
		flush(t, w)
	}))
}

func TestOpenAIStreamRetriesWithoutStreamOptions(t *testing.T) {
	log := &requestLog{}
	server := streamOptionsServer(t, log, http.StatusBadRequest,
		`{"error":{"message":"Unrecognized request argument supplied: stream_options","type":"invalid_request_error"}}`)
	defer server.Close()

	provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)

	first := streamAll(t, provider, core.AIOptions{})
	if first.text != "Hi" {
		t.Fatalf("first call text = %q", first.text)
	}
	assertFinal(t, first, "test-model", 0, 0)
	bodies := log.all()
	if len(bodies) != 2 {
		t.Fatalf("first call made %d requests, want 2", len(bodies))
	}
	if !hasStreamOptions(t, bodies[0]) {
		t.Fatalf("first attempt did not send stream_options: %s", bodies[0])
	}
	if hasStreamOptions(t, bodies[1]) {
		t.Fatalf("retry still sent stream_options: %s", bodies[1])
	}

	second := streamAll(t, provider, core.AIOptions{})
	if second.text != "Hi" {
		t.Fatalf("second call text = %q", second.text)
	}
	bodies = log.all()
	if len(bodies) != 3 {
		t.Fatalf("second call made %d requests, want 1", len(bodies)-2)
	}
	if hasStreamOptions(t, bodies[2]) {
		t.Fatalf("second call sent stream_options after a rejection: %s", bodies[2])
	}
}

func TestOpenAIStreamOptionsRetryDecision(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantRequests int
		wantErr      bool
	}{
		{"422 naming the field in upper case", http.StatusUnprocessableEntity, `{"detail":"Extra inputs are not permitted: STREAM_OPTIONS"}`, 2, false},
		{"400 unrelated to the field", http.StatusBadRequest, `{"error":{"message":"messages: at least one message is required"}}`, 1, true},
		{"401 naming the field", http.StatusUnauthorized, `{"error":{"message":"stream_options not allowed for this key"}}`, 1, true},
		{"500 naming the field", http.StatusInternalServerError, `stream_options handler crashed`, 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			log := &requestLog{}
			server := streamOptionsServer(t, log, tc.status, tc.body)
			defer server.Close()

			provider := testProvider(t, core.ProviderOpenAI, server.URL, nil)
			msgs := []core.AIMessage{{Role: "user", Content: "hi"}}
			ch, err := provider.Stream(context.Background(), msgs, core.AIOptions{})
			if got := len(log.all()); got != tc.wantRequests {
				t.Fatalf("requests = %d, want %d", got, tc.wantRequests)
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Stream: %v", err)
				}
				if got := drain(t, ch); !got.done || got.text != "Hi" {
					t.Fatalf("stream = %+v", got)
				}
				return
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Status != tc.status {
				t.Fatalf("error = %v, want an APIError with status %d", err, tc.status)
			}
			// A refusal that was not about the field must not disable it.
			if _, err := provider.Stream(context.Background(), msgs, core.AIOptions{}); err == nil {
				t.Fatal("second call unexpectedly succeeded")
			}
			bodies := log.all()
			if len(bodies) != tc.wantRequests+1 || !hasStreamOptions(t, bodies[len(bodies)-1]) {
				t.Fatalf("second call did not send stream_options again: %d requests", len(bodies))
			}
		})
	}
}

func TestOllamaStreamUsageOnFinalChunk(t *testing.T) {
	server := frameServer(t, "application/x-ndjson", []string{
		`{"model":"llama3.2","message":{"content":"Hi"},"done":false}` + "\n",
		`{"model":"llama3.2","message":{"content":""},"done":true,"done_reason":"stop",`,
		`"prompt_eval_count":26,"eval_count":9}` + "\n",
	}, nil)
	defer server.Close()

	provider := testProvider(t, core.ProviderOllama, server.URL, nil)
	got := streamAll(t, provider, core.AIOptions{})
	if got.text != "Hi" {
		t.Fatalf("text = %q", got.text)
	}
	assertFinal(t, got, "test-model", 26, 9)
}

func TestOllamaStreamWithoutUsageLeavesZeros(t *testing.T) {
	server := frameServer(t, "application/x-ndjson", []string{
		`{"message":{"content":"Hi"},"done":false}` + "\n",
		`{"message":{"content":""},"done":true}` + "\n",
	}, nil)
	defer server.Close()

	provider := testProvider(t, core.ProviderOllama, server.URL, nil)
	assertFinal(t, streamAll(t, provider, core.AIOptions{Model: "qwen3:8b"}), "qwen3:8b", 0, 0)
}

func TestAnthropicStreamUsageOnFinalChunk(t *testing.T) {
	server := frameServer(t, "text/event-stream", []string{
		"event: message_start\n" +
			`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-x","usage":{"input_tokens":25,"output_tokens":1}}}` + "\n\n",
		"event: content_block_start\n" +
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n",
		"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}` + "\n\n",
		"event: content_block_stop\n" +
			`data: {"type":"content_block_stop","index":0}` + "\n\n",
		"event: message_delta\n" +
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}` + "\n\n",
		"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n",
	}, nil)
	defer server.Close()

	provider := testProvider(t, core.ProviderAnthropic, server.URL, nil)
	got := streamAll(t, provider, core.AIOptions{})
	if got.text != "Hi" {
		t.Fatalf("text = %q", got.text)
	}
	assertFinal(t, got, "test-model", 25, 15)
}

func TestAnthropicStreamWithoutUsageLeavesZeros(t *testing.T) {
	server := frameServer(t, "text/event-stream", []string{
		"event: message_start\n" + `data: {"type":"message_start","message":{"id":"msg_1"}}` + "\n\n",
		"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}` + "\n\n",
		"event: message_delta\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}` + "\n\n",
		"event: message_stop\n" + `data: {"type":"message_stop"}` + "\n\n",
	}, nil)
	defer server.Close()

	provider := testProvider(t, core.ProviderAnthropic, server.URL, nil)
	assertFinal(t, streamAll(t, provider, core.AIOptions{Model: "claude-override"}), "claude-override", 0, 0)
}
