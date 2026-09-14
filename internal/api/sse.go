package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SSEKeepalive is how often a long event stream sends a comment frame to keep
// proxies from closing it.
const SSEKeepalive = 25 * time.Second

// SSE writes server-sent events to one response.
type SSE struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// NewSSE sets the streaming headers, writes a 200 status and flushes, so the
// client sees a response before the first event.
func NewSSE(w http.ResponseWriter) *SSE {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := &SSE{w: w, rc: http.NewResponseController(w)}
	s.flush()
	return s
}

// Send writes one named event whose data is the JSON form of v.
func (s *SSE) Send(event string, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	s.flush()
	return nil
}

// Ping writes a comment frame that clients ignore.
func (s *SSE) Ping() error {
	if _, err := fmt.Fprint(s.w, ": ping\n\n"); err != nil {
		return err
	}
	s.flush()
	return nil
}

// flush pushes buffered bytes to the client and ignores a writer that cannot
// flush.
func (s *SSE) flush() {
	_ = s.rc.Flush()
}

// shutdown is closed when the HTTP server starts shutting down. Long-lived
// streams watch it, because http.Server.Shutdown waits for every active
// request and a stream otherwise ends only when its client disconnects.
var (
	shutdownMu sync.Mutex
	shutdown   = make(chan struct{})
)

// BeginShutdown ends every stream opened through StreamContext. The server
// registers it with http.Server.RegisterOnShutdown. Calling it again does
// nothing.
func BeginShutdown() {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()
	select {
	case <-shutdown:
	default:
		close(shutdown)
	}
}

// shuttingDown returns the current shutdown channel.
func shuttingDown() <-chan struct{} {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()
	return shutdown
}

// StreamContext returns a context that ends when the request ends or when the
// server starts shutting down. Stream handlers use it in place of r.Context,
// so a shutdown does not wait for open event streams. Ordinary requests keep
// r.Context and finish their work.
func StreamContext(r *http.Request) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	done := shuttingDown()
	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
