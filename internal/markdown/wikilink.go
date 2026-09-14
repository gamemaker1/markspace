package markdown

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
	"go.abhg.dev/goldmark/wikilink"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// CSS classes emitted on wikilink anchors and embeds.
const (
	classWikilink      = "wikilink"
	classWikilinkNew   = "wikilink-new"
	classWikilinkEmbed = "wikilink-embed"
)

// danglingHref is the href of a wikilink whose target does not exist.
const danglingHref = "#"

// danglingAnchor is the sanitized form of a dangling wikilink anchor.
// bluemonday drops href="#", because net/url parses it to the empty string, so
// the attribute is put back afterwards. Only anchors this package emits can
// match, because user text is escaped by the sanitizer.
const (
	danglingAnchor      = `<a class="` + classWikilink + ` ` + classWikilinkNew + `">`
	danglingAnchorFixed = `<a class="` + classWikilink + ` ` + classWikilinkNew + `" href="` + danglingHref + `">`
)

// restoreDanglingHref puts href="#" back on dangling wikilink anchors.
func restoreDanglingHref(htmlFragment string) string {
	return strings.ReplaceAll(htmlFragment, danglingAnchor, danglingAnchorFixed)
}

// Node attributes that carry the resolution of one wikilink from the tree walk
// to the renderer. They never reach the HTML output.
const (
	attrHref  = "msHref"
	attrClass = "msClass"
	attrImage = "msImage"
	attrCard  = "msCard"
)

// Extra classes for embeds of files and notes.
const (
	classFileCard     = "file-card"
	classFileCardName = "file-card-name"
	classNoteCard     = "note-card"
)

// wikilinkExtender installs the wikilink inline parser together with a
// renderer that emits classed anchors. The parser must sit below goldmark's
// own link parser, which has priority 200.
type wikilinkExtender struct{}

// Extend adds wikilink parsing and rendering to md.
func (wikilinkExtender) Extend(md goldmark.Markdown) {
	md.Parser().AddOptions(parser.WithInlineParsers(
		util.Prioritized(&wikilink.Parser{}, 199),
	))
	md.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(&wikilinkRenderer{}, 199),
	))
}

// wikilinkRenderer renders wikilink nodes annotated by Renderer.annotate. It
// holds no state, so one instance serves every concurrent render.
type wikilinkRenderer struct{}

// RegisterFuncs registers the wikilink node renderer with goldmark.
func (w *wikilinkRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(wikilink.Kind, w.render)
}

func (w *wikilinkRenderer) render(writer util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n, ok := node.(*wikilink.Node)
	if !ok {
		return ast.WalkStop, fmt.Errorf("render wikilink: unexpected node %T", node)
	}
	href := attributeString(n, attrHref)
	if href == "" {
		href = "#"
	}
	class := attributeString(n, attrClass)
	if class == "" {
		class = classWikilink + " " + classWikilinkNew
	}

	if name := attributeString(n, attrCard); name != "" {
		if !entering {
			return ast.WalkContinue, nil
		}
		writer.WriteString(`<a href="`)
		writer.Write(util.EscapeHTML(util.URLEscape([]byte(href), true)))
		writer.WriteString(`" class="`)
		writer.Write(util.EscapeHTML([]byte(class)))
		writer.WriteString(`"><span class="` + classFileCardName + `">`)
		writer.Write(util.EscapeHTML([]byte(name)))
		writer.WriteString(`</span></a>`)
		return ast.WalkSkipChildren, nil
	}

	if imgSrc := attributeString(n, attrImage); imgSrc != "" {
		if !entering {
			return ast.WalkContinue, nil
		}
		writer.WriteString(`<img src="`)
		writer.Write(util.EscapeHTML(util.URLEscape([]byte(imgSrc), true)))
		writer.WriteString(`" alt="`)
		writer.Write(util.EscapeHTML([]byte(nodeText(src, n))))
		writer.WriteString(`" class="` + classWikilinkEmbed + `">`)
		return ast.WalkSkipChildren, nil
	}

	if entering {
		writer.WriteString(`<a href="`)
		writer.Write(util.EscapeHTML(util.URLEscape([]byte(href), true)))
		writer.WriteString(`" class="`)
		writer.Write(util.EscapeHTML([]byte(class)))
		writer.WriteString(`">`)
		return ast.WalkContinue, nil
	}
	writer.WriteString("</a>")
	return ast.WalkContinue, nil
}

// imageExts lists the extensions that make an embedded wikilink an image.
var imageExts = map[string]bool{
	".apng": true, ".avif": true, ".gif": true, ".jpeg": true, ".jpg": true,
	".png": true, ".svg": true, ".webp": true,
}

// isImageTarget reports whether n is an ![[embed]] of an image file.
func isImageTarget(n *wikilink.Node) bool {
	if !n.Embed {
		return false
	}
	return imageExts[strings.ToLower(path.Ext(string(n.Target)))]
}

