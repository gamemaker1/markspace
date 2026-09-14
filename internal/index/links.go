package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// isMarkdownForm reports whether a link form resolves only by path.
func isMarkdownForm(form string) bool {
	return form == ports.LinkMarkdown || form == ports.LinkMarkdownImage
}

// markdownLinkPath turns a markdown link target into an entry path from the
// space root. A target starting with / is already from the root. Anything
// else is relative to the folder of the note. The fragment goes and the rest
// is percent-decoded. A path that climbs above the root keeps its leading
// "..", so it never resolves.
func markdownLinkPath(notePath, target string) string {
	t := strings.TrimSpace(target)
	if strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">") && len(t) >= 2 {
		t = t[1 : len(t)-1]
	}
	if i := strings.IndexByte(t, '#'); i >= 0 {
		t = t[:i]
	}
	if dec, err := url.PathUnescape(t); err == nil {
		t = dec
	}
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	var p string
	if strings.HasPrefix(t, "/") {
		p = path.Clean(strings.TrimLeft(t, "/"))
	} else {
		p = path.Clean(joinPath(dirOf(notePath), t))
	}
	if p == "." {
		return ""
	}
	return strings.TrimPrefix(p, "/")
}

// storedLinkTarget returns the target stored for a link of the note at
// notePath.
func storedLinkTarget(notePath string, l ports.NoteLink) string {
	if isMarkdownForm(l.Form) {
		return markdownLinkPath(notePath, l.Target)
	}
	return normalizeTarget(l.Target)
}

