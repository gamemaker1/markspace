package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// countingTransport fails the test if it is ever reached.
type countingTransport struct{ calls atomic.Int32 }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return nil, errors.New("dialled")
}

func TestPublicFetcherAllowlist(t *testing.T) {
	f := NewPublicFetcher()
	if f.client.Timeout.Seconds() != 20 || f.limit != 32<<20 {
		t.Fatalf("timeout %v limit %d", f.client.Timeout, f.limit)
	}
	for _, ok := range []string{OpenRouterCatalogURL, LiteLLMCatalogURL} {
		u, _ := url.Parse(ok)
		if err := f.allowed(u); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	counter := &countingTransport{}
	f = newPublicFetcher(counter, publicCatalogHosts, 1024)
	for _, bad := range []string{
		"http://openrouter.ai/api/v1/models",
		"https://example.com/models.json",
		"https://openrouter.ai.evil.example/api",
		"https://127.0.0.1/",
		"ftp://raw.githubusercontent.com/x",
		"://bad",
	} {
		if _, err := f.Fetch(context.Background(), bad); err == nil {
			t.Errorf("Fetch(%q) succeeded", bad)
		}
	}
	if counter.calls.Load() != 0 {
		t.Fatalf("a refused URL reached the transport %d times", counter.calls.Load())
	}
}

func TestPublicFetcherStatusCapAndRedirects(t *testing.T) {
	var redirectTarget string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte(`{"data":[]}`))
		case "/big":
			w.Write([]byte(strings.Repeat("x", 2048)))
		case "/exact":
			w.Write([]byte(strings.Repeat("x", 1024)))
		case "/fail":
			http.Error(w, "down", http.StatusBadGateway)
		case "/away":
			http.Redirect(w, r, redirectTarget, http.StatusFound)
		case "/home":
			http.Redirect(w, r, "/ok", http.StatusFound)
		}
	}))
	defer srv.Close()
	host := strings.Split(strings.TrimPrefix(srv.URL, "https://"), ":")[0]
	f := newPublicFetcher(srv.Client().Transport, []string{host}, 1024)
	ctx := context.Background()

	if body, err := f.Fetch(ctx, srv.URL+"/ok"); err != nil || string(body) != `{"data":[]}` {
		t.Fatalf("ok = %s, %v", body, err)
	}
	if body, err := f.Fetch(ctx, srv.URL+"/exact"); err != nil || len(body) != 1024 {
		t.Fatalf("exact cap = %d, %v", len(body), err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/big"); !errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("over cap = %v", err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/fail"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("502 = %v", err)
	}
	if body, err := f.Fetch(ctx, srv.URL+"/home"); err != nil || string(body) != `{"data":[]}` {
		t.Fatalf("same host redirect = %s, %v", body, err)
	}
	redirectTarget = "https://example.com/x"
	if _, err := f.Fetch(ctx, srv.URL+"/away"); !errors.Is(err, errHostNotAllowed) {
		t.Fatalf("redirect to another host = %v", err)
	}
	redirectTarget = strings.Replace(srv.URL, "https://", "http://", 1) + "/ok"
	if _, err := f.Fetch(ctx, srv.URL+"/away"); !errors.Is(err, errHostNotAllowed) {
		t.Fatalf("redirect to http = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.Fetch(canceled, srv.URL+"/ok"); err == nil {
		t.Fatal("a canceled fetch succeeded")
	}
}
