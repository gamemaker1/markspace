package web

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// sampleAIView fills every field the AI templates read.
func sampleAIView() *aiView {
	price := &core.AIPrice{InputPerMTok: 2.5, OutputPerMTok: 10}
	model := &core.AIModel{ProviderID: "p1", ModelID: "openai/gpt-4o", Name: "GPT-4o", ContextLength: 128000,
		Inputs: []string{"text", "image"}, Outputs: []string{"text"}, Enabled: true, Available: true,
		EffectivePrice: price, EffectiveSource: core.PriceSourceOpenRouter, PriceOverride: price, ProviderName: "Local"}
	chip := aiChip{Value: "p1|openai/gpt-4o", ProviderID: "p1", Model: "openai/gpt-4o", Label: "GPT-4o", Provider: "Local", Price: "$2.50 in, $10.00 out per M"}
	provider := &core.AIProviderConfig{ID: "p1", Name: "Local", Kind: core.ProviderOllama, BaseURL: "http://localhost:11434",
		Enabled: true, Source: "db", HasAPIKey: true, Preset: "ollama", Local: true, ModelCount: 3, RefreshError: "timeout"}
	return &aiView{
		Admin: true, Action: "/admin/ai", Scope: core.RouteScopeInstance, AllowUserRoutes: true, AllowUserKeys: true,
		Tasks: []*aiTaskView{
			{Task: "edit", Label: "Edit", Hint: "h", Targets: []aiChip{chip, {Value: "x|y", Label: "y", Missing: true}}, Instance: []aiChip{chip}, HasOwn: true, Choices: []aiChip{chip}, Open: true},
			{Task: "embeddings", Label: "Embeddings", Hint: "h"},
		},
		Models: []*aiModelView{{AIModel: model, Key: "p1|openai/gpt-4o", Label: "GPT-4o", Context: "128k", InputPrice: "$2.50", OutputPrice: "$10.00",
			Source: "OpenRouter, 13 Sept", Caps: "text vision", InputMods: modalities(model.Inputs, "input"), OutputMods: modalities(model.Outputs, "output"), OverrideIn: "2.5", OverrideOut: "10"}},
		Providers: []*aiProviderView{
			{AIProviderConfig: provider, PresetName: "Ollama", Status: "Listing failed", Failed: true, Refreshed: "now", Editing: true, Fields: "name baseUrl"},
			{AIProviderConfig: &core.AIProviderConfig{ID: "f", Name: "File", Kind: core.ProviderOpenAI, Source: "file"}, FromFile: true, Status: "Disabled"},
		},
		Presets:           []aiPresetView{{AIPreset: core.AIPreset{ID: "ollama", Name: "Ollama", Fields: []string{"name", "baseUrl"}}, FieldList: "name baseUrl", Sub: "Runs on your hardware"}},
		Kinds:             core.AIProviderKinds,
		ProviderOptions:   []aiOption{{"p1", "Local"}},
		CapabilityOptions: aiCapabilityOptions,
		ShownModels:       1,
		Price:             "p1|openai/gpt-4o",
		AddOpen:           true,
		Error:             "Enter a price",
		ErrorField:        "inputPerMTok",
		ErrorForm:         "price",
	}
}

// aiFake records the AI calls the pages make.
type aiFake struct {
	*fakeBackend
	mu        sync.Mutex
	calls     []string
	saved     *core.AIProviderConfig
	saveErr   error
	scope     string
	task      string
	targets   []core.AIRouteTarget
	routeErr  error
	update    core.AIModelUpdate
	updateKey string
}

func newAIFake() *aiFake { return &aiFake{fakeBackend: newFake()} }

func (f *aiFake) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *aiFake) GetSettings(context.Context, *core.Actor) (*core.Settings, error) {
	return &core.Settings{InstanceName: "Test", AIEnabled: true, AIAllowUserKeys: true, AIAllowUserRoutes: true}, nil
}

