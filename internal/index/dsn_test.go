package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// TestOpenPathWithURIDelimiters opens databases whose paths hold characters
// that start an escape, a query or a fragment in a file: URI, and checks that
// each one gets its own file.
func TestOpenPathWithURIDelimiters(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"seed#1", "q?x", "pct%41"} {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "m.db")
		db, err := Open(p, nil)
		if err != nil {
			t.Fatalf("Open(%q): %v", p, err)
		}
		if err := db.CreateSpace(context.Background(), &core.Space{Slug: "s", Name: "S"}); err != nil {
			t.Fatalf("CreateSpace in %q: %v", name, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("database file %q missing: %v", p, err)
		}
	}
}
