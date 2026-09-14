package index

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// maxUsageError caps the stored error text in bytes.
const maxUsageError = 500

// Report and call list limits.
const (
	defaultUsageRows  = 1000
	maxUsageRows      = 10000
	defaultUsageCalls = 50
	maxUsageCalls     = 1000
)

// usageStatuses lists the values the status column accepts.
var usageStatuses = map[string]bool{
	core.UsageOK: true, core.UsageError: true, core.UsageCanceled: true, core.UsageBudget: true,
}

// RecordAIUsage stores one provider call. An empty ID gets a new identifier,
// a zero CreatedAt gets now, and an empty status becomes ok.
func (d *DB) RecordAIUsage(ctx context.Context, rec *core.AIUsageRecord) error {
	if rec == nil {
		return fmt.Errorf("index: record ai usage: nil record: %w", core.ErrInvalid)
	}
	if rec.ID == "" {
		rec.ID = core.NewID()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	if rec.Status == "" {
		rec.Status = core.UsageOK
	}
	if !usageStatuses[rec.Status] {
		return fmt.Errorf("index: record ai usage: status %q: %w", rec.Status, core.ErrInvalid)
	}
	rec.Error = truncateBytes(rec.Error, maxUsageError)
	rec.Attempts = max(rec.Attempts, 1)
	const q = `INSERT INTO ai_usage (id, user_id, provider_id, model, feature, plugin_id,
		space_id, note_id, input_tokens, output_tokens, images_in, images_out, duration_ms,
		status, error, cost_micros, estimated, created_at, task, attempts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, rec.ID, rec.UserID, rec.ProviderID, rec.Model,
		rec.Feature, rec.PluginID, rec.SpaceID, rec.NoteID, max(rec.InputTokens, 0),
		max(rec.OutputTokens, 0), max(rec.ImagesIn, 0), max(rec.ImagesOut, 0),
		max(rec.DurationMs, 0), rec.Status, rec.Error, max(rec.CostMicros, 0),
		boolInt(rec.Estimated), fmtTime(rec.CreatedAt), rec.Task, rec.Attempts)
	if err != nil {
		return writeErr("record ai usage", err)
	}
	return nil
}

// truncateBytes cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}

// groupColumn maps a group key to its column in the unioned usage rows.
var groupColumn = map[string]string{
	core.GroupDay:      "day",
	core.GroupMonth:    "month",
	core.GroupUser:     "user_id",
	core.GroupProvider: "provider_id",
	core.GroupModel:    "model",
	core.GroupFeature:  "feature",
	core.GroupPlugin:   "plugin_id",
}

// usageFilters builds the WHERE clauses shared by the raw and monthly tables.
func usageFilters(q core.AIUsageQuery) ([]string, []any) {
	var where []string
	var args []any
	add := func(col, v string) {
		if v != "" {
			where = append(where, col+" = ?")
			args = append(args, v)
		}
	}
	add("user_id", q.UserID)
	add("provider_id", q.ProviderID)
	add("model", q.Model)
	add("feature", q.Feature)
	add("plugin_id", q.PluginID)
	return where, args
}

// monthOf returns the YYYY-MM of a UTC instant.
func monthOf(t time.Time) string { return t.UTC().Format("2006-01") }

// AIUsageReport sums raw calls and monthly rollups for q. A rolled-up month
// is indivisible, so it counts in full when any part of it lies in the range.
func (d *DB) AIUsageReport(ctx context.Context, q core.AIUsageQuery) ([]core.AIUsageRow, core.AIUsageRow, error) {
	groups, err := core.NormalizeUsageGroupBy(q.GroupBy)
	if err != nil {
		return nil, core.AIUsageRow{}, err
	}
	filters, filterArgs := usageFilters(q)

	rawWhere := append([]string{"created_at <> ''"}, filters...)
	rawArgs := append([]any{}, filterArgs...)
	monWhere := append([]string{}, filters...)
	monArgs := append([]any{}, filterArgs...)
	if !q.From.IsZero() {
		rawWhere = append(rawWhere, "created_at >= ?")
		rawArgs = append(rawArgs, fmtTime(q.From))
		monWhere = append(monWhere, "month >= ?")
		monArgs = append(monArgs, monthOf(q.From))
	}
	if !q.To.IsZero() {
		rawWhere = append(rawWhere, "created_at < ?")
		rawArgs = append(rawArgs, fmtTime(q.To))
		monWhere = append(monWhere, "month <= ?")
		monArgs = append(monArgs, monthOf(q.To.Add(-time.Nanosecond)))
	}
	if len(monWhere) == 0 {
		monWhere = []string{"1 = 1"}
	}
	union := `SELECT substr(created_at, 1, 10) AS day, substr(created_at, 1, 7) AS month,
			user_id, provider_id, model, feature, plugin_id,
			1 AS calls, CASE WHEN status = 'ok' THEN 0 ELSE 1 END AS errors,
			input_tokens, output_tokens, images_in, images_out, duration_ms, cost_micros,
			estimated AS estimated_calls
		FROM ai_usage WHERE ` + strings.Join(rawWhere, " AND ") + `
		UNION ALL
		SELECT month AS day, month, user_id, provider_id, model, feature, plugin_id,
			calls, errors, input_tokens, output_tokens, images_in, images_out, duration_ms,
			cost_micros, estimated_calls
		FROM ai_usage_monthly WHERE ` + strings.Join(monWhere, " AND ")
	args := append(rawArgs, monArgs...)
	sums := `COUNT(*), COALESCE(SUM(calls), 0), COALESCE(SUM(errors), 0),
		COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(images_in), 0), COALESCE(SUM(images_out), 0),
		COALESCE(SUM(duration_ms), 0), COALESCE(SUM(cost_micros), 0),
		COALESCE(SUM(estimated_calls), 0)`

	var totals core.AIUsageRow
	var n int64
	err = d.sql.QueryRowContext(ctx, `SELECT `+sums+` FROM (`+union+`)`, args...).Scan(&n,
		&totals.Calls, &totals.Errors, &totals.InputTokens, &totals.OutputTokens,
		&totals.ImagesIn, &totals.ImagesOut, &totals.DurationMs, &totals.CostMicros,
		&totals.EstimatedCalls)
	if err != nil {
		return nil, core.AIUsageRow{}, readErr("ai usage totals", err)
	}
	totals.Finish()

	limit := q.Limit
	if limit <= 0 {
		limit = defaultUsageRows
	}
	limit = min(limit, maxUsageRows)
	if len(groups) == 0 {
		if n == 0 {
			return []core.AIUsageRow{}, totals, nil
		}
		return []core.AIUsageRow{totals}, totals, nil
	}

	cols := make([]string, len(groups))
	for i, g := range groups {
		cols[i] = groupColumn[g]
	}
	order := strings.Join(cols, ", ")
	if groups[0] != core.GroupDay && groups[0] != core.GroupMonth {
		order = "SUM(input_tokens) + SUM(output_tokens) DESC, SUM(cost_micros) DESC, SUM(calls) DESC, " + order
	}
	query := `SELECT ` + strings.Join(cols, ", ") + `, ` + sums + ` FROM (` + union + `)
		GROUP BY ` + strings.Join(cols, ", ") + ` ORDER BY ` + order + ` LIMIT ?`
	rows, err := d.sql.QueryContext(ctx, query, append(args, limit)...)
	if err != nil {
		return nil, core.AIUsageRow{}, readErr("ai usage report", err)
	}
	defer rows.Close()
	out := []core.AIUsageRow{}
	for rows.Next() {
		var r core.AIUsageRow
		dest := make([]any, 0, len(groups)+10)
		for _, g := range groups {
			dest = append(dest, groupField(&r, g))
		}
		var count int64
		dest = append(dest, &count, &r.Calls, &r.Errors, &r.InputTokens, &r.OutputTokens,
			&r.ImagesIn, &r.ImagesOut, &r.DurationMs, &r.CostMicros, &r.EstimatedCalls)
		if err := rows.Scan(dest...); err != nil {
			return nil, core.AIUsageRow{}, readErr("ai usage report", err)
		}
		r.Finish()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, core.AIUsageRow{}, readErr("ai usage report", err)
	}
	return out, totals, nil
}

// groupField returns the row field that holds one group key.
func groupField(r *core.AIUsageRow, key string) *string {
	switch key {
	case core.GroupDay:
		return &r.Day
	case core.GroupMonth:
		return &r.Month
	case core.GroupUser:
		return &r.UserID
	case core.GroupProvider:
		return &r.ProviderID
	case core.GroupModel:
		return &r.Model
	case core.GroupFeature:
		return &r.Feature
	}
	return &r.PluginID
}

// AIUsageCalls lists raw calls matching q. sort is recent (newest first),
// slowest (longest first) or failed (calls not ok, newest first).
func (d *DB) AIUsageCalls(ctx context.Context, q core.AIUsageQuery, sort string) ([]*core.AIUsageRecord, error) {
	where, args := usageFilters(q)
	if !q.From.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, fmtTime(q.From))
	}
	if !q.To.IsZero() {
		where = append(where, "created_at < ?")
		args = append(args, fmtTime(q.To))
	}
	var order string
	switch sort {
	case "", core.UsageSortRecent:
		order = "created_at DESC, id DESC"
	case core.UsageSortSlowest:
		order = "duration_ms DESC, created_at DESC, id DESC"
	case core.UsageSortFailed:
		where = append(where, "status <> 'ok'")
		order = "created_at DESC, id DESC"
	default:
		return nil, core.Invalid("sort", "sort must be recent, slowest or failed")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultUsageCalls
	}
	limit = min(limit, maxUsageCalls)
	query := `SELECT id, user_id, provider_id, model, feature, plugin_id, space_id, note_id,
		input_tokens, output_tokens, images_in, images_out, duration_ms, status, error,
		cost_micros, estimated, created_at, task, attempts FROM ai_usage`
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, " AND ")
	}
	query += ` ORDER BY ` + order + ` LIMIT ?`
	rows, err := d.sql.QueryContext(ctx, query, append(args, limit)...)
	if err != nil {
		return nil, readErr("ai usage calls", err)
	}
	defer rows.Close()
	out := []*core.AIUsageRecord{}
	for rows.Next() {
		var (
			r         core.AIUsageRecord
			estimated int
			created   string
		)
		if err := rows.Scan(&r.ID, &r.UserID, &r.ProviderID, &r.Model, &r.Feature, &r.PluginID,
			&r.SpaceID, &r.NoteID, &r.InputTokens, &r.OutputTokens, &r.ImagesIn, &r.ImagesOut,
			&r.DurationMs, &r.Status, &r.Error, &r.CostMicros, &estimated, &created,
			&r.Task, &r.Attempts); err != nil {
			return nil, readErr("ai usage calls", err)
		}
		r.Estimated = intBool(estimated)
		r.CreatedAt = parseTime(created)
		r.Cost = core.MicrosToCost(r.CostMicros)
		out = append(out, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("ai usage calls", err)
	}
	return out, nil
}

// RollupAIUsage folds raw calls from every UTC month before the month that
// contains before into ai_usage_monthly and deletes them. Each month commits
// in its own transaction. The upsert adds to an existing monthly row, and the
// raw rows are gone once folded, so a second run changes nothing.
func (d *DB) RollupAIUsage(ctx context.Context, before time.Time) (int, int, error) {
	b := before.UTC()
	cutoff := time.Date(b.Year(), b.Month(), 1, 0, 0, 0, 0, time.UTC)
	rows, err := d.sql.QueryContext(ctx,
		`SELECT DISTINCT substr(created_at, 1, 7) FROM ai_usage
		WHERE created_at <> '' AND created_at < ? ORDER BY 1`, fmtTime(cutoff))
	if err != nil {
		return 0, 0, readErr("ai usage months", err)
	}
	var months []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			rows.Close()
			return 0, 0, readErr("ai usage months", err)
		}
		months = append(months, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, readErr("ai usage months", err)
	}

	folded, total := 0, 0
	for _, m := range months {
		start, err := time.Parse("2006-01", m)
		if err != nil {
			d.logger.Warn("index: skip unreadable ai usage month", "month", m)
			continue
		}
		from, to := fmtTime(start), fmtTime(start.AddDate(0, 1, 0))
		var n int64
		err = d.tx(ctx, func(t *sql.Tx) error {
			const upsert = `INSERT INTO ai_usage_monthly (month, user_id, provider_id, model,
				feature, plugin_id, calls, errors, input_tokens, output_tokens, images_in,
				images_out, duration_ms, cost_micros, estimated_calls)
				SELECT ?, user_id, provider_id, model, feature, plugin_id, COUNT(*),
					SUM(CASE WHEN status = 'ok' THEN 0 ELSE 1 END), SUM(input_tokens),
					SUM(output_tokens), SUM(images_in), SUM(images_out), SUM(duration_ms),
					SUM(cost_micros), SUM(estimated)
				FROM ai_usage WHERE created_at >= ? AND created_at < ?
				GROUP BY user_id, provider_id, model, feature, plugin_id
				ON CONFLICT (month, user_id, provider_id, model, feature, plugin_id) DO UPDATE SET
					calls = calls + excluded.calls,
					errors = errors + excluded.errors,
					input_tokens = input_tokens + excluded.input_tokens,
					output_tokens = output_tokens + excluded.output_tokens,
					images_in = images_in + excluded.images_in,
					images_out = images_out + excluded.images_out,
					duration_ms = duration_ms + excluded.duration_ms,
					cost_micros = cost_micros + excluded.cost_micros,
					estimated_calls = estimated_calls + excluded.estimated_calls`
			if _, err := t.ExecContext(ctx, upsert, m, from, to); err != nil {
				return writeErr("roll up ai usage "+m, err)
			}
			res, err := t.ExecContext(ctx,
				`DELETE FROM ai_usage WHERE created_at >= ? AND created_at < ?`, from, to)
			if err != nil {
				return writeErr("delete rolled up ai usage "+m, err)
			}
			n, err = res.RowsAffected()
			return err
		})
		if err != nil {
			return folded, total, err
		}
		folded++
		total += int(n)
	}
	return folded, total, nil
}

// Budgets.

// validBudgetKind reports whether kind names a budget subject.
func validBudgetKind(kind string) bool {
	return kind == core.BudgetUser || kind == core.BudgetPlugin
}

// scanBudget reads one ai_budgets row.
func scanBudget(row rowScanner) (*core.AIBudget, error) {
	var (
		b      core.AIBudget
		tokens sql.NullInt64
		cost   sql.NullFloat64
		lifted string
	)
	if err := row.Scan(&b.SubjectKind, &b.SubjectID, &tokens, &cost, &lifted); err != nil {
		return nil, err
	}
	if tokens.Valid {
		v := tokens.Int64
		b.MonthlyTokens = &v
	}
	if cost.Valid {
		v := cost.Float64
		b.MonthlyCost = &v
	}
	b.LiftedUntil = parseTimePtr(lifted)
	return &b, nil
}

// GetAIBudget reads the override of one subject.
func (d *DB) GetAIBudget(ctx context.Context, kind, id string) (*core.AIBudget, error) {
	row := d.sql.QueryRowContext(ctx, `SELECT subject_kind, subject_id, monthly_tokens,
		monthly_cost, lifted_until FROM ai_budgets WHERE subject_kind = ? AND subject_id = ?`, kind, id)
	b, err := scanBudget(row)
	if err != nil {
		return nil, readErr("get ai budget", err)
	}
	return b, nil
}

// ListAIBudgets returns every override ordered by kind and id.
func (d *DB) ListAIBudgets(ctx context.Context) ([]*core.AIBudget, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT subject_kind, subject_id, monthly_tokens,
		monthly_cost, lifted_until FROM ai_budgets ORDER BY subject_kind, subject_id`)
	if err != nil {
		return nil, readErr("list ai budgets", err)
	}
	defer rows.Close()
	out := []*core.AIBudget{}
	for rows.Next() {
		b, err := scanBudget(rows)
		if err != nil {
			return nil, readErr("list ai budgets", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list ai budgets", err)
	}
	return out, nil
}

// SaveAIBudget creates or replaces the override of one subject.
func (d *DB) SaveAIBudget(ctx context.Context, b *core.AIBudget) error {
	if b == nil || !validBudgetKind(b.SubjectKind) || b.SubjectID == "" {
		return fmt.Errorf("index: save ai budget: %w", core.ErrInvalid)
	}
	var tokens, cost any
	if b.MonthlyTokens != nil {
		tokens = max(*b.MonthlyTokens, 0)
	}
	if b.MonthlyCost != nil {
		cost = core.MicrosToCost(core.CostToMicros(*b.MonthlyCost))
	}
	_, err := d.sql.ExecContext(ctx, `INSERT INTO ai_budgets (subject_kind, subject_id,
		monthly_tokens, monthly_cost, lifted_until) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (subject_kind, subject_id) DO UPDATE SET
			monthly_tokens = excluded.monthly_tokens,
			monthly_cost = excluded.monthly_cost,
			lifted_until = excluded.lifted_until`,
		b.SubjectKind, b.SubjectID, tokens, cost, fmtTimePtr(b.LiftedUntil))
	if err != nil {
		return writeErr("save ai budget", err)
	}
	return nil
}

// DeleteAIBudget removes the override of one subject.
func (d *DB) DeleteAIBudget(ctx context.Context, kind, id string) error {
	res, err := d.sql.ExecContext(ctx,
		`DELETE FROM ai_budgets WHERE subject_kind = ? AND subject_id = ?`, kind, id)
	return affected("delete ai budget", res, err)
}
