package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// aiModelColumns is the SELECT list matching scanAIModel.
const aiModelColumns = `provider_id, model_id, name, vendor, context_length, max_output,
	inputs, outputs, tools, price, price_source, price_matched, price_fetched_at,
	price_override, enabled, available, last_seen`

// scanAIModel reads one ai_models row. The fetched price is nil when the
// row has no price source or the source is none.
func scanAIModel(row rowScanner) (*core.AIModel, error) {
	var (
		m                         core.AIModel
		inputs, outputs           string
		price, override           string
		fetched, lastSeen         string
		tools, enabled, available int
	)
	err := row.Scan(&m.ProviderID, &m.ModelID, &m.Name, &m.Vendor, &m.ContextLength,
		&m.MaxOutput, &inputs, &outputs, &tools, &price, &m.PriceSource, &m.PriceMatched,
		&fetched, &override, &enabled, &available, &lastSeen)
	if err != nil {
		return nil, err
	}
	m.Inputs = modalities(inputs)
	m.Outputs = modalities(outputs)
	m.Tools = intBool(tools)
	if m.PriceSource != "" && m.PriceSource != core.PriceSourceNone {
		m.Price = unmarshalPrice(price)
	}
	if override != "" {
		m.PriceOverride = unmarshalPrice(override)
	}
	m.PriceFetchedAt = parseTime(fetched)
	m.Enabled = intBool(enabled)
	m.Available = intBool(available)
	m.LastSeen = parseTime(lastSeen)
	return &m, nil
}

// modalities reads a JSON list of modalities. An unreadable or empty value
// is text only.
func modalities(s string) []string {
	out := unmarshalStrings(s)
	if len(out) == 0 {
		return []string{core.ModalityText}
	}
	return out
}

// marshalModalities renders a modality list, text only when empty.
func marshalModalities(v []string) string {
	if len(v) == 0 {
		return `["text"]`
	}
	return marshalStrings(v)
}

// marshalPrice renders a price as JSON. A nil price becomes empty for the
// override column and "{}" for the fetched column, chosen by the caller.
func marshalPrice(p *core.AIPrice, empty string) string {
	if p == nil {
		return empty
	}
	b, err := json.Marshal(p)
	if err != nil {
		return empty
	}
	return string(b)
}

// unmarshalPrice reads a stored price. Unreadable JSON yields nil.
func unmarshalPrice(s string) *core.AIPrice {
	var p core.AIPrice
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return nil
	}
	return &p
}

