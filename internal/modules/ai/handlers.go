package ai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// aiReindexRequest is the body of POST /api/ai/reindex.
type aiReindexRequest struct {
	SpaceID string `json:"spaceId,omitempty"`
}

// indexedResponse reports how many records a reindex touched.
type indexedResponse struct {
	Indexed int `json:"indexed"`
}

// aiProviderInput is the body of provider create and update.
// core.AIProviderConfig hides APIKey from JSON so a key never appears in a
// response. This type accepts the key on the way in. Enabled is a pointer so
// an omitted flag can be told apart from false. The image fields
// imageGeneration, imageEditing, imageModel, imageSizes, comfyGenerate and
// comfyEdit come in through the embedded configuration on create and update.
type aiProviderInput struct {
	core.AIProviderConfig
	APIKey  string `json:"apiKey"`
	Enabled *bool  `json:"enabled"`
}

// config returns the provider configuration with the submitted key. An
// omitted enabled flag takes enabledDefault.
func (in *aiProviderInput) config(enabledDefault bool) *core.AIProviderConfig {
	cfg := in.AIProviderConfig
	cfg.APIKey = in.APIKey
	cfg.Enabled = enabledDefault
	if in.Enabled != nil {
		cfg.Enabled = *in.Enabled
	}
	return &cfg
}

// deltaFrame is the data of an AI stream delta event.
type deltaFrame struct {
	Delta string `json:"delta"`
}

// doneFrame ends an AI stream and reports the model and token usage when the
// provider sent them, plus the context sources that went into the prompt.
type doneFrame struct {
	Done             bool            `json:"done"`
	Model            string          `json:"model,omitempty"`
	Provider         string          `json:"provider,omitempty"`
	Attempts         int             `json:"attempts,omitempty"`
	PromptTokens     int             `json:"promptTokens,omitempty"`
	CompletionTokens int             `json:"completionTokens,omitempty"`
	Sources          []core.AISource `json:"sources,omitempty"`
	// Budget is set once a monthly budget is 80 percent used.
	Budget *core.AIBudgetWarning `json:"budget,omitempty"`
}

// errorFrame is the data of an AI stream error event.
type errorFrame struct {
	Error string `json:"error"`
}

// listAIProviders serves GET /api/ai/providers.
func (m *Module) listAIProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := m.Backend.ListAIProviders(r.Context(), api.ActorFrom(r.Context()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, providers)
}

// createAIProvider serves POST /api/ai/providers. A provider created without
// an enabled flag is enabled.
func (m *Module) createAIProvider(w http.ResponseWriter, r *http.Request) {
	var in aiProviderInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	out, err := m.Backend.SaveAIProvider(r.Context(), api.ActorFrom(r.Context()), in.config(true))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, out)
}

