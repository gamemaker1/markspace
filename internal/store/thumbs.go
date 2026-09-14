package store

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/extract"
)

// Thumbnail limits. They are variables so tests can lower them.
var (
	// maxThumbSourceBytes caps the size of an image that gets a thumbnail.
	maxThumbSourceBytes int64 = 64 << 20
	// maxThumbPixels caps the declared dimensions of such an image, checked
	// from the header before decoding.
	maxThumbPixels int64 = 100_000_000
)

// thumbConcurrency bounds how many thumbnails decode at once, because each
// decode can hold hundreds of megabytes.
const thumbConcurrency = 2

// thumbWidths are the widths a thumbnail may have.
var thumbWidths = map[int]bool{128: true, 256: true, 512: true, 1024: true}

// thumbCall is one thumbnail being made. Waiters block on done.
type thumbCall struct {
	done      chan struct{}
	path      string
	mediaType string
	err       error
}

// thumbFlights makes each thumbnail once when requests overlap.
type thumbFlights struct {
	mu    sync.Mutex
	calls map[string]*thumbCall
	sem   chan struct{}
}

func (f *thumbFlights) init() {
	f.calls = map[string]*thumbCall{}
	f.sem = make(chan struct{}, thumbConcurrency)
}

// thumbExts maps each cached thumbnail extension to its media type.
var thumbExts = []struct{ ext, mediaType string }{
	{".jpg", extract.TypeJPEG},
	{".png", extract.TypePNG},
}

// Thumbnail returns the path and media type of the thumbnail of the image at
// rel, width pixels wide at most, making and caching it under
// internal/thumbs/<fileID>/<sha256[:16]>-<width>.<ext> on first request. The
// hash is the caller's record of the current content, so a replaced file gets
// new thumbnails. It returns core.ErrInvalid for a width outside 128, 256,
// 512 and 1024, core.ErrUnsupported for a file that is not PNG, JPEG, GIF or
// WebP, and core.ErrTooLarge for a source over 64 MiB or 100 megapixels.
func (s *Store) Thumbnail(spaceSlug, rel, fileID, sha256 string, width int) (string, string, error) {
	if !thumbWidths[width] {
		return "", "", fmt.Errorf("thumbnail width %d: %w", width, core.ErrInvalid)
	}
	if err := validIDSegment(fileID); err != nil {
		return "", "", fmt.Errorf("file id: %w", err)
	}
	if len(sha256) < 16 || strings.Trim(sha256, "0123456789abcdef") != "" {
		return "", "", fmt.Errorf("thumbnail hash %q: %w", sha256, core.ErrInvalid)
	}
	dir := filepath.Join(s.thumbsDir, fileID)
	if !withinDir(s.thumbsDir, dir) || dir == s.thumbsDir {
		return "", "", fmt.Errorf("thumbnail dir for %q: %w", fileID, core.ErrInvalid)
	}
	stem := sha256[:16] + "-" + strconv.Itoa(width)
	if p, mt, ok := findThumb(dir, stem); ok {
		return p, mt, nil
	}
	key := fileID + "/" + stem
	f := &s.thumbs
	f.mu.Lock()
	if call, ok := f.calls[key]; ok {
		f.mu.Unlock()
		<-call.done
		return call.path, call.mediaType, call.err
	}
	call := &thumbCall{done: make(chan struct{})}
	f.calls[key] = call
	f.mu.Unlock()

	if p, mt, ok := findThumb(dir, stem); ok {
		call.path, call.mediaType = p, mt
	} else {
		call.path, call.mediaType, call.err = s.makeThumb(spaceSlug, rel, dir, stem, width)
	}
	f.mu.Lock()
	delete(f.calls, key)
	f.mu.Unlock()
	close(call.done)
	return call.path, call.mediaType, call.err
}

// findThumb looks for a cached thumbnail with either extension.
func findThumb(dir, stem string) (string, string, bool) {
	for _, te := range thumbExts {
		p := filepath.Join(dir, stem+te.ext)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p, te.mediaType, true
		}
	}
	return "", "", false
}

// makeThumb decodes the source, scales it and writes the cache file.
func (s *Store) makeThumb(spaceSlug, rel, dir, stem string, width int) (string, string, error) {
	s.thumbs.sem <- struct{}{}
	defer func() { <-s.thumbs.sem }()
	if hook := s.thumbGenHook; hook != nil {
		hook()
	}
	src, info, err := s.OpenFile(spaceSlug, rel)
	if err != nil {
		return "", "", err
	}
	defer src.Close()
	if info.Size() > maxThumbSourceBytes {
		return "", "", fmt.Errorf("thumbnail source %q is %d bytes: %w", rel, info.Size(), core.ErrTooLarge)
	}
	head := make([]byte, 64)
	n, _ := io.ReadFull(src, head)
	head = head[:n]
	if extract.ImageType(head) == "" {
		return "", "", fmt.Errorf("thumbnail of %q: %w", rel, core.ErrUnsupported)
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", "", fmt.Errorf("rewind %q: %w", rel, err)
	}
	// The header check runs before the whole file is read into memory.
	_, cfg, err := extract.ImageConfig(head, src)
	if err != nil {
		return "", "", err
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxThumbPixels {
		return "", "", fmt.Errorf("thumbnail source %q is %dx%d pixels: %w", rel, cfg.Width, cfg.Height, core.ErrTooLarge)
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", "", fmt.Errorf("rewind %q: %w", rel, err)
	}
	data, err := io.ReadAll(io.LimitReader(src, maxThumbSourceBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("read %q: %w", rel, err)
	}
	if int64(len(data)) > maxThumbSourceBytes {
		return "", "", fmt.Errorf("thumbnail source %q grew past %d bytes: %w", rel, maxThumbSourceBytes, core.ErrTooLarge)
	}
	img, err := extract.Thumbnail(data, width, maxThumbPixels)
	if err != nil {
		return "", "", err
	}
	ext := ".png"
	if img.MediaType == extract.TypeJPEG {
		ext = ".jpg"
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return "", "", fmt.Errorf("create thumbnail dir: %w", err)
	}
	out := filepath.Join(dir, stem+ext)
	if err := writeFileAtomic(out, img.Data); err != nil {
		return "", "", fmt.Errorf("write thumbnail: %w", err)
	}
	s.thumbGenerated.Add(1)
	return out, img.MediaType, nil
}

// DeleteThumbnails removes every cached thumbnail of a file.
func (s *Store) DeleteThumbnails(fileID string) error {
	if err := validIDSegment(fileID); err != nil {
		return fmt.Errorf("file id: %w", err)
	}
	dir := filepath.Join(s.thumbsDir, fileID)
	if !withinDir(s.thumbsDir, dir) || dir == s.thumbsDir {
		return fmt.Errorf("thumbnail dir for %q: %w", fileID, core.ErrInvalid)
	}
	if err := os.RemoveAll(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete thumbnails of %q: %w", fileID, err)
	}
	return nil
}
