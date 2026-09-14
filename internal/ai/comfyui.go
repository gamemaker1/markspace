package ai

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/gamemaker1/markspace/internal/core"
)

// The built-in workflow templates in ComfyUI API format.
var (
	//go:embed comfy/sdxl-generate.json
	comfyGenerateTemplate string
	//go:embed comfy/sdxl-edit.json
	comfyEditTemplate string
)

// ComfyUI template placeholders. A string value equal to one of them is
// replaced by the typed value. A placeholder inside a longer string is
// replaced as text.
const (
	comfyCheckpoint = "{{checkpoint}}"
	comfyPrompt     = "{{prompt}}"
	comfyNegative   = "{{negative}}"
	comfyWidth      = "{{width}}"
	comfyHeight     = "{{height}}"
	comfySeed       = "{{seed}}"
	comfyBatch      = "{{batch}}"
	comfyImage      = "{{image}}"
	comfyMask       = "{{mask}}"
	comfyDenoise    = "{{denoise}}"
)

const (
	// comfyMaskDenoise is the denoise strength of an edit with a mask.
	comfyMaskDenoise = 0.9
	// comfyFullDenoise is the denoise strength of an edit without a mask,
	// which changes the whole image but keeps its composition.
	comfyFullDenoise = 0.6
	// comfyDefaultSide is the width and height used when neither the request
	// nor the source image gives one.
	comfyDefaultSide = 1024
	// maxComfyJSON caps a JSON answer from ComfyUI. A history entry repeats
	// the workflow, so it can be large.
	maxComfyJSON = 16 << 20
	// comfyPollFailures is how many poll errors in a row end a job.
	comfyPollFailures = 3
	// comfyAbandonTimeout bounds the interrupt call after a cancel.
	comfyAbandonTimeout = 5 * time.Second
)

// comfyProvider runs image workflows on a ComfyUI server.
type comfyProvider struct {
	baseProvider
	// pollInterval is the time between history polls.
	pollInterval time.Duration
}

// compile time check that the ComfyUI provider generates images.
var _ core.ImageGenerator = (*comfyProvider)(nil)

// newComfyProvider builds a provider for the ComfyUI protocol.
func newComfyProvider(cfg *core.AIProviderConfig, client *http.Client, logger *slog.Logger) *comfyProvider {
	p := &comfyProvider{
		baseProvider: baseProvider{cfg: cfg, client: client, logger: logger, label: "comfyui"},
		pollInterval: time.Second,
	}
	p.auth = p.setAuth
	return p
}

// setAuth adds a bearer token when the configuration carries a key, for a
// server behind an authenticating proxy.
func (p *comfyProvider) setAuth(h http.Header) {
	if p.cfg.APIKey != "" {
		h.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
}

// Kind returns core.ProviderComfyUI.
func (p *comfyProvider) Kind() core.AIProviderKind { return core.ProviderComfyUI }

// Complete reports core.ErrUnsupported because ComfyUI has no chat API.
func (p *comfyProvider) Complete(context.Context, []core.AIMessage, core.AIOptions) (*core.AIResult, error) {
	return nil, fmt.Errorf("comfyui: chat: %w", core.ErrUnsupported)
}

// Stream reports core.ErrUnsupported because ComfyUI has no chat API.
func (p *comfyProvider) Stream(context.Context, []core.AIMessage, core.AIOptions) (<-chan core.AIChunk, error) {
	return nil, fmt.Errorf("comfyui: chat: %w", core.ErrUnsupported)
}

// Embed reports core.ErrUnsupported because ComfyUI has no embedding API.
func (p *comfyProvider) Embed(context.Context, []string, string) ([][]float32, error) {
	return nil, fmt.Errorf("comfyui: embeddings: %w", core.ErrUnsupported)
}

// comfyObjectInfo is the node description returned by /object_info/{node}.
type comfyObjectInfo map[string]struct {
	Input struct {
		Required map[string][]json.RawMessage `json:"required"`
	} `json:"input"`
}

// ListModels returns the sorted checkpoint names the server offers.
func (p *comfyProvider) ListModels(ctx context.Context) ([]string, error) {
	callCtx, cancel := p.withTimeout(ctx)
	defer cancel()

	target := endpoint(p.cfg.BaseURL, "/object_info/CheckpointLoaderSimple")
	var info comfyObjectInfo
	if err := p.getLimited(callCtx, target, &info); err != nil {
		return nil, err
	}
	spec := info["CheckpointLoaderSimple"].Input.Required["ckpt_name"]
	if len(spec) == 0 {
		return nil, fmt.Errorf("comfyui: %s lists no ckpt_name input", target)
	}
	// Older servers send [[names...], {...}]. Newer servers send
	// ["COMBO", {"options": [names...]}].
	var names []string
	if err := json.Unmarshal(spec[0], &names); err != nil {
		var combo struct {
			Options []string `json:"options"`
		}
		if len(spec) < 2 || json.Unmarshal(spec[1], &combo) != nil {
			return nil, fmt.Errorf("comfyui: %s has an unknown ckpt_name format", target)
		}
		names = combo.Options
	}
	models := make([]string, 0, len(names))
	for _, name := range names {
		if name != "" {
			models = append(models, name)
		}
	}
	sort.Strings(models)
	return models, nil
}

// Health lists the checkpoints.
func (p *comfyProvider) Health(ctx context.Context) error {
	_, err := p.ListModels(ctx)
	return err
}

// getLimited sends a GET request and decodes at most maxComfyJSON bytes of
// the JSON answer into out.
func (p *comfyProvider) getLimited(ctx context.Context, target string, out any) error {
	resp, err := p.do(ctx, http.MethodGet, target, nil, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxComfyJSON)).Decode(out); err != nil {
		return fmt.Errorf("comfyui: decode %s: %w", target, err)
	}
	return nil
}

