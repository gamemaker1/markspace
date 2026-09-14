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
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// anthropicVersion is the API version header every call sends.
	anthropicVersion = "2023-06-01"
	// anthropicDefaultMaxTokens applies when the configuration sets no limit,
	// because the API requires max_tokens.
	anthropicDefaultMaxTokens = 2048
)

// anthropicProvider speaks the Anthropic messages protocol on /v1/messages.
type anthropicProvider struct {
	baseProvider
}

// newAnthropicProvider builds a provider for the Anthropic protocol.
func newAnthropicProvider(cfg *core.AIProviderConfig, client *http.Client, logger *slog.Logger) *anthropicProvider {
	p := &anthropicProvider{baseProvider{cfg: cfg, client: client, logger: logger, label: "anthropic"}}
	p.auth = p.setAuth
	return p
}

// setAuth adds the API key and version headers.
func (p *anthropicProvider) setAuth(h http.Header) {
	if p.cfg.APIKey != "" {
		h.Set("x-api-key", p.cfg.APIKey)
	}
	h.Set("anthropic-version", anthropicVersion)
}

// Kind returns core.ProviderAnthropic.
func (p *anthropicProvider) Kind() core.AIProviderKind { return core.ProviderAnthropic }

// anthropicChatRequest is the body of a messages call.
type anthropicChatRequest struct {
	Model         string             `json:"model,omitempty"`
	MaxTokens     int                `json:"max_tokens"`
	Temperature   float64            `json:"temperature"`
	System        string             `json:"system,omitempty"`
	Messages      []anthropicMessage `json:"messages"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Stream        bool               `json:"stream"`
}

// anthropicErrorBody is the error object of a messages call.
type anthropicErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// anthropicMessageResponse is the result of a non-streamed messages call.
type anthropicMessageResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage *anthropicUsage     `json:"usage"`
	Error *anthropicErrorBody `json:"error"`
}

// anthropicUsage is the token accounting of a messages call.
type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// anthropicStreamFrame is one server-sent event of a streamed messages call.
// message_start carries the input token count under message.usage, and
// message_delta carries the running output token count under usage.
type anthropicStreamFrame struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
	Message *struct {
		Usage *anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage *anthropicUsage     `json:"usage"`
	Error *anthropicErrorBody `json:"error"`
}

// anthropicMessage is one turn on the wire. Content is a string for a turn
// without images, which encodes exactly as core.AIMessage without images, or
// a list of content blocks for a turn with images.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// anthropicBlock is one image or text content block.
type anthropicBlock struct {
	Type   string                `json:"type"`
	Text   string                `json:"text,omitempty"`
	Source *anthropicImageSource `json:"source,omitempty"`
}

// anthropicImageSource carries a base64 image.
type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// anthropicMessages converts turns to the wire form. A turn with images
// becomes one image block per image followed by the text block.
func anthropicMessages(turns []core.AIMessage) []anthropicMessage {
	out := make([]anthropicMessage, len(turns))
	for i, turn := range turns {
		if len(turn.Images) == 0 {
			out[i] = anthropicMessage{Role: turn.Role, Content: turn.Content}
			continue
		}
		blocks := make([]anthropicBlock, 0, len(turn.Images)+1)
		for _, img := range turn.Images {
			mediaType, data := imageData(img)
			blocks = append(blocks, anthropicBlock{
				Type:   "image",
				Source: &anthropicImageSource{Type: "base64", MediaType: mediaType, Data: data},
			})
		}
		if turn.Content != "" {
			blocks = append(blocks, anthropicBlock{Type: "text", Text: turn.Content})
		}
		out[i] = anthropicMessage{Role: turn.Role, Content: blocks}
	}
	return out
}

// splitSystem hoists every system message into the top-level system field and
// merges consecutive same-role messages, because the API rejects two turns in
// a row with the same role. Merged turns keep the images of both. A role that
// is neither user nor assistant is treated as a user turn. Images on a system
// message are dropped, because the system field holds text only.
func splitSystem(msgs []core.AIMessage) (string, []core.AIMessage) {
	var system []string
	out := make([]core.AIMessage, 0, len(msgs))
	for _, msg := range msgs {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "system" {
			if strings.TrimSpace(msg.Content) != "" {
				system = append(system, msg.Content)
			}
			continue
		}
		if role != "assistant" {
			role = "user"
		}
		if len(out) > 0 && out[len(out)-1].Role == role {
			last := &out[len(out)-1]
			last.Content += "\n\n" + msg.Content
			if len(msg.Images) > 0 {
				last.Images = append(append([]core.AIImage(nil), last.Images...), msg.Images...)
			}
			continue
		}
		turn := core.AIMessage{Role: role, Content: msg.Content}
		if len(msg.Images) > 0 {
			turn.Images = append([]core.AIImage(nil), msg.Images...)
		}
		out = append(out, turn)
	}
	return strings.Join(system, "\n\n"), out
}

// maxTokensOrDefault resolves the required max_tokens field.
func (p *anthropicProvider) maxTokensOrDefault(opts core.AIOptions) int {
	if limit := p.maxTokens(opts); limit > 0 {
		return limit
	}
	return anthropicDefaultMaxTokens
}

// chatRequest builds the request body for a messages call.
func (p *anthropicProvider) chatRequest(msgs []core.AIMessage, opts core.AIOptions, stream bool) (*anthropicChatRequest, error) {
	system, turns := splitSystem(msgs)
	if len(turns) == 0 {
		return nil, core.Invalid("messages", "at least one user message is required")
	}
	return &anthropicChatRequest{
		Model:         p.model(opts),
		MaxTokens:     p.maxTokensOrDefault(opts),
		Temperature:   p.temperature(opts),
		System:        system,
		Messages:      anthropicMessages(turns),
		StopSequences: opts.Stop,
		Stream:        stream,
	}, nil
}

// Complete sends one non-streamed messages call.
func (p *anthropicProvider) Complete(ctx context.Context, msgs []core.AIMessage, opts core.AIOptions) (*core.AIResult, error) {
	start := time.Now()
	payload, err := p.chatRequest(msgs, opts, false)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := p.withTimeout(ctx)
	defer cancel()

	target := endpoint(p.cfg.BaseURL, "/v1/messages")
	var out anthropicMessageResponse
	if err := p.postJSON(callCtx, target, payload, &out); err != nil {
		return nil, err
	}
	if out.Error != nil && out.Error.Message != "" {
		return nil, fmt.Errorf("anthropic: %s", p.redact(collapseSpace(out.Error.Message)))
	}
	var text strings.Builder
	for _, block := range out.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	result := &core.AIResult{
		Text:     text.String(),
		Provider: p.cfg.ID,
		Model:    p.model(opts),
		Elapsed:  time.Since(start),
	}
	if out.Usage != nil {
		result.PromptTokens = out.Usage.InputTokens
		result.CompletionTokens = out.Usage.OutputTokens
	}
	return result, nil
}

// Stream sends one streamed messages call. Text arrives in
// content_block_delta events and message_stop ends the stream. The Done chunk
// carries the model, the input tokens from message_start and the output
// tokens from message_delta, each zero when the server omits it. A stream
// canceled mid flight can close without a final chunk, so a consumer treats a
// closed channel as terminal.
func (p *anthropicProvider) Stream(ctx context.Context, msgs []core.AIMessage, opts core.AIOptions) (<-chan core.AIChunk, error) {
	payload, err := p.chatRequest(msgs, opts, true)
	if err != nil {
		return nil, err
	}
	model := payload.Model
	callCtx, cancel := p.withTimeout(ctx)
	target := endpoint(p.cfg.BaseURL, "/v1/messages")
	resp, err := p.do(callCtx, http.MethodPost, target, payload, "text/event-stream")
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
			data, ok := sseData(scanner.Text())
			if !ok || data == "" {
				continue
			}
			var frame anthropicStreamFrame
			if err := json.Unmarshal([]byte(data), &frame); err != nil {
				p.logger.Warn("anthropic stream frame ignored", "url", target, "error", err)
				continue
			}
			if frame.Message != nil && frame.Message.Usage != nil {
				usage.observe(frame.Message.Usage.InputTokens, frame.Message.Usage.OutputTokens)
			}
			if frame.Usage != nil {
				usage.observe(frame.Usage.InputTokens, frame.Usage.OutputTokens)
			}
			switch frame.Type {
			case "content_block_delta":
				if frame.Delta.Text == "" {
					continue
				}
				if !sendChunk(callCtx, ch, core.AIChunk{Delta: frame.Delta.Text}) {
					return
				}
			case "message_stop":
				finishStream(callCtx, ch, model, usage)
				return
			case "error":
				message := "stream failed"
				if frame.Error != nil && frame.Error.Message != "" {
					message = collapseSpace(frame.Error.Message)
				}
				failStream(callCtx, ch, fmt.Errorf("anthropic: %s", p.redact(message)))
				return
			}
		}
		if err := scanner.Err(); err != nil {
			failStream(callCtx, ch, fmt.Errorf("anthropic: read stream %s: %w", target, err))
			return
		}
		finishStream(callCtx, ch, model, usage)
	}()
	return ch, nil
}

// Embed always fails, because the Anthropic API has no embedding endpoint.
func (p *anthropicProvider) Embed(ctx context.Context, texts []string, model string) ([][]float32, error) {
	return nil, fmt.Errorf("anthropic: embeddings: %w", core.ErrUnsupported)
}

// ListModels returns the sorted model ids the server reports. A server or key
// without a model listing yields the single configured model.
func (p *anthropicProvider) ListModels(ctx context.Context) ([]string, error) {
	models, err := p.fetchModels(ctx)
	if err != nil {
		p.logger.Warn("anthropic model listing failed, using the configured model", "provider", p.cfg.ID, "error", err)
		return p.configuredModels(), nil
	}
	if len(models) == 0 {
		return p.configuredModels(), nil
	}
	return models, nil
}

// fetchModels reads /v1/models without the configured model fallback.
func (p *anthropicProvider) fetchModels(ctx context.Context) ([]string, error) {
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

// configuredModels returns the configured model as a one entry list.
func (p *anthropicProvider) configuredModels() []string {
	if p.cfg.Model == "" {
		return nil
	}
	return []string{p.cfg.Model}
}

// Health reads the model listing. A gateway without that endpoint is probed
// with a one token completion instead.
func (p *anthropicProvider) Health(ctx context.Context) error {
	_, err := p.fetchModels(ctx)
	if err == nil {
		return nil
	}
	if !errors.Is(err, core.ErrNotFound) {
		return err
	}
	p.logger.Warn("anthropic model listing missing, probing with a completion", "provider", p.cfg.ID)
	msgs := []core.AIMessage{{Role: "user", Content: "ping"}}
	if _, cerr := p.Complete(ctx, msgs, core.AIOptions{MaxTokens: 1}); cerr != nil {
		return cerr
	}
	return nil
}
