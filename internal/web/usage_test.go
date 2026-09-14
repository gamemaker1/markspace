package web

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// usageFake serves sample usage data and records budget writes.
type usageFake struct {
	*fakeBackend
	empty    bool
	settings core.Settings
	budgets  []*core.AIBudget
	saved    []core.AIBudget
	deleted  []string
	lifted   []string
	updated  []core.Settings
	queries  []core.AIUsageQuery
}

func newUsageFake() *usageFake {
	tokens := int64(50000)
	lifted := time.Now().UTC().AddDate(0, 0, 10)
	return &usageFake{
		fakeBackend: newFake(),
		settings:    core.Settings{InstanceName: "Test Space", AIEnabled: true, AIMonthlyTokenBudget: 100000, AIMonthlyCostBudget: 5},
		budgets: []*core.AIBudget{
			{SubjectKind: core.BudgetUser, SubjectID: "u2", MonthlyTokens: &tokens,
				Status: &core.AIBudgetStatus{TokenLimit: 50000, CostLimit: 5, TokensUsed: 42000, UsedPct: 84, Warn: true}},
			{SubjectKind: core.BudgetPlugin, SubjectID: "com.example.digest", LiftedUntil: &lifted,
				Status: &core.AIBudgetStatus{TokensUsed: 900, LiftedUntil: &lifted}},
		},
	}
}

// sampleUsageRecords spreads calls over the 12 days up to now, with one
// day that has no calls.
func sampleUsageRecords(now time.Time) []core.AIUsageRecord {
	day := usageDay(now)
	features := []string{"ask", "chat", "rewrite", "summarize"}
	models := []string{"gpt-small", "gpt-large"}
	users := []string{"u1", "u2"}
	plugins := []string{"", "", "com.example.digest"}
	var out []core.AIUsageRecord
	for d := 0; d < 12; d++ {
		if d == 4 {
			continue
		}
		for i := 0; i < 3; i++ {
			rec := core.AIUsageRecord{
				ID: fmt.Sprintf("r%d-%d", d, i), UserID: users[(d+i)%2], ProviderID: "p1",
				Model: models[i%2], Feature: features[(d+i)%4], PluginID: plugins[(d*i)%3],
				InputTokens: int64(800 + d*120 + i*40), OutputTokens: int64(200 + i*35),
				DurationMs: int64(900 + d*50 + i*300), Status: core.UsageOK,
				CostMicros: int64(2400 + d*10), CreatedAt: day.AddDate(0, 0, -d).Add(time.Duration(i) * time.Minute),
			}
			if i == 2 && d%5 == 1 {
				rec.Status, rec.Error = core.UsageError, "provider timeout"
			}
			if i == 0 {
				rec.NoteID = "n2"
			}
			rec.Cost = core.MicrosToCost(rec.CostMicros)
			out = append(out, rec)
		}
	}
	return out
}

// sampleUsageReport aggregates the sample records like the store does.
func sampleUsageReport(q core.AIUsageQuery) *core.AIUsageReport {
	names := map[string]string{"u1": "Ada Admin", "u2": "Mel Member"}
	rows := map[string]*core.AIUsageRow{}
	var keys []string
	var total core.AIUsageRow
	for _, rec := range sampleUsageRecords(time.Now()) {
		switch {
		case !q.From.IsZero() && rec.CreatedAt.Before(q.From),
			!q.To.IsZero() && !rec.CreatedAt.Before(q.To),
			q.UserID != "" && rec.UserID != q.UserID,
			q.ProviderID != "" && rec.ProviderID != q.ProviderID,
			q.Model != "" && rec.Model != q.Model,
			q.PluginID != "" && rec.PluginID != q.PluginID:
			continue
		}
		var row core.AIUsageRow
		for _, g := range q.GroupBy {
			switch g {
			case core.GroupDay:
				row.Day = rec.CreatedAt.Format(time.DateOnly)
			case core.GroupUser:
				row.UserID, row.UserName = rec.UserID, names[rec.UserID]
			case core.GroupProvider:
				row.ProviderID = rec.ProviderID
			case core.GroupModel:
				row.Model = rec.Model
			case core.GroupFeature:
				row.Feature = rec.Feature
			case core.GroupPlugin:
				row.PluginID = rec.PluginID
			}
		}
		key := fmt.Sprintf("%+v", row)
		if rows[key] == nil {
			r := row
			rows[key] = &r
			keys = append(keys, key)
		}
		for _, r := range []*core.AIUsageRow{rows[key], &total} {
			r.Calls++
			if rec.Status != core.UsageOK {
				r.Errors++
			}
			r.InputTokens += rec.InputTokens
			r.OutputTokens += rec.OutputTokens
			r.DurationMs += rec.DurationMs
			r.CostMicros += rec.CostMicros
		}
	}
	out := make([]core.AIUsageRow, 0, len(keys))
	for _, k := range keys {
		rows[k].Finish()
		out = append(out, *rows[k])
	}
	total.Finish()
	return &core.AIUsageReport{From: q.From, To: q.To, GroupBy: q.GroupBy, Rows: out, Totals: total, Currency: "USD"}
}

