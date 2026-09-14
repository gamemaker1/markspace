package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// Plugin statuses.
const (
	PluginNeedsApproval = "needs_approval"
	PluginActive        = "active"
	PluginPaused        = "paused"
	PluginDown          = "down"
	PluginUninstalled   = "uninstalled"
)

var pluginStatuses = []string{PluginNeedsApproval, PluginActive, PluginPaused, PluginDown, PluginUninstalled}

// Plugin grant roles. A plugin never gets admin or owner on a space.
const (
	PluginRoleViewer    = "viewer"
	PluginRoleCommenter = "commenter"
	PluginRoleEditor    = "editor"
)

// User kinds. A plugin user cannot sign in.
const (
	UserKindPerson = "person"
	UserKindPlugin = "plugin"
)

// Plugin storage limits and timings from docs/plugins.md.
const (
	// PluginDownAfter is the number of failed health checks in a row that
	// marks an active plugin down.
	PluginDownAfter = 3
	// MaxKVValue is the largest key-value entry.
	MaxKVValue = 1 << 20
	// MaxKVTotal is the key-value quota per plugin.
	MaxKVTotal = 50 << 20
	// MaxBlobSize is the largest blob.
	MaxBlobSize = 256 << 20
	// MaxBlobTotal is the blob quota per plugin.
	MaxBlobTotal = 2 << 30
	// MaxStoreKey is the longest key-value or blob key in bytes.
	MaxStoreKey = 512
	// DefaultStoreList and MaxStoreList bound a key listing page.
	DefaultStoreList = 100
	MaxStoreList     = 1000
	// MaxIdempotencyKey is the longest Idempotency-Key header.
	MaxIdempotencyKey = 128
	// IdempotencyRetention is how long a stored response is replayed.
	IdempotencyRetention = 24 * time.Hour
	// IdempotencyPendingTimeout is how long a request in progress holds its
	// key. After it, the request counts as abandoned and a retry may run.
	IdempotencyPendingTimeout = 15 * time.Minute
	// ActingTokenTTL is the lifetime of an acting token.
	ActingTokenTTL = 10 * time.Minute
	// DeliveryTTL is how long an event delivery is retried.
	DeliveryTTL = 24 * time.Hour
	// DeliveryClaimLease is how long a claimed delivery stays hidden from
	// other claims. A delivery whose sender stops before it reports the
	// outcome is claimed again after the lease.
	DeliveryClaimLease = 30 * time.Second
	// DeliveryBackoffMin and DeliveryBackoffMax bound the retry delay.
	DeliveryBackoffMin = 5 * time.Second
	DeliveryBackoffMax = 10 * time.Minute
	// MaxDeliveryBatch is the most deliveries one claim returns.
	MaxDeliveryBatch = 100

	maxPluginError = 1024
	maxSecretName  = 128
)

// Plugin storage errors.
var (
	// ErrIdempotencyPending reports a repeated key whose first request is
	// still in progress.
	ErrIdempotencyPending = fmt.Errorf("idempotency key is in use by a request in progress: %w", core.ErrConflict)
	// ErrIdempotencyMismatch reports a repeated key sent with another method
	// or path.
	ErrIdempotencyMismatch = fmt.Errorf("idempotency key was used for another request: %w", core.ErrConflict)
)

// Plugin is one plugins row.
type Plugin struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Version          string          `json:"version"`
	BaseURL          string          `json:"baseUrl"`
	Status           string          `json:"status"`
	Manifest         json.RawMessage `json:"manifest"`
	ApprovedManifest json.RawMessage `json:"approvedManifest"`
	UserID           string          `json:"userId"`
	TokenHash        string          `json:"-"`
	TokenPrefix      string          `json:"tokenPrefix,omitempty"`
	// SigningSecret is the base64 secret, stored as is because signing
	// needs it.
	SigningSecret  string          `json:"-"`
	Permissions    []string        `json:"permissions"`
	Settings       json.RawMessage `json:"settings"`
	HealthFailures int             `json:"healthFailures"`
	LastHealthAt   time.Time       `json:"lastHealthAt,omitzero"`
	LastError      string          `json:"lastError,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

// PluginGrant gives a plugin a role on a space.
type PluginGrant struct {
	PluginID string `json:"pluginId,omitempty"`
	SpaceID  string `json:"spaceId"`
	Role     string `json:"role"`
}

// PluginSecretInfo names a stored secret without its value.
type PluginSecretInfo struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// PluginDelivery is one queued request to a plugin, usually an event batch.
type PluginDelivery struct {
	ID            string
	PluginID      string
	Path          string
	Body          []byte
	Attempts      int
	NextAttemptAt time.Time
	ExpiresAt     time.Time
	LastStatus    int
	CreatedAt     time.Time
}

// KVEntry is one key-value entry. Listings leave Value nil.
type KVEntry struct {
	Key       string    `json:"key"`
	Value     []byte    `json:"value,omitempty"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// BlobMeta describes a blob. The bytes live on disk under
