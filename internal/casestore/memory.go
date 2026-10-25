package casestore

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"sync"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// NewMemory returns the F0 fake: the same semantics as the SQLite store (one
// file is one transaction, SHA-256 duplicates refused, caps enforced, erase
// idempotent) without encryption or persistence.
func NewMemory() Store { return NewMemoryWith(DefaultCaps, time.Now) }

// NewMemoryWith is NewMemory with explicit caps and clock, for tests.
func NewMemoryWith(caps Caps, now func() time.Time) Store {
	if now == nil {
		now = time.Now
	}
	return &memStore{cases: map[CaseID]*memData{}, caps: caps, now: now}
}

type memStore struct {
	mu    sync.Mutex
	cases map[CaseID]*memData
	caps  Caps
	now   func() time.Time
}

// memData is one case. importMu serialises imports for the whole of an import
// (run included); mu guards the fields readers see.
type memData struct {
	importMu sync.Mutex

	mu      sync.RWMutex
	info    CaseInfo
	key     []byte
	imports []ImportRecord
	events  []evidence.Event
	ids     map[string]struct{}
	erased  bool
	nextID  int64

	quarantines []QuarantineRecord
	nextQID     int64

	disc *memDiscover // discovery tables, created on first use
}

func (s *memStore) Create(ctx context.Context, title string) (CaseInfo, error) {
	if err := ctx.Err(); err != nil {
		return CaseInfo{}, err
	}
	if !ValidTitle(title) {
		return CaseInfo{}, ErrBadTitle
	}
	id, err := NewID()
	if err != nil {
		return CaseInfo{}, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return CaseInfo{}, err
	}
	d := &memData{
		info: CaseInfo{ID: id, Title: title, Created: s.now().UTC()},
		key:  key,
		ids:  map[string]struct{}{},
	}
	s.mu.Lock()
	s.cases[id] = d
	s.mu.Unlock()
	return copyInfo(d.info), nil
}

func copyInfo(i CaseInfo) CaseInfo {
	if i.Closed != nil {
		c := *i.Closed
		i.Closed = &c
	}
	return i
}

func (s *memStore) get(id CaseID) (*memData, error) {
	if !id.Valid() {
		return nil, ErrBadID
	}
	s.mu.Lock()
	d, ok := s.cases[id]
	s.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	return d, nil
}

func (s *memStore) List(ctx context.Context) ([]CaseInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	all := make([]*memData, 0, len(s.cases))
	for _, d := range s.cases {
		all = append(all, d)
	}
	s.mu.Unlock()
	out := make([]CaseInfo, 0, len(all))
	for _, d := range all {
		d.mu.RLock()
		out = append(out, copyInfo(d.info))
		d.mu.RUnlock()
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *memStore) Open(ctx context.Context, id CaseID) (Case, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d, err := s.get(id)
	if err != nil {
		return nil, err
	}
	return &memCase{s: s, d: d}, nil
}

func (s *memStore) CloseCase(ctx context.Context, id CaseID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d, err := s.get(id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.info.Closed == nil { // closing twice keeps the first time
		t := s.now().UTC()
		d.info.Closed = &t
	}
	return nil
}

func (s *memStore) Erase(ctx context.Context, id CaseID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !id.Valid() {
		return ErrBadID
	}
	s.mu.Lock()
	d, ok := s.cases[id]
	delete(s.cases, id)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	d.erase()
	return nil
}

// erase wipes the key and the data. Handles still open see ErrNotFound.
func (d *memData) erase() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.key {
		d.key[i] = 0
	}
	d.key, d.imports, d.events, d.ids, d.quarantines, d.disc = nil, nil, nil, nil, nil, nil
	d.erased = true
}

func (s *memStore) Sweep(ctx context.Context, now time.Time) ([]CaseID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	var due []CaseID
	for id, d := range s.cases {
		d.mu.RLock()
		if c := d.info.Closed; c != nil && now.Sub(*c) > Retention {
			due = append(due, id)
		}
		d.mu.RUnlock()
	}
	s.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i] < due[j] })
	for _, id := range due {
		if err := s.Erase(ctx, id); err != nil {
			return due, err
		}
	}
	return due, nil
}

type memCase struct {
	s      *memStore
	d      *memData
	mu     sync.Mutex
	closed bool
}

func (c *memCase) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrHandleClosed
	}
	c.d.mu.RLock()
	erased := c.d.erased
	c.d.mu.RUnlock()
	if erased {
		return ErrNotFound
	}
	return nil
}

func (c *memCase) Info() CaseInfo {
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	return copyInfo(c.d.info)
}

func (c *memCase) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return nil
}

func copyRecord(r ImportRecord) ImportRecord {
	r.KnownGaps = append([]string(nil), r.KnownGaps...)
	r.Supplied = copySupplied(r.Supplied)
	return r
}

func (c *memCase) Imports(ctx context.Context) ([]ImportRecord, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	out := make([]ImportRecord, len(c.d.imports))
	for i, r := range c.d.imports {
		out[i] = copyRecord(r)
	}
	return out, nil
}

