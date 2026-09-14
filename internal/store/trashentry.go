package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// trashMetaName is the sidecar in a trash slot. It starts with a dot, so a
// scan never reports it.
const trashMetaName = ".meta.json"

// trashMeta describes the item in one trash slot.
type trashMeta struct {
	Kind        core.ItemKind `json:"kind"`
	Name        string        `json:"name"`
	TrashedFrom string        `json:"trashedFrom"`
	DeletedAt   time.Time     `json:"deletedAt"`
	DeletedBy   string        `json:"deletedBy,omitempty"`
}

// parseTrashRel splits ".trash/<id>/<base>" into id and base.
func parseTrashRel(trashRel string) (string, string, error) {
	segments := strings.Split(trashRel, "/")
	if len(segments) != 3 || segments[0] != trashDirName {
		return "", "", fmt.Errorf("path %q is not a trash entry: %w", trashRel, core.ErrInvalid)
	}
	if err := validIDSegment(segments[1]); err != nil {
		return "", "", fmt.Errorf("trash path %q: %w", trashRel, err)
	}
	if err := ValidName(segments[2]); err != nil {
		return "", "", fmt.Errorf("trash path %q: %w", trashRel, err)
	}
	return segments[1], segments[2], nil
}

// TrashEntry moves a note, file or folder to ".trash/<id>/<base name>". The
// sidecar is written first and the entry is renamed second, so a crash leaves
// either the entry in place or a complete trash slot. A note also gets the
// trash frontmatter keys. It returns core.ErrExists when the slot already
// holds an entry.
func (s *Store) TrashEntry(spaceSlug, rel, id, deletedBy string) (string, error) {
	full, err := s.resolveEntry(spaceSlug, rel)
	if err != nil {
		return "", err
	}
	if err := validIDSegment(id); err != nil {
		return "", fmt.Errorf("trash %q: %w", rel, err)
	}
	root, err := s.spaceDir(spaceSlug)
	if err != nil {
		return "", err
	}
	base := path.Base(rel)
	trashRel := trashDirName + "/" + id + "/" + base
	slot := filepath.Join(root, trashDirName, id)
	dst := filepath.Join(slot, base)

	s.treeMu.Lock()
	defer s.treeMu.Unlock()
	info, err := lstatEntry(full, rel)
	if err != nil {
		return "", err
	}
	if entries, err := os.ReadDir(slot); err == nil {
		for _, e := range entries {
			if e.Name() != trashMetaName {
				return "", fmt.Errorf("trash slot %q: %w", trashRel, core.ErrExists)
			}
		}
	}
	kind := entryKind(base, info)
	var note *core.Note
	if kind == core.KindNote {
		note, err = s.readTrashCandidate(full, rel, info)
		if err != nil {
			kind = core.KindFile
		}
	}
	now := time.Now().UTC()
	if err := os.MkdirAll(slot, dirMode); err != nil {
		return "", fmt.Errorf("create trash slot for %q: %w", rel, err)
	}
	meta := trashMeta{Kind: kind, Name: base, TrashedFrom: rel, DeletedAt: now, DeletedBy: deletedBy}
	if err := writeTrashMeta(slot, meta); err != nil {
		return "", err
	}
	if err := renameNoReplace(full, dst); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("trash slot %q: %w", trashRel, core.ErrExists)
		}
		return "", fmt.Errorf("trash %q: %w", rel, err)
	}
	syncDir(filepath.Dir(full))
	if note != nil {
		note.Deleted = true
		note.DeletedAt = &now
		note.DeletedBy = deletedBy
		if note.Extra == nil {
			note.Extra = map[string]any{}
		}
		note.Extra[keyTrashedFrom] = rel
		note.Path = trashRel
		if err := s.writeFile(dst, note); err != nil {
			s.logger.Warn("could not mark trashed note", "space", spaceSlug, "path", trashRel, "err", err)
		}
	}
	return trashRel, nil
}

// readTrashCandidate parses a note without minting or writing anything.
func (s *Store) readTrashCandidate(full, rel string, info fs.FileInfo) (*core.Note, error) {
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	note, _, err := parseNote(data, rel, info.ModTime())
	if err != nil {
		return nil, err
	}
	note.ParentID = ""
	return note, nil
}

// writeTrashMeta writes the sidecar of a trash slot atomically.
func writeTrashMeta(slot string, meta trashMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(slot, trashMetaName), append(data, '\n')); err != nil {
		return fmt.Errorf("write trash metadata: %w", err)
	}
	return nil
}

// readTrashMeta loads the sidecar of a slot. ok is false when it is missing
// or unreadable.
func readTrashMeta(slot string) (trashMeta, bool) {
	data, err := os.ReadFile(filepath.Join(slot, trashMetaName))
	if err != nil {
		return trashMeta{}, false
	}
	var meta trashMeta
	if json.Unmarshal(data, &meta) != nil || meta.Kind == "" {
		return trashMeta{}, false
	}
	return meta, true
}