func sampleUsageCalls(sortBy string, limit int) []*core.AIUsageRecord {
	recs := sampleUsageRecords(time.Now())
	var out []*core.AIUsageRecord
	for i := range recs {
		if sortBy == core.UsageSortFailed && recs[i].Status == core.UsageOK {
			continue
		}
		out = append(out, &recs[i])
	}
	sort.SliceStable(out, func(a, b int) bool {
		if sortBy == core.UsageSortSlowest {
			return out[a].DurationMs > out[b].DurationMs
		}
		return out[a].CreatedAt.After(out[b].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func sampleUsageSummary() *core.AIUsageSummary {
	return &core.AIUsageSummary{
		Today:    core.AIUsagePeriod{Calls: 3, InputTokens: 1200, OutputTokens: 300, Tokens: 1500, Cost: 0.004},
		Month:    core.AIUsagePeriod{Calls: 41, InputTokens: 52000, OutputTokens: 9100, Tokens: 61100, Cost: 0.19},
		Currency: "USD",
		Budget:   core.AIBudgetStatus{TokenLimit: 100000, TokensUsed: 61100, UsedPct: 61.1},
	}
}

func (f *usageFake) GetSettings(context.Context, *core.Actor) (*core.Settings, error) {
	s := f.settings
	return &s, nil
}

func (f *usageFake) UpdateSettings(_ context.Context, _ *core.Actor, in *core.Settings) (*core.Settings, error) {
	f.updated = append(f.updated, *in)
	return in, nil
}

func (f *usageFake) MyAIUsage(_ context.Context, actor *core.Actor, q core.AIUsageQuery) (*core.AIUsageReport, error) {
	q.UserID = actor.UserID()
	f.queries = append(f.queries, q)
	if f.empty {
		return &core.AIUsageReport{From: q.From, To: q.To, Rows: []core.AIUsageRow{}}, nil
	}
	return sampleUsageReport(q), nil
}

func (f *usageFake) AIUsage(_ context.Context, actor *core.Actor, q core.AIUsageQuery) (*core.AIUsageReport, error) {
	if !actor.IsAdmin() {
		return nil, core.ErrForbidden
	}
	f.queries = append(f.queries, q)
	return sampleUsageReport(q), nil
}

func (f *usageFake) AIUsageCalls(_ context.Context, _ *core.Actor, q core.AIUsageQuery, sortBy string) ([]*core.AIUsageRecord, error) {
	if f.empty {
		return nil, nil
	}
	return sampleUsageCalls(sortBy, q.Limit), nil
}

func (f *usageFake) AIUsageSummary(context.Context, *core.Actor) (*core.AIUsageSummary, error) {
	if f.empty {
		return &core.AIUsageSummary{}, nil
	}
	return sampleUsageSummary(), nil
}

func (f *usageFake) ListAIBudgets(context.Context, *core.Actor) ([]*core.AIBudget, error) {
	return f.budgets, nil
}

func (f *usageFake) SaveAIBudget(_ context.Context, _ *core.Actor, b core.AIBudget) (*core.AIBudget, error) {
	f.saved = append(f.saved, b)
	return &b, nil
}

func (f *usageFake) DeleteAIBudget(_ context.Context, _ *core.Actor, kind, id string) error {
	f.deleted = append(f.deleted, kind+"/"+id)
	return nil
}

func (f *usageFake) LiftAIBudget(_ context.Context, _ *core.Actor, kind, id string) (*core.AIBudget, error) {
	f.lifted = append(f.lifted, kind+"/"+id)
	return &core.AIBudget{SubjectKind: kind, SubjectID: id}, nil
}

func (f *usageFake) ListUsers(context.Context, *core.Actor, string, int, int) ([]*core.User, int, error) {
	return []*core.User{f.admin, f.member}, 2, nil
}

func (f *usageFake) LookupUsers(_ context.Context, _ *core.Actor, query string, _ int) ([]*core.User, error) {
	var out []*core.User
	for _, u := range []*core.User{f.admin, f.member} {
		q := strings.ToLower(query)
		if strings.Contains(strings.ToLower(u.Email), q) || strings.Contains(strings.ToLower(u.Name), q) {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *usageFake) ListAIProviders(context.Context, *core.Actor) ([]*core.AIProviderConfig, error) {
	return []*core.AIProviderConfig{{ID: "p1", Name: "Local", Kind: core.ProviderOllama, Enabled: true}}, nil
}

func (f *usageFake) GetNote(_ context.Context, _ *core.Actor, id string) (*core.Note, *core.Grant, error) {
	if id == f.note.ID {
		return f.note, &core.Grant{Access: core.AccessView}, nil
	}
	return nil, nil, core.ErrNotFound
}

// sampleUsageView fills the user page for the template execution test.
func sampleUsageView() *usageView {
	to := usageDay(time.Now()).AddDate(0, 0, 1)
	from := to.AddDate(0, 0, -30)
	daily := sampleUsageReport(core.AIUsageQuery{From: from, To: to, GroupBy: []string{core.GroupDay, core.GroupFeature}})
	calls := sampleUsageCalls(core.UsageSortRecent, 3)
	return &usageView{
		From: from, To: to, Currency: "USD", Summary: sampleUsageSummary(),
		Chart:  buildUsageChart(daily.Rows, from, to, featureSeries, chartOptions{PlotWidth: 600, Fixed: usageFeatureSlots, Label: "Chart"}),
		Models: []usageRow{{AIUsageRow: sampleUsageReport(core.AIUsageQuery{From: from, To: to, GroupBy: []string{core.GroupModel}}).Rows[0], Label: "gpt-small"}},
		Recent: []usageCall{{AIUsageRecord: calls[0], NoteTitle: "Install"}, {AIUsageRecord: calls[1]}},
	}
}

// sampleAdminUsageView fills the admin page for the template execution test.
func sampleAdminUsageView() *adminUsageView {
	f := newUsageFake()
	filter, _ := parseUsageFilter(url.Values{"range": {"custom"}, "from": {"2026-09-01"}, "to": {"2026-09-13"}}, time.Now())
	daily := sampleUsageReport(core.AIUsageQuery{From: filter.Start, To: filter.End, GroupBy: []string{core.GroupDay, core.GroupUser}})
	calls := sampleUsageCalls(core.UsageSortSlowest, 2)
	v := &adminUsageView{
		Filter: filter, Ranges: usageRanges, Currency: "USD", Totals: daily.Totals,
		Chart:           buildUsageChart(daily.Rows, filter.Start, filter.End, func(r core.AIUsageRow) (string, string) { return r.UserID, r.UserName }, chartOptions{PlotWidth: 600}),
		People:          []usageRow{{AIUsageRow: daily.Totals, Label: "Mel Member", Sub: "mel@example.com"}},
		Slowest:         []usageCall{{AIUsageRecord: calls[0], Person: "Mel"}},
		Users:           []*core.User{f.admin, f.member},
		ProviderOptions: []usageOption{{"p1", "Local"}},
		ModelOptions:    []string{"gpt-small"},
		DefaultTokens:   "100000", DefaultCost: "5", DefaultsSaved: true, Removed: true, AddOpen: true,
		ErrorField: "person", ErrorMsg: "No account matches x.",
	}
	for i, b := range f.budgets {
		vb := budgetView(i, b, map[string]*core.User{"u2": f.member}, &f.settings, "USD", time.Now())
		vb.Editing, vb.Result = i == 0, "Budget saved."
		v.Budgets = append(v.Budgets, vb)
	}
	return v
}

func TestNiceScale(t *testing.T) {
	tests := []struct{ peak, top, step int64 }{
		{0, 0, 0}, {1, 1, 1}, {4, 4, 1}, {7, 8, 2}, {100, 100, 25},
		{980, 1000, 250}, {1000, 1000, 250}, {12345, 15000, 5000}, {61100, 80000, 20000},
	}
	for _, tt := range tests {
		top, step := niceScale(tt.peak)
		if top != tt.top || step != tt.step {
			t.Errorf("niceScale(%d) = %d, %d, want %d, %d", tt.peak, top, step, tt.top, tt.step)
		}
	}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 5000; i++ {
		peak := rng.Int63n(1e12) + 1
		if i%3 == 0 {
			peak = rng.Int63n(200) + 1
		}
		top, step := niceScale(peak)
		if top < peak || top-peak >= step || top%step != 0 || top/step > chartTicks || !niceStep(step) {
			t.Fatalf("niceScale(%d) = %d, %d is not a nice scale", peak, top, step)
		}
	}
}

// niceStep reports whether a step is 1, 2, 2.5 or 5 times a power of ten.
func niceStep(s int64) bool {
	for s >= 10 && s%10 == 0 {
		s /= 10
	}
	return s == 1 || s == 2 || s == 25 || s == 5
}

func TestUsageChartStacks(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 5)
	rows := []core.AIUsageRow{
		{Day: "2026-09-01", Feature: "chat", InputTokens: 300},
		{Day: "2026-09-01", Feature: "ask", InputTokens: 1000, OutputTokens: 200},
		{Day: "2026-09-03", Feature: "ask", InputTokens: 50, OutputTokens: 25},
		{Day: "2026-09-03", Feature: "image", InputTokens: 4000, OutputTokens: 10},
		{Day: "2026-09-04", Feature: "summarize", InputTokens: 1},
		{Day: "2026-08-31", Feature: "ask", InputTokens: 9e6},
		{Day: "2026-09", Feature: "ask", InputTokens: 5},
	}
	c := buildUsageChart(rows, from, to, featureSeries, chartOptions{PlotWidth: 600, Fixed: usageFeatureSlots})
	if len(c.Days) != 5 {
		t.Fatalf("days = %d, want 5", len(c.Days))
	}
	if c.Max != 6000 {
		t.Errorf("max = %d, want 6000", c.Max)
	}
	classes := map[string]string{}
	for _, s := range c.Series {
		classes[s.Key] = s.Class
	}
	// Known features keep their slot, and a new feature takes the first free one.
	if classes["ask"] != "uc-s1" || classes["chat"] != "uc-s2" || classes["image"] != "uc-s4" || classes["summarize"] != "uc-s3" {
		t.Errorf("series classes = %v", classes)
	}
	bars := 0
	for i, d := range c.Days {
		var sum float64
		var values int64
		segs := c.segments(i)
		for _, s := range segs {
			sum += s.H
			if s.H <= 0 || s.Y < chartPadTop-1e-9 {
				t.Errorf("day %d: bad segment %+v", i, s)
			}
		}
		for _, v := range d.Values {
			values += v
		}
		if values != d.Total {
			t.Errorf("day %d: values sum to %d, total %d", i, values, d.Total)
		}
		want := chartPlotH * float64(d.Total) / float64(c.Max)
		if math.Abs(sum-want) > 1e-9 {
			t.Errorf("day %d: segment heights sum to %v, want %v", i, sum, want)
		}
		if d.Total == 0 && len(segs) != 0 {
			t.Errorf("day %d: zero usage has %d segments", i, len(segs))
		}
		if d.Total > 0 {
			bars++
		}
	}
	svg := string(c.SVG)
	if got := strings.Count(svg, `<g class="uc-bar">`); got != bars || bars != 3 {
		t.Errorf("bars drawn = %d, days with usage = %d, want 3", got, bars)
	}
	for _, empty := range []string{"Wed 2 Sep 2026:", "Sat 5 Sep 2026:"} {
		if strings.Contains(svg, empty) {
			t.Errorf("a day without usage has a bar: %q", empty)
		}
	}
}

func TestUsageChartFoldsOther(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var rows []core.AIUsageRow
	for i := 1; i <= 9; i++ {
		rows = append(rows, core.AIUsageRow{Day: "2026-09-01", Feature: fmt.Sprintf("f%d", i), InputTokens: int64(i * 100)})
	}
	c := buildUsageChart(rows, from, from.AddDate(0, 0, 1), featureSeries, chartOptions{PlotWidth: 300})
	if len(c.Series) != 8 {
		t.Fatalf("series = %d, want 7 and Other", len(c.Series))
	}
	last := c.Series[7]
	if last.Class != "uc-other" || last.Total != 300 {
		t.Errorf("other = %+v, want the two smallest folded, 300 tokens", last)
	}
	if c.Series[0].Label != "f9" {
		t.Errorf("first slot = %q, want the largest series", c.Series[0].Label)
	}
}

func TestUsageChartTicksAndXML(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for n := 0; n < 200; n++ {
		days := 1 + rng.Intn(60)
		var rows []core.AIUsageRow
		for d := 0; d < days; d++ {
			if rng.Intn(3) == 0 {
				continue
			}
			for _, feature := range []string{"ask", "<b>&\"'", "x\x01y", ""} {
				rows = append(rows, core.AIUsageRow{
					Day: from.AddDate(0, 0, d).Format(time.DateOnly), Feature: feature,
					InputTokens: rng.Int63n(1 << uint(rng.Intn(40))), OutputTokens: rng.Int63n(1000),
				})
			}
		}
		c := buildUsageChart(rows, from, from.AddDate(0, 0, days), featureSeries, chartOptions{PlotWidth: 900, Fixed: usageFeatureSlots, Label: `Tokens <per> "day"`})

		var peak int64
		for _, d := range c.Days {
			peak = max(peak, d.Total)
		}
		if c.Max < peak {
			t.Fatalf("max %d below the peak %d", c.Max, peak)
		}
		if c.Ticks[0] != 0 || c.Ticks[len(c.Ticks)-1] != c.Max {
			t.Fatalf("ticks %v do not run from 0 to max %d", c.Ticks, c.Max)
		}

		plot := xml.NewDecoder(strings.NewReader(string(c.SVG)))
		plot.Strict = true
		for {
			if _, err := plot.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("chart %d plot is not valid XML: %v", n, err)
			}
		}
		dec := xml.NewDecoder(strings.NewReader(string(c.Axis)))
		dec.Strict = true
		var labels []string
		inTicks := false
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("chart %d is not valid XML: %v", n, err)
			}
			switch el := tok.(type) {
			case xml.StartElement:
				if el.Name.Local == "g" {
					for _, a := range el.Attr {
						inTicks = a.Name.Local == "class" && a.Value == "uc-ticks"
					}
				}
				if el.Name.Local == "text" && inTicks {
					for _, a := range el.Attr {
						if a.Name.Local == "y" {
							var y float64
							fmt.Sscan(a.Value, &y)
							if y < chartPadTop-1e-9 || y > chartPadTop+chartPlotH+1e-9 {
								t.Fatalf("tick label at y=%v is outside the plot", y)
							}
						}
					}
				}
			case xml.EndElement:
				if el.Name.Local == "g" {
					inTicks = false
				}
			case xml.CharData:
				if inTicks && strings.TrimSpace(string(el)) != "" {
					labels = append(labels, string(el))
				}
			}
		}
		var want []string
		for _, tick := range c.Ticks {
			if tick > c.Max {
				t.Fatalf("tick %d above max %d", tick, c.Max)
			}
			want = append(want, compactTokens(tick))
		}
		if strings.Join(labels, "|") != strings.Join(want, "|") {
			t.Fatalf("tick labels %v, want %v", labels, want)
		}
	}
}

func TestUsageFormatting(t *testing.T) {
	tests := []struct{ got, want string }{
		{compactTokens(0), "0"},
		{compactTokens(999), "999"},
		{compactTokens(1000), "1k"},
		{compactTokens(12345), "12.3k"},
		{compactTokens(99949), "99.9k"},
		{compactTokens(99950), "100k"},
		{compactTokens(999499), "999k"},
		{compactTokens(999500), "1M"},
		{compactTokens(1234567), "1.2M"},
		{compactTokens(2_500_000_000), "2.5B"},
		{usageNum(1234567), "1,234,567"},
		{usageNum(-1200), "-1,200"},
		{usageNum(12), "12"},
		{string(usageTokens(12345)), `<span class="usage-num">12.3k</span>`},
		{usageMoney(0.02799, "USD"), "0.03 USD"},
		{usageMoney(12, ""), "—"},
		{usageMoney(0.001, "EUR"), "<0.01 EUR"},
		{usageMoney(0, "EUR"), "0.00 EUR"},
		{usageMoney(3.5, "mixed"), "3.50 (mixed)"},
		{usageDuration(840), "840 ms"},
		{usageDuration(2280), "2.3 s"},
		{usageDuration(95000), "1.6 min"},
		{usagePct(61.14), "61.1%"},
		{usagePct(80), "80%"},
		{usageCount(1, "call", "calls"), "1 call"},
		{usageCount(1200, "call", "calls"), "1,200 calls"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
	for pct, color := range map[float64]string{10: "", 79.9: "", 80: `data-color="attention"`, 99.9: `data-color="attention"`, 100: `data-color="danger"`, 250: `data-color="danger"`} {
		m := string(usageProgress(pct, "Budget"))
		if !strings.Contains(m, `class="progress-bar usage-progress"`) || !strings.Contains(m, `role="progressbar"`) {
			t.Errorf("usageProgress(%v) = %s, want a Primer ProgressBar", pct, m)
		}
		if color == "" && strings.Contains(m, "data-color") || color != "" && !strings.Contains(m, color) {
			t.Errorf("usageProgress(%v) = %s, want colour %q", pct, m, color)
		}
	}
	m := string(usageProgress(250, `Budget used by "Ada"`))
	for _, want := range []string{`aria-valuenow="100"`, `aria-valuetext="250% used"`, `style="width: 100%"`, `aria-label="Budget used by &#34;Ada&#34;"`} {
		if !strings.Contains(m, want) {
			t.Errorf("progress above 100 percent lacks %s: %s", want, m)
		}
	}
}

func TestParseUsageFilter(t *testing.T) {
	now := time.Date(2026, 9, 13, 15, 0, 0, 0, time.UTC)
	day := func(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d }
	tests := []struct {
		query              string
		rng, start, end    string
		from, to, errField string
	}{
		{"", "30d", "2026-08-15", "2026-09-14", "2026-08-15", "2026-09-13", ""},
		{"range=7d", "7d", "2026-09-07", "2026-09-14", "2026-09-07", "2026-09-13", ""},
		{"range=month", "month", "2026-09-01", "2026-09-14", "2026-09-01", "2026-09-13", ""},
		{"range=lastmonth", "lastmonth", "2026-08-01", "2026-09-01", "2026-08-01", "2026-08-31", ""},
		{"from=2026-09-02&to=2026-09-05", "custom", "2026-09-02", "2026-09-06", "2026-09-02", "2026-09-05", ""},
		{"range=custom&from=2026-09-10&to=2026-09-01", "custom", "2026-08-15", "2026-09-14", "2026-09-10", "2026-09-01", "to"},
		{"range=custom&from=nope", "custom", "2026-08-15", "2026-09-14", "nope", "", "from"},
		{"range=custom&from=2024-01-01&to=2026-09-01", "custom", "2026-08-15", "2026-09-14", "2024-01-01", "2026-09-01", "from"},
	}
	for _, tt := range tests {
		q, _ := url.ParseQuery(tt.query)
		f, err := parseUsageFilter(q, now)
		if f.Range != tt.rng || !f.Start.Equal(day(tt.start)) || !f.End.Equal(day(tt.end)) || f.From != tt.from || f.To != tt.to || fieldOf(err) != tt.errField {
			t.Errorf("%q: got range %s %s..%s form %s..%s err %v", tt.query, f.Range, f.Start.Format(time.DateOnly), f.End.Format(time.DateOnly), f.From, f.To, err)
		}
	}
	q, _ := url.ParseQuery("stack=plugin&user=u2&model=m&plugin=p&provider=p1")
	if f, _ := parseUsageFilter(q, now); f.Stack != "plugin" || f.User != "u2" || f.Model != "m" || f.Plugin != "p" || f.Provider != "p1" {
		t.Errorf("filters = %+v", f)
	}
}

func TestUsagePagesAccess(t *testing.T) {
	h := newTestServer(t, newUsageFake()).Handler()
	for _, path := range []string{"/settings/usage", "/admin/usage?range=7d"} {
		rec := do(t, h, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?next="+url.QueryEscape(path) {
			t.Errorf("anonymous %s: %d %q", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	if rec := do(t, h, http.MethodGet, "/admin/usage", memberSession, nil); rec.Code != http.StatusForbidden {
		t.Errorf("member GET /admin/usage: %d, want 403", rec.Code)
	}
	fake := newUsageFake()
	h = newTestServer(t, fake).Handler()
	rec := do(t, h, http.MethodPost, "/admin/usage", memberSession, url.Values{"action": {"budget-delete"}, "subjectKind": {"user"}, "subjectId": {"u2"}})
	if rec.Code != http.StatusForbidden || len(fake.deleted) != 0 {
		t.Errorf("member POST /admin/usage: %d, deleted %v", rec.Code, fake.deleted)
	}
}

func TestUsagePageRenders(t *testing.T) {
	fake := newUsageFake()
	h := newTestServer(t, fake).Handler()
	rec := do(t, h, http.MethodGet, "/settings/usage", memberSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`href="/settings/usage" aria-current="page"`,
		`<svg xmlns="http://www.w3.org/2000/svg" class="usage-chart-svg"`,
		`<g class="uc-bar">`,
		`<caption>Tokens per day for the last 30 days, stacked by feature</caption>`,
		`<span class="usage-legend-label">chat</span>`,
		`gpt-small`,
		`<a href="/n/n2">Install</a>`,
		`61.1%`,
		`0.19 USD`,
		`<span class="ui-label ui-label-danger">Error</span>`,
		`<h1 class="page-header-title" tabindex="-1">AI usage</h1>`,
		`role="progressbar" aria-label="Monthly budget used"`,
		`<table class="data-table usage-table" aria-labelledby="usage-models-title">`,
		`<th scope="row">`,
		`<relative-time datetime=`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("usage page is missing %q", want)
		}
	}
	if len(fake.queries) == 0 || fake.queries[0].UserID != "u2" {
		t.Errorf("queries = %+v", fake.queries)
	}

	fake.empty = true
	body = do(t, h, http.MethodGet, "/settings/usage", memberSession, nil).Body.String()
	if !strings.Contains(body, "No AI usage yet") || strings.Contains(body, "usage-chart-svg") {
		t.Errorf("empty usage page does not show the empty state")
	}
}

func TestAdminUsageRenders(t *testing.T) {
	fake := newUsageFake()
	h := newTestServer(t, fake).Handler()
	rec := do(t, h, http.MethodGet, "/admin/usage?range=7d&stack=plugin&provider=p1", adminSession, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`href="/admin/usage" aria-current="page"`,
		`<option value="7d" selected>Last 7 days</option>`,
		`<caption>Tokens per day, stacked by plugin</caption>`,
		`<span class="usage-legend-label">com.example.digest</span>`,
		`id="by-person-title"`, `id="by-provider-title"`, `id="by-model-title"`, `id="by-plugin-title"`,
		`id="slowest-title"`, `id="failed-title"`,
		`name="defaultTokens" min="0" step="1" inputmode="numeric" value="100000"`,
		`id="budget-user-u2"`, `id="budget-plugin-com.example.digest"`,
		`Near limit`, `50k tokens`, `5.00 USD (default)`,
		`popovertarget="budget-menu-0"`,
		`value="budget-delete" data-confirm="Remove the override for `,
		`data-dialog-open="budget-edit-0"`,
		`<div class="dialog-source" id="budget-add"`,
		`usage-progress" data-color="attention" role="progressbar"`,
		`<span class="ui-label ui-label-attention">Near limit</span>`,
		`<button class="btn btn-primary" type="submit">Save defaults</button>`,
		`<script type="module" src="/static/js/admin.js"></script>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("admin usage page is missing %q", want)
		}
	}
	for _, gone := range []string{"data-usage-autosave", "Changes save at once", "Confirm remove", `class="pill`} {
		if strings.Contains(body, gone) {
			t.Errorf("admin usage page still has %q", gone)
		}
	}
	if strings.Contains(body, "data-dialog-autoopen") {
		t.Errorf("a Dialog opens without a refused form")
	}
	var chart core.AIUsageQuery
	for _, q := range fake.queries {
		if strings.Join(q.GroupBy, ",") == "day,plugin" {
			chart = q
		}
	}
	today := usageDay(time.Now())
	if chart.ProviderID != "p1" || !chart.From.Equal(today.AddDate(0, 0, -6)) || !chart.To.Equal(today.AddDate(0, 0, 1)) {
		t.Errorf("chart query = %+v", chart)
	}

	body = do(t, h, http.MethodGet, "/admin/usage?range=custom&from=2026-09-10&to=2026-09-01", adminSession, nil).Body.String()
	if !strings.Contains(body, `<p class="field-error form-validation" id="uf-to-error">`) || !strings.Contains(body, `aria-invalid="true" aria-describedby="uf-to-error"`) || strings.Contains(body, `data-flash="error"`) {
		t.Errorf("a bad range does not show its error under the field")
	}

	autoopen := func(body, id string) bool {
		start := strings.Index(body, `<div class="dialog-source" id="`+id+`"`)
		if start < 0 {
			return false
		}
		end := strings.Index(body[start:], ">")
		return strings.Contains(body[start:start+end], "data-dialog-autoopen")
	}

	body = do(t, h, http.MethodGet, "/admin/usage?row=user%3Au2&done=saved", adminSession, nil).Body.String()
	if !strings.Contains(body, `<p class="sr-only" data-saved-message>Budget saved.</p>`) || autoopen(body, "budget-edit-0") {
		t.Errorf("a saved row is not announced, or its Dialog opens again")
	}

	body = do(t, h, http.MethodGet, "/admin/usage?row=user%3Au2&edit=1", adminSession, nil).Body.String()
	if !autoopen(body, "budget-edit-0") || autoopen(body, "budget-edit-1") || autoopen(body, "budget-add") {
		t.Errorf("the edit link does not open exactly its row's Dialog")
	}

	body = do(t, h, http.MethodGet, "/admin/usage?row=user%3Au2&monthlyCost=-0.5&subjectKind=user&error=A+cost+limit+must+be+0+or+more.&field=monthlyCost", adminSession, nil).Body.String()
	if !autoopen(body, "budget-edit-0") || autoopen(body, "budget-add") || !strings.Contains(body, `name="monthlyCost" min="0" step="0.01" inputmode="decimal" value="-0.5"`) ||
		!strings.Contains(body, `id="be-cost-0-error"`) || strings.Contains(body, `data-flash="error"`) {
		t.Errorf("a refused row save does not reopen its Dialog with the entered value and the error")
	}

	body = do(t, h, http.MethodGet, "/admin/usage?error=No+account+matches+x.&field=person&person=x&subjectKind=user", adminSession, nil).Body.String()
	if !autoopen(body, "budget-add") || !strings.Contains(body, "No account matches x.") || !strings.Contains(body, `name="person" value="x"`) || strings.Contains(body, `data-flash="error"`) {
		t.Errorf("a refused new override does not reopen its Dialog with the error inline")
	}
}

func TestAdminUsageBudgetPosts(t *testing.T) {
	type post struct {
		name     string
		form     url.Values
		location string
		check    func(f *usageFake) string
	}
	i64 := func(v int64) *int64 { return &v }
	f64 := func(v float64) *float64 { return &v }
	same := func(a, b core.AIBudget) bool {
		eqI := (a.MonthlyTokens == nil) == (b.MonthlyTokens == nil) && (a.MonthlyTokens == nil || *a.MonthlyTokens == *b.MonthlyTokens)
		eqF := (a.MonthlyCost == nil) == (b.MonthlyCost == nil) && (a.MonthlyCost == nil || *a.MonthlyCost == *b.MonthlyCost)
		return a.SubjectKind == b.SubjectKind && a.SubjectID == b.SubjectID && eqI && eqF
	}
	savedIs := func(want core.AIBudget) func(f *usageFake) string {
		return func(f *usageFake) string {
			if len(f.saved) != 1 || !same(f.saved[0], want) {
				return fmt.Sprintf("saved %+v", f.saved)
			}
			return ""
		}
	}
	nothing := func(f *usageFake) string {
		if len(f.saved)+len(f.deleted)+len(f.lifted)+len(f.updated) != 0 {
			return fmt.Sprintf("backend called: %+v %v %v %+v", f.saved, f.deleted, f.lifted, f.updated)
		}
		return ""
	}
	posts := []post{
		{"add person", url.Values{"action": {"budget-save"}, "subjectKind": {"user"}, "person": {"MEL@example.com"}, "monthlyTokens": {"50000"}, "monthlyCost": {"2.5"}},
			"/admin/usage?row=user%3Au2&done=added#budget-user-u2", savedIs(core.AIBudget{SubjectKind: "user", SubjectID: "u2", MonthlyTokens: i64(50000), MonthlyCost: f64(2.5)})},
		{"add person by name", url.Values{"action": {"budget-save"}, "subjectKind": {"user"}, "person": {"ada admin"}, "monthlyTokens": {"0"}},
			"/admin/usage?row=user%3Au1&done=added#budget-user-u1", savedIs(core.AIBudget{SubjectKind: "user", SubjectID: "u1", MonthlyTokens: i64(0)})},
		{"add plugin without limits", url.Values{"action": {"budget-save"}, "subjectKind": {"plugin"}, "person": {"ignored"}, "pluginId": {"com.example.digest"}, "monthlyTokens": {""}, "monthlyCost": {" "}},
			"/admin/usage?row=plugin%3Acom.example.digest&done=added#budget-plugin-com.example.digest", savedIs(core.AIBudget{SubjectKind: "plugin", SubjectID: "com.example.digest"})},
		{"edit row", url.Values{"action": {"budget-save"}, "subjectKind": {"user"}, "subjectId": {"u2"}, "monthlyCost": {"7.25"}},
			"/admin/usage?row=user%3Au2&done=saved#budget-user-u2", savedIs(core.AIBudget{SubjectKind: "user", SubjectID: "u2", MonthlyCost: f64(7.25)})},
		{"negative tokens", url.Values{"action": {"budget-save"}, "subjectKind": {"user"}, "person": {"mel@example.com"}, "monthlyTokens": {"-1"}},
			"/admin/usage?monthlyTokens=-1&person=mel%40example.com&subjectKind=user&error=A+token+limit+must+be+0+or+more.&field=monthlyTokens#budgets", nothing},
		{"negative cost on a row", url.Values{"action": {"budget-save"}, "subjectKind": {"user"}, "subjectId": {"u2"}, "monthlyCost": {"-0.5"}},
			"/admin/usage?row=user%3Au2&monthlyCost=-0.5&subjectKind=user&error=A+cost+limit+must+be+0+or+more.&field=monthlyCost#budget-user-u2", nothing},
		{"not a number", url.Values{"action": {"budget-save"}, "subjectKind": {"plugin"}, "pluginId": {"p"}, "monthlyCost": {"NaN"}},
			"/admin/usage?monthlyCost=NaN&pluginId=p&subjectKind=plugin&error=Enter+an+amount+such+as+12.50.&field=monthlyCost#budgets", nothing},
		{"unknown person", url.Values{"action": {"budget-save"}, "subjectKind": {"user"}, "person": {"nobody@example.com"}},
			"/admin/usage?person=nobody%40example.com&subjectKind=user&error=No+account+matches+nobody%40example.com.&field=person#budgets", nothing},
		{"missing plugin", url.Values{"action": {"budget-save"}, "subjectKind": {"plugin"}},
			"/admin/usage?subjectKind=plugin&error=Enter+the+id+of+the+plugin.&field=pluginId#budgets", nothing},
		{"lift", url.Values{"action": {"budget-lift"}, "subjectKind": {"user"}, "subjectId": {"u2"}},
			"/admin/usage?row=user%3Au2&done=lifted#budget-user-u2", func(f *usageFake) string {
				if strings.Join(f.lifted, ",") != "user/u2" {
					return fmt.Sprintf("lifted %v", f.lifted)
				}
				return ""
			}},
		{"remove", url.Values{"action": {"budget-delete"}, "subjectKind": {"plugin"}, "subjectId": {"com.example.digest"}},
			"/admin/usage?done=removed#budgets", func(f *usageFake) string {
				if strings.Join(f.deleted, ",") != "plugin/com.example.digest" {
					return fmt.Sprintf("deleted %v", f.deleted)
				}
				return ""
			}},
		{"defaults", url.Values{"action": {"defaults-save"}, "defaultTokens": {"200000"}, "defaultCost": {"12.5"}},
			"/admin/usage?done=defaults#budget-defaults", func(f *usageFake) string {
				if len(f.updated) != 1 || f.updated[0].AIMonthlyTokenBudget != 200000 || f.updated[0].AIMonthlyCostBudget != 12.5 || f.updated[0].InstanceName != "Test Space" {
					return fmt.Sprintf("updated %+v", f.updated)
				}
				return ""
			}},
		{"defaults cleared", url.Values{"action": {"defaults-save"}, "defaultTokens": {""}, "defaultCost": {"0"}},
			"/admin/usage?done=defaults#budget-defaults", func(f *usageFake) string {
				if len(f.updated) != 1 || f.updated[0].AIMonthlyTokenBudget != 0 || f.updated[0].AIMonthlyCostBudget != 0 {
					return fmt.Sprintf("updated %+v", f.updated)
				}
				return ""
			}},
		{"negative defaults", url.Values{"action": {"defaults-save"}, "defaultTokens": {"-3"}, "defaultCost": {"1"}},
			"/admin/usage?defaultCost=1&defaultTokens=-3&error=A+token+limit+must+be+0+or+more.&field=defaultTokens#budget-defaults", nothing},
	}
	for _, p := range posts {
		t.Run(p.name, func(t *testing.T) {
			fake := newUsageFake()
			h := newTestServer(t, fake).Handler()
			rec := do(t, h, http.MethodPost, "/admin/usage", adminSession, p.form)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != p.location {
				t.Errorf("status %d, Location %q, want %q", rec.Code, rec.Header().Get("Location"), p.location)
			}
			if msg := p.check(fake); msg != "" {
				t.Error(msg)
			}
		})
	}
}

// TestAdminUsageDefaultsExplicitSave checks the instance defaults form: it
// saves only on submit, answers every request with a redirect, shows an
// InlineMessage beside Save, and brings a refused value back under its field.
func TestAdminUsageDefaultsExplicitSave(t *testing.T) {
	fake := newUsageFake()
	h := newTestServer(t, fake).Handler()
	send := func(form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/admin/usage", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// The page script that sent JSON requests is gone, so JSON gets a redirect too.
		req.Header.Set("Accept", "application/json")
		req.AddCookie(&http.Cookie{Name: api.SessionCookie, Value: adminSession})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	body := do(t, h, http.MethodGet, "/admin/usage", adminSession, nil).Body.String()
	if strings.Contains(body, "Defaults saved") || strings.Contains(body, "data-saved-message") {
		t.Errorf("the page reports a save before any save")
	}

	rec := send(url.Values{"action": {"defaults-save"}, "defaultTokens": {"300"}, "defaultCost": {"1.5"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/usage?done=defaults#budget-defaults" {
		t.Fatalf("save: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(fake.updated) != 1 || fake.updated[0].AIMonthlyTokenBudget != 300 || fake.updated[0].AIMonthlyCostBudget != 1.5 {
		t.Fatalf("updated %+v", fake.updated)
	}
	body = do(t, h, http.MethodGet, "/admin/usage?done=defaults", adminSession, nil).Body.String()
	if !strings.Contains(body, `<p class="inline-message inline-message-success" data-saved-message>`) || !strings.Contains(body, "Defaults saved") || strings.Contains(body, `data-flash="ok"`) {
		t.Errorf("a saved default does not show an InlineMessage beside Save")
	}

	rec = send(url.Values{"action": {"defaults-save"}, "defaultTokens": {"1"}, "defaultCost": {"-2"}})
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || loc != "/admin/usage?defaultCost=-2&defaultTokens=1&error=A+cost+limit+must+be+0+or+more.&field=defaultCost#budget-defaults" {
		t.Fatalf("negative cost: %d %q", rec.Code, loc)
	}
	if len(fake.updated) != 1 {
		t.Fatalf("a negative default reached the backend: %+v", fake.updated)
	}
	body = do(t, h, http.MethodGet, strings.TrimSuffix(loc, "#budget-defaults"), adminSession, nil).Body.String()
	for _, want := range []string{
		`name="defaultCost" min="0" step="0.01" inputmode="decimal" value="-2" aria-invalid="true" aria-describedby="bd-cost-error"`,
		`name="defaultTokens" min="0" step="1" inputmode="numeric" value="1"`,
		`id="bd-cost-error"`, "A cost limit must be 0 or more.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("a refused default lacks %q", want)
		}
	}
	if strings.Contains(body, `data-flash="error"`) || strings.Contains(body, "Defaults saved") {
		t.Errorf("a refused default shows a page Banner or a saved message")
	}
}
