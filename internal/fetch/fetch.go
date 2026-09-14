// Package fetch fetches web pages for AI context. Every connection target
// is checked at dial time, after DNS resolution and after each redirect, so a
// page cannot reach loopback, private, link-local or other internal addresses
// unless the configuration allows it. HTML becomes readable text.
package fetch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// DefaultTimeout bounds one fetch, redirects included.
	DefaultTimeout = 10 * time.Second
	// DefaultMaxBytes caps the response body that is read.
	DefaultMaxBytes = 2 << 20
	// DefaultMaxRedirects is how many redirects a fetch follows.
	DefaultMaxRedirects = 3
	// userAgent identifies the fetcher to web servers.
	userAgent = "Markspace/1 (AI context fetcher)"
)

var (
	// ErrPrivateAddress reports a connection target that is loopback, private,
	// carrier-grade NAT, link-local, unique local, multicast or unspecified.
	ErrPrivateAddress = fmt.Errorf("private address not allowed: %w", core.ErrForbidden)
	// ErrScheme reports a URL that is not http or https.
	ErrScheme = fmt.Errorf("only http and https URLs are allowed: %w", core.ErrInvalid)
	// ErrTooManyRedirects reports a redirect chain over the limit.
	ErrTooManyRedirects = fmt.Errorf("too many redirects: %w", core.ErrInvalid)
	// ErrContentType reports a response that is not HTML, plain text,
	// markdown or JSON.
	ErrContentType = fmt.Errorf("unsupported content type: %w", core.ErrUnsupported)
)

// allowedTypes lists the response media types a fetch accepts.
var allowedTypes = map[string]bool{
	"text/html":        true,
	"text/plain":       true,
	"text/markdown":    true,
	"application/json": true,
}

// blockedPrefixes lists the internal ranges that netip.Addr has no method for.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),          // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),      // carrier-grade NAT
	netip.MustParsePrefix("255.255.255.255/32"), // limited broadcast
	netip.MustParsePrefix("::/96"),              // IPv4-compatible IPv6, :: and ::1 included
	netip.MustParsePrefix("fec0::/10"),          // deprecated site-local
}

// IsForbidden reports whether a connection to ip is refused when private
// addresses are not allowed. It covers loopback, RFC 1918 and unique local
// private ranges, carrier-grade NAT, link-local, multicast and unspecified
// addresses, and it unmaps IPv4-mapped IPv6 addresses first. An invalid
// address is forbidden.
func IsForbidden(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// Options configures a Fetcher. Zero values select the defaults.
type Options struct {
	// AllowPrivate turns off the address check.
	AllowPrivate bool
	// Timeout bounds one fetch. The default is 10 s.
	Timeout time.Duration
	// MaxBytes caps the body read. The default is 2 MB.
	MaxBytes int64
	// MaxRedirects caps the redirects followed. The default is 3.
	MaxRedirects int
	// AnyType accepts every content type. A type other than HTML, plain text,
	// markdown or JSON returns only Body, with no Text. The plugin host uses
	// it for PDFs and other binary files.
	AnyType bool
	// KeepErrorStatus returns a final response with a status outside 2xx as
	// a Page with URL, ContentType and Status and no body, instead of an
	// error. The plugin host uses it to report dead pages.
	KeepErrorStatus bool
	// UserAgent replaces the default user agent.
	UserAgent string
}

// Page is the readable content of one fetched URL.
type Page struct {
	// URL is the final URL after redirects.
	URL string
	// Title is the HTML title, or empty for other content.
	Title string
	// Text is the readable text.
	Text string
	// ContentType is the media type of the response.
	ContentType string
	// Truncated reports that the body cap cut the response short.
	Truncated bool
	// Status is the HTTP status of the final response.
	Status int
	// Body is the response body as read, capped at MaxBytes.
	Body []byte
}

// Fetcher fetches pages under the address rules. It is safe for concurrent
// use.
type Fetcher struct {
	opts   Options
	client *http.Client
	// forbidden classifies a dial target. Tests replace it.
	forbidden func(netip.Addr) bool
	// lookup resolves a host name. Tests replace it.
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
}

// New builds a Fetcher. The HTTP client uses no proxy, because a proxy would
// connect to the target on the fetcher's behalf and bypass the dial check.
func New(opts Options) *Fetcher {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	if opts.MaxRedirects <= 0 {
		opts.MaxRedirects = DefaultMaxRedirects
	}
	f := &Fetcher{opts: opts, forbidden: IsForbidden, lookup: defaultLookup}
	dialer := &net.Dialer{Timeout: opts.Timeout, KeepAlive: 30 * time.Second, Control: f.control}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return f.dial(ctx, dialer, network, address)
		},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   opts.Timeout,
		ResponseHeaderTimeout: opts.Timeout,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
	}
	f.client = &http.Client{Transport: transport, Timeout: opts.Timeout, CheckRedirect: f.checkRedirect}
	return f
}

