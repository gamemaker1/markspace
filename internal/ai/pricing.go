package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// Public catalog sources and addresses.
const (
	OpenRouterCatalogURL = "https://openrouter.ai/api/v1/models"
	LiteLLMCatalogURL    = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
)

// CatalogEntry is one model of a public price catalog.
type CatalogEntry struct {
	// ID is the id as the catalog names it, such as openai/gpt-4o.
	ID string
	// Vendor is the canonical maker or host, such as openai or groq.
	Vendor        string
	Name          string
	Price         *core.AIPrice
	ContextLength int
	MaxOutput     int
	Inputs        []string
	Outputs       []string
	Tools         bool
	// HasCaps reports that the entry describes inputs and outputs.
	HasCaps bool
}

// PriceCatalog is one parsed public price catalog. It is safe for
// concurrent reads once built. The match index is built on first use.
type PriceCatalog struct {
	Source    string
	FetchedAt time.Time
	Entries   []CatalogEntry

	once   sync.Once
	byBase map[string][]indexedEntry
}

// indexedEntry is one catalog entry with its parsed id.
type indexedEntry struct {
	entry *CatalogEntry
	id    modelID
}

// Catalogs holds the parsed public catalogs. A nil catalog is absent.
type Catalogs struct {
	OpenRouter *PriceCatalog
	LiteLLM    *PriceCatalog
}

// modelID is a model id split into the parts that matching compares.
type modelID struct {
	// vendor is the canonical vendor from the id prefix, or empty.
	vendor string
	// raw is the lower-cased id without the vendor prefix, tag kept.
	raw string
	// base is the normalised id used to find candidates.
	base   string
	tag    string
	dated  bool
	hasTag bool
}

var (
	// dateSuffix matches -YYYY-MM-DD, -YYYYMMDD, @YYYY-MM-DD and @YYYYMMDD.
	dateSuffix = regexp.MustCompile(`[-@](\d{4}-\d{2}-\d{2}|\d{8})$`)
	// digitDot matches a dot between two digits.
	digitDot = regexp.MustCompile(`(\d)\.(\d)`)
)

// parseModelID splits and normalises an id. Normalising lower-cases the id,
// removes the tag after the last colon, removes a vendor prefix, removes a
// date suffix and turns a dot between two digits into a dash. For an id
// with more than one slash, such as openrouter/openai/gpt-4o, the first
// segment is the vendor and the base is the part after the last slash. A
// leading models/ segment is not a vendor.
func parseModelID(id string) modelID {
	s := strings.ToLower(strings.TrimSpace(id))
	var out modelID
	s = strings.TrimPrefix(s, "models/")
	if i := strings.Index(s, "/"); i >= 0 {
		out.vendor = CanonicalVendor(s[:i])
		s = s[i+1:]
	}
	out.raw = s
	if i := strings.LastIndex(s, ":"); i > 0 {
		out.tag = s[i+1:]
		out.hasTag = true
		s = s[:i]
	}
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if loc := dateSuffix.FindStringIndex(s); loc != nil && loc[0] > 0 {
		s = s[:loc[0]]
		out.dated = true
	}
	for digitDot.MatchString(s) {
		s = digitDot.ReplaceAllString(s, "$1-$2")
	}
	out.base = s
	return out
}

// NormalizeModelID returns the canonical vendor from the id prefix, or an
// empty string, and the normalised base id. "anthropic/claude-sonnet-4.5"
// and "claude-sonnet-4-5-20250929" share the base "claude-sonnet-4-5".
func NormalizeModelID(id string) (vendor, base string) {
	p := parseModelID(id)
	return p.vendor, p.base
}

