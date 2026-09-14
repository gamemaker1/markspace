package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// usageDB opens a fresh index database for one test.
func usageDB(t testing.TB) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "usage.db"), testLogger())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// usageBase is the earliest time the random usage generator uses.
var usageBase = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// testMonthStart returns the first instant of the UTC month of t.
func testMonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// randomUsage builds n usage rows spread over spanDays from usageBase. About
// one row in four sits exactly on a month boundary or one nanosecond before.
func randomUsage(r *rand.Rand, n, spanDays int) []core.AIUsageRecord {
	pick := func(vals ...string) string { return vals[r.IntN(len(vals))] }
	statuses := []string{core.UsageOK, core.UsageOK, core.UsageOK, core.UsageError, core.UsageCanceled, core.UsageBudget}
	out := make([]core.AIUsageRecord, 0, n)
	for i := 0; i < n; i++ {
		at := usageBase.Add(time.Duration(r.Int64N(int64(spanDays) * int64(24*time.Hour))))
		switch r.IntN(8) {
		case 0:
			at = testMonthStart(at)
		case 1:
			at = testMonthStart(at).Add(-time.Nanosecond)
		}
		out = append(out, core.AIUsageRecord{
			ID:     fmt.Sprintf("r%05d", i),
			UserID: pick("u1", "u2", "u3"), ProviderID: pick("p1", "p2"), Model: pick("m1", "m2", ""),
			Feature: pick("ask", "chat", "embeddings"), PluginID: pick("", "", "pl1"),
			InputTokens: r.Int64N(5000), OutputTokens: r.Int64N(2000),
			ImagesIn: r.Int64N(3), ImagesOut: r.Int64N(2), DurationMs: r.Int64N(10000),
			Status: statuses[r.IntN(len(statuses))], CostMicros: r.Int64N(100000),
			Estimated: r.IntN(3) == 0, CreatedAt: at,
		})
	}
	return out
}

// recordAll stores every row.
func recordAll(t testing.TB, db *DB, recs []core.AIUsageRecord) {
	t.Helper()
	for i := range recs {
		rec := recs[i]
		if err := db.RecordAIUsage(context.Background(), &rec); err != nil {
			t.Fatalf("RecordAIUsage: %v", err)
		}
	}
}

// rowKey joins every group key of a row.
func rowKey(r core.AIUsageRow) string {
	return strings.Join([]string{r.Day, r.Month, r.UserID, r.ProviderID, r.Model, r.Feature, r.PluginID}, "|")
}

// addRow adds the sums of src to dst.
func addRow(dst *core.AIUsageRow, src core.AIUsageRow) {
	dst.Calls += src.Calls
	dst.Errors += src.Errors
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.ImagesIn += src.ImagesIn
	dst.ImagesOut += src.ImagesOut
	dst.DurationMs += src.DurationMs
	dst.CostMicros += src.CostMicros
	dst.EstimatedCalls += src.EstimatedCalls
}

