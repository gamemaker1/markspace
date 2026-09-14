package store

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestValidName(t *testing.T) {
	long := strings.Repeat("a", 255)
	longRunes := strings.Repeat("ä", 127) // 254 bytes
	tests := []struct {
		name string
		ok   bool
	}{
		{"report.pdf", true},
		{"east wall.jpg", true},
		{"Übersicht 2026", true},
		{"日本語のノート.md", true},
		{"a..b", true},
		{"name.", true},
		{"~report", true},
		{"index.md", true},
		{long, true},
		{longRunes, true},
		{long + "a", false},
		{"", false},
		{".", false},
		{"..", false},
		{".hidden", false},
		{".trash", false},
		{"a/b", false},
		{`a\b`, false},
		{"a\x00b", false},
		{"a\nb", false},
		{"a\x7fb", false},
		{"a\u0085b", false},
		{" lead", false},
		{"trail ", false},
		{"\u00a0nbsp", false},
		{"bad\xffutf8", false},
		{"x.tmp-123", false},
		{"~$report.docx", false},
		{"Thumbs.db", false},
		{"desktop.ini", false},
		{".DS_Store", false},
	}
	for _, tt := range tests {
		err := ValidName(tt.name)
		if (err == nil) != tt.ok {
			t.Errorf("ValidName(%q) = %v, want ok=%v", tt.name, err, tt.ok)
		}
		if err != nil && !errors.Is(err, core.ErrInvalid) {
			t.Errorf("ValidName(%q) error %v does not wrap ErrInvalid", tt.name, err)
		}
	}
}

func TestValidEntryPath(t *testing.T) {
	tests := []struct {
		rel string
		ok  bool
	}{
		{"a", true},
		{"Projects/harbour/survey.md", true},
		{"a b/c d.pdf", true},
		{"", false},
		{"/a", false},
		{"a/", false},
		{"a//b", false},
		{"./a", false},
		{"a/./b", false},
		{"a/../b", false},
		{"..", false},
		{".trash/x/a.md", false},
		{"a/.git/config", false},
		{"a/Thumbs.db", false},
		{"a\\b", false},
		{strings.Repeat("a/", 1600) + "a", false},
	}
	for _, tt := range tests {
		if err := validEntryPath(tt.rel); (err == nil) != tt.ok {
			t.Errorf("validEntryPath(%q) = %v, want ok=%v", tt.rel, err, tt.ok)
		}
	}
}

func TestIgnoredName(t *testing.T) {
	for _, name := range []string{".x", ".tmp-1", "a.md.tmp-99", "~$a", ".DS_Store", "Thumbs.db", "desktop.ini"} {
		if !IgnoredName(name) {
			t.Errorf("IgnoredName(%q) = false", name)
		}
	}
	for _, name := range []string{"a", "tmp-1", "a.tmp", "~a", "thumbs.db.bak", "attachments"} {
		if IgnoredName(name) {
			t.Errorf("IgnoredName(%q) = true", name)
		}
	}
}

func TestCaseCollision(t *testing.T) {
	s := newTestStore(t)
	mustMkdir(t, s, "docs", "Projects")
	mustSave(t, s, "docs", "Projects/Report.PDF", "x")
	tests := []struct {
		parent, name, except string
		want                 string
		taken                bool
	}{
		{"", "projects", "", "Projects", true},
		{"", "PROJECTS", "", "Projects", true},
		{"", "Projects", "", "Projects", true},
		{"", "Projects", "Projects", "", false},
		{"", "other", "", "", false},
		{"Projects", "report.pdf", "", "Report.PDF", true},
		{"Projects", "report.pdf", "Projects/Report.PDF", "", false},
		{"Missing", "x", "", "", false},
		{"../x", "x", "", "", false},
	}
	for _, tt := range tests {
		got, taken := s.CaseCollision("docs", tt.parent, tt.name, tt.except)
		if got != tt.want || taken != tt.taken {
			t.Errorf("CaseCollision(%q, %q, %q) = %q, %v, want %q, %v", tt.parent, tt.name, tt.except, got, taken, tt.want, tt.taken)
		}
	}
}

