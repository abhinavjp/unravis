package keeper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
)

// The MongoDB audit data plane (P14b, owner ruling B19). In the shared install a saved MongoDB link's password lives only in the key
// keeper, so an audit source opened from such a link is opened by the keeper: it hosts the same audit repository Unravis uses
// (internal/store) and answers named, read-only audit operations on it. The password never leaves the keeper; Unravis sees the same
// answers its own repository would give. There is no operation that takes query text: every operation takes the typed inputs of the
// audit package, which the hosted repository validates exactly as it does in Unravis.

// HostedRepo is the audit repository the keeper hosts for one Unravis: internal/store's Store in the service, a fake in tests. The
// optional readers (activity, breakdown, coverage, slowest, rollup, schema) are found by type assertion, as in Unravis.
type HostedRepo = audit.Repository

// Limits of the audit host.
const (
	// DefaultSeatIdle is how long the sources of an Unravis that stopped asking stay open in the keeper.
	DefaultSeatIdle = 8 * time.Hour
	// DefaultMaxSeats bounds the Unravis processes with audit sources open in the keeper at once.
	DefaultMaxSeats = 16
	// DefaultSeatsPerPerson bounds them per Windows account, so one person cannot take every seat.
	DefaultSeatsPerPerson = 4
	// auditQueryTimeout is the longest one audit read may take in the keeper, waiting for a run slot included. It is much shorter than
	// Unravis's own bound on a read (audit.OperationTimeout): a read holds a pipe connection while it runs. A caller may ask for less.
	auditQueryTimeout = auditReadCap
	// maxInstance bounds the instance id an Unravis sends.
	maxInstance = 64
)

// AuditHost keeps one hosted repository per Unravis process ("seat"): sources are per person and per running Unravis, as they are in a
// per-user install, where they live only as long as the process. A seat is keyed by the caller's Windows account and process, both told
// by Windows, plus the random instance id that Unravis made when it started (so a reused process id never sees an earlier process's
// sources). A seat nobody used for IdleAfter is closed.
type AuditHost struct {
	newRepo   func() HostedRepo
	IdleAfter time.Duration
	MaxSeats  int
	PerPerson int
	now       func() time.Time

	mu    sync.Mutex
	seats map[seatKey]*seat
}

type seatKey struct {
	sid      string
	pid      uint32
	instance string
}

type seat struct {
	repo     HostedRepo
	used     time.Time
	inflight int
	// secrets are the stored passwords this seat's sources were opened with. No answer or error that holds one leaves the keeper.
	secrets []string
}

// NewAuditHost hosts repositories made by newRepo.
func NewAuditHost(newRepo func() HostedRepo) *AuditHost {
	return &AuditHost{newRepo: newRepo, seats: map[seatKey]*seat{}, now: time.Now}
}

var errNoSeatRoom = &WireError{Code: CodeBusy, Message: "The key keeper has too many audit sources open. Remove a source, or try again later."}

func (h *AuditHost) limits() (time.Duration, int, int) {
	idle, max, per := h.IdleAfter, h.MaxSeats, h.PerPerson
	if idle <= 0 {
		idle = DefaultSeatIdle
	}
	if max <= 0 {
		max = DefaultMaxSeats
	}
	if per <= 0 {
		per = DefaultSeatsPerPerson
	}
	return idle, max, per
}

// acquire returns the caller's seat for one call; create makes it when missing. release must be called when the call is done.
func (h *AuditHost) acquire(key seatKey, create bool) (*seat, func(), error) {
	h.mu.Lock()
	closing := h.reapLocked()
	s := h.seats[key]
	if s == nil && create {
		var err error
		var more []HostedRepo
		if s, more, err = h.seatLocked(key); err != nil {
			h.mu.Unlock()
			closeRepos(append(closing, more...))
			return nil, nil, err
		}
		closing = append(closing, more...)
	}
	if s != nil {
		s.inflight++
		s.used = h.now()
	}
	h.mu.Unlock()
	closeRepos(closing)
	if s == nil {
		return nil, nil, nil
	}
	return s, func() {
		h.mu.Lock()
		s.inflight--
		s.used = h.now()
		h.mu.Unlock()
	}, nil
}

