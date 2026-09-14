// Package plugins is the built-in module of the plugin host. It serves the
// host API under /api/plugin/v1/, the admin API under /api/admin/plugins and
// the admin page /admin/plugins. The protocol, the delivery queue, tokens and
// storage live behind the Host interface, which internal/plugin implements.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// ModuleID is the id of this module.
const ModuleID = "plugins"

// Grant is one space a plugin may use.
type Grant struct {
	SpaceID   string    `json:"spaceId"`
	SpaceName string    `json:"spaceName,omitempty"`
	Role      core.Role `json:"role"`
}

// SecretView names a manifest secret and whether a value is stored.
type SecretView struct {
	Name      string    `json:"name"`
	Label     string    `json:"label,omitempty"`
	Set       bool      `json:"set"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

// Contribution is a job, schedule, command, panel or importer from a
// manifest, for display.
type Contribution struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Extra string `json:"extra,omitempty"`
}

// ManifestSummary is the part of a manifest the admin reviews.
type ManifestSummary struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description,omitempty"`
	Homepage    string          `json:"homepage,omitempty"`
	Permissions []string        `json:"permissions"`
	Events      []string        `json:"events"`
	Jobs        []Contribution  `json:"jobs"`
	Schedules   []Contribution  `json:"schedules"`
	Commands    []Contribution  `json:"commands"`
	Panels      []Contribution  `json:"panels"`
	Importers   []Contribution  `json:"importers"`
	Secrets     []SecretView    `json:"secrets"`
	Settings    json.RawMessage `json:"settings,omitempty"` // the settings fields as JSON
}

// PluginView is one plugin as the admin page and API show it.
type PluginView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	BaseURL string `json:"baseUrl"`
	// Status is active, down, paused, needs_approval or uninstalled.
	Status   string          `json:"status"`
	UserID   string          `json:"userId"`
	Approved ManifestSummary `json:"approved"`
	// Pending is the newer manifest that waits for approval, when the status
	// is needs_approval.
	Pending          *ManifestSummary `json:"pending,omitempty"`
	Grants           []Grant          `json:"grants"`
	Settings         json.RawMessage  `json:"settings"`
	Secrets          []SecretView     `json:"secrets"`
	QueuedDeliveries int              `json:"queuedDeliveries"`
	// AI usage of the current UTC month.
	AITokensMonth     int64     `json:"aiTokensMonth"`
	AICostMicrosMonth int64     `json:"aiCostMicrosMonth"`
	AICallsMonth      int64     `json:"aiCallsMonth"`
	HealthFailures    int       `json:"healthFailures"`
	LastHealthAt      time.Time `json:"lastHealthAt,omitzero"`
	LastError         string    `json:"lastError,omitempty"`
	TokenPrefix       string    `json:"tokenPrefix,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// Preview is a fetched manifest before install.
type Preview struct {
	BaseURL  string          `json:"baseUrl"`
	Manifest ManifestSummary `json:"manifest"`
	// SHA256 is the digest of the manifest bytes. Install refuses a manifest
	// that changed since the preview.
	SHA256 string `json:"sha256"`
	// Installed is set when a plugin with this id exists and is not
	// uninstalled.
	Installed bool `json:"installed"`
}

// InstallInput approves a previewed plugin.
type InstallInput struct {
	BaseURL  string            `json:"baseUrl"`
	SHA256   string            `json:"sha256,omitempty"`
	Grants   []Grant           `json:"grants"`
	Secrets  map[string]string `json:"secrets,omitempty"`
	Settings map[string]any    `json:"settings,omitempty"`
}

// InstallResult is the outcome of an install. When the install call to the
// plugin failed, Token and SigningSecret hold the credentials, shown once.
type InstallResult struct {
	Plugin        PluginView `json:"plugin"`
	Delivered     bool       `json:"delivered"`
	InstallError  string     `json:"installError,omitempty"`
	Token         string     `json:"token,omitempty"`
	SigningSecret string     `json:"signingSecret,omitempty"`
	MarkspaceURL  string     `json:"markspaceUrl,omitempty"`
}

// ConfigInput changes grants, settings or secrets. Nil fields stay. An empty
// secret value deletes the secret.
type ConfigInput struct {
	Grants   *[]Grant          `json:"grants,omitempty"`
	Settings map[string]any    `json:"settings,omitempty"`
	Secrets  map[string]string `json:"secrets,omitempty"`
}

// StoredResponse is a replayed response for an Idempotency-Key.
type StoredResponse struct {
	Status int
	Body   []byte
}

// KVEntry is one key-value entry. Listings leave Value empty.
type KVEntry struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value,omitempty"`
	Size      int64           `json:"size"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// BlobInfo describes a stored blob.
type BlobInfo struct {
	Key         string    `json:"key"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	ContentType string    `json:"contentType,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// JobInput is the body of POST /jobs.
type JobInput struct {
	Kind     string          `json:"kind"`
	Title    string          `json:"title,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	SpaceID  string          `json:"spaceId,omitempty"`
	RunAfter *time.Time      `json:"runAfter,omitempty"`
	ParentID string          `json:"parentId,omitempty"`
}