func (f *aiFake) SaveAIProvider(_ context.Context, _ *core.Actor, cfg *core.AIProviderConfig) (*core.AIProviderConfig, error) {
	if f.saveErr != nil {
		return nil, f.saveErr
	}
	f.record("save")
	f.saved = cfg
	out := *cfg
	out.ModelCount = 3
	return &out, nil
}

func (f *aiFake) DeleteAIProvider(_ context.Context, _ *core.Actor, id string) error {
	f.record("delete " + id)
	return nil
}

func (f *aiFake) TestAIProvider(_ context.Context, _ *core.Actor, id string) error {
	f.record("test " + id)
	return nil
}

func (f *aiFake) RefreshAIProvider(_ context.Context, _ *core.Actor, id string) ([]*core.AIModel, error) {
	f.record("refresh " + id)
	return []*core.AIModel{{ModelID: "a"}, {ModelID: "b"}}, nil
}

func (f *aiFake) ReindexEmbeddings(context.Context, *core.Actor, string) (int, error) {
	f.record("reindex")
	return 4, nil
}

func (f *aiFake) RefreshAIPrices(context.Context, *core.Actor) (*core.AIPriceRefresh, error) {
	f.record("prices")
	return &core.AIPriceRefresh{Repriced: 2, Sources: []core.AIPriceCatalogStatus{
		{Source: core.PriceSourceOpenRouter, Models: 30}, {Source: core.PriceSourceLiteLLM, Error: "download failed"},
	}}, nil
}

func (f *aiFake) SaveAIRoute(_ context.Context, _ *core.Actor, scope, task string, targets []core.AIRouteTarget) (*core.AIRoute, error) {
	if f.routeErr != nil {
		return nil, f.routeErr
	}
	f.record("route " + scope + " " + task)
	f.scope, f.task, f.targets = scope, task, targets
	return &core.AIRoute{Task: task, Targets: targets}, nil
}

func (f *aiFake) DeleteAIRoute(_ context.Context, _ *core.Actor, task string) error {
	f.record("route-reset " + task)
	return nil
}

func (f *aiFake) UpdateAIModel(_ context.Context, _ *core.Actor, providerID, model string, in core.AIModelUpdate) (*core.AIModel, error) {
	f.record("model")
	f.updateKey, f.update = providerID+"|"+model, in
	return &core.AIModel{ProviderID: providerID, ModelID: model}, nil
}

func (f *aiFake) ListAIPresets(context.Context, *core.Actor) ([]core.AIPreset, error) {
	return []core.AIPreset{
		{ID: "openrouter", Name: "OpenRouter", Kind: core.ProviderOpenAI, Fields: []string{"name", "apiKey"}, PriceSource: core.PresetPricesProvider},
		{ID: "ollama", Name: "Ollama", Kind: core.ProviderOllama, Local: true, Fields: []string{"name", "baseUrl"}},
	}, nil
}

func (f *aiFake) ListAIProviders(_ context.Context, actor *core.Actor) ([]*core.AIProviderConfig, error) {
	return []*core.AIProviderConfig{
		{ID: "or", Name: "OpenRouter", Preset: "openrouter", Kind: core.ProviderOpenAI, Enabled: true, Source: "db", ModelCount: 2, RefreshedAt: testTime},
		{ID: "mine", Name: "My box", Preset: "ollama", Kind: core.ProviderOllama, Enabled: true, Source: "db", OwnerID: f.member.ID, Local: true},
	}, nil
}

func (f *aiFake) ListAIModels(_ context.Context, _ *core.Actor, q core.AIModelQuery) ([]*core.AIModel, error) {
	all := []*core.AIModel{
		{ProviderID: "or", ModelID: "openai/gpt-4o", Name: "GPT-4o", ContextLength: 128000, Inputs: []string{"text", "image"}, Outputs: []string{"text"},
			Enabled: true, Available: true, ProviderName: "OpenRouter", EffectivePrice: &core.AIPrice{InputPerMTok: 2.5, OutputPerMTok: 10},
			EffectiveSource: core.PriceSourceProvider},
		{ProviderID: "or", ModelID: "text-embedding-3-small", Inputs: []string{"text"}, Outputs: []string{"embedding"},
			Enabled: false, Available: false, ProviderName: "OpenRouter", EffectiveSource: core.PriceSourceNone},
	}
	if q.Capability == core.RouteEmbeddings {
		return all[1:], nil
	}
	return all, nil
}

