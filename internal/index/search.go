package index

import (
	"context"
	"database/sql"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// Search result page sizes used when a query asks for no limit.
const (
	// DefaultSearchLimit caps a search that gives no limit.
	DefaultSearchLimit = 50
	// DefaultSuggestLimit caps a suggestion query that gives no limit.
	DefaultSuggestLimit = 10
	// DefaultRecentLimit caps a recent-notes query that gives no limit.
	DefaultRecentLimit = 20
)

// ftsOperatorChars are the FTS5 expression characters dropped from a term.
// Everything else stays, because a term is wrapped in double quotes and FTS5
// treats any other non-token character inside a phrase as a separator.
const ftsOperatorChars = `*^:()[]{}`

// extraScanner appends destinations to a Scan call, so a row scanner written
// for a fixed column list can serve a query that selects extra columns after
// it.
type extraScanner struct {
	row   rowScanner
	extra []any
}

// Scan forwards dest with the extra destinations appended.
func (e extraScanner) Scan(dest ...any) error {
	return e.row.Scan(append(dest, e.extra...)...)
}

// ftsStopwords are English function words dropped from an any-term query.
// In that mode each one would match almost every note.
var ftsStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "but": true, "by": true, "can": true, "do": true, "does": true,
	"for": true, "from": true, "how": true, "i": true, "if": true, "in": true,
	"into": true, "is": true, "it": true, "me": true, "my": true, "not": true,
	"of": true, "on": true, "or": true, "should": true, "so": true, "that": true,
	"the": true, "their": true, "then": true, "there": true, "this": true,
	"to": true, "was": true, "we": true, "what": true, "when": true,
	"where": true, "which": true, "who": true, "why": true, "will": true,
	"with": true, "you": true, "your": true,
}

