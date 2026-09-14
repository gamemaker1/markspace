package ai

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// loadCatalogs parses both catalog fixtures.
func loadCatalogs(t *testing.T) Catalogs {
	t.Helper()
	or, err := ParseOpenRouterCatalog(fixture(t, "openrouter-catalog.json"))
	if err != nil {
		t.Fatalf("openrouter catalog: %v", err)
	}
	ll, err := ParseLiteLLMCatalog(fixture(t, "litellm-catalog.json"))
	if err != nil {
		t.Fatalf("litellm catalog: %v", err)
	}
	return Catalogs{OpenRouter: or, LiteLLM: ll}
}

func TestNormalizeModelID(t *testing.T) {
	for _, tc := range []struct{ id, vendor, base string }{
		{"gpt-4o", "", "gpt-4o"},
		{"GPT-4o-2024-08-06", "", "gpt-4o"},
		{"claude-sonnet-4-5-20250929", "", "claude-sonnet-4-5"},
		{"anthropic/claude-sonnet-4.5", "anthropic", "claude-sonnet-4-5"},
		{"meta-llama/llama-3.3-70b-instruct:free", "meta", "llama-3-3-70b-instruct"},
		{"llama3.2:latest", "", "llama3-2"},
		{"models/gemini-2.0-flash", "", "gemini-2-0-flash"},
		{"openrouter/openai/gpt-4o", "openrouter", "gpt-4o"},
		{"together_ai/meta-llama/Llama-3.3-70B-Instruct-Turbo", "together", "llama-3-3-70b-instruct-turbo"},
		{"claude-3-5-sonnet@20240620", "", "claude-3-5-sonnet"},
		{"mistralai/mistral-large-2411", "mistral", "mistral-large-2411"},
		{"gpt-3.5-turbo", "", "gpt-3-5-turbo"},
		{"x-ai/grok-3", "xai", "grok-3"},
		{"vertex_ai-language-models/gemini-pro", "google", "gemini-pro"},
	} {
		v, b := NormalizeModelID(tc.id)
		if v != tc.vendor || b != tc.base {
			t.Errorf("NormalizeModelID(%q) = %q, %q, want %q, %q", tc.id, v, b, tc.vendor, tc.base)
		}
	}
}

func TestModelMatchVectors(t *testing.T) {
	cats := loadCatalogs(t)
	var vectors []struct {
		Model, Vendor, Catalog, Want, Why string
	}
	if err := json.Unmarshal(fixture(t, "model-match.json"), &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 20 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	for _, v := range vectors {
		cat := cats.OpenRouter
		if v.Catalog == "litellm" {
			cat = cats.LiteLLM
		}
		got := ""
		if e, ok := cat.Match(v.Model, v.Vendor); ok {
			got = e.ID
		}
		if got != v.Want {
			t.Errorf("%s: Match(%q, %q) in %s = %q, want %q", v.Why, v.Model, v.Vendor, v.Catalog, got, v.Want)
		}
	}
}

func TestMatchNeverConfusesMini(t *testing.T) {
	cat := &PriceCatalog{Entries: []CatalogEntry{{ID: "openai/gpt-4o-mini", Vendor: "openai"}}}
	if e, ok := cat.Match("gpt-4o", "openai"); ok {
		t.Fatalf("gpt-4o matched %s", e.ID)
	}
	cat = &PriceCatalog{Entries: []CatalogEntry{{ID: "openai/gpt-4o", Vendor: "openai"}}}
	if e, ok := cat.Match("gpt-4o-mini", "openai"); ok {
		t.Fatalf("gpt-4o-mini matched %s", e.ID)
	}
	var nilCat *PriceCatalog
	if _, ok := nilCat.Match("gpt-4o", "openai"); ok {
		t.Fatal("a nil catalog matched")
	}
}

func TestMatchConcurrentFirstUse(t *testing.T) {
	cats := loadCatalogs(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e, ok := cats.OpenRouter.Match("gpt-4o", "openai"); !ok || e.ID != "openai/gpt-4o" {
				t.Errorf("concurrent match = %+v", e)
			}
		}()
	}
	wg.Wait()
}

