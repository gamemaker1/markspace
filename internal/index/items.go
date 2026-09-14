package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// dbtx is satisfied by *sql.DB and *sql.Tx.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// idChunk caps the number of ids bound into one IN list.
const idChunk = 500

// Path helpers. Entry paths are slash separated and relative to the space
// directory, with no leading or trailing slash.

// dirOf returns the parent directory of an entry path, "" for the root.
func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

// baseOf returns the last segment of an entry path.
func baseOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// joinPath joins a directory and a name. An empty directory is the root.
func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// validEntryPath checks the structure of an entry path. Name rules belong to
// the store. The index only refuses paths it cannot place in a tree.
func validEntryPath(p string) error {
	if p == "" {
		return core.Invalid("path", "path is required")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return core.Invalid("path", "path has an empty, . or .. segment")
		}
	}
	return nil
}

// hasFoldPrefix reports whether s starts with prefix under Unicode case
// folding.
func hasFoldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// isASCII reports whether s holds only ASCII bytes.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// validKind checks an item kind.
func validKind(k core.ItemKind) error {
	switch k {
	case core.KindFolder, core.KindNote, core.KindFile:
		return nil
	}
	return core.Invalid("kind", "kind must be folder, note or file")
}

// itemTables maps each kind to its table.
var itemTables = []struct {
	kind  core.ItemKind
	table string
}{
	{core.KindFolder, "folders"},
	{core.KindNote, "notes"},
	{core.KindFile, "files"},
}

// tableOf returns the table of a kind.
func tableOf(k core.ItemKind) string {
	for _, it := range itemTables {
		if it.kind == k {
			return it.table
		}
	}
	return ""
}

// itemKindQ looks an id up across the three item tables.
func itemKindQ(ctx context.Context, q dbtx, id string) (core.ItemKind, string, error) {
	var kind, spaceID string
	err := q.QueryRowContext(ctx, `SELECT 'folder', space_id FROM folders WHERE id = ?1
		UNION ALL SELECT 'note', space_id FROM notes WHERE id = ?1
		UNION ALL SELECT 'file', space_id FROM files WHERE id = ?1
		LIMIT 1`, id).Scan(&kind, &spaceID)
	if err != nil {
		return "", "", readErr("item kind "+id, err)
	}
	return core.ItemKind(kind), spaceID, nil
}

// ItemKind returns the kind and space of an id. It returns core.ErrNotFound
// when no folder, note or file has the id.
func (d *DB) ItemKind(ctx context.Context, id string) (core.ItemKind, string, error) {
	return itemKindQ(ctx, d.sql, id)
}