func TestAllocName(t *testing.T) {
	s := newTestStore(t)
	mustSave(t, s, "docs", "photo.jpg", "x")
	mustSave(t, s, "docs", "PHOTO (2).JPG", "x")
	mustSave(t, s, "docs", "backup.tar.gz", "x")
	mustMkdir(t, s, "docs", "v1.2")
	tests := []struct {
		name, except string
		isDir        bool
		want         string
		err          error
	}{
		{"new.txt", "", false, "new.txt", nil},
		{"photo.jpg", "", false, "photo (3).jpg", nil},
		{"Photo.jpg", "photo.jpg", false, "Photo.jpg", nil},
		{"backup.tar.gz", "", false, "backup (2).tar.gz", nil},
		{"v1.2", "", true, "v1.2 (2)", nil},
		{"V1.2", "", false, "V1 (2).2", nil},
		{"README", "", false, "README", nil},
		{".bad", "", false, "", core.ErrInvalid},
	}
	for _, tt := range tests {
		got, err := s.AllocName("docs", "", tt.name, tt.except, tt.isDir)
		if !errors.Is(err, tt.err) || got != tt.want {
			t.Errorf("AllocName(%q) = %q, %v, want %q, %v", tt.name, got, err, tt.want, tt.err)
		}
	}
	longStem := strings.Repeat("é", 126) + ".bin" // 256 bytes would not fit
	mustSave(t, s, "docs", strings.Repeat("é", 125)+".bin", "x")
	got, err := s.AllocName("docs", "", strings.Repeat("é", 125)+".bin", "", false)
	if err != nil || len(got) > maxNameBytes || !utf8.ValidString(got) || !strings.HasSuffix(got, " (2).bin") {
		t.Errorf("AllocName long = %q (%d bytes), %v", got, len(got), err)
	}
	if ValidName(longStem) == nil {
		t.Errorf("test name %d bytes should be invalid", len(longStem))
	}
}

func TestAllocCopyName(t *testing.T) {
	s := newTestStore(t)
	mustSave(t, s, "docs", "report.pdf", "x")
	got, err := s.AllocCopyName("docs", "", "report.pdf", false)
	if err != nil || got != "report copy.pdf" {
		t.Fatalf("AllocCopyName = %q, %v", got, err)
	}
	mustSave(t, s, "docs", "Report Copy.pdf", "x")
	got, err = s.AllocCopyName("docs", "", "report.pdf", false)
	if err != nil || got != "report copy 2.pdf" {
		t.Fatalf("AllocCopyName second = %q, %v", got, err)
	}
	mustMkdir(t, s, "docs", "Projects")
	got, err = s.AllocCopyName("docs", "", "Projects", true)
	if err != nil || got != "Projects copy" {
		t.Fatalf("AllocCopyName folder = %q, %v", got, err)
	}
}

func TestAllocNotePath(t *testing.T) {
	s := newTestStore(t)
	mustMkdir(t, s, "docs", "Projects")
	mustSave(t, s, "docs", "Projects/Harbour-Survey.md", "x")
	tests := []struct {
		folder, title, except, want string
	}{
		{"", "Harbour Survey", "", "harbour-survey.md"},
		{"Projects", "Harbour Survey", "", "Projects/harbour-survey-2.md"},
		{"Projects", "Harbour Survey", "Projects/Harbour-Survey.md", "Projects/harbour-survey.md"},
		{"Projects", "index", "", "Projects/index-note.md"},
	}
	for _, tt := range tests {
		got, err := s.AllocNotePath("docs", tt.folder, tt.title, tt.except)
		if err != nil || got != tt.want {
			t.Errorf("AllocNotePath(%q, %q, %q) = %q, %v, want %q", tt.folder, tt.title, tt.except, got, err, tt.want)
		}
	}
	got, err := s.AllocNotePath("docs", "", strings.Repeat("𝒜", 80), "")
	if err != nil || len(path.Base(got)) > maxNameBytes || !strings.HasSuffix(got, ".md") {
		t.Errorf("AllocNotePath long title = %q (%d bytes), %v", got, len(got), err)
	}
	if _, err := s.AllocNotePath("docs", "../x", "t", ""); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("AllocNotePath bad folder error = %v", err)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := map[string]string{
		"photo.png":              "photo.png",
		"../../etc/passwd":       "_.._etc_passwd",
		`C:\Users\x\a.png`:       `C:_Users_x_a.png`,
		"  .hidden  ":            "hidden",
		"":                       "file",
		"...":                    "file",
		"Thumbs.db":              "_Thumbs.db",
		"a.tmp-1":                "a_tmp-1",
		"line\nbreak":            "line_break",
		"bad\xffbyte":            "bad_byte",
		strings.Repeat("a", 300): strings.Repeat("a", 255),
	}
	for in, want := range tests {
		got := sanitizeName(in)
		if got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
		if ValidName(got) != nil {
			t.Errorf("sanitizeName(%q) = %q is invalid", in, got)
		}
	}
}

