package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/investigate"
	"api-audit-explorer/internal/keeper"
)

// Blackbox in Unravis (P9). The saved list (sealed, with read keys), the add flow,
// the this-PC actions and the host reading that feeds Step 9. Unravis only reads a
// Blackbox. The read key goes from the sealed list into the request header of the
// code that reads, and nowhere else: not into an answer, a log line, a case file
// or an export. A case that uses a Blackbox carries its name as a label only.

// BlackboxStore is the saved list. *bbsaved.Store is the real one.
type BlackboxStore interface {
	List() ([]bbsaved.Entry, error)
	Get(id string) (bbsaved.Entry, error)
	Find(canon, recHost, recName string) (bbsaved.Entry, bool)
	Add(in bbsaved.AddInput) (bbsaved.Entry, error)
	SetThisPC(id string, thisPC bool) error
	SetKey(id, key string) error
	SetRecorder(id, host, name string) error
	Remove(id string) error
	Key(id string) (string, error)
}

// BlackboxPC changes the Blackbox on this PC. *blackbox.ThisPC is the real one;
// tests and the demo give fakes, so no service is touched.
type BlackboxPC interface {
	Install(ctx context.Context, cfg blackbox.Config) (blackbox.InstallOutcome, error)
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Apply(ctx context.Context, cfg blackbox.Config) (bool, error)
	Remove(ctx context.Context, name string, deleteRecordings bool, confirmName string) (blackbox.RemoveOutcome, error)
	Snapshot(ctx context.Context) (blackbox.SnapshotOutcome, error)
}

// BlackboxDeps wires the Blackbox pages. Only Store is required; a missing ThisPC
// turns the this-PC actions off, a missing Scan means nothing is found on this PC,
// and a missing Program means a pack cannot be built.
type BlackboxDeps struct {
	Store   BlackboxStore
	ThisPC  BlackboxPC
	Scan    func() (blackbox.LocalFound, error)
	Program blackbox.ProgramSource
	// Open builds the reader of one place. nil uses the real folder and URL readers.
	Open func(loc blackbox.Location, key string) blackbox.Source
	Now  func() time.Time
}

// WithBlackbox attaches the Blackbox pages.
func WithBlackbox(d BlackboxDeps) Option {
	return func(h *handler) {
		if d.Store == nil {
			return
		}
		h.bb = newBlackboxHub(d)
	}
}

type blackboxHub struct {
	deps   BlackboxDeps
	pcMu   sync.Mutex // one change to this PC at a time
	probes connectionTestLimiter
}

func newBlackboxHub(d BlackboxDeps) *blackboxHub {
	b := &blackboxHub{deps: d}
	b.probes.limit = 30
	return b
}

func (b *blackboxHub) now() time.Time {
	if b.deps.Now != nil {
		return b.deps.Now()
	}
	return time.Now()
}

func (b *blackboxHub) open(loc blackbox.Location, key string) blackbox.Source {
	if b.deps.Open != nil {
		return b.deps.Open(loc, key)
	}
	if loc.Kind == blackbox.LocURL {
		return blackbox.NewHTTPSource(loc, key, nil)
	}
	return blackbox.NewFolderSource(loc)
}

const (
	bbProbeTimeout  = 8 * time.Second
	bbReadTimeout   = 40 * time.Second
	bbActionTimeout = 3 * time.Minute
	bbMaxBody       = 1 << 20
	bbMaxSpan       = 7 * 24 * time.Hour
	bbMaxName       = 200
	bbNewestLook    = 10 * time.Minute
)

/* ------------------------------------------------------------- routes */

func bbExact(p string) func(string) (string, bool) { return exactPath(p) }

// bbEntryPath matches /api/blackbox/{id}<suffix> for an id the list made ("bb-...").
func bbEntryPath(suffix string) func(string) (string, bool) {
	return func(path string) (string, bool) {
		rest, ok := strings.CutPrefix(path, "/api/blackbox/")
		if !ok {
			return "", false
		}
		id, ok := strings.CutSuffix(rest, suffix)
		if !ok || !strings.HasPrefix(id, "bb-") || strings.Contains(id, "/") || len(id) > 64 {
			return "", false
		}
		return id, true
	}
}

func bbRoute(pattern string, match func(string) (string, bool), methods map[string]routeFunc) route {
	return route{pattern: pattern, match: match, methods: methods}
}

func bbM(method string, fn func(*handler, http.ResponseWriter, *http.Request, string)) map[string]routeFunc {
	return map[string]routeFunc{method: fn}
}

func init() {
	routes = append(routes,
		bbRoute("/api/blackbox", bbExact("/api/blackbox"), map[string]routeFunc{
			http.MethodGet:  func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbList(w, r) },
			http.MethodPost: func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbAdd(w, r, true) },
		}),
		bbRoute("/api/blackbox/test", bbExact("/api/blackbox/test"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbAdd(w, r, false) })),
		bbRoute("/api/blackbox/validate", bbExact("/api/blackbox/validate"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbValidate(w, r) })),
		bbRoute("/api/blackbox/pack", bbExact("/api/blackbox/pack"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbPack(w, r) })),
		bbRoute("/api/blackbox/this-pc", bbExact("/api/blackbox/this-pc"), bbM(http.MethodGet, func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbThisPCScan(w, r) })),
		bbRoute("/api/blackbox/this-pc/install", bbExact("/api/blackbox/this-pc/install"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbInstall(w, r) })),
		bbRoute("/api/blackbox/this-pc/start", bbExact("/api/blackbox/this-pc/start"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbStartStop(w, r, true) })),
		bbRoute("/api/blackbox/this-pc/stop", bbExact("/api/blackbox/this-pc/stop"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbStartStop(w, r, false) })),
		bbRoute("/api/blackbox/this-pc/apply", bbExact("/api/blackbox/this-pc/apply"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbApply(w, r) })),
		bbRoute("/api/blackbox/this-pc/remove", bbExact("/api/blackbox/this-pc/remove"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbRemoveThisPC(w, r) })),
		bbRoute("/api/blackbox/this-pc/snapshot", bbExact("/api/blackbox/this-pc/snapshot"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.bbSnapshot(w, r) })),
		bbRoute("/api/blackbox/{id}/check", bbEntryPath("/check"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.bbCheck(w, r, id) })),
		bbRoute("/api/blackbox/{id}/key", bbEntryPath("/key"), bbM(http.MethodPut, func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.bbSetKey(w, r, id) })),
		bbRoute("/api/blackbox/{id}/reveal-key", bbEntryPath("/reveal-key"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.bbRevealKey(w, r, id) })),
		bbRoute("/api/blackbox/{id}/host-reading", bbEntryPath("/host-reading"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.bbHostReading(w, r, id) })),
		bbRoute("/api/blackbox/{id}", bbEntryPath(""), bbM(http.MethodDelete, func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.bbRemoveEntry(w, r, id) })),
	)
}

