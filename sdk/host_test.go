package sdk

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testHost(url string) (*Host, *[]time.Duration) {
	h := NewHost(url, "mspl_test", "word-count")
	var mu sync.Mutex
	sleeps := &[]time.Duration{}
	h.Sleep = func(d time.Duration) {
		mu.Lock()
		*sleeps = append(*sleeps, d)
		mu.Unlock()
	}
	return h, sleeps
}

func TestHostRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mspl_test" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		switch calls.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
		case 2:
			w.WriteHeader(503)
		default:
			w.Write([]byte(`{"pluginId":"word-count"}`))
		}
	}))
	defer srv.Close()
	h, sleeps := testHost(srv.URL)
	me, err := h.Me(context.Background())
	if err != nil || me["pluginId"] != "word-count" {
		t.Fatalf("me %v err %v", me, err)
	}
	if calls.Load() != 3 || len(*sleeps) != 2 {
		t.Fatalf("calls %d sleeps %v", calls.Load(), *sleeps)
	}
	if (*sleeps)[0] != time.Second {
		t.Errorf("first sleep %v, want Retry-After 1s", (*sleeps)[0])
	}
	if d := (*sleeps)[1]; d < 500*time.Millisecond || d >= time.Second {
		t.Errorf("second sleep %v outside backoff range", d)
	}
}

func TestHostRetryAfterOn502AndNo500Retry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		switch {
		case r.URL.Path == "/api/plugin/v1/me" && n == 1:
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(502)
		case r.URL.Path == "/api/plugin/v1/me":
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"internal error"}`))
		}
	}))
	defer srv.Close()
	h, sleeps := testHost(srv.URL)
	if _, err := h.Me(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*sleeps) != 1 || (*sleeps)[0] != 7*time.Second {
		t.Fatalf("sleeps %v, want [7s]", *sleeps)
	}
	calls.Store(0)
	_, err := h.GetItem(context.Background(), "n1")
	var he *HostError
	if !errors.As(err, &he) || he.Status != 500 || calls.Load() != 1 {
		t.Fatalf("500 err %v calls %d", err, calls.Load())
	}
}

func TestHostErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/api/plugin/v1/notes" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"title is required","field":"title"}`))
			return
		}
		w.WriteHeader(502)
	}))
	defer srv.Close()
	h, sleeps := testHost(srv.URL)
	_, err := h.CreateNote(context.Background(), NewNote{SpaceID: "s1"})
	var he *HostError
	if !errors.As(err, &he) || he.Status != 400 || he.Field != "title" || he.Message != "title is required" {
		t.Fatalf("err %#v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("400 retried, calls %d", calls.Load())
	}
	calls.Store(0)
	_, err = h.GetItem(context.Background(), "n1")
	if !errors.As(err, &he) || he.Status != 502 || calls.Load() != 5 || len(*sleeps) != 4 {
		t.Fatalf("err %v calls %d sleeps %d", err, calls.Load(), len(*sleeps))
	}
	if _, err := h.GetItem(context.Background(), "../x"); err == nil || calls.Load() != 5 {
		t.Fatal("invalid id sent")
	}

	srv.Close()
	*sleeps = nil
	_, err = h.GetItem(context.Background(), "n1")
	if !errors.As(err, &he) || he.Status != 0 || len(*sleeps) != 4 {
		t.Fatalf("connection error %v sleeps %d", err, len(*sleeps))
	}
}

