package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/ai"
	"github.com/gamemaker1/markspace/internal/config"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

const (
	// embedChunkRunes is the target size of one embedded chunk.
	embedChunkRunes = 1200
	// embedBatch is how many chunks go into one embedding request.
	embedBatch = 16
)

// errAIDisabled reports that the AI service is off.
var errAIDisabled = fmt.Errorf("ai is disabled: %w: %w", core.ErrUnavailable, core.ErrUnsupported)

// aiReady checks that the AI service is wired and switched on.
func (a *App) aiReady(ctx context.Context) (*core.Settings, error) {
	if a.ai == nil || !a.cfg.AI.Enabled {
		return nil, errAIDisabled
	}
	s, err := a.settings(ctx)
	if err != nil {
		return nil, err
	}
	if !s.AIEnabled {
		return nil, errAIDisabled
	}
	return s, nil
}

// fileProvider returns the configuration file entry with this id.
func (a *App) fileProvider(id string) *core.AIProviderConfig {
	for i := range a.cfg.AI.Providers {
		if a.cfg.AI.Providers[i].ID == id {
			cp := a.cfg.AI.Providers[i]
			cp.Source = "file"
			return &cp
		}
	}
	return nil
}

// providerConfigs merges the configuration file providers with the database
// providers. The database wins on an id clash. API keys are still present.
func (a *App) providerConfigs(ctx context.Context) ([]*core.AIProviderConfig, error) {
	out := make([]*core.AIProviderConfig, 0, len(a.cfg.AI.Providers))
	index := map[string]int{}
	for i := range a.cfg.AI.Providers {
		cp := a.cfg.AI.Providers[i]
		cp.Source = "file"
		index[cp.ID] = len(out)
		out = append(out, &cp)
	}
	rows, err := a.db.ListAIProviders(ctx)
	if err != nil {
		return nil, fmt.Errorf("list ai providers: %w", err)
	}
	for _, r := range rows {
		cp := *r
		cp.Source = "db"
		if i, ok := index[cp.ID]; ok {
			out[i] = &cp
			continue
		}
		index[cp.ID] = len(out)
		out = append(out, &cp)
	}
	return out, nil
}

// providerConfig finds one merged provider by id.
func (a *App) providerConfig(ctx context.Context, id string) (*core.AIProviderConfig, error) {
	all, err := a.providerConfigs(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("ai provider %s: %w", id, core.ErrNotFound)
}

// usable reports whether a provider is switched on. The configuration loader
// defaults Enabled to true for a file provider without an enabled key.
func usable(p *core.AIProviderConfig) bool {
	return p != nil && p.Enabled
}

// callable reports whether the actor may send requests through a provider.
// Instance providers are open to every user. A personal provider carries its
// owner's API key, so only the owner may call it, admins included.
func callable(actor *core.Actor, p *core.AIProviderConfig) bool {
	return usable(p) && (p.OwnerID == "" || p.OwnerID == actor.UserID())
}

// visible reports whether the actor may see a provider in a listing. Admins
// see every provider so they can manage them. An owner sees their personal
// provider even while it is disabled, so they can switch it back on. Other
// users see only enabled instance providers.
func visible(actor *core.Actor, p *core.AIProviderConfig) bool {
	if actor.IsAdmin() {
		return true
	}
	if p != nil && p.OwnerID != "" && p.OwnerID == actor.UserID() {
		return true
	}
	return callable(actor, p)
}

// evictProvider drops a cached provider so the next request rebuilds it from
// the stored configuration.
func (a *App) evictProvider(id string) {
	if ev, ok := a.ai.(ports.AIEvictor); ok {
		ev.Remove(id)
	}
}

// stripKey returns a copy of a provider without its API key.
func stripKey(p *core.AIProviderConfig) *core.AIProviderConfig {
	cp := *p
	cp.HasAPIKey = p.APIKey != "" || p.HasAPIKey
	cp.APIKey = ""
	if p.Headers != nil {
		cp.Headers = make(map[string]string, len(p.Headers))
		for k, v := range p.Headers {
			if strings.EqualFold(k, "authorization") || strings.Contains(strings.ToLower(k), "key") {
				v = ""
			}
			cp.Headers[k] = v
		}
	}
	return &cp
}

// embedTarget is the model that serves the embeddings route.
type embedTarget struct {
	provider core.AIProvider
	cfg      *core.AIProviderConfig
	model    string
	price    *core.AIPrice
}

// embeddingTarget resolves the instance embeddings route. Only its first
// entry counts, because vectors from different models cannot be mixed. It
// returns errNoEmbeddings when that model cannot serve the call.
func (a *App) embeddingTarget(ctx context.Context) (*embedTarget, error) {
	if a.ai == nil {
		return nil, errNoEmbeddings
	}
	v, err := a.loadRouteView(ctx)
	if err != nil {
		return nil, err
	}
	plan := ai.ResolveRoute(ai.RouteRequest{
		Task: core.RouteEmbeddings, Needs: core.RouteNeeds(core.RouteEmbeddings),
		InstanceRoutes: v.instance, Providers: v.providers, Models: v.models,
	})
	if len(plan.Candidates) == 0 {
		return nil, errNoEmbeddings
	}
	c := plan.Candidates[0]
	cfg := v.configs[c.Target.ProviderID]
	prov, err := a.ai.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("build ai provider %s: %w", cfg.ID, err)
	}
	price, _ := c.Model.Effective()
	return &embedTarget{provider: prov, cfg: cfg, model: c.Target.Model, price: price}, nil
}

