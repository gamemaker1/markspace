package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// usageRecordTimeout bounds one usage write, which runs even after the
	// request context is canceled.
	usageRecordTimeout = 5 * time.Second
	// budgetWarnPct is the share of a budget at which results carry a warning.
	budgetWarnPct = 80
	// maxFeatureBytes and maxPluginIDBytes cap the free-form usage labels.
	maxFeatureBytes  = 40
	maxPluginIDBytes = 64
	// usageRetentionMonths is how long raw usage rows stay before retention
	// folds them into monthly rows.
	usageRetentionMonths = 13
	// usageRollupInterval is how often the retention job runs.
	usageRollupInterval = 24 * time.Hour
	// defaultCallLimit and maxCallLimit bound a call listing.
	defaultCallLimit = 50
	maxCallLimit     = 500
	// maxReportRows bounds a usage report.
	maxReportRows = 5000
)

// monthStart returns the first instant of the UTC month containing t.
func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// dayStart returns the first instant of the UTC day containing t.
func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// cutLabel trims a free-form label and cuts it to n bytes on a rune boundary.
func cutLabel(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// usageFeature returns the usage label of a request: its feature, else its
// task name.
func usageFeature(req core.AIRequest) string {
	if f := cutLabel(req.Feature, maxFeatureBytes); f != "" {
		return f
	}
	return cutLabel(string(req.Task), maxFeatureBytes)
}

// estimateTokens is ceil(runes / 4), the estimate used when a provider
// reports no token counts.
func estimateTokens(runes int) int64 {
	if runes <= 0 {
		return 0
	}
	return int64((runes + 3) / 4)
}

// promptSize counts the runes and images of a transcript.
func promptSize(msgs []core.AIMessage) (runes int, images int) {
	for _, m := range msgs {
		runes += utf8.RuneCountInString(m.Content)
		images += len(m.Images)
	}
	return runes, images
}

// usageStatus classifies a call error. A canceled context is a cancel, not
// a provider failure.
func usageStatus(ctx context.Context, err error) string {
	switch {
	case err == nil:
		return core.UsageOK
	case errors.Is(err, context.Canceled) || ctx.Err() != nil:
		return core.UsageCanceled
	}
	return core.UsageError
}

// fillUsage completes a usage record for a finished call. When the provider
// reported neither token count and the call reached it, both counts are
// estimated from the text. It also sets the duration and prices the call with
// the effective price of the model at call time. A nil price costs 0.
func fillUsage(rec *core.AIUsageRecord, price *core.AIPrice, promptRunes int, prompt, completion int, output string, start time.Time) {
	rec.InputTokens = int64(max(prompt, 0))
	rec.OutputTokens = int64(max(completion, 0))
	reached := rec.Status == core.UsageOK || rec.Status == core.UsageCanceled
	if reached && rec.InputTokens == 0 && rec.OutputTokens == 0 && (promptRunes > 0 || output != "") {
		rec.InputTokens = estimateTokens(promptRunes)
		rec.OutputTokens = estimateTokens(utf8.RuneCountInString(output))
		rec.Estimated = true
	}
	if !start.IsZero() {
		rec.DurationMs = time.Since(start).Milliseconds()
	}
	rec.Attempts = max(rec.Attempts, 1)
	rec.CostMicros = 0
	if price != nil && (rec.Status == core.UsageOK || rec.Status == core.UsageCanceled) {
		rec.CostMicros = price.CostMicros(rec.InputTokens, rec.OutputTokens, rec.ImagesIn, rec.ImagesOut)
	}
}

// recordUsage stores one usage row. It never fails the caller: a failed
// write is logged at warn. It writes even when ctx is already canceled.
func (a *App) recordUsage(ctx context.Context, rec core.AIUsageRecord) {
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = a.now()
	}
	if rec.Error != "" {
		rec.Error = cutLabel(rec.Error, 500)
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), usageRecordTimeout)
	defer cancel()
	if err := a.db.RecordAIUsage(wctx, &rec); err != nil {
		a.log.Warn("record ai usage failed", "userId", rec.UserID, "providerId", rec.ProviderID,
			"feature", rec.Feature, "status", rec.Status, "err", err)
	}
}

