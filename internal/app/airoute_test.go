package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/ai"
	"github.com/gamemaker1/markspace/internal/core"
)

// addProvider appends a configuration file provider to the harness.
func addProvider(h *harness, p core.AIProviderConfig) {
	if p.Name == "" {
		p.Name = p.ID
	}
	if p.BaseURL == "" {
		p.BaseURL = "http://127.0.0.1:9"
	}
	if p.MaxTokens == 0 {
		p.MaxTokens = 100
	}
	p.Enabled = true
	h.cfg.AI.Providers = append(h.cfg.AI.Providers, p)
}

// fallbackHarness has two text providers and an answer route of local, then
// backup.
func fallbackHarness(t *testing.T) (*harness, *core.Actor) {
	t.Helper()
	h := newHarness(t)
	addProvider(h, core.AIProviderConfig{ID: "backup", Kind: core.ProviderOpenAI, Model: "backup-chat"})
	admin := h.user("Admin", true)
	_, err := h.app.SaveAIRoute(h.ctx, admin, core.RouteScopeInstance, core.RouteAnswer, []core.AIRouteTarget{
		{ProviderID: "local", Model: "chat"}, {ProviderID: "backup", Model: "backup-chat"},
	})
	if err != nil {
		t.Fatalf("SaveAIRoute: %v", err)
	}
	return h, admin
}

// recordModels makes the fake provider fail or answer by model and records
// the models it was called with.
func recordModels(h *harness, fail map[string]error) *[]string {
	var calls []string
	h.ai.mu.Lock()
	defer h.ai.mu.Unlock()
	h.ai.complete = func(_ context.Context, _ []core.AIMessage, opts core.AIOptions) (*core.AIResult, error) {
		calls = append(calls, opts.Model)
		if err := fail[opts.Model]; err != nil {
			return nil, err
		}
		return &core.AIResult{Text: "from " + opts.Model, PromptTokens: 4, CompletionTokens: 2}, nil
	}
	return &calls
}

func TestRunAIFallsBackOn503ThenSucceeds(t *testing.T) {
	h, _ := fallbackHarness(t)
	u := h.user("Writer", false)
	calls := recordModels(h, map[string]error{"chat": &ai.APIError{Provider: "openai", Status: 503}})

	res, err := h.app.RunAI(h.ctx, u, summarize)
	if err != nil {
		t.Fatalf("RunAI: %v", err)
	}
	if res.Provider != "backup" || res.Model != "backup-chat" || res.Attempts != 2 || res.Route != core.RouteAnswer {
		t.Fatalf("result = %+v", res)
	}
	rows := usageOf(h, u.UserID())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if r := rows[0]; r.Status != core.UsageError || r.ProviderID != "local" || r.Attempts != 1 || r.Task != core.RouteAnswer || r.InputTokens != 0 {
		t.Fatalf("failed attempt row = %+v", r)
	}
	if r := rows[1]; r.Status != core.UsageOK || r.ProviderID != "backup" || r.Attempts != 2 || r.Task != core.RouteAnswer || r.InputTokens != 4 {
		t.Fatalf("served row = %+v", r)
	}

	// The failing provider is skipped for 60 s.
	if res, err = h.app.RunAI(h.ctx, u, summarize); err != nil || res.Attempts != 1 || res.Provider != "backup" {
		t.Fatalf("second call = %+v, %v", res, err)
	}
	h.clock.Advance(61 * time.Second)
	if _, err = h.app.RunAI(h.ctx, u, summarize); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*calls, ","); got != "chat,backup-chat,backup-chat,chat,backup-chat" {
		t.Fatalf("calls = %s", got)
	}

	// A context-length error moves on without marking the provider down.
	calls = recordModels(h, map[string]error{"chat": &ai.APIError{Status: 400, Body: `{"error":{"code":"context_length_exceeded"}}`}})
	h.clock.Advance(61 * time.Second)
	for i := 0; i < 2; i++ {
		if res, err = h.app.RunAI(h.ctx, u, summarize); err != nil || res.Provider != "backup" {
			t.Fatalf("context-length fallback = %+v, %v", res, err)
		}
	}
	if got := strings.Join(*calls, ","); got != "chat,backup-chat,chat,backup-chat" {
		t.Fatalf("context-length calls = %s", got)
	}
}

