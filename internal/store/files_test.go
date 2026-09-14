package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// pngBytes encodes a w by h PNG, opaque or with a transparent pixel.
func pngBytes(t *testing.T, w, h int, alpha bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	if alpha {
		img.Set(0, 0, color.NRGBA{A: 0})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestSaveFile(t *testing.T) {
	s := newTestStore(t)
	mustMkdir(t, s, "docs", "Projects")
	mustSave(t, s, "docs", "Projects/taken.txt", "x")
	img := pngBytes(t, 3, 2, false)
	tests := []struct {
		rel     string
		content string
		max     int64
		err     error
		mime    string
		w, h    int
	}{
		{"hello.txt", "hello", 0, nil, "text/plain; charset=utf-8", 0, 0},
		{"Projects/east wall.png", string(img), 1 << 20, nil, "image/png", 3, 2},
		{"Projects/noext", "%PDF-1.7 rest", 0, nil, "application/pdf", 0, 0},
		{"exact.bin", "12345", 5, nil, "", 0, 0},
		{"big.bin", "123456", 5, core.ErrTooLarge, "", 0, 0},
		{"Projects/taken.txt", "y", 0, core.ErrExists, "", 0, 0},
		{"Projects/TAKEN.TXT", "y", 0, core.ErrExists, "", 0, 0},
		{"Missing/a.txt", "y", 0, core.ErrNotFound, "", 0, 0},
		{".secret", "y", 0, core.ErrInvalid, "", 0, 0},
		{"a/../b", "y", 0, core.ErrInvalid, "", 0, 0},
		{"Thumbs.db", "y", 0, core.ErrInvalid, "", 0, 0},
	}
	for _, tt := range tests {
		saved, err := s.SaveFile("docs", tt.rel, strings.NewReader(tt.content), tt.max)
		if !errors.Is(err, tt.err) {
			t.Errorf("SaveFile(%q) = %v, want %v", tt.rel, err, tt.err)
			continue
		}
		if err != nil {
			continue
		}
		if saved.Size != int64(len(tt.content)) || saved.SHA256 != sha(tt.content) {
			t.Errorf("SaveFile(%q) = %+v", tt.rel, saved)
		}
		if tt.mime != "" && saved.MIME != tt.mime {
			t.Errorf("SaveFile(%q) mime = %q, want %q", tt.rel, saved.MIME, tt.mime)
		}
		if saved.Width != tt.w || saved.Height != tt.h {
			t.Errorf("SaveFile(%q) size = %dx%d, want %dx%d", tt.rel, saved.Width, saved.Height, tt.w, tt.h)
		}
		data, err := os.ReadFile(filepath.Join(s.SpacesDir(), "docs", filepath.FromSlash(tt.rel)))
		if err != nil || string(data) != tt.content {
			t.Errorf("content of %q = %q, %v", tt.rel, data, err)
		}
	}
	if taken, _ := os.ReadFile(filepath.Join(s.SpacesDir(), "docs", "Projects", "taken.txt")); string(taken) != "x" {
		t.Errorf("existing file overwritten: %q", taken)
	}
	assertNoTemp(t, s.DataDir())
}

func TestOpenAndHashFile(t *testing.T) {
	s := newTestStore(t)
	mustMkdir(t, s, "docs", "dir")
	saved, err := s.SaveFile("docs", "dir/a.bin", strings.NewReader("content"), 0)
	if err != nil {
		t.Fatal(err)
	}
	f, info, err := s.OpenFile("docs", "dir/a.bin")
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.Seek(3, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(f)
	f.Close()
	if string(rest) != "tent" || info.Size() != 7 {
		t.Fatalf("OpenFile read %q size %d", rest, info.Size())
	}
	sum, err := s.HashFile("docs", "dir/a.bin")
	if err != nil || sum != saved.SHA256 {
		t.Fatalf("HashFile = %q, %v, want %q", sum, err, saved.SHA256)
	}
	if _, _, err := s.OpenFile("docs", "dir"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("OpenFile folder = %v", err)
	}
	if _, _, err := s.OpenFile("docs", "dir/missing"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("OpenFile missing = %v", err)
	}
	if _, err := s.HashFile("docs", "../x"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("HashFile invalid = %v", err)
	}
}

// readAllClose reads and closes a reader.
func readAllClose(t *testing.T, r io.ReadCloser) string {
	t.Helper()
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReplaceFileVersions(t *testing.T) {
	s := newTestStore(t)
	mustSave(t, s, "docs", "doc.txt", "v1")
	key := ports.FileHistoryKey{SpaceID: "sp1", FileID: "f1", AuthorID: "u1"}
	before, err := os.Stat(filepath.Join(s.SpacesDir(), "docs", "doc.txt"))
	if err != nil {
		t.Fatal(err)
	}

	saved, v1, err := s.ReplaceFile("docs", "doc.txt", strings.NewReader("version two"), 0, key)
	if err != nil {
		t.Fatalf("ReplaceFile: %v", err)
	}
	if saved.SHA256 != sha("version two") || saved.Size != 11 {
		t.Fatalf("saved = %+v", saved)
	}
	if v1.Name != "doc.txt" || v1.SHA256 != sha("v1") || v1.Size != 2 || v1.AuthorID != "u1" || v1.FileID != "f1" || !strings.HasPrefix(v1.MIME, "text/plain") {
		t.Fatalf("version = %+v", v1)
	}
	hist, err := filepath.Glob(filepath.Join(s.DataDir(), "internal", "file-history", "sp1", "f1", v1.ID+"-doc.txt"))
	if err != nil || len(hist) != 1 {
		t.Fatalf("history file = %v, %v", hist, err)
	}
	if info, err := os.Stat(hist[0]); err != nil || !os.SameFile(before, info) {
		t.Fatalf("history file is not the old inode: %v", err)
	}
	if _, err := os.Stat(hist[0] + ".json"); err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}

	key.Name = "doc.txt"
	_, v2, err := s.ReplaceFile("docs", "doc.txt", strings.NewReader("v3"), 0, key)
	if err != nil {
		t.Fatalf("second ReplaceFile: %v", err)
	}
	list, err := s.ListFileVersions("sp1", "f1")
	if err != nil || len(list) != 2 || list[0].ID != v2.ID || list[1].ID != v1.ID {
		t.Fatalf("ListFileVersions = %+v, %v", list, err)
	}
	if list[0].SHA256 != sha("version two") || list[0].AuthorID != "u1" {
		t.Fatalf("listed version = %+v", list[0])
	}
	rc, got, err := s.OpenFileVersion("sp1", "f1", v1.ID)
	if err != nil || got.ID != v1.ID {
		t.Fatalf("OpenFileVersion = %+v, %v", got, err)
	}
	if body := readAllClose(t, rc); body != "v1" {
		t.Fatalf("version body = %q", body)
	}

	restored, v3, err := s.RestoreFileVersion("docs", "doc.txt", v1.ID, key)
	if err != nil {
		t.Fatalf("RestoreFileVersion: %v", err)
	}
	if restored.SHA256 != sha("v1") || v3.SHA256 != sha("v3") {
		t.Fatalf("restore = %+v, version %+v", restored, v3)
	}
	current, _ := os.ReadFile(filepath.Join(s.SpacesDir(), "docs", "doc.txt"))
	if string(current) != "v1" {
		t.Fatalf("current after restore = %q", current)
	}
	if list, _ := s.ListFileVersions("sp1", "f1"); len(list) != 3 {
		t.Fatalf("versions after restore = %d", len(list))
	}

	removed, err := s.PruneFileVersions("sp1", "f1", 2, 0)
	if err != nil || len(removed) != 1 || removed[0].ID != v1.ID {
		t.Fatalf("PruneFileVersions keep 2 = %+v, %v", removed, err)
	}
	if removed, err := s.PruneFileVersions("sp1", "f1", -1, 0); err != nil || len(removed) != 0 {
		t.Fatalf("PruneFileVersions unlimited = %+v, %v", removed, err)
	}

	mustSave(t, s, "docs", "other.bin", strings.Repeat("o", 100))
	okey := ports.FileHistoryKey{SpaceID: "sp1", FileID: "f2"}
	if _, _, err := s.ReplaceFile("docs", "other.bin", strings.NewReader("new"), 0, okey); err != nil {
		t.Fatal(err)
	}
	// f1 holds 11 + 2 bytes and f2 holds 100. A budget of 100 drops the
	// oldest versions of the space until the total fits.
	removed, err = s.PruneFileVersions("sp1", "f1", -1, 100)
	if err != nil {
		t.Fatalf("PruneFileVersions budget: %v", err)
	}
	var ids []string
	for _, v := range removed {
		ids = append(ids, v.FileID+"/"+v.ID)
	}
	if len(removed) != 2 || removed[0].FileID != "f1" || removed[1].FileID != "f1" {
		t.Fatalf("budget pruning removed %v", ids)
	}
	if list, _ := s.ListFileVersions("sp1", "f2"); len(list) != 1 {
		t.Fatalf("f2 versions = %d", len(list))
	}
	if err := s.DeleteFileHistory("sp1", "f2"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListFileVersions("sp1", "f2"); len(list) != 0 {
		t.Fatalf("versions after delete = %d", len(list))
	}

	errTests := []struct {
		name string
		rel  string
		key  ports.FileHistoryKey
		body string
		max  int64
		err  error
	}{
		{"missing", "nope.txt", key, "x", 0, core.ErrNotFound},
		{"bad key", "doc.txt", ports.FileHistoryKey{SpaceID: "../x", FileID: "f"}, "x", 0, core.ErrInvalid},
		{"empty file id", "doc.txt", ports.FileHistoryKey{SpaceID: "sp"}, "x", 0, core.ErrInvalid},
		{"too large", "doc.txt", key, "123456", 5, core.ErrTooLarge},
	}
	for _, tt := range errTests {
		if _, _, err := s.ReplaceFile("docs", tt.rel, strings.NewReader(tt.body), tt.max, tt.key); !errors.Is(err, tt.err) {
			t.Errorf("ReplaceFile %s = %v, want %v", tt.name, err, tt.err)
		}
	}
	mustMkdir(t, s, "docs", "folder")
	if _, _, err := s.ReplaceFile("docs", "folder", strings.NewReader("x"), 0, key); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("ReplaceFile folder = %v", err)
	}
	if current, _ := os.ReadFile(filepath.Join(s.SpacesDir(), "docs", "doc.txt")); string(current) != "v1" {
		t.Errorf("failed replace changed content to %q", current)
	}
	if _, _, err := s.OpenFileVersion("sp1", "f1", "123"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("OpenFileVersion missing = %v", err)
	}
	if _, _, err := s.OpenFileVersion("sp1", "f1", "../1"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("OpenFileVersion invalid = %v", err)
	}
	assertNoTemp(t, s.DataDir())
}

func TestReplaceFileCopiesWhenLinkFails(t *testing.T) {
	setLimit(t, &linkFile, func(string, string) error { return errors.New("links not supported") })
	s := newTestStore(t)
	long := strings.Repeat("ö", 120) + ".txt"
	mustSave(t, s, "docs", long, "old")
	key := ports.FileHistoryKey{SpaceID: "sp", FileID: "f"}
	_, v, err := s.ReplaceFile("docs", long, strings.NewReader("new"), 0, key)
	if err != nil {
		t.Fatalf("ReplaceFile: %v", err)
	}
	if v.Name != long {
		t.Fatalf("version name = %q", v.Name)
	}
	rc, _, err := s.OpenFileVersion("sp", "f", v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if body := readAllClose(t, rc); body != "old" {
		t.Fatalf("copied version = %q", body)
	}
	list, err := s.ListFileVersions("sp", "f")
	if err != nil || len(list) != 1 || list[0].Name != long {
		t.Fatalf("ListFileVersions = %+v, %v", list, err)
	}
}