// defaultLookup resolves a host with the system resolver.
func defaultLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// control runs on every socket before it connects and refuses a forbidden
// address. It sees the resolved address, so a host name that resolves to a
// public address at validation and a private one on connect is still caught.
func (f *Fetcher) control(network, address string, _ syscall.RawConn) error {
	if f.opts.AllowPrivate {
		return nil
	}
	target, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("fetch: unparsable dial address %q: %w", address, ErrPrivateAddress)
	}
	if f.forbidden(target.Addr()) {
		return fmt.Errorf("fetch: dial %s: %w", address, ErrPrivateAddress)
	}
	return nil
}

// dial resolves the host itself and dials each address in turn. Forbidden
// addresses are skipped before dialing, and control checks again on connect.
func (f *Fetcher) dial(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("fetch: dial address %q: %w", address, core.ErrInvalid)
	}
	var addrs []netip.Addr
	if ip, perr := netip.ParseAddr(host); perr == nil {
		addrs = []netip.Addr{ip}
	} else {
		addrs, err = f.lookup(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("fetch: resolve %s: %w", host, err)
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("fetch: resolve %s: no addresses: %w", host, core.ErrNotFound)
		}
	}
	var firstErr error
	for _, ip := range addrs {
		if !f.opts.AllowPrivate && f.forbidden(ip) {
			if firstErr == nil {
				firstErr = fmt.Errorf("fetch: %s resolves to %s: %w", host, ip, ErrPrivateAddress)
			}
			continue
		}
		conn, derr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if derr == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = derr
		}
	}
	return nil, firstErr
}

// checkRedirect limits the redirect count and rechecks every hop.
func (f *Fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > f.opts.MaxRedirects {
		return fmt.Errorf("fetch: %d redirects: %w", len(via), ErrTooManyRedirects)
	}
	return f.checkURL(req.URL)
}

// checkURL accepts an absolute http or https URL. A literal IP host is
// checked here for a clear error, and every host is checked again on dial.
func (f *Fetcher) checkURL(u *url.URL) error {
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("fetch: scheme %q: %w", u.Scheme, ErrScheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("fetch: URL has no host: %w", core.ErrInvalid)
	}
	if ip, err := netip.ParseAddr(host); err == nil && !f.opts.AllowPrivate && f.forbidden(ip) {
		return fmt.Errorf("fetch: host %s: %w", host, ErrPrivateAddress)
	}
	return nil
}

