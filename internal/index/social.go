package index

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// Stars.

// SetStar adds or removes a star. Setting a star that already exists and
// clearing one that does not are both no-ops rather than errors.
func (d *DB) SetStar(ctx context.Context, userID, noteID string, starred bool) error {
	if starred {
		_, err := d.sql.ExecContext(ctx,
			`INSERT OR IGNORE INTO stars (user_id, note_id, created_at) VALUES (?, ?, ?)`,
			userID, noteID, fmtTime(time.Now()))
		if err != nil {
			return writeErr("set star", err)
		}
		return nil
	}
	_, err := d.sql.ExecContext(ctx,
		`DELETE FROM stars WHERE user_id = ? AND note_id = ?`, userID, noteID)
	if err != nil {
		return writeErr("clear star", err)
	}
	return nil
}

// IsStarred reports whether the user starred the note.
func (d *DB) IsStarred(ctx context.Context, userID, noteID string) (bool, error) {
	var n int
	err := d.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stars WHERE user_id = ? AND note_id = ?`, userID, noteID).Scan(&n)
	if err != nil {
		return false, readErr("is starred", err)
	}
	return n > 0, nil
}

// ListStarred returns the live notes the user starred, most recently starred
// first, with Starred set on every note.
func (d *DB) ListStarred(ctx context.Context, userID string) ([]*core.Note, error) {
	const q = `SELECT ` + noteColumns + ` FROM notes n
		JOIN stars st ON st.note_id = n.id
		WHERE st.user_id = ? AND n.deleted = 0
		ORDER BY st.created_at DESC, n.title COLLATE NOCASE`
	rows, err := d.sql.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, readErr("list starred", err)
	}
	defer rows.Close()

	notes, err := collectNotes(rows, "list starred")
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		n.Starred = true
	}
	return notes, nil
}

// Comments.

// commentColumns is the SELECT list matching scanComment.
const commentColumns = `id, note_id, space_id, user_id, parent_id, body, anchor,
	resolved, resolved_by, created_at, updated_at, deleted_at, edited_at`

// scanComment reads one comments row.
func scanComment(row rowScanner) (*core.Comment, error) {
	var (
		c                         core.Comment
		resolved                  int
		anchor                    string
		created, updated, deleted string
		edited                    string
	)
	err := row.Scan(&c.ID, &c.NoteID, &c.SpaceID, &c.UserID, &c.ParentID, &c.Body,
		&anchor, &resolved, &c.ResolvedBy, &created, &updated, &deleted, &edited)
	if err != nil {
		return nil, err
	}
	c.Anchor = unmarshalAnchor(anchor)
	c.Resolved = intBool(resolved)
	c.CreatedAt = parseTime(created)
	c.UpdatedAt = parseTime(updated)
	c.DeletedAt = parseTimePtr(deleted)
	c.EditedAt = parseTimePtr(edited)
	return &c, nil
}

// marshalAnchor stores an anchor as JSON, or the empty string for none.
func marshalAnchor(a *core.CommentAnchor) string {
	if a == nil {
		return ""
	}
	b, err := json.Marshal(a)
	if err != nil {
		return ""
	}
	return string(b)
}

// unmarshalAnchor reads a stored anchor. A value that is not a JSON object
// comes from before anchors were structured, and is read as the exact text
// with no known offsets.
func unmarshalAnchor(s string) *core.CommentAnchor {
	if s == "" {
		return nil
	}
	var a core.CommentAnchor
	if strings.HasPrefix(s, "{") && json.Unmarshal([]byte(s), &a) == nil {
		return &a
	}
	return &core.CommentAnchor{Exact: s}
}

// CreateComment inserts a comment. An empty ID is filled with a new
// identifier and zero timestamps are set to now.
func (d *DB) CreateComment(ctx context.Context, c *core.Comment) error {
	if c == nil {
		return fmt.Errorf("index: create comment: nil comment: %w", core.ErrInvalid)
	}
	if c.ID == "" {
		c.ID = core.NewID()
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.CreatedAt
	}
	const q = `INSERT INTO comments (` + commentColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, c.ID, c.NoteID, c.SpaceID, c.UserID, c.ParentID,
		c.Body, marshalAnchor(c.Anchor), boolInt(c.Resolved), c.ResolvedBy,
		fmtTime(c.CreatedAt), fmtTime(c.UpdatedAt), fmtTimePtr(c.DeletedAt), fmtTimePtr(c.EditedAt))
	if err != nil {
		return writeErr("create comment", err)
	}
	return nil
}

// GetComment reads one comment by id.
func (d *DB) GetComment(ctx context.Context, id string) (*core.Comment, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+commentColumns+` FROM comments WHERE id = ?`, id)
	c, err := scanComment(row)
	if err != nil {
		return nil, readErr("get comment "+id, err)
	}
	return c, nil
}

// UpdateComment writes the mutable fields of a comment and sets UpdatedAt to
// now. EditedAt is written as given, so only a caller that edits the body
// sets it. Setting DeletedAt through this call is how a comment is hidden
// while its replies stay readable.
func (d *DB) UpdateComment(ctx context.Context, c *core.Comment) error {
	if c == nil {
		return fmt.Errorf("index: update comment: nil comment: %w", core.ErrInvalid)
	}
	c.UpdatedAt = time.Now().UTC()
	const q = `UPDATE comments SET body = ?, anchor = ?, resolved = ?, resolved_by = ?,
		updated_at = ?, deleted_at = ?, edited_at = ? WHERE id = ?`
	res, err := d.sql.ExecContext(ctx, q, c.Body, marshalAnchor(c.Anchor), boolInt(c.Resolved),
		c.ResolvedBy, fmtTime(c.UpdatedAt), fmtTimePtr(c.DeletedAt), fmtTimePtr(c.EditedAt), c.ID)
	return affected("update comment", res, err)
}

// UpdateCommentAnchor replaces the anchor of one comment in a single
// statement, only while the stored anchor still serializes to prev. No other
// column changes. It reports whether a row was written.
func (d *DB) UpdateCommentAnchor(ctx context.Context, id string, prev, next *core.CommentAnchor) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE comments SET anchor = ? WHERE id = ? AND anchor = ?`,
		marshalAnchor(next), id, marshalAnchor(prev))
	if err != nil {
		return false, writeErr("update comment anchor", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, writeErr("update comment anchor", err)
	}
	return n == 1, nil
}

// DeleteComment removes a comment row for good, along with its replies. A
// soft delete that keeps the thread shape is UpdateComment with DeletedAt.
func (d *DB) DeleteComment(ctx context.Context, id string) error {
	if _, err := d.sql.ExecContext(ctx, `DELETE FROM comments WHERE parent_id = ?`, id); err != nil {
		return writeErr("delete comment replies", err)
	}
	res, err := d.sql.ExecContext(ctx, `DELETE FROM comments WHERE id = ?`, id)
	return affected("delete comment", res, err)
}

// ListComments returns every comment on a note, oldest first, soft-deleted
// rows included. The caller builds the reply tree and decides what to show.
func (d *DB) ListComments(ctx context.Context, noteID string) ([]*core.Comment, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+commentColumns+` FROM comments WHERE note_id = ? ORDER BY created_at, id`, noteID)
	if err != nil {
		return nil, readErr("list comments", err)
	}
	defer rows.Close()

	var out []*core.Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, readErr("list comments", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list comments", err)
	}
	return out, nil
}

