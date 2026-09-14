package core

import "errors"

// Sentinel errors. Handlers map these to HTTP status codes.
var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrUnauthorized = errors.New("unauthorized")
	ErrConflict     = errors.New("conflict")
	ErrExists       = errors.New("already exists")
	ErrInvalid      = errors.New("invalid")
	ErrTooLarge     = errors.New("too large")
	ErrUnsupported  = errors.New("unsupported")
	ErrRateLimited  = errors.New("rate limited")
	// ErrUnavailable reports a feature that is switched off or not configured
	// on this instance, such as AI with no provider.
	ErrUnavailable = errors.New("unavailable")
	// ErrQuotaExceeded reports that a monthly AI budget is used up.
	ErrQuotaExceeded = errors.New("quota exceeded")
)

// ValidationError names the field that failed and why.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

func (e *ValidationError) Unwrap() error { return ErrInvalid }

// Invalid builds a ValidationError.
func Invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}
