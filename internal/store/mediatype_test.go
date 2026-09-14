package store

import (
	"strings"
	"testing"
)

func TestDetectMediaType(t *testing.T) {
	tests := []struct {
		name   string
		head   []byte
		prefix string
	}{
		{"photo.png", nil, "image/png"},
		{"doc.pdf", nil, "application/pdf"},
		{"page.html", nil, "text/html"},
		{"blob.unknownext", []byte("plain text here"), "text/plain"},
		{"blob.unknownext", []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, "image/png"},
		{"noext", nil, defaultMediaType},
	}
	for _, tt := range tests {
		if got := DetectMediaType(tt.name, tt.head); !strings.HasPrefix(got, tt.prefix) {
			t.Errorf("DetectMediaType(%q) = %q, want prefix %q", tt.name, got, tt.prefix)
		}
	}
}