/* --------------------------------------------------------------- DTOs */

// bbFacts is what the manifest says, for the list and the status screens.
type bbFacts struct {
	Name            string     `json:"name"`
	Host            string     `json:"host"`
	Version         string     `json:"version,omitempty"`
	IntervalSeconds int        `json:"intervalSeconds"`
	State           string     `json:"state"`
	FirstSample     *time.Time `json:"firstSample,omitempty"`
	LastSample      *time.Time `json:"lastSample,omitempty"`
	Files           int        `json:"files"`
	Samples         int        `json:"samples"`
	Gaps            int        `json:"gaps"`
	// Unread is the number of recording files that could not be read to the end. It is what makes a Blackbox Incomplete.
	Unread      int      `json:"unread,omitempty"`
	FreePercent *float64 `json:"freePercent,omitempty"`
}

type bbItem struct {
	bbsaved.Entry
	State blackbox.StateInfo `json:"state"`
	Facts *bbFacts           `json:"facts,omitempty"`
}

type bbThisPC struct {
	// Available is true when this build can change the Blackbox on this PC.
	Available bool `json:"available"`
	// Scanned is false when this PC could not be asked (nothing is claimed then).
	Scanned          bool   `json:"scanned"`
	Installed        bool   `json:"installed"`
	Running          bool   `json:"running"`
	Name             string `json:"name,omitempty"`
	Folder           string `json:"folder,omitempty"`
	Port             int    `json:"port,omitempty"`
	Bind             string `json:"bind,omitempty"`
	ConfigUnreadable bool   `json:"configUnreadable,omitempty"`
	// Listed is true when the list holds an entry that stands for it.
	Listed  bool   `json:"listed"`
	ListID  string `json:"listId,omitempty"`
	Problem string `json:"problem,omitempty"`
	// Settings is what the installed service runs with, so Apply settings starts from it.
	Settings *blackbox.Config `json:"settings,omitempty"`
}

type bbListAnswer struct {
	Items   []bbItem `json:"items"`
	ThisPC  bbThisPC `json:"thisPc"`
	CanPack bool     `json:"canPack"`
}

type bbFailure struct {
	Error  string              `json:"error"`
	Reason string              `json:"reason"`
	What   string              `json:"what,omitempty"`
	Next   string              `json:"next,omitempty"`
	State  *blackbox.StateInfo `json:"state,omitempty"`
	// Existing is the entry already in the list, on a duplicate.
	Existing *bbItem `json:"existing,omitempty"`
}

func (b *blackboxHub) facts(rd *blackbox.Reading, rep *blackbox.ReadReport) *bbFacts {
	if rd == nil {
		return nil
	}
	m := rd.Manifest
	f := &bbFacts{
		Name: m.Name, Host: m.Host, Version: m.Version, IntervalSeconds: m.IntervalSeconds, State: m.State,
		FirstSample: m.FirstSample, LastSample: m.LastSample, Files: len(m.Files), Gaps: len(m.Gaps),
	}
	for _, fe := range m.Files {
		f.Samples += fe.Samples
	}
	if rep != nil {
		f.Unread = len(rep.Dropped) + len(rep.Missing)
	}
	if rd.Status != nil {
		f.FreePercent = rd.Status.FreePercent
	}
	return f
}

/* ------------------------------------------------------------ looking */

type bbLook struct {
	State   blackbox.StateInfo
	Reading *blackbox.Reading
	Report  *blackbox.ReadReport
	Err     error
}

func readFail(kind blackbox.ReadKind, msg string) error {
	return &blackbox.ReadError{Kind: kind, Message: msg}
}

// source opens the reader of a saved entry. The key is read from the sealed list
// here, handed to the reader and kept nowhere else.
func (b *blackboxHub) source(e bbsaved.Entry) (blackbox.Source, error) {
	loc, err := blackbox.ParseLocation(e.Location)
	if err != nil {
		return nil, readFail(blackbox.ReadNotFound, "The saved place of this Blackbox is not valid any more. Remove it and add it again.")
	}
	key := ""
	if loc.NeedsKey() {
		if kb, held := b.deps.Store.(keeperBB); held {
			// The read key is in the key keeper (owner ruling B19): the keyed reads go through it.
			return kb.KeyedSource(e.ID, loc), nil
		}
		k, kerr := b.deps.Store.Key(e.ID)
		if kerr != nil || k == "" {
			return nil, readFail(blackbox.ReadKeyRefused, "No read key could be opened for this Blackbox. Type the read key again.")
		}
		key = k
	}
	return b.open(loc, key), nil
}