// comfyQueued is the answer to POST /prompt.
type comfyQueued struct {
	PromptID   string          `json:"prompt_id"`
	NodeErrors json.RawMessage `json:"node_errors"`
}

// GenerateImages fills the generate or edit workflow template, queues it,
// polls the history until the outputs appear and downloads them. A canceled
// or timed out call removes a waiting job from the queue, or interrupts a
// running one.
func (p *comfyProvider) GenerateImages(ctx context.Context, req core.AIImageRequest, progress func(done, total int)) (*core.AIImageResult, error) {
	job, err := prepareImageJob(p.cfg, req)
	if err != nil {
		return nil, err
	}
	if job.size != "" && job.width == 0 {
		return nil, core.Invalid("size", "ComfyUI needs WIDTHxHEIGHT, not "+job.size)
	}
	callCtx, cancel := context.WithTimeout(ctx, p.imageTimeout())
	defer cancel()

	tmpl := p.cfg.ComfyGenerate
	if job.edit {
		tmpl = p.cfg.ComfyEdit
		if tmpl == "" {
			tmpl = comfyEditTemplate
		}
	} else if tmpl == "" {
		tmpl = comfyGenerateTemplate
	}
	checkpoint := req.Model
	if checkpoint == "" {
		checkpoint = p.cfg.ImageModel
	}
	if checkpoint == "" && strings.Contains(tmpl, comfyCheckpoint) {
		return nil, core.Invalid("imageModel", "names no checkpoint for the ComfyUI workflow")
	}

	width, height := job.width, job.height
	if width == 0 {
		width, height = comfyDefaultSide, comfyDefaultSide
		if job.edit {
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(req.Source.Data)); err == nil {
				width, height = cfg.Width, cfg.Height
			}
		}
	}
	seed := req.Seed
	if seed == 0 {
		seed = randomSeed()
	}
	values := map[string]any{
		comfyCheckpoint: checkpoint,
		comfyPrompt:     req.Prompt,
		comfyNegative:   req.NegativePrompt,
		comfyWidth:      int64(width),
		comfyHeight:     int64(height),
		comfySeed:       seed,
		comfyBatch:      int64(job.n),
	}
	usage := core.AIImageUsage{}
	if job.edit {
		usage.ImagesIn = 1
		sourceName, err := p.upload(callCtx, req.Source, "source")
		if err != nil {
			return nil, err
		}
		mask, denoise := req.Mask, comfyMaskDenoise
		if mask == nil {
			mask, denoise = &core.AIImage{MediaType: "image/png", Data: clearMaskPNG()}, comfyFullDenoise
		}
		maskName, err := p.upload(callCtx, mask, "mask")
		if err != nil {
			return nil, err
		}
		values[comfyImage] = sourceName
		values[comfyMask] = maskName
		values[comfyDenoise] = denoise
	}

	workflow, err := fillComfyTemplate([]byte(tmpl), values)
	if err != nil {
		return nil, err
	}
	target := endpoint(p.cfg.BaseURL, "/prompt")
	payload := map[string]any{"prompt": json.RawMessage(workflow), "client_id": "markspace-" + randomHex(8)}
	var queued comfyQueued
	if err := p.postJSON(callCtx, target, payload, &queued); err != nil {
		return nil, err
	}
	if queued.PromptID == "" {
		return nil, fmt.Errorf("comfyui: %s returned no prompt_id: %s", target, collapseSpace(string(queued.NodeErrors)))
	}
	reportProgress(progress, 0, job.n)

	images, state, err := p.wait(callCtx, queued.PromptID, req.Queue, progress)
	if err != nil {
		if callCtx.Err() != nil {
			p.abandon(ctx, queued.PromptID, state)
			return nil, fmt.Errorf("comfyui: prompt %s: %w", queued.PromptID, callCtx.Err())
		}
		return nil, err
	}
	usage.ImagesOut = len(images)
	return &core.AIImageResult{Images: images, Model: checkpoint, Usage: usage}, nil
}

