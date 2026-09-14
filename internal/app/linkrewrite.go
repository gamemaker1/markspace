package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/links"
	"github.com/gamemaker1/markspace/internal/ports"
)

// linkRewriteLabel labels the version saved before a link rewrite.
const linkRewriteLabel = "Links updated"

// linkForm maps a stored link form to the link library form.
func linkForm(stored string) (links.Form, bool) {
	switch stored {
	case ports.LinkWikilink:
		return links.FormWikilink, true
	case ports.LinkPathWikilink:
		return links.FormPathWikilink, true
	case ports.LinkMarkdown:
		return links.FormMarkdownLink, true
	case ports.LinkMarkdownImage:
		return links.FormMarkdownImage, true
	}
	return 0, false
}

// storedForm maps a link library form to the stored form.
func storedForm(f links.Form) string {
	switch f {
	case links.FormPathWikilink:
		return ports.LinkPathWikilink
	case links.FormMarkdownLink:
		return ports.LinkMarkdown
	case links.FormMarkdownImage:
		return ports.LinkMarkdownImage
	}
	return ports.LinkWikilink
}

// storedPathEscaper keeps a decoded markdown target intact through the
// index, which strips a fragment and angle brackets and percent-decodes.
var storedPathEscaper = strings.NewReplacer("%", "%25", "#", "%23", "<", "%3C", ">", "%3E")

// snapshotOf builds a link resolution snapshot from the live rows of a space.
func snapshotOf(rows *ports.TreeRows, extra ...links.Entry) *links.Snapshot {
	entries := make([]links.Entry, 0, len(rows.Folders)+len(rows.Notes)+len(rows.Files)+len(extra))
	replaced := map[string]links.Entry{}
	for _, e := range extra {
		replaced[e.ID] = e
	}
	add := func(e links.Entry) {
		if r, ok := replaced[e.ID]; ok {
			e = r
			delete(replaced, e.ID)
		}
		entries = append(entries, e)
	}
	for _, f := range rows.Folders {
		add(links.Entry{ID: f.ID, Kind: links.KindFolder, Path: f.Path})
	}
	for _, n := range rows.Notes {
		add(links.Entry{ID: n.ID, Kind: links.KindNote, Path: n.Path, Title: n.Title})
	}
	for _, f := range rows.Files {
		add(links.Entry{ID: f.ID, Kind: links.KindFile, Path: f.Path})
	}
	for _, e := range extra {
		if _, left := replaced[e.ID]; left {
			entries = append(entries, e)
		}
	}
	return links.NewSnapshot(entries)
}

// loadSnapshot builds the snapshot of a space from the index.
func (a *App) loadSnapshot(ctx context.Context, spaceID string, extra ...links.Entry) (*links.Snapshot, *ports.TreeRows, error) {
	rows, err := a.db.TreeItems(ctx, spaceID)
	if err != nil {
		return nil, nil, fmt.Errorf("tree items: %w", err)
	}
	return snapshotOf(rows, extra...), rows, nil
}

// noteLinkRefs parses a body and resolves each link against the snapshot.
func noteLinkRefs(snap *links.Snapshot, notePath, body string) []ports.NoteLink {
	parsed := links.Parse(body)
	out := make([]ports.NoteLink, 0, len(parsed))
	for _, l := range parsed {
		if l.External || l.Target == "" {
			continue
		}
		ref := ports.NoteLink{Form: storedForm(l.Form), Embed: l.Embed, Heading: l.Heading, Target: l.Target}
		if !l.Form.IsWikilink() {
			ref.Target = storedPathEscaper.Replace(l.Target)
		}
		if e := snap.Resolve(notePath, l); e != nil {
			ref.TargetID = e.ID
		}
		out = append(out, ref)
	}
	return out
}

// rewriteBase is the state a link rewrite needs from before a change: the
// snapshot and the outbound link keys of every live note.
type rewriteBase struct {
	before *links.Snapshot
	keys   map[string][]string
}