// lookAt reads the manifest, the status and the newest minutes of one place and
// derives the state to show. placed is false for a this-PC entry whose service is
// gone: nothing is read then.
func (b *blackboxHub) lookAt(ctx context.Context, src blackbox.Source, placed bool) bbLook {
	if !placed {
		return bbLook{State: blackbox.DeriveState(blackbox.StateInput{Placed: false})}
	}
	ctx, cancel := context.WithTimeout(ctx, bbProbeTimeout)
	defer cancel()
	rd, err := blackbox.ProbeSource(ctx, src, b.now)
	if err != nil {
		return bbLook{Err: err, State: blackbox.DeriveState(blackbox.StateInput{Placed: true, Err: err})}
	}
	var rep *blackbox.ReadReport
	if rd.Manifest.LastSample != nil {
		last := *rd.Manifest.LastSample
		if _, r, rerr := blackbox.ReadSamples(ctx, src, rd.Manifest, last.Add(-bbNewestLook), last.Add(time.Second)); rerr == nil {
			rep = &r
		}
	}
	return bbLook{Reading: &rd, Report: rep, State: blackbox.DeriveState(blackbox.StateInput{Placed: true, Reading: &rd, Report: rep})}
}

// scan asks this PC what it runs. nil means it could not be asked or cannot be.
func (b *blackboxHub) scan() (*blackbox.LocalFound, error) {
	if b.deps.Scan == nil {
		return nil, nil
	}
	f, err := b.deps.Scan()
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func stoppedHere() blackbox.StateInfo {
	return blackbox.StateInfo{
		State: blackbox.SStopped, Label: blackbox.SStopped.Label(), Tone: blackbox.ToneIdle, Readiness: blackbox.WordPartlyReady,
		Detail: "The Blackbox service on this PC is stopped. Its recordings are kept; nothing new is recorded. Start it to record again.",
	}
}

// lookEntry looks at one saved entry.
func (b *blackboxHub) lookEntry(ctx context.Context, e bbsaved.Entry, found *blackbox.LocalFound) bbLook {
	placed := true
	if e.ThisPC && found != nil && !found.Installed {
		placed = false
	}
	// The service on this PC belongs to another name: this entry's service is gone.
	if e.ThisPC && found != nil && found.Installed && found.Name != "" && e.RecorderName != "" && !strings.EqualFold(found.Name, e.RecorderName) {
		placed = false
	}
	src, err := b.source(e)
	if err != nil {
		return bbLook{Err: err, State: blackbox.DeriveState(blackbox.StateInput{Placed: placed, Err: err})}
	}
	l := b.lookAt(ctx, src, placed)
	if l.Err != nil && e.ThisPC && found != nil && found.Installed && !found.Running {
		l.State = stoppedHere()
	}
	if l.Reading != nil && e.RecorderHost == "" && e.RecorderName == "" {
		_ = b.deps.Store.SetRecorder(e.ID, l.Reading.Manifest.Host, l.Reading.Manifest.Name)
	}
	return l
}

func (b *blackboxHub) item(e bbsaved.Entry, l bbLook) bbItem {
	return bbItem{Entry: e, State: l.State, Facts: b.facts(l.Reading, l.Report)}
}

/* --------------------------------------------------------- unavailable */

func (h *handler) bbReady(w http.ResponseWriter) bool {
	if h.bb == nil {
		writeJSON(w, http.StatusServiceUnavailable, bbFailure{Error: "Blackbox is not available in this build.", Reason: "unavailable"}, 4096)
		return false
	}
	return true
}

func bbStoreFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, keeper.ErrNotRunning):
		writeJSON(w, http.StatusServiceUnavailable, bbFailure{Error: keeperStoppedText, Reason: "keeper-stopped"}, 4096)
	case errors.Is(err, keeper.ErrImpostor):
		writeJSON(w, http.StatusServiceUnavailable, bbFailure{Error: keeperImpostorText, Reason: "keeper-impostor"}, 4096)
	case errors.Is(err, keeper.ErrKeeperHeld):
		writeJSON(w, http.StatusForbidden, bbFailure{Error: "The key keeper holds this read key and never gives it out.", Reason: "keeper-held", Next: "On a PC set up for everyone, Unravis never shows a saved read key. Ask the person who looks after this PC."}, 4096)
	case errors.Is(err, bbsaved.ErrNotFound):
		writeJSON(w, http.StatusNotFound, bbFailure{Error: err.Error(), Reason: "not-listed"}, 4096)
	case errors.Is(err, bbsaved.ErrCorrupt):
		writeJSON(w, http.StatusConflict, bbFailure{Error: err.Error(), Reason: "list-damaged", Next: "The file was left as it is. Restore it from a backup, or move it away to start a new list."}, 4096)
	case errors.Is(err, bbsaved.ErrTooMany), errors.Is(err, bbsaved.ErrInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, bbFailure{Error: err.Error(), Reason: "invalid"}, 4096)
	default:
		writeJSON(w, http.StatusServiceUnavailable, bbFailure{Error: bbsaved.ErrUnavailable.Error(), Reason: "list-unavailable"}, 4096)
	}
}

/* ---------------------------------------------------------------- list */

func (h *handler) bbList(w http.ResponseWriter, r *http.Request) {
	if !h.bbReady(w) {
		return
	}
	b := h.bb
	entries, err := b.deps.Store.List()
	if err != nil {
		bbStoreFailure(w, err)
		return
	}
	found, scanErr := b.scan()
	items := make([]bbItem, len(entries))
	var wg sync.WaitGroup
	for i := range entries {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			items[i] = b.item(entries[i], b.lookEntry(r.Context(), entries[i], found))
		}(i)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, bbListAnswer{Items: items, ThisPC: b.thisPCBlock(entries, found, scanErr), CanPack: b.deps.Program != nil}, maxResponseBytes)
}