func TestParseOpenRouterCatalog(t *testing.T) {
	cats := loadCatalogs(t)
	if n := len(cats.OpenRouter.Entries); n < 25 || cats.OpenRouter.Source != core.PriceSourceOpenRouter {
		t.Fatalf("openrouter entries = %d source %q", n, cats.OpenRouter.Source)
	}
	sonnet, ok := cats.OpenRouter.Match("anthropic/claude-sonnet-4.5", "")
	want := &core.AIPrice{InputPerMTok: 3, OutputPerMTok: 15, CacheReadPerMTok: 0.3, CacheWritePerMTok: 3.75}
	if !ok || !reflect.DeepEqual(sonnet.Price, want) || sonnet.Vendor != "anthropic" || !sonnet.HasCaps || sonnet.ContextLength != 1000000 {
		t.Fatalf("sonnet = %+v price %+v", sonnet, sonnet.Price)
	}
	auto, ok := cats.OpenRouter.Match("openrouter/auto", "")
	if !ok || auto.Price != nil {
		t.Fatalf("auto router = %+v", auto)
	}
	sonar, _ := cats.OpenRouter.Match("perplexity/sonar", "")
	if sonar.Price == nil || sonar.Price.PerRequest != 0.005 {
		t.Fatalf("sonar = %+v", sonar.Price)
	}
	img, _ := cats.OpenRouter.Match("google/gemini-2.5-flash-image-preview", "")
	if !reflect.DeepEqual(img.Outputs, []string{"image", "text"}) || img.Price.PerImageIn != 0.001238 {
		t.Fatalf("image model = %+v", img)
	}
	for _, bad := range []string{`{`, `{"models":[]}`, `[]`} {
		if _, err := ParseOpenRouterCatalog([]byte(bad)); err == nil {
			t.Errorf("ParseOpenRouterCatalog(%s) succeeded", bad)
		}
	}
}

func TestParseLiteLLMCatalog(t *testing.T) {
	cats := loadCatalogs(t)
	ll := cats.LiteLLM
	for _, e := range ll.Entries {
		if e.ID == "sample_spec" {
			t.Fatal("sample_spec was parsed")
		}
	}
	gpt, _ := ll.Match("gpt-4o", "openai")
	if !reflect.DeepEqual(gpt.Price, &core.AIPrice{InputPerMTok: 2.5, OutputPerMTok: 10, CacheReadPerMTok: 1.25}) ||
		!reflect.DeepEqual(gpt.Inputs, []string{"text", "image"}) || gpt.ContextLength != 128000 || gpt.MaxOutput != 16384 || !gpt.Tools {
		t.Fatalf("gpt-4o = %+v price %+v", gpt, gpt.Price)
	}
	if gpt.Price.InputPerMTok != 2.5 {
		t.Fatalf("2.5e-06 per token should be exactly 2.5, got %v", gpt.Price.InputPerMTok)
	}
	dalle, _ := ll.Match("dall-e-3", "openai")
	if dalle.Price == nil || dalle.Price.PerImageOut != 0.04 || !reflect.DeepEqual(dalle.Outputs, []string{"image"}) {
		t.Fatalf("dall-e-3 = %+v", dalle)
	}
	embed, _ := ll.Match("text-embedding-3-small", "openai")
	if !reflect.DeepEqual(embed.Outputs, []string{"embedding"}) || embed.Price.InputPerMTok != 0.02 {
		t.Fatalf("embedding = %+v %+v", embed, embed.Price)
	}
	groq, _ := ll.Match("llama-3.3-70b-versatile", "groq")
	if groq.Vendor != "groq" || groq.Price.InputPerMTok != 0.59 {
		t.Fatalf("groq = %+v %+v", groq, groq.Price)
	}
	odd, ok := ll.Match("odd-types-model", "openai")
	if !ok || odd.Price == nil || odd.Price.InputPerMTok != 0 || odd.Price.OutputPerMTok != 2 || odd.ContextLength != 0 || odd.HasInputImage() {
		t.Fatalf("odd types = %+v %+v", odd, odd.Price)
	}
	none, ok := ll.Match("no-price-model", "openai")
	if !ok || none.Price != nil {
		t.Fatalf("no price model = %+v", none)
	}
	if _, err := ParseLiteLLMCatalog([]byte(`[1,2]`)); err == nil {
		t.Fatal("array parsed as a litellm catalog")
	}
}

// HasInputImage reports whether the entry accepts images. It keeps the odd
// types assertion short.
func (e *CatalogEntry) HasInputImage() bool {
	for _, v := range e.Inputs {
		if v == core.ModalityImage {
			return true
		}
	}
	return false
}

