package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/evtx"
	"api-audit-explorer/internal/discover/localread"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/sampler"
)

// Host discovery routes (spec sections 2, 3, 7). A scan looks at this computer
// through discover.Env and keeps its findings in memory only: the canonical
// paths behind a finding never leave this file's scan store. The client names a
// finding by its opaque id, and the import route (discover_import.go) resolves
// the id against the scan, so a path can neither be sent in nor read out.
//
// Nothing here logs, and no response holds a path, a machine name or the text of
// an OS error: failures are fixed reasons.

const (
	// scanTTL is how long a finished scan stays readable.
	scanTTL = time.Hour
	// maxScans is how many scans one process keeps.
	maxScans = 4
	// maxGoneIDs bounds the memory of expired scan ids (to answer
	// scan-expired rather than scan-unknown).
	maxGoneIDs = 256
	// maxIdentified bounds the files whose identity a scan records. A file past
	// the bound is listed but cannot be imported (its identity is unknown).
	maxIdentified = 20000
	// maxFindingIDLen and minFindingIDLen bound an opaque finding id.
	minFindingIDLen = 4
	maxFindingIDLen = 64
)

var (
	scanIDPattern    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	findingIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{4,64}$`)
)

// Scan states.
const (
	scanRunning   = "running"
	scanDone      = "done"
	scanCancelled = "cancelled"
	scanFailed    = "failed"
)

// Detector progress states.
const (
	progWaiting = "waiting"
	progRunning = "running"
	progDone    = "done"
	progSkipped = "skipped"
	progFailed  = "failed"
)

// QueryFunc reads one event channel as win-events CSV (evtx.Query).
type QueryFunc func(ctx context.Context, w io.Writer, channel string, from, to time.Time, afterRecordID uint64) (evtx.Result, error)

// DiscoverConfig is what the discovery routes read the computer through. The
// real one is WithSystemDiscover; tests pass fakes. Without WithDiscover the
// routes say discovery is not supported here.
type DiscoverConfig struct {
	// Env is what the detectors may touch. Env.Importers is filled in from the
	// server's registry when the scan starts.
	Env discover.Env
	// Opener opens the files a finding names (localread.System on Windows).
	Opener localread.Opener
	// Query reads an event channel. Nil means channels cannot be imported.
	Query QueryFunc
	// Roots are the IIS roots (SystemDrive, windir, ProgramData, ProgramFiles).
	Roots discover.IISRoots
	// SamplerSource reads the host for the live sampler; nil means the OS.
	SamplerSource sampler.Source
	// SamplerFlush is how often the sampler writes a batch; zero means 5 min.
	SamplerFlush time.Duration
	// Limiter paces file reads; nil means localread's process-wide limiter.
	Limiter localread.Limiter
	// Limits are the scan time bounds; zero means discover.DefaultLimits.
	Limits discover.Limits
	// OS is reported by GET /api/discover/capabilities; empty means runtime.GOOS.
	OS string
	// Unsupported marks a computer discovery cannot read (not Windows).
	Unsupported bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	// Test hooks: zero means the defaults above.
	ScanTTL  time.Duration
	MaxScans int
}

// WithDiscover attaches a discovery environment. Tests use it with fakes.
func WithDiscover(cfg DiscoverConfig) Option {
	return func(h *handler) {
		c := cfg
		h.disc.cfg = &c
	}
}

// WithSystemDiscover attaches the real computer: the Windows environment, the
// hardened file opener and the event log reader. The IIS roots come from the
// SystemDrive, windir, ProgramData and ProgramFiles variables as the server
// starts, defaulting to the C: values. Off Windows (or where the environment
// cannot be built) discovery reports itself unsupported.
func WithSystemDiscover() Option {
	return func(h *handler) {
		env, err := discover.NewSystemEnv()
		cfg := DiscoverConfig{OS: runtime.GOOS, Unsupported: err != nil}
		if err == nil {
			cfg.Env = env
			cfg.Opener = localread.System()
			cfg.Query = evtx.Query
			cfg.Roots = discover.IISRoots{
				SystemDrive:  os.Getenv("SystemDrive"),
				WinDir:       os.Getenv("windir"),
				ProgramData:  os.Getenv("ProgramData"),
				ProgramFiles: os.Getenv("ProgramFiles"),
			}
		}
		h.disc.cfg = &cfg
	}
}

func (c *DiscoverConfig) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *DiscoverConfig) osName() string {
	if c != nil && c.OS != "" {
		return c.OS
	}
	return runtime.GOOS
}

func (c *DiscoverConfig) ttlAndMax() (time.Duration, int) {
	ttl, max := scanTTL, maxScans
	if c != nil && c.ScanTTL > 0 {
		ttl = c.ScanTTL
	}
	if c != nil && c.MaxScans > 0 {
		max = c.MaxScans
	}
	return ttl, max
}

// roots returns the IIS roots with the C: defaults for any that are empty.
func (c *DiscoverConfig) roots() discover.IISRoots {
	r, d := c.Roots, discover.DefaultIISRoots()
	if r.SystemDrive == "" {
		r.SystemDrive = d.SystemDrive
	}
	if r.WinDir == "" {
		r.WinDir = d.WinDir
	}
	if r.ProgramData == "" {
		r.ProgramData = d.ProgramData
	}
	if r.ProgramFiles == "" {
		r.ProgramFiles = d.ProgramFiles
	}
	return r
}

// detectors builds the scan's detector list from the real constructors. The IIS
// site folders feed the application-log and host detectors.
func (c *DiscoverConfig) detectors(env discover.Env) []discover.Detector {
	roots := c.roots()
	sites := discover.IISSiteDirs(env, roots)
	return []discover.Detector{
		discover.NewIISDetector(roots),
		discover.NewHTTPErrDetector(roots),
		discover.NewEventsDetector(),
		discover.NewAppLog(sites),
		discover.RedisDetector{},
		discover.MongoDetector{},
		discover.SQLDetector{},
		discover.NewWeb(),
		discover.NewHost(discover.HostOptions{SystemDrive: roots.SystemDrive, SiteDirs: sites}),
	}
}

/* ------------------------------------------------------------ scan store */

// discoverState is the discovery part of the handler. The zero value works: no
// config means "not supported here".
type discoverState struct {
	mu        sync.Mutex
	cfg       *DiscoverConfig
	scans     map[string]*scanRec
	gone      map[string]bool
	goneOrder []string
	closed    bool
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc

	smu     sync.Mutex // sampler
	sampler *samplerRun
}

// scanFile is one file behind a finding with the identity recorded at scan time.
type scanFile struct {
	path string
	id   localread.FileID
	base string
	mod  time.Time
}

// scanFinding is what the import route resolves a finding id to.
type scanFinding struct {
	f     discover.Finding
	files []scanFile
}

type progressEntry struct {
	detector string
	state    string
}

type scanRec struct {
	id      string
	started time.Time
	cancel  context.CancelFunc
	host    hostInfo

	mu       sync.Mutex
	state    string
	progress []progressEntry
	scan     discover.Scan
	byID     map[string]*scanFinding
	finished time.Time
}

func (r *scanRec) setProgress(i int, state string) {
	r.mu.Lock()
	if i >= 0 && i < len(r.progress) {
		r.progress[i].state = state
	}
	r.mu.Unlock()
}

// progressDetector reports a detector's state to the scan record.
type progressDetector struct {
	discover.Detector
	rec *scanRec
	idx int
}

func (p progressDetector) Detect(ctx context.Context, env discover.Env) (fs []discover.Finding, gs []discover.Gap) {
	p.rec.setProgress(p.idx, progRunning)
	finished := false
	defer func() {
		switch {
		case !finished:
			p.rec.setProgress(p.idx, progFailed) // a panic: RunWith turns it into a gap
		case ctx.Err() != nil:
			p.rec.setProgress(p.idx, progFailed)
		default:
			p.rec.setProgress(p.idx, progDone)
		}
	}()
	fs, gs = p.Detector.Detect(ctx, env)
	finished = true
	return fs, gs
}

func (ds *discoverState) context() context.Context {
	if ds.ctx == nil {
		ds.ctx, ds.cancel = context.WithCancel(context.Background())
	}
	return ds.ctx
}

// pruneLocked drops scans past the TTL and remembers their ids.
func (ds *discoverState) pruneLocked(now time.Time, ttl time.Duration) {
	for id, rec := range ds.scans {
		rec.mu.Lock()
		old := !rec.finished.IsZero() && now.Sub(rec.finished) > ttl
		rec.mu.Unlock()
		if old {
			ds.forgetLocked(id)
		}
	}
}

func (ds *discoverState) forgetLocked(id string) {
	delete(ds.scans, id)
	if ds.gone == nil {
		ds.gone = map[string]bool{}
	}
	if !ds.gone[id] {
		ds.gone[id] = true
		ds.goneOrder = append(ds.goneOrder, id)
		if len(ds.goneOrder) > maxGoneIDs {
			delete(ds.gone, ds.goneOrder[0])
			ds.goneOrder = ds.goneOrder[1:]
		}
	}
}

// scanLookup is the answer of lookup.
type scanLookup int

const (
	scanFound scanLookup = iota
	scanUnknown
	scanExpired
)

// lookup finds a scan by id.
func (ds *discoverState) lookup(id string) (*scanRec, scanLookup) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ttl, _ := ds.cfg.ttlAndMax()
	ds.pruneLocked(ds.cfg.now(), ttl)
	if !scanIDPattern.MatchString(id) {
		return nil, scanUnknown
	}
	if rec := ds.scans[id]; rec != nil {
		return rec, scanFound
	}
	if ds.gone[id] {
		return nil, scanExpired
	}
	return nil, scanUnknown
}

// shutdown cancels every scan and the sampler as the dashboard exits.
func (ds *discoverState) shutdown(wait time.Duration) {
	ds.mu.Lock()
	ds.closed = true
	cancel := ds.cancel
	ds.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() { ds.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(wait):
	}
}

/* ----------------------------------------------------------- host entry */

// hostInfo is the local host as discovery sees it (spec 7.1). No machine name is
// ever part of it: a host with no entry in hosts.json is proposed under the label
// "local".
type hostInfo struct {
	hint     importer.HostHint
	proposed bool
	label    string
	tz       string
	cores    int
	// aliases are this computer's names, offered only with a proposal so the saved
	// host answers to the machine name event rows carry.
	aliases []string
}

// localHost resolves the local host entry: a Machine name that matches a host
// (label or alias) wins, then an entry labelled "local", else a proposal.
func (h *handler) localHost(env discover.Env) hostInfo {
	hm := h.hostsNow()
	if env.Machine != nil {
		for _, name := range env.Machine.Names() {
			if found, ok := hm.Lookup(name); ok {
				return hostFromHint(found)
			}
		}
	}
	if found, ok := hm.Lookup("local"); ok {
		return hostFromHint(found)
	}
	info := hostInfo{label: "local", proposed: true}
	if env.Machine != nil {
		if loc, ok := clock.WindowsZone(env.Machine.TZKey()); ok && loc != nil {
			info.tz = loc.String()
		}
		info.cores = env.Machine.LogicalCores()
		info.aliases = proposalAliases(env.Machine.Names())
	}
	if info.cores < 0 {
		info.cores = 0
	}
	return info
}

// proposalAliases keeps at most four distinct, non-empty machine names of a sane length.
func proposalAliases(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		key := strings.ToLower(n)
		if n == "" || len(n) > 253 || seen[key] || key == "local" {
			continue
		}
		seen[key] = true
		out = append(out, n)
		if len(out) == 4 {
			break
		}
	}
	return out
}

func hostFromHint(hint importer.HostHint) hostInfo {
	info := hostInfo{hint: hint, label: hint.Label, cores: hint.LogicalCores}
	if hint.TZ != nil {
		info.tz = hint.TZ.String()
	}
	return info
}

/* ------------------------------------------------------------- handlers */

type capabilitiesDTO struct {
	Supported bool   `json:"supported"`
	Elevated  bool   `json:"elevated"`
	OS        string `json:"os"`
}

// discoverCfg is the config, nil when discovery is not attached.
func (h *handler) discoverCfg() *DiscoverConfig {
	h.disc.mu.Lock()
	defer h.disc.mu.Unlock()
	return h.disc.cfg
}

// discoverSupported answers 501 when discovery cannot run here.
func (h *handler) discoverSupported(w http.ResponseWriter) (*DiscoverConfig, bool) {
	cfg := h.discoverCfg()
	if cfg == nil || cfg.Unsupported || cfg.Env.FS == nil || cfg.Env.Machine == nil {
		writeCaseFailure(w, http.StatusNotImplemented, string(discover.ReasonNotSupportedOS), "Host discovery is not available on this computer.")
		return nil, false
	}
	return cfg, true
}

// GET /api/discover/capabilities
func (h *handler) discoverCapabilities(w http.ResponseWriter, _ *http.Request) {
	cfg := h.discoverCfg()
	out := capabilitiesDTO{OS: cfg.osName()}
	if cfg != nil && !cfg.Unsupported && cfg.Env.FS != nil && cfg.Env.Machine != nil {
		out.Supported = true
		out.Elevated = cfg.Env.Machine.Elevated()
	}
	writeJSON(w, http.StatusOK, out, 4096)
}

type scanStarted struct {
	ScanID string `json:"scanId"`
}

// POST /api/discover/scans
func (h *handler) postDiscoverScan(w http.ResponseWriter, r *http.Request) {
	cfg, ok := h.discoverSupported(w)
	if !ok {
		return
	}
	var body struct{}
	if !decodeJSON(w, r, &body) {
		return
	}
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		writeCaseFailure(w, http.StatusInternalServerError, "scan-failed", "The scan could not start.")
		return
	}
	ds := &h.disc
	ds.mu.Lock()
	if ds.closed {
		ds.mu.Unlock()
		writeCaseFailure(w, http.StatusServiceUnavailable, "shutting-down", "The dashboard is shutting down.")
		return
	}
	now := cfg.now()
	ttl, max := cfg.ttlAndMax()
	ds.pruneLocked(now, ttl)
	var oldest *scanRec
	var oldestAt time.Time
	for _, rec := range ds.scans {
		rec.mu.Lock()
		running, at := rec.state == scanRunning, rec.finished
		rec.mu.Unlock()
		if running {
			ds.mu.Unlock()
			writeCaseFailure(w, http.StatusConflict, "scan-busy", "A scan is already running.")
			return
		}
		if oldest == nil || at.Before(oldestAt) {
			oldest, oldestAt = rec, at
		}
	}
	for len(ds.scans) >= max && oldest != nil {
		ds.forgetLocked(oldest.id)
		oldest = nil
		oldestAt = time.Time{}
		for _, rec := range ds.scans {
			rec.mu.Lock()
			at := rec.finished
			rec.mu.Unlock()
			if oldest == nil || at.Before(oldestAt) {
				oldest, oldestAt = rec, at
			}
		}
	}
	env := cfg.Env
	env.Importers = h.registry
	ctx, cancel := context.WithCancel(ds.context())
	rec := &scanRec{id: hex.EncodeToString(raw[:]), started: now.UTC(), cancel: cancel, state: scanRunning, host: h.localHost(env)}
	dets := cfg.detectors(env)
	wrapped := make([]discover.Detector, len(dets))
	for i, d := range dets {
		rec.progress = append(rec.progress, progressEntry{detector: d.Name(), state: progWaiting})
		wrapped[i] = progressDetector{Detector: d, rec: rec, idx: i}
	}
	if ds.scans == nil {
		ds.scans = map[string]*scanRec{}
	}
	ds.scans[rec.id] = rec
	ds.wg.Add(1)
	ds.mu.Unlock()

	go h.runScan(ctx, cfg, env, rec, wrapped)
	writeJSON(w, http.StatusAccepted, scanStarted{ScanID: rec.id}, 4096)
}

// runScan runs the detectors and then records the identity of every file the
// findings name, so an import can later tell a swapped file from the scanned one.
func (h *handler) runScan(ctx context.Context, cfg *DiscoverConfig, env discover.Env, rec *scanRec, dets []discover.Detector) {
	ds := &h.disc
	defer ds.wg.Done()
	defer rec.cancel()
	state := scanDone
	var scan discover.Scan
	byID := map[string]*scanFinding{}
	func() {
		defer func() {
			if recover() != nil {
				state = scanFailed
			}
		}()
		scan = discover.RunWith(ctx, env, dets, cfg.Limits)
		identified := 0
		for i := range scan.Findings {
			f := scan.Findings[i]
			// A machine name must not reach a response: the finding carries the
			// local host's label.
			f.HostLabel = rec.host.label
			sf := &scanFinding{f: f}
			for _, p := range f.Paths() {
				file := scanFile{path: p}
				if cfg.Opener != nil && identified < maxIdentified && ctx.Err() == nil {
					identified++
					if info, err := localread.IdentifyWith(cfg.Opener, p); err == nil {
						file.id, file.base, file.mod = info.ID, info.Base, info.ModTime
					}
				}
				sf.files = append(sf.files, file)
			}
			// A set of files is pulled oldest first, so a watermark never skips ahead.
			sort.SliceStable(sf.files, func(a, b int) bool { return sf.files[a].mod.Before(sf.files[b].mod) })
			scan.Findings[i] = f
			byID[f.ID] = sf
		}
	}()
	if ctx.Err() != nil && state == scanDone {
		state = scanCancelled
	}
	rec.mu.Lock()
	rec.scan = scan
	rec.byID = byID
	rec.state = state
	rec.finished = cfg.now().UTC()
	for i := range rec.progress {
		switch rec.progress[i].state {
		case progRunning:
			rec.progress[i].state = progFailed
		case progWaiting:
			rec.progress[i].state = progSkipped
		}
	}
	rec.mu.Unlock()
}

type progressDTO struct {
	Detector string `json:"detector"`
	State    string `json:"state"`
}

type hostDTO struct {
	Label    string `json:"label"`
	Proposed bool   `json:"proposed"`
	TZ       string `json:"tz"`
	Cores    int    `json:"cores"`
	// Aliases is set only on a proposal.
	Aliases []string `json:"aliases,omitempty"`
}

type scanDTO struct {
	State     string             `json:"state"`
	StartedAt time.Time          `json:"startedAt"`
	Findings  []discover.Finding `json:"findings"`
	Gaps      []discover.Gap     `json:"gaps"`
	Host      hostDTO            `json:"host"`
	Progress  []progressDTO      `json:"progress,omitempty"`
	Elevated  bool               `json:"elevated"`
	Complete  bool               `json:"complete"`
}

func (r *scanRec) dto() scanDTO {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := scanDTO{
		State: r.state, StartedAt: r.started, Findings: []discover.Finding{}, Gaps: []discover.Gap{},
		Host:     hostDTO{Label: r.host.label, Proposed: r.host.proposed, TZ: r.host.tz, Cores: r.host.cores, Aliases: r.host.aliases},
		Elevated: r.scan.Elevated, Complete: r.scan.Complete,
	}
	if r.state == scanDone || r.state == scanCancelled {
		out.Findings = append(out.Findings, r.scan.Findings...)
		out.Gaps = append(out.Gaps, r.scan.Gaps...)
	}
	for _, p := range r.progress {
		out.Progress = append(out.Progress, progressDTO{Detector: p.detector, State: p.state})
	}
	return out
}

// scanFromPath returns the scan the path's id names or writes the 404.
func (h *handler) scanFromPath(w http.ResponseWriter, id string) (*scanRec, bool) {
	rec, how := h.disc.lookup(id)
	switch how {
	case scanFound:
		return rec, true
	case scanExpired:
		writeCaseFailure(w, http.StatusNotFound, "scan-expired", "That scan has expired. Scan again.")
	default:
		writeCaseFailure(w, http.StatusNotFound, "scan-unknown", "That scan was not found.")
	}
	return nil, false
}

// GET /api/discover/scans/{scan}
func (h *handler) getDiscoverScan(w http.ResponseWriter, _ *http.Request, id string) {
	if _, ok := h.discoverSupported(w); !ok {
		return
	}
	rec, ok := h.scanFromPath(w, id)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, rec.dto(), maxResponseBytes)
}

// DELETE /api/discover/scans/{scan}: a running scan is cancelled; the scan is
// forgotten either way.
func (h *handler) deleteDiscoverScan(w http.ResponseWriter, _ *http.Request, id string) {
	if _, ok := h.discoverSupported(w); !ok {
		return
	}
	rec, ok := h.scanFromPath(w, id)
	if !ok {
		return
	}
	rec.cancel()
	h.disc.mu.Lock()
	delete(h.disc.scans, rec.id)
	h.disc.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// scanPath matches /api/discover/scans/{scan}: one non-empty segment.
func scanPath(path string) (string, bool) {
	id, ok := strings.CutPrefix(path, "/api/discover/scans/")
	if !ok || id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}
