package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/ai"
	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// providerDownFor is how long routing skips a provider after a
	// connection error, a timeout or a 5xx.
	providerDownFor = 60 * time.Second
	// catalogInterval is how often the provider listings and the public price
	// catalogs refresh.
	catalogInterval = 24 * time.Hour
	// saveListingTimeout bounds the listing that runs when a provider is saved.
	saveListingTimeout = 20 * time.Second
	// jobListingTimeout bounds one provider listing of the daily job or of a
	// refresh an admin asked for.
	jobListingTimeout = 2 * time.Minute
)

// catalogSource is one public price catalog.
type catalogSource struct {
	name  string
	url   string
	parse func([]byte) (*ai.PriceCatalog, error)
}

// catalogSources lists the public price catalogs in price precedence order.
var catalogSources = []catalogSource{
	{core.PriceSourceOpenRouter, ai.OpenRouterCatalogURL, ai.ParseOpenRouterCatalog},
	{core.PriceSourceLiteLLM, ai.LiteLLMCatalogURL, ai.ParseLiteLLMCatalog},
}

// aiState is the routing and catalog state kept in memory.
type aiState struct {
	mu sync.Mutex
	// down maps a provider id to the end of its skip window.
	down map[string]time.Time
	// refreshed maps a provider id to its last listing.
	refreshed map[string]providerRefresh
	// catalogs are the parsed public catalogs, loaded from the database on
	// first use.
	catalogs ai.Catalogs
	loaded   bool
}

// providerRefresh is the outcome of the last listing of one provider.
type providerRefresh struct {
	at  time.Time
	err string
}

// catalogOf returns the catalog of one source.
func catalogOf(cats ai.Catalogs, source string) *ai.PriceCatalog {
	if source == core.PriceSourceOpenRouter {
		return cats.OpenRouter
	}
	return cats.LiteLLM
}

// setCatalog replaces the catalog of one source.
func setCatalog(cats *ai.Catalogs, source string, c *ai.PriceCatalog) {
	if source == core.PriceSourceOpenRouter {
		cats.OpenRouter = c
		return
	}
	cats.LiteLLM = c
}

// markProviderDown makes routing skip a provider for providerDownFor.
func (a *App) markProviderDown(id string) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	if a.state.down == nil {
		a.state.down = map[string]time.Time{}
	}
	a.state.down[id] = a.now().Add(providerDownFor)
}

// providerIsDown reports whether a provider failed in the last 60 s.
func (a *App) providerIsDown(id string) bool {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	until, ok := a.state.down[id]
	if ok && !a.now().Before(until) {
		delete(a.state.down, id)
		return false
	}
	return ok
}

// noteCallError marks a provider down when a call error says the provider
// itself is failing.
func (a *App) noteCallError(ctx context.Context, id string, err error) {
	if ctx.Err() == nil && ai.ProviderDown(err) {
		a.markProviderDown(id)
	}
}

// setRefreshState records the outcome of a provider listing.
func (a *App) setRefreshState(id string, at time.Time, err error) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	if a.state.refreshed == nil {
		a.state.refreshed = map[string]providerRefresh{}
	}
	st := providerRefresh{at: at}
	if err != nil {
		st.err = cutLabel(err.Error(), 300)
	}
	a.state.refreshed[id] = st
}

// refreshState returns the outcome of the last listing of a provider.
func (a *App) refreshState(id string) (providerRefresh, bool) {
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	st, ok := a.state.refreshed[id]
	return st, ok
}

