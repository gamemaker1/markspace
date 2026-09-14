package index

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// spaceColumns is the SELECT list matching scanSpace. Every query aliases the
// spaces table as s.
const spaceColumns = `s.id, s.slug, s.name, s.description, s.icon, s.paste_folder,
	s.created_by, s.created_at, s.updated_at`

// scanSpace reads one spaces row.
func scanSpace(row rowScanner) (*core.Space, error) {
	var (
		s                core.Space
		created, updated string
	)
	err := row.Scan(&s.ID, &s.Slug, &s.Name, &s.Description, &s.Icon, &s.PasteFolder,
		&s.CreatedBy, &created, &updated)
	if err != nil {
		return nil, err
	}
	s.CreatedAt = parseTime(created)
	s.UpdatedAt = parseTime(updated)
	return &s, nil
}

// CreateSpace inserts a space. An empty ID is filled with a new identifier
// and zero timestamps are set to now. A duplicate slug returns
// core.ErrExists.
func (d *DB) CreateSpace(ctx context.Context, s *core.Space) error {
	if s == nil {
		return fmt.Errorf("index: create space: nil space: %w", core.ErrInvalid)
	}
	if s.ID == "" {
		s.ID = core.NewID()
	}
	now := time.Now().UTC()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.CreatedAt
	}
	const q = `INSERT INTO spaces (id, slug, name, description, icon, paste_folder, created_by,
		created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, s.ID, s.Slug, s.Name, s.Description, s.Icon, s.PasteFolder,
		s.CreatedBy, fmtTime(s.CreatedAt), fmtTime(s.UpdatedAt))
	if err != nil {
		return writeErr("create space", err)
	}
	return nil
}

// UpdateSpace writes the mutable fields of a space and sets UpdatedAt to now.
func (d *DB) UpdateSpace(ctx context.Context, s *core.Space) error {
	if s == nil {
		return fmt.Errorf("index: update space: nil space: %w", core.ErrInvalid)
	}
	s.UpdatedAt = time.Now().UTC()
	const q = `UPDATE spaces SET slug = ?, name = ?, description = ?, icon = ?, paste_folder = ?,
		updated_at = ? WHERE id = ?`
	res, err := d.sql.ExecContext(ctx, q, s.Slug, s.Name, s.Description, s.Icon, s.PasteFolder,
		fmtTime(s.UpdatedAt), s.ID)
	return affected("update space", res, err)
}

// DeleteSpace removes a space. Its notes and memberships cascade. Rows keyed
// by note id rather than space id, such as comments and stars, are removed
// here as well, because their notes are gone.
func (d *DB) DeleteSpace(ctx context.Context, id string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM notes_fts WHERE space_id = ?`,
			`DELETE FROM files_fts WHERE space_id = ?`,
			`DELETE FROM note_tags WHERE space_id = ?`,
			`DELETE FROM note_links WHERE space_id = ?`,
			`DELETE FROM embeddings WHERE space_id = ?`,
			`DELETE FROM comments WHERE space_id = ?`,
			`DELETE FROM shares WHERE space_id = ?`,
			`DELETE FROM public_links WHERE space_id = ?`,
			`DELETE FROM stars WHERE note_id IN (SELECT id FROM notes WHERE space_id = ?)`,
		} {
			if _, err := t.ExecContext(ctx, q, id); err != nil {
				return writeErr("delete space", err)
			}
		}
		res, err := t.ExecContext(ctx, `DELETE FROM spaces WHERE id = ?`, id)
		return affected("delete space", res, err)
	})
}

