package index

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// fileColumns is the SELECT list matching scanFile. Every query aliases the
// files table as fi.
const fileColumns = `fi.id, fi.space_id, fi.parent_id, fi.name, fi.path, fi.mime, fi.size,
	fi.sha256, fi.width, fi.height, fi.duration, fi.pages, fi.position, fi.versions, fi.rev,
	fi.mod_time, fi.created_at, fi.updated_at, fi.created_by, fi.updated_by, fi.deleted,
	fi.deleted_at, fi.deleted_by`

// scanFile reads one files row. SpaceSlug, URL and Thumb are left to the app
// layer.
func scanFile(row rowScanner) (*core.File, error) {
	var (
		f                                core.File
		deleted                          int
		modTime, created, updated, delAt string
	)
	err := row.Scan(&f.ID, &f.SpaceID, &f.ParentID, &f.Name, &f.Path, &f.MIME, &f.Size,
		&f.SHA256, &f.Width, &f.Height, &f.Duration, &f.Pages, &f.Position, &f.Versions, &f.Rev,
		&modTime, &created, &updated, &f.CreatedBy, &f.UpdatedBy, &deleted, &delAt, &f.DeletedBy)
	if err != nil {
		return nil, err
	}
	f.ModTime = parseTime(modTime)
	f.CreatedAt = parseTime(created)
	f.UpdatedAt = parseTime(updated)
	f.Deleted = intBool(deleted)
	f.DeletedAt = parseTimePtr(delAt)
	return &f, nil
}

// getFileQ reads one file by id.
func getFileQ(ctx context.Context, q dbtx, id string) (*core.File, error) {
	f, err := scanFile(q.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM files fi WHERE fi.id = ?`, id))
	if err != nil {
		return nil, readErr("get file "+id, err)
	}
	return f, nil
}

// collectFiles drains a file query.
func collectFiles(rows *sql.Rows, what string) ([]*core.File, error) {
	var out []*core.File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, readErr(what, err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr(what, err)
	}
	return out, nil
}

// queryFiles runs a file query and drains it.
func (d *DB) queryFiles(ctx context.Context, what, query string, args ...any) ([]*core.File, error) {
	rows, err := d.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, readErr(what, err)
	}
	defer rows.Close()
	return collectFiles(rows, what)
}

// setFileFTSName updates the name in the full-text row of a file, creating
// the row with an empty body when it is missing. clearBody empties the body.
func setFileFTSName(ctx context.Context, t *sql.Tx, f *core.File, clearBody bool) error {
	q := `UPDATE files_fts SET name = ?, space_id = ? WHERE file_id = ?`
	if clearBody {
		q = `UPDATE files_fts SET name = ?, space_id = ?, body = '' WHERE file_id = ?`
	}
	res, err := t.ExecContext(ctx, q, ftsText(f.Name), f.SpaceID, f.ID)
	if err != nil {
		return writeErr("file text", err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return nil
	}
	_, err = t.ExecContext(ctx, `INSERT INTO files_fts (name, body, file_id, space_id) VALUES (?, '', ?, ?)`,
		ftsText(f.Name), f.ID, f.SpaceID)
	if err != nil {
		return writeErr("file text", err)
	}
	return nil
}

// CreateFile inserts a file. An empty ID is filled with a new identifier, an
// empty Name is taken from the path, and zero timestamps are set to now. The
// text state starts pending, so the extraction queue sees every new file. A
// live file must fit the tree, see checkPlacement. An id held by any item
// returns core.ErrExists.
func (d *DB) CreateFile(ctx context.Context, f *core.File) error {
	if f == nil {
		return fmt.Errorf("index: create file: nil file: %w", core.ErrInvalid)
	}
	if f.SpaceID == "" {
		return fmt.Errorf("index: create file: %w", core.Invalid("spaceId", "space id is required"))
	}
	if f.ID == "" {
		f.ID = core.NewID()
	}
	if f.Name == "" {
		f.Name = baseOf(f.Path)
	}
	now := time.Now().UTC()
	if f.CreatedAt.IsZero() {
		f.CreatedAt = now
	}
	if f.UpdatedAt.IsZero() {
		f.UpdatedAt = f.CreatedAt
	}
	if f.ModTime.IsZero() {
		f.ModTime = f.UpdatedAt
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		if _, _, err := itemKindQ(ctx, t, f.ID); err == nil {
			return fmt.Errorf("index: create file %s: %w", f.ID, core.ErrExists)
		}
		if f.Deleted {
			if err := validEntryPath(f.Path); err != nil {
				return fmt.Errorf("index: create file: %w", err)
			}
		} else if err := checkPlacement(ctx, t, "create file", f.SpaceID, f.ID, f.ParentID, f.Name, f.Path); err != nil {
			return err
		}
		rev, err := nextRev(ctx, t)
		if err != nil {
			return err
		}
		_, err = t.ExecContext(ctx, `INSERT INTO files (id, space_id, parent_id, name, path, mime, size,
			sha256, width, height, duration, pages, position, versions, rev, mod_time, created_at,
			updated_at, created_by, updated_by, deleted, deleted_at, deleted_by, text_state)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending')`,
			f.ID, f.SpaceID, f.ParentID, f.Name, f.Path, f.MIME, f.Size, f.SHA256, f.Width, f.Height,
			f.Duration, f.Pages, f.Position, f.Versions, rev, fmtTime(f.ModTime), fmtTime(f.CreatedAt),
			fmtTime(f.UpdatedAt), f.CreatedBy, f.UpdatedBy, boolInt(f.Deleted), fmtTimePtr(f.DeletedAt),
			f.DeletedBy)
		if err != nil {
			return writeErr("create file", err)
		}
		f.Rev = rev
		if err := setFileFTSName(ctx, t, f, true); err != nil {
			return err
		}
		return resolveDanglingItem(ctx, t, f.ID)
	})
}

