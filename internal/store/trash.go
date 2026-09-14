package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// EmptyTrash permanently deletes the whole space trash directory.
func (s *Store) EmptyTrash(spaceSlug string) error {
	dir, err := s.spaceDir(spaceSlug)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(dir, trashDirName)); err != nil {
		return fmt.Errorf("empty trash of %q: %w", spaceSlug, err)
	}
	return nil
}

// removeTrashSlot deletes the .trash/<noteID> directory named by trashPath.
// When force is false it only removes the directory if it is empty.
func (s *Store) removeTrashSlot(spaceSlug, trashPath string, force bool) error {
	dir, err := s.spaceDir(spaceSlug)
	if err != nil {
		return err
	}
	noteID, err := trashSlotID(trashPath)
	if err != nil {
		return err
	}
	slot := filepath.Join(dir, trashDirName, noteID)
	if !withinDir(dir, slot) {
		return fmt.Errorf("trash path %q escapes the space: %w", trashPath, core.ErrInvalid)
	}
	if force {
		if err := os.RemoveAll(slot); err != nil {
			return fmt.Errorf("purge trash %q: %w", trashPath, err)
		}
		s.removeEmptyTrashRoot(dir)
		return nil
	}
	if err := os.Remove(slot); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// A non-empty slot still holds siblings, which is not an error.
		s.logger.Debug("trash slot kept", "space", spaceSlug, "slot", noteID, "err", err)
		return nil
	}
	s.removeEmptyTrashRoot(dir)
	return nil
}

// removeEmptyTrashRoot deletes the .trash directory once its last slot is
// gone. A non-empty directory is left alone.
func (s *Store) removeEmptyTrashRoot(spaceDir string) {
	root := filepath.Join(spaceDir, trashDirName)
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) > 0 {
		return
	}
	if err := os.Remove(root); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.logger.Debug("trash root kept", "path", root, "err", err)
	}
}

// trashSlotID returns the note id segment of a trash path.
func trashSlotID(trashPath string) (string, error) {
	segments := strings.Split(strings.Trim(trashPath, "/"), "/")
	if len(segments) < 2 || segments[0] != trashDirName {
		return "", fmt.Errorf("path %q is not a trash path: %w", trashPath, core.ErrInvalid)
	}
	if err := validIDSegment(segments[1]); err != nil {
		return "", fmt.Errorf("trash path %q: %w", trashPath, err)
	}
	return segments[1], nil
}

// validIDSegment checks a note id used as a directory name.
func validIDSegment(id string) error {
	if id == "" {
		return fmt.Errorf("note id is empty: %w", core.ErrInvalid)
	}
	if strings.ContainsAny(id, "/\\\x00") || strings.HasPrefix(id, ".") {
		return fmt.Errorf("note id %q is not a name: %w", id, core.ErrInvalid)
	}
	return nil
}
