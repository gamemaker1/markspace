package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gamemaker1/markspace/sdk/ui"
)

// Event is one event delivered to POST /events.
type Event struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	SpaceID string          `json:"spaceId,omitempty"`
	ItemID  string          `json:"itemId,omitempty"`
	Kind    string          `json:"kind,omitempty"`
	ActorID string          `json:"actorId,omitempty"`
	Path    string          `json:"path,omitempty"`
	OldPath string          `json:"oldPath,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	At      time.Time       `json:"at"`
}

// EventBatch holds the events of one delivery that match one handler's
// pattern.
type EventBatch struct {
	Events     []Event
	DeliveryID string
	// Host uses the plugin token.
	Host *Host
}

// User is the person who ran a command, opened a panel or ran an action.
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// JobInfo is the job field of a POST /jobs/{kind} body. Kind is the local
// kind from the manifest.
type JobInfo struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Attempt int             `json:"attempt"`
	Title   string          `json:"title,omitempty"`
	SpaceID string          `json:"spaceId,omitempty"`
}

// ErrJobCanceled is returned by a job handler that stops because Markspace
// canceled the job.
var ErrJobCanceled = errors.New("job canceled")

// JobContext is passed to a job handler.
type JobContext struct {
	Job        JobInfo
	DeliveryID string
	// User is the person who started the job. It is nil when no user
	// started it.
	User *User
	// Host uses the acting token from the job call when it has one, and the
	// plugin token otherwise.
	Host *Host

	cancel   context.CancelFunc
	canceled atomic.Bool
	mu       sync.Mutex
	done     int
	total    int
	reported time.Time
}

// DecodePayload decodes the job payload into v.
func (j *JobContext) DecodePayload(v any) error {
	if len(j.Job.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(j.Job.Payload, v)
}

// Progress reports progress through PATCH /jobs/{id}. An empty message is
// omitted. When the reply says the job is canceled, the job context is
// canceled and Canceled returns true.
func (j *JobContext) Progress(ctx context.Context, done, total int, message string) error {
	j.mu.Lock()
	j.done, j.total, j.reported = done, total, time.Now()
	j.mu.Unlock()
	body := map[string]any{"progress": done, "total": total}
	if message != "" {
		body["message"] = message
	}
	return j.patch(ctx, body)
}

func (j *JobContext) patch(ctx context.Context, body map[string]any) error {
	var out json.RawMessage
	err := j.Host.Do(ctx, "PATCH", "/jobs/"+url.PathEscape(j.Job.ID), body, &out)
	var st struct {
		Status string `json:"status"`
	}
	if len(out) > 0 && json.Unmarshal(out, &st) == nil && st.Status == "canceled" {
		j.markCanceled()
	}
	return err
}

// Log appends a line to the job log. level is debug, info, warn or error.
func (j *JobContext) Log(ctx context.Context, level, message string) error {
	if !contains([]string{"debug", "info", "warn", "error"}, level) {
		return fmt.Errorf("job log level %q is not debug, info, warn or error", level)
	}
	return j.Host.JobLog(ctx, j.Job.ID, level, message)
}

// Canceled reports whether Markspace canceled the job.
func (j *JobContext) Canceled() bool { return j.canceled.Load() }

func (j *JobContext) markCanceled() {
	j.canceled.Store(true)
	if j.cancel != nil {
		j.cancel()
	}
}

func (j *JobContext) lastReport() (int, int, time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done, j.total, j.reported
}

// CommandContext is passed to command and panel handlers.
type CommandContext struct {
	// ID is the local command or panel id.
	ID string
	// Context is {spaceId, itemId, kind, selection?, link?, imageFileId?}.
	Context    map[string]any
	User       User
	DeliveryID string
	// Host uses the acting token of this interaction.
	Host *Host
}

func (c *CommandContext) str(k string) string {
	s, _ := c.Context[k].(string)
	return s
}

// SpaceID returns context.spaceId.
func (c *CommandContext) SpaceID() string { return c.str("spaceId") }

// ItemID returns context.itemId.
func (c *CommandContext) ItemID() string { return c.str("itemId") }

// Kind returns context.kind.
func (c *CommandContext) Kind() string { return c.str("kind") }

// ActionContext is passed to action handlers.
type ActionContext struct {
	CommandContext
	// Values holds the submitted form values.
	Values map[string]any
	// Payload is the payload of the action object.
	Payload map[string]any
}

// ImportContext is passed to importer handlers.
type ImportContext struct {
	ID         string
	FileID     string
	SpaceID    string
	User       User
	DeliveryID string
	// Host uses the acting token of this interaction.
	Host *Host
}

// Handler types.
type (
	EventHandler   func(ctx context.Context, b *EventBatch) error
	JobHandler     func(ctx context.Context, j *JobContext) (any, error)
	CommandHandler func(ctx context.Context, c *CommandContext) (Response, error)
	PanelHandler   func(ctx context.Context, c *CommandContext) (ui.Node, error)
	ActionHandler  func(ctx context.Context, a *ActionContext) (Response, error)
	ImportHandler  func(ctx context.Context, c *ImportContext) (Response, error)
)
