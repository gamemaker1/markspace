package extract

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestThumbSize(t *testing.T) {
	tests := []struct {
		w, h, width, wantW, wantH int
	}{
		{100, 50, 256, 100, 50},
		{1000, 500, 256, 256, 128},
		{500, 1000, 256, 256, 512},
		{100, 5000, 256, 20, 1024},
		{3000, 1, 128, 128, 1},
		{256, 1024, 256, 256, 1024},
	}
	for _, tt := range tests {
		w, h := thumbSize(tt.w, tt.h, tt.width)
		if w != tt.wantW || h != tt.wantH {
			t.Errorf("thumbSize(%d, %d, %d) = %dx%d, want %dx%d", tt.w, tt.h, tt.width, w, h, tt.wantW, tt.wantH)
		}
	}
}

func encodeTestImage(t *testing.T, w, h int, alpha bool, asGIF bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 200
		if i%4 == 3 {
			img.Pix[i] = 255
		}
	}
	if alpha {
		img.Set(1, 1, color.NRGBA{})
	}
	var buf bytes.Buffer
	var err error
	if asGIF {
		err = gif.Encode(&buf, img, nil)
	} else {
		err = png.Encode(&buf, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestThumbnail(t *testing.T) {
	img, err := Thumbnail(encodeTestImage(t, 800, 400, false, false), 128, MaxImagePixels)
	if err != nil || img.MediaType != TypeJPEG || img.Width != 128 || img.Height != 64 {
		t.Fatalf("opaque thumbnail = %+v, %v", img, err)
	}
	img, err = Thumbnail(encodeTestImage(t, 800, 400, true, false), 512, MaxImagePixels)
	if err != nil || img.MediaType != TypePNG || img.Width != 512 {
		t.Fatalf("alpha thumbnail = %+v, %v", img, err)
	}
	img, err = Thumbnail(encodeTestImage(t, 64, 64, false, true), 128, MaxImagePixels)
	if err != nil || img.Width != 64 {
		t.Fatalf("gif thumbnail = %+v, %v", img, err)
	}
	if _, err := Thumbnail([]byte("not an image"), 128, MaxImagePixels); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("text = %v", err)
	}
	if _, err := Thumbnail(encodeTestImage(t, 20, 20, false, false), 128, 100); !errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("pixel limit = %v", err)
	}
	if _, err := Thumbnail(encodeTestImage(t, 20, 20, false, false), 0, 100); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("zero width = %v", err)
	}
	broken := encodeTestImage(t, 20, 20, false, false)[:40]
	if _, err := Thumbnail(broken, 128, MaxImagePixels); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("truncated = %v", err)
	}
}
