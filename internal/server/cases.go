package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"log"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/hosts"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/ledger"
)

// Limits of the case routes.
const (
	// uploadSlack is added to the byte cap of a file for the multipart framing
	// (boundaries, headers and the small form fields).
	uploadSlack = 1 << 20
	// ackTooLongText is the answer to an acknowledgement over casestore.MaxAckText,
	// the same cap the store enforces.
	ackTooLongText = "The acknowledgement text is longer than 4 KiB."
	// ackMismatchText is the answer to an acknowledgement that is neither the
	// banner text shown nor marked custom. It never repeats the text sent.
	ackMismatchText = "The acknowledgement does not match the notice shown. Reload the page and try again."
	// maxFileNameBytes bounds the display name kept for an upload.
	maxFileNameBytes = 128
	// maxEventFilters bounds the values of one event filter.
	maxEventFilters = 50
	// erasedReference replaces a reference to an erased case in the ledger. It
	// has the shape of an evidence ID so the entry stays editable.
	erasedReference = "erased-case-evidence"
)

// errPreviewDone ends a preview import after a full, successful parse. The store
// stores nothing when the run function returns an error, so this is the
// roll-back.
var errPreviewDone = errors.New("preview complete")

// previewFailure carries the error of a failed preview run through the store.
// It has no Unwrap on purpose: the store then sees an opaque failure and keeps
// no quarantine record and writes no log line for a file that was only previewed.
// importFile reads the cause back out.
type previewFailure struct{ cause error }

func (p *previewFailure) Error() string { return "preview failed" }

// casesState is the part of the handler that the case routes use. It is a
// separate struct so server.go needs one line for it.
type casesState struct {
	cases     casestore.Store
	registry  *importer.Registry
	hostsSave func(*hosts.Map) error
	// recoverer runs the recovery hooks of marked cases (see casestore.Recoverer).
	// nil means no hook is registered and nothing is ever marked.
	recoverer  *casestore.Recoverer
	archiveRec *ArchiveRecovery // the archive hooks, so a live job is told from a dead one
	gates      caseGates
	imports    importLimiter
	pending    pendingStrips
	ledgers    caseLedgerLocks
	wsLocks    caseLedgerLocks // serialises writes of each case's workspace note
	jobs       importJobs
	disc       discoverState
	keyTh      caseThrottles // failure throttles of the per-case keys (case_key.go)
	keyOp      sync.Mutex    // one case-key derivation (Argon2) at a time
	erased     ErasedLog     // the Erased lines of the start screen (erased_log.go); nil keeps none
	// eraseWait is how long Erase waits for the case to be free (A16); eraseAfter makes the timer. Tests replace the timer.
	eraseWait  time.Duration
	eraseAfter func(time.Duration) <-chan time.Time
	maxUpload  int64 // test hook: replaces the computed upload cap when > 0

	hostsMu sync.RWMutex
	hostMap *hosts.Map
}

// WithCases attaches the case store, the host map and the importer registry.
// Without it the case and host routes answer 503. hostMap may be nil (no hosts
// yet); PUT /api/hosts replaces it when WithHostsSaver is also given.
func WithCases(store casestore.Store, hostMap *hosts.Map, registry *importer.Registry) Option {
	return func(h *handler) {
		h.cases, h.hostMap, h.registry = store, hostMap, registry
	}
}

// WithRecoverer gives the server the marks-and-recovery framework. The server
// runs it at unlock (before the first reply that follows) and at the start of a
// call that touches a marked case. Start-up recovery is the caller's.
func WithRecoverer(r *casestore.Recoverer) Option {
	return func(h *handler) { h.recoverer = r }
}

// recoverAll repairs every marked case. Called after the master key is unlocked
// and before the unlock reply, so the first reply already tells the truth. A
// failure is logged; it never fails the unlock.
func (h *handler) recoverAll(ctx context.Context) {
	if h.recoverer == nil {
		return
	}
	rep, err := h.recoverer.RecoverAll(ctx)
	if err != nil {
		log.Printf("Case recovery after unlock did not run: %v", err)
		return
	}
	if rep.Marked > 0 {
		log.Printf("Case recovery after unlock: %d marked, %d finished, %d need another try", rep.Marked, rep.Recovered, len(rep.Failed))
	}
}

// WithHostsSaver sets how PUT /api/hosts persists a new host map (the sealed
// hosts file). Without it PUT answers 503 hosts-readonly.
func WithHostsSaver(save func(*hosts.Map) error) Option {
	return func(h *handler) { h.hostsSave = save }
}

// caseSubpath matches /api/cases/{id}<suffix> where {id} is one non-empty path
// segment (no slash, even after decoding) and returns it. The id is not
// validated here; the handler does that before anything else.
func caseSubpath(suffix string) func(string) (string, bool) {
	return func(path string) (string, bool) {
		rest, ok := strings.CutPrefix(path, "/api/cases/")
		if !ok {
			return "", false
		}
		id, ok := strings.CutSuffix(rest, suffix)
		if !ok || id == "" || strings.Contains(id, "/") {
			return "", false
		}
		return id, true
	}
}

// caseGates serialises imports (and erase) per case, and lets reads share a
// case with each other but never with an erase: an erase waits for the reads in
// flight to finish, and a read that starts while an erase is pending is told the
// case is gone.
type caseGates struct {
	mu sync.Mutex
	m  map[casestore.CaseID]*caseGate
}

type caseGate struct {
	ch      chan struct{} // the exclusive slot: one import, close or erase at a time
	refs    int           // holders and waiters of any kind; the gate is dropped at zero
	readers int           // reads in flight
	erasing bool          // an erase holds the slot and is draining readers
	idle    chan struct{} // closed when the last reader leaves while erasing
}

// ref returns the case's gate with one more reference. The caller holds g.mu.
func (g *caseGates) ref(id casestore.CaseID) *caseGate {
	if g.m == nil {
		g.m = map[casestore.CaseID]*caseGate{}
	}
	gate := g.m[id]
	if gate == nil {
		gate = &caseGate{ch: make(chan struct{}, 1)}
		g.m[id] = gate
	}
	gate.refs++
	return gate
}

// unref drops one reference. The caller holds g.mu.
func (g *caseGates) unref(id casestore.CaseID, gate *caseGate) {
	gate.refs--
	if gate.refs == 0 {
		delete(g.m, id)
	}
}

func (g *caseGates) acquire(ctx context.Context, id casestore.CaseID) (func(), error) {
	release, _, err := g.acquireGate(ctx, id)
	return release, err
}

func (g *caseGates) acquireGate(ctx context.Context, id casestore.CaseID) (func(), *caseGate, error) {
	g.mu.Lock()
	gate := g.ref(id)
	g.mu.Unlock()
	leave := func() {
		g.mu.Lock()
		g.unref(id, gate)
		g.mu.Unlock()
	}
	select {
	case gate.ch <- struct{}{}:
	case <-ctx.Done():
		leave()
		return nil, nil, ctx.Err()
	}
	return func() {
		<-gate.ch
		leave()
	}, gate, nil
}

// acquireRead takes a read share of the case. It does not wait for an import,
// and it fails (false) while an erase is pending or running, so an erase cannot
// be overtaken by a read that opened the case first. The caller releases the
// share exactly once.
func (g *caseGates) acquireRead(id casestore.CaseID) (func(), bool) {
	g.mu.Lock()
	gate := g.ref(id)
	if gate.erasing {
		g.unref(id, gate)
		g.mu.Unlock()
		return nil, false
	}
	gate.readers++
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		gate.readers--
		if gate.readers == 0 && gate.idle != nil {
			close(gate.idle)
			gate.idle = nil
		}
		g.unref(id, gate)
		g.mu.Unlock()
	}, true
}

// acquireErase takes the exclusive slot, then blocks new reads and waits for
// the reads in flight to finish before it returns. It waits as long as it takes.
func (g *caseGates) acquireErase(ctx context.Context, id casestore.CaseID) (func(), error) {
	return g.acquireEraseWithin(ctx, id, nil)
}

// errCaseInUse is M4: the erase gave up waiting for the case to be free.
var errCaseInUse = errors.New("the case is in use")

