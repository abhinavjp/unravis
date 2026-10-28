package setup

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/keeper"
)

// The setup window is a small web page (web/setup) served on a loopback port. The address carries a one-time key in its fragment
// (#k=...), which the page sends back in the X-Setup-Key header on every API call; the fragment never reaches a server log or a
// Referer. Requests with another Host or Origin are refused, so a web page in the person's browser cannot drive setup.

// UIState is what the page needs to draw its first screen.
type UIState struct {
	Version string `json:"version"`
	// Mode is what this setup run is for: install, upgrade, repair or uninstall.
	Mode      Op            `json:"mode"`
	Installed *UIInstalled  `json:"installed,omitempty"`
	Defaults  UIDefaults    `json:"defaults"`
	IsAdmin   bool          `json:"isAdmin"`
	LogPath   string        `json:"logPath"`
	Data      []UIDataPlace `json:"data,omitempty"`
	// Capture is true in the screenshot build; the page then draws no live clock or random values.
	Capture bool `json:"capture,omitempty"`
}

// UIInstalled describes the install that is already on this PC.
type UIInstalled struct {
	Version      string `json:"version"`
	Scope        Scope  `json:"scope"`
	Location     string `json:"location"`
	BlackboxName string `json:"blackboxName,omitempty"`
}

// UIDefaults are the choices the Options screen starts with.
type UIDefaults struct {
	UserLocation    string          `json:"userLocation"`
	MachineLocation string          `json:"machineLocation"`
	Blackbox        BlackboxOptions `json:"blackbox"`
}

// UIDataPlace is a data folder the uninstall screen offers to delete, with its size.
type UIDataPlace struct {
	Kind   string `json:"kind"` // "cases" or "recordings"
	Folder string `json:"folder"`
	Bytes  int64  `json:"bytes"`
}

// UIChanges is the Admin approval screen: what will change and what will not.
type UIChanges struct {
	NeedsAdmin    bool     `json:"needsAdmin"`
	WillChange    []string `json:"willChange"`
	WillNotChange []string `json:"willNotChange"`
}

// UIBackend does the work behind the page. LocalBackend is the real one; the screenshot build uses a fake that replays fixed runs.
type UIBackend interface {
	State() UIState
	Preflight(req WorkRequest) []Check
	Changes(req WorkRequest) UIChanges
	Run(ctx context.Context, req WorkRequest, onEvent func(StepEvent), onLog func(string)) Result
}

// UIServer serves the page and its API.
type UIServer struct {
	Key     string
	Static  fs.FS
	Backend UIBackend
	// Scenarios is set only in the screenshot build: the X-Setup-Scenario header picks one fake backend per request.
	Scenarios map[string]UIBackend
	// OpenApp starts Unravis as the signed-in person; OpenFolder shows a folder in Explorer; Quit ends setup.
	OpenApp    func(exe string) error
	OpenFolder func(dir string) error
	Quit       func()
	// Addr is host:port the page is served on, checked against Host and Origin.
	Addr string

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	events  []StepEvent
	lines   int
	result  *Result
	// keyShown is set once the read key has been handed to the page: it is shown once and never again.
	keyShown bool
	// lastSeen is the last keyed request; the page asks for its state every 30 seconds while it is open.
	lastSeen time.Time
}

// Idle reports that no run is going and the page has not been heard from for d: the person closed the window without pressing
// Close, and setup can end. The clock starts at the first call.
func (s *UIServer) Idle(now time.Time, d time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	if s.lastSeen.IsZero() {
		s.lastSeen = now
		return false
	}
	return now.Sub(s.lastSeen) > d
}

// NewKey makes a one-time key for the page address.
func NewKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Listen opens a loopback port (any free one).
func Listen() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }

// URL is the page address with the key in the fragment.
func (s *UIServer) URL() string { return "http://" + s.Addr + "/#k=" + s.Key }

const maxUIBody = 64 << 10