// internal/plugins/<id>/blobs/, which the app manages.
type BlobMeta struct {
	PluginID  string    `json:"-"`
	Key       string    `json:"key"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// IdempotentResponse is a stored response for an Idempotency-Key.
type IdempotentResponse struct {
	Method    string
	Path      string
	Status    int
	Body      []byte
	CreatedAt time.Time
}

// ActingToken lets a plugin act for a user during one interaction.
type ActingToken struct {
	TokenHash   string
	PluginID    string
	UserID      string
	Interaction string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

// UninstallOptions choose which plugin data UninstallPlugin deletes. Data
// not chosen is kept for a later reinstall.
type UninstallOptions struct {
	DeleteKV         bool
	DeleteBlobs      bool
	DeleteGrants     bool
	DeleteDeliveries bool
	DeleteSecrets    bool
}

// Plugins.

// pluginColumns is the column list matching scanPlugin.
const pluginColumns = `id, name, version, base_url, status, manifest, approved_manifest, user_id,
	token_hash, token_prefix, signing_secret, permissions, settings, health_failures,
	last_health_at, last_error, created_at, updated_at`

func scanPlugin(row rowScanner) (*Plugin, error) {
	var (
		p                                                       Plugin
		manifest, approved, perms, settings, health, created, u string
	)
	err := row.Scan(&p.ID, &p.Name, &p.Version, &p.BaseURL, &p.Status, &manifest, &approved,
		&p.UserID, &p.TokenHash, &p.TokenPrefix, &p.SigningSecret, &perms, &settings,
		&p.HealthFailures, &health, &p.LastError, &created, &u)
	if err != nil {
		return nil, err
	}
	p.Manifest = json.RawMessage(manifest)
	p.ApprovedManifest = json.RawMessage(approved)
	p.Permissions = unmarshalStrings(perms)
	p.Settings = json.RawMessage(settings)
	p.LastHealthAt = parseTime(health)
	p.CreatedAt = parseTime(created)
	p.UpdatedAt = parseTime(u)
	return &p, nil
}

func pluginJSON(field string, v json.RawMessage) (string, error) {
	if len(v) == 0 {
		return "{}", nil
	}
	if !json.Valid(v) {
		return "", core.Invalid(field, "must be valid JSON")
	}
	return string(v), nil
}

// pluginWriteArgs validates p and returns the stored forms of its JSON
// columns.
func pluginWriteArgs(p *Plugin) (manifest, approved, settings string, err error) {
	switch {
	case !module.ValidIdent(p.ID):
		return "", "", "", core.Invalid("id", fmt.Sprintf("invalid plugin id %q", p.ID))
	case strings.TrimSpace(p.BaseURL) == "":
		return "", "", "", core.Invalid("baseUrl", "is required")
	case !slices.Contains(pluginStatuses, p.Status):
		return "", "", "", core.Invalid("status", fmt.Sprintf("unknown plugin status %q", p.Status))
	}
	if manifest, err = pluginJSON("manifest", p.Manifest); err != nil {
		return "", "", "", err
	}
	if approved, err = pluginJSON("approvedManifest", p.ApprovedManifest); err != nil {
		return "", "", "", err
	}
	if settings, err = pluginJSON("settings", p.Settings); err != nil {
		return "", "", "", err
	}
	return manifest, approved, settings, nil
}

// CreatePlugin inserts a plugin. An empty status becomes needs_approval and
// zero timestamps become now. A taken id or token hash returns
// core.ErrExists.
func (d *DB) CreatePlugin(ctx context.Context, p *Plugin) error {
	if p == nil {
		return fmt.Errorf("index: create plugin: nil plugin: %w", core.ErrInvalid)
	}
	if p.Status == "" {
		p.Status = PluginNeedsApproval
	}
	manifest, approved, settings, err := pluginWriteArgs(p)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	_, err = d.sql.ExecContext(ctx, `INSERT INTO plugins (`+pluginColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Version, p.BaseURL, p.Status, manifest, approved, p.UserID,
		p.TokenHash, p.TokenPrefix, p.SigningSecret, marshalStrings(p.Permissions), settings,
		p.HealthFailures, fmtTime(p.LastHealthAt), p.LastError, fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt))
	if err != nil {
		return writeErr("create plugin", err)
	}
	return nil
}

// UpdatePlugin writes the identity, manifests, token, signing secret,
// permissions and settings of a plugin and sets UpdatedAt to now. The health
// fields belong to RecordPluginHealth and are not written.
func (d *DB) UpdatePlugin(ctx context.Context, p *Plugin) error {
	if p == nil {
		return fmt.Errorf("index: update plugin: nil plugin: %w", core.ErrInvalid)
	}
	manifest, approved, settings, err := pluginWriteArgs(p)
	if err != nil {
		return err
	}
	p.UpdatedAt = time.Now().UTC()
	res, err := d.sql.ExecContext(ctx, `UPDATE plugins SET name = ?, version = ?, base_url = ?, status = ?,
		manifest = ?, approved_manifest = ?, user_id = ?, token_hash = ?, token_prefix = ?,
		signing_secret = ?, permissions = ?, settings = ?, updated_at = ?
		WHERE id = ?`,
		p.Name, p.Version, p.BaseURL, p.Status, manifest, approved, p.UserID, p.TokenHash,
		p.TokenPrefix, p.SigningSecret, marshalStrings(p.Permissions), settings, fmtTime(p.UpdatedAt), p.ID)
	return affected("update plugin "+p.ID, res, err)
}

// SetPluginStatus changes the status of a plugin.
func (d *DB) SetPluginStatus(ctx context.Context, id, status string, now time.Time) error {
	if !slices.Contains(pluginStatuses, status) {
		return core.Invalid("status", fmt.Sprintf("unknown plugin status %q", status))
	}
	res, err := d.sql.ExecContext(ctx, `UPDATE plugins SET status = ?, updated_at = ? WHERE id = ?`,
		status, fmtTime(now), id)
	return affected("set plugin status "+id, res, err)
}

// GetPlugin reads one plugin.
func (d *DB) GetPlugin(ctx context.Context, id string) (*Plugin, error) {
	p, err := scanPlugin(d.sql.QueryRowContext(ctx, `SELECT `+pluginColumns+` FROM plugins WHERE id = ?`, id))
	if err != nil {
		return nil, readErr("get plugin "+id, err)
	}
	return p, nil
}

// GetPluginByTokenHash finds the plugin that holds a plugin token. The
// caller checks the status. An uninstalled plugin has no token.
func (d *DB) GetPluginByTokenHash(ctx context.Context, hash string) (*Plugin, error) {
	if hash == "" {
		return nil, notFound("get plugin by token")
	}
	p, err := scanPlugin(d.sql.QueryRowContext(ctx,
		`SELECT `+pluginColumns+` FROM plugins WHERE token_hash = ?`, hash))
	if err != nil {
		return nil, readErr("get plugin by token", err)
	}
	return p, nil
}

// ListPlugins returns every plugin ordered by name, then id.
func (d *DB) ListPlugins(ctx context.Context) ([]*Plugin, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT `+pluginColumns+` FROM plugins ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, readErr("list plugins", err)
	}
	defer rows.Close()
	var out []*Plugin
	for rows.Next() {
		p, err := scanPlugin(rows)
		if err != nil {
			return nil, readErr("list plugins", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list plugins", err)
	}
	return out, nil
}

// DeletePlugin removes a plugin row with its grants, secrets, deliveries,
// key-value entries, blob metadata, idempotency keys and acting tokens.
func (d *DB) DeletePlugin(ctx context.Context, id string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM plugins WHERE id = ?`, id)
	return affected("delete plugin "+id, res, err)
}

