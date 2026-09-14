package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

const (
	// syncDefaultLimit is the page size when the client sends none.
	syncDefaultLimit = 200
	// syncMaxLimit caps one page of the change feed.
	syncMaxLimit = 1000
)

// SyncPull returns one page of the change feed for a signed-in user. The feed
// covers every folder, note and file in spaces the actor can view in full,
// plus items shared with the actor directly or through a share on a folder
// or folder note that includes children. Access is checked per item before
// an item enters the page.
func (a *App) SyncPull(ctx context.Context, actor *core.Actor, since int64, limit int) (*core.SyncBatch, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	if since < 0 {
		return nil, core.Invalid("since", "since must not be negative")
	}
	if limit <= 0 {
		limit = syncDefaultLimit
	}
	if limit > syncMaxLimit {
		limit = syncMaxLimit
	}

	spaces, err := a.ListSpaces(ctx, actor)
	if err != nil {
		return nil, err
	}
	cache := a.grants(actor)
	bySpace := make(map[string]*core.Space, len(spaces))
	full := map[string]bool{}
	var fullSpaces []string
	for _, sp := range spaces {
		bySpace[sp.ID] = sp
		g, err := cache.space(ctx, sp.ID)
		if err != nil {
			return nil, err
		}
		if g.Access.AtLeast(core.AccessView) {
			full[sp.ID] = true
			fullSpaces = append(fullSpaces, sp.ID)
		}
	}
	shares, err := cache.shareGrants(ctx)
	if err != nil {
		return nil, err
	}
	sharedIDs, err := a.sharedItemIDs(ctx, shares, full)
	if err != nil {
		return nil, err
	}

	// Reading the ceiling first keeps a write that lands during this call
	// out of the page, so the returned cursor never skips it.
	upTo, err := a.db.MaxNoteRev(ctx)
	if err != nil {
		return nil, fmt.Errorf("sync: max revision: %w", err)
	}
	rows, err := a.db.ItemsChangedSince(ctx, fullSpaces, sharedIDs, since, upTo, limit+1)
	if err != nil {
		return nil, fmt.Errorf("sync: changed items: %w", err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}

	batch := &core.SyncBatch{
		Rev:        upTo,
		More:       more,
		ServerTime: a.now(),
		User:       actor.User,
		Spaces:     spaces,
		Notes:      make([]*core.SyncNote, 0, len(rows)),
		Folders:    []*core.Folder{},
		Files:      []*core.File{},
		AccessKey:  accessKey(actor, spaces, shares),
	}
	if more {
		batch.Rev = treeItemRevision(rows[len(rows)-1])
	}
	if batch.Spaces == nil {
		batch.Spaces = []*core.Space{}
	}

	spaceOf := func(id string) *core.Space {
		sp := bySpace[id]
		if sp == nil {
			if loaded, err := a.space(ctx, id); err == nil {
				sp = loaded
				bySpace[id] = sp
			}
		}
		return sp
	}
	for _, row := range rows {
		it := &core.Item{Kind: row.Kind, Folder: row.Folder, Note: row.Note, File: row.File}
		g, err := cache.item(ctx, refOf(it))
		if err != nil {
			return nil, err
		}
		if !g.Access.AtLeast(core.AccessView) {
			continue
		}
		sp := spaceOf(it.SpaceID())
		if sp == nil {
			a.log.Warn("sync: space of item unreadable, skipping item", "itemId", it.ID(), "spaceId", it.SpaceID())
			continue
		}
		switch {
		case row.Folder != nil:
			row.Folder.SpaceSlug = sp.Slug
			batch.Folders = append(batch.Folders, row.Folder)
		case row.File != nil:
			batch.Files = append(batch.Files, decorateFile(sp, row.File))
		case row.Note != nil:
			n := row.Note
			n.SpaceSlug = sp.Slug
			if !n.Deleted {
				if err := a.loadBody(ctx, sp, n); err != nil {
					// The next reconcile repairs the index. The note returns to
					// the feed then with a new revision.
					a.log.Warn("sync: note body unreadable, skipping note", "noteId", n.ID, "path", n.Path, "err", err)
					continue
				}
			}
			// Extra holds frontmatter keys, which a client has no use for.
			n.Extra = nil
			batch.Notes = append(batch.Notes, &core.SyncNote{Note: n, Access: g.Access})
		}
	}

	if !more {
		live, err := a.db.ItemIDsInSpaces(ctx, fullSpaces)
		if err != nil {
			return nil, fmt.Errorf("sync: live ids: %w", err)
		}
		batch.LiveIDs = mergeIDs(live, sharedIDs)
		liveSet := make(map[string]bool, len(batch.LiveIDs))
		for _, id := range batch.LiveIDs {
			liveSet[id] = true
		}
		starred, err := a.db.ListStarred(ctx, actor.User.ID)
		if err != nil {
			return nil, fmt.Errorf("sync: starred: %w", err)
		}
		batch.StarredIDs = []string{}
		for _, n := range starred {
			if liveSet[n.ID] {
				batch.StarredIDs = append(batch.StarredIDs, n.ID)
			}
		}
	}
	return batch, nil
}

// treeItemRevision returns the revision of a feed row.
func treeItemRevision(it *core.TreeItem) int64 {
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

// sharedItemIDs lists the items the actor reaches only through shares: each
// directly shared item and, for a share that includes children, every item
// below it, trashed items included. Items in fully visible spaces are left
// out, because the space scope of the feed already covers them.
func (a *App) sharedItemIDs(ctx context.Context, shares []ports.ShareGrant, full map[string]bool) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	type pathRow struct{ id, path string }
	spaceRows := map[string][]pathRow{}
	for _, g := range shares {
		if full[g.SpaceID] {
			continue
		}
		add(g.NoteID)
		if !g.IncludeChildren {
			continue
		}
		rows, ok := spaceRows[g.SpaceID]
		if !ok {
			folders, notes, files, err := a.allRows(ctx, g.SpaceID)
			if err != nil {
				return nil, err
			}
			for _, f := range folders {
				rows = append(rows, pathRow{f.ID, f.Path})
			}
			for _, n := range notes {
				rows = append(rows, pathRow{n.ID, n.Path})
			}
			for _, f := range files {
				rows = append(rows, pathRow{f.ID, f.Path})
			}
			spaceRows[g.SpaceID] = rows
		}
		for _, r := range rows {
			if coversPath(g.Kind, g.NotePath, r.path) {
				add(r.id)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// allRows lists every folder, note and file of a space, trashed ones included.
func (a *App) allRows(ctx context.Context, spaceID string) ([]*core.Folder, []*core.Note, []*core.File, error) {
	folders, err := a.db.ListFolders(ctx, spaceID, true)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sync: folders of shared space: %w", err)
	}
	notes, err := a.db.ListNotes(ctx, spaceID, core.NoteListOptions{IncludeTrash: true})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sync: notes of shared space: %w", err)
	}
	files, err := a.db.ListFiles(ctx, spaceID, ports.FileListOptions{IncludeDeleted: true, Sort: "path"})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sync: files of shared space: %w", err)
	}
	return folders, notes, files, nil
}

// mergeIDs returns the sorted union of two id lists. It never returns nil, so
// the JSON field is an array on the last page.
func mergeIDs(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, id := range list {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}

// accessKey hashes what decides the actor's item visibility: the admin flag,
// the role in each visible space and every share grant on an item of any
// kind. Any change produces a
// different key.
func accessKey(actor *core.Actor, spaces []*core.Space, shares []ports.ShareGrant) string {
	lines := make([]string, 0, len(spaces)+len(shares)+1)
	lines = append(lines, fmt.Sprintf("admin %t", actor.IsAdmin()))
	for _, s := range spaces {
		lines = append(lines, "space "+s.ID+" "+string(s.MyRole))
	}
	for _, g := range shares {
		lines = append(lines, fmt.Sprintf("share %s %s %d %t", g.Kind, g.NoteID, g.Access, g.IncludeChildren))
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
