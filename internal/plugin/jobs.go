package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/auth"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/module"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
	"github.com/gamemaker1/markspace/sdk"
)

// jobWaiter lets PATCH /jobs/{id} reach the running attempt of a plugin job.
type jobWaiter struct {
	pluginID string
	attempt  int
	rt       module.JobRuntime
	done     chan jobOutcome
}

type jobOutcome struct {
	result json.RawMessage
	err    error
}

// jobInfo is the job field of a POST /jobs/{kind} body.
type jobInfo struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Attempt int             `json:"attempt"`
	Title   string          `json:"title,omitempty"`
	SpaceID string          `json:"spaceId,omitempty"`
}

// jobHandler runs one attempt of a plugin job by posting it to the plugin. A
// 200 completes the job with its result. A 202 waits until the plugin reports
// done or failed through PATCH /jobs/{id}, and each progress report renews
// the lease. When the lease expires the engine cancels the wait.
func (h *Host) jobHandler(pluginID, kind string) module.JobHandler {
	return func(ctx context.Context, job *module.Job, rt module.JobRuntime) (json.RawMessage, error) {
		row, _, err := h.runnable(pluginID)
		if err != nil {
			return nil, err
		}
		w := &jobWaiter{pluginID: pluginID, attempt: job.Attempts, rt: rt, done: make(chan jobOutcome, 1)}
		h.mu.Lock()
		h.waiters[job.ID] = w
		h.mu.Unlock()
		defer func() {
			h.mu.Lock()
			if h.waiters[job.ID] == w {
				delete(h.waiters, job.ID)
			}
			h.mu.Unlock()
		}()

		body := map[string]any{"job": jobInfo{
			ID: job.ID, Kind: kind, Payload: job.Payload, Attempt: job.Attempts, Title: job.Title, SpaceID: job.SpaceID,
		}}
		interaction := "job:" + job.ID
		if core.ValidID(job.CreatedBy) {
			if u, err := h.store.GetUser(ctx, job.CreatedBy); err == nil && u.IsActive {
				tok, err := h.jobToken(ctx, pluginID, u.ID, interaction)
				if err != nil {
					return nil, err
				}
				defer h.revokeJobToken(pluginID, interaction)
				body["actingToken"] = tok
				body["user"] = module.Actor{ID: u.ID, Name: u.Name}
			}
		}
		res, err := h.call(ctx, row, http.MethodPost, "/jobs/"+kind, "", body, h.opts.JobTimeout)
		if err != nil {
			return nil, fmt.Errorf("plugin job call: %w", err)
		}
		switch res.Status {
		case http.StatusOK:
			var out struct {
				Result json.RawMessage `json:"result"`
			}
			if err := json.Unmarshal(res.Body, &out); err != nil {
				return nil, fmt.Errorf("plugin job response: %w", err)
			}
			return out.Result, nil
		case http.StatusAccepted:
			select {
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			case o := <-w.done:
				return o.result, o.err
			}
		case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
			return nil, module.Permanent(fmt.Errorf("plugin refused the job: %s", res.errorText()))
		}
		return nil, fmt.Errorf("plugin job failed: %s", res.errorText())
	}
}

// jobToken issues an acting token that lives while the job runs, for at most
// 24 hours.
func (h *Host) jobToken(ctx context.Context, pluginID, userID, interaction string) (string, error) {
	plain := core.ActingTokenPrefix + core.NewToken(25)
	tok := &index.ActingToken{TokenHash: auth.HashToken(plain), PluginID: pluginID, UserID: userID, Interaction: interaction}
	now := h.now()
	if err := h.store.CreateJobActingToken(ctx, tok, now.Add(index.MaxJobActingTokenTTL), now); err != nil {
		return "", fmt.Errorf("issue job acting token: %w", err)
	}
	return plain, nil
}

// revokeJobToken deletes the acting tokens of a job attempt that ended.
func (h *Host) revokeJobToken(pluginID, interaction string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := h.store.RevokeInteractionTokens(ctx, pluginID, interaction); err != nil {
		h.log.Warn("revoke job acting token failed", "plugin", pluginID, "interaction", interaction, "err", err)
	}
}

// pluginJob loads a job of the actor's plugin. A job of another plugin reads
// as not found.
func (h *Host) pluginJob(ctx context.Context, actor *core.Actor, id string) (*module.Job, string, error) {
	pid, err := pluginOf(actor)
	if err != nil {
		return nil, "", err
	}
	if !sdk.ValidObjectID(id) {
		return nil, "", fmt.Errorf("job %s: %w", id, core.ErrNotFound)
	}
	j, err := h.opts.Engine.Store().Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if j.Module != ModuleID(pid) {
		return nil, "", fmt.Errorf("job %s: %w", id, core.ErrNotFound)
	}
	return j, pid, nil
}