// RecordPluginHealth records one health check at time at. A success resets
// the failure count, clears the error and turns a down plugin active. A
// failure counts up and turns an active plugin down at PluginDownAfter
// failures in a row. Paused, unapproved and uninstalled plugins keep their
// status.
func (d *DB) RecordPluginHealth(ctx context.Context, id string, ok bool, errText string, at time.Time) (*Plugin, error) {
	okN := boolInt(ok)
	if ok {
		errText = ""
	}
	errText = truncateUTF8(errText, maxPluginError)
	p, err := scanPlugin(d.sql.QueryRowContext(ctx, `UPDATE plugins SET
		status = CASE
			WHEN ? = 1 AND status = 'down' THEN 'active'
			WHEN ? = 0 AND status = 'active' AND health_failures + 1 >= ? THEN 'down'
			ELSE status END,
		health_failures = CASE WHEN ? = 1 THEN 0 ELSE health_failures + 1 END,
		last_error = ?, last_health_at = ?, updated_at = ?
		WHERE id = ?
		RETURNING `+pluginColumns,
		okN, okN, PluginDownAfter, okN, errText, fmtTime(at), fmtTime(at), id))
	if err != nil {
		return nil, readErr("record plugin health "+id, err)
	}
	return p, nil
}

// pluginExists returns a wrapped core.ErrNotFound for an unknown plugin.
func pluginExists(ctx context.Context, q jobQuerier, id string) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM plugins WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("index: plugin %q: %w", id, core.ErrNotFound)
	}
	if err != nil {
		return readErr("read plugin", err)
	}
	return nil
}

// affectedCount returns the number of rows a statement changed.
func affectedCount(what string, res sql.Result, err error) (int, error) {
	if err != nil {
		return 0, writeErr(what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("index: %s: rows affected: %w", what, err)
	}
	return int(n), nil
}

// Users.

// SetUserKind sets the kind of a user to person or plugin.
func (d *DB) SetUserKind(ctx context.Context, userID, kind string) error {
	if kind != UserKindPerson && kind != UserKindPlugin {
		return core.Invalid("kind", fmt.Sprintf("unknown user kind %q", kind))
	}
	res, err := d.sql.ExecContext(ctx, `UPDATE users SET kind = ? WHERE id = ?`, kind, userID)
	return affected("set user kind "+userID, res, err)
}

// UserKind returns the kind of a user.
func (d *DB) UserKind(ctx context.Context, userID string) (string, error) {
	var kind string
	if err := d.sql.QueryRowContext(ctx, `SELECT kind FROM users WHERE id = ?`, userID).Scan(&kind); err != nil {
		return "", readErr("get user kind "+userID, err)
	}
	return kind, nil
}

// UserCanSignIn reports whether a user may sign in by kind. Every login,
// session and API token path must refuse a user for whom it returns false.
// It does not check IsActive or the password.
func (d *DB) UserCanSignIn(ctx context.Context, userID string) (bool, error) {
	kind, err := d.UserKind(ctx, userID)
	if err != nil {
		return false, err
	}
	return kind == UserKindPerson, nil
}

// Grants.

// SetPluginGrants replaces the grants of a plugin. It returns core.ErrInvalid
// for a role other than viewer, commenter or editor and for a repeated
// space, and a wrapped core.ErrNotFound for an unknown plugin or space.
func (d *DB) SetPluginGrants(ctx context.Context, pluginID string, grants []PluginGrant) error {
	seen := map[string]bool{}
	for i, g := range grants {
		field := fmt.Sprintf("grants[%d]", i)
		switch {
		case g.Role != PluginRoleViewer && g.Role != PluginRoleCommenter && g.Role != PluginRoleEditor:
			return core.Invalid(field+".role", fmt.Sprintf("role %q cannot be granted to a plugin", g.Role))
		case g.SpaceID == "" || seen[g.SpaceID]:
			return core.Invalid(field+".spaceId", "each space must appear once")
		}
		seen[g.SpaceID] = true
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		if err := pluginExists(ctx, t, pluginID); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `DELETE FROM plugin_grants WHERE plugin_id = ?`, pluginID); err != nil {
			return writeErr("set plugin grants", err)
		}
		for _, g := range grants {
			var one int
			err := t.QueryRowContext(ctx, `SELECT 1 FROM spaces WHERE id = ?`, g.SpaceID).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("index: space %q: %w", g.SpaceID, core.ErrNotFound)
			}
			if err != nil {
				return readErr("set plugin grants", err)
			}
			_, err = t.ExecContext(ctx, `INSERT INTO plugin_grants (plugin_id, space_id, role) VALUES (?, ?, ?)`,
				pluginID, g.SpaceID, g.Role)
			if err != nil {
				return writeErr("set plugin grants", err)
			}
		}
		return nil
	})
}

