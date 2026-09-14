package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// maxCatalogBody caps one model listing response.
	maxCatalogBody = 32 << 20
	// maxOllamaShows caps the POST /api/show calls of one Ollama listing.
	maxOllamaShows = 200
)

// ListCatalog lists the models the provider built for cfg offers, with the
// context length, modalities and price its listing reports. Every model
// carries the provider id, is enabled and available, and has the vendor from
// ModelVendor.
func (r *Registry) ListCatalog(ctx context.Context, cfg *core.AIProviderConfig) ([]core.AIModel, error) {
	provider, err := r.New(cfg)
	if err != nil {
		return nil, err
	}
	var models []core.AIModel
	switch p := provider.(type) {
	case *openaiProvider:
		models, err = p.listCatalog(ctx)
	case *ollamaProvider:
		models, err = p.listCatalog(ctx)
	case *anthropicProvider:
		models, err = p.listCatalog(ctx)
	case *comfyProvider:
		models, err = p.listCatalog(ctx)
	default:
		return nil, fmt.Errorf("ai provider %s: no model listing: %w", cfg.ID, core.ErrUnsupported)
	}
	if err != nil {
		return nil, err
	}
	for i := range models {
		m := &models[i]
		m.ProviderID = cfg.ID
		m.Enabled = true
		m.Available = true
		if m.Name == "" {
			m.Name = m.ModelID
		}
		m.Vendor = ModelVendor(cfg, m.ModelID)
	}
	sort.SliceStable(models, func(i, j int) bool { return models[i].ModelID < models[j].ModelID })
	return models, nil
}

// fetchBody sends one request with the call timeout and returns at most
// maxCatalogBody bytes of the answer. A larger answer is an error.
func (b *baseProvider) fetchBody(ctx context.Context, method, url string, payload any) ([]byte, error) {
	callCtx, cancel := b.withTimeout(ctx)
	defer cancel()
	resp, err := b.do(callCtx, method, url, payload, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBody+1))
	if err != nil {
		return nil, fmt.Errorf("%s: read %s: %w", b.label, url, err)
	}
	if len(body) > maxCatalogBody {
		return nil, fmt.Errorf("%s: %s is larger than 32 MiB: %w", b.label, url, core.ErrTooLarge)
	}
	return body, nil
}

// listCatalog reads GET /v1/models.
func (p *openaiProvider) listCatalog(ctx context.Context) ([]core.AIModel, error) {
	body, err := p.fetchBody(ctx, http.MethodGet, endpoint(p.cfg.BaseURL, "/v1/models"), nil)
	if err != nil {
		return nil, err
	}
	return parseOpenAIModels(body)
}

// listCatalog reads GET /api/tags and then POST /api/show for each model. A
// failed show keeps the model as text only.
func (p *ollamaProvider) listCatalog(ctx context.Context) ([]core.AIModel, error) {
	body, err := p.fetchBody(ctx, http.MethodGet, endpoint(p.cfg.BaseURL, "/api/tags"), nil)
	if err != nil {
		return nil, err
	}
	names, err := parseOllamaTags(body)
	if err != nil {
		return nil, err
	}
	out := make([]core.AIModel, 0, len(names))
	for i, name := range names {
		model := core.AIModel{ModelID: name, Inputs: []string{core.ModalityText}, Outputs: []string{core.ModalityText}}
		if i < maxOllamaShows && ctx.Err() == nil {
			show, err := p.fetchBody(ctx, http.MethodPost, endpoint(p.cfg.BaseURL, "/api/show"), map[string]string{"model": name})
			if err == nil {
				if parsed, perr := parseOllamaShow(name, show); perr == nil {
					model = parsed
				}
			} else {
				p.logger.Debug("ollama show failed", "provider", p.cfg.ID, "model", name, "error", err)
			}
		}
		out = append(out, model)
	}
	return out, nil
}

// listCatalog reads GET /v1/models?limit=1000.
func (p *anthropicProvider) listCatalog(ctx context.Context) ([]core.AIModel, error) {
	body, err := p.fetchBody(ctx, http.MethodGet, endpoint(p.cfg.BaseURL, "/v1/models?limit=1000"), nil)
	if err != nil {
		return nil, err
	}
	return parseAnthropicModels(body)
}

// listCatalog reads GET /object_info/CheckpointLoaderSimple.
func (p *comfyProvider) listCatalog(ctx context.Context) ([]core.AIModel, error) {
	body, err := p.fetchBody(ctx, http.MethodGet, endpoint(p.cfg.BaseURL, "/object_info/CheckpointLoaderSimple"), nil)
	if err != nil {
		return nil, err
	}
	return parseComfyObjectInfo(body, p.cfg.ImageEditing)
}

