package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestFileResponsesAreConfined uploads files of several types and checks the
// headers that stop an uploaded file from running script in the app origin.
func TestFileResponsesAreConfined(t *testing.T) {
	h := newHarness(t)
	space := h.signupOwner()

	cases := []struct {
		name, body  string
		disposition string // prefix
		sandboxed   bool
	}{
		{"evil.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>fetch("/api/tokens",{method:"POST"})</script></svg>`, "attachment", true},
		{"photo.png", "\x89PNG\r\n\x1a\nrest", "inline", true},
		{"notes.txt", "plain words", "inline", true},
		{"page.html", "<script>alert(1)</script>", "attachment", true},
		{"report.pdf", "%PDF-1.4\n%%EOF", "inline", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := h.uploadTo(space.ID, "", tc.name, []byte(tc.body))
			resp, err := h.client.Get(h.server.URL + f.URL)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, tc.disposition) {
				t.Errorf("Content-Disposition = %q, want %s", got, tc.disposition)
			}
			csp := resp.Header.Get("Content-Security-Policy")
			if tc.sandboxed && !strings.HasPrefix(csp, "sandbox;") {
				t.Errorf("Content-Security-Policy = %q, want a sandbox policy", csp)
			}
			if !tc.sandboxed && csp != "" {
				t.Errorf("PDF carries Content-Security-Policy %q, which stops Chrome showing it", csp)
			}
			if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Error("nosniff missing")
			}
		})
	}
}
