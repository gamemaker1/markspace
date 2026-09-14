package index

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// Snippet markers. FTS5 inserts them around matches, and the snippet is
// escaped for HTML before they become <mark> tags, so stored text can never
// produce markup. Both are private-use code points, removed from stored text.
const (
	markOpen  = "\uE000"
	markClose = "\uE001"
)

// ftsText removes the snippet markers from text bound for files_fts.
func ftsText(s string) string {
	if !strings.ContainsAny(s, markOpen+markClose) {
		return s
	}
	return strings.NewReplacer(markOpen, " ", markClose, " ").Replace(s)
}

// markSnippet escapes an FTS5 snippet for HTML and turns the markers into
// <mark> tags.
func markSnippet(s string) string {
	s = html.EscapeString(s)
	return strings.NewReplacer(markOpen, "<mark>", markClose, "</mark>").Replace(s)
}

// validTextState checks a file text state.
func validTextState(s ports.FileTextState) bool {
	switch s {
	case ports.TextNone, ports.TextPending, ports.TextDone, ports.TextFailed, ports.TextSkipped:
		return true
	}
	return false
}

// SetFileText stores the extracted text of a file and its extraction state.
// It takes no revision, because neither is part of the file metadata.
func (d *DB) SetFileText(ctx context.Context, id, text string, state ports.FileTextState) error {
	if !validTextState(state) {
		return fmt.Errorf("index: set file text: %w", core.Invalid("state", "unknown text state"))
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		f, err := getFileQ(ctx, t, id)
		if err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `UPDATE files SET text_state = ? WHERE id = ?`, string(state), id); err != nil {
			return writeErr("set file text", err)
		}
		if _, err := t.ExecContext(ctx, `DELETE FROM files_fts WHERE file_id = ?`, id); err != nil {
			return writeErr("set file text", err)
		}
		_, err = t.ExecContext(ctx, `INSERT INTO files_fts (name, body, file_id, space_id) VALUES (?, ?, ?, ?)`,
			ftsText(f.Name), ftsText(text), f.ID, f.SpaceID)
		if err != nil {
			return writeErr("set file text", err)
		}
		return nil
	})
}

// SearchFiles runs a full-text search over file names and extracted text in
// the given spaces, with the query rules of SearchNotes: every term first,
// any term other than a stopword when that finds nothing on the first page,
// and a listing by update time for text with no usable term. q.AuthorID
// matches the creator. Files carry no tags, so a query with tags returns no
// files. Snippet is escaped HTML with <mark> around matches, taken from
// whichever column matched best.
func (d *DB) SearchFiles(ctx context.Context, spaceIDs []string, q core.SearchQuery) ([]*ports.FileHit, error) {
	spaces := intersectIDs(spaceIDs, q.SpaceIDs)
	if len(spaces) == 0 {
		return nil, nil
	}
	for _, tag := range q.Tags {
		if strings.TrimSpace(tag) != "" {
			return nil, nil
		}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	match := ftsMatchQuery(q.Text)
	if match == "" {
		return d.searchFilesMatch(ctx, "", spaces, q, limit)
	}
	hits, err := d.searchFilesMatch(ctx, match, spaces, q, limit)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 && q.Offset == 0 && len(ftsTerms(q.Text, false)) > 1 {
		return d.searchFilesMatch(ctx, ftsAnyQuery(q.Text), spaces, q, limit)
	}
	return hits, nil
}

// searchFilesMatch runs one FTS5 MATCH expression, or lists by update time
// when match is empty.
func (d *DB) searchFilesMatch(ctx context.Context, match string, spaces []string, q core.SearchQuery, limit int) ([]*ports.FileHit, error) {
	where := []string{`fi.space_id IN (` + placeholders(len(spaces)) + `)`}
	args := stringArgs(spaces)
	if !q.IncludeTrash {
		where = append(where, `fi.deleted = 0`)
	}
	if author := strings.TrimSpace(q.AuthorID); author != "" {
		where = append(where, `fi.created_by = ?`)
		args = append(args, author)
	}
	page, pageArgs := limitClause(limit, q.Offset)
	var query string
	if match == "" {
		query = `SELECT ` + fileColumns + `, 0.0, '' FROM files fi WHERE ` + strings.Join(where, " AND ") +
			` ORDER BY fi.updated_at DESC, fi.name COLLATE NOCASE` + page
	} else {
		where = append([]string{`files_fts MATCH ?`}, where...)
		args = append([]any{match}, args...)
		query = `SELECT ` + fileColumns + `, bm25(files_fts),
			snippet(files_fts, -1, '` + markOpen + `', '` + markClose + `', '…', 12)
			FROM files_fts
			JOIN files fi ON fi.id = files_fts.file_id
			WHERE ` + strings.Join(where, " AND ") + `
			ORDER BY bm25(files_fts), fi.updated_at DESC` + page
	}
	rows, err := d.sql.QueryContext(ctx, query, append(args, pageArgs...)...)
	if err != nil {
		return nil, readErr("search files", err)
	}
	defer rows.Close()
	var out []*ports.FileHit
	for rows.Next() {
		var (
			rank    float64
			snippet string
		)
		f, err := scanFile(extraScanner{row: rows, extra: []any{&rank, &snippet}})
		if err != nil {
			return nil, readErr("search files", err)
		}
		out = append(out, &ports.FileHit{File: f, Snippet: markSnippet(snippet), Score: -rank})
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("search files", err)
	}
	return out, nil
}