func (f *aiFake) ListAIRoutes(context.Context, *core.Actor) (*core.AIRoutes, error) {
	return &core.AIRoutes{
		Tasks:           core.AIRouteTasks,
		AllowUserRoutes: true,
		Instance: []core.AIRoute{{Task: core.RouteEdit, Targets: []core.AIRouteTarget{{ProviderID: "or", Model: "openai/gpt-4o"}}},
			{Task: "plugin.wiki.summarise", Targets: []core.AIRouteTarget{{ProviderID: "gone", Model: "x"}}}},
		User: []core.AIRoute{{Task: core.RouteChat, Targets: []core.AIRouteTarget{{ProviderID: "mine", Model: "llama3.2"}}}},
	}, nil
}

func TestAdminAIPageRenders(t *testing.T) {
	f := newAIFake()
	h := newTestServer(t, f).Handler()
	rec := do(t, h, http.MethodGet, "/admin/ai", adminSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`id="tasks"`, `id="models"`, `id="providers"`, `data-task="edit"`, `data-task="plugin.wiki.summarise"`,
		`value="or|openai/gpt-4o"`, "$2.50", "128k", "Not listed", "Provider", `/static/js/ai/admin.js`,
		`data-confirm="Delete OpenRouter?"`, `value="openrouter"`, "Refresh prices", "Uses Default", "Plugin wiki, summarise",
		`class="split-nav"`, `/static/js/admin.js`, `data-dialog-open="provider-new"`, `id="provider-or-edit"`, `popovertarget="provider-menu-or"`,
		`aria-label="Save models for Edit"`, `data-add-model`, `data-toggle-form`, `data-price-edit`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("admin page lacks %q", want)
		}
	}
	if strings.Index(body, `id="tasks"`) > strings.Index(body, `id="models"`) || strings.Index(body, `id="models"`) > strings.Index(body, `id="providers"`) {
		t.Error("sections are out of order")
	}
	for _, gone := range []string{"data-two-step", "Confirm delete", `id="price-dialog"`, "data-dialog-autoopen"} {
		if strings.Contains(body, gone) {
			t.Errorf("admin page shows %q", gone)
		}
	}
	filtered := do(t, h, http.MethodGet, "/admin/ai?capability=embedding", adminSession, nil).Body.String()
	if !strings.Contains(filtered, `data-model="openai/gpt-4o" data-caps="text vision" data-search="openai/gpt-4o gpt-4o  openrouter" hidden`) {
		t.Error("the capability filter did not hide the chat model")
	}
}

func TestAdminAIRefusedFormsReopen(t *testing.T) {
	f := newAIFake()
	h := newTestServer(t, f).Handler()
	get := func(path string) string {
		t.Helper()
		rec := do(t, h, http.MethodGet, path, adminSession, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, rec.Code)
		}
		return rec.Body.String()
	}

	body := get("/admin/ai?price=or%7Copenai%2Fgpt-4o&error=Enter+a+price+of+0+or+more&field=inputPerMTok")
	for _, want := range []string{`id="price-dialog"`, `data-dialog-autoopen`, `aria-invalid="true"`, `id="price-in-error"`, "Enter a price of 0 or more"} {
		if !strings.Contains(body, want) {
			t.Errorf("price refusal lacks %q", want)
		}
	}
	if strings.Contains(body, `data-flash="error"`) {
		t.Error("the price refusal repeats its message in the page Banner")
	}

	body = get("/admin/ai?add=1&error=The+provider+is+unreachable")
	if !strings.Contains(body, `id="provider-new" data-dialog-title="Add provider"`) || !strings.Contains(body, `data-dialog-autoopen`) || !strings.Contains(body, `<p hidden data-dialog-error>The provider is unreachable</p>`) {
		t.Error("a refused new provider does not reopen its Dialog with the message")
	}

	body = get("/admin/ai?edit=or&error=Enter+an+address&field=baseUrl")
	if !strings.Contains(body, `id="pf-baseUrl-or-error"`) || strings.Contains(body, `<p hidden data-dialog-error>`) {
		t.Error("a refused provider edit does not show the message under Base URL")
	}

	body = get("/admin/ai?task=edit&saved=1")
	if strings.Count(body, "data-saved-message") != 1 || !strings.Contains(body, "Models saved") {
		t.Error("a saved route does not show one InlineMessage")
	}
}