func TestRunAIDoesNotFallBackOnFinalErrors(t *testing.T) {
	for name, failure := range map[string]error{
		"400":     &ai.APIError{Provider: "openai", Status: 400, Body: "temperature must be at most 2"},
		"401":     &ai.APIError{Provider: "openai", Status: 401},
		"refusal": errors.New("openai: the request was refused"),
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := fallbackHarness(t)
			u := h.user("Writer", false)
			calls := recordModels(h, map[string]error{"chat": failure})
			if _, err := h.app.RunAI(h.ctx, u, summarize); err == nil {
				t.Fatal("RunAI succeeded after a final error")
			}
			if got := strings.Join(*calls, ","); got != "chat" {
				t.Fatalf("calls = %s, want only the first target", got)
			}
			rows := usageOf(h, u.UserID())
			if len(rows) != 1 || rows[0].Status != core.UsageError || rows[0].Attempts != 1 {
				t.Fatalf("rows = %+v", rows)
			}
			if h.app.providerIsDown("local") {
				t.Fatal("a final error marked the provider down")
			}
		})
	}
}

func TestStreamFallsBackOnlyBeforeFirstDelta(t *testing.T) {
	h, _ := fallbackHarness(t)
	u := h.user("Streamer", false)
	var mode string
	var calls []string
	setStream(h, func(ctx context.Context, _ []core.AIMessage, opts core.AIOptions) (<-chan core.AIChunk, error) {
		calls = append(calls, opts.Model)
		ch := make(chan core.AIChunk, 3)
		if opts.Model == "chat" {
			switch mode {
			case "start":
				return nil, &ai.APIError{Status: 502}
			case "first":
				ch <- core.AIChunk{Err: &ai.APIError{Status: 503}}
			case "late":
				ch <- core.AIChunk{Delta: "half an ans"}
				ch <- core.AIChunk{Err: &ai.APIError{Status: 503}}
			}
			close(ch)
			return ch, nil
		}
		ch <- core.AIChunk{Delta: "backup answer"}
		ch <- core.AIChunk{Done: true}
		close(ch)
		return ch, nil
	})

	for _, m := range []string{"start", "first"} {
		mode, calls = m, nil
		h.clock.Advance(61 * time.Second)
		ch, err := h.app.StreamAI(h.ctx, u, summarize)
		if err != nil {
			t.Fatalf("%s: StreamAI: %v", m, err)
		}
		text, done, err := drainUsageStream(t, ch)
		if err != nil || text != "backup answer" || done == nil || done.Provider != "backup" || done.Attempts != 2 || done.Model != "backup-chat" {
			t.Fatalf("%s: text %q done %+v err %v", m, text, done, err)
		}
		if strings.Join(calls, ",") != "chat,backup-chat" {
			t.Fatalf("%s: calls = %v", m, calls)
		}
		rows := usageOf(h, u.UserID())
		if last := rows[len(rows)-1]; last.Status != core.UsageOK || last.ProviderID != "backup" || last.Attempts != 2 {
			t.Fatalf("%s: served row = %+v", m, last)
		}
		if prev := rows[len(rows)-2]; prev.Status != core.UsageError || prev.ProviderID != "local" || prev.Attempts != 1 {
			t.Fatalf("%s: failed row = %+v", m, prev)
		}
	}

	mode, calls = "late", nil
	h.clock.Advance(61 * time.Second)
	ch, err := h.app.StreamAI(h.ctx, u, summarize)
	if err != nil {
		t.Fatalf("late: StreamAI: %v", err)
	}
	text, done, err := drainUsageStream(t, ch)
	if err == nil || done != nil || text != "half an ans" {
		t.Fatalf("late: text %q done %+v err %v", text, done, err)
	}
	if strings.Join(calls, ",") != "chat" {
		t.Fatalf("late: the stream fell back after text reached the caller: %v", calls)
	}
	if last := lastUsageOf(t, h, u.UserID()); last.Status != core.UsageError || last.ProviderID != "local" || last.Attempts != 1 {
		t.Fatalf("late: row = %+v", last)
	}
}

