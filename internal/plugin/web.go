package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/modules/plugins"
)

// robotsTTL is how long a robots.txt stays cached.
const robotsTTL = time.Hour

// robotsEntry is a cached robots.txt.
type robotsEntry struct {
	rules   []robotsRule
	fetched time.Time
}

// robotsRule is one Allow or Disallow line.
type robotsRule struct {
	allow   bool
	pattern string
}

// textTypes are the content types a fetch returns as text.
var textTypes = map[string]bool{"text/html": true, "text/plain": true, "text/markdown": true, "application/json": true}

// WebFetch implements plugins.Host. It fetches through the guarded fetcher
// with at most one request per second per host, optionally obeying
// robots.txt. Text types come back as text and markdown, other types as
// base64, both capped at 10 MiB. A final response with a status of 300 or
// more comes back with that status, its final URL and no body, so a plugin
// can tell a dead page from a failed call.
func (h *Host) WebFetch(ctx context.Context, actor *core.Actor, in plugins.FetchInput) (*plugins.FetchResult, error) {
	if _, err := pluginOf(actor); err != nil {
		return nil, err
	}
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, core.Invalid("url", "enter an http or https URL")
	}
	if in.RespectRobots {
		if ok := h.robotsAllowed(ctx, u); !ok {
			return nil, fmt.Errorf("%w: %w", plugins.ErrRobotsDisallowed, core.ErrForbidden)
		}
	}
	if err := h.waitHost(ctx, strings.ToLower(u.Host)); err != nil {
		return nil, err
	}
	page, err := h.fetcher.Fetch(ctx, u.String())
	if err != nil {
		return nil, fetchError(err)
	}
	if page.Status >= 300 {
		return &plugins.FetchResult{FinalURL: page.URL, Status: page.Status, ContentType: page.ContentType, FetchedAt: h.now()}, nil
	}
	sum := sha256.Sum256(page.Body)
	res := &plugins.FetchResult{
		FinalURL: page.URL, Status: page.Status, ContentType: page.ContentType, Title: page.Title,
		SHA256: hex.EncodeToString(sum[:]), Truncated: page.Truncated, FetchedAt: h.now(),
	}
	if res.Status == 0 {
		res.Status = 200
	}
	if textTypes[page.ContentType] {
		res.Text = page.Text
		res.Markdown = markdownOf(page.ContentType, page.Title, page.Text)
		if in.IncludeHTML && page.ContentType == "text/html" {
			res.HTML = string(page.Body)
		}
	} else {
		res.BodyBase64 = base64.StdEncoding.EncodeToString(page.Body)
	}
	return res, nil
}

// fetchError keeps a fetch error that names a core sentinel and reports any
// other failure, such as a refused connection or a timeout, as unavailable,
// because the remote site is at fault rather than the server.
func fetchError(err error) error {
	for _, sentinel := range []error{
		core.ErrInvalid, core.ErrForbidden, core.ErrNotFound, core.ErrUnsupported, core.ErrTooLarge,
		core.ErrUnavailable, core.ErrRateLimited, core.ErrConflict,
	} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	return fmt.Errorf("%v: %w", err, core.ErrUnavailable)
}

// markdownOf renders fetched text as markdown. HTML text is already reduced
// to paragraphs, so the title becomes a heading above it. Text that already
// opens with a top-level heading keeps it and gets no second one.
func markdownOf(contentType, title, text string) string {
	switch contentType {
	case "text/html":
		t := strings.TrimSpace(title)
		if t == "" || strings.HasPrefix(strings.TrimLeft(text, "\n"), "# ") {
			return text
		}
		return "# " + t + "\n\n" + text
	case "application/json":
		return "```json\n" + strings.TrimRight(text, "\n") + "\n```\n"
	}
	return text
}

// waitHost spaces fetches to one host by FetchHostInterval. A wait longer
// than 30 s is refused as rate limited.
func (h *Host) waitHost(ctx context.Context, host string) error {
	interval := h.opts.FetchHostInterval
	now := time.Now()
	h.mu.Lock()
	next := h.hosts[host]
	if len(h.hosts) > 4096 {
		for k, t := range h.hosts {
			if t.Before(now) {
				delete(h.hosts, k)
			}
		}
	}
	var wait time.Duration
	if next.After(now) {
		wait = next.Sub(now)
		if wait > 30*time.Second {
			h.mu.Unlock()
			return fmt.Errorf("too many fetches from %s: %w", host, core.ErrRateLimited)
		}
		h.hosts[host] = next.Add(interval)
	} else {
		h.hosts[host] = now.Add(interval)
	}
	h.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// robotsAllowed reports whether robots.txt lets Markspace fetch u. A missing
// or unreadable robots.txt allows everything.
func (h *Host) robotsAllowed(ctx context.Context, u *url.URL) bool {
	origin := u.Scheme + "://" + strings.ToLower(u.Host)
	h.mu.Lock()
	e := h.robots[origin]
	h.mu.Unlock()
	if e == nil || time.Since(e.fetched) > robotsTTL {
		e = &robotsEntry{fetched: time.Now()}
		if err := h.waitHost(ctx, strings.ToLower(u.Host)); err != nil {
			return true
		}
		if page, err := h.robotsF.Fetch(ctx, origin+"/robots.txt"); err == nil && page.Status == 200 {
			e.rules = parseRobots(string(page.Body))
		}
		h.mu.Lock()
		h.robots[origin] = e
		h.mu.Unlock()
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return robotsMatch(e.rules, path)
}

// parseRobots reads the rules for Markspace from robots.txt: the group whose
// user agent names markspace, or else the group for every agent.
func parseRobots(body string) []robotsRule {
	type group struct {
		agents []string
		rules  []robotsRule
	}
	var groups []*group
	var cur *group
	lastWasAgent := false
	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "user-agent":
			if cur == nil || !lastWasAgent {
				cur = &group{}
				groups = append(groups, cur)
			}
			cur.agents = append(cur.agents, strings.ToLower(value))
			lastWasAgent = true
		case "allow", "disallow":
			lastWasAgent = false
			if cur == nil {
				continue
			}
			if key == "disallow" && value == "" {
				continue
			}
			cur.rules = append(cur.rules, robotsRule{allow: key == "allow", pattern: value})
		default:
			lastWasAgent = false
		}
	}
	var star []robotsRule
	for _, g := range groups {
		for _, a := range g.agents {
			if strings.Contains(a, "markspace") {
				return g.rules
			}
			if a == "*" {
				star = append(star, g.rules...)
			}
		}
	}
	return star
}

// robotsMatch applies the longest matching rule. Allow wins a tie, and a path
// no rule matches is allowed.
func robotsMatch(rules []robotsRule, path string) bool {
	best, allowed := -1, true
	for _, r := range rules {
		if !robotsPattern(r.pattern, path) {
			continue
		}
		if n := len(r.pattern); n > best || (n == best && r.allow) {
			best, allowed = n, r.allow
		}
	}
	return allowed
}

// robotsPattern matches a robots.txt path pattern with * wildcards and a
// trailing $ anchor.
func robotsPattern(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	pattern = strings.TrimSuffix(pattern, "$")
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(path, parts[0]) {
		return false
	}
	rest := path[len(parts[0]):]
	for _, p := range parts[1:] {
		i := strings.Index(rest, p)
		if i < 0 {
			return false
		}
		rest = rest[i+len(p):]
	}
	if anchored {
		return rest == "" || strings.HasSuffix(pattern, "*")
	}
	return true
}
