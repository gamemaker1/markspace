package index

import (
	"context"
	"fmt"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// CreateItemShare grants a user access to an item of the given kind. A second
// grant for the same item and user returns core.ErrExists.
func (d *DB) CreateItemShare(ctx context.Context, kind core.ItemKind, s *core.Share) error {
	if err := validKind(kind); err != nil {
		return fmt.Errorf("index: create share: %w", err)
	}
	if s == nil {
		return fmt.Errorf("index: create share: nil share: %w", core.ErrInvalid)
	}
	if s.ID == "" {
		s.ID = core.NewID()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	const q = `INSERT INTO shares (` + shareColumns + `, target_kind) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, s.ID, s.NoteID, s.SpaceID, s.UserID,
		int(s.Access), boolInt(s.IncludeChildren), s.CreatedBy, fmtTime(s.CreatedAt), string(kind))
	if err != nil {
		return writeErr("create share", err)
	}
	return nil
}

// ListItemShares returns every share on one item of the given kind, newest
// first.
func (d *DB) ListItemShares(ctx context.Context, kind core.ItemKind, itemID string) ([]*core.Share, error) {
	return d.listShares(ctx, `SELECT `+shareColumns+` FROM shares
		WHERE note_id = ? AND target_kind = ? ORDER BY created_at DESC`, itemID, string(kind))
}

// ItemShareGrantsForUser returns every direct grant the user holds on any
// kind of item, with the path of the target, sorted by path. A grant whose
// target row is missing is skipped.
func (d *DB) ItemShareGrantsForUser(ctx context.Context, userID string) ([]ports.ShareGrant, error) {
	const q = `SELECT s.note_id, s.space_id, COALESCE(n.path, fo.path, fi.path) AS p, s.access,
			s.include_children, s.target_kind
		FROM shares s
		LEFT JOIN notes n ON s.target_kind = 'note' AND n.id = s.note_id
		LEFT JOIN folders fo ON s.target_kind = 'folder' AND fo.id = s.note_id
		LEFT JOIN files fi ON s.target_kind = 'file' AND fi.id = s.note_id
		WHERE s.user_id = ? AND COALESCE(n.path, fo.path, fi.path) IS NOT NULL
		ORDER BY p, s.note_id`
	rows, err := d.sql.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, readErr("item share grants for user", err)
	}
	defer rows.Close()
	var out []ports.ShareGrant
	for rows.Next() {
		var (
			g               ports.ShareGrant
			access, include int
			kind            string
		)
		if err := rows.Scan(&g.NoteID, &g.SpaceID, &g.NotePath, &access, &include, &kind); err != nil {
			return nil, readErr("item share grants for user", err)
		}
		g.Access = core.Access(access)
		g.IncludeChildren = intBool(include)
		g.Kind = core.ItemKind(kind)
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("item share grants for user", err)
	}
	return out, nil
}

// CreateItemPublicLink inserts an anonymous access link on an item of the
// given kind. A duplicate token returns core.ErrExists.
func (d *DB) CreateItemPublicLink(ctx context.Context, kind core.ItemKind, l *core.PublicLink) error {
	if err := validKind(kind); err != nil {
		return fmt.Errorf("index: create public link: %w", err)
	}
	if l == nil {
		return fmt.Errorf("index: create public link: nil link: %w", core.ErrInvalid)
	}
	if l.ID == "" {
		l.ID = core.NewID()
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now().UTC()
	}
	const q = `INSERT INTO public_links (` + publicLinkColumns + `, target_kind)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, l.ID, l.Token, l.NoteID, l.SpaceID,
		int(l.Access), l.PasswordHash, boolInt(l.IncludeChildren),
		fmtTimePtr(l.ExpiresAt), l.Views, l.CreatedBy, fmtTime(l.CreatedAt), string(kind))
	if err != nil {
		return writeErr("create public link", err)
	}
	l.HasPassword = l.PasswordHash != ""
	return nil
}

// ListItemPublicLinks returns every link on one item of the given kind,
// newest first.
func (d *DB) ListItemPublicLinks(ctx context.Context, kind core.ItemKind, itemID string) ([]*core.PublicLink, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT `+publicLinkColumns+` FROM public_links
		WHERE note_id = ? AND target_kind = ? ORDER BY created_at DESC`, itemID, string(kind))
	if err != nil {
		return nil, readErr("list item public links", err)
	}
	defer rows.Close()
	var out []*core.PublicLink
	for rows.Next() {
		l, err := scanPublicLink(rows)
		if err != nil {
			return nil, readErr("list item public links", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list item public links", err)
	}
	return out, nil
}

// PublicLinkKind returns the target kind of one public link.
func (d *DB) PublicLinkKind(ctx context.Context, id string) (core.ItemKind, error) {
	var kind string
	err := d.sql.QueryRowContext(ctx, `SELECT target_kind FROM public_links WHERE id = ?`, id).Scan(&kind)
	if err != nil {
		return "", readErr("public link kind "+id, err)
	}
	return core.ItemKind(kind), nil
}

// CreateItemComment inserts a comment on an item of the given kind. An empty
// ID is filled with a new identifier and zero timestamps are set to now.
func (d *DB) CreateItemComment(ctx context.Context, kind core.ItemKind, c *core.Comment) error {
	if err := validKind(kind); err != nil {
		return fmt.Errorf("index: create comment: %w", err)
	}
	if c == nil {
		return fmt.Errorf("index: create comment: nil comment: %w", core.ErrInvalid)
	}
	if c.ID == "" {
		c.ID = core.NewID()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.CreatedAt
	}
	const q = `INSERT INTO comments (` + commentColumns + `, target_kind)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, c.ID, c.NoteID, c.SpaceID, c.UserID, c.ParentID,
		c.Body, marshalAnchor(c.Anchor), boolInt(c.Resolved), c.ResolvedBy,
		fmtTime(c.CreatedAt), fmtTime(c.UpdatedAt), fmtTimePtr(c.DeletedAt), fmtTimePtr(c.EditedAt), string(kind))
	if err != nil {
		return writeErr("create comment", err)
	}
	return nil
}

// ListItemComments returns every comment on one item of the given kind,
// oldest first, soft-deleted rows included.
func (d *DB) ListItemComments(ctx context.Context, kind core.ItemKind, itemID string) ([]*core.Comment, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT `+commentColumns+` FROM comments
		WHERE note_id = ? AND target_kind = ? ORDER BY created_at, id`, itemID, string(kind))
	if err != nil {
		return nil, readErr("list item comments", err)
	}
	defer rows.Close()
	var out []*core.Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, readErr("list item comments", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list item comments", err)
	}
	return out, nil
}
