package extract

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"

	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// MaxPDFPages is the default cap on pages read from one PDF.
	MaxPDFPages = 200
	// MaxPDFTextBytes is the default cap on text extracted from one PDF.
	MaxPDFTextBytes = 1 << 20
	// maxPageTreeNodes caps the page tree walk, which also stops a cyclic tree.
	maxPageTreeNodes = 10000
	// maxPageTreeDepth caps how deep the page tree walk descends.
	maxPageTreeDepth = 32
)

// IsPDF reports whether data starts with a PDF header within its first 1024
// bytes, where readers accept it.
func IsPDF(data []byte) bool {
	head := data[:min(len(data), 1024)]
	return bytes.Contains(head, []byte("%PDF-"))
}

// pdfResult carries the outcome of an extraction goroutine.
type pdfResult struct {
	text      string
	truncated bool
	err       error
}

// PDFText extracts the text of a PDF in page order. It reads at most maxPages
// pages and maxBytes bytes of text, and reports truncated when either cap cut
// the text short. A value of 0 or less selects MaxPDFPages or MaxPDFTextBytes.
// Within a page the text follows the content stream order, which is the
// reading order for most generated documents, with line breaks where the
// baseline moves and spaces where glyphs leave a gap. The PDF library panics
// on some malformed input, and PDFText recovers and returns core.ErrInvalid.
// The work runs on its own goroutine, so a canceled ctx returns at once.
func PDFText(ctx context.Context, data []byte, maxPages, maxBytes int) (string, bool, error) {
	if maxPages <= 0 {
		maxPages = MaxPDFPages
	}
	if maxBytes <= 0 {
		maxBytes = MaxPDFTextBytes
	}
	if !IsPDF(data) {
		return "", false, fmt.Errorf("extract: not a PDF file: %w", core.ErrUnsupported)
	}
	done := make(chan pdfResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- pdfResult{err: fmt.Errorf("extract: malformed PDF: %v: %w", r, core.ErrInvalid)}
			}
		}()
		text, truncated, err := pdfText(ctx, data, maxPages, maxBytes)
		done <- pdfResult{text: text, truncated: truncated, err: err}
	}()
	select {
	case r := <-done:
		return r.text, r.truncated, r.err
	case <-ctx.Done():
		return "", false, fmt.Errorf("extract: PDF text: %w", ctx.Err())
	}
}

// pdfText walks the page tree and collects the text of each page.
func pdfText(ctx context.Context, data []byte, maxPages, maxBytes int) (string, bool, error) {
	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", false, fmt.Errorf("extract: open PDF: %w: %w", core.ErrInvalid, err)
	}
	var out strings.Builder
	pages, nodes := 0, 0
	truncated := false

	var walk func(v pdf.Value, depth int) bool
	walk = func(v pdf.Value, depth int) bool {
		if ctx.Err() != nil {
			return false
		}
		nodes++
		if nodes > maxPageTreeNodes || depth > maxPageTreeDepth {
			truncated = true
			return false
		}
		kind := v.Key("Type").Name()
		kids := v.Key("Kids")
		switch {
		case kind == "Pages" || (kind == "" && kids.Kind() == pdf.Array):
			for i := 0; i < kids.Len(); i++ {
				if !walk(kids.Index(i), depth+1) {
					return false
				}
			}
			return true
		case kind == "Page" || (kind == "" && v.Key("Contents").Kind() != pdf.Null):
			if pages >= maxPages {
				truncated = true
				return false
			}
			pages++
			text := strings.TrimSpace(pageText(pdf.Page{V: v}))
			if text == "" {
				return true
			}
			if out.Len() > 0 {
				out.WriteString("\n\n")
			}
			out.WriteString(text)
			if out.Len() > maxBytes {
				truncated = true
				return false
			}
			return true
		default:
			return true
		}
	}
	walk(reader.Trailer().Key("Root").Key("Pages"), 0)
	if err := ctx.Err(); err != nil {
		return "", false, fmt.Errorf("extract: PDF text: %w", err)
	}
	text := out.String()
	if len(text) > maxBytes {
		text = text[:maxBytes]
	}
	return strings.ToValidUTF8(trimPartialRuneString(text), "�"), truncated, nil
}

// pageText lays out the glyphs of one page in content stream order. A panic
// inside the library on one page yields no text for that page only.
func pageText(page pdf.Page) (text string) {
	defer func() {
		if recover() != nil {
			text = ""
		}
	}()
	if page.V.IsNull() || page.V.Key("Contents").Kind() == pdf.Null {
		return ""
	}
	glyphs := page.Content().Text
	var b strings.Builder
	var prev *pdf.Text
	for i := range glyphs {
		g := &glyphs[i]
		if g.S == "" {
			continue
		}
		if prev != nil {
			size := math.Max(math.Abs(prev.FontSize), 1)
			switch {
			case math.Abs(g.Y-prev.Y) > size*0.5:
				b.WriteByte('\n')
			case g.X-(prev.X+prev.W) > size*0.2 && !strings.HasSuffix(prev.S, " ") && !strings.HasPrefix(g.S, " "):
				b.WriteByte(' ')
			}
		}
		b.WriteString(g.S)
		prev = g
	}
	return b.String()
}

// trimPartialRuneString drops an incomplete UTF-8 sequence from the end of s.
func trimPartialRuneString(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return string(trimPartialRune([]byte(s)))
}