func TestVisionTaskPicksVisionModel(t *testing.T) {
	h := newHarness(t)
	addProvider(h, core.AIProviderConfig{ID: "eye", Kind: core.ProviderOpenAI, Model: "eye-model", Vision: true})
	u := h.user("Viewer", false)

	res, err := h.app.RunAI(h.ctx, u, core.AIRequest{Task: core.TaskDescribe, Text: "a photo"})
	if err != nil || res.Provider != "eye" || res.Route != core.RouteVision {
		t.Fatalf("describe = %+v, %v", res, err)
	}
	if res, err = h.app.RunAI(h.ctx, u, summarize); err != nil || res.Provider != "local" {
		t.Fatalf("summarize = %+v, %v", res, err)
	}

	// Without any vision model a vision task fails with a request error.
	h.cfg.AI.Providers = h.cfg.AI.Providers[:1]
	if _, err := h.app.RunAI(h.ctx, u, core.AIRequest{Task: core.TaskExtract, Text: "scan"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("extract without a vision model = %v", err)
	}
}

func TestEmbeddingsRouteUsesOnlyItsFirstTarget(t *testing.T) {
	h := newHarness(t)
	addProvider(h, core.AIProviderConfig{ID: "vec", Kind: core.ProviderOllama, EmbedModel: "vec-embed"})
	admin := h.user("Admin", true)
	_, err := h.app.SaveAIRoute(h.ctx, admin, core.RouteScopeInstance, core.RouteEmbeddings, []core.AIRouteTarget{
		{ProviderID: "local", Model: "embed"}, {ProviderID: "vec", Model: "vec-embed"},
	})
	if err != nil {
		t.Fatalf("SaveAIRoute: %v", err)
	}
	target, err := h.app.embeddingTarget(h.ctx)
	if err != nil || target.cfg.ID != "local" || target.model != "embed" {
		t.Fatalf("embedding target = %+v, %v", target, err)
	}
	disabled := &core.AIModel{ModelID: "embed", Inputs: []string{"text"}, Outputs: []string{"embedding"}, Enabled: false}
	if err := h.db.UpsertAIModels(h.ctx, "local", []*core.AIModel{disabled}, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.embeddingTarget(h.ctx); !errors.Is(err, errNoEmbeddings) {
		t.Fatalf("a disabled first target fell back to another model: %v", err)
	}
	if _, err := h.app.SaveAIRoute(h.ctx, admin, core.RouteScopeInstance, core.RouteEmbeddings,
		[]core.AIRouteTarget{{ProviderID: "local", Model: "chat"}}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("a chat model in the embeddings route = %v", err)
	}
}

func TestRoutesAndCatalogVisibilityForNonAdmins(t *testing.T) {
	h := newHarness(t)
	admin := h.user("Admin", true)
	alice := h.user("Alice", false)
	bob := h.user("Bob", false)

	mine, err := h.app.SaveAIProvider(h.ctx, alice, &core.AIProviderConfig{
		Name: "Mine", Kind: core.ProviderOllama, BaseURL: "http://127.0.0.1:3", Model: "mine-model", Enabled: true,
	})
	if err != nil {
		t.Fatalf("personal provider: %v", err)
	}
	if mine.ModelCount != 1 || mine.OwnerID != alice.UserID() {
		t.Fatalf("personal provider = %+v", mine)
	}
	hidden := &core.AIModel{ModelID: "hidden", Inputs: []string{"text"}, Outputs: []string{"text"}, Enabled: true}
	shown := &core.AIModel{ModelID: "chat", Inputs: []string{"text"}, Outputs: []string{"text"}, Enabled: true}
	if err := h.db.UpsertAIModels(h.ctx, "local", []*core.AIModel{hidden, shown}, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.UpdateAIModel(h.ctx, admin, "local", "hidden", core.AIModelUpdate{Enabled: new(bool)}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	ids := func(actor *core.Actor) string {
		t.Helper()
		models, err := h.app.ListAIModels(h.ctx, actor, core.AIModelQuery{})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range models {
			out = append(out, m.ProviderID+"/"+m.ModelID)
		}
		return strings.Join(out, ",")
	}
	if got := ids(bob); got != "local/chat,local/embed" {
		t.Fatalf("bob sees %s", got)
	}
	if got := ids(alice); !strings.Contains(got, mine.ID+"/mine-model") || strings.Contains(got, "hidden") {
		t.Fatalf("alice sees %s", got)
	}
	if got := ids(admin); !strings.Contains(got, "local/hidden") || strings.Contains(got, "mine-model") {
		t.Fatalf("admin sees %s", got)
	}

	if _, err := h.app.UpdateAIModel(h.ctx, bob, "local", "chat", core.AIModelUpdate{Enabled: new(bool)}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("bob updated a model: %v", err)
	}
	if _, err := h.app.RefreshAIPrices(h.ctx, bob); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("bob refreshed prices: %v", err)
	}
	if _, err := h.app.RefreshAIProvider(h.ctx, bob, "local"); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("bob refreshed an instance provider: %v", err)
	}
	if _, err := h.app.RefreshAIProvider(h.ctx, bob, mine.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("bob refreshed alice's provider: %v", err)
	}
	if _, err := h.app.RefreshAIProvider(h.ctx, alice, mine.ID); err != nil {
		t.Fatalf("alice refreshed her provider: %v", err)
	}
	route := []core.AIRouteTarget{{ProviderID: mine.ID, Model: "mine-model"}}
	if _, err := h.app.SaveAIRoute(h.ctx, bob, core.RouteScopeInstance, core.RouteEdit, route); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("bob saved an instance route: %v", err)
	}
	if _, err := h.app.SaveAIRoute(h.ctx, bob, "me", core.RouteEdit, route); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bob routed to alice's provider: %v", err)
	}
	if _, err := h.app.SaveAIRoute(h.ctx, admin, core.RouteScopeInstance, core.RouteEdit, route); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("a personal provider in an instance route: %v", err)
	}
	if _, err := h.app.SaveAIRoute(h.ctx, alice, "me", core.RouteEdit, route); err != nil {
		t.Fatalf("alice routed to her provider: %v", err)
	}
	improve := core.AIRequest{Task: core.TaskImprove, Text: "some text"}
	if res, err := h.app.RunAI(h.ctx, alice, improve); err != nil || res.Provider != mine.ID {
		t.Fatalf("alice's edit = %+v, %v", res, err)
	}
	if res, err := h.app.RunAI(h.ctx, bob, improve); err != nil || res.Provider != "local" {
		t.Fatalf("bob's edit = %+v, %v", res, err)
	}
	routes, err := h.app.ListAIRoutes(h.ctx, alice)
	if err != nil || !routes.AllowUserRoutes || len(routes.User) != 1 || len(routes.Instance) == 0 {
		t.Fatalf("alice's routes = %+v, %v", routes, err)
	}
	for _, r := range routes.Instance {
		for _, tg := range r.Targets {
			if tg.ProviderID == mine.ID {
				t.Fatalf("a personal provider in the instance routes: %+v", routes.Instance)
			}
		}
	}

	if _, err := h.app.UpdateSettings(h.ctx, admin, &core.Settings{InstanceName: "T", AIEnabled: true, AIAllowUserKeys: true}); err != nil {
		t.Fatal(err)
	}
	if routes, _ = h.app.ListAIRoutes(h.ctx, alice); routes.AllowUserRoutes || routes.User != nil {
		t.Fatalf("user routes listed while off: %+v", routes)
	}
	if _, err := h.app.SaveAIRoute(h.ctx, alice, "me", core.RouteEdit, route); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("user route saved while off: %v", err)
	}
	if res, err := h.app.RunAI(h.ctx, alice, improve); err != nil || res.Provider != "local" {
		t.Fatalf("a user route applied while off: %+v, %v", res, err)
	}
	if err := h.app.DeleteAIRoute(h.ctx, alice, core.RouteEdit); err != nil {
		t.Fatal(err)
	}
	if err := h.app.DeleteAIRoute(h.ctx, alice, core.RouteEdit); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestSaveAIRouteValidation(t *testing.T) {
	h := newHarness(t)
	addProvider(h, core.AIProviderConfig{ID: "pics", Kind: core.ProviderComfyUI, ImageGeneration: true, ImageModel: "sdxl.safetensors"})
	admin := h.user("Admin", true)
	local := core.AIRouteTarget{ProviderID: "local", Model: "chat"}
	many := make([]core.AIRouteTarget, core.MaxRouteTargets+1)
	cases := map[string]struct {
		task    string
		targets []core.AIRouteTarget
	}{
		"unknown task":     {"summarise", []core.AIRouteTarget{local}},
		"missing model":    {core.RouteEdit, []core.AIRouteTarget{{ProviderID: "local", Model: "gpt-9"}}},
		"missing provider": {core.RouteEdit, []core.AIRouteTarget{{ProviderID: "nope", Model: "chat"}}},
		"embedding model":  {core.RouteEdit, []core.AIRouteTarget{{ProviderID: "local", Model: "embed"}}},
		"text for vision":  {core.RouteVision, []core.AIRouteTarget{local}},
		"comfyui text":     {core.RouteDefault, []core.AIRouteTarget{{ProviderID: "pics", Model: "sdxl.safetensors"}}},
		"duplicate":        {core.RouteEdit, []core.AIRouteTarget{local, local}},
		"too many":         {core.RouteEdit, many},
		"empty model":      {core.RouteEdit, []core.AIRouteTarget{{ProviderID: "local"}}},
	}
	for name, c := range cases {
		if _, err := h.app.SaveAIRoute(h.ctx, admin, core.RouteScopeInstance, c.task, c.targets); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := h.app.SaveAIRoute(h.ctx, admin, "everyone", core.RouteEdit, nil); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("unknown scope = %v", err)
	}
	for _, task := range []string{core.RouteEdit, "plugin.bookmark-wiki.summarise"} {
		if r, err := h.app.SaveAIRoute(h.ctx, admin, core.RouteScopeInstance, task, []core.AIRouteTarget{local}); err != nil || len(r.Targets) != 1 {
			t.Errorf("%s: %+v, %v", task, r, err)
		}
	}
	if r, err := h.app.SaveAIRoute(h.ctx, admin, core.RouteScopeInstance, core.RouteImageGenerate,
		[]core.AIRouteTarget{{ProviderID: "pics", Model: "sdxl.safetensors"}}); err != nil || len(r.Targets) != 1 {
		t.Errorf("image route: %+v, %v", r, err)
	}
	if r, err := h.app.SaveAIRoute(h.ctx, admin, core.RouteScopeInstance, core.RouteChat, nil); err != nil || len(r.Targets) != 0 {
		t.Errorf("empty route: %+v, %v", r, err)
	}
}

