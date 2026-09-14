package store

import (
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
)

// headerChunk is how many bytes ListVersions reads to find the frontmatter.
const headerChunk = 64 << 10

// SaveVersion writes a snapshot of a note under
// <dataDir>/internal/history/<spaceID>/<noteID>/<unix-millis>.md. The label is
// recorded as a frontmatter key in the snapshot only. The version id is the
// file base name.
func (s *Store) SaveVersion(spaceID string, note *core.Note, label string) (*core.NoteVersion, error) {
	if note == nil {
		return nil, fmt.Errorf("save version: nil note: %w", core.ErrInvalid)
	}
	dir, err := s.versionDir(spaceID, note.ID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create history dir for %q: %w", note.ID, err)
	}
	snapshot := *note
	snapshot.Extra = map[string]any{}
	for k, v := range note.Extra {
		snapshot.Extra[k] = v
	}
	if label != "" {
		snapshot.Extra[keyLabel] = label
	} else {
		delete(snapshot.Extra, keyLabel)
	}
	data, err := serializeNote(&snapshot)
	if err != nil {
		return nil, err
	}
	millis := time.Now().UTC().UnixMilli()
	var full string
	for {
		full = filepath.Join(dir, strconv.FormatInt(millis, 10)+mdExt)
		if _, err := os.Lstat(full); errors.Is(err, fs.ErrNotExist) {
			break
		}
		millis++
	}
	if err := writeFileAtomic(full, data); err != nil {
		return nil, fmt.Errorf("write version of %q: %w", note.ID, err)
	}
	return &core.NoteVersion{
		ID:        strconv.FormatInt(millis, 10),
		NoteID:    note.ID,
		SpaceID:   spaceID,
		Title:     note.Title,
		Size:      len(data),
		AuthorID:  versionAuthor(note.UpdatedBy, note.CreatedBy),
		Via:       versionVia(note.UpdatedBy, note.UpdatedVia),
		Label:     label,
		CreatedAt: time.UnixMilli(millis).UTC(),
	}, nil
}

// ListVersions returns snapshots newest first without loading bodies.
func (s *Store) ListVersions(spaceID, noteID string) ([]*core.NoteVersion, error) {
	dir, err := s.versionDir(spaceID, noteID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read history of %q: %w", noteID, err)
	}
	var out []*core.NoteVersion
	for _, e := range entries {
		id, ok := versionID(e)
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		v := &core.NoteVersion{
			ID:        id,
			NoteID:    noteID,
			SpaceID:   spaceID,
			Size:      int(info.Size()),
			CreatedAt: versionTime(id, info.ModTime()),
		}
		if header, err := readHeader(filepath.Join(dir, e.Name())); err == nil {
			fillVersionHeader(v, header)
		}
		out = append(out, v)
	}
	sortVersionsDesc(out)
	return out, nil
}

// ReadVersion returns one snapshot with its content.
func (s *Store) ReadVersion(spaceID, noteID, versionID string) (*core.NoteVersion, error) {
	dir, err := s.versionDir(spaceID, noteID)
	if err != nil {
		return nil, err
	}
	if err := validIDSegment(versionID); err != nil {
		return nil, fmt.Errorf("version %q: %w", versionID, err)
	}
	full := filepath.Join(dir, versionID+mdExt)
	if !withinDir(dir, full) {
		return nil, fmt.Errorf("version %q escapes the history dir: %w", versionID, core.ErrInvalid)
	}
	data, err := os.ReadFile(full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("version %q of note %q: %w", versionID, noteID, core.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read version %q: %w", versionID, err)
	}
	info, err := os.Stat(full)
	modTime := time.Time{}
	if err == nil {
		modTime = info.ModTime()
	}
	yamlText, body, _ := splitFrontmatter(data)
	fm, err := parseFrontmatter(yamlText)
	if err != nil {
		return nil, err
	}
	v := &core.NoteVersion{
		ID:        versionID,
		NoteID:    noteID,
		SpaceID:   spaceID,
		Content:   body,
		Size:      len(data),
		CreatedAt: versionTime(versionID, modTime),
	}
	fillVersionHeader(v, fm)
	return v, nil
}

// PruneVersions keeps the newest keep snapshots of a note and deletes the
// rest.
func (s *Store) PruneVersions(spaceID, noteID string, keep int) error {
	dir, err := s.versionDir(spaceID, noteID)
	if err != nil {
		return err
	}
	if keep < 0 {
		keep = 0
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read history of %q: %w", noteID, err)
	}
	var ids []string
	for _, e := range entries {
		if id, ok := versionID(e); ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return versionLess(ids[j], ids[i]) })
	if len(ids) <= keep {
		return nil
	}
	for _, id := range ids[keep:] {
		if err := os.Remove(filepath.Join(dir, id+mdExt)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("prune version %q: %w", id, err)
		}
	}
	return nil
}

