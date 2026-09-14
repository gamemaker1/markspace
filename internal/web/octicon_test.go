package web

import (
	"encoding/json"
	"html/template"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// octiconGolden holds the output of toSVG from @primer/octicons 19.36.0,
// called with the height and the options fill "currentColor" and focusable
// "false".
var octiconGolden = []struct {
	name string
	size int
	want string
}{
	{"x", 16, `<svg version="1.1" width="16" height="16" viewBox="0 0 16 16" class="octicon octicon-x" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M3.72 3.72a.75.75 0 0 1 1.06 0L8 6.94l3.22-3.22a.749.749 0 0 1 1.275.326.749.749 0 0 1-.215.734L9.06 8l3.22 3.22a.749.749 0 0 1-.326 1.275.749.749 0 0 1-.734-.215L8 9.06l-3.22 3.22a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042L6.94 8 3.72 4.78a.75.75 0 0 1 0-1.06Z"></path></svg>`},
	{"x", 24, `<svg version="1.1" width="24" height="24" viewBox="0 0 24 24" class="octicon octicon-x" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M5.72 5.72a.75.75 0 0 1 1.06 0L12 10.94l5.22-5.22a.749.749 0 0 1 1.275.326.749.749 0 0 1-.215.734L13.06 12l5.22 5.22a.749.749 0 0 1-.326 1.275.749.749 0 0 1-.734-.215L12 13.06l-5.22 5.22a.751.751 0 0 1-1.042-.018.751.751 0 0 1-.018-1.042L10.94 12 5.72 6.78a.75.75 0 0 1 0-1.06Z"></path></svg>`},
	{"kebab-horizontal", 16, `<svg version="1.1" width="16" height="16" viewBox="0 0 16 16" class="octicon octicon-kebab-horizontal" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M8 9a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3ZM1.5 9a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Zm13 0a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Z"></path></svg>`},
	{"kebab-horizontal", 24, `<svg version="1.1" width="24" height="24" viewBox="0 0 24 24" class="octicon octicon-kebab-horizontal" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M20 14a2 2 0 1 1-.001-3.999A2 2 0 0 1 20 14ZM6 12a2 2 0 1 1-3.999.001A2 2 0 0 1 6 12Zm8 0a2 2 0 1 1-3.999.001A2 2 0 0 1 14 12Z"></path></svg>`},
	{"file-directory-open-fill", 16, `<svg version="1.1" width="16" height="16" viewBox="0 0 16 16" class="octicon octicon-file-directory-open-fill" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M.513 1.513A1.75 1.75 0 0 1 1.75 1h3.5c.55 0 1.07.26 1.4.7l.9 1.2a.25.25 0 0 0 .2.1H13a1 1 0 0 1 1 1v.5H2.75a.75.75 0 0 0 0 1.5h11.978a1 1 0 0 1 .994 1.117L15 13.25A1.75 1.75 0 0 1 13.25 15H1.75A1.75 1.75 0 0 1 0 13.25V2.75c0-.464.184-.91.513-1.237Z"></path></svg>`},
	{"file-directory-open-fill", 24, `<svg version="1.1" width="24" height="24" viewBox="0 0 24 24" class="octicon octicon-file-directory-open-fill" aria-hidden="true" data-component="Octicon" fill="currentColor" focusable="false"><path d="M2 4.75C2 3.784 2.784 3 3.75 3h4.971c.58 0 1.12.286 1.447.765l1.404 2.063a.25.25 0 0 0 .207.11h6.224c.966 0 1.75.783 1.75 1.75v.117H5.408a.848.848 0 0 0 0 1.695h15.484a1 1 0 0 1 .995 1.102L21 19.25c-.106 1.05-.784 1.75-1.75 1.75H3.75A1.75 1.75 0 0 1 2 19.25V4.75Z"></path></svg>`},
}

var (
	svgMarkup = regexp.MustCompile(`^<svg((?:\s+[\w:-]+="[^"]*")*)>(.*)</svg>$`)
	svgAttr   = regexp.MustCompile(`([\w:-]+)="([^"]*)"`)
	svgPathD  = regexp.MustCompile(` d="([^"]*)"`)
)

// canonicalSVG returns the sorted attributes and the inner markup of an svg
// element, so two renderings compare equal apart from attribute order.
func canonicalSVG(t *testing.T, markup string) ([]string, string) {
	t.Helper()
	m := svgMarkup.FindStringSubmatch(markup)
	if m == nil {
		t.Fatalf("not a single svg element: %s", markup)
	}
	var attrs []string
	for _, a := range svgAttr.FindAllString(m[1], -1) {
		attrs = append(attrs, a)
	}
	slices.Sort(attrs)
	return attrs, m[2]
}

func TestOcticonMatchesOcticonsOutput(t *testing.T) {
	for _, g := range octiconGolden {
		got, err := octicon(g.name, g.size)
		if err != nil {
			t.Fatalf("octicon(%q, %d): %v", g.name, g.size, err)
		}
		gotAttrs, gotInner := canonicalSVG(t, string(got))
		wantAttrs, wantInner := canonicalSVG(t, g.want)
		if !slices.Equal(gotAttrs, wantAttrs) {
			t.Errorf("octicon(%q, %d) attributes\n got %q\nwant %q", g.name, g.size, gotAttrs, wantAttrs)
		}
		if gotInner != wantInner {
			t.Errorf("octicon(%q, %d) content\n got %s\nwant %s", g.name, g.size, gotInner, wantInner)
		}
	}
}

func TestOcticonDefaultSizeIs16(t *testing.T) {
	def, err := octicon("x")
	if err != nil {
		t.Fatal(err)
	}
	sixteen, _ := octicon("x", 16)
	if def != sixteen {
		t.Errorf("octicon(\"x\") = %s, want the 16 px icon %s", def, sixteen)
	}
}

func TestOcticonRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		size []int
	}{
		{"no-such-icon", nil},
		{"more", nil},
		{"x", []int{12}},
		{"x", []int{20}},
		{"x", []int{16, 24}},
		{`x" onload="alert(1)`, nil},
	} {
		if got, err := octicon(tc.name, tc.size...); err == nil {
			t.Errorf("octicon(%q, %v) = %s, want an error", tc.name, tc.size, got)
		}
	}
}