// loadRewriteBase reads the snapshot and the stored outbound links of a space.
func (a *App) loadRewriteBase(ctx context.Context, spaceID string, extra ...links.Entry) (*rewriteBase, error) {
	snap, _, err := a.loadSnapshot(ctx, spaceID, extra...)
	if err != nil {
		return nil, err
	}
	stored, err := a.db.SpaceLinks(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("space links: %w", err)
	}
	keys := map[string][]string{}
	seen := map[string]bool{}
	for _, s := range stored {
		form, ok := linkForm(s.Form)
		if !ok {
			continue
		}
		l := links.Link{Form: form, Target: s.Target}
		if !form.IsWikilink() {
			l.Target = "/" + s.Target
		}
		for _, k := range links.LinkKeys(s.NotePath, l) {
			if !seen[k+"\x00"+s.NoteID] {
				seen[k+"\x00"+s.NoteID] = true
				keys[k] = append(keys[k], s.NoteID)
			}
		}
	}
	return &rewriteBase{before: snap, keys: keys}, nil
}

// applyRewrites rewrites the links of every note the changes may affect. A
// nil actor is the system, as for a rename found on disk, and may edit every
// note. A note the actor cannot edit is skipped. Failures are logged, because
// the change itself already happened.
func (a *App) applyRewrites(ctx context.Context, actor *core.Actor, space *core.Space, base *rewriteBase, changes []links.Change) {
	if base == nil || len(changes) == 0 {
		return
	}
	ids := links.Affected(base.before, changes, func(key string) []string { return base.keys[key] })
	if len(ids) == 0 {
		return
	}
	rw := links.NewRewriter(base.before, changes)
	var cache *grantCache
	if actor != nil {
		cache = a.grants(actor)
	}
	for _, id := range ids {
		old := base.before.ByID(id)
		if old == nil || old.Kind != links.KindNote {
			continue
		}
		a.rewriteNote(ctx, actor, cache, space, rw, old.Path, id)
	}
}

// rewriteNote applies the rewrite to one note. It saves a version first and
// writes only when the note's updatedAt still matches what it read, retrying
// once from a fresh read.
func (a *App) rewriteNote(ctx context.Context, actor *core.Actor, cache *grantCache, space *core.Space, rw *links.Rewriter, oldPath, id string) {
	for attempt := 0; attempt < 2; attempt++ {
		row, err := a.db.GetNote(ctx, id)
		if err != nil || row == nil || row.Deleted || row.SpaceID != space.ID {
			return
		}
		if cache != nil {
			g, err := cache.note(ctx, row)
			if err != nil || !g.Access.AtLeast(core.AccessEdit) {
				a.log.Info("link rewrite skipped, no edit access", "noteId", id, "actor", actor.UserID())
				return
			}
		}
		if err := a.loadForWrite(ctx, space, row); err != nil {
			a.log.Warn("link rewrite read failed", "noteId", id, "err", err)
			return
		}
		edits := rw.Rewrite(oldPath, row.Path, row.Content)
		if len(edits) == 0 {
			return
		}
		oldBody := row.Content
		newBody := links.Apply(oldBody, edits)
		if cur, err := a.db.GetNote(ctx, id); err != nil || cur == nil || !cur.UpdatedAt.Equal(row.UpdatedAt) {
			continue
		}
		if _, err := a.files.SaveVersion(space.ID, row, linkRewriteLabel); err != nil {
			a.log.Warn("save version before link rewrite failed", "noteId", id, "err", err)
		} else if err := a.files.PruneVersions(space.ID, id, keepVersions); err != nil {
			a.log.Debug("prune versions failed", "noteId", id, "err", err)
		}
		row.Content = newBody
		row.UpdatedAt = a.now()
		if actor != nil {
			row.UpdatedBy, row.UpdatedVia = actor.UserID(), actor.Via
		}
		if err := a.writeNote(space.Slug, row); err != nil {
			a.log.Warn("link rewrite write failed", "noteId", id, "err", err)
			return
		}
		if err := a.indexNote(ctx, space, row); err != nil {
			a.log.Warn("link rewrite index failed", "noteId", id, "err", err)
		}
		a.remapCommentAnchors(ctx, id, oldBody, newBody)
		a.log.Info("links updated", "noteId", id, "space", space.Slug, "edits", len(edits))
		a.publishItem("note.updated", core.KindNote, space.ID, id, actor.UserID(), row.Path)
		return
	}
	a.log.Warn("link rewrite skipped, the note changed twice while rewriting", "noteId", id)
}