// GetSpace reads one space by id.
func (d *DB) GetSpace(ctx context.Context, id string) (*core.Space, error) {
	row := d.sql.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM spaces s WHERE s.id = ?`, id)
	s, err := scanSpace(row)
	if err != nil {
		return nil, readErr("get space "+id, err)
	}
	return s, nil
}

// GetSpaceBySlug reads one space by slug.
func (d *DB) GetSpaceBySlug(ctx context.Context, slug string) (*core.Space, error) {
	row := d.sql.QueryRowContext(ctx, `SELECT `+spaceColumns+` FROM spaces s WHERE s.slug = ?`, slug)
	s, err := scanSpace(row)
	if err != nil {
		return nil, readErr("get space by slug "+slug, err)
	}
	return s, nil
}

// ListSpaces returns every space ordered by name.
func (d *DB) ListSpaces(ctx context.Context) ([]*core.Space, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+spaceColumns+` FROM spaces s ORDER BY s.name COLLATE NOCASE, s.slug`)
	if err != nil {
		return nil, readErr("list spaces", err)
	}
	defer rows.Close()

	var out []*core.Space
	for rows.Next() {
		s, err := scanSpace(rows)
		if err != nil {
			return nil, readErr("list spaces", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list spaces", err)
	}
	return out, nil
}

// ListSpacesForUser returns the spaces the user is a member of, with MyRole
// from the membership row and NoteCount as the number of live notes. It does
// not add every space for an instance admin, because membership is what the
// sidebar shows.
func (d *DB) ListSpacesForUser(ctx context.Context, userID string) ([]*core.Space, error) {
	const q = `SELECT ` + spaceColumns + `, m.role, COALESCE(c.n, 0)
		FROM spaces s
		JOIN space_members m ON m.space_id = s.id AND m.user_id = ?
		LEFT JOIN (
			SELECT space_id, COUNT(*) AS n FROM notes WHERE deleted = 0 GROUP BY space_id
		) c ON c.space_id = s.id
		ORDER BY s.name COLLATE NOCASE, s.slug`
	rows, err := d.sql.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, readErr("list spaces for user", err)
	}
	defer rows.Close()

	var out []*core.Space
	for rows.Next() {
		var (
			s                core.Space
			created, updated string
			role             string
			noteCount        int
		)
		err := rows.Scan(&s.ID, &s.Slug, &s.Name, &s.Description, &s.Icon, &s.PasteFolder,
			&s.CreatedBy, &created, &updated, &role, &noteCount)
		if err != nil {
			return nil, readErr("list spaces for user", err)
		}
		s.CreatedAt = parseTime(created)
		s.UpdatedAt = parseTime(updated)
		s.MyRole = core.Role(role)
		s.NoteCount = noteCount
		out = append(out, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list spaces for user", err)
	}
	return out, nil
}

// CountSpaces returns the number of spaces.
func (d *DB) CountSpaces(ctx context.Context) (int, error) {
	return d.count(ctx, `SELECT COUNT(*) FROM spaces`, "count spaces")
}

// Space membership.

// memberColumns is the SELECT list matching scanMember.
const memberColumns = `space_id, user_id, role, created_at`

// scanMember reads one space_members row.
func scanMember(row rowScanner) (*core.SpaceMember, error) {
	var (
		m       core.SpaceMember
		role    string
		created string
	)
	if err := row.Scan(&m.SpaceID, &m.UserID, &role, &created); err != nil {
		return nil, err
	}
	m.Role = core.Role(role)
	m.CreatedAt = parseTime(created)
	return &m, nil
}

// AddMember grants a user a role in a space. Adding the same pair twice
// returns core.ErrExists.
func (d *DB) AddMember(ctx context.Context, m *core.SpaceMember) error {
	if m == nil {
		return fmt.Errorf("index: add member: nil member: %w", core.ErrInvalid)
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	const q = `INSERT INTO space_members (` + memberColumns + `) VALUES (?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, m.SpaceID, m.UserID, string(m.Role), fmtTime(m.CreatedAt))
	if err != nil {
		return writeErr("add member", err)
	}
	return nil
}

// UpdateMember changes an existing member's role.
func (d *DB) UpdateMember(ctx context.Context, spaceID, userID string, role core.Role) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE space_members SET role = ? WHERE space_id = ? AND user_id = ?`,
		string(role), spaceID, userID)
	return affected("update member", res, err)
}

// RemoveMember revokes a membership.
func (d *DB) RemoveMember(ctx context.Context, spaceID, userID string) error {
	res, err := d.sql.ExecContext(ctx,
		`DELETE FROM space_members WHERE space_id = ? AND user_id = ?`, spaceID, userID)
	return affected("remove member", res, err)
}

// GetMember reads one membership.
func (d *DB) GetMember(ctx context.Context, spaceID, userID string) (*core.SpaceMember, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+memberColumns+` FROM space_members WHERE space_id = ? AND user_id = ?`,
		spaceID, userID)
	m, err := scanMember(row)
	if err != nil {
		return nil, readErr("get member", err)
	}
	return m, nil
}

// ListMembers returns every member of a space with the User field filled in,
// ordered by name then email.
func (d *DB) ListMembers(ctx context.Context, spaceID string) ([]*core.SpaceMember, error) {
	const q = `SELECT m.space_id, m.user_id, m.role, m.created_at,
		u.id, u.email, u.name, u.password_hash, u.is_admin, u.is_active, u.locale,
		u.timezone, u.avatar_color, u.last_login_at, u.created_at, u.updated_at
		FROM space_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.space_id = ?
		ORDER BY u.name COLLATE NOCASE, u.email COLLATE NOCASE`
	rows, err := d.sql.QueryContext(ctx, q, spaceID)
	if err != nil {
		return nil, readErr("list members", err)
	}
	defer rows.Close()

	var out []*core.SpaceMember
	for rows.Next() {
		var (
			m                            core.SpaceMember
			role, created                string
			u                            core.User
			isAdmin, isActive            int
			lastLogin, userCreated, uUpd string
		)
		err := rows.Scan(&m.SpaceID, &m.UserID, &role, &created,
			&u.ID, &u.Email, &u.Name, &u.PasswordHash, &isAdmin, &isActive, &u.Locale,
			&u.Timezone, &u.AvatarColor, &lastLogin, &userCreated, &uUpd)
		if err != nil {
			return nil, readErr("list members", err)
		}
		m.Role = core.Role(role)
		m.CreatedAt = parseTime(created)
		u.IsAdmin = intBool(isAdmin)
		u.IsActive = intBool(isActive)
		u.LastLoginAt = parseTime(lastLogin)
		u.CreatedAt = parseTime(userCreated)
		u.UpdatedAt = parseTime(uUpd)
		m.User = &u
		out = append(out, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list members", err)
	}
	return out, nil
}

// ListMembershipsForUser returns every space role the user holds.
func (d *DB) ListMembershipsForUser(ctx context.Context, userID string) ([]*core.SpaceMember, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+memberColumns+` FROM space_members WHERE user_id = ? ORDER BY space_id`, userID)
	if err != nil {
		return nil, readErr("list memberships for user", err)
	}
	defer rows.Close()

	var out []*core.SpaceMember
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, readErr("list memberships for user", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list memberships for user", err)
	}
	return out, nil
}
