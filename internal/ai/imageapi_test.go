package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// testPNG returns a small valid PNG.
func testPNG(t testing.TB) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.NRGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testJPEG returns a valid JPEG of the given size.
func testJPEG(t testing.TB, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testGIF is a GIF header, which image calls must refuse.
var testGIF = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\xff\xff\xff\x00\x00\x00!\xf9\x04\x00\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")

// withImageLimits lowers the image size caps for one test.
func withImageLimits(t *testing.T, perImage, total int64) {
	t.Helper()
	oldPer, oldTotal := maxImageBytes, maxImageResponseBytes
	maxImageBytes, maxImageResponseBytes = perImage, total
	t.Cleanup(func() { maxImageBytes, maxImageResponseBytes = oldPer, oldTotal })
}

// imageGen builds an image capable provider for kind against baseURL.
func imageGen(t *testing.T, kind core.AIProviderKind, baseURL string, mutate func(*core.AIProviderConfig)) core.ImageGenerator {
	t.Helper()
	cfg := &core.AIProviderConfig{
		ID: "img", Kind: kind, BaseURL: baseURL, Model: "chat-model",
		ImageGeneration: true, ImageEditing: true, TimeoutSeconds: 5,
	}
	if mutate != nil {
		mutate(cfg)
	}
	provider, err := testRegistry().New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	gen, ok := provider.(core.ImageGenerator)
	if !ok {
		t.Fatalf("%s provider is not an image generator", kind)
	}
	return gen
}

// callLog records progress and queue callbacks in order.
type callLog struct {
	mu     sync.Mutex
	events []string
}

func (l *callLog) progress(done, total int) {
	l.add(fmt.Sprintf("progress %d/%d", done, total))
}

func (l *callLog) queue(position int) {
	l.add(fmt.Sprintf("queue %d", position))
}

func (l *callLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *callLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

// b64Images renders an image response with base64 data.
func b64Images(revised string, images ...[]byte) string {
	type item struct {
		B64JSON       string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt,omitempty"`
	}
	out := struct {
		Data []item `json:"data"`
	}{}
	for _, img := range images {
		out.Data = append(out.Data, item{B64JSON: base64.StdEncoding.EncodeToString(img), RevisedPrompt: revised})
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

func TestOpenAIGenerateBase64(t *testing.T) {
	pngData, jpegData := testPNG(t), testJPEG(t, 8, 8)
	var body []byte
	var path, auth, extra string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth, extra = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Extra")
		body = readAll(t, r)
		reply := b64Images("a red square", pngData, jpegData)
		reply = strings.TrimSuffix(reply, "}") + `,"usage":{"input_tokens":17,"output_tokens":4160}}`
		io.WriteString(w, reply)
	}))
	defer server.Close()

	gen := imageGen(t, core.ProviderOpenAI, server.URL+"/v1", func(c *core.AIProviderConfig) {
		c.APIKey = "sk-image"
		c.Headers = map[string]string{"X-Extra": "yes"}
		c.ImageModel = "gpt-image-1"
	})
	var log callLog
	result, err := gen.GenerateImages(context.Background(), core.AIImageRequest{
		Prompt: "red square", NegativePrompt: "blue", Size: "512x512", N: 2, Seed: 7,
	}, log.progress)
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if path != "/v1/images/generations" || auth != "Bearer sk-image" || extra != "yes" {
		t.Fatalf("path %q auth %q extra %q", path, auth, extra)
	}
	var sent map[string]any
	decodeBody(t, body, &sent)
	want := map[string]any{"model": "gpt-image-1", "prompt": "red square", "size": "512x512", "n": float64(2), "response_format": "b64_json"}
	if len(sent) != len(want) {
		t.Fatalf("request = %v, want %v", sent, want)
	}
	for k, v := range want {
		if sent[k] != v {
			t.Fatalf("request %s = %v, want %v", k, sent[k], v)
		}
	}
	if len(result.Images) != 2 || result.Images[0].MediaType != "image/png" || result.Images[1].MediaType != "image/jpeg" ||
		!bytes.Equal(result.Images[0].Data, pngData) || !bytes.Equal(result.Images[1].Data, jpegData) {
		t.Fatalf("images = %+v", result.Images)
	}
	if result.RevisedPrompt != "a red square" || result.Model != "gpt-image-1" {
		t.Fatalf("revised %q model %q", result.RevisedPrompt, result.Model)
	}
	if result.Usage != (core.AIImageUsage{ImagesOut: 2, PromptTokens: 17, CompletionTokens: 4160}) {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if got := strings.Join(log.list(), ","); got != "progress 0/2,progress 2/2" {
		t.Fatalf("progress = %s", got)
	}
}

func TestOpenAIGenerateURLSameHostOnly(t *testing.T) {
	pngData, jpegData := testPNG(t), testJPEG(t, 4, 4)
	var otherHits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		w.Write(pngData)
	}))
	defer other.Close()

	var reply atomic.Value
	var fileAuth atomic.Value
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/images/generations":
			io.WriteString(w, strings.ReplaceAll(reply.Load().(string), "SELF", server.URL))
		case "/files/a.png":
			fileAuth.Store(r.Header.Get("Authorization"))
			w.Write(pngData)
		case "/files/b.jpg":
			w.Write(jpegData)
		case "/files/big.png":
			w.Write(append(append([]byte(nil), pngData...), make([]byte, 400)...))
		case "/files/redirect":
			http.Redirect(w, r, other.URL+"/x.png", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	gen := imageGen(t, core.ProviderOpenAI, server.URL+"/v1", func(c *core.AIProviderConfig) { c.APIKey = "k" })
	req := core.AIImageRequest{Prompt: "p", N: 2}

	reply.Store(`{"data":[{"url":"SELF/files/a.png","revised_prompt":"r"},{"url":"/files/b.jpg"}]}`)
	result, err := gen.GenerateImages(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if len(result.Images) != 2 || result.Images[0].MediaType != "image/png" || result.Images[1].MediaType != "image/jpeg" ||
		result.RevisedPrompt != "r" || fileAuth.Load() != "Bearer k" {
		t.Fatalf("result = %+v auth %v", result, fileAuth.Load())
	}

	for name, body := range map[string]string{
		"other host": `{"data":[{"url":"` + other.URL + `/x.png"}]}`,
		"redirect":   `{"data":[{"url":"/files/redirect"}]}`,
	} {
		reply.Store(body)
		_, err := gen.GenerateImages(context.Background(), req, nil)
		if !errors.Is(err, core.ErrForbidden) {
			t.Fatalf("%s: error %v, want ErrForbidden", name, err)
		}
	}
	if otherHits.Load() != 0 {
		t.Fatalf("the other host was contacted %d times", otherHits.Load())
	}

	withImageLimits(t, 256, 1<<20)
	reply.Store(`{"data":[{"url":"/files/big.png"}]}`)
	if _, err := gen.GenerateImages(context.Background(), req, nil); !errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("oversize url image: error %v, want ErrTooLarge", err)
	}
}

