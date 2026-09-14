package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/gamemaker1/markspace/internal/api"
)

// Placeholders in static/sw.js that handleServiceWorker replaces.
const (
	swBuildPlaceholder  = "__MARKSPACE_BUILD__"
	swAssetsPlaceholder = "__MARKSPACE_ASSETS__"
	highlightAssetURL   = "/static/highlight.css"
)

// swBuild is the rendered service worker for one set of static files.
type swBuild struct {
	hash   string
	assets []string
	body   []byte
	err    error
}

// swOnce computes a server's service worker once, on first use.
type swOnce struct {
	once  sync.Once
	build *swBuild
}

// swBuilds holds one swOnce per Server, so servers with different assets
// never share a build.
var swBuilds sync.Map

// runtimeOnlyPrefixes lists /static/ URL prefixes that the service worker
// caches on first use instead of at install. The pdf.js bundle is large and
// only a file view loads it.
var runtimeOnlyPrefixes = []string{"/static/vendor/pdf"}

// octiconFilePrefix starts the URLs of the vendored Octicon SVG files. The
// app draws icons from the path data in static/js/octicons.js, so the files
// are never precached.
const octiconFilePrefix = "/static/vendor/octicons-"

// runtimeOnly reports whether an asset URL is left out of the precache list.
func runtimeOnly(asset string) bool {
	if strings.HasPrefix(asset, octiconFilePrefix) && strings.HasSuffix(asset, ".svg") {
		return true
	}
	for _, p := range runtimeOnlyPrefixes {
		if strings.HasPrefix(asset, p) {
			return true
		}
	}
	return false
}

// buildHash hashes every file under static/ in root together with the
// highlight stylesheet, and lists every precached file as a /static/ URL.
// Runtime-only assets count toward the hash but are not listed. The list is
// sorted and always contains /static/highlight.css.
func buildHash(root fs.FS, highlightCSS string) (string, []string, error) {
	h := sha256.New()
	var assets []string
	err := fs.WalkDir(root, "static", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(root, name)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(data))
		h.Write(data)
		if !runtimeOnly("/" + name) {
			assets = append(assets, "/"+name)
		}
		return nil
	})
	if err != nil {
		return "", nil, fmt.Errorf("web: hash static files: %w", err)
	}
	fmt.Fprintf(h, "%s\x00%d\x00%s", highlightAssetURL, len(highlightCSS), highlightCSS)
	hasHighlight := false
	for _, a := range assets {
		if a == highlightAssetURL {
			hasHighlight = true
		}
	}
	if !hasHighlight {
		assets = append(assets, highlightAssetURL)
	}
	return hex.EncodeToString(h.Sum(nil))[:20], assets, nil
}

// renderServiceWorker fills the sw.js template with the build hash and the
// asset list.
func renderServiceWorker(root fs.FS, highlightCSS string) *swBuild {
	hash, assets, err := buildHash(root, highlightCSS)
	if err != nil {
		return &swBuild{err: err}
	}
	tmpl, err := fs.ReadFile(root, "static/sw.js")
	if err != nil {
		return &swBuild{err: fmt.Errorf("web: read service worker: %w", err)}
	}
	list, err := json.Marshal(assets)
	if err != nil {
		return &swBuild{err: fmt.Errorf("web: encode asset list: %w", err)}
	}
	body := bytes.ReplaceAll(tmpl, []byte(swBuildPlaceholder), []byte(hash))
	body = bytes.ReplaceAll(body, []byte(swAssetsPlaceholder), list)
	return &swBuild{hash: hash, assets: assets, body: body}
}

// serviceWorker returns the rendered worker. Outside DevMode it is computed
// once per server; in DevMode it is recomputed from disk on every call.
func (s *Server) serviceWorker() *swBuild {
	if s.devMode {
		return renderServiceWorker(s.root, s.markdownCSS)
	}
	v, _ := swBuilds.LoadOrStore(s, &swOnce{})
	o := v.(*swOnce)
	o.once.Do(func() { o.build = renderServiceWorker(s.root, s.markdownCSS) })
	return o.build
}

// handleServiceWorker serves the service worker from the site root with the
// build hash and asset list filled in.
func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	b := s.serviceWorker()
	if b.err != nil {
		s.log.Error("web: service worker unavailable", "err", b.err)
		http.Error(w, "service worker unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := w.Write(b.body); err != nil {
		s.log.Debug("web: service worker write failed", "err", err)
	}
}

// manifestIcon is one entry of the manifest icon list.
type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

// webManifest is the web app manifest document.
type webManifest struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	ShortName       string         `json:"short_name"`
	Description     string         `json:"description"`
	StartURL        string         `json:"start_url"`
	Scope           string         `json:"scope"`
	Display         string         `json:"display"`
	BackgroundColor string         `json:"background_color"`
	ThemeColor      string         `json:"theme_color"`
	Lang            string         `json:"lang"`
	Categories      []string       `json:"categories"`
	Icons           []manifestIcon `json:"icons"`
}

// shortName cuts a name to at most 12 runes for the home screen label.
func shortName(name string) string {
	runes := []rune(name)
	if len(runes) <= 12 {
		return name
	}
	return strings.TrimSpace(string(runes[:12]))
}

// manifestName returns the instance name the page layout uses: the settings
// value when set, else Options.InstanceName.
func (s *Server) manifestName(r *http.Request) string {
	name := s.instanceName
	if st, err := s.backend.GetSettings(r.Context(), api.ActorFrom(r.Context())); err == nil && st != nil && st.InstanceName != "" {
		name = st.InstanceName
	}
	return name
}

// handleManifest serves the web app manifest built from the instance name.
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	name := s.manifestName(r)
	m := webManifest{
		ID:              "/",
		Name:            name,
		ShortName:       shortName(name),
		Description:     name + " notes",
		StartURL:        "/",
		Scope:           "/",
		Display:         "standalone",
		BackgroundColor: "#ffffff",
		ThemeColor:      "#a21caf",
		Lang:            "en",
		Categories:      []string{"productivity"},
		Icons: []manifestIcon{
			{Src: "/static/icons/icon-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
			{Src: "/static/icons/icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any"},
			{Src: "/static/icons/icon-maskable-512.png", Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
			{Src: "/static/icons/icon.svg", Sizes: "any", Type: "image/svg+xml", Purpose: "any"},
		},
	}
	body, err := json.Marshal(m)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := w.Write(body); err != nil {
		s.log.Debug("web: manifest write failed", "err", err)
	}
}
