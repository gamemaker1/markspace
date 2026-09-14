package links

import (
	"sort"
	"strings"
)

// Form is the syntax of a link.
type Form int

// Link forms. An embed is a wikilink or path wikilink with Embed set.
const (
	FormWikilink Form = iota
	FormPathWikilink
	FormMarkdownLink
	FormMarkdownImage
)

var formNames = [...]string{"wikilink", "pathWikilink", "markdownLink", "markdownImage"}

// String returns the name used in test/vectors/links.json.
func (f Form) String() string {
	if f < 0 || int(f) >= len(formNames) {
		return "unknown"
	}
	return formNames[f]
}

// ParseForm returns the form named s, and false for an unknown name.
func ParseForm(s string) (Form, bool) {
	for i, n := range formNames {
		if n == s {
			return Form(i), true
		}
	}
	return 0, false
}

// IsWikilink reports whether f is a wikilink or a path wikilink.
func (f Form) IsWikilink() bool { return f == FormWikilink || f == FormPathWikilink }

// Link is one link occurrence in a markdown body. Offsets are UTF-8 byte
// offsets into the body.
type Link struct {
	Form     Form
	Embed    bool   // ![[...]]
	External bool   // a URL with a scheme, a //host URL, a pure #anchor or an empty destination
	Target   string // decoded target without #heading, ?query and |label
	Heading  string // decoded text after the first #
	Label    string // wikilink label, or markdown link text as written
	Raw      string // target text as written, body[TargetStart:TargetEnd]
	Angle    bool   // markdown destination written as <...>

	Start, End             int // the whole link syntax
	TargetStart, TargetEnd int // the raw target text, which a rewrite replaces
	// DestStart and DestEnd cover a markdown destination including angle
	// brackets, #heading and ?query, without the title. For wikilinks they
	// equal TargetStart and TargetEnd.
	DestStart, DestEnd int
}

// Parse returns the links in body in document order, ordered by Start. It
// skips fenced and indented code blocks, inline code spans, HTML comments and
// autolinks. The body must not include frontmatter.
func Parse(body string) []Link {
	var out []Link
	for _, b := range inlineBlocks(body) {
		out = scanInline(body, b[0], b[1], out)
	}
	return out
}

// blockPass tracks block state while inlineBlocks walks the lines.
type blockPass struct {
	body       string
	blocks     [][2]int
	blockStart int
	blockEnd   int
	para       bool
	quote      int
	lists      []int
	fence      byte
	fenceLen   int
	fenceBase  int
	fenceQuote int
	comment    bool
}

// inlineBlocks returns the byte ranges whose text is parsed for inline links.
// A range is a paragraph, a heading line or the content of a list item line
// and its continuation lines.
func inlineBlocks(body string) [][2]int {
	p := &blockPass{body: body, blockStart: -1}
	for pos := 0; pos < len(body); {
		lineEnd := strings.IndexByte(body[pos:], '\n')
		next := len(body)
		if lineEnd < 0 {
			lineEnd = len(body)
		} else {
			lineEnd += pos
			next = lineEnd + 1
		}
		p.line(pos, lineEnd)
		pos = next
	}
	p.endBlock()
	return p.blocks
}

func (p *blockPass) endBlock() {
	if p.blockStart >= 0 {
		p.blocks = append(p.blocks, [2]int{p.blockStart, p.blockEnd})
	}
	p.blockStart = -1
}

// indent returns the column after the spaces and tabs that start at pos,
// counting from column col, and the position of the first other byte.
func indent(body string, pos, end, col int) (int, int) {
	for pos < end {
		switch body[pos] {
		case ' ':
			col++
		case '\t':
			col = (col/4 + 1) * 4
		default:
			return col, pos
		}
		pos++
	}
	return col, pos
}

// isBlankFrom reports whether body[pos:end] holds only spaces, tabs and CR.
func isBlankFrom(body string, pos, end int) bool {
	for ; pos < end; pos++ {
		if c := body[pos]; c != ' ' && c != '\t' && c != '\r' {
			return false
		}
	}
	return true
}