func TestImageTargetsAndComfyNeverServesText(t *testing.T) {
	h := newHarness(t)
	addProvider(h, core.AIProviderConfig{ID: "pics", Kind: core.ProviderComfyUI, ImageGeneration: true, ImageModel: "sdxl.safetensors"})
	u := h.user("Painter", false)
	targets, err := h.app.imageTargets(h.ctx, u, false, nil)
	if err != nil || len(targets) != 1 || targets[0].cfg.ID != "pics" || targets[0].model != "sdxl.safetensors" {
		t.Fatalf("generate targets = %+v, %v", targets, err)
	}
	if out, err := targets[0].gen.GenerateImages(h.ctx, core.AIImageRequest{Model: targets[0].model}, nil); err != nil || len(out.Images) != 1 {
		t.Fatalf("generate = %+v, %v", out, err)
	}
	if _, err := h.app.imageTargets(h.ctx, u, true, nil); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("edit without an editing provider = %v", err)
	}
	models, err := h.app.ListAIModels(h.ctx, u, core.AIModelQuery{Capability: core.RouteImageGenerate})
	if err != nil || len(models) != 1 || models[0].ProviderID != "pics" {
		t.Fatalf("image models = %+v, %v", models, err)
	}
	if _, err := h.app.ListAIModels(h.ctx, u, core.AIModelQuery{Capability: "telepathy"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown capability = %v", err)
	}
	if res, err := h.app.RunAI(h.ctx, u, summarize); err != nil || res.Provider != "local" {
		t.Fatalf("text call = %+v, %v", res, err)
	}
	h.cfg.AI.Providers = h.cfg.AI.Providers[1:]
	if _, err := h.app.RunAI(h.ctx, u, summarize); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("a comfyui provider served text: %v", err)
	}
}