// ListPluginGrants returns the grants of a plugin ordered by space id.
func (d *DB) ListPluginGrants(ctx context.Context, pluginID string) ([]PluginGrant, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT plugin_id, space_id, role FROM plugin_grants WHERE plugin_id = ? ORDER BY space_id`, pluginID)
	if err != nil {
		return nil, readErr("list plugin grants", err)
	}
	defer rows.Close()
	var out []PluginGrant
	for rows.Next() {
		var g PluginGrant
		if err := rows.Scan(&g.PluginID, &g.SpaceID, &g.Role); err != nil {
			return nil, readErr("list plugin grants", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list plugin grants", err)
	}
	return out, nil
}

// GetPluginGrant returns the role of a plugin on a space, or core.ErrNotFound
// when it has none.
func (d *DB) GetPluginGrant(ctx context.Context, pluginID, spaceID string) (string, error) {
	var role string
	err := d.sql.QueryRowContext(ctx,
		`SELECT role FROM plugin_grants WHERE plugin_id = ? AND space_id = ?`, pluginID, spaceID).Scan(&role)
	if err != nil {
		return "", readErr("get plugin grant", err)
	}
	return role, nil
}

// Secrets.

// SetPluginSecret stores or replaces a secret value.
func (d *DB) SetPluginSecret(ctx context.Context, pluginID, name, value string, now time.Time) error {
	if name == "" || len(name) > maxSecretName {
		return core.Invalid("name", fmt.Sprintf("must be 1 to %d bytes", maxSecretName))
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		if err := pluginExists(ctx, t, pluginID); err != nil {
			return err
		}
		_, err := t.ExecContext(ctx, `INSERT INTO plugin_secrets (plugin_id, name, value, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(plugin_id, name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			pluginID, name, value, fmtTime(now))
		if err != nil {
			return writeErr("set plugin secret", err)
		}
		return nil
	})
}

// GetPluginSecret returns a secret value.
func (d *DB) GetPluginSecret(ctx context.Context, pluginID, name string) (string, error) {
	var value string
	err := d.sql.QueryRowContext(ctx,
		`SELECT value FROM plugin_secrets WHERE plugin_id = ? AND name = ?`, pluginID, name).Scan(&value)
	if err != nil {
		return "", readErr("get plugin secret "+name, err)
	}
	return value, nil
}

// ListPluginSecrets names the stored secrets of a plugin, ordered by name.
func (d *DB) ListPluginSecrets(ctx context.Context, pluginID string) ([]PluginSecretInfo, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT name, updated_at FROM plugin_secrets WHERE plugin_id = ? ORDER BY name`, pluginID)
	if err != nil {
		return nil, readErr("list plugin secrets", err)
	}
	defer rows.Close()
	var out []PluginSecretInfo
	for rows.Next() {
		var (
			s  PluginSecretInfo
			at string
		)
		if err := rows.Scan(&s.Name, &at); err != nil {
			return nil, readErr("list plugin secrets", err)
		}
		s.UpdatedAt = parseTime(at)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list plugin secrets", err)
	}
	return out, nil
}

// DeletePluginSecret removes a secret.
func (d *DB) DeletePluginSecret(ctx context.Context, pluginID, name string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM plugin_secrets WHERE plugin_id = ? AND name = ?`, pluginID, name)
	return affected("delete plugin secret "+name, res, err)
}

// Deliveries.

// deliveryColumns is the column list matching scanDelivery.
const deliveryColumns = `id, plugin_id, path, body, attempts, next_attempt_at, expires_at, last_status, created_at`

func scanDelivery(row rowScanner) (PluginDelivery, error) {
	var (
		x                      PluginDelivery
		next, expires, created string
	)
	if err := row.Scan(&x.ID, &x.PluginID, &x.Path, &x.Body, &x.Attempts, &next, &expires, &x.LastStatus, &created); err != nil {
		return x, err
	}
	x.NextAttemptAt = parseTime(next)
	x.ExpiresAt = parseTime(expires)
	x.CreatedAt = parseTime(created)
	return x, nil
}

// DeliveryBackoff returns the retry delay after a failed attempt, which
// counts from 1. It starts at DeliveryBackoffMin and doubles up to
// DeliveryBackoffMax.
func DeliveryBackoff(attempts int) time.Duration {
	delay := DeliveryBackoffMin
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= DeliveryBackoffMax {
			return DeliveryBackoffMax
		}
	}
	return delay
}

// EnqueueDeliveries inserts a batch of deliveries in one transaction. Empty
// fields get defaults: ID core.NewID, CreatedAt now, NextAttemptAt CreatedAt,
// ExpiresAt CreatedAt plus DeliveryTTL. Attempts and LastStatus start at 0.
// The slice elements hold the stored values afterwards.
func (d *DB) EnqueueDeliveries(ctx context.Context, ds []PluginDelivery, now time.Time) error {
	now = now.UTC()
	for i := range ds {
		x := &ds[i]
		field := fmt.Sprintf("deliveries[%d]", i)
		switch {
		case x.PluginID == "":
			return core.Invalid(field+".pluginId", "is required")
		case !strings.HasPrefix(x.Path, "/"):
			return core.Invalid(field+".path", "must start with /")
		}
		if x.ID == "" {
			x.ID = core.NewID()
		}
		if x.CreatedAt.IsZero() {
			x.CreatedAt = now
		}
		if x.NextAttemptAt.IsZero() {
			x.NextAttemptAt = x.CreatedAt
		}
		if x.ExpiresAt.IsZero() {
			x.ExpiresAt = x.CreatedAt.Add(DeliveryTTL)
		}
		x.CreatedAt, x.NextAttemptAt, x.ExpiresAt = x.CreatedAt.UTC(), x.NextAttemptAt.UTC(), x.ExpiresAt.UTC()
		x.Attempts, x.LastStatus = 0, 0
	}
	if len(ds) == 0 {
		return nil
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		checked := map[string]bool{}
		for _, x := range ds {
			if !checked[x.PluginID] {
				if err := pluginExists(ctx, t, x.PluginID); err != nil {
					return err
				}
				checked[x.PluginID] = true
			}
			_, err := t.ExecContext(ctx, `INSERT INTO plugin_deliveries (`+deliveryColumns+`)
				VALUES (?, ?, ?, COALESCE(?, x''), 0, ?, ?, 0, ?)`,
				x.ID, x.PluginID, x.Path, x.Body, fmtTime(x.NextAttemptAt), fmtTime(x.ExpiresAt), fmtTime(x.CreatedAt))
			if err != nil {
				return writeErr("enqueue delivery", err)
			}
		}
		return nil
	})
}

