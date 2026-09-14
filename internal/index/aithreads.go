package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// aiThreadColumns is the column list matching scanAIThread.
const aiThreadColumns = `id, user_id, note_id, space_id, title, created_at, updated_at`

// aiMessageColumns is the column list matching scanAIMessage.
const aiMessageColumns = `id, thread_id, role, content, context, sources, applied, created_at`

func scanAIThread(row rowScanner) (*core.AIThread, error) {
	var (
		t                core.AIThread
		created, updated string
	)
	if err := row.Scan(&t.ID, &t.UserID, &t.NoteID, &t.SpaceID, &t.Title, &created, &updated); err != nil {
		return nil, err
	}
	t.CreatedAt, t.UpdatedAt = parseTime(created), parseTime(updated)
	return &t, nil
}

func scanAIMessage(row rowScanner) (*core.AIThreadMessage, error) {
	var (
		m                                 core.AIThreadMessage
		contextJSON, sourcesJSON, created string
		applied                           int
	)
	if err := row.Scan(&m.ID, &m.ThreadID, &m.Role, &m.Content, &contextJSON, &sourcesJSON, &applied, &created); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(contextJSON), &m.Context); err != nil {
		return nil, fmt.Errorf("ai message %q context: %w", m.ID, err)
	}
	if err := json.Unmarshal([]byte(sourcesJSON), &m.Sources); err != nil {
		return nil, fmt.Errorf("ai message %q sources: %w", m.ID, err)
	}
	if m.Context == nil {
		m.Context = []core.AIContextRef{}
	}
	if m.Sources == nil {
		m.Sources = []core.AISource{}
	}
	m.Applied = intBool(applied)
	m.CreatedAt = parseTime(created)
	return &m, nil
}

// marshalJSONArray renders a slice as JSON, with nil as an empty array.
func marshalJSONArray[T any](v []T) (string, error) {
	if len(v) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CreateAIThread stores a new thread. An empty ID gets a new identifier and
// zero times get now.
func (d *DB) CreateAIThread(ctx context.Context, t *core.AIThread) error {
	if t == nil || t.UserID == "" {
		return fmt.Errorf("index: create ai thread: user is required: %w", core.ErrInvalid)
	}
	if t.ID == "" {
		t.ID = core.NewID()
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}
	_, err := d.sql.ExecContext(ctx, `INSERT INTO ai_threads (`+aiThreadColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, t.NoteID, t.SpaceID, t.Title, fmtTime(t.CreatedAt), fmtTime(t.UpdatedAt))
	if err != nil {
		return writeErr("create ai thread", err)
	}
	return nil
}

// GetAIThread returns one thread without its messages, or core.ErrNotFound.
func (d *DB) GetAIThread(ctx context.Context, id string) (*core.AIThread, error) {
	t, err := scanAIThread(d.sql.QueryRowContext(ctx, `SELECT `+aiThreadColumns+` FROM ai_threads WHERE id = ?`, id))
	if err != nil {
		return nil, readErr("get ai thread", err)
	}
	return t, nil
}

// ListAIThreads returns the threads of one user about one note that were
// active at or after since, newest first, without their messages. A zero
// since lists every thread.
func (d *DB) ListAIThreads(ctx context.Context, userID, noteID string, since time.Time) ([]*core.AIThread, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT `+aiThreadColumns+` FROM ai_threads
		WHERE user_id = ? AND note_id = ? AND updated_at >= ?
		ORDER BY updated_at DESC, id DESC`, userID, noteID, fmtTime(since))
	if err != nil {
		return nil, readErr("list ai threads", err)
	}
	defer rows.Close()
	var out []*core.AIThread
	for rows.Next() {
		t, err := scanAIThread(rows)
		if err != nil {
			return nil, readErr("scan ai thread", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list ai threads", err)
	}
	return out, nil
}

// AddAIMessage stores a message and moves the updated_at of its thread to the
// message time, in one transaction. A missing thread is core.ErrNotFound.
func (d *DB) AddAIMessage(ctx context.Context, m *core.AIThreadMessage) error {
	if m == nil || m.ThreadID == "" {
		return fmt.Errorf("index: add ai message: thread is required: %w", core.ErrInvalid)
	}
	if m.Role != core.AIRoleUser && m.Role != core.AIRoleAssistant {
		return fmt.Errorf("index: add ai message: role %q: %w", m.Role, core.ErrInvalid)
	}
	if m.ID == "" {
		m.ID = core.NewID()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	contextJSON, err := marshalJSONArray(m.Context)
	if err != nil {
		return fmt.Errorf("index: add ai message: context: %w", err)
	}
	sourcesJSON, err := marshalJSONArray(m.Sources)
	if err != nil {
		return fmt.Errorf("index: add ai message: sources: %w", err)
	}
	return d.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE ai_threads SET updated_at = MAX(updated_at, ?) WHERE id = ?`,
			fmtTime(m.CreatedAt), m.ThreadID)
		if err := affected("add ai message", res, err); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_messages (`+aiMessageColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			m.ID, m.ThreadID, m.Role, m.Content, contextJSON, sourcesJSON, boolInt(m.Applied), fmtTime(m.CreatedAt))
		if err != nil {
			return writeErr("add ai message", err)
		}
		return nil
	})
}

// ListAIMessages returns the messages of a thread, oldest first.
func (d *DB) ListAIMessages(ctx context.Context, threadID string) ([]*core.AIThreadMessage, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT `+aiMessageColumns+` FROM ai_messages
		WHERE thread_id = ? ORDER BY created_at, rowid`, threadID)
	if err != nil {
		return nil, readErr("list ai messages", err)
	}
	defer rows.Close()
	var out []*core.AIThreadMessage
	for rows.Next() {
		m, err := scanAIMessage(rows)
		if err != nil {
			return nil, readErr("scan ai message", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list ai messages", err)
	}
	return out, nil
}

// SetAIMessageApplied sets the applied flag of a message in a thread. A
// message that is not in that thread is core.ErrNotFound.
func (d *DB) SetAIMessageApplied(ctx context.Context, threadID, messageID string, applied bool) error {
	res, err := d.sql.ExecContext(ctx, `UPDATE ai_messages SET applied = ? WHERE id = ? AND thread_id = ?`,
		boolInt(applied), messageID, threadID)
	return affected("set ai message applied", res, err)
}

// DeleteAIThread deletes a thread and, through the foreign key, its
// messages. A missing thread is core.ErrNotFound.
func (d *DB) DeleteAIThread(ctx context.Context, id string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM ai_threads WHERE id = ?`, id)
	return affected("delete ai thread", res, err)
}

// DeleteAIThreadsBefore deletes the threads last active before cutoff, of one
// user or of every user when userID is empty, and returns how many it
// deleted.
func (d *DB) DeleteAIThreadsBefore(ctx context.Context, userID string, cutoff time.Time) (int, error) {
	q := `DELETE FROM ai_threads WHERE updated_at < ?`
	args := []any{fmtTime(cutoff)}
	if userID != "" {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	res, err := d.sql.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, writeErr("delete old ai threads", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("index: delete old ai threads: rows affected: %w", err)
	}
	return int(n), nil
}
