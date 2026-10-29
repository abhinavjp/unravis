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
	"io"
	"log/slog"
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

// Writer is the rotating file writer on its own, so the caller can also send
// the standard library's log lines (the ones Unravis already prints) to the
// same file.
func (f *File) Writer() io.Writer { return f.writer }

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
	out := io.MultiWriter(os.Stderr, rw)
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
}

func (h sanitizeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h sanitizeHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, Sanitize(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(sanitizeAttr(a))
		return true
	})
	return h.inner.Handle(ctx, clean)
}

func (h sanitizeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = sanitizeAttr(a)
	}
	return sanitizeHandler{inner: h.inner.WithAttrs(clean)}
}

func (h sanitizeHandler) WithGroup(name string) slog.Handler {
	return sanitizeHandler{inner: h.inner.WithGroup(name)}
}

func sanitizeAttr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, Sanitize(a.Value.String()))
	case slog.KindGroup:
		attrs := a.Value.Group()
		clean := make([]slog.Attr, len(attrs))
		for i, g := range attrs {
			clean[i] = sanitizeAttr(g)
		}
		return slog.Group(a.Key, attrsToAny(clean)...)
	case slog.KindAny:
		switch v := a.Value.Any().(type) {
		case string:
			return slog.String(a.Key, Sanitize(v))
		case error:
			return slog.String(a.Key, Sanitize(v.Error()))
		}
	}
	return a
}

func attrsToAny(attrs []slog.Attr) []any {
	out := make([]any, len(attrs))
	for i, a := range attrs {
		out[i] = a
	}
	return out
}
