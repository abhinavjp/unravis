package blackbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	maxTrackedIPs = 1024 // addresses kept in the failure table
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
	SQL             SQLStatus  `json:"sql"`
}

// SQLStatus is the only SQL information /status carries (review C11): the four
// fields the SQL runner writes to <folder>\sql\status.json. No instance name,
// server version or error text ever crosses the plain-HTTP endpoint, and no SQL
// sample data is served at all (C12).
type SQLStatus struct {
	Enabled  bool       `json:"enabled"`
	State    string     `json:"state"`
	LastOK   *time.Time `json:"lastOK,omitempty"`
	Failures int        `json:"failures"`
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
	// WriteTimeout bounds how long a stalled client can hold a recording file
	// open (an open file cannot be removed by housekeeping on Windows). Reads
	// answer ranges, so a reader that is cut off resumes where it stopped.
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 30 * time.Second}
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
	if _, known := s.fails[ip]; !known && len(s.fails) >= maxTrackedIPs {
		s.evictLocked(now)
	}
	s.fails[ip] = append(s.fails[ip], now)
}

// evictLocked makes room in the failure table. Addresses whose failures are all
// outside the window go first. If the table is still full, the address with the
// fewest recent failures goes (the oldest of those on a tie), so a spray of
// one-off addresses pushes out other one-off addresses and never an address that
// is locked out.
func (s *Server) evictLocked(now time.Time) {
	for ip, ts := range s.fails {
		if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= failWindow {
			delete(s.fails, ip)
		}
	}
	if len(s.fails) < maxTrackedIPs {
		return
	}
	victim, n, last := "", 0, time.Time{}
	for ip, ts := range s.fails {
		l := ts[len(ts)-1]
		if victim == "" || len(ts) < n || (len(ts) == n && l.Before(last)) {
			victim, n, last = ip, len(ts), l
		}
	}
	delete(s.fails, victim)
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

// sqlStatusMaxAge is the documented freshness rule (W8-04): the SQL runner
// rewrites status.json on EVERY tick, and the longest tick interval is the C7
// back-off cap of 15 minutes. 20 minutes = that cap plus a 5-minute margin, so
// a file older than this (or implausibly far in the future) means the SQL
// service is no longer updating it — stopped, removed or crashed — and /status
// reports off instead of trusting an old enabled:true record forever. The
// file's modification time is the heartbeat; it is private metadata, and the
// public field set stays exactly the four C11 fields.
const sqlStatusMaxAge = 20 * time.Minute

// sqlStatusMaxBytes bounds the status file BEFORE any allocation (W8-04).
const sqlStatusMaxBytes = 4096

// readSQLStatus reads the SQL runner's status file best-effort: one small file
// read per /status, on demand, no goroutine. Missing, unreadable, non-regular,
// oversized, stale, malformed or inconsistent means the WHOLE off object, never
// an error answer and never a partial record (W8-04). The size is bounded
// BEFORE any allocation: Stat refuses an oversized file up front and the read
// itself is capped at sqlStatusMaxBytes+1, so a corrupt/oversized status file
// can never make /status allocate the whole file (W12-04). The document is
// validated in full: required fields present, no unknown fields, exactly one
// JSON value (trailing data or delimiters refused), nonnegative failures, a
// whitelisted state, enabled agreeing with state, and a non-zero lastOK when
// present. No instance name, version or error text can leak through the file.
func readSQLStatus(folder string, now time.Time) SQLStatus {
	off := SQLStatus{State: "off"}
	path := filepath.Join(SQLDataDir(folder), "status.json")
	f, err := os.Open(path)
	if err != nil {
		return off
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > sqlStatusMaxBytes {
		return off
	}
	if age := now.Sub(fi.ModTime()); age > sqlStatusMaxAge || age < -sqlStatusMaxAge {
		return off
	}
	data, err := io.ReadAll(io.LimitReader(f, sqlStatusMaxBytes+1))
	if err != nil || len(data) > sqlStatusMaxBytes {
		return off
	}
	// Pointer fields make a MISSING required field detectable; json.Unmarshal
	// into the value struct would silently default it.
	var p struct {
		Enabled  *bool      `json:"enabled"`
		State    *string    `json:"state"`
		LastOK   *time.Time `json:"lastOK"`
		Failures *int       `json:"failures"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return off
	}
	// A second decode must hit EOF: dec.More() alone accepts a trailing
	// delimiter after a complete object.
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return off
	}
	if p.Enabled == nil || p.State == nil || p.Failures == nil {
		return off
	}
	st := SQLStatus{Enabled: *p.Enabled, State: *p.State, LastOK: p.LastOK, Failures: *p.Failures}
	if st.Failures < 0 {
		return off
	}
	switch st.State {
	case "on", "backing-off", "auto-off", "denied", "off":
	default:
		return off
	}
	// enabled must agree with state: only on/backing-off are enabled; a
	// latched or off record can never advertise enabled:true.
	if st.Enabled != (st.State == "on" || st.State == "backing-off") {
		return off
	}
	if st.LastOK != nil && st.LastOK.IsZero() {
		return off
	}
	return st
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
		SQL: readSQLStatus(s.o.Config.Folder, now),
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

// errRecordingRefused is the fixed refusal for a recording entry that fails
// containment: a link/junction/reparse alias, a non-regular object, or a root
// that is itself a link. It never carries a path or provider text (handoff §3).
var errRecordingRefused = errors.New("recording entry refused")

// openRecordingFile opens one recording entry inside dir under a handle-based,
// race-resistant containment/no-follow policy (W12-05):
//
//   - The root must be a real directory, never a symlink/junction: os.OpenRoot
//     follows links in the root name, so a reparse root is refused before it is
//     pinned (Lstat, not Stat — no following).
//   - The root is then pinned as a directory HANDLE (os.OpenRoot). Substituting
//     an ancestor of dir after this point cannot redirect the open: the handle
//     keeps referencing the original directory.
//   - The entry is opened RELATIVE to that handle (Root.Open). Every path
//     component is validated against the root: on Windows the open uses
//     OBJ_DONT_REPARSE (with a handle-inspecting fallback on older builds), so
//     any symlink/junction/mount-point alias — final object or ancestor — is
//     refused outright; on Unix no component may escape the root. A link
//     substituted for the entry between validation and open cannot win: the
//     no-follow check happens inside the same handle-relative open.
//   - The FINAL object is checked on the opened handle (never the path): it
//     must be a regular file, so directories, devices and pipes are refused
//     even when they carry a valid recording basename.
//
// The caller still owns basename/shape validation and the read-key guard; this
// only adds filesystem containment underneath them.
func openRecordingFile(dir, name string) (*os.File, error) {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, errRecordingRefused
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, errRecordingRefused
	}
	return f, nil
}

// handleFile serves one recording file. Only names that are recording files are
// served; a path, a snapshot or the manifest is never reachable this way. The
// open is handle-based and no-follow (W12-05): a valid basename that is a
// filesystem link, or sits under a substituted ancestor, is refused instead of
// being followed out of the recordings folder.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/files/")
	if !ValidFileName(name) || name != filepath.Base(name) {
		s.errorJSON(w, http.StatusNotFound, "no such recording file")
		return
	}
	f, err := openRecordingFile(s.o.Store.Dir(), name)
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
