package app

import (
	"context"
	"fmt"

	"github.com/gamemaker1/markspace/internal/core"
)

// ListVersions returns the snapshots of a note, newest first, without content.
func (a *App) ListVersions(ctx context.Context, actor *core.Actor, noteID string) ([]*core.NoteVersion, error) {
	note, _, err := a.requireNoteAny(ctx, actor, noteID, core.AccessView)
	if err != nil {
		return nil, err
	}
	out, err := a.files.ListVersions(note.SpaceID, note.ID)
	if err != nil {
		return nil, fmt.Errorf("list versions: %w", err)
	}
	return out, nil
}

// GetVersion returns one snapshot with content.
func (a *App) GetVersion(ctx context.Context, actor *core.Actor, noteID, versionID string) (*core.NoteVersion, error) {
	note, _, err := a.requireNoteAny(ctx, actor, noteID, core.AccessView)
	if err != nil {
		return nil, err
	}
	return a.readVersion(note, versionID)
}

// readVersion loads a snapshot and checks it belongs to the note.
func (a *App) readVersion(note *core.Note, versionID string) (*core.NoteVersion, error) {
	v, err := a.files.ReadVersion(note.SpaceID, note.ID, versionID)
	if err != nil {
		return nil, fmt.Errorf("read version %s: %w", versionID, err)
	}
	if v == nil || (v.NoteID != "" && v.NoteID != note.ID) {
		return nil, fmt.Errorf("version %s: %w", versionID, core.ErrNotFound)
	}
	return v, nil
}

// DiffVersion returns an HTML line diff from the version to the current body.
func (a *App) DiffVersion(ctx context.Context, actor *core.Actor, noteID, versionID string) (string, error) {
	note, _, err := a.requireNoteAny(ctx, actor, noteID, core.AccessView)
	if err != nil {
		return "", err
	}
	v, err := a.readVersion(note, versionID)
	if err != nil {
		return "", err
	}
	space, err := a.space(ctx, note.SpaceID)
	if err != nil {
		return "", err
	}
	if err := a.noteBody(ctx, space, note); err != nil {
		return "", err
	}
	return a.rend.DiffHTML(v.Content, note.Content), nil
}

// RestoreVersion replaces the note body with a snapshot, after snapshotting
// the current body.
func (a *App) RestoreVersion(ctx context.Context, actor *core.Actor, noteID, versionID string) (*core.Note, error) {
	note, _, err := a.requireNote(ctx, actor, noteID, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	v, err := a.readVersion(note, versionID)
	if err != nil {
		return nil, err
	}
	space, err := a.space(ctx, note.SpaceID)
	if err != nil {
		return nil, err
	}
	if err := a.loadForWrite(ctx, space, note); err != nil {
		return nil, err
	}
	// The file write, the index update and the follow-on work finish even
	// when the client disconnects.
	ctx = context.WithoutCancel(ctx)
	if note.Content != v.Content {
		if _, err := a.files.SaveVersion(space.ID, note, "before restore"); err != nil {
			return nil, fmt.Errorf("snapshot current body: %w", err)
		}
	}
	oldBody := note.Content
	note.Content = v.Content
	note.UpdatedAt = a.now()
	note.UpdatedBy, note.UpdatedVia = actor.UserID(), actor.Via
	if err := a.writeNote(space.Slug, note); err != nil {
		return nil, err
	}
	if err := a.indexNote(ctx, space, note); err != nil {
		return nil, err
	}
	a.remapCommentAnchors(ctx, note.ID, oldBody, note.Content)
	if err := a.files.PruneVersions(space.ID, note.ID, keepVersions); err != nil {
		a.log.Warn("prune versions failed", "noteId", note.ID, "err", err)
	}
	note.SpaceSlug = space.Slug
	a.log.Info("version restored", "noteId", note.ID, "versionId", versionID, "by", actor.UserID())
	a.publish("note.updated", space.ID, note.ID, actor.UserID(), note.Path)
	return note, nil
}