// catalogs returns the parsed public price catalogs. The first call loads the
// last downloaded copies from the database. With ai.fetchPublicPrices off it
// returns no catalogs.
func (a *App) catalogs(ctx context.Context) ai.Catalogs {
	if !a.cfg.AI.FetchPublicPrices {
		return ai.Catalogs{}
	}
	a.state.mu.Lock()
	if a.state.loaded {
		cats := a.state.catalogs
		a.state.mu.Unlock()
		return cats
	}
	a.state.mu.Unlock()
	var cats ai.Catalogs
	for _, src := range catalogSources {
		body, fetched, err := a.db.GetPriceCatalog(ctx, src.name)
		if errors.Is(err, core.ErrNotFound) {
			continue
		}
		if err != nil {
			a.log.Warn("load price catalog failed", "source", src.name, "err", err)
			continue
		}
		cat, err := src.parse(body)
		if err != nil {
			a.log.Warn("stored price catalog unreadable", "source", src.name, "err", err)
			continue
		}
		cat.FetchedAt = fetched
		setCatalog(&cats, src.name, cat)
	}
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	if !a.state.loaded {
		a.state.catalogs = cats
		a.state.loaded = true
	}
	return a.state.catalogs
}

// refreshPriceCatalogs downloads every public catalog, stores the ones that
// parse and reprices the catalog rows. A failed download or parse keeps the
// last good copy.
func (a *App) refreshPriceCatalogs(ctx context.Context) *core.AIPriceRefresh {
	out := &core.AIPriceRefresh{Sources: []core.AIPriceCatalogStatus{}}
	cats := a.catalogs(ctx)
	for _, src := range catalogSources {
		st := core.AIPriceCatalogStatus{Source: src.name}
		if cur := catalogOf(cats, src.name); cur != nil {
			st.FetchedAt, st.Models = cur.FetchedAt, len(cur.Entries)
		}
		switch {
		case !a.cfg.AI.FetchPublicPrices:
			st.Error = "public prices are off in the configuration"
		case a.prices == nil:
			st.Error = "no price catalog fetcher is configured"
		default:
			if err := a.downloadCatalog(ctx, src, &cats, &st); err != nil {
				st.Error = cutLabel(err.Error(), 300)
				a.log.Warn("price catalog download failed, keeping the last copy", "source", src.name, "err", err)
			}
		}
		out.Sources = append(out.Sources, st)
	}
	if a.cfg.AI.FetchPublicPrices {
		a.state.mu.Lock()
		a.state.catalogs, a.state.loaded = cats, true
		a.state.mu.Unlock()
	}
	out.Repriced = a.repriceModels(ctx, a.catalogs(ctx))
	return out
}

// downloadCatalog fetches, parses and stores one catalog.
func (a *App) downloadCatalog(ctx context.Context, src catalogSource, cats *ai.Catalogs, st *core.AIPriceCatalogStatus) error {
	body, err := a.prices.Fetch(ctx, src.url)
	if err != nil {
		return fmt.Errorf("download %s: %w", src.name, err)
	}
	cat, err := src.parse(body)
	if err != nil {
		return fmt.Errorf("parse %s: %w", src.name, err)
	}
	if len(cat.Entries) == 0 {
		return fmt.Errorf("the %s catalog lists no models", src.name)
	}
	now := a.now()
	if err := a.db.SavePriceCatalog(ctx, src.name, body, now); err != nil {
		return fmt.Errorf("store %s: %w", src.name, err)
	}
	cat.FetchedAt = now
	setCatalog(cats, src.name, cat)
	st.FetchedAt, st.Models = now, len(cat.Entries)
	a.log.Info("price catalog downloaded", "source", src.name, "models", len(cat.Entries))
	return nil
}

// repriceModels recomputes the fetched price of every catalog row and
// returns how many rows changed.
func (a *App) repriceModels(ctx context.Context, cats ai.Catalogs) int {
	configs, err := a.providerConfigs(ctx)
	if err != nil {
		a.log.Warn("reprice: list providers failed", "err", err)
		return 0
	}
	byID := map[string]*core.AIProviderConfig{}
	for _, c := range configs {
		byID[c.ID] = c
	}
	rows, err := a.db.ListAIModels(ctx, "")
	if err != nil {
		a.log.Warn("reprice: list models failed", "err", err)
		return 0
	}
	now := a.now()
	changed := 0
	for _, m := range rows {
		cfg := byID[m.ProviderID]
		if cfg == nil {
			continue
		}
		before := *m
		ai.PriceModel(cfg, m, cats, now)
		if samePrice(&before, m) {
			continue
		}
		if err := a.db.UpdateAIModelPrice(ctx, m); err != nil {
			a.log.Warn("reprice: update failed", "providerId", m.ProviderID, "model", m.ModelID, "err", err)
			continue
		}
		changed++
	}
	return changed
}

