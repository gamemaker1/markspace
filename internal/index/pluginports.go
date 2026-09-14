package index

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gamemaker1/markspace/internal/core"

	"github.com/gamemaker1/markspace/internal/ports"
)

// Suggestions returns the suggestion store of d as a ports.SuggestionDB. It
// converts between StoredSuggestion and ports.StoredSuggestion, which carry
// the same fields.
func (d *DB) Suggestions() ports.SuggestionDB { return suggestionPort{d: d} }

var _ ports.UserKinds = (*DB)(nil)

type suggestionPort struct{ d *DB }

func toPort(s *StoredSuggestion) *ports.StoredSuggestion {
	if s == nil {
		return nil
	}
	return &ports.StoredSuggestion{Suggestion: s.Suggestion, Version: s.Version, UpdatedAt: s.UpdatedAt}
}

func toPorts(list []*StoredSuggestion) []*ports.StoredSuggestion {
	out := make([]*ports.StoredSuggestion, len(list))
	for i, s := range list {
		out[i] = toPort(s)
	}
	return out
}

func (p suggestionPort) CreateSuggestion(ctx context.Context, s *ports.StoredSuggestion, now time.Time) error {
	row := &StoredSuggestion{Suggestion: s.Suggestion}
	if err := p.d.CreateSuggestion(ctx, row, now); err != nil {
		return err
	}
	*s = *toPort(row)
	return nil
}

func (p suggestionPort) GetSuggestion(ctx context.Context, id string) (*ports.StoredSuggestion, error) {
	s, err := p.d.GetSuggestion(ctx, id)
	if err != nil {
		return nil, err
	}
	return toPort(s), nil
}

func (p suggestionPort) ListNoteSuggestions(ctx context.Context, noteID string, statuses []string) ([]*ports.StoredSuggestion, error) {
	list, err := p.d.ListNoteSuggestions(ctx, noteID, statuses)
	if err != nil {
		return nil, err
	}
	return toPorts(list), nil
}

func (p suggestionPort) ListSuggestions(ctx context.Context, f ports.SuggestionFilter) ([]*ports.StoredSuggestion, error) {
	list, err := p.d.ListSuggestions(ctx, SuggestionFilter{
		SpaceIDs: f.SpaceIDs, NoteID: f.NoteID, AuthorKind: f.AuthorKind, AuthorID: f.AuthorID,
		Statuses: f.Statuses, Limit: f.Limit,
	})
	if err != nil {
		return nil, err
	}
	return toPorts(list), nil
}

func (p suggestionPort) UpdateSuggestion(ctx context.Context, s *ports.StoredSuggestion, now time.Time) error {
	row := &StoredSuggestion{Suggestion: s.Suggestion, Version: s.Version, UpdatedAt: s.UpdatedAt}
	if err := p.d.UpdateSuggestion(ctx, row, now); err != nil {
		return err
	}
	s.Version, s.UpdatedAt = row.Version, row.UpdatedAt
	return nil
}

func (p suggestionPort) DeleteSuggestion(ctx context.Context, id string) error {
	return p.d.DeleteSuggestion(ctx, id)
}

func (p suggestionPort) DeleteNoteSuggestions(ctx context.Context, noteID string) (int, error) {
	return p.d.DeleteNoteSuggestions(ctx, noteID)
}

func (p suggestionPort) DeleteSpaceSuggestions(ctx context.Context, spaceID string) (int, error) {
	return p.d.DeleteSpaceSuggestions(ctx, spaceID)
}

// DeleteSchedules removes the last-run rows of schedules, so a plugin that is
// installed again starts its schedules afresh.
func (d *DB) DeleteSchedules(ctx context.Context, kinds []string) error {
	if len(kinds) == 0 {
		return nil
	}
	_, err := d.sql.ExecContext(ctx, `DELETE FROM job_schedules WHERE kind IN (`+placeholders(len(kinds))+`)`, stringArgs(kinds)...)
	if err != nil {
		return writeErr("delete schedules", err)
	}
	return nil
}

// DeletePluginUsage removes the raw and monthly AI usage rows of a plugin and
// returns the number of rows removed.
func (d *DB) DeletePluginUsage(ctx context.Context, pluginID string) (int, error) {
	if pluginID == "" {
		return 0, nil
	}
	total := 0
	for _, table := range []string{"ai_usage", "ai_usage_monthly"} {
		res, err := d.sql.ExecContext(ctx, `DELETE FROM `+table+` WHERE plugin_id = ?`, pluginID)
		n, err := affectedCount("delete plugin usage", res, err)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// FileText returns the extracted text of a file, or core.ErrNotFound when
// the file has none.
func (d *DB) FileText(ctx context.Context, fileID string) (string, error) {
	var body string
	err := d.sql.QueryRowContext(ctx, `SELECT body FROM files_fts WHERE file_id = ? LIMIT 1`, fileID).Scan(&body)
	if err != nil {
		return "", readErr("file text", err)
	}
	return body, nil
}

// MaxJobActingTokenTTL caps the life of a job-scoped acting token.
const MaxJobActingTokenTTL = 24 * time.Hour

// CreateJobActingToken stores an acting token for one job. Unlike an
// interaction token it may live until expiresAt, capped at
// MaxJobActingTokenTTL from now, because a job can outlast ten minutes. The
// host revokes it when the job reaches a terminal state.
func (d *DB) CreateJobActingToken(ctx context.Context, tok *ActingToken, expiresAt, now time.Time) error {
	if tok == nil || tok.TokenHash == "" || tok.PluginID == "" || tok.UserID == "" || tok.Interaction == "" {
		return fmt.Errorf("index: create job acting token: token hash, plugin, user and interaction are required: %w", core.ErrInvalid)
	}
	now = now.UTC()
	if limit := now.Add(MaxJobActingTokenTTL); expiresAt.IsZero() || expiresAt.After(limit) {
		expiresAt = limit
	}
	tok.CreatedAt, tok.ExpiresAt = now, expiresAt.UTC()
	return d.tx(ctx, func(t *sql.Tx) error {
		if err := pluginExists(ctx, t, tok.PluginID); err != nil {
			return err
		}
		_, err := t.ExecContext(ctx, `INSERT INTO acting_tokens (token_hash, plugin_id, user_id, interaction, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			tok.TokenHash, tok.PluginID, tok.UserID, tok.Interaction, fmtTime(tok.ExpiresAt), fmtTime(tok.CreatedAt))
		if err != nil {
			return writeErr("create job acting token", err)
		}
		return nil
	})
}

// RevokeInteractionTokens deletes the acting tokens of one interaction of a
// plugin, such as "job:<id>", and returns the number deleted.
func (d *DB) RevokeInteractionTokens(ctx context.Context, pluginID, interaction string) (int, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM acting_tokens WHERE plugin_id = ? AND interaction = ?`, pluginID, interaction)
	return affectedCount("revoke interaction tokens", res, err)
}