// JobUpdate is the body of PATCH /jobs/{id}.
type JobUpdate struct {
	Progress *int64          `json:"progress,omitempty"`
	Total    *int64          `json:"total,omitempty"`
	Message  *string         `json:"message,omitempty"`
	Status   string          `json:"status,omitempty"` // done or failed
	Result   json.RawMessage `json:"result,omitempty"`
}

// Notification is the body of POST /notify.
type Notification struct {
	UserIDs []string `json:"userIds,omitempty"`
	SpaceID string   `json:"spaceId,omitempty"`
	Message string   `json:"message"`
	Link    string   `json:"link,omitempty"`
}

// FetchInput is the body of POST /web/fetch.
type FetchInput struct {
	URL           string `json:"url"`
	RespectRobots bool   `json:"respectRobots,omitempty"`
	IncludeHTML   bool   `json:"includeHtml,omitempty"`
}

// FetchResult is the reply of POST /web/fetch. A text response fills Text
// and Markdown, and an HTML one also Title. Any other type, such as a PDF,
// fills BodyBase64 instead. SHA256 is the digest of the response body.
type FetchResult struct {
	FinalURL    string    `json:"finalUrl"`
	Status      int       `json:"status"`
	ContentType string    `json:"contentType"`
	Title       string    `json:"title"`
	Text        string    `json:"text"`
	Markdown    string    `json:"markdown"`
	HTML        string    `json:"html,omitempty"`
	BodyBase64  string    `json:"bodyBase64,omitempty"`
	SHA256      string    `json:"sha256"`
	Truncated   bool      `json:"truncated,omitempty"`
	FetchedAt   time.Time `json:"fetchedAt"`
}

// ErrRobotsDisallowed is returned by WebFetch when robots.txt refuses the
// URL. The host API answers it with 403 and the code robots_disallowed.
var ErrRobotsDisallowed = errors.New("robots.txt disallows this URL")

// SettingsUpdatedEvent is the type of the event a plugin receives when an
// admin changes its settings.
const SettingsUpdatedEvent = "plugin.settings.updated"

// Host is the plugin host behind the routes of this module. Every method
// that takes a plugin actor checks that the actor belongs to the plugin.
// Admin methods do not check the actor, the routes do.
type Host interface {
	// Admin.
	List(ctx context.Context) ([]PluginView, error)
	Get(ctx context.Context, id string) (*PluginView, error)
	Preview(ctx context.Context, baseURL string) (*Preview, error)
	Install(ctx context.Context, in InstallInput) (*InstallResult, error)
	Configure(ctx context.Context, id string, in ConfigInput) (*PluginView, error)
	SetPaused(ctx context.Context, id string, paused bool) (*PluginView, error)
	// CheckManifest fetches the manifest again. More permissions, events or
	// secrets set needs_approval and keep the old grant. Anything else is
	// applied at once.
	CheckManifest(ctx context.Context, id string) (*PluginView, error)
	// Approve applies a pending manifest.
	Approve(ctx context.Context, id string) (*PluginView, error)
	// Uninstall revokes tokens, stops deliveries and schedules, cancels jobs
	// and removes blob files. deleteData also deletes the plugin's items,
	// key-value data, secrets and usage records.
	Uninstall(ctx context.Context, id string, deleteData bool) error

	// Host API support.
	// Allow takes one request from the plugin's token bucket.
	Allow(pluginID string) (ok bool, retryAfter time.Duration)
	BeginIdempotent(ctx context.Context, pluginID, key, method, path string) (*StoredResponse, error)
	CompleteIdempotent(ctx context.Context, pluginID, key string, status int, body []byte) error
	ReleaseIdempotent(ctx context.Context, pluginID, key string) error

	KVGet(ctx context.Context, pluginID, key string) (*KVEntry, error)
	KVPut(ctx context.Context, pluginID, key string, value json.RawMessage) error
	KVDelete(ctx context.Context, pluginID, key string) error
	KVList(ctx context.Context, pluginID, prefix, cursor string, limit int) ([]KVEntry, string, error)
	BlobPut(ctx context.Context, pluginID, key, contentType string, r io.Reader, size int64) (*BlobInfo, error)
	BlobOpen(ctx context.Context, pluginID, key string) (*BlobInfo, io.ReadCloser, error)
	BlobDelete(ctx context.Context, pluginID, key string) error
	Secret(ctx context.Context, pluginID, name string) (string, error)
	// Settings returns the settings an admin set for the plugin, as a JSON
	// object.
	Settings(ctx context.Context, pluginID string) (json.RawMessage, error)

	CreateJob(ctx context.Context, actor *core.Actor, in JobInput) (*module.Job, error)
	UpdateJob(ctx context.Context, actor *core.Actor, id string, in JobUpdate) (*module.Job, error)
	JobLog(ctx context.Context, actor *core.Actor, id, level, message string) error

	Notify(ctx context.Context, actor *core.Actor, in Notification) error
	Emit(ctx context.Context, actor *core.Actor, typ, spaceID string, data json.RawMessage) error
	WebFetch(ctx context.Context, actor *core.Actor, in FetchInput) (*FetchResult, error)
}
