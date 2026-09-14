package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

const (
	// keepFileVersions is how many previous versions a file keeps.
	keepFileVersions = 20
	// keepFileVersionBytes caps the stored versions of one space.
	keepFileVersionBytes = 5 << 30
	// maxNameBytes is the longest name the store accepts.
	maxNameBytes = 255
)

// thumbWidths are the thumbnail widths OpenThumbnail accepts.
var thumbWidths = map[int]bool{128: true, 256: true, 512: true, 1024: true}

// cleanUploadName reduces a client file name to its base name.
func cleanUploadName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = path.Base(name)
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	return strings.TrimSpace(name)
}

// splitExt splits a name into stem and extension. A folder, and a name whose
// only dot leads, has no extension.
func splitExt(name string, isDir bool) (string, string) {
	if isDir {
		return name, ""
	}
	ext := path.Ext(name)
	if ext == name || ext == "." {
		return name, ""
	}
	return strings.TrimSuffix(name, ext), ext
}

// fitStem cuts a stem so that stem+suffix fits the name byte limit.
func fitStem(stem, suffix string) string {
	room := maxNameBytes - len(suffix)
	if room < 1 {
		room = 1
	}
	if len(stem) <= room {
		return stem + suffix
	}
	cut := stem[:room]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut) + suffix
}

// freeName returns name, or "<stem> (N).<ext>" with the first free N from 2.
func (a *App) freeName(ctx context.Context, space *core.Space, dir, name string, isDir bool) (string, error) {
	stem, ext := splitExt(name, isDir)
	cand := name
	for n := 2; n < 10000; n++ {
		taken, err := a.nameTaken(ctx, space, dir, cand, "", "")
		if err != nil {
			return "", err
		}
		if !taken {
			return cand, nil
		}
		cand = fitStem(stem, fmt.Sprintf(" (%d)%s", n, ext))
	}
	return "", fmt.Errorf("no free name for %q: %w", name, core.ErrConflict)
}

// copyName returns "<stem> copy.<ext>", then "<stem> copy N.<ext>", free in
// a folder.
func (a *App) copyName(ctx context.Context, space *core.Space, dir, name string, isDir bool) (string, error) {
	stem, ext := splitExt(name, isDir)
	cand := fitStem(stem, " copy"+ext)
	for n := 2; n < 10000; n++ {
		taken, err := a.nameTaken(ctx, space, dir, cand, "", "")
		if err != nil {
			return "", err
		}
		if !taken {
			return cand, nil
		}
		cand = fitStem(stem, fmt.Sprintf(" copy %d%s", n, ext))
	}
	return "", fmt.Errorf("no free copy name for %q: %w", name, core.ErrConflict)
}

// GetFile returns a file the actor may view.
func (a *App) GetFile(ctx context.Context, actor *core.Actor, id string) (*core.File, *core.Grant, error) {
	it, grant, err := a.requireItemAny(ctx, actor, id, core.AccessView)
	if err != nil {
		return nil, nil, err
	}
	if it.File == nil {
		return nil, nil, fmt.Errorf("item %s is not a file: %w", id, core.ErrNotFound)
	}
	space, err := a.space(ctx, it.File.SpaceID)
	if err != nil {
		return nil, nil, err
	}
	return decorateFile(space, it.File), grant, nil
}

