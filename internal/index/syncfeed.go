package index

import (
	"context"
	"database/sql"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// nextRev advances the note revision counter inside a transaction and returns
// the new value. A rolled-back transaction leaves the counter unchanged.
func nextRev(ctx context.Context, t *sql.Tx) (int64, error) {
	var rev int64
	err := t.QueryRowContext(ctx,
		`UPDATE counters SET value = value + 1 WHERE name = 'note_rev' RETURNING value`).Scan(&rev)
	if err != nil {
		return 0, writeErr("next note revision", err)
	}
	return rev, nil
}

// MaxNoteRev returns the highest note revision assigned so far. A deleted
// note keeps its revision number used, so the value never decreases.
func (d *DB) MaxNoteRev(ctx context.Context) (int64, error) {
	var v int64
	err := d.sql.QueryRowContext(ctx, `SELECT value FROM counters WHERE name = 'note_rev'`).Scan(&v)
	if err != nil {
		return 0, readErr("max note rev", err)
	}
	return v, nil
}

// NotesChangedSince returns notes that are in spaceIDs or have an id in
// noteIDs, with a revision in (since, upTo], ordered by revision, at most
// limit. Trashed notes are included. Content stays empty.
func (d *DB) NotesChangedSince(ctx context.Context, spaceIDs, noteIDs []string, since, upTo int64, limit int) ([]*core.Note, error) {
	if len(spaceIDs) == 0 && len(noteIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	args := []any{since, upTo}
	var scope []string
	if len(spaceIDs) > 0 {
		scope = append(scope, `n.space_id IN (`+placeholders(len(spaceIDs))+`)`)
		args = append(args, stringArgs(spaceIDs)...)
	}
	if len(noteIDs) > 0 {
		scope = append(scope, `n.id IN (`+placeholders(len(noteIDs))+`)`)
		args = append(args, stringArgs(noteIDs)...)
	}
	args = append(args, limit)
	q := `SELECT ` + noteColumns + ` FROM notes n
		WHERE n.rev > ? AND n.rev <= ? AND (` + strings.Join(scope, " OR ") + `)
		ORDER BY n.rev LIMIT ?`
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, readErr("notes changed since", err)
	}
	defer rows.Close()
	var out []*core.Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, readErr("notes changed since", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("notes changed since", err)
	}
	return out, nil
}

// NoteIDsInSpaces lists every note id in the spaces, trashed notes included,
// sorted by id.
func (d *DB) NoteIDsInSpaces(ctx context.Context, spaceIDs []string) ([]string, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	rows, err := d.sql.QueryContext(ctx,
		`SELECT id FROM notes WHERE space_id IN (`+placeholders(len(spaceIDs))+`) ORDER BY id`,
		stringArgs(spaceIDs)...)
	if err != nil {
		return nil, readErr("note ids in spaces", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, readErr("note ids in spaces", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("note ids in spaces", err)
	}
	return out, nil
}
