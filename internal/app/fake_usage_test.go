package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// The fake DB keeps usage rows in a slice and sums them by brute force. It
// never rolls rows up, so RollupAIUsage folds nothing.

func (d *fakeDB) RecordAIUsage(ctx context.Context, rec *core.AIUsageRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.usageErr != nil {
		return d.usageErr
	}
	cp := *rec
	if cp.ID == "" {
		cp.ID = core.NewID()
	}
	if cp.Status == "" {
		cp.Status = core.UsageOK
	}
	d.usage = append(d.usage, cp)
	return nil
}

// usageRows returns a copy of every recorded usage row.
func (d *fakeDB) usageRows() []core.AIUsageRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]core.AIUsageRecord(nil), d.usage...)
}

// fakeUsageMatch applies the filters and the time range of a query.
func fakeUsageMatch(r core.AIUsageRecord, q core.AIUsageQuery) bool {
	eq := func(want, got string) bool { return want == "" || want == got }
	if !eq(q.UserID, r.UserID) || !eq(q.ProviderID, r.ProviderID) || !eq(q.Model, r.Model) ||
		!eq(q.Feature, r.Feature) || !eq(q.PluginID, r.PluginID) {
		return false
	}
	if !q.From.IsZero() && r.CreatedAt.Before(q.From) {
		return false
	}
	if !q.To.IsZero() && !r.CreatedAt.Before(q.To) {
		return false
	}
	return true
}

func (d *fakeDB) AIUsageReport(ctx context.Context, q core.AIUsageQuery) ([]core.AIUsageRow, core.AIUsageRow, error) {
	groups, err := core.NormalizeUsageGroupBy(q.GroupBy)
	if err != nil {
		return nil, core.AIUsageRow{}, err
	}
	var totals core.AIUsageRow
	byKey := map[string]*core.AIUsageRow{}
	add := func(dst *core.AIUsageRow, r core.AIUsageRecord) {
		dst.Calls++
		if r.Status != core.UsageOK {
			dst.Errors++
		}
		dst.InputTokens += r.InputTokens
		dst.OutputTokens += r.OutputTokens
		dst.ImagesIn += r.ImagesIn
		dst.ImagesOut += r.ImagesOut
		dst.DurationMs += r.DurationMs
		dst.CostMicros += r.CostMicros
		if r.Estimated {
			dst.EstimatedCalls++
		}
	}
	for _, r := range d.usageRows() {
		if !fakeUsageMatch(r, q) {
			continue
		}
		add(&totals, r)
		if len(groups) == 0 {
			continue
		}
		var row core.AIUsageRow
		var parts []string
		for _, g := range groups {
			var v string
			switch g {
			case core.GroupDay:
				v, row.Day = r.CreatedAt.UTC().Format("2006-01-02"), r.CreatedAt.UTC().Format("2006-01-02")
			case core.GroupMonth:
				v, row.Month = r.CreatedAt.UTC().Format("2006-01"), r.CreatedAt.UTC().Format("2006-01")
			case core.GroupUser:
				v, row.UserID = r.UserID, r.UserID
			case core.GroupProvider:
				v, row.ProviderID = r.ProviderID, r.ProviderID
			case core.GroupModel:
				v, row.Model = r.Model, r.Model
			case core.GroupFeature:
				v, row.Feature = r.Feature, r.Feature
			case core.GroupPlugin:
				v, row.PluginID = r.PluginID, r.PluginID
			}
			parts = append(parts, v)
		}
		key := strings.Join(parts, "\x00")
		if byKey[key] == nil {
			byKey[key] = &row
		}
		add(byKey[key], r)
	}
	totals.Finish()
	rows := []core.AIUsageRow{}
	if len(groups) == 0 {
		if totals.Calls > 0 {
			rows = append(rows, totals)
		}
		return rows, totals, nil
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		byKey[k].Finish()
		rows = append(rows, *byKey[k])
	}
	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, totals, nil
}

func (d *fakeDB) AIUsageCalls(ctx context.Context, q core.AIUsageQuery, order string) ([]*core.AIUsageRecord, error) {
	var out []*core.AIUsageRecord
	for _, r := range d.usageRows() {
		if !fakeUsageMatch(r, q) {
			continue
		}
		if order == core.UsageSortFailed && r.Status == core.UsageOK {
			continue
		}
		cp := r
		cp.Cost = core.MicrosToCost(cp.CostMicros)
		out = append(out, &cp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if order == core.UsageSortSlowest && out[i].DurationMs != out[j].DurationMs {
			return out[i].DurationMs > out[j].DurationMs
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (d *fakeDB) RollupAIUsage(ctx context.Context, before time.Time) (int, int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rollupBefore = append(d.rollupBefore, before)
	return 0, 0, nil
}

// cpBudget deep copies a budget.
func cpBudget(b *core.AIBudget) *core.AIBudget {
	cp := *b
	if b.MonthlyTokens != nil {
		v := *b.MonthlyTokens
		cp.MonthlyTokens = &v
	}
	if b.MonthlyCost != nil {
		v := *b.MonthlyCost
		cp.MonthlyCost = &v
	}
	if b.LiftedUntil != nil {
		v := *b.LiftedUntil
		cp.LiftedUntil = &v
	}
	cp.Status = nil
	return &cp
}

func (d *fakeDB) GetAIBudget(ctx context.Context, kind, id string) (*core.AIBudget, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if b, ok := d.budgets[kind+"|"+id]; ok {
		return cpBudget(b), nil
	}
	return nil, core.ErrNotFound
}

func (d *fakeDB) ListAIBudgets(ctx context.Context) ([]*core.AIBudget, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []*core.AIBudget{}
	for _, b := range d.budgets {
		out = append(out, cpBudget(b))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SubjectKind+"|"+out[i].SubjectID < out[j].SubjectKind+"|"+out[j].SubjectID
	})
	return out, nil
}

func (d *fakeDB) SaveAIBudget(ctx context.Context, b *core.AIBudget) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.budgets[b.SubjectKind+"|"+b.SubjectID] = cpBudget(b)
	return nil
}

func (d *fakeDB) DeleteAIBudget(ctx context.Context, kind, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.budgets[kind+"|"+id]; !ok {
		return core.ErrNotFound
	}
	delete(d.budgets, kind+"|"+id)
	return nil
}
