package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestIsForbidden(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.255.0.9", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.254", true},
		{"192.168.1.1", true},
		{"100.64.0.1", true},
		{"100.127.255.254", true},
		{"169.254.169.254", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"fd12:3456::1", true},
		{"0.0.0.0", true},
		{"0.1.2.3", true},
		{"::", true},
		{"224.0.0.1", true},
		{"239.255.255.250", true},
		{"ff02::1", true},
		{"255.255.255.255", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:10.1.2.3", true},
		{"::ffff:169.254.169.254", true},
		{"::127.0.0.1", true},
		{"8.8.8.8", false},
		{"93.184.216.34", false},
		{"172.32.0.1", false},
		{"100.128.0.1", false},
		{"2606:4700:4700::1111", false},
		{"::ffff:8.8.8.8", false},
	}
	for _, tc := range tests {
		if got := IsForbidden(netip.MustParseAddr(tc.addr)); got != tc.want {
			t.Errorf("IsForbidden(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}
	if !IsForbidden(netip.Addr{}) {
		t.Error("the zero address should be forbidden")
	}
}

// serve starts a test server with one handler.
func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return server
}

// private returns a fetcher that may reach the loopback test servers.
func private() *Fetcher {
	return New(Options{AllowPrivate: true, Timeout: 5 * time.Second})
}

func TestFetchHTML(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<html><head><title>Spec page</title><script>var leak = "SCRIPTBODY";</script></head>
<body><nav>Menu</nav><h2>Rollback</h2><p>Roll back with the previous tag.</p></body></html>`)
	})
	page, err := private().Fetch(context.Background(), server.URL+"/spec#part")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if page.Title != "Spec page" || page.ContentType != "text/html" || page.Truncated {
		t.Fatalf("page = %+v", page)
	}
	if page.Text != "## Rollback\n\nRoll back with the previous tag." {
		t.Fatalf("text = %q", page.Text)
	}
	if page.URL != server.URL+"/spec" {
		t.Fatalf("url = %q", page.URL)
	}
}

func TestFetchTextTypes(t *testing.T) {
	for _, contentType := range []string{"text/plain", "text/markdown; charset=utf-8", "application/json"} {
		server := serve(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			io.WriteString(w, `{"a": "<b>kept</b>"}`)
		})
		page, err := private().Fetch(context.Background(), server.URL)
		if err != nil || page.Text != `{"a": "<b>kept</b>"}` || page.Title != "" {
			t.Fatalf("%s: page %+v, err %v", contentType, page, err)
		}
	}
}

func TestFetchSniffsMissingContentType(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil
		io.WriteString(w, "<!DOCTYPE html><title>Sniffed</title><p>body</p>")
	})
	page, err := private().Fetch(context.Background(), server.URL)
	if err != nil || page.Title != "Sniffed" || page.Text != "body" {
		t.Fatalf("page %+v, err %v", page, err)
	}
}

func TestFetchRejectsContentType(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("\x89PNG"))
	})
	if _, err := private().Fetch(context.Background(), server.URL); !errors.Is(err, ErrContentType) {
		t.Fatalf("err = %v, want ErrContentType", err)
	}
}

func TestFetchCapsBody(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, strings.Repeat("a", 5000))
	})
	f := New(Options{AllowPrivate: true, MaxBytes: 100})
	page, err := f.Fetch(context.Background(), server.URL)
	if err != nil || !page.Truncated || len(page.Text) != 100 {
		t.Fatalf("page truncated=%v len=%d err=%v", page != nil && page.Truncated, len(page.Text), err)
	}
}

func TestFetchLatin1(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<meta charset=\"iso-8859-1\"><p>caf\xe9</p>"))
	})
	page, err := private().Fetch(context.Background(), server.URL)
	if err != nil || page.Text != "café" {
		t.Fatalf("page %+v, err %v", page, err)
	}
}

func TestFetchStatus(t *testing.T) {
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := private().Fetch(context.Background(), server.URL+"/gone"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("404: err = %v", err)
	}
	if _, err := private().Fetch(context.Background(), server.URL); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("502: err = %v", err)
	}
}

// redirector serves /hop/n as a redirect to /hop/n-1, and /hop/0 as text.
func redirector(t *testing.T) *httptest.Server {
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		var n int
		fmt.Sscanf(r.URL.Path, "/hop/%d", &n)
		if n > 0 {
			http.Redirect(w, r, fmt.Sprintf("/hop/%d", n-1), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "arrived")
	})
}

func TestFetchRedirectLimit(t *testing.T) {
	server := redirector(t)
	page, err := private().Fetch(context.Background(), server.URL+"/hop/3")
	if err != nil || page.Text != "arrived" {
		t.Fatalf("three redirects: page %+v, err %v", page, err)
	}
	if _, err := private().Fetch(context.Background(), server.URL+"/hop/4"); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("four redirects: err = %v", err)
	}
}

func TestFetchRefusesOtherSchemes(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://example.com", "//no-scheme"} {
		if _, err := private().Fetch(context.Background(), raw); !errors.Is(err, ErrScheme) {
			t.Errorf("%s: err = %v, want ErrScheme", raw, err)
		}
	}
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	})
	if _, err := private().Fetch(context.Background(), server.URL); !errors.Is(err, ErrScheme) {
		t.Fatalf("redirect to file: err = %v", err)
	}
}

func TestFetchRefusesLoopbackByDefault(t *testing.T) {
	var hits int
	server := serve(t, func(w http.ResponseWriter, r *http.Request) { hits++ })
	f := New(Options{})
	if _, err := f.Fetch(context.Background(), server.URL); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("literal loopback: err = %v", err)
	}
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	f.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	if _, err := f.Fetch(context.Background(), "http://looks-public.test:"+port+"/"); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("host resolving to loopback: err = %v", err)
	}
	if hits != 0 {
		t.Fatalf("the loopback server was reached %d times", hits)
	}
}

// TestFetchRefusesRedirectToPrivateAddress runs offline. The classifier
// forbids only 10.0.0.0/8, so the loopback test server stands in for a public
// site, and a stub resolver maps intranet.test into 10.0.0.0/8.
func TestFetchRefusesRedirectToPrivateAddress(t *testing.T) {
	blocked := netip.MustParsePrefix("10.0.0.0/8")
	var port string
	server := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/literal":
			http.Redirect(w, r, "http://10.1.2.3:"+port+"/", http.StatusFound)
		default:
			http.Redirect(w, r, "http://intranet.test:"+port+"/", http.StatusFound)
		}
	})
	_, port, _ = net.SplitHostPort(server.Listener.Addr().String())

	f := New(Options{Timeout: 2 * time.Second})
	f.forbidden = func(ip netip.Addr) bool { return blocked.Contains(ip.Unmap()) }
	var lookups []string
	f.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		lookups = append(lookups, host)
		if host == "intranet.test" {
			return []netip.Addr{netip.MustParseAddr("10.20.30.40")}, nil
		}
		return nil, fmt.Errorf("unknown host %s", host)
	}
	for _, path := range []string{"/named", "/literal"} {
		start := time.Now()
		_, err := f.Fetch(context.Background(), server.URL+path)
		if !errors.Is(err, ErrPrivateAddress) {
			t.Fatalf("%s: err = %v, want ErrPrivateAddress", path, err)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("%s: refusal took %s, so a dial was attempted", path, time.Since(start))
		}
	}
	if len(lookups) != 1 || lookups[0] != "intranet.test" {
		t.Fatalf("lookups = %v", lookups)
	}
}

func TestControlChecksTheDialedAddress(t *testing.T) {
	f := New(Options{})
	for _, addr := range []string{"127.0.0.1:80", "[::ffff:127.0.0.1]:443", "[fe80::1%eth0]:80", "not-an-address"} {
		if err := f.control("tcp", addr, nil); !errors.Is(err, ErrPrivateAddress) {
			t.Errorf("control(%s) = %v, want ErrPrivateAddress", addr, err)
		}
	}
	if err := f.control("tcp", "93.184.216.34:443", nil); err != nil {
		t.Errorf("public address refused: %v", err)
	}
	if err := New(Options{AllowPrivate: true}).control("tcp", "127.0.0.1:80", nil); err != nil {
		t.Errorf("AllowPrivate still refused loopback: %v", err)
	}
}
