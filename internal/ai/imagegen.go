package ai

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// Image call limits. They are variables so tests can lower them.
var (
	// maxImageBytes caps one generated or uploaded image.
	maxImageBytes int64 = 32 << 20
	// maxImageResponseBytes caps everything one image call reads back.
	maxImageResponseBytes int64 = 140 << 20
)

const (
	// defaultImageTimeoutSeconds applies to image calls when a config sets
	// no timeout.
	defaultImageTimeoutSeconds = 300
	// defaultImageSize is the size of a generation that names none on a
	// provider without a size list.
	defaultImageSize = "1024x1024"
	// maxImagePromptBytes caps the prompt and the negative prompt.
	maxImagePromptBytes = 32000
)

// imageMediaTypes lists the image types an image call accepts and returns.
var imageMediaTypes = []string{"image/png", "image/jpeg", "image/webp"}

// imageTimeout returns the timeout of one image call.
func (b *baseProvider) imageTimeout() time.Duration {
	seconds := b.cfg.TimeoutSeconds
	if seconds <= 0 {
		seconds = defaultImageTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

// imageJob is a validated image request.
type imageJob struct {
	req  core.AIImageRequest
	edit bool
	n    int
	// size is empty for an edit that names no size.
	size string
	// width and height are 0 when size is empty or "auto".
	width, height int
}

// checkImageCapability reports core.ErrUnsupported when the provider does
// not have the capability flag for the call.
func checkImageCapability(cfg *core.AIProviderConfig, edit bool) error {
	if edit && !cfg.ImageEditing {
		return fmt.Errorf("ai provider %s: image editing is not enabled: %w", cfg.ID, core.ErrUnsupported)
	}
	if !edit && !cfg.ImageGeneration {
		return fmt.Errorf("ai provider %s: image generation is not enabled: %w", cfg.ID, core.ErrUnsupported)
	}
	return nil
}

// prepareImageJob validates a request against the provider configuration.
// It clamps N to 1 through 4 and resolves the size. A generation without a
// size takes the first configured size, else 1024x1024.
func prepareImageJob(cfg *core.AIProviderConfig, req core.AIImageRequest) (*imageJob, error) {
	job := &imageJob{req: req, edit: req.Source != nil, n: core.ClampImageN(req.N)}
	if err := checkImageCapability(cfg, job.edit); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, core.Invalid("prompt", "is required")
	}
	if len(req.Prompt) > maxImagePromptBytes || len(req.NegativePrompt) > maxImagePromptBytes {
		return nil, core.Invalid("prompt", "is longer than 32000 bytes")
	}
	if req.Seed < 0 {
		return nil, core.Invalid("seed", "must be 0 or more")
	}
	if req.Mask != nil && req.Source == nil {
		return nil, core.Invalid("mask", "needs a source image")
	}
	if req.Source != nil {
		if err := checkInputImage("source", req.Source, imageMediaTypes); err != nil {
			return nil, err
		}
	}
	if req.Mask != nil {
		if err := checkInputImage("mask", req.Mask, []string{"image/png"}); err != nil {
			return nil, err
		}
	}

	job.size = strings.TrimSpace(req.Size)
	if job.size == "" && !job.edit {
		job.size = defaultImageSize
		if len(cfg.ImageSizes) > 0 {
			job.size = cfg.ImageSizes[0]
		}
	}
	if job.size == "" {
		return job, nil
	}
	if len(cfg.ImageSizes) > 0 {
		if !slices.Contains(cfg.ImageSizes, job.size) {
			return nil, core.Invalid("size", "must be one of "+strings.Join(cfg.ImageSizes, ", "))
		}
	} else if _, _, ok := core.ParseImageSize(job.size); !ok {
		return nil, core.Invalid("size", "must be WIDTHxHEIGHT with 3 or 4 digit sides up to 4096")
	}
	job.width, job.height, _ = core.ParseImageSize(job.size)
	return job, nil
}

// checkInputImage checks the size and sniffed type of an uploaded image.
func checkInputImage(field string, img *core.AIImage, allowed []string) error {
	if len(img.Data) == 0 {
		return core.Invalid(field, "is empty")
	}
	if int64(len(img.Data)) > maxImageBytes {
		return core.Invalid(field, fmt.Sprintf("is larger than %d bytes", maxImageBytes))
	}
	mediaType := http.DetectContentType(img.Data)
	if !slices.Contains(allowed, mediaType) {
		return core.Invalid(field, "must be "+strings.Join(allowed, ", ")+", not "+mediaType)
	}
	return nil
}

// sniffImage returns the media type of image bytes a server sent back. Only
// PNG, JPEG and WebP pass.
func sniffImage(label string, data []byte) (string, error) {
	mediaType := http.DetectContentType(data)
	if !slices.Contains(imageMediaTypes, mediaType) {
		return "", fmt.Errorf("%s: returned image has type %s, want PNG, JPEG or WebP: %w", label, mediaType, core.ErrUnsupported)
	}
	return mediaType, nil
}

// readImage reads one image body of at most maxImageBytes and charges it to
// budget, the bytes left for the whole call.
func readImage(label string, r io.Reader, budget *int64) ([]byte, error) {
	limit := min(maxImageBytes, *budget)
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: read image: %w", label, err)
	}
	if int64(len(data)) > limit {
		return nil, imageTooLarge(label, limit == maxImageBytes)
	}
	*budget -= int64(len(data))
	return data, nil
}

// imageTooLarge builds the error for an image over the per image cap, or for
// a call over the response cap.
func imageTooLarge(label string, perImage bool) error {
	if perImage {
		return fmt.Errorf("%s: image is larger than %d bytes: %w", label, maxImageBytes, core.ErrTooLarge)
	}
	return fmt.Errorf("%s: images are larger than %d bytes in total: %w", label, maxImageResponseBytes, core.ErrTooLarge)
}

// reportProgress calls progress when it is set.
func reportProgress(progress func(done, total int), done, total int) {
	if progress != nil {
		progress(done, total)
	}
}

// randomSeed returns a random seed from 1 to 2^53-1, which every JSON reader
// holds exactly.
func randomSeed() int64 {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	seed := int64(binary.BigEndian.Uint64(buf[:]) & (1<<53 - 1))
	if seed == 0 {
		seed = 1
	}
	return seed
}

// randomHex returns n random bytes as hex.
func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// imageExtension returns the file extension of an image media type.
func imageExtension(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}