// fillComfyTemplate parses a workflow template and replaces the placeholders
// in its string values. A string equal to a placeholder becomes the typed
// value. Placeholders inside longer strings are replaced as text in a single
// pass, so replaced text is never scanned again. The work happens on the
// parsed tree and the result is encoded again, so no value can change the
// JSON structure.
func fillComfyTemplate(tmpl []byte, values map[string]any) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(tmpl))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("comfyui: workflow template is not valid JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("comfyui: workflow template has data after the JSON object")
	}
	if _, ok := root.(map[string]any); !ok {
		return nil, errors.New("comfyui: workflow template is not a JSON object")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, 2*len(keys))
	for _, key := range keys {
		pairs = append(pairs, key, placeholderText(values[key]))
	}
	filled := substitutePlaceholders(root, values, strings.NewReplacer(pairs...))
	out, err := json.Marshal(filled)
	if err != nil {
		return nil, fmt.Errorf("comfyui: encode workflow: %w", err)
	}
	return out, nil
}

// placeholderText renders a placeholder value for use inside a longer string.
func placeholderText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

// substitutePlaceholders walks a decoded JSON value and replaces placeholders
// in string values. Object keys stay as they are.
func substitutePlaceholders(v any, values map[string]any, text *strings.Replacer) any {
	switch t := v.(type) {
	case string:
		if value, ok := values[t]; ok {
			return value
		}
		if strings.Contains(t, "{{") {
			return text.Replace(t)
		}
		return t
	case map[string]any:
		for key, child := range t {
			t[key] = substitutePlaceholders(child, values, text)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = substitutePlaceholders(child, values, text)
		}
		return t
	default:
		return v
	}
}

// clearMaskPNG returns a small fully transparent PNG. ComfyUI reads the
// inverted alpha channel as the mask, so this mask lets the whole image
// change. The sampler scales the mask to the image size.
func clearMaskPNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// comfyUploaded is the answer to POST /upload/image.
type comfyUploaded struct {
	Name      string `json:"name"`
	Subfolder string `json:"subfolder"`
}