// acquireEraseWithin is acquireErase with a deadline: when timeout fires before the
// slot is taken and the readers have left, the refusal of new readers is lifted, the
// slot is given back and errCaseInUse is returned. Nothing was marked or erased. A nil
// timeout waits for ever.
func (g *caseGates) acquireEraseWithin(ctx context.Context, id casestore.CaseID, timeout <-chan time.Time) (func(), error) {
	g.mu.Lock()
	gate := g.ref(id)
	g.mu.Unlock()
	leave := func() {
		g.mu.Lock()
		g.unref(id, gate)
		g.mu.Unlock()
	}
	select {
	case gate.ch <- struct{}{}:
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	case <-timeout:
		leave()
		return nil, errCaseInUse
	}
	release := func() {
		<-gate.ch
		leave()
	}
	g.mu.Lock()
	gate.erasing = true
	var wait chan struct{}
	if gate.readers > 0 {
		gate.idle = make(chan struct{})
		wait = gate.idle
	}
	g.mu.Unlock()
	done := func() {
		g.mu.Lock()
		gate.erasing = false
		g.mu.Unlock()
		release()
	}
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			done()
			g.mu.Lock()
			gate.idle = nil
			g.mu.Unlock()
			return nil, ctx.Err()
		case <-timeout:
			done()
			g.mu.Lock()
			gate.idle = nil
			g.mu.Unlock()
			return nil, errCaseInUse
		}
	}
	return done, nil
}

// sharedCase releases the case's read share when the handle is closed, so a
// handler keeps its single defer c.Close().
type sharedCase struct {
	casestore.Case
	once    sync.Once
	release func()
}

func (s *sharedCase) Close() error {
	err := s.Case.Close()
	s.once.Do(s.release)
	return err
}

// defaultMaxImports is how many imports may parse at once across all cases. Each
// one holds a case database open and parses a whole file; a few more would only
// slow every other import down.
const defaultMaxImports = 2

// importLimiter bounds the imports that run at once. The zero value allows
// defaultMaxImports; max > 0 overrides it (a test hook).
type importLimiter struct {
	mu      sync.Mutex
	max     int
	running int
}

// tryAcquire takes a slot without waiting. A caller that gets no slot answers
// "busy" and the client retries.
func (l *importLimiter) tryAcquire() (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	limit := l.max
	if limit <= 0 {
		limit = defaultMaxImports
	}
	if l.running >= limit {
		return nil, false
	}
	l.running++
	return func() {
		l.mu.Lock()
		l.running--
		l.mu.Unlock()
	}, true
}

// pendingStrips holds the ids of cases whose erase has started and whose ledger
// strip has not finished. A repeat erase of such an id still runs, so a retry
// after a ledger failure completes the job; any other missing id is a 404.
type pendingStrips struct {
	mu  sync.Mutex
	ids map[casestore.CaseID]struct{}
}

func (p *pendingStrips) has(id casestore.CaseID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.ids[id]
	return ok
}

func (p *pendingStrips) add(id casestore.CaseID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ids == nil {
		p.ids = map[casestore.CaseID]struct{}{}
	}
	p.ids[id] = struct{}{}
}

func (p *pendingStrips) remove(id casestore.CaseID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.ids, id)
}

// caseFailure is the JSON body of a refused case request. Reason is a fixed
// word (an importer Reason or an API reason); no field ever holds file content.
type caseFailure struct {
	Error    string `json:"error"`
	Reason   string `json:"reason,omitempty"`
	Column   string `json:"column,omitempty"`
	ImportID int64  `json:"importId,omitempty"`
}

// untrustedCaseText is the message for a Windows sealed case folder that was not on
// this PC when the master key was set up.
const untrustedCaseText = "This case folder is not protected by the master key and was not on this PC when the master key was set up, so it is not opened. You can erase it."

func writeCaseFailure(w http.ResponseWriter, status int, reason, message string) {
	writeJSON(w, status, caseFailure{Error: message, Reason: reason}, 4096)
}

func (h *handler) casesAvailable(w http.ResponseWriter) bool {
	if h.cases == nil {
		writeCaseFailure(w, http.StatusServiceUnavailable, "cases-unavailable", "Cases are unavailable.")
		return false
	}
	return true
}

// caseIDOf validates the id from the path before anything else uses it.
func caseIDOf(w http.ResponseWriter, raw string) (casestore.CaseID, bool) {
	id, err := casestore.ParseID(raw)
	if err != nil {
		writeCaseFailure(w, http.StatusBadRequest, "bad-case-id", "That is not a case id.")
		return "", false
	}
	return id, true
}

// writeStoreError maps a store failure outside an import.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	// An archived case is a kind of not-found to the store, so M6 is listed first.
	case errors.Is(err, casestore.ErrArchived):
		writeCaseFailure(w, http.StatusConflict, "case-archived", caseArchivedText)
	case errors.Is(err, casestore.ErrCaseInUse):
		writeCaseFailure(w, http.StatusConflict, "case-in-use", caseInUseText)
	case errors.Is(err, casestore.ErrNotFound):
		writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
	case errors.Is(err, casestore.ErrBadID):
		writeCaseFailure(w, http.StatusBadRequest, "bad-case-id", "That is not a case id.")
	case errors.Is(err, casestore.ErrLocked):
		writeLocked(w)
	case errors.Is(err, casestore.ErrCaseLocked):
		writeCaseLocked(w)
	case errors.Is(err, casestore.ErrWrongCaseKey):
		writeWrongCaseKey(w)
	case errors.Is(err, casestore.ErrUntrustedLegacy):
		writeCaseFailure(w, http.StatusConflict, "untrusted-case", untrustedCaseText)
	case errors.Is(err, casestore.ErrNeedsRewrap):
		// Listed before the generic store-error: this one is repairable.
		writeCaseFailure(w, http.StatusConflict, "needs-rewrap", needsRewrapText)
	case errors.Is(err, casestore.ErrCaseClosed):
		writeCaseFailure(w, http.StatusConflict, "case-closed", "The case is closed. Nothing can be imported into it.")
	case errors.Is(err, casestore.ErrCaseChanged):
		writeCaseFailure(w, http.StatusConflict, "case-changed", caseChangedText)
	case errors.Is(err, casestore.ErrArchiveRunning):
		writeCaseFailure(w, http.StatusConflict, "archive-running", archiveRunningText)
	case errors.Is(err, casestore.ErrCancelArchiveFirst):
		writeCaseFailure(w, http.StatusConflict, "cancel-archive-first", cancelArchiveText)
	case errors.Is(err, casestore.ErrErasing):
		writeCaseFailure(w, http.StatusConflict, "case-erasing", caseErasingText)
	case errors.Is(err, errCaseInUse):
		writeCaseFailure(w, http.StatusConflict, "case-in-use", caseInUseText)
	case errors.Is(err, casestore.ErrFreeing):
		writeCaseFailure(w, http.StatusConflict, "case-freeing", caseFreeingText)
	case errors.Is(err, casestore.ErrAlreadyMade):
		writeCaseFailure(w, http.StatusConflict, "case-already-made", caseAlreadyMadeText)
	case errors.Is(err, casestore.ErrAckTooLong):
		writeCaseFailure(w, http.StatusBadRequest, "ack-too-long", ackTooLongText)
	case errors.Is(err, context.Canceled):
		writeCaseFailure(w, http.StatusBadRequest, "canceled", "The request was canceled.")
	case errors.Is(err, context.DeadlineExceeded):
		writeCaseFailure(w, http.StatusGatewayTimeout, "timeout", "The request timed out.")
	default:
		writeCaseFailure(w, http.StatusInternalServerError, "store-error", "The case store could not complete the request.")
	}
}

/* ---------------------------------------------------------------- DTOs */

type caseDTO struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Created time.Time  `json:"created"`
	Closed  *time.Time `json:"closed"`
	Bytes   int64      `json:"bytes"`
	Imports int        `json:"imports"`
	// State is the case's place in its life: open, closed, archiving, untrusted
	// (decisions A7). The page sends it back as X-Expect-State.
	State string `json:"state"`
	// Untrusted is true for a folder the store refuses (see
	// casestore.ErrUntrustedLegacy). Its title is a fixed placeholder.
	Untrusted bool `json:"untrusted,omitempty"`
	// OwnKey: the case is sealed with its own key. KeyLocked: that key is not
	// unlocked, so the case cannot be opened (its title is hidden then). KeyEnding
	// is "Key ending xxxx", the last 4 characters of the key check value.
	OwnKey    bool   `json:"ownKey,omitempty"`
	KeyLocked bool   `json:"keyLocked,omitempty"`
	KeyEnding string `json:"keyEnding,omitempty"`
	// Missing is true for a case whose working copy is gone without a Free: no
	// folder and no mark ("Working copy missing"). Only Erase, and Restore as new
	// when it was archived, are offered.
	Missing bool `json:"missing,omitempty"`
	// FilesMade is how many case files were made from this case (the home says
	// "Case files made earlier: n").
	FilesMade int         `json:"filesMade,omitempty"`
	Archive   *archiveDTO `json:"archive,omitempty"`
	// Percent is how far a running archive has got (an Archiving row only).
	Percent *int `json:"percent,omitempty"`
}

