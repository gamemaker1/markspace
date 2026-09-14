package core

import (
	"math"
	"strings"
	"time"
)

// Model modalities, used in AIModel.Inputs and AIModel.Outputs.
const (
	ModalityText      = "text"
	ModalityImage     = "image"
	ModalityFile      = "file"
	ModalityAudio     = "audio"
	ModalityEmbedding = "embedding"
)

// Price sources. A model's effective price comes from the first source that
// has one, in the order override, provider, local, openrouter, litellm.
const (
	PriceSourceOverride   = "override"
	PriceSourceProvider   = "provider"
	PriceSourceLocal      = "local"
	PriceSourceOpenRouter = "openrouter"
	PriceSourceLiteLLM    = "litellm"
	PriceSourceNone       = "none"
)

// Preset price sources name where a preset's prices come from.
const (
	PresetPricesProvider = "provider"
	PresetPricesCatalog  = "catalog"
	PresetPricesNone     = "none"
)

// AIPrice is the price of one model in US dollars.
type AIPrice struct {
	InputPerMTok      float64 `json:"inputPerMTok" yaml:"inputPerMTok"`
	OutputPerMTok     float64 `json:"outputPerMTok" yaml:"outputPerMTok"`
	CacheReadPerMTok  float64 `json:"cacheReadPerMTok,omitempty" yaml:"cacheReadPerMTok"`
	CacheWritePerMTok float64 `json:"cacheWritePerMTok,omitempty" yaml:"cacheWritePerMTok"`
	PerImageIn        float64 `json:"perImageIn,omitempty" yaml:"perImageIn"`
	PerImageOut       float64 `json:"perImageOut,omitempty" yaml:"perImageOut"`
	PerRequest        float64 `json:"perRequest,omitempty" yaml:"perRequest"`
}

// CostMicros returns the cost of a call in millionths of a dollar, rounded to
// the nearest micro. Negative, NaN and infinite inputs count as 0.
func (p AIPrice) CostMicros(inputTokens, outputTokens, imagesIn, imagesOut int64) int64 {
	cost := float64(max(inputTokens, 0))*clean(p.InputPerMTok)/1e6 +
		float64(max(outputTokens, 0))*clean(p.OutputPerMTok)/1e6 +
		float64(max(imagesIn, 0))*clean(p.PerImageIn) +
		float64(max(imagesOut, 0))*clean(p.PerImageOut) +
		clean(p.PerRequest)
	micros := math.Round(cost * 1e6)
	if micros >= math.MaxInt64/4 {
		return math.MaxInt64 / 4
	}
	return int64(micros)
}

// Validate checks that every price is a finite number of 0 or more.
func (p AIPrice) Validate(field string) error {
	for _, v := range []float64{p.InputPerMTok, p.OutputPerMTok, p.CacheReadPerMTok,
		p.CacheWritePerMTok, p.PerImageIn, p.PerImageOut, p.PerRequest} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e9 {
			return Invalid(field, "prices must be numbers from 0 to 1000000000")
		}
	}
	return nil
}

// clean maps a negative, NaN or infinite price to 0.
func clean(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0
	}
	return f
}

// AIModel is one row of the model catalog: a model that one provider offers.
type AIModel struct {
	ProviderID    string   `json:"providerId"`
	ModelID       string   `json:"model"`
	Name          string   `json:"name,omitempty"`
	Vendor        string   `json:"vendor,omitempty"`
	ContextLength int      `json:"contextLength,omitempty"`
	MaxOutput     int      `json:"maxOutput,omitempty"`
	Inputs        []string `json:"inputs"`
	Outputs       []string `json:"outputs"`
	Tools         bool     `json:"tools,omitempty"`

	// Price is the fetched price, nil when no source had one. PriceSource
	// names the source: provider, local, openrouter, litellm or none.
	Price          *AIPrice  `json:"price,omitempty"`
	PriceSource    string    `json:"priceSource"`
	PriceMatched   string    `json:"priceMatched,omitempty"`
	PriceFetchedAt time.Time `json:"priceFetchedAt,omitempty"`
	// PriceOverride is a price set by an admin. It wins over Price.
	PriceOverride *AIPrice `json:"priceOverride,omitempty"`

	// Enabled offers the model to users and routes. Available reports that
	// the model was present in the provider's last listing.
	Enabled   bool      `json:"enabled"`
	Available bool      `json:"available"`
	LastSeen  time.Time `json:"lastSeen,omitempty"`

	// The fields below are filled by listings and are not stored.
	ProviderName    string   `json:"providerName,omitempty"`
	Local           bool     `json:"local,omitempty"`
	EffectivePrice  *AIPrice `json:"effectivePrice,omitempty"`
	EffectiveSource string   `json:"effectiveSource,omitempty"`
	// CapsListed reports that the provider listing described the inputs and
	// outputs, so a catalog entry does not replace them.
	CapsListed bool `json:"-"`
}

// Effective returns the price a call is charged with and its source: the
// override, else the fetched price. The price is nil when neither exists.
func (m *AIModel) Effective() (*AIPrice, string) {
	if m == nil {
		return nil, PriceSourceNone
	}
	if m.PriceOverride != nil {
		p := *m.PriceOverride
		return &p, PriceSourceOverride
	}
	if m.Price != nil {
		p := *m.Price
		src := m.PriceSource
		if src == "" {
			src = PriceSourceNone
		}
		return &p, src
	}
	return nil, PriceSourceNone
}

