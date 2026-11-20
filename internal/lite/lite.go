// Package lite is Unravis Lite: a small read-only viewer for one Blackbox. It
// reuses the real Blackbox reader and series code of the full app, but the only
// requests it answers are the viewer's own (docs/auto-updater/SPEC.md, D6/D10).
// Everything else is a 404 before it reaches the full server, so a bug or a new
// route in the full app can never become reachable from Lite by accident.
package lite

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
)

// ID is the one entry Lite lists. It starts with "bb-" because the server only
// matches entry routes for ids of that shape.
const ID = "bb-lite"

var errReadOnly = errors.New("Unravis Lite only reads one Blackbox")

// Allowed reports whether Lite answers this request. The rule is an allowlist:
// the page and its files, the one-time launch link, the list of Blackboxes and
// the chart series of the one entry. Nothing else, whatever the method.
func Allowed(method, path string) bool {
	switch {
	case path == "/launch":
		return method == http.MethodGet
	case path == "/api/blackbox":
		return method == http.MethodGet
	case path == "/api/blackbox/"+ID+"/series":
		return method == http.MethodPost
	case path == "/api" || strings.HasPrefix(path, "/api/"):
		return false
	}
	return method == http.MethodGet || method == http.MethodHead
}

// Wrap answers only what Allowed lets through. Lite's page files carry fixed
// names (no hash), so they are marked no-cache: a new Lite on the same port
// must never run an old page from the browser's cache.
func Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Allowed(r.Method, r.URL.Path) {
			h := w.Header()
			h.Set("Cache-Control", "no-store")
			h.Set("Content-Security-Policy", "default-src 'none'")
			h.Set("X-Content-Type-Options", "nosniff")
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/launch" {
			w = &noCache{ResponseWriter: w}
		}
		next.ServeHTTP(w, r)
	})
}

type noCache struct{ http.ResponseWriter }

func (n *noCache) WriteHeader(code int) {
	n.Header().Set("Cache-Control", "no-cache")
	n.ResponseWriter.WriteHeader(code)
}

func (n *noCache) Write(b []byte) (int, error) {
	if n.Header().Get("Cache-Control") != "no-cache" {
		n.Header().Set("Cache-Control", "no-cache")
	}
	return n.ResponseWriter.Write(b)
}

// Store is a one-entry, in-memory Blackbox list. It never writes a file and holds no key.
type Store struct{ entry bbsaved.Entry }

// NewStore lists the Blackbox in folder under name.
func NewStore(name string, loc blackbox.Location, now time.Time) *Store {
	return &Store{entry: bbsaved.Entry{
		ID: ID, Name: name, Kind: loc.Kind, Location: loc.Display(),
		Canon: loc.Canonical(), AddedAt: now.UTC(),
	}}
}

func (s *Store) List() ([]bbsaved.Entry, error) { return []bbsaved.Entry{s.entry}, nil }

func (s *Store) Get(id string) (bbsaved.Entry, error) {
	if id != s.entry.ID {
		return bbsaved.Entry{}, bbsaved.ErrNotFound
	}
	return s.entry, nil
}

func (s *Store) Find(canon, _, _ string) (bbsaved.Entry, bool) {
	return s.entry, canon == s.entry.Canon
}

func (s *Store) Add(bbsaved.AddInput) (bbsaved.Entry, error) { return bbsaved.Entry{}, errReadOnly }
func (s *Store) SetThisPC(string, bool) error                { return errReadOnly }
func (s *Store) SetKey(string, string) error                 { return errReadOnly }
func (s *Store) SetRecorder(string, string, string) error    { return errReadOnly }
func (s *Store) Remove(string) error                         { return errReadOnly }
func (s *Store) Key(string) (string, error)                  { return "", nil }

// NoData stands in for the case database the full app has. Lite has none, and
// the allowlist keeps every route that would use it out of reach.
type NoData struct{}

func (NoData) Sources(context.Context) ([]audit.Source, error) { return nil, errReadOnly }
func (NoData) AddSource(context.Context, audit.SourceInput) (audit.Source, error) {
	return audit.Source{}, errReadOnly
}
func (NoData) DeleteSource(context.Context, string) error { return errReadOnly }
func (NoData) Analyze(context.Context, audit.Query) (audit.Analysis, error) {
	return audit.Analysis{}, errReadOnly
}
func (NoData) Requests(context.Context, audit.PageInput) (audit.Page, error) {
	return audit.Page{}, errReadOnly
}
func (NoData) Payload(context.Context, audit.PayloadInput) (audit.Payload, error) {
	return audit.Payload{}, errReadOnly
}
func (NoData) Record(context.Context, audit.RecordInput) (audit.RecordDetail, error) {
	return audit.RecordDetail{}, errReadOnly
}
func (NoData) Close(context.Context) error { return nil }