func toCaseDTO(c casestore.CaseInfo) caseDTO {
	d := caseDTO{ID: string(c.ID), Title: c.Title, Created: c.Created.UTC(), Bytes: c.Bytes, Imports: c.Imports, Untrusted: c.Untrusted, State: string(casestore.StateOf(c)),
		OwnKey: c.OwnKey, KeyLocked: c.KeyLocked, KeyEnding: c.KeyEnding,
		Missing: c.Missing, FilesMade: filesMade(c.Archive), Archive: toArchiveDTO(c.Archive, false)}
	if c.Closed != nil {
		t := c.Closed.UTC()
		d.Closed = &t
	}
	return d
}

type importRecordDTO struct {
	ID        int64                  `json:"id"`
	Importer  string                 `json:"importer"`
	FileName  string                 `json:"fileName"`
	SHA256    string                 `json:"sha256"`
	Bytes     int64                  `json:"bytes"`
	Rows      int64                  `json:"rows"`
	Rejected  int64                  `json:"rejected"`
	HostLabel string                 `json:"hostLabel"`
	TZRule    string                 `json:"tzRule"`
	Quality   evidence.SourceQuality `json:"quality"`
	KnownGaps []string               `json:"knownGaps"`
	Imported  time.Time              `json:"imported"`
	Status    string                 `json:"status"`
	Reason    string                 `json:"reason,omitempty"`
	// Supplied is what the owner supplied for this import, as an object (empty
	// when nothing was).
	Supplied map[string]string `json:"supplied"`
}

func toRecordDTO(r casestore.ImportRecord) importRecordDTO {
	gaps := r.KnownGaps
	if gaps == nil {
		gaps = []string{}
	}
	status := r.Status
	if status == "" {
		status = casestore.StatusOK
	}
	supplied := make(map[string]string, len(r.Supplied))
	for k, v := range r.Supplied {
		supplied[k] = v
	}
	return importRecordDTO{
		ID: r.ID, Importer: r.Importer, FileName: r.FileName, SHA256: r.SHA256, Bytes: r.Bytes, Rows: r.Rows,
		Rejected: r.Rejected, HostLabel: r.HostLabel, TZRule: r.TZRule, Quality: r.Quality, KnownGaps: gaps,
		Imported: r.Imported.UTC(), Status: status, Reason: string(r.Reason), Supplied: supplied,
	}
}

type rowErrorDTO struct {
	Line   int64  `json:"line"`
	Reason string `json:"reason"`
}

type reportDTO struct {
	Importer      string                 `json:"importer"`
	Rows          int64                  `json:"rows"`
	Emitted       int64                  `json:"emitted"`
	Rejected      int64                  `json:"rejected"`
	Errors        []rowErrorDTO          `json:"errors"`
	ObservedHosts []string               `json:"observedHosts"`
	First         *time.Time             `json:"first"`
	Last          *time.Time             `json:"last"`
	Quality       evidence.SourceQuality `json:"quality"`
	FieldRules    map[string]string      `json:"fieldRules"`
	KnownGaps     []string               `json:"knownGaps"`
}

func toReportDTO(r importer.Report) *reportDTO {
	d := &reportDTO{
		Importer: r.Importer, Rows: r.Rows, Emitted: r.Emitted, Rejected: r.Rejected,
		Errors: []rowErrorDTO{}, ObservedHosts: []string{}, Quality: r.Quality,
		FieldRules: map[string]string{}, KnownGaps: []string{},
	}
	for i, e := range r.Errors {
		if i >= importer.MaxReportErrors {
			break
		}
		d.Errors = append(d.Errors, rowErrorDTO{Line: e.Line, Reason: string(e.Reason)})
	}
	d.ObservedHosts = append(d.ObservedHosts, r.ObservedHosts...)
	for k, v := range r.FieldRules {
		d.FieldRules[k] = v
	}
	d.KnownGaps = append(d.KnownGaps, r.KnownGaps...)
	if !r.First.IsZero() {
		t := r.First.UTC()
		d.First = &t
	}
	if !r.Last.IsZero() {
		t := r.Last.UTC()
		d.Last = &t
	}
	return d
}

// importAnswer is what a preview or a commit answers.
type importAnswer struct {
	Importer string           `json:"importer"`
	Score    int              `json:"score"`
	Tied     []string         `json:"tied"`
	Report   *reportDTO       `json:"report"`
	Record   *importRecordDTO `json:"record"`
}

type eventDTO struct {
	EvidenceID string            `json:"evidenceId"`
	Source     string            `json:"source"`
	Kind       string            `json:"kind"`
	Host       string            `json:"host"`
	Start      time.Time         `json:"start"`
	End        time.Time         `json:"end"`
	DurationMs *float64          `json:"durationMs"`
	Value      *float64          `json:"value"`
	Unit       string            `json:"unit"`
	Severity   string            `json:"severity"`
	Line       int64             `json:"line"`
	TZRule     string            `json:"tzRule"`
	Attrs      map[string]string `json:"attrs"`
}

func toEventDTO(e evidence.Event) eventDTO {
	attrs := make(map[string]string, len(e.Attrs))
	for k, v := range e.Attrs {
		attrs[k] = v
	}
	return eventDTO{
		EvidenceID: e.EvidenceID, Source: e.Source, Kind: e.Kind, Host: e.Host, Start: e.Start.UTC(), End: e.End.UTC(),
		DurationMs: e.DurationMs, Value: e.Value, Unit: e.Unit, Severity: e.Severity, Line: e.Line, TZRule: e.TZRule, Attrs: attrs,
	}
}

/* ---------------------------------------------------------------- cases */

func (h *handler) listCases(w http.ResponseWriter, r *http.Request) {
	if !h.casesAvailable(w) {
		return
	}
	items, err := h.cases.List(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Archived cases have their own list, 50 to a page, newest archive first; the
	// default list holds every other case (Missing ones included) and carries the
	// archived total for the toggle.
	var live, kept []casestore.CaseInfo
	for _, c := range items {
		if c.Archive != nil && c.Archive.Active {
			kept = append(kept, c)
		} else {
			live = append(live, c)
		}
	}
	page, offset := live, 0
	wantArchived := r.URL.Query().Get("archived") == "1"
	if wantArchived {
		sort.SliceStable(kept, func(i, j int) bool { return archivedAt(kept[i]).After(archivedAt(kept[j])) })
		if v := r.URL.Query().Get("offset"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeCaseFailure(w, http.StatusBadRequest, "bad-request", "The request is invalid.")
				return
			}
			offset = n
		}
		if offset > len(kept) {
			offset = len(kept)
		}
		end := offset + archivedPageSize
		if end > len(kept) {
			end = len(kept)
		}
		page = kept[offset:end]
	}
	// A listing repairs only the marked rows of the page it returns.
	if h.recoverer != nil {
		page = h.recoverer.RecoverPage(r.Context(), page)
	}
	out := make([]caseDTO, 0, len(page))
	for _, c := range page {
		d := toCaseDTO(c)
		if c.Mark == casestore.MarkArchiving {
			if j := h.caseFiles().jobs.running(jobArchive, c.ID); j != nil {
				p := j.percent()
				d.Percent = &p
			}
		}
		out = append(out, d)
	}
	oldCases := 0
	if h.keeper != nil && h.keeper.OldCases != nil {
		oldCases = h.keeper.OldCases()
	}
	writeJSON(w, http.StatusOK, struct {
		Cases         []caseDTO   `json:"cases"`
		ArchivedTotal int         `json:"archivedTotal"`
		Offset        int         `json:"offset"`
		PageSize      int         `json:"pageSize"`
		Erased        []erasedDTO `json:"erased"`
		// OldCases is how many of the person's cases from before the shared layout stay in their own folder (P14, not moved).
		OldCases int `json:"oldCases,omitempty"`
	}{out, len(kept), offset, archivedPageSize, h.erasedLines(), oldCases}, maxResponseBytes)
}

