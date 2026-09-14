package markdown

import (
	"net/url"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// fuzzRenderer is shared by every fuzz input. A Renderer is safe for
// concurrent use.
var fuzzRenderer = New(nil)

// xssSeeds are markdown and HTML inputs aimed at the sanitizer.
var xssSeeds = []string{
	"",
	"# Title\n\nplain *text* with `code` and #tag\n",
	"<script>alert(1)</script>",
	"<SCRIPT SRC=//x.example/x.js></SCRIPT>",
	"<iframe src=\"https://x.example\"></iframe>",
	"<img src=x onerror=alert(1)>",
	"<a href=\"jav&#x09;ascript:alert(1)\">x</a>",
	"<a href=\"&#106;avascript:alert(1)\">x</a>",
	"[x](javascript:alert(1))",
	"[x](JaVaScRiPt:alert(1))",
	"[x](  javascript:alert(1))",
	"[x](data:text/html;base64,PHNjcmlwdD4=)",
	"![x](data:image/svg+xml;base64,PHN2Zz4=)",
	"<javascript:alert(1)>",
	"[[javascript:alert(1)]]",
	"[[Known|label]] [[missing]] ![[pic.png]] ![[Known.png]]",
	"```\"><script>alert(1)</script>\ncode\n```\n",
	"```go\nfunc main() {}\n```\n",
	"| a | b |\n| --- | --- |\n| <b>1</b> | 2 |\n",
	"- [ ] todo\n- [x] done\n",
	"text[^1]\n\n[^1]: note <svg onload=alert(1)>\n",
	"term\n: definition\n",
	"<svg><script>alert(1)</script></svg>",
	"<math><mi xlink:href=\"javascript:alert(1)\">x</mi></math>",
	"<form action=\"javascript:alert(1)\"><button formaction=javascript:alert(1)>x</button></form>",
	"[x](\\u0001javascript:alert(1))",
	"---\ntitle: x\n---\nbody #tag #Tag #tag/sub #a-\n",
	"<div onclick=\"alert(1)\">x</div>",
}

// fuzzResolve resolves every target that does not start with "missing".
func fuzzResolve(target string) (string, bool) {
	if strings.HasPrefix(target, "missing") {
		return "", false
	}
	return "/s/demo/n/" + target, true
}

// fuzzResolveItem resolves every target to a note, an image file or another
// file by its first byte, and hides targets starting with "missing". The
// target lands in the URL so escaping gets exercised.
func fuzzResolveItem(ref ports.LinkRef) (ports.LinkTarget, bool) {
	if strings.HasPrefix(ref.Target, "missing") || ref.Target == "" {
		return ports.LinkTarget{}, false
	}
	u := "/s/demo/f/" + url.PathEscape(ref.Target)
	switch ref.Target[0] % 3 {
	case 0:
		return ports.LinkTarget{Kind: core.KindNote, ID: "n", Name: ref.Target, URL: u}, true
	case 1:
		return ports.LinkTarget{Kind: core.KindFile, ID: "f", Name: ref.Target, MIME: "image/png", URL: u,
			FileURL: "/files/demo/f/" + url.PathEscape(ref.Target)}, true
	}
	return ports.LinkTarget{Kind: core.KindFile, ID: "g", Name: ref.Target, MIME: "application/pdf", URL: u,
		FileURL: "/files/demo/g/" + url.PathEscape(ref.Target)}, true
}

// FuzzRender checks that Render never fails, and that its output holds no
// script or iframe element, no event handler attribute and no javascript: or
// data: URL, with raw HTML both off and on.
func FuzzRender(f *testing.F) {
	for _, seed := range xssSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, md string) {
		for _, raw := range []bool{false, true} {
			out, err := fuzzRenderer.Render(md, ports.RenderOptions{
				ResolveLink:  fuzzResolve,
				Highlight:    true,
				HeadingIDs:   true,
				AllowRawHTML: raw,
			})
			if err != nil {
				t.Fatalf("Render(raw=%v) error: %v\ninput: %q", raw, err, md)
			}
			if problem := unsafeHTML(out.HTML); problem != "" {
				t.Fatalf("Render(raw=%v) output has %s\ninput: %q\noutput: %q", raw, problem, md, out.HTML)
			}
			out, err = fuzzRenderer.Render(md, ports.RenderOptions{
				ResolveItem:  fuzzResolveItem,
				Highlight:    true,
				AllowRawHTML: raw,
			})
			if err != nil {
				t.Fatalf("Render(items, raw=%v) error: %v\ninput: %q", raw, err, md)
			}
			if problem := unsafeHTML(out.HTML); problem != "" {
				t.Fatalf("Render(items, raw=%v) output has %s\ninput: %q\noutput: %q", raw, problem, md, out.HTML)
			}
		}
	})
}

