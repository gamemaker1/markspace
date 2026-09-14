package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// noteColumns is the SELECT list matching scanNote. Every query aliases the
// notes table as n.
const noteColumns = `n.id, n.space_id, n.parent_id, n.slug, n.path, n.title, n.icon,
	n.tags, n.excerpt, n.position, n.words, n.created_at, n.updated_at, n.created_by,
	n.updated_by, n.via, n.deleted, n.deleted_at, n.deleted_by, n.rev`

// queryer is satisfied by *sql.DB and *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// scanNote reads one notes row. Content stays empty, because note bodies live
// in the markdown files and not in the database.
func scanNote(row rowScanner) (*core.Note, error) {
	var (
		n                       core.Note
		tags                    string
		deleted                 int
		created, updated, delAt string
	)
	err := row.Scan(&n.ID, &n.SpaceID, &n.ParentID, &n.Slug, &n.Path, &n.Title, &n.Icon,
		&tags, &n.Excerpt, &n.Position, &n.Words, &created, &updated, &n.CreatedBy,
		&n.UpdatedBy, &n.UpdatedVia, &deleted, &delAt, &n.DeletedBy, &n.Rev)
	if err != nil {
		return nil, err
	}
	n.Tags = unmarshalStrings(tags)
	n.CreatedAt = parseTime(created)
	n.UpdatedAt = parseTime(updated)
	n.Deleted = intBool(deleted)
	n.DeletedAt = parseTimePtr(delAt)
	return &n, nil
}

// upsertNoteSQL writes the notes row. The note id is the conflict key, so a
// file that moved keeps its row and its relations.
const upsertNoteSQL = `INSERT INTO notes (
	id, space_id, parent_id, slug, path, title, icon, tags, excerpt, position, words,
	created_at, updated_at, created_by, updated_by, via, deleted, deleted_at, deleted_by,
	mod_time, size, rev
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	rev = excluded.rev,
	space_id = excluded.space_id,
	parent_id = excluded.parent_id,
	slug = excluded.slug,
	path = excluded.path,
	title = excluded.title,
	icon = excluded.icon,
	tags = excluded.tags,
	excerpt = excluded.excerpt,
	position = excluded.position,
	words = excluded.words,
	created_at = excluded.created_at,
	updated_at = excluded.updated_at,
	created_by = excluded.created_by,
	updated_by = excluded.updated_by,
	via = excluded.via,
	deleted = excluded.deleted,
	deleted_at = excluded.deleted_at,
	deleted_by = excluded.deleted_by,
	mod_time = excluded.mod_time,
	size = excluded.size`

// UpsertNote writes the note row, its tags, its outgoing links and its
// full-text entry in one transaction, keyed on note id. The full-text row is
// rebuilt from row.Plain, which the caller derives from the markdown body.
//
// Links resolve as described in resolveItemTarget and stay NULL when nothing
// matches. After the upsert, links anywhere in the space that pointed nowhere
// and now name this note are resolved, so a backlink appears as soon as its
// target is created. A live note whose path is held by a live folder or file
// returns core.ErrExists.
func (d *DB) UpsertNote(ctx context.Context, row ports.NoteRow) error {
	n := row.Note
	if n == nil {
		return fmt.Errorf("index: upsert note: nil note: %w", core.ErrInvalid)
	}
	if n.ID == "" {
		return fmt.Errorf("index: upsert note: %w", core.Invalid("id", "note id is required"))
	}
	if n.SpaceID == "" {
		return fmt.Errorf("index: upsert note: %w", core.Invalid("spaceId", "space id is required"))
	}

	return d.tx(ctx, func(t *sql.Tx) error {
		if !n.Deleted {
			if err := notePathFree(ctx, t, n.SpaceID, n.Path); err != nil {
				return err
			}
		}
		rev, err := nextRev(ctx, t)
		if err != nil {
			return err
		}
		_, err = t.ExecContext(ctx, upsertNoteSQL,
			n.ID, n.SpaceID, n.ParentID, n.Slug, n.Path, n.Title, n.Icon,
			marshalStrings(n.Tags), n.Excerpt, n.Position, n.Words,
			fmtTime(n.CreatedAt), fmtTime(n.UpdatedAt), n.CreatedBy, n.UpdatedBy, n.UpdatedVia,
			boolInt(n.Deleted), fmtTimePtr(n.DeletedAt), n.DeletedBy,
			fmtTime(row.ModTime), row.Size, rev)
		if err != nil {
			return writeErr("upsert note "+n.ID, err)
		}
		n.Rev = rev

		if err := replaceNoteTags(ctx, t, n); err != nil {
			return err
		}
		if err := replaceNoteLinks(ctx, t, n, row); err != nil {
			return err
		}
		if err := replaceNoteFTS(ctx, t, n, row.Plain); err != nil {
			return err
		}
		return resolveDanglingItem(ctx, t, n.ID)
	})
}