// seatLocked makes a seat, first making room for it: the caller's own least recently used idle seat goes when they hold their share.
func (h *AuditHost) seatLocked(key seatKey) (*seat, []HostedRepo, error) {
	_, max, per := h.limits()
	var mine []seatKey
	for k := range h.seats {
		if k.sid == key.sid {
			mine = append(mine, k)
		}
	}
	var closing []HostedRepo
	if len(mine) >= per {
		sort.Slice(mine, func(i, j int) bool { return h.seats[mine[i]].used.Before(h.seats[mine[j]].used) })
		freed := false
		for _, k := range mine {
			if h.seats[k].inflight == 0 {
				closing = append(closing, h.seats[k].repo)
				delete(h.seats, k)
				freed = true
				break
			}
		}
		if !freed {
			return nil, nil, errNoSeatRoom
		}
	}
	if len(h.seats) >= max {
		return nil, closing, errNoSeatRoom
	}
	s := &seat{repo: h.newRepo(), used: h.now()}
	h.seats[key] = s
	return s, closing, nil
}

// reapLocked takes out the seats nobody used for IdleAfter; the caller closes their repositories outside the lock.
func (h *AuditHost) reapLocked() []HostedRepo {
	idle, _, _ := h.limits()
	var out []HostedRepo
	now := h.now()
	for k, s := range h.seats {
		if s.inflight == 0 && now.Sub(s.used) >= idle {
			out = append(out, s.repo)
			delete(h.seats, k)
		}
	}
	return out
}

// remember notes a password a seat's source was opened with.
func (h *AuditHost) remember(s *seat, password string) {
	if password == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, v := range s.secrets {
		if v == password {
			return
		}
	}
	s.secrets = append(s.secrets, password)
}

// secretsOf is the passwords a seat holds, plus the one being used now (when not yet remembered).
func (h *AuditHost) secretsOf(s *seat, extra string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]string(nil), s.secrets...)
	if extra != "" {
		out = append(out, extra)
	}
	return out
}

// leaks says whether raw holds one of the secrets, as typed or as JSON writes it.
func leaks(raw []byte, secrets []string) bool {
	for _, v := range secrets {
		if v == "" {
			continue
		}
		if strings.Contains(string(raw), v) {
			return true
		}
		if q, err := json.Marshal(v); err == nil && strings.Contains(string(raw), string(q[1:len(q)-1])) {
			return true
		}
	}
	return false
}

// auditAnswerCap is the largest audit answer the keeper builds and sends (a variable so a test can lower it).
var auditAnswerCap = MaxAuditAnswer

// errAnswerTooLarge is an audit answer past auditAnswerCap. It is an audit limit, so Unravis words it as one.
var errAnswerTooLarge = fmt.Errorf("%w: the answer is too large", audit.ErrLimit)

var errAnswerHeldSecret = &WireError{Code: CodeInternal, Message: "The key keeper did not send an answer that held a stored password."}

// scrubbed is auditWireError, and a connection failure whose text holds a stored password loses that text.
func scrubbed(err error, secrets []string) error {
	err = auditWireError(err)
	if err == nil {
		return nil
	}
	var ce *audit.ConnectionError
	if errors.As(err, &ce) && leaks([]byte(ce.Stage+ce.Code+ce.Message), secrets) {
		return &audit.ConnectionError{Stage: "connect", Code: "connection_failed", Message: auditFailedText}
	}
	var we *WireError
	if errors.As(err, &we) && leaks([]byte(we.Message+string(we.Detail)), secrets) {
		return &WireError{Code: CodeInternal, Message: auditFailedText}
	}
	return err
}

