package ai

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// modelsBackend records the arguments of the catalog and route calls.
type modelsBackend struct {
	testkit.Fake
	provider, model string
	update          core.AIModelUpdate
	query           core.AIModelQuery
	scope, task     string
	targets         []core.AIRouteTarget
	err             error
}

func (b *modelsBackend) UpdateAIModel(_ context.Context, _ *core.Actor, provider, model string, in core.AIModelUpdate) (*core.AIModel, error) {
	b.provider, b.model, b.update = provider, model, in
	return &core.AIModel{ProviderID: provider, ModelID: model}, b.err
}

func (b *modelsBackend) ListAIModels(_ context.Context, _ *core.Actor, q core.AIModelQuery) ([]*core.AIModel, error) {
	b.query = q
	return nil, b.err
}

func (b *modelsBackend) SaveAIRoute(_ context.Context, _ *core.Actor, scope, task string, targets []core.AIRouteTarget) (*core.AIRoute, error) {
	b.scope, b.task, b.targets = scope, task, targets
	if b.err != nil {
		return nil, b.err
	}
	return &core.AIRoute{Task: task, Targets: targets}, nil
}

func (b *modelsBackend) DeleteAIRoute(_ context.Context, _ *core.Actor, task string) error {
	b.scope, b.task = "me", task
	return b.err
}

func TestAIModelAndRouteRoutesParseRequests(t *testing.T) {
	b := &modelsBackend{}
	b.ActorFromSessionFn = func(context.Context, string) (*core.Actor, error) {
		return &core.Actor{User: &core.User{ID: "u1", IsAdmin: true, IsActive: true}}, nil
	}
	h := serve(t, b)
	price := &core.AIPrice{InputPerMTok: 2.5, OutputPerMTok: 10}
	off := false
	for _, c := range []struct {
		body string
		want core.AIModelUpdate
	}{
		{`{"priceOverride":null}`, core.AIModelUpdate{SetPrice: true}},
		{`{"enabled":false}`, core.AIModelUpdate{Enabled: &off}},
		{`{"priceOverride":{"inputPerMTok":2.5,"outputPerMTok":10}}`, core.AIModelUpdate{SetPrice: true, PriceOverride: price}},
	} {
		rec := testkit.Do(t, h, "PATCH", "/api/ai/models/openrouter/openai/gpt-4o", c.body, "s")
		if rec.Code != 200 || b.provider != "openrouter" || b.model != "openai/gpt-4o" || !reflect.DeepEqual(b.update, c.want) {
			t.Fatalf("%s: %d %s, got %s %s %+v", c.body, rec.Code, rec.Body, b.provider, b.model, b.update)
		}
	}
	for _, body := range []string{`{"priceOverride":{"input":1}}`, `{"priceOverride":"free"}`, `{"price":1}`} {
		if rec := testkit.Do(t, h, "PATCH", "/api/ai/models/p/m", body, "s"); rec.Code != 400 {
			t.Fatalf("%s: status %d", body, rec.Code)
		}
	}

	testkit.Do(t, h, "GET", "/api/ai/models?capability=vision&provider=p&q=gpt", "", "s")
	if b.query != (core.AIModelQuery{Capability: "vision", ProviderID: "p", Q: "gpt"}) {
		t.Fatalf("query = %+v", b.query)
	}
	testkit.Do(t, h, "GET", "/api/ai/providers/local/models?provider=other", "", "s")
	if b.query.ProviderID != "local" {
		t.Fatalf("path provider = %+v", b.query)
	}

	body := `{"targets":[{"providerId":"a","model":"x/y"},{"providerId":"b","model":"z"}]}`
	if rec := testkit.Do(t, h, "PUT", "/api/ai/routes/instance/plugin.wiki.summarise", body, "s"); rec.Code != 200 ||
		b.scope != core.RouteScopeInstance || b.task != "plugin.wiki.summarise" || len(b.targets) != 2 || b.targets[0].Model != "x/y" {
		t.Fatalf("instance route: %d, %s %s %+v", rec.Code, b.scope, b.task, b.targets)
	}
	if rec := testkit.Do(t, h, "PUT", "/api/ai/routes/me/edit", `{}`, "s"); rec.Code != 200 || b.scope != "me" || b.targets != nil {
		t.Fatalf("my route: %d, %s %+v", rec.Code, b.scope, b.targets)
	}
	if rec := testkit.Do(t, h, "PUT", "/api/ai/routes/me/edit", `{"targets":{}}`, "s"); rec.Code != 400 {
		t.Fatalf("targets as an object: %d", rec.Code)
	}
	if rec := testkit.Do(t, h, "DELETE", "/api/ai/routes/me/edit", "", "s"); rec.Code != http.StatusNoContent || b.task != "edit" {
		t.Fatalf("delete: %d", rec.Code)
	}

	b.err = core.ErrForbidden
	for _, c := range [][3]string{
		{"PUT", "/api/ai/routes/instance/edit", `{"targets":[]}`},
		{"PATCH", "/api/ai/models/p/m", `{"enabled":true}`},
	} {
		if rec := testkit.Do(t, h, c[0], c[1], c[2], "s"); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: status %d, want 403", c[0], c[1], rec.Code)
		}
	}
	b.err = core.Invalid("targets", "model x of a is not in the catalog")
	rec := testkit.Do(t, h, "PUT", "/api/ai/routes/instance/edit", `{"targets":[{"providerId":"a","model":"x"}]}`, "s")
	var e struct{ Error, Field string }
	testkit.DecodeBody(t, rec, &e)
	if rec.Code != 400 || e.Field != "targets" {
		t.Fatalf("validation error: %d %+v", rec.Code, e)
	}
}