// CanonicalVendor maps the vendor and host names that catalogs use to one
// name, such as mistralai to mistral and vertex_ai to google.
func CanonicalVendor(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case v == "mistralai":
		return "mistral"
	case v == "together_ai" || v == "togetherai":
		return "together"
	case strings.HasPrefix(v, "vertex_ai") || v == "gemini" || strings.HasPrefix(v, "google"):
		return "google"
	case v == "meta-llama" || v == "meta_llama":
		return "meta"
	case v == "x-ai":
		return "xai"
	case v == "deepseek-ai":
		return "deepseek"
	case v == "text-completion-openai":
		return "openai"
	}
	return v
}

// inferVendor guesses the maker of a model from its id. It feeds display
// only and never matching.
func inferVendor(id string) string {
	_, base := NormalizeModelID(id)
	prefixes := []struct{ prefix, vendor string }{
		{"gpt-", "openai"}, {"o1", "openai"}, {"o3", "openai"}, {"o4", "openai"},
		{"text-embedding", "openai"}, {"dall-e", "openai"}, {"chatgpt", "openai"},
		{"claude", "anthropic"}, {"gemini", "google"}, {"gemma", "google"},
		{"mistral", "mistral"}, {"mixtral", "mistral"}, {"codestral", "mistral"},
		{"ministral", "mistral"}, {"llama", "meta"}, {"deepseek", "deepseek"}, {"qwen", "qwen"},
	}
	for _, p := range prefixes {
		if strings.HasPrefix(base, p.prefix) {
			return p.vendor
		}
	}
	return ""
}

// index builds the base id index once.
func (c *PriceCatalog) index() {
	c.once.Do(func() {
		c.byBase = make(map[string][]indexedEntry, len(c.Entries))
		for i := range c.Entries {
			e := &c.Entries[i]
			id := parseModelID(e.ID)
			c.byBase[id.base] = append(c.byBase[id.base], indexedEntry{entry: e, id: id})
		}
	})
}

// Match finds the entry for a model id and vendor. Candidates share the
// normalised base id. A vendor, or else the vendor prefix of the id, keeps
// only candidates of that vendor. Without either, a match needs every
// candidate to share one vendor. The ranking prefers an exact id, then a
// paid entry over a :free one, then an untagged entry, then an undated one,
// then the lowest id.
func (c *PriceCatalog) Match(modelID, vendor string) (*CatalogEntry, bool) {
	if c == nil || strings.TrimSpace(modelID) == "" {
		return nil, false
	}
	c.index()
	want := parseModelID(modelID)
	vendor = CanonicalVendor(vendor)
	if vendor == "" {
		vendor = want.vendor
	}
	var cands []indexedEntry
	for _, e := range c.byBase[want.base] {
		if vendor == "" || CanonicalVendor(e.entry.Vendor) == vendor {
			cands = append(cands, e)
		}
	}
	if len(cands) == 0 {
		return nil, false
	}
	if vendor == "" {
		first := CanonicalVendor(cands[0].entry.Vendor)
		for _, e := range cands[1:] {
			if CanonicalVendor(e.entry.Vendor) != first {
				return nil, false
			}
		}
	}
	rank := func(e indexedEntry) [4]bool {
		return [4]bool{e.id.raw != want.raw, e.id.tag == "free", e.id.hasTag, e.id.dated}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		ri, rj := rank(cands[i]), rank(cands[j])
		for k := range ri {
			if ri[k] != rj[k] {
				return !ri[k]
			}
		}
		return cands[i].entry.ID < cands[j].entry.ID
	})
	return cands[0].entry, true
}

// ModelVendor returns the vendor used to match a model of a provider: the
// vendor prefix of the id, else the vendor of the provider's preset, else
// an empty string.
func ModelVendor(cfg *core.AIProviderConfig, modelID string) string {
	if v, _ := NormalizeModelID(modelID); v != "" {
		return v
	}
	if cfg != nil && cfg.Preset != "" {
		if p, ok := PresetByID(cfg.Preset); ok {
			return CanonicalVendor(p.Vendor)
		}
	}
	return ""
}

