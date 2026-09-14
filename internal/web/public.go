package web

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// inlineTypes are the media types shown in the browser instead of being
// offered as a download.
var inlineTypes = []string{"image/", "application/pdf", "text/plain", "video/", "audio/"}

// downloadOnlyTypes can carry script when opened as a document, so they are
// always offered as a download even though they match inlineTypes. An <img>
// in a note still displays them, because browsers ignore Content-Disposition
// for subresources and never run script inside an image.
var downloadOnlyTypes = map[string]bool{"image/svg+xml": true}

// attachmentCSP confines an uploaded file opened directly in the browser. The
// sandbox directive gives the document a unique opaque origin with scripts,
// forms and plugins disabled, so a crafted file cannot act with the viewer's
// session. The source directives let an image, video or audio document still
// show its own content.
const attachmentCSP = "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'"

// thumbWidths are the thumbnail widths the thumbs route accepts.
var thumbWidths = map[int]bool{128: true, 256: true, 512: true, 1024: true}

// defaultThumbWidth is the width served when the request names none.
const defaultThumbWidth = 256

// linkCookieMaxAge is how long a public link password is remembered, in
// seconds.
const linkCookieMaxAge = 3600

// linkCookieName returns the cookie that remembers the password of one public
// link. It returns "" for a token that cannot be part of a cookie name.
func linkCookieName(token string) string {
	if token == "" || len(token) > 128 {
		return ""
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return ""
		}
	}
	return "ms_link_" + token
}

// linkPassword returns the remembered password of a public link, or "".
func linkPassword(r *http.Request, token string) string {
	name := linkCookieName(token)
	if name == "" {
		return ""
	}
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	if v, err := url.QueryUnescape(c.Value); err == nil {
		return v
	}
	return ""
}

// rememberLinkPassword stores a correct link password in a cookie scoped to
// the link's own paths.
func rememberLinkPassword(w http.ResponseWriter, token, password string) {
	name := linkCookieName(token)
	if name == "" || password == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    url.QueryEscape(password),
		Path:     "/p/" + token,
		MaxAge:   linkCookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// publicBase returns the URL prefix of one public link.
func publicBase(token string) string { return "/p/" + url.PathEscape(token) }

// publicRawURL returns the URL serving a file's content through a link.
func publicRawURL(token string, f *core.File) string {
	return publicBase(token) + "/raw/" + url.PathEscape(f.ID) + "/" + url.PathEscape(f.Name)
}

// handlePublicLink renders the item behind a public share token. It serves
// both GET and POST, because a protected link needs a password form. A
// correct password is remembered in a cookie scoped to the link, so the
// content routes below it work too. The response is never indexed.
func (s *Server) handlePublicLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	token := r.PathValue("token")
	password := linkPassword(r, token)
	posted := false
	if r.Method == http.MethodPost {
		if err := s.formValues(r); err != nil {
			s.fail(w, r, core.Invalid("form", "the form could not be read"))
			return
		}
		password = r.PostFormValue("password")
		posted = true
	}
	actor, item, err := s.backend.ResolvePublicItem(r.Context(), token, password)
	if err != nil {
		s.publicFail(w, r, token, posted, err)
		return
	}
	if posted {
		rememberLinkPassword(w, token, password)
	}
	s.renderPublicItem(w, r, token, actor, item)
}

// handlePublicChild renders one item inside a public folder link.
func (s *Server) handlePublicChild(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	token := r.PathValue("token")
	actor, _, err := s.backend.ResolvePublicItem(r.Context(), token, linkPassword(r, token))
	if err != nil {
		s.publicFail(w, r, token, false, err)
		return
	}
	item, err := s.backend.GetItem(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		s.publicFail(w, r, token, false, err)
		return
	}
	s.renderPublicItem(w, r, token, actor, item)
}