func (b *blackboxHub) thisPCBlock(entries []bbsaved.Entry, found *blackbox.LocalFound, scanErr error) bbThisPC {
	t := bbThisPC{Available: b.deps.ThisPC != nil}
	for _, e := range entries {
		if e.ThisPC {
			t.Listed, t.ListID = true, e.ID
			break
		}
	}
	switch {
	case scanErr != nil:
		t.Problem = "This PC could not be asked whether a Blackbox is installed."
	case found != nil:
		t.Scanned = true
		t.Installed, t.Running = found.Installed, found.Running
		t.Name, t.Folder, t.Port, t.Bind, t.ConfigUnreadable = found.Name, found.Folder, found.Port, found.Bind, found.ConfigUnreadable
		t.Settings = found.Settings
	}
	return t
}

func (h *handler) bbThisPCScan(w http.ResponseWriter, _ *http.Request) {
	if !h.bbReady(w) {
		return
	}
	entries, err := h.bb.deps.Store.List()
	if err != nil {
		bbStoreFailure(w, err)
		return
	}
	found, scanErr := h.bb.scan()
	writeJSON(w, http.StatusOK, h.bb.thisPCBlock(entries, found, scanErr), 16384)
}

/* ----------------------------------------------------------------- add */

type bbAddBody struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Key      string `json:"key"`
}

// readNext is the next step for a read that failed.
func readNext(st blackbox.StateInfo) string {
	switch st.State {
	case blackbox.SNotFound:
		return "Check the folder, share or address, and that a Blackbox is set up there."
	case blackbox.SUnreachable:
		switch st.Reason {
		case "key":
			return "Type the read key again. It is shown once, when the Blackbox is installed on its server."
		case "wait":
			return "Wait a minute, then try again. Too many wrong keys were tried from this PC."
		case "busy":
			return "Try again in a minute. The Blackbox is serving as many readers as it allows."
		case "newer-format":
			return "Update Unravis to a version that matches the Blackbox."
		}
		return "Check that the server is on, the Blackbox service is running, and that this PC can reach the share or port."
	}
	return ""
}

func failFromLook(w http.ResponseWriter, l bbLook) {
	st := l.State
	msg := st.Detail
	var re *blackbox.ReadError
	if errors.As(l.Err, &re) {
		msg = re.Message
	}
	writeJSON(w, http.StatusUnprocessableEntity, bbFailure{Error: msg, Reason: bbReason(st), Next: readNext(st), State: &st}, 8192)
}

// bbInvalid is the refusal for settings that failed Validate. A folder or a listen address a person typed wrong
// also carries a plain sentence (What), so the page never shows the file name or the field name.
func bbInvalid(err error) bbFailure {
	return bbFailure{Error: err.Error(), Reason: blackbox.ReasonInvalid, What: blackbox.PlainReason(err)}
}

func bbReason(st blackbox.StateInfo) string {
	if st.Reason != "" {
		return st.Reason
	}
	return string(st.State)
}

func bbDuplicate(e bbsaved.Entry, it bbItem) bbFailure {
	return bbFailure{Error: "That Blackbox is already in the list as " + e.Name + ". It was not added again.", Reason: "duplicate", Existing: &it}
}

// bbAdd adds a Blackbox (save true) or only tries it (save false). A Blackbox that
// cannot be read is never saved: the add fails with the reason and nothing is kept.
func (h *handler) bbAdd(w http.ResponseWriter, r *http.Request, save bool) {
	if !h.bbReady(w) {
		return
	}
	b := h.bb
	var body bbAddBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if !b.probes.allow() {
		writeJSON(w, http.StatusTooManyRequests, bbFailure{Error: "Too many tries in a minute.", Reason: "rate", Next: "Wait a minute and try again."}, 4096)
		return
	}
	loc, err := blackbox.ParseLocation(body.Location)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, bbFailure{Error: err.Error(), Reason: "location"}, 4096)
		return
	}
	if save {
		// The same place typed again is a duplicate whatever key came with it, so it is answered before the key is tried.
		if e, found := b.deps.Store.Find(loc.Canonical(), "", ""); found {
			it := b.item(e, b.lookEntry(r.Context(), e, nil))
			writeJSON(w, http.StatusConflict, bbDuplicate(e, it), 16384)
			return
		}
	}
	key := strings.TrimSpace(body.Key)
	if loc.NeedsKey() && key == "" {
		writeJSON(w, http.StatusUnprocessableEntity, bbFailure{Error: "Type the read key for this address.", Reason: "key", Next: readNext(blackbox.StateInfo{State: blackbox.SUnreachable, Reason: "key"})}, 4096)
		return
	}
	name := strings.TrimSpace(body.Name)
	if len(name) > bbMaxName {
		writeJSON(w, http.StatusUnprocessableEntity, bbFailure{Error: "The name is too long.", Reason: "invalid"}, 4096)
		return
	}
	l := b.lookAt(r.Context(), b.open(loc, key), true)
	if l.Err != nil {
		failFromLook(w, l)
		return
	}
	if name == "" {
		name = l.Reading.Manifest.Name
	}
	if !save {
		writeJSON(w, http.StatusOK, struct {
			OK    bool               `json:"ok"`
			Name  string             `json:"name"`
			State blackbox.StateInfo `json:"state"`
			Facts *bbFacts           `json:"facts"`
		}{true, name, l.State, b.facts(l.Reading, l.Report)}, 16384)
		return
	}
	e, err := b.deps.Store.Add(bbsaved.AddInput{
		Name: name, Location: loc, Key: key,
		RecorderHost: l.Reading.Manifest.Host, RecorderName: l.Reading.Manifest.Name,
	})
	switch {
	case errors.Is(err, bbsaved.ErrDuplicate):
		it := b.item(e, b.lookEntry(r.Context(), e, nil))
		writeJSON(w, http.StatusConflict, bbDuplicate(e, it), 16384)
		return
	case err != nil:
		bbStoreFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, b.item(e, l), 16384)
}

