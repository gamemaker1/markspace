package store

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	_ "image/jpeg"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// hugePNGHeader returns a valid PNG whose header declares w by h pixels while
// the file stays tiny.
func hugePNGHeader(t *testing.T, w, h uint32) []byte {
	t.Helper()
	data := pngBytes(t, 1, 1, false)
	binary.BigEndian.PutUint32(data[16:20], w)
	binary.BigEndian.PutUint32(data[20:24], h)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	return data
}

const shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestThumbnail(t *testing.T) {
	s := newTestStore(t)
	mustSave(t, s, "docs", "wide.png", string(pngBytes(t, 600, 300, false)))
	mustSave(t, s, "docs", "alpha.png", string(pngBytes(t, 40, 20, true)))
	mustSave(t, s, "docs", "notes.txt", "hello")

	p, mt, err := s.Thumbnail("docs", "wide.png", "file1", shaA, 256)
	if err != nil || mt != "image/jpeg" || !strings.HasSuffix(p, "/thumbs/file1/aaaaaaaaaaaaaaaa-256.jpg") {
		t.Fatalf("Thumbnail = %q, %q, %v", p, mt, err)
	}
	cfg := decodeFileConfig(t, p)
	if cfg.Width != 256 || cfg.Height != 128 {
		t.Fatalf("thumbnail size = %dx%d", cfg.Width, cfg.Height)
	}
	p2, mt2, err := s.Thumbnail("docs", "alpha.png", "file2", shaA, 128)
	if err != nil || mt2 != "image/png" || !strings.HasSuffix(p2, ".png") {
		t.Fatalf("alpha Thumbnail = %q, %q, %v", p2, mt2, err)
	}
	if cfg := decodeFileConfig(t, p2); cfg.Width != 40 || cfg.Height != 20 {
		t.Fatalf("small image was resized to %dx%d", cfg.Width, cfg.Height)
	}

	errTests := []struct {
		name, rel, id, sha string
		width              int
		err                error
	}{
		{"width", "wide.png", "file1", shaA, 100, core.ErrInvalid},
		{"short hash", "wide.png", "file1", "abc", 256, core.ErrInvalid},
		{"upper hash", "wide.png", "file1", strings.ToUpper(shaA), 256, core.ErrInvalid},
		{"bad id", "wide.png", "../x", shaA, 256, core.ErrInvalid},
		{"text", "notes.txt", "file3", shaA, 256, core.ErrUnsupported},
		{"missing", "gone.png", "file4", shaA, 256, core.ErrNotFound},
	}
	for _, tt := range errTests {
		if _, _, err := s.Thumbnail("docs", tt.rel, tt.id, tt.sha, tt.width); !errors.Is(err, tt.err) {
			t.Errorf("Thumbnail %s = %v, want %v", tt.name, err, tt.err)
		}
	}
	if err := s.DeleteThumbnails("file1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("thumbnail kept after delete: %v", err)
	}
	if err := s.DeleteThumbnails(".."); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("DeleteThumbnails(..) = %v", err)
	}
}

// decodeFileConfig reads the dimensions of an image file.
func decodeFileConfig(t *testing.T, full string) image.Config {
	t.Helper()
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode %s: %v", full, err)
	}
	return cfg
}

func TestThumbnailRefusesDecompressionBombs(t *testing.T) {
	s := newTestStore(t)
	bomb := hugePNGHeader(t, 50000, 50000)
	if len(bomb) > 200 {
		t.Fatalf("bomb fixture is %d bytes", len(bomb))
	}
	mustSave(t, s, "docs", "bomb.png", string(bomb))
	start := time.Now()
	if _, _, err := s.Thumbnail("docs", "bomb.png", "bomb", shaA, 256); !errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("bomb Thumbnail = %v, want ErrTooLarge", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("bomb check took %v, so it decoded pixels", time.Since(start))
	}

	setLimit(t, &maxThumbPixels, 100)
	mustSave(t, s, "docs", "eleven.png", string(pngBytes(t, 11, 10, false)))
	if _, _, err := s.Thumbnail("docs", "eleven.png", "eleven", shaA, 128); !errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("pixel limit = %v, want ErrTooLarge", err)
	}
	setLimit(t, &maxThumbPixels, 1_000_000)
	setLimit(t, &maxThumbSourceBytes, 50)
	if _, _, err := s.Thumbnail("docs", "eleven.png", "eleven", shaA, 128); !errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("byte limit = %v, want ErrTooLarge", err)
	}
	if s.thumbGenerated.Load() != 0 {
		t.Fatalf("a refused source produced %d thumbnails", s.thumbGenerated.Load())
	}
}

func TestThumbnailSingleflightAndCacheKey(t *testing.T) {
	s := newTestStore(t)
	mustSave(t, s, "docs", "pic.png", string(pngBytes(t, 300, 200, false)))
	s.thumbGenHook = func() { time.Sleep(50 * time.Millisecond) }
	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	paths := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			paths[i], _, errs[i] = s.Thumbnail("docs", "pic.png", "pic", shaA, 512)
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil || paths[i] != paths[0] {
			t.Fatalf("request %d = %q, %v", i, paths[i], errs[i])
		}
	}
	if got := s.thumbGenerated.Load(); got != 1 {
		t.Fatalf("%d concurrent requests generated %d thumbnails, want 1", n, got)
	}
	again, _, err := s.Thumbnail("docs", "pic.png", "pic", shaA, 512)
	if err != nil || again != paths[0] || s.thumbGenerated.Load() != 1 {
		t.Fatalf("cached request = %q, %v, generated %d", again, err, s.thumbGenerated.Load())
	}
	replaced, _, err := s.Thumbnail("docs", "pic.png", "pic", shaB, 512)
	if err != nil || replaced == paths[0] || s.thumbGenerated.Load() != 2 {
		t.Fatalf("new hash = %q, %v, generated %d", replaced, err, s.thumbGenerated.Load())
	}
	other, _, err := s.Thumbnail("docs", "pic.png", "pic", shaB, 128)
	if err != nil || other == replaced || s.thumbGenerated.Load() != 3 {
		t.Fatalf("new width = %q, %v, generated %d", other, err, s.thumbGenerated.Load())
	}
}