// handlePublicRaw serves the content of a file reached through a public link.
func (s *Server) handlePublicRaw(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	token := r.PathValue("token")
	actor, _, err := s.backend.ResolvePublicItem(r.Context(), token, linkPassword(r, token))
	if err != nil {
		s.contentFail(w, r, err)
		return
	}
	file, version, body, err := s.backend.OpenFile(r.Context(), actor, r.PathValue("id"), r.URL.Query().Get("v"))
	if err != nil {
		s.contentFail(w, r, err)
		return
	}
	defer body.Close()
	serveFileContent(w, r, file, version, body, boolQuery(r, "download"))
}

// handlePublicThumb serves a thumbnail of a file reached through a public
// link.
func (s *Server) handlePublicThumb(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	width, ok := thumbWidth(r)
	if !ok {
		http.Error(w, "w must be 128, 256, 512 or 1024", http.StatusBadRequest)
		return
	}
	actor, _, err := s.backend.ResolvePublicItem(r.Context(), token, linkPassword(r, token))
	if err != nil {
		s.contentFail(w, r, err)
		return
	}
	s.serveThumb(w, r, actor, width)
}

// publicFail answers a failed public link request. A link that needs a
// password shows the password form, and anything else is the error page.
func (s *Server) publicFail(w http.ResponseWriter, r *http.Request, token string, retry bool, err error) {
	if errors.Is(err, core.ErrUnauthorized) {
		s.renderLinkPassword(w, r, token, retry)
		return
	}
	s.fail(w, r, err)
}

// contentFail answers a failed content request with a bare status, because a
// login redirect or an HTML error page is useless inside an img or video
// element.
func (s *Server) contentFail(w http.ResponseWriter, r *http.Request, err error) {
	status := statusFor(err)
	if status == http.StatusInternalServerError {
		s.log.Error("web: content request failed", "path", r.URL.Path, "err", err)
	}
	http.Error(w, http.StatusText(status), status)
}

// publicChild is one row of a public folder listing.
type publicChild struct {
	Kind core.ItemKind
	Name string
	URL  string
	Size int64
	MIME string
}