// archivedPageSize is how many archived cases one page of the archived list holds.
const archivedPageSize = 50

// archivedAt is when a case was archived, for the order of the archived list.
func archivedAt(c casestore.CaseInfo) time.Time {
	if c.Archive != nil && c.Archive.At != nil {
		return *c.Archive.At
	}
	return c.Created
}

// badTitleText is the answer for a title the create rule refuses (D-A-08).
const badTitleText = "The title must be 2 to 80 characters and hold no control characters."

func (h *handler) createCase(w http.ResponseWriter, r *http.Request) {
	if !h.casesAvailable(w) {
		return
	}
	// title and requestId are text; caseKey and caseKeyConfirm (the optional own
	// key of the case) are read as bytes that never become strings.
	body := readKeyBody(w, r, []string{"caseKey", "caseKeyConfirm"}, []string{"title", "requestId"})
	if body == nil {
		return
	}
	defer body.wipe()
	// RequestID is kept by the page for one wizard run. A repeated id answers the
	// case the first request made (200) and makes no second one.
	requestID := body.strs["requestId"]
	title := strings.TrimSpace(body.strs["title"])
	if !casestore.ValidNewTitle(title) {
		writeCaseFailure(w, http.StatusBadRequest, "bad-title", badTitleText)
		return
	}
	if requestID != "" && !casestore.ValidRequestID(requestID) {
		writeCaseFailure(w, http.StatusBadRequest, "bad-request-id", badRequestIDText)
		return
	}
	var (
		info    casestore.CaseInfo
		existed bool
		err     error
	)
	if key, withKey := body.secrets["caseKey"]; withKey {
		ck, ok := h.caseKeyer(w)
		if !ok {
			return
		}
		if !newKeyChecked(w, key, body.secrets["caseKeyConfirm"]) {
			return
		}
		kc := bytes.Clone(key)
		defer clear(kc)
		info, existed, err = ck.CreateWithKey(r.Context(), title, requestID, kc)
	} else if _, stray := body.secrets["caseKeyConfirm"]; stray {
		writeCaseKeyFail(w, http.StatusBadRequest, "bad-request", "The request body is invalid.")
		return
	} else if lc, ok := h.cases.(casestore.Lifecycle); ok && requestID != "" {
		info, existed, err = lc.CreateFor(r.Context(), title, requestID)
	} else {
		info, err = h.cases.Create(r.Context(), title)
	}
	if err != nil {
		if errors.Is(err, casestore.ErrBadTitle) {
			writeCaseFailure(w, http.StatusBadRequest, "bad-title", badTitleText)
			return
		}
		writeStoreError(w, err)
		return
	}
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	writeJSON(w, status, toCaseDTO(info), maxResponseBytes)
}

// openCase opens the case for one read request, holding a read share of the
// case's gate until the handle is closed: an erase waits for it, and a case
// being erased answers 404. The caller closes the handle.
func (h *handler) openCase(w http.ResponseWriter, r *http.Request, raw string) (casestore.CaseID, casestore.Case, bool) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return "", nil, false
	}
	if !h.casesAvailable(w) {
		return "", nil, false
	}
	// A Free is waiting for the readers of this case to let go: a new one is told
	// to wait (M19) instead of joining them.
	if h.caseFiles().freeing.has(id) {
		writeStoreError(w, casestore.ErrFreeing)
		return "", nil, false
	}
	release, ok := h.gates.acquireRead(id)
	if !ok {
		// An erase is waiting for the readers in flight: new ones are told so (M5).
		writeStoreError(w, casestore.ErrErasing)
		return "", nil, false
	}
	c, err := h.cases.Open(r.Context(), id)
	if err != nil {
		release()
		writeStoreError(w, h.openFailed(r.Context(), id, err))
		return "", nil, false
	}
	// A case still marked Freeing or Erasing is repaired first; if the mark is
	// still there afterwards the call is told to wait (M19 / M5).
	if info := c.Info(); info.Mark != "" {
		if err := h.markAnswer(r.Context(), info); err != nil {
			_ = c.Close()
			release()
			writeStoreError(w, err)
			return "", nil, false
		}
	}
	return id, &sharedCase{Case: c, release: release}, true
}

// openCaseForWrite opens the case for a handler that takes the exclusive gate
// itself (import, export preview). It holds no read share: an erase holds the
// exclusive slot while it waits for readers, so a share held across that wait
// would deadlock. The exclusive slot already keeps an erase out.
func (h *handler) openCaseForWrite(w http.ResponseWriter, r *http.Request, raw string) (casestore.CaseID, casestore.Case, bool) {
	id, ok := caseIDOf(w, raw)
	if !ok {
		return "", nil, false
	}
	if !h.casesAvailable(w) {
		return "", nil, false
	}
	if h.caseFiles().freeing.has(id) {
		writeStoreError(w, casestore.ErrFreeing)
		return "", nil, false
	}
	c, err := h.cases.Open(r.Context(), id)
	if err != nil {
		writeStoreError(w, h.openFailed(r.Context(), id, err))
		return "", nil, false
	}
	if info := c.Info(); info.Mark != "" {
		if err := h.markAnswer(r.Context(), info); err != nil {
			_ = c.Close()
			writeStoreError(w, err)
			return "", nil, false
		}
	}
	return id, c, true
}

// openFailed is the answer to a case that did not open. A case that is "not found"
// may be one whose erase was marked and not finished (the process died, or the key
// was locked at the time): the store hides it, so the next call finishes the erase
// here. If the finish works the case is gone (404); if it cannot yet, the call is
// told the erase is under way (M5).
func (h *handler) openFailed(ctx context.Context, id casestore.CaseID, err error) error {
	if !errors.Is(err, casestore.ErrNotFound) || h.recoverer == nil {
		return err
	}
	if mark, _ := h.recoverer.RecoverOne(ctx, id); mark == casestore.MarkErasing {
		return casestore.ErrErasing
	}
	return err
}