func TestOcticonTemplateFunction(t *testing.T) {
	tmpl := template.Must(template.New("t").Funcs(funcMap).Parse(`<button>{{ octicon "kebab-horizontal" 24 }}</button>`))
	var b strings.Builder
	if err := tmpl.Execute(&b, nil); err != nil {
		t.Fatal(err)
	}
	want, _ := octicon("kebab-horizontal", 24)
	if b.String() != "<button>"+string(want)+"</button>" {
		t.Errorf("template output %s, want the unescaped icon", b.String())
	}
	bad := template.Must(template.New("t").Funcs(funcMap).Parse(`{{ octicon "no-such-icon" }}`))
	if err := bad.Execute(&b, nil); err == nil {
		t.Error("unknown icon rendered without an error")
	}
}

// TestOcticonTemplateNamesResolve checks every literal octicon call in the
// templates against the generated data.
func TestOcticonTemplateNamesResolve(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("templates", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	call := regexp.MustCompile(`\bocticon\s+"([^"]*)"(?:\s+(\d+))?`)
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range call.FindAllStringSubmatch(string(raw), -1) {
			var size []int
			if m[2] == "24" {
				size = []int{24}
			}
			if _, err := octicon(m[1], size...); err != nil {
				t.Errorf("%s: %v", file, err)
			}
		}
	}
}

// TestOcticonDataMatchesVendoredFiles checks that the generated Go data, the
// vendored SVG files and static/js/octicons.js hold the same icons and paths.
func TestOcticonDataMatchesVendoredFiles(t *testing.T) {
	dir := filepath.Join("static", "vendor", "octicons-"+octiconVersion)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	svgs := 0
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".svg")
		if !ok {
			continue
		}
		svgs++
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		paths, ok := octiconPaths[name]
		if !ok {
			t.Errorf("%s has no entry in octiconPaths", e.Name())
			continue
		}
		fileD := svgPathD.FindAllStringSubmatch(string(raw), -1)
		dataD := svgPathD.FindAllStringSubmatch(paths, -1)
		if len(fileD) == 0 || !slices.EqualFunc(fileD, dataD, func(a, b []string) bool { return a[1] == b[1] }) {
			t.Errorf("%s path data differs from octiconPaths", e.Name())
		}
	}
	if svgs != len(octiconPaths) {
		t.Errorf("%d vendored SVG files, %d entries in octiconPaths", svgs, len(octiconPaths))
	}
	for _, h := range []string{"-16", "-24"} {
		for key := range octiconPaths {
			base := strings.TrimSuffix(strings.TrimSuffix(key, "-16"), "-24")
			if _, ok := octiconPaths[base+h]; !ok {
				t.Errorf("%s has no %s px variant", base, h[1:])
			}
		}
	}

	license, err := os.ReadFile(filepath.Join(dir, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(license), "MIT License") || !strings.Contains(string(license), "GitHub Inc.") {
		t.Error("vendored LICENSE is not the Octicons MIT licence")
	}

	js, err := os.ReadFile(filepath.Join("static", "js", "octicons.js"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	if !strings.Contains(src, `export const VERSION = "`+octiconVersion+`";`) {
		t.Errorf("octicons.js does not declare version %s", octiconVersion)
	}
	_, obj, ok := strings.Cut(src, "export const PATHS = ")
	if !ok {
		t.Fatal("octicons.js has no PATHS export")
	}
	var jsPaths map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimSpace(obj), ";")), &jsPaths); err != nil {
		t.Fatalf("octicons.js PATHS: %v", err)
	}
	if !maps.Equal(jsPaths, octiconPaths) {
		t.Error("octicons.js PATHS differs from octiconPaths")
	}
}
