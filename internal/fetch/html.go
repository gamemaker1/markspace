package fetch

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/gamemaker1/markspace/internal/core"
)

// maxHTMLDepth caps how deep the text walk descends into nested elements.
const maxHTMLDepth = 512

// Separators between blocks of extracted text.
const (
	lineBreak  = "\n"
	blockBreak = "\n\n"
)

// HTMLText parses an HTML document and returns the text of its <title> and
// the readable text of the page. The walk drops script, style, noscript, nav,
// header, footer, aside and form elements, plus template, svg, iframe and
// object. Headings become markdown headings, list items become "- " lines,
// table cells in one row are joined with " | ", preformatted blocks keep their
// whitespace, and other text has its whitespace collapsed. The result is
// valid UTF-8. A document the parser rejects, such as one nested deeper than
// the parser allows, is read with a flat token scan under the same rules.
func HTMLText(r io.Reader) (title, text string, err error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", "", fmt.Errorf("fetch: read HTML: %w: %w", core.ErrInvalid, err)
	}
	w := &textWriter{}
	if doc, perr := html.Parse(bytes.NewReader(data)); perr == nil {
		w.walk(doc, 0)
	} else {
		w.tokens(data)
	}
	w.flush("")
	title = strings.ToValidUTF8(w.title, "�")
	text = strings.ToValidUTF8(strings.TrimSpace(w.out.String()), "�")
	return title, text, nil
}

// textWriter accumulates the text of a parsed document.
type textWriter struct {
	out   strings.Builder
	line  strings.Builder
	title string
	// prefix starts the next written line, such as "- " or "## ".
	prefix string
	// gap is the separator owed before the next written block.
	gap string
	// space records whitespace seen since the last word on the line.
	space bool
	// cells counts the cells written in the current table row.
	cells int
}

// dropped lists the elements whose content never reaches the text.
var dropped = map[atom.Atom]bool{
	atom.Script:   true,
	atom.Style:    true,
	atom.Noscript: true,
	atom.Nav:      true,
	atom.Header:   true,
	atom.Footer:   true,
	atom.Aside:    true,
	atom.Form:     true,
	atom.Template: true,
	atom.Svg:      true,
	atom.Iframe:   true,
	atom.Object:   true,
}

// blocks lists the elements that start and end a paragraph of text.
var blocks = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Section: true, atom.Article: true, atom.Main: true,
	atom.Blockquote: true, atom.Ul: true, atom.Ol: true, atom.Table: true, atom.Dl: true,
	atom.Dt: true, atom.Dd: true, atom.Figure: true, atom.Figcaption: true, atom.Hr: true,
	atom.Address: true, atom.Details: true, atom.Summary: true, atom.Body: true,
	atom.Caption: true, atom.Thead: true, atom.Tbody: true, atom.Tfoot: true,
}

// headingLevels maps heading elements to their level.
var headingLevels = map[atom.Atom]int{
	atom.H1: 1, atom.H2: 2, atom.H3: 3, atom.H4: 4, atom.H5: 5, atom.H6: 6,
}

// walk visits n and its children.
func (w *textWriter) walk(n *html.Node, depth int) {
	if depth > maxHTMLDepth {
		return
	}
	switch n.Type {
	case html.TextNode:
		w.text(n.Data)
		return
	case html.ElementNode:
		if dropped[n.DataAtom] {
			return
		}
		if level, ok := headingLevels[n.DataAtom]; ok {
			w.flush(blockBreak)
			w.prefix = strings.Repeat("#", level) + " "
			w.children(n, depth)
			w.flush(blockBreak)
			w.prefix = ""
			return
		}
		switch n.DataAtom {
		case atom.Title:
			if w.title == "" {
				w.title = strings.Join(strings.Fields(textContent(n, 0)), " ")
			}
			return
		case atom.Br:
			w.flush(lineBreak)
			return
		case atom.Pre:
			w.flush(blockBreak)
			w.raw(textContent(n, 0))
			return
		case atom.Li:
			w.flush(lineBreak)
			w.prefix = "- "
			w.children(n, depth)
			w.flush(lineBreak)
			w.prefix = ""
			return
		case atom.Tr:
			w.flush(lineBreak)
			w.cells = 0
			w.children(n, depth)
			w.flush(lineBreak)
			w.cells = 0
			return
		case atom.Td, atom.Th:
			if w.cells > 0 {
				w.line.WriteString(" | ")
				w.space = false
			}
			w.cells++
			w.children(n, depth)
			return
		}
		if blocks[n.DataAtom] {
			w.flush(blockBreak)
			w.children(n, depth)
			w.flush(blockBreak)
			return
		}
	}
	w.children(n, depth)
}

