package sdk

import (
	"container/list"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Request headers on every call from Markspace to a plugin.
const (
	HeaderPlugin    = "X-Markspace-Plugin"
	HeaderDelivery  = "X-Markspace-Delivery"
	HeaderSignature = "X-Markspace-Signature"
)

// DefaultWindow is the largest accepted difference between the signature
// timestamp and the receiver's clock.
const DefaultWindow = 300 * time.Second

// DefaultReplayCapacity is the number of accepted requests a ReplayGuard
// remembers.
const DefaultReplayCapacity = 10000

// Signature error codes.
const (
	CodeMalformed       = "malformed"
	CodeMismatch        = "mismatch"
	CodeExpired         = "expired"
	CodeMissingDelivery = "missing_delivery"
	CodeReplay          = "replay"
	CodeWrongPlugin     = "wrong_plugin"
)

// SignatureError is a rejected request signature. Code is one of the Code
// constants above.
type SignatureError struct {
	Code string
}

func (e *SignatureError) Error() string { return "markspace signature: " + e.Code }

func sigErr(code string) error { return &SignatureError{Code: code} }

// Sign returns the X-Markspace-Signature header value for a request at time
// t. The value is t=<unix seconds>,v1=<hex HMAC-SHA256>. The HMAC covers the
// timestamp, the method, the escaped path without query and the delivery id,
// each followed by a newline, and then the body bytes. The method is used as
// given.
func Sign(secret []byte, t time.Time, method, path, deliveryID string, body []byte) string {
	return signUnix(secret, t.Unix(), method, path, deliveryID, body)
}

func signUnix(secret []byte, t int64, method, path, deliveryID string, body []byte) string {
	ts := strconv.FormatInt(t, 10)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac(secret, ts, method, path, deliveryID, body))
}

func mac(secret []byte, ts, method, path, deliveryID string, body []byte) []byte {
	h := hmac.New(sha256.New, secret)
	_, _ = io.WriteString(h, ts+"\n"+method+"\n"+path+"\n"+deliveryID+"\n")
	h.Write(body)
	return h.Sum(nil)
}

