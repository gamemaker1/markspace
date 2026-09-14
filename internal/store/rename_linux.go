//go:build linux

package store

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames oldpath to newpath and fails with fs.ErrExist when
// newpath exists. It uses renameat2 with RENAME_NOREPLACE, which is atomic,
// and falls back to a check then a rename where the filesystem lacks it.
func renameNoReplace(oldpath, newpath string) error {
	err := unix.Renameat2(unix.AT_FDCWD, oldpath, unix.AT_FDCWD, newpath, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrExist}
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOTSUP):
		return renameNoClobber(oldpath, newpath)
	case errors.Is(err, unix.ENOENT):
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrNotExist}
	}
	return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: err}
}
