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

// importExtensions are the zip entry extensions treated as notes.
var importExtensions = map[string]bool{".md": true, ".markdown": true}

// ExportSpace writes a zip holding every note in notes at its own path,
// serialized from the given fields, plus every other entry in the space as it
// is on disk. Empty folders get a directory entry. Ignored names, the trash
// and symlinks are left out.
func (s *Store) ExportSpace(spaceSlug string, notes []*core.Note, w io.Writer) error {
	dir, err := s.spaceDir(spaceSlug)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	written := map[string]bool{}
	for _, note := range notes {
		if note == nil {
			continue
		}
		if err := validRelPath(note.Path); err != nil {
			s.logger.Warn("skipping note with bad path in export", "space", spaceSlug, "path", note.Path, "err", err)
			continue
		}
		data, err := s.encodeNote(note)
		if err != nil {
			return fmt.Errorf("export note %q: %w", note.Path, err)
		}
		if written[note.Path] {
			continue
		}
		written[note.Path] = true
		entry, err := zw.Create(note.Path)
		if err != nil {
			return fmt.Errorf("create zip entry %q: %w", note.Path, err)
		}
		if _, err := entry.Write(data); err != nil {
			return fmt.Errorf("write zip entry %q: %w", note.Path, err)
		}
	}
	if err := s.exportEntries(zw, dir, written); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("close zip: %w", err)
	}
	return nil
}

// exportEntries adds every entry of the space that is not already in the zip.
func (s *Store) exportEntries(zw *zip.Writer, spaceDir string, written map[string]bool) error {
	return filepath.WalkDir(spaceDir, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && full != spaceDir {
				return fs.SkipDir
			}
			return nil
		}
		if full == spaceDir {
			return nil
		}
		if IgnoredName(d.Name()) || d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(spaceDir, full)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.IsDir() {
			entries, err := os.ReadDir(full)
			if err != nil || len(entries) > 0 {
				return nil
			}
			hdr := &zip.FileHeader{Name: rel + "/", Modified: info.ModTime()}
			hdr.SetMode(fs.ModeDir | dirMode)
			if _, err := zw.CreateHeader(hdr); err != nil {
				return fmt.Errorf("create zip entry %q: %w", rel, err)
			}
			return nil
		}
		if !info.Mode().IsRegular() || written[rel] {
			return nil
		}
		src, err := os.Open(full)
		if err != nil {
			s.logger.Warn("skipping unreadable file in export", "path", full, "err", err)
			return nil
		}
		defer src.Close()
		hdr := &zip.FileHeader{Name: rel, Modified: info.ModTime(), Method: zipMethod(d.Name())}
		hdr.SetMode(fileMode)
		entry, err := zw.CreateHeader(hdr)
		if err != nil {
			return fmt.Errorf("create zip entry %q: %w", rel, err)
		}
		if _, err := io.Copy(entry, src); err != nil {
			return fmt.Errorf("copy file %q: %w", rel, err)
		}
		return nil
	})
}

// storedExtensions are formats that are already compressed, so the export
// stores them instead of deflating again.
var storedExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true, ".heic": true,
	".mp4": true, ".mov": true, ".mkv": true, ".webm": true, ".mp3": true, ".m4a": true, ".ogg": true,
	".opus": true, ".flac": true, ".zip": true, ".gz": true, ".xz": true, ".bz2": true, ".7z": true,
	".zst": true, ".rar": true, ".docx": true, ".xlsx": true, ".pptx": true, ".odt": true, ".epub": true,
}

// zipMethod picks Store for compressed formats and Deflate otherwise.
func zipMethod(name string) uint16 {
	if storedExtensions[strings.ToLower(path.Ext(name))] {
		return zip.Store
	}
	return zip.Deflate
}

// Import limits. They are variables so tests can lower them.
var (
	// maxImportEntries caps how many files one zip may hold.
	maxImportEntries = 20000
	// maxImportFileBytes caps one non-note entry after decompression.
	maxImportFileBytes int64 = 256 << 20
	// maxImportTotalBytes caps everything one import writes after
	// decompression, so a small zip that expands hugely cannot fill the disk.
	maxImportTotalBytes int64 = 2 << 30
)

// ImportMarkdown writes a single markdown file at the space root.
func (s *Store) ImportMarkdown(spaceSlug, filename string, data []byte) (*ports.ImportedNote, error) {
	if _, err := s.spaceDir(spaceSlug); err != nil {
		return nil, err
	}
	base := path.Base(strings.ReplaceAll(filename, `\`, "/"))
	base = sanitizeName(strings.TrimSuffix(base, path.Ext(base)))
	note, _, err := parseNote(data, base+mdExt, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	relPath, err := s.AllocNotePath(spaceSlug, "", note.Title, "")
	if err != nil {
		return nil, err
	}
	note.Path = relPath
	note.Slug = baseName(relPath)
	note.SpaceSlug = spaceSlug
	if err := s.Write(spaceSlug, note); err != nil {
		return nil, err
	}
	return &ports.ImportedNote{RelPath: relPath, Note: note}, nil
}

// writeImported parses imported bytes, picks a free path and writes the note.
func (s *Store) writeImported(spaceSlug, relPath string, data []byte, modTime time.Time) (*ports.ImportedNote, error) {
	if modTime.IsZero() {
		modTime = time.Now().UTC()
	}
	note, _, err := parseNote(data, relPath, modTime)
	if err != nil {
		return nil, err
	}
	target, err := s.allocSibling(spaceSlug, relPath)
	if err != nil {
		return nil, err
	}
	note.Path = target
	note.Slug = baseName(target)
	note.SpaceSlug = spaceSlug
	if note.ID == "" {
		note.ID = core.NewID()
	}
	if err := s.Write(spaceSlug, note); err != nil {
		return nil, err
	}
	return &ports.ImportedNote{RelPath: target, Note: note}, nil
}

// allocSibling returns relPath, or the same name with a "-2", "-3" suffix when
// the file is already present.
func (s *Store) allocSibling(spaceSlug, relPath string) (string, error) {
	dir, err := s.spaceDir(spaceSlug)
	if err != nil {
		return "", err
	}
	parent := path.Dir(relPath)
	if parent == "." {
		parent = ""
	}
	base := baseName(relPath)
	for attempt := 1; attempt <= maxAllocAttempts; attempt++ {
		name := base
		if attempt > 1 {
			name = fmt.Sprintf("%s-%d", base, attempt)
		}
		candidate := name + mdExt
		if parent != "" {
			candidate = parent + "/" + candidate
		}
		if err := validRelPath(candidate); err != nil {
			return "", err
		}
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(candidate))); errors.Is(err, fs.ErrNotExist) {
			if _, taken := s.CaseCollision(spaceSlug, parent, path.Base(candidate), ""); !taken {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("no free path near %q: %w", relPath, core.ErrExists)
}

// ignoredEntry reports whether a zip entry is macOS metadata.
func ignoredEntry(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if seg == "__MACOSX" || seg == "__MACOSX/" || seg == ".DS_Store" || strings.HasPrefix(seg, "._") {
			return true
		}
	}
	return false
}

// readZipEntry reads one entry with a byte ceiling.
func readZipEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("zip entry %q is larger than %d bytes: %w", f.Name, limit, core.ErrTooLarge)
	}
	return data, nil
}
