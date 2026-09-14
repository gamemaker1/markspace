package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"syscall"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestRetryableClassification(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	bg := context.Background()
	cases := []struct {
		name  string
		ctx   context.Context
		err   error
		retry bool
		down  bool
	}{
		{"timeout", bg, fmt.Errorf("openai: %w", context.DeadlineExceeded), true, true},
		{"connection refused", bg, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true, true},
		{"reset", bg, fmt.Errorf("read: %w", syscall.ECONNRESET), true, true},
		{"503", bg, &APIError{Status: 503}, true, true},
		{"wrapped 500", bg, fmt.Errorf("ai completion: %w", &APIError{Status: 500}), true, true},
		{"429", bg, &APIError{Status: 429, sentinel: core.ErrRateLimited}, true, false},
		{"408", bg, &APIError{Status: 408}, true, false},
		{"400", bg, &APIError{Status: 400, Body: `{"error":"bad temperature"}`}, false, false},
		{"401", bg, &APIError{Status: 401, sentinel: core.ErrUnauthorized}, false, false},
		{"404", bg, &APIError{Status: 404, sentinel: core.ErrNotFound}, false, false},
		{"context length 400", bg, &APIError{Status: 400, Body: `{"error":{"code":"context_length_exceeded"}}`}, true, false},
		{"prompt too long", bg, &APIError{Status: 413, Body: "prompt is too long: 210000 tokens"}, true, false},
		{"refusal", bg, errors.New("openai: the request was refused by the content filter"), false, false},
		{"unavailable", bg, fmt.Errorf("x: %w", core.ErrUnavailable), true, false},
		{"canceled context", canceled, &APIError{Status: 503}, false, true},
		{"canceled error", bg, context.Canceled, false, false},
		{"nil", bg, nil, false, false},
	}
	for _, c := range cases {
		if got := Retryable(c.ctx, c.err); got != c.retry {
			t.Errorf("%s: Retryable = %v, want %v", c.name, got, c.retry)
		}
		if got := ProviderDown(c.err); got != c.down {
			t.Errorf("%s: ProviderDown = %v, want %v", c.name, got, c.down)
		}
	}
}

func TestFallbackStopsOnFinalErrorAndStreamedOutput(t *testing.T) {
	ctx := context.Background()
	retry := &APIError{Status: 503}
	final := &APIError{Status: 400}
	type step struct {
		streamed bool
		err      error
	}
	cases := []struct {
		name      string
		steps     []step
		wantLast  int
		wantTried int
		wantErr   error
	}{
		{"first succeeds", []step{{false, nil}, {false, nil}}, 0, 1, nil},
		{"503 then success", []step{{false, retry}, {false, nil}}, 1, 2, nil},
		{"400 stops", []step{{false, final}, {false, nil}}, 0, 1, final},
		{"streamed 503 stops", []step{{true, retry}, {false, nil}}, 0, 1, retry},
		{"all retryable", []step{{false, retry}, {false, retry}}, 1, 2, retry},
		{"none", nil, -1, 0, nil},
	}
	for _, c := range cases {
		calls := 0
		last, tried, err := Fallback(ctx, len(c.steps), func(i int) (bool, error) {
			calls++
			return c.steps[i].streamed, c.steps[i].err
		})
		if last != c.wantLast || tried != c.wantTried || err != c.wantErr || calls != tried {
			t.Errorf("%s: last %d tried %d calls %d err %v", c.name, last, tried, calls, err)
		}
	}
}

