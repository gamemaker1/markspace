package markdown

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/wikilink"

	"github.com/gamemaker1/markspace/internal/core"
)

// defaultExcerptRunes is used when Excerpt is called with a non-positive size.
const defaultExcerptRunes = 240

// PlainText strips markup, for the full-text index and embeddings. Code block
// text is kept, because searching code is useful. Raw HTML is dropped.
// Frontmatter at the start of the body is removed defensively.
func (r *Renderer) PlainText(md string) string {
	src := []byte(stripFrontmatter(md))
	doc := r.plain().Parser().Parse(text.NewReader(src))
	return plainTextOf(doc, src)
}

// plainTextOf walks a parsed document and joins its text. Inline pieces are
// separated by one space and blocks by one blank line.
func plainTextOf(doc ast.Node, src []byte) string {
	var b strings.Builder
	blockBreak := false
	add := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 {
			if blockBreak {
				b.WriteString("\n\n")
			} else {
				b.WriteByte(' ')
			}
		}
		blockBreak = false
		b.WriteString(s)
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.HTMLBlock, *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		case *ast.AutoLink:
			add(string(n.Label(src)))
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			add(string(n.Segment.Value(src)))
			return ast.WalkContinue, nil
		case *ast.String:
			add(string(n.Value))
			return ast.WalkContinue, nil
		case *ast.CodeBlock:
			blockBreak = b.Len() > 0
			add(linesText(n, src))
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock:
			blockBreak = b.Len() > 0
			add(linesText(n, src))
			return ast.WalkSkipChildren, nil
		}
		if n.Type() == ast.TypeBlock && b.Len() > 0 {
			blockBreak = true
		}
		return ast.WalkContinue, nil
	})
	return collapseSpace(b.String())
}

// Excerpt returns the first maxRunes runes of plain text, cut at the last word
// boundary and followed by a horizontal ellipsis when the text was cut.
// A non-positive maxRunes means 240.
func (r *Renderer) Excerpt(md string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = defaultExcerptRunes
	}
	plain := strings.Join(strings.Fields(r.PlainText(md)), " ")
	runes := []rune(plain)
	if len(runes) <= maxRunes {
		return plain
	}
	cut := string(runes[:maxRunes])
	if idx := strings.LastIndexByte(cut, ' '); idx > 0 {
		cut = cut[:idx]
	}
	return strings.TrimRight(cut, " ,;:.") + "…"
}

// WordCount counts words in the plain text.
func (r *Renderer) WordCount(md string) int {
	return len(strings.Fields(r.PlainText(md)))
}

// tagPattern matches one inline tag: a hash, then a letter, then up to 49 more
// letters, digits, hyphens, underscores or slashes.
var tagPattern = regexp.MustCompile(`^#\p{L}[\p{L}\p{N}_/-]{0,49}`)

// InlineTags returns the lowercased #hashtags found in the body, deduplicated
// and sorted. Tags inside code spans, code blocks, autolinks and link
// destinations are ignored, and so is a hash that follows a word character, as
// in a URL fragment.
func (r *Renderer) InlineTags(md string) []string {
	src := []byte(stripFrontmatter(md))
	doc := r.plain().Parser().Parse(text.NewReader(src))
	found := map[string]struct{}{}
	collectTags(src, doc, found)
	out := make([]string, 0, len(found))
	for tag := range found {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// collectTags walks the tree and scans the text outside code, autolinks, raw
// HTML and wikilinks. Adjacent text nodes are joined before scanning, because
// goldmark splits a run of text at every emphasis delimiter, as in
// "#snake_case".
func collectTags(src []byte, n ast.Node, found map[string]struct{}) {
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			scanTags(run.String(), found)
			run.Reset()
		}
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			run.Write(c.Segment.Value(src))
			if c.SoftLineBreak() || c.HardLineBreak() {
				run.WriteByte('\n')
			}
			continue
		case *ast.String:
			run.Write(c.Value)
			continue
		case *ast.CodeSpan, *ast.CodeBlock, *ast.FencedCodeBlock,
			*ast.AutoLink, *ast.HTMLBlock, *ast.RawHTML, *wikilink.Node:
			flush()
			continue
		}
		flush()
		collectTags(src, c, found)
	}
	flush()
}

