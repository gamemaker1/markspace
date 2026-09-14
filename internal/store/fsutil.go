package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// linkFile makes a hard link. Tests replace it to force the copy fallback.
var linkFile = os.Link

// randomHex returns 16 random hex characters.
func randomHex() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("store: entropy source failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// tempPath returns a fresh temporary name inside dir. The name starts with a
// dot and contains ".tmp-", so scans and the watcher skip it.
func tempPath(dir string) string {
	return filepath.Join(dir, ".tmp-"+randomHex())
}

// syncDir flushes a directory entry change. Errors are ignored, because some
// filesystems do not support fsync on directories.
func syncDir(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = f.Sync()
	_ = f.Close()
}

// renameNoClobber renames when newpath does not exist, checking first. It is
// the fallback where the kernel has no atomic no-replace rename.
func renameNoClobber(oldpath, newpath string) error {
	if _, err := os.Lstat(newpath); err == nil {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrExist}
	}
	return os.Rename(oldpath, newpath)
}

// copyFileSynced copies a regular file to a new path, which must not exist,
// and fsyncs it. It returns the bytes copied.
func copyFileSynced(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(dst)
		return 0, err
	}
	return n, nil
}

// linkOrCopy hard-links src to dst and copies when linking fails. It returns
// the bytes copied, 0 for a link.
func linkOrCopy(src, dst string) (int64, error) {
	if err := linkFile(src, dst); err == nil {
		return 0, nil
	} else if errors.Is(err, fs.ErrExist) {
		return 0, err
	}
	n, err := copyFileSynced(src, dst)
	if err != nil {
		return 0, fmt.Errorf("copy %q: %w", src, err)
	}
	return n, nil
}