func TestPerMillionExact(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float64
		ok   bool
	}{
		{"0.0000025", 2.5, true}, {"0.00001", 10, true}, {"2.5e-06", 2.5, true}, {"0", 0, true},
		{"0.00000015", 0.15, true}, {"-1", 0, false}, {"", 0, false}, {"abc", 0, false}, {"1e-7", 0.1, true},
	} {
		got, ok := perMillion(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("perMillion(%q) = %v %v, want %v %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestModelVendorAndEnrich(t *testing.T) {
	cats := loadCatalogs(t)
	openai := &core.AIProviderConfig{ID: "oa", Kind: core.ProviderOpenAI, Preset: "openai"}
	if v := ModelVendor(openai, "gpt-4o"); v != "openai" {
		t.Fatalf("preset vendor = %q", v)
	}
	if v := ModelVendor(&core.AIProviderConfig{Preset: "openrouter"}, "mistralai/mistral-large"); v != "mistral" {
		t.Fatalf("prefix vendor = %q", v)
	}
	if v := ModelVendor(&core.AIProviderConfig{Preset: "custom"}, "gpt-4o"); v != "" {
		t.Fatalf("custom vendor = %q", v)
	}
	if v := ModelVendor(nil, "gpt-4o"); v != "" {
		t.Fatalf("nil config vendor = %q", v)
	}

	listed := []core.AIModel{
		{ModelID: "gpt-4o", Inputs: []string{"text"}, Outputs: []string{"text"}},
		{ModelID: "text-embedding-3-small"},
		{ModelID: "my-embedder"},
		{ModelID: "local-thing"},
		{ModelID: "claude-sonnet-4-5-20250929", Inputs: []string{"text", "image"}, Outputs: []string{"text"}, CapsListed: true},
	}
	out := EnrichModels(openai, listed, cats)
	if &out[0] == &listed[0] || !reflect.DeepEqual(listed[0].Inputs, []string{"text"}) {
		t.Fatal("EnrichModels changed its input")
	}
	byID := modelByID(out)
	if g := byID["gpt-4o"]; !g.HasInput(core.ModalityImage) || g.ContextLength != 128000 || g.MaxOutput != 16384 || !g.Tools || g.Vendor != "openai" {
		t.Fatalf("enriched gpt-4o = %+v", g)
	}
	if e := byID["text-embedding-3-small"]; !reflect.DeepEqual(e.Outputs, []string{"embedding"}) {
		t.Fatalf("catalog embedding = %+v", e)
	}
	if e := byID["my-embedder"]; !reflect.DeepEqual(e.Outputs, []string{"embedding"}) || !reflect.DeepEqual(e.Inputs, []string{"text"}) {
		t.Fatalf("embed heuristic = %+v", e)
	}

	custom := &core.AIProviderConfig{ID: "c", Kind: core.ProviderOpenAI, Vision: true}
	out = EnrichModels(custom, []core.AIModel{{ModelID: "local-thing"}, {ModelID: "claude-3-5-haiku-20241022"}, {ModelID: "anthropic/claude-sonnet-4.5", CapsListed: true, Inputs: []string{"text"}, Outputs: []string{"text"}}}, cats)
	byID = modelByID(out)
	if l := byID["local-thing"]; !reflect.DeepEqual(l.Inputs, []string{"text", "image"}) || !reflect.DeepEqual(l.Outputs, []string{"text"}) || l.Vendor != "" {
		t.Fatalf("vision flag = %+v", l)
	}
	if h := byID["claude-3-5-haiku-20241022"]; h.Vendor != "anthropic" || h.ContextLength != 200000 || !h.HasInput(core.ModalityImage) {
		t.Fatalf("without a vendor a match needs one vendor among candidates: %+v", h)
	}
	if s := byID["anthropic/claude-sonnet-4.5"]; s.ContextLength != 1000000 || s.HasInput(core.ModalityImage) {
		t.Fatalf("listed caps stay and context fills: %+v", s)
	}

	anth := &core.AIProviderConfig{ID: "an", Kind: core.ProviderAnthropic, Preset: "anthropic"}
	out = EnrichModels(anth, []core.AIModel{listed[4]}, cats)
	if out[0].ContextLength != 1000000 || out[0].MaxOutput != 64000 {
		t.Fatalf("anthropic context from the catalog = %+v", out[0])
	}

	comfy := &core.AIProviderConfig{ID: "cf", Kind: core.ProviderComfyUI}
	out = EnrichModels(comfy, []core.AIModel{{ModelID: "gpt-4o-image.safetensors", Outputs: []string{"image"}, CapsListed: true}}, cats)
	if !reflect.DeepEqual(out[0].Outputs, []string{"image"}) || out[0].ContextLength != 0 {
		t.Fatalf("comfy = %+v", out[0])
	}
}

// TestPriceModelPrecedence walks every combination of override, provider
// listing price, local flag, OpenRouter hit and LiteLLM hit.
func TestPriceModelPrecedence(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	orFetched := now.Add(-2 * time.Hour)
	orPrice := &core.AIPrice{InputPerMTok: 1, OutputPerMTok: 2}
	llPrice := &core.AIPrice{InputPerMTok: 3, OutputPerMTok: 4}
	listedPrice := &core.AIPrice{InputPerMTok: 5, OutputPerMTok: 6}
	override := &core.AIPrice{InputPerMTok: 7, OutputPerMTok: 8}
	or := &PriceCatalog{Source: "openrouter", FetchedAt: orFetched, Entries: []CatalogEntry{
		{ID: "acme/hit-both", Vendor: "acme", Price: orPrice},
		{ID: "acme/hit-or", Vendor: "acme", Price: orPrice},
		{ID: "acme/variable", Vendor: "acme"},
	}}
	ll := &PriceCatalog{Source: "litellm", Entries: []CatalogEntry{
		{ID: "acme/hit-both", Vendor: "acme", Price: llPrice},
		{ID: "acme/hit-ll", Vendor: "acme", Price: llPrice},
		{ID: "acme/variable", Vendor: "acme", Price: llPrice},
	}}
	for _, hasOverride := range []bool{false, true} {
		for _, listed := range []bool{false, true} {
			for _, local := range []bool{false, true} {
				for _, model := range []string{"hit-both", "hit-or", "hit-ll", "variable", "miss"} {
					name := fmt.Sprintf("override=%v listed=%v local=%v model=%s", hasOverride, listed, local, model)
					cfg := &core.AIProviderConfig{ID: "p", Kind: core.ProviderOpenAI, Local: local}
					m := &core.AIModel{ModelID: "acme/" + model}
					if listed {
						p := *listedPrice
						m.Price, m.PriceSource = &p, core.PriceSourceProvider
					}
					if hasOverride {
						m.PriceOverride = override
					}
					PriceModel(cfg, m, Catalogs{OpenRouter: or, LiteLLM: ll}, now)

					wantPrice, wantSource, wantMatched := (*core.AIPrice)(nil), core.PriceSourceNone, ""
					switch {
					case listed:
						wantPrice, wantSource = listedPrice, core.PriceSourceProvider
					case local:
						wantPrice, wantSource = &core.AIPrice{}, core.PriceSourceLocal
					case model == "hit-both" || model == "hit-or":
						wantPrice, wantSource, wantMatched = orPrice, core.PriceSourceOpenRouter, "acme/"+model
					case model == "hit-ll" || model == "variable":
						wantPrice, wantSource, wantMatched = llPrice, core.PriceSourceLiteLLM, "acme/"+model
					}
					if !reflect.DeepEqual(m.Price, wantPrice) || m.PriceSource != wantSource || m.PriceMatched != wantMatched {
						t.Errorf("%s: fetched = %+v %s %q, want %+v %s %q", name, m.Price, m.PriceSource, m.PriceMatched, wantPrice, wantSource, wantMatched)
					}
					if wantSource == core.PriceSourceOpenRouter && !m.PriceFetchedAt.Equal(orFetched) {
						t.Errorf("%s: fetched at %v, want the catalog time", name, m.PriceFetchedAt)
					}
					if wantSource == core.PriceSourceLiteLLM && !m.PriceFetchedAt.Equal(now) {
						t.Errorf("%s: fetched at %v, want now for a catalog without a time", name, m.PriceFetchedAt)
					}
					if !reflect.DeepEqual(m.PriceOverride, map[bool]*core.AIPrice{true: override}[hasOverride]) {
						t.Errorf("%s: the override changed", name)
					}
					effPrice, effSource := m.Effective()
					if hasOverride {
						wantPrice, wantSource = override, core.PriceSourceOverride
					}
					if !reflect.DeepEqual(effPrice, wantPrice) || effSource != wantSource {
						t.Errorf("%s: effective = %+v %s, want %+v %s", name, effPrice, effSource, wantPrice, wantSource)
					}
				}
			}
		}
	}

	// A stale catalog price is replaced when the catalogs no longer have it,
	// and a comfyui provider never takes catalog prices.
	m := &core.AIModel{ModelID: "acme/hit-or", Price: orPrice, PriceSource: core.PriceSourceOpenRouter, PriceMatched: "acme/hit-or"}
	PriceModel(&core.AIProviderConfig{Kind: core.ProviderOpenAI}, m, Catalogs{}, now)
	if m.Price != nil || m.PriceSource != core.PriceSourceNone || m.PriceMatched != "" {
		t.Fatalf("stale price kept: %+v", m)
	}
	m = &core.AIModel{ModelID: "acme/hit-or"}
	PriceModel(&core.AIProviderConfig{Kind: core.ProviderComfyUI}, m, Catalogs{OpenRouter: or}, now)
	if m.PriceSource != core.PriceSourceNone {
		t.Fatalf("comfy took a catalog price: %+v", m)
	}
	PriceModel(nil, nil, Catalogs{}, now)
}

func TestPriceModelWithFixtures(t *testing.T) {
	cats := loadCatalogs(t)
	now := time.Now()
	for _, tc := range []struct {
		cfg     *core.AIProviderConfig
		model   string
		source  string
		matched string
		input   float64
	}{
		{&core.AIProviderConfig{Kind: core.ProviderOpenAI, Preset: "openai"}, "gpt-4o-mini", core.PriceSourceOpenRouter, "openai/gpt-4o-mini", 0.15},
		{&core.AIProviderConfig{Kind: core.ProviderAnthropic, Preset: "anthropic"}, "claude-sonnet-4-5-20250929", core.PriceSourceOpenRouter, "anthropic/claude-sonnet-4.5", 3},
		{&core.AIProviderConfig{Kind: core.ProviderOpenAI, Preset: "groq"}, "llama-3.3-70b-versatile", core.PriceSourceLiteLLM, "groq/llama-3.3-70b-versatile", 0.59},
		{&core.AIProviderConfig{Kind: core.ProviderOpenAI, Preset: "openai"}, "dall-e-3", core.PriceSourceLiteLLM, "dall-e-3", 0},
		{&core.AIProviderConfig{Kind: core.ProviderOpenAI, Preset: "custom"}, "gpt-4o", core.PriceSourceOpenRouter, "openai/gpt-4o", 2.5},
		{&core.AIProviderConfig{Kind: core.ProviderOllama, Preset: "ollama", Local: true}, "llama3.2:latest", core.PriceSourceLocal, "", 0},
		{&core.AIProviderConfig{Kind: core.ProviderOpenAI, Preset: "openai"}, "gpt-5-unknown", core.PriceSourceNone, "", 0},
	} {
		m := &core.AIModel{ModelID: tc.model}
		PriceModel(tc.cfg, m, cats, now)
		gotInput := 0.0
		if m.Price != nil {
			gotInput = m.Price.InputPerMTok
		}
		if m.PriceSource != tc.source || m.PriceMatched != tc.matched || gotInput != tc.input {
			t.Errorf("%s via %s = %s %q %v, want %s %q %v", tc.model, tc.cfg.Preset, m.PriceSource, m.PriceMatched, gotInput, tc.source, tc.matched, tc.input)
		}
	}
}

// TestVariablePricesStayUnknown checks that a model a provider lists at "-1"
// does not take a catalog price. The OpenRouter preset reports its own prices,
// and an openrouter/ router id has no catalog price under any preset.
func TestVariablePricesStayUnknown(t *testing.T) {
	cats := loadCatalogs(t)
	now := time.Now()
	for name, cfg := range map[string]*core.AIProviderConfig{
		"openrouter preset": {ID: "or", Kind: core.ProviderOpenAI, Preset: "openrouter"},
		"custom preset":     {ID: "gw", Kind: core.ProviderOpenAI, Preset: "custom"},
	} {
		auto := &core.AIModel{ModelID: "openrouter/auto"}
		PriceModel(cfg, auto, cats, now)
		if auto.Price != nil || auto.PriceSource != core.PriceSourceNone {
			t.Errorf("%s: openrouter/auto = %+v from %s", name, auto.Price, auto.PriceSource)
		}
	}
	listed := &core.AIModel{ModelID: "openai/gpt-4o"}
	PriceModel(&core.AIProviderConfig{ID: "or", Kind: core.ProviderOpenAI, Preset: "openrouter"}, listed, cats, now)
	if listed.Price != nil {
		t.Errorf("an OpenRouter model without a listed price took %+v from %s", listed.Price, listed.PriceSource)
	}
	custom := &core.AIModel{ModelID: "openai/gpt-4o"}
	PriceModel(&core.AIProviderConfig{ID: "gw", Kind: core.ProviderOpenAI, Preset: "custom"}, custom, cats, now)
	if custom.Price == nil || custom.PriceSource != core.PriceSourceOpenRouter {
		t.Errorf("a custom gateway model = %+v from %s", custom.Price, custom.PriceSource)
	}
}