// errorText returns the message of err, or the empty string.
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// recordEmbed stores the usage row of one embedding request. Embedding APIs
// report no token counts, so the input is always estimated.
func (a *App) recordEmbed(ctx context.Context, userID, feature, spaceID, noteID string, target *embedTarget, texts []string, start time.Time, err error) {
	runes := 0
	for _, t := range texts {
		runes += utf8.RuneCountInString(t)
	}
	rec := core.AIUsageRecord{
		UserID: userID, ProviderID: target.cfg.ID, Model: target.model, Feature: feature,
		Task: core.RouteEmbeddings, SpaceID: spaceID, NoteID: noteID, Status: usageStatus(ctx, err),
		Error: errorText(err), CreatedAt: a.now(),
	}
	if err != nil {
		a.noteCallError(ctx, target.cfg.ID, err)
	}
	fillUsage(&rec, target.price, runes, 0, 0, "", start)
	a.recordUsage(ctx, rec)
}

// budgetState is the month-to-date position of one subject against its
// limits.
type budgetState struct {
	kind, id        string
	tokenLimit      int64
	costLimitMicros int64
	tokensUsed      int64
	costUsedMicros  int64
	liftedUntil     *time.Time
}

// limited reports whether the subject has any limit.
func (b budgetState) limited() bool { return b.tokenLimit > 0 || b.costLimitMicros > 0 }

// pct returns the used share in percent after adding a call's tokens and
// cost. It is the higher of the token share and the cost share.
func (b budgetState) pct(tokens, micros int64) float64 {
	p := 0.0
	if b.tokenLimit > 0 {
		p = math.Max(p, float64(b.tokensUsed+tokens)/float64(b.tokenLimit)*100)
	}
	if b.costLimitMicros > 0 {
		p = math.Max(p, float64(b.costUsedMicros+micros)/float64(b.costLimitMicros)*100)
	}
	return p
}

// exceeded reports whether a limit is reached. It compares integers so an
// exact hit counts regardless of float rounding.
func (b budgetState) exceeded() bool {
	return (b.tokenLimit > 0 && b.tokensUsed >= b.tokenLimit) ||
		(b.costLimitMicros > 0 && b.costUsedMicros >= b.costLimitMicros)
}

// lifted reports whether an admin lift is active at now.
func (b budgetState) lifted(now time.Time) bool {
	return b.liftedUntil != nil && now.Before(*b.liftedUntil)
}

// status renders the state for clients.
func (b budgetState) status(now time.Time) core.AIBudgetStatus {
	st := core.AIBudgetStatus{
		TokenLimit:  b.tokenLimit,
		CostLimit:   core.MicrosToCost(b.costLimitMicros),
		TokensUsed:  b.tokensUsed,
		CostUsed:    core.MicrosToCost(b.costUsedMicros),
		UsedPct:     roundPct(b.pct(0, 0)),
		Exceeded:    b.exceeded(),
		LiftedUntil: b.liftedUntil,
	}
	st.Warn = b.limited() && b.pct(0, 0) >= budgetWarnPct
	st.Stopped = st.Exceeded && !b.lifted(now)
	return st
}

// roundPct rounds a percentage to one decimal.
func roundPct(p float64) float64 { return math.Round(p*10) / 10 }

// budgetFor loads the limits of one subject and, when it has any, its usage
// in the current UTC month. A user without an override uses the instance
// defaults. A plugin without an override has no limit.
func (a *App) budgetFor(ctx context.Context, settings *core.Settings, kind, id string) (budgetState, error) {
	b := budgetState{kind: kind, id: id}
	if kind == core.BudgetUser {
		b.tokenLimit = max(settings.AIMonthlyTokenBudget, 0)
		b.costLimitMicros = core.CostToMicros(settings.AIMonthlyCostBudget)
	}
	override, err := a.db.GetAIBudget(ctx, kind, id)
	switch {
	case errors.Is(err, core.ErrNotFound):
	case err != nil:
		return b, fmt.Errorf("load ai budget: %w", err)
	default:
		if override.MonthlyTokens != nil {
			b.tokenLimit = max(*override.MonthlyTokens, 0)
		}
		if override.MonthlyCost != nil {
			b.costLimitMicros = core.CostToMicros(*override.MonthlyCost)
		}
		b.liftedUntil = override.LiftedUntil
	}
	if !b.limited() {
		return b, nil
	}
	now := a.now()
	q := core.AIUsageQuery{From: monthStart(now), To: monthStart(now).AddDate(0, 1, 0)}
	if kind == core.BudgetUser {
		q.UserID = id
	} else {
		q.PluginID = id
	}
	_, totals, err := a.db.AIUsageReport(ctx, q)
	if err != nil {
		return b, fmt.Errorf("load ai usage: %w", err)
	}
	b.tokensUsed = totals.InputTokens + totals.OutputTokens
	b.costUsedMicros = totals.CostMicros
	return b, nil
}