func TestHostIdempotencyKey(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	var notes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		if r.URL.Path == "/api/plugin/v1/notes" && notes.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"id":"n1"}`))
	}))
	defer srv.Close()
	h, _ := testHost(srv.URL)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := h.CreateNote(ctx, NewNote{SpaceID: "s1", Title: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.GetItem(ctx, "n1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.UpdateJob(ctx, "j1", JobUpdate{Status: "done"}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 5 {
		t.Fatalf("requests %v", seen)
	}
	key := func(i int) string { return strings.TrimPrefix(strings.TrimPrefix(seen[i], "POST "), "GET ") }
	k0 := key(0)
	if b, err := hex.DecodeString(k0); err != nil || len(k0) != 32 || len(b) != 16 {
		t.Fatalf("key %q is not 32 hex characters", k0)
	}
	if seen[1] != seen[0] {
		t.Errorf("retry changed the key: %q then %q", seen[0], seen[1])
	}
	if !strings.HasPrefix(seen[2], "POST ") || key(2) == k0 || len(key(2)) != 32 {
		t.Errorf("second call key %q, first %q", seen[2], k0)
	}
	if seen[3] != "GET " || seen[4] != "PATCH " {
		t.Errorf("non-POST requests carry a key: %q %q", seen[3], seen[4])
	}
}

func TestHostStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("accept %q key %q", r.Header.Get("Accept"), r.Header.Get("Idempotency-Key"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: delta\ndata: {\"delta\":\"Hel\"}\n\n: ping\n\nevent: delta\ndata: {\"delta\":\"lo\"}\n\nevent: done\ndata: {\"done\":true}\n\n")
	}))
	defer srv.Close()
	h, _ := testHost(srv.URL)
	s, err := h.AICompleteStream(context.Background(), map[string]any{"task": "ask"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var text strings.Builder
	var events []string
	for s.Next() {
		ev := s.Event()
		events = append(events, ev.Event)
		var d struct{ Delta string }
		json.Unmarshal(ev.Data, &d)
		text.WriteString(d.Delta)
	}
	if s.Err() != nil || text.String() != "Hello" || strings.Join(events, ",") != "delta,delta,done" {
		t.Fatalf("events %v text %q err %v", events, text.String(), s.Err())
	}
}

func TestHostUploadRetriesSeekableBody(t *testing.T) {
	var calls atomic.Int32
	var mu sync.Mutex
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("multipart: %v", err)
			w.WriteHeader(400)
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		b, _ := io.ReadAll(f)
		if string(b) != "hello words" || hdr.Filename != "a b.txt" || r.FormValue("spaceId") != "s1" || r.FormValue("onConflict") != "rename" {
			t.Errorf("got %q %q %q", b, hdr.Filename, r.FormValue("spaceId"))
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"id":"f1"}`))
	}))
	defer srv.Close()
	h, _ := testHost(srv.URL)
	out, err := h.UploadFile(context.Background(), Upload{SpaceID: "s1", Name: "a b.txt", MIME: "text/plain", OnConflict: "rename", Body: bytes.NewReader([]byte("hello words"))})
	if err != nil || out["id"] != "f1" || calls.Load() != 2 {
		t.Fatalf("out %v err %v calls %d", out, err, calls.Load())
	}
	if keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("upload keys %v", keys)
	}
	calls.Store(0)
	_, err = h.UploadFile(context.Background(), Upload{SpaceID: "s1", Name: "a b.txt", OnConflict: "rename", Body: io.MultiReader(strings.NewReader("hello words"))})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("non-seekable upload retried: err %v calls %d", err, calls.Load())
	}
}