// samePrice reports whether two rows carry the same fetched price.
func samePrice(x, y *core.AIModel) bool {
	if x.PriceSource != y.PriceSource || x.PriceMatched != y.PriceMatched || (x.Price == nil) != (y.Price == nil) {
		return false
	}
	return x.Price == nil || *x.Price == *y.Price
}

// refreshProvider lists one provider, fills vendors, capabilities and prices,
// and stores the rows. A failed listing keeps the stored rows.
func (a *App) refreshProvider(ctx context.Context, cfg *core.AIProviderConfig) ([]*core.AIModel, error) {
	listed, err := a.ai.ListCatalog(ctx, cfg)
	now := a.now()
	a.setRefreshState(cfg.ID, now, err)
	if err != nil {
		return nil, fmt.Errorf("list the models of %s: %w", cfg.ID, err)
	}
	cats := a.catalogs(ctx)
	listed = ai.EnrichModels(cfg, listed, cats)
	rows := make([]*core.AIModel, 0, len(listed))
	for i := range listed {
		m := &listed[i]
		if strings.TrimSpace(m.ModelID) == "" {
			continue
		}
		m.ProviderID, m.Enabled = cfg.ID, true
		ai.PriceModel(cfg, m, cats, now)
		rows = append(rows, m)
	}
	if err := a.db.UpsertAIModels(ctx, cfg.ID, rows, now); err != nil {
		return nil, fmt.Errorf("store the models of %s: %w", cfg.ID, err)
	}
	a.log.Info("ai catalog refreshed", "providerId", cfg.ID, "models", len(rows))
	return a.db.ListAIModels(ctx, cfg.ID)
}

// catalogLoop refreshes stale catalogs at startup and everything once a day.
func (a *App) catalogLoop(ctx context.Context) {
	a.catalogTick(ctx, true)
	t := time.NewTicker(catalogInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.catalogTick(ctx, false)
		}
	}
}

// catalogTick downloads the public catalogs and lists every enabled
// provider. At startup it skips what is younger than a day.
func (a *App) catalogTick(ctx context.Context, startup bool) {
	if a.ai == nil || !a.cfg.AI.Enabled {
		return
	}
	if a.cfg.AI.FetchPublicPrices && a.prices != nil {
		cats := a.catalogs(ctx)
		stale := !startup
		for _, src := range catalogSources {
			c := catalogOf(cats, src.name)
			stale = stale || c == nil || a.now().Sub(c.FetchedAt) > catalogInterval
		}
		if stale {
			a.refreshPriceCatalogs(ctx)
		}
	}
	configs, err := a.providerConfigs(ctx)
	if err != nil {
		a.log.Warn("catalog job: list providers failed", "err", err)
		return
	}
	for _, cfg := range configs {
		if !cfg.Enabled || ctx.Err() != nil {
			continue
		}
		if startup {
			rows, err := a.db.ListAIModels(ctx, cfg.ID)
			if err == nil && len(rows) > 0 && a.now().Sub(lastSeen(rows)) < catalogInterval {
				continue
			}
		}
		lctx, cancel := context.WithTimeout(ctx, jobListingTimeout)
		if _, err := a.refreshProvider(lctx, cfg); err != nil {
			a.log.Warn("catalog job: provider listing failed", "providerId", cfg.ID, "err", err)
		}
		cancel()
	}
}

// lastSeen returns the latest LastSeen of the rows.
func lastSeen(rows []*core.AIModel) time.Time {
	var at time.Time
	for _, m := range rows {
		if m.LastSeen.After(at) {
			at = m.LastSeen
		}
	}
	return at
}

