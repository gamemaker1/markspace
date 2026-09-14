package ai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

// usageRoutes lists every usage and budget route with a body where one is
// accepted.
var usageRoutes = []struct {
	method, path, body string
	admin              bool
}{
	{"GET", "/api/ai/usage", "", false},
	{"GET", "/api/ai/usage/summary", "", false},
	{"GET", "/api/ai/usage/calls?sort=slowest", "", false},
	{"GET", "/api/admin/ai/usage", "", true},
	{"GET", "/api/admin/ai/usage/calls", "", true},
	{"GET", "/api/admin/ai/budgets", "", true},
	{"PUT", "/api/admin/ai/budgets/user/u2", `{"monthlyTokens":10}`, true},
	{"DELETE", "/api/admin/ai/budgets/user/u2", "", true},
	{"POST", "/api/admin/ai/budgets/plugin/p1/lift", "", true},
}

func TestUsageRoutesAnonymousIs401(t *testing.T) {
	h := serve(t, &testkit.Fake{})
	for _, rt := range usageRoutes {
		rec := testkit.Do(t, h, rt.method, rt.path, rt.body, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous = %d, want 401: %s", rt.method, rt.path, rec.Code, rec.Body)
		}
	}
}

func TestUsageAdminRoutesNonAdminIs403(t *testing.T) {
	h := serve(t, testkit.SignedIn(false))
	for _, rt := range usageRoutes {
		if !rt.admin {
			continue
		}
		rec := testkit.Do(t, h, rt.method, rt.path, rt.body, "s")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as a user = %d, want 403: %s", rt.method, rt.path, rec.Code, rec.Body)
		}
	}
}

func TestQuotaExceededIs429(t *testing.T) {
	if got := api.StatusFor(fmt.Errorf("budget: %w", core.ErrQuotaExceeded)); got != http.StatusTooManyRequests {
		t.Fatalf("api.StatusFor(ErrQuotaExceeded) = %d", got)
	}
	fake := testkit.SignedIn(false)
	fake.RunAIFn = func(context.Context, *core.Actor, core.AIRequest) (*core.AIResult, error) {
		return nil, fmt.Errorf("your monthly AI budget is used up: %w", core.ErrQuotaExceeded)
	}
	fake.StreamAIFn = func(context.Context, *core.Actor, core.AIRequest) (<-chan core.AIChunk, error) {
		return nil, fmt.Errorf("your monthly AI budget is used up: %w", core.ErrQuotaExceeded)
	}
	h := serve(t, fake)
	for _, path := range []string{"/api/ai/run", "/api/ai/stream"} {
		rec := testkit.Do(t, h, "POST", path, `{"task":"ask"}`, "s")
		if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "budget is used up") {
			t.Errorf("%s = %d %s, want 429 with the message", path, rec.Code, rec.Body)
		}
	}
}

func TestAIRequestIgnoresClientPluginID(t *testing.T) {
	var got []core.AIRequest
	fake := testkit.SignedIn(false)
	fake.RunAIFn = func(_ context.Context, _ *core.Actor, req core.AIRequest) (*core.AIResult, error) {
		got = append(got, req)
		return &core.AIResult{Text: "ok", Budget: &core.AIBudgetWarning{UsedPct: 81.5, Warn: true}}, nil
	}
	fake.StreamAIFn = func(_ context.Context, _ *core.Actor, req core.AIRequest) (<-chan core.AIChunk, error) {
		got = append(got, req)
		ch := make(chan core.AIChunk, 1)
		ch <- core.AIChunk{Done: true, Budget: &core.AIBudgetWarning{UsedPct: 90, Warn: true}}
		close(ch)
		return ch, nil
	}
	h := serve(t, fake)
	body := `{"task":"ask","feature":"chat","pluginId":"spoofed"}`
	run := testkit.Do(t, h, "POST", "/api/ai/run", body, "s")
	if run.Code != http.StatusOK || !strings.Contains(run.Body.String(), `"budget":{"usedPct":81.5,"warn":true}`) {
		t.Fatalf("run = %d %s", run.Code, run.Body)
	}
	stream := testkit.Do(t, h, "POST", "/api/ai/stream", body, "s")
	if !strings.Contains(stream.Body.String(), `"budget":{"usedPct":90,"warn":true}`) {
		t.Fatalf("done frame lacks the budget warning: %s", stream.Body)
	}
	if len(got) != 2 {
		t.Fatalf("backend saw %d requests", len(got))
	}
	for _, req := range got {
		if req.PluginID != "" || req.Feature != "chat" {
			t.Errorf("request reached the backend as %+v", req)
		}
	}
}

func TestUsageQueryParameters(t *testing.T) {
	var got core.AIUsageQuery
	fake := testkit.SignedIn(false)
	fake.MyAIUsageFn = func(_ context.Context, _ *core.Actor, q core.AIUsageQuery) (*core.AIUsageReport, error) {
		got = q
		return &core.AIUsageReport{Rows: []core.AIUsageRow{}}, nil
	}
	h := serve(t, fake)
	rec := testkit.Do(t, h, "GET", "/api/ai/usage?from=2026-09-01&to=2026-09-30&groupBy=day,%20feature&provider=p&model=m&feature=ask&plugin=pl&user=someone&limit=7", "", "s")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	want := core.AIUsageQuery{
		From:       time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		To:         time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		ProviderID: "p", Model: "m", Feature: "ask", PluginID: "pl",
		GroupBy: []string{"day", " feature"}, Limit: 7,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("query = %+v\nwant    %+v", got, want)
	}
	rec = testkit.Do(t, h, "GET", "/api/ai/usage?from=2026-09-01T10:00:00%2B02:00", "", "s")
	if rec.Code != http.StatusOK || !got.From.Equal(time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("RFC 3339 from = %v (%d)", got.From, rec.Code)
	}
	for _, bad := range []string{"from=yesterday", "to=2026-13-01"} {
		if rec := testkit.Do(t, h, "GET", "/api/ai/usage?"+bad, "", "s"); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, rec.Code)
		}
	}
}

func TestMyUsageCallsForcesOwnUser(t *testing.T) {
	var gotQ core.AIUsageQuery
	var gotSort string
	fake := testkit.SignedIn(false)
	fake.AIUsageCallsFn = func(_ context.Context, _ *core.Actor, q core.AIUsageQuery, sort string) ([]*core.AIUsageRecord, error) {
		gotQ, gotSort = q, sort
		return nil, nil
	}
	h := serve(t, fake)
	rec := testkit.Do(t, h, "GET", "/api/ai/usage/calls?sort=slowest&user=u9&limit=3", "", "s")
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"items\":[]}\n" {
		t.Fatalf("calls = %d %q", rec.Code, rec.Body)
	}
	if gotQ.UserID != "u1" || gotSort != "slowest" || gotQ.Limit != 3 {
		t.Fatalf("backend got %+v sort %q", gotQ, gotSort)
	}
}
