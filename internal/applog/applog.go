// Package applog is Unravis's own journey and error log: one rotating text
// file the owner can send to a developer when something goes wrong. It records
// what the user did (screens, actions, named events), what the server answered
// and where a read failed, in structured log/slog text lines
// (time level msg key=value).
//
// The file is written for handing over, so nothing secret is ever written to
// it: every string passes through Sanitize (see sanitize.go), request and
// response bodies are never logged, and audit payloads and row data never
// reach it. The defaults put the file at %LOCALAPPDATA%\Unravis\logs\unravis.log.
package applog

import (
	"context"
	"encoding"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"api-audit-explorer/internal/datadir"
)

// EnvLogLevel chooses the log level: UNRAVIS_LOG_LEVEL=debug turns on the
// request lines for static assets and polling endpoints.
const EnvLogLevel = "UNRAVIS_LOG_LEVEL"

const (
	// FileName is the current log file; rotated files add .1, .2, ...
	FileName = "unravis.log"
	// MaxSize is the size one log file may reach before it rotates (5 MB).
	MaxSize = 5 << 20
	// MaxFiles is how many log files are kept in total, current included.
	MaxFiles = 5
)

// ParseLevel reads a level name; anything unrecognised is info.
func ParseLevel(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LevelFromEnv is the level UNRAVIS_LOG_LEVEL asks for (info by default).
func LevelFromEnv(getenv func(string) string) slog.Level {
	if getenv == nil {
		getenv = os.Getenv
	}
	return ParseLevel(getenv(EnvLogLevel))
}

// DefaultDir is the logs folder: %LOCALAPPDATA%\Unravis\logs on Windows. The
// data-root overrides the app already honours win first, so a test or the
// screenshot harness never touches the real folder: UNRAVIS_DATA_DIR (the
// shared root) and AUDIT_CONFIG_DIR (the per-user config folder) each get a
// logs folder inside them.
func DefaultDir(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if dir := getenv(datadir.EnvConfigDir); dir != "" {
		return filepath.Join(dir, "logs")
	}
	if root := getenv(datadir.EnvDataDir); root != "" {
		return filepath.Join(root, "logs")
	}
	cache, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "Unravis", "logs")
}

// File is an open log file plus the logger that writes to it.
type File struct {
	Logger *slog.Logger
	Dir    string
	Path   string
	writer *rotateWriter
}

// Writer is the log file as a plain writer, so the caller can also send the
// standard library's log lines (the ones Unravis already prints) to the same
// file. Every write is sanitized first, exactly like the slog path, so a
// registered secret or credential in a log.Printf line never reaches the file.
// After Close, writes fail instead of reopening the file.
func (f *File) Writer() io.Writer { return sanitizeWriter{w: f.writer} }

// sanitizeWriter runs each write through Sanitize before passing it on. The
// standard library logger issues one write per line, so a secret is never
// split across two writes. It reports the caller's byte count, not the length
// of the sanitized text.
type sanitizeWriter struct {
	w io.Writer
}

func (s sanitizeWriter) Write(p []byte) (int, error) {
	if _, err := s.w.Write([]byte(Sanitize(string(p)))); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (f *File) Close() error {
	if f == nil || f.writer == nil {
		return nil
	}
	return f.writer.Close()
}

// Open creates dir (restricted to this user) and the log file inside it, and
// returns a logger writing slog text lines to the rotating file AND to the
// console (stderr), so the existing console output stays. Every string value
// is sanitized on the way out. dir is a parameter so tests inject a temp dir.
func Open(dir string, level slog.Level, secure func(string) error) (*File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	rw, err := newRotateWriter(dir, FileName, MaxSize, MaxFiles, secure)
	if err != nil {
		return nil, err
	}
	out := Tee(os.Stderr, rw)
	handler := sanitizeHandler{inner: slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})}
	return &File{Logger: slog.New(handler), Dir: dir, Path: filepath.Join(dir, FileName), writer: rw}, nil
}

// Info describes the current log file for the Support logs section: where it
// is, how big it is, and when its newest entry was written (the file's
// modification time; the file is append-only, so that is the last line).
type Info struct {
	Path   string
	Dir    string
	Size   int64
	Newest time.Time
}

// Stat reads Info for the log in dir. A missing file reads as size 0.
func Stat(dir string) Info {
	info := Info{Dir: dir, Path: filepath.Join(dir, FileName)}
	if st, err := os.Stat(info.Path); err == nil {
		info.Size = st.Size()
		info.Newest = st.ModTime()
	}
	return info
}

// sanitizeHandler wraps a slog handler and runs every string it emits —
// message, attribute values, error values — through Sanitize, so a caller
// cannot leak a secret into the log by forgetting to clean a value.
type sanitizeHandler struct {
	inner slog.Handler
	// secretGroup is true once WithGroup opened a group named like a secret
	// (password, token, ...): every attribute below it is hidden.
	secretGroup bool
}

func (h sanitizeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h sanitizeHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, Sanitize(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(cleanAttr(a, h.secretGroup))
		return true
	})
	return h.inner.Handle(ctx, clean)
}

func (h sanitizeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = cleanAttr(a, h.secretGroup)
	}
	return sanitizeHandler{inner: h.inner.WithAttrs(clean), secretGroup: h.secretGroup}
}

