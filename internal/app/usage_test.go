package app

import (
	"context"
	"errors"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// usageOf returns the usage rows of one user in record order.
func usageOf(h *harness, userID string) []core.AIUsageRecord {
	var out []core.AIUsageRecord
	for _, r := range h.db.usageRows() {
		if r.UserID == userID {
			out = append(out, r)
		}
	}
	return out
}

// lastUsageOf returns the newest usage row of one user.
func lastUsageOf(t *testing.T, h *harness, userID string) core.AIUsageRecord {
	t.Helper()
	rows := usageOf(h, userID)
	if len(rows) == 0 {
		t.Fatalf("no usage row for %s", userID)
	}
	return rows[len(rows)-1]
}

// setBudgetDefaults stores settings with AI on and the default budgets.
func setBudgetDefaults(t *testing.T, h *harness, tokens int64, cost float64) {
	t.Helper()
	admin := h.user("Settings"+core.NewID(), true)
	_, err := h.app.UpdateSettings(h.ctx, admin, &core.Settings{
		InstanceName: "T", AllowSignup: true, AIEnabled: true, AIAllowUserKeys: true,
		AIMonthlyTokenBudget: tokens, AIMonthlyCostBudget: cost,
	})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
}

// usageTokens makes the fake provider report fixed token counts on both the
// completion and the stream path.
func usageTokens(h *harness, prompt, completion int) {
	h.ai.mu.Lock()
	defer h.ai.mu.Unlock()
	h.ai.complete = func(context.Context, []core.AIMessage, core.AIOptions) (*core.AIResult, error) {
		return &core.AIResult{Text: "answer", Model: "chat-2", PromptTokens: prompt, CompletionTokens: completion}, nil
	}
	h.ai.stream = func(context.Context, []core.AIMessage, core.AIOptions) (<-chan core.AIChunk, error) {
		ch := make(chan core.AIChunk, 2)
		ch <- core.AIChunk{Delta: "answer"}
		ch <- core.AIChunk{Done: true, Model: "chat-2", PromptTokens: prompt, CompletionTokens: completion}
		close(ch)
		return ch, nil
	}
}

// setStream replaces the fake provider stream.
func setStream(h *harness, fn func(ctx context.Context, msgs []core.AIMessage, opts core.AIOptions) (<-chan core.AIChunk, error)) {
	h.ai.mu.Lock()
	defer h.ai.mu.Unlock()
	h.ai.stream = fn
}

// drainUsageStream reads a stream until it closes and returns the text, the
// Done chunk and the last error.
func drainUsageStream(t *testing.T, ch <-chan core.AIChunk) (string, *core.AIChunk, error) {
	t.Helper()
	var (
		text strings.Builder
		done *core.AIChunk
		err  error
	)
	timeout := time.After(5 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return text.String(), done, err
			}
			text.WriteString(c.Delta)
			if c.Done {
				cc := c
				done = &cc
			}
			if c.Err != nil {
				err = c.Err
			}
		case <-timeout:
			t.Fatal("stream did not close")
		}
	}
}

// summarize is a plain AI request with no note.
var summarize = core.AIRequest{Task: core.TaskSummarize, Text: "some text to summarize"}

