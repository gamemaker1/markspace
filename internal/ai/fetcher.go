package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// publicFetchTimeout bounds one catalog download.
	publicFetchTimeout = 20 * time.Second
	// maxPublicCatalog caps one catalog body.
	maxPublicCatalog = 32 << 20
)

// publicCatalogHosts are the only hosts a PublicFetcher reaches.
var publicCatalogHosts = []string{"openrouter.ai", "raw.githubusercontent.com"}

// errHostNotAllowed reports a request to a host outside the allowlist.
var errHostNotAllowed = errors.New("host is not a public price catalog")

// PublicFetcher downloads public price catalogs over HTTPS from
// openrouter.ai and raw.githubusercontent.com only, redirects included, with
// a 20 s timeout and a 32 MiB body cap.
type PublicFetcher struct {
	client *http.Client
	hosts  map[string]bool
	limit  int64
}

// NewPublicFetcher returns a fetcher allowed only the two catalog hosts.
func NewPublicFetcher() *PublicFetcher {
	base := http.DefaultTransport.(*http.Transport).Clone()
	return newPublicFetcher(base, publicCatalogHosts, maxPublicCatalog)
}

// newPublicFetcher builds a fetcher over a transport. Tests pass the
// transport of a TLS test server and its host.
func newPublicFetcher(base http.RoundTripper, hosts []string, limit int64) *PublicFetcher {
	f := &PublicFetcher{hosts: map[string]bool{}, limit: limit}
	for _, h := range hosts {
		f.hosts[h] = true
	}
	f.client = &http.Client{
		Timeout:   publicFetchTimeout,
		Transport: &allowlistTransport{fetcher: f, base: base},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return f.allowed(req.URL)
		},
	}
	return f
}

// allowed checks the scheme and host of a URL.
func (f *PublicFetcher) allowed(u *url.URL) error {
	if u == nil || u.Scheme != "https" || !f.hosts[u.Hostname()] {
		return fmt.Errorf("fetch price catalog: %w: %w", errHostNotAllowed, core.ErrForbidden)
	}
	return nil
}

// allowlistTransport refuses a request outside the allowlist before it
// dials.
type allowlistTransport struct {
	fetcher *PublicFetcher
	base    http.RoundTripper
}

// RoundTrip checks the request URL and passes it on.
func (t *allowlistTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.fetcher.allowed(req.URL); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// Fetch returns the body at url. A non-2xx status, a body over the cap and a
// URL outside the allowlist are errors.
func (f *PublicFetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("fetch price catalog: %w", err)
	}
	if err := f.allowed(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("fetch price catalog: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Markspace")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch price catalog %s: %w", u.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("fetch price catalog %s: status %d: %w", u.Host, resp.StatusCode, core.ErrUnavailable)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.limit+1))
	if err != nil {
		return nil, fmt.Errorf("fetch price catalog %s: %w", u.Host, err)
	}
	if int64(len(body)) > f.limit {
		return nil, fmt.Errorf("fetch price catalog %s: body over %d bytes: %w", u.Host, f.limit, core.ErrTooLarge)
	}
	return body, nil
}