func TestAdminAIPostActions(t *testing.T) {
	cases := []struct {
		name      string
		form      url.Values
		wantCall  string
		wantLoc   string
		wantError bool
		setup     func(f *aiFake)
		check     func(t *testing.T, f *aiFake)
	}{
		{name: "save", form: url.Values{"action": {"save"}, "preset": {"openrouter"}, "name": {"OR"}, "apiKey": {"k"}, "kind": {"ollama"}},
			wantCall: "save", wantLoc: "Provider+saved.+3+models+listed.",
			check: func(t *testing.T, f *aiFake) {
				if s := f.saved; s.Preset != "openrouter" || s.Kind != "" || s.APIKey != "k" || !s.Enabled || s.OwnerID != "" {
					t.Errorf("saved = %+v", s)
				}
			}},
		{name: "save disabled", form: url.Values{"action": {"save"}, "preset": {"custom"}, "kind": {"ollama"}, "enabledField": {"1"}, "local": {"1"}},
			wantCall: "save", check: func(t *testing.T, f *aiFake) {
				if s := f.saved; s.Enabled || s.Kind != core.ProviderOllama || !s.Local {
					t.Errorf("saved = %+v", s)
				}
			}},
		{name: "delete", form: url.Values{"action": {"delete"}, "providerId": {"p1"}}, wantCall: "delete p1", wantLoc: "Provider+removed."},
		{name: "test", form: url.Values{"action": {"test"}, "providerId": {"p1"}}, wantCall: "test p1"},
		{name: "refresh", form: url.Values{"action": {"refresh"}, "providerId": {"p1"}}, wantCall: "refresh p1", wantLoc: "2+models+listed.#providers"},
		{name: "reindex", form: url.Values{"action": {"reindex"}}, wantCall: "reindex", wantLoc: "4+notes+embedded."},
		{name: "prices", form: url.Values{"action": {"prices-refresh"}}, wantCall: "prices", wantLoc: "OpenRouter%3A+30+models.+LiteLLM%3A+download+failed.+2+prices+changed.#models"},
		{name: "route", form: url.Values{"action": {"route-save"}, "task": {"edit"}, "target": {"a|m1", "b|openai/gpt-4o", "junk"}, "add": {"c|x"}, "op": {"up:1"}},
			wantCall: "route instance edit", wantLoc: "/admin/ai?task=edit&saved=1#task-edit", check: func(t *testing.T, f *aiFake) {
				want := []core.AIRouteTarget{{ProviderID: "b", Model: "openai/gpt-4o"}, {ProviderID: "a", Model: "m1"}, {ProviderID: "c", Model: "x"}}
				if !reflect.DeepEqual(f.targets, want) {
					t.Errorf("targets = %+v", f.targets)
				}
			}},
		{name: "toggle on", form: url.Values{"action": {"model-toggle"}, "providerId": {"or"}, "model": {"openai/gpt-4o"}, "enabled": {"1"}},
			wantCall: "model", check: func(t *testing.T, f *aiFake) {
				if f.updateKey != "or|openai/gpt-4o" || f.update.Enabled == nil || !*f.update.Enabled || f.update.SetPrice {
					t.Errorf("update %s = %+v", f.updateKey, f.update)
				}
			}},
		{name: "toggle off", form: url.Values{"action": {"model-toggle"}, "providerId": {"or"}, "model": {"m"}},
			wantCall: "model", check: func(t *testing.T, f *aiFake) {
				if f.update.Enabled == nil || *f.update.Enabled {
					t.Errorf("update = %+v", f.update)
				}
			}},
		{name: "price", form: url.Values{"action": {"model-price"}, "providerId": {"or"}, "model": {"m"}, "inputPerMTok": {"2.5"}, "outputPerMTok": {"10"}},
			wantCall: "model", check: func(t *testing.T, f *aiFake) {
				if !f.update.SetPrice || f.update.PriceOverride == nil || *f.update.PriceOverride != (core.AIPrice{InputPerMTok: 2.5, OutputPerMTok: 10}) || f.update.Enabled != nil {
					t.Errorf("update = %+v", f.update)
				}
			}},
		{name: "price refused", form: url.Values{"action": {"model-price"}, "providerId": {"or"}, "model": {"m"}, "inputPerMTok": {"-1"}}, wantError: true,
			wantLoc: "price=or%7Cm&error="},
		{name: "price not a number", form: url.Values{"action": {"model-price"}, "providerId": {"or"}, "model": {"m"}, "outputPerMTok": {"lots"}}, wantError: true,
			wantLoc: "field=outputPerMTok"},
		{name: "price clear button", form: url.Values{"action": {"model-price"}, "clear": {"1"}, "providerId": {"or"}, "model": {"m"}, "inputPerMTok": {"-1"}},
			wantCall: "model", check: func(t *testing.T, f *aiFake) {
				if !f.update.SetPrice || f.update.PriceOverride != nil {
					t.Errorf("update = %+v", f.update)
				}
			}},
		{name: "save refused", form: url.Values{"action": {"save"}, "providerId": {"or"}, "baseUrl": {"nope"}}, wantError: true,
			setup: func(f *aiFake) { f.saveErr = core.Invalid("baseUrl", "enter an address") }, wantLoc: "/admin/ai?edit=or&error="},
		{name: "new provider refused", form: url.Values{"action": {"save"}, "preset": {"openrouter"}}, wantError: true,
			setup: func(f *aiFake) { f.saveErr = core.Invalid("baseUrl", "enter an address") }, wantLoc: "/admin/ai?add=1&error="},
		{name: "route refused", form: url.Values{"action": {"route-save"}, "task": {"edit"}, "target": {"a|m1"}}, wantError: true,
			setup: func(f *aiFake) { f.routeErr = core.Invalid("targets", "choose a capable model") }, wantLoc: "/admin/ai?task=edit&error="},
		{name: "price clear", form: url.Values{"action": {"model-price-clear"}, "providerId": {"or"}, "model": {"m"}},
			wantCall: "model", check: func(t *testing.T, f *aiFake) {
				if !f.update.SetPrice || f.update.PriceOverride != nil {
					t.Errorf("update = %+v", f.update)
				}
			}},
		{name: "unknown", form: url.Values{"action": {"explode"}}, wantError: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAIFake()
			if c.setup != nil {
				c.setup(f)
			}
			h := newTestServer(t, f).Handler()
			rec := do(t, h, http.MethodPost, "/admin/ai", adminSession, c.form)
			loc := rec.Header().Get("Location")
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if c.wantError {
				if !strings.Contains(loc, "error=") || len(f.calls) != 0 || !strings.Contains(loc, c.wantLoc) {
					t.Fatalf("Location %q calls %v, want an error at %q and no call", loc, f.calls, c.wantLoc)
				}
				return
			}
			if c.wantCall != "" && (len(f.calls) != 1 || f.calls[0] != c.wantCall) {
				t.Fatalf("calls = %v, want %q", f.calls, c.wantCall)
			}
			if !strings.Contains(loc, c.wantLoc) || strings.Contains(loc, "error=") {
				t.Fatalf("Location %q, want %q", loc, c.wantLoc)
			}
			if c.check != nil {
				c.check(t, f)
			}
		})
	}
}

