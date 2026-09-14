package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

// tinyPNG encodes a 2 by 2 PNG of one colour.
func tinyPNG(t *testing.T, c uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for x := 0; x < 2; x++ {
		for y := 0; y < 2; y++ {
			img.Set(x, y, color.NRGBA{R: c, G: 10, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// imageFactory replaces the image generator of the fake AI factory.
type imageFactory struct {
	*fakeAI
	gen *pngGen
}

func (f *imageFactory) ImageGenerator(cfg *core.AIProviderConfig, edit bool) (core.ImageGenerator, error) {
	if _, err := f.fakeAI.ImageGenerator(cfg, edit); err != nil {
		return nil, err
	}
	return f.gen, nil
}

// pngGen returns req.N small PNGs, reports a queue position and progress, and
// records the requests.
type pngGen struct {
	t    *testing.T
	mu   sync.Mutex
	reqs []core.AIImageRequest
	err  error
}

func (g *pngGen) GenerateImages(ctx context.Context, req core.AIImageRequest, progress func(done, total int)) (*core.AIImageResult, error) {
	g.mu.Lock()
	g.reqs = append(g.reqs, req)
	err := g.err
	g.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if req.Queue != nil {
		req.Queue(2)
	}
	out := &core.AIImageResult{Model: req.Model, RevisedPrompt: "revised " + req.Prompt}
	for i := 0; i < req.N; i++ {
		out.Images = append(out.Images, core.AIImage{MediaType: "image/png", Data: tinyPNG(g.t, uint8(i*40))})
		if progress != nil {
			progress(i+1, req.N)
		}
	}
	return out, nil
}

// fakeImageSaver records file writes and serves one source image.
type fakeImageSaver struct {
	mu         sync.Mutex
	source     *core.File
	sourceData []byte
	uploads    []core.FileUpload
	replaces   []core.FileReplace
	replaceIDs []string
}

type nopReadSeeker struct{ *bytes.Reader }

func (nopReadSeeker) Close() error { return nil }

func (s *fakeImageSaver) OpenFile(ctx context.Context, actor *core.Actor, id, versionID string) (*core.File, *core.FileVersion, io.ReadSeekCloser, error) {
	if s.source == nil || id != s.source.ID {
		return nil, nil, nil, core.ErrNotFound
	}
	cp := *s.source
	return &cp, nil, nopReadSeeker{bytes.NewReader(s.sourceData)}, nil
}

func (s *fakeImageSaver) UploadFile(ctx context.Context, actor *core.Actor, in core.FileUpload, r io.Reader) (*core.File, error) {
	data, _ := io.ReadAll(r)
	if len(data) == 0 {
		return nil, core.Invalid("file", "empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads = append(s.uploads, in)
	return &core.File{ID: core.NewID(), SpaceID: in.SpaceID, ParentID: in.ParentID, Name: in.Name, Size: int64(len(data))}, nil
}

func (s *fakeImageSaver) ReplaceFile(ctx context.Context, actor *core.Actor, id string, in core.FileReplace, r io.Reader) (*core.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replaces = append(s.replaces, in)
	s.replaceIDs = append(s.replaceIDs, id)
	return &core.File{ID: id, Name: in.Name}, nil
}

// imageHarness adds an image provider with generation and editing, swaps in
// the PNG generator, and installs saver as the files service.
func imageHarness(t *testing.T, saver imageSaver) (*harness, *pngGen) {
	t.Helper()
	h := newHarness(t)
	addProvider(h, core.AIProviderConfig{ID: "pics", Kind: core.ProviderOpenAI, ImageGeneration: true, ImageEditing: true, ImageModel: "img-1"})
	gen := &pngGen{t: t}
	h.app.ai = &imageFactory{fakeAI: h.ai, gen: gen}
	restore := imageSaverFor
	imageSaverFor = func(*App) imageSaver { return saver }
	t.Cleanup(func() { imageSaverFor = restore })
	return h, gen
}

func TestGenerateImagesReturnsDataURLs(t *testing.T) {
	h, gen := imageHarness(t, nil)
	setModelPrice(h, "pics", "img-1", core.AIPrice{PerImageOut: 0.04})
	u := h.user("Painter", false)
	var frames []core.AIImageProgress
	res, err := h.app.GenerateImages(h.ctx, u, core.AIImageGenerateRequest{
		Prompt: "A lighthouse at dusk", N: 2, Size: "1024x1024", Save: core.AIImageSaveNone,
	}, func(p core.AIImageProgress) { frames = append(frames, p) })
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 2 || len(res.Files) != 0 || res.Files == nil {
		t.Fatalf("result = %+v", res)
	}
	for i, img := range res.Images {
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(img.DataURL, "data:image/png;base64,"))
		if !strings.HasPrefix(img.DataURL, "data:image/png;base64,") || err != nil || !bytes.Equal(data, tinyPNG(t, uint8(i*40))) {
			t.Fatalf("image %d = %.40s, %v", i, img.DataURL, err)
		}
	}
	if res.Provider != "pics" || res.Model != "img-1" || res.Route != core.RouteImageGenerate || res.Attempts != 1 ||
		res.Usage.ImagesOut != 2 || res.RevisedPrompt != "revised A lighthouse at dusk" {
		t.Fatalf("result meta = %+v", res)
	}
	if len(gen.reqs) != 1 || gen.reqs[0].N != 2 || gen.reqs[0].Size != "1024x1024" || gen.reqs[0].Source != nil {
		t.Fatalf("generator requests = %+v", gen.reqs)
	}
	sawQueue := false
	for _, f := range frames {
		if f.QueuePosition != nil && *f.QueuePosition == 2 {
			sawQueue = true
		}
	}
	if !sawQueue || frames[len(frames)-1] != (core.AIImageProgress{Done: 2, Total: 2}) {
		t.Fatalf("progress frames = %+v", frames)
	}
	row := lastUsageOf(t, h, u.UserID())
	if row.Feature != core.FeatureImage || row.Task != core.RouteImageGenerate || row.ImagesOut != 2 || row.ImagesIn != 0 ||
		row.Attempts != 1 || row.Status != core.UsageOK || row.ProviderID != "pics" || row.CostMicros != 80000 {
		t.Fatalf("usage row = %+v", row)
	}
}

func TestGenerateImagesRefusesWithoutCapableTarget(t *testing.T) {
	h := newHarness(t)
	u := h.user("Painter", false)
	req := core.AIImageGenerateRequest{Prompt: "A harbour", N: 1}
	if _, err := h.app.GenerateImages(h.ctx, u, req, nil); !errors.Is(err, core.ErrUnavailable) || !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("generation without an image provider = %v", err)
	}
	if rows := usageOf(h, u.UserID()); len(rows) != 0 {
		t.Fatalf("a refused call recorded usage: %+v", rows)
	}

	// A generation-only provider cannot serve an edit.
	addProvider(h, core.AIProviderConfig{ID: "gen", Kind: core.ProviderComfyUI, ImageGeneration: true, ImageModel: "sdxl"})
	saver := &fakeImageSaver{source: &core.File{ID: "f1", Name: "photo.png"}, sourceData: tinyPNG(t, 1)}
	restore := imageSaverFor
	imageSaverFor = func(*App) imageSaver { return saver }
	t.Cleanup(func() { imageSaverFor = restore })
	if _, err := h.app.GenerateImages(h.ctx, u, core.AIImageGenerateRequest{Prompt: "brighter", FileID: "f1"}, nil); !errors.Is(err, core.ErrUnavailable) {
		t.Fatalf("edit without an editing provider = %v", err)
	}
	if _, err := h.app.GenerateImages(h.ctx, u, req, nil); err != nil {
		t.Fatalf("generation with a generation provider = %v", err)
	}
}

func TestGenerateImagesSavesThroughFilesService(t *testing.T) {
	source := &core.File{ID: "f1", SpaceID: "s-src", ParentID: "folder-src", Name: "photo.png", SHA256: "abc"}
	saver := &fakeImageSaver{source: source, sourceData: tinyPNG(t, 9)}
	h, gen := imageHarness(t, saver)
	owner := h.user("Owner", false)
	sp := h.space(owner, "Harbour")
	note := indexedNote(h, sp, "Survey", "body")

	res, err := h.app.GenerateImages(h.ctx, owner, core.AIImageGenerateRequest{
		NoteID: note.ID, Prompt: "A lighthouse at dusk with gulls over the quay", N: 2, Save: core.AIImageSaveNew,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 || len(res.Images) != 0 || len(saver.uploads) != 2 {
		t.Fatalf("saved = %+v, uploads %+v", res, saver.uploads)
	}
	for i, want := range []string{"A lighthouse at dusk with gulls 1.png", "A lighthouse at dusk with gulls 2.png"} {
		up := saver.uploads[i]
		if up.Name != want || up.SpaceID != sp.ID || up.ParentID != note.ParentID || up.PasteFor != note.ID || up.OnConflict != core.OnConflictRename {
			t.Fatalf("upload %d = %+v", i, up)
		}
	}

	mask := "data:image/png;base64," + base64.StdEncoding.EncodeToString(tinyPNG(t, 0))
	res, err = h.app.GenerateImages(h.ctx, owner, core.AIImageGenerateRequest{
		NoteID: note.ID, Prompt: "remove the crane", N: 3, FileID: "f1", Mask: mask, Save: core.AIImageSaveVersion,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	last := gen.reqs[len(gen.reqs)-1]
	if last.N != 1 || last.Source == nil || !bytes.Equal(last.Source.Data, saver.sourceData) || last.Mask == nil || last.Mask.MediaType != "image/png" {
		t.Fatalf("edit request = %+v", last)
	}
	if len(saver.replaceIDs) != 1 || saver.replaceIDs[0] != "f1" || saver.replaces[0] != (core.FileReplace{Name: "photo.png", BaseSHA256: "abc"}) || len(res.Files) != 1 {
		t.Fatalf("version = %+v %+v", saver.replaceIDs, saver.replaces)
	}
	row := lastUsageOf(t, h, owner.UserID())
	if row.Task != core.RouteImageEdit || row.ImagesIn != 1 || row.ImagesOut != 1 {
		t.Fatalf("edit usage = %+v", row)
	}

	res, err = h.app.GenerateImages(h.ctx, owner, core.AIImageGenerateRequest{
		Prompt: "remove the crane", FileID: "f1", Save: core.AIImageSaveCopy,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	up := saver.uploads[len(saver.uploads)-1]
	if up.Name != "photo edited.png" || up.SpaceID != "s-src" || up.ParentID != "folder-src" || len(res.Files) != 1 {
		t.Fatalf("copy upload = %+v", up)
	}

	imageSaverFor = func(*App) imageSaver { return nil }
	if _, err := h.app.GenerateImages(h.ctx, owner, core.AIImageGenerateRequest{NoteID: note.ID, Prompt: "x", Save: core.AIImageSaveNew}, nil); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("save without a files service = %v", err)
	}
	if _, err := h.app.GenerateImages(h.ctx, owner, core.AIImageGenerateRequest{NoteID: note.ID, Prompt: "x"}, nil); err != nil {
		t.Fatalf("save none without a files service = %v", err)
	}
}

func TestGenerateImagesValidatesRequests(t *testing.T) {
	saver := &fakeImageSaver{source: &core.File{ID: "f1", Name: "notes.txt"}, sourceData: []byte("not an image")}
	h, gen := imageHarness(t, saver)
	u := h.user("Painter", false)
	cases := map[string]core.AIImageGenerateRequest{
		"empty prompt":         {Prompt: "  "},
		"long prompt":          {Prompt: strings.Repeat("a", maxImagePromptRunes+1)},
		"unknown save":         {Prompt: "x", Save: "somewhere"},
		"version without file": {Prompt: "x", Save: core.AIImageSaveVersion},
		"mask without file":    {Prompt: "x", Mask: base64.StdEncoding.EncodeToString(tinyPNG(t, 0))},
		"mask not a png":       {Prompt: "x", FileID: "f1", Mask: base64.StdEncoding.EncodeToString([]byte("GIF89a"))},
		"source not an image":  {Prompt: "x", FileID: "f1"},
	}
	for name, req := range cases {
		if _, err := h.app.GenerateImages(h.ctx, u, req, nil); !errors.Is(err, core.ErrInvalid) {
			t.Fatalf("%s = %v", name, err)
		}
	}
	if len(gen.reqs) != 0 {
		t.Fatalf("an invalid request reached the generator: %+v", gen.reqs)
	}
	if _, err := h.app.GenerateImages(h.ctx, &core.Actor{}, core.AIImageGenerateRequest{Prompt: "x"}, nil); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("anonymous = %v", err)
	}
}

func TestImageFileNames(t *testing.T) {
	cases := []struct {
		name, prompt string
		i, n         int
		mt, want     string
	}{
		{"", "A lighthouse at dusk with gulls over the quay", 0, 1, "image/png", "A lighthouse at dusk with gulls.png"},
		{"", "a/b\\c: d?", 1, 2, "image/jpeg", "a b c d 2.jpg"},
		{"", "   ", 0, 1, "image/webp", "image.webp"},
		{"Harbour plan.png", "ignored", 0, 1, "image/png", "Harbour plan.png"},
	}
	for _, c := range cases {
		if got := imageFileName(c.name, c.prompt, c.i, c.n, c.mt); got != c.want {
			t.Fatalf("imageFileName(%q, %q) = %q, want %q", c.name, c.prompt, got, c.want)
		}
	}
	if got := editedFileName("shot 1.jpeg", "image/png"); got != "shot 1 edited.png" {
		t.Fatalf("editedFileName = %q", got)
	}
	if got := versionFileName("shot.jpeg", "image/jpeg"); got != "shot.jpeg" {
		t.Fatalf("versionFileName same type = %q", got)
	}
	if got := versionFileName("shot.jpeg", "image/png"); got != "shot.png" {
		t.Fatalf("versionFileName new type = %q", got)
	}
}

func TestGenerateImagesChargesPluginRoute(t *testing.T) {
	h, gen := imageHarness(t, nil)
	admin := h.user("Admin", true)
	u := h.user("Painter", false)

	// A plugin route without targets of its own falls back to image.generate.
	res, err := h.app.GenerateImages(h.ctx, u, core.AIImageGenerateRequest{Prompt: "A lighthouse", Route: "plugin.wiki.illustrate", PluginID: "wiki"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Route != "plugin.wiki.illustrate" || res.Provider != "pics" || len(gen.reqs) != 1 {
		t.Fatalf("result = %+v", res)
	}
	row := lastUsageOf(t, h, u.UserID())
	if row.PluginID != "wiki" || row.Task != "plugin.wiki.illustrate" || row.Feature != core.FeatureImage || row.Status != core.UsageOK {
		t.Fatalf("usage row = %+v", row)
	}

	for _, route := range []string{core.RouteEdit, "plugin.", "image.edit"} {
		if _, err := h.app.GenerateImages(h.ctx, u, core.AIImageGenerateRequest{Prompt: "x", Route: route}, nil); !errors.Is(err, core.ErrInvalid) {
			t.Fatalf("route %q = %v", route, err)
		}
	}

	tokens := int64(1)
	if _, err := h.app.SaveAIBudget(h.ctx, admin, core.AIBudget{SubjectKind: core.BudgetPlugin, SubjectID: "wiki", MonthlyTokens: &tokens}); err != nil {
		t.Fatal(err)
	}
	usageTokens(h, 5, 5)
	if _, err := h.app.RunAI(h.ctx, u, core.AIRequest{Task: core.TaskSummarize, Text: "t", PluginID: "wiki"}); err != nil {
		t.Fatalf("plugin text call under the budget: %v", err)
	}
	_, err = h.app.GenerateImages(h.ctx, u, core.AIImageGenerateRequest{Prompt: "again", Route: "plugin.wiki.illustrate", PluginID: "wiki"}, nil)
	if !errors.Is(err, core.ErrQuotaExceeded) {
		t.Fatalf("plugin past its budget = %v", err)
	}
	if got := lastUsageOf(t, h, u.UserID()); got.Status != core.UsageBudget || got.PluginID != "wiki" || got.Feature != core.FeatureImage {
		t.Fatalf("budget row = %+v", got)
	}
	if _, err := h.app.GenerateImages(h.ctx, u, core.AIImageGenerateRequest{Prompt: "mine"}, nil); err != nil {
		t.Fatalf("the user's own image call was stopped by the plugin budget: %v", err)
	}
}