// CreateJob queues a job of a kind from the approved manifest. A plugin
// token starts it as the plugin, an acting token as the person.
func (h *Host) CreateJob(ctx context.Context, actor *core.Actor, in plugins.JobInput) (*module.Job, error) {
	pid, err := pluginOf(actor)
	if err != nil {
		return nil, err
	}
	_, man, _, ok := h.snapshot(pid)
	if !ok {
		return nil, fmt.Errorf("plugin %s: %w", pid, core.ErrNotFound)
	}
	if !slices.ContainsFunc(man.Jobs, func(j sdk.ManifestJob) bool { return j.Kind == in.Kind }) {
		return nil, core.Invalid("kind", fmt.Sprintf("the manifest declares no job kind %q", in.Kind))
	}
	if in.SpaceID != "" {
		g, err := h.opts.Backend.SpaceAccess(ctx, actor, in.SpaceID)
		if err != nil {
			return nil, err
		}
		if g == nil || g.Access < core.AccessView {
			return nil, fmt.Errorf("space %s: %w", in.SpaceID, core.ErrNotFound)
		}
	}
	if in.ParentID != "" {
		if _, _, err := h.pluginJob(ctx, actor, in.ParentID); err != nil {
			return nil, fmt.Errorf("parent job: %w", err)
		}
	}
	createdBy := "plugin:" + pid
	if actor.Via != "" {
		createdBy = actor.UserID()
	}
	req := module.EnqueueRequest{
		Kind: JobKind(pid, in.Kind), Title: strings.TrimSpace(in.Title), SpaceID: in.SpaceID,
		CreatedBy: createdBy, Payload: in.Payload, ParentID: in.ParentID,
	}
	if in.RunAfter != nil {
		req.RunAfter = in.RunAfter.UTC()
	}
	return h.opts.Engine.Enqueue(ctx, req)
}

// UpdateJob reports progress or the outcome of a running plugin job. The
// reply for a canceled job is the job itself, so the plugin stops.
func (h *Host) UpdateJob(ctx context.Context, actor *core.Actor, id string, in plugins.JobUpdate) (*module.Job, error) {
	j, _, err := h.pluginJob(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	switch {
	case j.Status == module.JobCanceled:
		return j, nil
	case j.Status.Finished():
		return nil, fmt.Errorf("job %s is %s: %w", id, j.Status, core.ErrConflict)
	case j.Status != module.JobRunning:
		return nil, fmt.Errorf("job %s is %s, not running: %w", id, j.Status, core.ErrConflict)
	}
	if in.Status != "" && in.Status != "done" && in.Status != "failed" {
		return nil, core.Invalid("status", "must be done or failed")
	}
	if len(in.Result) > 0 && (!json.Valid(in.Result) || len(in.Result) > module.MaxJobPayload) {
		return nil, core.Invalid("result", "must be valid JSON of at most 1 MiB")
	}
	h.mu.Lock()
	w := h.waiters[id]
	h.mu.Unlock()
	if w == nil || w.attempt != j.Attempts {
		return nil, fmt.Errorf("job %s is not waiting for a report in this process: %w", id, core.ErrConflict)
	}
	if in.Progress != nil || in.Total != nil || in.Message != nil {
		if err := w.rt.Progress(ctx, module.JobProgress{Progress: in.Progress, Total: in.Total, Message: in.Message}); err != nil {
			if errors.Is(err, module.ErrLeaseLost) {
				return h.opts.Engine.Store().Get(ctx, id)
			}
			return nil, err
		}
	}
	switch in.Status {
	case "done":
		select {
		case w.done <- jobOutcome{result: in.Result}:
		default:
		}
	case "failed":
		msg := "the plugin reported the job failed"
		if in.Message != nil && strings.TrimSpace(*in.Message) != "" {
			msg = *in.Message
		}
		select {
		case w.done <- jobOutcome{err: errors.New(msg)}:
		default:
		}
	}
	if in.Status != "" {
		return h.awaitFinished(ctx, id)
	}
	return h.opts.Engine.Store().Get(ctx, id)
}

// awaitFinished reads a job until the engine stored the attempt's outcome, for
// at most 2 s.
func (h *Host) awaitFinished(ctx context.Context, id string) (*module.Job, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		j, err := h.opts.Engine.Store().Get(ctx, id)
		if err != nil || j.Status != module.JobRunning || time.Now().After(deadline) {
			return j, err
		}
		select {
		case <-ctx.Done():
			return j, nil
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// JobLog appends a line to the log of a plugin job.
func (h *Host) JobLog(ctx context.Context, actor *core.Actor, id, level, message string) error {
	j, _, err := h.pluginJob(ctx, actor, id)
	if err != nil {
		return err
	}
	if !slices.Contains(module.JobLogLevels, level) {
		return core.Invalid("level", "must be debug, info, warn or error")
	}
	if len(message) > module.MaxJobLogMessage {
		return core.Invalid("message", fmt.Sprintf("is longer than %d bytes", module.MaxJobLogMessage))
	}
	return h.opts.Engine.Store().Log(ctx, module.JobLogEntry{JobID: j.ID, At: h.now(), Level: level, Message: message})
}