// match looks a model up in the OpenRouter catalog, then in LiteLLM. keep
// filters the entries that count as a hit.
func (cats Catalogs) match(cfg *core.AIProviderConfig, modelID string, keep func(*CatalogEntry) bool) (*CatalogEntry, string, time.Time) {
	vendor := ModelVendor(cfg, modelID)
	for _, c := range []struct {
		cat    *PriceCatalog
		source string
	}{{cats.OpenRouter, core.PriceSourceOpenRouter}, {cats.LiteLLM, core.PriceSourceLiteLLM}} {
		if c.cat == nil {
			continue
		}
		if e, ok := c.cat.Match(modelID, vendor); ok && keep(e) {
			return e, c.source, c.cat.FetchedAt
		}
	}
	return nil, "", time.Time{}
}

// EnrichModels returns a copy of the listed models with the vendor and the
// capabilities filled in. A model whose listing described no inputs and
// outputs takes them from a catalog entry, OpenRouter first. A model
// without a context length takes it from a catalog entry. Without any
// capabilities an id containing "embed" produces embeddings, and a provider
// with the vision flag accepts images.
func EnrichModels(cfg *core.AIProviderConfig, models []core.AIModel, cats Catalogs) []core.AIModel {
	out := make([]core.AIModel, len(models))
	comfy := cfg != nil && cfg.Kind == core.ProviderComfyUI
	for i, m := range models {
		m.Inputs = append([]string(nil), m.Inputs...)
		m.Outputs = append([]string(nil), m.Outputs...)
		if v := ModelVendor(cfg, m.ModelID); v != "" {
			m.Vendor = v
		} else if m.Vendor == "" {
			m.Vendor = inferVendor(m.ModelID)
		}
		if !comfy {
			if !m.CapsListed {
				if e, _, _ := cats.match(cfg, m.ModelID, func(e *CatalogEntry) bool { return e.HasCaps }); e != nil {
					m.Inputs = append([]string(nil), e.Inputs...)
					m.Outputs = append([]string(nil), e.Outputs...)
					m.Tools = m.Tools || e.Tools
					m.CapsListed = true
				}
			}
			if m.ContextLength == 0 || m.MaxOutput == 0 {
				if e, _, _ := cats.match(cfg, m.ModelID, func(e *CatalogEntry) bool { return e.ContextLength > 0 || e.MaxOutput > 0 }); e != nil {
					if m.ContextLength == 0 {
						m.ContextLength = e.ContextLength
					}
					if m.MaxOutput == 0 {
						m.MaxOutput = e.MaxOutput
					}
				}
			}
			if !m.CapsListed {
				if strings.Contains(strings.ToLower(m.ModelID), "embed") {
					m.Outputs = []string{core.ModalityEmbedding}
				}
				if len(m.Outputs) == 0 {
					m.Outputs = []string{core.ModalityText}
				}
				if len(m.Inputs) == 0 {
					m.Inputs = []string{core.ModalityText}
				}
				if cfg != nil && cfg.Vision && !m.HasInput(core.ModalityImage) {
					m.Inputs = append(m.Inputs, core.ModalityImage)
				}
			}
		}
		if len(m.Inputs) == 0 {
			m.Inputs = []string{core.ModalityText}
		}
		if len(m.Outputs) == 0 {
			m.Outputs = []string{core.ModalityText}
		}
		out[i] = m
	}
	return out
}

