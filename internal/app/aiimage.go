package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gamemaker1/markspace/internal/ai"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/extract"
)

// Limits of image requests.
const (
	// maxImagePromptRunes caps the prompt and the negative prompt.
	maxImagePromptRunes = 4000
	// maxImageMaskBytes caps a decoded mask.
	maxImageMaskBytes = 32 << 20
	// imageNameWords is how many words of the prompt name a saved image.
	imageNameWords = 6
	// maxImageNameRunes caps the stem of a saved image name.
	maxImageNameRunes = 80
)

// imageSaver is the part of the files service that image requests use. The
// methods are the file calls of core.ItemService: OpenFile reads the source
// of an edit, UploadFile saves a new image or a copy, and ReplaceFile saves
// an edit as a new version. The files service does every access check.
type imageSaver interface {
	OpenFile(ctx context.Context, actor *core.Actor, id, versionID string) (*core.File, *core.FileVersion, io.ReadSeekCloser, error)
	UploadFile(ctx context.Context, actor *core.Actor, in core.FileUpload, r io.Reader) (*core.File, error)
	ReplaceFile(ctx context.Context, actor *core.Actor, id string, in core.FileReplace, r io.Reader) (*core.File, error)
}

// imageSaverFor returns the files service of an app, or nil while the app
// lacks the methods. Tests replace it.
var imageSaverFor = func(a *App) imageSaver {
	if s, ok := any(a).(imageSaver); ok {
		return s
	}
	return nil
}

var _ core.AIImageService = (*App)(nil)

// errNoImageSaver reports an image request that needs the files service.
var errNoImageSaver = fmt.Errorf("saving images needs the files service: %w", core.ErrUnsupported)

// imageExt returns the file extension of an image media type.
func imageExt(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return "png"
}

// cleanImageStem keeps the letters, digits, spaces and simple punctuation of
// a name, collapses spaces, and cuts it to maxImageNameRunes.
func cleanImageStem(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_', r == '\'', r == ',', r == '(', r == ')':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	stem := strings.Join(strings.Fields(b.String()), " ")
	return strings.TrimSpace(truncateRunes(stem, maxImageNameRunes))
}

// imageFileName names image i of n: the stem of name when given, else the
// first 6 words of the prompt, with a counter when n is above 1.
func imageFileName(name, prompt string, i, n int, mediaType string) string {
	stem := ""
	if name = strings.TrimSpace(name); name != "" {
		stem = cleanImageStem(strings.TrimSuffix(name, path.Ext(name)))
	}
	if stem == "" {
		words := strings.Fields(prompt)
		if len(words) > imageNameWords {
			words = words[:imageNameWords]
		}
		stem = cleanImageStem(strings.Join(words, " "))
	}
	if stem == "" {
		stem = "image"
	}
	if n > 1 {
		stem = fmt.Sprintf("%s %d", stem, i+1)
	}
	return stem + "." + imageExt(mediaType)
}

// editedFileName names the copy of an edited image: "<stem> edited.<ext>".
func editedFileName(source, mediaType string) string {
	stem := strings.TrimSuffix(source, path.Ext(source))
	if strings.TrimSpace(stem) == "" {
		stem = "image"
	}
	return stem + " edited." + imageExt(mediaType)
}

// versionFileName keeps the source name, with the extension of the new
// media type when it differs.
func versionFileName(source, mediaType string) string {
	ext := imageExt(mediaType)
	cur := strings.ToLower(strings.TrimPrefix(path.Ext(source), "."))
	if cur == ext || (ext == "jpg" && cur == "jpeg") {
		return source
	}
	return strings.TrimSuffix(source, path.Ext(source)) + "." + ext
}

// decodeMask reads a base64 PNG mask, with or without a data URL prefix.
func decodeMask(s string) (*core.AIImage, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "data:") {
		comma := strings.IndexByte(s, ',')
		if comma < 0 {
			return nil, core.Invalid("mask", "the mask must be a base64 PNG")
		}
		s = s[comma+1:]
	}
	if base64.StdEncoding.DecodedLen(len(s)) > maxImageMaskBytes+3 {
		return nil, fmt.Errorf("the mask is larger than 32 MiB: %w", core.ErrTooLarge)
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
	}
	if err != nil || extract.ImageType(data) != "image/png" {
		return nil, core.Invalid("mask", "the mask must be a base64 PNG")
	}
	return &core.AIImage{MediaType: "image/png", Data: data, Name: "mask.png"}, nil
}

