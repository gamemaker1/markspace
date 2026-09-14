package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/core"
)

// fakeUpload is one file received on /upload/image.
type fakeUpload struct {
	Filename    string
	ContentType string
	Data        []byte
	Overwrite   string
	Type        string
}

// fakeComfy is a ComfyUI server for tests. The job id is always job-1.
type fakeComfy struct {
	t      *testing.T
	server *httptest.Server

	mu sync.Mutex
	// pendingPolls is how many history polls answer {} before the job is done.
	pendingPolls int
	// queueStates is consumed by each GET /queue. The last state repeats.
	// Values are running, pending and gone.
	queueStates []string
	neverDone   bool
	failed      bool
	outputs     []comfyFile
	files       map[string][]byte
	comboModels bool

	events     []string
	uploads    []fakeUpload
	workflows  []map[string]any
	rawPrompts []string
	clientIDs  []string
	polls      int
	queueCalls int
	interrupts []map[string]any
	deletes    []map[string]any
	views      []url.Values
}

func newFakeComfy(t *testing.T) *fakeComfy {
	f := &fakeComfy{
		t:           t,
		queueStates: []string{"running"},
		outputs:     []comfyFile{{Filename: "out-1.png", Type: "output"}},
		files:       map[string][]byte{"out-1.png": testPNG(t)},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /upload/image", f.upload)
	mux.HandleFunc("POST /prompt", f.prompt)
	mux.HandleFunc("GET /queue", f.queue)
	mux.HandleFunc("POST /queue", func(w http.ResponseWriter, r *http.Request) {
		f.record("delete", r, &f.deletes)
	})
	mux.HandleFunc("GET /history/{id}", f.history)
	mux.HandleFunc("GET /view", f.view)
	mux.HandleFunc("POST /interrupt", func(w http.ResponseWriter, r *http.Request) {
		f.record("interrupt", r, &f.interrupts)
	})
	mux.HandleFunc("GET /object_info/CheckpointLoaderSimple", f.objectInfo)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeComfy) log(event string) {
	f.events = append(f.events, event)
}

func (f *fakeComfy) upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		f.t.Errorf("upload form: %v", err)
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		f.t.Errorf("upload file: %v", err)
		http.Error(w, "no image", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, _ := io.ReadAll(file)
	f.mu.Lock()
	f.log("upload")
	f.uploads = append(f.uploads, fakeUpload{
		Filename: header.Filename, ContentType: header.Header.Get("Content-Type"), Data: data,
		Overwrite: r.FormValue("overwrite"), Type: r.FormValue("type"),
	})
	f.mu.Unlock()
	json.NewEncoder(w).Encode(map[string]string{"name": header.Filename, "subfolder": "", "type": "input"})
}

func (f *fakeComfy) prompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt   json.RawMessage `json:"prompt"`
		ClientID string          `json:"client_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Errorf("prompt body: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(body.Prompt))
	dec.UseNumber()
	var workflow map[string]any
	if err := dec.Decode(&workflow); err != nil {
		f.t.Errorf("prompt workflow %s: %v", body.Prompt, err)
	}
	f.mu.Lock()
	f.log("prompt")
	f.workflows = append(f.workflows, workflow)
	f.rawPrompts = append(f.rawPrompts, string(body.Prompt))
	f.clientIDs = append(f.clientIDs, body.ClientID)
	f.mu.Unlock()
	io.WriteString(w, `{"prompt_id":"job-1","number":7,"node_errors":{}}`)
}

func (f *fakeComfy) queue(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.log("queue")
	state := f.queueStates[min(f.queueCalls, len(f.queueStates)-1)]
	f.queueCalls++
	f.mu.Unlock()
	switch state {
	case "running":
		io.WriteString(w, `{"queue_running":[[7,"job-1",{},{},["9"]]],"queue_pending":[]}`)
	case "pending":
		io.WriteString(w, `{"queue_running":[[5,"other-a",{},{},[]]],"queue_pending":[[7,"job-1",{},{},[]],[6,"other-b",{},{},[]],[9,"other-c",{},{},[]]]}`)
	default:
		io.WriteString(w, `{"queue_running":[],"queue_pending":[]}`)
	}
}

func (f *fakeComfy) history(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.log("history")
	f.polls++
	done := !f.neverDone && f.polls > f.pendingPolls && r.PathValue("id") == "job-1"
	outputs, failed := f.outputs, f.failed
	f.mu.Unlock()
	if !done {
		io.WriteString(w, `{}`)
		return
	}
	status := map[string]any{"status_str": "success", "completed": true,
		"messages": []any{[]any{"execution_start", map[string]any{"prompt_id": "job-1"}}}}
	if failed {
		status = map[string]any{"status_str": "error", "completed": false, "messages": []any{
			[]any{"execution_start", map[string]any{"prompt_id": "job-1"}},
			[]any{"execution_error", map[string]any{"node_type": "KSampler", "exception_message": "CUDA out of memory"}},
		}}
	}
	json.NewEncoder(w).Encode(map[string]any{"job-1": map[string]any{
		"prompt": []any{7, "job-1", map[string]any{}, map[string]any{}, []string{"9"}},
		"outputs": map[string]any{
			"9":  map[string]any{"images": outputs},
			"12": map[string]any{"images": []comfyFile{{Filename: "preview.png", Type: "temp"}}},
		},
		"status": status,
	}})
}

func (f *fakeComfy) view(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.log("view")
	f.views = append(f.views, r.URL.Query())
	data, ok := f.files[r.URL.Query().Get("filename")]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Write(data)
}

func (f *fakeComfy) record(event string, r *http.Request, into *[]map[string]any) {
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log(event)
	*into = append(*into, body)
}

func (f *fakeComfy) objectInfo(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	combo := f.comboModels
	f.mu.Unlock()
	if combo {
		io.WriteString(w, `{"CheckpointLoaderSimple":{"input":{"required":{"ckpt_name":["COMBO",{"options":["z.safetensors","a.safetensors"]}]}}}}`)
		return
	}
	io.WriteString(w, `{"CheckpointLoaderSimple":{"input":{"required":{"ckpt_name":[["sdxl.safetensors","flux.safetensors"],{"tooltip":"x"}]}}}}`)
}

// snapshot copies the recorded calls under the lock.
func (f *fakeComfy) snapshot() fakeComfy {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeComfy{
		events: append([]string(nil), f.events...), uploads: append([]fakeUpload(nil), f.uploads...),
		workflows: append([]map[string]any(nil), f.workflows...), rawPrompts: append([]string(nil), f.rawPrompts...),
		clientIDs: append([]string(nil), f.clientIDs...), polls: f.polls, queueCalls: f.queueCalls,
		interrupts: append([]map[string]any(nil), f.interrupts...), deletes: append([]map[string]any(nil), f.deletes...),
		views: append([]url.Values(nil), f.views...),
	}
}

// comfyGen builds a ComfyUI provider against the fake with a fast poll.
func comfyGen(t *testing.T, f *fakeComfy, mutate func(*core.AIProviderConfig)) *comfyProvider {
	t.Helper()
	gen := imageGen(t, core.ProviderComfyUI, f.server.URL, func(c *core.AIProviderConfig) {
		c.ImageModel = "sdxl.safetensors"
		if mutate != nil {
			mutate(c)
		}
	})
	p := gen.(*comfyProvider)
	p.pollInterval = 5 * time.Millisecond
	return p
}

// inputs returns the inputs of one workflow node.
func inputs(t *testing.T, workflow map[string]any, node string) map[string]any {
	t.Helper()
	n, ok := workflow[node].(map[string]any)
	if !ok {
		t.Fatalf("workflow has no node %s: %v", node, workflow)
	}
	in, ok := n["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("node %s has no inputs: %v", node, n)
	}
	return in
}

func TestComfyGenerate(t *testing.T) {
	f := newFakeComfy(t)
	f.pendingPolls = 2
	second := testJPEG(t, 4, 4)
	f.outputs = []comfyFile{{Filename: "out-1.png", Subfolder: "day1", Type: "output"}, {Filename: "out-2.jpg", Type: "output"}}
	f.files["out-2.jpg"] = second
	gen := comfyGen(t, f, nil)

	var log callLog
	result, err := gen.GenerateImages(context.Background(), core.AIImageRequest{
		Prompt: "a lighthouse", NegativePrompt: "blurry", Size: "832x1216", N: 2, Seed: 42, Queue: log.queue,
	}, log.progress)
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	got := f.snapshot()
	if len(got.workflows) != 1 {
		t.Fatalf("workflows = %d", len(got.workflows))
	}
	wf := got.workflows[0]
	checks := []struct {
		node, input string
		want        any
	}{
		{"4", "ckpt_name", "sdxl.safetensors"},
		{"5", "width", json.Number("832")},
		{"5", "height", json.Number("1216")},
		{"5", "batch_size", json.Number("2")},
		{"3", "seed", json.Number("42")},
		{"3", "steps", json.Number("30")},
		{"6", "text", "a lighthouse"},
		{"7", "text", "blurry"},
	}
	for _, c := range checks {
		if v := inputs(t, wf, c.node)[c.input]; v != c.want {
			t.Errorf("node %s %s = %#v, want %#v", c.node, c.input, v, c.want)
		}
	}
	if !strings.HasPrefix(got.clientIDs[0], "markspace-") {
		t.Errorf("client_id = %q", got.clientIDs[0])
	}
	if len(result.Images) != 2 || result.Images[0].MediaType != "image/png" || result.Images[1].MediaType != "image/jpeg" ||
		!bytes.Equal(result.Images[1].Data, second) {
		t.Fatalf("images = %d", len(result.Images))
	}
	if result.Model != "sdxl.safetensors" || result.Usage != (core.AIImageUsage{ImagesOut: 2}) {
		t.Fatalf("model %q usage %+v", result.Model, result.Usage)
	}
	if len(got.views) != 2 || got.views[0].Get("filename") != "out-1.png" || got.views[0].Get("subfolder") != "day1" ||
		got.views[0].Get("type") != "output" || got.views[1].Get("filename") != "out-2.jpg" {
		t.Fatalf("views = %v", got.views)
	}
	if got.polls != 3 || got.queueCalls != 1 {
		t.Fatalf("history polls %d queue calls %d, want 3 and 1", got.polls, got.queueCalls)
	}
	wantEvents := "prompt,history,queue,history,history,view,view"
	if strings.Join(got.events, ",") != wantEvents {
		t.Fatalf("events = %v, want %s", got.events, wantEvents)
	}
	if got := strings.Join(log.list(), ","); got != "progress 0/2,queue 0,progress 1/2,progress 2/2" {
		t.Fatalf("callbacks = %s", got)
	}
}

func TestComfyRandomSeed(t *testing.T) {
	f := newFakeComfy(t)
	gen := comfyGen(t, f, nil)
	for i := 0; i < 2; i++ {
		if _, err := gen.GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p"}, nil); err != nil {
			t.Fatalf("GenerateImages: %v", err)
		}
	}
	got := f.snapshot()
	var seeds []int64
	for _, wf := range got.workflows {
		seed, err := strconv.ParseInt(string(inputs(t, wf, "3")["seed"].(json.Number)), 10, 64)
		if err != nil || seed <= 0 || seed >= 1<<53 {
			t.Fatalf("seed %v: %v", inputs(t, wf, "3")["seed"], err)
		}
		seeds = append(seeds, seed)
		if w := inputs(t, wf, "5")["width"]; w != json.Number("1024") {
			t.Fatalf("default width = %v", w)
		}
	}
	if seeds[0] == seeds[1] {
		t.Fatalf("two random seeds are equal: %d", seeds[0])
	}
}

func TestComfyEdit(t *testing.T) {
	f := newFakeComfy(t)
	gen := comfyGen(t, f, nil)
	source, mask := testJPEG(t, 64, 48), testPNG(t)

	result, err := gen.GenerateImages(context.Background(), core.AIImageRequest{
		Prompt: "add a boat", N: 3, Source: &core.AIImage{Data: source}, Mask: &core.AIImage{Data: mask},
	}, nil)
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	got := f.snapshot()
	if len(got.uploads) != 2 {
		t.Fatalf("uploads = %d", len(got.uploads))
	}
	up, mk := got.uploads[0], got.uploads[1]
	if !bytes.Equal(up.Data, source) || up.ContentType != "image/jpeg" || !strings.HasSuffix(up.Filename, "-source.jpg") ||
		up.Overwrite != "true" || up.Type != "input" {
		t.Fatalf("source upload = %+v", up)
	}
	if !bytes.Equal(mk.Data, mask) || mk.ContentType != "image/png" || !strings.HasSuffix(mk.Filename, "-mask.png") ||
		mk.Overwrite != "true" || mk.Type != "input" {
		t.Fatalf("mask upload = %+v", mk)
	}
	if up.Filename == mk.Filename {
		t.Fatal("source and mask share a file name")
	}
	wf := got.workflows[0]
	if inputs(t, wf, "10")["image"] != up.Filename || inputs(t, wf, "11")["image"] != mk.Filename {
		t.Fatalf("load nodes = %v %v", inputs(t, wf, "10"), inputs(t, wf, "11"))
	}
	if inputs(t, wf, "3")["denoise"] != json.Number("0.9") || inputs(t, wf, "14")["amount"] != json.Number("3") {
		t.Fatalf("sampler %v batch %v", inputs(t, wf, "3"), inputs(t, wf, "14"))
	}
	if strings.Join(got.events[:3], ",") != "upload,upload,prompt" {
		t.Fatalf("events = %v", got.events)
	}
	if result.Usage.ImagesIn != 1 || result.Usage.ImagesOut != 1 {
		t.Fatalf("usage = %+v", result.Usage)
	}

	// A second run gets new file names, and an edit without a mask sends a
	// clear mask with a lower denoise. The custom template reads the source
	// size.
	custom := `{"1":{"class_type":"X","inputs":{"w":"{{width}}","h":"{{height}}","label":"{{width}}x{{height}}",` +
		`"image":"{{image}}","mask":"{{mask}}","denoise":"{{denoise}}","prompt":"{{prompt}}"}},` +
		`"9":{"class_type":"SaveImage","inputs":{"images":["1",0]}}}`
	gen = comfyGen(t, f, func(c *core.AIProviderConfig) { c.ComfyEdit = custom })
	if _, err := gen.GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p", Source: &core.AIImage{Data: source}}, nil); err != nil {
		t.Fatalf("GenerateImages without mask: %v", err)
	}
	got = f.snapshot()
	if len(got.uploads) != 4 || got.uploads[2].Filename == up.Filename {
		t.Fatalf("uploads = %+v", got.uploads)
	}
	clear, err := png.Decode(bytes.NewReader(got.uploads[3].Data))
	if err != nil {
		t.Fatalf("clear mask is not a PNG: %v", err)
	}
	if _, _, _, a := clear.At(0, 0).RGBA(); a != 0 {
		t.Fatalf("clear mask alpha = %d, want 0", a)
	}
	in := inputs(t, got.workflows[1], "1")
	want := map[string]any{"w": json.Number("64"), "h": json.Number("48"), "label": "64x48", "image": got.uploads[2].Filename,
		"mask": got.uploads[3].Filename, "denoise": json.Number("0.6"), "prompt": "p"}
	if !reflect.DeepEqual(in, want) {
		t.Fatalf("custom inputs = %v, want %v", in, want)
	}
}

func TestComfyQueueAndProgressOrder(t *testing.T) {
	f := newFakeComfy(t)
	f.pendingPolls = 3
	f.queueStates = []string{"pending", "pending", "running"}
	f.outputs = []comfyFile{{Filename: "out-1.png", Type: "output"}, {Filename: "out-1.png", Type: "output"}}
	gen := comfyGen(t, f, nil)
	var log callLog
	if _, err := gen.GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p", N: 2, Queue: log.queue}, log.progress); err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	want := "progress 0/2,queue 2,queue 0,progress 1/2,progress 2/2"
	if got := strings.Join(log.list(), ","); got != want {
		t.Fatalf("callbacks = %s, want %s", got, want)
	}
}

func TestComfyCancel(t *testing.T) {
	for _, tc := range []struct {
		state      string
		interrupts int
	}{{"running", 1}, {"pending", 0}} {
		t.Run(tc.state, func(t *testing.T) {
			f := newFakeComfy(t)
			f.neverDone = true
			f.queueStates = []string{tc.state}
			gen := comfyGen(t, f, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := gen.GenerateImages(ctx, core.AIImageRequest{Prompt: "p", Queue: func(int) { cancel() }}, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error %v, want context.Canceled", err)
			}
			got := f.snapshot()
			if len(got.interrupts) != tc.interrupts || len(got.deletes) != 1 {
				t.Fatalf("interrupts %v deletes %v", got.interrupts, got.deletes)
			}
			if !reflect.DeepEqual(got.deletes[0], map[string]any{"delete": []any{"job-1"}}) {
				t.Fatalf("delete body = %v", got.deletes[0])
			}
			if tc.interrupts == 1 && got.interrupts[0]["prompt_id"] != "job-1" {
				t.Fatalf("interrupt body = %v", got.interrupts[0])
			}
		})
	}
}

func TestComfyTimeout(t *testing.T) {
	f := newFakeComfy(t)
	f.neverDone = true
	gen := comfyGen(t, f, func(c *core.AIProviderConfig) { c.TimeoutSeconds = 1 })
	start := time.Now()
	_, err := gen.GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed < time.Second || elapsed > 8*time.Second {
		t.Fatalf("elapsed %v, want about 1 s", elapsed)
	}
	if got := f.snapshot(); len(got.interrupts) != 1 {
		t.Fatalf("interrupts = %v, want one", got.interrupts)
	}
}

func TestComfyImageTimeoutDefault(t *testing.T) {
	gen := comfyGen(t, newFakeComfy(t), func(c *core.AIProviderConfig) { c.TimeoutSeconds = 0 })
	if got := gen.imageTimeout(); got != 300*time.Second {
		t.Fatalf("image timeout = %v, want 300 s", got)
	}
}

func TestComfyFailures(t *testing.T) {
	t.Run("missing outputs", func(t *testing.T) {
		f := newFakeComfy(t)
		f.outputs = nil
		_, err := comfyGen(t, f, nil).GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p"}, nil)
		if err == nil || !strings.Contains(err.Error(), "without output images") {
			t.Fatalf("error %v", err)
		}
		if got := f.snapshot(); len(got.views) != 0 || len(got.interrupts) != 0 {
			t.Fatalf("views %v interrupts %v", got.views, got.interrupts)
		}
	})
	t.Run("execution error", func(t *testing.T) {
		f := newFakeComfy(t)
		f.failed = true
		_, err := comfyGen(t, f, nil).GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p"}, nil)
		if err == nil || !strings.Contains(err.Error(), "KSampler: CUDA out of memory") {
			t.Fatalf("error %v", err)
		}
	})
	t.Run("oversize output", func(t *testing.T) {
		withImageLimits(t, 64, 1<<20)
		f := newFakeComfy(t)
		f.files["out-1.png"] = append(testPNG(t), make([]byte, 200)...)
		_, err := comfyGen(t, f, nil).GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p"}, nil)
		if !errors.Is(err, core.ErrTooLarge) {
			t.Fatalf("error %v, want ErrTooLarge", err)
		}
	})
	t.Run("oversize total", func(t *testing.T) {
		img := testPNG(t)
		withImageLimits(t, 1<<20, int64(len(img))+10)
		f := newFakeComfy(t)
		f.outputs = []comfyFile{{Filename: "out-1.png", Type: "output"}, {Filename: "out-1.png", Type: "output"}}
		_, err := comfyGen(t, f, nil).GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p", N: 2}, nil)
		if !errors.Is(err, core.ErrTooLarge) {
			t.Fatalf("error %v, want ErrTooLarge", err)
		}
	})
	t.Run("wrong media type", func(t *testing.T) {
		f := newFakeComfy(t)
		f.files["out-1.png"] = testGIF
		_, err := comfyGen(t, f, nil).GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p"}, nil)
		if !errors.Is(err, core.ErrUnsupported) {
			t.Fatalf("error %v, want ErrUnsupported", err)
		}
	})
	t.Run("missing view file", func(t *testing.T) {
		f := newFakeComfy(t)
		f.outputs = []comfyFile{{Filename: "gone.png", Type: "output"}}
		_, err := comfyGen(t, f, nil).GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p"}, nil)
		if !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("error %v, want ErrNotFound", err)
		}
	})
	t.Run("job vanished", func(t *testing.T) {
		f := newFakeComfy(t)
		f.neverDone = true
		f.queueStates = []string{"gone"}
		_, err := comfyGen(t, f, nil).GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p"}, nil)
		if err == nil || !strings.Contains(err.Error(), "neither queued") {
			t.Fatalf("error %v", err)
		}
	})
	t.Run("no checkpoint", func(t *testing.T) {
		f := newFakeComfy(t)
		_, err := comfyGen(t, f, func(c *core.AIProviderConfig) { c.ImageModel = "" }).
			GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p"}, nil)
		if !errors.Is(err, core.ErrInvalid) {
			t.Fatalf("error %v, want ErrInvalid", err)
		}
		if got := f.snapshot(); len(got.events) != 0 {
			t.Fatalf("server saw %v", got.events)
		}
	})
	t.Run("bad template", func(t *testing.T) {
		f := newFakeComfy(t)
		_, err := comfyGen(t, f, func(c *core.AIProviderConfig) { c.ComfyGenerate = `{"1": ` }).
			GenerateImages(context.Background(), core.AIImageRequest{Prompt: "p"}, nil)
		if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
			t.Fatalf("error %v", err)
		}
	})
}

func TestComfyTemplateInjection(t *testing.T) {
	f := newFakeComfy(t)
	gen := comfyGen(t, f, nil)
	prompt := `a cat"}, "evil": {"class_type": "SaveImage", "inputs": {"x": "{{seed}}`
	if _, err := gen.GenerateImages(context.Background(), core.AIImageRequest{Prompt: prompt, NegativePrompt: `\"}]}`, Seed: 5}, nil); err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	got := f.snapshot()
	wf := got.workflows[0]
	if _, ok := wf["evil"]; ok {
		t.Fatalf("the prompt added a node: %s", got.rawPrompts[0])
	}
	var keys []string
	for k := range wf {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "3,4,5,6,7,8,9" {
		t.Fatalf("nodes = %v", keys)
	}
	text := inputs(t, wf, "6")
	if text["text"] != prompt || len(text) != 2 {
		t.Fatalf("prompt node inputs = %v", text)
	}
	if inputs(t, wf, "7")["text"] != `\"}]}` {
		t.Fatalf("negative = %v", inputs(t, wf, "7")["text"])
	}

	// Placeholders inside longer strings are replaced once, so placeholder
	// text inside the prompt stays literal.
	custom := `{"6":{"class_type":"CLIPTextEncode","inputs":{"text":"masterpiece, {{prompt}}, seed {{seed}} {{unknown}}"}}}`
	gen = comfyGen(t, f, func(c *core.AIProviderConfig) { c.ComfyGenerate = custom })
	if _, err := gen.GenerateImages(context.Background(), core.AIImageRequest{Prompt: `x {{seed}} "y"`, Seed: 11}, nil); err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	got = f.snapshot()
	if v := inputs(t, got.workflows[1], "6")["text"]; v != `masterpiece, x {{seed}} "y", seed 11 {{unknown}}` {
		t.Fatalf("text = %q", v)
	}
}

func TestComfyModelsAndChat(t *testing.T) {
	f := newFakeComfy(t)
	gen := comfyGen(t, f, nil)
	models, err := gen.ListModels(context.Background())
	if err != nil || strings.Join(models, ",") != "flux.safetensors,sdxl.safetensors" {
		t.Fatalf("models %v: %v", models, err)
	}
	f.mu.Lock()
	f.comboModels = true
	f.mu.Unlock()
	models, err = gen.ListModels(context.Background())
	if err != nil || strings.Join(models, ",") != "a.safetensors,z.safetensors" {
		t.Fatalf("combo models %v: %v", models, err)
	}
	if err := gen.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	msgs := []core.AIMessage{{Role: "user", Content: "hi"}}
	if _, err := gen.Complete(context.Background(), msgs, core.AIOptions{}); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := gen.Stream(context.Background(), msgs, core.AIOptions{}); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("Stream: %v", err)
	}
	if _, err := gen.Embed(context.Background(), []string{"x"}, ""); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("Embed: %v", err)
	}
}