func TestOpenAIEditSendsMultipart(t *testing.T) {
	source, mask := testJPEG(t, 16, 16), testPNG(t)
	type received struct {
		fields               map[string]string
		imageType, maskType  string
		imageData, maskData  []byte
		imageName, maskName  string
		path, auth, formType string
	}
	var got received
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.auth = r.URL.Path, r.Header.Get("Authorization")
		got.formType = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		got.fields = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			got.fields[k] = v[0]
		}
		read := func(field string) ([]byte, string, string) {
			file, header, err := r.FormFile(field)
			if err != nil {
				t.Errorf("form file %s: %v", field, err)
				return nil, "", ""
			}
			defer file.Close()
			data, _ := io.ReadAll(file)
			return data, header.Header.Get("Content-Type"), header.Filename
		}
		got.imageData, got.imageType, got.imageName = read("image")
		got.maskData, got.maskType, got.maskName = read("mask")
		io.WriteString(w, b64Images("", testPNG(t)))
	}))
	defer server.Close()

	gen := imageGen(t, core.ProviderOpenAI, server.URL, func(c *core.AIProviderConfig) {
		c.APIKey = "sk-edit"
		c.ImageModel = "gpt-image-1"
		c.ImageSizes = []string{"1024x1024", "auto"}
	})
	result, err := gen.GenerateImages(context.Background(), core.AIImageRequest{
		Prompt: "add a hat", Size: "auto", N: 3,
		Source: &core.AIImage{Data: source}, Mask: &core.AIImage{MediaType: "image/png", Data: mask},
	}, nil)
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if got.path != "/v1/images/edits" || got.auth != "Bearer sk-edit" || !strings.HasPrefix(got.formType, "multipart/form-data") {
		t.Fatalf("path %q auth %q type %q", got.path, got.auth, got.formType)
	}
	wantFields := map[string]string{"prompt": "add a hat", "model": "gpt-image-1", "size": "auto", "n": "3", "response_format": "b64_json"}
	if len(got.fields) != len(wantFields) {
		t.Fatalf("fields = %v", got.fields)
	}
	for k, v := range wantFields {
		if got.fields[k] != v {
			t.Fatalf("field %s = %q, want %q", k, got.fields[k], v)
		}
	}
	if !bytes.Equal(got.imageData, source) || got.imageType != "image/jpeg" || got.imageName != "image.jpg" {
		t.Fatalf("image part: %d bytes, type %q name %q", len(got.imageData), got.imageType, got.imageName)
	}
	if !bytes.Equal(got.maskData, mask) || got.maskType != "image/png" || got.maskName != "mask.png" {
		t.Fatalf("mask part: %d bytes, type %q name %q", len(got.maskData), got.maskType, got.maskName)
	}
	if result.Usage.ImagesIn != 1 || result.Usage.ImagesOut != 1 {
		t.Fatalf("usage = %+v", result.Usage)
	}
}

