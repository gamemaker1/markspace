package api

import (
	"strconv"
	"strings"

	"github.com/gamemaker1/markspace/internal/core"
)

// RequireUser rejects an actor that is not a signed-in user. Handlers use it
// only for routes that are meaningless anonymously, to skip a backend call
// that could not succeed.
func RequireUser(actor *core.Actor) error {
	if !actor.IsUser() {
		return core.ErrUnauthorized
	}
	return nil
}

// RequireAdmin answers 401 for an anonymous actor and 403 for a user who is
// not an admin, before any backend call.
func RequireAdmin(actor *core.Actor) error {
	if !actor.IsUser() {
		return core.ErrUnauthorized
	}
	if !actor.IsAdmin() {
		return core.ErrForbidden
	}
	return nil
}

// IntParam reads a base-10 integer from a query value. A missing, negative or
// malformed value yields def, and a positive maximum clamps the result.
func IntParam(raw string, def, max int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return def
	}
	if n < 0 {
		return def
	}
	if max > 0 && n > max {
		return max
	}
	return n
}

// BoolParam reports whether a query value means true. Anything unrecognised
// counts as false.
func BoolParam(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