// upload sends an image to the ComfyUI input folder under a unique name and
// returns the name a LoadImage node reads.
func (p *comfyProvider) upload(ctx context.Context, img *core.AIImage, role string) (string, error) {
	mediaType := http.DetectContentType(img.Data)
	filename := "markspace-" + randomHex(12) + "-" + role + imageExtension(mediaType)

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="%s"`, filename))
	header.Set("Content-Type", mediaType)
	part, err := form.CreatePart(header)
	if err == nil {
		_, err = part.Write(img.Data)
	}
	if err == nil {
		err = form.WriteField("overwrite", "true")
	}
	if err == nil {
		err = form.WriteField("type", "input")
	}
	if err == nil {
		err = form.Close()
	}
	if err != nil {
		return "", fmt.Errorf("comfyui: build upload form: %w", err)
	}

	target := endpoint(p.cfg.BaseURL, "/upload/image")
	resp, err := p.doBody(ctx, http.MethodPost, target, &body, form.FormDataContentType(), "application/json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out comfyUploaded
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxComfyJSON)).Decode(&out); err != nil {
		return "", fmt.Errorf("comfyui: decode %s: %w", target, err)
	}
	if out.Name == "" {
		return "", fmt.Errorf("comfyui: %s returned no file name", target)
	}
	if out.Subfolder != "" {
		return out.Subfolder + "/" + out.Name, nil
	}
	return out.Name, nil
}

// comfyJobState is what the poller last saw of a job.
type comfyJobState int

const (
	comfyUnknown comfyJobState = iota
	comfyPending
	comfyRunning
	comfyDone
)

// comfyFile names one output file.
type comfyFile struct {
	Filename  string `json:"filename"`
	Subfolder string `json:"subfolder"`
	Type      string `json:"type"`
}

// comfyHistoryEntry is one job in GET /history/{id}.
type comfyHistoryEntry struct {
	Outputs map[string]struct {
		Images []comfyFile `json:"images"`
	} `json:"outputs"`
	Status *struct {
		StatusStr string              `json:"status_str"`
		Completed bool                `json:"completed"`
		Messages  [][]json.RawMessage `json:"messages"`
	} `json:"status"`
}

// wait polls the history of a job until it finishes, reporting the queue
// position while the job waits. It returns the downloaded images and the
// last seen state of the job.
func (p *comfyProvider) wait(ctx context.Context, id string, queue func(int), progress func(done, total int)) ([]core.AIImage, comfyJobState, error) {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	state := comfyUnknown
	lastPosition := -1
	failures := 0
	missing := 0
	for {
		entry, found, err := p.history(ctx, id)
		switch {
		case err != nil && ctx.Err() != nil:
			return nil, state, err
		case err != nil:
			failures++
			if failures >= comfyPollFailures {
				return nil, state, err
			}
			p.logger.Warn("comfyui history poll failed", "provider", p.cfg.ID, "prompt", id, "error", err)
		case found:
			failures, missing = 0, 0
			if entry.Status != nil && entry.Status.StatusStr == "error" {
				return nil, comfyDone, fmt.Errorf("comfyui: prompt %s failed: %s", id, comfyErrorMessage(entry))
			}
			if entry.Status == nil || entry.Status.Completed {
				images, err := p.collect(ctx, id, entry, progress)
				return images, comfyDone, err
			}
		default:
			failures = 0
		}

		if !found && state != comfyRunning {
			position, queued, err := p.queuePosition(ctx, id)
			switch {
			case err != nil && ctx.Err() != nil:
				return nil, state, err
			case err != nil:
				p.logger.Debug("comfyui queue poll failed", "provider", p.cfg.ID, "error", err)
			case queued:
				missing = 0
				state = comfyPending
				if position == 0 {
					state = comfyRunning
				}
				if queue != nil && position != lastPosition {
					queue(position)
				}
				lastPosition = position
			default:
				// A job leaves the queue and enters the history under one lock,
				// so a job missing from both for several polls is gone.
				missing++
				if missing >= comfyPollFailures {
					return nil, state, fmt.Errorf("comfyui: prompt %s is neither queued nor in the history", id)
				}
			}
		}

		select {
		case <-ctx.Done():
			return nil, state, ctx.Err()
		case <-ticker.C:
		}
	}
}

// history reads the history entry of one job. found is false while the job
// has not finished.
func (p *comfyProvider) history(ctx context.Context, id string) (*comfyHistoryEntry, bool, error) {
	target := endpoint(p.cfg.BaseURL, "/history/"+url.PathEscape(id))
	var out map[string]*comfyHistoryEntry
	if err := p.getLimited(ctx, target, &out); err != nil {
		return nil, false, err
	}
	entry, ok := out[id]
	if !ok || entry == nil {
		return nil, false, nil
	}
	return entry, true, nil
}

// queuePosition reports how many jobs run or wait ahead of the job. queued is
// false when the job is not in the queue.
func (p *comfyProvider) queuePosition(ctx context.Context, id string) (position int, queued bool, err error) {
	target := endpoint(p.cfg.BaseURL, "/queue")
	var out struct {
		Running [][]json.RawMessage `json:"queue_running"`
		Pending [][]json.RawMessage `json:"queue_pending"`
	}
	if err := p.getLimited(ctx, target, &out); err != nil {
		return 0, false, err
	}
	for _, item := range out.Running {
		if _, itemID := comfyQueueItem(item); itemID == id {
			return 0, true, nil
		}
	}
	ours := -1.0
	for _, item := range out.Pending {
		if number, itemID := comfyQueueItem(item); itemID == id {
			ours = number
		}
	}
	if ours < 0 {
		return 0, false, nil
	}
	ahead := len(out.Running)
	for _, item := range out.Pending {
		if number, itemID := comfyQueueItem(item); itemID != id && number < ours {
			ahead++
		}
	}
	return max(ahead, 1), true, nil
}

// comfyQueueItem reads the number and prompt id of one queue entry.
func comfyQueueItem(item []json.RawMessage) (float64, string) {
	if len(item) < 2 {
		return -1, ""
	}
	var number float64
	var id string
	_ = json.Unmarshal(item[0], &number)
	_ = json.Unmarshal(item[1], &id)
	return number, id
}

// comfyErrorMessage extracts the exception from the messages of a failed job.
func comfyErrorMessage(entry *comfyHistoryEntry) string {
	for _, msg := range entry.Status.Messages {
		if len(msg) < 2 {
			continue
		}
		var kind string
		if json.Unmarshal(msg[0], &kind) != nil || kind != "execution_error" {
			continue
		}
		var detail struct {
			NodeType         string `json:"node_type"`
			ExceptionMessage string `json:"exception_message"`
		}
		if json.Unmarshal(msg[1], &detail) == nil {
			return collapseSpace(detail.NodeType + ": " + detail.ExceptionMessage)
		}
	}
	return "execution error"
}

// collect downloads the output images of a finished job, in node id order,
// and reports each one to progress.
func (p *comfyProvider) collect(ctx context.Context, id string, entry *comfyHistoryEntry, progress func(done, total int)) ([]core.AIImage, error) {
	nodes := make([]string, 0, len(entry.Outputs))
	for node := range entry.Outputs {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		a, errA := strconv.Atoi(nodes[i])
		b, errB := strconv.Atoi(nodes[j])
		if errA == nil && errB == nil {
			return a < b
		}
		return nodes[i] < nodes[j]
	})
	var files []comfyFile
	for _, node := range nodes {
		for _, file := range entry.Outputs[node].Images {
			if file.Filename != "" && (file.Type == "output" || file.Type == "") {
				files = append(files, file)
			}
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("comfyui: prompt %s finished without output images", id)
	}
	if len(files) > core.MaxImageN {
		files = files[:core.MaxImageN]
	}

	budget := maxImageResponseBytes
	images := make([]core.AIImage, 0, len(files))
	for i, file := range files {
		data, err := p.view(ctx, file, &budget)
		if err != nil {
			return nil, err
		}
		mediaType, err := sniffImage("comfyui", data)
		if err != nil {
			return nil, err
		}
		images = append(images, core.AIImage{MediaType: mediaType, Data: data, Name: file.Filename})
		reportProgress(progress, i+1, len(files))
	}
	return images, nil
}

// view downloads one output file.
func (p *comfyProvider) view(ctx context.Context, file comfyFile, budget *int64) ([]byte, error) {
	query := url.Values{"filename": {file.Filename}, "subfolder": {file.Subfolder}, "type": {"output"}}
	target := endpoint(p.cfg.BaseURL, "/view") + "?" + query.Encode()
	resp, err := p.do(ctx, http.MethodGet, target, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readImage("comfyui", resp.Body, budget)
}

// abandon stops a job after its call was canceled or timed out. A job seen
// waiting is deleted from the queue. Any other job is interrupted, because
// it may be running. A failed call is logged and ignored.
func (p *comfyProvider) abandon(ctx context.Context, id string, state comfyJobState) {
	if state == comfyDone {
		return
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), comfyAbandonTimeout)
	defer cancel()

	send := func(path string, payload any) {
		resp, err := p.do(callCtx, http.MethodPost, endpoint(p.cfg.BaseURL, path), payload, "")
		if err != nil {
			p.logger.Warn("comfyui cancel call failed", "provider", p.cfg.ID, "path", path, "error", err)
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		resp.Body.Close()
	}
	send("/queue", map[string]any{"delete": []string{id}})
	if state != comfyPending {
		send("/interrupt", map[string]any{"prompt_id": id})
	}
}
