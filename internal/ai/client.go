// Package ai implements ports.AIFactory over four model server protocols:
// the OpenAI API, the native Ollama API, the Anthropic messages API and the
// ComfyUI workflow API. It uses only the standard library.
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// maxErrorBody caps how much of a failed response body is read back.
	maxErrorBody = 8 << 10
	// defaultTimeoutSeconds applies when a config sets no timeout.
	defaultTimeoutSeconds = 60
	// maxStreamLine caps one line of a streamed response body.
	maxStreamLine = 1 << 20
	// redacted replaces an API key that shows up in a response body.
	redacted = "[redacted]"
)

// APIError reports a non-2xx response from a model server. Unwrap returns the
// matching core sentinel error, or nil when the status maps to none.
type APIError struct {
	// Provider is the protocol label, such as "openai".
	Provider string
	// Status is the HTTP status code.
	Status int
	// Method is the HTTP method of the failed request.
	Method string
	// URL is the request URL, which never carries the API key.
	URL string
	// Body is the trimmed response body, at most 8 KiB.
	Body string

	sentinel error
}

// Error renders the provider, status, request and trimmed response body.
func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s: %d from %s %s", e.Provider, e.Status, e.Method, e.URL)
	}
	return fmt.Sprintf("%s: %d from %s %s: %s", e.Provider, e.Status, e.Method, e.URL, e.Body)
}

// Unwrap returns the core sentinel error for the status code.
func (e *APIError) Unwrap() error { return e.sentinel }

// sentinelForStatus maps an HTTP status to a core sentinel error, or nil.
func sentinelForStatus(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return core.ErrUnauthorized
	case http.StatusNotFound:
		return core.ErrNotFound
	case http.StatusRequestEntityTooLarge:
		return core.ErrTooLarge
	case http.StatusTooManyRequests:
		return core.ErrRateLimited
	default:
		return nil
	}
}

// newHTTPClient builds the client shared by every provider. It carries no
// client timeout because each call derives its deadline from the context.
func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 4
	transport.IdleConnTimeout = 90 * time.Second
	return &http.Client{Timeout: 0, Transport: transport}
}