func TestModelCapable(t *testing.T) {
	text := &core.AIModel{Inputs: []string{"text"}, Outputs: []string{"text"}}
	vision := &core.AIModel{Inputs: []string{"text", "image"}, Outputs: []string{"text"}}
	embed := &core.AIModel{Inputs: []string{"text"}, Outputs: []string{"embedding"}}
	picture := &core.AIModel{Inputs: []string{"text", "image"}, Outputs: []string{"image"}}
	openai := &RouteProvider{Kind: core.ProviderOpenAI}
	comfy := &RouteProvider{Kind: core.ProviderComfyUI, ImageGeneration: true, ImageEditing: true}
	cases := []struct {
		name  string
		m     *core.AIModel
		p     *RouteProvider
		needs core.AINeeds
		want  bool
	}{
		{"text", text, openai, core.RouteNeeds(core.RouteEdit), true},
		{"text model for vision", text, openai, core.RouteNeeds(core.RouteVision), false},
		{"vision model for vision", vision, openai, core.RouteNeeds(core.RouteVision), true},
		{"embedding model for text", embed, openai, core.RouteNeeds(core.RouteChat), false},
		{"embedding", embed, openai, core.RouteNeeds(core.RouteEmbeddings), true},
		{"embedding on anthropic", embed, &RouteProvider{Kind: core.ProviderAnthropic}, core.RouteNeeds(core.RouteEmbeddings), false},
		{"comfyui never serves text", vision, comfy, core.RouteNeeds(core.RouteDefault), false},
		{"comfyui generates", picture, comfy, core.RouteNeeds(core.RouteImageGenerate), true},
		{"comfyui edits", picture, comfy, core.RouteNeeds(core.RouteImageEdit), true},
		{"image output without the flag", picture, openai, core.RouteNeeds(core.RouteImageGenerate), false},
		{"edit needs image input", &core.AIModel{Outputs: []string{"image"}}, comfy, core.RouteNeeds(core.RouteImageEdit), false},
	}
	for _, c := range cases {
		if got := ModelCapable(c.m, c.p, c.needs); got != c.want {
			t.Errorf("%s: ModelCapable = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestResolveRouteOrderAndSkips(t *testing.T) {
	tgt := func(p, m string) core.AIRouteTarget { return core.AIRouteTarget{ProviderID: p, Model: m} }
	chat := func(ctx int) *core.AIModel {
		return &core.AIModel{Inputs: []string{"text"}, Outputs: []string{"text"}, Enabled: true, Available: true, ContextLength: ctx}
	}
	models := map[string]*core.AIModel{
		ModelKey("a", "small"): chat(1000),
		ModelKey("a", "big"):   chat(200000),
		ModelKey("b", "eye"):   {Inputs: []string{"text", "image"}, Outputs: []string{"text"}, Enabled: true, Available: true},
		ModelKey("b", "off"):   {Inputs: []string{"text"}, Outputs: []string{"text"}, Enabled: false, Available: true},
		ModelKey("b", "gone"):  {Inputs: []string{"text"}, Outputs: []string{"text"}, Enabled: true},
		ModelKey("mine", "m"):  chat(0),
		ModelKey("e", "vec"):   {Inputs: []string{"text"}, Outputs: []string{"embedding"}, Enabled: true, Available: true},
		ModelKey("e", "vec2"):  {Inputs: []string{"text"}, Outputs: []string{"embedding"}, Enabled: true, Available: true},
	}
	providers := map[string]*RouteProvider{
		"a":    {ID: "a", Kind: core.ProviderOpenAI, Enabled: true},
		"b":    {ID: "b", Kind: core.ProviderOpenAI, Enabled: true},
		"mine": {ID: "mine", Kind: core.ProviderOllama, Enabled: true, OwnerID: "u1"},
		"e":    {ID: "e", Kind: core.ProviderOllama, Enabled: true},
	}
	req := RouteRequest{
		Task: core.RouteEdit, Needs: core.AINeeds{Text: true, MinContext: 5000}, UserID: "u2",
		Requested:       &core.AIRouteTarget{ProviderID: "mine", Model: "m"},
		AllowUserRoutes: true,
		UserRoutes:      map[string][]core.AIRouteTarget{core.RouteEdit: {tgt("b", "off")}},
		InstanceRoutes: map[string][]core.AIRouteTarget{
			core.RouteEdit:       {tgt("a", "small"), tgt("b", "gone")},
			core.RouteDefault:    {tgt("a", "small"), tgt("b", "eye"), tgt("a", "big"), tgt("x", "y")},
			core.RouteEmbeddings: {tgt("e", "vec"), tgt("e", "vec2")},
		},
		Providers: providers, Models: models,
	}
	plan := ResolveRoute(req)
	wantTargets := []core.AIRouteTarget{tgt("mine", "m"), tgt("b", "off"), tgt("a", "small"), tgt("b", "gone"), tgt("b", "eye"), tgt("a", "big"), tgt("x", "y")}
	if !reflect.DeepEqual(plan.Targets, wantTargets) {
		t.Fatalf("targets = %+v", plan.Targets)
	}
	var got []core.AIRouteTarget
	for _, c := range plan.Candidates {
		got = append(got, c.Target)
	}
	if !reflect.DeepEqual(got, []core.AIRouteTarget{tgt("b", "eye"), tgt("a", "big")}) {
		t.Fatalf("candidates = %+v", got)
	}
	reasons := map[core.AIRouteTarget]string{}
	for _, s := range plan.Skipped {
		reasons[s.Target] = s.Reason
	}
	want := map[core.AIRouteTarget]string{
		tgt("mine", "m"): SkipNotAllowed, tgt("b", "off"): SkipModelDisabled, tgt("a", "small"): SkipContext,
		tgt("b", "gone"): SkipModelUnavailable, tgt("x", "y"): SkipProviderMissing,
	}
	if !reflect.DeepEqual(reasons, want) {
		t.Fatalf("skips = %+v", reasons)
	}

	req.AllowUserRoutes = false
	req.UserID = "u1"
	req.Needs.MinContext = 0
	providers["b"].Down = true
	plan = ResolveRoute(req)
	if len(plan.Candidates) != 3 || plan.Candidates[0].Target != tgt("mine", "m") || plan.Candidates[1].Target != tgt("a", "small") {
		t.Fatalf("owner with the user route off = %+v", plan.Candidates)
	}
	providers["b"].Down = false

	req.BudgetExhausted = true
	if plan = ResolveRoute(req); !plan.QuotaExceeded || len(plan.Candidates) != 0 {
		t.Fatalf("exhausted budget = %+v", plan)
	}

	req.BudgetExhausted = false
	req.Task, req.Needs = core.RouteEmbeddings, core.RouteNeeds(core.RouteEmbeddings)
	plan = ResolveRoute(req)
	if len(plan.Targets) != 1 || len(plan.Candidates) != 1 || plan.Candidates[0].Target != tgt("e", "vec") {
		t.Fatalf("embeddings = %+v", plan)
	}
	models[ModelKey("e", "vec")].Available = false
	if plan = ResolveRoute(req); len(plan.Candidates) != 0 {
		t.Fatalf("embeddings fell back past the first entry: %+v", plan.Candidates)
	}
}
