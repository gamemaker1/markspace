package web

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gamemaker1/markspace/internal/core"
)

var (
	swAssetsPattern = regexp.MustCompile(`const ASSETS = (\[[^\n]*\]);`)
	swBuildPattern  = regexp.MustCompile(`const BUILD = "([0-9a-f]{20})";`)
)

// swParts extracts the build hash and the asset list from a served worker.
func swParts(t *testing.T, body string) (string, []string) {
	t.Helper()
	if strings.Contains(body, "__MARKSPACE_") {
		t.Fatalf("service worker still holds a placeholder")
	}
	build := swBuildPattern.FindStringSubmatch(body)
	if build == nil {
		t.Fatalf("service worker has no build hash")
	}
	m := swAssetsPattern.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("service worker has no asset list")
	}
	var assets []string
	if err := json.Unmarshal([]byte(m[1]), &assets); err != nil {
		t.Fatalf("asset list is not JSON: %v", err)
	}
	return build[1], assets
}

func TestServiceWorkerServed(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	rec := do(t, h, http.MethodGet, "/sw.js", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control %q", cc)
	}
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "/" {
		t.Errorf("Service-Worker-Allowed %q", got)
	}
	build, assets := swParts(t, rec.Body.String())
	for _, want := range []string{"/static/js/store.js", "/static/js/sync.js", "/static/icons/icon-192.png",
		"/static/vendor/primer/primitives-11.10.0.css", "/static/vendor/primer/light-11.10.0.css", "/static/vendor/primer/dark-11.10.0.css",
		"/static/theme.css", "/static/css/base.css", "/static/css/components.css", "/static/css/shell.css",
		"/static/css/editor.css", "/static/css/panels.css", "/static/highlight.css"} {
		if !slices.Contains(assets, want) {
			t.Errorf("asset list lacks %s", want)
		}
	}
	for _, a := range assets {
		if !strings.HasPrefix(a, "/static/") {
			t.Errorf("asset %q is not a /static/ URL", a)
		}
		if strings.HasPrefix(a, "/static/vendor/octicons-") && strings.HasSuffix(a, ".svg") {
			t.Errorf("asset list holds the Octicon file %s", a)
		}
	}
	again := do(t, h, http.MethodGet, "/sw.js", "", nil)
	if b2, _ := swParts(t, again.Body.String()); b2 != build {
		t.Errorf("build hash changed between requests: %s then %s", build, b2)
	}
}

func TestBuildHashTracksAssets(t *testing.T) {
	fsys := fstest.MapFS{
		"static/sw.js":                                {Data: []byte("const BUILD = \"__MARKSPACE_BUILD__\";\nconst ASSETS = __MARKSPACE_ASSETS__;\n")},
		"static/js/store.js":                          {Data: []byte("export const a = 1;\n")},
		"static/css/base.css":                         {Data: []byte("body { margin: 0; }\n")},
		"static/vendor/octicons-19.36.0/alert-16.svg": {Data: []byte("<svg></svg>")},
	}
	h1, assets, err := buildHash(fsys, "css")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/static/css/base.css", "/static/js/store.js", "/static/sw.js", "/static/highlight.css"}; !slices.Equal(assets, want) {
		t.Fatalf("assets %v, want %v", assets, want)
	}
	if same, _, _ := buildHash(fsys, "css"); same != h1 {
		t.Fatalf("hash is not stable")
	}
	fsys["static/js/store.js"] = &fstest.MapFile{Data: []byte("export const a = 2;\n")}
	h2, _, _ := buildHash(fsys, "css")
	if h2 == h1 {
		t.Fatalf("hash did not change with an asset")
	}
	if h3, _, _ := buildHash(fsys, "other css"); h3 == h2 {
		t.Fatalf("hash did not change with the highlight stylesheet")
	}
	fsys["static/vendor/pdfjs.js"] = &fstest.MapFile{Data: []byte("pdf one")}
	fsys["static/vendor/pdfworker.js"] = &fstest.MapFile{Data: []byte("worker one")}
	h4, withPDF, _ := buildHash(fsys, "other css")
	if slices.ContainsFunc(withPDF, func(a string) bool { return strings.HasPrefix(a, "/static/vendor/pdf") }) {
		t.Fatalf("precache list holds the runtime-only pdf.js bundle: %v", withPDF)
	}
	if want := []string{"/static/css/base.css", "/static/js/store.js", "/static/sw.js", "/static/highlight.css"}; !slices.Equal(withPDF, want) {
		t.Fatalf("assets %v, want %v", withPDF, want)
	}
	fsys["static/vendor/pdfworker.js"] = &fstest.MapFile{Data: []byte("worker two")}
	if h5, _, _ := buildHash(fsys, "other css"); h5 == h4 {
		t.Fatalf("hash did not change with the pdf.js worker")
	}
	b := renderServiceWorker(fsys, "css")
	if b.err != nil || bytes.Contains(b.body, []byte("__MARKSPACE_")) {
		t.Fatalf("render: err %v body %s", b.err, b.body)
	}
}

