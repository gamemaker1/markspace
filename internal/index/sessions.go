package index

import (
	"context"
	"fmt"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// sessionColumns is the SELECT list matching scanSession.
const sessionColumns = `id, user_id, token_hash, user_agent, ip, created_at, expires_at, last_seen`

// scanSession reads one sessions row.
func scanSession(row rowScanner) (*core.Session, error) {
	var (
		s                          core.Session
		created, expires, lastSeen string
	)
	err := row.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.UserAgent, &s.IP,
		&created, &expires, &lastSeen)
	if err != nil {
		return nil, err
	}
	s.CreatedAt = parseTime(created)
	s.ExpiresAt = parseTime(expires)
	s.LastSeen = parseTime(lastSeen)
	return &s, nil
}

// CreateSession inserts a login session. An empty ID is filled with a new
// identifier and zero CreatedAt and LastSeen values are set to now.
func (d *DB) CreateSession(ctx context.Context, s *core.Session) error {
	if s == nil {
		return fmt.Errorf("index: create session: nil session: %w", core.ErrInvalid)
	}
	if s.ID == "" {
		s.ID = core.NewID()
	}
	now := time.Now().UTC()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	if s.LastSeen.IsZero() {
		s.LastSeen = s.CreatedAt
	}
	const q = `INSERT INTO sessions (` + sessionColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, s.ID, s.UserID, s.TokenHash, s.UserAgent, s.IP,
		fmtTime(s.CreatedAt), fmtTime(s.ExpiresAt), fmtTime(s.LastSeen))
	if err != nil {
		return writeErr("create session", err)
	}
	return nil
}

// GetSessionByHash looks a session up by the SHA-256 digest of its cookie
// value. An expired session reports core.ErrNotFound, so a caller cannot
// authenticate with one. A zero ExpiresAt means the session never expires.
func (d *DB) GetSessionByHash(ctx context.Context, tokenHash string) (*core.Session, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE token_hash = ?`, tokenHash)
	s, err := scanSession(row)
	if err != nil {
		return nil, readErr("get session", err)
	}
	if !s.ExpiresAt.IsZero() && !time.Now().UTC().Before(s.ExpiresAt) {
		return nil, fmt.Errorf("index: get session: expired: %w", core.ErrNotFound)
	}
	return s, nil
}

// TouchSession records the last time a session was used.
func (d *DB) TouchSession(ctx context.Context, id string, at time.Time) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE sessions SET last_seen = ? WHERE id = ?`, fmtTime(at), id)
	return affected("touch session", res, err)
}

// DeleteSession removes one session by id.
func (d *DB) DeleteSession(ctx context.Context, id string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return affected("delete session", res, err)
}

// DeleteSessionByHash removes one session by token digest, which is what a
// sign-out has to hand.
func (d *DB) DeleteSessionByHash(ctx context.Context, tokenHash string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return affected("delete session by hash", res, err)
}

// ListSessions returns every session of one user, newest first, including
// expired ones that the purge has not reached yet.
func (d *DB) ListSessions(ctx context.Context, userID string) ([]*core.Session, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, readErr("list sessions", err)
	}
	defer rows.Close()

	var out []*core.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, readErr("list sessions", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list sessions", err)
	}
	return out, nil
}

// PurgeExpiredSessions deletes every session whose expiry has passed and
// returns how many rows went. Sessions with an empty expiry are kept.
func (d *DB) PurgeExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	res, err := d.sql.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <> '' AND expires_at <= ?`, fmtTime(now))
	if err != nil {
		return 0, writeErr("purge expired sessions", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("index: purge expired sessions: rows affected: %w", err)
	}
	return int(n), nil
}

// API tokens.

// apiTokenColumns is the INSERT column list of api_tokens.
const apiTokenColumns = `id, user_id, name, token_hash, prefix, created_at, expires_at, last_used`

// apiTokenSelect is the SELECT list matching scanAPIToken. A system token
// stores NULL in user_id and reads back with an empty UserID.
const apiTokenSelect = `id, COALESCE(user_id, ''), name, token_hash, prefix, created_at, expires_at, last_used`

// scanAPIToken reads one api_tokens row.
func scanAPIToken(row rowScanner) (*core.APIToken, error) {
	var (
		t                          core.APIToken
		created, expires, lastUsed string
	)
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.Prefix,
		&created, &expires, &lastUsed)
	if err != nil {
		return nil, err
	}
	t.CreatedAt = parseTime(created)
	t.ExpiresAt = parseTimePtr(expires)
	t.LastUsed = parseTimePtr(lastUsed)
	return &t, nil
}

// CreateAPIToken inserts a bearer token record. Only the digest is stored.
func (d *DB) CreateAPIToken(ctx context.Context, t *core.APIToken) error {
	if t == nil {
		return fmt.Errorf("index: create api token: nil token: %w", core.ErrInvalid)
	}
	if t.ID == "" {
		t.ID = core.NewID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	const q = `INSERT INTO api_tokens (` + apiTokenColumns + `) VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, t.ID, t.UserID, t.Name, t.TokenHash, t.Prefix,
		fmtTime(t.CreatedAt), fmtTimePtr(t.ExpiresAt), fmtTimePtr(t.LastUsed))
	if err != nil {
		return writeErr("create api token", err)
	}
	return nil
}

