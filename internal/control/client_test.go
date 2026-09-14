package control

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gamemaker1/markspace/internal/core"
)

func TestLocalURL(t *testing.T) {
	for listen, want := range map[string]string{
		":8080":          "http://127.0.0.1:8080",
		"0.0.0.0:9000":   "http://127.0.0.1:9000",
		"[::]:9000":      "http://127.0.0.1:9000",
		"127.0.0.1:3000": "http://127.0.0.1:3000",
		"[::1]:3000":     "http://[::1]:3000",
		"notes.lan:80":   "http://notes.lan:80",
	} {
		if got := LocalURL(listen); got != want {
			t.Errorf("LocalURL(%q) = %q, want %q", listen, got, want)
		}
	}
}

func TestClientUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	c := New("http://"+addr, "msk_x")
	err = c.Health(t.Context())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Health = %v, want ErrUnreachable", err)
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "the server is not reachable at http://"+addr+" (") || strings.Contains(msg, "\n") {
		t.Fatalf("message %q is not one line naming the address and the cause", msg)
	}
}

func TestClientStatusErrors(t *testing.T) {
	status := http.StatusUnauthorized
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(`{"error":"email admin@example.com: already exists"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "msk_x")

	if _, err := c.Status(t.Context()); !errors.Is(err, ErrTokenRejected) {
		t.Fatalf("401: %v, want ErrTokenRejected", err)
	}
	status = http.StatusForbidden
	if _, err := c.Users(t.Context()); err == nil || !strings.HasPrefix(err.Error(), "the token has no admin rights") {
		t.Fatalf("403: %v", err)
	}
	status = http.StatusConflict
	_, err := c.CreateUser(t.Context(), core.UserInput{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 409 || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("409: %v", err)
	}

	if _, err := New(srv.URL, "").Status(t.Context()); !errors.Is(err, ErrTokenRejected) {
		t.Fatalf("no token: %v, want ErrTokenRejected before any request", err)
	}
}

func TestClientBearerAndEmailMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer msk_secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("q"); got != "ada@example.com" {
			t.Errorf("query %q, want the lowercased email", got)
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []core.User{
			{ID: "u2", Email: "ada@example.com.au"},
			{ID: "u1", Email: "ada@example.com"},
		}, "total": 2})
	}))
	defer srv.Close()
	u, err := New(srv.URL, "msk_secret").UserByEmail(t.Context(), " Ada@Example.com ")
	if err != nil || u.ID != "u1" {
		t.Fatalf("UserByEmail = %+v, %v, want u1", u, err)
	}
}

func TestClientBackupDetectsTruncation(t *testing.T) {
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	body := bytes.Repeat([]byte("note body\n"), 2000)
	tw.WriteHeader(&tar.Header{Name: "spaces/a/note.md", Mode: 0o644, Size: int64(len(body))})
	tw.Write(body)
	tw.Close()
	gz.Close()
	full := archive.Bytes()

	serve := full
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(serve)
	}))
	defer srv.Close()
	c := New(srv.URL, "msk_x")

	var out bytes.Buffer
	n, err := c.Backup(t.Context(), &out)
	if err != nil || n != int64(len(full)) || !bytes.Equal(out.Bytes(), full) {
		t.Fatalf("complete archive: n %d err %v, want %d bytes copied unchanged", n, err, len(full))
	}

	serve = full[:len(full)-12]
	out.Reset()
	if _, err := c.Backup(t.Context(), &out); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("truncated archive: %v, want an incomplete backup error", err)
	}
}
