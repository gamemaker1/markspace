package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// StoredSuggestion is one suggestions row. Version starts at 1 and grows by
// one on every UpdateSuggestion. It is the compare-and-swap token.
type StoredSuggestion struct {
	module.Suggestion
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SuggestionFilter selects suggestions in ListSuggestions. Empty fields match
// every row.
type SuggestionFilter struct {
	SpaceIDs   []string
	NoteID     string
	AuthorKind string
	AuthorID   string
	Statuses   []string
	// BeforeCreatedAt and BeforeID continue a listing after the last row of
	// the previous page. Both must be set together.
	BeforeCreatedAt time.Time
	BeforeID        string
	// Limit caps the result. 0 means no limit.
	Limit int
}

var suggestionStatuses = []string{
	module.SuggestionOpen, module.SuggestionAccepted, module.SuggestionPartial,
	module.SuggestionDiscarded, module.SuggestionStale,
}

// suggestionColumns is the column list matching scanSuggestion.
const suggestionColumns = `id, space_id, note_id, author_kind, author_id, title, rationale, edits,
	base_updated_at, status, created_at, resolved_by, resolved_at, version, updated_at`

func scanSuggestion(row rowScanner) (*StoredSuggestion, error) {
	var (
		s                                         StoredSuggestion
		edits, base, created, resolvedAt, updated string
	)
	err := row.Scan(&s.ID, &s.SpaceID, &s.NoteID, &s.AuthorKind, &s.AuthorID, &s.Title, &s.Rationale,
		&edits, &base, &s.Status, &created, &s.ResolvedBy, &resolvedAt, &s.Version, &updated)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(edits), &s.Edits); err != nil {
		return nil, fmt.Errorf("suggestion %q edits: %w", s.ID, err)
	}
	s.BaseUpdatedAt = parseTime(base)
	s.CreatedAt = parseTime(created)
	s.ResolvedAt = parseTimePtr(resolvedAt)
	s.UpdatedAt = parseTime(updated)
	return &s, nil
}

func marshalEdits(edits []module.SuggestedEdit) (string, error) {
	if len(edits) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(edits)
	if err != nil {
		return "", fmt.Errorf("index: marshal suggested edits: %w", err)
	}
	return string(b), nil
}

func validSuggestionStatuses(statuses []string) error {
	for _, st := range statuses {
		if !slices.Contains(suggestionStatuses, st) {
			return core.Invalid("status", fmt.Sprintf("unknown suggestion status %q", st))
		}
	}
	return nil
}

// CreateSuggestion inserts a suggestion. An empty ID gets core.NewID, an
// empty status becomes open and a zero CreatedAt becomes now. Version is set
// to 1 and UpdatedAt to now. Content rules live in
// module.ValidateNewSuggestion, which the caller runs first.
func (d *DB) CreateSuggestion(ctx context.Context, s *StoredSuggestion, now time.Time) error {
	if s == nil {
		return fmt.Errorf("index: create suggestion: nil suggestion: %w", core.ErrInvalid)
	}
	if s.Status == "" {
		s.Status = module.SuggestionOpen
	}
	switch {
	case s.SpaceID == "":
		return core.Invalid("spaceId", "is required")
	case s.NoteID == "":
		return core.Invalid("noteId", "is required")
	case s.AuthorKind != module.AuthorUser && s.AuthorKind != module.AuthorPlugin && s.AuthorKind != module.AuthorAI:
		return core.Invalid("authorKind", fmt.Sprintf("unknown author kind %q", s.AuthorKind))
	}
	if err := validSuggestionStatuses([]string{s.Status}); err != nil {
		return err
	}
	edits, err := marshalEdits(s.Edits)
	if err != nil {
		return err
	}
	if s.ID == "" {
		s.ID = core.NewID()
	}
	now = now.UTC()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	s.CreatedAt = s.CreatedAt.UTC()
	s.UpdatedAt = now
	s.Version = 1
	_, err = d.sql.ExecContext(ctx, `INSERT INTO suggestions (`+suggestionColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.SpaceID, s.NoteID, s.AuthorKind, s.AuthorID, s.Title, s.Rationale, edits,
		fmtTime(s.BaseUpdatedAt), s.Status, fmtTime(s.CreatedAt), s.ResolvedBy,
		fmtTimePtr(s.ResolvedAt), s.Version, fmtTime(s.UpdatedAt))
	if err != nil {
		return writeErr("create suggestion", err)
	}
	return nil
}

// GetSuggestion reads one suggestion.
func (d *DB) GetSuggestion(ctx context.Context, id string) (*StoredSuggestion, error) {
	s, err := scanSuggestion(d.sql.QueryRowContext(ctx,
		`SELECT `+suggestionColumns+` FROM suggestions WHERE id = ?`, id))
	if err != nil {
		return nil, readErr("get suggestion "+id, err)
	}
	return s, nil
}