// ParseSignature splits a header into its timestamp and hex digest. The
// grammar is strict. Any text other than t=<decimal>,v1=<64 lowercase hex> is
// malformed. The timestamp has no sign and no leading zero.
func ParseSignature(header string) (int64, string, error) {
	rest, ok := strings.CutPrefix(header, "t=")
	if !ok {
		return 0, "", sigErr(CodeMalformed)
	}
	ts, digest, ok := strings.Cut(rest, ",v1=")
	if !ok || ts == "" || ts[0] == '0' || len(digest) != 64 {
		return 0, "", sigErr(CodeMalformed)
	}
	for i := 0; i < len(ts); i++ {
		if ts[i] < '0' || ts[i] > '9' {
			return 0, "", sigErr(CodeMalformed)
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return 0, "", sigErr(CodeMalformed)
	}
	for i := 0; i < len(digest); i++ {
		c := digest[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return 0, "", sigErr(CodeMalformed)
		}
	}
	return t, digest, nil
}

// Verify checks a signature header against a request. It checks the grammar,
// then the HMAC in constant time, then the timestamp window. A difference of
// exactly window is accepted. The error is a *SignatureError.
func Verify(secret []byte, header, method, path, deliveryID string, body []byte, now time.Time, window time.Duration) error {
	t, digest, err := ParseSignature(header)
	if err != nil {
		return err
	}
	got, _ := hex.DecodeString(digest)
	if !hmac.Equal(got, mac(secret, strconv.FormatInt(t, 10), method, path, deliveryID, body)) {
		return sigErr(CodeMismatch)
	}
	if diffExceeds(now.Unix(), t, int64(window/time.Second)) {
		return sigErr(CodeExpired)
	}
	return nil
}

// diffExceeds reports whether |a - b| > w without integer overflow.
func diffExceeds(a, b, w int64) bool {
	if a < b {
		a, b = b, a
	}
	if b < 0 && a > math.MaxInt64+b {
		return true
	}
	return a-b > w
}

// ReplayGuard rejects a request whose delivery id and timestamp match a
// request it already accepted within the window. Markspace signs every
// delivery attempt with a fresh timestamp, so retries pass. The guard keeps
// its entries ordered by signature timestamp, whatever order requests arrive
// in, so pruning from the oldest end never drops an entry that is still
// inside the window while an expired one stays. When the capacity is
// reached it evicts the entry with the oldest timestamp. It is safe for
// concurrent use.
type ReplayGuard struct {
	mu       sync.Mutex
	capacity int
	window   int64
	order    *list.List
	keys     map[string]*list.Element
}

type replayEntry struct {
	key string
	t   int64
}

// NewReplayGuard returns a guard that remembers up to capacity requests. A
// capacity below 1 means DefaultReplayCapacity. A window below 1 s means
// DefaultWindow.
func NewReplayGuard(capacity int, window time.Duration) *ReplayGuard {
	if capacity < 1 {
		capacity = DefaultReplayCapacity
	}
	if window < time.Second {
		window = DefaultWindow
	}
	return &ReplayGuard{
		capacity: capacity,
		window:   int64(window / time.Second),
		order:    list.New(),
		keys:     map[string]*list.Element{},
	}
}

// Check records a request whose header has already passed Verify. It returns
// missing_delivery for an empty delivery id and replay for a repeat of an
// accepted delivery id and timestamp. A rejected request is not recorded.
func (g *ReplayGuard) Check(deliveryID, header string, now time.Time) error {
	if deliveryID == "" {
		return sigErr(CodeMissingDelivery)
	}
	t, _, err := ParseSignature(header)
	if err != nil {
		return err
	}
	key := strconv.FormatInt(t, 10) + ":" + deliveryID
	g.mu.Lock()
	defer g.mu.Unlock()
	cutoff := now.Unix()
	for e := g.order.Front(); e != nil; e = g.order.Front() {
		ent := e.Value.(*replayEntry)
		if ent.t > cutoff || !diffExceeds(cutoff, ent.t, g.window) {
			break
		}
		g.remove(e)
	}
	if _, ok := g.keys[key]; ok {
		return sigErr(CodeReplay)
	}
	g.keys[key] = g.insert(&replayEntry{key: key, t: t})
	for g.order.Len() > g.capacity {
		g.remove(g.order.Front())
	}
	return nil
}

// insert places an entry after every entry with a timestamp not after its
// own. Requests mostly arrive in order, so the walk from the back is short.
func (g *ReplayGuard) insert(ent *replayEntry) *list.Element {
	for e := g.order.Back(); e != nil; e = e.Prev() {
		if e.Value.(*replayEntry).t <= ent.t {
			return g.order.InsertAfter(ent, e)
		}
	}
	return g.order.PushFront(ent)
}

func (g *ReplayGuard) remove(e *list.Element) {
	delete(g.keys, e.Value.(*replayEntry).key)
	g.order.Remove(e)
}

// VerifySignature checks the signature of a request with DefaultWindow. It
// signs over r.Method, r.URL.EscapedPath() and the delivery header. When guard
// is not nil it also requires a delivery id and rejects replays.
func VerifySignature(secret []byte, r *http.Request, body []byte, now time.Time, guard *ReplayGuard) error {
	header := r.Header.Get(HeaderSignature)
	delivery := r.Header.Get(HeaderDelivery)
	if err := Verify(secret, header, r.Method, r.URL.EscapedPath(), delivery, body, now, DefaultWindow); err != nil {
		return err
	}
	if guard == nil {
		return nil
	}
	return guard.Check(delivery, header, now)
}

// SignRequest sets the plugin, delivery and signature headers on a request
// from Markspace to a plugin. It signs req.Method and req.URL.EscapedPath().
// body must be the exact request body. The plugin must receive the same
// path, so a proxy in front of it must not rewrite the path.
func SignRequest(req *http.Request, secret []byte, pluginID, deliveryID string, body []byte, now time.Time) {
	req.Header.Set(HeaderPlugin, pluginID)
	req.Header.Set(HeaderDelivery, deliveryID)
	req.Header.Set(HeaderSignature, Sign(secret, now, req.Method, req.URL.EscapedPath(), deliveryID, body))
}

var secretRe = regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)

// MinSecretBytes is the shortest accepted signing secret.
const MinSecretBytes = 32

// ErrInvalidSecret reports a signing secret that is not padded standard
// base64 of at least 32 bytes.
var ErrInvalidSecret = errors.New("signing secret must be padded standard base64 of at least 32 bytes")

// DecodeSecret decodes a base64 signing secret as sent in the install payload
// and in MARKSPACE_SIGNING_SECRET.
func DecodeSecret(b64 string) ([]byte, error) {
	if !secretRe.MatchString(b64) {
		return nil, ErrInvalidSecret
	}
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) < MinSecretBytes {
		return nil, ErrInvalidSecret
	}
	return b, nil
}
