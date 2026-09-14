package extract

import (
	"errors"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestIsText(t *testing.T) {
	tests := []struct {
		mediaType, name string
		want            bool
	}{
		{"text/plain; charset=utf-8", "notes.txt", true},
		{"text/csv", "data.bin", true},
		{"application/json", "", true},
		{"application/ld+json", "", true},
		{"application/x-yaml", "", true},
		{"application/octet-stream", "config.YML", true},
		{"application/octet-stream", "readme.md", true},
		{"", "table.csv", true},
		{"application/pdf", "doc.pdf", false},
		{"image/png", "photo.txt.png", false},
		{"application/octet-stream", "binary", false},
	}
	for _, tc := range tests {
		if got := IsText(tc.mediaType, tc.name); got != tc.want {
			t.Errorf("IsText(%q, %q) = %v, want %v", tc.mediaType, tc.name, got, tc.want)
		}
	}
}

func TestDecodeText(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
		err  error
	}{
		{"plain", []byte("héllo\nworld"), "héllo\nworld", nil},
		{"byte order mark", []byte("\xEF\xBB\xBFtext"), "text", nil},
		{"cut inside a rune", []byte("abc\xC3"), "abc", nil},
		{"cut inside a four byte rune", []byte("ok\xF0\x9F\x98"), "ok", nil},
		{"nul byte", []byte("a\x00b"), "", core.ErrUnsupported},
		{"invalid in the middle", []byte("a\xFFb"), "", core.ErrUnsupported},
		{"utf-16", []byte("\xFF\xFEh\x00i\x00"), "", core.ErrUnsupported},
	}
	for _, tc := range tests {
		got, err := DecodeText(tc.in)
		if tc.err != nil {
			if !errors.Is(err, tc.err) {
				t.Errorf("%s: err = %v, want %v", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: DecodeText = %q, %v, want %q", tc.name, got, err, tc.want)
		}
	}
}
