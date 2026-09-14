package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"io/fs"
	"strings"
	"testing"
	"time"
)

// relativeTimeSHA256 is the sha256 of dist/bundle.js in
// @github/relative-time-element 5.3.1, whose npm tarball has shasum
// 827f899c5945b032e1f96a991164c0fd4ccf52a6.
const relativeTimeSHA256 = "f091f47715db47fdf5d02a7027fadee93259ef580c1b71dcbece975e49348495"

func TestRelativeTimeHelpers(t *testing.T) {
	at := time.Date(2026, 8, 26, 15, 5, 0, 0, time.FixedZone("CEST", 2*3600))
	cases := []struct {
		name string
		got  template.HTML
		want string
	}{
		{"relative", relativeTime(at), `<relative-time datetime="2026-08-26T13:05:00Z" no-title>Aug 26, 2026</relative-time>`},
		{"relative pointer", relativeTime(&at), `<relative-time datetime="2026-08-26T13:05:00Z" no-title>Aug 26, 2026</relative-time>`},
		{"relative zero", relativeTime(time.Time{}), ""},
		{"relative nil pointer", relativeTime((*time.Time)(nil)), ""},
		{"relative not a time", relativeTime("2026-08-26"), ""},
		{"precise", preciseTime(at), `<relative-time datetime="2026-08-26T13:05:00Z" format="datetime" weekday="long" year="numeric" month="short" day="numeric" hour="numeric" minute="2-digit" no-title>Wednesday, Aug 26, 2026, 1:05 PM UTC</relative-time>`},
		{"precise zero", preciseTime(time.Time{}), ""},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, c.got, c.want)
		}
	}
}

func TestTimeAgoTemplateFuncEmitsRelativeTime(t *testing.T) {
	tmpl := template.Must(template.New("t").Funcs(funcMap).Parse(
		`<span>{{ timeAgo .T }}</span>|{{ preciseTime .T }}|{{ $s := "" }}{{ with .T }}{{ $s = timeAgo . }}{{ end }}{{ with $s }}{{ . }}{{ else }}Never{{ end }}|{{ timeAgo .Z }}`))
	var buf bytes.Buffer
	data := map[string]any{"T": time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC), "Z": time.Time{}}
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(buf.String(), "|")
	if len(parts) != 4 {
		t.Fatalf("output %q", buf.String())
	}
	if parts[0] != `<span><relative-time datetime="2026-09-13T09:00:00Z" no-title>Sep 13, 2026</relative-time></span>` {
		t.Errorf("timeAgo rendered %q", parts[0])
	}
	if !strings.HasPrefix(parts[1], `<relative-time datetime="2026-09-13T09:00:00Z" format="datetime"`) {
		t.Errorf("preciseTime rendered %q", parts[1])
	}
	if !strings.HasPrefix(parts[2], "<relative-time ") {
		t.Errorf("timeAgo through a variable rendered %q", parts[2])
	}
	if parts[3] != "" {
		t.Errorf("zero time rendered %q", parts[3])
	}
	// Plain Go callers keep the text form.
	if got := timeAgo(time.Now().Add(-3 * time.Hour)); got != "3 hours ago" {
		t.Errorf("timeAgo = %q", got)
	}
}

func TestRelativeTimeElementVendored(t *testing.T) {
	data, err := fs.ReadFile(embedded, "static/vendor/relative-time-element-5.3.1.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || !bytes.Contains(data, []byte("RelativeTimeElement")) || !bytes.Contains(data, []byte("export {")) {
		t.Fatalf("vendored relative-time-element is not the ES module bundle (%d bytes)", len(data))
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != relativeTimeSHA256 {
		t.Errorf("sha256 = %s, want %s", got, relativeTimeSHA256)
	}
	if _, err := fs.Stat(embedded, "static/vendor/LICENSE-relative-time-element"); err != nil {
		t.Errorf("licence not embedded: %v", err)
	}
}
