package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/modules/system"
)

// ErrUnreachable reports that no server answered at the address.
var ErrUnreachable = errors.New("the server is not reachable")

// ErrTokenRejected reports a 401 answer: the token is unknown, revoked or
// missing.
var ErrTokenRejected = errors.New("the server rejected the token")

// Client calls the JSON API of one server with a bearer token.
type Client struct {
	// BaseURL is the server root, such as http://127.0.0.1:8080.
	BaseURL string
	// Token is sent as Authorization: Bearer. Health needs none.
	Token string
	HTTP  *http.Client
}

// New returns a client for baseURL with the given token.
func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{}}
}

// LocalURL turns a listen address into a URL on the same machine. A wildcard
// or empty host, as in ":8080", becomes 127.0.0.1.
func LocalURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// APIError is a non-2xx answer other than 401.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Status == http.StatusForbidden {
		return "the token has no admin rights: " + e.Message
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// send issues one request and returns the response when the status is 2xx.
func (c *Client) send(ctx context.Context, method, path string, body any, auth bool) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		r = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		if c.Token == "" {
			return nil, fmt.Errorf("%w: no token was given", ErrTokenRejected)
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		cause := err
		var ue *url.Error
		if errors.As(err, &ue) {
			cause = ue.Err
		}
		return nil, fmt.Errorf("%w at %s (%v)", ErrUnreachable, c.BaseURL, cause)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var eb struct {
		Error string `json:"error"`
	}
	msg := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &eb) == nil && eb.Error != "" {
		msg = eb.Error
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%w at %s", ErrTokenRejected, c.BaseURL)
	}
	return nil, &APIError{Status: resp.StatusCode, Message: msg}
}

// call sends an authenticated JSON request and decodes the answer into out
// when out is not nil.
func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.send(ctx, method, path, body, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

// Health calls GET /healthz without a token.
func (c *Client) Health(ctx context.Context) error {
	resp, err := c.send(ctx, http.MethodGet, "/healthz", nil, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if strings.TrimSpace(string(data)) != "ok" {
		return fmt.Errorf("the server at %s answered /healthz with %q", c.BaseURL, data)
	}
	return nil
}

// Status returns the instance status.
func (c *Client) Status(ctx context.Context) (*system.Status, error) {
	var out system.Status
	if err := c.call(ctx, http.MethodGet, "/api/admin/status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// userPage is the body of GET /api/users.
type userPage struct {
	Items []*core.User `json:"items"`
	Total int          `json:"total"`
}

// Users returns every account, ordered by email.
func (c *Client) Users(ctx context.Context) ([]*core.User, error) {
	const limit = 200
	var out []*core.User
	for offset := 0; ; offset += limit {
		var page userPage
		path := "/api/users?limit=" + strconv.Itoa(limit) + "&offset=" + strconv.Itoa(offset)
		if err := c.call(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if len(page.Items) < limit || len(out) >= page.Total {
			return out, nil
		}
	}
}

// UserByEmail returns the account with this email address. The match ignores
// case and surrounding white space.
func (c *Client) UserByEmail(ctx context.Context, email string) (*core.User, error) {
	want := strings.ToLower(strings.TrimSpace(email))
	var page userPage
	path := "/api/users?limit=200&q=" + url.QueryEscape(want)
	if err := c.call(ctx, http.MethodGet, path, nil, &page); err != nil {
		return nil, err
	}
	for _, u := range page.Items {
		if strings.ToLower(u.Email) == want {
			return u, nil
		}
	}
	return nil, fmt.Errorf("no account has the email %s: %w", want, core.ErrNotFound)
}

// CreateUser adds an account.
func (c *Client) CreateUser(ctx context.Context, in core.UserInput) (*core.User, error) {
	var out core.User
	if err := c.call(ctx, http.MethodPost, "/api/users", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateUser changes an account. A new password signs out every session of
// the account.
func (c *Client) UpdateUser(ctx context.Context, id string, in core.UserInput) (*core.User, error) {
	var out core.User
	if err := c.call(ctx, http.MethodPatch, "/api/users/"+url.PathEscape(id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Reindex rereads every markdown file into the index and returns the count
// the server reports.
func (c *Client) Reindex(ctx context.Context) (int, error) {
	var out struct {
		Indexed int `json:"indexed"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/reindex", nil, &out); err != nil {
		return 0, err
	}
	return out.Indexed, nil
}

// Backup copies the backup archive to w and returns the bytes written. It
// reads the archive while copying and fails when the gzip stream or the tar
// stream is incomplete, which is how a failure on the server after the first
// byte shows.
func (c *Client) Backup(ctx context.Context, w io.Writer) (int64, error) {
	resp, err := c.send(ctx, http.MethodGet, "/api/admin/backup", nil, true)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	counter := &countWriter{w: w}
	gz, err := gzip.NewReader(io.TeeReader(resp.Body, counter))
	if err != nil {
		return counter.n, fmt.Errorf("the backup is incomplete: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		_, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return counter.n, fmt.Errorf("the backup is incomplete: %w", err)
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return counter.n, fmt.Errorf("the backup is incomplete: %w", err)
		}
	}
	// Reading to the end checks the gzip trailer and copies any padding.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return counter.n, fmt.Errorf("the backup is incomplete: %w", err)
	}
	if err := gz.Close(); err != nil {
		return counter.n, fmt.Errorf("the backup is incomplete: %w", err)
	}
	if _, err := io.Copy(counter, resp.Body); err != nil {
		return counter.n, fmt.Errorf("copy backup: %w", err)
	}
	if counter.err != nil {
		return counter.n, fmt.Errorf("write backup: %w", counter.err)
	}
	return counter.n, nil
}

// countWriter counts the bytes written to w and keeps the first error.
type countWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countWriter) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err != nil {
		c.err = err
	}
	return n, err
}