// imageMediaType returns the media type of a generated image, from its bytes
// when they are recognized.
func imageMediaType(img core.AIImage) string {
	if t := extract.ImageType(img.Data); t != "" {
		return t
	}
	if img.MediaType != "" {
		return img.MediaType
	}
	return "image/png"
}

// imageTargetsFor resolves the image route of a call and returns the
// generators in route order with the routing task used. route is empty for
// image.generate, or image.edit when edit is set, or a plugin route such as
// plugin.<id>.<purpose>. A plugin route falls back to the image route after
// its own targets, and never to the text default route. The monthly budgets
// of the actor and of pluginID apply, and a used-up budget returns
// core.ErrQuotaExceeded. A target whose provider cannot build a generator is
// left out.
func (a *App) imageTargetsFor(ctx context.Context, actor *core.Actor, edit bool, route, pluginID string, requested *core.AIRouteTarget) ([]imageTarget, string, error) {
	if err := requireUser(actor); err != nil {
		return nil, "", err
	}
	settings, err := a.aiReady(ctx)
	if err != nil {
		return nil, "", err
	}
	base := core.RouteImageGenerate
	if edit {
		base = core.RouteImageEdit
	}
	task := strings.TrimSpace(route)
	if task == "" {
		task = base
	}
	if task != base && (!core.ValidRouteTask(task) || !strings.HasPrefix(task, "plugin.")) {
		return nil, "", core.Invalid("route", "an image request takes "+base+" or a plugin route")
	}
	v, err := a.loadRouteView(ctx)
	if err != nil {
		return nil, "", err
	}
	rreq, err := a.routeRequest(ctx, v, settings, actor.UserID(), task, core.RouteNeeds(base))
	if err != nil {
		return nil, "", err
	}
	if task != base {
		instance := make(map[string][]core.AIRouteTarget, len(rreq.InstanceRoutes)+1)
		for k, targets := range rreq.InstanceRoutes {
			instance[k] = targets
		}
		instance[task] = append(append([]core.AIRouteTarget(nil), instance[task]...), v.instance[base]...)
		rreq.InstanceRoutes = instance
		if rreq.UserRoutes != nil {
			user := make(map[string][]core.AIRouteTarget, len(rreq.UserRoutes)+1)
			for k, targets := range rreq.UserRoutes {
				user[k] = targets
			}
			user[task] = append(append([]core.AIRouteTarget(nil), user[task]...), user[base]...)
			rreq.UserRoutes = user
		}
	}
	rreq.Requested = requested
	_, stop, err := a.checkBudgets(ctx, settings, actor.UserID(), pluginID)
	if err != nil {
		a.log.Warn("ai budget check failed", "userId", actor.UserID(), "pluginId", pluginID, "err", err)
		stop = nil
	}
	rreq.BudgetExhausted = stop != nil
	plan := ai.ResolveRoute(rreq)
	if plan.QuotaExceeded {
		rec := core.AIUsageRecord{
			UserID: actor.UserID(), Feature: core.FeatureImage, Task: task, PluginID: pluginID,
			Status: core.UsageBudget, CreatedAt: a.now(),
		}
		if len(plan.Candidates) > 0 {
			rec.ProviderID, rec.Model = plan.Candidates[0].Target.ProviderID, plan.Candidates[0].Target.Model
		}
		a.recordUsage(ctx, rec)
		return nil, "", quotaError(stop)
	}
	var out []imageTarget
	for _, c := range plan.Candidates {
		cfg := v.configs[c.Target.ProviderID]
		gen, err := a.ai.ImageGenerator(cfg, edit)
		if err != nil {
			a.log.Info("image target skipped", "providerId", cfg.ID, "err", err)
			continue
		}
		price, _ := c.Model.Effective()
		out = append(out, imageTarget{gen: gen, cfg: cfg, model: c.Target.Model, price: price})
	}
	if len(out) == 0 {
		return nil, "", errNoRoute(task)
	}
	return out, task, nil
}

