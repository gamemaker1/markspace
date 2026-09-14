package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gamemaker1/markspace/internal/ai"
	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/app"
	"github.com/gamemaker1/markspace/internal/config"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/index"
	"github.com/gamemaker1/markspace/internal/markdown"
	"github.com/gamemaker1/markspace/internal/store"
	"github.com/gamemaker1/markspace/internal/web"
	"github.com/gamemaker1/markspace/internal/wiring"
)

// aiCtxModel is a fake OpenAI server that records every chat request body.
type aiCtxModel struct {
	server *httptest.Server
	mu     sync.Mutex
	bodies [][]byte
}

// newAICtxModel starts the fake model server.
func newAICtxModel(t *testing.T) *aiCtxModel {
	t.Helper()
	m := &aiCtxModel{}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"data":[{"id":"vision-model"},{"id":"text-model"}]}`)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.bodies = append(m.bodies, body)
		m.mu.Unlock()
		var req struct {
			Stream bool `json:"stream"`
		}
		json.Unmarshal(body, &req)
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`)
	}))
	t.Cleanup(m.server.Close)
	return m
}

// aiCtxWire is the part of a chat completions request the tests inspect.
type aiCtxWire struct {
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// aiCtxPart is one content part of a message with images.
type aiCtxPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// last decodes the most recent chat request.
func (m *aiCtxModel) last(t *testing.T) aiCtxWire {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bodies) == 0 {
		t.Fatal("the model received no request")
	}
	var out aiCtxWire
	if err := json.Unmarshal(m.bodies[len(m.bodies)-1], &out); err != nil {
		t.Fatalf("decode model request: %v", err)
	}
	return out
}

// count returns how many chat requests the model received.
func (m *aiCtxModel) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.bodies)
}

// text joins the text of every message, from plain contents and text parts.
func (w aiCtxWire) text() string {
	var b strings.Builder
	for _, msg := range w.Messages {
		var s string
		if json.Unmarshal(msg.Content, &s) == nil {
			b.WriteString(s + "\n")
			continue
		}
		var parts []aiCtxPart
		json.Unmarshal(msg.Content, &parts)
		for _, p := range parts {
			b.WriteString(p.Text + "\n")
		}
	}
	return b.String()
}

// imageURLs lists every image_url part of every message.
func (w aiCtxWire) imageURLs() []string {
	var out []string
	for _, msg := range w.Messages {
		var parts []aiCtxPart
		if json.Unmarshal(msg.Content, &parts) != nil {
			continue
		}
		for _, p := range parts {
			if p.Type == "image_url" {
				out = append(out, p.ImageURL.URL)
			}
		}
	}
	return out
}

