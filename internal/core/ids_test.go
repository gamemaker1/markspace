package core

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func TestNewIDShapeAndOrder(t *testing.T) {
	const n = 200
	ids := make([]string, 0, n)
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		id := NewID()
		if len(id) != 26 {
			t.Fatalf("id %q has length %d, want 26", id, len(id))
		}
		if strings.HasPrefix(id, "0000") {
			t.Fatalf("id %q starts with zeroed timestamp bytes", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
		ids = append(ids, id)
		if i%50 == 0 {
			time.Sleep(2 * time.Millisecond)
		}
	}
	// Ids sort by creation time. Compare the 9-character prefix, which holds
	// only timestamp bits. The 10th character carries 2 random bits, so two ids
	// from one millisecond may order either way there.
	prefixes := make([]string, len(ids))
	for i, id := range ids {
		prefixes[i] = id[:9]
	}
	if !sort.StringsAreSorted(prefixes) {
		t.Fatal("id timestamp prefixes are not in creation order")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Getting Started":    "getting-started",
		"  --Hello, World!-": "hello-world",
		"Café déjà vu":       "café-déjà-vu",
		"snake_case title":   "snake_case-title",
		"":                   "untitled",
		"!!!":                "untitled",
		"CON":                "con-note",
		"Index":              "index-note",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Slugify(strings.Repeat("a", 200)); len([]rune(got)) != 80 {
		t.Errorf("long slug has %d runes, want 80", len([]rune(got)))
	}
}
