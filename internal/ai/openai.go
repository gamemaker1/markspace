package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// openaiProvider speaks the OpenAI chat completions protocol. It works
// against api.openai.com, OpenRouter, vLLM, llama.cpp server, LocalAI,
// LM Studio and the Ollama compatibility endpoint.
type openaiProvider struct {
	baseProvider

	// noStreamOptions is set once the server has rejected stream_options,
	// so later streamed calls leave the field out without a failed request.
	noStreamOptions atomic.Bool
	// noResponseFormat is set once the server has rejected response_format
	// on an image call, so later image calls leave the field out.
	noResponseFormat atomic.Bool
}

// newOpenAIProvider builds a provider for the OpenAI protocol.
func newOpenAIProvider(cfg *core.AIProviderConfig, client *http.Client, logger *slog.Logger) *openaiProvider {
	p := &openaiProvider{baseProvider: baseProvider{cfg: cfg, client: client, logger: logger, label: "openai"}}
	p.auth = p.setAuth
	return p
}

// setAuth adds the bearer token when the configuration carries a key.
func (p *openaiProvider) setAuth(h http.Header) {
	if p.cfg.APIKey != "" {
		h.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
}

// Kind returns core.ProviderOpenAI.
func (p *openaiProvider) Kind() core.AIProviderKind { return core.ProviderOpenAI }

// openaiChatRequest is the body of a chat completions call.
type openaiChatRequest struct {
	Model         string              `json:"model,omitempty"`
	Messages      []openaiMessage     `json:"messages"`
	Temperature   float64             `json:"temperature"`
	MaxTokens     int                 `json:"max_tokens,omitempty"`
	Stop          []string            `json:"stop,omitempty"`
	Stream        bool                `json:"stream"`
	StreamOptions *openaiStreamOption `json:"stream_options,omitempty"`
}

// openaiMessage is one chat turn on the wire. Content is a string for a turn
// without images, which encodes exactly as core.AIMessage without images, or
// a list of content parts for a turn with images.
type openaiMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// openaiContentPart is one text or image part of a turn with images.
type openaiContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openaiImageURL `json:"image_url,omitempty"`
}

// openaiImageURL carries an image as a data URL.
type openaiImageURL struct {
	URL string `json:"url"`
}

// openaiMessages converts a transcript to the wire form. A turn with images
// becomes a text part followed by one image_url part per image.
func openaiMessages(msgs []core.AIMessage) []openaiMessage {
	out := make([]openaiMessage, len(msgs))
	for i, msg := range msgs {
		if len(msg.Images) == 0 {
			out[i] = openaiMessage{Role: msg.Role, Content: msg.Content}
			continue
		}
		parts := make([]openaiContentPart, 0, len(msg.Images)+1)
		if msg.Content != "" {
			parts = append(parts, openaiContentPart{Type: "text", Text: msg.Content})
		}
		for _, img := range msg.Images {
			mediaType, data := imageData(img)
			parts = append(parts, openaiContentPart{
				Type:     "image_url",
				ImageURL: &openaiImageURL{URL: "data:" + mediaType + ";base64," + data},
			})
		}
		out[i] = openaiMessage{Role: msg.Role, Content: parts}
	}
	return out
}

// openaiStreamOption asks a server to append a usage frame to a stream.
type openaiStreamOption struct {
	IncludeUsage bool `json:"include_usage"`
}

