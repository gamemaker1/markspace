package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

// resetShutdown gives each test a fresh shutdown channel.
func resetShutdown(t *testing.T) {
	t.Helper()
	shutdownMu.Lock()
	shutdown = make(chan struct{})
	shutdownMu.Unlock()
	t.Cleanup(func() {
		shutdownMu.Lock()
		shutdown = make(chan struct{})
		shutdownMu.Unlock()
	})
}

func TestStreamContextEndsAtShutdown(t *testing.T) {
	resetShutdown(t)
	r := httptest.NewRequest("GET", "/api/events", nil)
	ctx, cancel := StreamContext(r)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatal("stream context ended before shutdown")
	case <-time.After(20 * time.Millisecond):
	}
	BeginShutdown()
	BeginShutdown() // a second call does nothing
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stream context did not end at shutdown")
	}
}

func TestStreamContextEndsWithRequest(t *testing.T) {
	resetShutdown(t)
	r := httptest.NewRequest("GET", "/api/events", nil)
	ctx, cancel := StreamContext(r)
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stream context did not end with its cancel")
	}
}
