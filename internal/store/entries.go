package store

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// resolveEntry validates an entry path and joins it onto the space
// directory. It refuses a path whose existing parent components include a
// symlink, so no operation reaches outside the space through a link.
func (s *Store) resolveEntry(spaceSlug, rel string) (string, error) {
	if err := validEntryPath(rel); err != nil {
		return "", err
	}
	root, err := s.spaceDir(spaceSlug)
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	if full == root || !withinDir(root, full) {
		return "", fmt.Errorf("entry path %q escapes the space directory: %w", rel, core.ErrInvalid)
	}
	cur := root
	segments := strings.Split(rel, "/")
	for _, seg := range segments[:len(segments)-1] {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if err != nil {
			break
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("entry path %q passes through a symlink: %w", rel, core.ErrInvalid)
		}
	}
	return full, nil
}

// resolveFolder resolves a folder path, where "" is the space root.
func (s *Store) resolveFolder(spaceSlug, rel string) (string, error) {
	if rel == "" {
		return s.spaceDir(spaceSlug)
	}
	return s.resolveEntry(spaceSlug, rel)
}

// lstatEntry returns the entry info, mapping a missing entry to
// core.ErrNotFound and a symlink to core.ErrInvalid.
func lstatEntry(full, rel string) (fs.FileInfo, error) {
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errNotDir) {
		return nil, fmt.Errorf("entry %q: %w", rel, core.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("stat entry %q: %w", rel, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("entry %q is a symlink: %w", rel, core.ErrInvalid)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("entry %q is not a file or folder: %w", rel, core.ErrInvalid)
	}
	return info, nil
}

// errNotDir matches ENOTDIR, which Lstat returns when a parent is a file.
var errNotDir error = syscall.ENOTDIR

// requireFolder checks that the folder at full exists.
func requireFolder(full, rel string) error {
	info, err := os.Lstat(full)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("folder %q: %w", rel, core.ErrNotFound)
	}
	return nil
}

// entryKind classifies an entry on disk by the scan rules. A ".md" file of at
// most 8 MiB is a note, a directory is a folder and anything else is a file.
func entryKind(name string, info fs.FileInfo) core.ItemKind {
	switch {
	case info.IsDir():
		return core.KindFolder
	case strings.HasSuffix(name, mdExt) && info.Size() <= maxNoteBytes:
		return core.KindNote
	default:
		return core.KindFile
	}
}

// ScanTree walks a space and returns its folders, notes and files, each
// sorted by path. It skips ignored names, names that fail ValidName and
// symlinks, and does not hash. A note is parsed, its parent key is ignored,
// and a missing id is minted and written back. A note over 8 MiB or with
// frontmatter that does not parse is reported as a file.
func (s *Store) ScanTree(spaceSlug string) (ports.ScanResult, error) {
	var res ports.ScanResult
	root, err := s.spaceDir(spaceSlug)
	if err != nil {
		return res, err
	}
	walkErr := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				s.logger.Warn("scan walk error", "space", spaceSlug, "path", full, "err", err)
			}
			if d != nil && d.IsDir() && full != root {
				return fs.SkipDir
			}
			return nil
		}
		if full == root {
			return nil
		}
		name := d.Name()
		if IgnoredName(name) || d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, full)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if nameErr := ValidName(name); nameErr != nil {
			s.logger.Warn("scan skips entry with unusable name", "space", spaceSlug, "path", rel, "err", nameErr)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		switch {
		case info.IsDir():
			res.Folders = append(res.Folders, ports.ScannedFolder{RelPath: rel, ModTime: info.ModTime().UTC()})
			return nil
		case !info.Mode().IsRegular():
			return nil
		}
		if entryKind(name, info) == core.KindNote {
			note, readErr := s.readNoteFile(spaceSlug, rel, full, info.ModTime())
			if readErr == nil {
				note.ParentID = ""
				res.Notes = append(res.Notes, ports.ScannedNote{
					Note: note, ModTime: info.ModTime(), Size: info.Size(), ParentRel: parentRel(rel),
				})
				return nil
			}
			s.logger.Warn("unparseable note reported as a file", "space", spaceSlug, "path", rel, "err", readErr)
		}
		res.Files = append(res.Files, ports.ScannedFile{
			RelPath: rel, Size: info.Size(), ModTime: info.ModTime().UTC(), MIME: scanMediaType(full, name),
		})
		return nil
	})
	if walkErr != nil {
		return res, fmt.Errorf("scan space %q: %w", spaceSlug, walkErr)
	}
	sort.Slice(res.Folders, func(i, j int) bool { return res.Folders[i].RelPath < res.Folders[j].RelPath })
	sort.Slice(res.Notes, func(i, j int) bool { return res.Notes[i].Note.Path < res.Notes[j].Note.Path })
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].RelPath < res.Files[j].RelPath })
	return res, nil
}