// ListAIModels returns the catalog rows of one provider, or of every
// provider when providerID is empty, ordered by provider and model id.
func (d *DB) ListAIModels(ctx context.Context, providerID string) ([]*core.AIModel, error) {
	q := `SELECT ` + aiModelColumns + ` FROM ai_models`
	var args []any
	if providerID != "" {
		q += ` WHERE provider_id = ?`
		args = append(args, providerID)
	}
	q += ` ORDER BY provider_id, model_id`
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, readErr("list ai models", err)
	}
	defer rows.Close()
	var out []*core.AIModel
	for rows.Next() {
		m, err := scanAIModel(rows)
		if err != nil {
			return nil, readErr("list ai models", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list ai models", err)
	}
	return out, nil
}

// GetAIModel reads one catalog row.
func (d *DB) GetAIModel(ctx context.Context, providerID, modelID string) (*core.AIModel, error) {
	row := d.sql.QueryRowContext(ctx, `SELECT `+aiModelColumns+` FROM ai_models
		WHERE provider_id = ? AND model_id = ?`, providerID, modelID)
	m, err := scanAIModel(row)
	if err != nil {
		return nil, readErr("get ai model "+providerID+"/"+modelID, err)
	}
	return m, nil
}

// UpsertAIModels writes the listed models of one provider in one
// transaction. A new row takes the Enabled flag of the model. An existing
// row keeps its enabled flag and price override. Every row of the provider
// that is not in the list becomes unavailable and keeps its other fields.
func (d *DB) UpsertAIModels(ctx context.Context, providerID string, models []*core.AIModel, seen time.Time) error {
	if providerID == "" {
		return fmt.Errorf("index: upsert ai models: empty provider id: %w", core.ErrInvalid)
	}
	const upsert = `INSERT INTO ai_models (provider_id, model_id, name, vendor,
		context_length, max_output, inputs, outputs, tools, price, price_source,
		price_matched, price_fetched_at, price_override, enabled, available, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, 1, ?)
		ON CONFLICT(provider_id, model_id) DO UPDATE SET
			name = excluded.name,
			vendor = excluded.vendor,
			context_length = excluded.context_length,
			max_output = excluded.max_output,
			inputs = excluded.inputs,
			outputs = excluded.outputs,
			tools = excluded.tools,
			price = excluded.price,
			price_source = excluded.price_source,
			price_matched = excluded.price_matched,
			price_fetched_at = excluded.price_fetched_at,
			available = 1,
			last_seen = excluded.last_seen`
	return d.tx(ctx, func(t *sql.Tx) error {
		ids := make([]string, 0, len(models))
		for _, m := range models {
			if m == nil || m.ModelID == "" {
				continue
			}
			ids = append(ids, m.ModelID)
			_, err := t.ExecContext(ctx, upsert, providerID, m.ModelID, m.Name, m.Vendor,
				max(m.ContextLength, 0), max(m.MaxOutput, 0), marshalModalities(m.Inputs),
				marshalModalities(m.Outputs), boolInt(m.Tools), marshalPrice(m.Price, "{}"),
				m.PriceSource, m.PriceMatched, fmtTime(m.PriceFetchedAt), boolInt(m.Enabled),
				fmtTime(seen))
			if err != nil {
				return writeErr("upsert ai model", err)
			}
		}
		q := `UPDATE ai_models SET available = 0 WHERE provider_id = ?`
		args := []any{providerID}
		if len(ids) > 0 {
			// A temporary table avoids the variable limit on long listings.
			if _, err := t.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS listed_models (model_id TEXT PRIMARY KEY)`); err != nil {
				return writeErr("mark unlisted ai models", err)
			}
			if _, err := t.ExecContext(ctx, `DELETE FROM listed_models`); err != nil {
				return writeErr("mark unlisted ai models", err)
			}
			for _, id := range ids {
				if _, err := t.ExecContext(ctx, `INSERT OR IGNORE INTO listed_models (model_id) VALUES (?)`, id); err != nil {
					return writeErr("mark unlisted ai models", err)
				}
			}
			q += ` AND model_id NOT IN (SELECT model_id FROM listed_models)`
		}
		if _, err := t.ExecContext(ctx, q, args...); err != nil {
			return writeErr("mark unlisted ai models", err)
		}
		if len(ids) > 0 {
			if _, err := t.ExecContext(ctx, `DELETE FROM listed_models`); err != nil {
				return writeErr("mark unlisted ai models", err)
			}
		}
		return nil
	})
}

// UpdateAIModelPrice writes the fetched price fields of one row.
func (d *DB) UpdateAIModelPrice(ctx context.Context, m *core.AIModel) error {
	if m == nil {
		return fmt.Errorf("index: update ai model price: nil model: %w", core.ErrInvalid)
	}
	res, err := d.sql.ExecContext(ctx, `UPDATE ai_models SET price = ?, price_source = ?,
		price_matched = ?, price_fetched_at = ? WHERE provider_id = ? AND model_id = ?`,
		marshalPrice(m.Price, "{}"), m.PriceSource, m.PriceMatched, fmtTime(m.PriceFetchedAt),
		m.ProviderID, m.ModelID)
	return affected("update ai model price", res, err)
}

// UpdateAIModelSettings writes the enabled flag and the price override of
// one row. A nil override clears it.
func (d *DB) UpdateAIModelSettings(ctx context.Context, providerID, modelID string, enabled bool, override *core.AIPrice) error {
	res, err := d.sql.ExecContext(ctx, `UPDATE ai_models SET enabled = ?, price_override = ?
		WHERE provider_id = ? AND model_id = ?`,
		boolInt(enabled), marshalPrice(override, ""), providerID, modelID)
	return affected("update ai model", res, err)
}

// DeleteAIModels removes every catalog row of one provider.
func (d *DB) DeleteAIModels(ctx context.Context, providerID string) error {
	if _, err := d.sql.ExecContext(ctx, `DELETE FROM ai_models WHERE provider_id = ?`, providerID); err != nil {
		return writeErr("delete ai models", err)
	}
	return nil
}

// ListAIRoutes returns the routes of one scope ordered by task.
func (d *DB) ListAIRoutes(ctx context.Context, scope string) ([]core.AIRoute, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT task, targets FROM ai_routes WHERE scope = ? ORDER BY task`, scope)
	if err != nil {
		return nil, readErr("list ai routes", err)
	}
	defer rows.Close()
	var out []core.AIRoute
	for rows.Next() {
		var r core.AIRoute
		var targets string
		if err := rows.Scan(&r.Task, &targets); err != nil {
			return nil, readErr("list ai routes", err)
		}
		if err := json.Unmarshal([]byte(targets), &r.Targets); err != nil {
			r.Targets = nil
		}
		if r.Targets == nil {
			r.Targets = []core.AIRouteTarget{}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list ai routes", err)
	}
	return out, nil
}

// SaveAIRoute replaces the route of one task in one scope.
func (d *DB) SaveAIRoute(ctx context.Context, scope string, r core.AIRoute) error {
	if strings.TrimSpace(scope) == "" || strings.TrimSpace(r.Task) == "" {
		return fmt.Errorf("index: save ai route: empty scope or task: %w", core.ErrInvalid)
	}
	targets := r.Targets
	if targets == nil {
		targets = []core.AIRouteTarget{}
	}
	b, err := json.Marshal(targets)
	if err != nil {
		return fmt.Errorf("index: save ai route: %w", err)
	}
	_, err = d.sql.ExecContext(ctx, `INSERT INTO ai_routes (scope, task, targets) VALUES (?, ?, ?)
		ON CONFLICT(scope, task) DO UPDATE SET targets = excluded.targets`, scope, r.Task, string(b))
	if err != nil {
		return writeErr("save ai route", err)
	}
	return nil
}

// DeleteAIRoute removes the route of one task in one scope.
func (d *DB) DeleteAIRoute(ctx context.Context, scope, task string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM ai_routes WHERE scope = ? AND task = ?`, scope, task)
	return affected("delete ai route", res, err)
}

// GetPriceCatalog returns the stored body of one public price catalog and
// when it was downloaded.
func (d *DB) GetPriceCatalog(ctx context.Context, source string) ([]byte, time.Time, error) {
	var body []byte
	var fetched string
	err := d.sql.QueryRowContext(ctx,
		`SELECT body, fetched_at FROM price_catalogs WHERE source = ?`, source).Scan(&body, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, fmt.Errorf("index: price catalog %s: %w", source, core.ErrNotFound)
	}
	if err != nil {
		return nil, time.Time{}, readErr("get price catalog", err)
	}
	return body, parseTime(fetched), nil
}

// SavePriceCatalog stores the body of one public price catalog.
func (d *DB) SavePriceCatalog(ctx context.Context, source string, body []byte, fetchedAt time.Time) error {
	_, err := d.sql.ExecContext(ctx, `INSERT INTO price_catalogs (source, body, fetched_at) VALUES (?, ?, ?)
		ON CONFLICT(source) DO UPDATE SET body = excluded.body, fetched_at = excluded.fetched_at`,
		source, body, fmtTime(fetchedAt))
	if err != nil {
		return writeErr("save price catalog", err)
	}
	return nil
}
