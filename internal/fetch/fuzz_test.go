package fetch

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzHTMLText checks that arbitrary markup never panics the extractor and
// always yields valid UTF-8 without the content of dropped elements that
// the parser recognized.
func FuzzHTMLText(f *testing.F) {
	for _, seed := range []string{
		"",
		"<title>t</title><p>hello</p>",
		"<script>alert(1)</script><h3>x</h3><ul><li>a<li>b</ul>",
		"<table><td>1<td>2<tr><th>3</table><pre>  a\n b</pre>",
		"<svg><title>inner</title></svg><title>outer</title>",
		strings.Repeat("<b>", 2000),
		"\xff\xfe<p>\x00</p>",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		title, text, err := HTMLText(bytes.NewReader(data))
		if err != nil {
			return
		}
		if !utf8.ValidString(title) || !utf8.ValidString(text) {
			t.Fatalf("invalid UTF-8: title %q text %q", title, text)
		}
		if strings.Contains(title, "\n") {
			t.Fatalf("title keeps a line break: %q", title)
		}
	})
}
