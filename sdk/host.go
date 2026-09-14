package sdk

import (
	"bufio"
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// APIPrefix is the path of the host API below the Markspace base URL.
const APIPrefix = "/api/plugin/v1"

// DefaultMaxTries is the number of attempts a host call makes.
const DefaultMaxTries = 5

// Retry delays. The delay after failed attempt n is
// min(maxBackoff, baseBackoff * 2^(n-1)) times a random factor in [0.5, 1).
// A Retry-After header on a retried status replaces it, up to maxRetryAfter.
const (
	baseBackoff   = 500 * time.Millisecond
	maxBackoff    = 30 * time.Second
	maxRetryAfter = 120 * time.Second
)

// Host is a client for the Markspace host API. The zero MaxTries means
// DefaultMaxTries. A nil Client means http.DefaultClient.
type Host struct {
	// BaseURL is the Markspace URL followed by APIPrefix.
	BaseURL  string
	Token    string
	PluginID string
	Client   *http.Client
	MaxTries int
	// Sleep replaces the retry wait in tests.
	Sleep func(time.Duration)
}

// NewHost returns a client for the host API of the Markspace instance at
// markspaceURL.
func NewHost(markspaceURL, token, pluginID string) *Host {
	return &Host{
		BaseURL:  strings.TrimRight(markspaceURL, "/") + APIPrefix,
		Token:    token,
		PluginID: pluginID,
		MaxTries: DefaultMaxTries,
	}
}

// WithToken returns a copy of h that authenticates with token.
func (h *Host) WithToken(token string) *Host {
	c := *h
	c.Token = token
	return &c
}

// HostError is a failed host API call. Status is 0 when no response arrived.
type HostError struct {
	Status  int
	Message string
	Field   string
}

func (e *HostError) Error() string {
	s := fmt.Sprintf("host API %d: %s", e.Status, e.Message)
	if e.Field != "" {
		s += " (field " + e.Field + ")"
	}
	return s
}

// IsNotFound reports whether err is a HostError with status 404.
func IsNotFound(err error) bool {
	var he *HostError
	return errors.As(err, &he) && he.Status == http.StatusNotFound
}

type hostRequest struct {
	method string
	path   string
	query  url.Values
	accept string
	// body returns a fresh body and its content type for each attempt.
	body func() (io.Reader, string, error)
	// stream is set when the body reads from a caller's reader. Such a
	// request retries only when stream is an io.Seeker.
	stream io.Reader
}

func jsonBody(v any) (func() (io.Reader, string, error), error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return func() (io.Reader, string, error) {
		return bytes.NewReader(b), "application/json", nil
	}, nil
}

func (h *Host) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return http.DefaultClient
}