func (h *handler) bbCheck(w http.ResponseWriter, r *http.Request, id string) {
	if !h.bbReady(w) {
		return
	}
	e, err := h.bb.deps.Store.Get(id)
	if err != nil {
		bbStoreFailure(w, err)
		return
	}
	found, _ := h.bb.scan()
	writeJSON(w, http.StatusOK, h.bb.item(e, h.bb.lookEntry(r.Context(), e, found)), 16384)
}

type bbKeyBody struct {
	Key string `json:"key"`
}

// bbSetKey replaces the sealed key of a URL entry. The new key is tried first, so a
// wrong one never replaces a good one.
func (h *handler) bbSetKey(w http.ResponseWriter, r *http.Request, id string) {
	if !h.bbReady(w) {
		return
	}
	b := h.bb
	var body bbKeyBody
	if !decodeJSON(w, r, &body) {
		return
	}
	e, err := b.deps.Store.Get(id)
	if err != nil {
		bbStoreFailure(w, err)
		return
	}
	key := strings.TrimSpace(body.Key)
	if e.Kind != blackbox.LocURL || key == "" {
		writeJSON(w, http.StatusUnprocessableEntity, bbFailure{Error: "Only a Blackbox reached by an address has a read key, and the key cannot be empty.", Reason: "invalid"}, 4096)
		return
	}
	if !b.probes.allow() {
		writeJSON(w, http.StatusTooManyRequests, bbFailure{Error: "Too many tries in a minute.", Reason: "rate", Next: "Wait a minute and try again."}, 4096)
		return
	}
	loc, err := blackbox.ParseLocation(e.Location)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, bbFailure{Error: err.Error(), Reason: "location"}, 4096)
		return
	}
	l := b.lookAt(r.Context(), b.open(loc, key), true)
	if l.Err != nil {
		failFromLook(w, l)
		return
	}
	if err := b.deps.Store.SetKey(id, key); err != nil {
		bbStoreFailure(w, err)
		return
	}
	e, _ = b.deps.Store.Get(id)
	writeJSON(w, http.StatusOK, b.item(e, l), 16384)
}

// bbRevealKeyAnswer is the one place a read key leaves the sealed list: the answer to
// a press of Show or Copy on a Blackbox installed on this PC (A33, wireframe 65).
type bbRevealKeyAnswer struct {
	Key string `json:"key"`
}

// bbRevealKey answers POST /api/blackbox/{id}/reveal-key with the sealed read key of
// the Blackbox that this Unravis installed on this PC, so its owner can give the key
// to another PC's Unravis. It is a POST, never cached, never logged and never kept: the
// page holds the key only while it is on view. A key the owner typed for another
// server is not given back, and neither is any key of a folder or share (none exists).
func (h *handler) bbRevealKey(w http.ResponseWriter, _ *http.Request, id string) {
	if !h.bbReady(w) {
		return
	}
	e, err := h.bb.deps.Store.Get(id)
	if err != nil {
		bbStoreFailure(w, err)
		return
	}
	if !e.ThisPC {
		writeJSON(w, http.StatusForbidden, bbFailure{Error: "A read key is shown only for the Blackbox installed on this PC.", Reason: "not-this-pc"}, 4096)
		return
	}
	key, err := h.bb.deps.Store.Key(id)
	if err != nil {
		bbStoreFailure(w, err)
		return
	}
	if key == "" {
		writeJSON(w, http.StatusNotFound, bbFailure{Error: "This Blackbox has no read key saved.", Reason: "no-key", Next: "Remove it from this PC and install it again to make a new key."}, 4096)
		return
	}
	writeJSON(w, http.StatusOK, bbRevealKeyAnswer{Key: key}, 1024)
}

// bbRemoveEntry takes a Blackbox out of the list and drops its sealed key. The
// Blackbox itself, on its server, and its recordings are not touched.
func (h *handler) bbRemoveEntry(w http.ResponseWriter, _ *http.Request, id string) {
	if !h.bbReady(w) {
		return
	}
	if err := h.bb.deps.Store.Remove(id); err != nil {
		bbStoreFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Removed bool   `json:"removed"`
		Note    string `json:"note"`
	}{true, "Removed from the list. The Blackbox and its recordings were not touched."}, 4096)
}

/* ---------------------------------------------------------- pack, check */

// bbConfigBody reads settings over the defaults, so a field that is not sent keeps
// its default.
func bbConfigBody(w http.ResponseWriter, r *http.Request) (blackbox.Config, bool) {
	cfg := blackbox.Default()
	if !decodeJSON(w, r, &cfg) {
		return cfg, false
	}
	return cfg, true
}

func (h *handler) bbValidate(w http.ResponseWriter, r *http.Request) {
	if !h.bbReady(w) {
		return
	}
	cfg, ok := bbConfigBody(w, r)
	if !ok {
		return
	}
	if err := cfg.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, bbInvalid(err), 4096)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{true}, 1024)
}

