package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// buildPDF writes a minimal PDF with one Helvetica text page per entry. Each
// line of an entry is drawn 16 points below the previous one.
func buildPDF(pages ...string) []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>"}
	var kids strings.Builder
	for i := range pages {
		fmt.Fprintf(&kids, "%d 0 R ", 4+2*i)
	}
	objects = append(objects,
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), len(pages)),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for i, page := range pages {
		var content strings.Builder
		content.WriteString("BT /F1 12 Tf 72 720 Td ")
		for j, line := range strings.Split(page, "\n") {
			if j > 0 {
				content.WriteString("0 -16 Td ")
			}
			line = strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(line)
			fmt.Fprintf(&content, "(%s) Tj ", line)
		}
		content.WriteString("ET")
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", content.Len(), content.String()))
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}

func TestPDFTextReadsPagesInOrder(t *testing.T) {
	data := buildPDF("Hello PDF\nSecond line", "Page two text")
	text, truncated, err := PDFText(context.Background(), data, 0, 0)
	if err != nil {
		t.Fatalf("PDFText: %v", err)
	}
	if truncated {
		t.Fatal("a two page PDF was reported as truncated")
	}
	want := "Hello PDF\nSecond line\n\nPage two text"
	if text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
}

func TestPDFTextCaps(t *testing.T) {
	data := buildPDF("first page", "second page", "third page")
	text, truncated, err := PDFText(context.Background(), data, 2, 0)
	if err != nil || !truncated || strings.Contains(text, "third") || !strings.Contains(text, "second page") {
		t.Fatalf("page cap: %q truncated=%v err=%v", text, truncated, err)
	}
	text, truncated, err = PDFText(context.Background(), data, 0, 12)
	if err != nil || !truncated || len(text) > 12 {
		t.Fatalf("byte cap: %q truncated=%v err=%v", text, truncated, err)
	}
}

func TestPDFTextRejects(t *testing.T) {
	if _, _, err := PDFText(context.Background(), []byte("plain text"), 0, 0); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("not a PDF: err = %v", err)
	}
	if _, _, err := PDFText(context.Background(), []byte("%PDF-1.4\ngarbage without xref"), 0, 0); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("malformed PDF: err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := PDFText(ctx, buildPDF("x"), 0, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: err = %v", err)
	}
}

func TestPDFTextSurvivesCorruptedObjects(t *testing.T) {
	data := buildPDF("some words here")
	for i := 9; i < len(data); i += 7 {
		corrupt := append([]byte(nil), data...)
		corrupt[i] ^= 0x5a
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, err := PDFText(ctx, corrupt, 0, 0)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("flipping byte %d hung the extractor", i)
		}
	}
}