func TestOpenAIEditWithoutMaskOrSize(t *testing.T) {
	var fields map[string][]string
	var files []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		fields = r.MultipartForm.Value
		for name := range r.MultipartForm.File {
			files = append(files, name)
		}
		io.WriteString(w, b64Images("", testPNG(t)))
	}))
	defer server.Close()
	gen := imageGen(t, core.ProviderOpenAI, server.URL, nil)
	_, err := gen.GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p", Source: &core.AIImage{Data: testPNG(t)}}, nil)
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if _, ok := fields["size"]; ok {
		t.Fatalf("an edit without a size sent size %v", fields["size"])
	}
	if _, ok := fields["model"]; ok {
		t.Fatalf("a provider without imageModel sent model %v", fields["model"])
	}
	if len(files) != 1 || files[0] != "image" {
		t.Fatalf("files = %v", files)
	}
}

func TestOpenAIImageErrors(t *testing.T) {
	var reply atomic.Value
	var status atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code := status.Load(); code != 0 {
			w.WriteHeader(int(code))
		}
		io.WriteString(w, reply.Load().(string))
	}))
	defer server.Close()
	gen := imageGen(t, core.ProviderOpenAI, server.URL, func(c *core.AIProviderConfig) { c.APIKey = "sk-secret" })
	req := core.AIImageRequest{Prompt: "p"}
	run := func(body string, code int) error {
		reply.Store(body)
		status.Store(int32(code))
		_, err := gen.GenerateImages(context.Background(), req, nil)
		return err
	}

	if err := run(`{"data":[{"b64_json":`, 0); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("malformed JSON: %v", err)
	}
	if err := run(`{"data":[]}`, 0); err == nil || !strings.Contains(err.Error(), "no images") {
		t.Fatalf("empty data: %v", err)
	}
	if err := run(`{"data":[{"b64_json":"!!!"}]}`, 0); err == nil {
		t.Fatal("bad base64 passed")
	}
	if err := run(`{"data":[{}]}`, 0); err == nil {
		t.Fatal("an item without data passed")
	}
	if err := run(`{"error":{"message":"content policy"}}`, 0); err == nil || !strings.Contains(err.Error(), "content policy") {
		t.Fatalf("error body in 200: %v", err)
	}
	if err := run(b64Images("", testGIF), 0); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("GIF: error %v, want ErrUnsupported", err)
	}
	if err := run(b64Images("", []byte("plain text, not an image")), 0); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("text: error %v, want ErrUnsupported", err)
	}

	err := run(`{"error":{"message":"size is not allowed for key sk-secret"}}`, http.StatusBadRequest)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || !strings.Contains(apiErr.Body, "size is not allowed") ||
		strings.Contains(apiErr.Body, "sk-secret") {
		t.Fatalf("400: %v", err)
	}
	if err := run(`{"error":{"message":"bad key"}}`, http.StatusUnauthorized); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("401: %v", err)
	}

	withImageLimits(t, 64, 1<<20)
	big := append(append([]byte(nil), testPNG(t)...), make([]byte, 200)...)
	if err := run(b64Images("", big), 0); !errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("oversize image: error %v, want ErrTooLarge", err)
	}
	withImageLimits(t, 1<<20, 256)
	if err := run(b64Images("", big, big), 0); !errors.Is(err, core.ErrTooLarge) {
		t.Fatalf("oversize response: error %v, want ErrTooLarge", err)
	}
}