// checkBudgets loads the budgets that apply to a call: the actor's, and the
// plugin's when the call comes from a plugin. It returns the budget that
// stops the call, if any.
func (a *App) checkBudgets(ctx context.Context, settings *core.Settings, userID, pluginID string) ([]budgetState, *budgetState, error) {
	var out []budgetState
	subjects := [][2]string{{core.BudgetUser, userID}}
	if pluginID != "" {
		subjects = append(subjects, [2]string{core.BudgetPlugin, pluginID})
	}
	now := a.now()
	for _, s := range subjects {
		b, err := a.budgetFor(ctx, settings, s[0], s[1])
		if err != nil {
			return nil, nil, err
		}
		out = append(out, b)
		if b.exceeded() && !b.lifted(now) {
			return out, &out[len(out)-1], nil
		}
	}
	return out, nil, nil
}

// budgetWarning returns the warning for a finished call once any budget it
// counts against reaches 80 percent, including the call itself.
func budgetWarning(states []budgetState, tokens, micros int64) *core.AIBudgetWarning {
	best := 0.0
	for _, b := range states {
		if b.limited() {
			best = math.Max(best, b.pct(tokens, micros))
		}
	}
	if best < budgetWarnPct {
		return nil
	}
	return &core.AIBudgetWarning{UsedPct: roundPct(best), Warn: true}
}

// quotaError builds the error for a stopped subject.
func quotaError(b *budgetState) error {
	if b.kind == core.BudgetPlugin {
		return fmt.Errorf("the monthly AI budget of plugin %s is used up: %w", b.id, core.ErrQuotaExceeded)
	}
	return fmt.Errorf("your monthly AI budget is used up: %w", core.ErrQuotaExceeded)
}

// meterStream forwards a provider stream whose first chunk was already
// read, puts the context sources, the provider, the attempts and any budget
// warning on the Done chunk, and records one usage row when the stream ends:
// ok after Done, error after Err, canceled when the consumer goes away first.
// The row is written before the returned channel closes.
func (a *App) meterStream(ctx context.Context, first core.AIChunk, in <-chan core.AIChunk, call *aiCall, t *callTarget, start time.Time) <-chan core.AIChunk {
	out := make(chan core.AIChunk, 1)
	go func() {
		var (
			text               strings.Builder
			prompt, completion int
			callErr            error
		)
		rec := t.usage
		rec.Status = core.UsageOK
		drain := func() {
			go func() {
				for range in {
				}
			}()
		}
		defer close(out)
		defer func() {
			rec.Error = errorText(callErr)
			if callErr != nil {
				a.noteCallError(ctx, t.cfg.ID, callErr)
			}
			fillUsage(&rec, t.price, call.promptRunes, prompt, completion, text.String(), start)
			a.recordUsage(ctx, rec)
		}()
		pending := true
		for {
			var (
				chunk core.AIChunk
				ok    bool
			)
			if pending {
				chunk, ok, pending = first, true, false
			} else {
				select {
				case chunk, ok = <-in:
				case <-ctx.Done():
					rec.Status = core.UsageCanceled
					drain()
					return
				}
			}
			if !ok {
				if ctx.Err() != nil {
					rec.Status = core.UsageCanceled
				}
				return
			}
			text.WriteString(chunk.Delta)
			switch {
			case chunk.Err != nil:
				callErr = chunk.Err
				rec.Status = usageStatus(ctx, chunk.Err)
			case chunk.Done:
				prompt, completion = chunk.PromptTokens, chunk.CompletionTokens
				if chunk.Model == "" {
					chunk.Model = t.model
				}
				rec.Model = chunk.Model
				chunk.Provider = t.cfg.ID
				chunk.Attempts = rec.Attempts
				chunk.Sources = call.sources
				probe := rec
				fillUsage(&probe, t.price, call.promptRunes, prompt, completion, text.String(), time.Time{})
				chunk.Budget = budgetWarning(call.budgets, probe.InputTokens+probe.OutputTokens, probe.CostMicros)
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				if !chunk.Done && chunk.Err == nil {
					rec.Status = core.UsageCanceled
				}
				drain()
				return
			}
			if chunk.Done || chunk.Err != nil {
				drain()
				return
			}
		}
	}()
	return out
}