// CountComments returns the number of comments that are not soft-deleted.
func (d *DB) CountComments(ctx context.Context) (int, error) {
	return d.count(ctx, `SELECT COUNT(*) FROM comments WHERE deleted_at = ''`, "count comments")
}

// Per-note shares.

// shareColumns is the SELECT list matching scanShare.
const shareColumns = `id, note_id, space_id, user_id, access, include_children,
	created_by, created_at`

// scanShare reads one shares row.
func scanShare(row rowScanner) (*core.Share, error) {
	var (
		s               core.Share
		access, include int
		created         string
	)
	err := row.Scan(&s.ID, &s.NoteID, &s.SpaceID, &s.UserID, &access, &include,
		&s.CreatedBy, &created)
	if err != nil {
		return nil, err
	}
	s.Access = core.Access(access)
	s.IncludeChildren = intBool(include)
	s.CreatedAt = parseTime(created)
	return &s, nil
}

// CreateShare grants a user access to a note. A second grant for the same
// note and user returns core.ErrExists, so the caller updates instead.
func (d *DB) CreateShare(ctx context.Context, s *core.Share) error {
	if s == nil {
		return fmt.Errorf("index: create share: nil share: %w", core.ErrInvalid)
	}
	if s.ID == "" {
		s.ID = core.NewID()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	const q = `INSERT INTO shares (` + shareColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, s.ID, s.NoteID, s.SpaceID, s.UserID,
		int(s.Access), boolInt(s.IncludeChildren), s.CreatedBy, fmtTime(s.CreatedAt))
	if err != nil {
		return writeErr("create share", err)
	}
	return nil
}