// siblingTaken reports whether a live folder, note or file other than
// exceptID sits at relPath. The unique indexes compare ASCII case only, so a
// path with other letters also scans its siblings with Unicode folding.
func siblingTaken(ctx context.Context, q dbtx, spaceID, relPath, exceptID string) (bool, error) {
	for _, it := range itemTables {
		var n int
		err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+it.table+`
			WHERE space_id = ? AND deleted = 0 AND path = ? COLLATE NOCASE AND id != ?`,
			spaceID, relPath, exceptID).Scan(&n)
		if err != nil {
			return false, readErr("sibling name", err)
		}
		if n > 0 {
			return true, nil
		}
	}
	if isASCII(relPath) {
		return false, nil
	}
	dir := dirOf(relPath)
	for _, it := range itemTables {
		cond := `instr(path, '/') = 0`
		args := []any{spaceID, exceptID}
		if dir != "" {
			prefix := dir + "/"
			cond = `substr(path, 1, ?) = ? COLLATE NOCASE`
			args = append(args, utf8.RuneCountInString(prefix), prefix)
		}
		rows, err := q.QueryContext(ctx, `SELECT path FROM `+it.table+`
			WHERE space_id = ? AND deleted = 0 AND id != ? AND `+cond, args...)
		if err != nil {
			return false, readErr("sibling name", err)
		}
		found := false
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return false, readErr("sibling name", err)
			}
			if strings.EqualFold(p, relPath) {
				found = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, readErr("sibling name", err)
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

// SiblingNameTaken reports whether a live folder, note or file other than
// exceptID has the name in the parent directory. A note's name is its file
// name with the .md extension.
func (d *DB) SiblingNameTaken(ctx context.Context, spaceID, parentPath, name, exceptID string) (bool, error) {
	return siblingTaken(ctx, d.sql, spaceID, joinPath(strings.Trim(parentPath, "/"), name), exceptID)
}

// checkPlacement verifies that a live folder or file fits the tree at
// relPath. The name must be the last segment, the parent id must name the
// live folder at the parent directory with that exact path, and no other live
// item may hold the path.
func checkPlacement(ctx context.Context, q dbtx, what, spaceID, id, parentID, name, relPath string) error {
	if err := validEntryPath(relPath); err != nil {
		return fmt.Errorf("index: %s: %w", what, err)
	}
	if name != baseOf(relPath) {
		return fmt.Errorf("index: %s: %w", what, core.Invalid("name", "name must equal the last path segment"))
	}
	if dir := dirOf(relPath); dir == "" {
		if parentID != "" {
			return fmt.Errorf("index: %s: %w", what, core.Invalid("parentId", "an item at the space root has no parent"))
		}
	} else {
		var pid, ppath string
		err := q.QueryRowContext(ctx, `SELECT id, path FROM folders
			WHERE space_id = ? AND deleted = 0 AND path = ? COLLATE NOCASE`, spaceID, dir).Scan(&pid, &ppath)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("index: %s: %w", what, core.Invalid("parentId", "no live folder at "+dir))
		}
		if err != nil {
			return readErr(what, err)
		}
		if pid != parentID || ppath != dir {
			return fmt.Errorf("index: %s: %w", what, core.Invalid("parentId", "parent id does not match the folder at "+dir))
		}
	}
	taken, err := siblingTaken(ctx, q, spaceID, relPath, id)
	if err != nil {
		return err
	}
	if taken {
		return fmt.Errorf("index: %s %q: %w", what, relPath, core.ErrExists)
	}
	return nil
}

// TreeItems returns the live folders, notes and files of one space, each
// list sorted by path.
func (d *DB) TreeItems(ctx context.Context, spaceID string) (*ports.TreeRows, error) {
	out := &ports.TreeRows{}
	var err error
	if out.Folders, err = d.ListFolders(ctx, spaceID, false); err != nil {
		return nil, err
	}
	rows, err := d.sql.QueryContext(ctx, `SELECT `+noteColumns+` FROM notes n
		WHERE n.space_id = ? AND n.deleted = 0 ORDER BY n.path`, spaceID)
	if err != nil {
		return nil, readErr("tree items", err)
	}
	out.Notes, err = collectNotes(rows, "tree items")
	rows.Close()
	if err != nil {
		return nil, err
	}
	if out.Files, err = d.ListFiles(ctx, spaceID, ports.FileListOptions{Sort: "path"}); err != nil {
		return nil, err
	}
	return out, nil
}

// Ancestors returns the ids of the folders above relPath, nearest first. A
// live folder wins over a trashed one at the same path, and a level with no
// folder row is skipped.
func (d *DB) Ancestors(ctx context.Context, spaceID, relPath string) ([]string, error) {
	var out []string
	for dir := dirOf(strings.Trim(relPath, "/")); dir != ""; dir = dirOf(dir) {
		var id string
		err := d.sql.QueryRowContext(ctx, `SELECT id FROM folders
			WHERE space_id = ? AND path = ? COLLATE NOCASE
			ORDER BY deleted, deleted_at DESC LIMIT 1`, spaceID, dir).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, readErr("ancestors", err)
		}
		out = append(out, id)
	}
	return out, nil
}

// changedScope builds the space-or-id condition of the change feed for one
// table alias.
func changedScope(alias string, spaceIDs, itemIDs []string) (string, []any) {
	var scope []string
	var args []any
	if len(spaceIDs) > 0 {
		scope = append(scope, alias+`.space_id IN (`+placeholders(len(spaceIDs))+`)`)
		args = append(args, stringArgs(spaceIDs)...)
	}
	if len(itemIDs) > 0 {
		scope = append(scope, alias+`.id IN (`+placeholders(len(itemIDs))+`)`)
		args = append(args, stringArgs(itemIDs)...)
	}
	return `(` + strings.Join(scope, " OR ") + `)`, args
}