// usageRetentionLoop folds old usage rows once at startup and then daily.
func (a *App) usageRetentionLoop(ctx context.Context) {
	a.rollupAIUsage(ctx)
	a.purgeAIThreads(ctx)
	t := time.NewTicker(usageRollupInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.rollupAIUsage(ctx)
			a.purgeAIThreads(ctx)
		}
	}
}

// purgeAIThreads deletes AI threads without activity for 90 days. The daily
// retention loop calls it next to the usage rollup.
func (a *App) purgeAIThreads(ctx context.Context) {
	if n, err := a.PurgeAIThreads(ctx); err != nil {
		a.log.Warn("ai thread purge failed", "err", err)
	} else if n > 0 {
		a.log.Info("purged ai threads", "threads", n)
	}
}

// rollupAIUsage folds raw usage rows from months that ended more than 13
// months ago into the monthly table.
func (a *App) rollupAIUsage(ctx context.Context) {
	cutoff := a.now().AddDate(0, -usageRetentionMonths, 0)
	months, rows, err := a.db.RollupAIUsage(ctx, cutoff)
	if err != nil {
		a.log.Warn("ai usage rollup failed", "err", err)
		return
	}
	if rows > 0 {
		a.log.Info("ai usage rolled up", "months", months, "rows", rows)
	}
}

// usageCurrency names the currency of recorded costs. Every catalog price is
// in US dollars.
func (a *App) usageCurrency(context.Context) string { return "USD" }

// normalizeUsageQuery validates a query and fills the defaults: the current
// UTC month when neither end is set, and the row limit.
func (a *App) normalizeUsageQuery(q core.AIUsageQuery) (core.AIUsageQuery, error) {
	groups, err := core.NormalizeUsageGroupBy(q.GroupBy)
	if err != nil {
		return q, err
	}
	q.GroupBy = groups
	if q.From.IsZero() && q.To.IsZero() {
		q.From = monthStart(a.now())
		q.To = q.From.AddDate(0, 1, 0)
	}
	if !q.From.IsZero() && !q.To.IsZero() && !q.From.Before(q.To) {
		return q, core.Invalid("to", "to must be after from")
	}
	if q.Limit <= 0 || q.Limit > maxReportRows {
		q.Limit = maxReportRows
	}
	q.From, q.To = q.From.UTC(), q.To.UTC()
	return q, nil
}

// usageReport runs a normalized query and decorates the rows.
func (a *App) usageReport(ctx context.Context, q core.AIUsageQuery, names bool) (*core.AIUsageReport, error) {
	q, err := a.normalizeUsageQuery(q)
	if err != nil {
		return nil, err
	}
	rows, totals, err := a.db.AIUsageReport(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("ai usage report: %w", err)
	}
	if rows == nil {
		rows = []core.AIUsageRow{}
	}
	if names {
		cache := map[string]string{}
		for i := range rows {
			id := rows[i].UserID
			if id == "" {
				continue
			}
			name, ok := cache[id]
			if !ok {
				if u, err := a.db.GetUser(ctx, id); err == nil && u != nil {
					name = u.Name
				}
				cache[id] = name
			}
			rows[i].UserName = name
		}
	}
	return &core.AIUsageReport{
		From: q.From, To: q.To, GroupBy: q.GroupBy, Rows: rows, Totals: totals,
		Currency: a.usageCurrency(ctx),
	}, nil
}