func (s *UIServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	if r.Host != s.Addr {
		http.Error(w, "wrong host", http.StatusMisdirectedRequest)
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+s.Addr {
		http.Error(w, "wrong origin", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if s.Static == nil {
			http.NotFound(w, r)
			return
		}
		http.FileServer(http.FS(s.Static)).ServeHTTP(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Setup-Key")), []byte(s.Key)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "This setup window has expired. Close it and start setup again."})
		return
	}
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
	be := s.Backend
	if s.Scenarios != nil {
		if b, ok := s.Scenarios[r.Header.Get("X-Setup-Scenario")]; ok {
			be = b
		}
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /api/state":
		writeJSON(w, http.StatusOK, be.State())
	case "POST /api/preflight":
		req, ok := readRequest(w, r)
		if ok {
			writeJSON(w, http.StatusOK, map[string]any{"checks": be.Preflight(req), "changes": be.Changes(req)})
		}
	case "POST /api/run":
		req, ok := readRequest(w, r)
		if ok {
			s.startRun(w, be, req)
		}
	case "GET /api/progress":
		since, _ := strconv.Atoi(r.URL.Query().Get("since"))
		s.mu.Lock()
		if since < 0 || since > len(s.events) {
			since = len(s.events)
		}
		var res *Result
		if s.result != nil {
			c := *s.result
			if s.keyShown {
				c.ReadKey = "" // the read key goes to the page once; after a reload the page says where to make a new one
			}
			s.keyShown = s.keyShown || c.ReadKey != ""
			res = &c
		}
		out := map[string]any{"events": append([]StepEvent{}, s.events[since:]...), "next": len(s.events), "running": s.running, "result": res}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, out)
	case "POST /api/cancel":
		s.mu.Lock()
		if s.cancel != nil {
			s.cancel()
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "POST /api/open-app":
		s.mu.Lock()
		res := s.result
		s.mu.Unlock()
		// Only the Unravis this run installed, and only after it finished.
		if res == nil || res.Outcome != OutcomeDone || res.AppExe == "" || res.Op == OpUninstall {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Unravis can be opened once setup has finished."})
			return
		}
		if s.OpenApp != nil {
			if err := s.OpenApp(res.AppExe); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Unravis could not be started: " + err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "POST /api/open-log-folder":
		dir := filepath.Dir(be.State().LogPath)
		if s.OpenFolder != nil && dir != "." {
			_ = s.OpenFolder(dir)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case "POST /api/close":
		s.mu.Lock()
		running := s.running
		s.mu.Unlock()
		if running {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Setup is still working. Press Cancel first."})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		if s.Quit != nil {
			go s.Quit()
		}
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown request"})
	}
}

func (s *UIServer) startRun(w http.ResponseWriter, be UIBackend, req WorkRequest) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Setup is already working."})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.running, s.cancel, s.events, s.result, s.keyShown = true, cancel, nil, nil, false
	s.mu.Unlock()
	go func() {
		res := be.Run(ctx, req, func(e StepEvent) {
			s.mu.Lock()
			s.events = append(s.events, e)
			s.mu.Unlock()
		}, func(string) {
			s.mu.Lock()
			s.lines++
			s.mu.Unlock()
		})
		res.ReadKey = strings.TrimSpace(res.ReadKey)
		s.mu.Lock()
		s.running, s.cancel, s.result = false, nil, &res
		s.mu.Unlock()
		cancel()
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func readRequest(w http.ResponseWriter, r *http.Request) (WorkRequest, bool) {
	var req WorkRequest
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "send JSON"})
		return req, false
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxUIBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "The request could not be read: " + err.Error()})
		return req, false
	}
	if !workerOps[req.Op] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action"})
		return req, false
	}
	req.Silent = false
	req.Options.ReadKeyFile = "" // the window shows the key; only silent mode writes it to a file
	return req, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- the real backend ----

// LocalBackend runs setup on this PC. Runs that need administrator rights go to the elevated worker through Elev; the rest run in
// this (unelevated) process.
type LocalBackend struct {
	R       *Runner
	Elev    Elevator
	Mode    Op
	IsAdmin bool
	LogPath string
	// Log is this process's log file.
	Log func(string)
	// AllowDowngrade is --allow-downgrade: the only way to install an older version over a newer one.
	AllowDowngrade bool
}

func (b *LocalBackend) installed() *Installed {
	old, err := Detect(b.R.Sys.Reg)
	if err != nil {
		return nil
	}
	return old
}

// State describes this PC for the first screen.
func (b *LocalBackend) State() UIState {
	st := UIState{Mode: b.Mode, IsAdmin: b.IsAdmin, LogPath: b.LogPath}
	if b.R.Payload != nil {
		st.Version = b.R.Payload.Manifest.Version
	}
	st.Defaults = UIDefaults{UserLocation: DefaultLocation(ScopeUser, b.R.Dirs), MachineLocation: DefaultLocation(ScopeMachine, b.R.Dirs),
		Blackbox: DefaultBlackbox(defaultBlackboxName(), b.R.Sys.Mach.DriveExists)}
	old := b.installed()
	if old != nil {
		st.Installed = &UIInstalled{Version: old.Version, Scope: old.State.Scope, Location: old.Location}
		if old.State.Blackbox != nil {
			st.Installed.BlackboxName = old.State.Blackbox.Name
		}
		if st.Mode == OpInstall {
			st.Mode = ResolveOp(OpInstall, old, st.Version)
		}
		l := NewLayout(old.State.Options(), b.R.Dirs)
		folders := l.UserData
		if l.SharedRoot != "" {
			folders = []string{l.SharedRoot}
		}
		for _, f := range folders {
			if n, err := folderSize(f); err == nil {
				st.Data = append(st.Data, UIDataPlace{Kind: "cases", Folder: f, Bytes: n})
			}
		}
		if old.State.Blackbox != nil {
			if n, err := folderSize(old.State.Blackbox.Folder); err == nil {
				st.Data = append(st.Data, UIDataPlace{Kind: "recordings", Folder: old.State.Blackbox.Folder, Bytes: n})
			}
		}
	}
	return st
}

func defaultBlackboxName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return strings.ToLower(h)
	}
	return "blackbox"
}