// ListAIProviders returns the providers visible to the actor without keys,
// with the size and the last refresh of each catalog.
func (a *App) ListAIProviders(ctx context.Context, actor *core.Actor) ([]*core.AIProviderConfig, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if a.ai == nil {
		return nil, errAIDisabled
	}
	all, err := a.providerConfigs(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := a.db.ListAIModels(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list ai models: %w", err)
	}
	counts := map[string]int{}
	seen := map[string]time.Time{}
	for _, m := range rows {
		if m.Available {
			counts[m.ProviderID]++
		}
		if m.LastSeen.After(seen[m.ProviderID]) {
			seen[m.ProviderID] = m.LastSeen
		}
	}
	out := make([]*core.AIProviderConfig, 0, len(all))
	for _, p := range all {
		if !visible(actor, p) {
			continue
		}
		cp := stripKey(p)
		cp.ModelCount, cp.RefreshedAt = counts[p.ID], seen[p.ID]
		if st, ok := a.refreshState(p.ID); ok {
			cp.RefreshedAt, cp.RefreshError = st.at, st.err
		}
		out = append(out, cp)
	}
	return out, nil
}

// SaveAIProvider creates or updates a database provider and lists its models.
// An admin may save any provider, and a provider an admin creates is an
// instance provider. A non-admin needs AIAllowUserKeys, and may create a
// personal provider or update one they own. The owner never changes after
// creation. A preset fills in the protocol, base URL and headers.
func (a *App) SaveAIProvider(ctx context.Context, actor *core.Actor, in *core.AIProviderConfig) (*core.AIProviderConfig, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if a.ai == nil {
		return nil, errAIDisabled
	}
	if in == nil {
		return nil, core.Invalid("provider", "provider is required")
	}
	cfg := *in
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Preset = strings.TrimSpace(cfg.Preset)
	if cfg.Name == "" && cfg.Preset != "" {
		if p, ok := ai.PresetByID(cfg.Preset); ok {
			cfg.Name = p.Name
		}
	}
	if cfg.ID == "" {
		cfg.ID = core.Slugify(cfg.Name)
	}
	if cfg.ID == "" {
		return nil, core.Invalid("name", "a name is required")
	}
	if cfg.Name == "" {
		cfg.Name = cfg.ID
	}
	if err := ai.ApplyPreset(&cfg); err != nil {
		return nil, err
	}
	if cfg.Kind == "" {
		cfg.Kind = core.ProviderOpenAI
	}
	if !validKind(cfg.Kind) {
		return nil, core.Invalid("kind", "unknown provider kind "+string(cfg.Kind))
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return nil, core.Invalid("baseUrl", "base URL is required")
	}
	if err := cfg.ValidateImageFields(); err != nil {
		return nil, err
	}
	if cfg.Kind == core.ProviderComfyUI && cfg.EmbedModel != "" {
		return nil, core.Invalid("embedModel", "the comfyui kind has no embeddings")
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = config.DefaultTimeoutSeconds(&cfg)
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 2048
	}

	existing, err := a.db.GetAIProvider(ctx, cfg.ID)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load ai provider: %w", err)
	}
	if !actor.IsAdmin() {
		s, err := a.settings(ctx)
		if err != nil {
			return nil, err
		}
		if !s.AIAllowUserKeys || !a.cfg.AI.AllowUserKeys {
			return nil, fmt.Errorf("user providers are off: %w", core.ErrForbidden)
		}
		if a.fileProvider(cfg.ID) != nil {
			return nil, fmt.Errorf("ai provider %s exists: %w", cfg.ID, core.ErrForbidden)
		}
		if existing != nil && existing.OwnerID != actor.UserID() {
			return nil, fmt.Errorf("ai provider %s exists: %w", cfg.ID, core.ErrForbidden)
		}
		cfg.OwnerID = actor.UserID()
	} else if existing == nil && cfg.OwnerID != actor.UserID() {
		// An admin creates an instance provider, or a personal one by naming
		// themselves as owner. An admin cannot create one owned by someone else.
		cfg.OwnerID = ""
	}
	if existing != nil {
		cfg.OwnerID = existing.OwnerID
	}

	now := a.now()
	cfg.Source = "db"
	cfg.UpdatedAt = now
	if existing != nil {
		if cfg.APIKey == "" {
			cfg.APIKey = existing.APIKey
		}
		cfg.CreatedAt = existing.CreatedAt
		if err := a.db.UpdateAIProvider(ctx, &cfg); err != nil {
			return nil, fmt.Errorf("update ai provider: %w", err)
		}
	} else {
		cfg.CreatedAt = now
		if err := a.db.CreateAIProvider(ctx, &cfg); err != nil {
			return nil, fmt.Errorf("create ai provider: %w", err)
		}
	}
	cfg.HasAPIKey = cfg.APIKey != ""
	a.evictProvider(cfg.ID)
	a.log.Info("ai provider saved", "providerId", cfg.ID, "kind", cfg.Kind, "preset", cfg.Preset,
		"owner", cfg.OwnerID, "by", actor.User.ID)
	if cfg.Enabled && a.cfg.AI.Enabled {
		lctx, cancel := context.WithTimeout(ctx, saveListingTimeout)
		if rows, err := a.refreshProvider(lctx, &cfg); err != nil {
			a.log.Warn("model listing after save failed", "providerId", cfg.ID, "err", err)
		} else {
			cfg.ModelCount = len(rows)
		}
		cancel()
		if st, ok := a.refreshState(cfg.ID); ok {
			cfg.RefreshedAt, cfg.RefreshError = st.at, st.err
		}
	}
	return stripKey(&cfg), nil
}