func (p *blockPass) line(start, end int) {
	body := p.body
	q, qpos := quotePrefix(body, start, end)

	if p.fence != 0 {
		if q >= p.fenceQuote {
			w, k := indent(body, qpos, end, 0)
			if w < p.fenceBase+4 && closesFence(body, k, end, p.fence, p.fenceLen) {
				p.fence = 0
			}
			return
		}
		p.fence = 0
	}
	if p.comment {
		if strings.Contains(body[start:end], "-->") {
			p.comment = false
		}
		return
	}

	w, k := indent(body, qpos, end, 0)
	if isBlankFrom(body, k, end) {
		p.endBlock()
		p.para = false
		return
	}
	if q != p.quote {
		p.endBlock()
		p.para = false
		p.lists = p.lists[:0]
		p.quote = q
	}

	marker, contentPos, contentCol := listMarker(body, k, end, w)
	if !p.para || marker {
		for len(p.lists) > 0 && w < p.lists[len(p.lists)-1] {
			p.lists = p.lists[:len(p.lists)-1]
		}
	}
	base := 0
	if len(p.lists) > 0 {
		base = p.lists[len(p.lists)-1]
	}
	if w-base >= 4 {
		if p.para {
			p.blockEnd = end
			return
		}
		p.endBlock()
		return
	}
	if marker {
		p.endBlock()
		p.para = false
		p.lists = append(p.lists, contentCol)
		if isBlankFrom(body, contentPos, end) {
			return
		}
		k = contentPos
		base = contentCol
	}
	if c, n := opensFence(body, k, end); n > 0 {
		p.endBlock()
		p.para = false
		p.fence, p.fenceLen, p.fenceBase, p.fenceQuote = c, n, base, q
		return
	}
	if strings.HasPrefix(body[k:end], "<!--") {
		p.endBlock()
		p.para = false
		if !strings.Contains(body[k+2:end], "-->") {
			p.comment = true
		}
		return
	}
	if isATXHeading(body, k, end) {
		p.endBlock()
		p.para = false
		p.blockStart, p.blockEnd = k, end
		p.endBlock()
		return
	}
	if !p.para {
		p.endBlock()
		p.blockStart = k
	}
	p.blockEnd = end
	p.para = true
}

// quotePrefix returns the blockquote depth of a line and the position after
// its markers.
func quotePrefix(body string, pos, end int) (int, int) {
	q := 0
	for {
		k := pos
		for n := 0; k < end && body[k] == ' ' && n < 3; n++ {
			k++
		}
		if k >= end || body[k] != '>' {
			return q, pos
		}
		k++
		if k < end && (body[k] == ' ' || body[k] == '\t') {
			k++
		}
		pos = k
		q++
	}
}

// listMarker reports whether a list item marker starts at k, where the line
// content has column w. It returns the content position and column.
func listMarker(body string, k, end, w int) (bool, int, int) {
	m := k
	switch {
	case k < end && (body[k] == '-' || body[k] == '+' || body[k] == '*'):
		m = k + 1
	default:
		for m < end && m-k < 9 && body[m] >= '0' && body[m] <= '9' {
			m++
		}
		if m == k || m >= end || (body[m] != '.' && body[m] != ')') {
			return false, 0, 0
		}
		m++
	}
	if m < end && body[m] != ' ' && body[m] != '\t' && body[m] != '\r' {
		return false, 0, 0
	}
	markerCol := w + (m - k)
	col, pos := indent(body, m, end, markerCol)
	if pos >= end || body[pos] == '\r' || col-markerCol > 4 || col == markerCol {
		return true, m, markerCol + 1
	}
	return true, pos, col
}

// opensFence returns the fence character and length when a code fence opens
// at k, and 0 otherwise.
func opensFence(body string, k, end int) (byte, int) {
	if k >= end || (body[k] != '`' && body[k] != '~') {
		return 0, 0
	}
	c := body[k]
	n := run(body, k, end, c)
	if n < 3 {
		return 0, 0
	}
	if c == '`' && strings.IndexByte(body[k+n:end], '`') >= 0 {
		return 0, 0
	}
	return c, n
}