func (h *handler) closeCase(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok || !h.casesAvailable(w) {
		return
	}
	believed, ok := believedState(w, r)
	if !ok {
		return
	}
	// Close takes the case's gate, like import and erase: an import in flight
	// finishes first, and no import starts between the close and its answer.
	release, err := h.gates.acquire(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer release()
	// The page believes the case is open. If another tab already closed it (or it
	// moved on in some other way) the answer is M2, not a second close.
	if err := h.guard(r.Context(), id, casestore.ActionClose, believed); err != nil {
		writeStoreError(w, err)
		return
	}
	// A sampler recording into this case is stopped once the move is allowed (a stale tab that gets M2 must not stop a live sampler), and
	// before the case closes, so its last batch is saved first.
	h.stopSamplerFor(id, false, stopCaseClosed)
	if err := h.cases.CloseCase(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	c, err := h.cases.Open(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer c.Close()
	writeJSON(w, http.StatusOK, h.viewOf(r.Context(), c), maxResponseBytes)
}

func (h *handler) eraseCase(w http.ResponseWriter, r *http.Request, raw string) {
	id, ok := caseIDOf(w, raw)
	if !ok || !h.casesAvailable(w) {
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !eraseCodeMatches(body.Confirm, id) {
		writeCaseFailure(w, http.StatusBadRequest, "confirm-mismatch", "The code does not match the case code.")
		return
	}
	believed, ok := believedState(w, r)
	if !ok {
		return
	}
	// An archive that is running must be cancelled first (M15). Checked before anything
	// is stopped, so a refused erase touches nothing, and again under the gate.
	if err := h.refuseWhileArchiving(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := h.eraseBelief(r.Context(), id, believed); err != nil {
		writeStoreError(w, err)
		return
	}
	// An import in flight is stopped, the sampler is stopped and its streams closed,
	// and the erase waits up to 30 s for every read that already opened the case
	// (A16). New reads are told "being erased" (M5) meanwhile. If the wait runs out
	// the answer is M4 and nothing was marked or erased. A background import job is
	// cancelled first, so the erase waits only for it to stop, not to finish.
	// A recording stopped here is put back if the erase is refused before it starts
	// (M4, or a belief that went stale), after the gate is given back.
	rec, wasRecording := h.samplerRunningFor(id)
	erasing := false
	defer func() {
		if wasRecording && !erasing {
			h.restartSampler(id, rec)
		}
	}()
	h.stopSamplerFor(id, true, stopCaseClosed)
	stopCancelling := h.jobs.cancelCaseUntilStopped(id)
	release, err := h.gates.acquireEraseWithin(r.Context(), id, h.eraseTimer())
	stopCancelling()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer release()
	if err := h.refuseWhileArchiving(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	if err := h.eraseBelief(r.Context(), id, believed); err != nil {
		writeStoreError(w, err)
		return
	}
	// An id that names no case is a 404 and touches nothing, the ledger included.
	// The exception is a case whose erase started and did not finish (the ledger
	// could not be stripped): erasing it again completes the job.
	exists, err := h.caseExists(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !exists && !h.pending.has(id) {
		writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
		return
	}
	erasing = true
	h.pending.add(id)
	if err := h.cases.Erase(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	// Previews built from the case are exports of it too: drop them.
	h.exports().dropCase(string(id))
	// Erase is idempotent, so a retry after a ledger failure completes the job.
	if stripper, ok := h.ledger.(ledger.Stripper); ok && h.ledger != nil {
		if _, err := stripper.StripEvidence(evidence.CasePrefixes(string(id)), erasedReference); err != nil {
			writeCaseFailure(w, http.StatusInternalServerError, "ledger-not-stripped", "The case was erased but the ledger could not be updated. Erase it again to retry.")
			return
		}
	}
	h.pending.remove(id)
	h.noteErased(id)
	h.noteFreedArchives(context.Background()) // an erased freed archive is no longer counted by M12
	w.WriteHeader(http.StatusNoContent)
}

// eraseCodeLen is the length of the short code typed to erase a case.
const eraseCodeLen = 8

// defaultEraseWait is how long Erase waits for the case to be free (A16).
const defaultEraseWait = 30 * time.Second

// eraseTimer starts the wait of one erase.
func (h *handler) eraseTimer() <-chan time.Time {
	d := h.eraseWait
	if d <= 0 {
		d = defaultEraseWait
	}
	if h.eraseAfter != nil {
		return h.eraseAfter(d)
	}
	return time.After(d)
}

// withEraseTimer replaces the timer of the 30 s wait. Tests only.
func withEraseTimer(after func(time.Duration) <-chan time.Time) Option {
	return func(h *handler) { h.eraseAfter = after }
}

// eraseCodeMatches reports whether typed names the case. The code is the first eight
// characters of the id, typed in any case, with spaces or dashes allowed
// ("K7M2-QX4A"). The whole id is accepted too, exactly as it is.
func eraseCodeMatches(typed string, id casestore.CaseID) bool {
	if typed == string(id) {
		return true
	}
	code := strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, strings.ToLower(typed))
	return len(code) == eraseCodeLen && len(id) >= eraseCodeLen && code == string(id[:eraseCodeLen])
}

// refuseWhileArchiving answers M15 when the case's archive is running.
func (h *handler) refuseWhileArchiving(ctx context.Context, id casestore.CaseID) error {
	// The job is the truth while it runs: a locked keyed case shows no Archiving mark in
	// the list (its manifest is sealed), but its archive is still writing.
	if h.caseFiles().jobs.running(jobArchive, id) != nil {
		return casestore.ErrCancelArchiveFirst
	}
	info, found, err := h.caseRow(ctx, id)
	if err != nil || !found {
		return err
	}
	return casestore.CheckBelief(casestore.ActionErase, "", casestore.StateOf(info))
}

// caseRow is the case's row of the listing, as the store holds it (no repair).
func (h *handler) caseRow(ctx context.Context, id casestore.CaseID) (casestore.CaseInfo, bool, error) {
	items, err := h.cases.List(ctx)
	if err != nil {
		return casestore.CaseInfo{}, false, err
	}
	for _, c := range items {
		if c.ID == id {
			return c, true, nil
		}
	}
	return casestore.CaseInfo{}, false, nil
}

// caseExists reports whether the store lists the case.
func (h *handler) caseExists(ctx context.Context, id casestore.CaseID) (bool, error) {
	items, err := h.cases.List(ctx)
	if err != nil {
		return false, err
	}
	for _, c := range items {
		if c.ID == id {
			return true, nil
		}
	}
	return false, nil
}

/* ---------------------------------------------------------------- imports */

func (h *handler) listImports(w http.ResponseWriter, r *http.Request, raw string) {
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	records, err := c.Imports(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]importRecordDTO, 0, len(records))
	for _, rec := range records {
		out = append(out, toRecordDTO(rec))
	}
	writeJSON(w, http.StatusOK, struct {
		Imports []importRecordDTO `json:"imports"`
	}{out}, maxResponseBytes)
}

// boundAck decides whether an acknowledgement may be stored. The standard
// banner text (any white space spacing) is stored as the canonical text, so the
// record always matches what the page showed. Anything else is stored only when
// the caller marked it custom and it is plain text within the cap: valid UTF-8,
// not blank, no control characters.
func boundAck(text string, custom bool) (string, bool) {
	if banner, ok := evidence.KnownBanner(text); ok {
		return banner, true
	}
	if !custom || len(text) > casestore.MaxAckText || !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
		return "", false
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", false
		}
	}
	return text, true
}

// baseName reduces an upload name to a display-only base name: the part after
// the last separator, with control and formatting characters, "..", and the
// characters Windows forbids removed. It is never used in a path.
func baseName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == utf8.RuneError, unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r):
			continue
		case strings.ContainsRune(`:*?"<>|`, r):
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", "")
	}
	out = strings.Trim(out, " .")
	if len(out) > maxFileNameBytes {
		cut := maxFileNameBytes
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		out = out[:cut]
	}
	if out == "" {
		return "upload"
	}
	return out
}

// uploadCap is the most bytes one import request may carry: the byte cap of the
// file plus the multipart framing. Before the importer is known it is the
// largest cap of any importer.
func (h *handler) uploadCap(imp importer.Importer) int64 {
	if h.maxUpload > 0 {
		return h.maxUpload
	}
	file := casestore.DefaultCaps.MaxFileBytes
	if imp != nil {
		if m := imp.DefaultLimits().MaxBytes; m > 0 && m < file {
			file = m
		}
	} else if h.registry != nil {
		var largest int64
		for _, name := range h.registry.Names() {
			if i, ok := h.registry.ByName(name); ok {
				if m := i.DefaultLimits().MaxBytes; m > largest {
					largest = m
				}
			}
		}
		if largest > 0 && largest < file {
			file = largest
		}
	}
	return file + uploadSlack
}

// hostsNow is the host map as of this request.
func (h *handler) hostsNow() *hosts.Map {
	h.hostsMu.RLock()
	defer h.hostsMu.RUnlock()
	return h.hostMap
}

// trackedReader remembers the first read error so the handler can tell a body
// that was too large from a failure inside the importer.
type trackedReader struct {
	r   io.Reader
	err error
}

func (t *trackedReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF && t.err == nil {
		t.err = err
	}
	return n, err
}

// importIdleTimeout is how long an import body may stay silent. The server's
// read timeout is a hard limit on the whole request, which is right for small
// requests but would cut off any import that takes longer than that to stream.
const importIdleTimeout = 35 * time.Second

// importWriteWindow is the time the server may take to answer once the body
// has been read: parsing and the commit run after the last byte arrives.
const importWriteWindow = requestTimeout + 10*time.Second

// rollingBody moves the connection deadlines forward on each read, so a slow
// but active import is not cut off by the server-wide read and write timeouts.
type rollingBody struct {
	rc   *http.ResponseController
	r    io.ReadCloser
	last time.Time
}

func newRollingBody(w http.ResponseWriter, body io.ReadCloser) *rollingBody {
	rb := &rollingBody{rc: http.NewResponseController(w), r: body}
	rb.extend()
	return rb
}

// extend sets fresh deadlines. A writer without deadline support (a test
// recorder) is ignored: there is no timeout to extend there.
func (b *rollingBody) extend() {
	now := time.Now()
	_ = b.rc.SetReadDeadline(now.Add(importIdleTimeout))
	_ = b.rc.SetWriteDeadline(now.Add(importWriteWindow))
	b.last = now
}

func (b *rollingBody) Read(p []byte) (int, error) {
	if time.Since(b.last) > time.Second {
		b.extend()
	}
	return b.r.Read(p)
}

func (b *rollingBody) Close() error { return b.r.Close() }

func randomNonce() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// trimToWindow makes a report describe what was stored after the window dropped
// events: the row and event counts, the coverage, and a known gap saying so.
func trimToWindow(rep *importer.Report, kept, dropped int64, first, last time.Time) {
	rep.Emitted = kept
	if rep.Rows -= dropped; rep.Rows < kept {
		rep.Rows = kept
	}
	rep.First, rep.Last = first.UTC(), last.UTC()
	if rep.Quality.CoverageFirst != nil || rep.Quality.CoverageLast != nil {
		f, l := rep.First, rep.Last
		rep.Quality.CoverageFirst, rep.Quality.CoverageLast = &f, &l
	}
	rep.Gap(importer.GapOutsideWindow)
}

// importRun builds the importer side of one import. preview parses the whole
// file, hashes it, and ends with errPreviewDone so the store keeps nothing.
func importRun(ctx context.Context, imp importer.Importer, opts importer.Options, preview bool, sum *string) casestore.RunFunc {
	return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		var hasher hash.Hash
		if preview {
			hasher = sha256.New()
			r = io.TeeReader(r, hasher)
		}
		// A preview keeps nothing, so its failures must not look like a refusal
		// to the store, which would record a quarantine entry and log it.
		fail := func(rep importer.Report, err error) (importer.Report, error) {
			if preview {
				err = &previewFailure{cause: err}
			}
			return rep, err
		}
		body, opts, err := importer.Prepare(imp, r, opts)
		if err != nil {
			return fail(importer.Report{Importer: imp.Name()}, err)
		}
		// A run that swallows an emit error must still fail.
		var emitErr error
		// The window applies to every importer, whether or not it filters itself and
		// whether or not the file could be sliced by time first: an event outside it
		// is dropped here and counted, never stored.
		win := opts.Window
		windowed := !win.From.IsZero() || !win.To.IsZero()
		var kept, dropped int64
		var first, last time.Time
		wrapped := func(ev evidence.Event) error {
			if windowed && !win.Overlaps(ev.Start, ev.End) {
				dropped++
				return nil
			}
			kept++
			if first.IsZero() || ev.Start.Before(first) {
				first = ev.Start
			}
			if ev.End.After(last) {
				last = ev.End
			}
			if err := emit(ev); err != nil {
				if emitErr == nil {
					emitErr = err
				}
				return err
			}
			return nil
		}
		rep, err := imp.Parse(ctx, body, opts, wrapped)
		if rep.Importer == "" {
			rep.Importer = imp.Name()
		}
		if err == nil {
			err = emitErr
		}
		if err == nil {
			err = importer.Finish(rep, opts.Limits)
		}
		if err == nil && windowed && dropped > 0 {
			if kept == 0 {
				err = importer.ErrOutOfWindow
			} else {
				trimToWindow(&rep, kept, dropped, first, last)
			}
		}
		if err != nil {
			return fail(rep, err)
		}
		if preview {
			if _, err := io.Copy(io.Discard, r); err != nil {
				return fail(rep, err)
			}
			*sum = hex.EncodeToString(hasher.Sum(nil))
			return rep, errPreviewDone
		}
		return rep, nil
	}
}