func TestOpenAIImageNClamping(t *testing.T) {
	var sentN atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body openaiImageRequest
		json.NewDecoder(r.Body).Decode(&body)
		sentN.Store(int32(body.N))
		img := testPNG(t)
		io.WriteString(w, b64Images("", img, img, img, img, img, img))
	}))
	defer server.Close()
	gen := imageGen(t, core.ProviderOpenAI, server.URL, nil)
	for _, tc := range []struct{ n, sent, images int }{{0, 1, 1}, {-3, 1, 1}, {1, 1, 1}, {3, 3, 3}, {4, 4, 4}, {9, 4, 4}} {
		result, err := gen.GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p", N: tc.n}, nil)
		if err != nil {
			t.Fatalf("N=%d: %v", tc.n, err)
		}
		if int(sentN.Load()) != tc.sent || len(result.Images) != tc.images {
			t.Fatalf("N=%d: sent n=%d, got %d images, want %d and %d", tc.n, sentN.Load(), len(result.Images), tc.sent, tc.images)
		}
	}
}

func TestOpenAIImageSizeValidation(t *testing.T) {
	var hits atomic.Int32
	var sentSize atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var body openaiImageRequest
		json.NewDecoder(r.Body).Decode(&body)
		sentSize.Store(body.Size)
		io.WriteString(w, b64Images("", testPNG(t)))
	}))
	defer server.Close()

	open := imageGen(t, core.ProviderOpenAI, server.URL, nil)
	listed := imageGen(t, core.ProviderOpenAI, server.URL, func(c *core.AIProviderConfig) {
		c.ImageSizes = []string{"768x768", "1024x1024"}
	})
	tests := []struct {
		name string
		gen  core.ImageGenerator
		size string
		want string // the size sent, or "" for a validation error
	}{
		{"default without list", open, "", "1024x1024"},
		{"free size", open, "1536x1024", "1536x1024"},
		{"largest", open, "4096x4096", "4096x4096"},
		{"three digits", open, "512x512", "512x512"},
		{"two digits", open, "99x512", ""},
		{"five digits", open, "10000x1024", ""},
		{"over 4096", open, "4097x1024", ""},
		{"zero", open, "000x512", ""},
		{"no x", open, "1024", ""},
		{"trailing", open, "1024x1024x", ""},
		{"letters", open, "abcxdef", ""},
		{"spaces inside", open, "1024 x 1024", ""},
		{"auto without list", open, "auto", ""},
		{"default from list", listed, "", "768x768"},
		{"listed", listed, "1024x1024", "1024x1024"},
		{"not listed", listed, "512x512", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := hits.Load()
			_, err := tc.gen.GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p", Size: tc.size}, nil)
			if tc.want == "" {
				if !errors.Is(err, core.ErrInvalid) {
					t.Fatalf("error %v, want ErrInvalid", err)
				}
				if hits.Load() != before {
					t.Fatal("an invalid size reached the server")
				}
				return
			}
			if err != nil {
				t.Fatalf("GenerateImages: %v", err)
			}
			if sentSize.Load() != tc.want {
				t.Fatalf("sent size %v, want %s", sentSize.Load(), tc.want)
			}
		})
	}
}