func (d *DB) readSuggestions(ctx context.Context, q string, args ...any) ([]*StoredSuggestion, error) {
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, readErr("list suggestions", err)
	}
	defer rows.Close()
	var out []*StoredSuggestion
	for rows.Next() {
		s, err := scanSuggestion(rows)
		if err != nil {
			return nil, readErr("list suggestions", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list suggestions", err)
	}
	return out, nil
}

// ListNoteSuggestions returns the suggestions of a note, oldest first. An
// empty statuses list matches every status.
func (d *DB) ListNoteSuggestions(ctx context.Context, noteID string, statuses []string) ([]*StoredSuggestion, error) {
	if err := validSuggestionStatuses(statuses); err != nil {
		return nil, err
	}
	q := `SELECT ` + suggestionColumns + ` FROM suggestions WHERE note_id = ?`
	args := []any{noteID}
	if len(statuses) > 0 {
		q += ` AND status IN (` + placeholders(len(statuses)) + `)`
		args = append(args, stringArgs(statuses)...)
	}
	return d.readSuggestions(ctx, q+` ORDER BY created_at, id`, args...)
}

// ListSuggestions returns the suggestions matching f, newest CreatedAt first,
// then larger ID first. The caller filters the result by access.
func (d *DB) ListSuggestions(ctx context.Context, f SuggestionFilter) ([]*StoredSuggestion, error) {
	if err := validSuggestionStatuses(f.Statuses); err != nil {
		return nil, err
	}
	var (
		where []string
		args  []any
	)
	if len(f.SpaceIDs) > 0 {
		where = append(where, `space_id IN (`+placeholders(len(f.SpaceIDs))+`)`)
		args = append(args, stringArgs(f.SpaceIDs)...)
	}
	for _, c := range []struct{ cond, v string }{
		{"note_id = ?", f.NoteID},
		{"author_kind = ?", f.AuthorKind},
		{"author_id = ?", f.AuthorID},
	} {
		if c.v != "" {
			where = append(where, c.cond)
			args = append(args, c.v)
		}
	}
	if len(f.Statuses) > 0 {
		where = append(where, `status IN (`+placeholders(len(f.Statuses))+`)`)
		args = append(args, stringArgs(f.Statuses)...)
	}
	if !f.BeforeCreatedAt.IsZero() {
		b := fmtTime(f.BeforeCreatedAt)
		where = append(where, `(created_at < ? OR (created_at = ? AND id < ?))`)
		args = append(args, b, b, f.BeforeID)
	}
	q := `SELECT ` + suggestionColumns + ` FROM suggestions`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	page, pageArgs := limitClause(f.Limit, 0)
	return d.readSuggestions(ctx, q+` ORDER BY created_at DESC, id DESC`+page, append(args, pageArgs...)...)
}

// UpdateSuggestion writes the edits, status and resolution of s when the
// stored version still equals s.Version. On success s.Version grows by one
// and s.UpdatedAt becomes now. A changed row returns core.ErrConflict and an
// unknown id returns core.ErrNotFound.
func (d *DB) UpdateSuggestion(ctx context.Context, s *StoredSuggestion, now time.Time) error {
	if s == nil {
		return fmt.Errorf("index: update suggestion: nil suggestion: %w", core.ErrInvalid)
	}
	if err := validSuggestionStatuses([]string{s.Status}); err != nil {
		return err
	}
	edits, err := marshalEdits(s.Edits)
	if err != nil {
		return err
	}
	now = now.UTC()
	res, err := d.sql.ExecContext(ctx, `UPDATE suggestions SET edits = ?, status = ?, resolved_by = ?,
		resolved_at = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`,
		edits, s.Status, s.ResolvedBy, fmtTimePtr(s.ResolvedAt), fmtTime(now), s.ID, s.Version)
	if err != nil {
		return writeErr("update suggestion", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("index: update suggestion: rows affected: %w", err)
	}
	if n == 0 {
		var stored int64
		err := d.sql.QueryRowContext(ctx, `SELECT version FROM suggestions WHERE id = ?`, s.ID).Scan(&stored)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("update suggestion " + s.ID)
		}
		if err != nil {
			return readErr("update suggestion", err)
		}
		return fmt.Errorf("index: suggestion %q is at version %d, not %d: %w", s.ID, stored, s.Version, core.ErrConflict)
	}
	s.Version++
	s.UpdatedAt = now
	return nil
}

// DeleteSuggestion removes one suggestion.
func (d *DB) DeleteSuggestion(ctx context.Context, id string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM suggestions WHERE id = ?`, id)
	return affected("delete suggestion", res, err)
}

// DeleteNoteSuggestions removes every suggestion of a note and returns the
// number removed. The app calls it when it deletes the note.
func (d *DB) DeleteNoteSuggestions(ctx context.Context, noteID string) (int, error) {
	return d.deleteSuggestionsWhere(ctx, "note_id", noteID)
}

// DeleteSpaceSuggestions removes every suggestion in a space and returns the
// number removed. The app calls it when it deletes the space.
func (d *DB) DeleteSpaceSuggestions(ctx context.Context, spaceID string) (int, error) {
	return d.deleteSuggestionsWhere(ctx, "space_id", spaceID)
}

func (d *DB) deleteSuggestionsWhere(ctx context.Context, column, value string) (int, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM suggestions WHERE `+column+` = ?`, value)
	if err != nil {
		return 0, writeErr("delete suggestions", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("index: delete suggestions: rows affected: %w", err)
	}
	return int(n), nil
}
