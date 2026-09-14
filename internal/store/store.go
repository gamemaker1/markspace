package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// File and directory modes used for everything the store writes.
const (
	fileMode fs.FileMode = 0o644
	dirMode  fs.FileMode = 0o755
)

// maxNoteBytes is the largest note file a scan will read.
const maxNoteBytes = 8 << 20

// Store is a ports.Files implementation over a data directory.
type Store struct {
	dataDir        string
	spacesDir      string
	internalDir    string
	historyDir     string
	fileHistoryDir string
	thumbsDir      string
	deletedDir     string
	logger         *slog.Logger
	treeMu         sync.Mutex
	thumbs         thumbFlights
	thumbGenHook   func()
	thumbGenerated atomic.Int64
}

var _ ports.Files = (*Store)(nil)

// New opens a store rooted at dataDir and creates the directories it owns:
// spaces, internal/history and deleted.
func New(dataDir string, logger *slog.Logger) (*Store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("data dir is empty: %w", core.ErrInvalid)
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data dir %q: %w", dataDir, err)
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	s := &Store{
		dataDir:    abs,
		spacesDir:  filepath.Join(abs, "spaces"),
		historyDir: filepath.Join(abs, "internal", "history"),
		deletedDir: filepath.Join(abs, "deleted"),
		logger:     logger,
	}
	s.internalDir = filepath.Join(abs, "internal")
	s.fileHistoryDir = filepath.Join(s.internalDir, "file-history")
	s.thumbsDir = filepath.Join(s.internalDir, "thumbs")
	s.thumbs.init()
	for _, dir := range []string{s.dataDir, s.spacesDir, s.historyDir, s.deletedDir} {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return nil, fmt.Errorf("create %q: %w", dir, err)
		}
	}
	return s, nil
}

// DataDir returns the root directory of the store.
func (s *Store) DataDir() string { return s.dataDir }

// SpacesDir returns the directory holding every space directory.
func (s *Store) SpacesDir() string { return s.spacesDir }

// EnsureSpace creates the space directory if it is missing.
func (s *Store) EnsureSpace(slug string) error {
	dir, err := s.spaceDir(slug)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create space %q: %w", slug, err)
	}
	return nil
}

// RenameSpace moves a space directory. It fails if the target exists.
func (s *Store) RenameSpace(oldSlug, newSlug string) error {
	oldDir, err := s.spaceDir(oldSlug)
	if err != nil {
		return err
	}
	newDir, err := s.spaceDir(newSlug)
	if err != nil {
		return err
	}
	if oldDir == newDir {
		return nil
	}
	if _, err := os.Lstat(newDir); err == nil {
		return fmt.Errorf("space %q: %w", newSlug, core.ErrExists)
	}
	if _, err := os.Lstat(oldDir); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("space %q: %w", oldSlug, core.ErrNotFound)
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("rename space %q to %q: %w", oldSlug, newSlug, err)
	}
	return nil
}

// ArchiveSpace moves a space directory out of spaces into
// deleted/<slug>-<unix-millis>.
func (s *Store) ArchiveSpace(slug string) error {
	dir, err := s.spaceDir(slug)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("space %q: %w", slug, core.ErrNotFound)
	}
	if err := os.MkdirAll(s.deletedDir, dirMode); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}
	target := filepath.Join(s.deletedDir, fmt.Sprintf("%s-%d", slug, time.Now().UTC().UnixMilli()))
	if err := os.Rename(dir, target); err != nil {
		return fmt.Errorf("archive space %q: %w", slug, err)
	}
	s.logger.Info("archived space", "slug", slug, "target", target)
	return nil
}