// closesFence reports whether a closing fence of at least n bytes c starts at k.
func closesFence(body string, k, end int, c byte, n int) bool {
	m := run(body, k, end, c)
	return m >= n && isBlankFrom(body, k+m, end)
}

// isATXHeading reports whether an ATX heading marker starts at k.
func isATXHeading(body string, k, end int) bool {
	n := run(body, k, end, '#')
	if n < 1 || n > 6 {
		return false
	}
	return k+n >= end || body[k+n] == ' ' || body[k+n] == '\t' || body[k+n] == '\r'
}

// run returns the number of consecutive bytes c starting at k.
func run(body string, k, end int, c byte) int {
	n := 0
	for k+n < end && body[k+n] == c {
		n++
	}
	return n
}

// isPunct reports whether c is ASCII punctuation, which a backslash escapes.
func isPunct(c byte) bool {
	return (c >= '!' && c <= '/') || (c >= ':' && c <= '@') || (c >= '[' && c <= '`') || (c >= '{' && c <= '~')
}

type opener struct {
	pos   int
	image bool
}

// scanInline appends the links in body[s:e] to out.
func scanInline(body string, s, e int, out []Link) []Link {
	first := len(out)
	var stack []opener
	active := 0 // non-image openers below this stack index are inactive
	lineEnd := -1
	wikiFrom, wikiClose := -1, -1 // a search from wikiFrom on the current line found wikiClose
	commentNone := -1             // no "-->" exists at or after this position
	i := s
	for i < e {
		switch body[i] {
		case '\\':
			if i+1 < e && isPunct(body[i+1]) {
				i += 2
			} else {
				i++
			}
		case '`':
			n := run(body, i, e, '`')
			if c := codeClose(body, i+n, e, n); c >= 0 {
				i = c
			} else {
				i += n
			}
		case '<':
			j := 0
			if strings.HasPrefix(body[i:e], "<!--") {
				if commentNone < 0 || i+2 < commentNone {
					if c := strings.Index(body[i+2:e], "-->"); c >= 0 {
						j = i + 2 + c + 3
					} else {
						commentNone = i + 2
					}
				}
			} else {
				j = autolinkEnd(body, i, e)
			}
			if j > 0 {
				i = j
			} else {
				i++
			}
		case '!', '[':
			image := body[i] == '!'
			at := i
			if image {
				at = i + 1
				if at >= e || body[at] != '[' {
					i++
					continue
				}
			}
			if at+1 < e && body[at+1] == '[' {
				if i >= lineEnd {
					lineEnd = strings.IndexByte(body[i:e], '\n')
					if lineEnd < 0 {
						lineEnd = e
					} else {
						lineEnd += i
					}
					wikiFrom, wikiClose = -1, -1
				}
				from := at + 2
				if wikiFrom < 0 || from < wikiFrom || (wikiClose >= 0 && wikiClose < from) {
					wikiFrom = from
					wikiClose = strings.Index(body[from:lineEnd], "]]")
					if wikiClose >= 0 {
						wikiClose += from
					}
				}
				if wikiClose >= 0 {
					if l, ok := wikilink(body, i, from, wikiClose, image); ok {
						out = append(out, l)
						if !image {
							active = len(stack)
						}
						i = l.End
						continue
					}
				}
			}
			stack = append(stack, opener{pos: i, image: image})
			i = at + 1
		case ']':
			if len(stack) == 0 {
				i++
				continue
			}
			op := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			inactive := len(stack) < active
			if active > len(stack) {
				active = len(stack)
			}
			if inactive {
				i++
				continue
			}
			l, ok := inlineLink(body, op, i, e)
			if !ok {
				i++
				continue
			}
			out = append(out, l)
			if !op.image {
				active = len(stack)
			}
			i = l.End
		default:
			i++
		}
	}
	tail := out[first:]
	sort.SliceStable(tail, func(a, b int) bool { return tail[a].Start < tail[b].Start })
	return out
}
