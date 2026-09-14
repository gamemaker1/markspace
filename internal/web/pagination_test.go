package web

import (
	"bytes"
	"html/template"
	"strconv"
	"strings"
	"testing"
)

// TestPageLinks checks that Pagination lists the first and last pages and
// two pages on each side of the current page, with gaps between them.
func TestPageLinks(t *testing.T) {
	link := func(n int) string { return "/p?" + strings.Repeat("x", n) }
	render := func(links []PageLink) string {
		var parts []string
		for _, l := range links {
			switch {
			case l.Gap:
				parts = append(parts, "…")
			case l.Current:
				parts = append(parts, "["+itoa(l.Number)+"]")
			default:
				parts = append(parts, itoa(l.Number))
			}
		}
		return strings.Join(parts, " ")
	}
	cases := []struct {
		current, pages int
		want           string
	}{
		{1, 1, ""},
		{1, 3, "[1] 2 3"},
		{1, 10, "[1] 2 3 … 10"},
		{5, 10, "1 … 3 4 [5] 6 7 … 10"},
		{10, 10, "1 … 8 9 [10]"},
		{4, 7, "1 2 3 [4] 5 6 7"},
	}
	for _, c := range cases {
		if got := render(pageLinks(c.current, c.pages, link)); got != c.want {
			t.Errorf("pageLinks(%d, %d) = %q, want %q", c.current, c.pages, got, c.want)
		}
	}
}

// TestListPaginationPages checks the page URLs of a listing with a known total.
func TestListPaginationPages(t *testing.T) {
	p := listPagination("/admin/users", "ana", 20, 20, 20, 84)
	if len(p.Pages) != 5 {
		t.Fatalf("pages = %+v", p.Pages)
	}
	if !p.Pages[1].Current || p.Pages[1].Number != 2 || p.Pages[1].URL != "/admin/users?offset=20&q=ana" {
		t.Errorf("current page = %+v", p.Pages[1])
	}
	if p.Pages[0].URL != "/admin/users?q=ana" {
		t.Errorf("first page URL = %q", p.Pages[0].URL)
	}
	if unknown := listPagination("/x", "", 0, 20, 20, 0); len(unknown.Pages) != 0 {
		t.Errorf("an unknown total lists pages: %+v", unknown.Pages)
	}
}

// TestPaginationTemplate renders the Primer Pagination partial.
func TestPaginationTemplate(t *testing.T) {
	src, err := embedded.ReadFile("templates/partials/pagination.html")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := template.New("t").Funcs(funcMap).Parse(string(src))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "pagination", listPagination("/admin/users", "", 0, 20, 20, 84)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`<nav class="pagination" aria-label="Pagination">`,
		`aria-current="page" aria-label="Page 1">1</a>`,
		`aria-label="Page 5">5</a>`,
		`aria-disabled="true"`,
		`rel="next" aria-label="Next Page"`,
		`1 through 20 of 84`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pagination output lacks %q:\n%s", want, out)
		}
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