// scanMediaType returns the media type from the extension, and sniffs the
// first 512 bytes only when the extension is unknown.
func scanMediaType(full, name string) string {
	if byName := DetectMediaType(name, nil); byName != defaultMediaType {
		return byName
	}
	f, err := os.Open(full)
	if err != nil {
		return defaultMediaType
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := f.Read(head)
	if n == 0 {
		return defaultMediaType
	}
	return http.DetectContentType(head[:n])
}

// StatEntry describes the entry at rel without following a symlink.
func (s *Store) StatEntry(spaceSlug, rel string) (fs.FileInfo, error) {
	full, err := s.resolveEntry(spaceSlug, rel)
	if err != nil {
		return nil, err
	}
	return lstatEntry(full, rel)
}

// MakeDir creates the folder rel. The parent folder must exist, and a name
// equal ignoring case returns core.ErrExists.
func (s *Store) MakeDir(spaceSlug, rel string) error {
	full, err := s.resolveEntry(spaceSlug, rel)
	if err != nil {
		return err
	}
	parent := parentRel(rel)
	s.treeMu.Lock()
	defer s.treeMu.Unlock()
	if err := requireFolder(filepath.Dir(full), parent); err != nil {
		return err
	}
	if existing, taken := s.CaseCollision(spaceSlug, parent, path.Base(rel), ""); taken {
		return fmt.Errorf("folder %q: name taken by %q: %w", rel, existing, core.ErrExists)
	}
	if err := os.Mkdir(full, dirMode); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("folder %q: %w", rel, core.ErrExists)
		}
		return fmt.Errorf("create folder %q: %w", rel, err)
	}
	return nil
}

// missingParents checks the folders above rel. It returns the folders that
// must be created, parents first, and core.ErrExists when a component is a
// file or would collide ignoring case with an existing name.
func (s *Store) missingParents(spaceSlug, root, rel string) ([]string, error) {
	parent := parentRel(rel)
	if parent == "" {
		return nil, nil
	}
	var missing []string
	cur := ""
	for _, seg := range strings.Split(parent, "/") {
		next := joinRel(cur, seg)
		if len(missing) > 0 {
			missing = append(missing, next)
			cur = next
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(next)))
		switch {
		case err == nil && info.IsDir():
		case err == nil:
			return nil, fmt.Errorf("folder %q: name taken by a file: %w", next, core.ErrExists)
		case errors.Is(err, fs.ErrNotExist):
			if existing, taken := s.CaseCollision(spaceSlug, cur, seg, ""); taken {
				return nil, fmt.Errorf("folder %q: name taken by %q: %w", next, existing, core.ErrExists)
			}
			missing = append(missing, next)
		default:
			return nil, fmt.Errorf("stat folder %q: %w", next, err)
		}
		cur = next
	}
	return missing, nil
}

