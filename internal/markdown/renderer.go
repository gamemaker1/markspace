// Package markdown implements ports.Renderer over goldmark. It renders note
// bodies to sanitized HTML and extracts the derived fields the rest of the
// application needs, such as the table of contents, outgoing links, task
// counts, plain text, chunks and diffs.
package markdown

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/wikilink"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// Option bits that select one of the prebuilt goldmark instances.
const (
	variantHighlight = 1 << iota
	variantHeadingIDs
	variantRawHTML
	variantCount
)

// maxTOCLevel is the deepest heading level that reaches Rendered.TOC.
const maxTOCLevel = 4

// Renderer converts markdown to HTML and extracts derived fields.
//
// A Renderer is safe for concurrent use. The goldmark instances it holds are
// immutable after New returns, and every per-render value lives either on the
// stack or on the syntax tree built for that one call.
type Renderer struct {
	logger *slog.Logger
	mds    [variantCount]goldmark.Markdown
	css    string
}

var _ ports.Renderer = (*Renderer)(nil)

// New builds a Renderer. A nil logger is replaced by one that discards output.
func New(logger *slog.Logger) *Renderer {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	r := &Renderer{logger: logger, css: buildCSS()}
	for i := range r.mds {
		r.mds[i] = buildMarkdown(
			i&variantHighlight != 0,
			i&variantHeadingIDs != 0,
			i&variantRawHTML != 0,
		)
	}
	return r
}

// buildMarkdown assembles one immutable goldmark instance.
func buildMarkdown(highlight, headingIDs, rawHTML bool) goldmark.Markdown {
	exts := []goldmark.Extender{
		extension.GFM,
		extension.Footnote,
		extension.DefinitionList,
		wikilinkExtender{},
	}
	if highlight {
		exts = append(exts, highlighting.NewHighlighting(
			highlighting.WithStyle(lightStyle),
			highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
		))
	}
	var popts []parser.Option
	if headingIDs {
		popts = append(popts, parser.WithAutoHeadingID())
	}
	// Hard wraps stay off. Raw HTML is only passed through when the caller
	// asks for it, and bluemonday sanitizes it afterwards either way.
	ropts := []renderer.Option{}
	if rawHTML {
		ropts = append(ropts, html.WithUnsafe())
	}
	return goldmark.New(
		goldmark.WithExtensions(exts...),
		goldmark.WithParserOptions(popts...),
		goldmark.WithRendererOptions(ropts...),
	)
}

// markdown returns the prebuilt instance matching opts.
func (r *Renderer) markdown(opts ports.RenderOptions) goldmark.Markdown {
	i := 0
	if opts.Highlight {
		i |= variantHighlight
	}
	if opts.HeadingIDs {
		i |= variantHeadingIDs
	}
	if opts.AllowRawHTML {
		i |= variantRawHTML
	}
	return r.mds[i]
}

// plain returns the instance used for text extraction, without highlighting,
// heading ids or raw HTML.
func (r *Renderer) plain() goldmark.Markdown {
	return r.mds[0]
}

// Render produces sanitized HTML plus the table of contents, the outgoing
// wikilinks and the task counts.
func (r *Renderer) Render(md string, opts ports.RenderOptions) (*core.Rendered, error) {
	src := []byte(md)
	gm := r.markdown(opts)
	doc := gm.Parser().Parse(text.NewReader(src))

	out := &core.Rendered{}
	if err := r.annotate(doc, src, opts, out); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := gm.Renderer().Render(&buf, src, doc); err != nil {
		return nil, fmt.Errorf("render markdown: %w", err)
	}
	out.HTML = restoreDanglingHref(policy.Sanitize(buf.String()))
	r.logger.Debug("rendered markdown",
		slog.Int("bytes", len(md)),
		slog.Int("links", len(out.Links)),
		slog.Int("headings", len(out.TOC)),
		slog.Int("tasks", out.Tasks))
	return out, nil
}

// annotate walks the tree once. It resolves every wikilink, writes the
// resolution onto the node for the wikilink renderer, and fills the table of
// contents, the link list and the task counts.
func (r *Renderer) annotate(doc ast.Node, src []byte, opts ports.RenderOptions, out *core.Rendered) error {
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *wikilink.Node:
			target := string(n.Target)
			label := nodeText(src, n)
			if label == "" {
				label = target
			}
			link := core.WikiLink{Target: target, Label: label}
			if opts.ResolveItem != nil {
				annotateItemWikilink(n, label, opts, &link)
			} else {
				annotateLegacyWikilink(n, opts)
			}
			out.Links = append(out.Links, link)
		case *ast.Link:
			if opts.ResolveItem != nil {
				if dest, ok := resolveDestination(n.Destination, ports.LinkMarkdown, false, opts); ok {
					n.Destination = dest
				}
			}
		case *ast.Image:
			if opts.ResolveItem != nil {
				if dest, ok := resolveDestination(n.Destination, ports.LinkMarkdownImage, true, opts); ok {
					n.Destination = dest
				}
			}
		case *ast.Heading:
			if !opts.HeadingIDs || n.Level > maxTOCLevel {
				return ast.WalkContinue, nil
			}
			out.TOC = append(out.TOC, core.Heading{
				Level: n.Level,
				Text:  nodeText(src, n),
				ID:    attributeString(n, "id"),
			})
		case *east.TaskCheckBox:
			out.Tasks++
			if n.IsChecked {
				out.TasksDone++
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return fmt.Errorf("walk markdown: %w", err)
	}
	return nil
}

// attributeString reads a node attribute as a string.
func attributeString(n ast.Node, name string) string {
	v, ok := n.AttributeString(name)
	if !ok {
		return ""
	}
	switch v := v.(type) {
	case []byte:
		return string(v)
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

// nodeText concatenates the text of a node and its descendants.
func nodeText(src []byte, n ast.Node) string {
	var b strings.Builder
	writeNodeText(&b, src, n)
	return strings.TrimSpace(b.String())
}

func writeNodeText(b *strings.Builder, src []byte, n ast.Node) {
	switch n := n.(type) {
	case *ast.Text:
		b.Write(n.Segment.Value(src))
		return
	case *ast.String:
		b.Write(n.Value)
		return
	case *ast.AutoLink:
		b.Write(n.Label(src))
		return
	case *ast.HTMLBlock, *ast.RawHTML:
		return
	}
	if n.Type() == ast.TypeBlock && n.Lines().Len() > 0 && n.FirstChild() == nil {
		b.WriteString(linesText(n, src))
		return
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		writeNodeText(b, src, c)
	}
}

// linesText joins the raw source lines of a block node.
func linesText(n ast.Node, src []byte) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(src))
	}
	return b.String()
}

// CSS returns the stylesheet for highlighted code blocks, the diff view and
// dangling wikilinks. Every rule is scoped under ".md-body".
func (r *Renderer) CSS() string {
	return r.css
}