// DeleteAIProvider removes a database provider and its catalog rows. An admin
// may delete any. A non-admin may delete only a personal provider they own. A
// provider from the configuration file cannot be deleted here.
func (a *App) DeleteAIProvider(ctx context.Context, actor *core.Actor, id string) error {
	if err := requireUser(actor); err != nil {
		return err
	}
	if a.ai == nil {
		return errAIDisabled
	}
	if !actor.IsAdmin() {
		s, err := a.settings(ctx)
		if err != nil {
			return err
		}
		if !s.AIAllowUserKeys || !a.cfg.AI.AllowUserKeys {
			return fmt.Errorf("user providers are off: %w", core.ErrForbidden)
		}
	}
	existing, err := a.db.GetAIProvider(ctx, id)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return fmt.Errorf("load ai provider: %w", err)
	}
	if existing == nil {
		if a.fileProvider(id) != nil {
			return core.Invalid("id", "provider is defined in the configuration file")
		}
		return fmt.Errorf("ai provider %s: %w", id, core.ErrNotFound)
	}
	if !actor.IsAdmin() && existing.OwnerID != actor.UserID() {
		return fmt.Errorf("ai provider %s: %w", id, core.ErrForbidden)
	}
	if err := a.db.DeleteAIProvider(ctx, id); err != nil {
		return fmt.Errorf("delete ai provider: %w", err)
	}
	a.evictProvider(id)
	a.log.Info("ai provider deleted", "providerId", id, "by", actor.User.ID)
	return nil
}

// TestAIProvider runs a health check against a provider. It needs an admin, or
// AIAllowUserKeys for a normal user.
func (a *App) TestAIProvider(ctx context.Context, actor *core.Actor, id string) error {
	if err := a.requireProviderUser(ctx, actor); err != nil {
		return err
	}
	cfg, err := a.providerConfig(ctx, id)
	if err != nil {
		return err
	}
	if cfg.OwnerID != "" && cfg.OwnerID != actor.UserID() {
		return fmt.Errorf("ai provider %s: %w", id, core.ErrNotFound)
	}
	p, err := a.ai.New(cfg)
	if err != nil {
		return fmt.Errorf("build ai provider %s: %w", cfg.ID, err)
	}
	began, at := time.Now(), a.now()
	err = p.Health(ctx)
	rec := core.AIUsageRecord{
		UserID: actor.UserID(), ProviderID: cfg.ID, Model: cfg.Model, Feature: core.FeatureTest,
		Status: usageStatus(ctx, err), Error: errorText(err), CreatedAt: at,
	}
	fillUsage(&rec, nil, 0, 0, 0, "", began)
	a.recordUsage(ctx, rec)
	if err != nil {
		return fmt.Errorf("ai provider %s health: %w", id, err)
	}
	return nil
}

