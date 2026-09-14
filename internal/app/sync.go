package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/links"
	"github.com/gamemaker1/markspace/internal/ports"
)

const (
	// excerptRunes is how much plain text goes into Note.Excerpt.
	excerptRunes = 240
	// reconcileSpaces is how many spaces the startup scan walks at once.
	reconcileSpaces = 4
	// renameWindow is how long the watcher keeps an item that vanished from
	// disk, so a write of its new path can still claim its id.
	renameWindow = 10 * time.Second
	// scanDebounce is the quiet time before the watcher rescans a space.
	scanDebounce = time.Second
)

// indexNote computes the derived fields of a note and writes the index row.
// The note body must already be loaded into Note.Content.
func (a *App) indexNote(ctx context.Context, space *core.Space, note *core.Note) error {
	if space == nil || note == nil {
		return fmt.Errorf("index note: missing space or note: %w", core.ErrInvalid)
	}
	snap, _, err := a.loadSnapshot(ctx, space.ID, links.Entry{ID: note.ID, Kind: links.KindNote, Path: note.Path, Title: note.Title})
	if err != nil {
		return err
	}
	return a.indexNoteSnap(ctx, space, note, snap)
}

// indexNoteSnap is indexNote with a snapshot the caller already built. The
// path decides the parent folder, and links resolve against the snapshot.
func (a *App) indexNoteSnap(ctx context.Context, space *core.Space, note *core.Note, snap *links.Snapshot) error {
	if space == nil || note == nil {
		return fmt.Errorf("index note: missing space or note: %w", core.ErrInvalid)
	}
	note.SpaceID = space.ID
	note.Slug = pathSlug(note.Path)
	body := note.Content
	note.Excerpt = a.rend.Excerpt(body, excerptRunes)
	note.Words = a.rend.WordCount(body)
	note.Tags = mergeTags(note.Tags, a.rend.InlineTags(body))
	if !note.Deleted {
		note.ParentID = a.folderIDAt(ctx, space.ID, dirOf(note.Path))
	}
	modTime, size, err := a.files.Stat(space.Slug, note.Path)
	if err != nil {
		// A trashed note has no file at its recorded path. Index it anyway so
		// the trash listing works.
		modTime, size = note.UpdatedAt, int64(len(body))
	}
	refs := noteLinkRefs(snap, note.Path, body)
	if refs == nil {
		refs = []ports.NoteLink{}
	}
	row := ports.NoteRow{Note: note, Plain: a.rend.PlainText(body), ModTime: modTime, Size: size, LinkRefs: refs}
	if err := a.db.UpsertNote(ctx, row); err != nil {
		return fmt.Errorf("index note %s: %w", note.ID, err)
	}
	return nil
}

// reconcile brings the index in line with the files on disk in every space.
// It creates space rows for new directories and returns the number of notes
// and files it indexed.
func (a *App) reconcile(ctx context.Context) (int, error) {
	start := time.Now()
	dirs, err := a.files.ListSpaceDirs()
	if err != nil {
		return 0, fmt.Errorf("list space dirs: %w", err)
	}
	rows, err := a.db.ListSpaces(ctx)
	if err != nil {
		return 0, fmt.Errorf("list spaces: %w", err)
	}
	bySlug := make(map[string]*core.Space, len(rows))
	for _, s := range rows {
		bySlug[s.Slug] = s
	}
	onDisk := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		onDisk[d] = true
	}
	created := 0
	for _, d := range dirs {
		if _, ok := bySlug[d]; ok {
			continue
		}
		sp, err := a.adoptSpaceDir(ctx, d)
		if err != nil {
			a.log.Warn("adopt space directory failed", "slug", d, "err", err)
			continue
		}
		bySlug[d] = sp
		created++
	}
	for _, s := range rows {
		if onDisk[s.Slug] {
			continue
		}
		if err := a.files.EnsureSpace(s.Slug); err != nil {
			a.log.Warn("recreate space directory failed", "slug", s.Slug, "err", err)
		}
	}

	all := make([]*core.Space, 0, len(bySlug))
	for _, s := range bySlug {
		all = append(all, s)
	}
	var (
		mu      sync.Mutex
		indexed int
		removed int
		failed  int
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, reconcileSpaces)
	for _, sp := range all {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			n, drop, err := a.reconcileSpace(ctx, sp, false)
			mu.Lock()
			indexed += n
			removed += drop
			if err != nil {
				failed++
			}
			mu.Unlock()
			if err != nil {
				a.log.Warn("reconcile space failed", "space", sp.Slug, "err", err)
			}
		}()
	}
	wg.Wait()

	a.statMu.Lock()
	a.indexedAt = a.now()
	a.statMu.Unlock()
	a.kickQueue()

	a.log.Info("reconciled index",
		"spaces", len(all),
		"spacesCreated", created,
		"itemsIndexed", indexed,
		"rowsRemoved", removed,
		"spacesFailed", failed,
		"duration", time.Since(start).String())
	return indexed, nil
}

