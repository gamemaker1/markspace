package module

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// JobStatus is the state of a job.
type JobStatus string

// Job states.
const (
	JobQueued   JobStatus = "queued"
	JobRunning  JobStatus = "running"
	JobPaused   JobStatus = "paused"
	JobDone     JobStatus = "done"
	JobFailed   JobStatus = "failed"
	JobCanceled JobStatus = "canceled"
)

// JobStatuses lists every job state.
var JobStatuses = []JobStatus{JobQueued, JobRunning, JobPaused, JobDone, JobFailed, JobCanceled}

// ActiveJobStatuses lists the states that count toward the queue cap.
var ActiveJobStatuses = []JobStatus{JobQueued, JobRunning, JobPaused}

// Valid reports whether s is a known state.
func (s JobStatus) Valid() bool { return slices.Contains(JobStatuses, s) }

// Finished reports whether s is terminal: done, failed or canceled.
func (s JobStatus) Finished() bool { return s == JobDone || s == JobFailed || s == JobCanceled }

// Job defaults and limits.
const (
	DefaultMaxAttempts = 3
	DefaultMaxQueued   = 10000
	DefaultLease       = 60 * time.Second
	DefaultWorkers     = 4
	DefaultRetention   = 30 * 24 * time.Hour
	MaxJobPayload      = 1 << 20
	MaxJobMessage      = 4096
	MaxJobLogMessage   = 8192
)

// DefaultBackoff is the delay before the retry after attempt 1, 2 and 3 or
// later.
var DefaultBackoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// JobLogLevels lists the levels of a job log entry.
var JobLogLevels = []string{"debug", "info", "warn", "error"}

// Job errors.
var (
	// ErrQueueFull reports that a module has reached its queued job cap.
	ErrQueueFull = fmt.Errorf("job queue full: %w", core.ErrRateLimited)
	// ErrLeaseLost reports a worker call for an attempt that no longer holds
	// the job, because it was canceled, requeued after its lease expired, or
	// finished.
	ErrLeaseLost = fmt.Errorf("job lease lost: %w", core.ErrConflict)
	// ErrJobCanceled is the context cause when a running job is canceled.
	ErrJobCanceled = errors.New("job canceled")
	// ErrLeaseExpired is the context cause when a running job's lease expired.
	ErrLeaseExpired = errors.New("job lease expired")
	// ErrEngineStopped is the context cause when the engine stops.
	ErrEngineStopped = errors.New("job engine stopped")
)

// Job is one durable background task. Children and ChildrenFinished are
// computed by the store on every read.
type Job struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Module         string          `json:"module"`
	SpaceID        string          `json:"spaceId,omitempty"`
	CreatedBy      string          `json:"createdBy,omitempty"`
	Status         JobStatus       `json:"status"`
	Title          string          `json:"title"`
	Progress       int64           `json:"progress"`
	Total          int64           `json:"total"`
	Message        string          `json:"message,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	Result         json.RawMessage `json:"result"`
	Attempts       int             `json:"attempts"`
	MaxAttempts    int             `json:"maxAttempts"`
	PauseRequested bool            `json:"pauseRequested,omitempty"`
	RunAfter       time.Time       `json:"runAfter,omitzero"`
	LeaseUntil     time.Time       `json:"leaseUntil,omitzero"`
	ParentID       string          `json:"parentId,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`

	Children         int `json:"children,omitempty"`
	ChildrenFinished int `json:"childrenFinished,omitempty"`
}

// Clone returns a deep copy.
func (j *Job) Clone() *Job {
	if j == nil {
		return nil
	}
	c := *j
	c.Payload = slices.Clone(j.Payload)
	c.Result = slices.Clone(j.Result)
	return &c
}

// DisplayProgress returns the progress shown to users. A job with total 0 and
// at least one child reports its finished children out of all children.
func (j *Job) DisplayProgress() (progress, total int64) {
	if j.Total == 0 && j.Children > 0 {
		return int64(j.ChildrenFinished), int64(j.Children)
	}
	return j.Progress, j.Total
}

