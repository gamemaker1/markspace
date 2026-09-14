package store

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// ImportArchive unpacks a zip into the space root. Entries named ".md" or
// ".markdown" of at most 8 MiB become notes at a free "-N" suffixed path. Any
// other entry, and a larger markdown entry, becomes a file with its own name,
// or "<stem> (N).<ext>" when that name is taken. Directory entries become
// folders, and a zip folder whose name matches an existing folder ignoring
// case merges into it. An entry with a name that fails ValidName, a symlink
// entry and macOS metadata are skipped. The zip may hold at most 20000
// entries, a file may expand to at most 256 MiB, which skips a larger one,
// and the whole import may expand to at most 2 GiB, which stops the import
// with core.ErrTooLarge. The declared sizes in the zip are not trusted, and
// an entry is never truncated.
func (s *Store) ImportArchive(spaceSlug string, r io.ReaderAt, size int64) (*ports.ImportResult, error) {
	root, err := s.spaceDir(spaceSlug)
	if err != nil {
		return nil, err
	}
	if err := requireFolder(root, ""); err != nil {
		return nil, fmt.Errorf("space %q: %w", spaceSlug, core.ErrNotFound)
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w: %v", core.ErrInvalid, err)
	}
	res := &ports.ImportResult{}
	imp := &archiveImport{s: s, space: spaceSlug, root: root, res: res, dirs: map[string]string{"": ""}}
	budget := maxImportTotalBytes
	entries := 0
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		for strings.HasPrefix(name, "./") {
			name = name[2:]
		}
		isDir := strings.HasSuffix(name, "/") || f.FileInfo().IsDir()
		name = strings.Trim(name, "/")
		if name == "" || ignoredEntry(name) {
			continue
		}
		entries++
		if entries > maxImportEntries {
			return res, fmt.Errorf("zip holds more than %d entries: %w", maxImportEntries, core.ErrTooLarge)
		}
		if f.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		if err := validEntryPath(name); err != nil {
			s.logger.Warn("skipping zip entry with unusable name", "space", spaceSlug, "entry", f.Name, "err", err)
			continue
		}
		if isDir {
			if _, err := imp.folder(name); err != nil {
				s.logger.Warn("skipping zip folder", "space", spaceSlug, "entry", f.Name, "err", err)
			}
			continue
		}
		parent, err := imp.folder(parentRel(name))
		if err != nil {
			s.logger.Warn("skipping zip entry without a folder", "space", spaceSlug, "entry", f.Name, "err", err)
			continue
		}
		base := path.Base(name)
		ext := strings.ToLower(path.Ext(base))
		if importExtensions[ext] && f.UncompressedSize64 <= maxNoteBytes {
			limit := min(int64(maxNoteBytes), budget)
			data, err := readZipEntry(f, limit)
			if err != nil {
				if errors.Is(err, core.ErrTooLarge) && limit < maxNoteBytes {
					return res, fmt.Errorf("import expands past %d bytes: %w", maxImportTotalBytes, core.ErrTooLarge)
				}
				s.logger.Warn("skipping unreadable zip entry", "space", spaceSlug, "entry", f.Name, "err", err)
				continue
			}
			budget -= int64(len(data))
			stem := strings.TrimSuffix(base, path.Ext(base))
			relPath := joinRel(parent, stem+mdExt)
			if ValidName(stem+mdExt) != nil {
				s.logger.Warn("skipping zip entry with unusable note name", "space", spaceSlug, "entry", f.Name)
				continue
			}
			imported, err := s.writeImported(spaceSlug, relPath, data, f.Modified)
			if err != nil {
				return res, err
			}
			res.Notes = append(res.Notes, *imported)
			continue
		}
		if f.UncompressedSize64 > uint64(maxImportFileBytes) {
			s.logger.Warn("skipping oversized zip entry", "space", spaceSlug, "entry", f.Name, "size", f.UncompressedSize64)
			continue
		}
		if f.UncompressedSize64 > uint64(max(budget, 0)) {
			return res, fmt.Errorf("import expands past %d bytes: %w", maxImportTotalBytes, core.ErrTooLarge)
		}
		limit := min(maxImportFileBytes, budget)
		file, n, err := imp.file(parent, base, f, limit)
		budget -= n
		if err != nil {
			if errors.Is(err, core.ErrTooLarge) {
				if limit < maxImportFileBytes {
					return res, fmt.Errorf("import expands past %d bytes: %w", maxImportTotalBytes, core.ErrTooLarge)
				}
				s.logger.Warn("skipping oversized zip entry", "space", spaceSlug, "entry", f.Name)
				continue
			}
			s.logger.Warn("skipping zip entry", "space", spaceSlug, "entry", f.Name, "err", err)
			continue
		}
		res.Files = append(res.Files, *file)
	}
	return res, nil
}