// ItemsChangedSince returns folders, notes and files that are in spaceIDs or
// have an id in itemIDs, with a revision in (since, upTo], merged in revision
// order, at most limit. Trashed items are included and notes carry no
// content. Every kind takes revisions from one counter, so no two items
// share a revision.
func (d *DB) ItemsChangedSince(ctx context.Context, spaceIDs, itemIDs []string, since, upTo int64, limit int) ([]*core.TreeItem, error) {
	if len(spaceIDs) == 0 && len(itemIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	var out []*core.TreeItem
	run := func(query, alias string, scan func(*sql.Rows) (*core.TreeItem, error)) error {
		scope, scopeArgs := changedScope(alias, spaceIDs, itemIDs)
		args := append([]any{since, upTo}, scopeArgs...)
		args = append(args, limit)
		rows, err := d.sql.QueryContext(ctx, query+` WHERE `+alias+`.rev > ? AND `+alias+`.rev <= ? AND `+
			scope+` ORDER BY `+alias+`.rev LIMIT ?`, args...)
		if err != nil {
			return readErr("items changed since", err)
		}
		defer rows.Close()
		for rows.Next() {
			it, err := scan(rows)
			if err != nil {
				return readErr("items changed since", err)
			}
			out = append(out, it)
		}
		if err := rows.Err(); err != nil {
			return readErr("items changed since", err)
		}
		return nil
	}
	err := run(`SELECT `+folderColumns+` FROM folders fo`, "fo", func(r *sql.Rows) (*core.TreeItem, error) {
		f, err := scanFolder(r)
		return &core.TreeItem{Kind: core.KindFolder, Folder: f}, err
	})
	if err != nil {
		return nil, err
	}
	err = run(`SELECT `+noteColumns+` FROM notes n`, "n", func(r *sql.Rows) (*core.TreeItem, error) {
		n, err := scanNote(r)
		return &core.TreeItem{Kind: core.KindNote, Note: n}, err
	})
	if err != nil {
		return nil, err
	}
	err = run(`SELECT `+fileColumns+` FROM files fi`, "fi", func(r *sql.Rows) (*core.TreeItem, error) {
		f, err := scanFile(r)
		return &core.TreeItem{Kind: core.KindFile, File: f}, err
	})
	if err != nil {
		return nil, err
	}
	// Each kind returned its own first limit rows, so the first limit rows of
	// the merge are the first limit rows overall.
	sort.Slice(out, func(i, j int) bool { return treeItemRev(out[i]) < treeItemRev(out[j]) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// treeItemRev returns the revision of whichever item is set.
func treeItemRev(it *core.TreeItem) int64 {
	switch {
	case it.Folder != nil:
		return it.Folder.Rev
	case it.Note != nil:
		return it.Note.Rev
	case it.File != nil:
		return it.File.Rev
	}
	return 0
}

// ItemIDsInSpaces lists every folder, note and file id in the spaces, trashed
// items included, sorted by id.
func (d *DB) ItemIDsInSpaces(ctx context.Context, spaceIDs []string) ([]string, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	list := `(` + placeholders(len(spaceIDs)) + `)`
	args := stringArgs(spaceIDs)
	all := append(append(append([]any{}, args...), args...), args...)
	rows, err := d.sql.QueryContext(ctx, `SELECT id FROM folders WHERE space_id IN `+list+`
		UNION ALL SELECT id FROM notes WHERE space_id IN `+list+`
		UNION ALL SELECT id FROM files WHERE space_id IN `+list+`
		ORDER BY id`, all...)
	if err != nil {
		return nil, readErr("item ids in spaces", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, readErr("item ids in spaces", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("item ids in spaces", err)
	}
	return out, nil
}

// Subtrees.

// subRow is one item below a folder.
type subRow struct {
	kind      core.ItemKind
	id        string
	path      string
	deleted   bool
	deletedAt string
}

// loadSubtree returns the items below a folder, sorted by path. An item
// belongs to the subtree when its parent chain reaches the folder, or when
// its path lies under the folder path and it shares the folder's state: live
// for a live folder, trashed at the same instant for a trashed one. The
// parent chain finds items trashed on their own below a live folder. The
// state rule keeps an older trashed subtree at the same path apart.
func loadSubtree(ctx context.Context, q dbtx, root *core.Folder, rootDeletedAt string) ([]subRow, error) {
	prefix := root.Path + "/"
	state := `deleted = 0`
	if root.Deleted {
		state = `deleted = 1 AND deleted_at = :dat`
	}
	near := `(` + state + ` AND substr(path, 1, :plen) = :prefix)`
	query := `WITH RECURSIVE sub(id) AS (
			SELECT :root
			UNION
			SELECT fo.id FROM folders fo JOIN sub ON fo.parent_id = sub.id WHERE fo.space_id = :space
		)
		SELECT 'folder', id, path, deleted, deleted_at FROM folders
			WHERE space_id = :space AND id != :root AND (id IN (SELECT id FROM sub) OR ` + near + `)
		UNION ALL
		SELECT 'note', id, path, deleted, deleted_at FROM notes
			WHERE space_id = :space AND (parent_id IN (SELECT id FROM sub) OR ` + near + `)
		UNION ALL
		SELECT 'file', id, path, deleted, deleted_at FROM files
			WHERE space_id = :space AND (parent_id IN (SELECT id FROM sub) OR ` + near + `)
		ORDER BY 3, 2`
	rows, err := q.QueryContext(ctx, query,
		sql.Named("root", root.ID), sql.Named("space", root.SpaceID),
		sql.Named("dat", rootDeletedAt), sql.Named("plen", utf8.RuneCountInString(prefix)),
		sql.Named("prefix", prefix))
	if err != nil {
		return nil, readErr("load subtree", err)
	}
	defer rows.Close()
	var out []subRow
	for rows.Next() {
		var (
			r       subRow
			kind    string
			deleted int
		)
		if err := rows.Scan(&kind, &r.id, &r.path, &deleted, &r.deletedAt); err != nil {
			return nil, readErr("load subtree", err)
		}
		r.kind = core.ItemKind(kind)
		r.deleted = intBool(deleted)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("load subtree", err)
	}
	return out, nil
}

// movedPath returns the path of a subtree row after its root moves from
// oldRoot to newRoot, and whether it changes.
func movedPath(p, oldRoot, newRoot string) (string, bool) {
	if oldRoot == newRoot || !strings.HasPrefix(p, oldRoot+"/") {
		return p, false
	}
	return newRoot + p[len(oldRoot):], true
}

// deleteItemRows removes the rows of folders, notes and files, with their
// comments, shares, public links and stars. Links that pointed at them
// become unresolved.
func deleteItemRows(ctx context.Context, t *sql.Tx, folderIDs, noteIDs, fileIDs []string) error {
	all := make([]string, 0, len(folderIDs)+len(noteIDs)+len(fileIDs))
	all = append(append(append(all, folderIDs...), noteIDs...), fileIDs...)
	for start := 0; start < len(all); start += idChunk {
		chunk := all[start:min(start+idChunk, len(all))]
		list := `(` + placeholders(len(chunk)) + `)`
		args := stringArgs(chunk)
		for _, q := range []string{
			`DELETE FROM comments WHERE note_id IN ` + list,
			`DELETE FROM shares WHERE note_id IN ` + list,
			`DELETE FROM public_links WHERE note_id IN ` + list,
			`DELETE FROM stars WHERE note_id IN ` + list,
			`DELETE FROM files_fts WHERE file_id IN ` + list,
			`DELETE FROM files WHERE id IN ` + list,
			`DELETE FROM folders WHERE id IN ` + list,
		} {
			if _, err := t.ExecContext(ctx, q, args...); err != nil {
				return writeErr("delete item rows", err)
			}
		}
	}
	for start := 0; start < len(noteIDs); start += idChunk {
		if err := deleteNoteRows(ctx, t, noteIDs[start:min(start+idChunk, len(noteIDs))]); err != nil {
			return err
		}
	}
	for start := 0; start < len(all); start += idChunk {
		chunk := all[start:min(start+idChunk, len(all))]
		_, err := t.ExecContext(ctx, `UPDATE note_links SET target_note_id = NULL, target_kind = ''
			WHERE target_note_id IN (`+placeholders(len(chunk))+`)`, stringArgs(chunk)...)
		if err != nil {
			return writeErr("delete item rows", err)
		}
	}
	return nil
}
