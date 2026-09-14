package ai

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// testLogger returns a logger that writes nowhere.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// testRegistry returns a registry with a quiet logger.
func testRegistry() *Registry {
	return NewRegistry(testLogger(), 4)
}

// testProvider builds a provider against a test server URL.
func testProvider(t *testing.T, kind core.AIProviderKind, baseURL string, mutate func(*core.AIProviderConfig)) core.AIProvider {
	t.Helper()
	cfg := &core.AIProviderConfig{
		ID:             "p1",
		Name:           "test",
		Kind:           kind,
		BaseURL:        baseURL,
		Model:          "test-model",
		TimeoutSeconds: 5,
	}
	if mutate != nil {
		mutate(cfg)
	}
	provider, err := testRegistry().New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return provider
}

// drained is the outcome of reading a stream to its end.
type drained struct {
	deltas []string
	text   string
	done   bool
	err    error
	count  int
	// final is the terminal Done or Err chunk, zero when none arrived.
	final core.AIChunk
}

// drain reads every chunk until the channel closes.
func drain(t *testing.T, ch <-chan core.AIChunk) drained {
	t.Helper()
	var out drained
	deadline := time.After(5 * time.Second)
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				return out
			}
			out.count++
			switch {
			case chunk.Err != nil:
				out.err = chunk.Err
				out.final = chunk
			case chunk.Done:
				out.done = true
				out.final = chunk
			default:
				out.deltas = append(out.deltas, chunk.Delta)
				out.text += chunk.Delta
			}
		case <-deadline:
			t.Fatal("stream did not finish within 5s")
		}
	}
}

// decodeBody decodes a captured request body into out.
func decodeBody(t *testing.T, body []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode request body %q: %v", string(body), err)
	}
}

// readAll reads a request body in a test handler.
func readAll(t *testing.T, r *http.Request) []byte {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return raw
}

// flush pushes buffered bytes of a streaming test handler to the client.
func flush(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("test server response writer cannot flush")
	}
	flusher.Flush()
}

func TestEndpointDoesNotDoubleV1(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		{"bare host", "http://h:1/", "/v1/chat/completions", "http://h:1/v1/chat/completions"},
		{"base with v1", "http://h:1/v1", "/v1/chat/completions", "http://h:1/v1/chat/completions"},
		{"base with trailing slash v1", "http://h:1/v1/", "/v1/embeddings", "http://h:1/v1/embeddings"},
		{"nested base with v1", "http://h:1/api/v1", "/v1/models", "http://h:1/api/v1/models"},
		{"models on bare host", "http://h:1", "/v1/models", "http://h:1/v1/models"},
		{"v1 path only", "http://h:1/v1", "/v1", "http://h:1/v1"},
		{"ollama path untouched", "http://h:1", "/api/chat", "http://h:1/api/chat"},
		{"ollama path under v1 base", "http://h:1/v1", "/api/chat", "http://h:1/v1/api/chat"},
		{"path without leading slash", "http://h:1", "v1/models", "http://h:1/v1/models"},
		{"empty path", "http://h:1/", "", "http://h:1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := endpoint(tc.base, tc.path); got != tc.want {
				t.Fatalf("endpoint(%q, %q) = %q, want %q", tc.base, tc.path, got, tc.want)
			}
		})
	}
}

func TestCollapseSpace(t *testing.T) {
	got := collapseSpace("  line one\n\n  line two\t")
	if got != "line one line two" {
		t.Fatalf("collapseSpace = %q", got)
	}
}

func TestSendChunkStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := make(chan core.AIChunk)
	if sendChunk(ctx, ch, core.AIChunk{Delta: "x"}) {
		t.Fatal("sendChunk delivered a chunk on a canceled context")
	}
}
