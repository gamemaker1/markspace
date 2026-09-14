package api

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

func TestBreakable(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"plugin.word-count.count-space", "plugin.<wbr>word-<wbr>count.<wbr>count-<wbr>space"},
		{"katherine@example.com", "katherine@<wbr>example.<wbr>com"},
		{"http://127.0.0.1:8080/v1", "http:<wbr>/<wbr>/<wbr>127.<wbr>0.<wbr>0.<wbr>1:<wbr>8080/<wbr>v1"},
		{"trailing.", "trailing."},
		{"plain", "plain"},
		{"<b>a.b</b>", "&lt;b&gt;a.<wbr>b&lt;/<wbr>b&gt;"},
		{nil, ""},
		{42, "42"},
	}
	for _, c := range cases {
		if got := string(Breakable(c.in)); got != c.want {
			t.Errorf("Breakable(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBreakableInPageTemplate(t *testing.T) {
	tmpl := template.Must(template.New("p").Funcs(PageFuncs()).Parse(`<span>{{ breakable .Kind }}</span>`))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"Kind": `a.b"c`}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != `<span>a.<wbr>b&#34;c</span>` && !strings.Contains(got, "a.<wbr>b") {
		t.Errorf("page template output %q", got)
	}
}
