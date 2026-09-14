package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// GetSettings returns the instance settings, merging the configuration file
// defaults with the stored row. Any caller may read them, because the login
// and signup pages need the instance name and the signup flag.
func (a *App) GetSettings(ctx context.Context, actor *core.Actor) (*core.Settings, error) {
	s, err := a.settings(ctx)
	if err != nil {
		return nil, err
	}
	if actor.IsAdmin() {
		return s, nil
	}
	// The login and signup pages need the name and whether signup is open.
	public := &core.Settings{InstanceName: s.InstanceName, AllowSignup: s.AllowSignup}
	if !actor.IsUser() {
		return public, nil
	}
	// A signed-in user also needs the AI switches for the editor and the AI
	// preference page, and the default space when they can open it.
	public.AIEnabled = s.AIEnabled
	public.AIAllowUserKeys = s.AIAllowUserKeys
	public.AIAllowUserRoutes = s.AIAllowUserRoutes
	if s.DefaultSpaceID != "" {
		if g, err := a.grants(actor).space(ctx, s.DefaultSpaceID); err == nil && g.Access.AtLeast(core.AccessView) {
			public.DefaultSpaceID = s.DefaultSpaceID
		}
	}
	return public, nil
}

// UpdateSettings stores the instance settings. Admin only.
func (a *App) UpdateSettings(ctx context.Context, actor *core.Actor, in *core.Settings) (*core.Settings, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if in == nil {
		return nil, core.Invalid("settings", "settings are required")
	}
	row := *in
	row.InstanceName = strings.TrimSpace(row.InstanceName)
	if row.AIMonthlyTokenBudget < 0 {
		return nil, core.Invalid("aiMonthlyTokenBudget", "the budget must be 0 or more")
	}
	if c := row.AIMonthlyCostBudget; math.IsNaN(c) || math.IsInf(c, 0) || c < 0 {
		return nil, core.Invalid("aiMonthlyCostBudget", "the budget must be 0 or more")
	}
	if row.DefaultSpaceID != "" {
		if _, err := a.space(ctx, row.DefaultSpaceID); err != nil {
			return nil, core.Invalid("defaultSpaceId", "unknown space")
		}
	}
	row.UpdatedAt = a.now()
	if err := a.db.SaveSettings(ctx, &row); err != nil {
		return nil, fmt.Errorf("save settings: %w", err)
	}
	a.log.Info("settings updated", "by", actor.User.ID, "allowSignup", row.AllowSignup, "aiEnabled", row.AIEnabled)
	return a.settings(ctx)
}

// Stats returns the admin dashboard counts. Admin only.
func (a *App) Stats(ctx context.Context, actor *core.Actor) (*core.Stats, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	var (
		st  core.Stats
		err error
	)
	if st.Users, err = a.db.CountUsers(ctx); err != nil {
		return nil, fmt.Errorf("count users: %w", err)
	}
	if st.Spaces, err = a.db.CountSpaces(ctx); err != nil {
		return nil, fmt.Errorf("count spaces: %w", err)
	}
	if st.Notes, err = a.db.CountNotes(ctx); err != nil {
		return nil, fmt.Errorf("count notes: %w", err)
	}
	if st.Files, err = a.db.CountFiles(ctx); err != nil {
		return nil, fmt.Errorf("count files: %w", err)
	}
	if st.Comments, err = a.db.CountComments(ctx); err != nil {
		return nil, fmt.Errorf("count comments: %w", err)
	}
	if st.DiskBytes, err = a.files.DiskUsage(); err != nil {
		a.log.Warn("disk usage failed", "err", err)
		st.DiskBytes = 0
	}
	a.statMu.Lock()
	st.IndexedAt = a.indexedAt
	a.statMu.Unlock()
	return &st, nil
}

// Reindex rereads every markdown file into the index. Admin only.
func (a *App) Reindex(ctx context.Context, actor *core.Actor) (int, error) {
	if err := requireAdmin(actor); err != nil {
		return 0, err
	}
	n, err := a.reconcile(ctx)
	if err != nil {
		return n, err
	}
	a.publish("tree.changed", "", "", actor.User.ID, "")
	return n, nil
}

// ExportSpace writes a zip of a space: the live notes serialized from their
// fields, and every folder and file as it is on disk. It needs AccessView on
// the space.
func (a *App) ExportSpace(ctx context.Context, actor *core.Actor, spaceID string, w io.Writer) error {
	space, _, err := a.requireSpace(ctx, actor, spaceID, core.AccessView)
	if err != nil {
		return err
	}
	notes, err := a.db.ListNotes(ctx, space.ID, core.NoteListOptions{})
	if err != nil {
		return fmt.Errorf("list notes: %w", err)
	}
	live := make([]*core.Note, 0, len(notes))
	for _, n := range notes {
		if n.Deleted {
			continue
		}
		if err := a.loadForWrite(ctx, space, n); err != nil {
			a.log.Warn("skip note in export", "noteId", n.ID, "err", err)
			continue
		}
		live = append(live, n)
	}
	if err := a.files.ExportSpace(space.Slug, live, w); err != nil {
		return fmt.Errorf("export space: %w", err)
	}
	a.log.Info("space exported", "space", space.Slug, "notes", len(live), "by", actor.UserID())
	return nil
}

