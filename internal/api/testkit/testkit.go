// Package testkit holds the test helpers of the built-in module handlers: a
// core.Backend stub, a fake with overridable methods, and a server over a
// module registry.
package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
	"github.com/gamemaker1/markspace/internal/module"
)

// BaseURL is the origin Env configures as the base URL.
const BaseURL = "https://notes.test"

// Env returns a handler environment over backend with a discarding logger,
// a one-hour session, a 1 MiB upload limit and BaseURL.
func Env(backend core.Backend) api.Env {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.Env{
		Backend:    backend,
		Logger:     logger,
		Auth:       &api.Authenticator{Backend: backend, Logger: logger},
		SessionTTL: time.Hour,
		MaxUpload:  1 << 20,
		BaseURL:    BaseURL,
	}
}

// Serve registers the modules and returns the API handler with the
// authentication middleware and the JSON 404 of api.Server.
func Serve(tb testing.TB, env api.Env, mods ...module.Module) http.Handler {
	tb.Helper()
	reg := module.NewRegistry()
	for _, m := range mods {
		if err := reg.Add(m); err != nil {
			tb.Fatalf("add module: %v", err)
		}
	}
	srv, err := api.NewServer(api.Options{
		Logger: env.Logger,
		Auth:   env.Auth,
		Mount:  func(mux *http.ServeMux) error { return reg.Mount(mux, nil) },
	})
	if err != nil {
		tb.Fatalf("NewServer: %v", err)
	}
	return srv.Handler()
}

// SignedIn returns a fake whose every session cookie resolves to user u1 with
// the given admin flag.
func SignedIn(admin bool) *Fake {
	return &Fake{
		ActorFromSessionFn: func(_ context.Context, _ string) (*core.Actor, error) {
			return &core.Actor{User: &core.User{ID: "u1", IsAdmin: admin, IsActive: true}}, nil
		},
	}
}

// Do runs one request through the handler. A non-empty body is sent as JSON
// and a non-empty cookie as the session cookie.
func Do(tb testing.TB, h http.Handler, method, target, body, cookie string) *httptest.ResponseRecorder {
	tb.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: api.SessionCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// DecodeBody unmarshals a recorder body into v.
func DecodeBody(tb testing.TB, rec *httptest.ResponseRecorder, v any) {
	tb.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		tb.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

// MultipartBody builds a multipart body with fields in order, then a file
// part when fileName is not empty.
func MultipartBody(tb testing.TB, fields [][2]string, fileName, content string) (*bytes.Buffer, string) {
	tb.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			tb.Fatalf("WriteField: %v", err)
		}
	}
	if fileName != "" {
		part, err := mw.CreateFormFile("file", fileName)
		if err != nil {
			tb.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			tb.Fatalf("write part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		tb.Fatalf("close writer: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

// ServeBody runs one request with a raw body and content type.
func ServeBody(tb testing.TB, h http.Handler, method, target string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	tb.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// SSEEvents parses a text/event-stream body into event names and data.
func SSEEvents(body string) (names []string, data []string) {
	for _, frame := range strings.Split(body, "\n\n") {
		var name, payload string
		for _, line := range strings.Split(frame, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				payload = strings.TrimPrefix(line, "data: ")
			}
		}
		if name != "" {
			names = append(names, name)
			data = append(data, payload)
		}
	}
	return names, data
}
