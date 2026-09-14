package markdown

import (
	"regexp"

	"github.com/microcosm-cc/bluemonday"
)

// policy is the one sanitizer policy used for every render. bluemonday
// policies are immutable once built and safe for concurrent use.
var policy = buildPolicy()

var (
	// idPattern matches the ids goldmark generates for headings, footnotes and
	// footnote references.
	idPattern = regexp.MustCompile(`^[A-Za-z0-9:_.\-]+$`)
	// rolePattern matches the ARIA roles goldmark emits for footnotes.
	rolePattern = regexp.MustCompile(`^[A-Za-z-]+$`)
	// checkboxType matches the only input type that survives.
	checkboxType = regexp.MustCompile(`(?i)^checkbox$`)
	// emptyOrName matches boolean attributes written either empty or by name.
	emptyOrName = regexp.MustCompile(`(?i)^(|checked|disabled|true)$`)
)

// buildPolicy assembles the sanitizer policy. It starts from the bluemonday
// user generated content policy and then permits exactly the markup this
// package emits.
func buildPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()

	// Class carries the chroma token classes, the wikilink state, the diff
	// line kinds and the footnote classes.
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).Globally()

	// Heading anchors, footnote definitions and footnote references need ids.
	p.AllowAttrs("id").Matching(idPattern).OnElements(
		"h1", "h2", "h3", "h4", "h5", "h6", "li", "sup", "a", "div", "section",
	)
	p.AllowAttrs("role").Matching(rolePattern).OnElements(
		"a", "div", "section", "li", "sup", "ol", "ul",
	)

	// GFM tables.
	p.AllowTables()

	// Task list checkboxes. They are always disabled, so they cannot submit.
	p.AllowAttrs("type").Matching(checkboxType).OnElements("input")
	p.AllowAttrs("checked", "disabled").Matching(emptyOrName).OnElements("input")

	// Images, with parseable sources only.
	p.AllowImages()
	p.AllowAttrs("title").Matching(bluemonday.Paragraph).OnElements("img")
	p.AllowAttrs("width", "height").Matching(bluemonday.NumberOrPercent).OnElements("img")

	// URLs: relative, or one of three schemes.
	p.RequireParseableURLs(true)
	p.AllowRelativeURLs(true)
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireNoFollowOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)

	return p
}

// Sanitize runs the package policy over an HTML fragment. It is exported so
// that callers which assemble HTML from several rendered notes can reuse the
// same policy.
func Sanitize(htmlFragment string) string {
	return policy.Sanitize(htmlFragment)
}