// requireProviderUser admits an admin, or a user when user providers are on.
func (a *App) requireProviderUser(ctx context.Context, actor *core.Actor) error {
	if err := requireUser(actor); err != nil {
		return err
	}
	if a.ai == nil {
		return errAIDisabled
	}
	if actor.IsAdmin() {
		return nil
	}
	s, err := a.settings(ctx)
	if err != nil {
		return err
	}
	if !s.AIAllowUserKeys || !a.cfg.AI.AllowUserKeys {
		return fmt.Errorf("user providers are off: %w", core.ErrForbidden)
	}
	return nil
}

// routeTask returns the routing task of a request: its route, else the task
// its feature or prompt task implies.
func routeTask(req core.AIRequest) string {
	if r := strings.TrimSpace(req.Route); r != "" {
		return r
	}
	if visionTask(req.Task) {
		return core.RouteVision
	}
	switch req.Feature {
	case core.FeatureChat:
		return core.RouteChat
	case core.FeatureAsk:
		return core.RouteAnswer
	case core.FeatureRewrite:
		return core.RouteEdit
	}
	switch req.Task {
	case core.TaskAsk, core.TaskExplain, core.TaskSummarize:
		return core.RouteAnswer
	case core.TaskImprove, core.TaskGrammar, core.TaskShorten, core.TaskExpand, core.TaskTranslate,
		core.TaskContinue, core.TaskCustom, core.TaskTitle, core.TaskTags, core.TaskOutline, core.TaskActions:
		return core.RouteEdit
	}
	return core.RouteDefault
}

// visionTask reports whether a prompt task means nothing without images.
func visionTask(task core.AITask) bool {
	return task == core.TaskDescribe || task == core.TaskExtract
}

// errNoRoute reports that no model of a route can serve a call.
func errNoRoute(task string) error {
	return fmt.Errorf("no model can serve the %s task: %w: %w", task, core.ErrUnavailable, core.ErrUnsupported)
}

// requestedTarget returns the model a request chose. A provider id without a
// model takes the provider's chat model. A model without a provider takes
// the first provider the actor may call that offers it. A provider the actor
// may not see is not found.
func requestedTarget(v *routeView, actor *core.Actor, req core.AIRequest) (*core.AIRouteTarget, error) {
	providerID, model := strings.TrimSpace(req.ProviderID), strings.TrimSpace(req.Model)
	if providerID == "" && model == "" {
		return nil, nil
	}
	if providerID != "" {
		cfg := v.configs[providerID]
		if cfg == nil || (cfg.OwnerID != "" && cfg.OwnerID != actor.UserID()) {
			return nil, fmt.Errorf("ai provider %s: %w", providerID, core.ErrNotFound)
		}
		if model == "" {
			model = cfg.Model
		}
		if model == "" {
			return nil, nil
		}
		return &core.AIRouteTarget{ProviderID: providerID, Model: model}, nil
	}
	for _, cfg := range v.order {
		if callable(actor, cfg) && v.models[ai.ModelKey(cfg.ID, model)] != nil {
			return &core.AIRouteTarget{ProviderID: cfg.ID, Model: model}, nil
		}
	}
	return nil, nil
}

// callTarget is one route candidate prepared for a call.
type callTarget struct {
	cfg   *core.AIProviderConfig
	model string
	price *core.AIPrice
	// usage is the usage row of a call to this target before it runs.
	usage core.AIUsageRecord
}

// aiCall is a request prepared for its route.
type aiCall struct {
	route   string
	targets []*callTarget
	msgs    []core.AIMessage
	sources []core.AISource
	// promptRunes feeds the token estimate, and budgets feed the budget
	// warning.
	promptRunes int
	budgets     []budgetState
}

// options returns the call options of a target.
func (t *callTarget) options() core.AIOptions {
	return core.AIOptions{Model: t.model, MaxTokens: t.cfg.MaxTokens}
}