func TestAdminAIRefusesMembers(t *testing.T) {
	f := newAIFake()
	h := newTestServer(t, f).Handler()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := do(t, h, method, "/admin/ai", memberSession, url.Values{"action": {"delete"}, "providerId": {"or"}})
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "You do not have access") {
			t.Fatalf("%s as a member = %d", method, rec.Code)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("a member reached the backend: %v", f.calls)
	}
}

func TestUserAIPage(t *testing.T) {
	f := newAIFake()
	h := newTestServer(t, f).Handler()
	rec := do(t, h, http.MethodGet, "/settings/ai", memberSession, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"Use a different model", "Instance", "Personal providers", "My box", `data-scope="me"`, "Use the instance choice"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}
	for _, leak := range []string{`id="models"`, "OpenRouter</span>\n              <span class=\"cell-sub\">", "Refresh prices"} {
		if strings.Contains(body, leak) {
			t.Errorf("settings page shows %q", leak)
		}
	}

	post := func(form url.Values) string {
		t.Helper()
		f.calls = nil
		rec := do(t, h, http.MethodPost, "/settings/ai", memberSession, form)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%v: status %d", form, rec.Code)
		}
		return rec.Header().Get("Location")
	}
	if loc := post(url.Values{"action": {"route-save"}, "task": {"chat"}, "target": {"mine|llama3.2"}}); f.scope != "me" || f.task != "chat" || !strings.HasPrefix(loc, "/settings/ai?task=chat&saved=1") {
		t.Fatalf("route-save scope %q task %q loc %q", f.scope, f.task, loc)
	}
	if post(url.Values{"action": {"route-reset"}, "task": {"chat"}}); len(f.calls) != 1 || f.calls[0] != "route-reset chat" {
		t.Fatalf("route-reset calls = %v", f.calls)
	}
	if post(url.Values{"action": {"provider-save"}, "preset": {"ollama"}, "baseUrl": {"http://box:11434"}}); f.saved == nil || f.saved.OwnerID != f.member.ID {
		t.Fatalf("provider-save = %+v", f.saved)
	}
	if post(url.Values{"action": {"provider-delete"}, "providerId": {"mine"}}); len(f.calls) != 1 || f.calls[0] != "delete mine" {
		t.Fatalf("provider-delete calls = %v", f.calls)
	}
	for _, action := range []string{"model-price", "model-toggle", "prices-refresh", "reindex"} {
		if loc := post(url.Values{"action": {action}, "providerId": {"or"}, "model": {"m"}}); len(f.calls) != 0 || !strings.Contains(loc, "error=") {
			t.Fatalf("%s on the settings page: calls %v loc %q", action, f.calls, loc)
		}
	}
}