// notePathFree returns core.ErrExists when a live folder or file holds the
// path, because both would share one directory entry with the note file.
// Live notes are kept apart by the notes_live_path index.
func notePathFree(ctx context.Context, q dbtx, spaceID, relPath string) error {
	for _, table := range []string{"folders", "files"} {
		var taken int
		err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+`
			WHERE space_id = ? AND deleted = 0 AND path = ? COLLATE NOCASE`, spaceID, relPath).Scan(&taken)
		if err != nil {
			return readErr("note path", err)
		}
		if taken > 0 {
			return fmt.Errorf("index: note path %q: %w", relPath, core.ErrExists)
		}
	}
	return nil
}

// replaceNoteTags rewrites the tag index rows of one note.
func replaceNoteTags(ctx context.Context, t *sql.Tx, n *core.Note) error {
	if _, err := t.ExecContext(ctx, `DELETE FROM note_tags WHERE note_id = ?`, n.ID); err != nil {
		return writeErr("replace note tags", err)
	}
	for _, tag := range n.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		// OR IGNORE drops a tag list that repeats a tag in a different case,
		// because the primary key collates the tag column case-insensitively.
		_, err := t.ExecContext(ctx,
			`INSERT OR IGNORE INTO note_tags (note_id, space_id, tag) VALUES (?, ?, ?)`,
			n.ID, n.SpaceID, tag)
		if err != nil {
			return writeErr("replace note tags", err)
		}
	}
	return nil
}

// replaceNoteFTS rebuilds the full-text row of one note. The table holds its
// own content, so the row is deleted by note id and inserted again. An
// external-content table is not used, because the body is not in any table.
func replaceNoteFTS(ctx context.Context, t *sql.Tx, n *core.Note, plain string) error {
	if _, err := t.ExecContext(ctx, `DELETE FROM notes_fts WHERE note_id = ?`, n.ID); err != nil {
		return writeErr("replace note text", err)
	}
	_, err := t.ExecContext(ctx,
		`INSERT INTO notes_fts (title, body, tags, note_id, space_id) VALUES (?, ?, ?, ?, ?)`,
		n.Title, plain, strings.Join(n.Tags, " "), n.ID, n.SpaceID)
	if err != nil {
		return writeErr("replace note text", err)
	}
	return nil
}

// normalizeTarget cleans a link target for storage and matching. It trims
// space, drops a fragment, drops a leading "./" or "/", drops a trailing "/"
// and drops a ".md" extension. Case is kept, because the stored value is what
// the interface shows for an unresolved link.
func normalizeTarget(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, "/")
	s = pathWithoutExt(s)
	return strings.TrimSpace(s)
}

// pathWithoutExt drops a trailing ".md" or ".markdown", whatever its case.
func pathWithoutExt(s string) string {
	for _, ext := range []string{".md", ".markdown"} {
		if len(s) > len(ext) && strings.EqualFold(s[len(s)-len(ext):], ext) {
			return s[:len(s)-len(ext)]
		}
	}
	return s
}

// resolveTargetSQL matches a link target against one space. Title wins over
// slug, and slug wins over path.
const resolveTargetSQL = `SELECT n.id FROM notes n
	WHERE n.space_id = ? AND n.deleted = 0
	AND (lower(n.title) = ? OR lower(n.slug) = ? OR lower(n.path) = ? OR lower(n.path) = ?)
	ORDER BY CASE
		WHEN lower(n.title) = ? THEN 0
		WHEN lower(n.slug) = ? THEN 1
		ELSE 2
	END, n.updated_at DESC
	LIMIT 1`