// GenerateImages creates images from a prompt, or edits the image file
// req.FileID, through the image.generate or image.edit route. Each target
// that fails with a retryable error hands the call to the next one. Every
// attempt records a usage row with feature image. The results come back as
// data URLs for save mode none, or are saved through the files service: as
// new files in the note's folder, as a new version of the source, or as a
// copy named "<stem> edited.<ext>".
func (a *App) GenerateImages(ctx context.Context, actor *core.Actor, req core.AIImageGenerateRequest, progress func(core.AIImageProgress)) (*core.AIImageGenerateResult, error) {
	if err := requireUser(actor); err != nil {
		return nil, err
	}
	prompt := strings.TrimSpace(req.Prompt)
	negative := strings.TrimSpace(req.NegativePrompt)
	switch {
	case prompt == "":
		return nil, core.Invalid("prompt", "a prompt is required")
	case utf8.RuneCountInString(prompt) > maxImagePromptRunes:
		return nil, core.Invalid("prompt", fmt.Sprintf("a prompt is at most %d characters", maxImagePromptRunes))
	case utf8.RuneCountInString(negative) > maxImagePromptRunes:
		return nil, core.Invalid("negativePrompt", fmt.Sprintf("a negative prompt is at most %d characters", maxImagePromptRunes))
	case len(req.Size) > 16:
		return nil, core.Invalid("size", "size must be WIDTHxHEIGHT")
	}
	save := req.Save
	if save == "" {
		save = core.AIImageSaveNone
	}
	edit := strings.TrimSpace(req.FileID) != ""
	switch save {
	case core.AIImageSaveNone, core.AIImageSaveNew:
	case core.AIImageSaveVersion, core.AIImageSaveCopy:
		if !edit {
			return nil, core.Invalid("save", "saving a version or a copy needs fileId")
		}
	default:
		return nil, core.Invalid("save", "save must be none, new, version or copy")
	}
	n := core.ClampImageN(req.N)
	if save == core.AIImageSaveVersion {
		n = 1
	}
	var mask *core.AIImage
	if req.Mask != "" {
		if !edit {
			return nil, core.Invalid("mask", "a mask needs fileId")
		}
		m, err := decodeMask(req.Mask)
		if err != nil {
			return nil, err
		}
		mask = m
	}

	spaceID, parentID := strings.TrimSpace(req.SpaceID), strings.TrimSpace(req.ParentID)
	if req.NoteID != "" {
		note, _, err := a.requireNoteAny(ctx, actor, req.NoteID, core.AccessView)
		if err != nil {
			return nil, err
		}
		spaceID = note.SpaceID
		if parentID == "" {
			parentID = note.ParentID
		}
	}
	var saver imageSaver
	if save != core.AIImageSaveNone || edit {
		if saver = imageSaverFor(a); saver == nil {
			return nil, errNoImageSaver
		}
	}

	var requested *core.AIRouteTarget
	if p := strings.TrimSpace(req.ProviderID); p != "" {
		requested = &core.AIRouteTarget{ProviderID: p, Model: strings.TrimSpace(req.Model)}
	}
	pluginID := cutLabel(strings.TrimSpace(req.PluginID), maxPluginIDBytes)
	targets, route, err := a.imageTargetsFor(ctx, actor, edit, req.Route, pluginID, requested)
	if err != nil {
		return nil, err
	}

	var (
		source     *core.AIImage
		sourceFile *core.File
	)
	if edit {
		f, _, rc, err := saver.OpenFile(ctx, actor, req.FileID, "")
		if err != nil {
			return nil, err
		}
		data, over, err := readCapped(rc, maxFileBytes)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read image %s: %w", f.ID, err)
		}
		if over {
			return nil, fmt.Errorf("the image is larger than 32 MiB: %w", core.ErrTooLarge)
		}
		switch t := extract.ImageType(data); t {
		case "image/png", "image/jpeg", "image/webp":
			source = &core.AIImage{MediaType: t, Data: data, Name: f.Name}
		default:
			return nil, core.Invalid("fileId", "only PNG, JPEG and WebP images can be edited")
		}
		sourceFile = f
		if spaceID == "" {
			spaceID = f.SpaceID
		}
		if parentID == "" && req.NoteID == "" {
			parentID = f.ParentID
		}
	}
	if save == core.AIImageSaveNew && spaceID == "" {
		return nil, core.Invalid("spaceId", "a space is required to save images")
	}

	base := core.AIUsageRecord{
		UserID: actor.UserID(), Feature: core.FeatureImage, Task: route, PluginID: pluginID, SpaceID: spaceID, NoteID: req.NoteID,
	}
	if source != nil {
		base.ImagesIn = 1
	}
	var (
		res     *core.AIImageResult
		began   time.Time
		started time.Time
	)
	last, tried, err := ai.Fallback(ctx, len(targets), func(i int) (bool, error) {
		t := targets[i]
		began, started = time.Now(), a.now()
		call := core.AIImageRequest{
			Prompt: prompt, NegativePrompt: negative, Size: req.Size, N: n, Model: t.model, Source: source, Mask: mask,
		}
		if progress != nil {
			call.Queue = func(position int) {
				p := position
				progress(core.AIImageProgress{Done: 0, Total: n, QueuePosition: &p})
			}
			progress(core.AIImageProgress{Done: 0, Total: n})
		}
		out, err := t.gen.GenerateImages(ctx, call, func(done, total int) {
			if progress != nil {
				progress(core.AIImageProgress{Done: done, Total: total})
			}
		})
		if err == nil && (out == nil || len(out.Images) == 0) {
			err = fmt.Errorf("the image model returned no images: %w", core.ErrUnavailable)
		}
		if err != nil {
			a.noteCallError(ctx, t.cfg.ID, err)
			rec := base
			rec.ProviderID, rec.Model, rec.Attempts, rec.CreatedAt = t.cfg.ID, t.model, i+1, started
			rec.Status, rec.Error = usageStatus(ctx, err), err.Error()
			fillUsage(&rec, t.price, 0, 0, 0, "", began)
			a.recordUsage(ctx, rec)
			return false, err
		}
		res = out
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("ai images: %w", err)
	}
	t := targets[last]
	model := res.Model
	if model == "" {
		model = t.model
	}
	usage := res.Usage
	usage.ImagesOut = len(res.Images)
	if source != nil && usage.ImagesIn == 0 {
		usage.ImagesIn = 1
	}
	rec := base
	rec.ProviderID, rec.Model, rec.Attempts, rec.CreatedAt, rec.Status = t.cfg.ID, model, tried, started, core.UsageOK
	rec.ImagesIn, rec.ImagesOut = int64(usage.ImagesIn), int64(usage.ImagesOut)
	fillUsage(&rec, t.price, 0, usage.PromptTokens, usage.CompletionTokens, "", began)
	a.recordUsage(ctx, rec)
	a.log.Info("ai images", "route", route, "provider", t.cfg.ID, "model", model, "attempts", tried,
		"images", len(res.Images), "save", save, "userId", actor.UserID())

	out := &core.AIImageGenerateResult{
		Files: []*core.File{}, Usage: usage, Provider: t.cfg.ID, Model: model, Route: route, Attempts: tried,
		RevisedPrompt: res.RevisedPrompt,
	}
	switch save {
	case core.AIImageSaveNone:
		for _, img := range res.Images {
			out.Images = append(out.Images, core.AIImageOutput{
				DataURL: "data:" + imageMediaType(img) + ";base64," + base64.StdEncoding.EncodeToString(img.Data),
			})
		}
	case core.AIImageSaveNew:
		for i, img := range res.Images {
			mt := imageMediaType(img)
			f, err := saver.UploadFile(ctx, actor, core.FileUpload{
				SpaceID: spaceID, ParentID: parentID, Name: imageFileName(req.Name, prompt, i, len(res.Images), mt),
				OnConflict: core.OnConflictRename, PasteFor: req.NoteID,
			}, bytes.NewReader(img.Data))
			if err != nil {
				return nil, fmt.Errorf("save image %d: %w", i+1, err)
			}
			out.Files = append(out.Files, f)
		}
	case core.AIImageSaveVersion:
		img := res.Images[0]
		f, err := saver.ReplaceFile(ctx, actor, sourceFile.ID, core.FileReplace{
			Name: versionFileName(sourceFile.Name, imageMediaType(img)), BaseSHA256: sourceFile.SHA256,
		}, bytes.NewReader(img.Data))
		if err != nil {
			return nil, fmt.Errorf("save image version: %w", err)
		}
		out.Files = append(out.Files, f)
	case core.AIImageSaveCopy:
		for _, img := range res.Images {
			name := editedFileName(sourceFile.Name, imageMediaType(img))
			f, err := saver.UploadFile(ctx, actor, core.FileUpload{
				SpaceID: sourceFile.SpaceID, ParentID: sourceFile.ParentID, Name: name,
				OnConflict: core.OnConflictRename, PasteFor: req.NoteID,
			}, bytes.NewReader(img.Data))
			if err != nil {
				return nil, fmt.Errorf("save image copy: %w", err)
			}
			out.Files = append(out.Files, f)
		}
	}
	return out, nil
}