// tokens extracts text from a token stream without building a tree. It
// applies the element rules of walk to start and end tags.
func (w *textWriter) tokens(data []byte) {
	z := html.NewTokenizer(bytes.NewReader(data))
	skip := 0
	inTitle, inPre := false, false
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return
		case html.TextToken:
			if skip > 0 {
				continue
			}
			s := string(z.Text())
			switch {
			case inTitle:
				if w.title == "" {
					w.title = strings.Join(strings.Fields(s), " ")
				}
			case inPre:
				w.raw(s)
			default:
				w.text(s)
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			a := atom.Lookup(name)
			start := tt == html.StartTagToken
			if dropped[a] {
				if start {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
				continue
			}
			if skip > 0 {
				continue
			}
			if level, ok := headingLevels[a]; ok {
				w.flush(blockBreak)
				w.prefix = ""
				if start {
					w.prefix = strings.Repeat("#", level) + " "
				}
				continue
			}
			switch {
			case a == atom.Title:
				inTitle = start
			case a == atom.Pre:
				w.flush(blockBreak)
				inPre = start
			case a == atom.Br:
				w.flush(lineBreak)
			case a == atom.Li:
				w.flush(lineBreak)
				w.prefix = ""
				if start {
					w.prefix = "- "
				}
			case a == atom.Tr:
				w.flush(lineBreak)
				w.cells = 0
			case (a == atom.Td || a == atom.Th) && start:
				if w.cells > 0 {
					w.line.WriteString(" | ")
					w.space = false
				}
				w.cells++
			case blocks[a]:
				w.flush(blockBreak)
			}
		}
	}
}

// children visits every child of n.
func (w *textWriter) children(n *html.Node, depth int) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c, depth+1)
	}
}

// text appends inline text with its whitespace collapsed.
func (w *textWriter) text(s string) {
	if s == "" {
		return
	}
	if isSpace(s[0]) {
		w.space = true
	}
	for _, word := range strings.Fields(s) {
		if w.space && w.line.Len() > 0 && !strings.HasSuffix(w.line.String(), " ") {
			w.line.WriteByte(' ')
		}
		w.line.WriteString(word)
		w.space = true
	}
	if !isSpace(s[len(s)-1]) {
		w.space = false
	}
}

// raw writes a preformatted block with its whitespace kept.
func (w *textWriter) raw(s string) {
	s = strings.Trim(s, "\n")
	if strings.TrimSpace(s) == "" {
		return
	}
	if w.out.Len() > 0 {
		w.out.WriteString(blockBreak)
	}
	w.out.WriteString(s)
	w.gap = blockBreak
}

// flush writes the pending line as a block and owes gap before the next one.
// A line with no text writes nothing and keeps the prefix for the text that
// follows.
func (w *textWriter) flush(gap string) {
	line := strings.TrimSpace(w.line.String())
	w.line.Reset()
	w.space = false
	if line != "" {
		if w.out.Len() > 0 {
			sep := w.gap
			if sep == "" {
				sep = lineBreak
			}
			w.out.WriteString(sep)
		}
		w.out.WriteString(w.prefix)
		w.out.WriteString(line)
		w.prefix = ""
		w.gap = ""
	}
	if len(gap) > len(w.gap) {
		w.gap = gap
	}
}

// textContent returns the concatenated text below n, without dropped
// elements.
func textContent(n *html.Node, depth int) string {
	if depth > maxHTMLDepth {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Type == html.ElementNode && dropped[n.DataAtom] {
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textContent(c, depth+1))
	}
	return b.String()
}

// isSpace reports whether c is HTML whitespace.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}
