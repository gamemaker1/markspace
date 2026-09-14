package ai

import (
	"net/http"
	"strings"

	"github.com/gamemaker1/markspace/internal/api"
	"github.com/gamemaker1/markspace/internal/core"
)

// threadDoneFrame ends a conversation stream. It adds the routing task and the
// stored user and assistant turns to the done frame of /api/ai/stream.
type threadDoneFrame struct {
	doneFrame
	Route       string                `json:"route,omitempty"`
	UserMessage *core.AIThreadMessage `json:"userMessage,omitempty"`
	Message     *core.AIThreadMessage `json:"message,omitempty"`
}

// aiAppliedInput is the body of PUT
// /api/ai/threads/{id}/messages/{messageId}/applied.
type aiAppliedInput struct {
	Applied *bool `json:"applied"`
}

// listAIThreads serves GET /api/ai/threads?noteId=.
func (m *Module) listAIThreads(w http.ResponseWriter, r *http.Request) {
	out, err := m.Backend.ListAIThreads(r.Context(), api.ActorFrom(r.Context()), strings.TrimSpace(r.URL.Query().Get("noteId")))
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteItems(w, out)
}

// createAIThread serves POST /api/ai/threads.
func (m *Module) createAIThread(w http.ResponseWriter, r *http.Request) {
	var in core.AIThreadInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	out, err := m.Backend.CreateAIThread(r.Context(), api.ActorFrom(r.Context()), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, out)
}

// streamAIThreadMessage serves POST /api/ai/threads/{id}/messages as a
// text/event-stream of delta events closed by a done event, or by an error
// event when the provider fails. An error before the answer starts is a plain
// JSON error.
func (m *Module) streamAIThreadMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.StreamContext(r)
	defer cancel()
	var in core.AIThreadMessageInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	ch, err := m.Backend.StreamAIThreadMessage(ctx, api.ActorFrom(ctx), r.PathValue("id"), in)
	if err != nil {
		m.Fail(w, r, err)
		return
	}
	stream := api.NewSSE(w)
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-ch:
			if !ok {
				return
			}
			switch {
			case chunk.Err != nil:
				_ = stream.Send("error", errorFrame{Error: chunk.Err.Error()})
				return
			case chunk.Done:
				_ = stream.Send("done", threadDoneFrame{
					doneFrame: doneFrame{
						Done: true, Model: chunk.Model, Provider: chunk.Provider, Attempts: chunk.Attempts,
						PromptTokens: chunk.PromptTokens, CompletionTokens: chunk.CompletionTokens,
						Sources: chunk.Sources, Budget: chunk.Budget,
					},
					Route: chunk.Route, UserMessage: chunk.UserMessage, Message: chunk.Message,
				})
				return
			case chunk.Delta != "":
				if err := stream.Send("delta", deltaFrame{Delta: chunk.Delta}); err != nil {
					m.Logger.Debug("ai thread stream write failed", "err", err)
					return
				}
			}
		}
	}
}

// setAIMessageApplied serves PUT
// /api/ai/threads/{id}/messages/{messageId}/applied with {"applied": bool}.
func (m *Module) setAIMessageApplied(w http.ResponseWriter, r *http.Request) {
	var in aiAppliedInput
	if err := api.Decode(r, &in); err != nil {
		m.Fail(w, r, err)
		return
	}
	if in.Applied == nil {
		m.Fail(w, r, core.Invalid("applied", "applied is required"))
		return
	}
	if err := m.Backend.SetAIMessageApplied(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id"), r.PathValue("messageId"), *in.Applied); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}

// deleteAIThread serves DELETE /api/ai/threads/{id}.
func (m *Module) deleteAIThread(w http.ResponseWriter, r *http.Request) {
	if err := m.Backend.DeleteAIThread(r.Context(), api.ActorFrom(r.Context()), r.PathValue("id")); err != nil {
		m.Fail(w, r, err)
		return
	}
	api.WriteNoContent(w)
}