// Fetch downloads rawURL and returns its readable text. HTML is converted
// with HTMLText. Plain text, markdown and JSON are returned as they are.
// Errors wrap ErrPrivateAddress, ErrScheme, ErrTooManyRedirects,
// ErrContentType or a core sentinel.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (*Page, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("fetch: parse URL: %w", core.ErrInvalid)
	}
	if err := f.checkURL(u); err != nil {
		return nil, err
	}
	u.Fragment = ""
	ctx, cancel := context.WithTimeout(ctx, f.opts.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("fetch: build request: %w", core.ErrInvalid)
	}
	ua := userAgent
	if f.opts.UserAgent != "" {
		ua = f.opts.UserAgent
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html, text/markdown;q=0.9, text/plain;q=0.9, application/json;q=0.8")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: get %s: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if f.opts.KeepErrorStatus {
			mediaType, _ := parseContentType(resp.Header.Get("Content-Type"))
			return &Page{URL: resp.Request.URL.String(), ContentType: mediaType, Status: resp.StatusCode}, nil
		}
		sentinel := core.ErrUnavailable
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			sentinel = core.ErrNotFound
		}
		return nil, fmt.Errorf("fetch: get %s: status %d: %w", u.Redacted(), resp.StatusCode, sentinel)
	}

	mediaType, charset := parseContentType(resp.Header.Get("Content-Type"))
	if mediaType != "" && !allowedTypes[mediaType] && !f.opts.AnyType {
		return nil, fmt.Errorf("fetch: %s is %s: %w", u.Redacted(), mediaType, ErrContentType)
	}
	body, truncated, err := readCapped(resp.Body, f.opts.MaxBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch: read %s: %w", u.Redacted(), err)
	}
	if mediaType == "" {
		mediaType, charset = parseContentType(http.DetectContentType(body))
		if !allowedTypes[mediaType] && !f.opts.AnyType {
			return nil, fmt.Errorf("fetch: %s is %s: %w", u.Redacted(), mediaType, ErrContentType)
		}
	}

	page := &Page{URL: resp.Request.URL.String(), ContentType: mediaType, Truncated: truncated,
		Status: resp.StatusCode, Body: body}
	if !allowedTypes[mediaType] {
		return page, nil
	}
	if mediaType == "text/html" {
		if charset == "" {
			charset = metaCharset(body)
		}
		title, text, err := HTMLText(strings.NewReader(toUTF8(body, charset)))
		if err != nil {
			return nil, err
		}
		page.Title, page.Text = title, text
		return page, nil
	}
	page.Text = toUTF8(body, charset)
	return page, nil
}

// parseContentType returns the lowercase media type and charset of a header.
func parseContentType(header string) (string, string) {
	if strings.TrimSpace(header) == "" {
		return "", ""
	}
	mediaType, params, err := mime.ParseMediaType(header)
	if err != nil {
		base, _, _ := strings.Cut(header, ";")
		return strings.ToLower(strings.TrimSpace(base)), ""
	}
	return strings.ToLower(mediaType), strings.ToLower(params["charset"])
}

// readCapped reads at most limit bytes and reports whether more followed.
func readCapped(r io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

// metaCharsetPattern finds a charset declaration in the head of an HTML page.
var metaCharsetPattern = regexp.MustCompile(`(?i)<meta[^>]+charset\s*=\s*["']?\s*([a-z0-9_-]+)`)

// metaCharset returns the charset a page declares in its first 2048 bytes.
func metaCharset(body []byte) string {
	head := body[:min(len(body), 2048)]
	if m := metaCharsetPattern.FindSubmatch(head); m != nil {
		return strings.ToLower(string(m[1]))
	}
	return ""
}

// toUTF8 decodes body as UTF-8, or byte by byte for the Latin-1 family of
// charsets, and replaces invalid sequences.
func toUTF8(body []byte, charset string) string {
	switch charset {
	case "iso-8859-1", "latin1", "l1", "windows-1252", "cp1252", "us-ascii", "ascii":
		runes := make([]rune, len(body))
		for i, b := range body {
			runes[i] = rune(b)
		}
		return string(runes)
	default:
		return strings.ToValidUTF8(string(bytes.TrimPrefix(body, []byte("\xEF\xBB\xBF"))), "�")
	}
}