// newAICtxHarness builds the full stack with AI on, one vision provider and
// one text provider, both on the fake model server. mutate adjusts the
// configuration before the app starts.
func newAICtxHarness(t *testing.T, model *aiCtxModel, mutate func(*config.Config)) *harness {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MARKSPACE_DATA_DIR", dir)
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.BaseURL = ""
	cfg.WatchFiles = false
	cfg.AI.Enabled = true
	provider := func(id string, vision bool) core.AIProviderConfig {
		return core.AIProviderConfig{
			ID: id, Name: id, Kind: core.ProviderOpenAI, BaseURL: model.server.URL + "/v1", Model: id + "-model",
			Vision: vision, Enabled: true, Source: "file", TimeoutSeconds: 20, MaxTokens: 256,
		}
	}
	cfg.AI.Providers = []core.AIProviderConfig{provider("vision", true), provider("text", false)}
	cfg.AI.AllowPrivateURLs = true
	if mutate != nil {
		mutate(cfg)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	files, err := store.New(cfg.DataDir, logger)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	db, err := index.Open(cfg.DBPath, logger)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	renderer := markdown.New(logger)
	backend, err := app.New(app.Deps{
		Files: files, DB: db, Renderer: renderer, AI: ai.NewRegistry(logger, cfg.AI.ContextChunks),
		Hasher: hasher{}, Config: cfg, Logger: logger,
	})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	if err := backend.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { backend.Close() })
	authn := &api.Authenticator{Backend: backend, Logger: logger}
	mount, err := wiring.MountCore(wiring.NewEnv(cfg, backend, authn, logger))
	if err != nil {
		t.Fatalf("modules: %v", err)
	}
	apiSrv, err := api.NewServer(api.Options{Logger: logger, Auth: authn, Mount: mount})
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	webSrv, err := web.NewServer(web.Options{
		Backend: backend, Logger: logger, Auth: authn, SessionTTL: cfg.SessionTTL,
		InstanceName: "Test", MaxUpload: cfg.MaxUploadBytes(), MarkdownCSS: renderer.CSS(),
	})
	if err != nil {
		t.Fatalf("web: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", apiSrv.Handler())
	mux.Handle("/", webSrv.Handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &harness{t: t, cfg: cfg, server: srv, client: &http.Client{Jar: jar}, app: backend}
}

// aiCtxUpload uploads a file into a folder.
func aiCtxUpload(h *harness, spaceID, folderID, name string, data []byte) core.File {
	h.t.Helper()
	return *h.uploadTo(spaceID, folderID, name, data)
}

// aiCtxRun posts to /api/ai/run and returns the status, the decoded result
// and the raw body.
func aiCtxRun(h *harness, body map[string]any) (int, core.AIResult, string) {
	h.t.Helper()
	buf, _ := json.Marshal(body)
	resp, err := h.client.Post(h.server.URL+"/api/ai/run", "application/json", bytes.NewReader(buf))
	if err != nil {
		h.t.Fatalf("ai run: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var res core.AIResult
	if resp.StatusCode == 200 {
		if err := json.Unmarshal(raw, &res); err != nil {
			h.t.Fatalf("decode ai result %s: %v", raw, err)
		}
	}
	return resp.StatusCode, res, string(raw)
}

// aiCtxPNG encodes an opaque width x height PNG with a gradient.
func aiCtxPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// aiCtxPage serves the page a url reference fetches.
func aiCtxPage(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<!doctype html><html><head><title>Release page</title>
<script>var tracker = "SCRIPTCONTENT";</script></head>
<body><nav>Menu Home</nav><h1>Rollback guide</h1><p>Readable rollback paragraph.</p></body></html>`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// aiCtxWorkspace holds the notes and files the tests refer to.
type aiCtxWorkspace struct {
	reader              *harness
	plan, deploy        core.Note
	secret, retro, long core.Note
	image, text         core.File
}

// newAICtxWorkspace signs up an owner, creates the notes and files, and
// logs in a reader who can see every note except the secret one.
func newAICtxWorkspace(t *testing.T, h *harness) *aiCtxWorkspace {
	t.Helper()
	h.mustStatus(h.do("POST", "/api/auth/signup", map[string]string{
		"email": "owner@example.com", "name": "Olive Owner", "password": "correct horse battery",
	}, nil), 201, "signup")
	var spaces struct{ Items []*core.Space }
	h.do("GET", "/api/spaces", nil, &spaces)
	space := spaces.Items[0]
	create := func(title, content string) core.Note {
		var n core.Note
		h.mustStatus(h.do("POST", "/api/notes", core.NoteInput{SpaceID: space.ID, Title: title, Content: content}, &n), 201, "create "+title)
		return n
	}
	ws := &aiCtxWorkspace{}
	ws.plan = create("Release plan", "# Release plan\n\nIntro paragraph.\n\n## Rollout\n\nRoll out region by region.\n\n"+
		"```\n## Not a heading\n```\n\n### Canary\n\nCanary first.\n\n## Rollback\n\nRevert the tag.\n\n"+
		"See [[Deploy guide]] and [[Secret plan]].\n")
	ws.deploy = create("Deploy guide", "Deploy with the blue green switch.\n")
	ws.secret = create("Secret plan", "Walrus budget figures. Back to [[Release plan]].\n")
	ws.retro = create("Retro", "Lessons that link to [[Release plan]].\n")
	ws.long = create("Long note", strings.Repeat("longtext ", 120))
	var assets core.Folder
	h.mustStatus(h.do("POST", "/api/folders", core.FolderInput{SpaceID: space.ID, Name: "Assets"}, &assets), 201, "create assets")
	ws.image = aiCtxUpload(h, space.ID, assets.ID, "diagram.png", aiCtxPNG(t, 3000, 2000))
	ws.text = aiCtxUpload(h, space.ID, assets.ID, "checklist.txt", []byte("Checklist: freeze merges.\n"))

	var reader core.User
	h.mustStatus(h.do("POST", "/api/users", core.UserInput{
		Email: ptr("reader@example.com"), Name: ptr("Rex Reader"), Password: ptr("another good password"),
	}, &reader), 201, "create reader")
	for _, n := range []core.Note{ws.plan, ws.deploy, ws.retro, ws.long} {
		h.mustStatus(h.do("POST", "/api/notes/"+n.ID+"/shares", core.ShareInput{
			NoteID: n.ID, UserID: reader.ID, Access: core.AccessView,
		}, nil), 201, "share "+n.Title)
	}
	h.mustStatus(h.do("POST", "/api/items/"+assets.ID+"/shares", core.ShareInput{
		NoteID: assets.ID, UserID: reader.ID, Access: core.AccessView, IncludeChildren: true,
	}, nil), 201, "share assets")
	ws.reader = h.as("reader@example.com", "another good password")
	return ws
}

// mustOneSource checks that a result has exactly one source and returns it.
func mustOneSource(t *testing.T, res core.AIResult, raw string) core.AISource {
	t.Helper()
	if len(res.Sources) != 1 {
		t.Fatalf("want one source, got %s", raw)
	}
	return res.Sources[0]
}

func TestAIContextEndToEnd(t *testing.T) {
	model := newAICtxModel(t)
	page := aiCtxPage(t)
	h := newAICtxHarness(t, model, nil)
	ws := newAICtxWorkspace(t, h)
	r := ws.reader
	run := func(body map[string]any) (core.AIResult, string, aiCtxWire) {
		t.Helper()
		status, res, raw := aiCtxRun(r, body)
		if status != 200 {
			t.Fatalf("ai run status %d: %s", status, raw)
		}
		return res, raw, model.last(t)
	}
	ref := func(fields ...any) []map[string]any {
		out := map[string]any{}
		for i := 0; i+1 < len(fields); i += 2 {
			out[fields[i].(string)] = fields[i+1]
		}
		return []map[string]any{out}
	}

	t.Run("linked notes without the hidden one", func(t *testing.T) {
		res, raw, wire := run(map[string]any{"task": "summarize", "text": "summary please",
			"context": ref("kind", "linked", "noteId", ws.plan.ID)})
		src := mustOneSource(t, res, raw)
		if src.Kind != core.ContextLinked || src.NoteID != ws.deploy.ID || src.Title != "Deploy guide" || src.Skipped != "" || src.Truncated {
			t.Fatalf("source = %+v", src)
		}
		text := wire.text()
		if !strings.Contains(text, "Deploy with the blue green switch.") || !strings.Contains(text, "[1] linked: Deploy guide") {
			t.Fatalf("model request lacks the linked note:\n%s", text)
		}
		for _, leak := range []string{"Walrus", "Secret plan", ws.secret.ID} {
			if strings.Contains(text, leak) || strings.Contains(raw, leak) {
				t.Fatalf("hidden note leaked %q", leak)
			}
		}
	})

	t.Run("backlinks without the hidden one", func(t *testing.T) {
		res, raw, wire := run(map[string]any{"task": "summarize", "text": "summary please",
			"context": ref("kind", "backlinks", "noteId", ws.plan.ID)})
		src := mustOneSource(t, res, raw)
		if src.Kind != core.ContextBacklinks || src.NoteID != ws.retro.ID || src.Title != "Retro" {
			t.Fatalf("source = %+v", src)
		}
		if strings.Contains(wire.text(), "Walrus") || strings.Contains(raw, ws.secret.ID) {
			t.Fatal("hidden backlink leaked")
		}
	})

	t.Run("hidden note named directly", func(t *testing.T) {
		res, raw, wire := run(map[string]any{"task": "summarize", "text": "summary please",
			"context": ref("kind", "note", "noteId", ws.secret.ID)})
		src := mustOneSource(t, res, raw)
		if src.Skipped != "not found" || src.Title != "" {
			t.Fatalf("source = %+v", src)
		}
		if strings.Contains(wire.text(), "Walrus") || strings.Contains(raw, "Secret") {
			t.Fatal("hidden note leaked")
		}
	})

	t.Run("section text only", func(t *testing.T) {
		res, raw, wire := run(map[string]any{"task": "explain", "text": "explain it",
			"context": ref("kind", "section", "noteId", ws.plan.ID, "heading", "## ROLLOUT")})
		src := mustOneSource(t, res, raw)
		if src.Kind != core.ContextSection || src.Title != "Release plan > Rollout" || src.Path != ws.plan.Path {
			t.Fatalf("source = %+v", src)
		}
		text := wire.text()
		for _, want := range []string{"Roll out region by region.", "Canary first.", "## Not a heading"} {
			if !strings.Contains(text, want) {
				t.Fatalf("section lacks %q:\n%s", want, text)
			}
		}
		for _, gone := range []string{"Revert the tag.", "Intro paragraph."} {
			if strings.Contains(text, gone) {
				t.Fatalf("section keeps %q", gone)
			}
		}
	})

	t.Run("image downscaled for the vision model", func(t *testing.T) {
		res, raw, wire := run(map[string]any{"task": "describe", "providerId": "vision",
			"context": ref("kind", "file", "fileId", ws.image.ID)})
		src := mustOneSource(t, res, raw)
		if src.Kind != core.ContextFile || src.Title != "diagram.png" || src.FileID != ws.image.ID || src.Skipped != "" {
			t.Fatalf("source = %+v", src)
		}
		urls := wire.imageURLs()
		if len(urls) != 1 || !strings.HasPrefix(urls[0], "data:image/jpeg;base64,") {
			t.Fatalf("image parts = %d", len(urls))
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(urls[0], "data:image/jpeg;base64,"))
		if err != nil {
			t.Fatalf("decode data URL: %v", err)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || format != "jpeg" || cfg.Width > 1568 || cfg.Height > 1568 || cfg.Width != 1568 {
			t.Fatalf("image %s %dx%d: %v", format, cfg.Width, cfg.Height, err)
		}
		last := wire.Messages[len(wire.Messages)-1]
		if last.Role != "user" {
			t.Fatalf("images on a %s message", last.Role)
		}
	})

	t.Run("text attachment", func(t *testing.T) {
		res, raw, wire := run(map[string]any{"task": "summarize", "text": "summary please",
			"context": ref("kind", "file", "fileId", ws.text.ID)})
		src := mustOneSource(t, res, raw)
		if src.Title != "checklist.txt" || src.Skipped != "" || !strings.Contains(wire.text(), "Checklist: freeze merges.") {
			t.Fatalf("source = %+v\n%s", src, wire.text())
		}
	})

	t.Run("web page text without scripts", func(t *testing.T) {
		res, raw, wire := run(map[string]any{"task": "summarize",
			"context": ref("kind", "url", "url", page.URL+"/release")})
		src := mustOneSource(t, res, raw)
		if src.Kind != core.ContextURL || src.Title != "Release page" || src.URL != page.URL+"/release" || src.Skipped != "" {
			t.Fatalf("source = %+v", src)
		}
		text := wire.text()
		if !strings.Contains(text, "Readable rollback paragraph.") || !strings.Contains(text, "# Rollback guide") {
			t.Fatalf("model request lacks the page text:\n%s", text)
		}
		if strings.Contains(text, "SCRIPTCONTENT") || strings.Contains(text, "Menu Home") {
			t.Fatalf("page text keeps script or navigation:\n%s", text)
		}
	})

	t.Run("images skipped on a model without vision", func(t *testing.T) {
		withImages := map[string]any{"task": "summarize", "text": "summary please", "providerId": "text",
			"context": []map[string]any{
				{"kind": "file", "fileId": ws.image.ID},
				{"kind": "file", "fileId": ws.text.ID},
			}}
		// A request with images needs a model that accepts them, so routing
		// skips the named text model and the vision model answers.
		res, raw, wire := run(withImages)
		if res.Provider != "vision" || len(res.Sources) != 2 || res.Sources[0].Skipped != "" || len(wire.imageURLs()) != 1 {
			t.Fatalf("with a vision model in the route: %s", raw)
		}

		// With every model that accepts images disabled, a text model answers
		// and the images are left out.
		h.mustStatus(h.do("POST", "/api/ai/providers/vision/refresh", nil, nil), 200, "refresh vision")
		setEnabled := func(on bool) {
			for _, m := range []string{"vision-model", "text-model"} {
				h.mustStatus(h.do("PATCH", "/api/ai/models/vision/"+m, map[string]any{"enabled": on}, nil), 200, "toggle "+m)
			}
		}
		setEnabled(false)
		defer setEnabled(true)
		res, raw, wire = run(withImages)
		if len(res.Sources) != 2 || res.Sources[0].Skipped != "model does not accept images" || res.Sources[1].Skipped != "" {
			t.Fatalf("sources = %s", raw)
		}
		if len(wire.imageURLs()) != 0 {
			t.Fatal("an image reached the text model")
		}
		before := model.count()
		status, _, body := aiCtxRun(r, map[string]any{"task": "describe", "providerId": "text",
			"context": ref("kind", "file", "fileId", ws.image.ID)})
		if status != 400 || !strings.Contains(body, "vision") {
			t.Fatalf("describe on the text model: status %d: %s", status, body)
		}
		if model.count() != before {
			t.Fatal("a refused describe still called the model")
		}
	})

	t.Run("stream done frame carries sources", func(t *testing.T) {
		buf, _ := json.Marshal(map[string]any{"task": "summarize", "text": "summary please",
			"context": ref("kind", "section", "noteId", ws.plan.ID, "heading", "Rollback")})
		resp, err := r.client.Post(r.server.URL+"/api/ai/stream", "application/json", bytes.NewReader(buf))
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var done struct {
			Done    bool            `json:"done"`
			Sources []core.AISource `json:"sources"`
		}
		for _, frame := range strings.Split(string(raw), "\n\n") {
			if !strings.HasPrefix(frame, "event: done\n") {
				continue
			}
			data := strings.TrimPrefix(frame[strings.Index(frame, "\n")+1:], "data: ")
			if err := json.Unmarshal([]byte(data), &done); err != nil {
				t.Fatalf("decode done frame %q: %v", data, err)
			}
		}
		if !done.Done || len(done.Sources) != 1 || done.Sources[0].Kind != core.ContextSection ||
			done.Sources[0].Title != "Release plan > Rollback" {
			t.Fatalf("stream body %s", raw)
		}
	})
}

func TestAIContextBudgetEndToEnd(t *testing.T) {
	model := newAICtxModel(t)
	h := newAICtxHarness(t, model, func(cfg *config.Config) { cfg.AI.ContextChars = 300 })
	ws := newAICtxWorkspace(t, h)
	status, res, raw := aiCtxRun(ws.reader, map[string]any{"task": "summarize", "text": "t", "context": []map[string]any{
		{"kind": "selection", "text": strings.Repeat("s", 200)},
		{"kind": "note", "noteId": ws.long.ID},
		{"kind": "note", "noteId": ws.deploy.ID},
		{"kind": "file", "fileId": ws.text.ID},
	}})
	if status != 200 || len(res.Sources) != 4 {
		t.Fatalf("status %d: %s", status, raw)
	}
	if s := res.Sources[0]; s.Skipped != "" || s.Truncated {
		t.Fatalf("selection = %+v", s)
	}
	if s := res.Sources[1]; s.Skipped != "" || !s.Truncated || s.Title != "Long note" {
		t.Fatalf("cut note = %+v", s)
	}
	for _, s := range res.Sources[2:] {
		if s.Skipped != "context budget reached" {
			t.Fatalf("late reference = %+v", s)
		}
	}
	text := model.last(t).text()
	if strings.Contains(text, "blue green") || strings.Contains(text, "freeze merges") {
		t.Fatal("a reference past the budget reached the model")
	}
	if strings.Count(text, "longtext") > 12 {
		t.Fatalf("the cut note kept %d words", strings.Count(text, "longtext"))
	}
}

func TestAIContextPrivateURLRefused(t *testing.T) {
	model := newAICtxModel(t)
	page := aiCtxPage(t)
	h := newAICtxHarness(t, model, func(cfg *config.Config) { cfg.AI.AllowPrivateURLs = false })
	ws := newAICtxWorkspace(t, h)
	status, res, raw := aiCtxRun(ws.reader, map[string]any{"task": "summarize", "text": "t",
		"context": []map[string]any{{"kind": "url", "url": page.URL}}})
	if status != 200 {
		t.Fatalf("status %d: %s", status, raw)
	}
	src := mustOneSource(t, res, raw)
	if src.Kind != core.ContextURL || !strings.Contains(src.Skipped, "private address") || src.URL != page.URL {
		t.Fatalf("source = %+v", src)
	}
	if strings.Contains(model.last(t).text(), "Readable rollback paragraph.") {
		t.Fatal("the private page reached the model")
	}
}
