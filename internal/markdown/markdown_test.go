package markdown

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/ports"
)

// testResolver resolves the targets "Known" and "Deep/Note" and nothing else.
func testResolver(target string) (string, bool) {
	switch target {
	case "Known":
		return "/s/demo/known", true
	case "Deep/Note":
		return "/s/demo/deep/note", true
	}
	return "", false
}

func renderOpts() ports.RenderOptions {
	return ports.RenderOptions{
		ResolveLink: testResolver,
		Highlight:   true,
		HeadingIDs:  true,
	}
}

func TestRenderHTML(t *testing.T) {
	r := New(nil)
	tests := []struct {
		name     string
		md       string
		opts     ports.RenderOptions
		contains []string
		absent   []string
	}{
		{
			name: "gfm table",
			md:   "| a | b |\n| --- | --- |\n| 1 | 2 |\n",
			opts: renderOpts(),
			contains: []string{
				"<table>", "<thead>", "<th>a</th>", "<td>1</td>", "</table>",
			},
		},
		{
			name: "gfm strikethrough and autolink",
			md:   "~~gone~~ and https://example.test/page\n",
			opts: renderOpts(),
			contains: []string{
				"<del>gone</del>",
				`href="https://example.test/page"`,
			},
		},
		{
			name: "task list",
			md:   "- [ ] open\n- [x] done\n",
			opts: renderOpts(),
			contains: []string{
				`<input disabled="" type="checkbox">`,
				`<input checked="" disabled="" type="checkbox">`,
			},
		},
		{
			name:     "definition list",
			md:       "term\n: definition\n",
			opts:     renderOpts(),
			contains: []string{"<dl>", "<dt>term</dt>", "<dd>definition</dd>"},
		},
		{
			name:     "footnote",
			md:       "body[^1]\n\n[^1]: note text\n",
			opts:     renderOpts(),
			contains: []string{`id="fnref:1"`, `class="footnote-ref"`, `id="fn:1"`, "note text"},
		},
		{
			name:     "highlighted code emits chroma classes",
			md:       "```go\nfunc main() {}\n```\n",
			opts:     renderOpts(),
			contains: []string{`<pre class="chroma">`, `<span class="kd">func</span>`},
			absent:   []string{"style="},
		},
		{
			name:     "unhighlighted code keeps language class",
			md:       "```go\nfunc main() {}\n```\n",
			opts:     ports.RenderOptions{},
			contains: []string{`<code class="language-go">`, "func main() {}"},
			absent:   []string{"chroma"},
		},
		{
			name:     "hard wraps stay off",
			md:       "one\ntwo\n",
			opts:     renderOpts(),
			absent:   []string{"<br"},
			contains: []string{"one\ntwo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Render(tt.md, tt.opts)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got.HTML, want) {
					t.Errorf("HTML missing %q\ngot: %s", want, got.HTML)
				}
			}
			for _, bad := range tt.absent {
				if strings.Contains(got.HTML, bad) {
					t.Errorf("HTML should not contain %q\ngot: %s", bad, got.HTML)
				}
			}
		})
	}
}

func TestRenderTaskCounts(t *testing.T) {
	r := New(nil)
	tests := []struct {
		name      string
		md        string
		tasks     int
		tasksDone int
	}{
		{name: "none", md: "plain text\n"},
		{name: "one open", md: "- [ ] a\n", tasks: 1},
		{name: "mixed", md: "- [ ] a\n- [x] b\n- [X] c\n", tasks: 3, tasksDone: 2},
		{name: "nested", md: "- [x] a\n  - [ ] b\n", tasks: 2, tasksDone: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Render(tt.md, renderOpts())
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got.Tasks != tt.tasks || got.TasksDone != tt.tasksDone {
				t.Errorf("tasks = %d/%d, want %d/%d", got.TasksDone, got.Tasks, tt.tasksDone, tt.tasks)
			}
		})
	}
}