// ClaimDeliveries returns up to limit due deliveries of one plugin, ordered
// by NextAttemptAt, then CreatedAt, then ID. A limit outside 1 to
// MaxDeliveryBatch means MaxDeliveryBatch. Expired deliveries are skipped.
// Each claimed delivery counts an attempt and is hidden from claims for
// DeliveryClaimLease. The returned values reflect both changes.
func (d *DB) ClaimDeliveries(ctx context.Context, pluginID string, now time.Time, limit int) ([]PluginDelivery, error) {
	if limit <= 0 || limit > MaxDeliveryBatch {
		limit = MaxDeliveryBatch
	}
	now = now.UTC()
	nowS := fmtTime(now)
	lease := now.Add(DeliveryClaimLease)
	var out []PluginDelivery
	err := d.tx(ctx, func(t *sql.Tx) error {
		rows, err := t.QueryContext(ctx, `SELECT `+deliveryColumns+` FROM plugin_deliveries
			WHERE plugin_id = ? AND next_attempt_at <= ? AND expires_at > ?
			ORDER BY next_attempt_at, created_at, id
			LIMIT ?`, pluginID, nowS, nowS, limit)
		if err != nil {
			return readErr("claim deliveries", err)
		}
		for rows.Next() {
			x, err := scanDelivery(rows)
			if err != nil {
				rows.Close()
				return readErr("claim deliveries", err)
			}
			out = append(out, x)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return readErr("claim deliveries", err)
		}
		for i := range out {
			out[i].Attempts++
			out[i].NextAttemptAt = lease
			_, err := t.ExecContext(ctx, `UPDATE plugin_deliveries SET attempts = ?, next_attempt_at = ? WHERE id = ?`,
				out[i].Attempts, fmtTime(lease), out[i].ID)
			if err != nil {
				return writeErr("claim deliveries", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CompleteDeliveries deletes delivered rows. Unknown ids are ignored.
func (d *DB) CompleteDeliveries(ctx context.Context, ids []string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		for chunk := range slices.Chunk(ids, deleteChunk) {
			_, err := t.ExecContext(ctx,
				`DELETE FROM plugin_deliveries WHERE id IN (`+placeholders(len(chunk))+`)`, stringArgs(chunk)...)
			if err != nil {
				return writeErr("complete deliveries", err)
			}
		}
		return nil
	})
}

// FailDeliveries records a failed attempt of each delivery. The next attempt
// waits DeliveryBackoff of the attempt count. lastStatus is the HTTP status,
// or 0 when no response arrived. Unknown ids are ignored.
func (d *DB) FailDeliveries(ctx context.Context, ids []string, lastStatus int, now time.Time) error {
	now = now.UTC()
	return d.tx(ctx, func(t *sql.Tx) error {
		for chunk := range slices.Chunk(ids, deleteChunk) {
			rows, err := t.QueryContext(ctx, `SELECT id, attempts FROM plugin_deliveries WHERE id IN (`+
				placeholders(len(chunk))+`)`, stringArgs(chunk)...)
			if err != nil {
				return readErr("fail deliveries", err)
			}
			attempts := map[string]int{}
			for rows.Next() {
				var (
					id string
					n  int
				)
				if err := rows.Scan(&id, &n); err != nil {
					rows.Close()
					return readErr("fail deliveries", err)
				}
				attempts[id] = n
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return readErr("fail deliveries", err)
			}
			for id, n := range attempts {
				_, err := t.ExecContext(ctx, `UPDATE plugin_deliveries SET last_status = ?, next_attempt_at = ? WHERE id = ?`,
					lastStatus, fmtTime(now.Add(DeliveryBackoff(n))), id)
				if err != nil {
					return writeErr("fail deliveries", err)
				}
			}
		}
		return nil
	})
}

// ExpireDeliveries deletes every delivery whose ExpiresAt is not after now
// and returns the number deleted.
func (d *DB) ExpireDeliveries(ctx context.Context, now time.Time) (int, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM plugin_deliveries WHERE expires_at <= ?`, fmtTime(now))
	return affectedCount("expire deliveries", res, err)
}

// CountDeliveries counts the queued deliveries of a plugin.
func (d *DB) CountDeliveries(ctx context.Context, pluginID string) (int, error) {
	var n int
	err := d.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_deliveries WHERE plugin_id = ?`, pluginID).Scan(&n)
	if err != nil {
		return 0, readErr("count deliveries", err)
	}
	return n, nil
}

// CountDeliveriesByPlugin counts queued deliveries for every plugin that has
// any, for the admin page.
func (d *DB) CountDeliveriesByPlugin(ctx context.Context) (map[string]int, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT plugin_id, COUNT(*) FROM plugin_deliveries GROUP BY plugin_id`)
	if err != nil {
		return nil, readErr("count deliveries", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var (
			id string
			n  int
		)
		if err := rows.Scan(&id, &n); err != nil {
			return nil, readErr("count deliveries", err)
		}
		out[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("count deliveries", err)
	}
	return out, nil
}

// NextDeliveryAt returns the earliest NextAttemptAt among the unexpired
// deliveries of a plugin, or the zero time when there are none. The sender
// sleeps until then.
func (d *DB) NextDeliveryAt(ctx context.Context, pluginID string, now time.Time) (time.Time, error) {
	var next sql.NullString
	err := d.sql.QueryRowContext(ctx, `SELECT MIN(next_attempt_at) FROM plugin_deliveries
		WHERE plugin_id = ? AND expires_at > ?`, pluginID, fmtTime(now)).Scan(&next)
	if err != nil {
		return time.Time{}, readErr("next delivery", err)
	}
	return parseTime(next.String), nil
}

// Key-value store.

func validStoreKey(key string) error {
	if key == "" || len(key) > MaxStoreKey || !utf8.ValidString(key) || strings.ContainsRune(key, 0) {
		return core.Invalid("key", fmt.Sprintf("must be 1 to %d bytes of UTF-8 without NUL", MaxStoreKey))
	}
	return nil
}

func listLimit(limit int) int {
	if limit <= 0 || limit > MaxStoreList {
		return DefaultStoreList
	}
	return limit
}

// keyRange builds the prefix and cursor conditions of a key listing. A key
// with the prefix sorts at or after the prefix, so the first condition lets
// SQLite seek the primary key.
func keyRange(prefix, cursor string) (string, []any) {
	cond := ""
	var args []any
	if prefix != "" {
		cond += ` AND key >= ? AND substr(key, 1, length(?)) = ?`
		args = append(args, prefix, prefix, prefix)
	}
	if cursor != "" {
		cond += ` AND key > ?`
		args = append(args, cursor)
	}
	return cond, args
}

// PutKV stores a value. It returns core.ErrTooLarge for a value over
// MaxKVValue and core.ErrQuotaExceeded when the plugin would hold more than
// MaxKVTotal bytes. The replaced value of the same key does not count.
func (d *DB) PutKV(ctx context.Context, pluginID, key string, value []byte, now time.Time) error {
	if err := validStoreKey(key); err != nil {
		return err
	}
	if len(value) > MaxKVValue {
		return fmt.Errorf("index: kv value of %d bytes exceeds %d: %w", len(value), MaxKVValue, core.ErrTooLarge)
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		if err := pluginExists(ctx, t, pluginID); err != nil {
			return err
		}
		var used int64
		err := t.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM plugin_kv WHERE plugin_id = ? AND key <> ?`,
			pluginID, key).Scan(&used)
		if err != nil {
			return readErr("kv usage", err)
		}
		if used+int64(len(value)) > MaxKVTotal {
			return fmt.Errorf("index: plugin %q kv store would hold %d bytes, limit %d: %w",
				pluginID, used+int64(len(value)), MaxKVTotal, core.ErrQuotaExceeded)
		}
		_, err = t.ExecContext(ctx, `INSERT INTO plugin_kv (plugin_id, key, size, updated_at, value)
			VALUES (?, ?, ?, ?, COALESCE(?, x''))
			ON CONFLICT(plugin_id, key) DO UPDATE SET size = excluded.size, updated_at = excluded.updated_at,
				value = excluded.value`,
			pluginID, key, len(value), fmtTime(now), value)
		if err != nil {
			return writeErr("put kv", err)
		}
		return nil
	})
}

