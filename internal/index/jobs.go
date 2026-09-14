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

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// JobStore implements module.JobStore over the jobs, job_log and
// job_schedules tables of migration 7. It follows the reference semantics of
// module.MemJobStore and passes conformance.Run.
//
// Claim takes the job with one UPDATE whose WHERE clause picks the row by a
// subquery and rechecks the queued status. SQLite runs a statement under its
// write lock, so two claimers never take the same job, even from separate
// connections. Every other change reads and writes inside one transaction.
// DB holds a single connection, so those transactions run one at a time in
// the process. A second process on the same file is not supported for job
// changes other than Claim, because a deferred transaction that reads first
// can fail with SQLITE_BUSY when it upgrades to a write.
type JobStore struct {
	d *DB
}

// NewJobStore returns the job store of d.
func NewJobStore(d *DB) *JobStore { return &JobStore{d: d} }

var _ module.JobStore = (*JobStore)(nil)

// jobColumns is the column list matching scanJob.
const jobColumns = `id, kind, module, space_id, created_by, status, title, progress, total,
	message, payload, result, attempts, max_attempts, pause_requested, run_after,
	lease_until, parent_id, created_at, updated_at`

// jobFinishedList is the SQL list of terminal job states.
const jobFinishedList = `('done', 'failed', 'canceled')`

// jobSelect reads jobColumns followed by the child count and the finished
// child count.
const jobSelect = `SELECT ` + jobColumns + `,
	(SELECT COUNT(*) FROM jobs c WHERE c.parent_id = jobs.id),
	(SELECT COUNT(*) FROM jobs c WHERE c.parent_id = jobs.id AND c.status IN ` + jobFinishedList + `)
	FROM jobs`

// jobQuerier is satisfied by *sql.DB and *sql.Tx.
type jobQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// scanJob reads one row of jobSelect.
func scanJob(row rowScanner) (*module.Job, error) {
	var (
		j                                                          module.Job
		status, payload, result, runAfter, lease, created, updated string
		pause                                                      int
	)
	err := row.Scan(&j.ID, &j.Kind, &j.Module, &j.SpaceID, &j.CreatedBy, &status, &j.Title,
		&j.Progress, &j.Total, &j.Message, &payload, &result, &j.Attempts, &j.MaxAttempts,
		&pause, &runAfter, &lease, &j.ParentID, &created, &updated,
		&j.Children, &j.ChildrenFinished)
	if err != nil {
		return nil, err
	}
	j.Status = module.JobStatus(status)
	j.Payload = json.RawMessage(payload)
	j.Result = json.RawMessage(result)
	j.PauseRequested = intBool(pause)
	j.RunAfter = parseTime(runAfter)
	j.LeaseUntil = parseTime(lease)
	j.CreatedAt = parseTime(created)
	j.UpdatedAt = parseTime(updated)
	return &j, nil
}

func jobNotFound(id string) error {
	return fmt.Errorf("index: job %q: %w", id, core.ErrNotFound)
}

func jobConflict(id string, status module.JobStatus) error {
	return fmt.Errorf("index: job %q is %s: %w", id, status, core.ErrConflict)
}

