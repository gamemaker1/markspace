package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/extract"
	"github.com/gamemaker1/markspace/internal/ports"
)

// streamed is content written to a temporary file.
type streamed struct {
	tmp  string
	size int64
	sha  string
	head []byte
}

// streamToTemp writes r into a new temporary file in dir, hashing as it goes,
// and fsyncs it. Past maxBytes, when maxBytes is positive, it removes the file
// and returns core.ErrTooLarge. A missing dir returns core.ErrNotFound.
func streamToTemp(dir string, r io.Reader, maxBytes int64) (*streamed, error) {
	tmp := tempPath(dir)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("folder of %q: %w", dir, core.ErrNotFound)
		}
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	src := r
	if maxBytes > 0 {
		src = io.LimitReader(r, maxBytes+1)
	}
	h := sha256.New()
	head := make([]byte, 0, 512)
	n, err := copyWithSniff(io.MultiWriter(f, h), src, &head)
	if err == nil && maxBytes > 0 && n > maxBytes {
		err = fmt.Errorf("content is over %d bytes: %w", maxBytes, core.ErrTooLarge)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		if errors.Is(err, core.ErrTooLarge) {
			return nil, err
		}
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	return &streamed{tmp: tmp, size: n, sha: hex.EncodeToString(h.Sum(nil)), head: head}, nil
}

// describe fills a SavedFile for streamed content stored under name.
func describe(st *streamed, name string) ports.SavedFile {
	saved := ports.SavedFile{Size: st.size, MIME: DetectMediaType(name, st.head), SHA256: st.sha}
	saved.Width, saved.Height = imageSize(st.tmp, st.head)
	return saved
}