// ListFiles lists the live files of a space the actor may view.
func (a *App) ListFiles(ctx context.Context, actor *core.Actor, spaceID string, q core.FileQuery) ([]*core.File, error) {
	space, grant, _, err := a.spaceView(ctx, actor, spaceID)
	if err != nil {
		return nil, err
	}
	switch q.Sort {
	case "", "name", "size", "updated":
	default:
		return nil, core.Invalid("sort", "sort must be name, size or updated")
	}
	if q.Limit <= 0 || q.Limit > 1000 {
		q.Limit = 200
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	list, err := a.db.ListFiles(ctx, space.ID, ports.FileListOptions{Type: q.Type, Sort: q.Sort, Limit: q.Limit, Offset: q.Offset})
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	full := grant.Access.AtLeast(core.AccessView)
	cache := a.grants(actor)
	out := make([]*core.File, 0, len(list))
	for _, f := range list {
		if !full {
			g, err := cache.item(ctx, fileRef(f))
			if err != nil {
				return nil, err
			}
			if !g.Access.AtLeast(core.AccessView) {
				continue
			}
		}
		out = append(out, decorateFile(space, f))
	}
	return out, nil
}

// UploadFile streams an upload into a folder and records it. The content is
// written before the space lock is taken, so a large upload does not hold up
// other writes in the space.
func (a *App) UploadFile(ctx context.Context, actor *core.Actor, in core.FileUpload, r io.Reader) (*core.File, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	name := cleanUploadName(in.Name)
	if name == "" {
		return nil, core.Invalid("name", "a file name is required")
	}
	if err := a.checkName(core.KindFile, name); err != nil {
		return nil, err
	}
	switch in.OnConflict {
	case core.OnConflictFail, core.OnConflictRename, core.OnConflictReplace:
	default:
		return nil, core.Invalid("onConflict", "onConflict must be rename or replace")
	}

	var (
		space   *core.Space
		parent  *core.Folder
		pasteTo string
		err     error
	)
	if in.PasteFor != "" {
		it, _, err := a.requireItem(ctx, actor, in.PasteFor, core.AccessView)
		if err != nil {
			return nil, err
		}
		if it.Note == nil {
			return nil, core.Invalid("pasteFor", "pasteFor must name a note")
		}
		if space, err = a.space(ctx, it.Note.SpaceID); err != nil {
			return nil, err
		}
		pasteTo = dirOf(it.Note.Path)
		if space.PasteFolder != "" {
			pasteTo = joinRel(pasteTo, space.PasteFolder)
		}
	} else if space, parent, err = a.resolveParent(ctx, actor, in.SpaceID, in.ParentID, core.AccessEdit); err != nil {
		return nil, err
	}

	id := core.NewID()
	if in.ID != "" {
		got, err := a.replayOf(ctx, actor, in.ID, core.KindFile, space.ID)
		if err != nil {
			return nil, err
		}
		if got != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(r, a.cfg.MaxUploadBytes()))
			return decorateFile(space, got.File), nil
		}
		id = in.ID
	}
	if in.PasteFor != "" {
		unlock := a.lockSpaces(space.ID)
		parent, err = a.ensureFolderPath(ctx, actor, space, pasteTo)
		unlock()
		if err != nil {
			return nil, err
		}
		if _, err := a.requireContainer(ctx, actor, space, folderID(parent), core.AccessEdit); err != nil {
			return nil, err
		}
	}

	dir := folderPath(parent)
	taken, err := a.nameTaken(ctx, space, dir, name, "", "")
	if err != nil {
		return nil, err
	}
	if taken {
		switch in.OnConflict {
		case core.OnConflictRename:
			if name, err = a.freeName(ctx, space, dir, name, false); err != nil {
				return nil, err
			}
		case core.OnConflictReplace:
			existing, err := a.db.GetFileByPath(ctx, space.ID, joinRel(dir, name))
			if err != nil || existing == nil || existing.Deleted {
				return nil, fmt.Errorf("name %q is taken by a folder or note: %w", name, core.ErrExists)
			}
			if _, _, err := a.requireItem(ctx, actor, existing.ID, core.AccessEdit); err != nil {
				return nil, err
			}
			return a.replaceContent(ctx, actor, space, existing, name, r, false, "")
		default:
			return nil, fmt.Errorf("name %q in %q: %w", name, dir, core.ErrExists)
		}
	}

	rel := joinRel(dir, name)
	saved, err := a.files.SaveFile(space.Slug, rel, r, a.cfg.MaxUploadBytes())
	if err != nil {
		return nil, fmt.Errorf("save file: %w", err)
	}
	a.markWrite(space.Slug, rel)
	// The content is on disk. A client that disconnects from here on must not
	// leave a file without its index row.
	ctx = context.WithoutCancel(ctx)

	unlock := a.lockSpaces(space.ID)
	defer unlock()
	now := a.now()
	f := &core.File{
		ID: id, SpaceID: space.ID, ParentID: folderID(parent), Name: name, Path: rel,
		MIME: saved.MIME, Size: saved.Size, SHA256: saved.SHA256, Width: saved.Width, Height: saved.Height,
		Position: a.nextPosition(ctx, space.ID, folderID(parent)), ModTime: now,
		CreatedAt: now, UpdatedAt: now, CreatedBy: actor.UserID(), UpdatedBy: actor.UserID(),
	}
	if info, err := a.files.StatEntry(space.Slug, rel); err == nil {
		f.ModTime = info.ModTime().UTC()
	}
	err = a.db.CreateFile(ctx, f)
	if errors.Is(err, core.ErrExists) && a.dropScannedRow(ctx, space, rel) {
		err = a.db.CreateFile(ctx, f)
	}
	if err != nil {
		if rerr := a.files.RemoveEntry(space.Slug, rel); rerr != nil {
			a.log.Warn("remove orphan upload failed", "space", space.Slug, "path", rel, "err", rerr)
		}
		return nil, fmt.Errorf("record file: %w", err)
	}
	a.kickQueue()
	a.log.Info("file uploaded", "fileId", f.ID, "space", space.Slug, "path", rel, "size", f.Size, "by", actor.UserID())
	a.publishItem("file.created", core.KindFile, space.ID, f.ID, actor.UserID(), rel)
	a.publishItem("tree.changed", core.KindFile, space.ID, f.ID, actor.UserID(), rel)
	return decorateFile(space, f), nil
}