// resolveTargetID returns the id of the note a target names, or the empty
// string when nothing in the space matches.
func resolveTargetID(ctx context.Context, q queryer, spaceID, target string) (string, error) {
	key := strings.ToLower(normalizeTarget(target))
	if key == "" {
		return "", nil
	}
	var id string
	err := q.QueryRowContext(ctx, resolveTargetSQL,
		spaceID, key, key, key, key+".md", key, key).Scan(&id)
	switch {
	case err == nil:
		return id, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	default:
		return "", readErr("resolve link target", err)
	}
}

// GetNote reads one note by id.
func (d *DB) GetNote(ctx context.Context, id string) (*core.Note, error) {
	row := d.sql.QueryRowContext(ctx, `SELECT `+noteColumns+` FROM notes n WHERE n.id = ?`, id)
	n, err := scanNote(row)
	if err != nil {
		return nil, readErr("get note "+id, err)
	}
	return n, nil
}

// GetNoteByPath reads one note by its path inside a space. The path is the
// stored relative path with its .md extension. A live note wins over a
// trashed one that kept the same path.
func (d *DB) GetNoteByPath(ctx context.Context, spaceID, relPath string) (*core.Note, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+noteColumns+` FROM notes n WHERE n.space_id = ? AND n.path = ?
			ORDER BY n.deleted, n.deleted_at DESC LIMIT 1`,
		spaceID, relPath)
	n, err := scanNote(row)
	if err != nil {
		return nil, readErr("get note by path "+relPath, err)
	}
	return n, nil
}

// ListNotes lists the notes of one space. Content is always empty, because
// note bodies are not stored here. opts.WithContent is accepted and ignored,
// so the app layer can load bodies from disk for the rows it needs.
func (d *DB) ListNotes(ctx context.Context, spaceID string, opts core.NoteListOptions) ([]*core.Note, error) {
	where := []string{`n.space_id = ?`}
	args := []any{spaceID}

	switch {
	case opts.OnlyTrash:
		where = append(where, `n.deleted = 1`)
	case !opts.IncludeTrash:
		where = append(where, `n.deleted = 0`)
	}

	if opts.HasParent || opts.ParentID != "" {
		if opts.ParentID == "" {
			where = append(where, `(n.parent_id IS NULL OR n.parent_id = '')`)
		} else {
			where = append(where, `n.parent_id = ?`)
			args = append(args, opts.ParentID)
		}
	}

	if tag := strings.TrimSpace(opts.Tag); tag != "" {
		where = append(where, `EXISTS (SELECT 1 FROM note_tags t WHERE t.note_id = n.id AND t.tag = ?)`)
		args = append(args, tag)
	}

	page, pageArgs := limitClause(opts.Limit, opts.Offset)
	q := `SELECT ` + noteColumns + ` FROM notes n WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY ` + noteOrderBy(opts.Sort) + page
	rows, err := d.sql.QueryContext(ctx, q, append(args, pageArgs...)...)
	if err != nil {
		return nil, readErr("list notes", err)
	}
	defer rows.Close()
	return collectNotes(rows, "list notes")
}

// noteOrderBy maps a sort name to an ORDER BY clause. An unknown name sorts
// by position and then title, which is the sidebar order.
func noteOrderBy(sort string) string {
	switch sort {
	case "title":
		return `n.title COLLATE NOCASE, n.path`
	case "updated":
		return `n.updated_at DESC, n.title COLLATE NOCASE`
	case "created":
		return `n.created_at DESC, n.title COLLATE NOCASE`
	default:
		return `n.position, n.title COLLATE NOCASE`
	}
}

// collectNotes drains a note query.
func collectNotes(rows *sql.Rows, what string) ([]*core.Note, error) {
	var out []*core.Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, readErr(what, err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr(what, err)
	}
	return out, nil
}

// ListNotesByIDs returns the notes that exist, in the order of the ids given.
// Trashed notes are included, because a caller that holds an id wants the
// row. Unknown ids are skipped rather than reported.
func (d *DB) ListNotesByIDs(ctx context.Context, ids []string) ([]*core.Note, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := `SELECT ` + noteColumns + ` FROM notes n WHERE n.id IN (` + placeholders(len(ids)) + `)`
	rows, err := d.sql.QueryContext(ctx, q, stringArgs(ids)...)
	if err != nil {
		return nil, readErr("list notes by ids", err)
	}
	defer rows.Close()

	found, err := collectNotes(rows, "list notes by ids")
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*core.Note, len(found))
	for _, n := range found {
		byID[n.ID] = n
	}
	out := make([]*core.Note, 0, len(found))
	for _, id := range ids {
		if n, ok := byID[id]; ok {
			out = append(out, n)
			delete(byID, id)
		}
	}
	return out, nil
}