// imageSize reads the dimensions of a PNG, JPEG, GIF or WebP file from its
// header. It returns zeros for anything else.
func imageSize(full string, head []byte) (int, int) {
	if extract.ImageType(head) == "" {
		return 0, 0
	}
	f, err := os.Open(full)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	_, cfg, err := extract.ImageConfig(head, f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// SaveFile streams r into a temporary file in the folder of rel and renames
// it to rel. The folder must exist. A name taken ignoring case returns
// core.ErrExists, before and after the upload. Past maxBytes it returns
// core.ErrTooLarge and leaves nothing behind.
func (s *Store) SaveFile(spaceSlug, rel string, r io.Reader, maxBytes int64) (ports.SavedFile, error) {
	full, err := s.resolveEntry(spaceSlug, rel)
	if err != nil {
		return ports.SavedFile{}, err
	}
	parent := parentRel(rel)
	dir := filepath.Dir(full)
	if err := requireFolder(dir, parent); err != nil {
		return ports.SavedFile{}, err
	}
	if existing, taken := s.CaseCollision(spaceSlug, parent, path.Base(rel), ""); taken {
		return ports.SavedFile{}, fmt.Errorf("file %q: name taken by %q: %w", rel, existing, core.ErrExists)
	}
	st, err := streamToTemp(dir, r, maxBytes)
	if err != nil {
		return ports.SavedFile{}, err
	}
	saved := describe(st, path.Base(rel))
	s.treeMu.Lock()
	defer s.treeMu.Unlock()
	if existing, taken := s.CaseCollision(spaceSlug, parent, path.Base(rel), ""); taken {
		_ = os.Remove(st.tmp)
		return ports.SavedFile{}, fmt.Errorf("file %q: name taken by %q: %w", rel, existing, core.ErrExists)
	}
	if err := renameNoReplace(st.tmp, full); err != nil {
		_ = os.Remove(st.tmp)
		if errors.Is(err, fs.ErrExist) {
			return ports.SavedFile{}, fmt.Errorf("file %q: %w", rel, core.ErrExists)
		}
		return ports.SavedFile{}, fmt.Errorf("save file %q: %w", rel, err)
	}
	syncDir(dir)
	return saved, nil
}

// ReplaceFile streams r into a temporary file, keeps the current content of
// rel as a file version and renames the new content over rel. The old content
// is hard-linked into the history before the rename, or copied when linking
// fails, so rel holds a complete file at every moment.
func (s *Store) ReplaceFile(spaceSlug, rel string, r io.Reader, maxBytes int64, key ports.FileHistoryKey) (ports.SavedFile, *core.FileVersion, error) {
	full, err := s.resolveEntry(spaceSlug, rel)
	if err != nil {
		return ports.SavedFile{}, nil, err
	}
	dir, err := s.fileHistoryPath(key.SpaceID, key.FileID)
	if err != nil {
		return ports.SavedFile{}, nil, err
	}
	info, err := lstatEntry(full, rel)
	if err != nil {
		return ports.SavedFile{}, nil, err
	}
	if !info.Mode().IsRegular() {
		return ports.SavedFile{}, nil, fmt.Errorf("replace %q: not a file: %w", rel, core.ErrInvalid)
	}
	st, err := streamToTemp(filepath.Dir(full), r, maxBytes)
	if err != nil {
		return ports.SavedFile{}, nil, err
	}
	saved := describe(st, path.Base(rel))
	name := key.Name
	if name == "" {
		name = path.Base(rel)
	}

	s.treeMu.Lock()
	info, err = lstatEntry(full, rel)
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("replace %q: not a file: %w", rel, core.ErrInvalid)
	}
	var slot *historySlot
	if err == nil {
		slot, err = s.linkIntoHistory(dir, full, name)
	}
	if err == nil {
		if renameErr := os.Rename(st.tmp, full); renameErr != nil {
			slot.discard()
			err = fmt.Errorf("replace %q: %w", rel, renameErr)
		}
	}
	s.treeMu.Unlock()
	if err != nil {
		_ = os.Remove(st.tmp)
		return ports.SavedFile{}, nil, err
	}
	syncDir(filepath.Dir(full))

	version := &core.FileVersion{
		ID:        slot.id,
		FileID:    key.FileID,
		SpaceID:   key.SpaceID,
		Name:      name,
		MIME:      key.MIME,
		Size:      info.Size(),
		SHA256:    key.SHA256,
		AuthorID:  key.AuthorID,
		CreatedAt: slot.created,
	}
	if version.MIME == "" {
		version.MIME = mediaTypeOfFile(slot.content, name)
	}
	if version.SHA256 == "" {
		if sum, err := hashPath(slot.content); err == nil {
			version.SHA256 = sum
		} else {
			s.logger.Warn("could not hash file version", "fileId", key.FileID, "err", err)
		}
	}
	if err := writeVersionMeta(slot.meta, version); err != nil {
		s.logger.Warn("could not write file version metadata", "fileId", key.FileID, "version", slot.id, "err", err)
	}
	return saved, version, nil
}

// mediaTypeOfFile detects a media type from a name and the leading bytes.
func mediaTypeOfFile(full, name string) string {
	head := make([]byte, 512)
	f, err := os.Open(full)
	if err != nil {
		return DetectMediaType(name, nil)
	}
	defer f.Close()
	n, _ := io.ReadFull(f, head)
	return DetectMediaType(name, head[:n])
}

// OpenFile opens a regular file for reading and returns its info. A folder or
// symlink returns core.ErrInvalid. The caller closes the file.
func (s *Store) OpenFile(spaceSlug, rel string) (io.ReadSeekCloser, fs.FileInfo, error) {
	full, err := s.resolveEntry(spaceSlug, rel)
	if err != nil {
		return nil, nil, err
	}
	info, err := lstatEntry(full, rel)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("open %q: not a file: %w", rel, core.ErrInvalid)
	}
	f, err := os.Open(full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("file %q: %w", rel, core.ErrNotFound)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open %q: %w", rel, err)
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, nil, fmt.Errorf("file %q changed while opening: %w", rel, core.ErrConflict)
	}
	return f, opened, nil
}

// HashFile returns the hex SHA-256 of a file.
func (s *Store) HashFile(spaceSlug, rel string) (string, error) {
	f, _, err := s.OpenFile(spaceSlug, rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return hashReader(f)
}

// hashPath returns the hex SHA-256 of the file at full.
func hashPath(full string) (string, error) {
	f, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return hashReader(f)
}

// hashReader returns the hex SHA-256 of everything r yields.
func hashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