// routeView is the provider, catalog and instance route state that one call
// resolves against.
type routeView struct {
	order     []*core.AIProviderConfig
	configs   map[string]*core.AIProviderConfig
	providers map[string]*ai.RouteProvider
	models    map[string]*core.AIModel
	instance  map[string][]core.AIRouteTarget
}

// loadRouteView reads the providers, the catalog and the instance routes. The
// models a provider configures, such as its chat, embedding and image model,
// count as catalog rows before the first listing.
func (a *App) loadRouteView(ctx context.Context) (*routeView, error) {
	configs, err := a.providerConfigs(ctx)
	if err != nil {
		return nil, err
	}
	v := &routeView{
		order:     configs,
		configs:   make(map[string]*core.AIProviderConfig, len(configs)),
		providers: make(map[string]*ai.RouteProvider, len(configs)),
		models:    map[string]*core.AIModel{},
	}
	for _, c := range configs {
		v.configs[c.ID] = c
		v.providers[c.ID] = &ai.RouteProvider{
			ID: c.ID, Kind: c.Kind, OwnerID: c.OwnerID, Enabled: c.Enabled, Down: a.providerIsDown(c.ID),
			ImageGeneration: c.ImageGeneration, ImageEditing: c.ImageEditing,
		}
	}
	rows, err := a.db.ListAIModels(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list ai models: %w", err)
	}
	for _, m := range rows {
		if v.configs[m.ProviderID] != nil {
			v.models[ai.ModelKey(m.ProviderID, m.ModelID)] = m
		}
	}
	for _, c := range configs {
		addConfiguredModels(v.models, c)
	}
	saved, err := a.db.ListAIRoutes(ctx, core.RouteScopeInstance)
	if err != nil {
		return nil, fmt.Errorf("list ai routes: %w", err)
	}
	v.instance = instanceRoutes(saved, a.cfg.AI.Routes, configs)
	return v, nil
}

// addConfiguredModels makes the chat, listed, embedding and image models a
// provider configures usable before its first listing. An embedding or image
// model the catalog already has gains that output.
func addConfiguredModels(models map[string]*core.AIModel, cfg *core.AIProviderConfig) {
	add := func(id string, inputs, outputs []string, augment bool) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		key := ai.ModelKey(cfg.ID, id)
		if m, ok := models[key]; ok {
			if augment {
				cp := *m
				cp.Inputs = unionStrings(cp.Inputs, inputs)
				cp.Outputs = unionStrings(cp.Outputs, outputs)
				models[key] = &cp
			}
			return
		}
		m := &core.AIModel{
			ProviderID: cfg.ID, ModelID: id, Name: id, Inputs: inputs, Outputs: outputs,
			Enabled: true, Available: true, PriceSource: core.PriceSourceNone,
		}
		if cfg.Local {
			m.Price, m.PriceSource = &core.AIPrice{}, core.PriceSourceLocal
		}
		models[key] = m
	}
	chatIn := []string{core.ModalityText}
	if cfg.Vision {
		chatIn = append(chatIn, core.ModalityImage)
	}
	text := []string{core.ModalityText}
	if cfg.Kind != core.ProviderComfyUI {
		add(cfg.Model, chatIn, text, false)
		for _, id := range cfg.Models {
			add(id, chatIn, text, false)
		}
		if cfg.Kind != core.ProviderAnthropic {
			add(cfg.EmbedModel, text, []string{core.ModalityEmbedding}, true)
		}
	}
	if cfg.ImageGeneration || cfg.ImageEditing {
		in := []string{core.ModalityText}
		if cfg.ImageEditing {
			in = append(in, core.ModalityImage)
		}
		add(cfg.ImageModel, in, []string{core.ModalityImage}, true)
	}
}

// unionStrings appends the values of b missing from a.
func unionStrings(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, v := range b {
		found := false
		for _, w := range out {
			found = found || w == v
		}
		if !found {
			out = append(out, v)
		}
	}
	return out
}

