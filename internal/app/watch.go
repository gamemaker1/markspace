package app

import (
	"context"
	"errors"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/links"
	"github.com/gamemaker1/markspace/internal/ports"
)

// watch consumes file events and keeps the index in step with changes made
// outside the application. The store debounces, so each event is acted on
// directly.
func (a *App) watch(ctx context.Context, ch <-chan ports.FileEvent) {
	a.log.Info("watching space files")
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			a.handleFileEvent(ctx, ev)
		}
	}
}

// handleFileEvent reacts to one outside change. A write of a known note, or
// of a new note in a known folder, is indexed at once. Everything else, such
// as a removal, a folder or a file, schedules a rescan of the space, which
// matches renames and keeps a vanished item for renameWindow.
func (a *App) handleFileEvent(ctx context.Context, ev ports.FileEvent) {
	if ev.SpaceSlug == "" || ev.RelPath == "" {
		return
	}
	if a.selfWrote(ev.SpaceSlug, ev.RelPath) {
		return
	}
	if ev.Kind == core.KindNote && ev.Op == "write" && !ev.IsDir && !a.scanPending(ev.SpaceSlug) {
		if a.indexChangedNote(ctx, ev.SpaceSlug, ev.RelPath) {
			return
		}
	}
	a.scheduleScan(ev.SpaceSlug, scanDebounce)
}

// indexChangedNote indexes a note written outside the application. It
// reports false when the change needs a rescan instead, as for a note whose
// folder the index does not know yet or a copy of a note that still exists.
func (a *App) indexChangedNote(ctx context.Context, slug, rel string) bool {
	space, err := a.db.GetSpaceBySlug(ctx, slug)
	if err != nil || space == nil {
		return false
	}
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	note, err := a.files.Read(slug, rel)
	if err != nil || note == nil || note.ID == "" {
		return false
	}
	note.Path = rel
	dir := dirOf(rel)
	if dir != "" && a.folderIDAt(ctx, space.ID, dir) == "" {
		return false
	}
	row, err := a.db.GetNote(ctx, note.ID)
	switch {
	case errors.Is(err, core.ErrNotFound):
		if held, err := a.db.GetNoteByPath(ctx, space.ID, rel); err == nil && held != nil && !held.Deleted {
			return false
		}
		if taken, err := a.db.SiblingNameTaken(ctx, space.ID, dir, baseOf(rel), note.ID); err != nil || taken {
			return false
		}
		fillNote(note, nil, a.now())
		if err := a.indexNote(ctx, space, note); err != nil {
			a.log.Warn("index new note failed", "space", slug, "path", rel, "err", err)
			return false
		}
		if isFolderNotePath(rel) {
			a.syncFolderNote(ctx, space.ID, dir)
		}
		a.log.Info("note created on disk", "space", slug, "path", rel)
		a.publishItem("note.created", core.KindNote, space.ID, note.ID, "", rel)
		a.publishItem("tree.changed", core.KindNote, space.ID, note.ID, "", rel)
		return true
	case err != nil || row == nil || row.SpaceID != space.ID || row.Deleted:
		return false
	case row.Path == rel:
		fillNote(note, row, a.now())
		if err := a.indexNote(ctx, space, note); err != nil {
			a.log.Warn("index changed note failed", "space", slug, "path", rel, "err", err)
			return false
		}
		a.log.Debug("note changed on disk", "space", slug, "path", rel)
		a.publishItem("note.updated", core.KindNote, space.ID, note.ID, "", rel)
		return true
	}
	if _, err := a.files.StatEntry(slug, row.Path); err == nil {
		return false
	}
	base, err := a.loadRewriteBase(ctx, space.ID)
	if err != nil {
		return false
	}
	fillNote(note, row, a.now())
	if err := a.indexNote(ctx, space, note); err != nil {
		a.log.Warn("index moved note failed", "space", slug, "path", rel, "err", err)
		return false
	}
	a.syncFolderNote(ctx, space.ID, dirOf(row.Path))
	a.syncFolderNote(ctx, space.ID, dir)
	a.applyRewrites(ctx, nil, space, base, []links.Change{{Kind: "move", ID: note.ID, OldPath: row.Path, NewPath: rel}})
	a.log.Info("note moved on disk", "space", slug, "noteId", note.ID, "from", row.Path, "to", rel)
	a.publishItem("item.moved", core.KindNote, space.ID, note.ID, "", rel)
	a.publishItem("tree.changed", core.KindNote, space.ID, note.ID, "", rel)
	return true
}

// fillNote gives a note read from disk the fields a file may lack.
func fillNote(note, row *core.Note, now time.Time) {
	if note.Title == "" {
		note.Title = pathSlug(note.Path)
	}
	if note.CreatedAt.IsZero() {
		note.CreatedAt = now
		if row != nil {
			note.CreatedAt = row.CreatedAt
		}
	}
	if note.UpdatedAt.IsZero() {
		note.UpdatedAt = now
	}
	if row != nil && note.CreatedBy == "" {
		note.CreatedBy = row.CreatedBy
	}
}

// scheduleScan runs a rescan of a space after delay. An earlier pending scan
// of the same space is kept.
func (a *App) scheduleScan(slug string, delay time.Duration) {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	if a.scanClosed {
		return
	}
	due := time.Now().Add(delay)
	if cur, ok := a.scanDue[slug]; ok && !cur.After(due) {
		return
	}
	if t, ok := a.scanTimers[slug]; ok {
		t.Stop()
	}
	a.scanDue[slug] = due
	a.scanTimers[slug] = time.AfterFunc(delay, func() { a.runScheduledScan(slug) })
}

// scanPending reports whether a rescan of the space is scheduled.
func (a *App) scanPending(slug string) bool {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	_, ok := a.scanTimers[slug]
	return ok
}

// runScheduledScan runs one scheduled rescan.
func (a *App) runScheduledScan(slug string) {
	a.scanMu.Lock()
	if a.scanClosed {
		a.scanMu.Unlock()
		return
	}
	delete(a.scanTimers, slug)
	delete(a.scanDue, slug)
	a.wg.Add(1)
	ctx := a.runCtx
	a.scanMu.Unlock()
	defer a.wg.Done()
	if ctx == nil {
		ctx = context.Background()
	}
	a.rescan(ctx, slug)
}

// rescan reconciles one space with the rename window.
func (a *App) rescan(ctx context.Context, slug string) {
	space, err := a.db.GetSpaceBySlug(ctx, slug)
	if err != nil || space == nil {
		a.log.Debug("rescan of unknown space", "space", slug, "err", err)
		return
	}
	if _, _, err := a.reconcileSpace(ctx, space, true); err != nil {
		a.log.Warn("rescan failed", "space", slug, "err", err)
	}
	a.kickQueue()
}

// flushScans runs every scheduled rescan now. Tests use it.
func (a *App) flushScans(ctx context.Context) {
	a.scanMu.Lock()
	slugs := make([]string, 0, len(a.scanTimers))
	for slug, t := range a.scanTimers {
		t.Stop()
		slugs = append(slugs, slug)
	}
	a.scanTimers = map[string]*time.Timer{}
	a.scanDue = map[string]time.Time{}
	a.scanMu.Unlock()
	for _, slug := range slugs {
		a.rescan(ctx, slug)
	}
}

// stopScans cancels scheduled rescans and refuses new ones.
func (a *App) stopScans() {
	a.scanMu.Lock()
	defer a.scanMu.Unlock()
	a.scanClosed = true
	for slug, t := range a.scanTimers {
		t.Stop()
		delete(a.scanTimers, slug)
	}
}