// updateAIProvider serves PATCH /api/ai/providers/{id}. The path id wins over
// any id in the body. The body replaces the stored provider, except that an
// omitted apiKey keeps the stored key and an omitted enabled flag keeps the
// stored flag.
func (m *Module) updateAIProvider(w http.ResponseWriter, r *http.Request) {
	var in aiProviderInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	enabled := true
	if in.Enabled == nil {
		if list, err := m.Backend.ListAIProviders(r.Context(), api.ActorFrom(r.Context())); err == nil {
			for _, p := range list {
				if p.ID == id {
					enabled = p.Enabled
					break
				}
			}
		}
	}
	cfg := in.config(enabled)
	cfg.ID = id
	out, err := m.Backend.SaveAIProvider(r.Context(), api.ActorFrom(r.Context()), cfg)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// deleteAIProvider serves DELETE /api/ai/providers/{id}.
func (m *Module) deleteAIProvider(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.DeleteAIProvider(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// testAIProvider serves POST /api/ai/providers/{id}/test.
func (m *Module) testAIProvider(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.TestAIProvider(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// listAIModels serves GET /api/ai/models?capability=&provider=&q= and
// GET /api/ai/providers/{id}/models, which filters on the path provider.
func (m *Module) listAIModels(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	q := core.AIModelQuery{
		Capability: strings.TrimSpace(v.Get("capability")),
		ProviderID: strings.TrimSpace(v.Get("provider")),
		Q:          strings.TrimSpace(v.Get("q")),
	}
	if id := r.PathValue("id"); id != "" {
		q.ProviderID = id
	}
	models, err := m.Backend.ListAIModels(r.Context(), api.ActorFrom(r.Context()), q)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, models)
}

// refreshAIProvider serves POST /api/ai/providers/{id}/refresh.
func (m *Module) refreshAIProvider(w http.ResponseWriter, r *http.Request) {
	models, err := m.Backend.RefreshAIProvider(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, models)
}

// aiModelPatch is the body of PATCH /api/ai/models/{providerId}/{model}. An
// omitted field keeps its value, and a null priceOverride clears the override.
type aiModelPatch struct {
	Enabled       *bool           `json:"enabled"`
	PriceOverride json.RawMessage `json:"priceOverride"`
}

// updateAIModel serves PATCH /api/ai/models/{providerId}/{model...}. The
// model may contain slashes, such as openai/gpt-4o.
func (m *Module) updateAIModel(w http.ResponseWriter, r *http.Request) {
	var in aiModelPatch
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	update := core.AIModelUpdate{Enabled: in.Enabled}
	if raw := bytes.TrimSpace(in.PriceOverride); len(raw) > 0 {
		update.SetPrice = true
		if !bytes.Equal(raw, []byte("null")) {
			var price core.AIPrice
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&price); err != nil {
				m.Fail(w, r, core.Invalid("priceOverride", "priceOverride must be a price object or null"))
				return
			}
			update.PriceOverride = &price
		}
	}
	out, err := m.Backend.UpdateAIModel(r.Context(), api.ActorFrom(r.Context()), r.PathValue("providerId"), r.PathValue("model"), update)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// aiRouteInput is the body of a route save.
type aiRouteInput struct {
	Targets []core.AIRouteTarget `json:"targets"`
}

// listAIRoutes serves GET /api/ai/routes.
func (m *Module) listAIRoutes(w http.ResponseWriter, r *http.Request) {
	out, err := m.Backend.ListAIRoutes(r.Context(), api.ActorFrom(r.Context()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// saveAIRoute replaces the targets of one route in a scope.
func (m *Module) saveAIRoute(w http.ResponseWriter, r *http.Request, scope string) {
	var in aiRouteInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	out, err := m.Backend.SaveAIRoute(r.Context(), api.ActorFrom(r.Context()), scope, r.PathValue("task"), in.Targets)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// saveInstanceAIRoute serves PUT /api/ai/routes/instance/{task}.
func (m *Module) saveInstanceAIRoute(w http.ResponseWriter, r *http.Request) {
	m.saveAIRoute(w, r, core.RouteScopeInstance)
}

// saveMyAIRoute serves PUT /api/ai/routes/me/{task}.
func (m *Module) saveMyAIRoute(w http.ResponseWriter, r *http.Request) {
	m.saveAIRoute(w, r, "me")
}

// deleteMyAIRoute serves DELETE /api/ai/routes/me/{task}.
func (m *Module) deleteMyAIRoute(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.DeleteAIRoute(r.Context(), api.ActorFrom(r.Context()), r.PathValue("task")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// listAIPresets serves GET /api/ai/presets.
func (m *Module) listAIPresets(w http.ResponseWriter, r *http.Request) {
	presets, err := m.Backend.ListAIPresets(r.Context(), api.ActorFrom(r.Context()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, presets)
}

// refreshAIPrices serves POST /api/admin/ai/prices/refresh.
func (m *Module) refreshAIPrices(w http.ResponseWriter, r *http.Request) {
	out, err := m.Backend.RefreshAIPrices(r.Context(), api.ActorFrom(r.Context()))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// runAI serves POST /api/ai/run.
func (m *Module) runAI(w http.ResponseWriter, r *http.Request) {
	var req core.AIRequest
	if err := api.Decode(r, &req); err != nil {
		m.Fail(w, r, err)
		return
	}
	// Only the plugin host may attribute a call to a plugin.
	req.PluginID = ""
	out, err := m.Backend.RunAI(r.Context(), api.ActorFrom(r.Context()), req)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// streamAI serves POST /api/ai/stream as a text/event-stream of delta events
// closed by a done event, or by an error event when the provider fails.
func (m *Module) streamAI(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.StreamContext(r)
	defer cancel()
	var req core.AIRequest
	if err := api.Decode(r, &req); err != nil {
		m.Fail(w, r, err)
		return
	}
	// Only the plugin host may attribute a call to a plugin.
	req.PluginID = ""
	ch, err := m.Backend.StreamAI(ctx, api.ActorFrom(ctx), req)
	if err != nil {
		m.Fail(w, r, err)
		return
	}

	stream := api.NewSSE(w)
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-ch:
			if !ok {
				return
			}
			switch {
			case chunk.Err != nil:
				_ = stream.Send("error", errorFrame{Error: chunk.Err.Error()})
				return
			case chunk.Done:
				_ = stream.Send("done", doneFrame{
					Done:             true,
					Model:            chunk.Model,
					Provider:         chunk.Provider,
					Attempts:         chunk.Attempts,
					PromptTokens:     chunk.PromptTokens,
					CompletionTokens: chunk.CompletionTokens,
					Sources:          chunk.Sources,
					Budget:           chunk.Budget,
				})
				return
			case chunk.Delta != "":
				if err := stream.Send("delta", deltaFrame{Delta: chunk.Delta}); err != nil {
					m.Logger.Debug("ai stream write failed", "err", err)
					return
				}
			}
		}
	}
}

// reindexEmbeddings serves POST /api/ai/reindex.
func (m *Module) reindexEmbeddings(w http.ResponseWriter, r *http.Request) {
	var in aiReindexRequest
	if r.ContentLength != 0 {
		if err := api.Decode(r, &in); err != nil {
			m.Fail(w, r, err)
			return
		}
	}
	n, err := m.Backend.ReindexEmbeddings(r.Context(), api.ActorFrom(r.Context()), in.SpaceID)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, indexedResponse{Indexed: n})
}
