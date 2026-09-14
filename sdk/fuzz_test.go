package sdk

import (
	"strings"
	"testing"
	"time"
)

// FuzzVerify checks that Verify never panics and accepts only the exact
// header Sign produces for that secret, method, path, delivery id and body
// inside the window.
func FuzzVerify(f *testing.F) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	body := []byte(`{"events":[]}`)
	const now = int64(1789300000)
	good := signUnix(secret, now, "POST", "/events", "d1", body)
	f.Add(secret, good, "POST", "/events", "d1", body, now)
	f.Add(secret, good, "POST", "/events", "d1", body, now+300)
	f.Add(secret, good, "POST", "/events", "d1", body, now+301)
	f.Add(secret, good, "post", "/events", "d1", body, now)
	f.Add(secret, good, "POST", "/commands/x", "d1", body, now)
	f.Add(secret, good, "POST", "/events", "d2", body, now)
	f.Add(secret, good, "POST", "/events\nd1", "", body, now)
	f.Add(secret, strings.ToUpper(good), "POST", "/events", "d1", body, now)
	f.Add(secret, good+",v1=00", "POST", "/events", "d1", body, now)
	f.Add(secret, strings.Replace(good, "t=", "t=0", 1), "POST", "/events", "d1", body, now)
	f.Add([]byte{}, signUnix(nil, 1, "", "", "", nil), "", "", "", []byte{}, int64(1))
	f.Add(secret, "t=9223372036854775807,v1="+strings.Repeat("0", 64), "POST", "/", "d", body, int64(9223372036854775807))
	f.Add(secret, "t=1,v1="+strings.Repeat("a", 64), "POST", "/", "d", body, int64(-9223372036854775808))
	f.Add(secret, "", "POST", "/events", "d1", body, now)
	v := loadVectors(f)
	for _, c := range v.Signature.Verify {
		f.Add(b64(f, c.SecretBase64), c.Header, c.Method, c.Path, c.DeliveryID, b64(f, c.BodyBase64), c.Now)
	}
	f.Fuzz(func(t *testing.T, secret []byte, header, method, path, delivery string, body []byte, nowUnix int64) {
		err := Verify(secret, header, method, path, delivery, body, time.Unix(nowUnix, 0), DefaultWindow)
		if err == nil {
			ts, _, perr := ParseSignature(header)
			if perr != nil {
				t.Fatalf("accepted header that does not parse: %q", header)
			}
			if header != signUnix(secret, ts, method, path, delivery, body) {
				t.Fatalf("accepted header not produced by Sign: %q", header)
			}
			if diffExceeds(nowUnix, ts, int64(DefaultWindow/time.Second)) {
				t.Fatalf("accepted header outside window: t=%d now=%d", ts, nowUnix)
			}
		} else if _, ok := err.(*SignatureError); !ok {
			t.Fatalf("error of type %T", err)
		}
		if nowUnix > 0 {
			h := signUnix(secret, nowUnix, method, path, delivery, body)
			if err := Verify(secret, h, method, path, delivery, body, time.Unix(nowUnix, 0), DefaultWindow); err != nil {
				t.Fatalf("own signature rejected: %v", err)
			}
		}
	})
}