// bruteReport sums rows in Go, the oracle for AIUsageReport over raw rows.
func bruteReport(recs []core.AIUsageRecord, q core.AIUsageQuery) (map[string]core.AIUsageRow, core.AIUsageRow) {
	groups, _ := core.NormalizeUsageGroupBy(q.GroupBy)
	has := map[string]bool{}
	for _, g := range groups {
		has[g] = true
	}
	out := map[string]core.AIUsageRow{}
	var totals core.AIUsageRow
	eq := func(want, got string) bool { return want == "" || want == got }
	for _, rec := range recs {
		if !eq(q.UserID, rec.UserID) || !eq(q.ProviderID, rec.ProviderID) || !eq(q.Model, rec.Model) ||
			!eq(q.Feature, rec.Feature) || !eq(q.PluginID, rec.PluginID) {
			continue
		}
		if (!q.From.IsZero() && rec.CreatedAt.Before(q.From)) || (!q.To.IsZero() && !rec.CreatedAt.Before(q.To)) {
			continue
		}
		one := core.AIUsageRow{
			Calls: 1, InputTokens: rec.InputTokens, OutputTokens: rec.OutputTokens,
			ImagesIn: rec.ImagesIn, ImagesOut: rec.ImagesOut, DurationMs: rec.DurationMs,
			CostMicros: rec.CostMicros,
		}
		if rec.Status != core.UsageOK {
			one.Errors = 1
		}
		if rec.Estimated {
			one.EstimatedCalls = 1
		}
		addRow(&totals, one)
		var key core.AIUsageRow
		at := rec.CreatedAt.UTC()
		if has[core.GroupDay] {
			key.Day = at.Format("2006-01-02")
		}
		if has[core.GroupMonth] {
			key.Month = at.Format("2006-01")
		}
		if has[core.GroupUser] {
			key.UserID = rec.UserID
		}
		if has[core.GroupProvider] {
			key.ProviderID = rec.ProviderID
		}
		if has[core.GroupModel] {
			key.Model = rec.Model
		}
		if has[core.GroupFeature] {
			key.Feature = rec.Feature
		}
		if has[core.GroupPlugin] {
			key.PluginID = rec.PluginID
		}
		row := out[rowKey(key)]
		if row.Calls == 0 {
			row = key
		}
		addRow(&row, one)
		out[rowKey(key)] = row
	}
	totals.Finish()
	if len(groups) == 0 && totals.Calls > 0 {
		out = map[string]core.AIUsageRow{rowKey(core.AIUsageRow{}): totals}
	}
	for k, r := range out {
		r.Finish()
		out[k] = r
	}
	return out, totals
}

// dbReport runs AIUsageReport and indexes the rows by key. With coarse set,
// day keys are cut to the month, the only resolution a rolled-up month has.
func dbReport(t testing.TB, db *DB, q core.AIUsageQuery, coarse bool) (map[string]core.AIUsageRow, core.AIUsageRow) {
	t.Helper()
	rows, totals, err := db.AIUsageReport(context.Background(), q)
	if err != nil {
		t.Fatalf("AIUsageReport(%+v): %v", q, err)
	}
	out := map[string]core.AIUsageRow{}
	for _, r := range rows {
		if !coarse {
			if _, dup := out[rowKey(r)]; dup {
				t.Fatalf("duplicate group %q in %+v", rowKey(r), q)
			}
			out[rowKey(r)] = r
			continue
		}
		out[rowKey(coarsen(r))] = merge(out[rowKey(coarsen(r))], coarsen(r))
	}
	return out, totals
}

// coarsen cuts the day key of a row to its month.
func coarsen(r core.AIUsageRow) core.AIUsageRow {
	if len(r.Day) > 7 {
		r.Day = r.Day[:7]
	}
	return r
}

// coarseMap applies coarsen to every row of a report map.
func coarseMap(m map[string]core.AIUsageRow) map[string]core.AIUsageRow {
	out := map[string]core.AIUsageRow{}
	for _, r := range m {
		c := coarsen(r)
		out[rowKey(c)] = merge(out[rowKey(c)], c)
	}
	return out
}

// merge adds src to dst, keeping the keys of src when dst is empty.
func merge(dst, src core.AIUsageRow) core.AIUsageRow {
	if dst.Calls == 0 {
		keys := src
		keys.Calls, keys.Errors, keys.InputTokens, keys.OutputTokens = 0, 0, 0, 0
		keys.ImagesIn, keys.ImagesOut, keys.DurationMs, keys.CostMicros, keys.EstimatedCalls = 0, 0, 0, 0, 0
		dst = keys
	}
	addRow(&dst, src)
	dst.Finish()
	return dst
}

// sameReport fails when two report maps or totals differ.
func sameReport(t testing.TB, what string, got, want map[string]core.AIUsageRow, gotT, wantT core.AIUsageRow) {
	t.Helper()
	if !reflect.DeepEqual(gotT, wantT) {
		t.Fatalf("%s: totals\n got %+v\nwant %+v", what, gotT, wantT)
	}
	if len(got) != len(want) {
		t.Fatalf("%s: %d groups, want %d", what, len(got), len(want))
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || !reflect.DeepEqual(g, w) {
			t.Fatalf("%s: group %q\n got %+v\nwant %+v", what, k, g, w)
		}
	}
}