// dropScannedRow removes a row that a scan recorded for a file the app was
// still writing, so the upload can record its own row. It reports whether it
// removed one.
func (a *App) dropScannedRow(ctx context.Context, space *core.Space, rel string) bool {
	row, err := a.db.GetFileByPath(ctx, space.ID, rel)
	if err != nil || row == nil || row.CreatedBy != "" {
		return false
	}
	return a.db.DeleteFile(ctx, row.ID) == nil
}

// RenameFile renames a file in its folder and rewrites links.
func (a *App) RenameFile(ctx context.Context, actor *core.Actor, id, name string) (*core.File, error) {
	it, _, err := a.requireItem(ctx, actor, id, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	if it.File == nil {
		return nil, fmt.Errorf("item %s is not a file: %w", id, core.ErrNotFound)
	}
	out, err := a.renameItem(ctx, actor, it, name)
	if err != nil {
		return nil, err
	}
	return out.File, nil
}

// ReplaceFile writes new content over a file after checking the base hash.
func (a *App) ReplaceFile(ctx context.Context, actor *core.Actor, id string, in core.FileReplace, r io.Reader) (*core.File, error) {
	it, _, err := a.requireItem(ctx, actor, id, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	if it.File == nil {
		return nil, fmt.Errorf("item %s is not a file: %w", id, core.ErrNotFound)
	}
	if strings.TrimSpace(in.BaseSHA256) == "" {
		return nil, core.Invalid("baseSha256", "the hash of the content being replaced is required")
	}
	space, err := a.space(ctx, it.File.SpaceID)
	if err != nil {
		return nil, err
	}
	return a.replaceContent(ctx, actor, space, it.File, in.Name, r, true, in.BaseSHA256)
}

// currentHash returns the stored hash of a file, computing and storing it
// when it is missing.
func (a *App) currentHash(ctx context.Context, space *core.Space, f *core.File) (string, error) {
	if f.SHA256 != "" {
		return f.SHA256, nil
	}
	sha, err := a.files.HashFile(space.Slug, f.Path)
	if err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}
	if info, err := a.files.StatEntry(space.Slug, f.Path); err == nil {
		if err := a.db.SetFileHash(ctx, f.ID, sha, info.Size(), info.ModTime().UTC()); err != nil {
			a.log.Debug("store hash failed", "fileId", f.ID, "err", err)
		}
	}
	f.SHA256 = sha
	return sha, nil
}

// replaceContent streams new content over a file. With check set, base must
// equal the current hash. An upload name with another extension renames the
// file to that extension.
func (a *App) replaceContent(ctx context.Context, actor *core.Actor, space *core.Space, f *core.File, uploadName string, r io.Reader, check bool, base string) (*core.File, error) {
	cur, err := a.currentHash(ctx, space, f)
	if err != nil {
		return nil, err
	}
	if check && !strings.EqualFold(strings.TrimSpace(base), cur) {
		// A client replays a replace whose response it never received. When
		// the file already holds the uploaded content, the file comes back
		// unchanged.
		if sameContent(r, cur, a.cfg.MaxUploadBytes()) {
			return decorateFile(space, f), nil
		}
		return nil, fmt.Errorf("file %s changed, current hash %s: %w", f.ID, cur, core.ErrConflict)
	}
	newName := f.Name
	if up := cleanUploadName(uploadName); up != "" {
		stem, oldExt := splitExt(f.Name, false)
		if _, ext := splitExt(up, false); !strings.EqualFold(ext, oldExt) {
			newName = fitStem(stem, ext)
			if err := a.checkName(core.KindFile, newName); err != nil {
				return nil, err
			}
		}
	}
	saved, _, err := a.files.ReplaceFile(space.Slug, f.Path, r, a.cfg.MaxUploadBytes(), ports.FileHistoryKey{
		SpaceID: space.ID, FileID: f.ID, Name: f.Name, AuthorID: f.UpdatedBy, SHA256: cur, MIME: f.MIME,
	})
	if err != nil {
		return nil, fmt.Errorf("replace file: %w", err)
	}
	a.markWrite(space.Slug, f.Path)
	// The content is on disk. A client that disconnects from here on must not
	// leave the index with the old hash.
	ctx = context.WithoutCancel(ctx)

	unlock := a.lockSpaces(space.ID)
	defer unlock()
	fresh, err := a.db.GetFile(ctx, f.ID)
	if err != nil {
		return nil, fmt.Errorf("reload file: %w", err)
	}
	if err := a.recordContent(ctx, actor, space, fresh, saved); err != nil {
		return nil, err
	}
	if newName != fresh.Name {
		it := &core.Item{Kind: core.KindFile, File: fresh}
		if err := a.renameLocked(ctx, actor, space, it, newName); err != nil {
			a.log.Warn("rename to the new extension failed", "fileId", fresh.ID, "name", newName, "err", err)
		}
	}
	a.log.Info("file replaced", "fileId", fresh.ID, "space", space.Slug, "size", fresh.Size, "by", actor.UserID())
	a.publishItem("file.updated", core.KindFile, space.ID, fresh.ID, actor.UserID(), fresh.Path)
	return decorateFile(space, fresh), nil
}

// sameContent reports whether r holds content with the SHA-256 hash sum. It
// reads at most limit bytes and counts longer content as different.
func sameContent(r io.Reader, sum string, limit int64) bool {
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(r, limit+1))
	if err != nil || n > limit {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), sum)
}