func folderSize(dir string) (int64, error) {
	var n int64
	if _, err := os.Stat(dir); err != nil {
		return 0, err
	}
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n, err
}

// Preflight checks the PC for the request.
func (b *LocalBackend) Preflight(req WorkRequest) []Check {
	old := b.installed()
	op := ResolveOp(req.Op, old, b.version())
	return b.R.Preflight(PreflightInput{Op: op, Opts: req.Options, Old: old, AllowDowngrade: b.AllowDowngrade})
}

func (b *LocalBackend) version() string {
	if b.R.Payload == nil {
		return ""
	}
	return b.R.Payload.Manifest.Version
}

// Changes lists what the run will change for the Admin approval screen.
func (b *LocalBackend) Changes(req WorkRequest) UIChanges {
	return DescribeChanges(req, b.installed(), b.R.Dirs, b.IsAdmin)
}

// DescribeChanges is the Admin approval screen's two lists. Upgrade, repair and uninstall keep the choices of the install.
func DescribeChanges(req WorkRequest, old *Installed, d Dirs, isAdmin bool) UIChanges {
	o := req.Options
	if req.Op != OpInstall && old != nil {
		o = old.State.Options()
	} else if old != nil {
		o.Scope = old.State.Scope
	}
	c := UIChanges{NeedsAdmin: NeedsAdmin(o) && !isAdmin}
	l := NewLayout(o, d)
	if req.Op == OpUninstall {
		c.WillChange = append(c.WillChange, "Remove the program folder "+l.InstallDir, "Remove the Start menu shortcut and the Installed apps entry")
		if o.Scope == ScopeMachine {
			c.WillChange = append(c.WillChange, "Remove the key keeper service")
		}
		if o.Blackbox != nil {
			c.WillChange = append(c.WillChange, "Remove the Blackbox service "+blackbox.ServiceName)
		}
		if o.Firewall != nil {
			c.WillChange = append(c.WillChange, "Remove the firewall rule for port "+strconv.Itoa(o.Blackbox.Port))
		}
		c.WillNotChange = []string{"Cases and saved connections (unless you ticked them)", "Blackbox recordings (unless you ticked them)", "Other programs and services", "Any database or server"}
		return c
	}
	c.WillChange = append(c.WillChange, "Program files in "+l.InstallDir)
	if o.Scope == ScopeMachine {
		c.WillChange = append(c.WillChange, "Shared data folder "+filepath.Join(d.ProgramData, SharedFolderName)+" (everyone on this PC reads it; only Unravis and the key keeper change it)",
			"Key keeper service "+keeper.ServiceName+" (keeps saved passwords sealed for everyone)")
	}
	if o.Blackbox != nil {
		c.WillChange = append(c.WillChange, "Blackbox service "+blackbox.ServiceName+" and its folder "+o.Blackbox.Folder)
		if o.Firewall != nil {
			c.WillChange = append(c.WillChange, "Firewall rule for port "+strconv.Itoa(o.Blackbox.Port)+", open only to "+strings.Join(o.Firewall.RemoteAddresses, ", "))
		}
	}
	c.WillNotChange = []string{"Your cases and saved connections", "Saved passwords (they stay sealed as they are)", "Other programs and services", "Any database or server"}
	return c
}

// Run runs the request, through the elevated worker when it needs administrator rights.
func (b *LocalBackend) Run(ctx context.Context, req WorkRequest, onEvent func(StepEvent), onLog func(string)) Result {
	req.AllowDowngrade = b.AllowDowngrade // the page cannot ask for a downgrade; only the command line can
	req.Silent = false
	old := b.installed()
	o := req.Options
	if req.Op != OpInstall && old != nil {
		o = old.State.Options()
	}
	if req.Op == OpInstall && old != nil {
		o = old.State.Options() // a second install over an install keeps its choices
	}
	if NeedsAdmin(o) && !b.IsAdmin {
		if b.Elev == nil {
			return Result{Outcome: OutcomeRolledBack, Op: req.Op, Message: "This choice needs administrator approval, which this setup cannot ask for. Nothing was changed."}
		}
		u := b.R.Dirs.User()
		req.User = &u // the worker checks these against the profile Windows names for this account
		res := RunWorker(ctx, b.Elev, req, onEvent, func(s string) {
			if b.Log != nil {
				b.Log("worker: " + s)
			}
			onLog(s)
		})
		return res
	}
	r := *b.R
	r.Events = func(e StepEvent) {
		if b.R.Events != nil {
			b.R.Events(e)
		}
		onEvent(e)
	}
	r.Log = func(s string) {
		if b.R.Log != nil {
			b.R.Log(s)
		}
		onLog(s)
	}
	return r.Perform(ctx, req)
}
