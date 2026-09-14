package index

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// userColumns is the SELECT list matching scanUser.
const userColumns = `id, email, name, password_hash, is_admin, is_active, locale, timezone,
	avatar_color, last_login_at, created_at, updated_at`

// scanUser reads one users row.
func scanUser(row rowScanner) (*core.User, error) {
	var (
		u                             core.User
		isAdmin, isActive             int
		lastLogin, created, updatedAt string
	)
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &isAdmin, &isActive,
		&u.Locale, &u.Timezone, &u.AvatarColor, &lastLogin, &created, &updatedAt)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = intBool(isAdmin)
	u.IsActive = intBool(isActive)
	u.LastLoginAt = parseTime(lastLogin)
	u.CreatedAt = parseTime(created)
	u.UpdatedAt = parseTime(updatedAt)
	return &u, nil
}

// CreateUser inserts a user. An empty ID is filled with a new identifier and
// zero timestamps are set to now, so the caller may pass a bare record. A
// duplicate email returns core.ErrExists, since the email column is unique
// and case-insensitive.
func (d *DB) CreateUser(ctx context.Context, u *core.User) error {
	if u == nil {
		return fmt.Errorf("index: create user: nil user: %w", core.ErrInvalid)
	}
	if u.ID == "" {
		u.ID = core.NewID()
	}
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = u.CreatedAt
	}
	const q = `INSERT INTO users (` + userColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q,
		u.ID, u.Email, u.Name, u.PasswordHash, boolInt(u.IsAdmin), boolInt(u.IsActive),
		u.Locale, u.Timezone, u.AvatarColor, fmtTime(u.LastLoginAt), fmtTime(u.CreatedAt),
		fmtTime(u.UpdatedAt))
	if err != nil {
		return writeErr("create user", err)
	}
	return nil
}

// UpdateUser writes every mutable field of an existing user and sets
// UpdatedAt to now. It returns core.ErrNotFound when the id is unknown.
func (d *DB) UpdateUser(ctx context.Context, u *core.User) error {
	if u == nil {
		return fmt.Errorf("index: update user: nil user: %w", core.ErrInvalid)
	}
	u.UpdatedAt = time.Now().UTC()
	const q = `UPDATE users SET
		email = ?, name = ?, password_hash = ?, is_admin = ?, is_active = ?,
		locale = ?, timezone = ?, avatar_color = ?, last_login_at = ?,
		updated_at = ?
		WHERE id = ?`
	res, err := d.sql.ExecContext(ctx, q,
		u.Email, u.Name, u.PasswordHash, boolInt(u.IsAdmin), boolInt(u.IsActive),
		u.Locale, u.Timezone, u.AvatarColor, fmtTime(u.LastLoginAt),
		fmtTime(u.UpdatedAt), u.ID)
	return affected("update user", res, err)
}

// DeleteUser removes a user. Sessions, API tokens and space memberships
// cascade. Notes keep the created_by and updated_by identifiers, because they
// live in the markdown frontmatter.
func (d *DB) DeleteUser(ctx context.Context, id string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		res, err := t.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
		if err := affected("delete user", res, err); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `DELETE FROM ai_routes WHERE scope = ?`, id); err != nil {
			return writeErr("delete user routes", err)
		}
		return nil
	})
}

// GetUser reads one user by id.
func (d *DB) GetUser(ctx context.Context, id string) (*core.User, error) {
	row := d.sql.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if err != nil {
		return nil, readErr("get user "+id, err)
	}
	return u, nil
}

// GetUserByEmail reads one user by email. The lookup is case-insensitive.
func (d *DB) GetUserByEmail(ctx context.Context, email string) (*core.User, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = ? COLLATE NOCASE`,
		strings.TrimSpace(email))
	u, err := scanUser(row)
	if err != nil {
		return nil, readErr("get user by email", err)
	}
	return u, nil
}

// ListUsers returns a page of users ordered by email, along with the total
// number of users matching query. An empty query lists everyone. The query
// matches a substring of the email or the name, case-insensitively.
func (d *DB) ListUsers(ctx context.Context, query string, limit, offset int) ([]*core.User, int, error) {
	where := ""
	var args []any
	if q := strings.TrimSpace(query); q != "" {
		pattern := "%" + likeEscape(strings.ToLower(q)) + "%"
		where = ` WHERE (lower(email) LIKE ? ESCAPE '\' OR lower(name) LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern)
	}

	var total int
	if err := d.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, 0, readErr("count users", err)
	}

	page, pageArgs := limitClause(limit, offset)
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users`+where+` ORDER BY email COLLATE NOCASE`+page,
		append(args, pageArgs...)...)
	if err != nil {
		return nil, 0, readErr("list users", err)
	}
	defer rows.Close()

	var out []*core.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, readErr("list users", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, readErr("list users", err)
	}
	return out, total, nil
}

// CountUsers returns the number of accounts.
func (d *DB) CountUsers(ctx context.Context) (int, error) {
	return d.count(ctx, `SELECT COUNT(*) FROM users`, "count users")
}

// TouchUserLogin records a successful sign-in.
func (d *DB) TouchUserLogin(ctx context.Context, id string, at time.Time) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE id = ?`, fmtTime(at), id)
	return affected("touch user login", res, err)
}

// count runs a single-value COUNT query.
func (d *DB) count(ctx context.Context, query, what string) (int, error) {
	var n int
	if err := d.sql.QueryRowContext(ctx, query).Scan(&n); err != nil {
		return 0, readErr(what, err)
	}
	return n, nil
}
