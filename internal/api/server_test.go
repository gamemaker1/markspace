package api_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/api/testkit"
	"github.com/gamemaker1/markspace/internal/core"
)

func TestNewServerValidatesOptions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := []struct {
		name string
		opts api.Options
	}{
		{"no logger", api.Options{Auth: &api.Authenticator{}}},
		{"no auth", api.Options{Logger: logger}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := api.NewServer(tc.opts); err == nil {
				t.Fatal("want an error, got nil")
			}
		})
	}
}

func TestNewServerReturnsMountError(t *testing.T) {
	env := testkit.Env(&testkit.Fake{})
	_, err := api.NewServer(api.Options{Logger: env.Logger, Auth: env.Auth, Mount: func(*http.ServeMux) error {
		return core.ErrConflict
	}})
	if err == nil {
		t.Fatal("want the mount error, got nil")
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	rec := testkit.Do(t, testkit.Serve(t, testkit.Env(&testkit.Fake{})), "GET", "/api/nope", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
	var body api.ErrorBody
	testkit.DecodeBody(t, rec, &body)
	if body.Error == "" {
		t.Error("error message is empty")
	}
}

func TestIntParam(t *testing.T) {
	cases := []struct {
		raw  string
		def  int
		max  int
		want int
	}{
		{"", 25, 100, 25},
		{"7", 25, 100, 7},
		{"700", 25, 100, 100},
		{"junk", 25, 100, 25},
		{"-1", 25, 100, 25},
		{"900", 25, 0, 900},
		{" 12 ", 25, 100, 12},
	}
	for _, tc := range cases {
		if got := api.IntParam(tc.raw, tc.def, tc.max); got != tc.want {
			t.Errorf("IntParam(%q, %d, %d) = %d, want %d", tc.raw, tc.def, tc.max, got, tc.want)
		}
	}
}

func TestStatusFor(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{core.ErrInvalid, http.StatusBadRequest},
		{core.Invalid("f", "m"), http.StatusBadRequest},
		{core.ErrUnauthorized, http.StatusUnauthorized},
		{core.ErrForbidden, http.StatusForbidden},
		{core.ErrNotFound, http.StatusNotFound},
		{core.ErrConflict, http.StatusConflict},
		{core.ErrExists, http.StatusConflict},
		{core.ErrTooLarge, http.StatusRequestEntityTooLarge},
		{core.ErrUnsupported, http.StatusUnsupportedMediaType},
		{core.ErrRateLimited, http.StatusTooManyRequests},
		{core.ErrQuotaExceeded, http.StatusTooManyRequests},
		{fmt.Errorf("wrapped: %w", core.ErrForbidden), http.StatusForbidden},
		{fmt.Errorf("plain"), 0},
	}
	for _, tc := range cases {
		if got := api.StatusFor(tc.err); got != tc.want {
			t.Errorf("StatusFor(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestUnavailableMapsTo503(t *testing.T) {
	both := fmt.Errorf("ai is disabled: %w: %w", core.ErrUnavailable, core.ErrUnsupported)
	cases := []struct {
		err  error
		want int
	}{
		{both, http.StatusServiceUnavailable},
		{core.ErrUnavailable, http.StatusServiceUnavailable},
		{core.ErrUnsupported, http.StatusUnsupportedMediaType},
	}
	for _, tc := range cases {
		if got := api.StatusFor(tc.err); got != tc.want {
			t.Errorf("StatusFor(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestWriteErrorHidesUnmappedErrors(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.WriteError(w, r, nil, fmt.Errorf("disk on fire at /secret/path"))
	})
	rec := testkit.Do(t, h, "GET", "/api/x", "", "")
	var body api.ErrorBody
	testkit.DecodeBody(t, rec, &body)
	if rec.Code != http.StatusInternalServerError || body.Error != "internal error" {
		t.Fatalf("unmapped error = %d %+v", rec.Code, body)
	}
}
