package extract

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/draw"

	"github.com/gamemaker1/markspace/internal/core"
)

// ThumbJPEGQuality is the quality of an opaque thumbnail.
const ThumbJPEGQuality = 80

// thumbTallFactor caps the height of a thumbnail at this many times its
// width, so a very tall image does not make a huge thumbnail.
const thumbTallFactor = 4

// ImageConfig reads the media type and dimensions of a PNG, JPEG, GIF or
// WebP image from its header without decoding pixels. head must hold at least
// the first 12 bytes. It wraps core.ErrUnsupported for other formats and
// core.ErrInvalid for a corrupt header.
func ImageConfig(head []byte, r io.Reader) (string, image.Config, error) {
	mediaType := ImageType(head)
	if mediaType == "" {
		return "", image.Config{}, fmt.Errorf("extract: not a PNG, JPEG, GIF or WebP image: %w", core.ErrUnsupported)
	}
	var cfg image.Config
	var err error
	switch mediaType {
	case TypePNG:
		cfg, err = png.DecodeConfig(r)
	case TypeJPEG:
		cfg, err = jpeg.DecodeConfig(r)
	default:
		var data []byte
		data, err = io.ReadAll(io.LimitReader(r, 1<<20))
		if err == nil {
			cfg, err = decodeConfig(mediaType, data)
		}
	}
	if err != nil {
		return mediaType, image.Config{}, fmt.Errorf("extract: read %s header: %w: %w", mediaType, core.ErrInvalid, err)
	}
	return mediaType, cfg, nil
}

// Thumbnail decodes a PNG, JPEG, GIF or WebP image and scales it to fit a box
// width pixels wide and four times as tall. A smaller image is not enlarged.
// The header is checked against maxPixels before any pixel is decoded, so a
// small file that declares huge dimensions is refused with core.ErrTooLarge.
// The result is JPEG at quality 80 when every pixel is opaque, and PNG
// otherwise. A GIF uses its first frame.
func Thumbnail(data []byte, width int, maxPixels int64) (*Image, error) {
	if width <= 0 {
		return nil, fmt.Errorf("extract: thumbnail width %d: %w", width, core.ErrInvalid)
	}
	mediaType, cfg, err := ImageConfig(data, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("extract: %s has no pixels: %w", mediaType, core.ErrInvalid)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, fmt.Errorf("extract: %s is %dx%d pixels: %w", mediaType, cfg.Width, cfg.Height, core.ErrTooLarge)
	}
	img, err := decode(mediaType, data)
	if err != nil {
		return nil, fmt.Errorf("extract: decode %s: %w: %w", mediaType, core.ErrInvalid, err)
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 || int64(w)*int64(h) > maxPixels {
		return nil, fmt.Errorf("extract: %s decoded to %dx%d pixels: %w", mediaType, w, h, core.ErrTooLarge)
	}
	newW, newH := thumbSize(w, h, width)
	opaque := isOpaque(img)
	target := image.Rect(0, 0, newW, newH)
	var dst draw.Image
	if opaque {
		dst = image.NewRGBA(target)
	} else {
		dst = image.NewNRGBA(target)
	}
	if newW == w && newH == h {
		draw.Draw(dst, target, img, bounds.Min, draw.Src)
	} else {
		draw.CatmullRom.Scale(dst, target, img, bounds, draw.Src, nil)
	}
	var buf bytes.Buffer
	outType := TypeJPEG
	if opaque {
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: ThumbJPEGQuality})
	} else {
		outType = TypePNG
		err = png.Encode(&buf, dst)
	}
	if err != nil {
		return nil, fmt.Errorf("extract: encode %s thumbnail: %w", outType, err)
	}
	return &Image{MediaType: outType, Data: buf.Bytes(), Width: newW, Height: newH}, nil
}

// thumbSize fits w by h inside a box width wide and thumbTallFactor times as
// tall, keeping the aspect ratio and never enlarging.
func thumbSize(w, h, width int) (int, int) {
	boxH := width * thumbTallFactor
	if w <= width && h <= boxH {
		return w, h
	}
	// Scale by the tighter of the two limits.
	if int64(w)*int64(boxH) >= int64(h)*int64(width) {
		nh := int((int64(h)*int64(width) + int64(w)/2) / int64(w))
		return width, max(nh, 1)
	}
	nw := int((int64(w)*int64(boxH) + int64(h)/2) / int64(h))
	return max(nw, 1), boxH
}