// instanceRoutes merges the stored instance routes over the configured ones.
// A route with no targets counts as absent. Without a default route the
// chat model of every enabled instance provider forms one, in configuration
// order. Without an embeddings route the first instance provider with an
// embedding model forms one, and without image routes the providers with an
// image flag and an image model form them.
func instanceRoutes(saved []core.AIRoute, configured map[string][]core.AIRouteTarget, configs []*core.AIProviderConfig) map[string][]core.AIRouteTarget {
	out := map[string][]core.AIRouteTarget{}
	for task, targets := range configured {
		if len(targets) > 0 {
			out[task] = targets
		}
	}
	for _, r := range saved {
		if len(r.Targets) > 0 {
			out[r.Task] = r.Targets
		}
	}
	implicit := func(task string, pick func(c *core.AIProviderConfig) string, first bool) {
		if _, ok := out[task]; ok {
			return
		}
		var list []core.AIRouteTarget
		for _, c := range configs {
			if c.OwnerID != "" || !c.Enabled {
				continue
			}
			if model := pick(c); model != "" {
				list = append(list, core.AIRouteTarget{ProviderID: c.ID, Model: model})
				if first {
					break
				}
			}
		}
		if len(list) > 0 {
			out[task] = list
		}
	}
	implicit(core.RouteDefault, func(c *core.AIProviderConfig) string {
		if c.Kind == core.ProviderComfyUI {
			return ""
		}
		return c.Model
	}, false)
	implicit(core.RouteEmbeddings, func(c *core.AIProviderConfig) string {
		if c.Kind == core.ProviderComfyUI || c.Kind == core.ProviderAnthropic {
			return ""
		}
		return c.EmbedModel
	}, true)
	implicit(core.RouteImageGenerate, func(c *core.AIProviderConfig) string {
		if !c.ImageGeneration {
			return ""
		}
		return c.ImageModel
	}, false)
	implicit(core.RouteImageEdit, func(c *core.AIProviderConfig) string {
		if !c.ImageEditing {
			return ""
		}
		return c.ImageModel
	}, false)
	return out
}

// routeRequest builds the routing input of one call. userID is empty for
// work the instance does for itself, such as index builds.
func (a *App) routeRequest(ctx context.Context, v *routeView, settings *core.Settings, userID, task string, needs core.AINeeds) (ai.RouteRequest, error) {
	req := ai.RouteRequest{
		Task: task, Needs: needs, UserID: userID,
		InstanceRoutes: v.instance, Providers: v.providers, Models: v.models,
	}
	if userID != "" && settings != nil && settings.AIAllowUserRoutes {
		routes, err := a.db.ListAIRoutes(ctx, userID)
		if err != nil {
			return req, fmt.Errorf("list user routes: %w", err)
		}
		req.AllowUserRoutes = true
		req.UserRoutes = map[string][]core.AIRouteTarget{}
		for _, r := range routes {
			req.UserRoutes[r.Task] = r.Targets
		}
	}
	return req, nil
}

// capabilityNeeds maps the capability filter of a catalog listing.
func capabilityNeeds(capability string) (core.AINeeds, error) {
	switch strings.TrimSpace(capability) {
	case "":
		return core.AINeeds{}, nil
	case "text":
		return core.AINeeds{Text: true}, nil
	case "vision", "image-input":
		return core.AINeeds{Text: true, ImageIn: true}, nil
	case "image-output":
		return core.AINeeds{ImageOut: true}, nil
	case "embedding":
		return core.AINeeds{Embedding: true}, nil
	}
	if core.ValidRouteTask(capability) {
		return core.RouteNeeds(capability), nil
	}
	return core.AINeeds{}, core.Invalid("capability",
		"capability must be text, vision, image-output, embedding or a task such as image.edit")
}

