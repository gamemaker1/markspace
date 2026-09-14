package store

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// buildZip writes a zip holding the given name to content entries.
func buildZip(t *testing.T, entries [][2]string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

func limitedStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSpace("docs"); err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func setLimit[T any](t *testing.T, v *T, to T) {
	t.Helper()
	old := *v
	*v = to
	t.Cleanup(func() { *v = old })
}

func TestReadZipEntryRefusesInsteadOfTruncating(t *testing.T) {
	zr := buildZip(t, [][2]string{{"a.md", strings.Repeat("z", 100)}})
	r, err := zip.NewReader(zr, zr.Size())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readZipEntry(r.File[0], 50); !errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	data, err := readZipEntry(r.File[0], 100)
	if err != nil || len(data) != 100 {
		t.Fatalf("exact-size read = %d, %v", len(data), err)
	}
}
