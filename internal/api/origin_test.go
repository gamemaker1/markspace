package api

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestRequestOrigin(t *testing.T) {
	cases := []struct {
		name       string
		host       string
		tls        bool
		proto      string
		fwdHost    string
		trustProxy bool
		want       string
	}{
		{"plain", "notes.test:9000", false, "", "", false, "http://notes.test:9000"},
		{"tls", "notes.test", true, "", "", false, "https://notes.test"},
		{"proxy headers ignored when untrusted", "10.0.0.5:8080", false, "https", "notes.test", false, "http://10.0.0.5:8080"},
		{"proxy headers honoured when trusted", "10.0.0.5:8080", false, "https, http", "notes.test, other", true, "https://notes.test"},
		{"bogus proto ignored", "notes.test", false, "javascript", "", true, "http://notes.test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Host = tc.host
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			if tc.fwdHost != "" {
				r.Header.Set("X-Forwarded-Host", tc.fwdHost)
			}
			if got := RequestOrigin(r, tc.trustProxy); got != tc.want {
				t.Fatalf("RequestOrigin = %q, want %q", got, tc.want)
			}
		})
	}

	a := &Authenticator{}
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "notes.test:1234"
	if got := a.Origin(r, ""); got != "http://notes.test:1234" {
		t.Fatalf("Origin without base = %q", got)
	}
	if got := a.Origin(r, "https://public.example/"); got != "https://public.example" {
		t.Fatalf("Origin with base = %q", got)
	}
}