// annotateLegacyWikilink resolves a wikilink through RenderOptions.ResolveLink.
func annotateLegacyWikilink(n *wikilink.Node, opts ports.RenderOptions) {
	target := string(n.Target)
	href, exists := "", false
	if opts.ResolveLink != nil {
		href, exists = opts.ResolveLink(target)
	}
	if !exists || href == "" {
		markDangling(n)
		return
	}
	if len(n.Fragment) > 0 {
		href += "#" + string(n.Fragment)
	}
	n.SetAttributeString(attrHref, []byte(href))
	n.SetAttributeString(attrClass, []byte(classWikilink))
	if isImageTarget(n) {
		// An embed of a resolved image becomes an <img>. An unresolved one
		// stays an anchor, because an image without a source renders as a
		// broken box.
		n.SetAttributeString(attrImage, []byte(href))
	}
}

// annotateItemWikilink resolves a wikilink through RenderOptions.ResolveItem
// and records the result on link. An embedded image file becomes an <img>,
// another embedded file a file card and an embedded note a note card. A
// target that does not resolve stays dangling, so a hidden item never shows.
func annotateItemWikilink(n *wikilink.Node, label string, opts ports.RenderOptions, link *core.WikiLink) {
	target := string(n.Target)
	form := ports.LinkWikilink
	if strings.Contains(target, "/") {
		form = ports.LinkPathWikilink
	}
	t, ok := opts.ResolveItem(ports.LinkRef{Target: target, Form: form, Embed: n.Embed})
	href := t.URL
	if href == "" {
		href = t.FileURL
	}
	if !ok || href == "" {
		markDangling(n)
		return
	}
	link.ItemID = t.ID
	link.Kind = t.Kind
	if t.Kind == core.KindNote {
		link.NoteID = t.ID
	}
	class := classWikilink
	if n.Embed {
		switch {
		case t.Kind == core.KindFile && t.FileURL != "" && strings.HasPrefix(strings.ToLower(t.MIME), "image/"):
			n.SetAttributeString(attrHref, []byte(href))
			n.SetAttributeString(attrClass, []byte(class))
			n.SetAttributeString(attrImage, []byte(t.FileURL))
			return
		case t.Kind == core.KindFile:
			name := t.Name
			written := target
			if len(n.Fragment) > 0 {
				written += "#" + string(n.Fragment)
			}
			if name == "" || label != written {
				name = label
			}
			class += " " + classFileCard
			n.SetAttributeString(attrCard, []byte(name))
		case t.Kind == core.KindNote:
			class += " " + classNoteCard
		}
	}
	if len(n.Fragment) > 0 {
		href += "#" + string(n.Fragment)
	}
	n.SetAttributeString(attrHref, []byte(href))
	n.SetAttributeString(attrClass, []byte(class))
}

// markDangling renders a wikilink as a link to nothing.
func markDangling(n *wikilink.Node) {
	n.SetAttributeString(attrHref, []byte(danglingHref))
	n.SetAttributeString(attrClass, []byte(classWikilink+" "+classWikilinkNew))
}

// resolveDestination resolves a markdown link or image destination through
// RenderOptions.ResolveItem. It returns the new destination and true when the
// target is a local path that resolves. A link keeps its #fragment. An image
// takes the file URL and is left alone when the item has none.
func resolveDestination(dest []byte, form string, image bool, opts ports.RenderOptions) ([]byte, bool) {
	raw := util.UnescapePunctuations(dest)
	raw = util.ResolveNumericReferences(raw)
	raw = util.ResolveEntityNames(raw)
	p := string(raw)
	if externalDestination(p) {
		return nil, false
	}
	frag := ""
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		if j := strings.IndexByte(p[i:], '#'); j >= 0 {
			frag = p[i+j:]
		}
		p = p[:i]
	}
	if dec, err := url.PathUnescape(p); err == nil && utf8.ValidString(dec) {
		p = dec
	}
	if p == "" {
		return nil, false
	}
	t, ok := opts.ResolveItem(ports.LinkRef{Target: p, Form: form, Embed: image})
	if !ok {
		return nil, false
	}
	if image {
		if t.FileURL == "" {
			return nil, false
		}
		return []byte(t.FileURL), true
	}
	if t.URL == "" {
		return nil, false
	}
	return []byte(t.URL + frag), true
}

// externalDestination reports whether a markdown destination is not a path
// in the space: empty, an anchor, a //host URL or a URL with a scheme of 2 to
// 32 characters.
func externalDestination(p string) bool {
	if p == "" || strings.HasPrefix(p, "#") || strings.HasPrefix(p, "//") {
		return true
	}
	for i := 0; i < len(p) && i <= 32; i++ {
		c := p[i]
		switch {
		case c == ':':
			return i >= 2
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		case i > 0 && ((c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return false
}
