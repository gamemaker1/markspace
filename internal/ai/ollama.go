package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// ollamaProvider speaks the native Ollama API on /api/chat, /api/embed and
// /api/tags.
type ollamaProvider struct {
	baseProvider
}

// newOllamaProvider builds a provider for the Ollama protocol.
func newOllamaProvider(cfg *core.AIProviderConfig, client *http.Client, logger *slog.Logger) *ollamaProvider {
	p := &ollamaProvider{baseProvider{cfg: cfg, client: client, logger: logger, label: "ollama"}}
	p.auth = p.setAuth
	return p
}

// setAuth adds the bearer token when the configuration carries a key, which
// covers an Ollama instance behind an authenticating proxy.
func (p *ollamaProvider) setAuth(h http.Header) {
	if p.cfg.APIKey != "" {
		h.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
}

// Kind returns core.ProviderOllama.
func (p *ollamaProvider) Kind() core.AIProviderKind { return core.ProviderOllama }

// ollamaOptions carries the sampling settings of one call.
type ollamaOptions struct {
	Temperature float64  `json:"temperature"`
	NumPredict  int      `json:"num_predict,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

// ollamaChatRequest is the body of an /api/chat call.
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  ollamaOptions   `json:"options"`
}

// ollamaChatResponse is one /api/chat response or stream frame.
type ollamaChatResponse struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done            bool   `json:"done"`
	Error           string `json:"error"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

// ollamaMessage is one chat turn on the wire. Without images it encodes
// exactly as core.AIMessage without images.
type ollamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

// ollamaMessages converts a transcript to the wire form, with each image as a
// base64 string in the images array.
func ollamaMessages(msgs []core.AIMessage) []ollamaMessage {
	out := make([]ollamaMessage, len(msgs))
	for i, msg := range msgs {
		out[i] = ollamaMessage{Role: msg.Role, Content: msg.Content}
		for _, img := range msg.Images {
			_, data := imageData(img)
			out[i].Images = append(out[i].Images, data)
		}
	}
	return out
}

// chatRequest builds the request body for a chat call.
func (p *ollamaProvider) chatRequest(msgs []core.AIMessage, opts core.AIOptions, stream bool) *ollamaChatRequest {
	return &ollamaChatRequest{
		Model:    p.model(opts),
		Messages: ollamaMessages(msgs),
		Stream:   stream,
		Options: ollamaOptions{
			Temperature: p.temperature(opts),
			NumPredict:  p.maxTokens(opts),
			Stop:        opts.Stop,
		},
	}
}

// Complete sends one non-streamed chat call.
func (p *ollamaProvider) Complete(ctx context.Context, msgs []core.AIMessage, opts core.AIOptions) (*core.AIResult, error) {
	start := time.Now()
	callCtx, cancel := p.withTimeout(ctx)
	defer cancel()

	target := endpoint(p.cfg.BaseURL, "/api/chat")
	var out ollamaChatResponse
	if err := p.postJSON(callCtx, target, p.chatRequest(msgs, opts, false), &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama: %s", p.redact(collapseSpace(out.Error)))
	}
	return &core.AIResult{
		Text:             out.Message.Content,
		Provider:         p.cfg.ID,
		Model:            p.model(opts),
		PromptTokens:     out.PromptEvalCount,
		CompletionTokens: out.EvalCount,
		Elapsed:          time.Since(start),
	}, nil
}

// Stream sends one streamed chat call. Ollama answers with newline delimited
// JSON objects and a final object carrying the token counts, which the Done
// chunk reports together with the model. A stream canceled mid flight can
// close without a final chunk, so a consumer treats a closed channel as
// terminal.
func (p *ollamaProvider) Stream(ctx context.Context, msgs []core.AIMessage, opts core.AIOptions) (<-chan core.AIChunk, error) {
	callCtx, cancel := p.withTimeout(ctx)
	target := endpoint(p.cfg.BaseURL, "/api/chat")
	model := p.model(opts)
	resp, err := p.do(callCtx, http.MethodPost, target, p.chatRequest(msgs, opts, true), "application/x-ndjson")
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
		scanner := newLineScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var frame ollamaChatResponse
			if err := json.Unmarshal(line, &frame); err != nil {
				p.logger.Warn("ollama stream frame ignored", "url", target, "error", err)
				continue
			}
			if frame.Error != "" {
				failStream(callCtx, ch, fmt.Errorf("ollama: %s", p.redact(collapseSpace(frame.Error))))
				return
			}
			usage.observe(frame.PromptEvalCount, frame.EvalCount)
			if frame.Message.Content != "" {
				if !sendChunk(callCtx, ch, core.AIChunk{Delta: frame.Message.Content}) {
					return
				}
			}
			if frame.Done {
				finishStream(callCtx, ch, model, usage)
				return
			}
		}
		if err := scanner.Err(); err != nil {
			failStream(callCtx, ch, fmt.Errorf("ollama: read stream %s: %w", target, err))
			return
		}
		finishStream(callCtx, ch, model, usage)
	}()
	return ch, nil
}