// Quarantines lists refused files, oldest first (see Quarantiner).
func (c *memCase) Quarantines(ctx context.Context) ([]QuarantineRecord, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	return append([]QuarantineRecord(nil), c.d.quarantines...), nil
}

var _ Quarantiner = (*memCase)(nil)

func contains(set []string, v string) bool {
	if len(set) == 0 {
		return true
	}
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func (c *memCase) Events(ctx context.Context, q EventQuery) ([]evidence.Event, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 || limit > MaxEventQuery {
		limit = MaxEventQuery
	}
	c.d.mu.RLock()
	var out []evidence.Event
	for _, ev := range c.d.events {
		if q.Window.Contains(ev.Start) && contains(q.Kinds, ev.Kind) && contains(q.Sources, ev.Source) && contains(q.Hosts, ev.Host) {
			out = append(out, ev)
		}
	}
	c.d.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].EvidenceID < out[j].EvidenceID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		out[i] = out[i].Clone()
	}
	return out, nil
}

// Each streams the matching events, grouped by kind and ordered by Start within
// a kind (the order the SQLite store reads them in). See Case.Each.
func (c *memCase) Each(ctx context.Context, q EventQuery, fn func(evidence.Event) error) error {
	if err := c.check(ctx); err != nil {
		return err
	}
	budget := q.NewScanBudget()
	c.d.mu.RLock()
	var sel []evidence.Event
	for _, ev := range c.d.events {
		if q.Window.Contains(ev.Start) && contains(q.Kinds, ev.Kind) && contains(q.Sources, ev.Source) && contains(q.Hosts, ev.Host) {
			sel = append(sel, ev)
		}
	}
	c.d.mu.RUnlock()
	sort.SliceStable(sel, func(i, j int) bool {
		if sel[i].Kind != sel[j].Kind {
			return sel[i].Kind < sel[j].Kind
		}
		return sel[i].Start.Before(sel[j].Start)
	})
	for i := range sel {
		if i%handleCheckEventsEvery == 0 {
			if err := c.check(ctx); err != nil {
				return err
			}
		}
		if err := budget.Admit(); err != nil {
			return err
		}
		if err := fn(sel[i].Clone()); err != nil {
			return err
		}
	}
	return nil
}

// Pseudonymize maps a value to a stable label for this case only:
// kind + "-" + 8 hex characters of HMAC-SHA256 under the case key. An empty
// value maps to "" (there is nothing to hide). The key never leaves the case.
func (c *memCase) Pseudonymize(kind, value string) string {
	if value == "" {
		return ""
	}
	c.d.mu.RLock()
	key := c.d.key
	c.d.mu.RUnlock()
	if key == nil {
		return "" // erased
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(kind))
	m.Write([]byte{0})
	m.Write([]byte(value))
	return kind + "-" + hex.EncodeToString(m.Sum(nil)[:4])
}

// countingReader hashes and counts what the importer reads and fails with
// ErrCapExceeded when more than max bytes arrive.
type countingReader struct {
	r    io.Reader
	h    interface{ Write([]byte) (int, error) }
	n    int64
	max  int64
	over bool
}

func (cr *countingReader) Read(p []byte) (int, error) {
	if cr.over {
		return 0, ErrCapExceeded
	}
	n, err := cr.r.Read(p)
	if n > 0 {
		cr.n += int64(n)
		if cr.n > cr.max {
			cr.over = true
			return 0, ErrCapExceeded
		}
		cr.h.Write(p[:n])
	}
	return n, err
}