// MoveEntry renames a note, file or folder within a space. Missing parent
// folders are created. It returns core.ErrNotFound for a missing source,
// core.ErrInvalid for a folder moved into itself, and core.ErrExists when the
// target name, or a folder it needs, is taken ignoring case. A rename that
// only changes case is allowed. No folder is removed afterwards, because an
// empty folder is an item.
func (s *Store) MoveEntry(spaceSlug, oldRel, newRel string) error {
	oldFull, err := s.resolveEntry(spaceSlug, oldRel)
	if err != nil {
		return err
	}
	newFull, err := s.resolveEntry(spaceSlug, newRel)
	if err != nil {
		return err
	}
	root, err := s.spaceDir(spaceSlug)
	if err != nil {
		return err
	}
	s.treeMu.Lock()
	defer s.treeMu.Unlock()
	info, err := lstatEntry(oldFull, oldRel)
	if err != nil {
		return err
	}
	if oldRel == newRel {
		return nil
	}
	if info.IsDir() && foldHasPrefix(newRel, oldRel+"/") {
		return fmt.Errorf("move folder %q into itself at %q: %w", oldRel, newRel, core.ErrInvalid)
	}
	missing, err := s.missingParents(spaceSlug, root, newRel)
	if err != nil {
		return err
	}
	newParent := parentRel(newRel)
	if len(missing) == 0 {
		if err := requireFolder(filepath.Dir(newFull), newParent); err != nil {
			return err
		}
		if existing, taken := s.CaseCollision(spaceSlug, newParent, path.Base(newRel), oldRel); taken {
			return fmt.Errorf("move to %q: name taken by %q: %w", newRel, existing, core.ErrExists)
		}
	}
	for _, dir := range missing {
		if err := os.Mkdir(filepath.Join(root, filepath.FromSlash(dir)), dirMode); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create folder %q: %w", dir, err)
		}
	}
	caseOnly := parentRel(oldRel) == newParent && strings.EqualFold(path.Base(oldRel), path.Base(newRel))
	if caseOnly {
		err = os.Rename(oldFull, newFull)
	} else {
		err = renameNoReplace(oldFull, newFull)
	}
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("move to %q: %w", newRel, core.ErrExists)
		}
		return fmt.Errorf("move %q to %q: %w", oldRel, newRel, err)
	}
	syncDir(filepath.Dir(newFull))
	return nil
}

// RemoveEntry permanently deletes an entry and everything below it.
func (s *Store) RemoveEntry(spaceSlug, rel string) error {
	full, err := s.resolveEntry(spaceSlug, rel)
	if err != nil {
		return err
	}
	s.treeMu.Lock()
	defer s.treeMu.Unlock()
	if _, err := os.Lstat(full); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errNotDir) {
			return fmt.Errorf("entry %q: %w", rel, core.ErrNotFound)
		}
		return fmt.Errorf("stat entry %q: %w", rel, err)
	}
	if err := os.RemoveAll(full); err != nil {
		return fmt.Errorf("remove entry %q: %w", rel, err)
	}
	return nil
}

// CopyEntry copies an entry to dstRel in dstSpace, recursively for a folder.
// The parent of dstRel must exist. Ignored names, invalid names and symlinks
// inside a folder are not copied. Every note gets the id rewriteNoteID
// returns for its old id, or core.NewID when the callback is nil or returns
// "". A note whose frontmatter does not parse is copied byte for byte as a
// file. The copy is built under a temporary dot name next to the target and
// renamed into place, so a failed copy leaves nothing a scan would see.
func (s *Store) CopyEntry(spaceSlug, srcRel, dstSpace, dstRel string, rewriteNoteID func(oldID string) string) ([]ports.CopiedEntry, error) {
	srcFull, err := s.resolveEntry(spaceSlug, srcRel)
	if err != nil {
		return nil, err
	}
	dstFull, err := s.resolveEntry(dstSpace, dstRel)
	if err != nil {
		return nil, err
	}
	info, err := lstatEntry(srcFull, srcRel)
	if err != nil {
		return nil, err
	}
	if spaceSlug == dstSpace && info.IsDir() && (strings.EqualFold(dstRel, srcRel) || foldHasPrefix(dstRel, srcRel+"/")) {
		return nil, fmt.Errorf("copy folder %q into itself at %q: %w", srcRel, dstRel, core.ErrInvalid)
	}
	dstParent := parentRel(dstRel)
	dstParentFull := filepath.Dir(dstFull)
	if err := requireFolder(dstParentFull, dstParent); err != nil {
		return nil, err
	}
	if existing, taken := s.CaseCollision(dstSpace, dstParent, path.Base(dstRel), ""); taken {
		return nil, fmt.Errorf("copy to %q: name taken by %q: %w", dstRel, existing, core.ErrExists)
	}
	tmp := tempPath(dstParentFull)
	var copied []ports.CopiedEntry
	if err := s.copyTree(srcFull, srcRel, tmp, dstRel, info, rewriteNoteID, &copied); err != nil {
		_ = os.RemoveAll(tmp)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("copy %q: %w: %v", srcRel, core.ErrNotFound, err)
		}
		return nil, err
	}
	s.treeMu.Lock()
	defer s.treeMu.Unlock()
	if existing, taken := s.CaseCollision(dstSpace, dstParent, path.Base(dstRel), ""); taken {
		_ = os.RemoveAll(tmp)
		return nil, fmt.Errorf("copy to %q: name taken by %q: %w", dstRel, existing, core.ErrExists)
	}
	if err := renameNoReplace(tmp, dstFull); err != nil {
		_ = os.RemoveAll(tmp)
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("copy to %q: %w", dstRel, core.ErrExists)
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("copy to %q: %w", dstRel, core.ErrNotFound)
		}
		return nil, fmt.Errorf("copy %q to %q: %w", srcRel, dstRel, err)
	}
	syncDir(dstParentFull)
	return copied, nil
}