func TestRenderTOC(t *testing.T) {
	r := New(nil)
	md := "# One\n\n## Two words\n\n### Three\n\n#### Four\n\n##### Five\n"

	got, err := r.Render(md, renderOpts())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []core.Heading{
		{Level: 1, Text: "One", ID: "one"},
		{Level: 2, Text: "Two words", ID: "two-words"},
		{Level: 3, Text: "Three", ID: "three"},
		{Level: 4, Text: "Four", ID: "four"},
	}
	if !reflect.DeepEqual(got.TOC, want) {
		t.Errorf("TOC = %+v, want %+v", got.TOC, want)
	}
	for _, h := range want {
		if !strings.Contains(got.HTML, fmt.Sprintf(`id="%s"`, h.ID)) {
			t.Errorf("heading id %q did not survive sanitization: %s", h.ID, got.HTML)
		}
	}

	off, err := r.Render(md, ports.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(off.TOC) != 0 {
		t.Errorf("TOC = %+v, want empty when HeadingIDs is off", off.TOC)
	}
	if strings.Contains(off.HTML, "id=") {
		t.Errorf("HTML should carry no ids when HeadingIDs is off: %s", off.HTML)
	}
}

func TestRenderWikilinks(t *testing.T) {
	r := New(nil)
	tests := []struct {
		name     string
		md       string
		opts     ports.RenderOptions
		contains []string
		links    []core.WikiLink
	}{
		{
			name: "resolved",
			md:   "see [[Known]]\n",
			opts: renderOpts(),
			contains: []string{
				`<a href="/s/demo/known" class="wikilink"`,
				">Known</a>",
			},
			links: []core.WikiLink{{Target: "Known", Label: "Known"}},
		},
		{
			name:     "dangling",
			md:       "see [[Missing]]\n",
			opts:     renderOpts(),
			contains: []string{`class="wikilink wikilink-new"`, `href="#"`, ">Missing</a>"},
			links:    []core.WikiLink{{Target: "Missing", Label: "Missing"}},
		},
		{
			name:     "labelled resolved",
			md:       "see [[Known|the label]]\n",
			opts:     renderOpts(),
			contains: []string{`<a href="/s/demo/known" class="wikilink"`, ">the label</a>"},
			links:    []core.WikiLink{{Target: "Known", Label: "the label"}},
		},
		{
			name:     "labelled dangling",
			md:       "see [[a|b]]\n",
			opts:     renderOpts(),
			contains: []string{`class="wikilink wikilink-new"`, ">b</a>"},
			links:    []core.WikiLink{{Target: "a", Label: "b"}},
		},
		{
			name:     "fragment kept on resolved target",
			md:       "see [[Deep/Note#part]]\n",
			opts:     renderOpts(),
			contains: []string{`href="/s/demo/deep/note#part"`},
			links:    []core.WikiLink{{Target: "Deep/Note", Label: "Deep/Note#part"}},
		},
		{
			name:     "nil resolver marks every link dangling",
			md:       "see [[Known]]\n",
			opts:     ports.RenderOptions{},
			contains: []string{`class="wikilink wikilink-new"`},
			links:    []core.WikiLink{{Target: "Known", Label: "Known"}},
		},
		{
			name:     "two links keep document order",
			md:       "[[Known]] then [[Missing]]\n",
			opts:     renderOpts(),
			contains: []string{"Known", "Missing"},
			links: []core.WikiLink{
				{Target: "Known", Label: "Known"},
				{Target: "Missing", Label: "Missing"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Render(tt.md, tt.opts)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got.HTML, want) {
					t.Errorf("HTML missing %q\ngot: %s", want, got.HTML)
				}
			}
			if !reflect.DeepEqual(got.Links, tt.links) {
				t.Errorf("Links = %+v, want %+v", got.Links, tt.links)
			}
			for _, l := range got.Links {
				if l.NoteID != "" {
					t.Errorf("NoteID = %q, want empty", l.NoteID)
				}
			}
		})
	}
}

func TestRenderSanitizes(t *testing.T) {
	r := New(nil)
	tests := []struct {
		name     string
		md       string
		absent   []string
		contains []string
	}{
		{
			name:   "script tag",
			md:     "before\n\n<script>alert(1)</script>\n\nafter\n",
			absent: []string{"<script", "alert(1)"},
		},
		{
			name:   "event handler",
			md:     "<p onclick=\"steal()\">text</p>\n",
			absent: []string{"onclick", "steal()"},
		},
		{
			name:   "javascript href",
			md:     "[click](javascript:alert(1))\n",
			absent: []string{"javascript:"},
		},
		{
			name:   "iframe",
			md:     "<iframe src=\"https://evil.test\"></iframe>\n",
			absent: []string{"<iframe", "evil.test"},
		},
		{
			name:   "inline event handler in raw html span",
			md:     "text <span onmouseover=\"x\">hover</span>\n",
			absent: []string{"onmouseover"},
		},
		{
			name:     "chroma spans and heading ids survive",
			md:       "# Kept heading\n\n```go\nvar x = 1\n```\n",
			contains: []string{`id="kept-heading"`, `class="chroma"`, `<span class="kd">var</span>`},
		},
		{
			name:     "external link gets nofollow and target",
			md:       "[x](https://example.test/)\n",
			contains: []string{`rel="nofollow`, `target="_blank"`},
		},
		{
			name:     "image keeps src alt and title",
			md:       "![alt text](pics/a.png \"the title\")\n",
			contains: []string{`src="pics/a.png"`, `alt="alt text"`, `title="the title"`},
		},
	}
	for _, tt := range tests {
		for _, raw := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/raw=%v", tt.name, raw), func(t *testing.T) {
				opts := renderOpts()
				opts.AllowRawHTML = raw
				got, err := r.Render(tt.md, opts)
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				for _, bad := range tt.absent {
					if strings.Contains(got.HTML, bad) {
						t.Errorf("HTML should not contain %q\ngot: %s", bad, got.HTML)
					}
				}
				for _, want := range tt.contains {
					if !strings.Contains(got.HTML, want) {
						t.Errorf("HTML missing %q\ngot: %s", want, got.HTML)
					}
				}
			})
		}
	}
}

