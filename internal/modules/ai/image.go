package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

const (
	// maxImageBodyBytes caps the body of an image request, which can carry a
	// base64 mask of up to 32 MiB.
	maxImageBodyBytes = 48 << 20
	// imagePingInterval keeps a long image stream open through proxies.
	imagePingInterval = 15 * time.Second
)

// imageDoneFrame ends an image stream.
type imageDoneFrame struct {
	Done bool `json:"done"`
	*core.AIImageGenerateResult
}

// streamAIImages serves POST /api/ai/image/stream. The stream opens with the
// first progress frame, so a request the backend refuses before any work gets
// a plain JSON error. It sends progress events of {done, total,
// queuePosition?}, comment pings while a job runs, and ends with a done event
// of {done, files, images, usage, provider, model, route, attempts} or an
// error event.
func (m *Module) streamAIImages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.StreamContext(r)
	defer cancel()
	var req core.AIImageGenerateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImageBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			m.Fail(w, r, fmt.Errorf("the request is larger than 48 MiB: %w", core.ErrTooLarge))
			return
		}
		m.Fail(w, r, core.Invalid("body", err.Error()))
		return
	}
	// Only the plugin host may attribute a call to a plugin.
	req.PluginID = ""

	var (
		mu       sync.Mutex
		stream   *api.SSE
		finished bool
		stop     = make(chan struct{})
	)
	// open starts the stream and its pings. The caller holds mu.
	open := func() {
		if stream != nil {
			return
		}
		stream = api.NewSSE(w)
		go func() {
			t := time.NewTicker(imagePingInterval)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ctx.Done():
					return
				case <-t.C:
					mu.Lock()
					if !finished {
						_ = stream.Ping()
					}
					mu.Unlock()
				}
			}
		}()
	}
	progress := func(p core.AIImageProgress) {
		mu.Lock()
		defer mu.Unlock()
		if finished {
			return
		}
		open()
		if err := stream.Send("progress", p); err != nil {
			m.Logger.Debug("ai image stream write failed", "err", err)
		}
	}

	res, err := m.Backend.GenerateImages(ctx, api.ActorFrom(ctx), req, progress)
	mu.Lock()
	defer mu.Unlock()
	finished = true
	close(stop)
	if err != nil {
		if stream == nil {
			m.Fail(w, r, err)
			return
		}
		message := err.Error()
		if api.StatusFor(err) == 0 {
			m.Logger.Error("ai image stream failed", "err", err)
			message = "internal error"
		}
		_ = stream.Send("error", errorFrame{Error: message})
		return
	}
	open()
	_ = stream.Send("done", imageDoneFrame{Done: true, AIImageGenerateResult: res})
}
