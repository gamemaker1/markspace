package store

import (
	"fmt"
	"os"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
)

// maxNameBytes is the longest folder, note or file name the store accepts.
const maxNameBytes = 255

// maxEntryPathBytes bounds a whole entry path, below the usual PATH_MAX.
const maxEntryPathBytes = 3072

// maxNameAllocAttempts bounds the "(N)" and "copy N" search.
const maxNameAllocAttempts = 10000

// ignoredExactNames are operating system metadata files that scans skip.
var ignoredExactNames = map[string]bool{
	".DS_Store":   true,
	"Thumbs.db":   true,
	"desktop.ini": true,
}

// IgnoredName reports whether a scan skips name. That covers dot names,
// temporary names matching "*.tmp-*" or "~$*", and operating system metadata
// files.
func IgnoredName(name string) bool {
	return strings.HasPrefix(name, ".") ||
		strings.Contains(name, ".tmp-") ||
		strings.HasPrefix(name, "~$") ||
		ignoredExactNames[name]
}

// ValidName checks one folder, note or file name. A valid name is non-empty
// UTF-8 of at most 255 bytes, is not "." or "..", holds no "/", "\", NUL or
// control character, does not start with "." or end or start with white
// space, and is not an ignored name.
func ValidName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("name is empty: %w", core.ErrInvalid)
	case name == "." || name == "..":
		return fmt.Errorf("name %q is a relative segment: %w", name, core.ErrInvalid)
	case len(name) > maxNameBytes:
		return fmt.Errorf("name is %d bytes, over %d: %w", len(name), maxNameBytes, core.ErrInvalid)
	case !utf8.ValidString(name):
		return fmt.Errorf("name %q is not UTF-8: %w", name, core.ErrInvalid)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("name %q starts with a dot: %w", name, core.ErrInvalid)
	case strings.TrimSpace(name) != name:
		return fmt.Errorf("name %q has leading or trailing space: %w", name, core.ErrInvalid)
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) {
			return fmt.Errorf("name %q contains %U: %w", name, r, core.ErrInvalid)
		}
	}
	if IgnoredName(name) {
		return fmt.Errorf("name %q is a temporary or system name: %w", name, core.ErrInvalid)
	}
	return nil
}

// ValidName checks one name against the name rules.
func (s *Store) ValidName(name string) error { return ValidName(name) }

// validEntryPath checks an entry path: slash separated segments that each
// pass ValidName. Such a path is already in path.Clean form and cannot leave
// the space directory.
func validEntryPath(rel string) error {
	if rel == "" {
		return fmt.Errorf("entry path is empty: %w", core.ErrInvalid)
	}
	if len(rel) > maxEntryPathBytes {
		return fmt.Errorf("entry path is %d bytes: %w", len(rel), core.ErrInvalid)
	}
	for _, seg := range strings.Split(rel, "/") {
		if err := ValidName(seg); err != nil {
			return fmt.Errorf("entry path %q: %w", rel, err)
		}
	}
	return nil
}

// validFolderPath accepts "" for the space root or an entry path.
func validFolderPath(rel string) error {
	if rel == "" {
		return nil
	}
	return validEntryPath(rel)
}

// parentRel returns the folder holding rel, "" at the space root.
func parentRel(rel string) string {
	dir := path.Dir(rel)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

// joinRel appends name to a folder path, where "" is the space root.
func joinRel(folder, name string) string {
	if folder == "" {
		return name
	}
	return folder + "/" + name
}

// foldKey maps every rune to the smallest rune of its simple case folding
// orbit. Two strings have equal keys exactly when strings.EqualFold reports
// them equal.
func foldKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		lowest := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < lowest {
				lowest = f
			}
		}
		b.WriteRune(lowest)
	}
	return b.String()
}

// foldHasPrefix reports whether s starts with prefix ignoring case.
func foldHasPrefix(s, prefix string) bool {
	return strings.HasPrefix(foldKey(s), foldKey(prefix))
}

// CaseCollision returns the name of an entry in the folder parentRel that
// equals name ignoring case, other than the entry at exceptRel. An exact
// match counts. A missing folder has no collisions.
func (s *Store) CaseCollision(spaceSlug, parentRel, name, exceptRel string) (string, bool) {
	if validFolderPath(parentRel) != nil {
		return "", false
	}
	dir, err := s.resolveFolder(spaceSlug, parentRel)
	if err != nil {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	key := foldKey(name)
	for _, e := range entries {
		if foldKey(e.Name()) != key {
			continue
		}
		if exceptRel != "" && joinRel(parentRel, e.Name()) == exceptRel {
			continue
		}
		return e.Name(), true
	}
	return "", false
}

// takenNames returns the fold keys of every entry in a folder except
// exceptName.
func (s *Store) takenNames(spaceSlug, folder, exceptName string) (map[string]bool, error) {
	dir, err := s.resolveFolder(spaceSlug, folder)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read folder %q: %w", folder, err)
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		if exceptName != "" && e.Name() == exceptName {
			continue
		}
		out[foldKey(e.Name())] = true
	}
	return out, nil
}