func TestSaveAIProviderPresetsImagesAndTimeouts(t *testing.T) {
	h := newHarness(t)
	admin := h.user("Admin", true)
	or, err := h.app.SaveAIProvider(h.ctx, admin, &core.AIProviderConfig{Preset: "openrouter", APIKey: "k", Enabled: true})
	if err != nil {
		t.Fatalf("openrouter preset: %v", err)
	}
	if or.ID != "openrouter" || or.Name != "OpenRouter" || or.Kind != core.ProviderOpenAI ||
		or.BaseURL != "https://openrouter.ai/api/v1" || or.TimeoutSeconds != 120 || or.Local || !or.HasAPIKey {
		t.Fatalf("openrouter = %+v", or)
	}
	comfy, err := h.app.SaveAIProvider(h.ctx, admin, &core.AIProviderConfig{Name: "Pics", Preset: "comfyui",
		BaseURL: "http://127.0.0.1:8188", ImageGeneration: true, Enabled: true})
	if err != nil || comfy.Kind != core.ProviderComfyUI || !comfy.Local || comfy.TimeoutSeconds != 300 {
		t.Fatalf("comfyui = %+v, %v", comfy, err)
	}
	img, err := h.app.SaveAIProvider(h.ctx, admin, &core.AIProviderConfig{Name: "Img", Kind: core.ProviderOpenAI,
		BaseURL: "http://127.0.0.1:8000/v1", ImageEditing: true})
	if err != nil || img.TimeoutSeconds != 300 {
		t.Fatalf("image openai = %+v, %v", img, err)
	}
	for name, cfg := range map[string]*core.AIProviderConfig{
		"image flag on ollama": {Name: "Bad", Kind: core.ProviderOllama, BaseURL: "http://x", ImageGeneration: true},
		"bad size":             {Name: "Bad", Kind: core.ProviderOpenAI, BaseURL: "http://x", ImageSizes: []string{"12x12"}},
		"unknown preset":       {Name: "Bad", Preset: "skynet"},
		"comfyui embeddings":   {Name: "Bad", Preset: "comfyui", BaseURL: "http://x", EmbedModel: "e"},
		"no base URL":          {Name: "Bad", Kind: core.ProviderOllama},
	} {
		if _, err := h.app.SaveAIProvider(h.ctx, admin, cfg); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestPriceSourcesPrecedenceAndFailedRefresh(t *testing.T) {
	h := newHarness(t)
	admin := h.user("Admin", true)
	read := func(name string) []byte {
		body, err := os.ReadFile(filepath.Join("..", "ai", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	fetcher := &fakeFetcher{bodies: map[string][]byte{
		ai.OpenRouterCatalogURL: read("openrouter-catalog.json"),
		ai.LiteLLMCatalogURL:    read("litellm-catalog.json"),
	}}
	h.app.prices = fetcher
	addProvider(h, core.AIProviderConfig{ID: "cloud", Preset: "openai", Kind: core.ProviderOpenAI, Model: "gpt-4o"})
	addProvider(h, core.AIProviderConfig{ID: "groq", Preset: "groq", Kind: core.ProviderOpenAI, Model: "llama-3.3-70b-versatile"})
	addProvider(h, core.AIProviderConfig{ID: "box", Preset: "ollama", Kind: core.ProviderOllama, Local: true, Model: "llama3.2"})
	addProvider(h, core.AIProviderConfig{ID: "gw", Kind: core.ProviderOpenAI, Model: "gateway-model"})
	listingErr := error(nil)
	setListing(h.ai, func(cfg *core.AIProviderConfig) ([]core.AIModel, error) {
		switch cfg.ID {
		case "cloud":
			if listingErr != nil {
				return nil, listingErr
			}
			return []core.AIModel{{ModelID: "gpt-4o"}, {ModelID: "gpt-4o-mini"}, {ModelID: "mystery-model"}}, nil
		case "groq":
			return []core.AIModel{{ModelID: "llama-3.3-70b-versatile"}}, nil
		case "box":
			return []core.AIModel{{ModelID: "llama3.2"}}, nil
		case "gw":
			return []core.AIModel{{ModelID: "gateway-model", Price: &core.AIPrice{InputPerMTok: 9, OutputPerMTok: 9},
				PriceSource: core.PriceSourceProvider}}, nil
		}
		return []core.AIModel{{ModelID: cfg.Model}}, nil
	})

	refresh, err := h.app.RefreshAIPrices(h.ctx, admin)
	if err != nil || len(refresh.Sources) != 2 {
		t.Fatalf("RefreshAIPrices = %+v, %v", refresh, err)
	}
	for _, s := range refresh.Sources {
		if s.Error != "" || s.Models < 20 || s.FetchedAt.IsZero() {
			t.Fatalf("source = %+v", s)
		}
	}
	for _, id := range []string{"cloud", "groq", "box", "gw"} {
		if _, err := h.app.RefreshAIProvider(h.ctx, admin, id); err != nil {
			t.Fatalf("refresh %s: %v", id, err)
		}
	}
	check := func(provider, model, source, matched string, in, out float64) {
		t.Helper()
		m, err := h.db.GetAIModel(h.ctx, provider, model)
		if err != nil {
			t.Fatalf("%s/%s: %v", provider, model, err)
		}
		price, src := m.Effective()
		if src != source || m.PriceMatched != matched {
			t.Fatalf("%s/%s: source %s matched %q, want %s %q", provider, model, src, m.PriceMatched, source, matched)
		}
		if source == core.PriceSourceNone {
			if price != nil {
				t.Fatalf("%s/%s: price %+v, want none", provider, model, price)
			}
			return
		}
		if price == nil || price.InputPerMTok != in || price.OutputPerMTok != out {
			t.Fatalf("%s/%s: price %+v, want %v and %v", provider, model, price, in, out)
		}
	}
	check("cloud", "gpt-4o", core.PriceSourceOpenRouter, "openai/gpt-4o", 2.5, 10)
	check("cloud", "gpt-4o-mini", core.PriceSourceOpenRouter, "openai/gpt-4o-mini", 0.15, 0.6)
	check("cloud", "mystery-model", core.PriceSourceNone, "", 0, 0)
	check("groq", "llama-3.3-70b-versatile", core.PriceSourceLiteLLM, "groq/llama-3.3-70b-versatile", 0.59, 0.79)
	check("box", "llama3.2", core.PriceSourceLocal, "", 0, 0)
	check("gw", "gateway-model", core.PriceSourceProvider, "", 9, 9)

	// An override wins over every fetched price, and clearing it returns to
	// the fetched one.
	over := core.AIPrice{InputPerMTok: 1, OutputPerMTok: 2}
	if m, err := h.app.UpdateAIModel(h.ctx, admin, "cloud", "gpt-4o", core.AIModelUpdate{SetPrice: true, PriceOverride: &over}); err != nil ||
		m.EffectiveSource != core.PriceSourceOverride || *m.EffectivePrice != over {
		t.Fatalf("override = %+v, %v", m, err)
	}
	check("cloud", "gpt-4o", core.PriceSourceOverride, "openai/gpt-4o", 1, 2)
	if _, err := h.app.UpdateAIModel(h.ctx, admin, "box", "llama3.2", core.AIModelUpdate{SetPrice: true, PriceOverride: &core.AIPrice{InputPerMTok: 0.01}}); err != nil {
		t.Fatal(err)
	}
	check("box", "llama3.2", core.PriceSourceOverride, "", 0.01, 0)
	bad := core.AIPrice{InputPerMTok: -1}
	if _, err := h.app.UpdateAIModel(h.ctx, admin, "cloud", "gpt-4o", core.AIModelUpdate{SetPrice: true, PriceOverride: &bad}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("negative override = %v", err)
	}
	if _, err := h.app.UpdateAIModel(h.ctx, admin, "cloud", "gpt-4o", core.AIModelUpdate{SetPrice: true}); err != nil {
		t.Fatal(err)
	}
	check("cloud", "gpt-4o", core.PriceSourceOpenRouter, "openai/gpt-4o", 2.5, 10)

	// A failed download keeps the last copy and the prices.
	first := refresh.Sources[0].FetchedAt
	h.clock.Advance(time.Hour)
	fetcher.mu.Lock()
	fetcher.err = errors.New("network is unreachable")
	fetcher.mu.Unlock()
	failed, err := h.app.RefreshAIPrices(h.ctx, admin)
	if err != nil || failed.Sources[0].Error == "" || !failed.Sources[0].FetchedAt.Equal(first) || failed.Sources[0].Models != refresh.Sources[0].Models {
		t.Fatalf("failed refresh = %+v, %v", failed, err)
	}
	check("cloud", "gpt-4o", core.PriceSourceOpenRouter, "openai/gpt-4o", 2.5, 10)
	// A body that does not parse also keeps the last copy.
	fetcher.mu.Lock()
	fetcher.err = nil
	fetcher.bodies[ai.OpenRouterCatalogURL] = []byte("<html>rate limited</html>")
	fetcher.mu.Unlock()
	if bad, _ := h.app.RefreshAIPrices(h.ctx, admin); bad.Sources[0].Error == "" || !bad.Sources[0].FetchedAt.Equal(first) {
		t.Fatalf("unparsable refresh = %+v", bad.Sources[0])
	}
	stored, at, err := h.db.GetPriceCatalog(h.ctx, core.PriceSourceOpenRouter)
	if err != nil || !at.Equal(first) || !strings.Contains(string(stored), "openai/gpt-4o") {
		t.Fatalf("stored catalog at %v, %v", at, err)
	}

	// A failed listing keeps the catalog rows and reports the error.
	listingErr = errors.New("401 from GET /v1/models")
	if _, err := h.app.RefreshAIProvider(h.ctx, admin, "cloud"); !errors.Is(err, core.ErrUnavailable) || errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("failed listing = %v", err)
	}
	providers, _ := h.app.ListAIProviders(h.ctx, admin)
	for _, p := range providers {
		if p.ID == "cloud" && (p.ModelCount != 3 || p.RefreshError == "") {
			t.Fatalf("cloud after a failed listing = %+v", p)
		}
	}

	// With public prices off, provider and local prices still apply.
	h.cfg.AI.FetchPublicPrices = false
	off, _ := h.app.RefreshAIPrices(h.ctx, admin)
	if off.Sources[0].Error == "" {
		t.Fatalf("refresh with public prices off = %+v", off)
	}
	check("cloud", "gpt-4o-mini", core.PriceSourceNone, "", 0, 0)
	check("gw", "gateway-model", core.PriceSourceProvider, "", 9, 9)
	check("box", "llama3.2", core.PriceSourceOverride, "", 0.01, 0)
}

func TestUsageCostUsesEffectivePriceAtCallTime(t *testing.T) {
	h := newHarness(t)
	u := h.user("Payer", false)
	usageTokens(h, 1000, 1000)
	setModelPrice(h, "local", "chat", core.AIPrice{InputPerMTok: 1, OutputPerMTok: 3, PerRequest: 0.001})
	if _, err := h.app.RunAI(h.ctx, u, summarize); err != nil {
		t.Fatal(err)
	}
	if got := lastUsageOf(t, h, u.UserID()); got.CostMicros != 1000+3000+1000 {
		t.Fatalf("cost = %d", got.CostMicros)
	}
	setModelPrice(h, "local", "chat", core.AIPrice{InputPerMTok: 100})
	rows := usageOf(h, u.UserID())
	if rows[0].CostMicros != 5000 {
		t.Fatalf("a price change rewrote history: %d", rows[0].CostMicros)
	}
}