// RestoreEntry moves a trashed entry to targetRel. The parent folder of
// targetRel must exist, and a name taken ignoring case returns
// core.ErrExists. A note loses its trash frontmatter keys before the move, and
// the sidecar stays until the move completes, so a crash leaves a trash slot
// that still describes the item.
func (s *Store) RestoreEntry(spaceSlug, trashRel, targetRel string) error {
	id, base, err := parseTrashRel(trashRel)
	if err != nil {
		return err
	}
	dstFull, err := s.resolveEntry(spaceSlug, targetRel)
	if err != nil {
		return err
	}
	root, err := s.spaceDir(spaceSlug)
	if err != nil {
		return err
	}
	slot := filepath.Join(root, trashDirName, id)
	srcFull := filepath.Join(slot, base)
	target := parentRel(targetRel)

	s.treeMu.Lock()
	defer s.treeMu.Unlock()
	info, err := lstatEntry(srcFull, trashRel)
	if err != nil {
		return err
	}
	if err := requireFolder(filepath.Dir(dstFull), target); err != nil {
		return err
	}
	if existing, taken := s.CaseCollision(spaceSlug, target, path.Base(targetRel), ""); taken {
		return fmt.Errorf("restore to %q: name taken by %q: %w", targetRel, existing, core.ErrExists)
	}
	meta, hasMeta := readTrashMeta(slot)
	if entryKind(base, info) == core.KindNote && (!hasMeta || meta.Kind == core.KindNote) {
		if note, err := s.readTrashCandidate(srcFull, trashRel, info); err == nil {
			note.Deleted = false
			note.DeletedAt = nil
			note.DeletedBy = ""
			delete(note.Extra, keyTrashedFrom)
			if err := s.writeFile(srcFull, note); err != nil {
				return fmt.Errorf("clear trash keys on %q: %w", trashRel, err)
			}
		}
	}
	if err := renameNoReplace(srcFull, dstFull); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("restore to %q: %w", targetRel, core.ErrExists)
		}
		return fmt.Errorf("restore %q: %w", trashRel, err)
	}
	syncDir(filepath.Dir(dstFull))
	s.removeFinishedSlot(root, slot)
	return nil
}

// removeFinishedSlot deletes a slot that holds nothing but its sidecar, then
// the trash root when it is empty.
func (s *Store) removeFinishedSlot(root, slot string) {
	entries, err := os.ReadDir(slot)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != trashMetaName {
			return
		}
	}
	_ = os.Remove(filepath.Join(slot, trashMetaName))
	_ = os.Remove(slot)
	s.removeEmptyTrashRoot(root)
}

// ScanTrashEntries lists every trashed item, sorted by trash path. Each slot
// reports the entry its sidecar names. A slot without a sidecar, or whose
// entry is missing, as after a crash between the sidecar and the move, is
// skipped.
func (s *Store) ScanTrashEntries(spaceSlug string) ([]ports.TrashedEntry, error) {
	root, err := s.spaceDir(spaceSlug)
	if err != nil {
		return nil, err
	}
	trashRoot := filepath.Join(root, trashDirName)
	slots, err := os.ReadDir(trashRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read trash of %q: %w", spaceSlug, err)
	}
	var out []ports.TrashedEntry
	for _, slotEntry := range slots {
		id := slotEntry.Name()
		if !slotEntry.IsDir() || validIDSegment(id) != nil {
			continue
		}
		slot := filepath.Join(trashRoot, id)
		if meta, ok := readTrashMeta(slot); ok {
			if te, ok := s.trashedFromMeta(spaceSlug, slot, id, meta); ok {
				out = append(out, te)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TrashRel < out[j].TrashRel })
	return out, nil
}

// trashedFromMeta builds the record of a slot with a sidecar.
func (s *Store) trashedFromMeta(spaceSlug, slot, id string, meta trashMeta) (ports.TrashedEntry, bool) {
	name := meta.Name
	if ValidName(name) != nil {
		return ports.TrashedEntry{}, false
	}
	full := filepath.Join(slot, name)
	info, err := os.Lstat(full)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 {
		return ports.TrashedEntry{}, false
	}
	trashRel := trashDirName + "/" + id + "/" + name
	te := ports.TrashedEntry{
		ID: id, Kind: meta.Kind, TrashRel: trashRel, TrashedFrom: meta.TrashedFrom,
		DeletedAt: meta.DeletedAt, DeletedBy: meta.DeletedBy, ModTime: info.ModTime().UTC(),
	}
	if info.IsDir() {
		te.Kind = core.KindFolder
		te.Size = treeSize(full)
		return te, true
	}
	te.Size = info.Size()
	if meta.Kind == core.KindNote {
		if note, err := s.readTrashCandidate(full, trashRel, info); err == nil {
			note.SpaceSlug = spaceSlug
			te.Note = note
		} else {
			te.Kind = core.KindFile
		}
	}
	return te, true
}

// treeSize sums the sizes of regular files below dir.
func treeSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// PurgeTrashEntry permanently deletes the slot named by ".trash/<id>" or
// ".trash/<id>/<base>".
func (s *Store) PurgeTrashEntry(spaceSlug, trashRel string) error {
	s.treeMu.Lock()
	defer s.treeMu.Unlock()
	return s.removeTrashSlot(spaceSlug, trashRel, true)
}