// splitName separates a name into stem and extension. A folder has no
// extension, and ".tar.gz" style double extensions stay together.
func splitName(name string, isDir bool) (string, string) {
	if isDir {
		return name, ""
	}
	ext := path.Ext(name)
	if ext == "" || ext == "." || len(ext) == len(name) || strings.ContainsRune(ext, ' ') {
		return name, ""
	}
	stem := name[:len(name)-len(ext)]
	if inner := path.Ext(stem); strings.EqualFold(inner, ".tar") && len(inner) < len(stem) {
		stem = stem[:len(stem)-len(inner)]
		ext = inner + ext
	}
	return stem, ext
}

// truncateUTF8 shortens s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// fitName joins stem and suffix, shortening the stem so the result fits the
// name limit.
func fitName(stem, suffix string) string {
	room := maxNameBytes - len(suffix)
	if room < 1 {
		room = 1
	}
	stem = strings.TrimRightFunc(truncateUTF8(stem, room), unicode.IsSpace)
	return stem + suffix
}

// allocFrom returns the first candidate that is valid and not taken.
func allocFrom(taken map[string]bool, first string, next func(n int) string) (string, error) {
	if ValidName(first) == nil && !taken[foldKey(first)] {
		return first, nil
	}
	for n := 2; n <= maxNameAllocAttempts; n++ {
		candidate := next(n)
		if ValidName(candidate) == nil && !taken[foldKey(candidate)] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free name near %q: %w", first, core.ErrExists)
}

// AllocName returns name when no entry of parentRel other than exceptRel
// collides with it ignoring case, and otherwise "<stem> (N).<ext>" with the
// first free N from 2.
func (s *Store) AllocName(spaceSlug, parentRel, name, exceptRel string, isDir bool) (string, error) {
	if err := ValidName(name); err != nil {
		return "", err
	}
	if err := validFolderPath(parentRel); err != nil {
		return "", err
	}
	except := ""
	if exceptRel != "" && parentRel == parentRelOf(exceptRel) {
		except = path.Base(exceptRel)
	}
	taken, err := s.takenNames(spaceSlug, parentRel, except)
	if err != nil {
		return "", err
	}
	stem, ext := splitName(name, isDir)
	return allocFrom(taken, name, func(n int) string {
		return fitName(stem, fmt.Sprintf(" (%d)%s", n, ext))
	})
}

// AllocCopyName returns "<stem> copy.<ext>", then "<stem> copy N.<ext>" with
// the first free N from 2.
func (s *Store) AllocCopyName(spaceSlug, parentRel, name string, isDir bool) (string, error) {
	if err := ValidName(name); err != nil {
		return "", err
	}
	if err := validFolderPath(parentRel); err != nil {
		return "", err
	}
	taken, err := s.takenNames(spaceSlug, parentRel, "")
	if err != nil {
		return "", err
	}
	stem, ext := splitName(name, isDir)
	return allocFrom(taken, fitName(stem, " copy"+ext), func(n int) string {
		return fitName(stem, fmt.Sprintf(" copy %d%s", n, ext))
	})
}

// AllocNotePath picks "<slug>.md", then "<slug>-N.md", free in folderRel
// ignoring case. exceptRel keeps its own name on a rename.
func (s *Store) AllocNotePath(spaceSlug, folderRel, title, exceptRel string) (string, error) {
	if err := validFolderPath(folderRel); err != nil {
		return "", err
	}
	except := ""
	if exceptRel != "" && folderRel == parentRelOf(exceptRel) {
		except = path.Base(exceptRel)
	}
	taken, err := s.takenNames(spaceSlug, folderRel, except)
	if err != nil {
		return "", err
	}
	slug := core.Slugify(title)
	name, err := allocFrom(taken, fitName(slug, mdExt), func(n int) string {
		return fitName(slug, fmt.Sprintf("-%d%s", n, mdExt))
	})
	if err != nil {
		return "", err
	}
	return joinRel(folderRel, name), nil
}

// parentRelOf is parentRel for a path that may be empty.
func parentRelOf(rel string) string {
	if rel == "" {
		return ""
	}
	return parentRel(rel)
}

// sanitizeName turns an arbitrary name, such as a client upload name, into a
// valid name. Separators and control characters become
// "_", leading dots and outer space are dropped, and an ignored name gets a
// "_" prefix. It returns "file" when nothing usable is left.
func sanitizeName(name string) string {
	name = strings.ToValidUTF8(name, "_")
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	name = strings.TrimLeft(name, ".")
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, ".tmp-", "_tmp-")
	if IgnoredName(name) && name != "" {
		name = "_" + name
	}
	name = strings.TrimRightFunc(truncateUTF8(name, maxNameBytes), unicode.IsSpace)
	if ValidName(name) != nil {
		return "file"
	}
	return name
}