// DeleteNoteRow removes the index rows of one note: the note itself, its
// tags, its outgoing links, its full-text entry and its embeddings. Inbound
// links become unresolved again rather than disappearing, so a backlink
// returns when the note comes back. Comments, shares, public links, stars and
// attachments are kept, because they are app state that must survive a
// reindex. The call is idempotent and reports no error for an unknown id.
func (d *DB) DeleteNoteRow(ctx context.Context, id string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		return deleteNoteRows(ctx, t, []string{id})
	})
}

// DeleteNoteRowsUnder removes the index rows of every note at pathPrefix or
// below it. The prefix matches the note file itself, the same path with a .md
// extension and anything under the prefix directory. An empty prefix matches
// the whole space.
func (d *DB) DeleteNoteRowsUnder(ctx context.Context, spaceID, pathPrefix string) error {
	prefix := strings.TrimSuffix(strings.TrimSpace(pathPrefix), "/")
	var (
		q    string
		args []any
	)
	if prefix == "" {
		q = `SELECT n.id FROM notes n WHERE n.space_id = ?`
		args = []any{spaceID}
	} else {
		q = `SELECT n.id FROM notes n WHERE n.space_id = ?
			AND (n.path = ? OR n.path = ? OR n.path LIKE ? ESCAPE '\')`
		args = []any{spaceID, prefix, prefix + ".md", likeEscape(prefix) + "/%"}
	}

	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return readErr("delete note rows under "+pathPrefix, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return readErr("delete note rows under "+pathPrefix, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return readErr("delete note rows under "+pathPrefix, err)
	}
	rows.Close()

	if len(ids) == 0 {
		return nil
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		return deleteNoteRows(ctx, t, ids)
	})
}

// deleteNoteRows removes the index rows of the given note ids.
func deleteNoteRows(ctx context.Context, t *sql.Tx, ids []string) error {
	list := `(` + placeholders(len(ids)) + `)`
	args := stringArgs(ids)
	for _, q := range []string{
		`DELETE FROM note_tags WHERE note_id IN ` + list,
		`DELETE FROM note_links WHERE note_id IN ` + list,
		`DELETE FROM notes_fts WHERE note_id IN ` + list,
		`DELETE FROM embeddings WHERE note_id IN ` + list,
		`UPDATE note_links SET target_note_id = NULL, target_kind = '' WHERE target_note_id IN ` + list,
		`DELETE FROM notes WHERE id IN ` + list,
	} {
		if _, err := t.ExecContext(ctx, q, args...); err != nil {
			return writeErr("delete note rows", err)
		}
	}
	return nil
}

// SetNoteDeleted flags a note as trashed or restores it. The full-text row
// stays in place, because search joins the notes table and filters on the
// flag, which keeps trash searchable when a query asks for it.
func (d *DB) SetNoteDeleted(ctx context.Context, id string, deleted bool, at *time.Time, by string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		if !deleted {
			var spaceID, relPath string
			err := t.QueryRowContext(ctx, `SELECT space_id, path FROM notes WHERE id = ?`, id).Scan(&spaceID, &relPath)
			if err != nil {
				return readErr("set note deleted", err)
			}
			if err := notePathFree(ctx, t, spaceID, relPath); err != nil {
				return err
			}
		}
		rev, err := nextRev(ctx, t)
		if err != nil {
			return err
		}
		res, err := t.ExecContext(ctx,
			`UPDATE notes SET deleted = ?, deleted_at = ?, deleted_by = ?, rev = ? WHERE id = ?`,
			boolInt(deleted), fmtTimePtr(at), by, rev, id)
		return affected("set note deleted", res, err)
	})
}

// SetNotePath records a move. It writes the new path, slug and parent id and
// leaves every other field alone.
func (d *DB) SetNotePath(ctx context.Context, id, relPath, slug, parentID string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		var (
			spaceID string
			deleted int
		)
		err := t.QueryRowContext(ctx, `SELECT space_id, deleted FROM notes WHERE id = ?`, id).Scan(&spaceID, &deleted)
		if err != nil {
			return readErr("set note path", err)
		}
		if deleted == 0 {
			if err := notePathFree(ctx, t, spaceID, relPath); err != nil {
				return err
			}
		}
		rev, err := nextRev(ctx, t)
		if err != nil {
			return err
		}
		res, err := t.ExecContext(ctx,
			`UPDATE notes SET path = ?, slug = ?, parent_id = ?, rev = ? WHERE id = ?`,
			relPath, slug, parentID, rev, id)
		return affected("set note path", res, err)
	})
}