// recordContent stores new content fields of a file after a replace or a
// version restore, prunes versions and drops stale thumbnails.
func (a *App) recordContent(ctx context.Context, actor *core.Actor, space *core.Space, f *core.File, saved ports.SavedFile) error {
	now := a.now()
	f.Size, f.SHA256, f.Width, f.Height = saved.Size, saved.SHA256, saved.Width, saved.Height
	if saved.MIME != "" {
		f.MIME = saved.MIME
	}
	f.ModTime = now
	if info, err := a.files.StatEntry(space.Slug, f.Path); err == nil {
		f.ModTime = info.ModTime().UTC()
	}
	f.UpdatedAt, f.UpdatedBy = now, actor.UserID()
	pruned, err := a.files.PruneFileVersions(space.ID, f.ID, keepFileVersions, keepFileVersionBytes)
	if err != nil {
		a.log.Warn("prune file versions failed", "fileId", f.ID, "err", err)
	}
	if versions, err := a.files.ListFileVersions(space.ID, f.ID); err == nil {
		f.Versions = len(versions)
	}
	if err := a.files.DeleteThumbnails(f.ID); err != nil {
		a.log.Debug("delete thumbnails failed", "fileId", f.ID, "err", err)
	}
	if err := a.db.UpdateFile(ctx, f); err != nil {
		return fmt.Errorf("record content: %w", err)
	}
	others := map[string]bool{}
	for _, v := range pruned {
		if v.FileID != f.ID {
			others[v.FileID] = true
		}
	}
	for id := range others {
		row, err := a.db.GetFile(ctx, id)
		if err != nil || row == nil {
			continue
		}
		if versions, err := a.files.ListFileVersions(space.ID, id); err == nil && row.Versions != len(versions) {
			row.Versions = len(versions)
			if err := a.db.UpdateFile(ctx, row); err != nil {
				a.log.Debug("update version count failed", "fileId", id, "err", err)
			}
		}
	}
	a.kickQueue()
	return nil
}

// ListFileVersions returns the stored versions of a file, newest first.
func (a *App) ListFileVersions(ctx context.Context, actor *core.Actor, id string) ([]*core.FileVersion, error) {
	it, _, err := a.requireItemAny(ctx, actor, id, core.AccessView)
	if err != nil {
		return nil, err
	}
	if it.File == nil {
		return nil, fmt.Errorf("item %s is not a file: %w", id, core.ErrNotFound)
	}
	out, err := a.files.ListFileVersions(it.File.SpaceID, id)
	if err != nil {
		return nil, fmt.Errorf("list file versions: %w", err)
	}
	if out == nil {
		out = []*core.FileVersion{}
	}
	return out, nil
}

