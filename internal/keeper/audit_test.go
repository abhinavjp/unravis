package keeper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

/* ------------------------------------------------------ fake hosted repo */

// hostedFactory makes the audit repositories the keeper hosts in tests, one per seat, and remembers them. leak makes every repository
// it made put the password it was handed into its answers ("answer") or its errors ("error"), as a broken driver might.
type hostedFactory struct {
	mu   sync.Mutex
	made []*fakeHosted
	leak string
	big  int // bytes of warnings Analyze answers with

	// gate, when set, makes Analyze wait until it is closed (or its context ends). started counts the reads that began, canceled the
	// ones whose context ended while they waited.
	gate     chan struct{}
	started  atomic.Int32
	canceled atomic.Int32
}

func (f *hostedFactory) make() HostedRepo {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := &fakeHosted{factory: f}
	f.made = append(f.made, r)
	return r
}

func (f *hostedFactory) set(leak string, big int) {
	f.mu.Lock()
	f.leak, f.big = leak, big
	f.mu.Unlock()
}

func (f *hostedFactory) mode() (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.leak, f.big
}

func (f *hostedFactory) repos() []*fakeHosted {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*fakeHosted(nil), f.made...)
}

// fakeHosted answers from memory and never connects. It is also used as Unravis's own repository behind the router.
type fakeHosted struct {
	factory   *hostedFactory
	mu        sync.Mutex
	passwords []string
	sources   []audit.Source
	next      int
	closed    int
	prefix    string
}

var rollupAt = time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)

func (f *fakeHosted) password() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.passwords) == 0 {
		return ""
	}
	return f.passwords[len(f.passwords)-1]
}

func (f *fakeHosted) mode() (string, int) {
	if f.factory == nil {
		return "", 0
	}
	return f.factory.mode()
}

func (f *fakeHosted) Sources(context.Context) ([]audit.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]audit.Source(nil), f.sources...), nil
}

func (f *fakeHosted) AddSource(_ context.Context, in audit.SourceInput) (audit.Source, error) {
	leak, _ := f.mode()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.passwords = append(f.passwords, in.Password)
	if leak == "error" {
		return audit.Source{}, &audit.ConnectionError{Stage: "authentication", Code: "credentials_rejected", Message: "MongoDB rejected " + in.Password}
	}
	f.next++
	prefix := f.prefix
	if prefix == "" {
		prefix = "kept"
	}
	src := audit.Source{ID: fmt.Sprintf("%s-%d", prefix, f.next), Label: in.Label, Database: in.Database, Collection: in.Collection, SelectionKey: in.Database + "/" + in.Collection}
	if leak == "answer" {
		src.Label = "opened with " + in.Password
	}
	f.sources = append(f.sources, src)
	return src, nil
}