// ListSpaceDirs returns the space directory names present on disk, sorted.
func (s *Store) ListSpaceDirs() ([]string, error) {
	entries, err := os.ReadDir(s.spacesDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read spaces dir: %w", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// Read loads one note. A file whose frontmatter has no id is given one and
// rewritten so the id persists.
func (s *Store) Read(spaceSlug, relPath string) (*core.Note, error) {
	full, err := s.resolve(spaceSlug, relPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("note %q: %w", relPath, core.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("stat note %q: %w", relPath, err)
	}
	return s.readNoteFile(spaceSlug, relPath, full, info.ModTime())
}

// readNoteFile parses one file on disk into a note and mints a missing id.
func (s *Store) readNoteFile(spaceSlug, relPath, full string, modTime time.Time) (*core.Note, error) {
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read note %q: %w", relPath, err)
	}
	note, minted, err := parseNote(data, relPath, modTime)
	if err != nil {
		return nil, err
	}
	note.SpaceSlug = spaceSlug
	// The containing folder is the parent, so a parent key is ignored.
	note.ParentID = ""
	if minted {
		if writeErr := s.writeFile(full, note); writeErr != nil {
			s.logger.Warn("could not persist minted note id", "space", spaceSlug, "path", relPath, "err", writeErr)
		} else {
			s.logger.Info("minted note id", "space", spaceSlug, "path", relPath, "id", note.ID)
		}
	}
	return note, nil
}

// parseNote turns file bytes into a note. It reports whether an id had to be
// minted, which means the file should be rewritten.
func parseNote(data []byte, relPath string, modTime time.Time) (*core.Note, bool, error) {
	yamlText, body, hasFM := splitFrontmatter(data)
	note := &core.Note{
		Path:    relPath,
		Slug:    baseName(relPath),
		Content: body,
	}
	if hasFM {
		fm, err := parseFrontmatter(yamlText)
		if err != nil {
			return nil, false, fmt.Errorf("note %q: %w", relPath, err)
		}
		noteFromFrontmatter(fm, note)
	}
	minted := false
	if note.ID == "" {
		note.ID = core.NewID()
		minted = true
	}
	if note.Title == "" {
		if h := leadingHeading(body); h != "" {
			note.Title = h
		} else {
			note.Title = baseName(relPath)
		}
		// A derived title is written into the frontmatter, and YAML holds
		// only valid UTF-8, so invalid bytes become U+FFFD now rather than
		// on the next read.
		note.Title = strings.ToValidUTF8(note.Title, "\ufffd")
	}
	if note.CreatedAt.IsZero() {
		note.CreatedAt = modTime.UTC()
	}
	if note.UpdatedAt.IsZero() {
		note.UpdatedAt = modTime.UTC()
	}
	return note, minted, nil
}

// Write creates or replaces the file at note.Path, creating directories as
// needed. The write is atomic within the space directory.
func (s *Store) Write(spaceSlug string, note *core.Note) error {
	if note == nil {
		return fmt.Errorf("write note: nil note: %w", core.ErrInvalid)
	}
	full, err := s.resolve(spaceSlug, note.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), dirMode); err != nil {
		return fmt.Errorf("create dir for %q: %w", note.Path, err)
	}
	return s.writeFile(full, note)
}

// writeFile serializes a note and replaces full atomically.
func (s *Store) writeFile(full string, note *core.Note) error {
	data, err := s.encodeNote(note)
	if err != nil {
		return err
	}
	return writeFileAtomic(full, data)
}

// encodeNote serializes a note for a file in a space. It never writes a
// parent key, because the containing folder is the parent.
func (s *Store) encodeNote(note *core.Note) ([]byte, error) {
	if note.ParentID != "" {
		copied := *note
		copied.ParentID = ""
		note = &copied
	}
	return serializeNote(note)
}

// writeFileAtomic writes data to a temporary file in the same directory,
// fsyncs it and renames it over the target.
func writeFileAtomic(full string, data []byte) error {
	dir := filepath.Dir(full)
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("random temp name: %w", err)
	}
	tmp := full + ".tmp-" + hex.EncodeToString(suffix[:])
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return fmt.Errorf("create temp file in %q: %w", dir, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmp, full); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename temp file onto %q: %w", full, err)
	}
	return nil
}

// Exists reports whether a note file is present.
func (s *Store) Exists(spaceSlug, relPath string) bool {
	full, err := s.resolve(spaceSlug, relPath)
	if err != nil {
		return false
	}
	info, err := os.Lstat(full)
	return err == nil && info.Mode().IsRegular()
}

// Stat returns the modification time and size of a note file.
func (s *Store) Stat(spaceSlug, relPath string) (time.Time, int64, error) {
	full, err := s.resolve(spaceSlug, relPath)
	if err != nil {
		return time.Time{}, 0, err
	}
	info, err := os.Stat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, 0, fmt.Errorf("note %q: %w", relPath, core.ErrNotFound)
	}
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("stat note %q: %w", relPath, err)
	}
	return info.ModTime().UTC(), info.Size(), nil
}

// DiskUsage returns the byte size of every file under the data directory.
func (s *Store) DiskUsage() (int64, error) {
	var total int64
	err := filepath.WalkDir(s.dataDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("disk usage: %w", err)
	}
	return total, nil
}