func TestAIFormatting(t *testing.T) {
	for v, want := range map[float64]string{0: "$0", 2.5: "$2.50", 10: "$10", 0.15: "$0.15", 0.075: "$0.075", 2.4999999999999996: "$2.50"} {
		if got := usd(v); got != want {
			t.Errorf("usd(%v) = %q, want %q", v, got, want)
		}
	}
	for n, want := range map[int]string{0: "", 512: "512", 8192: "8k", 128000: "128k", 131072: "131k", 1000000: "1M", 1048576: "1M", 2000000: "2M"} {
		if got := contextLabel(n); got != want {
			t.Errorf("contextLabel(%d) = %q, want %q", n, got, want)
		}
	}
	if got := shortDate(time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)); got != "13 Sept" {
		t.Errorf("shortDate = %q", got)
	}
	local := &core.AIModel{EffectivePrice: &core.AIPrice{}, EffectiveSource: core.PriceSourceLocal}
	if in, out := priceLabels(local); in != "Free" || out != "Free" || chipPrice(local) != "free" {
		t.Errorf("local price = %q %q", in, out)
	}
	if chipPrice(&core.AIModel{}) != "price unknown" {
		t.Error("a model without a price should read price unknown")
	}
	comfy := &core.AIProviderConfig{Kind: core.ProviderComfyUI, ImageGeneration: true}
	if got := modelCaps(&core.AIModel{Inputs: []string{"text"}, Outputs: []string{"text", "image"}}, comfy); got != "image-output" {
		t.Errorf("comfy caps = %q", got)
	}
}
