package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// maxHistoryNameBytes caps the name part of a version file name, so
// "<unix-millis>-<name>.json" stays within the 255 byte name limit.
const maxHistoryNameBytes = 200

// versionMetaExt is the suffix of a version metadata sidecar.
const versionMetaExt = ".json"

// versionMeta is the sidecar of one file version.
type versionMeta struct {
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	AuthorID  string    `json:"authorId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// historySlot is a version file just linked into the history.
type historySlot struct {
	id      string
	created time.Time
	content string
	meta    string
}

// discard removes a version whose replace did not complete.
func (h *historySlot) discard() {
	_ = os.Remove(h.content)
	_ = os.Remove(h.meta)
}

// fileHistoryPath returns internal/file-history/<spaceID>/<fileID>.
func (s *Store) fileHistoryPath(spaceID, fileID string) (string, error) {
	if err := validIDSegment(spaceID); err != nil {
		return "", fmt.Errorf("space id: %w", err)
	}
	if err := validIDSegment(fileID); err != nil {
		return "", fmt.Errorf("file id: %w", err)
	}
	dir := filepath.Join(s.fileHistoryDir, spaceID, fileID)
	if !withinDir(s.fileHistoryDir, dir) || filepath.Dir(filepath.Dir(dir)) != s.fileHistoryDir {
		return "", fmt.Errorf("file history path for %q: %w", fileID, core.ErrInvalid)
	}
	return dir, nil
}

// historyFileName returns the content file name of a version.
func historyFileName(id, name string) string {
	return id + "-" + truncateUTF8(sanitizeName(name), maxHistoryNameBytes)
}

// linkIntoHistory links, or copies, the file at full into dir under the next
// free millisecond id.
func (s *Store) linkIntoHistory(dir, full, name string) (*historySlot, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create file history dir: %w", err)
	}
	used, err := versionIDsIn(dir)
	if err != nil {
		return nil, err
	}
	millis := time.Now().UTC().UnixMilli()
	for used[strconv.FormatInt(millis, 10)] {
		millis++
	}
	for {
		id := strconv.FormatInt(millis, 10)
		content := filepath.Join(dir, historyFileName(id, name))
		_, err := linkOrCopy(full, content)
		if errors.Is(err, fs.ErrExist) {
			millis++
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("keep old content: %w", err)
		}
		return &historySlot{id: id, created: time.UnixMilli(millis).UTC(), content: content, meta: content + versionMetaExt}, nil
	}
}

// writeVersionMeta writes the sidecar of a version atomically.
func writeVersionMeta(full string, v *core.FileVersion) error {
	data, err := json.Marshal(versionMeta{
		Name: v.Name, MIME: v.MIME, Size: v.Size, SHA256: v.SHA256, AuthorID: v.AuthorID, CreatedAt: v.CreatedAt,
	})
	if err != nil {
		return err
	}
	return writeFileAtomic(full, append(data, '\n'))
}

// versionEntry groups the content file and sidecar of one version on disk.
type versionEntry struct {
	id      string
	content string
	meta    string
}

// versionIDsIn returns the set of version ids present in dir.
func versionIDsIn(dir string) (map[string]bool, error) {
	entries, err := readVersionEntries(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[e.id] = true
	}
	return out, nil
}

// readVersionEntries lists the versions stored in one file history dir. The
// content file of a version is its shortest name, and the sidecar is that name
// plus ".json".
func readVersionEntries(dir string) ([]versionEntry, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read file history: %w", err)
	}
	byID := map[string][]string{}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		id, _, ok := strings.Cut(e.Name(), "-")
		if !ok || id == "" || strings.Trim(id, "0123456789") != "" {
			continue
		}
		byID[id] = append(byID[id], e.Name())
	}
	var out []versionEntry
	for id, names := range byID {
		sort.Slice(names, func(i, j int) bool { return len(names[i]) < len(names[j]) })
		content := names[0]
		ve := versionEntry{id: id, content: filepath.Join(dir, content)}
		for _, n := range names[1:] {
			if n == content+versionMetaExt {
				ve.meta = filepath.Join(dir, n)
			}
		}
		if ve.meta == "" && strings.HasSuffix(content, versionMetaExt) {
			// Only a sidecar is left, so the content is gone.
			continue
		}
		if ve.meta == "" {
			ve.meta = ve.content + versionMetaExt
		}
		out = append(out, ve)
	}
	return out, nil
}

// loadVersion builds the record of one version from its sidecar, falling back
// to the file itself when the sidecar is missing.
func loadVersion(spaceID, fileID string, ve versionEntry) (*core.FileVersion, error) {
	info, err := os.Stat(ve.content)
	if err != nil {
		return nil, err
	}
	v := &core.FileVersion{
		ID:        ve.id,
		FileID:    fileID,
		SpaceID:   spaceID,
		Size:      info.Size(),
		CreatedAt: versionTime(ve.id, info.ModTime()),
	}
	_, v.Name, _ = strings.Cut(filepath.Base(ve.content), "-")
	if data, err := os.ReadFile(ve.meta); err == nil {
		var meta versionMeta
		if json.Unmarshal(data, &meta) == nil {
			if meta.Name != "" {
				v.Name = meta.Name
			}
			v.MIME = meta.MIME
			v.SHA256 = meta.SHA256
			v.AuthorID = meta.AuthorID
		}
	}
	if v.MIME == "" {
		v.MIME = DetectMediaType(v.Name, nil)
	}
	return v, nil
}

// ListFileVersions returns the stored versions of a file, newest first.
func (s *Store) ListFileVersions(spaceID, fileID string) ([]*core.FileVersion, error) {
	dir, err := s.fileHistoryPath(spaceID, fileID)
	if err != nil {
		return nil, err
	}
	entries, err := readVersionEntries(dir)
	if err != nil {
		return nil, err
	}
	out := make([]*core.FileVersion, 0, len(entries))
	for _, ve := range entries {
		v, err := loadVersion(spaceID, fileID, ve)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return versionLess(out[j].ID, out[i].ID) })
	return out, nil
}

// findVersion returns the entry of one version id.
func (s *Store) findVersion(spaceID, fileID, versionID string) (versionEntry, error) {
	dir, err := s.fileHistoryPath(spaceID, fileID)
	if err != nil {
		return versionEntry{}, err
	}
	if versionID == "" || strings.Trim(versionID, "0123456789") != "" {
		return versionEntry{}, fmt.Errorf("version id %q: %w", versionID, core.ErrInvalid)
	}
	entries, err := readVersionEntries(dir)
	if err != nil {
		return versionEntry{}, err
	}
	for _, ve := range entries {
		if ve.id == versionID {
			return ve, nil
		}
	}
	return versionEntry{}, fmt.Errorf("version %q of file %q: %w", versionID, fileID, core.ErrNotFound)
}

// OpenFileVersion opens one stored version. The caller closes it.
func (s *Store) OpenFileVersion(spaceID, fileID, versionID string) (io.ReadSeekCloser, *core.FileVersion, error) {
	ve, err := s.findVersion(spaceID, fileID, versionID)
	if err != nil {
		return nil, nil, err
	}
	v, err := loadVersion(spaceID, fileID, ve)
	if err != nil {
		return nil, nil, fmt.Errorf("version %q: %w", versionID, core.ErrNotFound)
	}
	f, err := os.Open(ve.content)
	if err != nil {
		return nil, nil, fmt.Errorf("version %q: %w", versionID, core.ErrNotFound)
	}
	return f, v, nil
}

// RestoreFileVersion writes a stored version over the file at rel through
// ReplaceFile, so the current content becomes a new version. The restored
// version stays in the history.
func (s *Store) RestoreFileVersion(spaceSlug, rel, versionID string, key ports.FileHistoryKey) (ports.SavedFile, *core.FileVersion, error) {
	src, _, err := s.OpenFileVersion(key.SpaceID, key.FileID, versionID)
	if err != nil {
		return ports.SavedFile{}, nil, err
	}
	defer src.Close()
	return s.ReplaceFile(spaceSlug, rel, src, 0, key)
}

// removeVersion deletes the content and sidecar of one version.
func removeVersion(ve versionEntry) error {
	if err := os.Remove(ve.content); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(ve.meta); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// PruneFileVersions keeps the newest keep versions of one file. Then, when
// spaceByteBudget is positive, it deletes the oldest versions of any file in
// the space until the space history fits. keep < 0 means no count limit. It
// returns the deleted versions.
func (s *Store) PruneFileVersions(spaceID, fileID string, keep int, spaceByteBudget int64) ([]*core.FileVersion, error) {
	dir, err := s.fileHistoryPath(spaceID, fileID)
	if err != nil {
		return nil, err
	}
	var removed []*core.FileVersion
	if keep >= 0 {
		entries, err := readVersionEntries(dir)
		if err != nil {
			return nil, err
		}
		sort.Slice(entries, func(i, j int) bool { return versionLess(entries[j].id, entries[i].id) })
		if len(entries) > keep {
			for _, ve := range entries[keep:] {
				v, _ := loadVersion(spaceID, fileID, ve)
				if err := removeVersion(ve); err != nil {
					return removed, fmt.Errorf("prune version %q: %w", ve.id, err)
				}
				if v != nil {
					removed = append(removed, v)
				}
			}
		}
		removeDirIfEmpty(dir)
	}
	if spaceByteBudget <= 0 {
		return removed, nil
	}
	type spaced struct {
		fileID string
		ve     versionEntry
		v      *core.FileVersion
	}
	spaceDir := filepath.Dir(dir)
	files, err := os.ReadDir(spaceDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return removed, fmt.Errorf("read space file history: %w", err)
	}
	var all []spaced
	var total int64
	for _, fd := range files {
		if !fd.IsDir() || validIDSegment(fd.Name()) != nil {
			continue
		}
		entries, err := readVersionEntries(filepath.Join(spaceDir, fd.Name()))
		if err != nil {
			continue
		}
		for _, ve := range entries {
			v, err := loadVersion(spaceID, fd.Name(), ve)
			if err != nil {
				continue
			}
			all = append(all, spaced{fileID: fd.Name(), ve: ve, v: v})
			total += v.Size
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].ve.id != all[j].ve.id {
			return versionLess(all[i].ve.id, all[j].ve.id)
		}
		return all[i].fileID < all[j].fileID
	})
	for _, item := range all {
		if total <= spaceByteBudget {
			break
		}
		if err := removeVersion(item.ve); err != nil {
			return removed, fmt.Errorf("prune version %q: %w", item.ve.id, err)
		}
		total -= item.v.Size
		removed = append(removed, item.v)
		removeDirIfEmpty(filepath.Join(spaceDir, item.fileID))
	}
	return removed, nil
}

// DeleteFileHistory deletes every version of a file.
func (s *Store) DeleteFileHistory(spaceID, fileID string) error {
	dir, err := s.fileHistoryPath(spaceID, fileID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete file history of %q: %w", fileID, err)
	}
	removeDirIfEmpty(filepath.Dir(dir))
	return nil
}

// removeDirIfEmpty removes a directory that has no entries.
func removeDirIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(dir)
	}
}