// archiveImport holds the state of one ImportArchive call.
type archiveImport struct {
	s     *Store
	space string
	root  string
	res   *ports.ImportResult
	// dirs maps a folder path inside the zip to the folder it landed in.
	dirs map[string]string
}

// folder returns the space folder for a zip folder path, creating folders as
// needed. A name that matches an existing folder ignoring case reuses it, and
// a name taken by a file gets a free "(N)" name.
func (a *archiveImport) folder(zipRel string) (string, error) {
	if actual, ok := a.dirs[zipRel]; ok {
		return actual, nil
	}
	parent, err := a.folder(parentRel(zipRel))
	if err != nil {
		return "", err
	}
	name := path.Base(zipRel)
	dirFull, err := a.s.resolveFolder(a.space, parent)
	if err != nil {
		return "", err
	}
	a.s.treeMu.Lock()
	defer a.s.treeMu.Unlock()
	actual := ""
	if existing, taken := a.s.CaseCollision(a.space, parent, name, ""); taken {
		info, err := os.Lstat(filepath.Join(dirFull, existing))
		if err == nil && info.IsDir() {
			actual = joinRel(parent, existing)
		} else {
			free, err := a.s.AllocName(a.space, parent, name, "", true)
			if err != nil {
				return "", err
			}
			name = free
		}
	}
	if actual == "" {
		actual = joinRel(parent, name)
		if err := os.Mkdir(filepath.Join(dirFull, name), dirMode); err != nil {
			return "", fmt.Errorf("create folder %q: %w", actual, err)
		}
		a.res.Folders = append(a.res.Folders, actual)
	}
	a.dirs[zipRel] = actual
	return actual, nil
}

// file streams one zip entry into a free name in folder and returns it with
// the bytes it read.
func (a *archiveImport) file(folder, name string, f *zip.File, limit int64) (*ports.ImportedFile, int64, error) {
	dirFull, err := a.s.resolveFolder(a.space, folder)
	if err != nil {
		return nil, 0, err
	}
	rc, err := f.Open()
	if err != nil {
		return nil, 0, fmt.Errorf("open zip entry %q: %w", f.Name, err)
	}
	var src io.Reader = rc
	if limit <= 0 {
		// streamToTemp treats 0 as no limit, so an exhausted budget accepts
		// only an entry that really is empty.
		var one [1]byte
		if n, _ := io.ReadFull(rc, one[:]); n > 0 {
			rc.Close()
			return nil, 0, fmt.Errorf("zip entry %q: %w", f.Name, core.ErrTooLarge)
		}
		src = strings.NewReader("")
	}
	st, err := streamToTemp(dirFull, src, max(limit, 0))
	rc.Close()
	if err != nil {
		return nil, 0, err
	}
	a.s.treeMu.Lock()
	defer a.s.treeMu.Unlock()
	free, err := a.s.AllocName(a.space, folder, name, "", false)
	if err != nil {
		_ = os.Remove(st.tmp)
		return nil, st.size, err
	}
	rel := joinRel(folder, free)
	full := filepath.Join(dirFull, free)
	if err := renameNoReplace(st.tmp, full); err != nil {
		_ = os.Remove(st.tmp)
		return nil, st.size, fmt.Errorf("place %q: %w", rel, err)
	}
	if !f.Modified.IsZero() {
		_ = os.Chtimes(full, time.Now(), f.Modified)
	}
	return &ports.ImportedFile{RelPath: rel, SavedFile: describe(&streamed{tmp: full, size: st.size, sha: st.sha, head: st.head}, free)}, st.size, nil
}