// ftsTerms turns user text into quoted FTS5 terms. Each whitespace-separated
// term loses the FTS operator characters, has its internal double quotes
// doubled and is wrapped in double quotes, so no input can be read as an
// operator. The last term gets a * for prefix matching, which is what a
// search-as-you-type box wants. With dropStopwords set, terms in ftsStopwords
// are removed, unless that would remove every term.
func ftsTerms(text string, dropStopwords bool) []string {
	fields := strings.Fields(text)
	if dropStopwords {
		kept := make([]string, 0, len(fields))
		for _, f := range fields {
			if !ftsStopwords[strings.ToLower(strings.Trim(f, ftsOperatorChars+`"?!.,;`))] {
				kept = append(kept, f)
			}
		}
		if len(kept) > 0 {
			fields = kept
		}
	}
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		term := strings.Map(func(r rune) rune {
			// FTS5 reads the expression as a C string, so a NUL ends it inside
			// the open quote and MATCH fails with "unterminated string". Inside
			// a phrase a space separates tokens just as the NUL did.
			if r == 0 {
				return ' '
			}
			if strings.ContainsRune(ftsOperatorChars, r) {
				return -1
			}
			return r
		}, field)
		term = strings.TrimSpace(term)
		if term == "" || strings.Trim(term, `"`) == "" {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	if len(terms) > 0 {
		terms[len(terms)-1] += "*"
	}
	return terms
}

// ftsMatchQuery returns an FTS5 expression matching notes that contain every
// term. Text with no usable term returns the empty string.
func ftsMatchQuery(text string) string {
	return strings.Join(ftsTerms(text, false), " ")
}

// ftsAnyQuery returns an FTS5 expression matching notes that contain at
// least one term other than a stopword. bm25 still ranks notes with more
// matching terms higher.
func ftsAnyQuery(text string) string {
	return strings.Join(ftsTerms(text, true), " OR ")
}

// SearchNotes runs a full-text search over the given spaces. spaceIDs is the
// set of spaces the actor may read and always applies; q.SpaceIDs narrows it
// further when it is set. An empty spaceIDs returns no hits rather than
// searching everything.
//
// Empty q.Text lists notes by update time with an empty snippet. Otherwise a
// note must contain every term, and when that finds nothing on the first page
// a note may contain any term. Hits rank by bm25, Score is the negated bm25 value so that higher is
// better, and Snippet holds the body extract with <mark> around matches.
// Several tags narrow the result to notes carrying all of them. q.AuthorID
// matches the note author, which is the created_by field. Note bodies are not
// stored here, so Note.Content stays empty.
func (d *DB) SearchNotes(ctx context.Context, spaceIDs []string, q core.SearchQuery) ([]*core.SearchHit, error) {
	spaces := intersectIDs(spaceIDs, q.SpaceIDs)
	if len(spaces) == 0 {
		return nil, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	match := ftsMatchQuery(q.Text)
	if match == "" {
		return d.searchByRecency(ctx, spaces, q, limit)
	}
	hits, err := d.searchMatch(ctx, match, spaces, q, limit)
	if err != nil {
		return nil, err
	}
	// A question typed in natural language rarely has every word in one note.
	// When requiring every term finds nothing, accept any term instead.
	if len(hits) == 0 && q.Offset == 0 && len(ftsTerms(q.Text, false)) > 1 {
		return d.searchMatch(ctx, ftsAnyQuery(q.Text), spaces, q, limit)
	}
	return hits, nil
}

// searchMatch runs one FTS5 MATCH expression with the search filters applied.
func (d *DB) searchMatch(ctx context.Context, match string, spaces []string, q core.SearchQuery, limit int) ([]*core.SearchHit, error) {
	where := []string{`notes_fts MATCH ?`, `n.space_id IN (` + placeholders(len(spaces)) + `)`}
	args := []any{match}
	args = append(args, stringArgs(spaces)...)
	where, args = appendSearchFilters(where, args, q)

	page, pageArgs := limitClause(limit, q.Offset)
	query := `SELECT ` + noteColumns + `, bm25(notes_fts),
		snippet(notes_fts, 1, '<mark>', '</mark>', '…', 12)
		FROM notes_fts
		JOIN notes n ON n.id = notes_fts.note_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY bm25(notes_fts), n.updated_at DESC` + page

	rows, err := d.sql.QueryContext(ctx, query, append(args, pageArgs...)...)
	if err != nil {
		return nil, readErr("search notes", err)
	}
	defer rows.Close()

	var out []*core.SearchHit
	for rows.Next() {
		var (
			rank    float64
			snippet string
		)
		n, err := scanNote(extraScanner{row: rows, extra: []any{&rank, &snippet}})
		if err != nil {
			return nil, readErr("search notes", err)
		}
		out = append(out, &core.SearchHit{Note: n, Snippet: snippet, Score: -rank})
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("search notes", err)
	}
	return out, nil
}

// searchByRecency answers a search with no usable term by listing notes from
// newest to oldest.
func (d *DB) searchByRecency(ctx context.Context, spaces []string, q core.SearchQuery, limit int) ([]*core.SearchHit, error) {
	where := []string{`n.space_id IN (` + placeholders(len(spaces)) + `)`}
	args := stringArgs(spaces)
	where, args = appendSearchFilters(where, args, q)

	page, pageArgs := limitClause(limit, q.Offset)
	query := `SELECT ` + noteColumns + ` FROM notes n WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY n.updated_at DESC, n.title COLLATE NOCASE` + page

	rows, err := d.sql.QueryContext(ctx, query, append(args, pageArgs...)...)
	if err != nil {
		return nil, readErr("search notes", err)
	}
	defer rows.Close()

	notes, err := collectNotes(rows, "search notes")
	if err != nil {
		return nil, err
	}
	out := make([]*core.SearchHit, 0, len(notes))
	for _, n := range notes {
		out = append(out, &core.SearchHit{Note: n})
	}
	return out, nil
}

// appendSearchFilters adds the trash, tag and author conditions shared by
// both search paths.
func appendSearchFilters(where []string, args []any, q core.SearchQuery) ([]string, []any) {
	if !q.IncludeTrash {
		where = append(where, `n.deleted = 0`)
	}
	for _, tag := range q.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		where = append(where, `EXISTS (SELECT 1 FROM note_tags t WHERE t.note_id = n.id AND t.tag = ?)`)
		args = append(args, tag)
	}
	if author := strings.TrimSpace(q.AuthorID); author != "" {
		where = append(where, `n.created_by = ?`)
		args = append(args, author)
	}
	return where, args
}

// intersectIDs returns allowed when narrow is empty, and otherwise the
// members of allowed that narrow also names.
func intersectIDs(allowed, narrow []string) []string {
	if len(allowed) == 0 || len(narrow) == 0 {
		return allowed
	}
	want := make(map[string]bool, len(narrow))
	for _, id := range narrow {
		want[id] = true
	}
	out := make([]string, 0, len(allowed))
	for _, id := range allowed {
		if want[id] {
			out = append(out, id)
		}
	}
	return out
}

// SuggestNotes finds notes for a title picker. A title that starts with the
// prefix comes first, then a title that contains it, then a slug that
// contains it. Within a group the most recently updated note comes first.
// Matching is case-insensitive and trash is excluded.
func (d *DB) SuggestNotes(ctx context.Context, spaceIDs []string, prefix string, limit int) ([]*core.Note, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = DefaultSuggestLimit
	}
	p := likeEscape(strings.ToLower(strings.TrimSpace(prefix)))
	starts := p + "%"
	contains := "%" + p + "%"

	query := `SELECT ` + noteColumns + ` FROM notes n
		WHERE n.deleted = 0 AND n.space_id IN (` + placeholders(len(spaceIDs)) + `)
		AND (lower(n.title) LIKE ? ESCAPE '\' OR lower(n.slug) LIKE ? ESCAPE '\')
		ORDER BY CASE
			WHEN lower(n.title) LIKE ? ESCAPE '\' THEN 0
			WHEN lower(n.title) LIKE ? ESCAPE '\' THEN 1
			ELSE 2
		END, n.updated_at DESC, n.title COLLATE NOCASE
		LIMIT ?`
	args := stringArgs(spaceIDs)
	args = append(args, contains, contains, starts, contains, limit)

	rows, err := d.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, readErr("suggest notes", err)
	}
	defer rows.Close()
	return collectNotes(rows, "suggest notes")
}

// RecentNotes lists the most recently updated live notes across the given
// spaces.
func (d *DB) RecentNotes(ctx context.Context, spaceIDs []string, limit int) ([]*core.Note, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = DefaultRecentLimit
	}
	query := `SELECT ` + noteColumns + ` FROM notes n
		WHERE n.deleted = 0 AND n.space_id IN (` + placeholders(len(spaceIDs)) + `)
		ORDER BY n.updated_at DESC, n.title COLLATE NOCASE
		LIMIT ?`
	args := append(stringArgs(spaceIDs), limit)

	rows, err := d.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, readErr("recent notes", err)
	}
	defer rows.Close()
	return collectNotes(rows, "recent notes")
}

// compile-time check that *sql.Rows satisfies the row scanner the extra
// scanner wraps.
var _ rowScanner = (*sql.Rows)(nil)