// GetShare reads one share by id.
func (d *DB) GetShare(ctx context.Context, id string) (*core.Share, error) {
	row := d.sql.QueryRowContext(ctx, `SELECT `+shareColumns+` FROM shares WHERE id = ?`, id)
	s, err := scanShare(row)
	if err != nil {
		return nil, readErr("get share "+id, err)
	}
	return s, nil
}

// UpdateShare changes the access level and the child inheritance of a share.
func (d *DB) UpdateShare(ctx context.Context, s *core.Share) error {
	if s == nil {
		return fmt.Errorf("index: update share: nil share: %w", core.ErrInvalid)
	}
	res, err := d.sql.ExecContext(ctx,
		`UPDATE shares SET access = ?, include_children = ? WHERE id = ?`,
		int(s.Access), boolInt(s.IncludeChildren), s.ID)
	return affected("update share", res, err)
}

// DeleteShare revokes a share.
func (d *DB) DeleteShare(ctx context.Context, id string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM shares WHERE id = ?`, id)
	return affected("delete share", res, err)
}

// ListSharesForNote returns every share on one note, newest first.
func (d *DB) ListSharesForNote(ctx context.Context, noteID string) ([]*core.Share, error) {
	return d.listShares(ctx, `SELECT `+shareColumns+` FROM shares
		WHERE note_id = ? ORDER BY created_at DESC`, noteID)
}

// ListSharesForUser returns every share granted to one user, newest first.
func (d *DB) ListSharesForUser(ctx context.Context, userID string) ([]*core.Share, error) {
	return d.listShares(ctx, `SELECT `+shareColumns+` FROM shares
		WHERE user_id = ? ORDER BY created_at DESC`, userID)
}

// listShares drains a share query.
func (d *DB) listShares(ctx context.Context, query string, args ...any) ([]*core.Share, error) {
	rows, err := d.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, readErr("list shares", err)
	}
	defer rows.Close()

	var out []*core.Share
	for rows.Next() {
		s, err := scanShare(rows)
		if err != nil {
			return nil, readErr("list shares", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list shares", err)
	}
	return out, nil
}

// FindShare returns the share of one note for one user.
func (d *DB) FindShare(ctx context.Context, noteID, userID string) (*core.Share, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+shareColumns+` FROM shares WHERE note_id = ? AND user_id = ?`, noteID, userID)
	s, err := scanShare(row)
	if err != nil {
		return nil, readErr("find share", err)
	}
	return s, nil
}

// ShareGrantsForUser returns every direct grant the user holds with the path
// of the shared note, so the caller can apply IncludeChildren by prefix
// without a query per note. A grant whose note row is missing is skipped.
func (d *DB) ShareGrantsForUser(ctx context.Context, userID string) ([]ports.ShareGrant, error) {
	const q = `SELECT s.note_id, s.space_id, n.path, s.access, s.include_children
		FROM shares s
		JOIN notes n ON n.id = s.note_id
		WHERE s.user_id = ? AND s.target_kind = 'note'
		ORDER BY n.path`
	rows, err := d.sql.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, readErr("share grants for user", err)
	}
	defer rows.Close()

	var out []ports.ShareGrant
	for rows.Next() {
		var (
			g               ports.ShareGrant
			access, include int
		)
		if err := rows.Scan(&g.NoteID, &g.SpaceID, &g.NotePath, &access, &include); err != nil {
			return nil, readErr("share grants for user", err)
		}
		g.Access = core.Access(access)
		g.IncludeChildren = intBool(include)
		g.Kind = core.KindNote
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("share grants for user", err)
	}
	return out, nil
}

// Public links.

// publicLinkColumns is the SELECT list matching scanPublicLink.
const publicLinkColumns = `id, token, note_id, space_id, access, password_hash,
	include_children, expires_at, views, created_by, created_at`

// scanPublicLink reads one public_links row. HasPassword is derived from the
// stored digest, because the digest itself never leaves the server.
func scanPublicLink(row rowScanner) (*core.PublicLink, error) {
	var (
		l                core.PublicLink
		access, include  int
		expires, created string
	)
	err := row.Scan(&l.ID, &l.Token, &l.NoteID, &l.SpaceID, &access, &l.PasswordHash,
		&include, &expires, &l.Views, &l.CreatedBy, &created)
	if err != nil {
		return nil, err
	}
	l.Access = core.Access(access)
	l.IncludeChildren = intBool(include)
	l.HasPassword = l.PasswordHash != ""
	l.ExpiresAt = parseTimePtr(expires)
	l.CreatedAt = parseTime(created)
	return &l, nil
}

// CreatePublicLink inserts an anonymous access link. A duplicate token
// returns core.ErrExists.
func (d *DB) CreatePublicLink(ctx context.Context, l *core.PublicLink) error {
	if l == nil {
		return fmt.Errorf("index: create public link: nil link: %w", core.ErrInvalid)
	}
	if l.ID == "" {
		l.ID = core.NewID()
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now().UTC()
	}
	const q = `INSERT INTO public_links (` + publicLinkColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, l.ID, l.Token, l.NoteID, l.SpaceID,
		int(l.Access), l.PasswordHash, boolInt(l.IncludeChildren),
		fmtTimePtr(l.ExpiresAt), l.Views, l.CreatedBy, fmtTime(l.CreatedAt))
	if err != nil {
		return writeErr("create public link", err)
	}
	return nil
}

// GetPublicLink reads one link by id.
func (d *DB) GetPublicLink(ctx context.Context, id string) (*core.PublicLink, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+publicLinkColumns+` FROM public_links WHERE id = ?`, id)
	l, err := scanPublicLink(row)
	if err != nil {
		return nil, readErr("get public link "+id, err)
	}
	return l, nil
}

// GetPublicLinkByToken reads one link by its secret token. An expired link is
// still returned, because the caller needs core.PublicLink.Expired to tell a
// visitor that the link has lapsed.
func (d *DB) GetPublicLinkByToken(ctx context.Context, token string) (*core.PublicLink, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+publicLinkColumns+` FROM public_links WHERE token = ?`, token)
	l, err := scanPublicLink(row)
	if err != nil {
		return nil, readErr("get public link by token", err)
	}
	return l, nil
}

// UpdatePublicLink writes the mutable fields of a link. The view counter is
// left alone, because IncrementLinkViews owns it.
func (d *DB) UpdatePublicLink(ctx context.Context, l *core.PublicLink) error {
	if l == nil {
		return fmt.Errorf("index: update public link: nil link: %w", core.ErrInvalid)
	}
	const q = `UPDATE public_links SET access = ?, password_hash = ?, include_children = ?,
		expires_at = ? WHERE id = ?`
	res, err := d.sql.ExecContext(ctx, q, int(l.Access), l.PasswordHash,
		boolInt(l.IncludeChildren), fmtTimePtr(l.ExpiresAt), l.ID)
	if err == nil {
		l.HasPassword = l.PasswordHash != ""
	}
	return affected("update public link", res, err)
}

// DeletePublicLink revokes a link.
func (d *DB) DeletePublicLink(ctx context.Context, id string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM public_links WHERE id = ?`, id)
	return affected("delete public link", res, err)
}

// ListPublicLinks returns every link on one note, newest first.
func (d *DB) ListPublicLinks(ctx context.Context, noteID string) ([]*core.PublicLink, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+publicLinkColumns+` FROM public_links WHERE note_id = ? ORDER BY created_at DESC`,
		noteID)
	if err != nil {
		return nil, readErr("list public links", err)
	}
	defer rows.Close()

	var out []*core.PublicLink
	for rows.Next() {
		l, err := scanPublicLink(rows)
		if err != nil {
			return nil, readErr("list public links", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list public links", err)
	}
	return out, nil
}

// IncrementLinkViews adds one to the view counter of a link.
func (d *DB) IncrementLinkViews(ctx context.Context, id string) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE public_links SET views = views + 1 WHERE id = ?`, id)
	return affected("increment link views", res, err)
}