// endpoint joins a base URL and a path without duplicating a trailing "/v1"
// already present on the base URL.
func endpoint(base, path string) string {
	base = strings.TrimRight(base, "/")
	if path == "" {
		return base
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.HasSuffix(base, "/v1") && (path == "/v1" || strings.HasPrefix(path, "/v1/")) {
		path = strings.TrimPrefix(path, "/v1")
	}
	return base + path
}

// baseProvider holds the state every provider shares.
type baseProvider struct {
	cfg    *core.AIProviderConfig
	client *http.Client
	logger *slog.Logger
	label  string
	// auth writes the protocol specific authentication headers.
	auth func(h http.Header)
}

// ID returns the configuration id of the provider.
func (b *baseProvider) ID() string { return b.cfg.ID }

// Config returns the configuration the provider was built from.
func (b *baseProvider) Config() *core.AIProviderConfig { return b.cfg }

// timeout returns the per-call timeout from the configuration.
func (b *baseProvider) timeout() time.Duration {
	seconds := b.cfg.TimeoutSeconds
	if seconds <= 0 {
		seconds = defaultTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

// withTimeout wraps ctx with the configured per-call timeout.
func (b *baseProvider) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, b.timeout())
}

// model resolves the chat model for one call.
func (b *baseProvider) model(opts core.AIOptions) string {
	if opts.Model != "" {
		return opts.Model
	}
	return b.cfg.Model
}

// embedModel resolves the embedding model for one call.
func (b *baseProvider) embedModel(model string) string {
	if model != "" {
		return model
	}
	if b.cfg.EmbedModel != "" {
		return b.cfg.EmbedModel
	}
	return b.cfg.Model
}

// temperature resolves the sampling temperature for one call.
func (b *baseProvider) temperature(opts core.AIOptions) float64 {
	if opts.Temperature != nil {
		return *opts.Temperature
	}
	return b.cfg.Temperature
}

// maxTokens resolves the output token limit for one call, or 0 when unset.
func (b *baseProvider) maxTokens(opts core.AIOptions) int {
	if opts.MaxTokens > 0 {
		return opts.MaxTokens
	}
	if b.cfg.MaxTokens > 0 {
		return b.cfg.MaxTokens
	}
	return 0
}

// redact removes the API key from text that came back from a server.
func (b *baseProvider) redact(s string) string {
	if b.cfg.APIKey == "" {
		return s
	}
	return strings.ReplaceAll(s, b.cfg.APIKey, redacted)
}

// do sends one request and returns the response on a 2xx status. On any other
// status it closes the body and returns an *APIError. accept sets the Accept
// header, which selects between a JSON body and an event stream.
func (b *baseProvider) do(ctx context.Context, method, url string, payload any, accept string) (*http.Response, error) {
	if payload == nil {
		return b.doBody(ctx, method, url, nil, "", accept)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%s: encode request for %s: %w", b.label, url, err)
	}
	return b.doBody(ctx, method, url, bytes.NewReader(encoded), "application/json", accept)
}

// doBody sends one request with a raw body of the given content type. It
// handles authentication, custom headers and errors like do.
func (b *baseProvider) doBody(ctx context.Context, method, url string, body io.Reader, contentType, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("%s: build request for %s %s: %w", b.label, method, url, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if b.auth != nil {
		b.auth(req.Header)
	}
	for name, value := range b.cfg.Headers {
		req.Header.Set(name, value)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %s %s: %w", b.label, method, url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return nil, b.apiError(method, url, resp)
	}
	return resp, nil
}

// apiError reads at most 8 KiB of a failed response and builds an *APIError.
func (b *baseProvider) apiError(method, url string, resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return &APIError{
		Provider: b.label,
		Status:   resp.StatusCode,
		Method:   method,
		URL:      url,
		Body:     b.redact(collapseSpace(string(raw))),
		sentinel: sentinelForStatus(resp.StatusCode),
	}
}

// getJSON sends a GET request and decodes the JSON response into out.
func (b *baseProvider) getJSON(ctx context.Context, url string, out any) error {
	resp, err := b.do(ctx, http.MethodGet, url, nil, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s: decode %s: %w", b.label, url, err)
	}
	return nil
}

// postJSON sends a POST request with a JSON body and decodes the response.
func (b *baseProvider) postJSON(ctx context.Context, url string, payload, out any) error {
	resp, err := b.do(ctx, http.MethodPost, url, payload, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s: decode %s: %w", b.label, url, err)
	}
	return nil
}

// collapseSpace trims the text and squashes runs of whitespace into one space
// so an error message stays on a single line.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// newLineScanner returns a scanner over r with a line buffer large enough for
// a streamed frame.
func newLineScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxStreamLine)
	return scanner
}

// sseData returns the payload of a "data:" server-sent event line.
func sseData(line string) (string, bool) {
	line = strings.TrimRight(line, "\r")
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
}

// isEventStream reports whether a response body is a server-sent event stream.
func isEventStream(resp *http.Response) bool {
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "event-stream")
}

// sendChunk delivers one chunk unless ctx is done first. It reports whether
// the chunk was delivered.
func sendChunk(ctx context.Context, ch chan<- core.AIChunk, chunk core.AIChunk) bool {
	select {
	case ch <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

// finishStream sends the single terminal chunk of a completed stream. It
// carries the model that answered and the usage the server reported, which is
// zero when the server reported none.
func finishStream(ctx context.Context, ch chan<- core.AIChunk, model string, usage streamUsage) {
	sendChunk(ctx, ch, core.AIChunk{
		Done:             true,
		Model:            model,
		PromptTokens:     usage.prompt,
		CompletionTokens: usage.completion,
	})
}

// failStream sends the single terminal chunk of a failed stream.
func failStream(ctx context.Context, ch chan<- core.AIChunk, err error) {
	sendChunk(ctx, ch, core.AIChunk{Err: err})
}

// streamUsage accumulates the token counts a stream reports, which arrive in
// the last frames and are zero when the server reports none.
type streamUsage struct {
	prompt     int
	completion int
}

// observe keeps the highest counts seen, because a server may repeat or
// update them across frames.
func (u *streamUsage) observe(prompt, completion int) {
	if prompt > u.prompt {
		u.prompt = prompt
	}
	if completion > u.completion {
		u.completion = completion
	}
}

// imageData returns the media type and base64 data of an image. An image
// without a media type is sniffed from its bytes.
func imageData(img core.AIImage) (string, string) {
	mediaType := strings.TrimSpace(img.MediaType)
	if mediaType == "" {
		mediaType = http.DetectContentType(img.Data)
	}
	return mediaType, base64.StdEncoding.EncodeToString(img.Data)
}

// toFloat32 converts a JSON embedding vector to float32.
func toFloat32(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}
