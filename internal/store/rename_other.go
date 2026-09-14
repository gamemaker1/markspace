//go:build !linux

package store

// renameNoReplace renames oldpath to newpath and fails with fs.ErrExist when
// newpath exists. Outside Linux the check and the rename are two steps.
func renameNoReplace(oldpath, newpath string) error {
	return renameNoClobber(oldpath, newpath)
}