// drop closes the caller's seat (Unravis is stopping).
func (h *AuditHost) drop(key seatKey) {
	h.mu.Lock()
	s := h.seats[key]
	if s != nil && s.inflight == 0 {
		delete(h.seats, key)
	} else {
		s = nil
	}
	h.mu.Unlock()
	if s != nil {
		closeRepos([]HostedRepo{s.repo})
	}
}

// Close closes every seat (the keeper is stopping).
func (h *AuditHost) Close() {
	h.mu.Lock()
	var all []HostedRepo
	for k, s := range h.seats {
		all = append(all, s.repo)
		delete(h.seats, k)
	}
	h.mu.Unlock()
	closeRepos(all)
}

// Seats is how many seats are open (tests).
func (h *AuditHost) Seats() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.seats)
}

func closeRepos(repos []HostedRepo) {
	for _, r := range repos {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = r.Close(ctx)
		cancel()
	}
}

/* ------------------------------------------------------------ the wire */

// AuditSeat names the caller's seat: the instance id its Unravis made when it started.
type AuditSeat struct {
	Instance string `json:"instance"`
}

// AuditAddArgs is audit.add: open an audit source from a saved link. Connection carries the saved link's id and the settings the person
// saw, never a password: the keeper uses the one it stores.
type AuditAddArgs struct {
	Instance        string                `json:"instance"`
	Connection      audit.ConnectionInput `json:"connection"`
	ConnectionLabel string                `json:"connectionLabel"`
	Label           string                `json:"label"`
	Database        string                `json:"database"`
	Collection      string                `json:"collection"`
}

// AuditIDArgs is audit.delete.
type AuditIDArgs struct {
	Instance string `json:"instance"`
	ID       string `json:"id"`
}

// AuditArgs is every audit read: the seat and the typed input of the audit package's own call.
type AuditArgs[T any] struct {
	Instance string `json:"instance"`
	Input    T      `json:"input"`
	// TimeoutMs is how long the caller will wait for the answer. The keeper stops the read after that long, or after auditQueryTimeout,
	// whichever is less; 0 means auditQueryTimeout.
	TimeoutMs int `json:"timeoutMs,omitempty"`
}

// AuditSchemaInput is audit.schema's input.
type AuditSchemaInput struct {
	SourceID string `json:"sourceId"`
	N        int    `json:"n"`
}

