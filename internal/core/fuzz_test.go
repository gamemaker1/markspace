package core

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// idAlphabet is the character set NewID draws from.
const idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// FuzzSlugify checks that Slugify returns a short, non-empty slug made only of
// letters, digits, '-' and '_', with no hyphen at either end, and that it is
// idempotent.
func FuzzSlugify(f *testing.F) {
	for _, seed := range []string{
		"", " ", "Getting started", "con", "CON", "..", "a--b", "-a-", "_x_",
		"İstanbul", "ǅemal", "Crème brûlée", "日本語のタイトル", "áb",
		strings.Repeat("a", 79) + "-b", strings.Repeat("ab ", 40), "\xff\xfe",
		"ß", "ﬁle", "Ⅻ roman", "x‍y", "١٢٣",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := Slugify(in)
		if out == "" {
			t.Fatalf("Slugify(%q) is empty", in)
		}
		if n := utf8.RuneCountInString(out); n > 80 {
			t.Fatalf("Slugify(%q) = %q has %d runes, want at most 80", in, out, n)
		}
		if !utf8.ValidString(out) {
			t.Fatalf("Slugify(%q) = %q is not valid UTF-8", in, out)
		}
		for _, r := range out {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
				t.Fatalf("Slugify(%q) = %q contains %q", in, out, r)
			}
		}
		if strings.HasPrefix(out, "-") || strings.HasSuffix(out, "-") {
			t.Fatalf("Slugify(%q) = %q has an outer hyphen", in, out)
		}
		if again := Slugify(out); again != out {
			t.Fatalf("Slugify is not idempotent: Slugify(%q) = %q, Slugify(%q) = %q", in, out, out, again)
		}
	})
}

// FuzzValidID checks that ValidID accepts exactly the 26-character strings
// over the id alphabet, and always accepts NewID.
func FuzzValidID(f *testing.F) {
	f.Add("")
	f.Add(NewID())
	f.Add(strings.ToUpper(NewID()))
	f.Add(strings.Repeat("i", 26))
	f.Add(strings.Repeat("0", 25))
	f.Add(strings.Repeat("0", 27))
	f.Add(strings.Repeat("é", 13))
	f.Fuzz(func(t *testing.T, in string) {
		if id := NewID(); !ValidID(id) {
			t.Fatalf("ValidID(NewID()) is false for %q", id)
		}
		want := len(in) == 26
		for i := 0; i < len(in) && want; i++ {
			if !strings.ContainsRune(idAlphabet, rune(in[i])) {
				want = false
			}
		}
		if got := ValidID(in); got != want {
			t.Fatalf("ValidID(%q) = %v, want %v", in, got, want)
		}
	})
}
