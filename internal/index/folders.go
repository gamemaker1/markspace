package index

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// folderColumns is the SELECT list matching scanFolder. Every query aliases
// the folders table as fo.
const folderColumns = `fo.id, fo.space_id, fo.parent_id, fo.name, fo.path, fo.position,
	fo.index_note_id, fo.rev, fo.created_at, fo.updated_at, fo.created_by, fo.deleted,
	fo.deleted_at, fo.deleted_by`

// scanFolder reads one folders row.
func scanFolder(row rowScanner) (*core.Folder, error) {
	var (
		f                       core.Folder
		deleted                 int
		created, updated, delAt string
	)
	err := row.Scan(&f.ID, &f.SpaceID, &f.ParentID, &f.Name, &f.Path, &f.Position,
		&f.IndexNoteID, &f.Rev, &created, &updated, &f.CreatedBy, &deleted, &delAt, &f.DeletedBy)
	if err != nil {
		return nil, err
	}
	f.CreatedAt = parseTime(created)
	f.UpdatedAt = parseTime(updated)
	f.Deleted = intBool(deleted)
	f.DeletedAt = parseTimePtr(delAt)
	return &f, nil
}

// getFolderQ reads one folder and the stored text of its deletion time.
func getFolderQ(ctx context.Context, q dbtx, id string) (*core.Folder, string, error) {
	var delAt string
	f, err := scanFolder(extraScanner{
		row:   q.QueryRowContext(ctx, `SELECT `+folderColumns+`, fo.deleted_at FROM folders fo WHERE fo.id = ?`, id),
		extra: []any{&delAt},
	})
	if err != nil {
		return nil, "", readErr("get folder "+id, err)
	}
	return f, delAt, nil
}

