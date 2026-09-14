package plugin

import (
	"errors"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
	"github.com/gamemaker1/markspace/sdk"
)

func TestNormalizeBaseURL(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"http://bookmark-wiki:8090", "http://bookmark-wiki:8090", true},
		{"http://bookmark-wiki:8090/", "http://bookmark-wiki:8090", true},
		{"  https://example.com/plug/  ", "https://example.com/plug", true},
		{"http://10.0.0.5:9000", "http://10.0.0.5:9000", true},
		{"ftp://example.com", "", false},
		{"http://user:pw@example.com", "", false},
		{"http://example.com?x=1", "", false},
		{"http://example.com#frag", "", false},
		{"example.com", "", false},
		{"", "", false},
	} {
		got, err := normalizeBaseURL(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("normalizeBaseURL(%q) = %q, %v; want %q ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
		if err != nil && !errors.Is(err, core.ErrInvalid) {
			t.Errorf("normalizeBaseURL(%q) error %v is not invalid", tc.in, err)
		}
	}
}

func TestAsksForMore(t *testing.T) {
	base := sdk.Manifest{
		Permissions: []string{"items:read", "ui"},
		Events:      []string{"note.*"},
		Secrets:     []sdk.ManifestSecret{{Name: "token"}},
	}
	same := base
	fewer := sdk.Manifest{Permissions: []string{"ui"}}
	morePerm := base
	morePerm.Permissions = []string{"items:read", "ui", "items:write"}
	moreEvent := base
	moreEvent.Events = []string{"note.*", "file.*"}
	moreSecret := base
	moreSecret.Secrets = []sdk.ManifestSecret{{Name: "token"}, {Name: "other"}}
	for name, tc := range map[string]struct {
		next sdk.Manifest
		want bool
	}{
		"same": {same, false}, "fewer": {fewer, false}, "permission": {morePerm, true},
		"event": {moreEvent, true}, "secret": {moreSecret, true},
	} {
		if got := asksForMore(base, tc.next); got != tc.want {
			t.Errorf("%s: asksForMore = %v, want %v", name, got, tc.want)
		}
	}
}

func TestDefaultAccessAndJitter(t *testing.T) {
	if a := defaultAccess("", []string{"global"}); a != core.AccessNone {
		t.Errorf("global command access %v", a)
	}
	if a := defaultAccess("", []string{"global", "note"}); a != core.AccessView {
		t.Errorf("note command access %v", a)
	}
	if a := defaultAccess("manage", []string{"note"}); a != core.AccessEdit {
		t.Errorf("manage must cap at edit, got %v", a)
	}
	if a := defaultAccess("comment", []string{"note"}); a != core.AccessComment {
		t.Errorf("comment access %v", a)
	}
	if j := scheduleJitter(24 * time.Hour); j != time.Hour {
		t.Errorf("jitter of a day %v", j)
	}
	if j := scheduleJitter(10 * time.Minute); j != time.Minute {
		t.Errorf("jitter of 10 minutes %v", j)
	}
}

func TestRobots(t *testing.T) {
	body := `
# comment
User-agent: *
Disallow: /private
Allow: /private/open
Disallow: /*.pdf$

User-agent: OtherBot
Disallow: /
`
	rules := parseRobots(body)
	for path, want := range map[string]bool{
		"/":                  true,
		"/private":           false,
		"/private/x":         false,
		"/private/open/page": true,
		"/docs/a.pdf":        false,
		"/docs/a.pdf?x=1":    true,
		"/public":            true,
	} {
		if got := robotsMatch(rules, path); got != want {
			t.Errorf("robotsMatch(%q) = %v, want %v", path, got, want)
		}
	}
	own := parseRobots("User-agent: *\nDisallow:\n\nUser-agent: Markspace\nDisallow: /wiki\n")
	if robotsMatch(own, "/wiki/page") || !robotsMatch(own, "/other") {
		t.Error("a group naming markspace must win over the star group")
	}
	if !robotsMatch(parseRobots("garbage without colons"), "/anything") {
		t.Error("an unreadable robots.txt must allow")
	}
	if !robotsMatch(parseRobots("User-agent: *\nDisallow:\n"), "/x") {
		t.Error("an empty Disallow allows everything")
	}
}

func TestCheckSettingsAndSecrets(t *testing.T) {
	man, err := sdk.ParseManifest([]byte(`{"protocol":1,"id":"p","name":"P","version":"1",
		"settings":{"fields":[{"type":"space","name":"space","label":"Space","required":true},
		{"type":"text","name":"note","label":"Note"}]},
		"secrets":[{"name":"token","label":"Token"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkSettings(man, map[string]any{"note": "x"}, false); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("missing required setting: %v", err)
	}
	if _, err := checkSettings(man, map[string]any{"space": "s", "bogus": 1}, false); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("undeclared setting: %v", err)
	}
	raw, err := checkSettings(man, map[string]any{"space": "s"}, false)
	if err != nil || string(raw) != `{"space":"s"}` {
		t.Errorf("valid settings = %s, %v", raw, err)
	}
	if err := checkSecrets(man, map[string]string{"token": "v"}); err != nil {
		t.Errorf("declared secret: %v", err)
	}
	if err := checkSecrets(man, map[string]string{"other": "v"}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("undeclared secret: %v", err)
	}
	if _, err := grantsOf([]plugins.Grant{{SpaceID: "s", Role: core.RoleOwner}}); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("owner grant: %v", err)
	}
	g, err := grantsOf([]plugins.Grant{{SpaceID: "a", Role: core.RoleEditor}, {SpaceID: "b", Role: "none"}})
	if err != nil || len(g) != 1 || g[0].Role != "editor" {
		t.Errorf("grants = %+v, %v", g, err)
	}
}

func TestValidLink(t *testing.T) {
	for link, want := range map[string]bool{
		"": true, "/s/docs/n/page": true, "https://example.com/x": true,
		"//evil.com": false, "/\\evil": false, "javascript:alert(1)": false, "http://u:p@h/": false, "mailto:x@y": false,
	} {
		if got := validLink(link); got != want {
			t.Errorf("validLink(%q) = %v, want %v", link, got, want)
		}
	}
}

func TestTokenBucket(t *testing.T) {
	h := &Host{opts: Options{RateLimit: 20, Burst: 100}, buckets: map[string]*bucket{}}
	allowed := 0
	var retry time.Duration
	for i := 0; i < 150; i++ {
		ok, wait := h.Allow("p")
		if ok {
			allowed++
		} else {
			retry = wait
		}
	}
	if allowed < 100 || allowed > 105 {
		t.Fatalf("allowed %d of 150 in a burst", allowed)
	}
	if retry <= 0 || retry > time.Second {
		t.Fatalf("retry after %v", retry)
	}
	if ok, _ := h.Allow("other"); !ok {
		t.Fatal("buckets must be per plugin")
	}
	time.Sleep(120 * time.Millisecond)
	if ok, _ := h.Allow("p"); !ok {
		t.Fatal("the bucket did not refill")
	}
}