// openaiChoice covers both a non-streamed message and a streamed delta.
type openaiChoice struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Delta struct {
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

// openaiUsage is the token accounting of one call.
type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// openaiErrorBody is the error object some servers put in a 200 response.
type openaiErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// openaiChatResponse is one chat completions response or stream frame.
type openaiChatResponse struct {
	Choices []openaiChoice   `json:"choices"`
	Usage   *openaiUsage     `json:"usage"`
	Error   *openaiErrorBody `json:"error"`
}

// chatRequest builds the request body for a chat call. A streamed call asks
// for a usage frame through stream_options unless this server has already
// rejected the field.
func (p *openaiProvider) chatRequest(msgs []core.AIMessage, opts core.AIOptions, stream bool) *openaiChatRequest {
	body := &openaiChatRequest{
		Model:       p.model(opts),
		Messages:    openaiMessages(msgs),
		Temperature: p.temperature(opts),
		MaxTokens:   p.maxTokens(opts),
		Stop:        opts.Stop,
		Stream:      stream,
	}
	if stream && !p.noStreamOptions.Load() {
		body.StreamOptions = &openaiStreamOption{IncludeUsage: true}
	}
	return body
}

// Complete sends one non-streamed chat completion.
func (p *openaiProvider) Complete(ctx context.Context, msgs []core.AIMessage, opts core.AIOptions) (*core.AIResult, error) {
	start := time.Now()
	callCtx, cancel := p.withTimeout(ctx)
	defer cancel()

	target := endpoint(p.cfg.BaseURL, "/v1/chat/completions")
	var out openaiChatResponse
	if err := p.postJSON(callCtx, target, p.chatRequest(msgs, opts, false), &out); err != nil {
		return nil, err
	}
	if err := p.bodyError(&out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("openai: %s returned no choices", target)
	}
	result := &core.AIResult{
		Text:     out.Choices[0].Message.Content,
		Provider: p.cfg.ID,
		Model:    p.model(opts),
		Elapsed:  time.Since(start),
	}
	if out.Usage != nil {
		result.PromptTokens = out.Usage.PromptTokens
		result.CompletionTokens = out.Usage.CompletionTokens
	}
	return result, nil
}

// rejectsStreamOptions reports whether err is a 400 or 422 response whose body
// names stream_options, which is how a server that does not accept the field
// refuses it. Any other failure is returned to the caller without a retry.
func rejectsStreamOptions(err error) bool {
	return rejectsField(err, "stream_options")
}

// rejectsField reports whether err is a 400 or 422 response whose body names
// the request field.
func rejectsField(err error, field string) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.Status != http.StatusBadRequest && apiErr.Status != http.StatusUnprocessableEntity {
		return false
	}
	return strings.Contains(strings.ToLower(apiErr.Body), field)
}

// bodyError turns an error object inside a 200 response into an error.
func (p *openaiProvider) bodyError(out *openaiChatResponse) error {
	if out.Error == nil || out.Error.Message == "" {
		return nil
	}
	return fmt.Errorf("openai: %s", p.redact(collapseSpace(out.Error.Message)))
}

// Stream sends one streamed chat completion. The returned channel carries the
// deltas, then exactly one chunk with Done or Err, and is then closed. The
// Done chunk carries the model and the usage of the final frame, which stays
// at zero on a server that reports none. A stream canceled mid flight can
// close without a final chunk, so a consumer treats a closed channel as
// terminal. A server that ignores the stream flag and answers with one JSON
// body is reported as a single delta. A server that rejects stream_options
// with a 400 or 422 gets the same request once more without the field, and
// the provider leaves the field out from then on.
func (p *openaiProvider) Stream(ctx context.Context, msgs []core.AIMessage, opts core.AIOptions) (<-chan core.AIChunk, error) {
	callCtx, cancel := p.withTimeout(ctx)
	target := endpoint(p.cfg.BaseURL, "/v1/chat/completions")
	model := p.model(opts)
	payload := p.chatRequest(msgs, opts, true)
	resp, err := p.do(callCtx, http.MethodPost, target, payload, "text/event-stream")
	if err != nil && payload.StreamOptions != nil && rejectsStreamOptions(err) {
		p.noStreamOptions.Store(true)
		p.logger.Warn("openai server rejected stream_options, retrying without it", "provider", p.cfg.ID, "error", err)
		payload.StreamOptions = nil
		resp, err = p.do(callCtx, http.MethodPost, target, payload, "text/event-stream")
	}
	if err != nil {
		cancel()
		return nil, err
	}

	ch := make(chan core.AIChunk, 1)
	go func() {
		defer cancel()
		defer close(ch)
		defer resp.Body.Close()

		var usage streamUsage
		if !isEventStream(resp) {
			p.logger.Warn("openai stream answered with a plain body", "url", target, "contentType", resp.Header.Get("Content-Type"))
			var out openaiChatResponse
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				failStream(callCtx, ch, fmt.Errorf("openai: decode %s: %w", target, err))
				return
			}
			if err := p.bodyError(&out); err != nil {
				failStream(callCtx, ch, err)
				return
			}
			if out.Usage != nil {
				usage.observe(out.Usage.PromptTokens, out.Usage.CompletionTokens)
			}
			if len(out.Choices) > 0 {
				text := out.Choices[0].Message.Content
				if text == "" {
					text = out.Choices[0].Delta.Content
				}
				if text != "" && !sendChunk(callCtx, ch, core.AIChunk{Delta: text}) {
					return
				}
			}
			finishStream(callCtx, ch, model, usage)
			return
		}

		scanner := newLineScanner(resp.Body)
		for scanner.Scan() {
			data, ok := sseData(scanner.Text())
			if !ok || data == "" {
				continue
			}
			if data == "[DONE]" {
				finishStream(callCtx, ch, model, usage)
				return
			}
			var frame openaiChatResponse
			if err := json.Unmarshal([]byte(data), &frame); err != nil {
				p.logger.Warn("openai stream frame ignored", "url", target, "error", err)
				continue
			}
			if err := p.bodyError(&frame); err != nil {
				failStream(callCtx, ch, err)
				return
			}
			// The usage frame arrives last and usually carries no choice.
			if frame.Usage != nil {
				usage.observe(frame.Usage.PromptTokens, frame.Usage.CompletionTokens)
			}
			if len(frame.Choices) == 0 {
				continue
			}
			if delta := frame.Choices[0].Delta.Content; delta != "" {
				if !sendChunk(callCtx, ch, core.AIChunk{Delta: delta}) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			failStream(callCtx, ch, fmt.Errorf("openai: read stream %s: %w", target, err))
			return
		}
		finishStream(callCtx, ch, model, usage)
	}()
	return ch, nil
}