func (h *handler) postImport(w http.ResponseWriter, r *http.Request, raw string) {
	id, c, ok := h.openCaseForWrite(w, r, raw)
	if !ok {
		return
	}
	// The early handle serves the checks before the gate. It is dropped before the
	// wait for the gate, so a Close that holds the gate never waits on it, and the
	// case is opened again fresh once the gate is ours.
	defer func() {
		if c != nil {
			_ = c.Close()
		}
	}()
	if h.registry == nil {
		writeCaseFailure(w, http.StatusServiceUnavailable, "cases-unavailable", "Cases are unavailable.")
		return
	}
	if c.Info().Closed != nil {
		writeCaseFailure(w, http.StatusConflict, "case-closed", "The case is closed. Nothing can be imported into it.")
		return
	}
	query := r.URL.Query()
	preview := query.Get("preview") == "1"
	var imp importer.Importer
	if name := query.Get("importer"); name != "" {
		found, ok := h.registry.ByName(name)
		if !ok {
			writeCaseFailure(w, http.StatusBadRequest, "unknown-importer", "That importer is not available.")
			return
		}
		if found.Name() == "win-live" {
			// Only the in-process sampler writes win-live batches; a body
			// that arrives over the network is never one of them.
			writeCaseFailure(w, http.StatusBadRequest, "importer-internal", "That importer is used only by the live sampler on this computer.")
			return
		}
		imp = found
	} else if !preview {
		writeCaseFailure(w, http.StatusBadRequest, "importer-required", "Choose an importer before importing.")
		return
	}
	// Owner-supplied settings (s.<key>=<value> in the query) are checked against
	// the chosen importer's declared keys before the gate is taken and before any
	// byte of the body is read. A refusal never repeats a key or a value.
	supplied, err := suppliedFromQuery(query, imp)
	if err != nil {
		writeCaseFailure(w, http.StatusBadRequest, "bad-supplied", badSuppliedText)
		return
	}
	var hint *importer.HostHint
	if label := query.Get("host"); label != "" {
		found, ok := h.hostsNow().Lookup(label)
		if !ok {
			writeCaseFailure(w, http.StatusUnprocessableEntity, string(importer.ReasonUnknownHost), importer.ReasonUnknownHost.Text())
			return
		}
		hint = &found
	}
	limit := h.uploadCap(imp)
	if r.ContentLength > limit {
		writeCaseFailure(w, http.StatusRequestEntityTooLarge, "request-too-large", "The upload is larger than the cap.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		writeCaseFailure(w, http.StatusUnsupportedMediaType, "not-multipart", "Content-Type must be multipart/form-data.")
		return
	}
	_ = c.Close()
	c = nil
	release, err := h.gates.acquire(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// A case that is not Open takes no file, whatever the page believed (the
	// answer is case-closed), and a marked case is repaired or told to wait.
	if !preview {
		if err := h.guard(r.Context(), id, casestore.ActionAddImport, ""); err != nil {
			release()
			writeStoreError(w, err)
			return
		}
	}
	// The gate is taken first, so imports queued behind one case hold no slot.
	slot, ok := h.imports.tryAcquire()
	if !ok {
		release()
		w.Header().Set("Retry-After", "2")
		writeCaseFailure(w, http.StatusTooManyRequests, "import-busy", "Other imports are running. Try again in a moment.")
		return
	}
	// An async import hands the gate and the slot to its job, which holds them
	// until the job ends; every other exit gives them back here.
	var job *importJob
	handedOff := false
	defer func() {
		if !handedOff {
			if job != nil {
				h.jobs.discard(job)
			}
			slot()
			release()
		}
	}()
	if query.Get("async") == "1" {
		if job = h.jobs.create(id); job == nil {
			w.Header().Set("Retry-After", "2")
			writeCaseFailure(w, http.StatusTooManyRequests, "import-busy", "Other imports are running. Try again in a moment.")
			return
		}
	}

	// The wait for the gate may have used up the server's read deadline, so the
	// body gets its own rolling deadline from here on.
	r.Body = http.MaxBytesReader(w, newRollingBody(w, r.Body), limit)
	form, err := r.MultipartReader()
	if err != nil {
		writeCaseFailure(w, http.StatusBadRequest, "bad-form", "The upload is not a valid multipart form.")
		return
	}
	var ackGating, ackCustom bool
	var ackText string
	for {
		part, err := form.NextPart()
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeCaseFailure(w, http.StatusRequestEntityTooLarge, "request-too-large", "The upload is larger than the cap.")
				return
			}
			if err == io.EOF {
				writeCaseFailure(w, http.StatusBadRequest, "file-missing", "The upload has no file.")
				return
			}
			writeCaseFailure(w, http.StatusBadRequest, "bad-form", "The upload is not a valid multipart form.")
			return
		}
		switch part.FormName() {
		case "ackGating", "ackText", "ackCustom":
			value, err := io.ReadAll(io.LimitReader(part, casestore.MaxAckText+1))
			if err != nil {
				writeCaseFailure(w, http.StatusBadRequest, "bad-form", "A form field is invalid.")
				return
			}
			if len(value) > casestore.MaxAckText {
				if part.FormName() == "ackText" {
					writeCaseFailure(w, http.StatusBadRequest, "ack-too-long", ackTooLongText)
				} else {
					writeCaseFailure(w, http.StatusBadRequest, "bad-form", "A form field is invalid.")
				}
				return
			}
			switch part.FormName() {
			case "ackGating":
				ackGating = string(value) == "true"
			case "ackCustom":
				ackCustom = string(value) == "true"
			default:
				ackText = string(value)
			}
		case "file":
			if !preview && ackGating && ackText != "" {
				// The acknowledgement is bound to the notice the page shows: the
				// standard banner text (stored in its canonical form), or text the
				// caller marks as custom, within the caps.
				bound, ok := boundAck(ackText, ackCustom)
				if !ok {
					writeCaseFailure(w, http.StatusBadRequest, "ack-mismatch", ackMismatchText)
					return
				}
				ackText = bound
			}
			if job != nil {
				h.importAsync(w, id, job, imp, hint, supplied, part, preview, ackGating, ackText, baseName(part.FileName()), func() {
					handedOff = true
				}, slot, release)
				return
			}
			c, err = h.cases.Open(r.Context(), id)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			h.importFile(r.Context(), w, id, c, imp, hint, supplied, part, preview, ackGating, ackText, baseName(part.FileName()))
			return
		default:
			writeCaseFailure(w, http.StatusBadRequest, "bad-form", "The upload has a field the server does not know.")
			return
		}
	}
}

