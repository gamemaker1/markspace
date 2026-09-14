package extract

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/color"
	"testing"
	"time"
	"unicode/utf8"
)

// FuzzNormalizeImage checks that arbitrary bytes never panic or hang the
// image normalizer, and that any accepted image fits the model limits.
func FuzzNormalizeImage(f *testing.F) {
	webp, _ := base64.StdEncoding.DecodeString(tinyWebP)
	f.Add(encodePNG(f, filled(4, 3, color.White)))
	f.Add(encodeJPEG(f, filled(4, 3, color.Black)))
	f.Add(twoFrameGIF(f))
	f.Add(webp)
	f.Add([]byte("\x89PNG\r\n\x1a\n"))
	f.Add([]byte("GIF89a\x01\x00\x01\x00"))
	f.Add([]byte("RIFF\x10\x00\x00\x00WEBPVP8L"))
	f.Add([]byte("\xFF\xD8\xFF\xE0"))
	f.Fuzz(func(t *testing.T, data []byte) {
		type outcome struct {
			img *Image
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			img, err := NormalizeImage(data)
			done <- outcome{img, err}
		}()
		select {
		case out := <-done:
			if out.err != nil {
				return
			}
			img := out.img
			if img.Width <= 0 || img.Height <= 0 || img.Width > MaxImageSide || img.Height > MaxImageSide {
				t.Fatalf("accepted image is %dx%d", img.Width, img.Height)
			}
			if len(img.Data) > MaxImageBytes || ImageType(img.Data) != img.MediaType {
				t.Fatalf("accepted %s image of %d bytes detects as %q", img.MediaType, len(img.Data), ImageType(img.Data))
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("NormalizeImage did not return within 20 s for %d bytes", len(data))
		}
	})
}

// FuzzPDFText checks that arbitrary bytes never panic or hang the PDF
// extractor, and that its text is valid UTF-8 within the byte cap.
func FuzzPDFText(f *testing.F) {
	f.Add(buildPDF("Hello PDF\nSecond line", "two"))
	f.Add([]byte("%PDF-1.4\n"))
	f.Add([]byte("%PDF-1.7\n1 0 obj << /Type /Pages /Kids [1 0 R] /Count 5 >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF"))
	f.Add(bytes.Repeat([]byte("%PDF-"), 20))
	f.Fuzz(func(t *testing.T, data []byte) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		start := time.Now()
		text, _, err := PDFText(ctx, data, 20, 4096)
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("PDFText returned after %s", elapsed)
		}
		if err != nil {
			return
		}
		if !utf8.ValidString(text) || len(text) > 4096 {
			t.Fatalf("text of %d bytes, valid UTF-8 %v", len(text), utf8.ValidString(text))
		}
	})
}
