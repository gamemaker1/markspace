package app

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/ai"
	"github.com/gamemaker1/markspace/internal/core"
)

// The fake catalog, route and price catalog tables live beside fakeDB in a
// map keyed by the fake, so the shared fake file needs no new fields.

type fakeCatalogState struct {
	models map[string]*core.AIModel
	routes map[string]map[string][]core.AIRouteTarget
	prices map[string]fakePriceCatalog
}

type fakePriceCatalog struct {
	body []byte
	at   time.Time
}

var (
	fakeCatalogMu sync.Mutex
	fakeCatalogs  = map[*fakeDB]*fakeCatalogState{}
)

// catalog returns the fake's tables. The caller holds fakeCatalogMu.
func (d *fakeDB) catalog() *fakeCatalogState {
	st := fakeCatalogs[d]
	if st == nil {
		st = &fakeCatalogState{
			models: map[string]*core.AIModel{},
			routes: map[string]map[string][]core.AIRouteTarget{},
			prices: map[string]fakePriceCatalog{},
		}
		fakeCatalogs[d] = st
	}
	return st
}

func copyModel(m *core.AIModel) *core.AIModel {
	cp := *m
	cp.Inputs = append([]string(nil), m.Inputs...)
	cp.Outputs = append([]string(nil), m.Outputs...)
	if m.Price != nil {
		p := *m.Price
		cp.Price = &p
	}
	if m.PriceOverride != nil {
		p := *m.PriceOverride
		cp.PriceOverride = &p
	}
	return &cp
}

