package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbedCoversStaticFiles checks that every file under static and
// templates on disk is also in the embedded filesystem. A new directory that
// the go:embed pattern misses would otherwise work in dev mode and return 404
// in a release binary.
func TestEmbedCoversStaticFiles(t *testing.T) {
	for _, root := range []string{"static", "templates"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() || strings.HasPrefix(name, ".") {
				return nil
			}
			if _, statErr := fs.Stat(embedded, filepath.ToSlash(p)); statErr != nil {
				t.Errorf("%s is on disk but not embedded", p)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}