// versionDir returns the history directory of one note.
func (s *Store) versionDir(spaceID, noteID string) (string, error) {
	if err := validIDSegment(spaceID); err != nil {
		return "", fmt.Errorf("space id: %w", err)
	}
	if err := validIDSegment(noteID); err != nil {
		return "", fmt.Errorf("note id: %w", err)
	}
	dir := filepath.Join(s.historyDir, spaceID, noteID)
	if !withinDir(s.historyDir, dir) {
		return "", fmt.Errorf("history path for %q escapes the history dir: %w", noteID, core.ErrInvalid)
	}
	return dir, nil
}

// versionID returns the version id of a history directory entry.
func versionID(e fs.DirEntry) (string, bool) {
	if e.IsDir() || !strings.HasSuffix(e.Name(), mdExt) {
		return "", false
	}
	return strings.TrimSuffix(e.Name(), mdExt), true
}

// versionTime turns a unix millisecond version id into a time, falling back
// to the file modification time.
func versionTime(id string, modTime time.Time) time.Time {
	if millis, err := strconv.ParseInt(id, 10, 64); err == nil {
		return time.UnixMilli(millis).UTC()
	}
	return modTime.UTC()
}

// versionLess orders version ids oldest first, numerically when possible.
func versionLess(a, b string) bool {
	ai, aerr := strconv.ParseInt(a, 10, 64)
	bi, berr := strconv.ParseInt(b, 10, 64)
	if aerr == nil && berr == nil {
		return ai < bi
	}
	return a < b
}

// sortVersionsDesc orders versions newest first.
func sortVersionsDesc(vs []*core.NoteVersion) {
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[j].ID, vs[i].ID) })
}

// fillVersionHeader copies title, author, via and label out of snapshot
// frontmatter.
func fillVersionHeader(v *core.NoteVersion, fm map[string]any) {
	v.Title = asString(fm[keyTitle])
	v.AuthorID = versionAuthor(asString(fm[keyUpdatedBy]), asString(fm[keyAuthor]))
	v.Via = versionVia(asString(fm[keyUpdatedBy]), asString(fm[keyUpdatedVia]))
	v.Label = asString(fm[keyLabel])
}

// versionAuthor prefers the last editor and falls back to the creator.
func versionAuthor(updatedBy, author string) string {
	if updatedBy != "" {
		return updatedBy
	}
	return author
}

// versionVia returns the plugin of the last editor. A version whose author
// falls back to the creator has no via.
func versionVia(updatedBy, via string) string {
	if updatedBy == "" {
		return ""
	}
	return via
}

// readHeader parses the frontmatter of a file without loading the whole body.
func readHeader(full string) (map[string]any, error) {
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, headerChunk)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	yamlText, _, ok := splitFrontmatter(buf[:n])
	if !ok {
		if n < headerChunk {
			return map[string]any{}, nil
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		yamlText, _, _ = splitFrontmatter(data)
	}
	return parseFrontmatter(yamlText)
}