// GetKV reads one entry with its value.
func (d *DB) GetKV(ctx context.Context, pluginID, key string) (*KVEntry, error) {
	var (
		e  KVEntry
		at string
	)
	err := d.sql.QueryRowContext(ctx, `SELECT key, size, updated_at, value FROM plugin_kv WHERE plugin_id = ? AND key = ?`,
		pluginID, key).Scan(&e.Key, &e.Size, &at, &e.Value)
	if err != nil {
		return nil, readErr("get kv", err)
	}
	if e.Value == nil {
		e.Value = []byte{}
	}
	e.UpdatedAt = parseTime(at)
	return &e, nil
}

// DeleteKV removes one entry.
func (d *DB) DeleteKV(ctx context.Context, pluginID, key string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM plugin_kv WHERE plugin_id = ? AND key = ?`, pluginID, key)
	return affected("delete kv", res, err)
}

// ListKV returns entries without values in key order, starting after cursor
// and limited to keys with prefix. next is the cursor of the following page,
// or empty on the last page.
func (d *DB) ListKV(ctx context.Context, pluginID, prefix, cursor string, limit int) (entries []KVEntry, next string, err error) {
	limit = listLimit(limit)
	cond, args := keyRange(prefix, cursor)
	rows, err := d.sql.QueryContext(ctx, `SELECT key, size, updated_at FROM plugin_kv WHERE plugin_id = ?`+cond+
		` ORDER BY key LIMIT ?`, append(append([]any{pluginID}, args...), limit+1)...)
	if err != nil {
		return nil, "", readErr("list kv", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			e  KVEntry
			at string
		)
		if err := rows.Scan(&e.Key, &e.Size, &at); err != nil {
			return nil, "", readErr("list kv", err)
		}
		e.UpdatedAt = parseTime(at)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", readErr("list kv", err)
	}
	if len(entries) > limit {
		entries = entries[:limit]
		next = entries[limit-1].Key
	}
	return entries, next, nil
}

// KVUsage returns the number of entries and bytes a plugin stores.
func (d *DB) KVUsage(ctx context.Context, pluginID string) (count int, bytes int64, err error) {
	err = d.sql.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size), 0) FROM plugin_kv WHERE plugin_id = ?`,
		pluginID).Scan(&count, &bytes)
	if err != nil {
		return 0, 0, readErr("kv usage", err)
	}
	return count, bytes, nil
}

// Blob metadata.

func checkBlobQuota(ctx context.Context, q jobQuerier, pluginID, key string, size int64) error {
	if size < 0 {
		return core.Invalid("size", "must not be negative")
	}
	if size > MaxBlobSize {
		return fmt.Errorf("index: blob of %d bytes exceeds %d: %w", size, MaxBlobSize, core.ErrTooLarge)
	}
	var used int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM plugin_blobs WHERE plugin_id = ? AND key <> ?`,
		pluginID, key).Scan(&used)
	if err != nil {
		return readErr("blob usage", err)
	}
	if used+size > MaxBlobTotal {
		return fmt.Errorf("index: plugin %q blob store would hold %d bytes, limit %d: %w",
			pluginID, used+size, int64(MaxBlobTotal), core.ErrQuotaExceeded)
	}
	return nil
}

// CheckBlobQuota reports whether a blob of size bytes may be stored under
// key before the app writes the bytes. It returns core.ErrTooLarge over
// MaxBlobSize and core.ErrQuotaExceeded over MaxBlobTotal. The replaced blob
// of the same key does not count. PutBlobMeta checks again atomically.
func (d *DB) CheckBlobQuota(ctx context.Context, pluginID, key string, size int64) error {
	if err := validStoreKey(key); err != nil {
		return err
	}
	return checkBlobQuota(ctx, d.sql, pluginID, key, size)
}

// PutBlobMeta stores or replaces the metadata of a blob after its bytes are
// written. SHA256 is the lowercase hex digest.
func (d *DB) PutBlobMeta(ctx context.Context, m BlobMeta) error {
	if err := validStoreKey(m.Key); err != nil {
		return err
	}
	if len(m.SHA256) != 64 || strings.Trim(m.SHA256, "0123456789abcdef") != "" {
		return core.Invalid("sha256", "must be 64 lowercase hex digits")
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		if err := pluginExists(ctx, t, m.PluginID); err != nil {
			return err
		}
		if err := checkBlobQuota(ctx, t, m.PluginID, m.Key, m.Size); err != nil {
			return err
		}
		_, err := t.ExecContext(ctx, `INSERT INTO plugin_blobs (plugin_id, key, size, sha256, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(plugin_id, key) DO UPDATE SET size = excluded.size, sha256 = excluded.sha256,
				updated_at = excluded.updated_at`,
			m.PluginID, m.Key, m.Size, m.SHA256, fmtTime(m.UpdatedAt))
		if err != nil {
			return writeErr("put blob", err)
		}
		return nil
	})
}

// GetBlobMeta reads the metadata of one blob.
func (d *DB) GetBlobMeta(ctx context.Context, pluginID, key string) (*BlobMeta, error) {
	m := BlobMeta{PluginID: pluginID}
	var at string
	err := d.sql.QueryRowContext(ctx, `SELECT key, size, sha256, updated_at FROM plugin_blobs WHERE plugin_id = ? AND key = ?`,
		pluginID, key).Scan(&m.Key, &m.Size, &m.SHA256, &at)
	if err != nil {
		return nil, readErr("get blob", err)
	}
	m.UpdatedAt = parseTime(at)
	return &m, nil
}

// DeleteBlobMeta removes the metadata of one blob. The app removes the bytes.
func (d *DB) DeleteBlobMeta(ctx context.Context, pluginID, key string) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM plugin_blobs WHERE plugin_id = ? AND key = ?`, pluginID, key)
	return affected("delete blob", res, err)
}