// copyTree writes the entry at srcFull to dstFull and records every copied
// entry under its final path dstRel.
func (s *Store) copyTree(srcFull, srcRel, dstFull, dstRel string, info fs.FileInfo, rewrite func(string) string, out *[]ports.CopiedEntry) error {
	name := path.Base(srcRel)
	switch entryKind(name, info) {
	case core.KindFolder:
		if err := os.Mkdir(dstFull, dirMode); err != nil {
			return fmt.Errorf("create folder copy %q: %w", dstRel, err)
		}
		*out = append(*out, ports.CopiedEntry{Kind: core.KindFolder, OldRel: srcRel, NewRel: dstRel})
		children, err := os.ReadDir(srcFull)
		if err != nil {
			return fmt.Errorf("read folder %q: %w", srcRel, err)
		}
		for _, child := range children {
			childName := child.Name()
			if child.Type()&fs.ModeSymlink != 0 || ValidName(childName) != nil {
				continue
			}
			childInfo, err := child.Info()
			if err != nil {
				continue
			}
			if !childInfo.IsDir() && !childInfo.Mode().IsRegular() {
				continue
			}
			if err := s.copyTree(filepath.Join(srcFull, childName), srcRel+"/"+childName,
				filepath.Join(dstFull, childName), dstRel+"/"+childName, childInfo, rewrite, out); err != nil {
				return err
			}
		}
		return nil
	case core.KindNote:
		data, err := os.ReadFile(srcFull)
		if err != nil {
			return fmt.Errorf("read note %q: %w", srcRel, err)
		}
		if int64(len(data)) <= maxNoteBytes {
			if note, minted, perr := parseNote(data, srcRel, info.ModTime()); perr == nil {
				oldID := note.ID
				if minted {
					oldID = ""
				}
				newID := ""
				if rewrite != nil {
					newID = rewrite(oldID)
				}
				if newID == "" || validIDSegment(newID) != nil {
					newID = core.NewID()
				}
				note.ID = newID
				note.Path = dstRel
				encoded, err := s.encodeNote(note)
				if err != nil {
					return err
				}
				if err := writeNewFile(dstFull, encoded); err != nil {
					return fmt.Errorf("write note copy %q: %w", dstRel, err)
				}
				*out = append(*out, ports.CopiedEntry{Kind: core.KindNote, OldRel: srcRel, NewRel: dstRel, OldID: oldID, NewID: newID})
				return nil
			}
		}
		fallthrough
	default:
		if _, err := copyFileSynced(srcFull, dstFull); err != nil {
			return fmt.Errorf("copy file %q: %w", srcRel, err)
		}
		*out = append(*out, ports.CopiedEntry{Kind: core.KindFile, OldRel: srcRel, NewRel: dstRel})
		return nil
	}
}

// writeNewFile creates a file that must not exist, writes data and fsyncs.
func writeNewFile(full string, data []byte) error {
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(full)
	}
	return err
}
