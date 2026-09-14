package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// TestAIUsageEndToEnd runs AI calls through the HTTP API over the real index
// and checks the usage report, the call list, the summary, the budget stop
// with its 429 and budget row, the lift, and the access rules of the routes.
func TestAIUsageEndToEnd(t *testing.T) {
	model := newAICtxModel(t)
	h := newAICtxHarness(t, model, nil)
	var admin core.User
	h.mustStatus(h.do("POST", "/api/auth/signup", map[string]string{
		"email": "owner@example.com", "name": "Olive Owner", "password": "correct horse battery",
	}, &admin), 201, "signup")
	// The price comes from an admin override on the listed model.
	h.mustStatus(h.do("POST", "/api/ai/providers/vision/refresh", nil, nil), 200, "refresh")
	h.mustStatus(h.do("PATCH", "/api/ai/models/vision/vision-model", map[string]any{
		"priceOverride": map[string]float64{"inputPerMTok": 2, "outputPerMTok": 4},
	}, nil), 200, "price override")

	// The fake model reports 5 prompt and 1 completion tokens on /run.
	status, _, body := aiCtxRun(h, map[string]any{"task": "summarize", "text": "hello", "feature": "chat", "pluginId": "spoofed"})
	if status != 200 {
		t.Fatalf("run = %d %s", status, body)
	}
	// The stream reports no usage, so its row is estimated.
	resp, err := h.client.Post(h.server.URL+"/api/ai/stream", "application/json",
		strings.NewReader(`{"task":"summarize","text":"hello there"}`))
	if err != nil {
		t.Fatal(err)
	}
	frames, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(frames), "event: done") {
		t.Fatalf("stream = %s", frames)
	}

	var report core.AIUsageReport
	h.mustStatus(h.do("GET", "/api/ai/usage?groupBy=feature,plugin", nil, &report), 200, "own usage")
	byFeature := map[string]core.AIUsageRow{}
	for _, r := range report.Rows {
		if r.PluginID != "" {
			t.Fatalf("a client-supplied pluginId reached the usage row: %+v", r)
		}
		byFeature[r.Feature] = r
	}
	chat, sum := byFeature["chat"], byFeature["summarize"]
	if len(report.Rows) != 2 || chat.Calls != 1 || chat.InputTokens != 5 || chat.OutputTokens != 1 ||
		chat.CostMicros != 14 || chat.EstimatedCalls != 0 || sum.EstimatedCalls != 1 || sum.OutputTokens != 1 {
		t.Fatalf("report rows = %+v", report.Rows)
	}
	if report.Totals.Calls != 2 || report.Currency != "USD" {
		t.Fatalf("totals %+v currency %q", report.Totals, report.Currency)
	}

	var calls struct{ Items []core.AIUsageRecord }
	h.mustStatus(h.do("GET", "/api/ai/usage/calls?sort=slowest", nil, &calls), 200, "own calls")
	if len(calls.Items) != 2 || calls.Items[0].UserID != admin.ID || calls.Items[1].UserID != admin.ID {
		t.Fatalf("calls = %+v", calls.Items)
	}
	var summary core.AIUsageSummary
	h.mustStatus(h.do("GET", "/api/ai/usage/summary", nil, &summary), 200, "summary")
	if summary.Month.Calls != 2 || summary.Today.Calls != 2 || summary.Budget.TokenLimit != 0 {
		t.Fatalf("summary = %+v", summary)
	}

	var settings core.Settings
	h.mustStatus(h.do("PATCH", "/api/settings", map[string]any{
		"instanceName": "Test", "allowSignup": true, "aiEnabled": true, "aiAllowUserKeys": true,
		"aiMonthlyTokenBudget": 5,
	}, &settings), 200, "set budget")
	if settings.AIMonthlyTokenBudget != 5 {
		t.Fatalf("settings = %+v", settings)
	}
	status, _, body = aiCtxRun(h, map[string]any{"task": "summarize", "text": "hello"})
	if status != http.StatusTooManyRequests || !strings.Contains(body, "budget") {
		t.Fatalf("run past the budget = %d %s", status, body)
	}
	var failed struct{ Items []core.AIUsageRecord }
	h.mustStatus(h.do("GET", "/api/admin/ai/usage/calls?sort=failed", nil, &failed), 200, "failed calls")
	if len(failed.Items) != 1 || failed.Items[0].Status != core.UsageBudget || failed.Items[0].InputTokens != 0 {
		t.Fatalf("failed calls = %+v", failed.Items)
	}
	var lifted core.AIBudget
	h.mustStatus(h.do("POST", "/api/admin/ai/budgets/user/"+admin.ID+"/lift", nil, &lifted), 200, "lift")
	if lifted.LiftedUntil == nil || lifted.Status == nil || lifted.Status.Stopped || !lifted.Status.Exceeded {
		t.Fatalf("lifted = %+v", lifted)
	}
	if status, _, body = aiCtxRun(h, map[string]any{"task": "summarize", "text": "hello"}); status != 200 {
		t.Fatalf("run after the lift = %d %s", status, body)
	}

	var user core.User
	h.mustStatus(h.do("POST", "/api/users", core.UserInput{
		Email: ptr("usage-user@example.com"), Name: ptr("Una User"), Password: ptr("another good password"),
	}, &user), 201, "create user")
	u := h.as("usage-user@example.com", "another good password")
	for _, path := range []string{"/api/admin/ai/usage", "/api/admin/ai/usage/calls", "/api/admin/ai/budgets"} {
		u.mustStatus(u.do("GET", path, nil, nil), 403, "user on "+path)
	}
	u.mustStatus(u.do("PUT", "/api/admin/ai/budgets/user/"+user.ID, map[string]any{"monthlyTokens": 0}, nil), 403, "user sets a budget")
	var mine core.AIUsageReport
	u.mustStatus(u.do("GET", "/api/ai/usage?groupBy=user", nil, &mine), 200, "user usage")
	if mine.Totals.Calls != 0 || len(mine.Rows) != 0 {
		t.Fatalf("a new user sees usage: %+v", mine)
	}
	var userSettings map[string]any
	u.do("GET", "/api/settings", nil, &userSettings)
	if _, ok := userSettings["aiMonthlyTokenBudget"]; ok {
		t.Fatalf("user settings show the budget default: %v", userSettings)
	}

	for _, path := range []string{"/api/ai/usage", "/api/ai/usage/summary", "/api/ai/usage/calls", "/api/admin/ai/usage"} {
		anon, err := http.Get(h.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		anon.Body.Close()
		if anon.StatusCode != http.StatusUnauthorized {
			t.Errorf("anonymous %s = %d, want 401", path, anon.StatusCode)
		}
	}
	var budgets struct{ Items []core.AIBudget }
	h.mustStatus(h.do("GET", "/api/admin/ai/budgets", nil, &budgets), 200, "budgets")
	if len(budgets.Items) != 1 || budgets.Items[0].SubjectID != admin.ID {
		t.Fatalf("budgets = %+v", budgets.Items)
	}
	raw, _ := json.Marshal(budgets.Items[0])
	if !bytes.Contains(raw, []byte(`"monthlyTokens":null`)) {
		t.Fatalf("a lift-only override should keep null limits: %s", raw)
	}
}