// HasInput reports whether the model accepts a modality.
func (m *AIModel) HasInput(modality string) bool { return hasString(m.Inputs, modality) }

// HasOutput reports whether the model produces a modality.
func (m *AIModel) HasOutput(modality string) bool { return hasString(m.Outputs, modality) }

func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// AIModelQuery filters a catalog listing. Empty fields match every row.
type AIModelQuery struct {
	// Capability is text, vision, image.generate, image.edit, embeddings or a
	// route task, and keeps the models able to serve it.
	Capability string `json:"capability,omitempty"`
	ProviderID string `json:"provider,omitempty"`
	// Q matches the model id, name or vendor, ignoring case.
	Q string `json:"q,omitempty"`
}

// AIModelUpdate changes the admin settings of one catalog row. A nil Enabled
// keeps the flag. SetPrice with a nil PriceOverride clears the override.
type AIModelUpdate struct {
	Enabled       *bool
	SetPrice      bool
	PriceOverride *AIPrice
}

// Routing tasks.
const (
	RouteDefault       = "default"
	RouteEdit          = "edit"
	RouteAnswer        = "answer"
	RouteChat          = "chat"
	RouteVision        = "vision"
	RouteImageGenerate = "image.generate"
	RouteImageEdit     = "image.edit"
	RouteEmbeddings    = "embeddings"
)

// AIRouteTasks lists the built-in routing tasks in display order.
var AIRouteTasks = []string{RouteDefault, RouteEdit, RouteAnswer, RouteChat, RouteVision,
	RouteImageGenerate, RouteImageEdit, RouteEmbeddings}

// RouteScopeInstance names the instance routes. Any other scope is a user id.
const RouteScopeInstance = "instance"

// MaxRouteTargets caps the targets of one route.
const MaxRouteTargets = 16

// ValidRouteTask reports whether task is a built-in task or a plugin task of
// the form plugin.<id>.<purpose>.
func ValidRouteTask(task string) bool {
	for _, t := range AIRouteTasks {
		if t == task {
			return true
		}
	}
	rest, ok := strings.CutPrefix(task, "plugin.")
	if !ok || len(task) > 128 {
		return false
	}
	id, purpose, ok := strings.Cut(rest, ".")
	return ok && routeWord(id) && routeWord(purpose)
}

// routeWord reports whether s is 1 to 64 lower-case letters, digits, dashes
// or underscores.
func routeWord(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// AINeeds lists what a call needs from a model.
type AINeeds struct {
	Text      bool `json:"text,omitempty"`
	ImageIn   bool `json:"imageIn,omitempty"`
	ImageOut  bool `json:"imageOut,omitempty"`
	ImageEdit bool `json:"imageEdit,omitempty"`
	Embedding bool `json:"embedding,omitempty"`
	// MinContext is the estimated prompt plus requested output in tokens. A
	// model with a smaller known context length is skipped.
	MinContext int `json:"minContext,omitempty"`
}

// RouteNeeds returns the capabilities a task needs.
func RouteNeeds(task string) AINeeds {
	switch task {
	case RouteVision:
		return AINeeds{Text: true, ImageIn: true}
	case RouteImageGenerate:
		return AINeeds{ImageOut: true}
	case RouteImageEdit:
		return AINeeds{ImageEdit: true}
	case RouteEmbeddings:
		return AINeeds{Embedding: true}
	}
	return AINeeds{Text: true}
}

// AIRoute is the ordered model list of one task. The first model that is
// enabled, available, capable and within budget handles the call.
type AIRoute struct {
	Task    string          `json:"task"`
	Targets []AIRouteTarget `json:"targets"`
}

// AIRouteTarget names one model of one provider.
type AIRouteTarget struct {
	ProviderID string `json:"providerId" yaml:"providerId"`
	Model      string `json:"model" yaml:"model"`
}

// AIRoutes is the answer to a route listing.
type AIRoutes struct {
	// Instance holds the instance route of every task that has one.
	Instance []AIRoute `json:"instance"`
	// User holds the actor's own routes, only when AllowUserRoutes is on.
	User            []AIRoute `json:"user,omitempty"`
	AllowUserRoutes bool      `json:"allowUserRoutes"`
	// Tasks lists the built-in tasks in display order.
	Tasks []string `json:"tasks"`
}

// AIPreset fills in a new provider. Fields lists the form fields the preset
// needs, from name, apiKey, baseUrl, kind and local.
type AIPreset struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Kind        AIProviderKind    `json:"kind"`
	BaseURL     string            `json:"baseUrl,omitempty"`
	Local       bool              `json:"local"`
	PriceSource string            `json:"priceSource"`
	Vendor      string            `json:"vendor,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Fields      []string          `json:"fields"`
}

// AIPriceRefresh reports a download of the public price catalogs.
type AIPriceRefresh struct {
	Sources []AIPriceCatalogStatus `json:"sources"`
	// Repriced counts catalog rows whose price was recomputed.
	Repriced int `json:"repriced"`
}

// AIPriceCatalogStatus is the state of one public price catalog.
type AIPriceCatalogStatus struct {
	Source    string    `json:"source"`
	FetchedAt time.Time `json:"fetchedAt,omitempty"`
	Models    int       `json:"models"`
	Error     string    `json:"error,omitempty"`
}