func (c *memCase) Import(ctx context.Context, meta ImportMeta, body io.Reader, run RunFunc) (ImportResult, error) {
	var res ImportResult
	if err := c.check(ctx); err != nil {
		return res, err
	}
	if !meta.AckGating || meta.AckText == "" {
		return res, ErrAckRequired
	}
	if len(meta.AckText) > MaxAckText {
		return res, ErrAckTooLong
	}
	if run == nil || body == nil {
		return res, errors.New("casestore: import needs a body and a run function")
	}
	supplied, err := CleanSupplied(meta.Supplied)
	if err != nil {
		return res, err
	}
	meta.Supplied = supplied
	// One import at a time per case.
	c.d.importMu.Lock()
	defer c.d.importMu.Unlock()

	c.d.mu.RLock()
	used := c.d.info.Bytes
	closedAlready := c.d.info.Closed != nil
	c.d.mu.RUnlock()
	if closedAlready {
		return res, ErrCaseClosed
	}
	max := c.s.caps.MaxFileBytes
	if room := c.s.caps.MaxCaseBytes - used; room < max {
		max = room
	}
	if max < 0 {
		max = 0
	}
	h := sha256.New()
	cr := &countingReader{r: body, h: h, max: max}

	var staged []evidence.Event
	seen := map[string]struct{}{}
	caseID := string(c.d.info.ID) // immutable after Create
	// first is the first error emit returned. A run that ignores an emit error
	// and returns nil must not commit a smaller import that looks complete: the
	// import fails with this error, and emit keeps refusing after it.
	var first error
	emit := func(ev evidence.Event) error {
		if first != nil {
			return first
		}
		if err := c.admit(ctx, ev, caseID, meta, int64(len(staged)), seen); err != nil {
			first = err
			return err
		}
		seen[ev.EvidenceID] = struct{}{}
		staged = append(staged, ev.Clone())
		return nil
	}

	rep, err := run(cr, emit)
	res.Report = rep
	res.Record = c.record(meta, rep, cr.n, "")
	if cr.over {
		return res, ErrCapExceeded
	}
	if err == nil && first != nil {
		err = first
	}
	if err != nil {
		var qe *importer.QuarantineError
		if errors.As(err, &qe) {
			return c.quarantine(ctx, meta, qe, cr, h, res, err)
		}
		return res, err
	}
	// The importer may stop before the end of the input; the hash covers all of it.
	if _, err := io.Copy(io.Discard, cr); err != nil {
		if cr.over {
			return res, ErrCapExceeded
		}
		return res, err
	}
	if rep.Rows > c.s.caps.MaxFileRows {
		return res, ErrCapExceeded
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	res.Record.SHA256, res.Record.Bytes = sum, cr.n

	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	if c.d.erased {
		return res, ErrNotFound
	}
	if c.d.info.Closed != nil {
		return res, ErrCaseClosed // closed while the import ran
	}
	for _, r := range c.d.imports {
		if r.SHA256 == sum {
			dup := copyRecord(r)
			dup.Status = StatusDuplicate
			res.Record = dup
			return res, &DuplicateError{Existing: copyRecord(r)}
		}
	}
	if c.d.info.Bytes+cr.n > c.s.caps.MaxCaseBytes {
		return res, ErrCapExceeded
	}
	c.d.nextID++
	res.Record.ID = c.d.nextID
	res.Record.Status = StatusOK
	res.Record.Imported = c.s.now().UTC()
	c.d.imports = append(c.d.imports, copyRecord(res.Record))
	c.d.events = append(c.d.events, staged...)
	for id := range seen {
		c.d.ids[id] = struct{}{}
	}
	c.d.info.Bytes += cr.n
	c.d.info.Imports = len(c.d.imports)
	return res, nil
}

// admit checks one emitted event: context, row cap, event shape and
// provenance (ID prefix, Source, attribute allowlist) and ID uniqueness within
// the file and the case.
func (c *memCase) admit(ctx context.Context, ev evidence.Event, caseID string, meta ImportMeta, staged int64, seen map[string]struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if staged >= c.s.caps.MaxFileRows {
		return ErrCapExceeded
	}
	imp := meta.Importer
	if imp == "" {
		imp = ev.Source
	}
	allowed := meta.AllowedAttrs
	if allowed == nil {
		// No allowlist supplied: keep the generic bounds, skip the key check.
		allowed = make(map[string]bool, len(ev.Attrs))
		for k := range ev.Attrs {
			allowed[k] = true
		}
	}
	if err := ev.ValidateFor(caseID, imp, allowed); err != nil {
		return err
	}
	if _, dup := seen[ev.EvidenceID]; dup {
		return ErrDuplicateEvent
	}
	c.d.mu.RLock()
	_, dup := c.d.ids[ev.EvidenceID]
	c.d.mu.RUnlock()
	if dup {
		return ErrDuplicateEvent
	}
	return nil
}

// quarantine reads the rest of the body so the hash covers the whole file,
// then records the refusal. It stores no events and no import. A cap breach
// while reading on wins (the file is too big to even refuse by hash).
func (c *memCase) quarantine(ctx context.Context, meta ImportMeta, qe *importer.QuarantineError, cr *countingReader, h interface{ Sum([]byte) []byte }, res ImportResult, cause error) (ImportResult, error) {
	if _, err := io.Copy(io.Discard, cr); err != nil {
		if cr.over {
			return res, ErrCapExceeded
		}
		return res, err
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	res.Record.SHA256, res.Record.Bytes = sum, cr.n
	res.Record.Status, res.Record.Reason = StatusQuarantined, qe.Reason
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	if c.d.erased {
		return res, ErrNotFound
	}
	c.d.nextQID++
	c.d.quarantines = append(c.d.quarantines, QuarantineRecord{
		ID: c.d.nextQID, Importer: res.Record.Importer, FileName: meta.FileName, SHA256: sum,
		Reason: qe.Reason, Column: importer.SafeColumn(qe.Column), Rows: res.Report.Rows, At: c.s.now().UTC(),
	})
	return res, cause
}

func (c *memCase) record(meta ImportMeta, rep importer.Report, bytes int64, sha string) ImportRecord {
	name := meta.Importer
	if name == "" {
		name = rep.Importer
	}
	return ImportRecord{
		Importer: name, FileName: meta.FileName, SHA256: sha, Bytes: bytes,
		Rows: rep.Rows, Rejected: rep.Rejected, HostLabel: meta.HostLabel, TZRule: meta.DerivedTZRule(rep), AckText: meta.AckText,
		Quality: rep.Quality, KnownGaps: append([]string(nil), rep.KnownGaps...),
		Supplied: copySupplied(meta.Supplied),
	}
}