// ListBlobMeta lists blob metadata like ListKV lists entries.
func (d *DB) ListBlobMeta(ctx context.Context, pluginID, prefix, cursor string, limit int) (blobs []BlobMeta, next string, err error) {
	limit = listLimit(limit)
	cond, args := keyRange(prefix, cursor)
	rows, err := d.sql.QueryContext(ctx, `SELECT key, size, sha256, updated_at FROM plugin_blobs WHERE plugin_id = ?`+cond+
		` ORDER BY key LIMIT ?`, append(append([]any{pluginID}, args...), limit+1)...)
	if err != nil {
		return nil, "", readErr("list blobs", err)
	}
	defer rows.Close()
	for rows.Next() {
		m := BlobMeta{PluginID: pluginID}
		var at string
		if err := rows.Scan(&m.Key, &m.Size, &m.SHA256, &at); err != nil {
			return nil, "", readErr("list blobs", err)
		}
		m.UpdatedAt = parseTime(at)
		blobs = append(blobs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", readErr("list blobs", err)
	}
	if len(blobs) > limit {
		blobs = blobs[:limit]
		next = blobs[limit-1].Key
	}
	return blobs, next, nil
}

// BlobUsage returns the number of blobs and bytes a plugin stores.
func (d *DB) BlobUsage(ctx context.Context, pluginID string) (count int, bytes int64, err error) {
	err = d.sql.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size), 0) FROM plugin_blobs WHERE plugin_id = ?`,
		pluginID).Scan(&count, &bytes)
	if err != nil {
		return 0, 0, readErr("blob usage", err)
	}
	return count, bytes, nil
}

// Idempotency keys.

// BeginIdempotent reserves an Idempotency-Key for a request. It returns nil
// and no error when the caller should run the request and then call
// CompleteIdempotent or ReleaseIdempotent. It returns the stored response
// when the key already completed. A key used with another method or path
// returns ErrIdempotencyMismatch. A key whose first request is still in
// progress returns ErrIdempotencyPending. Both wrap core.ErrConflict. A row
// older than IdempotencyRetention, or pending for longer than
// IdempotencyPendingTimeout, is replaced.
func (d *DB) BeginIdempotent(ctx context.Context, pluginID, key, method, path string, now time.Time) (*IdempotentResponse, error) {
	if key == "" || len(key) > MaxIdempotencyKey {
		return nil, core.Invalid("Idempotency-Key", fmt.Sprintf("must be 1 to %d characters", MaxIdempotencyKey))
	}
	now = now.UTC()
	var out *IdempotentResponse
	err := d.tx(ctx, func(t *sql.Tx) error {
		if err := pluginExists(ctx, t, pluginID); err != nil {
			return err
		}
		_, err := t.ExecContext(ctx, `DELETE FROM plugin_idempotency WHERE plugin_id = ? AND key = ?
			AND (created_at <= ? OR (status = 0 AND created_at <= ?))`,
			pluginID, key, fmtTime(now.Add(-IdempotencyRetention)), fmtTime(now.Add(-IdempotencyPendingTimeout)))
		if err != nil {
			return writeErr("begin idempotent request", err)
		}
		res, err := t.ExecContext(ctx, `INSERT INTO plugin_idempotency (plugin_id, key, method, path, status, created_at, body)
			VALUES (?, ?, ?, ?, 0, ?, x'') ON CONFLICT(plugin_id, key) DO NOTHING`,
			pluginID, key, method, path, fmtTime(now))
		n, err := affectedCount("begin idempotent request", res, err)
		if err != nil || n == 1 {
			return err
		}
		var (
			r       IdempotentResponse
			created string
		)
		err = t.QueryRowContext(ctx, `SELECT method, path, status, body, created_at FROM plugin_idempotency
			WHERE plugin_id = ? AND key = ?`, pluginID, key).Scan(&r.Method, &r.Path, &r.Status, &r.Body, &created)
		if err != nil {
			return readErr("begin idempotent request", err)
		}
		switch {
		case r.Method != method || r.Path != path:
			return fmt.Errorf("index: key %q was used for %s %s: %w", key, r.Method, r.Path, ErrIdempotencyMismatch)
		case r.Status == 0:
			return fmt.Errorf("index: key %q: %w", key, ErrIdempotencyPending)
		}
		r.CreatedAt = parseTime(created)
		out = &r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CompleteIdempotent stores the response of a reserved key. It returns
// core.ErrNotFound for a key with no row and core.ErrConflict for a key that
// already completed.
func (d *DB) CompleteIdempotent(ctx context.Context, pluginID, key string, status int, body []byte) error {
	if status < 100 || status > 599 {
		return core.Invalid("status", "must be an HTTP status")
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		res, err := t.ExecContext(ctx, `UPDATE plugin_idempotency SET status = ?, body = COALESCE(?, x'')
			WHERE plugin_id = ? AND key = ? AND status = 0`, status, body, pluginID, key)
		n, err := affectedCount("complete idempotent request", res, err)
		if err != nil || n == 1 {
			return err
		}
		var one int
		err = t.QueryRowContext(ctx, `SELECT 1 FROM plugin_idempotency WHERE plugin_id = ? AND key = ?`, pluginID, key).Scan(&one)
		if err != nil {
			return readErr("complete idempotent request", err)
		}
		return fmt.Errorf("index: key %q already completed: %w", key, core.ErrConflict)
	})
}

// ReleaseIdempotent deletes a pending key, so a retry may run the request.
// The app calls it when a request fails in a way that must not be replayed.
// A completed key is kept.
func (d *DB) ReleaseIdempotent(ctx context.Context, pluginID, key string) error {
	_, err := d.sql.ExecContext(ctx, `DELETE FROM plugin_idempotency WHERE plugin_id = ? AND key = ? AND status = 0`,
		pluginID, key)
	if err != nil {
		return writeErr("release idempotent request", err)
	}
	return nil
}

// PurgeIdempotency deletes keys older than IdempotencyRetention and returns
// the number deleted.
func (d *DB) PurgeIdempotency(ctx context.Context, now time.Time) (int, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM plugin_idempotency WHERE created_at <= ?`,
		fmtTime(now.Add(-IdempotencyRetention)))
	return affectedCount("purge idempotency keys", res, err)
}