func TestRenderRawHTMLOption(t *testing.T) {
	r := New(nil)
	const md = "<p class=\"note\">kept</p>\n"

	off, err := r.Render(md, ports.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(off.HTML, "kept") {
		t.Errorf("raw HTML should be dropped when AllowRawHTML is off: %s", off.HTML)
	}

	on, err := r.Render(md, ports.RenderOptions{AllowRawHTML: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(on.HTML, "kept") {
		t.Errorf("raw HTML should survive sanitization when AllowRawHTML is on: %s", on.HTML)
	}
}

func TestPlainText(t *testing.T) {
	r := New(nil)
	tests := []struct {
		name string
		md   string
		want string
	}{
		{
			name: "inline markup",
			md:   "A **bold** and *italic* and `code` word.\n",
			want: "A bold and italic and code word.",
		},
		{
			name: "frontmatter stripped",
			md:   "---\ntitle: Note\ntags: [a, b]\n---\n\nbody text\n",
			want: "body text",
		},
		{
			name: "frontmatter without trailing blank line",
			md:   "---\nid: 1\n---\nbody text\n",
			want: "body text",
		},
		{
			name: "blocks separated by a blank line",
			md:   "# Heading\n\nfirst para\n\nsecond para\n",
			want: "Heading\n\nfirst para\n\nsecond para",
		},
		{
			name: "code block text is kept",
			md:   "para\n\n```go\nfunc f() {}\n```\n",
			want: "para\n\nfunc f() {}",
		},
		{
			name: "html blocks are dropped",
			md:   "para\n\n<div><b>markup</b></div>\n\ntail\n",
			want: "para\n\ntail",
		},
		{
			name: "links keep their label",
			md:   "see [the label](https://example.test/x) now\n",
			want: "see the label now",
		},
		{
			name: "wikilinks keep their label",
			md:   "see [[Target|label here]] now\n",
			want: "see label here now",
		},
		{
			name: "whitespace runs collapse",
			md:   "a     b\t\tc\n",
			want: "a b c",
		},
		{
			name: "table cells",
			md:   "| a | b |\n| --- | --- |\n| 1 | 2 |\n",
			want: "a\n\nb\n\n1\n\n2",
		},
		{
			name: "empty body",
			md:   "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.PlainText(tt.md); got != tt.want {
				t.Errorf("PlainText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExcerpt(t *testing.T) {
	r := New(nil)
	long := strings.Repeat("alpha beta gamma delta ", 40)
	tests := []struct {
		name     string
		md       string
		maxRunes int
		want     string
	}{
		{name: "short text is returned whole", md: "just a few words", maxRunes: 100, want: "just a few words"},
		{name: "cut on a word boundary", md: "one two three four five", maxRunes: 12, want: "one two…"},
		{name: "cut drops the trailing comma", md: "one, two, three, four", maxRunes: 10, want: "one, two…"},
		{name: "markup is stripped first", md: "# Title\n\n**bold** words here\n", maxRunes: 11, want: "Title bold…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Excerpt(tt.md, tt.maxRunes); got != tt.want {
				t.Errorf("Excerpt = %q, want %q", got, tt.want)
			}
		})
	}

	got := r.Excerpt(long, 0)
	if n := len([]rune(got)); n > defaultExcerptRunes+1 {
		t.Errorf("default excerpt is %d runes, want at most %d", n, defaultExcerptRunes+1)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("cut excerpt should end with an ellipsis, got %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("excerpt should be one line, got %q", got)
	}
}

func TestWordCount(t *testing.T) {
	r := New(nil)
	tests := []struct {
		name string
		md   string
		want int
	}{
		{name: "empty", md: "", want: 0},
		{name: "plain", md: "one two three", want: 3},
		{name: "markup does not count", md: "# Title\n\n**one** two\n", want: 3},
		{name: "frontmatter does not count", md: "---\ntitle: x\n---\n\none two\n", want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.WordCount(tt.md); got != tt.want {
				t.Errorf("WordCount = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestInlineTags(t *testing.T) {
	r := New(nil)
	tests := []struct {
		name string
		md   string
		want []string
	}{
		{name: "none", md: "no tags here", want: []string{}},
		{name: "simple", md: "about #golang today", want: []string{"golang"}},
		{name: "lowercased and deduplicated", md: "#Alpha and #alpha and #ALPHA", want: []string{"alpha"}},
		{name: "sorted", md: "#zulu #alpha #mike", want: []string{"alpha", "mike", "zulu"}},
		{name: "slash and dash and underscore", md: "#area/sub #two-part #snake_case", want: []string{"area/sub", "snake_case", "two-part"}},
		{name: "must start with a letter", md: "#1234 #-x #_y", want: []string{}},
		{name: "code span ignored", md: "`#nope` but #yes", want: []string{"yes"}},
		{name: "fenced block ignored", md: "```\n#nope\n```\n\n#yes\n", want: []string{"yes"}},
		{name: "indented code block ignored", md: "    #nope\n\n#yes\n", want: []string{"yes"}},
		{name: "url fragment ignored", md: "https://example.test/p#nope and #yes", want: []string{"yes"}},
		{name: "bare url fragment ignored", md: "example.test/p#nope and #yes", want: []string{"yes"}},
		{name: "link destination ignored", md: "[t](/a/b#nope) and #yes", want: []string{"yes"}},
		{name: "markdown heading is not a tag", md: "# Heading\n\n#yes\n", want: []string{"yes"}},
		{name: "trailing punctuation", md: "ends with #yes.", want: []string{"yes"}},
		{name: "too long is skipped", md: "#" + strings.Repeat("a", 60), want: []string{}},
		{name: "at the limit is kept", md: "#" + strings.Repeat("a", 50), want: []string{strings.Repeat("a", 50)}},
		{name: "wikilink target ignored", md: "[[Note#nope]] and #yes", want: []string{"yes"}},
		{name: "frontmatter ignored", md: "---\ntags: [#nope]\n---\n\n#yes\n", want: []string{"yes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.InlineTags(tt.md)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("InlineTags = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLinks(t *testing.T) {
	r := New(nil)
	tests := []struct {
		name string
		md   string
		want []core.WikiLink
	}{
		{
			name: "relative markdown link loses the md suffix",
			md:   "[Install](guides/install.md)\n",
			want: []core.WikiLink{{Target: "guides/install", Label: "Install"}},
		},
		{
			name: "absolute links are skipped",
			md:   "[a](https://example.test/x) [b](http://example.test) [c](mailto:me@example.test)\n",
			want: nil,
		},
		{
			name: "anchor only links are skipped",
			md:   "[top](#top)\n",
			want: nil,
		},
		{
			name: "protocol relative links are skipped",
			md:   "[a](//example.test/x)\n",
			want: nil,
		},
		{
			name: "wikilinks are included",
			md:   "[[Some Note]] and [[other/note|Other]]\n",
			want: []core.WikiLink{
				{Target: "Some Note", Label: "Some Note"},
				{Target: "other/note", Label: "Other"},
			},
		},
		{
			name: "fragment is cut from the target",
			md:   "[Part](guides/install.md#step-2)\n",
			want: []core.WikiLink{{Target: "guides/install", Label: "Part"}},
		},
		{
			name: "duplicates are removed",
			md:   "[a](x.md) and [a](x.md) and [b](x.md)\n",
			want: []core.WikiLink{
				{Target: "x", Label: "a"},
				{Target: "x", Label: "b"},
			},
		},
		{
			name: "percent escapes are decoded",
			md:   "[a](my%20note.md)\n",
			want: []core.WikiLink{{Target: "my note", Label: "a"}},
		},
		{
			name: "frontmatter is ignored",
			md:   "---\nparent: [x](y.md)\n---\n\n[a](b.md)\n",
			want: []core.WikiLink{{Target: "b", Label: "a"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Links(tt.md)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Links = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDiffHTML(t *testing.T) {
	r := New(nil)
	tests := []struct {
		name     string
		old      string
		new      string
		contains []string
		absent   []string
	}{
		{
			name: "added line",
			old:  "one\n",
			new:  "one\ntwo\n",
			contains: []string{
				`<div class="diff">`,
				`<div class="diff-line diff-eq"><span class="diff-mark"> </span>one</div>`,
				`<div class="diff-line diff-add"><span class="diff-mark">+</span>two</div>`,
			},
		},
		{
			name: "deleted line",
			old:  "one\ntwo\n",
			new:  "one\n",
			contains: []string{
				`<div class="diff-line diff-del"><span class="diff-mark">-</span>two</div>`,
			},
		},
		{
			name: "changed line shows both sides",
			old:  "alpha\n",
			new:  "beta\n",
			contains: []string{
				`diff-del"><span class="diff-mark">-</span>alpha`,
				`diff-add"><span class="diff-mark">+</span>beta`,
			},
		},
		{
			name:     "html is escaped",
			old:      "plain\n",
			new:      "<b>bold</b> & \"quoted\"\n",
			contains: []string{"&lt;b&gt;bold&lt;/b&gt; &amp;"},
			absent:   []string{"<b>bold"},
		},
		{
			name:     "identical text",
			old:      "same\n",
			new:      "same\n",
			contains: []string{`diff-eq"><span class="diff-mark"> </span>same`},
			absent:   []string{"diff-add", "diff-del"},
		},
		{
			name:     "empty inputs",
			old:      "",
			new:      "",
			contains: []string{`<div class="diff"></div>`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.DiffHTML(tt.old, tt.new)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("diff missing %q\ngot: %s", want, got)
				}
			}
			for _, bad := range tt.absent {
				if strings.Contains(got, bad) {
					t.Errorf("diff should not contain %q\ngot: %s", bad, got)
				}
			}
		})
	}
}

func TestDiffHTMLCollapse(t *testing.T) {
	r := New(nil)
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	same := b.String()
	got := r.DiffHTML(same, same+"tail\n")

	hidden := 20 - 2*diffContextLines
	want := fmt.Sprintf(`<div class="diff-skip">… %d unchanged lines …</div>`, hidden)
	if !strings.Contains(got, want) {
		t.Errorf("diff missing collapse marker %q\ngot: %s", want, got)
	}
	if strings.Contains(got, "line 10") {
		t.Errorf("collapsed lines should be hidden\ngot: %s", got)
	}
	for _, keep := range []string{"line 0", "line 1", "line 2", "line 17", "line 18", "line 19"} {
		if !strings.Contains(got, keep) {
			t.Errorf("context line %q should stay visible\ngot: %s", keep, got)
		}
	}
	if strings.Count(got, "diff-skip") != 1 {
		t.Errorf("want exactly one collapsed run, got: %s", got)
	}

	short := "a\nb\nc\nd\ne\nf\n"
	if out := r.DiffHTML(short, short+"g\n"); strings.Contains(out, "diff-skip") {
		t.Errorf("a run of six lines should not collapse\ngot: %s", out)
	}
}

func TestChunks(t *testing.T) {
	r := New(nil)
	para := "This paragraph is long enough to be kept as a chunk of its own text."

	t.Run("short body is one chunk without a prefix", func(t *testing.T) {
		got := r.Chunks(para, 0)
		if len(got) != 1 || got[0] != para {
			t.Fatalf("Chunks = %q", got)
		}
	})

	t.Run("tiny body is dropped", func(t *testing.T) {
		if got := r.Chunks("too short", 0); len(got) != 0 {
			t.Errorf("Chunks = %q, want none", got)
		}
	})

	t.Run("heading path is carried", func(t *testing.T) {
		md := "# Top\n\n## Section two\n\n" + para + "\n"
		got := r.Chunks(md, 0)
		if len(got) != 1 {
			t.Fatalf("Chunks = %q, want one chunk", got)
		}
		want := "# Top > ## Section two\n\n" + para
		if got[0] != want {
			t.Errorf("chunk = %q, want %q", got[0], want)
		}
	})

	t.Run("sibling heading replaces the tail of the path", func(t *testing.T) {
		md := "# Top\n\n## A\n\n" + para + "\n\n## B\n\n" + para + "\n"
		got := r.Chunks(md, 0)
		if len(got) != 2 {
			t.Fatalf("Chunks = %q, want two chunks", got)
		}
		if !strings.HasPrefix(got[0], "# Top > ## A\n\n") {
			t.Errorf("chunk 0 prefix = %q", got[0])
		}
		if !strings.HasPrefix(got[1], "# Top > ## B\n\n") {
			t.Errorf("chunk 1 prefix = %q", got[1])
		}
	})

	t.Run("fenced block is never split", func(t *testing.T) {
		fence := "```go\n" + strings.Repeat("var x = 1 // padding line\n", 12) + "```"
		md := para + "\n\n" + fence + "\n\n" + para + "\n"
		got := r.Chunks(md, 80)
		if len(got) < 2 {
			t.Fatalf("Chunks = %d, want at least two", len(got))
		}
		fenced := 0
		for _, c := range got {
			opens := strings.Count(c, "```")
			if opens == 0 {
				continue
			}
			fenced++
			if opens%2 != 0 {
				t.Errorf("chunk holds an unbalanced fence: %q", c)
			}
			if !strings.Contains(c, "```go\n"+strings.Repeat("var x = 1 // padding line\n", 12)+"```") {
				t.Errorf("fenced block was split: %q", c)
			}
		}
		if fenced != 1 {
			t.Errorf("the fence should appear in exactly one chunk, got %d", fenced)
		}
	})

	t.Run("blank line inside a fence does not split", func(t *testing.T) {
		md := "```\nfirst line of code here\n\nsecond line of code here\n```\n\n" + para + "\n"
		got := r.Chunks(md, 40)
		if len(got) != 2 {
			t.Fatalf("Chunks = %q, want two chunks", got)
		}
		if !strings.Contains(got[0], "first line of code here\n\nsecond line of code here") {
			t.Errorf("fence was split on its blank line: %q", got[0])
		}
	})

	t.Run("size driven split carries an overlap", func(t *testing.T) {
		md := para + "\n\n" + para + "\n\n" + para + "\n"
		got := r.Chunks(md, 80)
		if len(got) < 2 {
			t.Fatalf("Chunks = %d, want at least two", len(got))
		}
		tail := got[0][len(got[0])-10:]
		if !strings.Contains(got[1], strings.TrimSpace(tail)) {
			t.Errorf("chunk 1 %q does not overlap the tail of chunk 0 %q", got[1], tail)
		}
	})

	t.Run("at most 200 chunks", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 400; i++ {
			fmt.Fprintf(&b, "## Section %d\n\n%s\n\n", i, para)
		}
		got := r.Chunks(b.String(), 100)
		if len(got) != maxChunks {
			t.Errorf("Chunks = %d, want %d", len(got), maxChunks)
		}
	})

	t.Run("frontmatter is dropped", func(t *testing.T) {
		md := "---\ntitle: Secret Frontmatter Value\n---\n\n" + para + "\n"
		got := r.Chunks(md, 0)
		if len(got) != 1 || strings.Contains(got[0], "Secret Frontmatter Value") {
			t.Errorf("Chunks = %q, want the frontmatter dropped", got)
		}
	})

	t.Run("default target", func(t *testing.T) {
		md := strings.Repeat(para+"\n\n", 60)
		got := r.Chunks(md, -1)
		if len(got) < 2 {
			t.Fatalf("Chunks = %d, want several", len(got))
		}
		for _, c := range got[:len(got)-1] {
			if n := len([]rune(c)); n > defaultChunkRunes*2 {
				t.Errorf("chunk of %d runes is far past the default target", n)
			}
		}
	})
}

func TestCSS(t *testing.T) {
	r := New(nil)
	css := r.CSS()
	for _, want := range []string{
		".md-body .chroma",
		".md-body .diff-add",
		".md-body .diff-del",
		".md-body .diff-skip",
		".md-body a.wikilink-new",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("CSS missing a rule for %q", want)
		}
	}
	if strings.Contains(css, "/* Background */") {
		t.Errorf("CSS should carry no chroma comments")
	}
	// The page never sets a dark class, so a .dark scope would never apply.
	// Dark colours come from Primer tokens that follow data-color-mode.
	if strings.Contains(css, ".dark") {
		t.Errorf("CSS still scopes rules to a .dark class")
	}
	for _, token := range []string{"var(--fgColor-danger", "var(--diffBlob-additionLine-bgColor", "var(--diffBlob-deletionLine-bgColor"} {
		if !strings.Contains(css, token) {
			t.Errorf("CSS does not use the Primer token %q", token)
		}
	}
	for _, line := range strings.Split(css, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "/*") {
			continue
		}
		if !strings.HasPrefix(line, ".md-body") {
			t.Errorf("unscoped CSS rule: %q", line)
		}
	}
}

func TestRenderConcurrent(t *testing.T) {
	r := New(nil)
	md := "# Heading one\n\n" +
		"- [x] done\n- [ ] open\n\n" +
		"See [[Known]] and [[Missing|label]].\n\n" +
		"```go\nfunc main() { println(\"hi\") }\n```\n\n" +
		"| a | b |\n| --- | --- |\n| 1 | 2 |\n"

	want, err := r.Render(md, renderOpts())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				got, err := r.Render(md, renderOpts())
				if err != nil {
					t.Errorf("Render: %v", err)
					return
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("concurrent render differs:\ngot  %+v\nwant %+v", got, want)
					return
				}
				_ = r.PlainText(md)
				_ = r.InlineTags(md)
				_ = r.Links(md)
				_ = r.Chunks(md, 0)
				_ = r.CSS()
			}
		}()
	}
	wg.Wait()
}

func TestNewImplementsPort(t *testing.T) {
	var renderer ports.Renderer = New(nil)
	if renderer == nil {
		t.Fatal("New returned nil")
	}
	if _, err := renderer.Render("ok", ports.RenderOptions{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
}

// itemResolver resolves a fixed set of items and records every call.
type itemResolver struct {
	mu   sync.Mutex
	refs []ports.LinkRef
}

func (r *itemResolver) resolve(ref ports.LinkRef) (ports.LinkTarget, bool) {
	r.mu.Lock()
	r.refs = append(r.refs, ref)
	r.mu.Unlock()
	switch ref.Target {
	case "Guide":
		return ports.LinkTarget{Kind: core.KindNote, ID: "n1", Name: "guide.md", URL: "/s/demo/n/guide"}, true
	case "Projects/Plan":
		return ports.LinkTarget{Kind: core.KindNote, ID: "n4", Name: "plan.md", URL: "/s/demo/n/Projects/plan"}, true
	case "Projects":
		return ports.LinkTarget{Kind: core.KindFolder, ID: "d1", Name: "Projects", URL: "/s/demo/d/Projects"}, true
	case "east wall.jpg":
		return ports.LinkTarget{Kind: core.KindFile, ID: "f1", Name: "east wall.jpg", MIME: "image/jpeg",
			URL: "/s/demo/f/east%20wall.jpg", FileURL: "/files/demo/f1/east%20wall.jpg"}, true
	case "report.pdf":
		return ports.LinkTarget{Kind: core.KindFile, ID: "f2", Name: "report.pdf", MIME: "application/pdf",
			URL: "/s/demo/f/report.pdf", FileURL: "/files/demo/f2/report.pdf"}, true
	case "logo.svg":
		return ports.LinkTarget{Kind: core.KindFile, ID: "f3", Name: "logo.svg", MIME: "image/svg+xml",
			URL: "/s/demo/f/logo.svg", FileURL: "/files/demo/f3/logo.svg"}, true
	case "café.png":
		return ports.LinkTarget{Kind: core.KindFile, ID: "f4", Name: "café.png", MIME: "image/png",
			URL: "/s/demo/f/café.png", FileURL: "/files/demo/f4/café.png"}, true
	}
	return ports.LinkTarget{}, false
}

func TestRenderItemLinks(t *testing.T) {
	r := New(nil)
	tests := []struct {
		name     string
		md       string
		contains []string
		absent   []string
		refs     []ports.LinkRef
		links    []core.WikiLink
	}{
		{
			name:     "note wikilink with fragment",
			md:       "see [[Guide#Setup]]\n",
			contains: []string{`<a href="/s/demo/n/guide#Setup" class="wikilink"`, ">Guide#Setup</a>"},
			refs:     []ports.LinkRef{{Target: "Guide", Form: ports.LinkWikilink}},
			links:    []core.WikiLink{{Target: "Guide", Label: "Guide#Setup", NoteID: "n1", ItemID: "n1", Kind: core.KindNote}},
		},
		{
			name:     "path wikilink",
			md:       "see [[Projects/Plan]]\n",
			contains: []string{`href="/s/demo/n/Projects/plan"`},
			refs:     []ports.LinkRef{{Target: "Projects/Plan", Form: ports.LinkPathWikilink}},
			links:    []core.WikiLink{{Target: "Projects/Plan", Label: "Projects/Plan", NoteID: "n4", ItemID: "n4", Kind: core.KindNote}},
		},
		{
			name:     "folder wikilink",
			md:       "see [[Projects]]\n",
			contains: []string{`<a href="/s/demo/d/Projects" class="wikilink"`},
			links:    []core.WikiLink{{Target: "Projects", Label: "Projects", ItemID: "d1", Kind: core.KindFolder}},
		},
		{
			name:     "file wikilink without embed",
			md:       "see [[report.pdf]]\n",
			contains: []string{`<a href="/s/demo/f/report.pdf" class="wikilink"`, ">report.pdf</a>"},
			absent:   []string{"file-card"},
			links:    []core.WikiLink{{Target: "report.pdf", Label: "report.pdf", ItemID: "f2", Kind: core.KindFile}},
		},
		{
			name:     "image embed",
			md:       "![[east wall.jpg]]\n",
			contains: []string{`<img src="/files/demo/f1/east%20wall.jpg" alt="east wall.jpg" class="wikilink-embed"`},
			absent:   []string{"<a "},
			refs:     []ports.LinkRef{{Target: "east wall.jpg", Form: ports.LinkWikilink, Embed: true}},
			links:    []core.WikiLink{{Target: "east wall.jpg", Label: "east wall.jpg", ItemID: "f1", Kind: core.KindFile}},
		},
		{
			name:     "svg embed",
			md:       "![[logo.svg]]\n",
			contains: []string{`<img src="/files/demo/f3/logo.svg"`},
			links:    []core.WikiLink{{Target: "logo.svg", Label: "logo.svg", ItemID: "f3", Kind: core.KindFile}},
		},
		{
			name:     "non-ASCII image embed",
			md:       "![[café.png]]\n",
			contains: []string{`<img src="/files/demo/f4/caf%C3%A9.png"`},
			links:    []core.WikiLink{{Target: "café.png", Label: "café.png", ItemID: "f4", Kind: core.KindFile}},
		},
		{
			name: "file card",
			md:   "![[report.pdf]]\n",
			contains: []string{
				`<a href="/s/demo/f/report.pdf" class="wikilink file-card"`,
				`<span class="file-card-name">report.pdf</span></a>`,
			},
			links: []core.WikiLink{{Target: "report.pdf", Label: "report.pdf", ItemID: "f2", Kind: core.KindFile}},
		},
		{
			name:     "file card with label",
			md:       "![[report.pdf|Q3 report]]\n",
			contains: []string{`<span class="file-card-name">Q3 report</span>`},
			links:    []core.WikiLink{{Target: "report.pdf", Label: "Q3 report", ItemID: "f2", Kind: core.KindFile}},
		},
		{
			name:     "note card",
			md:       "![[Guide]]\n",
			contains: []string{`<a href="/s/demo/n/guide" class="wikilink note-card"`, ">Guide</a>"},
			links:    []core.WikiLink{{Target: "Guide", Label: "Guide", NoteID: "n1", ItemID: "n1", Kind: core.KindNote}},
		},
		{
			name:     "hidden wikilink stays dangling",
			md:       "[[Secret]] and ![[Secret.png]]\n",
			contains: []string{`class="wikilink wikilink-new"`, `href="#"`},
			absent:   []string{"<img", "Secret.png\"", "file-card"},
			links:    []core.WikiLink{{Target: "Secret", Label: "Secret"}, {Target: "Secret.png", Label: "Secret.png"}},
		},
		{
			name:     "markdown link keeps fragment and drops query",
			md:       "[report](report.pdf?x=1#page=2)\n",
			contains: []string{`href="/s/demo/f/report.pdf#page=2"`},
			refs:     []ports.LinkRef{{Target: "report.pdf", Form: ports.LinkMarkdown}},
		},
		{
			name:     "markdown image with percent escape",
			md:       "![wall](east%20wall.jpg)\n",
			contains: []string{`<img src="/files/demo/f1/east%20wall.jpg" alt="wall"`},
			refs:     []ports.LinkRef{{Target: "east wall.jpg", Form: ports.LinkMarkdownImage, Embed: true}},
		},
		{
			name:     "angle destination",
			md:       "[wall](<east wall.jpg>)\n",
			contains: []string{`href="/s/demo/f/east%20wall.jpg"`},
			refs:     []ports.LinkRef{{Target: "east wall.jpg", Form: ports.LinkMarkdown}},
		},
		{
			name:     "hidden markdown link unchanged",
			md:       "[s](secret.md) ![i](hidden.png)\n",
			contains: []string{`href="secret.md"`, `src="hidden.png"`},
			refs: []ports.LinkRef{
				{Target: "secret.md", Form: ports.LinkMarkdown},
				{Target: "hidden.png", Form: ports.LinkMarkdownImage, Embed: true},
			},
		},
		{
			name:     "external links are not resolved",
			md:       "[a](https://example.com/report.pdf) [b](#report.pdf) [c](//host/report.pdf) [d](mailto:x@example.com)\n",
			contains: []string{`href="https://example.com/report.pdf"`, `href="#report.pdf"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &itemResolver{}
			got, err := r.Render(tt.md, ports.RenderOptions{
				ResolveItem: res.resolve,
				ResolveLink: func(string) (string, bool) {
					t.Error("ResolveLink called while ResolveItem is set")
					return "", false
				},
			})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got.HTML, want) {
					t.Errorf("HTML missing %q\ngot: %s", want, got.HTML)
				}
			}
			for _, bad := range tt.absent {
				if strings.Contains(got.HTML, bad) {
					t.Errorf("HTML has %q\ngot: %s", bad, got.HTML)
				}
			}
			if tt.refs != nil && !reflect.DeepEqual(res.refs, tt.refs) {
				t.Errorf("refs = %+v, want %+v", res.refs, tt.refs)
			}
			if tt.name == "external links are not resolved" && len(res.refs) != 0 {
				t.Errorf("external links resolved: %+v", res.refs)
			}
			if !reflect.DeepEqual(got.Links, tt.links) {
				t.Errorf("Links = %+v, want %+v", got.Links, tt.links)
			}
		})
	}
}

func TestExternalDestination(t *testing.T) {
	for dest, want := range map[string]bool{
		"":                 true,
		"#x":               true,
		"//host/x":         true,
		"https://x":        true,
		"mailto:a@b":       true,
		"x+y.z-1:rest":     true,
		"c:x":              false,
		"a/b:c":            false,
		"report.pdf":       false,
		"../a b.png":       false,
		"1abc:x":           false,
		"/root/file.txt":   false,
		"weird name:x.png": false,
	} {
		if got := externalDestination(dest); got != want {
			t.Errorf("externalDestination(%q) = %v, want %v", dest, got, want)
		}
	}
}