// bbPack builds the pack zip for the settings: the program, blackbox.json and a
// README. No read key is in it: the key is made on the server at install.
func (h *handler) bbPack(w http.ResponseWriter, r *http.Request) {
	if !h.bbReady(w) {
		return
	}
	cfg, ok := bbConfigBody(w, r)
	if !ok {
		return
	}
	if err := cfg.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, bbInvalid(err), 4096)
		return
	}
	if h.bb.deps.Program == nil {
		writeJSON(w, http.StatusUnprocessableEntity, bbFailure{Error: blackbox.ErrNoProgram.Error(), Reason: blackbox.ReasonNoProgram}, 4096)
		return
	}
	var buf bytes.Buffer
	info, err := blackbox.BuildPack(&buf, cfg, h.bb.deps.Program)
	if err != nil {
		reason := blackbox.ReasonInvalid
		if errors.Is(err, blackbox.ErrNoProgram) {
			reason = blackbox.ReasonNoProgram
		}
		writeJSON(w, http.StatusUnprocessableEntity, bbFailure{Error: err.Error(), Reason: reason}, 4096)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+info.FileName+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

/* ------------------------------------------------------------ this PC */

func (h *handler) bbPC(w http.ResponseWriter) (BlackboxPC, bool) {
	if !h.bbReady(w) {
		return nil, false
	}
	if h.bb.deps.ThisPC == nil {
		writeJSON(w, http.StatusServiceUnavailable, bbFailure{
			Error: "This build cannot change the Blackbox on this PC.", Reason: "no-helper",
			Next: "Use the pack and set the Blackbox up on the server by hand.",
		}, 4096)
		return nil, false
	}
	return h.bb.deps.ThisPC, true
}

// bbLockPC lets one change to this PC run at a time. A second press while one is
// running is refused, not queued: the person would otherwise get two permission
// prompts.
func (h *handler) bbLockPC(w http.ResponseWriter) bool {
	if !h.bb.pcMu.TryLock() {
		writeJSON(w, http.StatusConflict, bbFailure{Error: "Another change to the Blackbox on this PC is still running.", Reason: "busy-here", Next: "Wait for it to finish."}, 4096)
		return false
	}
	return true
}

func bbPCFailure(w http.ResponseWriter, err error) {
	var te *blackbox.ThisPCError
	if errors.As(err, &te) {
		what, next := blackbox.FailureWords(te.Reason)
		writeJSON(w, http.StatusUnprocessableEntity, bbFailure{Error: te.Message, Reason: te.Reason, What: what, Next: next}, 8192)
		return
	}
	what, next := blackbox.FailureWords("other")
	writeJSON(w, http.StatusInternalServerError, bbFailure{Error: what, Reason: "other", What: what, Next: next}, 4096)
}

// thisPCEntry is the list entry that stands for the Blackbox on this PC.
func (b *blackboxHub) thisPCEntry() (bbsaved.Entry, bool) {
	es, err := b.deps.Store.List()
	if err != nil {
		return bbsaved.Entry{}, false
	}
	for _, e := range es {
		if e.ThisPC {
			return e, true
		}
	}
	return bbsaved.Entry{}, false
}

func (h *handler) bbInstall(w http.ResponseWriter, r *http.Request) {
	pc, ok := h.bbPC(w)
	if !ok {
		return
	}
	cfg, ok := bbConfigBody(w, r)
	if !ok {
		return
	}
	if err := cfg.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, bbInvalid(err), 4096)
		return
	}
	if !h.bbLockPC(w) {
		return
	}
	defer h.bb.pcMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), bbActionTimeout)
	defer cancel()
	out, err := pc.Install(ctx, cfg)
	if err != nil {
		bbPCFailure(w, err)
		return
	}
	// The read key exists now and is known only here. It is sealed at once under
	// the loopback address and dropped; it is never in an answer.
	key := out.ReadKey
	out.ReadKey = ""
	loc, perr := blackbox.ParseLocation("http://127.0.0.1:" + strconv.Itoa(out.Port))
	var e bbsaved.Entry
	var serr error
	if perr == nil {
		e, serr = h.bb.deps.Store.Add(bbsaved.AddInput{Name: out.Name, Location: loc, Key: key, ThisPC: true, RecorderName: out.Name})
		if errors.Is(serr, bbsaved.ErrDuplicate) {
			// Already in the list (added by hand before): keep that entry, give it the
			// key the new install made and mark it as run by this PC.
			serr = h.bb.deps.Store.SetKey(e.ID, key)
			if serr == nil {
				serr = h.bb.deps.Store.SetThisPC(e.ID, true)
			}
		}
	} else {
		serr = perr
	}
	key = ""
	if serr != nil {
		writeJSON(w, http.StatusInternalServerError, bbFailure{
			Error:  "The Blackbox was installed on this PC, but Unravis could not save its read key.",
			Reason: "key-not-saved",
			Next:   "Remove the Blackbox from this PC and install it again.",
		}, 4096)
		return
	}
	g, _ := h.bb.deps.Store.Get(e.ID)
	writeJSON(w, http.StatusCreated, struct {
		Installed bool   `json:"installed"`
		Name      string `json:"name"`
		Folder    string `json:"folder"`
		Port      int    `json:"port"`
		Entry     bbItem `json:"entry"`
	}{true, out.Name, out.Folder, out.Port, bbItem{Entry: g, State: blackbox.DeriveState(blackbox.StateInput{Placed: true, Err: readFail(blackbox.ReadUnreachable, "The Blackbox has just been installed and has not answered yet. Press Check in a few seconds.")})}}, 16384)
}

func (h *handler) bbStartStop(w http.ResponseWriter, r *http.Request, start bool) {
	pc, ok := h.bbPC(w)
	if !ok {
		return
	}
	if !h.bbLockPC(w) {
		return
	}
	defer h.bb.pcMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), bbActionTimeout)
	defer cancel()
	var err error
	if start {
		err = pc.Start(ctx)
	} else {
		err = pc.Stop(ctx)
	}
	if err != nil {
		bbPCFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		OK      bool `json:"ok"`
		Started bool `json:"started"`
	}{true, start}, 1024)
}

func (h *handler) bbApply(w http.ResponseWriter, r *http.Request) {
	pc, ok := h.bbPC(w)
	if !ok {
		return
	}
	cfg, ok := bbConfigBody(w, r)
	if !ok {
		return
	}
	if err := cfg.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, bbInvalid(err), 4096)
		return
	}
	if !h.bbLockPC(w) {
		return
	}
	defer h.bb.pcMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), bbActionTimeout)
	defer cancel()
	restarted, err := pc.Apply(ctx, cfg)
	if err != nil {
		bbPCFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		OK        bool `json:"ok"`
		Restarted bool `json:"restarted"`
	}{true, restarted}, 1024)
}