// replaceNoteLinks rewrites the outgoing links of one note. A link with a
// TargetID keeps it when the id names an item in the same space. Any other
// link is resolved here.
func replaceNoteLinks(ctx context.Context, t *sql.Tx, n *core.Note, row ports.NoteRow) error {
	if _, err := t.ExecContext(ctx, `DELETE FROM note_links WHERE note_id = ?`, n.ID); err != nil {
		return writeErr("replace note links", err)
	}
	refs := row.LinkRefs
	if refs == nil {
		refs = make([]ports.NoteLink, 0, len(row.Links))
		for _, raw := range row.Links {
			refs = append(refs, ports.NoteLink{Target: raw})
		}
	}
	seen := make(map[string]bool, len(refs))
	for _, l := range refs {
		target := storedLinkTarget(n.Path, l)
		if target == "" {
			continue
		}
		key := l.Form + "\x00" + fmt.Sprint(l.Embed) + "\x00" + l.Heading + "\x00" + strings.ToLower(target)
		if seen[key] {
			continue
		}
		seen[key] = true

		var (
			id   string
			kind core.ItemKind
		)
		if l.TargetID != "" {
			k, space, err := itemKindQ(ctx, t, l.TargetID)
			switch {
			case err == nil && space == n.SpaceID:
				id, kind = l.TargetID, k
			case err != nil && !errors.Is(err, core.ErrNotFound):
				return err
			}
		} else {
			var err error
			if id, kind, err = resolveItemTarget(ctx, t, n.SpaceID, dirOf(n.Path), l.Form, target); err != nil {
				return err
			}
		}
		var idArg any
		if id != "" {
			idArg = id
		}
		_, err := t.ExecContext(ctx, `INSERT INTO note_links (note_id, space_id, target, target_note_id,
			target_kind, form, embed, heading) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			n.ID, n.SpaceID, target, idArg, string(kind), l.Form, boolInt(l.Embed), l.Heading)
		if err != nil {
			return writeErr("replace note links", err)
		}
	}
	return nil
}

// linkCand is one item a link target could name.
type linkCand struct {
	id   string
	kind core.ItemKind
	path string
}

// treeDistance counts the folder steps between two directories.
func treeDistance(a, b string) int {
	var as, bs []string
	if a != "" {
		as = strings.Split(a, "/")
	}
	if b != "" {
		bs = strings.Split(b, "/")
	}
	common := 0
	for common < len(as) && common < len(bs) && as[common] == bs[common] {
		common++
	}
	return len(as) + len(bs) - 2*common
}

// pickCandidate applies the tie rules: nearest folder to fromDir by tree
// distance, then the shortest path, then the lexically smallest path.
func pickCandidate(fromDir string, cands []linkCand) (string, core.ItemKind) {
	if len(cands) == 0 {
		return "", ""
	}
	sort.SliceStable(cands, func(i, j int) bool {
		di, dj := treeDistance(fromDir, dirOf(cands[i].path)), treeDistance(fromDir, dirOf(cands[j].path))
		if di != dj {
			return di < dj
		}
		if len(cands[i].path) != len(cands[j].path) {
			return len(cands[i].path) < len(cands[j].path)
		}
		return cands[i].path < cands[j].path
	})
	return cands[0].id, cands[0].kind
}

// queryCands runs a query selecting id, kind and path.
func queryCands(ctx context.Context, q dbtx, query string, args ...any) ([]linkCand, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, readErr("resolve link", err)
	}
	defer rows.Close()
	var out []linkCand
	for rows.Next() {
		var c linkCand
		var kind string
		if err := rows.Scan(&c.id, &kind, &c.path); err != nil {
			return nil, readErr("resolve link", err)
		}
		c.kind = core.ItemKind(kind)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("resolve link", err)
	}
	return out, nil
}

// resolveItemTarget finds the live item a stored link target names in one
// space. It returns empty values when nothing matches.
//
// The markdown forms match the entry path exactly, a note also without its
// .md extension. A target with a slash matches the same way, then notes and
// files whose path ends with the target. A bare name matches, in order, a
// file name, a note slug equal to the slug of the target, a note title and a
// folder name. A folder with a folder note resolves to the note. The legacy
// empty form tries the title, slug and path rule of ResolveLink first.
// Matching ignores ASCII case.
func resolveItemTarget(ctx context.Context, q dbtx, spaceID, fromDir, form, target string) (string, core.ItemKind, error) {
	if target == "" {
		return "", "", nil
	}
	if form == "" {
		id, err := resolveTargetID(ctx, q, spaceID, target)
		if err != nil || id != "" {
			return id, core.KindNote, err
		}
	}
	exact := func() ([]linkCand, error) {
		return queryCands(ctx, q, `SELECT id, 'file', path FROM files
				WHERE space_id = :space AND deleted = 0 AND path = :p COLLATE NOCASE
			UNION ALL SELECT id, 'folder', path FROM folders
				WHERE space_id = :space AND deleted = 0 AND path = :p COLLATE NOCASE
			UNION ALL SELECT id, 'note', path FROM notes
				WHERE space_id = :space AND deleted = 0 AND (path = :p COLLATE NOCASE OR path = :pmd COLLATE NOCASE)`,
			sql.Named("space", spaceID), sql.Named("p", target), sql.Named("pmd", target+".md"))
	}
	if isMarkdownForm(form) || strings.Contains(target, "/") {
		cands, err := exact()
		if err != nil || len(cands) > 0 || isMarkdownForm(form) {
			id, kind := pickCandidate(fromDir, cands)
			return id, kind, err
		}
		suffix := "/" + strings.ToLower(target)
		n := utf8.RuneCountInString(suffix)
		cands, err = queryCands(ctx, q, `SELECT id, 'file', path FROM files
				WHERE space_id = :space AND deleted = 0 AND substr(lower(path), -:n) = :s
			UNION ALL SELECT id, 'note', path FROM notes
				WHERE space_id = :space AND deleted = 0
				AND (substr(lower(path), -:n) = :s OR substr(lower(path), -:nmd) = :smd)`,
			sql.Named("space", spaceID), sql.Named("n", n), sql.Named("s", suffix),
			sql.Named("nmd", n+3), sql.Named("smd", suffix+".md"))
		id, kind := pickCandidate(fromDir, cands)
		return id, kind, err
	}

	tiers := []struct {
		query string
		arg   string
	}{
		{`SELECT id, 'file', path FROM files WHERE space_id = ? AND deleted = 0 AND name = ? COLLATE NOCASE`, target},
		{`SELECT id, 'note', path FROM notes WHERE space_id = ? AND deleted = 0 AND slug = ?`, core.Slugify(target)},
		{`SELECT id, 'note', path FROM notes WHERE space_id = ? AND deleted = 0 AND title = ? COLLATE NOCASE`, target},
		{`SELECT COALESCE(n.id, fo.id), CASE WHEN n.id IS NULL THEN 'folder' ELSE 'note' END,
				COALESCE(n.path, fo.path)
			FROM folders fo LEFT JOIN notes n ON n.id = fo.index_note_id AND n.deleted = 0
			WHERE fo.space_id = ? AND fo.deleted = 0 AND fo.name = ? COLLATE NOCASE`, target},
	}
	for _, tier := range tiers {
		if tier.arg == "" {
			continue
		}
		cands, err := queryCands(ctx, q, tier.query, spaceID, tier.arg)
		if err != nil {
			return "", "", err
		}
		if len(cands) > 0 {
			id, kind := pickCandidate(fromDir, cands)
			return id, kind, nil
		}
	}
	return "", "", nil
}

// linkable describes a live item for dangling link resolution.
type linkable struct {
	kind    core.ItemKind
	id      string
	spaceID string
	path    string
	name    string
	title   string
	slug    string
}

// loadLinkable reads the fields dangling resolution needs. It reports false
// for a trashed or missing item.
func loadLinkable(ctx context.Context, q dbtx, id string) (linkable, bool, error) {
	var (
		it      linkable
		kind    string
		deleted int
	)
	err := q.QueryRowContext(ctx, `SELECT 'folder', id, space_id, path, name, '', '', deleted FROM folders WHERE id = ?1
		UNION ALL SELECT 'note', id, space_id, path, '', title, slug, deleted FROM notes WHERE id = ?1
		UNION ALL SELECT 'file', id, space_id, path, name, '', '', deleted FROM files WHERE id = ?1
		LIMIT 1`, id).Scan(&kind, &it.id, &it.spaceID, &it.path, &it.name, &it.title, &it.slug, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return it, false, nil
	}
	if err != nil {
		return it, false, readErr("load item", err)
	}
	it.kind = core.ItemKind(kind)
	if it.kind == core.KindNote {
		it.name = baseOf(it.path)
	}
	return it, deleted == 0, nil
}

// linkNames reports whether a stored link names an item, by the rules of
// resolveItemTarget. Case is folded.
func linkNames(it linkable, form, target string) bool {
	eq := strings.EqualFold
	noExt := pathWithoutExt(it.path)
	if isMarkdownForm(form) {
		return eq(target, it.path) || (it.kind == core.KindNote && eq(target, noExt))
	}
	if strings.Contains(target, "/") {
		if eq(target, it.path) || (it.kind == core.KindNote && eq(target, noExt)) {
			return true
		}
		if it.kind == core.KindFolder {
			return false
		}
		return hasFoldSuffix(it.path, "/"+target) || (it.kind == core.KindNote && hasFoldSuffix(noExt, "/"+target))
	}
	switch it.kind {
	case core.KindFile:
		return eq(target, it.name)
	case core.KindFolder:
		return eq(target, it.name)
	default:
		return eq(target, it.title) || eq(target, it.slug) || core.Slugify(target) == it.slug ||
			(form == "" && eq(target, noExt))
	}
}

// hasFoldSuffix reports whether s ends with suffix under case folding.
func hasFoldSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

// resolveDanglingItem points unresolved links in the space of a live item at
// it when they name it. A trashed or missing item changes nothing. A folder
// with a live folder note resolves name links to the note.
func resolveDanglingItem(ctx context.Context, q dbtx, id string) error {
	it, live, err := loadLinkable(ctx, q, id)
	if err != nil || !live {
		return err
	}
	keys := map[string]bool{}
	for _, k := range []string{it.path, it.name, it.title, it.slug, pathWithoutExt(it.path)} {
		if k != "" {
			keys[strings.ToLower(k)] = true
		}
	}
	args := []any{it.spaceID}
	list := make([]string, 0, len(keys))
	for k := range keys {
		list = append(list, k)
		args = append(args, k)
	}
	lowPath, lowNoExt := strings.ToLower(it.path), strings.ToLower(pathWithoutExt(it.path))
	args = append(args, lowPath, lowNoExt)
	rows, err := q.QueryContext(ctx, `SELECT rowid, target, form FROM note_links
		WHERE space_id = ? AND target_note_id IS NULL AND (
			lower(target) IN (`+placeholders(len(list))+`)
			OR (instr(target, '/') > 0 AND (
				substr(?, -(length(target) + 1)) = '/' || lower(target)
				OR substr(?, -(length(target) + 1)) = '/' || lower(target)
			))
		)`, args...)
	if err != nil {
		return readErr("resolve dangling links", err)
	}
	type hit struct {
		rowid int64
	}
	var hits []hit
	for rows.Next() {
		var (
			h            hit
			target, form string
		)
		if err := rows.Scan(&h.rowid, &target, &form); err != nil {
			rows.Close()
			return readErr("resolve dangling links", err)
		}
		if linkNames(it, form, target) {
			hits = append(hits, h)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return readErr("resolve dangling links", err)
	}
	if len(hits) == 0 {
		return nil
	}
	targetID, targetKind := it.id, it.kind
	if it.kind == core.KindFolder {
		var noteID string
		err := q.QueryRowContext(ctx, `SELECT n.id FROM folders fo JOIN notes n ON n.id = fo.index_note_id
			WHERE fo.id = ? AND n.deleted = 0`, it.id).Scan(&noteID)
		if err == nil {
			targetID, targetKind = noteID, core.KindNote
		} else if !errors.Is(err, sql.ErrNoRows) {
			return readErr("resolve dangling links", err)
		}
	}
	for _, h := range hits {
		_, err := q.ExecContext(ctx, `UPDATE note_links SET target_note_id = ?, target_kind = ? WHERE rowid = ?`,
			targetID, string(targetKind), h.rowid)
		if err != nil {
			return writeErr("resolve dangling links", err)
		}
	}
	return nil
}

// ResolveDanglingLinks points unresolved links in the item's space at the
// item when they name it. Creating, moving or restoring an item through this
// package does this already. A trashed item changes nothing, and an unknown
// id returns core.ErrNotFound.
func (d *DB) ResolveDanglingLinks(ctx context.Context, itemID string) error {
	if _, _, err := itemKindQ(ctx, d.sql, itemID); err != nil {
		return err
	}
	return d.tx(ctx, func(t *sql.Tx) error {
		return resolveDanglingItem(ctx, t, itemID)
	})
}

// OutboundLinks returns the stored links of one note in the order they were
// written.
func (d *DB) OutboundLinks(ctx context.Context, noteID string) ([]ports.NoteLink, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT target, form, embed, heading,
		COALESCE(target_note_id, ''), target_kind FROM note_links WHERE note_id = ? ORDER BY rowid`, noteID)
	if err != nil {
		return nil, readErr("outbound links", err)
	}
	defer rows.Close()
	var out []ports.NoteLink
	for rows.Next() {
		var (
			l     ports.NoteLink
			embed int
			kind  string
		)
		if err := rows.Scan(&l.Target, &l.Form, &embed, &l.Heading, &l.TargetID, &kind); err != nil {
			return nil, readErr("outbound links", err)
		}
		l.Embed = intBool(embed)
		l.TargetKind = core.ItemKind(kind)
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("outbound links", err)
	}
	return out, nil
}

// BacklinksTo returns the live notes that link to an item of any kind,
// newest first, each note once.
func (d *DB) BacklinksTo(ctx context.Context, itemID string) ([]*core.Backlink, error) {
	return d.Backlinks(ctx, itemID)
}

// NotesLinkingToIDs returns the live notes with a link resolved to any of the
// ids, each once, sorted by space and path. These are the candidates for a
// link rewrite after a rename or move.
func (d *DB) NotesLinkingToIDs(ctx context.Context, ids []string) ([]*core.Note, error) {
	byID := map[string]*core.Note{}
	for start := 0; start < len(ids); start += idChunk {
		chunk := ids[start:min(start+idChunk, len(ids))]
		rows, err := d.sql.QueryContext(ctx, `SELECT `+noteColumns+` FROM notes n
			WHERE n.deleted = 0 AND n.id IN (
				SELECT l.note_id FROM note_links l WHERE l.target_note_id IN (`+placeholders(len(chunk))+`)
			)`, stringArgs(chunk)...)
		if err != nil {
			return nil, readErr("notes linking to ids", err)
		}
		notes, err := collectNotes(rows, "notes linking to ids")
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, n := range notes {
			byID[n.ID] = n
		}
	}
	out := make([]*core.Note, 0, len(byID))
	for _, n := range byID {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SpaceID != out[j].SpaceID {
			return out[i].SpaceID < out[j].SpaceID
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// SpaceLinks returns the stored outbound links of every live note in a space,
// ordered by note path and then by the order the links were written. The app
// builds the rewrite candidate index from it without parsing every note.
func (d *DB) SpaceLinks(ctx context.Context, spaceID string) ([]ports.SpaceLink, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT n.id, n.path, l.target, l.form, l.embed, l.heading,
		COALESCE(l.target_note_id, ''), l.target_kind
		FROM note_links l JOIN notes n ON n.id = l.note_id
		WHERE n.space_id = ? AND n.deleted = 0 ORDER BY n.path, l.rowid`, spaceID)
	if err != nil {
		return nil, readErr("space links", err)
	}
	defer rows.Close()
	var out []ports.SpaceLink
	for rows.Next() {
		var (
			l     ports.SpaceLink
			embed int
			kind  string
		)
		if err := rows.Scan(&l.NoteID, &l.NotePath, &l.Target, &l.Form, &embed, &l.Heading, &l.TargetID, &kind); err != nil {
			return nil, readErr("space links", err)
		}
		l.Embed = intBool(embed)
		l.TargetKind = core.ItemKind(kind)
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, readErr("space links", err)
	}
	return out, nil
}