func (h sanitizeHandler) WithGroup(name string) slog.Handler {
	return sanitizeHandler{inner: h.inner.WithGroup(name), secretGroup: h.secretGroup || isSecretKey(name)}
}

func sanitizeAttr(a slog.Attr) slog.Attr { return cleanAttr(a, false) }

// cleanAttr cleans one attribute. inSecret is true below a group whose name says
// it holds secrets: every value in it is hidden, whatever its key.
func cleanAttr(a slog.Attr, inSecret bool) slog.Attr {
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		inSecret = inSecret || isSecretKey(a.Key)
		attrs := a.Value.Group()
		clean := make([]slog.Attr, len(attrs))
		for i, g := range attrs {
			clean[i] = cleanAttr(g, inSecret)
		}
		return slog.Group(a.Key, attrsToAny(clean)...)
	}
	if inSecret || isSecretKey(a.Key) {
		// The key says the value is a secret, whatever its text looks like.
		if a.Value.Kind() == slog.KindString && a.Value.String() == "" {
			return a
		}
		return slog.String(a.Key, redacted)
	}
	if a.Value.Kind() != slog.KindAny {
		if a.Value.Kind() == slog.KindString {
			return slog.String(a.Key, Sanitize(a.Value.String()))
		}
		return a
	}
	switch v := a.Value.Any().(type) {
	case string:
		return slog.String(a.Key, Sanitize(v))
	case error:
		return slog.String(a.Key, Sanitize(safeText(func() string { return v.Error() })))
	case url.URL:
		return slog.String(a.Key, Sanitize(v.String()))
	case []byte:
		return slog.String(a.Key, Sanitize(string(v)))
	case http.Header:
		return slog.String(a.Key, Sanitize(fmt.Sprintf("%+v", hideSecretValues(v))))
	case map[string][]string:
		return slog.String(a.Key, Sanitize(fmt.Sprintf("%+v", hideSecretValues(v))))
	case map[string]string:
		clean := make(map[string]string, len(v))
		for k, val := range v {
			if isSecretKey(k) && val != "" {
				val = redacted
			}
			clean[k] = val
		}
		return slog.String(a.Key, Sanitize(fmt.Sprintf("%+v", clean)))
	case encoding.TextMarshaler:
		text, ok := "", false
		func() {
			// A nil pointer whose MarshalText has a value receiver panics; slog's own
			// handler prints "<nil>" for it, so do the same.
			defer func() { _ = recover() }()
			if b, err := v.MarshalText(); err == nil {
				text, ok = string(b), true
			}
		}()
		if ok {
			return slog.String(a.Key, Sanitize(text))
		}
		return slog.String(a.Key, Sanitize(safeText(func() string { return fmt.Sprintf("%+v", v) })))
	default:
		// The handler would format any other value itself, unsanitized (a
		// Stringer, a URL, a slice, a map, a struct). Render it the way the
		// text handler does and clean the result.
		return slog.String(a.Key, Sanitize(fmt.Sprintf("%+v", v)))
	}
}

// safeText runs f and returns "<nil>" if it panics (a nil pointer receiver).
func safeText(f func() string) (out string) {
	defer func() {
		if recover() != nil {
			out = "<nil>"
		}
	}()
	return f()
}

// hideSecretValues copies header-like data with the values of secret keys
// (Authorization, Cookie, X-Api-Key, ...) replaced.
func hideSecretValues(m map[string][]string) map[string][]string {
	clean := make(map[string][]string, len(m))
	for k, vals := range m {
		if isSecretKey(k) && len(vals) > 0 {
			vals = []string{redacted}
		}
		clean[k] = vals
	}
	return clean
}

func attrsToAny(attrs []slog.Attr) []any {
	out := make([]any, len(attrs))
	for i, a := range attrs {
		out[i] = a
	}
	return out
}

// secretKeySuffixes end the name of an attribute that holds a secret, once the
// name is lower-cased and its "_", "-", "." and spaces are dropped: db_password,
// MYSQL_PWD, clientSecret, access-token, X-Api-Key, Authorization, Set-Cookie,
// private_key, connection_string.
var secretKeySuffixes = []string{
	"password", "passwd", "pwd", "secret", "token", "apikey", "accesskey",
	"accountkey", "sharedaccesskey", "authorization", "cookie", "credential",
	"credentials", "privatekey", "passphrase", "dsn", "connectionstring", "pgpass",
}

// secretKeyFolder drops the separators from a key. It is built once: isSecretKey
// runs for every slog attribute.
var secretKeyFolder = strings.NewReplacer("_", "", "-", "", ".", "", " ", "")

// isSecretKey reports whether a slog attribute key names a secret. The check
// is by suffix, so "db_password" and "refresh_token" count and "tokens" or
// "passwordPolicy" do not. "pass" counts only after a separator (db_pass), and
// "cred" only as the whole key.
func isSecretKey(key string) bool {
	k := strings.ToLower(key)
	if k == "cred" {
		return true
	}
	if n := len(k); n > 4 && strings.HasSuffix(k, "pass") && (k[n-5] == '_' || k[n-5] == '-' || k[n-5] == '.') {
		return true
	}
	if strings.ContainsAny(k, "_-. ") {
		k = secretKeyFolder.Replace(k)
	}
	for _, suf := range secretKeySuffixes {
		if strings.HasSuffix(k, suf) {
			return true
		}
	}
	return false
}
