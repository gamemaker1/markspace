package app

import (
	"context"
	"fmt"
	"net/url"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/links"
	"github.com/gamemaker1/markspace/internal/ports"
)

// RenderMarkdown renders a markdown fragment. With a space id, links resolve
// inside that space from its root.
func (a *App) RenderMarkdown(ctx context.Context, actor *core.Actor, spaceID, md string) (*core.Rendered, error) {
	if anonymous(actor) {
		return nil, fmt.Errorf("render: %w", core.ErrUnauthorized)
	}
	var space *core.Space
	if spaceID != "" {
		sp, _, _, err := a.spaceView(ctx, actor, spaceID)
		if err != nil {
			return nil, err
		}
		space = sp
	}
	return a.render(ctx, actor, space, md)
}

// RenderNote renders a note body and resolves its links in the note's
// space, relative to the note's folder. It needs AccessView on the stored
// note.
func (a *App) RenderNote(ctx context.Context, actor *core.Actor, note *core.Note) (*core.Rendered, error) {
	if note == nil {
		return nil, core.Invalid("note", "note is required")
	}
	stored, _, err := a.requireNoteAny(ctx, actor, note.ID, core.AccessView)
	if err != nil {
		return nil, err
	}
	space, err := a.space(ctx, stored.SpaceID)
	if err != nil {
		return nil, err
	}
	return a.renderAt(ctx, actor, space, stored.Path, note.Content)
}

// render calls the renderer with link resolution scoped to one space root.
func (a *App) render(ctx context.Context, actor *core.Actor, space *core.Space, md string) (*core.Rendered, error) {
	return a.renderAt(ctx, actor, space, "", md)
}

// renderAt renders markdown as if written in the note at fromPath. A link
// resolves only to an item the actor may view, so rendering never discloses
// a hidden path. A public link actor gets URLs under its link.
func (a *App) renderAt(ctx context.Context, actor *core.Actor, space *core.Space, fromPath, md string) (*core.Rendered, error) {
	var (
		snap   *links.Snapshot
		files  map[string]*core.File
		loaded bool
	)
	cache := a.grants(actor)
	resolve := func(ref ports.LinkRef) (ports.LinkTarget, bool) {
		if space == nil {
			return ports.LinkTarget{}, false
		}
		if !loaded {
			loaded = true
			s, rows, err := a.loadSnapshot(ctx, space.ID)
			if err != nil {
				a.log.Warn("render: snapshot failed", "space", space.Slug, "err", err)
				return ports.LinkTarget{}, false
			}
			snap = s
			files = make(map[string]*core.File, len(rows.Files))
			for _, f := range rows.Files {
				files[f.ID] = f
			}
		}
		if snap == nil {
			return ports.LinkTarget{}, false
		}
		form, ok := linkForm(ref.Form)
		if !ok {
			form = links.FormWikilink
		}
		e := snap.ResolveTarget(fromPath, ref.Target, form)
		if e == nil {
			return ports.LinkTarget{}, false
		}
		kind := core.ItemKind(e.Kind)
		g, err := cache.item(ctx, itemRef{kind: kind, id: e.ID, spaceID: space.ID, path: e.Path})
		if err != nil || !g.Access.AtLeast(core.AccessView) {
			return ports.LinkTarget{}, false
		}
		t := ports.LinkTarget{Kind: kind, ID: e.ID, Name: e.Name, URL: itemURL(space.Slug, kind, e.Path)}
		link := actor != nil && actor.Link != nil && actor.User == nil
		if link {
			t.URL = "/p/" + url.PathEscape(actor.Link.Token) + "/i/" + url.PathEscape(e.ID)
		}
		if f := files[e.ID]; f != nil {
			t.MIME = f.MIME
			t.FileURL = fileContentURL(space.Slug, f)
			if link {
				t.FileURL = "/p/" + url.PathEscape(actor.Link.Token) + "/raw/" + url.PathEscape(f.ID) + "/" + url.PathEscape(f.Name)
			}
		}
		return t, true
	}
	opts := ports.RenderOptions{
		Highlight:   true,
		HeadingIDs:  true,
		ResolveItem: resolve,
	}
	out, err := a.rend.Render(md, opts)
	if err != nil {
		return nil, fmt.Errorf("render markdown: %w", err)
	}
	return out, nil
}

// noteURL builds the web path of a note.
func noteURL(spaceSlug, relPath string) string {
	return itemURL(spaceSlug, core.KindNote, relPath)
}