func TestServiceWorkerDevModeRecomputes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("static/sw.js", "const BUILD = \"__MARKSPACE_BUILD__\";\nconst ASSETS = __MARKSPACE_ASSETS__;\n")
	write("static/js/store.js", "one")
	srv := newTestServer(t, newFake())
	srv.devMode = true
	srv.root = os.DirFS(dir)
	h := srv.Handler()
	b1, assets := swParts(t, do(t, h, http.MethodGet, "/sw.js", "", nil).Body.String())
	if !slices.Contains(assets, "/static/js/store.js") {
		t.Fatalf("assets %v", assets)
	}
	write("static/js/store.js", "two")
	b2, _ := swParts(t, do(t, h, http.MethodGet, "/sw.js", "", nil).Body.String())
	if b1 == b2 {
		t.Fatalf("dev mode build hash did not change after an edit")
	}
}

// settingsFailBackend refuses the settings lookup, as for an anonymous actor.
type settingsFailBackend struct{ *fakeBackend }

func (settingsFailBackend) GetSettings(context.Context, *core.Actor) (*core.Settings, error) {
	return nil, core.ErrUnauthorized
}

func TestManifest(t *testing.T) {
	check := func(backend core.Backend, wantName string) {
		t.Helper()
		rec := do(t, newTestServer(t, backend).Handler(), http.MethodGet, "/manifest.webmanifest", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/manifest+json" {
			t.Errorf("Content-Type %q", ct)
		}
		var m struct {
			Name      string `json:"name"`
			ShortName string `json:"short_name"`
			StartURL  string `json:"start_url"`
			Scope     string `json:"scope"`
			Display   string `json:"display"`
			Theme     string `json:"theme_color"`
			Bg        string `json:"background_color"`
			Icons     []struct {
				Src, Sizes, Type, Purpose string
			} `json:"icons"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("manifest is not JSON: %v", err)
		}
		if m.Name != wantName || m.ShortName == "" || m.StartURL != "/" || m.Scope != "/" || m.Display != "standalone" || m.Theme == "" || m.Bg == "" {
			t.Errorf("manifest fields %+v", m)
		}
		found := map[string]bool{}
		for _, ic := range m.Icons {
			found[ic.Sizes+" "+ic.Type+" "+ic.Purpose] = true
		}
		for _, want := range []string{"192x192 image/png any", "512x512 image/png any", "512x512 image/png maskable", "any image/svg+xml any"} {
			if !found[want] {
				t.Errorf("manifest lacks icon %q in %v", want, found)
			}
		}
	}
	check(newFake(), "Test Space")
	check(settingsFailBackend{newFake()}, "Markspace")
}

func TestModulesAndIconsServed(t *testing.T) {
	h := newTestServer(t, newFake()).Handler()
	for _, name := range []string{"ids", "api", "session", "store", "sync", "markdown", "boot"} {
		rec := do(t, h, http.MethodGet, "/static/js/"+name+".js", "", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s.js status %d", name, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
			t.Errorf("%s.js Content-Type %q", name, ct)
		}
	}
	for name, size := range map[string]int{"icon-192.png": 192, "icon-512.png": 512, "icon-maskable-512.png": 512, "apple-touch-icon.png": 180} {
		rec := do(t, h, http.MethodGet, "/static/icons/"+name, "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status %d", name, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("%s Content-Type %q", name, ct)
		}
		img, err := png.Decode(rec.Body)
		if err != nil {
			t.Fatalf("%s does not decode: %v", name, err)
		}
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Errorf("%s is %dx%d, want %d", name, b.Dx(), b.Dy(), size)
		}
	}
	rec := do(t, h, http.MethodGet, "/static/icons/icon.svg", "", nil)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/svg+xml") {
		t.Errorf("icon.svg status %d type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
