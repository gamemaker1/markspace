package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// compile time check that the OpenAI provider generates images.
var _ core.ImageGenerator = (*openaiProvider)(nil)

// openaiImageRequest is the body of an image generation call.
type openaiImageRequest struct {
	Model          string `json:"model,omitempty"`
	Prompt         string `json:"prompt"`
	Size           string `json:"size,omitempty"`
	N              int    `json:"n"`
	ResponseFormat string `json:"response_format,omitempty"`
}

// openaiImageResponse is the result of an image generation or edit call.
type openaiImageResponse struct {
	Data []struct {
		B64JSON       string `json:"b64_json"`
		URL           string `json:"url"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	Usage *struct {
		InputTokens      int `json:"input_tokens"`
		OutputTokens     int `json:"output_tokens"`
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *openaiErrorBody `json:"error"`
}

// GenerateImages calls /v1/images/generations, or /v1/images/edits when the
// request carries a source image. It asks for base64 images and also reads
// URL answers from the provider host. The negative prompt and the seed have
// no field in this protocol and are not sent.
func (p *openaiProvider) GenerateImages(ctx context.Context, req core.AIImageRequest, progress func(done, total int)) (*core.AIImageResult, error) {
	job, err := prepareImageJob(p.cfg, req)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, p.imageTimeout())
	defer cancel()

	model := req.Model
	if model == "" {
		model = p.cfg.ImageModel
	}
	reportProgress(progress, 0, job.n)

	withFormat := !p.noResponseFormat.Load()
	resp, target, err := p.sendImageCall(callCtx, job, model, withFormat)
	if err != nil && withFormat && rejectsField(err, "response_format") {
		p.noResponseFormat.Store(true)
		p.logger.Warn("openai server rejected response_format, retrying without it", "provider", p.cfg.ID, "error", err)
		resp, target, err = p.sendImageCall(callCtx, job, model, false)
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	budget := maxImageResponseBytes
	raw, err := io.ReadAll(io.LimitReader(resp.Body, budget+1))
	if err != nil {
		return nil, fmt.Errorf("openai: read %s: %w", target, err)
	}
	if int64(len(raw)) > budget {
		return nil, imageTooLarge("openai", false)
	}
	var out openaiImageResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("openai: decode %s: %w", target, err)
	}
	budget -= int64(len(raw))
	raw = nil
	if out.Error != nil && out.Error.Message != "" {
		return nil, fmt.Errorf("openai: %s", p.redact(collapseSpace(out.Error.Message)))
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("openai: %s returned no images", target)
	}
	if len(out.Data) > job.n {
		out.Data = out.Data[:job.n]
	}

	result := &core.AIImageResult{Model: model}
	for _, item := range out.Data {
		var data []byte
		switch {
		case item.B64JSON != "":
			if int64(base64.StdEncoding.DecodedLen(len(item.B64JSON))) > maxImageBytes+2 {
				return nil, imageTooLarge("openai", true)
			}
			data, err = base64.StdEncoding.DecodeString(item.B64JSON)
			if err != nil {
				return nil, fmt.Errorf("openai: decode image from %s: %w", target, err)
			}
			if int64(len(data)) > maxImageBytes {
				return nil, imageTooLarge("openai", true)
			}
		case item.URL != "":
			data, err = p.fetchImageURL(callCtx, item.URL, &budget)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("openai: %s returned an image without data or url", target)
		}
		mediaType, err := sniffImage("openai", data)
		if err != nil {
			return nil, err
		}
		result.Images = append(result.Images, core.AIImage{MediaType: mediaType, Data: data})
		if result.RevisedPrompt == "" {
			result.RevisedPrompt = item.RevisedPrompt
		}
	}
	result.Usage.ImagesOut = len(result.Images)
	if job.edit {
		result.Usage.ImagesIn = 1
	}
	if out.Usage != nil {
		result.Usage.PromptTokens = max(out.Usage.InputTokens, out.Usage.PromptTokens)
		result.Usage.CompletionTokens = max(out.Usage.OutputTokens, out.Usage.CompletionTokens)
	}
	reportProgress(progress, len(result.Images), len(result.Images))
	return result, nil
}

// sendImageCall sends the generation or edit request and returns the 2xx
// response and the target URL.
func (p *openaiProvider) sendImageCall(ctx context.Context, job *imageJob, model string, withFormat bool) (*http.Response, string, error) {
	format := ""
	if withFormat {
		format = "b64_json"
	}
	if !job.edit {
		target := endpoint(p.cfg.BaseURL, "/v1/images/generations")
		payload := &openaiImageRequest{Model: model, Prompt: job.req.Prompt, Size: job.size, N: job.n, ResponseFormat: format}
		resp, err := p.do(ctx, http.MethodPost, target, payload, "application/json")
		return resp, target, err
	}

	target := endpoint(p.cfg.BaseURL, "/v1/images/edits")
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := writeImagePart(form, "image", job.req.Source); err != nil {
		return nil, target, err
	}
	if job.req.Mask != nil {
		if err := writeImagePart(form, "mask", job.req.Mask); err != nil {
			return nil, target, err
		}
	}
	fields := [][2]string{{"prompt", job.req.Prompt}, {"model", model}, {"size", job.size},
		{"n", strconv.Itoa(job.n)}, {"response_format", format}}
	for _, field := range fields {
		if field[1] == "" {
			continue
		}
		if err := form.WriteField(field[0], field[1]); err != nil {
			return nil, target, fmt.Errorf("openai: build edit form: %w", err)
		}
	}
	if err := form.Close(); err != nil {
		return nil, target, fmt.Errorf("openai: build edit form: %w", err)
	}
	resp, err := p.doBody(ctx, http.MethodPost, target, &body, form.FormDataContentType(), "application/json")
	return resp, target, err
}

// writeImagePart adds an image file part with its sniffed content type.
func writeImagePart(form *multipart.Writer, field string, img *core.AIImage) error {
	mediaType := http.DetectContentType(img.Data)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s%s"`, field, field, imageExtension(mediaType)))
	header.Set("Content-Type", mediaType)
	part, err := form.CreatePart(header)
	if err != nil {
		return fmt.Errorf("openai: build edit form: %w", err)
	}
	if _, err := part.Write(img.Data); err != nil {
		return fmt.Errorf("openai: build edit form: %w", err)
	}
	return nil
}

// maxImageRedirects caps redirects when fetching an image URL.
const maxImageRedirects = 3

// fetchImageURL downloads an image URL from an image response. The URL and
// every redirect must stay on the scheme and host of the provider base URL,
// so a response cannot point the server at another host.
func (p *openaiProvider) fetchImageURL(ctx context.Context, raw string, budget *int64) ([]byte, error) {
	base, err := url.Parse(p.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("openai: parse base url: %w", err)
	}
	target, err := base.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("openai: image url is not valid: %w", err)
	}
	sameHost := func(u *url.URL) bool {
		return u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host)
	}
	if !sameHost(target) {
		return nil, fmt.Errorf("openai: image url host %s is not the provider host %s: %w", target.Host, base.Host, core.ErrForbidden)
	}

	client := *p.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxImageRedirects {
			return errors.New("too many redirects")
		}
		if !sameHost(req.URL) {
			return fmt.Errorf("redirect to host %s: %w", req.URL.Host, core.ErrForbidden)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("openai: build image request: %w", err)
	}
	p.setAuth(req.Header)
	for name, value := range p.cfg.Headers {
		req.Header.Set(name, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: fetch image %s: %w", target.Redacted(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, p.apiError(http.MethodGet, target.Redacted(), resp)
	}
	return readImage("openai", resp.Body, budget)
}