type bbRemoveBody struct {
	DeleteRecordings bool   `json:"deleteRecordings"`
	ConfirmName      string `json:"confirmName"`
}

// bbRemoveThisPC removes the service from this PC. The recordings stay unless the
// person chose to delete them and typed the Blackbox name. The entry and its
// sealed key leave the list either way: a new install makes a new key.
func (h *handler) bbRemoveThisPC(w http.ResponseWriter, r *http.Request) {
	pc, ok := h.bbPC(w)
	if !ok {
		return
	}
	var body bbRemoveBody
	if !decodeJSON(w, r, &body) {
		return
	}
	entry, listed := h.bb.thisPCEntry()
	name := ""
	if listed {
		name = entry.RecorderName
		if name == "" {
			name = entry.Name
		}
	}
	if name == "" {
		if found, err := h.bb.scan(); err == nil && found != nil {
			name = found.Name
		}
	}
	if name == "" {
		what, next := blackbox.FailureWords(blackbox.ReasonNotInstall)
		writeJSON(w, http.StatusUnprocessableEntity, bbFailure{Error: what, Reason: blackbox.ReasonNotInstall, What: what, Next: next}, 4096)
		return
	}
	if !h.bbLockPC(w) {
		return
	}
	defer h.bb.pcMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), bbActionTimeout)
	defer cancel()
	out, err := pc.Remove(ctx, name, body.DeleteRecordings, body.ConfirmName)
	if err != nil {
		bbPCFailure(w, err)
		return
	}
	if listed {
		_ = h.bb.deps.Store.Remove(entry.ID)
	}
	writeJSON(w, http.StatusOK, struct {
		ServiceRemoved    bool `json:"serviceRemoved"`
		RecordingsDeleted bool `json:"recordingsDeleted"`
		FilesDeleted      int  `json:"filesDeleted"`
	}{out.ServiceRemoved, out.RecordingsDeleted, out.FilesDeleted}, 4096)
}

func (h *handler) bbSnapshot(w http.ResponseWriter, r *http.Request) {
	pc, ok := h.bbPC(w)
	if !ok {
		return
	}
	if !h.bbLockPC(w) {
		return
	}
	defer h.bb.pcMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), bbActionTimeout)
	defer cancel()
	out, err := pc.Snapshot(ctx)
	if err != nil {
		bbPCFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Dir        string `json:"dir"`
		Bytes      int64  `json:"bytes"`
		Recordings int    `json:"recordings"`
		Logs       int    `json:"logs"`
	}{out.Dir, out.Bytes, out.Recordings, out.Logs}, 4096)
}

/* -------------------------------------------------------- host reading */

type bbWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func (w bbWindow) valid() bool {
	return !w.Start.IsZero() && w.End.After(w.Start) && w.End.Sub(w.Start) <= bbMaxSpan
}

func (w bbWindow) engine() investigate.Window {
	return investigate.Window{Start: w.Start.UTC(), End: w.End.UTC()}
}

type bbHostBody struct {
	Incident  bbWindow `json:"incident"`
	Reference bbWindow `json:"reference"`
}

type bbReadSummary struct {
	Files   int      `json:"files"`
	Samples int      `json:"samples"`
	Torn    int      `json:"torn"`
	Dropped []string `json:"dropped"`
	Missing []string `json:"missing"`
}

type bbHostAnswer struct {
	Step       investigate.Step   `json:"step"`
	H1         investigate.Tri    `json:"h1"`
	Gaps       []investigate.Span `json:"gaps"`
	Onset      *time.Time         `json:"onset,omitempty"`
	TopProcess string             `json:"topProcess,omitempty"`
	Covered    int                `json:"covered"`
	Note       string             `json:"note,omitempty"`
	Read       bbReadSummary      `json:"read"`
	State      blackbox.StateInfo `json:"state"`
}

// hostRead is a Blackbox read for the two windows, ready for the engine.
type hostRead struct {
	Input  investigate.HostInput
	Report blackbox.ReadReport
	State  blackbox.StateInfo
}

func hostSamples(in []blackbox.Sample) []investigate.HostSample {
	out := make([]investigate.HostSample, 0, len(in))
	for _, s := range in {
		hs := investigate.HostSample{T: s.T.UTC()}
		if s.CPU != nil {
			v := *s.CPU
			hs.CPU = &v
		}
		for _, p := range s.Procs {
			hp := investigate.HostProc{Name: p.Name}
			if p.CPUPct != nil {
				v := *p.CPUPct
				hp.CPUPct = &v
			}
			hs.Procs = append(hs.Procs, hp)
		}
		out = append(out, hs)
	}
	return out
}

func addNames(dst []string, src []string) []string {
	for _, n := range src {
		dup := false
		for _, d := range dst {
			if d == n {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, n)
		}
	}
	return dst
}