func TestOpenAIImageRequestValidation(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, b64Images("", testPNG(t)))
	}))
	defer server.Close()
	gen := imageGen(t, core.ProviderOpenAI, server.URL, nil)
	noEdit := imageGen(t, core.ProviderOpenAI, server.URL, func(c *core.AIProviderConfig) { c.ImageEditing = false })
	noGenerate := imageGen(t, core.ProviderOpenAI, server.URL, func(c *core.AIProviderConfig) { c.ImageGeneration = false })
	png := &core.AIImage{Data: testPNG(t)}

	tests := []struct {
		name string
		gen  core.ImageGenerator
		req  core.AIImageRequest
		want error
	}{
		{"empty prompt", gen, core.AIImageRequest{Prompt: "  "}, core.ErrInvalid},
		{"negative seed", gen, core.AIImageRequest{Prompt: "p", Seed: -1}, core.ErrInvalid},
		{"mask without source", gen, core.AIImageRequest{Prompt: "p", Mask: png}, core.ErrInvalid},
		{"gif source", gen, core.AIImageRequest{Prompt: "p", Source: &core.AIImage{Data: testGIF}}, core.ErrInvalid},
		{"jpeg mask", gen, core.AIImageRequest{Prompt: "p", Source: png, Mask: &core.AIImage{Data: testJPEG(t, 2, 2)}}, core.ErrInvalid},
		{"empty source", gen, core.AIImageRequest{Prompt: "p", Source: &core.AIImage{}}, core.ErrInvalid},
		{"long prompt", gen, core.AIImageRequest{Prompt: strings.Repeat("a", 32001)}, core.ErrInvalid},
		{"editing off", noEdit, core.AIImageRequest{Prompt: "p", Source: png}, core.ErrUnsupported},
		{"generation off", noGenerate, core.AIImageRequest{Prompt: "p"}, core.ErrUnsupported},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.gen.GenerateImages(context.Background(), tc.req, nil); !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatalf("invalid requests reached the server %d times", hits.Load())
	}
	if _, err := noGenerate.GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p", Source: png}, nil); err != nil {
		t.Fatalf("an edit on a provider with editing only failed: %v", err)
	}
}

func TestOpenAIImageRetriesWithoutResponseFormat(t *testing.T) {
	var bodies []map[string]any
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		if _, ok := body["response_format"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":{"message":"Unknown parameter: 'response_format'."}}`)
			return
		}
		io.WriteString(w, b64Images("", testPNG(t)))
	}))
	defer server.Close()
	gen := imageGen(t, core.ProviderOpenAI, server.URL, nil)
	for i := 0; i < 2; i++ {
		if _, err := gen.GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p"}, nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want 3: one refused, one retry, one without the field", len(bodies))
	}
	for i, body := range bodies[1:] {
		if _, ok := body["response_format"]; ok {
			t.Fatalf("request %d still sent response_format", i+2)
		}
	}
}