func TestComfyEmbeddedTemplatesFill(t *testing.T) {
	values := map[string]any{
		comfyCheckpoint: "c", comfyPrompt: "p", comfyNegative: "n", comfyWidth: int64(1), comfyHeight: int64(2),
		comfySeed: int64(3), comfyBatch: int64(4), comfyImage: "i", comfyMask: "m", comfyDenoise: 0.5,
	}
	for name, tmpl := range map[string]string{"generate": comfyGenerateTemplate, "edit": comfyEditTemplate} {
		out, err := fillComfyTemplate([]byte(tmpl), values)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(string(out), "{{") {
			t.Fatalf("%s keeps a placeholder: %s", name, out)
		}
	}
}

// FuzzComfyTemplate fills random templates with random prompts. A template
// that is a JSON object must fill without error, the output must be valid
// JSON, and each string must be replaced exactly as the rules say.
func FuzzComfyTemplate(f *testing.F) {
	f.Add(comfyGenerateTemplate, "a cat", "", int64(1))
	f.Add(comfyEditTemplate, `"}, "evil": {"`, `\`, int64(0))
	f.Add(`{"a":"{{prompt}}","b":["x {{seed}} y",1,null,true,{"c":"{{width}}"}],"{{prompt}}":"{{negative}}"}`, "{{seed}}", "{{prompt}}", int64(-5))
	f.Add(`{"a":"{{denoise}}{{batch}}{{","b":1e400}`, "\xff\xfe", " ", int64(1<<62))
	f.Add(`[]`, "p", "n", int64(2))
	f.Add(`{"a":`, "p", "n", int64(2))
	f.Add(`{} {}`, "p", "n", int64(2))

	f.Fuzz(func(t *testing.T, tmpl, prompt, negative string, seed int64) {
		values := map[string]any{
			comfyCheckpoint: "ckpt", comfyPrompt: prompt, comfyNegative: negative,
			comfyWidth: int64(1024), comfyHeight: int64(768), comfySeed: seed, comfyBatch: int64(2),
			comfyImage: "in.png", comfyMask: "mask.png", comfyDenoise: 0.75,
		}
		out, err := fillComfyTemplate([]byte(tmpl), values)
		trimmed := strings.TrimSpace(tmpl)
		isObject := json.Valid([]byte(tmpl)) && strings.HasPrefix(trimmed, "{")
		if err != nil {
			if isObject {
				t.Fatalf("a JSON object failed to fill: %v\ntemplate: %q", err, tmpl)
			}
			return
		}
		if !isObject {
			t.Fatalf("a template that is not a JSON object filled\ntemplate: %q", tmpl)
		}
		if !json.Valid(out) {
			t.Fatalf("output is not valid JSON: %q\ntemplate: %q prompt: %q", out, tmpl, prompt)
		}
		if !utf8.ValidString(prompt) || !utf8.ValidString(negative) {
			// The encoder replaces invalid bytes, so exact comparison does not
			// apply. Valid JSON is the property that matters here.
			return
		}
		pairs := []string{}
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			pairs = append(pairs, k, placeholderText(values[k]))
		}
		text := strings.NewReplacer(pairs...)
		compareFilled(t, decodeNumbers(t, []byte(tmpl)), decodeNumbers(t, out), values, text, tmpl)
	})
}

// decodeNumbers decodes JSON keeping numbers as json.Number.
func decodeNumbers(t *testing.T, data []byte) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return v
}

// compareFilled walks a template and its filled output side by side.
func compareFilled(t *testing.T, tmpl, out any, values map[string]any, text *strings.Replacer, raw string) {
	t.Helper()
	switch tv := tmpl.(type) {
	case string:
		want := any(tv)
		if value, ok := values[tv]; ok {
			want = value
			switch v := value.(type) {
			case int64:
				want = json.Number(strconv.FormatInt(v, 10))
			case float64:
				want = json.Number(strconv.FormatFloat(v, 'f', -1, 64))
			}
		} else if strings.Contains(tv, "{{") {
			want = text.Replace(tv)
		}
		if out != want {
			t.Fatalf("string %q became %#v, want %#v\ntemplate: %q", tv, out, want, raw)
		}
	case map[string]any:
		ov, ok := out.(map[string]any)
		if !ok || len(ov) != len(tv) {
			t.Fatalf("object %v became %#v\ntemplate: %q", tv, out, raw)
		}
		for k, child := range tv {
			oc, ok := ov[k]
			if !ok {
				t.Fatalf("key %q is missing from the output\ntemplate: %q", k, raw)
			}
			compareFilled(t, child, oc, values, text, raw)
		}
	case []any:
		ov, ok := out.([]any)
		if !ok || len(ov) != len(tv) {
			t.Fatalf("array %v became %#v\ntemplate: %q", tv, out, raw)
		}
		for i := range tv {
			compareFilled(t, tv[i], ov[i], values, text, raw)
		}
	default:
		if !reflect.DeepEqual(tmpl, out) {
			t.Fatalf("value %#v became %#v\ntemplate: %q", tmpl, out, raw)
		}
	}
}