// adoptSpaceDir creates a space row for a directory that has none.
func (a *App) adoptSpaceDir(ctx context.Context, slug string) (*core.Space, error) {
	now := a.now()
	sp := &core.Space{
		ID:        core.NewID(),
		Slug:      slug,
		Name:      slug,
		CreatedBy: a.firstAdminID(ctx),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := a.db.CreateSpace(ctx, sp); err != nil {
		return nil, fmt.Errorf("create space row: %w", err)
	}
	if sp.CreatedBy != "" {
		m := &core.SpaceMember{SpaceID: sp.ID, UserID: sp.CreatedBy, Role: core.RoleOwner, CreatedAt: now}
		if err := a.db.AddMember(ctx, m); err != nil {
			a.log.Warn("add owner to adopted space failed", "space", slug, "err", err)
		}
	}
	a.log.Info("adopted space directory", "slug", slug, "spaceId", sp.ID, "owner", sp.CreatedBy)
	return sp, nil
}

// firstAdminID returns the id of the first instance admin, or the empty string
// when the instance has no users yet.
func (a *App) firstAdminID(ctx context.Context) string {
	users, _, err := a.db.ListUsers(ctx, "", 100, 0)
	if err != nil {
		a.log.Warn("list users failed", "err", err)
		return ""
	}
	for _, u := range users {
		if u.IsAdmin {
			return u.ID
		}
	}
	if len(users) > 0 {
		return users[0].ID
	}
	return ""
}

// reconcileSpace scans one space under its lock. With grace set, as for the
// watcher, an item that vanished from disk less than renameWindow ago keeps
// its row, so a later write of its new path can still claim its id. It
// returns the items indexed and the rows removed.
func (a *App) reconcileSpace(ctx context.Context, space *core.Space, grace bool) (int, int, error) {
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	return a.reconcileLocked(ctx, space, grace)
}

// scanState is the working state of one reconcile pass.
type scanState struct {
	space   *core.Space
	scan    ports.ScanResult
	folders map[string]bool
	files   map[string]ports.ScannedFile
	notes   map[string]*core.Note
	noteIDs map[string]bool
	changes []links.Change
	indexed int
	removed int
	touched bool
}

// reconcileLocked is reconcileSpace with the space lock held. It matches
// items that moved outside the application before it creates or removes
// rows: a note by the id in its frontmatter, a file by size and SHA-256 and
// a folder by the ids known below it. Links follow every match.
func (a *App) reconcileLocked(ctx context.Context, space *core.Space, grace bool) (int, int, error) {
	scan, err := a.files.ScanTree(space.Slug)
	if err != nil {
		return 0, 0, fmt.Errorf("scan space %s: %w", space.Slug, err)
	}
	st := &scanState{
		space: space, scan: scan,
		folders: map[string]bool{}, files: map[string]ports.ScannedFile{},
		notes: map[string]*core.Note{}, noteIDs: map[string]bool{},
	}
	for _, f := range scan.Folders {
		st.folders[f.RelPath] = true
	}
	for _, f := range scan.Files {
		st.files[f.RelPath] = f
	}
	for _, n := range scan.Notes {
		st.notes[n.Note.Path] = n.Note
		if n.Note.ID != "" {
			st.noteIDs[n.Note.ID] = true
		}
	}
	base, err := a.loadRewriteBase(ctx, space.ID)
	if err != nil {
		a.log.Warn("load link index for reconcile failed, links will not follow moves", "space", space.Slug, "err", err)
		base = nil
	}
	rows, err := a.db.TreeItems(ctx, space.ID)
	if err != nil {
		return 0, 0, fmt.Errorf("tree items: %w", err)
	}

	fileMatch, fileSHA := a.matchFiles(space, st, rows)
	if err := a.reconcileFolders(ctx, st, rows, fileMatch); err != nil {
		return st.indexed, st.removed, err
	}
	if err := a.reconcileFiles(ctx, st, fileMatch, fileSHA); err != nil {
		return st.indexed, st.removed, err
	}
	if err := a.reconcileNotes(ctx, st); err != nil {
		return st.indexed, st.removed, err
	}
	trashed := a.reconcileTrashLocked(ctx, space)
	if err := a.dropVanished(ctx, st, trashed, grace); err != nil {
		return st.indexed, st.removed, err
	}
	a.syncFolderNotes(ctx, space.ID)
	if len(st.changes) > 0 {
		a.applyRewrites(ctx, nil, space, base, st.changes)
	}
	if st.touched {
		a.publish("tree.changed", space.ID, "", "", "")
	}
	return st.indexed, st.removed, nil
}

// matchFiles pairs each file on disk at an unknown path with a live file row
// whose path vanished, by size and SHA-256. Among several rows the one with
// the same base name wins, then the one whose folder is nearest, then the
// smallest path. It hashes only files whose size equals a candidate.
func (a *App) matchFiles(space *core.Space, st *scanState, rows *ports.TreeRows) (map[string]*core.File, map[string]string) {
	known := map[string]bool{}
	bySize := map[int64][]*core.File{}
	for _, f := range rows.Files {
		known[f.Path] = true
		if _, onDisk := st.files[f.Path]; !onDisk && f.SHA256 != "" {
			bySize[f.Size] = append(bySize[f.Size], f)
		}
	}
	match := map[string]*core.File{}
	shas := map[string]string{}
	used := map[string]bool{}
	for _, sf := range st.scan.Files {
		if known[sf.RelPath] || len(bySize[sf.Size]) == 0 {
			continue
		}
		sha, err := a.files.HashFile(space.Slug, sf.RelPath)
		if err != nil {
			continue
		}
		shas[sf.RelPath] = sha
		var best *core.File
		for _, c := range bySize[sf.Size] {
			if used[c.ID] || !strings.EqualFold(c.SHA256, sha) {
				continue
			}
			if best == nil || betterFileMatch(sf.RelPath, c, best) {
				best = c
			}
		}
		if best != nil {
			used[best.ID] = true
			match[sf.RelPath] = best
		}
	}
	return match, shas
}

// betterFileMatch reports whether candidate c beats best for a file found at
// newRel.
func betterFileMatch(newRel string, c, best *core.File) bool {
	name := baseOf(newRel)
	if cs, bs := c.Name == name, best.Name == name; cs != bs {
		return cs
	}
	cd, bd := folderDistance(dirOf(c.Path), dirOf(newRel)), folderDistance(dirOf(best.Path), dirOf(newRel))
	if cd != bd {
		return cd < bd
	}
	return c.Path < best.Path
}

// folderDistance counts the folder steps between two folder paths.
func folderDistance(x, y string) int {
	split := func(p string) []string {
		if p == "" {
			return nil
		}
		return strings.Split(p, "/")
	}
	xs, ys := split(x), split(y)
	common := 0
	for common < len(xs) && common < len(ys) && xs[common] == ys[common] {
		common++
	}
	return len(xs) + len(ys) - 2*common
}

// reconcileFolders creates a row for every folder on disk that has none, or
// moves the row of a vanished folder when at least half of the note and file
// ids known below it, and at least one, are found below the new folder.
// Parents come first, so a moved folder carries its subtree to the new path
// before its children are considered.
func (a *App) reconcileFolders(ctx context.Context, st *scanState, rows *ports.TreeRows, fileMatch map[string]*core.File) error {
	space := st.space
	scanned := append([]ports.ScannedFolder(nil), st.scan.Folders...)
	sort.SliceStable(scanned, func(i, j int) bool {
		di, dj := strings.Count(scanned[i].RelPath, "/"), strings.Count(scanned[j].RelPath, "/")
		if di != dj {
			return di < dj
		}
		return scanned[i].RelPath < scanned[j].RelPath
	})
	current := rows
	dirty := false
	used := map[string]bool{}
	for _, sf := range scanned {
		if dirty {
			fresh, err := a.db.TreeItems(ctx, space.ID)
			if err != nil {
				return fmt.Errorf("tree items: %w", err)
			}
			current, dirty = fresh, false
		}
		p := sf.RelPath
		byPath := map[string]*core.Folder{}
		for _, f := range current.Folders {
			byPath[f.Path] = f
		}
		if _, ok := byPath[p]; ok {
			continue
		}
		parentID := ""
		if d := dirOf(p); d != "" {
			pf, ok := byPath[d]
			if !ok {
				a.log.Warn("scanned folder has no indexed parent", "space", space.Slug, "path", p)
				continue
			}
			parentID = pf.ID
		}
		// A folder whose name changed only in case keeps its row.
		var best *core.Folder
		for _, f := range current.Folders {
			if !st.folders[f.Path] && !used[f.ID] && strings.EqualFold(f.Path, p) {
				best = f
				break
			}
		}
		if best == nil {
			best = a.majorityFolder(st, current, p, fileMatch, used)
		}
		now := a.now()
		if best != nil {
			used[best.ID] = true
			next := *best
			next.ParentID, next.Path, next.Name, next.UpdatedAt = parentID, p, baseOf(p), now
			if err := a.db.UpdateFolder(ctx, &next); err != nil {
				a.log.Warn("move folder row failed", "space", space.Slug, "folderId", best.ID, "path", p, "err", err)
			} else {
				st.changes = append(st.changes, links.Change{Kind: "move", ID: best.ID, OldPath: best.Path, NewPath: p})
				st.touched, dirty = true, true
				a.log.Info("folder moved on disk", "space", space.Slug, "folderId", best.ID, "from", best.Path, "to", p)
				a.publishItem("item.moved", core.KindFolder, space.ID, best.ID, "", p)
				continue
			}
		}
		f := &core.Folder{ID: core.NewID(), SpaceID: space.ID, ParentID: parentID, Name: baseOf(p), Path: p, CreatedAt: now, UpdatedAt: now}
		err := a.db.CreateFolder(ctx, f)
		if errors.Is(err, core.ErrExists) && a.clearBlocker(ctx, st, p) {
			err = a.db.CreateFolder(ctx, f)
		}
		if err != nil {
			a.log.Warn("index folder failed", "space", space.Slug, "path", p, "err", err)
			continue
		}
		st.indexed++
		st.touched, dirty = true, true
		a.publishItem("folder.created", core.KindFolder, space.ID, f.ID, "", p)
	}
	return nil
}

// majorityFolder picks the vanished folder whose known note and file ids
// are best represented below the folder found at p.
func (a *App) majorityFolder(st *scanState, current *ports.TreeRows, p string, fileMatch map[string]*core.File, used map[string]bool) *core.Folder {
	below := map[string]bool{}
	for rel, n := range st.notes {
		if underPath(rel, p) && n.ID != "" {
			below[n.ID] = true
		}
	}
	for rel, f := range fileMatch {
		if underPath(rel, p) {
			below[f.ID] = true
		}
	}
	for _, f := range current.Files {
		if underPath(f.Path, p) {
			if _, ok := st.files[f.Path]; ok {
				below[f.ID] = true
			}
		}
	}
	if len(below) == 0 {
		return nil
	}
	var (
		best      *core.Folder
		bestScore int
	)
	for _, v := range current.Folders {
		if st.folders[v.Path] || used[v.ID] {
			continue
		}
		known, overlap := 0, 0
		for _, n := range current.Notes {
			if underPath(n.Path, v.Path) {
				known++
				if below[n.ID] {
					overlap++
				}
			}
		}
		for _, f := range current.Files {
			if underPath(f.Path, v.Path) {
				known++
				if below[f.ID] {
					overlap++
				}
			}
		}
		need := max(1, (known+1)/2)
		if known == 0 || overlap < need {
			continue
		}
		if overlap > bestScore || (overlap == bestScore && best != nil && baseOf(v.Path) == baseOf(p) && baseOf(best.Path) != baseOf(p)) {
			best, bestScore = v, overlap
		}
	}
	return best
}

// clearBlocker removes the row of a vanished note or file that holds the path
// where an entry was found on disk. It reports whether it removed one.
func (a *App) clearBlocker(ctx context.Context, st *scanState, rel string) bool {
	if f, err := a.db.GetFileByPath(ctx, st.space.ID, rel); err == nil && f != nil {
		if _, onDisk := st.files[f.Path]; !onDisk && a.db.DeleteFile(ctx, f.ID) == nil {
			a.dropFileData(st.space, f.ID)
			st.removed++
			return true
		}
	}
	for _, p := range []string{rel, rel + ".md"} {
		if n, err := a.db.GetNoteByPath(ctx, st.space.ID, p); err == nil && n != nil && !n.Deleted {
			if disk, ok := st.notes[n.Path]; !ok || disk.ID != n.ID {
				if a.db.DeleteNoteRow(ctx, n.ID) == nil {
					a.dropNoteRelations(ctx, []*core.Note{n})
					st.removed++
					return true
				}
			}
		}
	}
	if f, err := a.db.GetFolderByPath(ctx, st.space.ID, rel); err == nil && f != nil && !st.folders[f.Path] {
		if a.db.DeleteFolder(ctx, f.ID) == nil {
			st.removed++
			return true
		}
	}
	return false
}

// reconcileFiles updates changed file rows, moves matched rows and creates
// rows for new files.
func (a *App) reconcileFiles(ctx context.Context, st *scanState, fileMatch map[string]*core.File, fileSHA map[string]string) error {
	space := st.space
	rows, err := a.db.TreeItems(ctx, space.ID)
	if err != nil {
		return fmt.Errorf("tree items: %w", err)
	}
	folderAt := map[string]string{}
	for _, f := range rows.Folders {
		folderAt[f.Path] = f.ID
	}
	byPath := map[string]*core.File{}
	byID := map[string]*core.File{}
	for _, f := range rows.Files {
		byPath[f.Path] = f
		byID[f.ID] = f
	}
	for _, sf := range st.scan.Files {
		now := a.now()
		if row, ok := byPath[sf.RelPath]; ok {
			if row.Size == sf.Size && row.ModTime.Equal(sf.ModTime) {
				continue
			}
			next := *row
			next.Size, next.ModTime, next.SHA256, next.UpdatedAt = sf.Size, sf.ModTime, "", now
			if sf.MIME != "" {
				next.MIME = sf.MIME
			}
			next.Width, next.Height = 0, 0
			if err := a.db.UpdateFile(ctx, &next); err != nil {
				a.log.Warn("update changed file failed", "space", space.Slug, "path", sf.RelPath, "err", err)
				continue
			}
			if err := a.files.DeleteThumbnails(row.ID); err != nil {
				a.log.Debug("delete thumbnails failed", "fileId", row.ID, "err", err)
			}
			st.indexed++
			st.touched = true
			a.publishItem("file.updated", core.KindFile, space.ID, row.ID, "", sf.RelPath)
			continue
		}
		dir := dirOf(sf.RelPath)
		parentID, ok := folderAt[dir]
		if dir != "" && !ok {
			a.log.Warn("scanned file has no indexed folder", "space", space.Slug, "path", sf.RelPath)
			continue
		}
		if m, ok := fileMatch[sf.RelPath]; ok {
			if cur := byID[m.ID]; cur != nil {
				if _, stillThere := st.files[cur.Path]; !stillThere {
					next := *cur
					next.Path, next.Name, next.ParentID = sf.RelPath, baseOf(sf.RelPath), parentID
					next.ModTime, next.UpdatedAt = sf.ModTime, now
					if err := a.db.UpdateFile(ctx, &next); err == nil {
						st.changes = append(st.changes, links.Change{Kind: "move", ID: cur.ID, OldPath: cur.Path, NewPath: sf.RelPath})
						st.touched = true
						byPath[sf.RelPath] = &next
						delete(byPath, cur.Path)
						a.log.Info("file moved on disk", "space", space.Slug, "fileId", cur.ID, "from", cur.Path, "to", sf.RelPath)
						a.publishItem("item.moved", core.KindFile, space.ID, cur.ID, "", sf.RelPath)
						continue
					} else {
						a.log.Warn("move file row failed", "space", space.Slug, "fileId", cur.ID, "err", err)
					}
				}
			}
		}
		f := &core.File{
			ID: core.NewID(), SpaceID: space.ID, ParentID: parentID, Name: baseOf(sf.RelPath), Path: sf.RelPath,
			MIME: sf.MIME, Size: sf.Size, SHA256: fileSHA[sf.RelPath], ModTime: sf.ModTime, CreatedAt: now, UpdatedAt: now,
		}
		err := a.db.CreateFile(ctx, f)
		if errors.Is(err, core.ErrExists) && a.clearBlocker(ctx, st, sf.RelPath) {
			err = a.db.CreateFile(ctx, f)
		}
		if err != nil {
			a.log.Warn("index file failed", "space", space.Slug, "path", sf.RelPath, "err", err)
			continue
		}
		byPath[sf.RelPath] = f
		st.indexed++
		st.touched = true
		a.publishItem("file.created", core.KindFile, space.ID, f.ID, "", sf.RelPath)
	}
	return nil
}

// reconcileNotes indexes new and changed notes. A note keeps the id in its
// frontmatter, so a note moved outside the application keeps its row. A
// second file with the same id gets a new one written into its frontmatter.
func (a *App) reconcileNotes(ctx context.Context, st *scanState) error {
	space := st.space
	rows, err := a.db.TreeItems(ctx, space.ID)
	if err != nil {
		return fmt.Errorf("tree items: %w", err)
	}
	folderAt := map[string]string{}
	for _, f := range rows.Folders {
		folderAt[f.Path] = f.ID
	}
	state, err := a.db.IndexState(ctx, space.ID)
	if err != nil {
		return fmt.Errorf("index state %s: %w", space.Slug, err)
	}
	entries := make([]links.Entry, 0, len(rows.Folders)+len(rows.Files)+len(st.scan.Notes))
	for _, f := range rows.Folders {
		entries = append(entries, links.Entry{ID: f.ID, Kind: links.KindFolder, Path: f.Path})
	}
	for _, f := range rows.Files {
		entries = append(entries, links.Entry{ID: f.ID, Kind: links.KindFile, Path: f.Path})
	}
	seen := map[string]bool{}
	// When several files carry one id, the file at the indexed path keeps it.
	counts := map[string]int{}
	for _, sn := range st.scan.Notes {
		if sn.Note.ID != "" {
			counts[sn.Note.ID]++
		}
	}
	keeper := map[string]string{}
	for id, n := range counts {
		if n < 2 {
			continue
		}
		if row, err := a.db.GetNote(ctx, id); err == nil && row != nil && row.SpaceID == space.ID {
			if disk, ok := st.notes[row.Path]; ok && disk.ID == id {
				keeper[id] = row.Path
			}
		}
	}
	type pending struct {
		sn      ports.ScannedNote
		row     *core.Note
		rewrite bool
	}
	var work []pending
	for _, sn := range st.scan.Notes {
		note := sn.Note
		rewrite := false
		if note.ID == "" || seen[note.ID] || (keeper[note.ID] != "" && keeper[note.ID] != note.Path) {
			note.ID = core.NewID()
			rewrite = true
		}
		row, err := a.db.GetNote(ctx, note.ID)
		if err != nil && !errors.Is(err, core.ErrNotFound) {
			a.log.Warn("load note row failed", "noteId", note.ID, "err", err)
			continue
		}
		if row != nil && row.SpaceID != space.ID {
			note.ID, row, rewrite = core.NewID(), nil, true
		}
		seen[note.ID] = true
		entries = append(entries, links.Entry{ID: note.ID, Kind: links.KindNote, Path: note.Path, Title: note.Title})
		work = append(work, pending{sn: sn, row: row, rewrite: rewrite})
	}
	snap := links.NewSnapshot(entries)
	for _, w := range work {
		note := w.sn.Note
		note.SpaceID = space.ID
		rewrite := w.rewrite
		if note.Title == "" {
			note.Title = pathSlug(note.Path)
			rewrite = true
		}
		if note.CreatedAt.IsZero() {
			note.CreatedAt = a.now()
			rewrite = true
		}
		if note.UpdatedAt.IsZero() {
			note.UpdatedAt = note.CreatedAt
			rewrite = true
		}
		if other, ok := state[note.Path]; ok && other.NoteID != note.ID {
			if o, err := a.db.GetNote(ctx, other.NoteID); err == nil && o != nil && !o.Deleted && !seen[o.ID] {
				// The file at this path carries another id. The file wins.
				if err := a.db.DeleteNoteRow(ctx, o.ID); err == nil {
					a.dropNoteRelations(ctx, []*core.Note{o})
					st.removed++
				}
			}
		}
		row := w.row
		if row != nil && !row.Deleted && row.Path != note.Path {
			st.changes = append(st.changes, links.Change{Kind: "move", ID: row.ID, OldPath: row.Path, NewPath: note.Path})
			a.log.Info("note moved on disk", "space", space.Slug, "noteId", row.ID, "from", row.Path, "to", note.Path)
		}
		if row != nil && !rewrite && !row.Deleted && row.Path == note.Path && row.ParentID == folderAt[dirOf(note.Path)] {
			if s, ok := state[note.Path]; ok && s.NoteID == note.ID && s.Size == w.sn.Size && s.ModTime.Equal(w.sn.ModTime) {
				continue
			}
		}
		if rewrite {
			if err := a.writeNote(space.Slug, note); err != nil {
				a.log.Warn("rewrite frontmatter failed", "space", space.Slug, "path", note.Path, "err", err)
			}
		}
		if err := a.indexNoteSnap(ctx, space, note, snap); err != nil {
			a.log.Warn("index note failed", "space", space.Slug, "path", note.Path, "err", err)
			continue
		}
		st.indexed++
		st.touched = true
		if row == nil {
			a.publishItem("note.created", core.KindNote, space.ID, note.ID, "", note.Path)
		} else {
			a.publishItem("note.updated", core.KindNote, space.ID, note.ID, "", note.Path)
		}
	}
	st.noteIDs = seen
	return nil
}

// dropVanished removes the rows of live items that are no longer on disk.
// With grace set an item is kept until it has been missing for renameWindow,
// and a rescan is scheduled for when that ends.
func (a *App) dropVanished(ctx context.Context, st *scanState, trashed map[string]bool, grace bool) error {
	space := st.space
	rows, err := a.db.TreeItems(ctx, space.ID)
	if err != nil {
		return fmt.Errorf("tree items: %w", err)
	}
	now := a.now()
	missing := map[string]bool{}
	wait := time.Duration(0)
	keep := func(id string) bool {
		if trashed[id] {
			return true
		}
		missing[id] = true
		if !grace {
			return false
		}
		since := a.missingSince(id, now)
		if left := renameWindow - now.Sub(since); left > 0 {
			if wait == 0 || left < wait {
				wait = left
			}
			return true
		}
		return false
	}
	for _, n := range rows.Notes {
		if disk, ok := st.notes[n.Path]; ok && disk.ID == n.ID {
			continue
		}
		if st.noteIDs[n.ID] || keep(n.ID) {
			continue
		}
		if err := a.db.DeleteNoteRow(ctx, n.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			a.log.Warn("drop note row failed", "noteId", n.ID, "err", err)
			continue
		}
		a.dropNoteRelations(ctx, []*core.Note{n})
		st.removed++
		st.touched = true
		a.log.Info("note removed on disk", "space", space.Slug, "path", n.Path)
		a.publishItem("note.deleted", core.KindNote, space.ID, n.ID, "", n.Path)
	}
	for _, f := range rows.Files {
		if _, ok := st.files[f.Path]; ok || keep(f.ID) {
			continue
		}
		if err := a.db.DeleteFile(ctx, f.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			a.log.Warn("drop file row failed", "fileId", f.ID, "err", err)
			continue
		}
		a.dropFileData(space, f.ID)
		st.removed++
		st.touched = true
		a.log.Info("file removed on disk", "space", space.Slug, "path", f.Path)
		a.publishItem("file.deleted", core.KindFile, space.ID, f.ID, "", f.Path)
	}
	for _, f := range rows.Folders {
		if st.folders[f.Path] || keep(f.ID) {
			continue
		}
		if err := a.db.DeleteFolder(ctx, f.ID); err != nil && !errors.Is(err, core.ErrNotFound) {
			a.log.Warn("drop folder row failed", "folderId", f.ID, "err", err)
			continue
		}
		st.removed++
		st.touched = true
		a.log.Info("folder removed on disk", "space", space.Slug, "path", f.Path)
		a.publishItem("folder.deleted", core.KindFolder, space.ID, f.ID, "", f.Path)
	}
	a.forgetFound(missing)
	if wait > 0 {
		a.scheduleScan(space.Slug, wait+100*time.Millisecond)
	}
	return nil
}

// missingSince records when an item was first seen missing and returns that
// time.
func (a *App) missingSince(id string, now time.Time) time.Time {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	if t, ok := a.missing[id]; ok {
		return t
	}
	a.missing[id] = now
	return now
}

// forgetFound drops the missing records of items not in stillMissing.
func (a *App) forgetFound(stillMissing map[string]bool) {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	for id := range a.missing {
		if !stillMissing[id] {
			delete(a.missing, id)
		}
	}
}

// syncFolderNotes points every live folder of a space at its folder note.
func (a *App) syncFolderNotes(ctx context.Context, spaceID string) {
	rows, err := a.db.TreeItems(ctx, spaceID)
	if err != nil {
		return
	}
	notes := map[string]string{}
	for _, n := range rows.Notes {
		if isFolderNotePath(n.Path) {
			notes[dirOf(n.Path)] = n.ID
		}
	}
	for _, f := range rows.Folders {
		if want := notes[f.Path]; f.IndexNoteID != want {
			f.IndexNoteID = want
			f.UpdatedAt = a.now()
			if err := a.db.UpdateFolder(ctx, f); err != nil {
				a.log.Warn("update folder note failed", "folderId", f.ID, "err", err)
			}
		}
	}
}

// reconcileTrashLocked makes the trashed rows of a space agree with its trash
// directory. A trash entry without a row gets one. A trashed row whose entry
// is gone, and which did not go to the trash with a folder, is removed. It
// returns the ids of every item in the trash.
func (a *App) reconcileTrashLocked(ctx context.Context, space *core.Space) map[string]bool {
	out := map[string]bool{}
	entries, err := a.files.ScanTrashEntries(space.Slug)
	if err != nil {
		a.log.Debug("scan trash failed", "space", space.Slug, "err", err)
		return out
	}
	for _, e := range entries {
		out[e.ID] = true
		at := e.DeletedAt
		if at.IsZero() {
			at = a.now()
		}
		it, err := a.loadItem(ctx, e.ID)
		if err == nil {
			if !itemDeleted(it) {
				switch it.Kind {
				case core.KindFolder:
					err = a.db.TrashFolder(ctx, e.ID, at, e.DeletedBy)
				case core.KindFile:
					err = a.db.TrashFile(ctx, e.ID, at, e.DeletedBy)
				default:
					err = a.db.SetNoteDeleted(ctx, e.ID, true, &at, e.DeletedBy)
				}
				if err != nil {
					a.log.Warn("mark trashed item failed", "itemId", e.ID, "err", err)
				}
			}
			continue
		}
		if !errors.Is(err, core.ErrNotFound) || e.TrashedFrom == "" {
			continue
		}
		switch e.Kind {
		case core.KindNote:
			if e.Note == nil {
				continue
			}
			n := e.Note
			n.ID, n.Path, n.SpaceID = e.ID, e.TrashedFrom, space.ID
			n.Deleted, n.DeletedAt, n.DeletedBy = true, &at, e.DeletedBy
			delete(n.Extra, "trashed_from")
			err = a.indexNote(ctx, space, n)
		case core.KindFile:
			err = a.db.CreateFile(ctx, &core.File{
				ID: e.ID, SpaceID: space.ID, Name: baseOf(e.TrashedFrom), Path: e.TrashedFrom, Size: e.Size,
				ModTime: e.ModTime, CreatedAt: at, UpdatedAt: at, Deleted: true, DeletedAt: &at, DeletedBy: e.DeletedBy,
			})
		case core.KindFolder:
			err = a.db.CreateFolder(ctx, &core.Folder{
				ID: e.ID, SpaceID: space.ID, Name: baseOf(e.TrashedFrom), Path: e.TrashedFrom,
				CreatedAt: at, UpdatedAt: at, Deleted: true, DeletedAt: &at, DeletedBy: e.DeletedBy,
			})
		}
		if err != nil {
			a.log.Warn("rebuild trashed item failed", "itemId", e.ID, "err", err)
		}
	}
	folders, notes, files, err := a.trashedRows(ctx, space.ID)
	if err != nil {
		return out
	}
	for _, f := range folders {
		if out[f.ID] {
			continue
		}
		if trashedWithFolder(folders, f.ID, f.Path, f.DeletedAt) {
			out[f.ID] = true
			continue
		}
		if err := a.db.DeleteFolder(ctx, f.ID); err == nil {
			a.log.Info("trashed folder gone from disk", "space", space.Slug, "folderId", f.ID)
		}
	}
	for _, n := range notes {
		if out[n.ID] {
			continue
		}
		if trashedWithFolder(folders, n.ID, n.Path, n.DeletedAt) {
			out[n.ID] = true
			continue
		}
		if err := a.db.DeleteNoteRow(ctx, n.ID); err == nil {
			a.dropNoteRelations(ctx, []*core.Note{n})
		}
	}
	for _, f := range files {
		if out[f.ID] {
			continue
		}
		if trashedWithFolder(folders, f.ID, f.Path, f.DeletedAt) {
			out[f.ID] = true
			continue
		}
		if err := a.db.DeleteFile(ctx, f.ID); err == nil {
			a.dropFileData(space, f.ID)
		}
	}
	return out
}