// openaiEmbedRequest is the body of an embeddings call.
type openaiEmbedRequest struct {
	Model string   `json:"model,omitempty"`
	Input []string `json:"input"`
}

// openaiEmbedResponse is the result of an embeddings call.
type openaiEmbedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Error *openaiErrorBody `json:"error"`
}

// Embed returns one vector per text, ordered to match texts.
func (p *openaiProvider) Embed(ctx context.Context, texts []string, model string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	callCtx, cancel := p.withTimeout(ctx)
	defer cancel()

	target := endpoint(p.cfg.BaseURL, "/v1/embeddings")
	payload := &openaiEmbedRequest{Model: p.embedModel(model), Input: texts}
	var out openaiEmbedResponse
	if err := p.postJSON(callCtx, target, payload, &out); err != nil {
		return nil, err
	}
	if out.Error != nil && out.Error.Message != "" {
		return nil, fmt.Errorf("openai: %s", p.redact(collapseSpace(out.Error.Message)))
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("openai: %s returned no embeddings", target)
	}
	vectors := make([][]float32, len(out.Data))
	for i, item := range out.Data {
		pos := item.Index
		if pos < 0 || pos >= len(vectors) {
			pos = i
		}
		vectors[pos] = toFloat32(item.Embedding)
	}
	if len(vectors) != len(texts) {
		p.logger.Warn("openai returned an unexpected embedding count", "want", len(texts), "got", len(vectors))
	}
	return vectors, nil
}

// openaiModelList is the result of a model listing.
type openaiModelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ListModels returns the sorted model ids the server reports.
func (p *openaiProvider) ListModels(ctx context.Context) ([]string, error) {
	callCtx, cancel := p.withTimeout(ctx)
	defer cancel()

	target := endpoint(p.cfg.BaseURL, "/v1/models")
	var out openaiModelList
	if err := p.getJSON(callCtx, target, &out); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(out.Data))
	for _, item := range out.Data {
		if item.ID != "" {
			models = append(models, item.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}

// Health lists the models. A server without a model listing is probed with a
// one token completion instead.
func (p *openaiProvider) Health(ctx context.Context) error {
	_, err := p.ListModels(ctx)
	if err == nil {
		return nil
	}
	if !errors.Is(err, core.ErrNotFound) {
		return err
	}
	p.logger.Warn("openai model listing missing, probing with a completion", "provider", p.cfg.ID)
	msgs := []core.AIMessage{{Role: "user", Content: "ping"}}
	if _, cerr := p.Complete(ctx, msgs, core.AIOptions{MaxTokens: 1}); cerr != nil {
		return cerr
	}
	return nil
}