func TestHostStorage(t *testing.T) {
	var mu sync.Mutex
	kv := map[string]json.RawMessage{}
	var lastEmit map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/api/plugin/v1/kv":
			if r.URL.Query().Get("prefix") != "note/" {
				t.Errorf("prefix %q", r.URL.Query().Get("prefix"))
			}
			switch r.URL.Query().Get("cursor") {
			case "":
				w.Write([]byte(`{"items":[{"key":"note/a","size":3,"updatedAt":"2026-09-13T11:00:00Z"}],"nextCursor":"c2"}`))
			case "c2":
				w.Write([]byte(`{"items":[{"key":"note/b","size":4,"updatedAt":"2026-09-13T11:01:00Z"}]}`))
			default:
				w.WriteHeader(400)
			}
		case strings.HasPrefix(r.URL.Path, "/api/plugin/v1/kv/"):
			key := strings.TrimPrefix(r.URL.Path, "/api/plugin/v1/kv/")
			if strings.Contains(key, "/") && !strings.Contains(r.URL.EscapedPath(), "%2F") {
				t.Errorf("key not escaped: %s", r.URL.EscapedPath())
			}
			switch r.Method {
			case "PUT":
				var in struct {
					Value json.RawMessage `json:"value"`
				}
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Value) == 0 {
					t.Errorf("put body: %v", err)
				}
				kv[key] = in.Value
				w.WriteHeader(204)
			case "GET":
				v, ok := kv[key]
				if !ok {
					w.WriteHeader(404)
					w.Write([]byte(`{"error":"not found"}`))
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"key": key, "value": v, "updatedAt": "2026-09-13T11:00:00Z"})
			}
		case r.URL.Path == "/api/plugin/v1/secrets/api_key":
			w.Write([]byte(`{"name":"api_key","value":"s3cret"}`))
		case r.URL.Path == "/api/plugin/v1/files/f1/text":
			w.Write([]byte(`{"text":"hello","truncated":true}`))
		case r.URL.Path == "/api/plugin/v1/events":
			json.NewDecoder(r.Body).Decode(&lastEmit)
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	h, _ := testHost(srv.URL)
	ctx := context.Background()
	var got map[string]int
	if ok, err := h.KVGet(ctx, "note/n1", &got); ok || err != nil {
		t.Fatalf("missing key: %v %v", ok, err)
	}
	if err := h.KVPut(ctx, "note/n1", map[string]int{"words": 3}); err != nil {
		t.Fatal(err)
	}
	if ok, err := h.KVGet(ctx, "note/n1", &got); !ok || err != nil || got["words"] != 3 {
		t.Fatalf("kv get %v %v %v", ok, err, got)
	}
	item, ok, err := h.KVEntry(ctx, "note/n1")
	if err != nil || !ok || item.Key != "note/n1" || item.UpdatedAt == "" || string(item.Value) != `{"words":3}` {
		t.Fatalf("kv entry %+v %v %v", item, ok, err)
	}
	if err := h.KVPut(ctx, "", 1); err == nil {
		t.Fatal("empty key accepted")
	}
	var keys []string
	cursor := ""
	for pages := 0; pages < 5; pages++ {
		page, err := h.KVList(ctx, "note/", cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range page.Items {
			keys = append(keys, k.Key)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if strings.Join(keys, ",") != "note/a,note/b" {
		t.Fatalf("kv list %v", keys)
	}
	if s, err := h.Secret(ctx, "api_key"); err != nil || s != "s3cret" {
		t.Fatalf("secret %q %v", s, err)
	}
	if ft, err := h.FileText(ctx, "f1"); err != nil || ft.Text != "hello" || !ft.Truncated {
		t.Fatalf("file text %+v %v", ft, err)
	}
	if err := h.Emit(ctx, "counted", "s1", map[string]int{"n": 1}); err != nil || lastEmit["type"] != "plugin.word-count.counted" {
		t.Fatalf("emit %v %v", lastEmit, err)
	}
}

func TestHostPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/plugin/v1/spaces/s1/tree":
			w.Write([]byte(`{"items":[{"kind":"note","note":{"id":"n1"}}],"nextCursor":"t2"}`))
		case "/api/plugin/v1/search":
			if r.URL.Query().Get("cursor") != "c1" || r.URL.Query().Get("q") != "wal" {
				t.Errorf("search query %v", r.URL.Query())
			}
			w.Write([]byte(`{"items":[{"kind":"note","id":"n1"}]}`))
		case "/api/plugin/v1/resolve":
			w.Write([]byte(`{"items":[{"target":"SQLite","kind":"note","id":"n1","path":"Wiki/sqlite.md","url":"/i/n1"}]}`))
		case "/api/plugin/v1/ai/models":
			w.Write([]byte(`{"items":[{"providerId":"p1","model":"m","vision":true,"imageGeneration":false,"imageEditing":true,"prices":{"input":1}}]}`))
		default:
			w.Write([]byte(`{"items":[]}`))
		}
	}))
	defer srv.Close()
	h, _ := testHost(srv.URL)
	ctx := context.Background()
	tree, err := h.Tree(ctx, "s1")
	if err != nil || len(tree.Items) != 1 || tree.Items[0]["kind"] != "note" || tree.NextCursor != "t2" {
		t.Fatalf("tree %+v %v", tree, err)
	}
	if res, err := h.Search(ctx, SearchQuery{Q: "wal", Cursor: "c1"}); err != nil || len(res.Items) != 1 {
		t.Fatalf("search %+v %v", res, err)
	}
	links, err := h.Resolve(ctx, "s1", "n2", []string{"SQLite"})
	if err != nil || len(links.Items) != 1 || links.Items[0].Path != "Wiki/sqlite.md" {
		t.Fatalf("resolve %+v %v", links, err)
	}
	models, err := h.AIModels(ctx)
	if err != nil || len(models.Items) != 1 || models.Items[0].ProviderID != "p1" || !models.Items[0].Vision || !models.Items[0].ImageEditing || string(models.Items[0].Prices) != `{"input":1}` {
		t.Fatalf("models %+v %v", models, err)
	}
	if bl, err := h.Backlinks(ctx, "n1"); err != nil || bl.Items == nil {
		t.Fatalf("backlinks %+v %v", bl, err)
	}
	if ol, err := h.OutboundLinks(ctx, "n1"); err != nil || ol.Items == nil {
		t.Fatalf("outbound %+v %v", ol, err)
	}
}