// CreateFolder inserts a folder. An empty ID is filled with a new identifier,
// an empty Name is taken from the path, and zero timestamps are set to now.
// A live folder must fit the tree, see checkPlacement. An id held by any
// item returns core.ErrExists.
func (d *DB) CreateFolder(ctx context.Context, f *core.Folder) error {
	if f == nil {
		return fmt.Errorf("index: create folder: nil folder: %w", core.ErrInvalid)
	}
	if f.SpaceID == "" {
		return fmt.Errorf("index: create folder: %w", core.Invalid("spaceId", "space id is required"))
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
	return d.tx(ctx, func(t *sql.Tx) error {
		if _, _, err := itemKindQ(ctx, t, f.ID); err == nil {
			return fmt.Errorf("index: create folder %s: %w", f.ID, core.ErrExists)
		}
		if f.Deleted {
			if err := validEntryPath(f.Path); err != nil {
				return fmt.Errorf("index: create folder: %w", err)
			}
		} else if err := checkPlacement(ctx, t, "create folder", f.SpaceID, f.ID, f.ParentID, f.Name, f.Path); err != nil {
			return err
		}
		rev, err := nextRev(ctx, t)
		if err != nil {
			return err
		}
		_, err = t.ExecContext(ctx, `INSERT INTO folders (id, space_id, parent_id, name, path,
			position, index_note_id, rev, created_at, updated_at, created_by, deleted, deleted_at,
			deleted_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			f.ID, f.SpaceID, f.ParentID, f.Name, f.Path, f.Position, f.IndexNoteID, rev,
			fmtTime(f.CreatedAt), fmtTime(f.UpdatedAt), f.CreatedBy, boolInt(f.Deleted),
			fmtTimePtr(f.DeletedAt), f.DeletedBy)
		if err != nil {
			return writeErr("create folder", err)
		}
		f.Rev = rev
		return resolveDanglingItem(ctx, t, f.ID)
	})
}

// GetFolder reads one folder by id.
func (d *DB) GetFolder(ctx context.Context, id string) (*core.Folder, error) {
	f, _, err := getFolderQ(ctx, d.sql, id)
	return f, err
}

// GetFolderByPath reads the live folder at relPath, compared
// case-insensitively.
func (d *DB) GetFolderByPath(ctx context.Context, spaceID, relPath string) (*core.Folder, error) {
	row := d.sql.QueryRowContext(ctx, `SELECT `+folderColumns+` FROM folders fo
		WHERE fo.space_id = ? AND fo.deleted = 0 AND fo.path = ? COLLATE NOCASE`, spaceID, relPath)
	f, err := scanFolder(row)
	if err != nil {
		return nil, readErr("get folder by path "+relPath, err)
	}
	return f, nil
}

// ListFolders lists the folders of one space sorted by path.
func (d *DB) ListFolders(ctx context.Context, spaceID string, includeDeleted bool) ([]*core.Folder, error) {
	q := `SELECT ` + folderColumns + ` FROM folders fo WHERE fo.space_id = ?`
	if !includeDeleted {
		q += ` AND fo.deleted = 0`
	}
	rows, err := d.sql.QueryContext(ctx, q+` ORDER BY fo.path`, spaceID)
	if err != nil {
		return nil, readErr("list folders", err)
	}
	defer rows.Close()
	var out []*core.Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, readErr("list folders", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("list folders", err)
	}
	return out, nil
}

// UpdateFolder writes the name, path, parent, position, index note and
// UpdatedAt of a folder. An empty Name is taken from the path. When the path
// changes, every item below the folder moves with it as in MoveFolder. A
// trashed folder accepts no path or parent change.
func (d *DB) UpdateFolder(ctx context.Context, f *core.Folder) error {
	if f == nil {
		return fmt.Errorf("index: update folder: nil folder: %w", core.ErrInvalid)
	}
	if f.Name == "" {
		f.Name = baseOf(f.Path)
	}
	if f.UpdatedAt.IsZero() {
		f.UpdatedAt = time.Now().UTC()
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		cur, delAt, err := getFolderQ(ctx, t, f.ID)
		if err != nil {
			return err
		}
		f.SpaceID = cur.SpaceID
		return d.placeFolder(ctx, t, cur, delAt, f, false)
	})
}

// MoveFolder sets the parent and path of a live folder and rewrites the path
// of every folder, note and file below it. Each changed row takes the next
// revision. Moving a folder into itself or below itself returns
// core.ErrInvalid.
func (d *DB) MoveFolder(ctx context.Context, id, parentID, relPath string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		cur, delAt, err := getFolderQ(ctx, t, id)
		if err != nil {
			return err
		}
		next := *cur
		next.ParentID = parentID
		next.Path = relPath
		next.Name = baseOf(relPath)
		next.UpdatedAt = time.Now().UTC()
		return d.placeFolder(ctx, t, cur, delAt, &next, false)
	})
}

// placeFolder writes next over cur and moves the subtree when the path
// changes. With restore set it also clears the deleted mark on the folder
// and on the items trashed with it.
func (d *DB) placeFolder(ctx context.Context, t *sql.Tx, cur *core.Folder, curDelAt string, next *core.Folder, restore bool) error {
	if err := validEntryPath(next.Path); err != nil {
		return fmt.Errorf("index: place folder: %w", err)
	}
	if next.Name != baseOf(next.Path) {
		return fmt.Errorf("index: place folder: %w", core.Invalid("name", "name must equal the last path segment"))
	}
	moved := next.Path != cur.Path || next.ParentID != cur.ParentID
	live := !cur.Deleted || restore
	if !live && moved {
		return fmt.Errorf("index: move folder: %w", core.Invalid("id", "a trashed folder cannot move"))
	}
	if !strings.EqualFold(next.Path, cur.Path) && hasFoldPrefix(next.Path, cur.Path+"/") {
		return fmt.Errorf("index: move folder: %w", core.Invalid("parentId", "a folder cannot move into itself"))
	}
	if live && (moved || restore || next.Name != cur.Name) {
		if err := checkPlacement(ctx, t, "place folder", cur.SpaceID, cur.ID, next.ParentID, next.Name, next.Path); err != nil {
			return err
		}
	}
	var rows []subRow
	if moved || restore {
		var err error
		if rows, err = loadSubtree(ctx, t, cur, curDelAt); err != nil {
			return err
		}
	}

	rev, err := nextRev(ctx, t)
	if err != nil {
		return err
	}
	deleted, delAt, delBy := boolInt(cur.Deleted), fmtTimePtr(cur.DeletedAt), cur.DeletedBy
	if restore {
		deleted, delAt, delBy = 0, "", ""
	}
	res, err := t.ExecContext(ctx, `UPDATE folders SET name = ?, path = ?, parent_id = ?, position = ?,
		index_note_id = ?, updated_at = ?, deleted = ?, deleted_at = ?, deleted_by = ?, rev = ?
		WHERE id = ?`,
		next.Name, next.Path, next.ParentID, next.Position, next.IndexNoteID,
		fmtTime(next.UpdatedAt), deleted, delAt, delBy, rev, cur.ID)
	if err := affected("place folder", res, err); err != nil {
		return err
	}
	next.Rev = rev

	var touched []string
	for _, r := range rows {
		newPath, changed := movedPath(r.path, cur.Path, next.Path)
		revive := restore && r.deleted && r.deletedAt == curDelAt
		if !changed && !revive {
			continue
		}
		rev, err := nextRev(ctx, t)
		if err != nil {
			return err
		}
		q := `UPDATE ` + tableOf(r.kind) + ` SET path = ?, rev = ? WHERE id = ?`
		args := []any{newPath, rev, r.id}
		if revive {
			q = `UPDATE ` + tableOf(r.kind) + ` SET path = ?, rev = ?, deleted = 0, deleted_at = '',
				deleted_by = '' WHERE id = ?`
		}
		if _, err := t.ExecContext(ctx, q, args...); err != nil {
			return writeErr("move subtree", err)
		}
		if revive || !r.deleted {
			touched = append(touched, r.id)
		}
	}
	if !live {
		return nil
	}
	if err := resolveDanglingItem(ctx, t, cur.ID); err != nil {
		return err
	}
	for _, id := range touched {
		if err := resolveDanglingItem(ctx, t, id); err != nil {
			return err
		}
	}
	return nil
}

// TrashFolder marks a live folder and every live item below it deleted, with
// the same time and actor, each with the next revision. The shared time is
// what RestoreFolder and DeleteFolder use to find the items trashed with the
// folder. Trashing a trashed folder does nothing.
func (d *DB) TrashFolder(ctx context.Context, id string, at time.Time, by string) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		cur, delAt, err := getFolderQ(ctx, t, id)
		if err != nil {
			return err
		}
		if cur.Deleted {
			return nil
		}
		rows, err := loadSubtree(ctx, t, cur, delAt)
		if err != nil {
			return err
		}
		stamp := fmtTime(at)
		mark := func(table, id string) error {
			rev, err := nextRev(ctx, t)
			if err != nil {
				return err
			}
			_, err = t.ExecContext(ctx, `UPDATE `+table+` SET deleted = 1, deleted_at = ?, deleted_by = ?, rev = ?
				WHERE id = ?`, stamp, by, rev, id)
			if err != nil {
				return writeErr("trash folder", err)
			}
			return nil
		}
		if err := mark("folders", cur.ID); err != nil {
			return err
		}
		for _, r := range rows {
			if r.deleted {
				continue
			}
			if err := mark(tableOf(r.kind), r.id); err != nil {
				return err
			}
		}
		return nil
	})
}

// RestoreFolder clears the deleted mark on a trashed folder and on the items
// trashed with it, and places the folder at relPath under parentID. An empty
// relPath keeps the old path. The whole subtree moves when the path changes.
// A path taken by a live item returns core.ErrExists. Restoring a live folder
// does nothing.
func (d *DB) RestoreFolder(ctx context.Context, id, parentID, relPath string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		cur, delAt, err := getFolderQ(ctx, t, id)
		if err != nil {
			return err
		}
		if !cur.Deleted {
			return nil
		}
		next := *cur
		if relPath != "" {
			next.Path = relPath
			next.Name = baseOf(relPath)
		}
		next.ParentID = parentID
		next.Deleted = false
		next.DeletedAt = nil
		next.DeletedBy = ""
		// A trashed folder refuses a move, so the check runs as a restore.
		return d.placeFolder(ctx, t, cur, delAt, &next, true)
	})
}

// DeleteFolder removes a folder and the items below it that share its state:
// every live item below a live folder, or the items trashed with a trashed
// folder. Comments, shares, public links and stars on them go too, and links
// to them become unresolved. Items trashed on their own below the folder stay
// in the trash.
func (d *DB) DeleteFolder(ctx context.Context, id string) error {
	return d.tx(ctx, func(t *sql.Tx) error {
		cur, delAt, err := getFolderQ(ctx, t, id)
		if err != nil {
			return err
		}
		rows, err := loadSubtree(ctx, t, cur, delAt)
		if err != nil {
			return err
		}
		folders := []string{cur.ID}
		var notes, files []string
		for _, r := range rows {
			same := !r.deleted
			if cur.Deleted {
				same = r.deleted && r.deletedAt == delAt
			}
			if !same {
				continue
			}
			switch r.kind {
			case core.KindFolder:
				folders = append(folders, r.id)
			case core.KindNote:
				notes = append(notes, r.id)
			case core.KindFile:
				files = append(files, r.id)
			}
		}
		return deleteItemRows(ctx, t, folders, notes, files)
	})
}