// PriceModel sets the fetched price of one model. The price is the first of:
// the price in the provider's own listing, 0 for a local provider, the
// OpenRouter catalog, the LiteLLM catalog, and none. The admin override is a
// separate field and stays unchanged.
func PriceModel(cfg *core.AIProviderConfig, m *core.AIModel, cats Catalogs, now time.Time) {
	if m == nil {
		return
	}
	if m.PriceSource == core.PriceSourceProvider && m.Price != nil {
		m.PriceMatched = ""
		if m.PriceFetchedAt.IsZero() {
			m.PriceFetchedAt = now
		}
		return
	}
	if cfg != nil && cfg.Local {
		m.Price, m.PriceSource, m.PriceMatched, m.PriceFetchedAt = &core.AIPrice{}, core.PriceSourceLocal, "", now
		return
	}
	if catalogPriced(cfg, m.ModelID) {
		e, source, fetched := cats.match(cfg, m.ModelID, func(e *CatalogEntry) bool { return e.Price != nil })
		if e != nil {
			p := *e.Price
			if fetched.IsZero() {
				fetched = now
			}
			m.Price, m.PriceSource, m.PriceMatched, m.PriceFetchedAt = &p, source, e.ID, fetched
			return
		}
	}
	m.Price, m.PriceSource, m.PriceMatched, m.PriceFetchedAt = nil, core.PriceSourceNone, "", time.Time{}
}

// catalogPriced reports whether a model may take a public catalog price. A
// comfyui provider never does. A provider whose preset reports its own prices,
// such as OpenRouter, does not, because a model it lists without a price has a
// variable price. An openrouter/ id names a router, not a model, so it has no
// catalog price either.
func catalogPriced(cfg *core.AIProviderConfig, modelID string) bool {
	if vendor, _ := NormalizeModelID(modelID); vendor == "openrouter" {
		return false
	}
	if cfg == nil {
		return true
	}
	if cfg.Kind == core.ProviderComfyUI {
		return false
	}
	if p, ok := PresetByID(cfg.Preset); ok && p.PriceSource == core.PresetPricesProvider {
		return false
	}
	return true
}

// perMillion converts a price per token, written as a decimal string, to a
// price per million tokens. It uses exact rational arithmetic, so
// "0.0000025" becomes exactly 2.5. A negative or unreadable price is unknown.
func perMillion(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() < 0 {
		return 0, false
	}
	f, _ := r.Mul(r, big.NewRat(1_000_000, 1)).Float64()
	return f, true
}

// plainAmount converts a price per image or per request, written as a
// decimal string, to a float. A negative or unreadable price is unknown.
func plainAmount(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() < 0 {
		return 0, false
	}
	f, _ := r.Float64()
	return f, true
}

// flexNumber holds a JSON string or number as its decimal text. Any other
// JSON value leaves it empty.
type flexNumber string

// UnmarshalJSON accepts a string or a number and ignores other values.
func (n *flexNumber) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*n = flexNumber(s)
		return nil
	}
	var num json.Number
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	if err := dec.Decode(&num); err == nil {
		*n = flexNumber(num.String())
		return nil
	}
	*n = ""
	return nil
}

// flexInt holds a JSON number as an int. Any other value is 0.
type flexInt int

// UnmarshalJSON accepts a number or a numeric string and ignores other values.
func (n *flexInt) UnmarshalJSON(b []byte) error {
	var f flexNumber
	_ = f.UnmarshalJSON(b)
	r, ok := new(big.Rat).SetString(string(f))
	if !ok || r.Sign() < 0 {
		*n = 0
		return nil
	}
	v, _ := r.Float64()
	if v > 1<<40 {
		v = 1 << 40
	}
	*n = flexInt(int(v))
	return nil
}

// flexBool holds a JSON boolean. Any other value is false.
type flexBool bool

// UnmarshalJSON accepts a boolean and ignores other values.
func (b *flexBool) UnmarshalJSON(raw []byte) error {
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		*b = false
		return nil
	}
	*b = flexBool(v)
	return nil
}

// openRouterModel is one model in the OpenRouter listing format, which
// OpenRouter and some gateways return from GET /models.
type openRouterModel struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	ContextLength flexInt `json:"context_length"`
	Architecture  *struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	SupportedParameters []string              `json:"supported_parameters"`
	Pricing             map[string]flexNumber `json:"pricing"`
	TopProvider         *struct {
		ContextLength       flexInt `json:"context_length"`
		MaxCompletionTokens flexInt `json:"max_completion_tokens"`
	} `json:"top_provider"`
}

