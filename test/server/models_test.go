package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// TestAIModelsAndRoutesOverHTTP runs the catalog, route and preset routes
// through the real stack. Admin-only routes answer 403 to a normal user, and a
// route save names models that exist and have the task's capabilities.
func TestAIModelsAndRoutesOverHTTP(t *testing.T) {
	model := newAICtxModel(t)
	h := newAICtxHarness(t, model, nil)
	h.mustStatus(h.do("POST", "/api/auth/signup", map[string]string{
		"email": "owner@example.com", "name": "Olive Owner", "password": "correct horse battery",
	}, nil), 201, "signup")
	h.mustStatus(h.do("POST", "/api/users", map[string]any{
		"email": "bob@example.com", "name": "Bob", "password": "another good password",
	}, nil), 201, "create bob")
	bob := h.as("bob@example.com", "another good password")

	var listed struct{ Items []core.AIModel }
	h.mustStatus(h.do("POST", "/api/ai/providers/vision/refresh", nil, &listed), 200, "refresh")
	if len(listed.Items) != 2 {
		t.Fatalf("refreshed models = %+v", listed.Items)
	}
	h.mustStatus(h.do("POST", "/api/ai/providers/text/refresh", nil, nil), 200, "refresh text")

	var models struct{ Items []core.AIModel }
	bob.mustStatus(bob.do("GET", "/api/ai/models?capability=vision", nil, &models), 200, "bob lists vision models")
	for _, m := range models.Items {
		if m.ProviderID != "vision" {
			t.Fatalf("a model without image input in the vision listing: %+v", m)
		}
	}
	if len(models.Items) == 0 || models.Items[0].EffectiveSource == "" {
		t.Fatalf("vision models = %+v", models.Items)
	}

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", "/api/ai/models/vision/vision-model", map[string]any{"enabled": false}},
		{"PUT", "/api/ai/routes/instance/edit", map[string]any{"targets": []any{}}},
		{"POST", "/api/admin/ai/prices/refresh", nil},
		{"POST", "/api/ai/providers/vision/refresh", nil},
	} {
		bob.mustStatus(bob.do(c.method, c.path, c.body, nil), http.StatusForbidden, "bob "+c.method+" "+c.path)
	}

	target := func(provider, model string) map[string]any {
		return map[string]any{"targets": []map[string]string{{"providerId": provider, "model": model}}}
	}
	for what, c := range map[string]struct {
		task string
		body any
	}{
		"unknown model":    {"edit", target("vision", "gpt-9")},
		"unknown provider": {"edit", target("nope", "vision-model")},
		"no image input":   {"vision", target("text", "text-model")},
		"no embeddings":    {"embeddings", target("vision", "vision-model")},
		"unknown task":     {"summarise", target("text", "text-model")},
	} {
		var e struct{ Error, Field string }
		resp := h.do("PUT", "/api/ai/routes/instance/"+c.task, c.body, &e)
		if resp.StatusCode != http.StatusBadRequest || e.Error == "" {
			t.Fatalf("%s: status %d %+v", what, resp.StatusCode, e)
		}
	}
	h.mustStatus(h.do("PUT", "/api/ai/routes/instance/edit", map[string]any{"targets": []map[string]string{
		{"providerId": "text", "model": "text-model"}, {"providerId": "vision", "model": "vision-model"},
	}}, nil), 200, "save the edit route")

	var run core.AIResult
	bob.mustStatus(bob.do("POST", "/api/ai/run", map[string]any{"task": "improve", "text": "fix this"}, &run), 200, "bob runs an edit")
	if run.Provider != "text" || run.Route != core.RouteEdit || run.Attempts != 1 {
		t.Fatalf("run = %+v", run)
	}

	var routes core.AIRoutes
	bob.mustStatus(bob.do("GET", "/api/ai/routes", nil, &routes), 200, "bob lists routes")
	found := false
	for _, r := range routes.Instance {
		found = found || (r.Task == core.RouteEdit && len(r.Targets) == 2)
	}
	if !found || !routes.AllowUserRoutes {
		t.Fatalf("routes = %+v", routes)
	}
	bob.mustStatus(bob.do("PUT", "/api/ai/routes/me/edit", target("vision", "vision-model"), nil), 200, "bob saves his route")
	bob.mustStatus(bob.do("POST", "/api/ai/run", map[string]any{"task": "improve", "text": "fix this"}, &run), 200, "bob runs his route")
	if run.Provider != "vision" {
		t.Fatalf("bob's route did not apply: %+v", run)
	}
	bob.mustStatus(bob.do("DELETE", "/api/ai/routes/me/edit", nil, nil), 204, "bob resets his route")
	bob.mustStatus(bob.do("DELETE", "/api/ai/routes/me/edit", nil, nil), 404, "second reset")

	h.mustStatus(h.do("PATCH", "/api/settings", map[string]any{
		"instanceName": "Test", "allowSignup": true, "aiEnabled": true, "aiAllowUserKeys": true, "aiAllowUserRoutes": false,
	}, nil), 200, "turn user routes off")
	bob.mustStatus(bob.do("PUT", "/api/ai/routes/me/edit", target("vision", "vision-model"), nil), 403, "user route while off")

	var e struct{ Error string }
	resp := h.do("PATCH", "/api/ai/models/vision/vision-model", map[string]any{"priceOverride": "free"}, &e)
	if resp.StatusCode != 400 || !strings.Contains(e.Error, "priceOverride") {
		t.Fatalf("bad override = %d %+v", resp.StatusCode, e)
	}
	var row core.AIModel
	h.mustStatus(h.do("PATCH", "/api/ai/models/vision/vision-model", map[string]any{
		"priceOverride": map[string]float64{"inputPerMTok": 0.5, "outputPerMTok": 1.5},
	}, &row), 200, "override")
	if row.EffectiveSource != core.PriceSourceOverride || row.EffectivePrice.OutputPerMTok != 1.5 {
		t.Fatalf("override row = %+v", row)
	}
	var cleared core.AIModel
	h.mustStatus(h.do("PATCH", "/api/ai/models/vision/vision-model", json.RawMessage(`{"priceOverride":null}`), &cleared), 200, "clear")
	if cleared.PriceOverride != nil || cleared.EffectiveSource == core.PriceSourceOverride {
		t.Fatalf("cleared row = %+v", cleared)
	}

	var presets struct{ Items []core.AIPreset }
	bob.mustStatus(bob.do("GET", "/api/ai/presets", nil, &presets), 200, "presets")
	if len(presets.Items) < 10 || presets.Items[0].ID != "openrouter" {
		t.Fatalf("presets = %+v", presets.Items)
	}
	var prices core.AIPriceRefresh
	h.mustStatus(h.do("POST", "/api/admin/ai/prices/refresh", nil, &prices), 200, "price refresh")
	if len(prices.Sources) != 2 || prices.Sources[0].Error == "" {
		t.Fatalf("price refresh without a fetcher = %+v", prices)
	}
}