func (d *fakeDB) ListAIModels(ctx context.Context, providerID string) ([]*core.AIModel, error) {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	var out []*core.AIModel
	for _, m := range d.catalog().models {
		if providerID == "" || m.ProviderID == providerID {
			out = append(out, copyModel(m))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out, nil
}

func (d *fakeDB) GetAIModel(ctx context.Context, providerID, modelID string) (*core.AIModel, error) {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	m, ok := d.catalog().models[ai.ModelKey(providerID, modelID)]
	if !ok {
		return nil, core.ErrNotFound
	}
	return copyModel(m), nil
}

func (d *fakeDB) UpsertAIModels(ctx context.Context, providerID string, models []*core.AIModel, seen time.Time) error {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	st := d.catalog()
	listed := map[string]bool{}
	for _, m := range models {
		key := ai.ModelKey(providerID, m.ModelID)
		listed[key] = true
		cp := copyModel(m)
		cp.ProviderID, cp.Available, cp.LastSeen = providerID, true, seen
		if old, ok := st.models[key]; ok {
			cp.Enabled, cp.PriceOverride = old.Enabled, old.PriceOverride
		}
		st.models[key] = cp
	}
	for key, m := range st.models {
		if m.ProviderID == providerID && !listed[key] {
			m.Available = false
		}
	}
	return nil
}

func (d *fakeDB) UpdateAIModelPrice(ctx context.Context, m *core.AIModel) error {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	row, ok := d.catalog().models[ai.ModelKey(m.ProviderID, m.ModelID)]
	if !ok {
		return core.ErrNotFound
	}
	cp := copyModel(m)
	row.Price, row.PriceSource, row.PriceMatched, row.PriceFetchedAt = cp.Price, cp.PriceSource, cp.PriceMatched, cp.PriceFetchedAt
	return nil
}

func (d *fakeDB) UpdateAIModelSettings(ctx context.Context, providerID, modelID string, enabled bool, override *core.AIPrice) error {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	row, ok := d.catalog().models[ai.ModelKey(providerID, modelID)]
	if !ok {
		return core.ErrNotFound
	}
	row.Enabled = enabled
	row.PriceOverride = nil
	if override != nil {
		p := *override
		row.PriceOverride = &p
	}
	return nil
}

func (d *fakeDB) DeleteAIModels(ctx context.Context, providerID string) error {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	st := d.catalog()
	for key, m := range st.models {
		if m.ProviderID == providerID {
			delete(st.models, key)
		}
	}
	return nil
}

func (d *fakeDB) ListAIRoutes(ctx context.Context, scope string) ([]core.AIRoute, error) {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	var out []core.AIRoute
	for task, targets := range d.catalog().routes[scope] {
		out = append(out, core.AIRoute{Task: task, Targets: append([]core.AIRouteTarget{}, targets...)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Task < out[j].Task })
	return out, nil
}

func (d *fakeDB) SaveAIRoute(ctx context.Context, scope string, r core.AIRoute) error {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	st := d.catalog()
	if st.routes[scope] == nil {
		st.routes[scope] = map[string][]core.AIRouteTarget{}
	}
	st.routes[scope][r.Task] = append([]core.AIRouteTarget{}, r.Targets...)
	return nil
}

func (d *fakeDB) DeleteAIRoute(ctx context.Context, scope, task string) error {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	st := d.catalog()
	if _, ok := st.routes[scope][task]; !ok {
		return core.ErrNotFound
	}
	delete(st.routes[scope], task)
	return nil
}

func (d *fakeDB) GetPriceCatalog(ctx context.Context, source string) ([]byte, time.Time, error) {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	c, ok := d.catalog().prices[source]
	if !ok {
		return nil, time.Time{}, core.ErrNotFound
	}
	return append([]byte(nil), c.body...), c.at, nil
}

func (d *fakeDB) SavePriceCatalog(ctx context.Context, source string, body []byte, fetchedAt time.Time) error {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	d.catalog().prices[source] = fakePriceCatalog{body: append([]byte(nil), body...), at: fetchedAt}
	return nil
}

// fakeAIExtras replaces the listing and image behaviour of a fakeAI.
type fakeAIExtras struct {
	catalog func(cfg *core.AIProviderConfig) ([]core.AIModel, error)
}

var fakeAIExtra = map[*fakeAI]*fakeAIExtras{}

// setListing makes ListCatalog return the result of fn.
func setListing(f *fakeAI, fn func(cfg *core.AIProviderConfig) ([]core.AIModel, error)) {
	fakeCatalogMu.Lock()
	defer fakeCatalogMu.Unlock()
	fakeAIExtra[f] = &fakeAIExtras{catalog: fn}
}

// ListCatalog lists the configured chat and embedding models unless a test
// set a listing.
func (f *fakeAI) ListCatalog(ctx context.Context, cfg *core.AIProviderConfig) ([]core.AIModel, error) {
	fakeCatalogMu.Lock()
	extra := fakeAIExtra[f]
	fakeCatalogMu.Unlock()
	if extra != nil && extra.catalog != nil {
		return extra.catalog(cfg)
	}
	var out []core.AIModel
	if cfg.Model != "" {
		out = append(out, core.AIModel{ModelID: cfg.Model, Inputs: []string{"text"}, Outputs: []string{"text"}, CapsListed: true})
	}
	if cfg.EmbedModel != "" {
		out = append(out, core.AIModel{ModelID: cfg.EmbedModel, Inputs: []string{"text"}, Outputs: []string{"embedding"}, CapsListed: true})
	}
	return out, nil
}

// fakeImageGen is an image generator that returns one empty PNG.
type fakeImageGen struct{ cfg *core.AIProviderConfig }

func (g fakeImageGen) GenerateImages(ctx context.Context, req core.AIImageRequest, progress func(done, total int)) (*core.AIImageResult, error) {
	return &core.AIImageResult{Model: req.Model, Images: []core.AIImage{{MediaType: "image/png"}}}, nil
}

// ImageGenerator follows the registry rules: openai and comfyui only, and
// the flag for the call.
func (f *fakeAI) ImageGenerator(cfg *core.AIProviderConfig, edit bool) (core.ImageGenerator, error) {
	if cfg.Kind != core.ProviderOpenAI && cfg.Kind != core.ProviderComfyUI {
		return nil, fmt.Errorf("fake: %s has no images: %w", cfg.Kind, core.ErrUnsupported)
	}
	if (edit && !cfg.ImageEditing) || (!edit && !cfg.ImageGeneration) {
		return nil, fmt.Errorf("fake: flag missing: %w", core.ErrUnsupported)
	}
	return fakeImageGen{cfg: cfg}, nil
}

// fakeFetcher serves price catalog bodies by URL, or fails.
type fakeFetcher struct {
	mu     sync.Mutex
	bodies map[string][]byte
	err    error
	calls  int
}

func (f *fakeFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	body, ok := f.bodies[url]
	if !ok {
		return nil, fmt.Errorf("fake fetcher: no body for %s: %w", url, core.ErrNotFound)
	}
	return body, nil
}

// setModelPrice stores a catalog row with a price override, which every
// call to the model is then charged with.
func setModelPrice(h *harness, providerID, model string, price core.AIPrice) {
	h.t.Helper()
	ctx := context.Background()
	existing, _ := h.db.ListAIModels(ctx, providerID)
	rows := []*core.AIModel{}
	found := false
	for _, m := range existing {
		if m.Available {
			rows = append(rows, m)
		}
		found = found || m.ModelID == model
	}
	if !found {
		rows = append(rows, &core.AIModel{ProviderID: providerID, ModelID: model, Inputs: []string{"text"},
			Outputs: []string{"text"}, Enabled: true, PriceSource: core.PriceSourceNone})
	}
	if err := h.db.UpsertAIModels(ctx, providerID, rows, h.clock.Now()); err != nil {
		h.t.Fatal(err)
	}
	if !found {
		if err := h.db.UpdateAIModelSettings(ctx, providerID, model, true, &price); err != nil {
			h.t.Fatal(err)
		}
		return
	}
	if err := h.db.UpdateAIModelSettings(ctx, providerID, model, true, &price); err != nil {
		h.t.Fatal(err)
	}
}
