// Package api serves the JSON REST interface and the shared HTTP middleware.
package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// SessionCookie is the name of the session cookie.
const SessionCookie = "markspace_session"

type actorKey struct{}

// Authenticator resolves the actor behind a request.
type Authenticator struct {
	Backend core.Backend
	Logger  *slog.Logger
	// Secure marks cookies secure and same-site strict.
	Secure bool
	// TrustProxy reads the client address from X-Forwarded-For.
	TrustProxy bool
}

// Resolve returns the actor for a request. It never returns nil. An anonymous
// visitor gets an actor with no user and no link.
func (a *Authenticator) Resolve(r *http.Request) *core.Actor {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); h != "" {
		if token, ok := bearer(h); ok {
			// Plugin and acting tokens authenticate only on the plugin host
			// API, where the manifest permission groups apply. Anywhere else
			// they count as no credential.
			if IsPluginToken(token) && !strings.HasPrefix(r.URL.Path, PluginAPIPrefix) {
				return &core.Actor{}
			}
			if actor, err := a.Backend.ActorFromAPIToken(ctx, token); err == nil {
				// A system token has no account, so it authenticates only
				// on the JSON API outside the plugin host API. Server pages
				// and the plugin host API see no credential.
				if actor.IsSystem() && (!strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, PluginAPIPrefix)) {
					return &core.Actor{}
				}
				return actor
			}
		}
	}
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		if actor, err := a.Backend.ActorFromSession(ctx, c.Value); err == nil {
			return actor
		}
	}
	return &core.Actor{}
}

// PluginAPIPrefix is the path prefix of the plugin host API.
const PluginAPIPrefix = "/api/plugin/v1/"

// IsPluginToken reports whether a bearer token is a plugin token or an acting
// token.
func IsPluginToken(token string) bool {
	return strings.HasPrefix(token, core.PluginTokenPrefix) || strings.HasPrefix(token, core.ActingTokenPrefix)
}

func bearer(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):]), true
	}
	return "", false
}

// Middleware attaches the resolved actor to the request context.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := a.Resolve(r)
		next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), actor)))
	})
}

// WithActor stores an actor in a context.
func WithActor(ctx context.Context, actor *core.Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the actor stored by Middleware. It never returns nil.
func ActorFrom(ctx context.Context) *core.Actor {
	if v, ok := ctx.Value(actorKey{}).(*core.Actor); ok && v != nil {
		return v
	}
	return &core.Actor{}
}

// SetSessionCookie writes the session cookie.
func (a *Authenticator) SetSessionCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.Secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
		MaxAge:   int(ttl.Seconds()),
	})
}

// ClearSessionCookie expires the session cookie.
func (a *Authenticator) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// ClientIP returns the request source address.
func (a *Authenticator) ClientIP(r *http.Request) string {
	if a.TrustProxy {
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			if i := strings.IndexByte(v, ','); i > 0 {
				return strings.TrimSpace(v[:i])
			}
			return strings.TrimSpace(v)
		}
		if v := r.Header.Get("X-Real-Ip"); v != "" {
			return strings.TrimSpace(v)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RequestOrigin returns the scheme and host a request arrived on, such as
// "https://notes.example.com". With trustProxy it honours X-Forwarded-Proto
// and X-Forwarded-Host, which a reverse proxy sets.
func RequestOrigin(r *http.Request, trustProxy bool) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if trustProxy {
		if p := firstHeaderValue(r.Header.Get("X-Forwarded-Proto")); p == "http" || p == "https" {
			scheme = p
		}
		if h := firstHeaderValue(r.Header.Get("X-Forwarded-Host")); h != "" {
			host = h
		}
	}
	return scheme + "://" + host
}

// firstHeaderValue returns the first comma-separated value of a header.
func firstHeaderValue(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// Origin returns the external root for links in a response: the configured
// base URL when one is set, else the origin the request arrived on.
func (a *Authenticator) Origin(r *http.Request, baseURL string) string {
	if baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}
	return RequestOrigin(r, a.TrustProxy)
}

// statusWriter records the response status for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap exposes the underlying writer to http.ResponseController, which
// server-sent event handlers use to flush.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Logging logs one line per request at debug level, and failures at warn.
func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			if sw.status == 0 {
				sw.status = http.StatusOK
			}
			level := slog.LevelDebug
			// 503 reports a feature switched off by configuration, such as AI
			// with no provider, so it is not a server fault.
			if sw.status >= 500 && sw.status != http.StatusServiceUnavailable {
				level = slog.LevelWarn
			}
			logger.Log(r.Context(), level, "request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"bytes", sw.bytes,
				"ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// Recover turns a panic into a 500 response and logs the stack.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					logger.Error("panic",
						"path", r.URL.Path,
						"value", v,
						"stack", string(debug.Stack()))
					if strings.HasPrefix(r.URL.Path, "/api/") {
						w.Header().Set("Content-Type", "application/json; charset=utf-8")
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = w.Write([]byte(`{"error":"internal error"}` + "\n"))
						return
					}
					http.Error(w, "Internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Chain applies middleware left to right, so the first argument is outermost.
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}