// importFile runs one uploaded file. The acknowledgement must already have
// arrived: it travels in the fields before the file, so a refusal needs no read
// of the file, and the body is streamed, never spilled to disk. (An async
// import, import_jobs.go, keeps the upload in an encrypted spool for the job
// and runs this same function over it.)
func (h *handler) importFile(ctx context.Context, w http.ResponseWriter, id casestore.CaseID, c casestore.Case, imp importer.Importer, hint *importer.HostHint, supplied map[string]string, part io.Reader, preview, ackGating bool, ackText, fileName string) {
	h.importFileWith(ctx, w, id, c, imp, hint, supplied, part, preview, ackGating, ackText, fileName, importer.Window{})
}

// importFileWith is importFile with a time window handed to the importer (the
// zero window is open). Host discovery uses it for the files it reads locally.
func (h *handler) importFileWith(ctx context.Context, w http.ResponseWriter, id casestore.CaseID, c casestore.Case, imp importer.Importer, hint *importer.HostHint, supplied map[string]string, part io.Reader, preview, ackGating bool, ackText, fileName string, win importer.Window) {
	if !preview && (!ackGating || ackText == "") {
		writeCaseFailure(w, http.StatusBadRequest, "ack-required", "Send ackGating and ackText before the file.")
		return
	}
	tracked := &trackedReader{r: part}
	answer := importAnswer{Tied: []string{}}
	// Peek the first 64 KiB once. Archives and binary exports that cannot be read
	// in pure Go are refused here with the exact reason, whether or not an importer
	// was named (sniff.go: by content, and by extension only on binary bytes).
	br := bufio.NewReaderSize(tracked, importer.HeadSize)
	head, perr := br.Peek(importer.HeadSize)
	if perr != nil && !errors.Is(perr, io.EOF) && !errors.Is(perr, bufio.ErrBufferFull) {
		h.writeImportError(w, perr, tracked, casestore.ImportResult{})
		return
	}
	if err := importer.SniffNamed(head, fileName); err != nil {
		h.writeImportError(w, err, tracked, casestore.ImportResult{})
		return
	}
	var body io.Reader = br
	if imp == nil {
		// Preview without an importer: detect it from the head.
		best, score, tied := h.registry.Detect(head, fileName)
		if best == nil {
			writeCaseFailure(w, http.StatusUnprocessableEntity, string(importer.ReasonUnsupportedFormat), importer.ReasonUnsupportedFormat.Text())
			return
		}
		answer.Score = score
		if tied != nil {
			answer.Tied = tied
			writeJSON(w, http.StatusOK, answer, maxResponseBytes)
			return
		}
		imp = best
	}
	answer.Importer = imp.Name()

	nonce, err := randomNonce()
	if err != nil {
		writeCaseFailure(w, http.StatusInternalServerError, "import-failed", "The import could not start.")
		return
	}
	opts := importer.Options{
		CaseID: string(id), ImportNonce: nonce, FileName: fileName, Host: hint,
		Hosts: h.hostsNow().Lookup, Pseudonymize: c.Pseudonymize,
		Supplied: supplied, Window: win,
	}
	meta := casestore.ImportMeta{
		Importer: imp.Name(), FileName: fileName, AckGating: ackGating, AckText: ackText, AllowedAttrs: imp.AllowedAttrs(),
		Supplied: supplied,
	}
	if hint != nil {
		meta.HostLabel = hint.Label
	}
	if preview {
		// The store insists on an acknowledgement; a preview keeps nothing.
		meta.AckGating, meta.AckText = true, "preview"
	}
	var sum string
	if !preview {
		// Say "this file needs another try" before the work starts: a kill in the middle leaves the line (C17).
		noteImportAttempt(ctx, c, fileName)
	}
	res, err := c.Import(ctx, meta, body, importRun(ctx, imp, opts, preview, &sum))
	if !preview && importLanded(err) {
		clearImportAttempt(ctx, c, fileName)
	}
	if preview && errors.Is(err, errPreviewDone) {
		if existing, found := h.sameBytes(ctx, c, sum); found {
			writeJSON(w, http.StatusConflict, caseFailure{Error: "These bytes are already imported into this case.", Reason: "duplicate", ImportID: existing}, 4096)
			return
		}
		answer.Report = toReportDTO(res.Report)
		writeJSON(w, http.StatusOK, answer, maxResponseBytes)
		return
	}
	if err != nil {
		var failed *previewFailure
		if errors.As(err, &failed) {
			err = failed.cause
		}
		h.writeImportError(w, err, tracked, res)
		return
	}
	record := toRecordDTO(res.Record)
	answer.Report = toReportDTO(res.Report)
	answer.Record = &record
	writeJSON(w, http.StatusCreated, answer, maxResponseBytes)
}

// sameBytes reports the id of an import that already holds the same bytes.
func (h *handler) sameBytes(ctx context.Context, c casestore.Case, sum string) (int64, bool) {
	if sum == "" {
		return 0, false
	}
	records, err := c.Imports(ctx)
	if err != nil {
		return 0, false
	}
	for _, rec := range records {
		if rec.SHA256 == sum {
			return rec.ID, true
		}
	}
	return 0, false
}

// writeImportError turns an import failure into a Reason. Nothing from the
// file, and no error text from the importer, reaches the response.
func (h *handler) writeImportError(w http.ResponseWriter, err error, body *trackedReader, res casestore.ImportResult) {
	var (
		tooLarge   *http.MaxBytesError
		duplicate  *casestore.DuplicateError
		limit      *importer.LimitError
		quarantine *importer.QuarantineError
	)
	switch {
	case body != nil && errors.As(body.err, &tooLarge), errors.As(err, &tooLarge):
		writeCaseFailure(w, http.StatusRequestEntityTooLarge, "request-too-large", "The upload is larger than the cap.")
	case errors.Is(err, casestore.ErrAckRequired):
		writeCaseFailure(w, http.StatusBadRequest, "ack-required", "Send ackGating and ackText before the file.")
	case errors.Is(err, casestore.ErrBadSupplied), errors.Is(err, importer.ErrBadSupplied):
		writeCaseFailure(w, http.StatusBadRequest, "bad-supplied", badSuppliedText)
	case errors.Is(err, casestore.ErrAckTooLong):
		writeCaseFailure(w, http.StatusBadRequest, "ack-too-long", ackTooLongText)
	case errors.Is(err, casestore.ErrCaseClosed):
		writeCaseFailure(w, http.StatusConflict, "case-closed", "The case is closed. Nothing can be imported into it.")
	case errors.As(err, &duplicate):
		writeJSON(w, http.StatusConflict, caseFailure{Error: "These bytes are already imported into this case.", Reason: "duplicate", ImportID: duplicate.Existing.ID}, 4096)
	case errors.Is(err, casestore.ErrCapExceeded):
		writeCaseFailure(w, http.StatusRequestEntityTooLarge, "cap-exceeded", "The file or the case is over its size or row cap.")
	case errors.As(err, &limit):
		writeCaseFailure(w, http.StatusRequestEntityTooLarge, "limit-"+limit.Limit, "The file is over the "+limit.Limit+" limit of its importer.")
	case errors.As(err, &quarantine):
		reason := quarantine.Reason
		if !reason.Valid() {
			reason = importer.ReasonUnsupportedFormat
		}
		writeJSON(w, http.StatusUnprocessableEntity, caseFailure{Error: "The file was refused: " + reason.Text() + ".", Reason: string(reason), Column: importer.SafeColumn(quarantine.Column)}, 4096)
	case errors.Is(err, importer.ErrOutOfWindow):
		writeCaseFailure(w, http.StatusConflict, "out-of-window", "Nothing in the file falls inside the chosen time window.")
	case errors.Is(err, importer.ErrUnknownHost):
		writeCaseFailure(w, http.StatusUnprocessableEntity, string(importer.ReasonUnknownHost), importer.ReasonUnknownHost.Text())
	case errors.Is(err, importer.ErrTimezoneUnlabelled):
		writeCaseFailure(w, http.StatusUnprocessableEntity, string(importer.ReasonTZUnlabelled), importer.ReasonTZUnlabelled.Text())
	case errors.Is(err, casestore.ErrDuplicateEvent):
		writeCaseFailure(w, http.StatusConflict, "duplicate-event", "The file repeats an event the case already holds.")
	case errors.Is(err, evidence.ErrEventInvalid):
		writeCaseFailure(w, http.StatusUnprocessableEntity, "invalid-event", "The importer produced an event that failed its checks.")
	case errors.Is(err, casestore.ErrNotFound):
		writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
	case errors.Is(err, context.Canceled):
		writeCaseFailure(w, http.StatusBadRequest, "canceled", "The request was canceled.")
	case errors.Is(err, context.DeadlineExceeded):
		writeCaseFailure(w, http.StatusGatewayTimeout, "timeout", "The request timed out.")
	default:
		writeCaseFailure(w, http.StatusInternalServerError, "import-failed", "The import could not be completed. Nothing was stored.")
	}
}

