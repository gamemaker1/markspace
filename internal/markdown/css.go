package markdown

import (
	"bytes"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
)

// The chroma style and the CSS scope it is written under. The page never sets
// a dark class. markdown.css colours the chroma classes with Primer's
// prettylights tokens, which follow data-color-mode in both themes, so only
// the light style ships here as a fallback.
const (
	lightStyle = "github"
	// lightScope scopes the rules to the rendered note container.
	lightScope = ".md-body"
)

// buildCSS renders the chroma class stylesheet and appends the static rules
// for diffs and dangling wikilinks.
func buildCSS() string {
	var b strings.Builder
	b.WriteString("/* syntax highlighting */\n")
	b.WriteString(chromaCSS(lightStyle, lightScope))
	b.WriteString("\n")
	b.WriteString(staticCSS)
	return b.String()
}

// chromaCSS writes the class stylesheet of one chroma style and prefixes every
// selector with scope. Chroma cannot scope its own output, so the rules are
// rewritten here. Each rule chroma writes occupies one line of the form
// "selector { declarations }".
func chromaCSS(styleName, scope string) string {
	style := styles.Get(styleName)
	if style == nil {
		style = styles.Fallback
	}
	var raw bytes.Buffer
	formatter := chromahtml.New(chromahtml.WithClasses(true), chromahtml.WithCSSComments(false))
	if err := formatter.WriteCSS(&raw, style); err != nil {
		return ""
	}

	var b strings.Builder
	for _, line := range strings.Split(raw.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		brace := strings.Index(line, "{")
		if brace < 0 {
			continue
		}
		selectors := strings.Split(line[:brace], ",")
		for i, sel := range selectors {
			selectors[i] = scope + " " + strings.TrimSpace(sel)
		}
		b.WriteString(strings.Join(selectors, ", "))
		b.WriteString(" ")
		b.WriteString(strings.TrimSpace(line[brace:]))
		b.WriteString("\n")
	}
	return b.String()
}

// staticCSS holds the rules this package needs beyond syntax highlighting.
// Colours come from Primer functional tokens, so they follow the theme. The
// second value of each var() is the light colour for a page without Primer.
const staticCSS = `/* dangling wikilinks */
.md-body a.wikilink { text-decoration: none; border-bottom: 1px solid currentColor; }
.md-body a.wikilink-new { color: var(--fgColor-danger, #b4483c); border-bottom-style: dashed; }
.md-body img.wikilink-embed { max-width: 100%; height: auto; }

/* version diffs */
.md-body .diff { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.85rem; line-height: 1.5; overflow-x: auto; }
.md-body .diff-line { white-space: pre-wrap; padding: 0 0.4rem; }
.md-body .diff-mark { display: inline-block; width: 1ch; user-select: none; opacity: 0.6; }
.md-body .diff-add { background: var(--diffBlob-additionLine-bgColor, #e6ffec); }
.md-body .diff-del { background: var(--diffBlob-deletionLine-bgColor, #ffebe9); }
.md-body .diff-eq { background: transparent; }
.md-body .diff-skip { padding: 0.2rem 0.4rem; text-align: center; opacity: 0.6; font-style: italic; }
`
