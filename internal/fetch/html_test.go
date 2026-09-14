package fetch

import (
	"strings"
	"testing"
)

func TestHTMLText(t *testing.T) {
	const page = `<html><head><title>  Rollout
  Spec </title><style>body{color:red}</style><script>var x = "SCRIPTSECRET";</script></head>
<body>
<header>Site header</header>
<nav><a href="/">Home</a></nav>
<main>
<h1>Rollout plan</h1>
<p>First   paragraph
with <b>bold</b> text.</p>
<ul><li>one</li><li>two <i>items</i></li></ul>
<table><tr><th>Name</th><th>Value</th></tr><tr><td>a</td><td>1</td></tr></table>
<pre>
code  line 1
  indented</pre>
<form><label>Form text</label><input value="x"></form>
<aside>Aside text</aside>
<noscript>Enable JS</noscript>
<div>Tail<br>next line</div>
</main>
<footer>Footer text</footer>
</body></html>`
	title, text, err := HTMLText(strings.NewReader(page))
	if err != nil {
		t.Fatalf("HTMLText: %v", err)
	}
	if title != "Rollout Spec" {
		t.Errorf("title = %q", title)
	}
	want := "# Rollout plan\n\n" +
		"First paragraph with bold text.\n\n" +
		"- one\n- two items\n\n" +
		"Name | Value\na | 1\n\n" +
		"code  line 1\n  indented\n\n" +
		"Tail\nnext line"
	if text != want {
		t.Errorf("text = %q\nwant   %q", text, want)
	}
	for _, gone := range []string{"SCRIPTSECRET", "color:red", "Site header", "Home", "Form text", "Aside", "Enable JS", "Footer"} {
		if strings.Contains(text, gone) {
			t.Errorf("text keeps %q", gone)
		}
	}
}

func TestHTMLTextDeepNesting(t *testing.T) {
	page := strings.Repeat("<div>", 5000) + "deep" + strings.Repeat("</div>", 5000) + "<p>shallow</p>"
	if _, text, err := HTMLText(strings.NewReader(page)); err != nil || !strings.Contains(text, "shallow") {
		t.Fatalf("text %q, err %v", text, err)
	}
}

func TestHTMLTextInvalidUTF8(t *testing.T) {
	title, text, err := HTMLText(strings.NewReader("<title>a\xffb</title><p>c\xfed</p>"))
	if err != nil || title != "a�b" || text != "c�d" {
		t.Fatalf("title %q text %q err %v", title, text, err)
	}
}