// getJob reads one job with its child counts.
func getJob(ctx context.Context, q jobQuerier, id string) (*module.Job, error) {
	j, err := scanJob(q.QueryRowContext(ctx, jobSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, jobNotFound(id)
	}
	if err != nil {
		return nil, readErr("get job", err)
	}
	return j, nil
}

// heldJob reads a job that the attempt must hold.
func heldJob(ctx context.Context, t *sql.Tx, id string, attempt int) (*module.Job, error) {
	j, err := getJob(ctx, t, id)
	if err != nil {
		return nil, err
	}
	if j.Status != module.JobRunning || j.Attempts != attempt {
		return nil, fmt.Errorf("index: job %q attempt %d: %w", id, attempt, module.ErrLeaseLost)
	}
	return j, nil
}

// saveJob writes the mutable fields of a job.
func saveJob(ctx context.Context, t *sql.Tx, j *module.Job) error {
	_, err := t.ExecContext(ctx, `UPDATE jobs SET status = ?, progress = ?, total = ?, message = ?,
		result = ?, attempts = ?, pause_requested = ?, run_after = ?, lease_until = ?, updated_at = ?
		WHERE id = ?`,
		string(j.Status), j.Progress, j.Total, j.Message, string(j.Result), j.Attempts,
		boolInt(j.PauseRequested), fmtTime(j.RunAfter), fmtTime(j.LeaseUntil), fmtTime(j.UpdatedAt), j.ID)
	if err != nil {
		return writeErr("save job", err)
	}
	return nil
}

func countActiveJobs(ctx context.Context, q jobQuerier, mod string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE module = ? AND status IN ('queued', 'running', 'paused')`, mod).Scan(&n)
	if err != nil {
		return 0, readErr("count queued jobs", err)
	}
	return n, nil
}

// Enqueue implements module.JobStore.
func (s *JobStore) Enqueue(ctx context.Context, j *module.Job, maxQueued int) error {
	stored := j.Clone()
	if err := module.ValidateNewJob(stored); err != nil {
		return err
	}
	err := s.d.tx(ctx, func(t *sql.Tx) error {
		if stored.ID == "" {
			stored.ID = core.NewID()
		} else {
			var one int
			err := t.QueryRowContext(ctx, `SELECT 1 FROM jobs WHERE id = ?`, stored.ID).Scan(&one)
			if err == nil {
				return fmt.Errorf("index: job %q: %w", stored.ID, core.ErrExists)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return readErr("enqueue job", err)
			}
		}
		if stored.ParentID != "" {
			var status string
			err := t.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = ?`, stored.ParentID).Scan(&status)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("index: parent job %q: %w", stored.ParentID, core.ErrNotFound)
			}
			if err != nil {
				return readErr("enqueue job parent", err)
			}
			if module.JobStatus(status).Finished() {
				return fmt.Errorf("index: parent job %q is %s: %w", stored.ParentID, status, core.ErrConflict)
			}
		}
		if maxQueued > 0 {
			n, err := countActiveJobs(ctx, t, stored.Module)
			if err != nil {
				return err
			}
			if n >= maxQueued {
				return fmt.Errorf("index: module %q has %d queued jobs: %w", stored.Module, maxQueued, module.ErrQueueFull)
			}
		}
		_, err := t.ExecContext(ctx, `INSERT INTO jobs (`+jobColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			stored.ID, stored.Kind, stored.Module, stored.SpaceID, stored.CreatedBy, string(stored.Status),
			stored.Title, stored.Progress, stored.Total, stored.Message, string(stored.Payload),
			string(stored.Result), stored.Attempts, stored.MaxAttempts, boolInt(stored.PauseRequested),
			fmtTime(stored.RunAfter), fmtTime(stored.LeaseUntil), stored.ParentID,
			fmtTime(stored.CreatedAt), fmtTime(stored.UpdatedAt))
		if err != nil {
			return writeErr("enqueue job", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	*j = *stored
	return nil
}

// Claim implements module.JobStore.
func (s *JobStore) Claim(ctx context.Context, now time.Time, lease time.Duration, kinds []string) (*module.Job, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	now = now.UTC()
	q := `UPDATE jobs SET status = 'running', attempts = attempts + 1, lease_until = ?, updated_at = ?
		WHERE id = (
			SELECT id FROM jobs
			WHERE status = 'queued' AND run_after <= ? AND attempts < max_attempts
				AND kind IN (` + placeholders(len(kinds)) + `)
			ORDER BY run_after, created_at, id
			LIMIT 1)
		AND status = 'queued'
		RETURNING id`
	args := append([]any{fmtTime(now.Add(lease)), fmtTime(now), fmtTime(now)}, stringArgs(kinds)...)
	var out *module.Job
	err := s.d.tx(ctx, func(t *sql.Tx) error {
		var id string
		err := t.QueryRowContext(ctx, q, args...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return writeErr("claim job", err)
		}
		out, err = getJob(ctx, t, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RenewLease implements module.JobStore.
func (s *JobStore) RenewLease(ctx context.Context, id string, attempt int, now time.Time, lease time.Duration) error {
	return s.d.tx(ctx, func(t *sql.Tx) error {
		j, err := heldJob(ctx, t, id, attempt)
		if err != nil {
			return err
		}
		j.LeaseUntil = now.UTC().Add(lease)
		j.UpdatedAt = now.UTC()
		return saveJob(ctx, t, j)
	})
}

// Progress implements module.JobStore.
func (s *JobStore) Progress(ctx context.Context, id string, attempt int, p module.JobProgress, now time.Time, lease time.Duration) (*module.Job, error) {
	switch {
	case p.Progress != nil && *p.Progress < 0:
		return nil, core.Invalid("progress", "must not be negative")
	case p.Total != nil && *p.Total < 0:
		return nil, core.Invalid("total", "must not be negative")
	case p.Message != nil && len(*p.Message) > module.MaxJobMessage:
		return nil, core.Invalid("message", fmt.Sprintf("is longer than %d bytes", module.MaxJobMessage))
	}
	var out *module.Job
	err := s.d.tx(ctx, func(t *sql.Tx) error {
		j, err := heldJob(ctx, t, id, attempt)
		if err != nil {
			return err
		}
		if p.Progress != nil {
			j.Progress = *p.Progress
		}
		if p.Total != nil {
			j.Total = *p.Total
		}
		if p.Message != nil {
			j.Message = *p.Message
		}
		j.LeaseUntil = now.UTC().Add(lease)
		j.UpdatedAt = now.UTC()
		out = j
		return saveJob(ctx, t, j)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Log implements module.JobStore.
func (s *JobStore) Log(ctx context.Context, e module.JobLogEntry) error {
	switch {
	case !slices.Contains(module.JobLogLevels, e.Level):
		return core.Invalid("level", fmt.Sprintf("unknown level %q", e.Level))
	case len(e.Message) > module.MaxJobLogMessage:
		return core.Invalid("message", fmt.Sprintf("is longer than %d bytes", module.MaxJobLogMessage))
	}
	return s.d.tx(ctx, func(t *sql.Tx) error {
		if err := jobExists(ctx, t, e.JobID); err != nil {
			return err
		}
		_, err := t.ExecContext(ctx, `INSERT INTO job_log (job_id, at, level, message) VALUES (?, ?, ?, ?)`,
			e.JobID, fmtTime(e.At), e.Level, e.Message)
		if err != nil {
			return writeErr("append job log", err)
		}
		return nil
	})
}

func jobExists(ctx context.Context, q jobQuerier, id string) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM jobs WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return jobNotFound(id)
	}
	if err != nil {
		return readErr("read job", err)
	}
	return nil
}

// Logs implements module.JobStore. Entries come back in insertion order.
func (s *JobStore) Logs(ctx context.Context, id string) ([]module.JobLogEntry, error) {
	var out []module.JobLogEntry
	err := s.d.tx(ctx, func(t *sql.Tx) error {
		if err := jobExists(ctx, t, id); err != nil {
			return err
		}
		rows, err := t.QueryContext(ctx,
			`SELECT job_id, at, level, message FROM job_log WHERE job_id = ? ORDER BY rowid`, id)
		if err != nil {
			return readErr("read job log", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				e  module.JobLogEntry
				at string
			)
			if err := rows.Scan(&e.JobID, &at, &e.Level, &e.Message); err != nil {
				return readErr("read job log", err)
			}
			e.At = parseTime(at)
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			return readErr("read job log", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Complete implements module.JobStore.
func (s *JobStore) Complete(ctx context.Context, id string, attempt int, result json.RawMessage, now time.Time) (*module.Job, error) {
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	} else if !json.Valid(result) {
		return nil, core.Invalid("result", "must be valid JSON")
	} else if len(result) > module.MaxJobPayload {
		return nil, fmt.Errorf("index: job result exceeds %d bytes: %w", module.MaxJobPayload, core.ErrTooLarge)
	}
	var out *module.Job
	err := s.d.tx(ctx, func(t *sql.Tx) error {
		j, err := heldJob(ctx, t, id, attempt)
		if err != nil {
			return err
		}
		j.Status = module.JobDone
		j.Result = slices.Clone(result)
		j.LeaseUntil = time.Time{}
		j.PauseRequested = false
		j.UpdatedAt = now.UTC()
		out = j
		return saveJob(ctx, t, j)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Fail implements module.JobStore.
func (s *JobStore) Fail(ctx context.Context, id string, attempt int, message string, retryAt, now time.Time) (*module.Job, error) {
	message = truncateUTF8(message, module.MaxJobMessage)
	var out *module.Job
	err := s.d.tx(ctx, func(t *sql.Tx) error {
		j, err := heldJob(ctx, t, id, attempt)
		if err != nil {
			return err
		}
		j.Message = message
		j.LeaseUntil = time.Time{}
		j.UpdatedAt = now.UTC()
		switch {
		case retryAt.IsZero() || j.Attempts >= j.MaxAttempts:
			j.Status = module.JobFailed
			j.PauseRequested = false
		case j.PauseRequested:
			j.Status = module.JobPaused
			j.PauseRequested = false
			j.RunAfter = retryAt.UTC()
		default:
			j.Status = module.JobQueued
			j.RunAfter = retryAt.UTC()
		}
		out = j
		return saveJob(ctx, t, j)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// readJobs collects every row of a jobSelect query.
func readJobs(ctx context.Context, q jobQuerier, query string, args ...any) ([]*module.Job, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, readErr("list jobs", err)
	}
	defer rows.Close()
	var out []*module.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, readErr("list jobs", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list jobs", err)
	}
	return out, nil
}

// reread returns fresh views of the named jobs, so child counts reflect
// every change made in the transaction.
func reread(ctx context.Context, t *sql.Tx, ids []string) ([]*module.Job, error) {
	out := make([]*module.Job, 0, len(ids))
	for _, id := range ids {
		j, err := getJob(ctx, t, id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

// RequeueExpired implements module.JobStore.
func (s *JobStore) RequeueExpired(ctx context.Context, now time.Time) ([]*module.Job, error) {
	now = now.UTC()
	var out []*module.Job
	err := s.d.tx(ctx, func(t *sql.Tx) error {
		expired, err := readJobs(ctx, t,
			jobSelect+` WHERE status = 'running' AND lease_until <= ? ORDER BY id`, fmtTime(now))
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(expired))
		for _, j := range expired {
			j.LeaseUntil = time.Time{}
			j.UpdatedAt = now
			switch {
			case j.Attempts >= j.MaxAttempts:
				j.Status = module.JobFailed
				j.Message = "lease expired"
			case j.PauseRequested:
				j.Status = module.JobPaused
				j.RunAfter = now
			default:
				j.Status = module.JobQueued
				j.RunAfter = now
			}
			j.PauseRequested = false
			if err := saveJob(ctx, t, j); err != nil {
				return err
			}
			ids = append(ids, j.ID)
		}
		out, err = reread(ctx, t, ids)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Cancel implements module.JobStore.
func (s *JobStore) Cancel(ctx context.Context, id string, now time.Time) ([]*module.Job, error) {
	now = now.UTC()
	var out []*module.Job
	err := s.d.tx(ctx, func(t *sql.Tx) error {
		root, err := getJob(ctx, t, id)
		if err != nil {
			return err
		}
		switch root.Status {
		case module.JobCanceled:
			return nil
		case module.JobDone, module.JobFailed:
			return jobConflict(id, root.Status)
		}
		type node struct {
			id     string
			status module.JobStatus
		}
		var changed []string
		queue := []node{{root.ID, root.Status}}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if !cur.status.Finished() {
				_, err := t.ExecContext(ctx, `UPDATE jobs SET status = 'canceled', lease_until = '',
					pause_requested = 0, updated_at = ? WHERE id = ?`, fmtTime(now), cur.id)
				if err != nil {
					return writeErr("cancel job", err)
				}
				changed = append(changed, cur.id)
			}
			rows, err := t.QueryContext(ctx, `SELECT id, status FROM jobs WHERE parent_id = ? ORDER BY rowid`, cur.id)
			if err != nil {
				return readErr("cancel job children", err)
			}
			for rows.Next() {
				var n node
				var status string
				if err := rows.Scan(&n.id, &status); err != nil {
					rows.Close()
					return readErr("cancel job children", err)
				}
				n.status = module.JobStatus(status)
				queue = append(queue, n)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return readErr("cancel job children", err)
			}
		}
		out, err = reread(ctx, t, changed)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Pause implements module.JobStore.
func (s *JobStore) Pause(ctx context.Context, id string, now time.Time) (*module.Job, error) {
	var out *module.Job
	err := s.d.tx(ctx, func(t *sql.Tx) error {
		j, err := getJob(ctx, t, id)
		if err != nil {
			return err
		}
		out = j
		switch j.Status {
		case module.JobQueued:
			j.Status = module.JobPaused
		case module.JobRunning:
			if j.PauseRequested {
				return nil
			}
			j.PauseRequested = true
		case module.JobPaused:
			return nil
		default:
			return jobConflict(id, j.Status)
		}
		j.UpdatedAt = now.UTC()
		return saveJob(ctx, t, j)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Resume implements module.JobStore.
func (s *JobStore) Resume(ctx context.Context, id string, now time.Time) (*module.Job, error) {
	var out *module.Job
	err := s.d.tx(ctx, func(t *sql.Tx) error {
		j, err := getJob(ctx, t, id)
		if err != nil {
			return err
		}
		out = j
		switch j.Status {
		case module.JobPaused:
			j.Status = module.JobQueued
			j.RunAfter = now.UTC()
		case module.JobRunning:
			if !j.PauseRequested {
				return nil
			}
			j.PauseRequested = false
		case module.JobQueued:
			return nil
		default:
			return jobConflict(id, j.Status)
		}
		j.UpdatedAt = now.UTC()
		return saveJob(ctx, t, j)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Get implements module.JobStore.
func (s *JobStore) Get(ctx context.Context, id string) (*module.Job, error) {
	return getJob(ctx, s.d.sql, id)
}

// List implements module.JobStore.
func (s *JobStore) List(ctx context.Context, f module.JobFilter) ([]*module.Job, error) {
	var (
		where []string
		args  []any
	)
	add := func(cond string, v string) {
		if v != "" {
			where = append(where, cond)
			args = append(args, v)
		}
	}
	add("module = ?", f.Module)
	add("kind = ?", f.Kind)
	add("space_id = ?", f.SpaceID)
	add("created_by = ?", f.CreatedBy)
	add("parent_id = ?", f.ParentID)
	if len(f.Statuses) > 0 {
		where = append(where, "status IN ("+placeholders(len(f.Statuses))+")")
		for _, st := range f.Statuses {
			args = append(args, string(st))
		}
	}
	q := jobSelect
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC, id DESC"
	page, pageArgs := limitClause(f.Limit, 0)
	return readJobs(ctx, s.d.sql, q+page, append(args, pageArgs...)...)
}

// CountByStatus counts the jobs of each status. A status without jobs is left
// out.
func (s *JobStore) CountByStatus(ctx context.Context) (map[module.JobStatus]int, error) {
	rows, err := s.d.sql.QueryContext(ctx, `SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err != nil {
		return nil, readErr("count jobs by status", err)
	}
	defer rows.Close()
	out := map[module.JobStatus]int{}
	for rows.Next() {
		var (
			status string
			n      int
		)
		if err := rows.Scan(&status, &n); err != nil {
			return nil, readErr("count jobs by status", err)
		}
		out[module.JobStatus(status)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("count jobs by status", err)
	}
	return out, nil
}

// CountQueued implements module.JobStore.
func (s *JobStore) CountQueued(ctx context.Context, mod string) (int, error) {
	return countActiveJobs(ctx, s.d.sql, mod)
}

// deleteChunk is the number of ids bound in one DELETE statement.
const deleteChunk = 500

// DeleteFinishedBefore implements module.JobStore. A tree is deleted only
// when every job in it is finished with UpdatedAt before cutoff. A job whose
// parent row is missing counts as a root.
func (s *JobStore) DeleteFinishedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	c := fmtTime(cutoff)
	const q = `WITH RECURSIVE tree(root, id) AS (
			SELECT r.id, r.id FROM jobs r
			WHERE r.status IN ` + jobFinishedList + ` AND r.updated_at < ?
				AND (r.parent_id = '' OR NOT EXISTS (SELECT 1 FROM jobs p WHERE p.id = r.parent_id))
			UNION ALL
			SELECT tree.root, ch.id FROM jobs ch JOIN tree ON ch.parent_id = tree.id
		)
		SELECT id FROM tree
		WHERE root NOT IN (
			SELECT t2.root FROM tree t2 JOIN jobs j ON j.id = t2.id
			WHERE j.status NOT IN ` + jobFinishedList + ` OR j.updated_at >= ?)`
	deleted := 0
	err := s.d.tx(ctx, func(t *sql.Tx) error {
		rows, err := t.QueryContext(ctx, q, c, c)
		if err != nil {
			return readErr("find finished jobs", err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return readErr("find finished jobs", err)
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return readErr("find finished jobs", err)
		}
		for chunk := range slices.Chunk(ids, deleteChunk) {
			args := stringArgs(chunk)
			in := "(" + placeholders(len(chunk)) + ")"
			if _, err := t.ExecContext(ctx, `DELETE FROM job_log WHERE job_id IN `+in, args...); err != nil {
				return writeErr("delete job logs", err)
			}
			res, err := t.ExecContext(ctx, `DELETE FROM jobs WHERE id IN `+in, args...)
			if err != nil {
				return writeErr("delete jobs", err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("index: delete jobs: rows affected: %w", err)
			}
			deleted += int(n)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// ChildrenProgress implements module.JobStore.
func (s *JobStore) ChildrenProgress(ctx context.Context, parentID string) (finished, total int, err error) {
	j, err := getJob(ctx, s.d.sql, parentID)
	if err != nil {
		return 0, 0, err
	}
	return j.ChildrenFinished, j.Children, nil
}

// LastRun implements module.JobStore.
func (s *JobStore) LastRun(ctx context.Context, kind string) (time.Time, error) {
	var last string
	err := s.d.sql.QueryRowContext(ctx, `SELECT last_run FROM job_schedules WHERE kind = ?`, kind).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, readErr("read schedule", err)
	}
	return parseTime(last), nil
}

// SetLastRun implements module.JobStore.
func (s *JobStore) SetLastRun(ctx context.Context, kind string, t time.Time) error {
	_, err := s.d.sql.ExecContext(ctx, `INSERT INTO job_schedules (kind, last_run) VALUES (?, ?)
		ON CONFLICT(kind) DO UPDATE SET last_run = excluded.last_run`, kind, fmtTime(t))
	if err != nil {
		return writeErr("set schedule last run", err)
	}
	return nil
}

// truncateUTF8 cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}
