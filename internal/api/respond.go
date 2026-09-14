package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/gamemaker1/markspace/internal/core"
)

// maxBodyBytes is the largest JSON request body Decode accepts.
const maxBodyBytes = 2 << 20

// listEnvelope wraps a collection so later versions can add sibling fields
// without breaking clients.
type listEnvelope struct {
	Items any  `json:"items"`
	Total *int `json:"total,omitempty"`
}

// ErrorBody is the error body described in docs/routes.md.
type ErrorBody struct {
	Error string `json:"error"`
	Field string `json:"field,omitempty"`
}

// WriteJSON encodes v as JSON with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// WriteItems writes a collection as {"items":[...]} with status 200. A nil
// slice encodes as an empty array.
func WriteItems[T any](w http.ResponseWriter, items []T) {
	if items == nil {
		items = []T{}
	}
	WriteJSON(w, http.StatusOK, listEnvelope{Items: items})
}

// WriteItemsTotal writes a collection with the backend row count attached.
func WriteItemsTotal[T any](w http.ResponseWriter, items []T, total int) {
	if items == nil {
		items = []T{}
	}
	WriteJSON(w, http.StatusOK, listEnvelope{Items: items, Total: &total})
}

// WriteNoContent answers 204 with an empty body.
func WriteNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// StatusFor maps a core sentinel error to an HTTP status code. It returns 0
// for an error that carries no mapping.
func StatusFor(err error) int {
	switch {
	case errors.Is(err, core.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, core.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, core.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, core.ErrConflict), errors.Is(err, core.ErrExists):
		return http.StatusConflict
	case errors.Is(err, core.ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, core.ErrUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, core.ErrUnsupported):
		return http.StatusUnsupportedMediaType
	case errors.Is(err, core.ErrRateLimited), errors.Is(err, core.ErrQuotaExceeded):
		return http.StatusTooManyRequests
	case errors.Is(err, core.ErrInvalid):
		return http.StatusBadRequest
	}
	return 0
}

// WriteError maps err to a status code and writes the error body. An error
// with no mapping is logged with its route and answered with a generic 500, so
// no internal message reaches the client. A nil logger uses slog.Default.
func WriteError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	if err == nil {
		return
	}
	status := StatusFor(err)
	if status == 0 {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Error("api handler failed",
			"method", r.Method,
			"path", r.URL.Path,
			"pattern", r.Pattern,
			"err", err)
		WriteJSON(w, http.StatusInternalServerError, ErrorBody{Error: "internal error"})
		return
	}
	body := ErrorBody{Error: err.Error()}
	var ve *core.ValidationError
	if errors.As(err, &ve) {
		body.Field = ve.Field
		body.Error = ve.Message
		if body.Error == "" {
			body.Error = ve.Error()
		}
	}
	WriteJSON(w, status, body)
}

// Decode reads a JSON request body into v. It caps the body at 2 MiB, rejects
// unknown fields, and reports a malformed body as a validation error on
// "body".
func Decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var ve *core.ValidationError
		if errors.As(err, &ve) {
			return ve
		}
		return core.Invalid("body", err.Error())
	}
	return nil
}