// zipMagic is the signature at the start of a zip archive.
var zipMagic = []byte("PK\x03\x04")

// ImportSpace reads a zip, or one markdown file, into the root of a space and
// indexes what it wrote. Markdown entries become notes and every other entry
// becomes a file. It needs AccessEdit on the space and returns the number of
// notes and files written.
func (a *App) ImportSpace(ctx context.Context, actor *core.Actor, spaceID string, r io.ReaderAt, size int64) (int, error) {
	space, _, err := a.requireSpace(ctx, actor, spaceID, core.AccessEdit)
	if err != nil {
		return 0, err
	}
	if size <= 0 {
		return 0, core.Invalid("file", "the upload is empty")
	}
	if size > a.cfg.MaxUploadBytes() {
		return 0, fmt.Errorf("import: %w", core.ErrTooLarge)
	}
	head := make([]byte, len(zipMagic))
	if _, err := r.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("read import: %w", err)
	}

	count := 0
	if bytes.Equal(head, zipMagic) {
		res, err := a.files.ImportArchive(space.Slug, r, size)
		if err != nil {
			return 0, fmt.Errorf("import archive: %w", err)
		}
		unlock := a.lockSpaces(space.ID)
		defer unlock()
		for _, rel := range res.Folders {
			a.markTree(space.Slug, rel)
		}
		for _, in := range res.Notes {
			a.markWrite(space.Slug, in.RelPath)
			n := a.importedNote(space, in.RelPath, in.Note)
			if n == nil || n.CreatedBy != "" {
				continue
			}
			n.CreatedBy, n.UpdatedBy, n.UpdatedVia = actor.UserID(), actor.UserID(), actor.Via
			fillNote(n, nil, a.now())
			if err := a.writeNote(space.Slug, n); err != nil {
				a.log.Warn("rewrite imported note failed", "path", n.Path, "err", err)
			}
		}
		for _, f := range res.Files {
			a.markWrite(space.Slug, f.RelPath)
		}
		if _, _, err := a.reconcileLocked(ctx, space, false); err != nil {
			return 0, fmt.Errorf("index import: %w", err)
		}
		for _, f := range res.Files {
			row, err := a.db.GetFileByPath(ctx, space.ID, f.RelPath)
			if err != nil || row == nil {
				continue
			}
			changed := false
			if row.SHA256 == "" && f.SHA256 != "" && row.Size == f.Size {
				row.SHA256, changed = f.SHA256, true
			}
			if row.CreatedBy == "" {
				row.CreatedBy, row.UpdatedBy, changed = actor.UserID(), actor.UserID(), true
			}
			if f.Width > 0 && row.Width == 0 {
				row.Width, row.Height, changed = f.Width, f.Height, true
			}
			if changed {
				if err := a.db.UpdateFile(ctx, row); err != nil {
					a.log.Debug("record imported file details failed", "path", f.RelPath, "err", err)
				}
			}
		}
		count = len(res.Notes) + len(res.Files)
	} else {
		data := make([]byte, size)
		n, err := r.ReadAt(data, 0)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("read import: %w", err)
		}
		in, err := a.files.ImportMarkdown(space.Slug, "import.md", data[:n])
		if err != nil {
			return 0, fmt.Errorf("import markdown: %w", err)
		}
		unlock := a.lockSpaces(space.ID)
		defer unlock()
		if note := a.importedNote(space, in.RelPath, in.Note); note != nil {
			fillNote(note, nil, a.now())
			if note.CreatedBy == "" {
				note.CreatedBy, note.UpdatedBy, note.UpdatedVia = actor.UserID(), actor.UserID(), actor.Via
			}
			if err := a.writeNote(space.Slug, note); err != nil {
				return 0, err
			}
			if err := a.indexNote(ctx, space, note); err != nil {
				return 0, err
			}
			count = 1
		}
	}
	a.kickQueue()
	a.log.Info("space imported", "space", space.Slug, "items", count, "by", actor.UserID())
	a.publish("tree.changed", space.ID, "", actor.UserID(), "")
	return count, nil
}

// importedNote returns the note an import wrote, reading it back from disk
// when the store did not return it.
func (a *App) importedNote(space *core.Space, relPath string, note *core.Note) *core.Note {
	if note == nil {
		disk, err := a.files.Read(space.Slug, relPath)
		if err != nil {
			a.log.Warn("read imported note failed", "path", relPath, "err", err)
			return nil
		}
		note = disk
	}
	if note.Path == "" {
		note.Path = relPath
	}
	note.SpaceID = space.ID
	return note
}
