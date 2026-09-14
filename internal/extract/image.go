// Package extract turns attachment bytes into model input. It normalizes
// images for vision models, decodes text files and extracts the text of PDF
// documents.
package extract

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"

	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// MaxImageSide is the longest side, in pixels, of an image sent to a model.
	// A larger image is downscaled.
	MaxImageSide = 1568
	// MaxImageBytes caps the encoded size of one image after scaling.
	MaxImageBytes = 5 << 20
	// JPEGQuality is the quality of a re-encoded opaque image.
	JPEGQuality = 85
	// MaxImagePixels caps the decoded size of an input image, so a small file
	// that declares huge dimensions cannot exhaust memory.
	MaxImagePixels = 40_000_000
)

// Media types of the image formats the package decodes.
const (
	TypePNG  = "image/png"
	TypeJPEG = "image/jpeg"
	TypeGIF  = "image/gif"
	TypeWebP = "image/webp"
)

// Image is a normalized image ready for a model request.
type Image struct {
	// MediaType is image/png, image/jpeg, image/gif or image/webp.
	MediaType string
	// Data is the encoded image.
	Data []byte
	// Width and Height are the pixel dimensions of Data.
	Width  int
	Height int
}

// ImageType detects PNG, JPEG, GIF and WebP by their leading bytes and returns
// the media type, or the empty string for anything else.
func ImageType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return TypePNG
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return TypeJPEG
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return TypeGIF
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return TypeWebP
	default:
		return ""
	}
}

// decodeConfig reads the dimensions from the image header.
func decodeConfig(mediaType string, data []byte) (image.Config, error) {
	r := bytes.NewReader(data)
	switch mediaType {
	case TypePNG:
		return png.DecodeConfig(r)
	case TypeJPEG:
		return jpeg.DecodeConfig(r)
	case TypeGIF:
		return gif.DecodeConfig(r)
	case TypeWebP:
		return webp.DecodeConfig(r)
	default:
		return image.Config{}, fmt.Errorf("extract: no decoder for %q: %w", mediaType, core.ErrUnsupported)
	}
}

// decode decodes the whole image. For a GIF it returns the first frame.
func decode(mediaType string, data []byte) (image.Image, error) {
	r := bytes.NewReader(data)
	switch mediaType {
	case TypePNG:
		return png.Decode(r)
	case TypeJPEG:
		return jpeg.Decode(r)
	case TypeGIF:
		return gif.Decode(r)
	case TypeWebP:
		return webp.Decode(r)
	default:
		return nil, fmt.Errorf("extract: no decoder for %q: %w", mediaType, core.ErrUnsupported)
	}
}

// NormalizeImage validates an image and fits it to the model limits. The
// format is detected from the content, not from a file name. An image whose
// longest side is at most MaxImageSide keeps its original bytes, except a GIF,
// which is re-encoded from its first frame. A larger image is downscaled with
// Catmull-Rom interpolation and written as PNG when any pixel is transparent,
// or as JPEG at quality 85 otherwise. The result must be at most
// MaxImageBytes. Errors wrap core.ErrUnsupported for an unknown format,
// core.ErrInvalid for a corrupt file and core.ErrTooLarge for an image over
// the limits.
func NormalizeImage(data []byte) (*Image, error) {
	mediaType := ImageType(data)
	if mediaType == "" {
		return nil, fmt.Errorf("extract: not a PNG, JPEG, GIF or WebP image: %w", core.ErrUnsupported)
	}
	cfg, err := decodeConfig(mediaType, data)
	if err != nil {
		return nil, fmt.Errorf("extract: read %s header: %w: %w", mediaType, core.ErrInvalid, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("extract: %s has no pixels: %w", mediaType, core.ErrInvalid)
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
		return nil, fmt.Errorf("extract: %s is %dx%d pixels: %w", mediaType, cfg.Width, cfg.Height, core.ErrTooLarge)
	}
	img, err := decode(mediaType, data)
	if err != nil {
		return nil, fmt.Errorf("extract: decode %s: %w: %w", mediaType, core.ErrInvalid, err)
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("extract: %s has no pixels: %w", mediaType, core.ErrInvalid)
	}
	if int64(width)*int64(height) > MaxImagePixels {
		return nil, fmt.Errorf("extract: %s is %dx%d pixels: %w", mediaType, width, height, core.ErrTooLarge)
	}
	longest := max(width, height)
	if longest <= MaxImageSide && mediaType != TypeGIF && len(data) <= MaxImageBytes {
		return &Image{MediaType: mediaType, Data: data, Width: width, Height: height}, nil
	}

	newWidth, newHeight := width, height
	if longest > MaxImageSide {
		newWidth, newHeight = scaledSize(width, height, MaxImageSide)
	}
	opaque := isOpaque(img)
	target := image.Rect(0, 0, newWidth, newHeight)
	var dst draw.Image
	if opaque {
		dst = image.NewRGBA(target)
	} else {
		dst = image.NewNRGBA(target)
	}
	if newWidth == width && newHeight == height {
		draw.Draw(dst, target, img, bounds.Min, draw.Src)
	} else {
		draw.CatmullRom.Scale(dst, target, img, bounds, draw.Src, nil)
	}

	var buf bytes.Buffer
	outType := TypeJPEG
	if opaque {
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: JPEGQuality})
	} else {
		outType = TypePNG
		err = png.Encode(&buf, dst)
	}
	if err != nil {
		return nil, fmt.Errorf("extract: encode %s: %w", outType, err)
	}
	if buf.Len() > MaxImageBytes {
		return nil, fmt.Errorf("extract: %s is %d bytes after scaling: %w", outType, buf.Len(), core.ErrTooLarge)
	}
	return &Image{MediaType: outType, Data: buf.Bytes(), Width: newWidth, Height: newHeight}, nil
}

// scaledSize fits width and height inside a square of side limit, keeping the
// aspect ratio and at least one pixel on each side.
func scaledSize(width, height, limit int) (int, int) {
	if width >= height {
		h := int((int64(height)*int64(limit) + int64(width)/2) / int64(width))
		return limit, max(h, 1)
	}
	w := int((int64(width)*int64(limit) + int64(height)/2) / int64(height))
	return max(w, 1), limit
}

// isOpaque reports whether no pixel of img is transparent. The standard image
// types answer through their Opaque method, and any other type is scanned.
func isOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
				return false
			}
		}
	}
	return true
}
