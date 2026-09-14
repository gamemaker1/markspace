package ports

import "github.com/gamemaker1/markspace/internal/core"

// RenderOptions configures one markdown render.
type RenderOptions struct {
	// ResolveLink maps a [[wikilink]] target to a URL. exists reports whether
	// a note matched, so the renderer can mark dangling links. A nil function
	// renders every wikilink as dangling.
	ResolveLink func(target string) (url string, exists bool)
	// ResolveItem resolves wikilinks, relative markdown links and markdown
	// images to items. When it is set, ResolveLink is not called. A result
	// with ok false leaves a wikilink dangling and a markdown destination as
	// written.
	ResolveItem func(ref LinkRef) (target LinkTarget, ok bool)
	// Highlight turns on server-side syntax highlighting of code blocks.
	Highlight bool
	// HeadingIDs adds anchor ids to headings and fills Rendered.TOC.
	HeadingIDs bool
	// AllowRawHTML keeps sanitized inline HTML. It is always sanitized.
	AllowRawHTML bool
}

// LinkRef is one link as the renderer found it. Target is decoded and holds
// no #heading or |label. Form is one of the link forms in db.go.
type LinkRef struct {
	Target string
	Form   string
	Embed  bool
}

// LinkTarget is the item a link resolved to. URL opens the item. FileURL
// serves a file's content and is empty for notes and folders. MIME is set
// for files.
type LinkTarget struct {
	Kind    core.ItemKind
	ID      string
	Name    string
	URL     string
	FileURL string
	MIME    string
}

// Renderer converts markdown to HTML and extracts derived fields.
type Renderer interface {
	// Render produces sanitized HTML plus the table of contents, the outgoing
	// wikilinks and task counts.
	Render(md string, opts RenderOptions) (*core.Rendered, error)
	// PlainText strips markup, for the full-text index and embeddings.
	PlainText(md string) string
	// Excerpt returns the first maxRunes runes of plain text.
	Excerpt(md string, maxRunes int) string
	// WordCount counts words in the plain text.
	WordCount(md string) int
	// InlineTags returns #hashtags found in the body.
	InlineTags(md string) []string
	// Links returns every [[wikilink]] and relative markdown link.
	Links(md string) []core.WikiLink
	// DiffHTML renders a line diff between two bodies as HTML.
	DiffHTML(oldText, newText string) string
	// Chunks splits a body into overlapping pieces of about targetRunes runes
	// each, for embedding.
	Chunks(md string, targetRunes int) []string
	// CSS returns the stylesheet for highlighted code blocks.
	CSS() string
}