// MyAIUsage reports the actor's own usage.
func (a *App) MyAIUsage(ctx context.Context, actor *core.Actor, q core.AIUsageQuery) (*core.AIUsageReport, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	q.UserID = actor.UserID()
	return a.usageReport(ctx, q, false)
}

// AIUsage reports usage across the instance. Admin only.
func (a *App) AIUsage(ctx context.Context, actor *core.Actor, q core.AIUsageQuery) (*core.AIUsageReport, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	return a.usageReport(ctx, q, true)
}

// AIUsageCalls lists raw calls. An admin may list any calls. A user may list
// only their own, by setting q.UserID to themselves.
func (a *App) AIUsageCalls(ctx context.Context, actor *core.Actor, q core.AIUsageQuery, sort string) ([]*core.AIUsageRecord, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if !actor.IsAdmin() && q.UserID != actor.UserID() {
		return nil, fmt.Errorf("usage of other users: %w", core.ErrForbidden)
	}
	switch sort {
	case "":
		sort = core.UsageSortRecent
	case core.UsageSortRecent, core.UsageSortSlowest, core.UsageSortFailed:
	default:
		return nil, core.Invalid("sort", "sort must be recent, slowest or failed")
	}
	if q.Limit <= 0 {
		q.Limit = defaultCallLimit
	}
	q.Limit = min(q.Limit, maxCallLimit)
	if !q.From.IsZero() && !q.To.IsZero() && !q.From.Before(q.To) {
		return nil, core.Invalid("to", "to must be after from")
	}
	calls, err := a.db.AIUsageCalls(ctx, q, sort)
	if err != nil {
		return nil, fmt.Errorf("ai usage calls: %w", err)
	}
	if calls == nil {
		calls = []*core.AIUsageRecord{}
	}
	return calls, nil
}

// AIUsageSummary returns the actor's usage today and this month, both UTC,
// and their budget status.
func (a *App) AIUsageSummary(ctx context.Context, actor *core.Actor) (*core.AIUsageSummary, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	now := a.now()
	period := func(from, to time.Time) (core.AIUsagePeriod, error) {
		_, t, err := a.db.AIUsageReport(ctx, core.AIUsageQuery{From: from, To: to, UserID: actor.UserID()})
		if err != nil {
			return core.AIUsagePeriod{}, fmt.Errorf("ai usage summary: %w", err)
		}
		return core.AIUsagePeriod{
			Calls: t.Calls, InputTokens: t.InputTokens, OutputTokens: t.OutputTokens,
			Tokens: t.InputTokens + t.OutputTokens, Cost: core.MicrosToCost(t.CostMicros),
		}, nil
	}
	out := &core.AIUsageSummary{Currency: a.usageCurrency(ctx)}
	var err error
	if out.Today, err = period(dayStart(now), dayStart(now).AddDate(0, 0, 1)); err != nil {
		return nil, err
	}
	if out.Month, err = period(monthStart(now), monthStart(now).AddDate(0, 1, 0)); err != nil {
		return nil, err
	}
	settings, err := a.settings(ctx)
	if err != nil {
		return nil, err
	}
	b, err := a.budgetFor(ctx, settings, core.BudgetUser, actor.UserID())
	if err != nil {
		return nil, err
	}
	out.Budget = b.status(now)
	return out, nil
}

// budgetSubject validates a budget kind and id. A user subject must exist.
func (a *App) budgetSubject(ctx context.Context, kind, id string) (string, string, error) {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if kind != core.BudgetUser && kind != core.BudgetPlugin {
		return "", "", core.Invalid("subjectKind", "kind must be user or plugin")
	}
	if id == "" || len(id) > maxPluginIDBytes {
		return "", "", core.Invalid("subjectId", "id must be 1 to 64 bytes")
	}
	if kind == core.BudgetUser {
		if _, err := a.db.GetUser(ctx, id); err != nil {
			if errors.Is(err, core.ErrNotFound) {
				return "", "", fmt.Errorf("user %s: %w", id, core.ErrNotFound)
			}
			return "", "", fmt.Errorf("load user: %w", err)
		}
	}
	return kind, id, nil
}

