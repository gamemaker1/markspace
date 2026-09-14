package server

import (
	"bufio"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/modules/plugins"
)

// docRoute is one route line of docs/routes.md with the module named above its
// block and whether it sits in the module pages section.
type docRoute struct {
	module, pattern string
	web             bool
}

func (d docRoute) key() string {
	kind := "api"
	if d.web {
		kind = "page"
	}
	return kind + " " + d.module + " " + d.pattern
}

var (
	docModule = regexp.MustCompile("module `([a-z]+)`")
	docLine   = regexp.MustCompile(`^(GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS)\s+(/\S*)`)
)

// readDocRoutes parses the two module sections of docs/routes.md. A route line
// inside a code block starts with a method and a path. Its module is the last
// "module `name`" mentioned outside a code block before it. The query string
// of a documented path is dropped.
func readDocRoutes(t *testing.T, path string) []docRoute {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var (
		out      []docRoute
		section  string
		module   string
		inCode   bool
		lineNo   int
		scanner  = bufio.NewScanner(f)
		sections = map[string]bool{}
	)
	for scanner.Scan() {
		line := scanner.Text()
		lineNo++
		if strings.HasPrefix(line, "## ") {
			switch {
			case strings.HasPrefix(line, "## JSON API"):
				section = "api"
			case strings.HasPrefix(line, "## Module pages"):
				section = "page"
			default:
				section = ""
			}
			sections[section] = true
			module = ""
			continue
		}
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			continue
		}
		if section == "" {
			continue
		}
		if !inCode {
			if m := docModule.FindAllStringSubmatch(line, -1); m != nil {
				module = m[len(m)-1][1]
			}
			continue
		}
		m := docLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if module == "" {
			t.Fatalf("docs/routes.md:%d: route %q has no module above its block", lineNo, line)
		}
		p, _, _ := strings.Cut(m[2], "?")
		out = append(out, docRoute{module: module, pattern: m[1] + " " + p, web: section == "page"})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !sections["api"] || !sections["page"] {
		t.Fatalf("docs/routes.md lacks the JSON API or the module pages section")
	}
	return out
}

// TestRoutesMatchDocs assembles the registry as cmd/markspace does and
// compares every mounted module route, its module and its kind with docs/routes.md.
// The plugin host API is listed in docs/plugins.md and is left out.
func TestRoutesMatchDocs(t *testing.T) {
	s := newPluginStack(t)
	mounted := map[string]bool{}
	for _, r := range s.parts.Registry.Routes() {
		if strings.HasPrefix(r.Path, plugins.APIPrefix+"/") {
			continue
		}
		d := docRoute{module: r.Module, pattern: r.Pattern, web: r.Web}
		if mounted[d.key()] {
			t.Errorf("route mounted twice: %s", d.key())
		}
		mounted[d.key()] = true
	}
	documented := map[string]bool{}
	for _, d := range readDocRoutes(t, "../../docs/routes.md") {
		if documented[d.key()] {
			t.Errorf("docs/routes.md lists a route twice: %s", d.key())
		}
		documented[d.key()] = true
	}
	if len(documented) < 150 {
		t.Fatalf("docs/routes.md yielded only %d routes, the parser is probably broken", len(documented))
	}
	var missing, extra []string
	for k := range mounted {
		if !documented[k] {
			missing = append(missing, k)
		}
	}
	for k := range documented {
		if !mounted[k] {
			extra = append(extra, k)
		}
	}
	slices.Sort(missing)
	slices.Sort(extra)
	for _, k := range missing {
		t.Errorf("mounted but not in docs/routes.md: %s", k)
	}
	for _, k := range extra {
		t.Errorf("in docs/routes.md but not mounted: %s", k)
	}
}