func (h *Host) wait(ctx context.Context, d time.Duration) error {
	if h.Sleep != nil {
		h.Sleep(d)
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func backoff(attempt int) time.Duration {
	d := baseBackoff << (attempt - 1)
	if d > maxBackoff || d <= 0 {
		d = maxBackoff
	}
	return time.Duration(float64(d) * (0.5 + rand.Float64()/2))
}

func retryAfter(resp *http.Response) (time.Duration, bool) {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	var d time.Duration
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
		d = time.Duration(n) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
		if d < 0 {
			d = 0
		}
	} else {
		return 0, false
	}
	if d > maxRetryAfter {
		d = maxRetryAfter
	}
	return d, true
}

// newIdempotencyKey returns 32 random hex characters.
func newIdempotencyKey() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// retryable reports whether a status is retried. 500 is not.
func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// send runs a request with retries. It returns a response with a 2xx status
// or an error. The caller closes the response body.
func (h *Host) send(ctx context.Context, r hostRequest) (*http.Response, error) {
	tries := h.MaxTries
	if tries < 1 {
		tries = DefaultMaxTries
	}
	var seeker io.Seeker
	var start int64
	if r.stream != nil {
		if s, ok := r.stream.(io.Seeker); ok {
			off, err := s.Seek(0, io.SeekCurrent)
			if err != nil {
				tries = 1
			} else {
				seeker, start = s, off
			}
		} else {
			tries = 1
		}
	}
	target := h.BaseURL + r.path
	if len(r.query) > 0 {
		target += "?" + r.query.Encode()
	}
	// Every POST carries one Idempotency-Key for all of its attempts.
	idemKey := ""
	if r.method == http.MethodPost {
		idemKey = newIdempotencyKey()
	}
	for attempt := 1; ; attempt++ {
		if attempt > 1 && seeker != nil {
			if _, err := seeker.Seek(start, io.SeekStart); err != nil {
				return nil, &HostError{Message: "cannot rewind upload: " + err.Error()}
			}
		}
		var body io.Reader
		contentType := ""
		if r.body != nil {
			b, ct, err := r.body()
			if err != nil {
				return nil, err
			}
			body, contentType = b, ct
		}
		req, err := http.NewRequestWithContext(ctx, r.method, target, body)
		if err != nil {
			return nil, err
		}
		if h.Token != "" {
			req.Header.Set("Authorization", "Bearer "+h.Token)
		}
		accept := r.accept
		if accept == "" {
			accept = "application/json"
		}
		req.Header.Set("Accept", accept)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("User-Agent", "markspace-sdk-go/1 ("+h.PluginID+")")
		if idemKey != "" {
			req.Header.Set("Idempotency-Key", idemKey)
		}
		resp, err := h.client().Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt >= tries {
				return nil, &HostError{Message: err.Error()}
			}
			if err := h.wait(ctx, backoff(attempt)); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		if retryable(resp.StatusCode) && attempt < tries {
			delay := backoff(attempt)
			if d, ok := retryAfter(resp); ok {
				delay = d
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			if err := h.wait(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		defer resp.Body.Close()
		return nil, parseHostError(resp)
	}
}

func parseHostError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	he := &HostError{Status: resp.StatusCode}
	var body struct {
		Error string `json:"error"`
		Field string `json:"field"`
	}
	if json.Unmarshal(b, &body) == nil && body.Error != "" {
		he.Message, he.Field = body.Error, body.Field
	} else {
		he.Message = strings.TrimSpace(string(b))
		if he.Message == "" {
			he.Message = http.StatusText(resp.StatusCode)
		}
	}
	return he
}

// Do sends a JSON request to path below BaseURL and decodes the JSON reply
// into out. in and out may be nil.
func (h *Host) Do(ctx context.Context, method, path string, in, out any) error {
	return h.do(ctx, method, path, nil, in, out)
}

func (h *Host) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	body, err := jsonBody(in)
	if err != nil {
		return err
	}
	resp, err := h.send(ctx, hostRequest{method: method, path: path, query: query, body: body})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return &HostError{Status: resp.StatusCode, Message: err.Error()}
	}
	if out == nil || len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

func objectPath(prefix, id, suffix string) (string, error) {
	if !ValidObjectID(id) {
		return "", fmt.Errorf("invalid id %q", id)
	}
	return prefix + id + suffix, nil
}

func (h *Host) getMap(ctx context.Context, path string) (map[string]any, error) {
	var out map[string]any
	return out, h.Do(ctx, "GET", path, nil, &out)
}

// Page is one page of a list reply. NextCursor is empty on the last page.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func getPage[T any](ctx context.Context, h *Host, path string, q url.Values) (*Page[T], error) {
	var p Page[T]
	if err := h.do(ctx, "GET", path, q, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (h *Host) postMap(ctx context.Context, method, path string, in any) (map[string]any, error) {
	var out map[string]any
	return out, h.Do(ctx, method, path, in, &out)
}

// Me returns the plugin id, grants and permissions of the token.
func (h *Host) Me(ctx context.Context) (map[string]any, error) { return h.getMap(ctx, "/me") }

// Settings returns the settings an admin saved for the plugin, from GET /settings.
func (h *Host) Settings(ctx context.Context) (map[string]any, error) {
	out, err := h.getMap(ctx, "/settings")
	if err != nil {
		return nil, err
	}
	settings, _ := out["settings"].(map[string]any)
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, nil
}

// Tree returns the tree items of a space.
func (h *Host) Tree(ctx context.Context, spaceID string) (*Page[map[string]any], error) {
	p, err := objectPath("/spaces/", spaceID, "/tree")
	if err != nil {
		return nil, err
	}
	return getPage[map[string]any](ctx, h, p, nil)
}

// GetItem returns {kind, folder|note|file}.
func (h *Host) GetItem(ctx context.Context, id string) (map[string]any, error) {
	p, err := objectPath("/items/", id, "")
	if err != nil {
		return nil, err
	}
	return h.getMap(ctx, p)
}

// NewNote is the body of POST /notes. An empty ParentID is the space root.
// Content is the body. Frontmatter sets frontmatter keys other than the ones
// Markspace owns.
type NewNote struct {
	ID          string         `json:"id,omitempty"`
	SpaceID     string         `json:"spaceId"`
	ParentID    string         `json:"parentId"`
	Title       string         `json:"title"`
	Content     string         `json:"content"`
	Tags        []string       `json:"tags,omitempty"`
	Icon        string         `json:"icon,omitempty"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
}

// CreateNote creates a note.
func (h *Host) CreateNote(ctx context.Context, n NewNote) (map[string]any, error) {
	return h.postMap(ctx, "POST", "/notes", n)
}

// NoteUpdate is the body of PATCH /notes/{id}. BaseUpdatedAt is required. A
// nil value in Frontmatter deletes that key.
type NoteUpdate struct {
	Title         *string        `json:"title,omitempty"`
	Content       *string        `json:"content,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	Icon          *string        `json:"icon,omitempty"`
	Frontmatter   map[string]any `json:"frontmatter,omitempty"`
	BaseUpdatedAt string         `json:"baseUpdatedAt"`
}

// UpdateNote updates a note. A stale BaseUpdatedAt returns a 409 HostError.
func (h *Host) UpdateNote(ctx context.Context, id string, u NoteUpdate) (map[string]any, error) {
	if u.BaseUpdatedAt == "" {
		return nil, errors.New("UpdateNote needs BaseUpdatedAt")
	}
	p, err := objectPath("/notes/", id, "")
	if err != nil {
		return nil, err
	}
	return h.postMap(ctx, "PATCH", p, u)
}

// CreateFolder creates a folder. An empty parentID is the space root.
func (h *Host) CreateFolder(ctx context.Context, spaceID, parentID, name string) (map[string]any, error) {
	return h.postMap(ctx, "POST", "/folders", map[string]any{"spaceId": spaceID, "parentId": parentID, "name": name})
}

// Upload describes a file upload. Body is streamed. The upload retries only
// when Body is an io.Seeker.
type Upload struct {
	SpaceID    string
	ParentID   string
	Name       string
	MIME       string
	ID         string
	OnConflict string
	Body       io.Reader
}

// UploadFile uploads a file as multipart/form-data with the fields spaceId,
// parentId, id, onConflict and file.
func (h *Host) UploadFile(ctx context.Context, u Upload) (map[string]any, error) {
	if u.Body == nil || u.Name == "" {
		return nil, errors.New("UploadFile needs Name and Body")
	}
	body := func() (io.Reader, string, error) {
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		go func() {
			err := writeUpload(mw, u)
			if err == nil {
				err = mw.Close()
			}
			pw.CloseWithError(err)
		}()
		return pr, mw.FormDataContentType(), nil
	}
	resp, err := h.send(ctx, hostRequest{method: "POST", path: "/files", body: body, stream: u.Body})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := decodeBody(resp, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func writeUpload(mw *multipart.Writer, u Upload) error {
	fields := [][2]string{{"spaceId", u.SpaceID}, {"parentId", u.ParentID}, {"id", u.ID}, {"onConflict", u.OnConflict}}
	for _, f := range fields {
		if f[1] == "" && f[0] != "parentId" {
			continue
		}
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return err
		}
	}
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": u.Name}))
	ct := u.MIME
	if ct == "" {
		ct = "application/octet-stream"
	}
	hdr.Set("Content-Type", ct)
	part, err := mw.CreatePart(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(part, u.Body)
	return err
}

func decodeBody(resp *http.Response, out any) error {
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return &HostError{Status: resp.StatusCode, Message: err.Error()}
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

// FileContent streams the content of a file. The caller closes the reader.
func (h *Host) FileContent(ctx context.Context, id string) (io.ReadCloser, error) {
	p, err := objectPath("/files/", id, "/content")
	if err != nil {
		return nil, err
	}
	resp, err := h.send(ctx, hostRequest{method: "GET", path: p, accept: "*/*"})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// FileText is the extracted text of a file. Truncated is true when the
// extractor kept only the first part.
type FileText struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// FileText returns the extracted text of a file.
func (h *Host) FileText(ctx context.Context, id string) (*FileText, error) {
	p, err := objectPath("/files/", id, "/text")
	if err != nil {
		return nil, err
	}
	var out FileText
	if err := h.Do(ctx, "GET", p, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Move moves an item. An empty parentID is the space root. A nil position
// appends.
func (h *Host) Move(ctx context.Context, id, parentID string, position *int) (map[string]any, error) {
	p, err := objectPath("/items/", id, "/move")
	if err != nil {
		return nil, err
	}
	body := map[string]any{"parentId": parentID}
	if position != nil {
		body["position"] = *position
	}
	return h.postMap(ctx, "POST", p, body)
}

// Copy copies an item into a space and folder. An empty name keeps the
// default copy name.
func (h *Host) Copy(ctx context.Context, id, spaceID, parentID, name string) (map[string]any, error) {
	p, err := objectPath("/items/", id, "/copy")
	if err != nil {
		return nil, err
	}
	body := map[string]any{"spaceId": spaceID, "parentId": parentID}
	if name != "" {
		body["name"] = name
	}
	return h.postMap(ctx, "POST", p, body)
}

// Trash moves an item to the trash.
func (h *Host) Trash(ctx context.Context, id string) error {
	p, err := objectPath("/items/", id, "/trash")
	if err != nil {
		return err
	}
	return h.Do(ctx, "POST", p, nil, nil)
}

// OutboundLinks returns the resolved outbound links of a note.
func (h *Host) OutboundLinks(ctx context.Context, noteID string) (*Page[map[string]any], error) {
	p, err := objectPath("/notes/", noteID, "/links")
	if err != nil {
		return nil, err
	}
	return getPage[map[string]any](ctx, h, p, nil)
}

// Backlinks returns the items that link to an item.
func (h *Host) Backlinks(ctx context.Context, id string) (*Page[map[string]any], error) {
	p, err := objectPath("/items/", id, "/backlinks")
	if err != nil {
		return nil, err
	}
	return getPage[map[string]any](ctx, h, p, nil)
}

// ResolvedLink is one result of Resolve.
type ResolvedLink struct {
	Target string `json:"target"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Path   string `json:"path"`
	URL    string `json:"url"`
}

// Resolve resolves link targets as written in the note fromNoteID.
func (h *Host) Resolve(ctx context.Context, spaceID, fromNoteID string, targets []string) (*Page[ResolvedLink], error) {
	var out Page[ResolvedLink]
	body := map[string]any{"spaceId": spaceID, "fromNoteId": fromNoteID, "targets": targets}
	if err := h.Do(ctx, "POST", "/resolve", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchQuery is the query of GET /search. Empty fields are omitted.
type SearchQuery struct {
	Q      string
	Mode   string
	Space  string
	Limit  int
	Cursor string
}

// Search runs a text or semantic search.
func (h *Host) Search(ctx context.Context, q SearchQuery) (*Page[map[string]any], error) {
	v := url.Values{"q": {q.Q}}
	if q.Mode != "" {
		v.Set("mode", q.Mode)
	}
	if q.Space != "" {
		v.Set("space", q.Space)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	return getPage[map[string]any](ctx, h, "/search", v)
}

// SuggestedEdit replaces the anchored passage with Text.
type SuggestedEdit struct {
	Anchor map[string]any `json:"anchor"`
	Text   string         `json:"text"`
}

// NewSuggestion is the body of POST /suggestions.
type NewSuggestion struct {
	NoteID    string          `json:"noteId"`
	Title     string          `json:"title"`
	Rationale string          `json:"rationale,omitempty"`
	Edits     []SuggestedEdit `json:"edits"`
}

// CreateSuggestion proposes edits to a note.
func (h *Host) CreateSuggestion(ctx context.Context, s NewSuggestion) (map[string]any, error) {
	return h.postMap(ctx, "POST", "/suggestions", s)
}

// WithdrawSuggestion deletes a suggestion the plugin created.
func (h *Host) WithdrawSuggestion(ctx context.Context, id string) error {
	p, err := objectPath("/suggestions/", id, "")
	if err != nil {
		return err
	}
	return h.Do(ctx, "DELETE", p, nil, nil)
}

// CreateComment adds a comment. A nil anchor is omitted.
func (h *Host) CreateComment(ctx context.Context, itemID, body string, anchor map[string]any) (map[string]any, error) {
	in := map[string]any{"itemId": itemID, "body": body}
	if anchor != nil {
		in["anchor"] = anchor
	}
	return h.postMap(ctx, "POST", "/comments", in)
}

// AIComplete runs an AI request as described in docs/ai.md.
func (h *Host) AIComplete(ctx context.Context, req any) (map[string]any, error) {
	return h.postMap(ctx, "POST", "/ai/complete", req)
}

// StreamEvent is one server-sent event frame.
type StreamEvent struct {
	Event string
	Data  json.RawMessage
}

// AIStream reads the frames of a streamed AI request.
type AIStream struct {
	resp *http.Response
	sc   *bufio.Scanner
	ev   StreamEvent
	err  error
}

// AICompleteStream runs an AI request with Accept: text/event-stream. Retries
// happen only before the stream starts. The caller closes the stream.
func (h *Host) AICompleteStream(ctx context.Context, req any) (*AIStream, error) {
	body, err := jsonBody(req)
	if err != nil {
		return nil, err
	}
	resp, err := h.send(ctx, hostRequest{method: "POST", path: "/ai/complete", body: body, accept: "text/event-stream"})
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	return &AIStream{resp: resp, sc: sc}, nil
}

// Next reads the next frame. It returns false at the end of the stream or on
// an error.
func (s *AIStream) Next() bool {
	event := ""
	var data []string
	for s.sc.Scan() {
		line := s.sc.Text()
		if line == "" {
			if event == "" && data == nil {
				continue
			}
			if event == "" {
				event = "message"
			}
			s.ev = StreamEvent{Event: event, Data: json.RawMessage(strings.Join(data, "\n"))}
			return true
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event = value
		case "data":
			data = append(data, value)
		}
	}
	s.err = s.sc.Err()
	return false
}

// Event returns the frame read by the last call to Next.
func (s *AIStream) Event() StreamEvent { return s.ev }

// Err returns the read error that stopped Next, if any.
func (s *AIStream) Err() error { return s.err }

// Close closes the stream.
func (s *AIStream) Close() error { return s.resp.Body.Close() }

// AIEmbed returns embeddings for texts.
func (h *Host) AIEmbed(ctx context.Context, texts []string) (map[string]any, error) {
	return h.postMap(ctx, "POST", "/ai/embed", map[string]any{"texts": texts})
}

// AIImageRequest is the body of POST /ai/image.
type AIImageRequest struct {
	Prompt     string `json:"prompt"`
	Size       string `json:"size,omitempty"`
	FileID     string `json:"fileId,omitempty"`
	MaskFileID string `json:"maskFileId,omitempty"`
}

// AIImage generates or edits an image.
func (h *Host) AIImage(ctx context.Context, r AIImageRequest) (map[string]any, error) {
	return h.postMap(ctx, "POST", "/ai/image", r)
}

// AIModel is one model the plugin may call.
type AIModel struct {
	ProviderID      string          `json:"providerId"`
	Model           string          `json:"model"`
	Vision          bool            `json:"vision"`
	ImageGeneration bool            `json:"imageGeneration"`
	ImageEditing    bool            `json:"imageEditing"`
	Prices          json.RawMessage `json:"prices,omitempty"`
}

// AIModels returns the models the plugin may call, with capabilities and
// prices.
func (h *Host) AIModels(ctx context.Context) (*Page[AIModel], error) {
	return getPage[AIModel](ctx, h, "/ai/models", nil)
}

// WebPage is the reply of POST /web/fetch.
type WebPage struct {
	FinalURL  string `json:"finalUrl"`
	Status    int    `json:"status"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Markdown  string `json:"markdown"`
	HTML      string `json:"html,omitempty"`
	SHA256    string `json:"sha256"`
	FetchedAt string `json:"fetchedAt"`
}

// WebFetch fetches a page through the guarded fetcher. A nil respectRobots
// is omitted.
func (h *Host) WebFetch(ctx context.Context, pageURL string, respectRobots *bool) (*WebPage, error) {
	in := map[string]any{"url": pageURL}
	if respectRobots != nil {
		in["respectRobots"] = *respectRobots
	}
	var out WebPage
	if err := h.Do(ctx, "POST", "/web/fetch", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// NewJob is the body of POST /jobs.
type NewJob struct {
	Kind     string `json:"kind"`
	Title    string `json:"title,omitempty"`
	Payload  any    `json:"payload,omitempty"`
	SpaceID  string `json:"spaceId,omitempty"`
	RunAfter string `json:"runAfter,omitempty"`
	ParentID string `json:"parentId,omitempty"`
}

// CreateJob queues a job of a kind from the manifest.
func (h *Host) CreateJob(ctx context.Context, j NewJob) (map[string]any, error) {
	if !ValidLocalID(j.Kind) {
		return nil, fmt.Errorf("invalid job kind %q", j.Kind)
	}
	return h.postMap(ctx, "POST", "/jobs", j)
}

// JobUpdate is the body of PATCH /jobs/{id}. Status is done or failed.
type JobUpdate struct {
	Progress *int    `json:"progress,omitempty"`
	Total    *int    `json:"total,omitempty"`
	Message  *string `json:"message,omitempty"`
	Status   string  `json:"status,omitempty"`
	Result   any     `json:"result,omitempty"`
}

// UpdateJob reports progress or completion of a job.
func (h *Host) UpdateJob(ctx context.Context, id string, u JobUpdate) (map[string]any, error) {
	p, err := objectPath("/jobs/", id, "")
	if err != nil {
		return nil, err
	}
	return h.postMap(ctx, "PATCH", p, u)
}

// JobLog appends a line to a job log.
func (h *Host) JobLog(ctx context.Context, id, level, message string) error {
	p, err := objectPath("/jobs/", id, "/log")
	if err != nil {
		return err
	}
	return h.Do(ctx, "POST", p, map[string]any{"level": level, "message": message}, nil)
}

func storagePath(prefix, key string) (string, error) {
	if !ValidStorageKey(key) {
		return "", fmt.Errorf("invalid storage key %q", key)
	}
	return prefix + url.PathEscape(key), nil
}

// KVItem is a stored key-value entry.
type KVItem struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	UpdatedAt string          `json:"updatedAt"`
}

// KVKey is one row of KVList.
type KVKey struct {
	Key       string `json:"key"`
	Size      int64  `json:"size"`
	UpdatedAt string `json:"updatedAt"`
}

// KVEntry returns the entry at key. It returns false when the key does not
// exist.
func (h *Host) KVEntry(ctx context.Context, key string) (*KVItem, bool, error) {
	p, err := storagePath("/kv/", key)
	if err != nil {
		return nil, false, err
	}
	var item KVItem
	err = h.Do(ctx, "GET", p, nil, &item)
	if IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &item, true, nil
}

// KVGet decodes the value at key into out. It returns false when the key does
// not exist.
func (h *Host) KVGet(ctx context.Context, key string, out any) (bool, error) {
	item, ok, err := h.KVEntry(ctx, key)
	if err != nil || !ok {
		return false, err
	}
	if out != nil && len(item.Value) > 0 {
		if err := json.Unmarshal(item.Value, out); err != nil {
			return true, err
		}
	}
	return true, nil
}

// KVPut stores value at key. The request body is {"value": value}.
func (h *Host) KVPut(ctx context.Context, key string, value any) error {
	p, err := storagePath("/kv/", key)
	if err != nil {
		return err
	}
	return h.Do(ctx, "PUT", p, map[string]any{"value": value}, nil)
}

// KVDelete deletes key.
func (h *Host) KVDelete(ctx context.Context, key string) error {
	p, err := storagePath("/kv/", key)
	if err != nil {
		return err
	}
	return h.Do(ctx, "DELETE", p, nil, nil)
}

// KVList returns one page of the keys that start with prefix. An empty
// cursor returns the first page.
func (h *Host) KVList(ctx context.Context, prefix, cursor string) (*Page[KVKey], error) {
	q := url.Values{"prefix": {prefix}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return getPage[KVKey](ctx, h, "/kv", q)
}

// BlobPut streams r to the blob at key. It retries only when r is an
// io.Seeker.
func (h *Host) BlobPut(ctx context.Context, key string, r io.Reader, contentType string) error {
	p, err := storagePath("/blobs/", key)
	if err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	body := func() (io.Reader, string, error) { return io.NopCloser(r), contentType, nil }
	resp, err := h.send(ctx, hostRequest{method: "PUT", path: p, body: body, stream: r})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// BlobGet streams the blob at key. It returns false when the blob does not
// exist. The caller closes the reader.
func (h *Host) BlobGet(ctx context.Context, key string) (io.ReadCloser, bool, error) {
	p, err := storagePath("/blobs/", key)
	if err != nil {
		return nil, false, err
	}
	resp, err := h.send(ctx, hostRequest{method: "GET", path: p, accept: "*/*"})
	if IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return resp.Body, true, nil
}

// BlobDelete deletes the blob at key.
func (h *Host) BlobDelete(ctx context.Context, key string) error {
	p, err := storagePath("/blobs/", key)
	if err != nil {
		return err
	}
	return h.Do(ctx, "DELETE", p, nil, nil)
}

// Secret returns the value of a manifest secret.
func (h *Host) Secret(ctx context.Context, name string) (string, error) {
	if !ValidSecretName(name) {
		return "", fmt.Errorf("invalid secret name %q", name)
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := h.Do(ctx, "GET", "/secrets/"+name, nil, &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

// Notification is the body of POST /notify.
type Notification struct {
	UserIDs []string `json:"userIds,omitempty"`
	SpaceID string   `json:"spaceId,omitempty"`
	Message string   `json:"message"`
	Link    string   `json:"link,omitempty"`
}

// Notify sends a notification to users or to the members of a space.
func (h *Host) Notify(ctx context.Context, n Notification) error {
	return h.Do(ctx, "POST", "/notify", n, nil)
}

// Emit publishes the event plugin.<pluginId>.<name>. A name that already
// starts with plugin. is sent as given.
func (h *Host) Emit(ctx context.Context, name, spaceID string, data any) error {
	typ := name
	if !strings.HasPrefix(name, "plugin.") {
		typ = "plugin." + h.PluginID + "." + name
	}
	in := map[string]any{"type": typ, "data": data}
	if spaceID != "" {
		in["spaceId"] = spaceID
	}
	return h.Do(ctx, "POST", "/events", in, nil)
}