// IndexState maps the relative path of every indexed note in one space to its
// recorded file state. Trashed notes are included, because the caller
// compares the map against what is on disk.
func (d *DB) IndexState(ctx context.Context, spaceID string) (map[string]ports.IndexState, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT n.path, n.id, n.mod_time, n.size FROM notes n WHERE n.space_id = ?`, spaceID)
	if err != nil {
		return nil, readErr("index state", err)
	}
	defer rows.Close()

	out := map[string]ports.IndexState{}
	for rows.Next() {
		var (
			path, id, modTime string
			size              int64
		)
		if err := rows.Scan(&path, &id, &modTime, &size); err != nil {
			return nil, readErr("index state", err)
		}
		out[path] = ports.IndexState{NoteID: id, ModTime: parseTime(modTime), Size: size}
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("index state", err)
	}
	return out, nil
}

// CountNotes returns the number of live notes, trash excluded.
func (d *DB) CountNotes(ctx context.Context) (int, error) {
	return d.count(ctx, `SELECT COUNT(*) FROM notes WHERE deleted = 0`, "count notes")
}

// NoteIDsInSpace lists the ids of live notes in one space, ordered by path.
func (d *DB) NoteIDsInSpace(ctx context.Context, spaceID string) ([]string, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT n.id FROM notes n WHERE n.space_id = ? AND n.deleted = 0 ORDER BY n.path`, spaceID)
	if err != nil {
		return nil, readErr("note ids in space", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, readErr("note ids in space", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("note ids in space", err)
	}
	return out, nil
}

// Backlinks returns the live notes that link to this note, newest first. A
// note that links to the target more than once appears once. Context is left
// empty, because the surrounding text is in the markdown file and the caller
// reads bodies from disk.
func (d *DB) Backlinks(ctx context.Context, noteID string) ([]*core.Backlink, error) {
	const q = `SELECT ` + noteColumns + ` FROM notes n
		WHERE n.deleted = 0 AND n.id IN (
			SELECT l.note_id FROM note_links l WHERE l.target_note_id = ?
		)
		ORDER BY n.updated_at DESC, n.title COLLATE NOCASE`
	rows, err := d.sql.QueryContext(ctx, q, noteID)
	if err != nil {
		return nil, readErr("backlinks", err)
	}
	defer rows.Close()

	notes, err := collectNotes(rows, "backlinks")
	if err != nil {
		return nil, err
	}
	out := make([]*core.Backlink, 0, len(notes))
	for _, n := range notes {
		out = append(out, &core.Backlink{FromNote: n})
	}
	return out, nil
}

// ResolveLink matches a wikilink target against note titles, slugs and
// extension-free paths in one space, in that order of preference. It returns
// core.ErrNotFound when nothing matches.
func (d *DB) ResolveLink(ctx context.Context, spaceID, target string) (*core.Note, error) {
	id, err := resolveTargetID(ctx, d.sql, spaceID, target)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, fmt.Errorf("index: resolve link %q: %w", target, core.ErrNotFound)
	}
	return d.GetNote(ctx, id)
}

// ListTags counts the tags of live notes across the given spaces, most used
// first. An empty space list returns no tags.
func (d *DB) ListTags(ctx context.Context, spaceIDs []string) ([]*core.TagCount, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	q := `SELECT t.tag, COUNT(DISTINCT t.note_id) AS n
		FROM note_tags t
		JOIN notes n ON n.id = t.note_id
		WHERE n.deleted = 0 AND t.space_id IN (` + placeholders(len(spaceIDs)) + `)
		GROUP BY t.tag
		ORDER BY n DESC, t.tag COLLATE NOCASE`
	rows, err := d.sql.QueryContext(ctx, q, stringArgs(spaceIDs)...)
	if err != nil {
		return nil, readErr("list tags", err)
	}
	defer rows.Close()

	var out []*core.TagCount
	for rows.Next() {
		var tc core.TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return nil, readErr("list tags", err)
		}
		out = append(out, &tc)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list tags", err)
	}
	return out, nil
}