// decorateModel copies a row and fills the provider name, the local flag and
// the effective price.
func decorateModel(m *core.AIModel, cfg *core.AIProviderConfig) *core.AIModel {
	cp := *m
	if cfg != nil {
		cp.ProviderName, cp.Local = cfg.Name, cfg.Local
	}
	cp.EffectivePrice, cp.EffectiveSource = m.Effective()
	if cp.Inputs == nil {
		cp.Inputs = []string{core.ModalityText}
	}
	if cp.Outputs == nil {
		cp.Outputs = []string{core.ModalityText}
	}
	return &cp
}

// ListAIModels returns the catalog rows the actor may use. A non-admin sees
// the enabled, available models of the providers they may call. An admin
// also sees disabled and unavailable rows. Nobody sees the models of another
// user's personal provider.
func (a *App) ListAIModels(ctx context.Context, actor *core.Actor, q core.AIModelQuery) ([]*core.AIModel, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if a.ai == nil {
		return nil, errAIDisabled
	}
	needs, err := capabilityNeeds(q.Capability)
	if err != nil {
		return nil, err
	}
	v, err := a.loadRouteView(ctx)
	if err != nil {
		return nil, err
	}
	text := strings.ToLower(strings.TrimSpace(q.Q))
	out := []*core.AIModel{}
	for _, m := range v.models {
		cfg := v.configs[m.ProviderID]
		switch {
		case cfg.OwnerID != "" && cfg.OwnerID != actor.UserID():
			continue
		case !actor.IsAdmin() && (!callable(actor, cfg) || !m.Enabled || !m.Available):
			continue
		case q.ProviderID != "" && m.ProviderID != q.ProviderID:
			continue
		case text != "" && !strings.Contains(strings.ToLower(m.ModelID+"\x00"+m.Name+"\x00"+m.Vendor), text):
			continue
		case needs != (core.AINeeds{}) && !ai.ModelCapable(m, v.providers[m.ProviderID], needs):
			continue
		}
		out = append(out, decorateModel(m, cfg))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProviderName != out[j].ProviderName {
			return strings.ToLower(out[i].ProviderName) < strings.ToLower(out[j].ProviderName)
		}
		if out[i].ProviderID != out[j].ProviderID {
			return out[i].ProviderID < out[j].ProviderID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out, nil
}

// UpdateAIModel enables or disables a catalog row or sets its price
// override. Admin only.
func (a *App) UpdateAIModel(ctx context.Context, actor *core.Actor, providerID, model string, in core.AIModelUpdate) (*core.AIModel, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if a.ai == nil {
		return nil, errAIDisabled
	}
	cfg, err := a.providerConfig(ctx, providerID)
	if err != nil {
		return nil, err
	}
	m, err := a.db.GetAIModel(ctx, providerID, model)
	if errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("model %s of %s is not in the catalog, refresh the provider: %w", model, providerID, core.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load ai model: %w", err)
	}
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	if in.SetPrice {
		if in.PriceOverride != nil {
			if err := in.PriceOverride.Validate("priceOverride"); err != nil {
				return nil, err
			}
			p := *in.PriceOverride
			m.PriceOverride = &p
		} else {
			m.PriceOverride = nil
		}
	}
	if err := a.db.UpdateAIModelSettings(ctx, providerID, model, m.Enabled, m.PriceOverride); err != nil {
		return nil, fmt.Errorf("update ai model: %w", err)
	}
	a.log.Info("ai model updated", "providerId", providerID, "model", model, "enabled", m.Enabled,
		"override", m.PriceOverride != nil, "by", actor.UserID())
	return decorateModel(m, cfg), nil
}

// RefreshAIProvider lists a provider's models again. An admin may refresh any
// provider, and a user only a personal provider they own.
func (a *App) RefreshAIProvider(ctx context.Context, actor *core.Actor, id string) ([]*core.AIModel, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if a.ai == nil || !a.cfg.AI.Enabled {
		return nil, errAIDisabled
	}
	cfg, err := a.providerConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	if !actor.IsAdmin() {
		if cfg.OwnerID == "" {
			return nil, fmt.Errorf("refresh an instance provider: %w", core.ErrForbidden)
		}
		if cfg.OwnerID != actor.UserID() {
			return nil, fmt.Errorf("ai provider %s: %w", id, core.ErrNotFound)
		}
	}
	lctx, cancel := context.WithTimeout(ctx, jobListingTimeout)
	defer cancel()
	rows, err := a.refreshProvider(lctx, cfg)
	if err != nil {
		// The provider's own status, such as 401, must not become the status
		// of this request.
		return nil, fmt.Errorf("%v: %w", err, core.ErrUnavailable)
	}
	out := make([]*core.AIModel, 0, len(rows))
	for _, m := range rows {
		out = append(out, decorateModel(m, cfg))
	}
	return out, nil
}

// RefreshAIPrices downloads the public price catalogs now. Admin only.
func (a *App) RefreshAIPrices(ctx context.Context, actor *core.Actor) (*core.AIPriceRefresh, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if a.ai == nil {
		return nil, errAIDisabled
	}
	return a.refreshPriceCatalogs(ctx), nil
}

// ListAIPresets returns the provider preset table.
func (a *App) ListAIPresets(ctx context.Context, actor *core.Actor) ([]core.AIPreset, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	return ai.Presets(), nil
}

// routeOrder lists the built-in tasks first, then plugin tasks by name.
func routeOrder(routes map[string][]core.AIRouteTarget) []string {
	out := append([]string(nil), core.AIRouteTasks...)
	var extra []string
	for task := range routes {
		if !slicesContains(core.AIRouteTasks, task) {
			extra = append(extra, task)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// slicesContains reports whether list holds s.
func slicesContains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ListAIRoutes returns the instance routes that calls use, including the
// configured and implicit ones, and the actor's own routes when user routes
// are allowed.
func (a *App) ListAIRoutes(ctx context.Context, actor *core.Actor) (*core.AIRoutes, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if a.ai == nil {
		return nil, errAIDisabled
	}
	settings, err := a.settings(ctx)
	if err != nil {
		return nil, err
	}
	v, err := a.loadRouteView(ctx)
	if err != nil {
		return nil, err
	}
	out := &core.AIRoutes{
		Instance:        []core.AIRoute{},
		AllowUserRoutes: settings.AIAllowUserRoutes,
		Tasks:           append([]string(nil), core.AIRouteTasks...),
	}
	for _, task := range routeOrder(v.instance) {
		if targets, ok := v.instance[task]; ok {
			out.Instance = append(out.Instance, core.AIRoute{Task: task, Targets: targets})
		}
	}
	if settings.AIAllowUserRoutes {
		mine, err := a.db.ListAIRoutes(ctx, actor.UserID())
		if err != nil {
			return nil, fmt.Errorf("list user routes: %w", err)
		}
		out.User = append([]core.AIRoute{}, mine...)
	}
	return out, nil
}

// SaveAIRoute replaces the targets of one route. The instance scope needs an
// admin and instance providers. The "me" scope needs user routes on and
// accepts instance providers and the actor's own. Every target must name a
// catalog model that has the task's capabilities. Changing the first target
// of the instance embeddings route starts a reindex.
func (a *App) SaveAIRoute(ctx context.Context, actor *core.Actor, scope, task string, targets []core.AIRouteTarget) (*core.AIRoute, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if a.ai == nil {
		return nil, errAIDisabled
	}
	var dbScope string
	switch scope {
	case core.RouteScopeInstance:
		if err := requireAdmin(actor); err != nil {
			return nil, err
		}
		dbScope = core.RouteScopeInstance
	case "me":
		settings, err := a.settings(ctx)
		if err != nil {
			return nil, err
		}
		if !settings.AIAllowUserRoutes {
			return nil, fmt.Errorf("user routes are off: %w", core.ErrForbidden)
		}
		dbScope = actor.UserID()
	default:
		return nil, core.Invalid("scope", "scope must be instance or me")
	}
	task = strings.TrimSpace(task)
	if !core.ValidRouteTask(task) {
		return nil, core.Invalid("task", "unknown task "+task)
	}
	if len(targets) > core.MaxRouteTargets {
		return nil, core.Invalid("targets", fmt.Sprintf("a route has at most %d targets", core.MaxRouteTargets))
	}
	v, err := a.loadRouteView(ctx)
	if err != nil {
		return nil, err
	}
	needs := core.RouteNeeds(task)
	seen := map[core.AIRouteTarget]bool{}
	clean := make([]core.AIRouteTarget, 0, len(targets))
	for _, t := range targets {
		t.ProviderID, t.Model = strings.TrimSpace(t.ProviderID), strings.TrimSpace(t.Model)
		if t.ProviderID == "" || t.Model == "" {
			return nil, core.Invalid("targets", "every target needs providerId and model")
		}
		if seen[t] {
			return nil, core.Invalid("targets", "model "+t.Model+" of "+t.ProviderID+" is listed twice")
		}
		seen[t] = true
		cfg := v.configs[t.ProviderID]
		if cfg == nil || (cfg.OwnerID != "" && (dbScope == core.RouteScopeInstance || cfg.OwnerID != actor.UserID())) {
			return nil, core.Invalid("targets", "provider "+t.ProviderID+" does not exist")
		}
		m := v.models[ai.ModelKey(t.ProviderID, t.Model)]
		if m == nil {
			return nil, core.Invalid("targets", "model "+t.Model+" of "+t.ProviderID+" is not in the catalog")
		}
		if !ai.ModelCapable(m, v.providers[t.ProviderID], needs) {
			return nil, core.Invalid("targets", "model "+t.Model+" of "+t.ProviderID+" cannot serve the "+task+" task")
		}
		clean = append(clean, t)
	}
	before := v.instance[core.RouteEmbeddings]
	if err := a.db.SaveAIRoute(ctx, dbScope, core.AIRoute{Task: task, Targets: clean}); err != nil {
		return nil, fmt.Errorf("save ai route: %w", err)
	}
	a.log.Info("ai route saved", "scope", scope, "task", task, "targets", len(clean), "by", actor.UserID())
	if dbScope == core.RouteScopeInstance && task == core.RouteEmbeddings && len(clean) > 0 &&
		(len(before) == 0 || before[0] != clean[0]) {
		a.startReindex(actor.UserID())
	}
	return &core.AIRoute{Task: task, Targets: clean}, nil
}

// DeleteAIRoute removes the actor's own route for a task.
func (a *App) DeleteAIRoute(ctx context.Context, actor *core.Actor, task string) error {
	if err := requireUser(actor); err != nil {
		return err
	}
	if a.ai == nil {
		return errAIDisabled
	}
	if !core.ValidRouteTask(task) {
		return core.Invalid("task", "unknown task "+task)
	}
	if err := a.db.DeleteAIRoute(ctx, actor.UserID(), task); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return fmt.Errorf("route %s: %w", task, core.ErrNotFound)
		}
		return fmt.Errorf("delete ai route: %w", err)
	}
	return nil
}

// startReindex rebuilds the semantic index of every space in the background
// after the embedding model changed.
func (a *App) startReindex(userID string) {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx := a.bg
		t, err := a.embeddingTarget(ctx)
		if err != nil {
			a.log.Warn("reindex after an embeddings route change skipped", "err", err)
			return
		}
		spaces, err := a.db.ListSpaces(ctx)
		if err != nil {
			a.log.Warn("reindex: list spaces failed", "err", err)
			return
		}
		total := 0
		for _, sp := range spaces {
			n, err := a.embedSpace(ctx, userID, t, sp)
			total += n
			if err != nil {
				a.log.Warn("reindex failed", "spaceId", sp.ID, "err", err)
				return
			}
		}
		a.log.Info("embeddings reindexed after a route change", "notes", total, "model", t.model)
	}()
}