// readHost reads the samples of the windows from a saved Blackbox. A window that
// cannot be read in full leaves a gap in the samples, and the gap rule (A35) in the
// engine decides what that means; nothing is filled in.
func (b *blackboxHub) readHost(ctx context.Context, e bbsaved.Entry, windows ...investigate.Window) (hostRead, error) {
	ctx, cancel := context.WithTimeout(ctx, bbReadTimeout)
	defer cancel()
	src, err := b.source(e)
	if err != nil {
		return hostRead{}, err
	}
	rd, err := blackbox.ProbeSource(ctx, src, b.now)
	if err != nil {
		return hostRead{}, err
	}
	var all []blackbox.Sample
	var rep blackbox.ReadReport
	for _, w := range windows {
		if !w.Valid() {
			continue
		}
		part, r, rerr := blackbox.ReadSamples(ctx, src, rd.Manifest, w.Start, w.End)
		if rerr != nil {
			return hostRead{}, rerr
		}
		all = append(all, part...)
		rep.Files += r.Files
		rep.Torn = addNames(rep.Torn, r.Torn)
		rep.Dropped = addNames(rep.Dropped, r.Dropped)
		rep.Missing = addNames(rep.Missing, r.Missing)
		rep.Newer = rep.Newer || r.Newer
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].T.Before(all[j].T) })
	kept := all[:0]
	for i, s := range all {
		if i > 0 && s.T.Equal(all[i-1].T) && s.Run == all[i-1].Run {
			continue
		}
		kept = append(kept, s)
	}
	rep.Samples = len(kept)
	hi := investigate.HostInput{Name: rd.Manifest.Name, IntervalSeconds: rd.Manifest.IntervalSeconds, Samples: hostSamples(kept)}
	if rd.Manifest.FirstSample != nil {
		hi.First = rd.Manifest.FirstSample.UTC()
	}
	if rd.Manifest.LastSample != nil {
		hi.Last = rd.Manifest.LastSample.UTC()
	}
	rp := rep
	return hostRead{Input: hi, Report: rep, State: blackbox.DeriveState(blackbox.StateInput{Placed: true, Reading: &rd, Report: &rp})}, nil
}

func summarize(r blackbox.ReadReport) bbReadSummary {
	s := bbReadSummary{Files: r.Files, Samples: r.Samples, Torn: len(r.Torn), Dropped: r.Dropped, Missing: r.Missing}
	if s.Dropped == nil {
		s.Dropped = []string{}
	}
	if s.Missing == nil {
		s.Missing = []string{}
	}
	return s
}

// bbHostReading reads one saved Blackbox for the two windows and answers what Step 9
// (the host) says, with the gap warning.
func (h *handler) bbHostReading(w http.ResponseWriter, r *http.Request, id string) {
	if !h.bbReady(w) {
		return
	}
	b := h.bb
	var body bbHostBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if !body.Incident.valid() || !body.Reference.valid() {
		writeError(w, http.StatusBadRequest, "Request validation failed.")
		return
	}
	e, err := b.deps.Store.Get(id)
	if err != nil {
		bbStoreFailure(w, err)
		return
	}
	in := investigate.Input{Incident: body.Incident.engine(), Reference: body.Reference.engine()}
	hr, err := b.readHost(r.Context(), e, in.Incident, in.Reference)
	if err != nil {
		st := blackbox.DeriveState(blackbox.StateInput{Placed: true, Err: err})
		failFromLook(w, bbLook{Err: err, State: st})
		return
	}
	res := in.ApplyHost(hr.Input)
	step := investigate.Analyze(in).Steps[9]
	if step.UntestedSpans == nil {
		step.UntestedSpans = []investigate.Span{}
	}
	gaps := res.Gaps
	if gaps == nil {
		gaps = []investigate.Span{}
	}
	ans := bbHostAnswer{
		Step: step, H1: res.Lanes["H1"], Gaps: gaps, Onset: res.Onset, TopProcess: res.TopProcess,
		Covered: res.Covered, Note: res.Note, Read: summarize(hr.Report), State: hr.State,
	}
	if ans.H1 == "" {
		ans.H1 = investigate.Unknown
	}
	writeJSON(w, http.StatusOK, ans, maxResponseBytes)
}

/* ------------------------------------------------------------ the case */

// blackboxTagKind is the SourceTag kind of a Blackbox a case uses. The tag holds
// the Blackbox name as its label and nothing else: no address, no key.
const blackboxTagKind = "blackbox"

// applyBlackboxLane adds the host lane to an investigation when the case uses a
// Blackbox. The Blackbox is found by its name in the saved list and read with its
// sealed key on this side. A Blackbox that was removed from the list, or cannot be
// read, adds a warning and leaves the host lane unknown.
func (h *handler) applyBlackboxLane(ctx context.Context, c casestore.Case, in *investigate.Input, read *investigationRead) {
	if h.bb == nil {
		return
	}
	var inner casestore.Case = c
	if sc, wrapped := c.(*sharedCase); wrapped {
		inner = sc.Case
	}
	t, ok := inner.(casestore.Tagger)
	if !ok {
		return
	}
	tags, err := t.Sources(ctx)
	if err != nil {
		return
	}
	var names []string
	for _, tag := range tags {
		if tag.Kind != blackboxTagKind {
			continue
		}
		n := strings.TrimSpace(tag.Label)
		dup := false
		for _, x := range names {
			if strings.EqualFold(x, n) {
				dup = true
			}
		}
		if n != "" && !dup {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return
	}
	if len(names) > 1 {
		read.Warnings = append(read.Warnings, "This case uses "+strconv.Itoa(len(names))+" Blackboxes. Only the first, "+names[0]+", is read for the host in this release.")
	}
	name := names[0]
	entries, err := h.bb.deps.Store.List()
	if err != nil {
		read.Warnings = append(read.Warnings, "The saved Blackbox list could not be read, so the host lane was not tested.")
		return
	}
	var entry *bbsaved.Entry
	for i := range entries {
		if strings.EqualFold(entries[i].Name, name) {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		read.Warnings = append(read.Warnings, "The Blackbox "+name+" is no longer in the Blackbox list, so the host lane was not tested. Add it again to use it.")
		return
	}
	hr, err := h.bb.readHost(ctx, *entry, in.Incident, in.Reference)
	if err != nil {
		msg := "The Blackbox " + name + " could not be read, so the host lane was not tested."
		var re *blackbox.ReadError
		if errors.As(err, &re) {
			msg += " " + re.Message
		}
		read.Warnings = append(read.Warnings, msg)
		return
	}
	if hr.Report.Incomplete() {
		read.Warnings = append(read.Warnings, "Part of the Blackbox "+name+" could not be read to the end. What was read is used, and the rest counts as a gap.")
	}
	in.ApplyHost(hr.Input)
}