// parseOpenAIModels parses an OpenAI-compatible model listing. A listing in
// the OpenRouter format also yields the context length, the modalities, the
// tools flag and the price. A body without a data array yields no models.
func parseOpenAIModels(body []byte) ([]core.AIModel, error) {
	var doc struct {
		Data []openRouterModel `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("openai: decode model listing: %w", err)
	}
	out := make([]core.AIModel, 0, len(doc.Data))
	for _, item := range doc.Data {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		e := item.entry()
		m := core.AIModel{
			ModelID:       item.ID,
			Name:          item.Name,
			ContextLength: e.ContextLength,
			MaxOutput:     e.MaxOutput,
			Tools:         e.Tools,
			Inputs:        []string{core.ModalityText},
			Outputs:       []string{core.ModalityText},
		}
		if e.HasCaps {
			m.Inputs, m.Outputs, m.CapsListed = e.Inputs, e.Outputs, true
		}
		if e.Price != nil {
			m.Price, m.PriceSource = e.Price, core.PriceSourceProvider
		}
		out = append(out, m)
	}
	return out, nil
}

// parseOllamaTags parses GET /api/tags into model names.
func parseOllamaTags(body []byte) ([]string, error) {
	var doc struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("ollama: decode tags: %w", err)
	}
	names := make([]string, 0, len(doc.Models))
	for _, m := range doc.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// parseOllamaShow parses POST /api/show for one model. The capabilities
// completion, vision, embedding and tools set the modalities and the tools
// flag. The context length is the model_info key ending in
// ".context_length".
func parseOllamaShow(name string, body []byte) (core.AIModel, error) {
	var doc struct {
		Capabilities []string                   `json:"capabilities"`
		ModelInfo    map[string]json.RawMessage `json:"model_info"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return core.AIModel{}, fmt.Errorf("ollama: decode show %s: %w", name, err)
	}
	m := core.AIModel{ModelID: name, Name: name}
	for key, raw := range doc.ModelInfo {
		if strings.HasSuffix(key, ".context_length") {
			var n flexInt
			_ = n.UnmarshalJSON(raw)
			m.ContextLength = int(n)
		}
	}
	if len(doc.Capabilities) > 0 {
		m.CapsListed = true
		m.Inputs = []string{core.ModalityText}
		for _, c := range doc.Capabilities {
			switch c {
			case "completion":
				m.Outputs = append(m.Outputs, core.ModalityText)
			case "vision":
				m.Inputs = append(m.Inputs, core.ModalityImage)
			case "embedding":
				m.Outputs = append(m.Outputs, core.ModalityEmbedding)
			case "tools":
				m.Tools = true
			}
		}
	}
	if len(m.Inputs) == 0 {
		m.Inputs = []string{core.ModalityText}
	}
	if len(m.Outputs) == 0 {
		m.Outputs = []string{core.ModalityText}
	}
	return m, nil
}

// parseAnthropicModels parses GET /v1/models. Every current Claude model
// accepts images, so each model takes text and image input.
func parseAnthropicModels(body []byte) ([]core.AIModel, error) {
	var doc struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("anthropic: decode model listing: %w", err)
	}
	out := make([]core.AIModel, 0, len(doc.Data))
	for _, item := range doc.Data {
		if item.ID == "" {
			continue
		}
		out = append(out, core.AIModel{
			ModelID:    item.ID,
			Name:       item.DisplayName,
			Inputs:     []string{core.ModalityText, core.ModalityImage},
			Outputs:    []string{core.ModalityText},
			CapsListed: true,
		})
	}
	return out, nil
}

// parseComfyObjectInfo parses GET /object_info/CheckpointLoaderSimple. Each
// checkpoint produces images, and accepts an image when editing is on. Older
// servers send [[names...], {...}] and newer ones ["COMBO", {"options":
// [names...]}].
func parseComfyObjectInfo(body []byte, editing bool) ([]core.AIModel, error) {
	var info comfyObjectInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("comfyui: decode object info: %w", err)
	}
	spec := info["CheckpointLoaderSimple"].Input.Required["ckpt_name"]
	if len(spec) == 0 {
		return nil, fmt.Errorf("comfyui: object info lists no ckpt_name input")
	}
	var names []string
	if err := json.Unmarshal(spec[0], &names); err != nil {
		var combo struct {
			Options []string `json:"options"`
		}
		if len(spec) < 2 || json.Unmarshal(spec[1], &combo) != nil {
			return nil, fmt.Errorf("comfyui: object info has an unknown ckpt_name format")
		}
		names = combo.Options
	}
	inputs := []string{core.ModalityText}
	if editing {
		inputs = append(inputs, core.ModalityImage)
	}
	sort.Strings(names)
	out := make([]core.AIModel, 0, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		out = append(out, core.AIModel{
			ModelID:    name,
			Name:       name,
			Inputs:     append([]string(nil), inputs...),
			Outputs:    []string{core.ModalityImage},
			CapsListed: true,
		})
	}
	return out, nil
}
