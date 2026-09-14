package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// DefaultInstanceName is the instance name used until an admin sets one.
const DefaultInstanceName = "Markspace"

// defaultSettings returns the settings of a fresh instance.
func defaultSettings() *core.Settings {
	return &core.Settings{
		InstanceName:      DefaultInstanceName,
		AllowSignup:       true,
		AIEnabled:         true,
		AIAllowUserKeys:   true,
		AIAllowUserRoutes: true,
	}
}

// GetSettings reads the single settings row. A database that has none yet
// returns the defaults, so this call never reports core.ErrNotFound.
func (d *DB) GetSettings(ctx context.Context) (*core.Settings, error) {
	const q = `SELECT instance_name, allow_signup, default_space_id, ai_enabled,
		ai_allow_user_keys, ai_allow_user_routes,
		ai_monthly_token_budget, ai_monthly_cost_budget, updated_at
		FROM settings WHERE id = 1`
	var (
		s                                                  core.Settings
		allowSignup, aiEnabled, allowUserKeys, allowRoutes int
		updated                                            string
	)
	err := d.sql.QueryRowContext(ctx, q).Scan(&s.InstanceName, &allowSignup,
		&s.DefaultSpaceID, &aiEnabled, &allowUserKeys, &allowRoutes,
		&s.AIMonthlyTokenBudget, &s.AIMonthlyCostBudget, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultSettings(), nil
	}
	if err != nil {
		return nil, readErr("get settings", err)
	}
	s.AllowSignup = intBool(allowSignup)
	s.AIEnabled = intBool(aiEnabled)
	s.AIAllowUserKeys = intBool(allowUserKeys)
	s.AIAllowUserRoutes = intBool(allowRoutes)
	s.UpdatedAt = parseTime(updated)
	if s.InstanceName == "" {
		s.InstanceName = DefaultInstanceName
	}
	return &s, nil
}

// SaveSettings writes the single settings row and sets UpdatedAt to now.
func (d *DB) SaveSettings(ctx context.Context, s *core.Settings) error {
	if s == nil {
		return fmt.Errorf("index: save settings: nil settings: %w", core.ErrInvalid)
	}
	s.UpdatedAt = time.Now().UTC()
	const q = `INSERT INTO settings (id, instance_name, allow_signup, default_space_id,
		ai_enabled, ai_allow_user_keys, ai_allow_user_routes,
		ai_monthly_token_budget, ai_monthly_cost_budget, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			instance_name = excluded.instance_name,
			allow_signup = excluded.allow_signup,
			default_space_id = excluded.default_space_id,
			ai_enabled = excluded.ai_enabled,
			ai_allow_user_keys = excluded.ai_allow_user_keys,
			ai_allow_user_routes = excluded.ai_allow_user_routes,
			ai_monthly_token_budget = excluded.ai_monthly_token_budget,
			ai_monthly_cost_budget = excluded.ai_monthly_cost_budget,
			updated_at = excluded.updated_at`
	_, err := d.sql.ExecContext(ctx, q, s.InstanceName, boolInt(s.AllowSignup),
		s.DefaultSpaceID, boolInt(s.AIEnabled), boolInt(s.AIAllowUserKeys),
		boolInt(s.AIAllowUserRoutes), max(s.AIMonthlyTokenBudget, 0),
		core.MicrosToCost(core.CostToMicros(s.AIMonthlyCostBudget)), fmtTime(s.UpdatedAt))
	if err != nil {
		return writeErr("save settings", err)
	}
	return nil
}

// AI providers.

// aiProviderColumns is the SELECT list matching scanAIProvider.
const aiProviderColumns = `id, name, kind, base_url, api_key, model, embed_model,
	models, headers, temperature, max_tokens, timeout_seconds, enabled,
	created_at, updated_at, owner_id, image_config, preset, local`

// scanAIProvider reads one ai_providers row. Source is set to "db", so the
// caller can tell a stored provider from one defined in the configuration
// file. HasAPIKey is derived, and APIKey is returned because the AI layer
// needs it to call the model server.
func scanAIProvider(row rowScanner) (*core.AIProviderConfig, error) {
	var (
		p                            core.AIProviderConfig
		kind                         string
		models, headers, imageConfig string
		enabled, local               int
		created, updated             string
	)
	err := row.Scan(&p.ID, &p.Name, &kind, &p.BaseURL, &p.APIKey, &p.Model,
		&p.EmbedModel, &models, &headers, &p.Temperature, &p.MaxTokens,
		&p.TimeoutSeconds, &enabled, &created, &updated, &p.OwnerID, &imageConfig,
		&p.Preset, &local)
	if err != nil {
		return nil, err
	}
	p.Kind = core.AIProviderKind(kind)
	p.Models = unmarshalStrings(models)
	p.Headers = unmarshalHeaders(headers)
	applyImageConfig(&p, imageConfig)
	p.Enabled = intBool(enabled)
	p.Local = intBool(local)
	p.HasAPIKey = p.APIKey != ""
	p.Source = "db"
	p.CreatedAt = parseTime(created)
	p.UpdatedAt = parseTime(updated)
	return &p, nil
}