// RestoreFileVersion makes a stored version the current content and keeps the
// replaced content as a new version.
func (a *App) RestoreFileVersion(ctx context.Context, actor *core.Actor, id, versionID string) (*core.File, error) {
	it, _, err := a.requireItem(ctx, actor, id, core.AccessEdit)
	if err != nil {
		return nil, err
	}
	if it.File == nil {
		return nil, fmt.Errorf("item %s is not a file: %w", id, core.ErrNotFound)
	}
	space, err := a.space(ctx, it.File.SpaceID)
	if err != nil {
		return nil, err
	}
	unlock := a.lockSpaces(space.ID)
	defer unlock()
	f, err := a.db.GetFile(ctx, id)
	if err != nil {
		return nil, err
	}
	cur, err := a.currentHash(ctx, space, f)
	if err != nil {
		return nil, err
	}
	saved, _, err := a.files.RestoreFileVersion(space.Slug, f.Path, versionID, ports.FileHistoryKey{
		SpaceID: space.ID, FileID: f.ID, Name: f.Name, AuthorID: f.UpdatedBy, SHA256: cur, MIME: f.MIME,
	})
	if err != nil {
		return nil, fmt.Errorf("restore file version: %w", err)
	}
	a.markWrite(space.Slug, f.Path)
	ctx = context.WithoutCancel(ctx)
	if err := a.recordContent(ctx, actor, space, f, saved); err != nil {
		return nil, err
	}
	a.log.Info("file version restored", "fileId", f.ID, "versionId", versionID, "by", actor.UserID())
	a.publishItem("file.updated", core.KindFile, space.ID, f.ID, actor.UserID(), f.Path)
	return decorateFile(space, f), nil
}

// OpenFile opens the current content of a file, or one version.
func (a *App) OpenFile(ctx context.Context, actor *core.Actor, id, versionID string) (*core.File, *core.FileVersion, io.ReadSeekCloser, error) {
	it, _, err := a.requireItemAny(ctx, actor, id, core.AccessView)
	if err != nil {
		return nil, nil, nil, err
	}
	f := it.File
	if f == nil {
		return nil, nil, nil, fmt.Errorf("item %s is not a file: %w", id, core.ErrNotFound)
	}
	space, err := a.space(ctx, f.SpaceID)
	if err != nil {
		return nil, nil, nil, err
	}
	decorateFile(space, f)
	if versionID != "" {
		rc, v, err := a.files.OpenFileVersion(space.ID, f.ID, versionID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("open file version: %w", err)
		}
		if v == nil || (v.FileID != "" && v.FileID != f.ID) {
			rc.Close()
			return nil, nil, nil, fmt.Errorf("version %s: %w", versionID, core.ErrNotFound)
		}
		return f, v, rc, nil
	}
	if f.Deleted {
		return nil, nil, nil, fmt.Errorf("file %s is in the trash: %w", id, core.ErrConflict)
	}
	rc, info, err := a.files.OpenFile(space.Slug, f.Path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open file: %w", err)
	}
	f.ModTime, f.Size = info.ModTime().UTC(), info.Size()
	return f, nil, rc, nil
}

// OpenThumbnail opens a cached thumbnail of an image, making it on first
// request.
func (a *App) OpenThumbnail(ctx context.Context, actor *core.Actor, id string, width int) (*core.File, io.ReadSeekCloser, string, error) {
	if !thumbWidths[width] {
		return nil, nil, "", core.Invalid("w", "w must be 128, 256, 512 or 1024")
	}
	it, _, err := a.requireItemAny(ctx, actor, id, core.AccessView)
	if err != nil {
		return nil, nil, "", err
	}
	f := it.File
	if f == nil {
		return nil, nil, "", fmt.Errorf("item %s is not a file: %w", id, core.ErrNotFound)
	}
	if f.Deleted {
		return nil, nil, "", fmt.Errorf("file %s is in the trash: %w", id, core.ErrConflict)
	}
	if !thumbable(f.MIME) {
		return nil, nil, "", fmt.Errorf("no thumbnail for %s: %w", f.MIME, core.ErrUnsupported)
	}
	space, err := a.space(ctx, f.SpaceID)
	if err != nil {
		return nil, nil, "", err
	}
	sha, err := a.currentHash(ctx, space, f)
	if err != nil {
		return nil, nil, "", err
	}
	p, mt, err := a.files.Thumbnail(space.Slug, f.Path, f.ID, sha, width)
	if err != nil {
		return nil, nil, "", fmt.Errorf("thumbnail: %w", err)
	}
	fh, err := os.Open(p)
	if err != nil {
		return nil, nil, "", fmt.Errorf("open thumbnail: %w", err)
	}
	return decorateFile(space, f), fh, mt, nil
}