// unsafeHTML walks the tokens of an HTML fragment and names the first unsafe
// construct, or returns "".
func unsafeHTML(fragment string) string {
	if strings.Contains(strings.ToLower(fragment), "<script") {
		return "a literal <script"
	}
	z := html.NewTokenizer(strings.NewReader(fragment))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			tok := z.Token()
			switch strings.ToLower(tok.Data) {
			case "script", "iframe", "frame", "object", "embed":
				return "a <" + tok.Data + "> element"
			}
			for _, a := range tok.Attr {
				key := strings.ToLower(a.Key)
				if strings.HasPrefix(key, "on") {
					return "an event attribute " + a.Key
				}
				switch key {
				case "href", "src", "action", "formaction", "xlink:href", "poster", "srcset":
					if scheme := dangerousScheme(a.Val); scheme != "" {
						return "a " + scheme + " URL in " + a.Key + "=" + a.Val
					}
				}
			}
		}
	}
}

// dangerousScheme reports a javascript:, vbscript: or data: URL the way a
// browser reads it: entities are already decoded by the tokenizer, leading
// control characters and spaces are ignored, tabs and newlines inside the
// scheme are ignored, and case does not matter.
func dangerousScheme(val string) string {
	v := strings.TrimLeft(val, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f ")
	v = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(v)
	v = strings.ToLower(v)
	for _, scheme := range []string{"javascript:", "vbscript:", "data:"} {
		if strings.HasPrefix(v, scheme) {
			return scheme
		}
	}
	return ""
}

// FuzzTextHelpers checks that the text extraction helpers never panic, that
// Chunks returns at most 200 chunks, and that InlineTags returns lowercase,
// deduplicated and sorted tags.
func FuzzTextHelpers(f *testing.F) {
	for _, seed := range xssSeeds {
		f.Add(seed, 40)
	}
	f.Add(strings.Repeat("word word word word word\n\n", 400), 1)
	f.Add("# A\n## B\n### C\ntext text text text text text\n", 0)
	f.Add("#ǅemal #İstanbul #ΣΑΣ #ẞ", -5)
	f.Fuzz(func(t *testing.T, md string, size int) {
		_ = fuzzRenderer.PlainText(md)
		_ = fuzzRenderer.Excerpt(md, size)
		_ = fuzzRenderer.WordCount(md)
		_ = fuzzRenderer.Links(md)

		target := size % 2000
		if chunks := fuzzRenderer.Chunks(md, target); len(chunks) > maxChunks {
			t.Fatalf("Chunks(size=%d) returned %d chunks", target, len(chunks))
		}

		tags := fuzzRenderer.InlineTags(md)
		if !sort.StringsAreSorted(tags) {
			t.Fatalf("InlineTags(%q) = %q is not sorted", md, tags)
		}
		for i, tag := range tags {
			if tag != strings.ToLower(tag) {
				t.Fatalf("InlineTags(%q) returned %q, which is not lowercase", md, tag)
			}
			if i > 0 && tags[i-1] == tag {
				t.Fatalf("InlineTags(%q) = %q has a duplicate", md, tags)
			}
		}
	})
}

// FuzzDiffHTML checks that a diff parses as HTML made only of div and span
// elements, so no markup from either input survives unescaped.
func FuzzDiffHTML(f *testing.F) {
	f.Add("<b>bold</b>\n", "<script>alert(1)</script>\n")
	f.Add("a\nb\nc\nd\ne\nf\ng\nh\n", "a\nb\nc\nX\ne\nf\ng\nh\n<b>")
	f.Add("", "<img src=x onerror=alert(1)>")
	f.Add("&lt;b&gt;", "\"'<>&")
	f.Fuzz(func(t *testing.T, oldText, newText string) {
		out := fuzzRenderer.DiffHTML(oldText, newText)
		if _, err := html.Parse(strings.NewReader(out)); err != nil {
			t.Fatalf("DiffHTML output does not parse: %v", err)
		}
		z := html.NewTokenizer(strings.NewReader(out))
		for {
			tt := z.Next()
			if tt == html.ErrorToken {
				return
			}
			if tt != html.StartTagToken && tt != html.EndTagToken && tt != html.SelfClosingTagToken {
				continue
			}
			tok := z.Token()
			if tok.Data != "div" && tok.Data != "span" {
				t.Fatalf("DiffHTML emitted <%s>\nold: %q\nnew: %q\nout: %q", tok.Data, oldText, newText, out)
			}
			for _, a := range tok.Attr {
				if a.Key != "class" {
					t.Fatalf("DiffHTML emitted attribute %s\nold: %q\nnew: %q\nout: %q", a.Key, oldText, newText, out)
				}
			}
		}
	})
}
