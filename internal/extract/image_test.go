package extract

import (
	"bytes"
	"encoding/base64"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// tinyWebP is gopher-doc.1bpp.lossless.webp from the golang.org/x/image
// test data.
const tinyWebP = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="

// filled returns an opaque image of one color.
func filled(width, height int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

// encodePNG encodes img as PNG.
func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// encodeJPEG encodes img as JPEG.
func encodeJPEG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// twoFrameGIF returns a 20x20 GIF whose first frame is red and second blue.
func twoFrameGIF(t testing.TB) []byte {
	t.Helper()
	palette := color.Palette{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}}
	red := image.NewPaletted(image.Rect(0, 0, 20, 20), palette)
	blue := image.NewPaletted(image.Rect(0, 0, 20, 20), palette)
	for i := range blue.Pix {
		blue.Pix[i] = 1
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{red, blue}, Delay: []int{10, 10}}); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

func TestImageType(t *testing.T) {
	webp, _ := base64.StdEncoding.DecodeString(tinyWebP)
	small := filled(4, 4, color.White)
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"png", encodePNG(t, small), TypePNG},
		{"jpeg", encodeJPEG(t, small), TypeJPEG},
		{"gif", twoFrameGIF(t), TypeGIF},
		{"webp", webp, TypeWebP},
		{"text", []byte("hello"), ""},
		{"empty", nil, ""},
		{"riff without webp", []byte("RIFF\x00\x00\x00\x00WAVE"), ""},
	}
	for _, tc := range tests {
		if got := ImageType(tc.data); got != tc.want {
			t.Errorf("%s: ImageType = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeDownscalesOpaqueImageToJPEG(t *testing.T) {
	src := encodePNG(t, filled(3000, 2000, color.RGBA{10, 120, 200, 255}))
	img, err := NormalizeImage(src)
	if err != nil {
		t.Fatalf("NormalizeImage: %v", err)
	}
	if img.MediaType != TypeJPEG || img.Width != 1568 || img.Height != 1045 {
		t.Fatalf("got %s %dx%d", img.MediaType, img.Width, img.Height)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil || cfg.Width != 1568 || cfg.Height != 1045 {
		t.Fatalf("encoded jpeg %dx%d: %v", cfg.Width, cfg.Height, err)
	}
}

func TestNormalizeKeepsPNGForTransparency(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2000, 1000))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.NRGBA{0, 200, 0, 255}), image.Point{}, draw.Src)
	src.SetNRGBA(10, 10, color.NRGBA{0, 0, 0, 0})
	img, err := NormalizeImage(encodePNG(t, src))
	if err != nil {
		t.Fatalf("NormalizeImage: %v", err)
	}
	if img.MediaType != TypePNG || img.Width != 1568 || img.Height != 784 {
		t.Fatalf("got %s %dx%d", img.MediaType, img.Width, img.Height)
	}
	if ImageType(img.Data) != TypePNG {
		t.Fatal("data is not PNG")
	}
}

func TestNormalizeKeepsSmallImageBytes(t *testing.T) {
	for name, data := range map[string][]byte{
		"png":  encodePNG(t, filled(100, 50, color.Black)),
		"jpeg": encodeJPEG(t, filled(1568, 900, color.Black)),
	} {
		img, err := NormalizeImage(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(img.Data, data) || img.MediaType != ImageType(data) {
			t.Fatalf("%s: small image was re-encoded as %s", name, img.MediaType)
		}
	}
}

func TestNormalizeWebP(t *testing.T) {
	data, _ := base64.StdEncoding.DecodeString(tinyWebP)
	img, err := NormalizeImage(data)
	if err != nil {
		t.Fatalf("NormalizeImage: %v", err)
	}
	if img.MediaType != TypeWebP || img.Width <= 0 || !bytes.Equal(img.Data, data) {
		t.Fatalf("got %s %dx%d", img.MediaType, img.Width, img.Height)
	}
}

func TestNormalizeGIFUsesFirstFrame(t *testing.T) {
	img, err := NormalizeImage(twoFrameGIF(t))
	if err != nil {
		t.Fatalf("NormalizeImage: %v", err)
	}
	if img.MediaType != TypeJPEG || img.Width != 20 || img.Height != 20 {
		t.Fatalf("got %s %dx%d", img.MediaType, img.Width, img.Height)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(img.Data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	r, _, b, _ := decoded.At(10, 10).RGBA()
	if r < 0xc000 || b > 0x4000 {
		t.Fatalf("center pixel r=%x b=%x, want the red first frame", r, b)
	}
}

// pngWithSize returns a 1x1 PNG whose header claims width x height, with a
// valid header checksum.
func pngWithSize(t *testing.T, width, height uint32) []byte {
	t.Helper()
	data := encodePNG(t, filled(1, 1, color.White))
	// The IHDR chunk data starts at byte 16, after the signature, the length
	// and the chunk type, and its checksum follows the 13 data bytes.
	put := func(off int, v uint32) {
		data[off], data[off+1], data[off+2], data[off+3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	put(16, width)
	put(20, height)
	put(29, crc32.ChecksumIEEE(data[12:29]))
	return data
}

func TestNormalizeRejects(t *testing.T) {
	valid := encodePNG(t, filled(8, 8, color.White))
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"not an image", []byte("plain text"), core.ErrUnsupported},
		{"truncated png", valid[:40], core.ErrInvalid},
		{"huge dimensions", pngWithSize(t, 100000, 100000), core.ErrTooLarge},
		{"zero dimensions", pngWithSize(t, 0, 5), core.ErrInvalid},
	}
	for _, tc := range tests {
		if _, err := NormalizeImage(tc.data); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestScaledSize(t *testing.T) {
	tests := []struct{ w, h, wantW, wantH int }{
		{3000, 2000, 1568, 1045},
		{2000, 3000, 1045, 1568},
		{4000, 4000, 1568, 1568},
		{100000, 10, 1568, 1},
	}
	for _, tc := range tests {
		if w, h := scaledSize(tc.w, tc.h, MaxImageSide); w != tc.wantW || h != tc.wantH {
			t.Errorf("scaledSize(%d, %d) = %dx%d, want %dx%d", tc.w, tc.h, w, h, tc.wantW, tc.wantH)
		}
	}
}