// groupSubsets returns every subset of the group keys.
func groupSubsets() [][]string {
	var out [][]string
	for mask := 0; mask < 1<<len(core.UsageGroupKeys); mask++ {
		var g []string
		for i, k := range core.UsageGroupKeys {
			if mask&(1<<i) != 0 {
				g = append(g, k)
			}
		}
		out = append(out, g)
	}
	return out
}

func TestMigrateFromV3Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v3.db")
	sdb, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	sdb.SetMaxOpenConns(1)
	old := &DB{sql: sdb, logger: testLogger()}
	if _, err := sdb.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	for i, script := range migrations[:3] {
		if err := old.applyMigration(ctx, i+1, script); err != nil {
			t.Fatalf("apply v%d: %v", i+1, err)
		}
	}
	for _, q := range []string{
		`INSERT INTO settings (id, instance_name, ai_enabled, updated_at) VALUES (1, 'Old', 1, '2026-01-01T00:00:00.000000000Z')`,
		`INSERT INTO ai_providers (id, name, kind, base_url, model) VALUES ('p', 'P', 'openai', 'http://127.0.0.1:1', 'm')`,
	} {
		if _, err := sdb.Exec(q); err != nil {
			t.Fatalf("seed v3: %v", err)
		}
	}
	if err := sdb.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path, testLogger())
	if err != nil {
		t.Fatalf("open v3 database with the current build: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.SQL().QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != SchemaVersion() {
		t.Fatalf("version %d (%v), want %d", version, err, SchemaVersion())
	}
	s, err := db.GetSettings(ctx)
	if err != nil || s.InstanceName != "Old" || s.AIMonthlyTokenBudget != 0 || s.AIMonthlyCostBudget != 0 {
		t.Fatalf("settings after upgrade = %+v, %v", s, err)
	}
	p, err := db.GetAIProvider(ctx, "p")
	if err != nil || p.Model != "m" || p.Local || p.Preset != "" {
		t.Fatalf("provider after upgrade = %+v, %v", p, err)
	}
	rec := core.AIUsageRecord{UserID: "u", Feature: "ask", InputTokens: 3, CreatedAt: time.Now()}
	if err := db.RecordAIUsage(ctx, &rec); err != nil {
		t.Fatalf("record after upgrade: %v", err)
	}
	if _, totals, err := db.AIUsageReport(ctx, core.AIUsageQuery{}); err != nil || totals.Calls != 1 {
		t.Fatalf("report after upgrade = %+v, %v", totals, err)
	}
}

func TestAIUsageRecordAndCallsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := usageDB(t)
	at := time.Date(2026, 9, 13, 9, 0, 0, 123456789, time.FixedZone("x", 7200))
	full := core.AIUsageRecord{
		ID: "a", UserID: "u1", ProviderID: "p", Model: "m", Feature: "chat", PluginID: "pl",
		SpaceID: "s", NoteID: "n", InputTokens: 10, OutputTokens: 20, ImagesIn: 1, ImagesOut: 2,
		DurationMs: 300, Status: core.UsageError, Error: strings.Repeat("é", 400), CostMicros: 1500,
		Estimated: true, CreatedAt: at,
	}
	if err := db.RecordAIUsage(ctx, &full); err != nil {
		t.Fatal(err)
	}
	slow := core.AIUsageRecord{ID: "b", UserID: "u2", DurationMs: 9000, CreatedAt: at.Add(-time.Hour)}
	recent := core.AIUsageRecord{ID: "c", UserID: "u1", DurationMs: 5, CreatedAt: at.Add(time.Hour)}
	for _, r := range []*core.AIUsageRecord{&slow, &recent} {
		if err := db.RecordAIUsage(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordAIUsage(ctx, &core.AIUsageRecord{Status: "weird"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad status = %v, want ErrInvalid", err)
	}

	calls, err := db.AIUsageCalls(ctx, core.AIUsageQuery{}, core.UsageSortRecent)
	if err != nil || len(calls) != 3 || calls[0].ID != "c" || calls[1].ID != "a" || calls[2].ID != "b" {
		t.Fatalf("recent = %+v, %v", calls, err)
	}
	got := *calls[1]
	if len(got.Error) > 500 || !strings.HasPrefix(full.Error, got.Error) || got.Error == "" {
		t.Fatalf("error text not cut on a rune boundary to 500 bytes: %d bytes", len(got.Error))
	}
	want := full
	want.Error = got.Error
	want.CreatedAt = at.UTC()
	want.Cost = 0.0015
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip\n got %+v\nwant %+v", got, want)
	}
	if calls, _ := db.AIUsageCalls(ctx, core.AIUsageQuery{}, core.UsageSortSlowest); calls[0].ID != "b" {
		t.Fatalf("slowest first = %s", calls[0].ID)
	}
	if calls, _ := db.AIUsageCalls(ctx, core.AIUsageQuery{UserID: "u1"}, core.UsageSortFailed); len(calls) != 1 || calls[0].ID != "a" {
		t.Fatalf("failed calls of u1 = %+v", calls)
	}
	if calls, _ := db.AIUsageCalls(ctx, core.AIUsageQuery{Limit: 1}, ""); len(calls) != 1 {
		t.Fatalf("limit 1 gave %d calls", len(calls))
	}
	if _, err := db.AIUsageCalls(ctx, core.AIUsageQuery{}, "loudest"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown sort = %v", err)
	}
	if _, _, err := db.AIUsageReport(ctx, core.AIUsageQuery{GroupBy: []string{"colour"}}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("unknown group = %v", err)
	}
}

func TestAIUsageGroupByMatchesBruteForce(t *testing.T) {
	db := usageDB(t)
	recs := randomUsage(rand.New(rand.NewPCG(1, 2)), 400, 900)
	recordAll(t, db, recs)
	queries := []core.AIUsageQuery{
		{},
		{UserID: "u2"},
		{From: time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC), To: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ProviderID: "p1", Feature: "chat", PluginID: "pl1"},
		{Model: "m2", From: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, groups := range groupSubsets() {
		for _, q := range queries {
			q.GroupBy = groups
			got, gotT := dbReport(t, db, q, false)
			want, wantT := bruteReport(recs, q)
			sameReport(t, fmt.Sprintf("group %v query %+v", groups, q), got, want, gotT, wantT)
		}
	}
	rows, _, err := db.AIUsageReport(context.Background(), core.AIUsageQuery{GroupBy: []string{"feature", "month"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Month > rows[i].Month {
			t.Fatalf("time groups not in order: %s before %s", rows[i-1].Month, rows[i].Month)
		}
	}
}

func TestAIUsageMonthBoundariesUTC(t *testing.T) {
	ctx := context.Background()
	db := usageDB(t)
	plus2 := time.FixedZone("plus2", 2*3600)
	recs := []core.AIUsageRecord{
		{ID: "jan-last", InputTokens: 1, CreatedAt: time.Date(2026, 1, 31, 23, 59, 59, 999999999, time.UTC)},
		{ID: "feb-first", InputTokens: 10, CreatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		// 01:00 on 1 February at +02:00 is 23:00 on 31 January in UTC.
		{ID: "jan-local", InputTokens: 100, CreatedAt: time.Date(2026, 2, 1, 1, 0, 0, 0, plus2)},
	}
	recordAll(t, db, recs)
	feb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	check := func(stage string) {
		t.Helper()
		months, _, err := db.AIUsageReport(ctx, core.AIUsageQuery{GroupBy: []string{"month"}})
		if err != nil || len(months) != 2 || months[0].Month != "2026-01" || months[0].InputTokens != 101 ||
			months[1].Month != "2026-02" || months[1].InputTokens != 10 {
			t.Fatalf("%s: months = %+v, %v", stage, months, err)
		}
		if _, tot, _ := db.AIUsageReport(ctx, core.AIUsageQuery{To: feb}); tot.InputTokens != 101 {
			t.Fatalf("%s: before February = %d tokens, want 101", stage, tot.InputTokens)
		}
		if _, tot, _ := db.AIUsageReport(ctx, core.AIUsageQuery{From: feb, To: feb.AddDate(0, 1, 0)}); tot.InputTokens != 10 {
			t.Fatalf("%s: February = %d tokens, want 10", stage, tot.InputTokens)
		}
	}
	check("raw")
	months, rows, err := db.RollupAIUsage(ctx, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err != nil || months != 2 || rows != 3 {
		t.Fatalf("rollup = %d months %d rows, %v", months, rows, err)
	}
	check("rolled up")
	days, _, _ := db.AIUsageReport(ctx, core.AIUsageQuery{GroupBy: []string{"day"}})
	if len(days) != 2 || days[0].Day != "2026-01" || days[1].Day != "2026-02" {
		t.Fatalf("days of rolled-up months = %+v", days)
	}
	// A rolled-up month counts in full when the range touches it.
	if _, tot, _ := db.AIUsageReport(ctx, core.AIUsageQuery{From: feb.Add(12 * time.Hour), To: feb.AddDate(0, 0, 2)}); tot.InputTokens != 10 {
		t.Fatalf("part of rolled-up February = %d tokens, want 10", tot.InputTokens)
	}
}

func TestAIUsageRollupIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := usageDB(t)
	recs := randomUsage(rand.New(rand.NewPCG(5, 6)), 300, 1000)
	recordAll(t, db, recs)
	cutoff := time.Date(2025, 8, 13, 0, 0, 0, 0, time.UTC)

	months, rows, err := db.RollupAIUsage(ctx, cutoff)
	if err != nil || months == 0 || rows == 0 {
		t.Fatalf("first rollup = %d months %d rows, %v", months, rows, err)
	}
	var left int
	var oldest string
	db.SQL().QueryRow(`SELECT COUNT(*), COALESCE(MIN(created_at), '') FROM ai_usage`).Scan(&left, &oldest)
	if left+rows != len(recs) || (left > 0 && oldest < "2025-08-01") {
		t.Fatalf("after rollup %d raw rows left, oldest %s", left, oldest)
	}
	snapshot := func() string {
		var b strings.Builder
		r, _ := db.SQL().Query(`SELECT month, user_id, provider_id, model, feature, plugin_id, calls, errors,
			input_tokens, output_tokens, images_in, images_out, duration_ms, cost_micros, estimated_calls
			FROM ai_usage_monthly ORDER BY 1, 2, 3, 4, 5, 6`)
		defer r.Close()
		cols, _ := r.Columns()
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for r.Next() {
			r.Scan(ptrs...)
			fmt.Fprintln(&b, vals...)
		}
		var n int
		db.SQL().QueryRow(`SELECT COUNT(*) FROM ai_usage`).Scan(&n)
		fmt.Fprintln(&b, "raw", n)
		return b.String()
	}
	first := snapshot()
	if months, rows, err := db.RollupAIUsage(ctx, cutoff); err != nil || months != 0 || rows != 0 {
		t.Fatalf("second rollup = %d months %d rows, %v", months, rows, err)
	}
	if second := snapshot(); second != first {
		t.Fatalf("second rollup changed the tables:\n%s\nvs\n%s", first, second)
	}
	for _, groups := range groupSubsets() {
		q := core.AIUsageQuery{GroupBy: groups}
		got, gotT := dbReport(t, db, q, true)
		want, wantT := bruteReport(recs, q)
		sameReport(t, fmt.Sprintf("after rollup, group %v", groups), got, coarseMap(want), gotT, wantT)
	}
}

func TestAIBudgetsAndSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := usageDB(t)

	if err := db.SaveSettings(ctx, &core.Settings{InstanceName: "X", AIMonthlyTokenBudget: 5000, AIMonthlyCostBudget: 12.5}); err != nil {
		t.Fatal(err)
	}
	if s, err := db.GetSettings(ctx); err != nil || s.AIMonthlyTokenBudget != 5000 || s.AIMonthlyCostBudget != 12.5 {
		t.Fatalf("settings = %+v, %v", s, err)
	}

	if _, err := db.GetAIBudget(ctx, core.BudgetUser, "u1"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing budget = %v", err)
	}
	tokens, cost := int64(100), 2.5
	until := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	in := []*core.AIBudget{
		{SubjectKind: core.BudgetUser, SubjectID: "u1"},
		{SubjectKind: core.BudgetPlugin, SubjectID: "pl", MonthlyTokens: &tokens, MonthlyCost: &cost, LiftedUntil: &until},
	}
	for _, b := range in {
		if err := db.SaveAIBudget(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	list, err := db.ListAIBudgets(ctx)
	if err != nil || !reflect.DeepEqual(list, []*core.AIBudget{in[1], in[0]}) {
		t.Fatalf("budgets = %+v, %v", list, err)
	}
	zero := int64(0)
	in[1].MonthlyTokens, in[1].LiftedUntil = &zero, nil
	if err := db.SaveAIBudget(ctx, in[1]); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetAIBudget(ctx, core.BudgetPlugin, "pl"); *got.MonthlyTokens != 0 || got.LiftedUntil != nil || *got.MonthlyCost != 2.5 {
		t.Fatalf("replaced budget = %+v", got)
	}
	if err := db.SaveAIBudget(ctx, &core.AIBudget{SubjectKind: "team", SubjectID: "x"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("bad kind = %v", err)
	}
	if err := db.DeleteAIBudget(ctx, core.BudgetUser, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAIBudget(ctx, core.BudgetUser, "u1"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

// FuzzUsageRollup records random usage across months, rolls it up at a random
// cutoff and checks that every group-by report has the same totals before
// and after, and against a brute-force sum. Day groups are compared at month
// resolution, the finest a rolled-up month keeps. A range aligned to month
// starts must match exactly. A second rollup must change nothing.
func FuzzUsageRollup(f *testing.F) {
	f.Add(uint64(1), uint8(60), uint16(400), uint8(3), uint8(9))
	f.Add(uint64(7), uint8(200), uint16(0), uint8(0), uint8(40))
	f.Add(uint64(42), uint8(0), uint16(1100), uint8(12), uint8(1))
	f.Add(uint64(99), uint8(255), uint16(760), uint8(20), uint8(20))
	db := usageDB(f)
	groupings := [][]string{
		nil, {"day"}, {"month"}, {"user"}, {"provider"}, {"model"}, {"feature"}, {"plugin"},
		{"month", "user"}, {"day", "feature", "plugin"}, {"provider", "model", "feature", "plugin"},
		{"day", "month", "user", "provider", "model", "feature", "plugin"},
	}
	f.Fuzz(func(t *testing.T, seed uint64, n uint8, cutoffDays uint16, fromMonth, spanMonths uint8) {
		ctx := context.Background()
		if _, err := db.SQL().Exec(`DELETE FROM ai_usage; DELETE FROM ai_usage_monthly`); err != nil {
			t.Fatal(err)
		}
		r := rand.New(rand.NewPCG(seed, uint64(n)))
		recs := randomUsage(r, int(n), 1000)
		recordAll(t, db, recs)
		from := usageBase.AddDate(0, int(fromMonth%40)-1, 0)
		aligned := core.AIUsageQuery{From: from, To: from.AddDate(0, int(spanMonths%40)+1, 0)}

		type report struct {
			rows   map[string]core.AIUsageRow
			totals core.AIUsageRow
		}
		take := func() []report {
			var out []report
			for _, g := range groupings {
				for _, base := range []core.AIUsageQuery{{}, aligned, {UserID: "u1", PluginID: "pl1"}} {
					q := base
					q.GroupBy = g
					rows, totals := dbReport(t, db, q, true)
					want, wantT := bruteReport(recs, q)
					sameReport(t, fmt.Sprintf("group %v query %+v", g, base), rows, coarseMap(want), totals, wantT)
					out = append(out, report{rows, totals})
				}
			}
			return out
		}
		before := take()
		cutoff := usageBase.Add(time.Duration(cutoffDays%1100)*24*time.Hour + time.Duration(r.Int64N(int64(24*time.Hour))))
		if _, _, err := db.RollupAIUsage(ctx, cutoff); err != nil {
			t.Fatalf("rollup: %v", err)
		}
		var stale int
		db.SQL().QueryRow(`SELECT COUNT(*) FROM ai_usage WHERE created_at < ?`, fmtTime(testMonthStart(cutoff))).Scan(&stale)
		if stale != 0 {
			t.Fatalf("%d raw rows left before the cutoff month", stale)
		}
		after := take()
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("reports changed across the rollup at %v", cutoff)
		}
		if months, rows, err := db.RollupAIUsage(ctx, cutoff); err != nil || months != 0 || rows != 0 {
			t.Fatalf("second rollup = %d months %d rows, %v", months, rows, err)
		}
		if again := take(); !reflect.DeepEqual(after, again) {
			t.Fatal("a second rollup changed the reports")
		}
	})
}