// GetFile reads one file by id.
func (d *DB) GetFile(ctx context.Context, id string) (*core.File, error) {
	return getFileQ(ctx, d.sql, id)
}

// GetFileByPath reads the live file at relPath, compared case-insensitively.
func (d *DB) GetFileByPath(ctx context.Context, spaceID, relPath string) (*core.File, error) {
	f, err := scanFile(d.sql.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM files fi
		WHERE fi.space_id = ? AND fi.deleted = 0 AND fi.path = ? COLLATE NOCASE`, spaceID, relPath))
	if err != nil {
		return nil, readErr("get file by path "+relPath, err)
	}
	return f, nil
}

// contentChanged reports whether new content replaced the old. A hash that
// appears for unchanged size is the first hash of the same content.
func contentChanged(oldSHA string, oldSize int64, newSHA string, newSize int64) bool {
	return oldSize != newSize || (oldSHA != "" && oldSHA != newSHA)
}

// UpdateFile writes every persisted field of a file except CreatedAt,
// CreatedBy and the deleted fields, with the next revision. An empty Name is
// taken from the path. A live file must still fit the tree. Changed content
// clears the extracted text and sets the text state to pending. A trashed
// file accepts no path or parent change.
func (d *DB) UpdateFile(ctx context.Context, f *core.File) error {
	if f == nil {
		return fmt.Errorf("index: update file: nil file: %w", core.ErrInvalid)
	}
	if f.Name == "" {
		f.Name = baseOf(f.Path)
	}
	if f.UpdatedAt.IsZero() {
		f.UpdatedAt = time.Now().UTC()
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		cur, err := getFileQ(ctx, t, f.ID)
		if err != nil {
			return err
		}
		f.SpaceID = cur.SpaceID
		placed := f.Path != cur.Path || f.ParentID != cur.ParentID || f.Name != cur.Name
		if placed && cur.Deleted {
			return fmt.Errorf("index: update file: %w", core.Invalid("id", "a trashed file cannot move"))
		}
		if placed {
			if err := checkPlacement(ctx, t, "update file", cur.SpaceID, cur.ID, f.ParentID, f.Name, f.Path); err != nil {
				return err
			}
		}
		rev, err := nextRev(ctx, t)
		if err != nil {
			return err
		}
		changed := contentChanged(cur.SHA256, cur.Size, f.SHA256, f.Size)
		state := `text_state`
		if changed {
			state = `'pending'`
		}
		res, err := t.ExecContext(ctx, `UPDATE files SET name = ?, path = ?, parent_id = ?, mime = ?,
			size = ?, sha256 = ?, width = ?, height = ?, duration = ?, pages = ?, position = ?,
			versions = ?, rev = ?, mod_time = ?, updated_at = ?, updated_by = ?, text_state = `+state+`
			WHERE id = ?`,
			f.Name, f.Path, f.ParentID, f.MIME, f.Size, f.SHA256, f.Width, f.Height, f.Duration,
			f.Pages, f.Position, f.Versions, rev, fmtTime(f.ModTime), fmtTime(f.UpdatedAt),
			f.UpdatedBy, f.ID)
		if err := affected("update file", res, err); err != nil {
			return err
		}
		f.Rev = rev
		f.CreatedAt, f.CreatedBy = cur.CreatedAt, cur.CreatedBy
		f.Deleted, f.DeletedAt, f.DeletedBy = cur.Deleted, cur.DeletedAt, cur.DeletedBy
		if err := setFileFTSName(ctx, t, f, changed); err != nil {
			return err
		}
		if cur.Deleted {
			return nil
		}
		return resolveDanglingItem(ctx, t, f.ID)
	})
}

// SetFileHash records the hash, size and mod time of a file's content with
// the next revision. Changed content clears the extracted text and sets the
// text state to pending.
func (d *DB) SetFileHash(ctx context.Context, id, sha256 string, size int64, modTime time.Time) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		cur, err := getFileQ(ctx, t, id)
		if err != nil {
			return err
		}
		rev, err := nextRev(ctx, t)
		if err != nil {
			return err
		}
		changed := contentChanged(cur.SHA256, cur.Size, sha256, size)
		state := `text_state`
		if changed {
			state = `'pending'`
		}
		_, err = t.ExecContext(ctx, `UPDATE files SET sha256 = ?, size = ?, mod_time = ?, rev = ?,
			text_state = `+state+` WHERE id = ?`, sha256, size, fmtTime(modTime), rev, id)
		if err != nil {
			return writeErr("set file hash", err)
		}
		if changed {
			if _, err := t.ExecContext(ctx, `UPDATE files_fts SET body = '' WHERE file_id = ?`, id); err != nil {
				return writeErr("set file hash", err)
			}
		}
		return nil
	})
}

// ListFilesNeedingHash lists live files with no stored hash, oldest first.
func (d *DB) ListFilesNeedingHash(ctx context.Context, limit int) ([]*core.File, error) {
	if limit <= 0 {
		limit = 100
	}
	return d.queryFiles(ctx, "list files needing hash", `SELECT `+fileColumns+` FROM files fi
		WHERE fi.sha256 = '' AND fi.deleted = 0 ORDER BY fi.created_at, fi.id LIMIT ?`, limit)
}

// ListFilesNeedingText lists live files whose text state is pending, oldest
// first.
func (d *DB) ListFilesNeedingText(ctx context.Context, limit int) ([]*core.File, error) {
	if limit <= 0 {
		limit = 100
	}
	return d.queryFiles(ctx, "list files needing text", `SELECT `+fileColumns+` FROM files fi
		WHERE fi.text_state = 'pending' AND fi.deleted = 0 ORDER BY fi.created_at, fi.id LIMIT ?`, limit)
}

// FindFilesByHash lists the live files in a space with the given hash and
// size, sorted by path.
func (d *DB) FindFilesByHash(ctx context.Context, spaceID, sha256 string, size int64) ([]*core.File, error) {
	if sha256 == "" {
		return nil, nil
	}
	return d.queryFiles(ctx, "find files by hash", `SELECT `+fileColumns+` FROM files fi
		WHERE fi.space_id = ? AND fi.sha256 = ? AND fi.size = ? AND fi.deleted = 0
		ORDER BY fi.path`, spaceID, sha256, size)
}

// TrashFile marks a live file deleted with the next revision. The row keeps
// its path, which RestoreFile uses by default. Trashing a trashed file does
// nothing.
func (d *DB) TrashFile(ctx context.Context, id string, at time.Time, by string) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		cur, err := getFileQ(ctx, t, id)
		if err != nil {
			return err
		}
		if cur.Deleted {
			return nil
		}
		rev, err := nextRev(ctx, t)
		if err != nil {
			return err
		}
		_, err = t.ExecContext(ctx, `UPDATE files SET deleted = 1, deleted_at = ?, deleted_by = ?, rev = ?
			WHERE id = ?`, fmtTime(at), by, rev, id)
		if err != nil {
			return writeErr("trash file", err)
		}
		return nil
	})
}

// RestoreFile clears the deleted mark on a trashed file and places it at
// relPath under parentID, with the next revision. An empty relPath keeps the
// old path. A path taken by a live item returns core.ErrExists. Restoring a
// live file does nothing.
func (d *DB) RestoreFile(ctx context.Context, id, parentID, relPath string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		cur, err := getFileQ(ctx, t, id)
		if err != nil {
			return err
		}
		if !cur.Deleted {
			return nil
		}
		if relPath == "" {
			relPath = cur.Path
		}
		name := baseOf(relPath)
		if err := checkPlacement(ctx, t, "restore file", cur.SpaceID, cur.ID, parentID, name, relPath); err != nil {
			return err
		}
		rev, err := nextRev(ctx, t)
		if err != nil {
			return err
		}
		_, err = t.ExecContext(ctx, `UPDATE files SET deleted = 0, deleted_at = '', deleted_by = '',
			name = ?, path = ?, parent_id = ?, rev = ? WHERE id = ?`, name, relPath, parentID, rev, id)
		if err != nil {
			return writeErr("restore file", err)
		}
		cur.Name = name
		if err := setFileFTSName(ctx, t, cur, false); err != nil {
			return err
		}
		return resolveDanglingItem(ctx, t, id)
	})
}

// DeleteFile removes a file row with its text, comments, shares and public
// links. Links to it become unresolved.
func (d *DB) DeleteFile(ctx context.Context, id string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		if _, err := getFileQ(ctx, t, id); err != nil {
			return err
		}
		return deleteItemRows(ctx, t, nil, nil, []string{id})
	})
}

// ListFiles lists the files of one space. Type is a MIME prefix. A value with
// no slash, such as "image", matches the whole type. Sort is name, size,
// updated or path. An unknown sort name sorts by name.
func (d *DB) ListFiles(ctx context.Context, spaceID string, opts ports.FileListOptions) ([]*core.File, error) {
	where := []string{`fi.space_id = ?`}
	args := []any{spaceID}
	if !opts.IncludeDeleted {
		where = append(where, `fi.deleted = 0`)
	}
	if opts.HasParent || opts.ParentID != "" {
		where = append(where, `fi.parent_id = ?`)
		args = append(args, opts.ParentID)
	}
	if typ := strings.ToLower(strings.TrimSpace(opts.Type)); typ != "" {
		if !strings.Contains(typ, "/") {
			typ += "/"
		}
		where = append(where, `lower(fi.mime) LIKE ? ESCAPE '\'`)
		args = append(args, likeEscape(typ)+"%")
	}
	order := `fi.name COLLATE NOCASE, fi.path`
	switch opts.Sort {
	case "size":
		order = `fi.size DESC, fi.name COLLATE NOCASE, fi.path`
	case "updated":
		order = `fi.updated_at DESC, fi.name COLLATE NOCASE, fi.path`
	case "path":
		order = `fi.path`
	}
	page, pageArgs := limitClause(opts.Limit, opts.Offset)
	return d.queryFiles(ctx, "list files", `SELECT `+fileColumns+` FROM files fi WHERE `+
		strings.Join(where, " AND ")+` ORDER BY `+order+page, append(args, pageArgs...)...)
}

// CountFiles returns the number of live files.
func (d *DB) CountFiles(ctx context.Context) (int, error) {
	return d.count(ctx, `SELECT COUNT(*) FROM files WHERE deleted = 0`, "count files")
}