// withBudgetStatus attaches the month-to-date status to a budget.
func (a *App) withBudgetStatus(ctx context.Context, settings *core.Settings, b *core.AIBudget) error {
	st, err := a.budgetFor(ctx, settings, b.SubjectKind, b.SubjectID)
	if err != nil {
		return err
	}
	status := st.status(a.now())
	b.Status = &status
	return nil
}

// ListAIBudgets returns every budget override with its status. Admin only.
func (a *App) ListAIBudgets(ctx context.Context, actor *core.Actor) ([]*core.AIBudget, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	list, err := a.db.ListAIBudgets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list ai budgets: %w", err)
	}
	settings, err := a.settings(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range list {
		if err := a.withBudgetStatus(ctx, settings, b); err != nil {
			return nil, err
		}
	}
	if list == nil {
		list = []*core.AIBudget{}
	}
	return list, nil
}

// validLimit checks one optional budget limit.
func validLimit(field string, v *float64) error {
	if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0) {
		return core.Invalid(field, "limit must be 0 or more")
	}
	return nil
}

// SaveAIBudget creates or replaces the limits of one subject. An active lift
// stays. Admin only.
func (a *App) SaveAIBudget(ctx context.Context, actor *core.Actor, in core.AIBudget) (*core.AIBudget, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	kind, id, err := a.budgetSubject(ctx, in.SubjectKind, in.SubjectID)
	if err != nil {
		return nil, err
	}
	if in.MonthlyTokens != nil && *in.MonthlyTokens < 0 {
		return nil, core.Invalid("monthlyTokens", "limit must be 0 or more")
	}
	if err := validLimit("monthlyCost", in.MonthlyCost); err != nil {
		return nil, err
	}
	b := &core.AIBudget{SubjectKind: kind, SubjectID: id, MonthlyTokens: in.MonthlyTokens, MonthlyCost: in.MonthlyCost}
	if existing, err := a.db.GetAIBudget(ctx, kind, id); err == nil {
		b.LiftedUntil = existing.LiftedUntil
	} else if !errors.Is(err, core.ErrNotFound) {
		return nil, fmt.Errorf("load ai budget: %w", err)
	}
	if err := a.db.SaveAIBudget(ctx, b); err != nil {
		return nil, fmt.Errorf("save ai budget: %w", err)
	}
	settings, err := a.settings(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.withBudgetStatus(ctx, settings, b); err != nil {
		return nil, err
	}
	a.log.Info("ai budget saved", "kind", kind, "id", id, "by", actor.UserID())
	return b, nil
}

// DeleteAIBudget removes the override of one subject. Admin only.
func (a *App) DeleteAIBudget(ctx context.Context, actor *core.Actor, kind, id string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	kind = strings.TrimSpace(kind)
	if kind != core.BudgetUser && kind != core.BudgetPlugin {
		return core.Invalid("subjectKind", "kind must be user or plugin")
	}
	if err := a.db.DeleteAIBudget(ctx, kind, strings.TrimSpace(id)); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return fmt.Errorf("ai budget %s/%s: %w", kind, id, core.ErrNotFound)
		}
		return fmt.Errorf("delete ai budget: %w", err)
	}
	a.log.Info("ai budget deleted", "kind", kind, "id", id, "by", actor.UserID())
	return nil
}

// LiftAIBudget lets a subject call past its budget until the start of the
// next UTC month. The limits stay. Admin only.
func (a *App) LiftAIBudget(ctx context.Context, actor *core.Actor, kind, id string) (*core.AIBudget, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	kind, id, err := a.budgetSubject(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	b, err := a.db.GetAIBudget(ctx, kind, id)
	if errors.Is(err, core.ErrNotFound) {
		b, err = &core.AIBudget{SubjectKind: kind, SubjectID: id}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load ai budget: %w", err)
	}
	until := monthStart(a.now()).AddDate(0, 1, 0)
	b.LiftedUntil = &until
	b.Status = nil
	if err := a.db.SaveAIBudget(ctx, b); err != nil {
		return nil, fmt.Errorf("save ai budget: %w", err)
	}
	settings, err := a.settings(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.withBudgetStatus(ctx, settings, b); err != nil {
		return nil, err
	}
	a.log.Info("ai budget lifted", "kind", kind, "id", id, "until", until, "by", actor.UserID())
	return b, nil
}