// prepareAI checks access, resolves the route, checks the monthly budgets,
// resolves the context references and builds the transcript. The context
// resolves with images when the route has a model that accepts them. A
// stopped budget records a usage row with status budget and returns
// core.ErrQuotaExceeded.
func (a *App) prepareAI(ctx context.Context, actor *core.Actor, req core.AIRequest) (*aiCall, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	settings, err := a.aiReady(ctx)
	if err != nil {
		return nil, err
	}
	if !core.ValidAIOutput(req.Output) {
		return nil, core.Invalid("output", "output must be replace, insert or answer")
	}
	spaceID := req.SpaceID
	var note *core.Note
	if req.NoteID != "" {
		note, _, err = a.requireNoteAny(ctx, actor, req.NoteID, core.AccessView)
		if err != nil {
			return nil, err
		}
		spaceID = note.SpaceID
	} else if spaceID != "" {
		if _, _, _, err := a.spaceView(ctx, actor, spaceID); err != nil {
			return nil, err
		}
	}
	route := routeTask(req)
	if !core.ValidRouteTask(route) {
		return nil, core.Invalid("route", "unknown routing task "+route)
	}
	view, err := a.loadRouteView(ctx)
	if err != nil {
		return nil, err
	}
	requested, err := requestedTarget(view, actor, req)
	if err != nil {
		return nil, err
	}
	base := core.AIUsageRecord{
		UserID: actor.UserID(), Feature: usageFeature(req), Task: route,
		PluginID: cutLabel(req.PluginID, maxPluginIDBytes), SpaceID: spaceID, NoteID: req.NoteID,
	}
	budgets, stop, err := a.checkBudgets(ctx, settings, base.UserID, base.PluginID)
	if err != nil {
		// A budget that cannot be read does not block the call.
		a.log.Warn("ai budget check failed", "userId", base.UserID, "pluginId", base.PluginID, "err", err)
		budgets, stop = nil, nil
	}
	needs := core.RouteNeeds(route)
	if visionTask(req.Task) {
		needs.Text, needs.ImageIn = true, true
	}
	rreq, err := a.routeRequest(ctx, view, settings, actor.UserID(), route, needs)
	if err != nil {
		return nil, err
	}
	rreq.Requested = requested

	// A first pass without the prompt size stops a call that no model could
	// serve, or that the budget refuses, before any context is fetched.
	pre := ai.ResolveRoute(rreq)
	if len(pre.Candidates) == 0 {
		if visionTask(req.Task) {
			return nil, core.Invalid("providerId", fmt.Sprintf(
				"the %s task needs a vision model: add a model that accepts images to the vision route", req.Task))
		}
		return nil, errNoRoute(route)
	}
	rreq.BudgetExhausted = stop != nil
	if ai.ResolveRoute(rreq).QuotaExceeded {
		rec := base
		rec.ProviderID, rec.Model = pre.Candidates[0].Target.ProviderID, pre.Candidates[0].Target.Model
		rec.Status, rec.CreatedAt = core.UsageBudget, a.now()
		a.recordUsage(ctx, rec)
		return nil, quotaError(stop)
	}
	rreq.BudgetExhausted = false

	// The task text is the text sent, else the first selection reference,
	// else the note body.
	if req.Text == "" {
		req.Text = firstSelection(req.Context)
	}
	if req.Text == "" && note != nil && req.Task != core.TaskAsk {
		space, err := a.space(ctx, note.SpaceID)
		if err != nil {
			return nil, err
		}
		if err := a.noteBody(ctx, space, note); err != nil {
			return nil, err
		}
		req.Text = note.Content
	}
	reserve := 0
	for _, c := range pre.Candidates {
		if mt := view.configs[c.Target.ProviderID].MaxTokens; mt > 0 && (reserve == 0 || mt < reserve) {
			reserve = mt
		}
	}
	budget := a.contextBudget()
	build := func(vision bool) ([]core.AIMessage, []core.AISource, int, int) {
		resolved := a.resolveContext(ctx, actor, req, spaceID, vision, budget)
		msgs := a.ai.BuildPrompt(req, ports.PromptInput{Sources: resolved.sources, Images: resolved.images, Budget: budget})
		runes, images := promptSize(msgs)
		return msgs, resolved.sources, runes, images
	}
	msgs, sources, runes, images := build(true)
	rreq.Needs.ImageIn = needs.ImageIn || images > 0
	rreq.Needs.MinContext = int(estimateTokens(runes)) + reserve
	plan := ai.ResolveRoute(rreq)
	if len(plan.Candidates) == 0 && images > 0 && !needs.ImageIn {
		// No model of the route accepts images, so they are left out.
		msgs, sources, runes, images = build(false)
		rreq.Needs.ImageIn = false
		rreq.Needs.MinContext = int(estimateTokens(runes)) + reserve
		plan = ai.ResolveRoute(rreq)
	}
	if len(plan.Candidates) == 0 {
		for _, s := range plan.Skipped {
			if s.Reason == ai.SkipContext {
				return nil, fmt.Errorf("the prompt is too long for every model of the %s task: %w", route, core.ErrTooLarge)
			}
		}
		return nil, errNoRoute(route)
	}
	call := &aiCall{route: route, msgs: msgs, sources: publicSources(sources), promptRunes: runes, budgets: budgets}
	for i, c := range plan.Candidates {
		price, _ := c.Model.Effective()
		rec := base
		rec.ProviderID, rec.Model, rec.Attempts, rec.ImagesIn = c.Target.ProviderID, c.Target.Model, i+1, int64(images)
		call.targets = append(call.targets, &callTarget{
			cfg: view.configs[c.Target.ProviderID], model: c.Target.Model, price: price, usage: rec,
		})
	}
	return call, nil
}

