package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"api-audit-explorer/internal/applog"
	"api-audit-explorer/internal/sessionproof"
)

// Journey and error logging (see internal/applog). One middleware wraps the
// whole handler: it gives every request a short id (returned as X-Request-Id
// so the UI can quote it), logs one line per request with the ROUTE PATTERN
// (never the raw path or query, which can hold ids), recovers panics into a
// plain 500, and logs every 4xx/5xx with an error_class slug and a sanitized
// message. Named business events go through handler.event. Nothing here ever
// logs a request or response body, audit payloads or row data; error lines
// read only the small {"error","code"} body of an error reply, sanitized.

// WithLogger sets the logger the server writes journey and error lines to.
// Main wires the log file's logger through here; without one the server is
// quiet, so tests and embedders see no log output unless they ask for it.
func WithLogger(logger *slog.Logger) Option {
	return func(h *handler) { h.logger = logger }
}

// discardLogger drops every line; it is the unset default of handler.log.
var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// WithLogDir tells GET /api/logs/info where the log file lives.
func WithLogDir(dir string) Option {
	return func(h *handler) { h.logDir = dir }
}

func (h *handler) log() *slog.Logger {
	if h.logger != nil {
		return h.logger
	}
	return discardLogger
}

type logCtxKey struct{}

type requestLogState struct {
	id    string
	route string
}

func requestID(r *http.Request) string {
	if st, ok := r.Context().Value(logCtxKey{}).(*requestLogState); ok && st != nil {
		return st.id
	}
	return ""
}

// event logs one named business event at info, with the request's id when the
// call has one. Callers pass only ids, kinds, counts and durations — never
// names, keys, connection details or row data.
func (h *handler) event(r *http.Request, name string, attrs ...any) {
	if r != nil {
		if id := requestID(r); id != "" {
			attrs = append([]any{"req_id", id}, attrs...)
		}
	}
	h.log().Info(name, attrs...)
}

// eventWarn is event at warn, for an action that was tried and failed.
func (h *handler) eventWarn(r *http.Request, name string, attrs ...any) {
	if r != nil {
		if id := requestID(r); id != "" {
			attrs = append([]any{"req_id", id}, attrs...)
		}
	}
	h.log().Warn(name, attrs...)
}

// logEvent is event for work with no request in hand (background jobs).
func (h *handler) logEvent(name string, attrs ...any) {
	h.log().Info(name, attrs...)
}

func newRequestID() string {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(raw[:])
}

// routePatternFor names the request's route without any of its ids: the
// route table's pattern, or a fixed word for assets and unmatched API paths.
func routePatternFor(r *http.Request) string {
	if rt, _, ok := lookup(r.URL.Path); ok {
		return rt.pattern
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return "/api/{unknown}"
	}
	return "static"
}

// quietRoute reports whether a successful request line belongs at debug:
// static assets and the endpoints the page polls while a job runs.
func quietRoute(method, pattern string) bool {
	if pattern == "static" || pattern == sessionproof.Path {
		return true
	}
	if method != http.MethodGet {
		return false
	}
	switch pattern {
	case "/api/cases/{id}/imports/jobs/{job}",
		"/api/discover/scans/{scan}",
		"/api/cases/{id}/archive",
		"/api/cases/{id}/verify",
		"/api/cases/{id}/sampler",
		"/api/keeper/status":
		return true
	}
	return false
}

// statusRecorder counts the reply and keeps the first bytes of an error body
// so the error line can name what failed; success bodies are never read back.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	size        int
	wroteHeader bool
	errBody     bytes.Buffer
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if r.status >= 400 && r.errBody.Len() < 4096 {
		keep := 4096 - r.errBody.Len()
		if len(p) < keep {
			keep = len(p)
		}
		r.errBody.Write(p[:keep])
	}
	n, err := r.ResponseWriter.Write(p)
	r.size += n
	return n, err
}

// Flush passes through so a streaming handler keeps working behind the recorder.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// errorClass is the slug an error line carries, chosen from the reply's own
// error code when it has one and the status otherwise.
func errorClass(status int, code string) string {
	switch code {
	case "capacity_timeout", "query_timeout":
		return "timeout"
	case "connection_failed", "source_unavailable":
		return "source_unreachable"
	case "authentication_failed", "permission_denied":
		return "source_auth"
	case "query_failed", "query_cancelled", "query_memory_limit":
		return "query_failed"
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "auth"
	case status == 423: // Locked: the master-key gate's answer.
		return "locked"
	case status == http.StatusGatewayTimeout:
		return "timeout"
	case status == http.StatusBadGateway:
		return "source_unreachable"
	case status == http.StatusRequestEntityTooLarge:
		return "too_many_rows"
	case status == http.StatusInsufficientStorage:
		return "disk"
	case status >= 500:
		return "internal"
	default:
		return "validation"
	}
}

// uaFamily is the only header-derived value the log keeps: the browser family
// from User-Agent, never the full string.
func uaFamily(ua string) string {
	switch {
	case strings.Contains(ua, "Edg/"):
		return "edge"
	case strings.Contains(ua, "Firefox/"):
		return "firefox"
	case strings.Contains(ua, "Chrome/"):
		return "chrome"
	case strings.Contains(ua, "Safari/"):
		return "safari"
	case ua == "":
		return ""
	default:
		return "other"
	}
}

// logRequest writes the one journey line for a finished request. Errors are
// the same line at warn/error with the error class and sanitized message, so a
// failure can be followed from the UI's quoted request id.
func (h *handler) logRequest(r *http.Request, rec *statusRecorder, route, id string, start time.Time) {
	attrs := []any{
		"req_id", id,
		"method", r.Method,
		"route", route,
		"status", rec.status,
		"duration_ms", time.Since(start).Milliseconds(),
		"size", rec.size,
	}
	if fam := uaFamily(r.UserAgent()); fam != "" {
		attrs = append(attrs, "ua", fam)
	}
	if rec.status >= 400 {
		var body struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.Unmarshal(rec.errBody.Bytes(), &body)
		attrs = append(attrs,
			"error_class", errorClass(rec.status, body.Code),
			"error", applog.Sanitize(body.Error),
		)
		if body.Code != "" {
			attrs = append(attrs, "code", body.Code)
		}
		level := slog.LevelWarn
		if rec.status >= 500 {
			level = slog.LevelError
		}
		h.log().Log(r.Context(), level, "request", attrs...)
		return
	}
	if quietRoute(r.Method, route) {
		h.log().Debug("request", attrs...)
		return
	}
	h.log().Info("request", attrs...)
}

// serveHTTP wraps the whole handler in the logging middleware: request id,
// panic recovery, and the one-line-per-request journey log.
func (h *handler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	id := newRequestID()
	route := routePatternFor(r)
	r = r.WithContext(context.WithValue(r.Context(), logCtxKey{}, &requestLogState{id: id, route: route}))
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	rec.Header().Set("X-Request-Id", id)
	defer func() {
		if p := recover(); p != nil {
			h.log().Error("panic recovered",
				"req_id", id, "route", route, "error_class", "internal",
				"error", applog.Sanitize(fmt.Sprintf("%v", p)),
				"stack", applog.Sanitize(string(debug.Stack())))
			if !rec.wroteHeader {
				writeError(rec, http.StatusInternalServerError, "Internal server error.")
			}
		}
		h.logRequest(r, rec, route, id, start)
	}()
	h.serve(rec, r)
}