// openRouterPrice converts OpenRouter pricing strings, in dollars per token.
// A missing, negative or unreadable prompt or completion price, such as the
// "-1" of variable pricing, means the price is unknown.
func openRouterPrice(pricing map[string]flexNumber) *core.AIPrice {
	if pricing == nil {
		return nil
	}
	in, okIn := perMillion(string(pricing["prompt"]))
	out, okOut := perMillion(string(pricing["completion"]))
	if !okIn || !okOut {
		return nil
	}
	p := &core.AIPrice{InputPerMTok: in, OutputPerMTok: out}
	if v, ok := perMillion(string(pricing["input_cache_read"])); ok {
		p.CacheReadPerMTok = v
	}
	if v, ok := perMillion(string(pricing["input_cache_write"])); ok {
		p.CacheWritePerMTok = v
	}
	if v, ok := plainAmount(string(pricing["image"])); ok {
		p.PerImageIn = v
	}
	if v, ok := plainAmount(string(pricing["request"])); ok {
		p.PerRequest = v
	}
	return p
}

// mapModalities keeps the known modalities of a listing. output selects the
// output names, where "embeddings" means embedding.
func mapModalities(in []string, output bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		switch v {
		case "embeddings":
			v = core.ModalityEmbedding
		case core.ModalityText, core.ModalityImage, core.ModalityFile, core.ModalityAudio:
		case core.ModalityEmbedding:
		default:
			continue
		}
		if !output && v == core.ModalityEmbedding {
			continue
		}
		if output && (v == core.ModalityFile || v == core.ModalityAudio) {
			continue
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// entry converts one OpenRouter model to a catalog entry.
func (m openRouterModel) entry() CatalogEntry {
	e := CatalogEntry{
		ID:            m.ID,
		Name:          m.Name,
		Price:         openRouterPrice(m.Pricing),
		ContextLength: int(m.ContextLength),
	}
	e.Vendor, _ = NormalizeModelID(m.ID)
	if m.TopProvider != nil {
		if e.ContextLength == 0 {
			e.ContextLength = int(m.TopProvider.ContextLength)
		}
		e.MaxOutput = int(m.TopProvider.MaxCompletionTokens)
	}
	if m.Architecture != nil && len(m.Architecture.OutputModalities) > 0 {
		e.Inputs = mapModalities(m.Architecture.InputModalities, false)
		e.Outputs = mapModalities(m.Architecture.OutputModalities, true)
		if len(e.Inputs) == 0 {
			e.Inputs = []string{core.ModalityText}
		}
		if len(e.Outputs) == 0 {
			e.Outputs = []string{core.ModalityText}
		}
		e.HasCaps = true
	}
	for _, p := range m.SupportedParameters {
		if p == "tools" {
			e.Tools = true
		}
	}
	return e
}

// ParseOpenRouterCatalog parses GET https://openrouter.ai/api/v1/models. The
// vendor of an entry is its id prefix. A body that is not JSON or has no
// data array is an error.
func ParseOpenRouterCatalog(body []byte) (*PriceCatalog, error) {
	var doc struct {
		Data *[]openRouterModel `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("openrouter catalog: %w", err)
	}
	if doc.Data == nil {
		return nil, errors.New("openrouter catalog: no data array")
	}
	c := &PriceCatalog{Source: core.PriceSourceOpenRouter}
	for _, m := range *doc.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		c.Entries = append(c.Entries, m.entry())
	}
	return c, nil
}

// liteLLMModel is one entry of model_prices_and_context_window.json. Every
// field tolerates a value of the wrong type by reading it as absent.
type liteLLMModel struct {
	Provider               flexNumber `json:"litellm_provider"`
	Mode                   flexNumber `json:"mode"`
	MaxInputTokens         flexInt    `json:"max_input_tokens"`
	MaxTokens              flexInt    `json:"max_tokens"`
	MaxOutputTokens        flexInt    `json:"max_output_tokens"`
	SupportsVision         flexBool   `json:"supports_vision"`
	SupportsFunctionCall   flexBool   `json:"supports_function_calling"`
	InputCostPerToken      flexNumber `json:"input_cost_per_token"`
	OutputCostPerToken     flexNumber `json:"output_cost_per_token"`
	CacheReadCostPerToken  flexNumber `json:"cache_read_input_token_cost"`
	CacheWriteCostPerToken flexNumber `json:"cache_creation_input_token_cost"`
	InputCostPerImage      flexNumber `json:"input_cost_per_image"`
	OutputCostPerImage     flexNumber `json:"output_cost_per_image"`
	InputCostPerRequest    flexNumber `json:"input_cost_per_request"`
}

// entry converts one LiteLLM model to a catalog entry.
func (m liteLLMModel) entry(key string) CatalogEntry {
	e := CatalogEntry{ID: key, Name: key}
	vendor, _ := NormalizeModelID(key)
	if p := CanonicalVendor(string(m.Provider)); p != "" {
		vendor = p
	}
	e.Vendor = vendor
	e.ContextLength = int(m.MaxInputTokens)
	if e.ContextLength == 0 {
		e.ContextLength = int(m.MaxTokens)
	}
	e.MaxOutput = int(m.MaxOutputTokens)
	e.Tools = bool(m.SupportsFunctionCall)
	switch string(m.Mode) {
	case "chat", "completion", "responses":
		e.Inputs = []string{core.ModalityText}
		e.Outputs = []string{core.ModalityText}
		e.HasCaps = true
	case "embedding":
		e.Inputs = []string{core.ModalityText}
		e.Outputs = []string{core.ModalityEmbedding}
		e.HasCaps = true
	case "image_generation":
		e.Inputs = []string{core.ModalityText}
		e.Outputs = []string{core.ModalityImage}
		e.HasCaps = true
	}
	if e.HasCaps && bool(m.SupportsVision) {
		e.Inputs = append(e.Inputs, core.ModalityImage)
	}
	var p core.AIPrice
	found := false
	set := func(dst *float64, v float64, ok bool) {
		if ok {
			*dst = v
			found = true
		}
	}
	v, ok := perMillion(string(m.InputCostPerToken))
	set(&p.InputPerMTok, v, ok)
	v, ok = perMillion(string(m.OutputCostPerToken))
	set(&p.OutputPerMTok, v, ok)
	v, ok = plainAmount(string(m.InputCostPerImage))
	set(&p.PerImageIn, v, ok)
	v, ok = plainAmount(string(m.OutputCostPerImage))
	set(&p.PerImageOut, v, ok)
	if found {
		if v, ok := perMillion(string(m.CacheReadCostPerToken)); ok {
			p.CacheReadPerMTok = v
		}
		if v, ok := perMillion(string(m.CacheWriteCostPerToken)); ok {
			p.CacheWritePerMTok = v
		}
		if v, ok := plainAmount(string(m.InputCostPerRequest)); ok {
			p.PerRequest = v
		}
		e.Price = &p
	}
	return e
}

// ParseLiteLLMCatalog parses model_prices_and_context_window.json, an object
// keyed by model name. The sample_spec entry and entries that are not
// objects are skipped. The vendor of an entry is its litellm_provider, else
// its key prefix.
func ParseLiteLLMCatalog(body []byte) (*PriceCatalog, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("litellm catalog: %w", err)
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	c := &PriceCatalog{Source: core.PriceSourceLiteLLM}
	for _, key := range keys {
		if key == "sample_spec" || strings.TrimSpace(key) == "" {
			continue
		}
		var m liteLLMModel
		if err := json.Unmarshal(doc[key], &m); err != nil {
			continue
		}
		c.Entries = append(c.Entries, m.entry(key))
	}
	return c, nil
}