// retrieveContext returns the note chunks most similar to a question, from
// notes in one space the actor may view.
func (a *App) retrieveContext(ctx context.Context, actor *core.Actor, spaceID, question string) ([]core.AISource, error) {
	if strings.TrimSpace(question) == "" {
		return nil, nil
	}
	target, err := a.embeddingTarget(ctx)
	if err != nil {
		return nil, err
	}
	began := time.Now()
	vecs, err := target.provider.Embed(ctx, []string{question}, target.model)
	a.recordEmbed(ctx, actor.UserID(), core.FeatureSearch, spaceID, "", target, []string{question}, began, err)
	if err != nil {
		return nil, fmt.Errorf("embed question: %w", err)
	}
	if len(vecs) == 0 {
		return nil, nil
	}
	chunks, err := a.db.ListEmbeddings(ctx, []string{spaceID}, target.model)
	if err != nil {
		return nil, fmt.Errorf("list embeddings: %w", err)
	}
	ranked := a.rankChunks(vecs[0], chunks, false)
	ids := make([]string, 0, len(ranked))
	seen := map[string]bool{}
	for _, r := range ranked {
		if !seen[r.chunk.NoteID] {
			seen[r.chunk.NoteID] = true
			ids = append(ids, r.chunk.NoteID)
		}
	}
	notes, err := a.db.ListNotesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load notes: %w", err)
	}
	cache := a.grants(actor)
	allowed := map[string]*core.Note{}
	for _, n := range notes {
		if n.Deleted {
			continue
		}
		g, err := cache.note(ctx, n)
		if err != nil {
			return nil, err
		}
		if g.Access.AtLeast(core.AccessView) {
			allowed[n.ID] = n
		}
	}
	limit := a.cfg.AI.ContextChunks
	if limit <= 0 {
		limit = 8
	}
	out := make([]core.AISource, 0, limit)
	for _, r := range ranked {
		n, ok := allowed[r.chunk.NoteID]
		if !ok {
			continue
		}
		out = append(out, core.AISource{
			Kind:    core.ContextSearch,
			NoteID:  n.ID,
			Title:   n.Title,
			Path:    n.Path,
			Score:   r.score,
			Excerpt: r.chunk.Text,
		})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// failAttempt records the usage row of a failed attempt and marks the
// provider down when the error says it is failing.
func (a *App) failAttempt(ctx context.Context, call *aiCall, t *callTarget, began time.Time, err error) {
	a.noteCallError(ctx, t.cfg.ID, err)
	rec := t.usage
	rec.CreatedAt = a.now()
	rec.Status = usageStatus(ctx, err)
	rec.Error = err.Error()
	fillUsage(&rec, t.price, call.promptRunes, 0, 0, "", began)
	a.recordUsage(ctx, rec)
}

// RunAI runs one AI task to completion. It needs AccessView on the note the
// request refers to. A target that fails with a retryable error hands the
// call to the next target of the route.
func (a *App) RunAI(ctx context.Context, actor *core.Actor, req core.AIRequest) (*core.AIResult, error) {
	call, err := a.prepareAI(ctx, actor, req)
	if err != nil {
		return nil, err
	}
	var (
		res     *core.AIResult
		began   time.Time
		started time.Time
	)
	last, tried, err := ai.Fallback(ctx, len(call.targets), func(i int) (bool, error) {
		t := call.targets[i]
		began, started = time.Now(), a.now()
		prov, err := a.ai.New(t.cfg)
		if err != nil {
			return false, fmt.Errorf("build ai provider %s: %w: %w", t.cfg.ID, err, core.ErrUnavailable)
		}
		out, err := prov.Complete(ctx, call.msgs, t.options())
		if err != nil {
			a.failAttempt(ctx, call, t, began, err)
			return false, err
		}
		if out == nil {
			out = &core.AIResult{}
		}
		res = out
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("ai completion: %w", err)
	}
	t := call.targets[last]
	if res.Provider == "" {
		res.Provider = t.cfg.ID
	}
	if res.Model == "" {
		res.Model = t.model
	}
	if res.Elapsed == 0 {
		res.Elapsed = time.Since(began)
	}
	res.Route, res.Attempts = call.route, tried
	res.Sources = call.sources
	rec := t.usage
	rec.Model, rec.Status, rec.CreatedAt, rec.Attempts = res.Model, core.UsageOK, started, tried
	fillUsage(&rec, t.price, call.promptRunes, res.PromptTokens, res.CompletionTokens, res.Text, began)
	a.recordUsage(ctx, rec)
	res.Budget = budgetWarning(call.budgets, rec.InputTokens+rec.OutputTokens, rec.CostMicros)
	a.log.Info("ai task", "task", req.Task, "route", call.route, "provider", res.Provider, "model", res.Model,
		"attempts", tried, "userId", actor.UserID(), "elapsed", res.Elapsed.String())
	return res, nil
}

// StreamAI runs one AI task as a stream. It needs AccessView on the note the
// request refers to. A target whose stream fails before its first chunk
// hands the call to the next target. Once a chunk reached the caller the
// stream stays with its target. The Done chunk carries the context sources.
func (a *App) StreamAI(ctx context.Context, actor *core.Actor, req core.AIRequest) (<-chan core.AIChunk, error) {
	call, err := a.prepareAI(ctx, actor, req)
	if err != nil {
		return nil, err
	}
	var (
		stream <-chan core.AIChunk
		first  core.AIChunk
		began  time.Time
	)
	last, tried, err := ai.Fallback(ctx, len(call.targets), func(i int) (bool, error) {
		t := call.targets[i]
		began = time.Now()
		t.usage.CreatedAt = a.now()
		prov, err := a.ai.New(t.cfg)
		if err != nil {
			return false, fmt.Errorf("build ai provider %s: %w: %w", t.cfg.ID, err, core.ErrUnavailable)
		}
		ch, err := prov.Stream(ctx, call.msgs, t.options())
		if err == nil {
			first, err = firstChunk(ctx, ch)
		}
		if err != nil {
			a.failAttempt(ctx, call, t, began, err)
			return false, err
		}
		stream = ch
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("ai stream: %w", err)
	}
	t := call.targets[last]
	t.usage.Attempts = tried
	return a.meterStream(ctx, first, stream, call, t, began), nil
}

// errStreamEnded reports a stream that closed before its first chunk.
var errStreamEnded = fmt.Errorf("the stream ended before any output: %w", core.ErrUnavailable)

// firstChunk waits for the first chunk of a stream. An error chunk or a
// stream that closes first is a failure, and the rest of the stream is
// drained.
func firstChunk(ctx context.Context, ch <-chan core.AIChunk) (core.AIChunk, error) {
	select {
	case c, ok := <-ch:
		switch {
		case !ok:
			if ctx.Err() != nil {
				return c, ctx.Err()
			}
			return c, errStreamEnded
		case c.Err != nil:
			go func() {
				for range ch {
				}
			}()
			return c, c.Err
		}
		return c, nil
	case <-ctx.Done():
		go func() {
			for range ch {
			}
		}()
		return core.AIChunk{}, ctx.Err()
	}
}

// imageTarget is one model of an image route with its generator.
type imageTarget struct {
	gen   core.ImageGenerator
	cfg   *core.AIProviderConfig
	model string
	price *core.AIPrice
}

// imageTargets resolves the image.generate route, or image.edit when edit is
// set, for the actor and returns the generators in route order, for a caller
// that tries them with ai.Fallback. A target whose provider cannot build a
// generator is left out. A used-up budget returns core.ErrQuotaExceeded.
func (a *App) imageTargets(ctx context.Context, actor *core.Actor, edit bool, requested *core.AIRouteTarget) ([]imageTarget, error) {
	out, _, err := a.imageTargetsFor(ctx, actor, edit, "", "", requested)
	return out, err
}

// ReindexEmbeddings rebuilds the semantic index for one space, or for every
// space the actor manages when spaceID is empty. A note whose content and
// embedding model are unchanged is skipped. It returns the number of notes
// embedded.
func (a *App) ReindexEmbeddings(ctx context.Context, actor *core.Actor, spaceID string) (int, error) {
	if err := requireUser(actor); err != nil {
		return 0, err
	}
	if _, err := a.aiReady(ctx); err != nil {
		return 0, err
	}
	target, err := a.embeddingTarget(ctx)
	if err != nil {
		return 0, err
	}
	var spaces []*core.Space
	if spaceID != "" {
		sp, _, err := a.requireSpace(ctx, actor, spaceID, core.AccessManage)
		if err != nil {
			return 0, err
		}
		spaces = []*core.Space{sp}
	} else {
		all, err := a.db.ListSpaces(ctx)
		if err != nil {
			return 0, fmt.Errorf("list spaces: %w", err)
		}
		cache := a.grants(actor)
		for _, sp := range all {
			g, err := cache.space(ctx, sp.ID)
			if err != nil {
				return 0, err
			}
			if g.Access.AtLeast(core.AccessManage) {
				spaces = append(spaces, sp)
			}
		}
	}
	total := 0
	for _, sp := range spaces {
		n, err := a.embedSpace(ctx, actor.UserID(), target, sp)
		total += n
		if err != nil {
			return total, err
		}
	}
	a.log.Info("embeddings reindexed", "spaces", len(spaces), "notes", total, "provider", target.cfg.ID, "model", target.model)
	return total, nil
}

// embedSpace embeds the changed notes of one space. The stored hash covers
// the model and the content, so a new embedding model embeds every note
// again. Each embedding request records one usage row for userID.
func (a *App) embedSpace(ctx context.Context, userID string, target *embedTarget, space *core.Space) (int, error) {
	hashes, err := a.db.EmbeddingHashes(ctx, space.ID)
	if err != nil {
		return 0, fmt.Errorf("embedding hashes: %w", err)
	}
	ids, err := a.db.NoteIDsInSpace(ctx, space.ID)
	if err != nil {
		return 0, fmt.Errorf("note ids: %w", err)
	}
	notes, err := a.db.ListNotesByIDs(ctx, ids)
	if err != nil {
		return 0, fmt.Errorf("load notes: %w", err)
	}
	count := 0
	for _, note := range notes {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		if err := a.loadBody(ctx, space, note); err != nil {
			a.log.Debug("skip note without file", "noteId", note.ID, "err", err)
			continue
		}
		sum := sha256.Sum256([]byte(target.model + "\x00" + note.Content))
		hash := hex.EncodeToString(sum[:])
		if hashes[note.ID] == hash {
			continue
		}
		texts := a.rend.Chunks(note.Content, embedChunkRunes)
		now := a.now()
		chunks := make([]*core.EmbeddingChunk, 0, len(texts))
		for start := 0; start < len(texts); start += embedBatch {
			end := start + embedBatch
			if end > len(texts) {
				end = len(texts)
			}
			began := time.Now()
			vecs, err := target.provider.Embed(ctx, texts[start:end], target.model)
			a.recordEmbed(ctx, userID, core.FeatureEmbeddings, space.ID, note.ID, target, texts[start:end], began, err)
			if err != nil {
				return count, fmt.Errorf("embed note %s: %w", note.ID, err)
			}
			if len(vecs) != end-start {
				return count, fmt.Errorf("embed note %s: got %d vectors for %d chunks: %w",
					note.ID, len(vecs), end-start, core.ErrInvalid)
			}
			for i, v := range vecs {
				chunks = append(chunks, &core.EmbeddingChunk{
					ID:        core.NewID(),
					NoteID:    note.ID,
					SpaceID:   space.ID,
					Ordinal:   start + i,
					Text:      texts[start+i],
					Vector:    v,
					Model:     target.model,
					Hash:      hash,
					UpdatedAt: now,
				})
			}
		}
		if err := a.db.ReplaceEmbeddings(ctx, note.ID, space.ID, chunks); err != nil {
			return count, fmt.Errorf("store embeddings: %w", err)
		}
		count++
	}
	return count, nil
}

// validKind reports whether k is a supported provider protocol.
func validKind(k core.AIProviderKind) bool {
	for _, v := range core.AIProviderKinds {
		if v == k {
			return true
		}
	}
	return false
}