// JobLogEntry is one line of a job log.
type JobLogEntry struct {
	JobID   string    `json:"jobId"`
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// JobProgress updates the progress fields of a running job. Nil fields are
// left unchanged.
type JobProgress struct {
	Progress *int64  `json:"progress,omitempty"`
	Total    *int64  `json:"total,omitempty"`
	Message  *string `json:"message,omitempty"`
}

// JobFilter selects jobs in List. Empty fields match every job.
type JobFilter struct {
	Module    string
	Kind      string
	SpaceID   string
	CreatedBy string
	ParentID  string
	Statuses  []JobStatus
	// Limit caps the result. 0 means no limit.
	Limit int
}

// Matches reports whether a job passes the filter, ignoring Limit.
func (f JobFilter) Matches(j *Job) bool {
	return (f.Module == "" || j.Module == f.Module) &&
		(f.Kind == "" || j.Kind == f.Kind) &&
		(f.SpaceID == "" || j.SpaceID == f.SpaceID) &&
		(f.CreatedBy == "" || j.CreatedBy == f.CreatedBy) &&
		(f.ParentID == "" || j.ParentID == f.ParentID) &&
		(len(f.Statuses) == 0 || slices.Contains(f.Statuses, j.Status))
}

// JobHandler runs one attempt of a job. It must return when ctx is canceled.
// A nil error completes the job with result. An error wrapped with Permanent
// fails the job at once. Any other error retries after the backoff until the
// attempts run out.
type JobHandler func(ctx context.Context, job *Job, rt JobRuntime) (result json.RawMessage, err error)

// JobRuntime is what a running handler may do with its job.
type JobRuntime interface {
	// Progress updates progress, total or message and renews the lease. It
	// returns ErrLeaseLost and cancels the handler context when the attempt
	// no longer holds the job.
	Progress(ctx context.Context, p JobProgress) error
	// Log appends a line to the job log.
	Log(ctx context.Context, level, message string) error
	// EnqueueChild enqueues a job whose parent is the running job. SpaceID
	// and CreatedBy default to the parent's.
	EnqueueChild(ctx context.Context, req EnqueueRequest) (*Job, error)
}

// EnqueueRequest asks the engine for a new job.
type EnqueueRequest struct {
	// ID is optional. An empty ID gets core.NewID.
	ID          string          `json:"id,omitempty"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title,omitempty"`
	SpaceID     string          `json:"spaceId,omitempty"`
	CreatedBy   string          `json:"createdBy,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	RunAfter    time.Time       `json:"runAfter,omitzero"`
	ParentID    string          `json:"parentId,omitempty"`
	MaxAttempts int             `json:"maxAttempts,omitempty"`
}

type permanentError struct{ err error }

func (p *permanentError) Error() string { return p.err.Error() }
func (p *permanentError) Unwrap() error { return p.err }

// Permanent marks an error as not worth retrying. Permanent(nil) is nil.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err was marked with Permanent.
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}

// RetryDelay returns the delay before retrying after the given attempt, which
// counts from 1. Attempts past the end of backoff use its last entry.
func RetryDelay(backoff []time.Duration, attempt int) time.Duration {
	if len(backoff) == 0 {
		backoff = DefaultBackoff
	}
	i := min(max(attempt, 1), len(backoff)) - 1
	return backoff[i]
}

// ValidateNewJob checks a job before a store inserts it and fills defaults:
// MaxAttempts 3, payload {}, result {}, RunAfter equal to CreatedAt. Stores
// call it from Enqueue so every implementation applies the same rules.
func ValidateNewJob(j *Job) error {
	switch {
	case j.ID != "" && !core.ValidID(j.ID):
		return core.Invalid("id", "invalid job id")
	case !ValidIdent(j.Kind):
		return core.Invalid("kind", fmt.Sprintf("invalid job kind %q", j.Kind))
	case !ValidIdent(j.Module):
		return core.Invalid("module", fmt.Sprintf("invalid module %q", j.Module))
	case j.SpaceID != "" && !core.ValidID(j.SpaceID):
		return core.Invalid("spaceId", "invalid space id")
	case j.ParentID != "" && !core.ValidID(j.ParentID):
		return core.Invalid("parentId", "invalid parent id")
	case len(j.CreatedBy) > 128:
		return core.Invalid("createdBy", "is longer than 128 bytes")
	case len(j.Title) > 200:
		return core.Invalid("title", "is longer than 200 bytes")
	case j.MaxAttempts < 0 || j.MaxAttempts > MaxJobAttempts:
		return core.Invalid("maxAttempts", fmt.Sprintf("must be 1 to %d", MaxJobAttempts))
	case j.CreatedAt.IsZero():
		return core.Invalid("createdAt", "is required")
	case len(j.Payload) > MaxJobPayload:
		return fmt.Errorf("job payload exceeds %d bytes: %w", MaxJobPayload, core.ErrTooLarge)
	}
	if len(j.Payload) == 0 {
		j.Payload = json.RawMessage(`{}`)
	} else if !json.Valid(j.Payload) {
		return core.Invalid("payload", "must be valid JSON")
	}
	if j.MaxAttempts == 0 {
		j.MaxAttempts = DefaultMaxAttempts
	}
	j.CreatedAt = j.CreatedAt.UTC()
	if j.RunAfter.IsZero() {
		j.RunAfter = j.CreatedAt
	}
	j.RunAfter = j.RunAfter.UTC()
	j.UpdatedAt = j.CreatedAt
	j.Status = JobQueued
	j.Result = json.RawMessage(`{}`)
	j.Progress, j.Total, j.Message = 0, 0, ""
	j.Attempts = 0
	j.PauseRequested = false
	j.LeaseUntil = time.Time{}
	j.Children, j.ChildrenFinished = 0, 0
	return nil
}