// renderPublicItem renders a note as the public note page, and a file or a
// folder as the public item page.
func (s *Server) renderPublicItem(w http.ResponseWriter, r *http.Request, token string, actor *core.Actor, item *core.Item) {
	ctx := r.Context()
	grant := &core.Grant{Access: core.AccessView, Source: "link"}
	if actor != nil && actor.Link != nil {
		grant = &core.Grant{Access: actor.Link.Access, Source: "link"}
	}
	switch {
	case item != nil && item.Note != nil:
		note := item.Note
		p := s.newPage(r, note.Title, "public")
		p.Chrome = false
		p.Actor = actor
		p.Note = note
		p.Grant = grant
		p.Set("Token", token)
		p.Set("ReadOnly", true)
		rendered, err := s.backend.RenderNote(ctx, actor, note)
		if err != nil {
			s.log.Warn("web: public render failed", "note", note.ID, "err", err)
			rendered = &core.Rendered{}
		}
		p.Set("Rendered", rendered)
		p.Set("ShowTOC", len(rendered.TOC) >= tocMinHeadings)
		if hasAccess(p.Grant, core.AccessComment) {
			if comments, err := s.backend.ListComments(ctx, actor, note.ID); err == nil {
				p.Set("Comments", comments)
			}
		}
		s.Render(w, r, http.StatusOK, "public", p)
	case item != nil && item.File != nil:
		f := item.File
		p := s.newPage(r, f.Name, "public")
		p.Chrome = false
		p.Actor = actor
		p.Grant = grant
		raw := publicRawURL(token, f)
		p.Set("Token", token)
		p.Set("File", f)
		p.Set("Viewer", viewerKind(f.MIME))
		p.Set("RawURL", raw)
		p.Set("DownloadURL", raw+"?download=1")
		s.Render(w, r, http.StatusOK, "public/item", p)
	case item != nil && item.Folder != nil:
		folder := item.Folder
		p := s.newPage(r, folder.Name, "public")
		p.Chrome = false
		p.Actor = actor
		p.Grant = grant
		p.Set("Token", token)
		p.Set("Folder", folder)
		view, _, err := s.backend.GetFolder(ctx, actor, folder.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		children := make([]publicChild, 0, len(view.Children))
		for _, c := range view.Children {
			switch {
			case c.Folder != nil:
				children = append(children, publicChild{Kind: core.KindFolder, Name: c.Folder.Name,
					URL: publicBase(token) + "/i/" + url.PathEscape(c.Folder.ID)})
			case c.Note != nil:
				children = append(children, publicChild{Kind: core.KindNote, Name: c.Note.Title,
					URL: publicBase(token) + "/i/" + url.PathEscape(c.Note.ID)})
			case c.File != nil:
				children = append(children, publicChild{Kind: core.KindFile, Name: c.File.Name,
					URL: publicRawURL(token, c.File), Size: c.File.Size, MIME: c.File.MIME})
			}
		}
		p.Set("Listing", children)
		s.Render(w, r, http.StatusOK, "public/item", p)
	default:
		s.fail(w, r, core.ErrNotFound)
	}
}

// viewerKind picks how the public item page shows a file.
func viewerKind(contentType string) string {
	base := baseMediaType(contentType)
	switch {
	case base == "application/pdf":
		return "pdf"
	case strings.HasPrefix(base, "image/"):
		return "image"
	case strings.HasPrefix(base, "video/"):
		return "video"
	case strings.HasPrefix(base, "audio/"):
		return "audio"
	}
	return "other"
}

// renderLinkPassword asks for the password of a protected public link.
func (s *Server) renderLinkPassword(w http.ResponseWriter, r *http.Request, token string, retry bool) {
	p := s.newPage(r, "Protected note", "public")
	p.Chrome = false
	p.Set("Token", token)
	status := http.StatusOK
	if retry {
		// The message sits under the password field (docs/primer.md 6.6).
		form := newForm("link", r)
		if r.PostFormValue("password") == "" {
			form.add("password", "Enter the password")
		} else {
			form.add("password", "That password does not match this link")
		}
		p.Set(formDataKey, form)
		status = http.StatusUnauthorized
	}
	s.Render(w, r, status, "public/password", p)
}

// handleFile serves the content of a file, or of one of its versions with
// ?v=. Images, PDFs, plain text, audio and video are shown inline and
// everything else, including SVG, is offered as a download. ?download=1
// always downloads. Every response except a PDF carries a sandboxing content
// security policy. Chrome refuses to display a PDF under a sandbox policy, and
// PDF JavaScript runs inside the browser's PDF engine rather than in this
// origin. Range requests are answered by http.ServeContent.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	actor := currentActor(r)
	file, version, body, err := s.backend.OpenFile(r.Context(), actor, r.PathValue("id"), r.URL.Query().Get("v"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer body.Close()
	serveFileContent(w, r, file, version, body, boolQuery(r, "download"))
}

// handleThumb serves a cached thumbnail of an image file.
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	width, ok := thumbWidth(r)
	if !ok {
		http.Error(w, "w must be 128, 256, 512 or 1024", http.StatusBadRequest)
		return
	}
	s.serveThumb(w, r, currentActor(r), width)
}

// serveThumb opens and serves one thumbnail for actor.
func (s *Server) serveThumb(w http.ResponseWriter, r *http.Request, actor *core.Actor, width int) {
	file, body, mediaType, err := s.backend.OpenThumbnail(r.Context(), actor, r.PathValue("id"), width)
	if err != nil {
		s.contentFail(w, r, err)
		return
	}
	defer body.Close()
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Security-Policy", attachmentCSP)
	modtime := time.Time{}
	if file != nil {
		modtime = file.ModTime
		if file.SHA256 != "" {
			w.Header().Set("ETag", `"`+file.SHA256+"-"+strconv.Itoa(width)+`"`)
		}
	}
	http.ServeContent(w, r, "", modtime, body)
}

// thumbWidth reads the w query parameter. A missing value is the default
// width, and a value outside thumbWidths is refused.
func thumbWidth(r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("w")
	if raw == "" {
		return defaultThumbWidth, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || !thumbWidths[n] {
		return 0, false
	}
	return n, true
}

// boolQuery reports whether a query parameter is set to a true value.
func boolQuery(r *http.Request, key string) bool {
	switch strings.ToLower(r.URL.Query().Get(key)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// serveFileContent writes the headers from docs/design.md "Uploaded files" and
// serves body with Range support. A version supplies its own name, type,
// hash and time.
func serveFileContent(w http.ResponseWriter, r *http.Request, file *core.File, version *core.FileVersion, body io.ReadSeeker, download bool) {
	name, ct, sum := "", "", ""
	var modtime time.Time
	if file != nil {
		name, ct, sum, modtime = file.Name, file.MIME, file.SHA256, file.ModTime
		if modtime.IsZero() {
			modtime = file.UpdatedAt
		}
	}
	if version != nil {
		if version.Name != "" {
			name = version.Name
		}
		if version.MIME != "" {
			ct = version.MIME
		}
		sum, modtime = version.SHA256, version.CreatedAt
	}
	if name == "" {
		name = path.Base(r.URL.Path)
	}
	if ct == "" {
		ct = mime.TypeByExtension(path.Ext(name))
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, max-age=3600")
	if download {
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(name)}))
	} else {
		h.Set("Content-Disposition", disposition(ct, name))
	}
	if baseMediaType(ct) != "application/pdf" {
		h.Set("Content-Security-Policy", attachmentCSP)
	}
	if sum != "" {
		h.Set("ETag", `"`+sum+`"`)
	}
	http.ServeContent(w, r, name, modtime, body)
}

// disposition builds the Content-Disposition header for one media type.
func disposition(contentType, name string) string {
	kind := "attachment"
	base := baseMediaType(contentType)
	if !downloadOnlyTypes[base] {
		for _, prefix := range inlineTypes {
			if strings.HasPrefix(base, prefix) {
				kind = "inline"
				break
			}
		}
	}
	return mime.FormatMediaType(kind, map[string]string{"filename": path.Base(name)})
}

// baseMediaType returns the lowercase type and subtype of a Content-Type
// value, without parameters such as charset.
func baseMediaType(contentType string) string {
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		return mt
	}
	base, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

// handleStatic serves the embedded stylesheets and scripts. Responses are
// cacheable for an hour, and immutable outside dev mode.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := path.Clean("/" + r.PathValue("path"))
	name = strings.TrimPrefix(name, "/")
	if name == "" || name == "." || strings.HasPrefix(name, "../") {
		s.fail(w, r, core.ErrNotFound)
		return
	}
	file, err := s.root.Open("static/" + name)
	if err != nil {
		s.fail(w, r, core.ErrNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		s.fail(w, r, core.ErrNotFound)
		return
	}
	seeker, ok := file.(io.ReadSeeker)
	if !ok {
		s.fail(w, r, core.ErrNotFound)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", cacheControl(s.devMode))
	http.ServeContent(w, r, info.Name(), info.ModTime(), seeker)
}

// cacheControl returns the caching policy for a static asset.
func cacheControl(dev bool) string {
	if dev {
		return "no-cache"
	}
	return "public, max-age=3600, immutable"
}

// handleHighlightCSS serves the syntax-highlighting stylesheet supplied by
// the markdown renderer.
func (s *Server) handleHighlightCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", cacheControl(s.devMode))
	if _, err := w.Write([]byte(s.markdownCSS)); err != nil {
		s.log.Debug("web: highlight stylesheet write failed", "err", err)
	}
}

// handleHealthz answers a liveness probe.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write([]byte("ok")); err != nil {
		s.log.Debug("web: health response write failed", "err", err)
	}
}