func (f *fakeHosted) DeleteSource(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, s := range f.sources {
		if s.ID == id {
			f.sources = append(f.sources[:i], f.sources[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%w: source not found", audit.ErrNotFound)
}

func (f *fakeHosted) Analyze(ctx context.Context, q audit.Query) (audit.Analysis, error) {
	if f.factory != nil && f.factory.gate != nil {
		f.factory.started.Add(1)
		select {
		case <-f.factory.gate:
		case <-ctx.Done():
			f.factory.canceled.Add(1)
			return audit.Analysis{}, ctx.Err()
		}
	}
	leak, big := f.mode()
	pw := f.password()
	switch leak {
	case "error":
		return audit.Analysis{}, fmt.Errorf("%w: the driver said password=%s", audit.ErrSummaryRowLimit, pw)
	case "answer":
		return audit.Analysis{Warnings: []string{"connected with " + pw}}, nil
	}
	out := audit.Analysis{Query: q, Sources: []audit.SourceResult{{SourceID: strings.Join(q.SourceIDs, ","), OK: true}}}
	for n := 0; n < big; n += 1000 {
		out.Warnings = append(out.Warnings, strings.Repeat("w", 1000))
	}
	return out, nil
}

func (f *fakeHosted) Requests(context.Context, audit.PageInput) (audit.Page, error) {
	return audit.Page{}, nil
}

func (f *fakeHosted) Payload(_ context.Context, in audit.PayloadInput) (audit.Payload, error) {
	return audit.Payload{Request: in.SourceID}, nil
}

func (f *fakeHosted) Record(context.Context, audit.RecordInput) (audit.RecordDetail, error) {
	return audit.RecordDetail{}, nil
}

func (f *fakeHosted) Rollup(_ context.Context, plan audit.RollupPlan) (audit.RollupSlots, error) {
	b := audit.NewRollupBuilder(plan.Width)
	out := audit.RollupSlots{Builder: b}
	for _, id := range plan.SourceIDs {
		d := 100.0
		b.AddRow(rollupAt, "synchronous", "valid", audit.StatusSuccess, &d, plan.SlowMs)
		out.Sources = append(out.Sources, audit.SourceResult{SourceID: id, OK: true})
	}
	return out, nil
}

func (f *fakeHosted) Close(context.Context) error {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
	return nil
}

/* ---------------------------------------------------------------- helpers */

// rememberMongo saves a MongoDB link with password in the keeper and returns the settings that name it (no password).
func (h *harness) rememberMongo(password string) audit.SourceInput {
	h.t.Helper()
	link := mongoLink
	link.Password = password
	rec, err := NewMongoStore(h.client).Remember("Audit prod", link, "events", true)
	if err != nil || !rec.HasCredentials {
		h.t.Fatalf("remember: %+v %v", rec, err)
	}
	return audit.SourceInput{URI: mongoLink.URI, Username: mongoLink.Username, AuthSource: mongoLink.AuthSource, TLSMode: mongoLink.TLSMode, SavedConnectionID: rec.ID, Database: "audit", Collection: "events", Label: "Prod audit"}
}

func plan(t *testing.T, ids ...string) audit.RollupPlan {
	t.Helper()
	p, err := audit.PlanRollup(audit.RollupInput{SourceIDs: ids, From: rollupAt.Add(-time.Hour), To: rollupAt.Add(time.Hour), TimeZone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func total(s audit.RollupSlots) int64 {
	var n int64
	for _, slot := range s.Builder.Wire().Slots {
		n += slot.Counts.Total
	}
	return n
}

/* ------------------------------------------------------------------ tests */

func TestAuditSourceFromASavedLinkIsOpenedByTheKeeper(t *testing.T) {
	h := newHarness(t, nil)
	in := h.rememberMongo(mongoSecret)
	local := &fakeHosted{prefix: "local"}
	r := NewAuditRouter(local, h.client)
	ctx := context.Background()

	src, err := r.AddHeldSource(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	repos := h.hosted.repos()
	if len(repos) != 1 || repos[0].password() != mongoSecret {
		t.Fatalf("the keeper's repository was not opened with the stored password (%d repositories)", len(repos))
	}
	if len(local.passwords) != 0 {
		t.Fatal("Unravis's own repository was asked to open a keeper-held source")
	}
	items, states, err := r.SourcesWithKeeper(ctx)
	if err != nil || len(items) != 1 || items[0].ID != src.ID || states[src.ID] != HeldByKeeper {
		t.Fatalf("sources = %+v %v %v", items, states, err)
	}
	a, err := r.Analyze(ctx, audit.Query{SourceIDs: []string{src.ID}})
	if err != nil || len(a.Sources) != 1 || a.Sources[0].SourceID != src.ID {
		t.Fatalf("analyze = %+v %v", a, err)
	}
	if p, err := r.Payload(ctx, audit.PayloadInput{SourceID: src.ID}); err != nil || p.Request != src.ID {
		t.Fatalf("payload = %+v %v", p, err)
	}
	// A typed password is never sent to the keeper: the router refuses it, and so does the keeper itself.
	typed := in
	typed.Password = "typed-now"
	if _, err := r.AddHeldSource(ctx, typed); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("typed password to the keeper: %v", err)
	}
	conn := audit.ConnectionInput{URI: in.URI, Username: in.Username, Password: "typed-now", AuthSource: in.AuthSource, TLSMode: in.TLSMode, SavedConnectionID: in.SavedConnectionID}
	if err := h.client.Call(ctx, OpAuditAdd, AuditAddArgs{Instance: "abc", Connection: conn}, nil); wireCode(err) != CodeInvalid {
		t.Fatalf("keeper took a typed password: %v", err)
	}
	// A link the keeper holds no password for, or a changed address, is refused by the keeper.
	moved := in
	moved.URI = "mongodb://elsewhere.example.test:27017"
	if _, err := r.AddHeldSource(ctx, moved); err == nil {
		t.Fatal("a changed address opened a source")
	}
	// Removing the source removes it in the keeper; closing Unravis closes its seat there.
	if err := r.DeleteSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	if items, _ := repos[0].Sources(ctx); len(items) != 0 {
		t.Fatalf("keeper still has %+v", items)
	}
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if h.backend.Audit.Seats() != 0 || repos[0].closed != 1 || local.closed != 1 {
		t.Fatalf("after close: seats %d, keeper repo closed %d, local closed %d", h.backend.Audit.Seats(), repos[0].closed, local.closed)
	}
	h.noSecrets()
}

func TestAuditAnswersLargerThanOneFrameAreStreamed(t *testing.T) {
	h := newHarness(t, nil)
	r := NewAuditRouter(&fakeHosted{prefix: "local"}, h.client)
	src, err := r.AddHeldSource(context.Background(), h.rememberMongo(mongoSecret))
	if err != nil {
		t.Fatal(err)
	}
	h.hosted.set("", 3*MaxFrame)
	a, err := r.Analyze(context.Background(), audit.Query{SourceIDs: []string{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, w := range a.Warnings {
		n += len(w)
	}
	if n < 3*MaxFrame {
		t.Fatalf("got %d bytes of the answer, want at least %d", n, 3*MaxFrame)
	}
}

// A distinctive password must not leave the keeper in any audit answer, error or log line, even when the hosted repository puts it
// in its answers or errors.
func TestAuditCanaryPasswordNeverLeavesTheKeeper(t *testing.T) {
	const canary = `Canary-9f2e<&>"pw"-AUDIT`
	h := newHarness(t, nil)
	in := h.rememberMongo(canary)
	r := NewAuditRouter(&fakeHosted{prefix: "local"}, h.client)
	ctx := context.Background()
	var errs []error

	// Opening: the repository fails with the password in its text, then answers with it.
	h.hosted.set("error", 0)
	_, err := r.AddHeldSource(ctx, in)
	var ce *audit.ConnectionError
	if !errors.As(err, &ce) {
		t.Fatalf("a connection failure is still a connection failure: %v", err)
	}
	errs = append(errs, err)
	h.hosted.set("answer", 0)
	_, err = r.AddHeldSource(ctx, in)
	if err == nil {
		t.Fatal("an answer holding the password was sent")
	}
	errs = append(errs, err)
	for _, repo := range h.hosted.repos() {
		if items, _ := repo.Sources(ctx); len(items) != 0 {
			t.Fatalf("the refused source stayed open: %+v", items)
		}
	}

	// Reading: the same, on an open source.
	h.hosted.set("", 0)
	src, err := r.AddHeldSource(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	h.hosted.set("error", 0)
	_, err = r.Analyze(ctx, audit.Query{SourceIDs: []string{src.ID}})
	if !errors.Is(err, audit.ErrSummaryRowLimit) {
		t.Fatalf("the error kind is kept: %v", err)
	}
	errs = append(errs, err)
	h.hosted.set("answer", 0)
	_, err = r.Analyze(ctx, audit.Query{SourceIDs: []string{src.ID}})
	if err == nil {
		t.Fatal("an answer holding the password was sent")
	}
	errs = append(errs, err)

	escaped, _ := json.Marshal(canary)
	for _, needle := range []string{canary, string(escaped[1 : len(escaped)-1]), "Canary-9f2e"} {
		for _, e := range errs {
			if strings.Contains(e.Error(), needle) {
				t.Fatalf("an error carried the password: %v", e)
			}
		}
		if strings.Contains(h.wire.String(), needle) {
			t.Fatal("a keeper answer carried the password")
		}
		if strings.Contains(h.log.String(), needle) {
			t.Fatal("the keeper's audit log holds the password")
		}
	}
}

func TestAuditSeatsAreKeptApart(t *testing.T) {
	h := newHarness(t, nil)
	// A clock that moves on every reading: "least recently used" is decided by time stamps, and the Windows wall clock can give two
	// uses in a row the same stamp, which left the evicted seat to chance.
	var tick atomic.Int64
	start := time.Now()
	h.backend.Audit.now = func() time.Time { return start.Add(time.Duration(tick.Add(1)) * time.Millisecond) }
	in := h.rememberMongo(mongoSecret)
	ctx := context.Background()
	a := NewAuditRouter(&fakeHosted{prefix: "local"}, h.client)
	src, err := a.AddHeldSource(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	// Another Unravis of the same person (another instance) has its own, empty seat.
	b := NewAuditRouter(&fakeHosted{prefix: "local"}, h.client)
	if items, err := b.Sources(ctx); err != nil || len(items) != 0 {
		t.Fatalf("b sees %+v %v", items, err)
	}
	if err := h.client.Call(ctx, OpAuditPayload, AuditArgs[audit.PayloadInput]{Instance: b.instance, Input: audit.PayloadInput{SourceID: src.ID}}, nil); !errors.Is(auditErr(err), ErrHeldSourceGone) {
		t.Fatalf("b read a's source: %v", err)
	}
	// Another Windows account, or another process, naming a's instance gets nothing either.
	for _, caller := range []Caller{{SID: "S-1-5-21-9-9-9-2002", Name: `PC\other`, PID: 4242, Session: 1}, {SID: testSID, Name: `PC\analyst`, PID: 777, Session: 1}} {
		other := NewClient(h.l.Dialer(caller))
		err := other.Call(ctx, OpAuditPayload, AuditArgs[audit.PayloadInput]{Instance: a.instance, Input: audit.PayloadInput{SourceID: src.ID}}, nil)
		if !errors.Is(auditErr(err), ErrHeldSourceGone) {
			t.Fatalf("%s/%d read a's source: %v", caller.Name, caller.PID, err)
		}
	}
	if err := h.client.Call(ctx, OpAuditSources, AuditSeat{Instance: "Not-An-Id!"}, nil); wireCode(err) != CodeInvalid {
		t.Fatalf("bad instance: %v", err)
	}

	// A person holds at most PerPerson seats: a new one closes their least recently used idle seat, whose sources are then gone.
	h.backend.Audit.PerPerson = 2
	if _, err := b.AddHeldSource(ctx, in); err != nil {
		t.Fatal(err)
	}
	c := NewAuditRouter(&fakeHosted{prefix: "local"}, h.client)
	if _, err := c.AddHeldSource(ctx, in); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Analyze(ctx, audit.Query{SourceIDs: []string{src.ID}}); !errors.Is(err, ErrHeldSourceGone) {
		t.Fatalf("a's evicted seat: %v", err)
	}
	if items, _ := a.Sources(ctx); len(items) != 0 {
		t.Fatalf("a still lists %+v", items)
	}
	// The whole keeper holds at most MaxSeats: then a new seat is refused as busy.
	h.backend.Audit.MaxSeats = 2
	other := NewAuditRouter(&fakeHosted{prefix: "local"}, NewClient(h.l.Dialer(Caller{SID: "S-1-5-21-9-9-9-2002", Name: `PC\other`, PID: 99, Session: 2})))
	if _, err := other.AddHeldSource(ctx, in); wireCode(err) != CodeBusy {
		t.Fatalf("seat over the limit: %v", err)
	}
	// Seats nobody used for IdleAfter are closed.
	h.backend.Audit.now = func() time.Time { return time.Now().Add(DefaultSeatIdle + time.Minute) }
	if items, _ := b.Sources(ctx); len(items) != 0 {
		t.Fatalf("idle seat still open: %+v", items)
	}
	if n := h.backend.Audit.Seats(); n != 0 {
		t.Fatalf("seats after idle = %d", n)
	}
}

// auditErr turns a raw keeper answer into the router's error, as the router does.
func auditErr(err error) error {
	var we *WireError
	if errors.As(err, &we) && we.Code == CodeAudit {
		return auditNative(we)
	}
	return err
}

func TestAuditRouterMergesOnlyTheTimelineAndSurvivesAStoppedKeeper(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	local := &fakeHosted{prefix: "local"}
	r := NewAuditRouter(local, h.client)
	held, err := r.AddHeldSource(ctx, h.rememberMongo(mongoSecret))
	if err != nil {
		t.Fatal(err)
	}
	mine, err := r.AddSource(ctx, audit.SourceInput{Password: "typed", Database: "audit", Collection: "other"})
	if err != nil {
		t.Fatal(err)
	}
	// The same collection from both sides is one source too many.
	if _, err := r.AddSource(ctx, audit.SourceInput{Password: "typed", Database: "audit", Collection: "events"}); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("duplicate across sides: %v", err)
	}
	if items, _ := local.Sources(ctx); len(items) != 1 {
		t.Fatalf("the refused duplicate stayed open here: %+v", items)
	}
	if _, err := r.Analyze(ctx, audit.Query{SourceIDs: []string{held.ID, mine.ID}}); !errors.Is(err, ErrMixedSources) {
		t.Fatalf("mixed read: %v", err)
	}
	slots, err := r.Rollup(ctx, plan(t, mine.ID, held.ID))
	if err != nil {
		t.Fatal(err)
	}
	if total(slots) != 2 || len(slots.Sources) != 2 || slots.Sources[0].SourceID != mine.ID || slots.Sources[1].SourceID != held.ID {
		t.Fatalf("merged rollup: total %d, sources %+v", total(slots), slots.Sources)
	}

	h.stop()
	items, states, err := r.SourcesWithKeeper(ctx)
	if err != nil || len(items) != 2 || states[held.ID] != HeldButDown || states[mine.ID] != "" {
		t.Fatalf("sources when stopped = %+v %v %v", items, states, err)
	}
	if _, err := r.Analyze(ctx, audit.Query{SourceIDs: []string{held.ID}}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("read when stopped: %v", err)
	}
	if _, err := r.Analyze(ctx, audit.Query{SourceIDs: []string{mine.ID}}); err != nil {
		t.Fatalf("a typed source still reads: %v", err)
	}
	slots, err = r.Rollup(ctx, plan(t, mine.ID, held.ID))
	if err != nil {
		t.Fatal(err)
	}
	if total(slots) != 1 || len(slots.Sources) != 2 || slots.Sources[1].ErrorCode != "keeper_stopped" || slots.Sources[1].OK {
		t.Fatalf("rollup when stopped: total %d, sources %+v", total(slots), slots.Sources)
	}
	if _, err := r.Rollup(ctx, plan(t, held.ID)); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("keeper-only rollup when stopped: %v", err)
	}
	if err := r.DeleteSource(ctx, held.ID); err != nil {
		t.Fatalf("remove when stopped: %v", err)
	}
	if items, _ := r.Sources(ctx); len(items) != 1 || items[0].ID != mine.ID {
		t.Fatalf("after remove = %+v", items)
	}
}

func TestAuditReadsHaveTheirOwnRateAndAreFoldedInTheLog(t *testing.T) {
	h := newHarness(t, map[string]int{classRead: 10000, classWrite: 10000, classReach: 10000, classBlob: 10000, classAudit: 5})
	ctx := context.Background()
	r := NewAuditRouter(&fakeHosted{prefix: "local"}, h.client)
	src, err := r.AddHeldSource(ctx, h.rememberMongo(mongoSecret))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := r.Analyze(ctx, audit.Query{SourceIDs: []string{src.ID}}); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	if _, err := r.Analyze(ctx, audit.Query{SourceIDs: []string{src.ID}}); wireCode(err) != CodeRateLimited {
		t.Fatalf("read over the audit rate: %v", err)
	}
	// Other classes are not used up by audit reads.
	if _, err := r.Sources(ctx); err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(h.log.String(), `"op":"audit.analyze","record":`) + strings.Count(h.log.String(), `"op":"audit.analyze","outcome":"ok"`)
	if lines != 1 {
		t.Fatalf("audit reads written %d times, want once per minute:\n%s", lines, h.log.String())
	}
	if !strings.Contains(h.log.String(), `"op":"audit.add"`) {
		t.Fatalf("opening a source is written:\n%s", h.log.String())
	}
}

func TestAuditOperationsAreNamedAndReadOnly(t *testing.T) {
	h := newHarness(t, nil)
	h.server.mu.RLock()
	defer h.server.mu.RUnlock()
	var audits []string
	for op := range h.server.ops {
		if strings.HasPrefix(op, ReservedPrefixP14b) {
			audits = append(audits, op)
		}
	}
	want := map[string]bool{OpAuditAdd: true, OpAuditSources: true, OpAuditDelete: true, OpAuditClose: true, OpAuditAnalyze: true,
		OpAuditRequests: true, OpAuditPayload: true, OpAuditRecord: true, OpAuditActivity: true, OpAuditBreakdown: true,
		OpAuditCoverage: true, OpAuditSlowest: true, OpAuditRollup: true, OpAuditSchema: true}
	if len(audits) != len(want) {
		t.Fatalf("audit operations = %v", audits)
	}
	for _, op := range audits {
		if !want[op] {
			t.Fatalf("unexpected audit operation %q", op)
		}
	}
}