/* -------------------------------------------------------------- supplied */

const badSuppliedText = "A supplied setting is not one this importer accepts, or its value is not valid."

// suppliedFromQuery collects the s.<key>=<value> query parameters and checks
// them against the keys imp declares. A query with none gives nil, nil. A key
// given twice, an s.* key with no importer, an importer that declares no keys,
// or a bad key or value gives importer.ErrBadSupplied. The error never carries
// a key or a value.
func suppliedFromQuery(query map[string][]string, imp importer.Importer) (map[string]string, error) {
	var raw map[string]string
	for name, values := range query {
		key, ok := strings.CutPrefix(name, "s.")
		if !ok {
			continue
		}
		if len(values) != 1 || (raw != nil && len(raw) >= importer.MaxSuppliedKeys) {
			return nil, importer.ErrBadSupplied
		}
		if raw == nil {
			raw = make(map[string]string)
		}
		raw[key] = values[0]
	}
	if raw == nil {
		return nil, nil
	}
	if imp == nil {
		return nil, importer.ErrBadSupplied
	}
	return importer.ValidateSupplied(imp, raw)
}

/* ---------------------------------------------------------------- series */

// caseSeries answers GET /api/cases/{id}/series: per-bucket min, mean, max and
// count for each sel, over the events of one case. The scan streams through
// Case.Each under the scan budget; a stopped scan is answered with
// partial=true. The case is read under the read share, so an erase waits for
// the scan, and a scan that meets an erase answers 404.
func (h *handler) caseSeries(w http.ResponseWriter, r *http.Request, raw string) {
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	query := r.URL.Query()
	q := casestore.SeriesQuery{Sel: query["sel"]}
	for _, f := range []struct {
		name string
		into *time.Time
	}{{"from", &q.From}, {"to", &q.To}} {
		t, err := time.Parse(time.RFC3339, query.Get(f.name))
		if err != nil {
			writeCaseFailure(w, http.StatusBadRequest, casestore.SeriesBadTime, "from and to must be RFC 3339 times with a zone.")
			return
		}
		*f.into = t.UTC()
	}
	bucket, err := strconv.ParseInt(query.Get("bucket"), 10, 64)
	if err != nil {
		writeCaseFailure(w, http.StatusBadRequest, casestore.SeriesBadBucket, "bucket must be a whole number of milliseconds.")
		return
	}
	q.BucketMs = bucket
	res, err := casestore.Series(ctx, c, q)
	if err != nil {
		var se *casestore.SeriesError
		if errors.As(err, &se) {
			writeCaseFailure(w, http.StatusBadRequest, se.Reason, se.Message)
			return
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res, maxResponseBytes)
}

/* ---------------------------------------------------------------- events */

func eventFilter(values []string) ([]string, bool) {
	var out []string
	for _, v := range values {
		for _, item := range strings.Split(v, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if len(item) > 64 || !utf8.ValidString(item) {
				return nil, false
			}
			out = append(out, item)
		}
	}
	return out, len(out) <= maxEventFilters
}

func (h *handler) caseEvents(w http.ResponseWriter, r *http.Request, raw string) {
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	query := r.URL.Query()
	var q casestore.EventQuery
	for _, f := range []struct {
		name string
		into *time.Time
	}{{"from", &q.Window.From}, {"to", &q.Window.To}} {
		if v := query.Get(f.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeCaseFailure(w, http.StatusBadRequest, "bad-time", "from and to must be RFC 3339 times with a zone.")
				return
			}
			*f.into = t.UTC()
		}
	}
	if !q.Window.From.IsZero() && !q.Window.To.IsZero() && q.Window.From.After(q.Window.To) {
		writeCaseFailure(w, http.StatusBadRequest, "bad-time", "from is after to.")
		return
	}
	var good [3]bool
	q.Kinds, good[0] = eventFilter(query["kind"])
	q.Sources, good[1] = eventFilter(query["source"])
	q.Hosts, good[2] = eventFilter(query["host"])
	if !good[0] || !good[1] || !good[2] {
		writeCaseFailure(w, http.StatusBadRequest, "bad-filter", "A filter has too many values or a value that is too long.")
		return
	}
	q.Limit = casestore.MaxEventQuery
	if v := query.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > casestore.MaxEventQuery {
			writeCaseFailure(w, http.StatusBadRequest, "bad-limit", "limit must be 1 to 50000.")
			return
		}
		q.Limit = n
	}
	events, err := c.Events(r.Context(), q)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]eventDTO, 0, len(events))
	for _, e := range events {
		out = append(out, toEventDTO(e))
	}
	writeJSON(w, http.StatusOK, struct {
		Events    []eventDTO `json:"events"`
		Limit     int        `json:"limit"`
		Truncated bool       `json:"truncated"`
	}{out, q.Limit, len(out) >= q.Limit}, maxResponseBytes)
}

/* ---------------------------------------------------------------- hosts */

func (h *handler) getHosts(w http.ResponseWriter, _ *http.Request) {
	if !h.casesAvailable(w) {
		return
	}
	doc, err := h.hostsNow().Marshal()
	if err != nil {
		writeCaseFailure(w, http.StatusInternalServerError, "hosts-error", "The host map could not be read.")
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(doc), maxResponseBytes)
}

func (h *handler) putHosts(w http.ResponseWriter, r *http.Request) {
	if !h.casesAvailable(w) {
		return
	}
	if h.hostsSave == nil {
		writeCaseFailure(w, http.StatusServiceUnavailable, "hosts-readonly", "The host map cannot be saved in this session.")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeCaseFailure(w, http.StatusUnsupportedMediaType, "bad-form", "Content-Type must be application/json.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, hosts.MaxFileBytes+1)
	next, err := hosts.Load(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeCaseFailure(w, http.StatusRequestEntityTooLarge, "request-too-large", "The host map is larger than its cap.")
			return
		}
		if errors.Is(err, hosts.ErrInvalid) {
			// hosts messages name the field and the rule, never the value.
			writeCaseFailure(w, http.StatusBadRequest, "invalid-hosts", err.Error())
			return
		}
		writeCaseFailure(w, http.StatusBadRequest, "invalid-hosts", "The host map could not be read.")
		return
	}
	h.hostsMu.Lock()
	defer h.hostsMu.Unlock()
	if err := h.hostsSave(next); err != nil {
		writeCaseFailure(w, http.StatusInternalServerError, "hosts-not-saved", "The host map could not be saved. The previous map is unchanged.")
		return
	}
	h.hostMap = next
	doc, err := next.Marshal()
	if err != nil {
		writeCaseFailure(w, http.StatusInternalServerError, "hosts-error", "The host map was saved but could not be read back.")
		return
	}
	writeJSON(w, http.StatusOK, json.RawMessage(doc), maxResponseBytes)
}