// ollamaEmbedRequest is the body of an /api/embed call.
type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// ollamaEmbedResponse is the result of an /api/embed call.
type ollamaEmbedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
	Error      string      `json:"error"`
}

// ollamaLegacyEmbedRequest is the body of the older /api/embeddings call.
type ollamaLegacyEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// ollamaLegacyEmbedResponse is the result of the older /api/embeddings call.
type ollamaLegacyEmbedResponse struct {
	Embedding []float64 `json:"embedding"`
	Error     string    `json:"error"`
}

// Embed returns one vector per text. A server without /api/embed falls back
// to one /api/embeddings call per text.
func (p *ollamaProvider) Embed(ctx context.Context, texts []string, model string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	callCtx, cancel := p.withTimeout(ctx)
	defer cancel()

	target := endpoint(p.cfg.BaseURL, "/api/embed")
	payload := &ollamaEmbedRequest{Model: p.embedModel(model), Input: texts}
	var out ollamaEmbedResponse
	err := p.postJSON(callCtx, target, payload, &out)
	if err != nil {
		if !errors.Is(err, core.ErrNotFound) {
			return nil, err
		}
		p.logger.Warn("ollama /api/embed missing, falling back to /api/embeddings", "provider", p.cfg.ID)
		return p.embedLegacy(callCtx, texts, model)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama: %s", p.redact(collapseSpace(out.Error)))
	}
	if len(out.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama: %s returned no embeddings", target)
	}
	vectors := make([][]float32, len(out.Embeddings))
	for i, vector := range out.Embeddings {
		vectors[i] = toFloat32(vector)
	}
	if len(vectors) != len(texts) {
		p.logger.Warn("ollama returned an unexpected embedding count", "want", len(texts), "got", len(vectors))
	}
	return vectors, nil
}

// embedLegacy embeds every text with one /api/embeddings call each.
func (p *ollamaProvider) embedLegacy(ctx context.Context, texts []string, model string) ([][]float32, error) {
	target := endpoint(p.cfg.BaseURL, "/api/embeddings")
	vectors := make([][]float32, 0, len(texts))
	for _, text := range texts {
		payload := &ollamaLegacyEmbedRequest{Model: p.embedModel(model), Prompt: text}
		var out ollamaLegacyEmbedResponse
		if err := p.postJSON(ctx, target, payload, &out); err != nil {
			return nil, err
		}
		if out.Error != "" {
			return nil, fmt.Errorf("ollama: %s", p.redact(collapseSpace(out.Error)))
		}
		vectors = append(vectors, toFloat32(out.Embedding))
	}
	return vectors, nil
}

// ollamaTags is the result of an /api/tags call.
type ollamaTags struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// ListModels returns the sorted model names the server has pulled.
func (p *ollamaProvider) ListModels(ctx context.Context) ([]string, error) {
	callCtx, cancel := p.withTimeout(ctx)
	defer cancel()

	target := endpoint(p.cfg.BaseURL, "/api/tags")
	var out ollamaTags
	if err := p.getJSON(callCtx, target, &out); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(out.Models))
	for _, item := range out.Models {
		if item.Name != "" {
			models = append(models, item.Name)
		}
	}
	sort.Strings(models)
	return models, nil
}

// Health lists the models.
func (p *ollamaProvider) Health(ctx context.Context) error {
	_, err := p.ListModels(ctx)
	return err
}