// FuzzValidName checks that an accepted name is one safe path segment: no
// separator, no leading dot, not "." or "..", within the byte limit, and
// joined onto a directory it stays one level below it.
func FuzzValidName(f *testing.F) {
	for _, seed := range []string{
		"", ".", "..", "a", ".a", "a/b", `a\b`, "a\x00", " a", "a ", "ä", "Thumbs.db",
		"x.tmp-1", "~$x", strings.Repeat("a", 256), "\u2028", "a\u200bb", "COM1", "a:b",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if ValidName(name) != nil {
			if sanitized := sanitizeName(name); ValidName(sanitized) != nil {
				t.Fatalf("sanitizeName(%q) = %q is invalid", name, sanitized)
			}
			return
		}
		if strings.ContainsAny(name, "/\\\x00") || strings.HasPrefix(name, ".") || name == ".." || len(name) > maxNameBytes {
			t.Fatalf("accepted unsafe name %q", name)
		}
		if IgnoredName(name) {
			t.Fatalf("accepted ignored name %q", name)
		}
		root := filepath.Join(string(filepath.Separator), "space")
		joined := filepath.Join(root, name)
		if filepath.Dir(joined) != root || filepath.Base(joined) != name {
			t.Fatalf("name %q joins to %q", name, joined)
		}
		if sanitizeName(name) != name {
			t.Fatalf("sanitizeName changed valid name %q to %q", name, sanitizeName(name))
		}
		if !strings.EqualFold(name, name) || foldKey(name) != foldKey(strings.ToUpper(name)) && strings.EqualFold(name, strings.ToUpper(name)) {
			t.Fatalf("foldKey disagrees with EqualFold for %q", name)
		}
	})
}

// FuzzEntryPath checks that an accepted entry path is in path.Clean form,
// stays strictly inside the space directory when joined, and has no dot or
// empty segment.
func FuzzEntryPath(f *testing.F) {
	for _, seed := range []string{
		"", "a", "a/b", "/a", "a/", "a//b", "./a", "a/../b", "..", "../a", ".trash/x",
		"a/.b", "a\\..\\b", "a/b c/d.pdf", "a/ b", "\x00", "ä/Ä",
	} {
		f.Add(seed)
	}
	root := filepath.Join(os.TempDir(), "fuzz-space")
	f.Fuzz(func(t *testing.T, rel string) {
		if validEntryPath(rel) != nil {
			return
		}
		if path.Clean(rel) != rel {
			t.Fatalf("accepted path %q is not clean: %q", rel, path.Clean(rel))
		}
		joined := filepath.Join(root, filepath.FromSlash(rel))
		if joined == root || !withinDir(root, joined) {
			t.Fatalf("accepted path %q leaves the space: %q", rel, joined)
		}
		back, err := filepath.Rel(root, joined)
		if err != nil || filepath.ToSlash(back) != rel {
			t.Fatalf("accepted path %q does not round trip: %q, %v", rel, back, err)
		}
		for _, seg := range strings.Split(rel, "/") {
			if seg == "" || strings.HasPrefix(seg, ".") {
				t.Fatalf("accepted path %q has segment %q", rel, seg)
			}
		}
		if p := parentRel(rel); p != "" && validEntryPath(p) != nil {
			t.Fatalf("parent %q of accepted path %q is rejected", p, rel)
		}
	})
}

// FuzzFoldKey checks that equal fold keys match strings.EqualFold.
func FuzzFoldKey(f *testing.F) {
	f.Add("a", "A")
	f.Add("ß", "SS")
	f.Add("K", "\u212a")
	f.Add("Σς", "σΣ")
	f.Fuzz(func(t *testing.T, a, b string) {
		if !utf8.ValidString(a) || !utf8.ValidString(b) {
			return
		}
		if (foldKey(a) == foldKey(b)) != strings.EqualFold(a, b) {
			t.Fatalf("foldKey(%q)=%q foldKey(%q)=%q but EqualFold=%v", a, foldKey(a), b, foldKey(b), strings.EqualFold(a, b))
		}
	})
}

// mustMkdir creates a folder or fails the test.
func mustMkdir(t *testing.T, s *Store, space, rel string) {
	t.Helper()
	if err := s.MakeDir(space, rel); err != nil {
		t.Fatalf("MakeDir %q: %v", rel, err)
	}
}

// mustSave writes a file or fails the test.
func mustSave(t *testing.T, s *Store, space, rel, content string) {
	t.Helper()
	if _, err := s.SaveFile(space, rel, strings.NewReader(content), 0); err != nil {
		t.Fatalf("SaveFile %q: %v", rel, err)
	}
}