// GetAPITokenByHash looks a token up by digest. It returns expired tokens
// too. The application checks ExpiresAt against its own clock, so tests can
// move time and one place owns the rule. A nil ExpiresAt means the token
// never expires.
func (d *DB) GetAPITokenByHash(ctx context.Context, tokenHash string) (*core.APIToken, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+apiTokenSelect+` FROM api_tokens WHERE token_hash = ?`, tokenHash)
	t, err := scanAPIToken(row)
	if err != nil {
		return nil, readErr("get api token", err)
	}
	return t, nil
}

// ListAPITokens returns every token of one user, newest first.
func (d *DB) ListAPITokens(ctx context.Context, userID string) ([]*core.APIToken, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+apiTokenSelect+` FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, readErr("list api tokens", err)
	}
	defer rows.Close()

	var out []*core.APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, readErr("list api tokens", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list api tokens", err)
	}
	return out, nil
}

// DeleteAPIToken removes one token. The user id is part of the condition, so
// one user cannot revoke another user's token.
func (d *DB) DeleteAPIToken(ctx context.Context, id, userID string) error {
	res, err := d.sql.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID)
	return affected("delete api token", res, err)
}

// ListSystemAPITokens returns every token that belongs to no user, newest
// first.
func (d *DB) ListSystemAPITokens(ctx context.Context) ([]*core.APIToken, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+apiTokenSelect+` FROM api_tokens WHERE user_id IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, readErr("list system api tokens", err)
	}
	defer rows.Close()

	var out []*core.APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, readErr("list system api tokens", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list system api tokens", err)
	}
	return out, nil
}

// DeleteSystemAPIToken removes one token that belongs to no user. A user's
// token with the same id stays.
func (d *DB) DeleteSystemAPIToken(ctx context.Context, id string) error {
	res, err := d.sql.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE id = ? AND user_id IS NULL`, id)
	return affected("delete system api token", res, err)
}

// TouchAPIToken records the last time a token authenticated a request.
func (d *DB) TouchAPIToken(ctx context.Context, id string, at time.Time) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE api_tokens SET last_used = ? WHERE id = ?`, fmtTime(at), id)
	return affected("touch api token", res, err)
}

// Invites.

// inviteColumns is the SELECT list matching scanInvite.
const inviteColumns = `id, email, token, space_id, role, is_admin, created_by,
	created_at, expires_at, used_at`

// scanInvite reads one invites row.
func scanInvite(row rowScanner) (*core.Invite, error) {
	var (
		in                       core.Invite
		role                     string
		isAdmin                  int
		created, expires, usedAt string
	)
	err := row.Scan(&in.ID, &in.Email, &in.Token, &in.SpaceID, &role, &isAdmin,
		&in.CreatedBy, &created, &expires, &usedAt)
	if err != nil {
		return nil, err
	}
	in.Role = core.Role(role)
	in.IsAdmin = intBool(isAdmin)
	in.CreatedAt = parseTime(created)
	in.ExpiresAt = parseTime(expires)
	in.UsedAt = parseTimePtr(usedAt)
	return &in, nil
}

// CreateInvite inserts an invite. The token is stored in clear, because the
// recipient needs the link and the invite grants nothing on its own.
func (d *DB) CreateInvite(ctx context.Context, in *core.Invite) error {
	if in == nil {
		return fmt.Errorf("index: create invite: nil invite: %w", core.ErrInvalid)
	}
	if in.ID == "" {
		in.ID = core.NewID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	const q = `INSERT INTO invites (` + inviteColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.sql.ExecContext(ctx, q, in.ID, in.Email, in.Token, in.SpaceID,
		string(in.Role), boolInt(in.IsAdmin), in.CreatedBy,
		fmtTime(in.CreatedAt), fmtTime(in.ExpiresAt), fmtTimePtr(in.UsedAt))
	if err != nil {
		return writeErr("create invite", err)
	}
	return nil
}

// GetInviteByToken reads one invite by its token. Expiry and reuse are left
// to the caller to judge, because the two cases need different messages.
func (d *DB) GetInviteByToken(ctx context.Context, token string) (*core.Invite, error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT `+inviteColumns+` FROM invites WHERE token = ?`, token)
	in, err := scanInvite(row)
	if err != nil {
		return nil, readErr("get invite by token", err)
	}
	return in, nil
}

// ListInvites returns every invite, newest first, used ones included.
func (d *DB) ListInvites(ctx context.Context) ([]*core.Invite, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+inviteColumns+` FROM invites ORDER BY created_at DESC`)
	if err != nil {
		return nil, readErr("list invites", err)
	}
	defer rows.Close()

	var out []*core.Invite
	for rows.Next() {
		in, err := scanInvite(rows)
		if err != nil {
			return nil, readErr("list invites", err)
		}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list invites", err)
	}
	return out, nil
}

// DeleteInvite removes one invite.
func (d *DB) DeleteInvite(ctx context.Context, id string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM invites WHERE id = ?`, id)
	return affected("delete invite", res, err)
}

// MarkInviteUsed records that an invite was redeemed.
func (d *DB) MarkInviteUsed(ctx context.Context, id string, at time.Time) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE invites SET used_at = ? WHERE id = ?`, fmtTime(at), id)
	return affected("mark invite used", res, err)
}