// scanTags collects the tags of one text segment into found.
func scanTags(s string, found map[string]struct{}) {
	for i := 0; i < len(s); i++ {
		if s[i] != '#' {
			continue
		}
		if i > 0 {
			prev, _ := utf8.DecodeLastRuneInString(s[:i])
			if isTagBoundaryBlocker(prev) {
				continue
			}
		}
		m := tagPattern.FindString(s[i:])
		if m == "" {
			continue
		}
		rest := s[i+len(m):]
		if rest != "" {
			next, _ := utf8.DecodeRuneInString(rest)
			if isTagRune(next) {
				// Longer than the allowed 50 characters, so not a tag.
				i += len(m) - 1
				continue
			}
		}
		tag := strings.ToLower(strings.TrimRight(m[1:], "-_/"))
		if tag != "" {
			found[tag] = struct{}{}
		}
		i += len(m) - 1
	}
}

// isTagBoundaryBlocker reports whether a hash preceded by r cannot start a tag.
func isTagBoundaryBlocker(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsNumber(r) {
		return true
	}
	switch r {
	case '-', '_', '/', '#', ':', '=', '&', '?', '.', '%', '@':
		return true
	}
	return false
}

// isTagRune reports whether r may appear inside a tag.
func isTagRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsNumber(r) {
		return true
	}
	return r == '-' || r == '_' || r == '/'
}

// Links returns every [[wikilink]] and every relative markdown link, with the
// trailing ".md" stripped from the target and the link text as the label.
// Absolute and anchor-only destinations are skipped. The result keeps document
// order and is deduplicated by target and label.
func (r *Renderer) Links(md string) []core.WikiLink {
	src := []byte(stripFrontmatter(md))
	doc := r.plain().Parser().Parse(text.NewReader(src))
	var out []core.WikiLink
	seen := map[string]struct{}{}
	appendLink := func(target, label string) {
		if target == "" {
			return
		}
		key := target + "\x00" + label
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, core.WikiLink{Target: target, Label: label})
	}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *wikilink.Node:
			label := nodeText(src, n)
			target := linkTarget(string(n.Target))
			if label == "" {
				label = target
			}
			appendLink(target, label)
		case *ast.Link:
			dest := string(n.Destination)
			if !isRelativeDestination(dest) {
				return ast.WalkContinue, nil
			}
			appendLink(linkTarget(dest), nodeText(src, n))
		}
		return ast.WalkContinue, nil
	})
	return out
}

// linkTarget normalizes a link destination: the query and fragment are cut and
// a trailing ".md" is removed.
func linkTarget(dest string) string {
	dest = strings.TrimSpace(dest)
	if i := strings.IndexAny(dest, "#?"); i >= 0 {
		dest = dest[:i]
	}
	if unescaped, err := url.PathUnescape(dest); err == nil {
		dest = unescaped
	}
	return strings.TrimSuffix(dest, ".md")
}

// isRelativeDestination reports whether a markdown link destination points
// inside the space rather than at an external URL or an anchor on the page.
func isRelativeDestination(dest string) bool {
	dest = strings.TrimSpace(dest)
	if dest == "" || strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "//") {
		return false
	}
	u, err := url.Parse(dest)
	if err != nil {
		return false
	}
	return u.Scheme == "" && u.Host == ""
}

// stripFrontmatter drops a leading YAML frontmatter block. A body that starts
// with a "---" line loses everything through the next "---" line.
func stripFrontmatter(md string) string {
	body := strings.TrimLeft(md, "\ufeff")
	if !strings.HasPrefix(body, "---\n") && !strings.HasPrefix(body, "---\r\n") {
		return body
	}
	rest := body[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return body
	}
	after := rest[idx+4:]
	if nl := strings.IndexByte(after, '\n'); nl >= 0 {
		return after[nl+1:]
	}
	return ""
}

// collapseSpace collapses runs of whitespace. A run holding two or more line
// feeds becomes one blank line, every other run becomes one space.
func collapseSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		ru, size := utf8.DecodeRuneInString(s[i:])
		if !unicode.IsSpace(ru) {
			b.WriteRune(ru)
			i += size
			continue
		}
		newlines := 0
		for i < len(s) {
			r2, sz := utf8.DecodeRuneInString(s[i:])
			if !unicode.IsSpace(r2) {
				break
			}
			if r2 == '\n' {
				newlines++
			}
			i += sz
		}
		if b.Len() == 0 {
			continue
		}
		if newlines >= 2 {
			b.WriteString("\n\n")
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.TrimRight(b.String(), " \n")
}