// CreateAIProvider inserts a provider. An empty ID is filled with a new
// identifier and zero timestamps are set to now.
func (d *DB) CreateAIProvider(ctx context.Context, p *core.AIProviderConfig) error {
	if p == nil {
		return fmt.Errorf("index: create ai provider: nil provider: %w", core.ErrInvalid)
	}
	if p.ID == "" {
		p.ID = core.NewID()
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	const q = `INSERT INTO ai_providers (` + aiProviderColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, p.ID, p.Name, string(p.Kind), p.BaseURL,
		p.APIKey, p.Model, p.EmbedModel, marshalStrings(p.Models),
		marshalHeaders(p.Headers), p.Temperature, p.MaxTokens, p.TimeoutSeconds,
		boolInt(p.Enabled), fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt), p.OwnerID,
		marshalImageConfig(p), p.Preset, boolInt(p.Local))
	if err != nil {
		return writeErr("create ai provider", err)
	}
	p.HasAPIKey = p.APIKey != ""
	p.Source = "db"
	return nil
}

// UpdateAIProvider writes every mutable field of a provider and sets
// UpdatedAt to now. OwnerID is fixed at creation and not changed here. An
// empty APIKey clears the stored key, so a caller that wants to keep it must
// read the provider first and pass the key back.
func (d *DB) UpdateAIProvider(ctx context.Context, p *core.AIProviderConfig) error {
	if p == nil {
		return fmt.Errorf("index: update ai provider: nil provider: %w", core.ErrInvalid)
	}
	p.UpdatedAt = time.Now().UTC()
	const q = `UPDATE ai_providers SET name = ?, kind = ?, base_url = ?, api_key = ?,
		model = ?, embed_model = ?, models = ?, headers = ?, temperature = ?,
		max_tokens = ?, timeout_seconds = ?, enabled = ?, updated_at = ?,
		image_config = ?, preset = ?, local = ?
		WHERE id = ?`
	res, err := d.sql.ExecContext(ctx, q, p.Name, string(p.Kind), p.BaseURL, p.APIKey,
		p.Model, p.EmbedModel, marshalStrings(p.Models), marshalHeaders(p.Headers),
		p.Temperature, p.MaxTokens, p.TimeoutSeconds, boolInt(p.Enabled),
		fmtTime(p.UpdatedAt), marshalImageConfig(p), p.Preset, boolInt(p.Local), p.ID)
	if err := affected("update ai provider", res, err); err != nil {
		return err
	}
	p.HasAPIKey = p.APIKey != ""
	p.Source = "db"
	return nil
}

// DeleteAIProvider removes a provider and its catalog rows.
func (d *DB) DeleteAIProvider(ctx context.Context, id string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		res, err := t.ExecContext(ctx, `DELETE FROM ai_providers WHERE id = ?`, id)
		if err := affected("delete ai provider", res, err); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `DELETE FROM ai_models WHERE provider_id = ?`, id); err != nil {
			return writeErr("delete ai provider models", err)
		}
		return nil
	})
}

// GetAIProvider reads one provider by id.
func (d *DB) GetAIProvider(ctx context.Context, id string) (*core.AIProviderConfig, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+aiProviderColumns+` FROM ai_providers WHERE id = ?`, id)
	p, err := scanAIProvider(row)
	if err != nil {
		return nil, readErr("get ai provider "+id, err)
	}
	return p, nil
}

// ListAIProviders returns every stored provider by name.
func (d *DB) ListAIProviders(ctx context.Context) ([]*core.AIProviderConfig, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+aiProviderColumns+` FROM ai_providers
		ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, readErr("list ai providers", err)
	}
	defer rows.Close()

	var out []*core.AIProviderConfig
	for rows.Next() {
		p, err := scanAIProvider(rows)
		if err != nil {
			return nil, readErr("list ai providers", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list ai providers", err)
	}
	return out, nil
}