func TestUsageRecordedForRunAI(t *testing.T) {
	h := newHarness(t)
	setModelPrice(h, "local", "chat", core.AIPrice{InputPerMTok: 1000, OutputPerMTok: 2000})
	u := h.user("Runner", false)
	usageTokens(h, 12, 3)

	res, err := h.app.RunAI(h.ctx, u, summarize)
	if err != nil {
		t.Fatalf("RunAI: %v", err)
	}
	if res.Budget != nil {
		t.Fatalf("budget warning without a budget: %+v", res.Budget)
	}
	got := lastUsageOf(t, h, u.UserID())
	want := core.AIUsageRecord{
		ID: got.ID, UserID: u.UserID(), ProviderID: "local", Model: "chat-2", Feature: "summarize",
		Task: core.RouteAnswer, Attempts: 1,
		InputTokens: 12, OutputTokens: 3, Status: core.UsageOK, CostMicros: 18000,
		DurationMs: got.DurationMs, CreatedAt: h.clock.Now(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("usage row\n got %+v\nwant %+v", got, want)
	}

	req := summarize
	req.Feature, req.PluginID = "rewrite", "plug"
	if _, err := h.app.RunAI(h.ctx, u, req); err != nil {
		t.Fatal(err)
	}
	if got := lastUsageOf(t, h, u.UserID()); got.Feature != "rewrite" || got.PluginID != "plug" {
		t.Fatalf("labels = %q %q", got.Feature, got.PluginID)
	}
	req.Feature = strings.Repeat("x", 100)
	if _, err := h.app.RunAI(h.ctx, u, req); err != nil {
		t.Fatal(err)
	}
	if got := lastUsageOf(t, h, u.UserID()); len(got.Feature) != maxFeatureBytes {
		t.Fatalf("long feature kept %d bytes", len(got.Feature))
	}
	sum, err := h.app.AIUsageSummary(h.ctx, u)
	if err != nil || sum.Currency != "USD" || sum.Today.Calls != 3 || sum.Month.Tokens != 45 || sum.Month.Cost != 0.054 {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
}

func TestUsageEstimatedWithoutTokenCounts(t *testing.T) {
	h := newHarness(t)
	u := h.user("Estimator", false)
	res, err := h.app.RunAI(h.ctx, u, summarize)
	if err != nil {
		t.Fatal(err)
	}
	got := lastUsageOf(t, h, u.UserID())
	if !got.Estimated || got.InputTokens <= 0 || got.OutputTokens != estimateTokens(len([]rune(res.Text))) {
		t.Fatalf("estimated row = %+v for text %q", got, res.Text)
	}
	ch, err := h.app.StreamAI(h.ctx, u, summarize)
	if err != nil {
		t.Fatal(err)
	}
	if _, done, err := drainUsageStream(t, ch); done == nil || err != nil {
		t.Fatalf("stream done %v err %v", done, err)
	}
	got = lastUsageOf(t, h, u.UserID())
	if !got.Estimated || got.Status != core.UsageOK || got.OutputTokens != 1 || got.InputTokens <= 0 {
		t.Fatalf("estimated stream row = %+v", got)
	}
	if estimateTokens(0) != 0 || estimateTokens(1) != 1 || estimateTokens(4) != 1 || estimateTokens(5) != 2 {
		t.Fatal("estimateTokens is not ceil(runes / 4)")
	}
}

func TestUsageStreamOutcomes(t *testing.T) {
	h := newHarness(t)
	u := h.user("Streamer", false)

	usageTokens(h, 7, 2)
	ch, err := h.app.StreamAI(h.ctx, u, summarize)
	if err != nil {
		t.Fatal(err)
	}
	if text, done, err := drainUsageStream(t, ch); text != "answer" || done == nil || err != nil {
		t.Fatalf("stream = %q %v %v", text, done, err)
	}
	if got := lastUsageOf(t, h, u.UserID()); got.Status != core.UsageOK || got.InputTokens != 7 ||
		got.OutputTokens != 2 || got.Estimated || got.Model != "chat-2" {
		t.Fatalf("success row = %+v", got)
	}

	setStream(h, func(context.Context, []core.AIMessage, core.AIOptions) (<-chan core.AIChunk, error) {
		ch := make(chan core.AIChunk, 2)
		ch <- core.AIChunk{Delta: "part"}
		ch <- core.AIChunk{Err: errors.New("upstream 500")}
		close(ch)
		return ch, nil
	})
	ch, err = h.app.StreamAI(h.ctx, u, summarize)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := drainUsageStream(t, ch); err == nil {
		t.Fatal("stream error was not forwarded")
	}
	if got := lastUsageOf(t, h, u.UserID()); got.Status != core.UsageError || !strings.Contains(got.Error, "upstream 500") ||
		got.InputTokens != 0 || got.OutputTokens != 0 {
		t.Fatalf("error row = %+v", got)
	}

	setStream(h, func(context.Context, []core.AIMessage, core.AIOptions) (<-chan core.AIChunk, error) {
		return nil, errors.New("connection refused")
	})
	if _, err := h.app.StreamAI(h.ctx, u, summarize); err == nil {
		t.Fatal("StreamAI hid the provider error")
	}
	if got := lastUsageOf(t, h, u.UserID()); got.Status != core.UsageError || !strings.Contains(got.Error, "connection refused") {
		t.Fatalf("start error row = %+v", got)
	}

	setStream(h, func(ctx context.Context, _ []core.AIMessage, _ core.AIOptions) (<-chan core.AIChunk, error) {
		ch := make(chan core.AIChunk)
		go func() {
			defer close(ch)
			select {
			case ch <- core.AIChunk{Delta: "first words"}:
			case <-ctx.Done():
				return
			}
			<-ctx.Done()
		}()
		return ch, nil
	})
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	ch, err = h.app.StreamAI(ctx, u, summarize)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case first := <-ch:
		if first.Delta != "first words" {
			t.Fatalf("first chunk = %+v", first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no first chunk")
	}
	cancel()
	drainUsageStream(t, ch)
	got := lastUsageOf(t, h, u.UserID())
	if got.Status != core.UsageCanceled || got.OutputTokens != 3 || !got.Estimated {
		t.Fatalf("canceled row = %+v", got)
	}
	if n := len(usageOf(h, u.UserID())); n != 4 {
		t.Fatalf("%d usage rows for 4 stream calls", n)
	}
}

func TestUsageRecordedForEmbeddingsSearchAndTest(t *testing.T) {
	h := newHarness(t)
	admin := h.user("Admin", true)
	sp := h.space(admin, "Docs")
	// The fake index does not model folders, so the note is written straight
	// into the fakes instead of through CreateNote.
	now := h.clock.Now()
	n := &core.Note{
		ID: core.NewID(), SpaceID: sp.ID, Title: "Long", Path: "long.md", Slug: "long",
		Content: strings.Repeat("word ", 4000), CreatedAt: now, UpdatedAt: now, CreatedBy: admin.UserID(),
	}
	if err := h.files.Write(sp.Slug, n); err != nil {
		t.Fatal(err)
	}
	if err := h.db.UpsertNote(h.ctx, ports.NoteRow{Note: n, Plain: n.Content}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.ReindexEmbeddings(h.ctx, admin, sp.ID); err != nil {
		t.Fatalf("ReindexEmbeddings: %v", err)
	}
	disk, err := h.files.Read(sp.Slug, n.Path)
	if err != nil {
		t.Fatal(err)
	}
	chunks := len(fakeRenderer{}.Chunks(disk.Content, embedChunkRunes))
	var batches []core.AIUsageRecord
	for _, r := range usageOf(h, admin.UserID()) {
		if r.Feature == core.FeatureEmbeddings && r.NoteID == n.ID {
			batches = append(batches, r)
		}
	}
	if want := (chunks + embedBatch - 1) / embedBatch; len(batches) != want || want < 2 {
		t.Fatalf("%d embedding rows for %d chunks, want %d", len(batches), chunks, want)
	}
	for _, r := range batches {
		if r.Status != core.UsageOK || !r.Estimated || r.InputTokens <= 0 || r.Model != "embed" || r.SpaceID != sp.ID {
			t.Fatalf("embedding row = %+v", r)
		}
	}

	if _, err := h.app.Search(h.ctx, admin, core.SearchQuery{Text: "word", Mode: "semantic"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := lastUsageOf(t, h, admin.UserID()); got.Feature != core.FeatureSearch || got.InputTokens != 1 {
		t.Fatalf("search row = %+v", got)
	}

	if err := h.app.TestAIProvider(h.ctx, admin, "local"); err != nil {
		t.Fatalf("TestAIProvider: %v", err)
	}
	if got := lastUsageOf(t, h, admin.UserID()); got.Feature != core.FeatureTest || got.ProviderID != "local" || got.Status != core.UsageOK {
		t.Fatalf("test row = %+v", got)
	}
}

func TestUsageRecordingFailureDoesNotFailCall(t *testing.T) {
	h := newHarness(t)
	h.db.mu.Lock()
	h.db.usageErr = errors.New("disk full")
	h.db.mu.Unlock()
	admin := h.user("Admin", true)
	if res, err := h.app.RunAI(h.ctx, admin, summarize); err != nil || res.Text == "" {
		t.Fatalf("RunAI with a failing usage store = %v, %v", res, err)
	}
	ch, err := h.app.StreamAI(h.ctx, admin, summarize)
	if err != nil {
		t.Fatal(err)
	}
	if _, done, err := drainUsageStream(t, ch); done == nil || err != nil {
		t.Fatalf("stream with a failing usage store: done %v err %v", done, err)
	}
	if err := h.app.TestAIProvider(h.ctx, admin, "local"); err != nil {
		t.Fatalf("TestAIProvider with a failing usage store: %v", err)
	}
	if rows := h.db.usageRows(); len(rows) != 0 {
		t.Fatalf("rows stored despite the failure: %d", len(rows))
	}
}

func TestBudgetStopsAtLimit(t *testing.T) {
	h := newHarness(t)
	setBudgetDefaults(t, h, 100, 0)
	u := h.user("Spender", false)
	usageTokens(h, 60, 0)

	res, err := h.app.RunAI(h.ctx, u, summarize)
	if err != nil || res.Budget != nil {
		t.Fatalf("first call = %+v, %v", res, err)
	}
	res, err = h.app.RunAI(h.ctx, u, summarize)
	if err != nil || res.Budget == nil || !res.Budget.Warn || res.Budget.UsedPct != 120 {
		t.Fatalf("second call = %+v, %v", res, err)
	}
	if _, err := h.app.RunAI(h.ctx, u, summarize); !errors.Is(err, core.ErrQuotaExceeded) {
		t.Fatalf("third call = %v, want ErrQuotaExceeded", err)
	}
	got := lastUsageOf(t, h, u.UserID())
	if got.Status != core.UsageBudget || got.InputTokens != 0 || got.OutputTokens != 0 || got.CostMicros != 0 ||
		got.Feature != "summarize" || got.ProviderID != "local" {
		t.Fatalf("budget row = %+v", got)
	}
	if _, err := h.app.StreamAI(h.ctx, u, summarize); !errors.Is(err, core.ErrQuotaExceeded) {
		t.Fatalf("stream past the budget = %v", err)
	}
	sum, err := h.app.AIUsageSummary(h.ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if b := sum.Budget; !b.Stopped || !b.Exceeded || !b.Warn || b.TokensUsed != 120 || b.TokenLimit != 100 || b.UsedPct != 120 {
		t.Fatalf("budget status = %+v", b)
	}
	if sum.Month.Calls != 4 || sum.Month.Tokens != 120 {
		t.Fatalf("month = %+v", sum.Month)
	}

	other := h.user("Other", false)
	if _, err := h.app.RunAI(h.ctx, other, summarize); err != nil {
		t.Fatalf("another user was stopped: %v", err)
	}

	boss := h.user("Boss", true)
	for i := 0; i < 2; i++ {
		if _, err := h.app.RunAI(h.ctx, boss, summarize); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.app.RunAI(h.ctx, boss, summarize); !errors.Is(err, core.ErrQuotaExceeded) {
		t.Fatalf("admin past the default budget = %v", err)
	}
	zero := int64(0)
	if _, err := h.app.SaveAIBudget(h.ctx, boss, core.AIBudget{SubjectKind: core.BudgetUser, SubjectID: boss.UserID(), MonthlyTokens: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.RunAI(h.ctx, boss, summarize); err != nil {
		t.Fatalf("admin with an unlimited override = %v", err)
	}
}

func TestBudgetWarnsAt80(t *testing.T) {
	h := newHarness(t)
	setBudgetDefaults(t, h, 100, 0)

	usageTokens(h, 79, 0)
	if res, err := h.app.RunAI(h.ctx, h.user("Low", false), summarize); err != nil || res.Budget != nil {
		t.Fatalf("79 percent = %+v, %v", res, err)
	}
	usageTokens(h, 80, 0)
	if res, err := h.app.RunAI(h.ctx, h.user("Edge", false), summarize); err != nil || res.Budget == nil ||
		*res.Budget != (core.AIBudgetWarning{UsedPct: 80, Warn: true}) {
		t.Fatalf("80 percent = %+v, %v", res, err)
	}
	usageTokens(h, 50, 30)
	ch, err := h.app.StreamAI(h.ctx, h.user("Stream", false), summarize)
	if err != nil {
		t.Fatal(err)
	}
	if _, done, _ := drainUsageStream(t, ch); done == nil || done.Budget == nil || done.Budget.UsedPct != 80 {
		t.Fatalf("stream done at 80 percent = %+v", done)
	}
}

func TestCostAndPluginBudgets(t *testing.T) {
	h := newHarness(t)
	setModelPrice(h, "local", "chat", core.AIPrice{InputPerMTok: 1000})
	admin := h.user("Admin", true)
	u := h.user("User", false)
	cost := 0.1
	if _, err := h.app.SaveAIBudget(h.ctx, admin, core.AIBudget{SubjectKind: core.BudgetUser, SubjectID: u.UserID(), MonthlyCost: &cost}); err != nil {
		t.Fatal(err)
	}
	usageTokens(h, 60, 0) // 0.06 per call
	for i := 0; i < 2; i++ {
		if _, err := h.app.RunAI(h.ctx, u, summarize); err != nil {
			t.Fatalf("call %d under the cost budget: %v", i, err)
		}
	}
	if _, err := h.app.RunAI(h.ctx, u, summarize); !errors.Is(err, core.ErrQuotaExceeded) {
		t.Fatalf("call past the cost budget = %v", err)
	}

	tokens := int64(50)
	if _, err := h.app.SaveAIBudget(h.ctx, admin, core.AIBudget{SubjectKind: core.BudgetPlugin, SubjectID: "pl", MonthlyTokens: &tokens}); err != nil {
		t.Fatal(err)
	}
	v := h.user("V", false)
	fromPlugin := summarize
	fromPlugin.PluginID = "pl"
	if _, err := h.app.RunAI(h.ctx, v, fromPlugin); err != nil {
		t.Fatal(err)
	}
	_, err := h.app.RunAI(h.ctx, v, fromPlugin)
	if !errors.Is(err, core.ErrQuotaExceeded) || !strings.Contains(err.Error(), "plugin pl") {
		t.Fatalf("plugin past its budget = %v", err)
	}
	if got := lastUsageOf(t, h, v.UserID()); got.Status != core.UsageBudget || got.PluginID != "pl" {
		t.Fatalf("plugin budget row = %+v", got)
	}
	if _, err := h.app.RunAI(h.ctx, v, summarize); err != nil {
		t.Fatalf("the user's own call was stopped by the plugin budget: %v", err)
	}

	list, err := h.app.ListAIBudgets(h.ctx, admin)
	if err != nil || len(list) != 2 {
		t.Fatalf("budgets = %+v, %v", list, err)
	}
	for _, b := range list {
		if b.Status == nil || !b.Status.Stopped {
			t.Fatalf("budget %s/%s status = %+v", b.SubjectKind, b.SubjectID, b.Status)
		}
	}
}

func TestBudgetLiftLastsUntilMonthEnd(t *testing.T) {
	h := newHarness(t)
	setBudgetDefaults(t, h, 50, 0)
	admin := h.user("Lifter", true)
	u := h.user("Stopped", false)
	usageTokens(h, 60, 0)
	if _, err := h.app.RunAI(h.ctx, u, summarize); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.RunAI(h.ctx, u, summarize); !errors.Is(err, core.ErrQuotaExceeded) {
		t.Fatalf("before the lift = %v", err)
	}
	b, err := h.app.LiftAIBudget(h.ctx, admin, core.BudgetUser, u.UserID())
	if err != nil {
		t.Fatal(err)
	}
	monthEnd := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if b.LiftedUntil == nil || !b.LiftedUntil.Equal(monthEnd) || b.Status.Stopped || !b.Status.Exceeded {
		t.Fatalf("lifted budget = %+v status %+v", b, b.Status)
	}
	if _, err := h.app.RunAI(h.ctx, u, summarize); err != nil {
		t.Fatalf("after the lift = %v", err)
	}
	if _, err := h.app.SaveAIBudget(h.ctx, admin, core.AIBudget{SubjectKind: core.BudgetUser, SubjectID: u.UserID()}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.RunAI(h.ctx, u, summarize); err != nil {
		t.Fatalf("saving limits dropped the lift: %v", err)
	}

	h.clock.Advance(monthEnd.Sub(h.clock.Now()) + time.Hour)
	if _, err := h.app.RunAI(h.ctx, u, summarize); err != nil {
		t.Fatalf("first call of the new month = %v", err)
	}
	if _, err := h.app.RunAI(h.ctx, u, summarize); !errors.Is(err, core.ErrQuotaExceeded) {
		t.Fatalf("the lift outlived its month: %v", err)
	}

	if _, err := h.app.LiftAIBudget(h.ctx, admin, core.BudgetUser, "nobody"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("lift of an unknown user = %v", err)
	}
	if _, err := h.app.LiftAIBudget(h.ctx, admin, "team", "x"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("lift of an unknown kind = %v", err)
	}
	if _, err := h.app.LiftAIBudget(h.ctx, u, core.BudgetUser, u.UserID()); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("self lift = %v", err)
	}
	if err := h.app.DeleteAIBudget(h.ctx, admin, core.BudgetUser, u.UserID()); err != nil {
		t.Fatal(err)
	}
	if err := h.app.DeleteAIBudget(h.ctx, admin, core.BudgetUser, u.UserID()); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	if _, err := h.app.RunAI(h.ctx, u, summarize); !errors.Is(err, core.ErrQuotaExceeded) {
		t.Fatalf("deleting the override did not restore the default: %v", err)
	}
}

// TestMyAIUsageSeesOnlyOwnRows runs random calls for three users and checks,
// for several groupings, that a user's own report equals the admin report
// filtered to that user and the brute-force sum of their rows, whatever user
// id the request names.
func TestMyAIUsageSeesOnlyOwnRows(t *testing.T) {
	h := newHarness(t)
	admin := h.user("Admin", true)
	alice := h.user("Alice", false)
	bob := h.user("Bob", false)
	actors := []*core.Actor{admin, alice, bob}
	r := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 60; i++ {
		usageTokens(h, r.IntN(500)+1, r.IntN(200))
		req := core.AIRequest{Task: core.TaskAsk, Question: "q", Feature: []string{"ask", "chat", "rewrite"}[r.IntN(3)]}
		if _, err := h.app.RunAI(h.ctx, actors[r.IntN(3)], req); err != nil {
			t.Fatal(err)
		}
	}
	for _, groups := range [][]string{nil, {"feature"}, {"user"}, {"day", "model"}, {"provider", "feature", "plugin"}} {
		for _, pair := range [][2]*core.Actor{{alice, bob}, {bob, alice}} {
			self, other := pair[0], pair[1]
			mine, err := h.app.MyAIUsage(h.ctx, self, core.AIUsageQuery{GroupBy: groups, UserID: other.UserID()})
			if err != nil {
				t.Fatal(err)
			}
			theirs, err := h.app.AIUsage(h.ctx, admin, core.AIUsageQuery{GroupBy: groups, UserID: self.UserID()})
			if err != nil {
				t.Fatal(err)
			}
			for i := range theirs.Rows {
				theirs.Rows[i].UserName = ""
			}
			if !reflect.DeepEqual(mine.Rows, theirs.Rows) || mine.Totals != theirs.Totals {
				t.Fatalf("group %v: own report differs from the admin view of %s", groups, self.User.Name)
			}
			var calls, tokens int64
			for _, row := range usageOf(h, self.UserID()) {
				calls++
				tokens += row.InputTokens + row.OutputTokens
			}
			if mine.Totals.Calls != calls || mine.Totals.InputTokens+mine.Totals.OutputTokens != tokens {
				t.Fatalf("group %v: totals %+v, brute force %d calls %d tokens", groups, mine.Totals, calls, tokens)
			}
			for _, row := range mine.Rows {
				if row.UserID != "" && row.UserID != self.UserID() {
					t.Fatalf("own report of %s holds a row of %s", self.UserID(), row.UserID)
				}
			}
		}
	}
	named, err := h.app.AIUsage(h.ctx, admin, core.AIUsageQuery{GroupBy: []string{"user"}})
	if err != nil || len(named.Rows) == 0 {
		t.Fatal(err)
	}
	for _, row := range named.Rows {
		if row.UserName == "" {
			t.Fatalf("admin report row without a user name: %+v", row)
		}
	}

	if _, err := h.app.AIUsageCalls(h.ctx, alice, core.AIUsageQuery{UserID: bob.UserID()}, ""); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("calls of another user = %v", err)
	}
	if _, err := h.app.AIUsageCalls(h.ctx, alice, core.AIUsageQuery{}, ""); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("calls of every user as a user = %v", err)
	}
	own, err := h.app.AIUsageCalls(h.ctx, alice, core.AIUsageQuery{UserID: alice.UserID(), Limit: 500}, core.UsageSortSlowest)
	if err != nil || len(own) != len(usageOf(h, alice.UserID())) {
		t.Fatalf("own calls = %d, %v", len(own), err)
	}
	for _, c := range own {
		if c.UserID != alice.UserID() {
			t.Fatalf("own calls hold a call of %s", c.UserID)
		}
	}
	if _, err := h.app.AIUsage(h.ctx, alice, core.AIUsageQuery{}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("AIUsage as a user = %v", err)
	}
	if _, err := h.app.MyAIUsage(h.ctx, nil, core.AIUsageQuery{}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("MyAIUsage anonymous = %v", err)
	}
	if _, err := h.app.AIUsageSummary(h.ctx, nil); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("AIUsageSummary anonymous = %v", err)
	}
	if _, err := h.app.ListAIBudgets(h.ctx, alice); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("ListAIBudgets as a user = %v", err)
	}
	if _, err := h.app.SaveAIBudget(h.ctx, alice, core.AIBudget{SubjectKind: core.BudgetUser, SubjectID: alice.UserID()}); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("SaveAIBudget as a user = %v", err)
	}
	if err := h.app.DeleteAIBudget(h.ctx, alice, core.BudgetUser, alice.UserID()); !errors.Is(err, core.ErrForbidden) {
		t.Fatalf("DeleteAIBudget as a user = %v", err)
	}
	if _, err := h.app.MyAIUsage(h.ctx, alice, core.AIUsageQuery{GroupBy: []string{"colour"}}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown group = %v", err)
	}
	now := h.clock.Now()
	if _, err := h.app.MyAIUsage(h.ctx, alice, core.AIUsageQuery{From: now, To: now.Add(-time.Hour)}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("reversed range = %v", err)
	}
	if _, err := h.app.AIUsageCalls(h.ctx, admin, core.AIUsageQuery{}, "loudest"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown sort = %v", err)
	}
}

func TestAIUsageSummaryPeriods(t *testing.T) {
	h := newHarness(t)
	u := h.user("Summary", false)
	other := h.user("Other", false)
	now := h.clock.Now() // 2026-09-13 09:00 UTC
	for _, rec := range []core.AIUsageRecord{
		{UserID: u.UserID(), InputTokens: 5, OutputTokens: 5, CostMicros: 1000, CreatedAt: now.Add(-time.Hour)},
		{UserID: u.UserID(), InputTokens: 10, CreatedAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)},
		{UserID: u.UserID(), InputTokens: 1000, CreatedAt: time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)},
		{UserID: other.UserID(), InputTokens: 7, CreatedAt: now},
	} {
		if err := h.db.RecordAIUsage(h.ctx, &rec); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := h.app.AIUsageSummary(h.ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Today != (core.AIUsagePeriod{Calls: 1, InputTokens: 5, OutputTokens: 5, Tokens: 10, Cost: 0.001}) {
		t.Fatalf("today = %+v", sum.Today)
	}
	if sum.Month.Calls != 2 || sum.Month.Tokens != 20 {
		t.Fatalf("month = %+v", sum.Month)
	}
	if sum.Budget.TokenLimit != 0 || sum.Budget.Stopped || sum.Budget.Warn || sum.Budget.UsedPct != 0 {
		t.Fatalf("budget without limits = %+v", sum.Budget)
	}
}

func TestSettingsBudgetDefaults(t *testing.T) {
	h := newHarness(t)
	admin := h.user("Admin", true)
	u := h.user("User", false)
	base := core.Settings{InstanceName: "T", AIEnabled: true}
	for _, bad := range []core.Settings{{AIMonthlyTokenBudget: -1}, {AIMonthlyCostBudget: -0.5}} {
		in := base
		in.AIMonthlyTokenBudget, in.AIMonthlyCostBudget = bad.AIMonthlyTokenBudget, bad.AIMonthlyCostBudget
		if _, err := h.app.UpdateSettings(h.ctx, admin, &in); !errors.Is(err, core.ErrInvalid) {
			t.Fatalf("negative budget %+v = %v", bad, err)
		}
	}
	in := base
	in.AIMonthlyTokenBudget, in.AIMonthlyCostBudget = 1000, 2.5
	if _, err := h.app.UpdateSettings(h.ctx, admin, &in); err != nil {
		t.Fatal(err)
	}
	if s, _ := h.app.GetSettings(h.ctx, admin); s.AIMonthlyTokenBudget != 1000 || s.AIMonthlyCostBudget != 2.5 {
		t.Fatalf("admin settings = %+v", s)
	}
	if s, _ := h.app.GetSettings(h.ctx, u); s.AIMonthlyTokenBudget != 0 || s.AIMonthlyCostBudget != 0 {
		t.Fatalf("user sees the budget defaults: %+v", s)
	}
}

func TestUsageRollupCutoffAndUserDelete(t *testing.T) {
	h := newHarness(t)
	h.app.rollupAIUsage(h.ctx)
	if got := h.db.rollupBefore; len(got) != 1 || !got[0].Equal(time.Date(2025, 8, 13, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("rollup cutoffs = %v", got)
	}

	admin := h.user("Admin", true)
	u := h.user("Gone", false)
	if _, err := h.app.SaveAIBudget(h.ctx, admin, core.AIBudget{SubjectKind: core.BudgetUser, SubjectID: u.UserID()}); err != nil {
		t.Fatal(err)
	}
	if err := h.app.DeleteUser(h.ctx, admin, u.UserID()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.GetAIBudget(h.ctx, core.BudgetUser, u.UserID()); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("budget of a deleted user = %v", err)
	}
}