// AuditRollupInput is audit.rollup's input: the plan's parts that travel (the keeper plans again, with its own time zone data).
type AuditRollupInput struct {
	SourceIDs []string  `json:"sourceIds"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Zone      string    `json:"zone"`
	SlowMs    float64   `json:"slowMs"`
}

// AuditRollupResult is audit.rollup's answer.
type AuditRollupResult struct {
	Rollup     audit.RollupWire     `json:"rollup"`
	Sources    []audit.SourceResult `json:"sources"`
	Provenance []audit.Provenance   `json:"provenance"`
}

// auditKinds is every audit error that crosses the pipe, by name. The order puts the specific ones first.
var auditKinds = []struct {
	name string
	err  error
}{
	{"seat-gone", ErrHeldSourceGone},
	{"legacy-scan-limit", audit.ErrLegacyScanLimit},
	{"api-group-limit", audit.ErrAPIGroupLimit},
	{"summary-row-limit", audit.ErrSummaryRowLimit},
	{"summary-bytes-limit", audit.ErrSummaryBytesLimit},
	{"analysis-bytes-limit", audit.ErrAnalysisBytesLimit},
	{"breakdown-group-limit", audit.ErrBreakdownGroupLimit},
	{"rollup-group-limit", audit.ErrRollupGroupLimit},
	{"broad-scan-refused", audit.ErrBroadScanRefused},
	{"docs-examined-budget", audit.ErrDocsExaminedBudget},
	{"collection-size-check", audit.ErrCollectionSizeCheck},
	{"no-whole-days", audit.ErrNoWholeDays},
	{"too-few-calibration-rows", audit.ErrTooFewCalibrationRows},
	{"patterns-unavailable", audit.ErrPatternsUnavailable},
	{"busy", audit.ErrBusy},
	{"limit", audit.ErrLimit},
	{"not-found", audit.ErrNotFound},
	{"invalid", audit.ErrInvalidInput},
	{"timeout", context.DeadlineExceeded},
	{"canceled", context.Canceled},
}

const auditFailedText = "The audit source in the key keeper could not answer that request."

// auditWireError turns what the hosted repository returned into what crosses the pipe. Its own text is dropped (only names from
// auditKinds travel); a MongoDB connection failure keeps its fixed classification; anything else becomes "internal".
func auditWireError(err error) error {
	if err == nil {
		return nil
	}
	var we *WireError
	var ce *audit.ConnectionError
	if errors.As(err, &we) || errors.As(err, &ce) {
		return err
	}
	var kinds []string
	for _, k := range auditKinds {
		if errors.Is(err, k.err) {
			kinds = append(kinds, k.name)
		}
	}
	if len(kinds) == 0 {
		return &WireError{Code: CodeInternal, Message: auditFailedText}
	}
	detail, _ := json.Marshal(map[string][]string{"kinds": kinds})
	return &WireError{Code: CodeAudit, Message: auditFailedText, Detail: detail}
}

// auditNative turns an audit wire error back into the errors the local repository would have returned. errors.As still finds the
// *WireError.
func auditNative(we *WireError) error {
	var d struct {
		Kinds []string `json:"kinds"`
	}
	_ = json.Unmarshal(we.Detail, &d)
	var errs []error
	for _, name := range d.Kinds {
		for _, k := range auditKinds {
			if k.name == name {
				errs = append(errs, k.err)
			}
		}
	}
	if len(errs) == 0 {
		return we
	}
	return &nativeError{native: errors.Join(errs...), wire: we}
}

func okInstance(s string) bool {
	if s == "" || len(s) > maxInstance {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

var errBadInstance = &WireError{Code: CodeInvalid, Message: "The request to the key keeper was not understood."}

func (b *Backend) seatKey(c *Call, instance string) (seatKey, error) {
	if !okInstance(instance) {
		return seatKey{}, errBadInstance
	}
	return seatKey{sid: c.Caller.SID, pid: c.Caller.PID, instance: instance}, nil
}

// registerAudit adds the audit.* operations. Opening a source reaches a MongoDB server, so it counts as "reach"; the reads count in
// their own "audit" class; closing counts as "read".
func (b *Backend) registerAudit(s *Server) {
	s.Handle(OpAuditAdd, classReach, reachTimeout, b.auditAdd)
	s.Handle(OpAuditSources, classRead, storeTimeout, b.auditSources)
	s.Handle(OpAuditDelete, classRead, storeTimeout, b.auditDelete)
	s.Handle(OpAuditClose, classRead, storeTimeout, b.auditClose)
	s.Handle(OpAuditAnalyze, classAudit, auditQueryTimeout, auditRead(b, func(ctx context.Context, r HostedRepo, in audit.Query) (any, error) {
		return r.Analyze(ctx, in)
	}))
	s.Handle(OpAuditRequests, classAudit, auditQueryTimeout, auditRead(b, func(ctx context.Context, r HostedRepo, in audit.PageInput) (any, error) {
		return r.Requests(ctx, in)
	}))
	s.Handle(OpAuditPayload, classAudit, auditQueryTimeout, auditRead(b, func(ctx context.Context, r HostedRepo, in audit.PayloadInput) (any, error) {
		return r.Payload(ctx, in)
	}))
	s.Handle(OpAuditRecord, classAudit, auditQueryTimeout, auditRead(b, func(ctx context.Context, r HostedRepo, in audit.RecordInput) (any, error) {
		return r.Record(ctx, in)
	}))
	s.Handle(OpAuditActivity, classAudit, auditQueryTimeout, auditRead(b, func(ctx context.Context, r HostedRepo, in audit.ActivityInput) (any, error) {
		reader, ok := r.(audit.ActivityReader)
		if !ok {
			return nil, errAuditUnavailable
		}
		return reader.Activity(ctx, in)
	}))
	s.Handle(OpAuditBreakdown, classAudit, auditQueryTimeout, auditRead(b, func(ctx context.Context, r HostedRepo, in audit.BreakdownInput) (any, error) {
		reader, ok := r.(audit.BreakdownReader)
		if !ok {
			return nil, errAuditUnavailable
		}
		return reader.Breakdown(ctx, in)
	}))
	s.Handle(OpAuditCoverage, classAudit, auditQueryTimeout, auditRead(b, func(ctx context.Context, r HostedRepo, in audit.CoverageInput) (any, error) {
		reader, ok := r.(audit.CoverageProber)
		if !ok {
			return nil, errAuditUnavailable
		}
		return reader.Coverage(ctx, in)
	}))
	s.Handle(OpAuditSlowest, classAudit, auditQueryTimeout, auditRead(b, func(ctx context.Context, r HostedRepo, in audit.PageInput) (any, error) {
		reader, ok := r.(audit.SlowestReader)
		if !ok {
			return nil, errAuditUnavailable
		}
		return reader.Slowest(ctx, in)
	}))
	s.Handle(OpAuditRollup, classAudit, auditQueryTimeout, auditRead(b, func(ctx context.Context, r HostedRepo, in AuditRollupInput) (any, error) {
		reader, ok := r.(audit.RollupReader)
		if !ok {
			return nil, errAuditUnavailable
		}
		plan, err := audit.PlanRollup(audit.RollupInput{SourceIDs: in.SourceIDs, From: in.From, To: in.To, TimeZone: in.Zone, SlowMs: in.SlowMs})
		if err != nil {
			return nil, err
		}
		slots, err := reader.Rollup(ctx, plan)
		if err != nil {
			return nil, err
		}
		out := AuditRollupResult{Sources: slots.Sources, Provenance: slots.Provenance}
		if slots.Builder != nil {
			out.Rollup = slots.Builder.Wire()
		} else {
			out.Rollup = audit.NewRollupBuilder(plan.Width).Wire()
		}
		return out, nil
	}))
	s.Handle(OpAuditSchema, classAudit, auditQueryTimeout, auditRead(b, func(ctx context.Context, r HostedRepo, in AuditSchemaInput) (any, error) {
		reader, ok := r.(audit.SchemaSampler)
		if !ok {
			return nil, errAuditUnavailable
		}
		return reader.SampleSchema(ctx, in.SourceID, in.N)
	}))
}

var errAuditUnavailable = &WireError{Code: CodeAudit, Message: auditFailedText, Detail: json.RawMessage(`{"kinds":["patterns-unavailable"]}`)}

// errNoAuditSource answers a read on a seat that holds no source (the keeper restarted, or the seat was closed after a long idle time).
var errNoAuditSource = &WireError{Code: CodeAudit, Message: auditFailedText, Detail: json.RawMessage(`{"kinds":["seat-gone","not-found"]}`)}

// auditRead runs one read on the caller's seat. The store call runs inside the handler; its answer is sent as a stream, so an answer
// of any size crosses the pipe in frames of at most MaxFrame.
func auditRead[T any](b *Backend, run func(context.Context, HostedRepo, T) (any, error)) Handler {
	return func(ctx context.Context, c *Call) (any, error) {
		var a AuditArgs[T]
		if err := c.Decode(&a); err != nil {
			return nil, err
		}
		key, err := b.seatKey(c, a.Instance)
		if err != nil {
			return nil, err
		}
		s, release, err := b.Audit.acquire(key, false)
		if err != nil {
			return nil, err
		}
		if s == nil {
			return nil, errNoAuditSource
		}
		defer release()
		if a.TimeoutMs > 0 {
			if d := time.Duration(a.TimeoutMs) * time.Millisecond; d < auditQueryTimeout {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, d)
				defer cancel()
			}
		}
		secrets := b.Audit.secretsOf(s, "")
		out, err := run(ctx, s.repo, a.Input)
		if err != nil {
			return nil, scrubbed(err, secrets)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return nil, &WireError{Code: CodeInternal, Message: "The key keeper could not prepare the answer."}
		}
		if len(raw) > auditAnswerCap {
			// Nothing is streamed: the client would only throw the answer away at its own cap.
			return nil, scrubbed(errAnswerTooLarge, secrets)
		}
		if leaks(raw, secrets) {
			return nil, errAnswerHeldSecret
		}
		c.Stream(func(_ context.Context, w io.Writer) error {
			_, err := w.Write(raw)
			return err
		})
		return struct{}{}, nil
	}
}

func (b *Backend) auditAdd(ctx context.Context, c *Call) (any, error) {
	var a AuditAddArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.Connection.SavedConnectionID
	key, err := b.seatKey(c, a.Instance)
	if err != nil {
		return nil, err
	}
	if a.Connection.Password != "" {
		// The keeper opens a source only with the password it stores. A typed password stays in the person's own Unravis.
		return nil, &WireError{Code: CodeInvalid, Message: "The key keeper opens a saved link only with the password it stores."}
	}
	in, err := b.mongoResolve(a.Connection)
	if err != nil {
		return nil, err
	}
	if err := keeperMongoAllowed(in); err != nil {
		return nil, err
	}
	s, release, err := b.Audit.acquire(key, true)
	if err != nil {
		return nil, err
	}
	defer release()
	secrets := b.Audit.secretsOf(s, in.Password)
	src, err := s.repo.AddSource(ctx, audit.SourceInput{
		ConnectionLabel: strings.TrimSpace(a.ConnectionLabel), Label: a.Label, Database: a.Database, Collection: a.Collection,
		URI: in.URI, Username: in.Username, Password: in.Password, AuthSource: in.AuthSource, TLSMode: in.TLSMode, CACertificate: in.CACertificate,
	})
	if err != nil {
		return nil, scrubbed(err, secrets)
	}
	if raw, _ := json.Marshal(src); leaks(raw, secrets) {
		_ = s.repo.DeleteSource(ctx, src.ID)
		return nil, errAnswerHeldSecret
	}
	b.Audit.remember(s, in.Password)
	return src, nil
}

func (b *Backend) auditSources(ctx context.Context, c *Call) (any, error) {
	var a AuditSeat
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	key, err := b.seatKey(c, a.Instance)
	if err != nil {
		return nil, err
	}
	s, release, err := b.Audit.acquire(key, false)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return []audit.Source{}, nil
	}
	defer release()
	secrets := b.Audit.secretsOf(s, "")
	items, err := s.repo.Sources(ctx)
	if err != nil {
		return nil, scrubbed(err, secrets)
	}
	if items == nil {
		items = []audit.Source{}
	}
	if raw, _ := json.Marshal(items); leaks(raw, secrets) {
		return nil, errAnswerHeldSecret
	}
	return items, nil
}

func (b *Backend) auditDelete(ctx context.Context, c *Call) (any, error) {
	var a AuditIDArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	key, err := b.seatKey(c, a.Instance)
	if err != nil {
		return nil, err
	}
	s, release, err := b.Audit.acquire(key, false)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errNoAuditSource
	}
	defer release()
	return struct{}{}, scrubbed(s.repo.DeleteSource(ctx, a.ID), b.Audit.secretsOf(s, ""))
}

func (b *Backend) auditClose(_ context.Context, c *Call) (any, error) {
	var a AuditSeat
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	key, err := b.seatKey(c, a.Instance)
	if err != nil {
		return nil, err
	}
	b.Audit.drop(key)
	return struct{}{}, nil
}
