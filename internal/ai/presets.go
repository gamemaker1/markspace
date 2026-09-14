package ai

import "github.com/gamemaker1/markspace/internal/core"

// Preset form fields.
const (
	FieldName    = "name"
	FieldAPIKey  = "apiKey"
	FieldBaseURL = "baseUrl"
	FieldKind    = "kind"
	FieldLocal   = "local"
)

// presets is the table in docs/models.md. Vendor names the model maker used to
// match catalog prices. A gateway that hosts many makers carries its own
// name, which the LiteLLM list uses as the provider.
var presets = []core.AIPreset{
	{ID: "openrouter", Name: "OpenRouter", Kind: core.ProviderOpenAI, BaseURL: "https://openrouter.ai/api/v1",
		PriceSource: core.PresetPricesProvider, Headers: map[string]string{"X-Title": "Markspace"},
		Fields: []string{FieldName, FieldAPIKey}},
	{ID: "openai", Name: "OpenAI", Kind: core.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
		PriceSource: core.PresetPricesCatalog, Vendor: "openai", Fields: []string{FieldName, FieldAPIKey}},
	{ID: "anthropic", Name: "Anthropic", Kind: core.ProviderAnthropic, BaseURL: "https://api.anthropic.com",
		PriceSource: core.PresetPricesCatalog, Vendor: "anthropic", Fields: []string{FieldName, FieldAPIKey}},
	{ID: "gemini", Name: "Google Gemini", Kind: core.ProviderOpenAI, BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
		PriceSource: core.PresetPricesCatalog, Vendor: "google", Fields: []string{FieldName, FieldAPIKey}},
	{ID: "mistral", Name: "Mistral", Kind: core.ProviderOpenAI, BaseURL: "https://api.mistral.ai/v1",
		PriceSource: core.PresetPricesCatalog, Vendor: "mistral", Fields: []string{FieldName, FieldAPIKey}},
	{ID: "groq", Name: "Groq", Kind: core.ProviderOpenAI, BaseURL: "https://api.groq.com/openai/v1",
		PriceSource: core.PresetPricesCatalog, Vendor: "groq", Fields: []string{FieldName, FieldAPIKey}},
	{ID: "deepseek", Name: "DeepSeek", Kind: core.ProviderOpenAI, BaseURL: "https://api.deepseek.com/v1",
		PriceSource: core.PresetPricesCatalog, Vendor: "deepseek", Fields: []string{FieldName, FieldAPIKey}},
	{ID: "together", Name: "Together", Kind: core.ProviderOpenAI, BaseURL: "https://api.together.xyz/v1",
		PriceSource: core.PresetPricesCatalog, Vendor: "together", Fields: []string{FieldName, FieldAPIKey}},
	{ID: "ollama", Name: "Ollama", Kind: core.ProviderOllama, BaseURL: "http://ollama:11434", Local: true,
		PriceSource: core.PresetPricesNone, Fields: []string{FieldName, FieldBaseURL}},
	{ID: "openai-local", Name: "llama.cpp, LM Studio, vLLM, LocalAI", Kind: core.ProviderOpenAI, Local: true,
		PriceSource: core.PresetPricesNone, Fields: []string{FieldName, FieldBaseURL, FieldAPIKey}},
	{ID: "comfyui", Name: "ComfyUI", Kind: core.ProviderComfyUI, Local: true,
		PriceSource: core.PresetPricesNone, Fields: []string{FieldName, FieldBaseURL}},
	{ID: "custom", Name: "Custom", Kind: core.ProviderOpenAI,
		PriceSource: core.PresetPricesCatalog, Fields: []string{FieldName, FieldKind, FieldBaseURL, FieldAPIKey, FieldLocal}},
}

// Presets returns a copy of the preset table.
func Presets() []core.AIPreset {
	out := make([]core.AIPreset, len(presets))
	for i, p := range presets {
		out[i] = p
		out[i].Fields = append([]string(nil), p.Fields...)
		if p.Headers != nil {
			out[i].Headers = make(map[string]string, len(p.Headers))
			for k, v := range p.Headers {
				out[i].Headers[k] = v
			}
		}
	}
	return out
}

// PresetByID returns the preset with this id.
func PresetByID(id string) (core.AIPreset, bool) {
	for _, p := range Presets() {
		if p.ID == id {
			return p, true
		}
	}
	return core.AIPreset{}, false
}

// ApplyPreset fills the empty kind, base URL and headers of a provider from
// its preset, and sets Local when the preset is local. An unknown preset
// returns an error for the preset field.
func ApplyPreset(cfg *core.AIProviderConfig) error {
	if cfg.Preset == "" {
		return nil
	}
	p, ok := PresetByID(cfg.Preset)
	if !ok {
		return core.Invalid("preset", "unknown preset "+cfg.Preset)
	}
	if cfg.Kind == "" || cfg.Preset != "custom" {
		cfg.Kind = p.Kind
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = p.BaseURL
	}
	if p.Local {
		cfg.Local = true
	}
	for k, v := range p.Headers {
		if cfg.Headers == nil {
			cfg.Headers = map[string]string{}
		}
		if _, set := cfg.Headers[k]; !set {
			cfg.Headers[k] = v
		}
	}
	return nil
}
