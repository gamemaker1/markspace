package store

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// mdExt is the only note file extension the store reads or writes.
const mdExt = ".md"

// trashDirName is the per-space trash directory.
const trashDirName = ".trash"

// maxAllocAttempts bounds the "-2", "-3" suffix search of an imported note.
const maxAllocAttempts = 200

// validSlug checks a space directory name. It stays permissive enough to
// accept every name ListSpaceDirs can return, and rejects only names that
// would leave the spaces directory or hide from a scan.
func validSlug(slug string) error {
	switch {
	case slug == "":
		return fmt.Errorf("space slug is empty: %w", core.ErrInvalid)
	case strings.ContainsAny(slug, "/\\\x00"):
		return fmt.Errorf("space slug %q is not a single name: %w", slug, core.ErrInvalid)
	case strings.HasPrefix(slug, "."):
		return fmt.Errorf("space slug %q starts with a dot: %w", slug, core.ErrInvalid)
	case strings.TrimSpace(slug) != slug:
		return fmt.Errorf("space slug %q is padded: %w", slug, core.ErrInvalid)
	}
	return nil
}

// validRelPath checks a note path. Paths are slash separated, relative to the
// space directory, and end in ".md". Any segment starting with "." is
// rejected, so the trash is reachable only through the trash entry methods.
func validRelPath(relPath string) error {
	if err := validPathShape(relPath); err != nil {
		return err
	}
	for _, seg := range strings.Split(relPath, "/") {
		if strings.HasPrefix(seg, ".") {
			return fmt.Errorf("path %q has a dot segment: %w", relPath, core.ErrInvalid)
		}
	}
	return nil
}

// validPathShape checks everything about a note path except dot segments.
func validPathShape(relPath string) error {
	switch {
	case relPath == "":
		return fmt.Errorf("path is empty: %w", core.ErrInvalid)
	case strings.ContainsRune(relPath, 0):
		return fmt.Errorf("path contains NUL: %w", core.ErrInvalid)
	case strings.ContainsRune(relPath, '\\'):
		return fmt.Errorf("path %q contains a backslash: %w", relPath, core.ErrInvalid)
	case strings.HasPrefix(relPath, "/"):
		return fmt.Errorf("path %q is absolute: %w", relPath, core.ErrInvalid)
	case filepath.IsAbs(relPath):
		return fmt.Errorf("path %q is absolute: %w", relPath, core.ErrInvalid)
	case !strings.HasSuffix(relPath, mdExt):
		return fmt.Errorf("path %q does not end in %s: %w", relPath, mdExt, core.ErrInvalid)
	}
	segments := strings.Split(relPath, "/")
	for _, seg := range segments {
		switch seg {
		case "":
			return fmt.Errorf("path %q has an empty segment: %w", relPath, core.ErrInvalid)
		case ".", "..":
			return fmt.Errorf("path %q has a relative segment: %w", relPath, core.ErrInvalid)
		}
		if strings.TrimSpace(seg) != seg {
			return fmt.Errorf("path %q has a padded segment: %w", relPath, core.ErrInvalid)
		}
	}
	if segments[len(segments)-1] == mdExt {
		return fmt.Errorf("path %q has an empty file name: %w", relPath, core.ErrInvalid)
	}
	return nil
}

// spaceDir returns the absolute directory of one space.
func (s *Store) spaceDir(slug string) (string, error) {
	if err := validSlug(slug); err != nil {
		return "", err
	}
	return filepath.Join(s.spacesDir, slug), nil
}

// resolve validates relPath and joins it onto the space directory. It fails if
// the result leaves the space directory.
func (s *Store) resolve(spaceSlug, relPath string) (string, error) {
	if err := validRelPath(relPath); err != nil {
		return "", err
	}
	return s.resolveUnchecked(spaceSlug, relPath)
}

// resolveUnchecked joins relPath onto the space directory without the dot
// segment rule. Callers must have validated the shape of relPath.
func (s *Store) resolveUnchecked(spaceSlug, relPath string) (string, error) {
	dir, err := s.spaceDir(spaceSlug)
	if err != nil {
		return "", err
	}
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if !withinDir(dir, full) {
		return "", fmt.Errorf("path %q escapes the space directory: %w", relPath, core.ErrInvalid)
	}
	return full, nil
}

// withinDir reports whether target is dir itself or below it.
func withinDir(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// baseName returns the file name of relPath without the ".md" extension.
func baseName(relPath string) string {
	return strings.TrimSuffix(path.Base(relPath), mdExt)
}