// Acting tokens.

// CreateActingToken stores an acting token. CreatedAt becomes now. A zero
// ExpiresAt, or one later than now plus ActingTokenTTL, becomes now plus
// ActingTokenTTL.
func (d *DB) CreateActingToken(ctx context.Context, tok *ActingToken, now time.Time) error {
	if tok == nil || tok.TokenHash == "" || tok.PluginID == "" || tok.UserID == "" {
		return fmt.Errorf("index: create acting token: token hash, plugin and user are required: %w", core.ErrInvalid)
	}
	now = now.UTC()
	tok.CreatedAt = now
	if limit := now.Add(ActingTokenTTL); tok.ExpiresAt.IsZero() || tok.ExpiresAt.After(limit) {
		tok.ExpiresAt = limit
	}
	tok.ExpiresAt = tok.ExpiresAt.UTC()
	return d.tx(ctx, func(t *sql.Tx) error {
		if err := pluginExists(ctx, t, tok.PluginID); err != nil {
			return err
		}
		_, err := t.ExecContext(ctx, `INSERT INTO acting_tokens (token_hash, plugin_id, user_id, interaction, expires_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			tok.TokenHash, tok.PluginID, tok.UserID, tok.Interaction, fmtTime(tok.ExpiresAt), fmtTime(tok.CreatedAt))
		if err != nil {
			return writeErr("create acting token", err)
		}
		return nil
	})
}

// ResolveActingToken returns the token with the hash when it expires after
// now. An unknown or expired token returns core.ErrNotFound.
func (d *DB) ResolveActingToken(ctx context.Context, hash string, now time.Time) (*ActingToken, error) {
	var (
		tok              ActingToken
		expires, created string
	)
	err := d.sql.QueryRowContext(ctx, `SELECT token_hash, plugin_id, user_id, interaction, expires_at, created_at
		FROM acting_tokens WHERE token_hash = ? AND expires_at > ?`, hash, fmtTime(now)).
		Scan(&tok.TokenHash, &tok.PluginID, &tok.UserID, &tok.Interaction, &expires, &created)
	if err != nil {
		return nil, readErr("resolve acting token", err)
	}
	tok.ExpiresAt = parseTime(expires)
	tok.CreatedAt = parseTime(created)
	return &tok, nil
}

// RevokeActingTokens deletes every acting token of a plugin and returns the
// number deleted.
func (d *DB) RevokeActingTokens(ctx context.Context, pluginID string) (int, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM acting_tokens WHERE plugin_id = ?`, pluginID)
	return affectedCount("revoke acting tokens", res, err)
}

// PurgeActingTokens deletes tokens that expire at or before now and returns
// the number deleted.
func (d *DB) PurgeActingTokens(ctx context.Context, now time.Time) (int, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM acting_tokens WHERE expires_at <= ?`, fmtTime(now))
	return affectedCount("purge acting tokens", res, err)
}

// Uninstall.

// UninstallPlugin marks a plugin uninstalled in one transaction. It always
// clears the token hash and token prefix, resets the health count, and
// deletes acting tokens and idempotency keys. It keeps the signing secret,
// because a later install signs its install call with it. opts choose which
// other data goes. The plugins row stays. The app stops schedules, cancels
// jobs and removes blob files.
func (d *DB) UninstallPlugin(ctx context.Context, id string, opts UninstallOptions, now time.Time) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		res, err := t.ExecContext(ctx, `UPDATE plugins SET status = 'uninstalled', token_hash = '', token_prefix = '',
			health_failures = 0, updated_at = ? WHERE id = ?`, fmtTime(now), id)
		if err := affected("uninstall plugin "+id, res, err); err != nil {
			return err
		}
		tables := []string{"acting_tokens", "plugin_idempotency"}
		for _, o := range []struct {
			on    bool
			table string
		}{
			{opts.DeleteKV, "plugin_kv"},
			{opts.DeleteBlobs, "plugin_blobs"},
			{opts.DeleteGrants, "plugin_grants"},
			{opts.DeleteDeliveries, "plugin_deliveries"},
			{opts.DeleteSecrets, "plugin_secrets"},
		} {
			if o.on {
				tables = append(tables, o.table)
			}
		}
		for _, table := range tables {
			if _, err := t.ExecContext(ctx, `DELETE FROM `+table+` WHERE plugin_id = ?`, id); err != nil {
				return writeErr("uninstall plugin "+id, err)
			}
		}
		return nil
	})
}
