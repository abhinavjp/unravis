package blackbox

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TimeHeader carries the recorder's clock on every answer. A reader compares it
// with its own clock for the "Clock differs" check (more than 1 s).
const TimeHeader = "X-Blackbox-Time"

// Limits on what remote readers can ask of the recorder.
const (
	maxConcurrent = 8
	failWindow    = time.Minute
	maxFailures   = 5
)

// StatusDoc is the answer of /status. Every answer carries Time.
type StatusDoc struct {
	Format          int        `json:"format"`
	Time            time.Time  `json:"time"`
	Name            string     `json:"name"`
	Version         string     `json:"version"`
	Host            string     `json:"host"`
	State           string     `json:"state"`
	Run             string     `json:"run"`
	IntervalSeconds int        `json:"intervalSeconds"`
	Started         time.Time  `json:"started"`
	FirstSample     *time.Time `json:"firstSample,omitempty"`
	LastSample      *time.Time `json:"lastSample,omitempty"`
	Samples         int        `json:"samples"`
	Files           int        `json:"files"`
	Bytes           int64      `json:"bytes"`
	Gaps            int        `json:"gaps"`
	FreePercent     *float64   `json:"freePercent,omitempty"`
	MinFreePercent  int        `json:"minFreePercent"`
	KeepDays        int        `json:"keepDays"`
	MaxGB           float64    `json:"maxGB"`
	ManifestOK      bool       `json:"manifestOK"`
	WriteOK         bool       `json:"writeOK"`
	Probe           Probe      `json:"probe"`
}

// ServerOptions wire the status server.
type ServerOptions struct {
	Store    *Store
	Recorder *Recorder
	Config   Config
	Keys     *KeyCheck
	Now      func() time.Time
}

// Server is the status HTTP server. Reading is allowed only with the read key;
// the one page without a key is "/", and only from this PC.
type Server struct {
	o   ServerOptions
	now func() time.Time
	srv *http.Server
	sem chan struct{}

	mu    sync.Mutex
	fails map[string][]time.Time
}

// NewServer builds the server. It does not listen.
func NewServer(o ServerOptions) *Server {
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &Server{o: o, now: o.Now, sem: make(chan struct{}, maxConcurrent), fails: map[string][]time.Time{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/status", s.guard(s.handleStatus))
	mux.HandleFunc("/manifest", s.guard(s.handleManifest))
	mux.HandleFunc("/files", s.guard(s.handleFiles))
	mux.HandleFunc("/files/", s.guard(s.handleFile))
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	return s
}

// Handler is the HTTP handler, for tests.
func (s *Server) Handler() http.Handler { return s.srv.Handler }

// Listen opens the port. It is separate from Serve so that a busy port is an
// error the caller sees at start, not a message lost inside a goroutine.
func Listen(bind string, port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
}

// Serve answers on ln until Shutdown.
func (s *Server) Serve(ln net.Listener) error {
	err := s.srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown stops the server, waiting for open requests until ctx ends.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func isLoopback(r *http.Request) bool {
	ip := net.ParseIP(clientIP(r))
	return ip != nil && ip.IsLoopback()
}

func (s *Server) stamp(w http.ResponseWriter) time.Time {
	t := s.now().UTC()
	w.Header().Set(TimeHeader, t.Format(time.RFC3339Nano))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	return t
}

func (s *Server) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) errorJSON(w http.ResponseWriter, code int, msg string) {
	s.writeJSON(w, code, map[string]any{"format": FormatVersion, "time": s.now().UTC(), "error": msg})
}

// throttled says whether ip has failed too often in the last minute.
func (s *Server) throttled(ip string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.fails[ip][:0]
	for _, t := range s.fails[ip] {
		if now.Sub(t) < failWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(s.fails, ip)
	} else {
		s.fails[ip] = kept
	}
	return len(kept) >= maxFailures
}

func (s *Server) fail(ip string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fails) > 1024 { // bound the table against a spray of addresses
		s.fails = map[string][]time.Time{}
	}
	s.fails[ip] = append(s.fails[ip], now)
}

// guard wraps a handler with method, key, throttle and concurrency checks.
func (s *Server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := s.stamp(w)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			s.errorJSON(w, http.StatusMethodNotAllowed, "read only: GET and HEAD only")
			return
		}
		ip := clientIP(r)
		if s.throttled(ip, now) {
			w.Header().Set("Retry-After", "60")
			s.errorJSON(w, http.StatusTooManyRequests, "too many wrong keys from this address; wait a minute")
			return
		}
		if s.o.Keys == nil || !s.o.Keys.Check(r.Header.Get(KeyHeader)) {
			s.fail(ip, now)
			s.errorJSON(w, http.StatusUnauthorized, "the read key is missing or wrong")
			return
		}
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		default:
			w.Header().Set("Retry-After", "5")
			s.errorJSON(w, http.StatusServiceUnavailable, "busy: too many readers at once")
			return
		}
		h(w, r)
	}
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	s.stamp(w)
	if r.URL.Path != "/" || !isLoopback(r) {
		http.NotFound(w, r)
		return
	}
	st := s.o.Recorder.Status(s.now())
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Unravis Blackbox " + s.o.Config.Name + ": " + st.State + "\n" +
		"Read the status with the read key in the " + KeyHeader + " header: /status, /manifest, /files, /files/<name>.\n"))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	now := s.now().UTC()
	m := s.o.Store.Manifest()
	st := s.o.Recorder.Status(now)
	doc := StatusDoc{
		Format: FormatVersion, Time: now, Name: s.o.Config.Name, Version: m.Version, Host: m.Host,
		State: st.State, Run: st.RunID, IntervalSeconds: s.o.Config.IntervalSeconds, Started: st.Started,
		FirstSample: m.FirstSample, LastSample: m.LastSample,
		Samples: m.SampleCount(), Files: len(m.Files), Bytes: m.TotalBytes(), Gaps: len(m.Gaps),
		MinFreePercent: s.o.Config.MinFreePercent, KeepDays: s.o.Config.KeepDays, MaxGB: s.o.Config.MaxGB,
		ManifestOK: s.o.Store.ManifestWritable(), WriteOK: st.WriteOK, Probe: Probe{Enabled: false},
	}
	if st.FreeKnown {
		p := st.FreePercent
		doc.FreePercent = &p
	}
	s.writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]any{"format": FormatVersion, "time": s.now().UTC(), "manifest": s.o.Store.Manifest()})
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	m := s.o.Store.Manifest()
	s.writeJSON(w, http.StatusOK, map[string]any{"format": FormatVersion, "time": s.now().UTC(), "files": m.Files, "current": s.o.Store.CurrentFile()})
}

// handleFile serves one recording file. Only names that are recording files are
// served; a path, a snapshot or the manifest is never reachable this way.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/files/")
	if !ValidFileName(name) || name != filepath.Base(name) {
		s.errorJSON(w, http.StatusNotFound, "no such recording file")
		return
	}
	f, err := os.Open(filepath.Join(s.o.Store.Dir(), name))
	if err != nil {
		s.errorJSON(w, http.StatusNotFound, "no such recording file")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.errorJSON(w, http.StatusInternalServerError, "cannot read the file")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	// ServeContent answers ranges, so a reader can resume or tail the open file.
	http.ServeContent(w, r, name, st.ModTime(), f)
}
