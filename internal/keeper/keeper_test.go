package keeper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/sqlconn"
)

const (
	sqlSecret   = "Tr0ub4dor&3-sql-SECRET"
	mongoSecret = "c0rrect-horse-mongo-SECRET"
	bbSecret    = "bbkey-battery-staple-SECRET"
	testSID     = "S-1-5-21-1000-1000-1000-1001"
)

type xorProtector struct{}

func (xorProtector) Protect(v []byte) ([]byte, error) {
	out := append([]byte("sealed:"), v...)
	for i := len("sealed:"); i < len(out); i++ {
		out[i] ^= 0x5a
	}
	return out, nil
}

func (xorProtector) Unprotect(v []byte) ([]byte, error) {
	if !bytes.HasPrefix(v, []byte("sealed:")) {
		return nil, errors.New("bad")
	}
	out := append([]byte(nil), v[len("sealed:"):]...)
	for i := range out {
		out[i] ^= 0x5a
	}
	return out, nil
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

/* --------------------------------------------------------- fake runner */

type fakeRunner struct {
	mu        sync.Mutex
	sqlSeen   []dbhealth.SQLTarget
	mongoSeen []audit.ConnectionInput
	sqlErr    error
	mongoErr  error
	block     chan struct{}
	started   chan struct{}
}

func (f *fakeRunner) sawSQL(t dbhealth.SQLTarget) error {
	f.mu.Lock()
	f.sqlSeen = append(f.sqlSeen, t)
	err := f.sqlErr
	f.mu.Unlock()
	return err
}

func (f *fakeRunner) sawMongo(in audit.ConnectionInput) error {
	f.mu.Lock()
	f.mongoSeen = append(f.mongoSeen, in)
	err := f.mongoErr
	f.mu.Unlock()
	return err
}

func (f *fakeRunner) lastSQL() dbhealth.SQLTarget {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sqlSeen[len(f.sqlSeen)-1]
}

func (f *fakeRunner) lastMongo() audit.ConnectionInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mongoSeen[len(f.mongoSeen)-1]
}

func (f *fakeRunner) TestSQL(_ context.Context, t dbhealth.SQLTarget) (dbhealth.TestResult, error) {
	if err := f.sawSQL(t); err != nil {
		return dbhealth.TestResult{}, err
	}
	return dbhealth.TestResult{Version: "16.0", Edition: "Developer", Databases: []string{"Payroll"}}, nil
}

func (f *fakeRunner) HealthSQL(ctx context.Context, t dbhealth.SQLTarget, label string) (dbhealth.Report, error) {
	if err := f.sawSQL(t); err != nil {
		return dbhealth.Report{}, err
	}
	if f.block != nil {
		f.started <- struct{}{}
		select {
		case <-f.block:
		case <-ctx.Done():
		}
	}
	return dbhealth.Report{Kind: "sql", Label: label, Database: t.Database, Overall: "ok"}, nil
}

func (f *fakeRunner) LaneSQL(_ context.Context, t dbhealth.SQLTarget, label string, req dbhealth.LaneRequest) (dbhealth.Report, error) {
	if err := f.sawSQL(t); err != nil {
		return dbhealth.Report{}, err
	}
	return dbhealth.Report{Kind: "sql", Label: label, Database: req.Database}, nil
}

func (f *fakeRunner) TestMongo(_ context.Context, in audit.ConnectionInput) (audit.ConnectionResult, error) {
	if err := f.sawMongo(in); err != nil {
		return audit.ConnectionResult{}, err
	}
	return audit.ConnectionResult{Databases: []string{"audit"}}, nil
}

func (f *fakeRunner) ValidateMongo(in audit.ConnectionInput) error {
	if strings.Contains(in.URI, "bad-tls") {
		return audit.ErrInvalidInput
	}
	return nil
}

func (f *fakeRunner) HealthMongo(_ context.Context, in audit.ConnectionInput, database, collection string) (dbhealth.Report, error) {
	if err := f.sawMongo(in); err != nil {
		return dbhealth.Report{}, err
	}
	return dbhealth.Report{Kind: "mongo", Label: "MongoDB", Database: database}, nil
}

func (f *fakeRunner) LaneMongo(_ context.Context, in audit.ConnectionInput, label string, req dbhealth.LaneRequest) (dbhealth.Report, error) {
	if err := f.sawMongo(in); err != nil {
		return dbhealth.Report{}, err
	}
	return dbhealth.Report{Kind: "mongo", Label: label, Database: req.Database}, nil
}

/* -------------------------------------------------------------- harness */

type harness struct {
	t       *testing.T
	backend *Backend
	runner  *fakeRunner
	log     *syncBuffer
	wire    *syncBuffer
	hosted  *hostedFactory
	l       *MemoryListener
	client  *Client
	server  *Server
	cancel  context.CancelFunc
	done    chan struct{}
}

// recordingDialer keeps every byte the keeper sends back, so a test can prove no answer ever carries a secret.
type recordingDialer struct {
	inner Dialer
	buf   *syncBuffer
}

func (d recordingDialer) Dial(ctx context.Context) (ClientConn, error) {
	c, err := d.inner.Dial(ctx)
	if err != nil {
		return nil, err
	}
	return &recordingConn{ClientConn: c, buf: d.buf}, nil
}

type recordingConn struct {
	ClientConn
	buf *syncBuffer
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.ClientConn.Read(p)
	c.buf.Write(p[:n])
	return n, err
}

func newHarness(t *testing.T, limits map[string]int) *harness {
	t.Helper()
	dir := t.TempDir()
	sql, err := sqlconn.New(filepath.Join(dir, "sqlconnections.dat"), xorProtector{})
	if err != nil {
		t.Fatal(err)
	}
	mongo, err := history.New(filepath.Join(dir, "connections.dat"), xorProtector{})
	if err != nil {
		t.Fatal(err)
	}
	bb, err := bbsaved.New(filepath.Join(dir, "blackboxes.dat"), xorProtector{})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, runner: &fakeRunner{}, log: &syncBuffer{}, wire: &syncBuffer{}, hosted: &hostedFactory{}, l: NewMemoryListener(), done: make(chan struct{})}
	h.backend = &Backend{SQL: sql, Mongo: mongo, BB: bb, Runner: h.runner, Audit: NewAuditHost(h.hosted.make)}
	if limits == nil {
		limits = map[string]int{classRead: 10000, classWrite: 10000, classReach: 10000, classBlob: 10000, classAudit: 10000}
	}
	h.server = NewServer(ServerOptions{Log: NewAuditWriter(h.log), Limiter: NewRateLimiter(limits, time.Minute)})
	h.backend.Register(h.server)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		defer close(h.done)
		_ = h.server.Serve(ctx, h.l)
	}()
	h.client = NewClient(recordingDialer{inner: h.l.Dialer(Caller{SID: testSID, Name: `PC\analyst`, PID: 4242, Session: 1}), buf: h.wire})
	t.Cleanup(h.stop)
	return h
}

func (h *harness) stop() {
	h.cancel()
	<-h.done
}

// noSecrets fails when a secret appears in what the keeper sent back or in its audit log.
func (h *harness) noSecrets() {
	h.t.Helper()
	for _, s := range []string{sqlSecret, mongoSecret, bbSecret} {
		if strings.Contains(h.wire.String(), s) {
			h.t.Fatalf("a keeper answer carried the secret %q", s)
		}
		if strings.Contains(h.log.String(), s) {
			h.t.Fatalf("the audit log holds the secret %q", s)
		}
	}
}

func (h *harness) saveSQL(auth string) sqlconn.Recent {
	h.t.Helper()
	in := sqlconn.Input{Label: "Payroll prod", Server: "sql01.example.test,1433", Auth: auth, Username: "auditor", Password: sqlSecret, RememberPassword: true, Database: "Payroll", Encrypt: sqlconn.EncryptMandatory}
	if auth == sqlconn.AuthWindows {
		in.Username, in.Password, in.Server = "", "", "sql02.example.test"
	}
	rec, err := NewSQLStore(h.client).Save(in)
	if err != nil {
		h.t.Fatal(err)
	}
	return rec
}

var mongoLink = audit.ConnectionInput{URI: "mongodb://mongo01.example.test:27017", Username: "reader", AuthSource: "admin", TLSMode: "system"}

func lane() dbhealth.LaneRequest {
	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	return dbhealth.LaneRequest{
		Incident:  dbhealth.Window{Start: t0, End: t0.Add(time.Hour)},
		Reference: dbhealth.Window{Start: t0.Add(-25 * time.Hour), End: t0.Add(-24 * time.Hour)},
		Database:  "audit", Collection: "events",
	}
}

func wireCode(err error) string {
	var we *WireError
	if errors.As(err, &we) {
		return we.Code
	}
	return ""
}

/* ---------------------------------------------------------------- tests */

func TestFramesRoundTripAndRefuseOversize(t *testing.T) {
	var b bytes.Buffer
	if err := WriteFrame(&b, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&b)
	if err != nil || string(got) != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
	if err := WriteFrame(&b, make([]byte, MaxFrame+1)); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("oversize write: %v", err)
	}
	b.Reset()
	b.Write([]byte{0xff, 0xff, 0xff, 0xff})
	if _, err := ReadFrame(&b); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("oversize read: %v", err)
	}
}

func TestSQLChecksRunInsideTheKeeperWithTheSavedPassword(t *testing.T) {
	h := newHarness(t, nil)
	s := NewSQLStore(h.client)
	rec := h.saveSQL(sqlconn.AuthSQL)
	if !rec.HasPassword {
		t.Fatal("password not kept")
	}
	held, err := s.Held(rec.ID)
	if err != nil || !held {
		t.Fatalf("held = %v, %v", held, err)
	}
	in, err := s.Resolve(rec.ID)
	if err != nil || in.Password != "" || in.Server == "" {
		t.Fatalf("Resolve must leave the password out: %+v, %v", in, err)
	}

	res, listErr, err := s.TestSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil || listErr != "" || res.Version != "16.0" {
		t.Fatalf("test: %+v %q %v", res, listErr, err)
	}
	if got := h.runner.lastSQL(); got.Password != sqlSecret || got.Username != "auditor" || got.Database != "Payroll" {
		t.Fatalf("runner got %+v", got)
	}

	if _, err := s.HealthSaved(context.Background(), SQLRef{ID: rec.ID, Database: "Other"}, "Prod"); err != nil {
		t.Fatal(err)
	}
	if got := h.runner.lastSQL(); got.Database != "Other" || got.Password != sqlSecret {
		t.Fatalf("database override: %+v", got)
	}

	if _, _, err := s.TestSaved(context.Background(), SQLRef{ID: rec.ID, Password: "typed-now"}); err != nil {
		t.Fatal(err)
	}
	if got := h.runner.lastSQL(); got.Password != "typed-now" {
		t.Fatalf("a typed password must win: %+v", got)
	}

	rep, err := s.LaneSaved(context.Background(), SQLRef{ID: rec.ID}, "Prod", lane())
	if err != nil || rep.Database != "Payroll" {
		t.Fatalf("lane: %+v %v", rep, err)
	}
	h.noSecrets()
}

func TestWindowsSignInIsRefusedInTheKeeper(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.saveSQL(sqlconn.AuthWindows)
	s := NewSQLStore(h.client)
	if held, _ := s.Held(rec.ID); held {
		t.Fatal("a Windows sign-in is never held")
	}
	_, _, err := s.TestSaved(context.Background(), SQLRef{ID: rec.ID})
	if wireCode(err) != CodeRefused {
		t.Fatalf("want refused, got %v", err)
	}
	if len(h.runner.sqlSeen) != 0 {
		t.Fatal("the runner must not be reached")
	}
}

func TestConnectFailuresComeBackInTheirNativeForm(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.saveSQL(sqlconn.AuthSQL)
	h.runner.sqlErr = &dbhealth.ConnectError{Code: "login", Message: "The server refused the sign-in."}
	_, _, err := NewSQLStore(h.client).TestSaved(context.Background(), SQLRef{ID: rec.ID})
	var ce *dbhealth.ConnectError
	if !errors.As(err, &ce) || ce.Code != "login" || err.Error() != "The server refused the sign-in." {
		t.Fatalf("got %T %v", err, err)
	}

	h.runner.sqlErr = errors.New("dial tcp 10.9.8.7:1433: " + sqlSecret)
	_, err = NewSQLStore(h.client).HealthSaved(context.Background(), SQLRef{ID: rec.ID}, "x")
	if wireCode(err) != CodeInternal || strings.Contains(err.Error(), "10.9.8.7") {
		t.Fatalf("an unknown error must travel as a fixed sentence: %v", err)
	}

	if _, err := NewSQLStore(h.client).Describe("nope"); !errors.Is(err, sqlconn.ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	if _, err := NewSQLStore(h.client).Save(sqlconn.Input{Server: "", Auth: "sql"}); !errors.Is(err, sqlconn.ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
	h.noSecrets()
}

func TestAChecksRunsOnceAtATimePerSavedConnection(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.saveSQL(sqlconn.AuthSQL)
	h.runner.block, h.runner.started = make(chan struct{}), make(chan struct{}, 1)
	errc := make(chan error, 1)
	go func() {
		_, err := NewSQLStore(h.client).HealthSaved(context.Background(), SQLRef{ID: rec.ID}, "x")
		errc <- err
	}()
	<-h.runner.started
	_, err := NewSQLStore(h.client).HealthSaved(context.Background(), SQLRef{ID: rec.ID}, "x")
	if wireCode(err) != CodeBusy {
		t.Fatalf("want busy, got %v", err)
	}
	close(h.runner.block)
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestRateLimitPerCallerAndClass(t *testing.T) {
	h := newHarness(t, map[string]int{classRead: 2})
	s := NewSQLStore(h.client)
	for i := 0; i < 2; i++ {
		if _, err := s.List(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.List(); wireCode(err) != CodeRateLimited {
		t.Fatalf("want rate-limited, got %v", err)
	}
	other := NewSQLStore(NewClient(h.l.Dialer(Caller{SID: "S-1-5-21-other"})))
	if _, err := other.List(); err != nil {
		t.Fatalf("another account has its own count: %v", err)
	}
	if !strings.Contains(h.log.String(), `"outcome":"rate-limited"`) {
		t.Fatal("refusal not logged")
	}

	r := NewRateLimiter(map[string]int{classWrite: 1}, time.Minute)
	now := time.Unix(1000, 0)
	r.now = func() time.Time { return now }
	if !r.Allow("a", classWrite) || r.Allow("a", classWrite) {
		t.Fatal("limit of one")
	}
	now = now.Add(time.Minute)
	if !r.Allow("a", classWrite) {
		t.Fatal("the window slides")
	}
}

// One person opening connections in a loop holds at most part of the keeper's slots: someone else signed in at the same time is still
// answered, and the flood of refusals is one audit line per caller and minute, not thousands.
func TestOneSessionCannotStarveTheOthers(t *testing.T) {
	log := &syncBuffer{}
	l := NewMemoryListener()
	s := NewServer(ServerOptions{Log: NewAuditWriter(log), Limiter: NewRateLimiter(nil, 0), MaxConns: 4, MaxPerSession: 2, ReadTimeout: 3 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx, l)
	}()
	defer func() { cancel(); <-done }()

	flooder := Caller{SID: testSID, Name: `PClood`, PID: 7, Session: 1}
	var idle []ClientConn
	for i := 0; i < 20; i++ { // connect and send nothing
		c, err := l.Dialer(flooder).Dial(ctx)
		if err != nil {
			t.Fatal(err)
		}
		idle = append(idle, c)
	}
	defer func() {
		for _, c := range idle {
			c.Close()
		}
	}()
	other := NewClient(l.Dialer(Caller{SID: "S-1-5-21-1-2-3-2002", Name: `PC\other`, PID: 8, Session: 2}))
	var out map[string]any
	if err := other.Call(ctx, OpPing, nil, &out); err != nil {
		t.Fatalf("another person is starved: %v", err)
	}
	// The line is written before the answer is sent.
	if !strings.Contains(log.String(), `"op":"ping"`) {
		t.Fatal("the request was answered before it was recorded")
	}
	// The flooding session itself is answered "busy", or (with every refusal slot taken) simply closed.
	if err := NewClient(l.Dialer(flooder)).Call(ctx, OpPing, nil, &out); err == nil || (wireCode(err) != CodeBusy && !errors.Is(err, errTransport)) {
		t.Fatalf("the flooding session got a slot: %v", err)
	}
	if n := strings.Count(log.String(), `"outcome":"busy"`); n != 1 {
		t.Fatalf("%d busy lines for one caller within a minute:\n%s", n, log.String())
	}
}

// A caller who sends a stream of requests the keeper will not run, each naming a long operation of its own, gets short cleaned names
// into the log, at most the "bad" limit of them a minute, and then one folded rate-limited line: the log stays small and never
// rotates, so the real lines in it are not pushed out.
func TestUnknownOpFloodCannotFillTheAuditLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	audit := NewAuditLog(path, 16<<10)
	l := NewMemoryListener()
	s := NewServer(ServerOptions{Log: audit, Limiter: NewRateLimiter(nil, 0)})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx, l)
	}()
	long := strings.Repeat("A", 60<<10)
	codes := map[string]int{}
	for i := 0; i < 100; i++ {
		// The keeper frees a connection's slot only after it has finished draining the 60 KB request, which can be after this
		// side has read the answer, so the next connection may be refused "busy" by the per-session cap. That refusal is not a
		// request the keeper judged (it does not touch the "bad" limit), so ask again until the keeper takes it.
		var r Response
		for try := 0; ; try++ {
			c, err := l.Dialer(Caller{SID: testSID, Name: `PC\flood`, PID: 9, Session: 1}).Dial(ctx)
			if err != nil {
				t.Fatal(err)
			}
			req := Request{V: ProtocolVersion, Op: fmt.Sprintf("x%03d\n\"%s", i, long)}
			if i%10 == 9 {
				req.V = 99 // another version, same limit
			}
			if err := writeJSONFrame(c, req); err != nil {
				t.Fatal(err)
			}
			f, err := ReadFrame(c)
			if err != nil {
				t.Fatal(err)
			}
			r = Response{}
			_ = json.Unmarshal(f, &r)
			c.Close()
			if r.Error != nil && r.Error.Code == CodeBusy && try < 2000 {
				time.Sleep(time.Millisecond)
				continue
			}
			break
		}
		if r.Error == nil {
			t.Fatalf("request %d was run", i)
		}
		codes[r.Error.Code]++
	}
	if codes[CodeRateLimited] != 100-DefaultLimits[classBad] {
		t.Fatalf("answers: %v", codes)
	}
	// The flooder is limited, not everyone: a real request from the same person still runs.
	var out map[string]any
	// (The last flood connection may still be draining, so a "busy" answer is asked again.)
	var pingErr error
	for try := 0; try < 2000; try++ {
		pingErr = NewClient(l.Dialer(Caller{SID: testSID, PID: 9, Session: 1})).Call(ctx, OpPing, nil, &out)
		if pingErr == nil || wireCode(pingErr) != CodeBusy {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if pingErr != nil {
		t.Fatalf("ping after the flood: %v", pingErr)
	}
	cancel()
	<-done
	audit.Flush()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Fatal("the flood rotated the log")
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(raw) > 12<<10 || len(lines) > DefaultLimits[classBad]+4 {
		t.Fatalf("%d bytes, %d lines", len(raw), len(lines))
	}
	for _, ln := range lines {
		if len(ln) > 400 || strings.Contains(ln, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") || strings.Contains(ln, `\n`) || strings.Contains(ln, `\"`) {
			t.Fatalf("a caller's text reached the log: %s", ln)
		}
	}
	if !strings.Contains(string(raw), `"op":"x000AAAAAAAAAAAAAAAAAAAAAAAAAAAA~"`) || !strings.Contains(string(raw), `"outcome":"rate-limited"`) || !strings.Contains(string(raw), `"op":"ping"`) {
		t.Fatalf("log:\n%s", raw)
	}
}

// TestCallerIDsCannotFillTheAuditLog sends 1 MB record ids, 100 times on each of three operations that log the id before checking it.
// Every line must stay short and clean, and the log must stay small and never rotate.
func TestCallerIDsCannotFillTheAuditLog(t *testing.T) {
	dir := t.TempDir()
	sql, err := sqlconn.New(filepath.Join(dir, "sqlconnections.dat"), xorProtector{})
	if err != nil {
		t.Fatal(err)
	}
	mongo, err := history.New(filepath.Join(dir, "connections.dat"), xorProtector{})
	if err != nil {
		t.Fatal(err)
	}
	bb, err := bbsaved.New(filepath.Join(dir, "blackboxes.dat"), xorProtector{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audit.log")
	const limit = 128 << 10
	audit := NewAuditLog(path, limit)
	s := NewServer(ServerOptions{Log: audit, Limiter: NewRateLimiter(map[string]int{classRead: 10000, classWrite: 10000, classReach: 10000, classBlob: 10000}, time.Minute)})
	(&Backend{SQL: sql, Mongo: mongo, BB: bb, Runner: &fakeRunner{}}).Register(s)
	l := NewMemoryListener()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx, l)
	}()
	id := "x\n\"" + strings.Repeat("B", MaxFrame-512)
	ops := map[string]any{
		OpSQLDescribe: IDArgs{ID: id},
		OpBBGet:       IDArgs{ID: id},
		OpBBHTTP:      BBHTTPArgs{ID: id, Path: "/manifest"},
	}
	for op, args := range ops {
		raw, _ := json.Marshal(args)
		for i := 0; i < 100; i++ {
			c, err := l.Dialer(Caller{SID: testSID, Name: `PC\flood`, PID: 9, Session: 1}).Dial(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeJSONFrame(c, Request{V: ProtocolVersion, Op: op, Args: raw}); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadFrame(c); err != nil {
				t.Fatalf("%s %d: %v", op, i, err)
			}
			c.Close()
		}
	}
	cancel()
	<-done
	audit.Flush()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Fatal("the long ids rotated the log")
	}
	if len(raw) >= limit {
		t.Fatalf("the log grew to %d bytes", len(raw))
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 300 {
		t.Fatalf("only %d lines: the calls did not all reach the log", len(lines))
	}
	for _, ln := range lines {
		if len(ln) > 400 || strings.Contains(ln, strings.Repeat("B", maxAuditOp+1)) || strings.Contains(ln, `\n`) || strings.Contains(ln, `\"`) {
			t.Fatalf("a caller's id reached the log: %.500s", ln)
		}
	}
	for op := range ops {
		if !strings.Contains(string(raw), `"op":"`+op+`","record":"x`+strings.Repeat("B", maxAuditOp-1)+`~"`) {
			t.Fatalf("no cut record for %s in:\n%.2000s", op, raw)
		}
	}
}

func TestAuditOpIsShortAndClean(t *testing.T) {
	for in, want := range map[string]string{
		"sql.reveal":                        "sql.reveal",
		"":                                  "?",
		"\n\"{}":                            "?",
		"sql.list\n{\"op\":\"fake\"}":       "sql.listopfake~",
		strings.Repeat("b", 40):             strings.Repeat("b", maxAuditOp) + "~",
		strings.Repeat("c", maxAuditOp):     strings.Repeat("c", maxAuditOp),
		"bb.read‮" + strings.Repeat("d", 5): "bb.readddddd~",
	} {
		if got := auditOp(in); got != want {
			t.Errorf("auditOp(%q) = %q, want %q", in, got, want)
		}
	}
	if r := NewRateLimiter(map[string]int{classRead: 1}, 0); r.limits[classBad] != DefaultLimits[classBad] {
		t.Fatal("limits without the bad class must still limit it")
	}
}

// A connection whose Windows session cannot be told is not exempt from the per-session cap: all such connections share one bucket.
func TestUnknownSessionSharesOneCappedBucket(t *testing.T) {
	log := &syncBuffer{}
	l := anonListener{NewMemoryListener()}
	s := NewServer(ServerOptions{Log: NewAuditWriter(log), Limiter: NewRateLimiter(nil, 0), MaxConns: 8, MaxPerSession: 2, ReadTimeout: 3 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx, l)
	}()
	defer func() { cancel(); <-done }()
	var idle []ClientConn
	defer func() {
		for _, c := range idle {
			c.Close()
		}
	}()
	for i := 0; i < 2; i++ {
		c, err := l.Dialer(Caller{SID: testSID, PID: uint32(10 + i), Session: uint32(1 + i)}).Dial(ctx)
		if err != nil {
			t.Fatal(err)
		}
		idle = append(idle, c)
	}
	var out map[string]any
	err := NewClient(l.Dialer(Caller{SID: testSID, PID: 12, Session: 3})).Call(ctx, OpPing, nil, &out)
	if err == nil || (wireCode(err) != CodeBusy && !errors.Is(err, errTransport)) {
		t.Fatalf("a third connection of unknown session got a slot: %v", err)
	}
	if !strings.Contains(log.String(), fmt.Sprintf(`"session":%d`, unknownSession)) {
		t.Fatalf("refusal not logged under the unknown-session bucket:\n%s", log.String())
	}
}

// anonListener hands out connections that cannot tell their caller's session (no Peer method).
type anonListener struct{ *MemoryListener }

type anonConn struct{ Conn }

func (l anonListener) Accept() (Conn, error) {
	c, err := l.MemoryListener.Accept()
	if err != nil {
		return nil, err
	}
	return anonConn{c}, nil
}

func TestAuditLogFoldsRepeatsAndKeepsSeveralFiles(t *testing.T) {
	var b syncBuffer
	l := NewAuditWriter(&b)
	now := time.Unix(5000, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 50; i++ {
		l.Write(AuditEntry{SID: testSID, Op: OpSQLList, Outcome: CodeRateLimited})
	}
	l.Write(AuditEntry{SID: testSID, Op: OpSQLList, Outcome: "ok"})
	if n := strings.Count(b.String(), "\n"); n != 2 {
		t.Fatalf("want the first refusal and the ok line, got %d lines:\n%s", n, b.String())
	}
	now = now.Add(61 * time.Second)
	l.Write(AuditEntry{SID: testSID, Op: OpSQLList, Outcome: CodeRateLimited})
	if !strings.Contains(b.String(), `"repeated":49`) || strings.Count(b.String(), "\n") != 4 {
		t.Fatalf("summary line missing:\n%s", b.String())
	}
	l.Write(AuditEntry{SID: testSID, Op: OpSQLList, Outcome: CodeRateLimited})
	l.Flush()
	if !strings.Contains(b.String(), `"repeated":1`) {
		t.Fatalf("flush on stop:\n%s", b.String())
	}

	path := filepath.Join(t.TempDir(), "audit.log")
	fl := NewAuditLog(path, 200)
	for i := 0; i < 40; i++ {
		fl.Write(AuditEntry{SID: testSID, Op: OpPing, Outcome: "ok"})
	}
	for i := 1; i <= AuditKeep; i++ {
		if _, err := os.Stat(fmt.Sprintf("%s.%d", path, i)); err != nil {
			t.Fatalf("rotated file %d: %v", i, err)
		}
	}
	if _, err := os.Stat(fmt.Sprintf("%s.%d", path, AuditKeep+1)); err == nil {
		t.Fatal("more rotated files than kept")
	}
}

func TestProtocolRefusals(t *testing.T) {
	h := newHarness(t, nil)
	call := func(req any) Response {
		t.Helper()
		c, err := h.l.Dialer(Caller{SID: testSID}).Dial(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := writeJSONFrame(c, req); err != nil {
			t.Fatal(err)
		}
		f, err := ReadFrame(c)
		if err != nil {
			t.Fatal(err)
		}
		var r Response
		_ = json.Unmarshal(f, &r)
		return r
	}
	if r := call(Request{V: 99, Op: OpPing}); r.Error == nil || r.Error.Code != CodeVersion {
		t.Fatalf("version: %+v", r)
	}
	if r := call(Request{V: ProtocolVersion, Op: "sql.reveal"}); r.Error == nil || r.Error.Code != CodeUnknownOp {
		t.Fatalf("unknown op: %+v", r)
	}
	if r := call(Request{V: ProtocolVersion, Op: OpSQLDescribe, Args: json.RawMessage(`{"id":"x","password":"y"}`)}); r.Error == nil || r.Error.Code != CodeInvalid {
		t.Fatalf("unknown field: %+v", r)
	}
	if r := call(Request{V: ProtocolVersion, Op: OpPing}); !r.OK {
		t.Fatalf("ping: %+v", r)
	}

	// No identity, no answer.
	anon := NewClient(h.l.Dialer(Caller{}))
	if err := anon.Ping(context.Background()); wireCode(err) != CodeRefused {
		t.Fatalf("anonymous: %v", err)
	}
}

func TestPanickingHandlerAnswersInternal(t *testing.T) {
	h := newHarness(t, nil)
	h.server.Handle("test.panic", classRead, time.Second, func(context.Context, *Call) (any, error) { panic("boom " + sqlSecret) })
	err := h.client.Call(context.Background(), "test.panic", nil, nil)
	if wireCode(err) != CodeInternal {
		t.Fatalf("got %v", err)
	}
	h.noSecrets()
}

func TestNoOperationGivesOutASecret(t *testing.T) {
	h := newHarness(t, nil)
	sqlRec := h.saveSQL(sqlconn.AuthSQL)
	m := NewMongoStore(h.client)
	link := mongoLink
	link.Password = mongoSecret
	mRec, err := m.Remember("Audit prod", link, "events", true)
	if err != nil {
		t.Fatal(err)
	}
	bb := NewBBStore(h.client)
	loc, _ := blackbox.ParseLocation("http://127.0.0.1:1")
	bRec, err := bb.Add(bbsaved.AddInput{Name: "Box", Location: loc, Key: bbSecret})
	if err != nil {
		t.Fatal(err)
	}
	h.wire.b.Reset()
	// Every registered operation, asked about every saved record in every way a request can name one.
	h.server.mu.RLock()
	var ops []string
	for op := range h.server.ops {
		ops = append(ops, op)
	}
	h.server.mu.RUnlock()
	// Deleting ops go last, so every other op sees the saved records.
	sort.SliceStable(ops, func(i, j int) bool { return !isDelete(ops[i]) && isDelete(ops[j]) })
	if k, err := bb.Key(bRec.ID); k != "" || !errors.Is(err, ErrKeeperHeld) {
		t.Fatalf("Key must never answer: %q %v", k, err)
	}
	if _, err := m.Resolve(audit.ConnectionInput{URI: mongoLink.URI, Username: "reader", AuthSource: "admin", TLSMode: "system", SavedConnectionID: mRec.ID}); !errors.Is(err, ErrKeeperHeld) {
		t.Fatalf("Resolve of a held link: %v", err)
	}
	for _, op := range ops {
		for _, id := range []string{sqlRec.ID, mRec.ID, bRec.ID} {
			for _, args := range []string{
				`{"id":"` + id + `"}`,
				`{"ref":{"id":"` + id + `"}}`,
				`{"connection":{"uri":"` + mongoLink.URI + `","username":"reader","authSource":"admin","tlsMode":"system","savedConnectionId":"` + id + `"}}`,
				`{"id":"` + id + `","path":"/manifest"}`,
				`{"instance":"probe","id":"` + id + `"}`,
				`{"instance":"probe","connection":{"uri":"` + mongoLink.URI + `","username":"reader","authSource":"admin","tlsMode":"system","savedConnectionId":"` + id + `"}}`,
				`{"instance":"probe","input":{"sourceId":"kept-1"}}`,
				`{"instance":"probe","input":{"sourceIds":["kept-1"]}}`,
			} {
				var out json.RawMessage
				_ = h.client.Call(context.Background(), op, json.RawMessage(args), &out)
			}
		}
	}
	h.noSecrets()
}

func isDelete(op string) bool {
	return strings.HasSuffix(op, ".delete") || strings.HasSuffix(op, ".remove")
}

func TestMongoSavedLinksStayInTheKeeper(t *testing.T) {
	h := newHarness(t, nil)
	m := NewMongoStore(h.client)
	link := mongoLink
	link.Password = mongoSecret
	rec, err := m.Remember("Audit prod", link, "events", true)
	if err != nil || !rec.HasCredentials {
		t.Fatalf("remember: %+v %v", rec, err)
	}
	ref := mongoLink
	ref.SavedConnectionID = rec.ID
	if held, err := m.Held(ref); err != nil || !held {
		t.Fatalf("held = %v %v", held, err)
	}
	if _, err := m.TestSaved(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if got := h.runner.lastMongo(); got.Password != mongoSecret || got.URI != mongoLink.URI {
		t.Fatalf("runner got %+v", got)
	}
	if _, err := m.HealthSaved(context.Background(), ref, "audit", "events"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.LaneSaved(context.Background(), ref, "Audit", lane()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.HealthSaved(context.Background(), ref, "bad name", ""); wireCode(err) != CodeInvalid {
		t.Fatalf("database name: %v", err)
	}

	// A changed address no longer matches.
	moved := ref
	moved.URI = "mongodb://elsewhere.example.test:27017"
	if _, err := m.TestSaved(context.Background(), moved); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("mismatch: %v", err)
	}
	if _, err := m.Resolve(moved); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("local mismatch: %v", err)
	}

	// Not remembered: the keeper stores no password; this Unravis keeps it for the session.
	other := audit.ConnectionInput{URI: "mongodb://mongo02.example.test:27017", Username: "reader", Password: "session-only", AuthSource: "admin", TLSMode: "system"}
	rec2, err := m.Remember("Audit test", other, "events", false)
	if err != nil || rec2.HasCredentials {
		t.Fatalf("remember without password: %+v %v", rec2, err)
	}
	stored, _ := h.backend.Mongo.Export()
	for _, r := range stored {
		if r.ID == rec2.ID && r.Password != "" {
			t.Fatal("the keeper kept a password it was not asked to remember")
		}
	}
	ref2 := other
	ref2.Password, ref2.SavedConnectionID = "", rec2.ID
	got, err := m.Resolve(ref2)
	if err != nil || got.Password != "session-only" || got.SavedConnectionID != "" {
		t.Fatalf("session password: %+v %v", got, err)
	}

	if _, err := m.Remember("x", audit.ConnectionInput{URI: "mongodb://bad-tls.example.test"}, "events", false); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("validation runs in the keeper: %v", err)
	}

	h.runner.mongoErr = &audit.ConnectionError{Stage: "auth", Code: "auth_failed", Message: "MongoDB refused the sign-in."}
	_, err = m.TestSaved(context.Background(), ref)
	var ae *audit.ConnectionError
	if !errors.As(err, &ae) || ae.Code != "auth_failed" {
		t.Fatalf("mongo connect error: %T %v", err, err)
	}

	if err := m.Delete(rec2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Resolve(ref2); !errors.Is(err, history.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	h.noSecrets()
}

// The keeper opens a saved MongoDB link only with its stored user name and password over a password sign-in, and never with an option
// that names a file: anything else would sign in or read files as the keeper's own Windows account.
func TestMongoLinksThatWouldUseTheKeepersAccountAreRefused(t *testing.T) {
	h := newHarness(t, nil)
	m := NewMongoStore(h.client)
	ctx := context.Background()
	save := func(uri, password string) audit.ConnectionInput {
		t.Helper()
		link := audit.ConnectionInput{URI: uri, Username: "reader", Password: password, AuthSource: "admin", TLSMode: "uri"}
		rec, err := m.Remember("x", link, "events", true)
		if err != nil {
			t.Fatalf("remember %s: %v", uri, err)
		}
		link.Password, link.SavedConnectionID = "", rec.ID
		return link
	}
	refused := func(name string, ref audit.ConnectionInput) {
		t.Helper()
		before := len(h.runner.mongoSeen)
		if _, err := m.TestSaved(ctx, ref); wireCode(err) != CodeRefused {
			t.Errorf("%s test: %v", name, err)
		}
		if _, err := m.HealthSaved(ctx, ref, "audit", "events"); wireCode(err) != CodeRefused {
			t.Errorf("%s health: %v", name, err)
		}
		if _, err := m.LaneSaved(ctx, ref, "Audit", lane()); wireCode(err) != CodeRefused {
			t.Errorf("%s lane: %v", name, err)
		}
		if len(h.runner.mongoSeen) != before {
			t.Errorf("%s reached the driver", name)
		}
	}
	refused("no stored password", save("mongodb://m1.example.test:27017", ""))
	refused("X.509", save("mongodb://m2.example.test:27017/?authMechanism=MONGODB-X509&tls=true", mongoSecret))
	refused("Kerberos", save("mongodb://m3.example.test:27017/?authMechanism=GSSAPI", mongoSecret))
	refused("AWS", save("mongodb://m4.example.test:27017/?authmechanism=mongodb-aws", mongoSecret))
	refused("LDAP", save("mongodb://m5.example.test:27017/?authMechanism=PLAIN", mongoSecret))

	for _, ok := range []string{"mongodb://m6.example.test:27017", "mongodb://m7.example.test:27017/?authMechanism=SCRAM-SHA-256&tls=true"} {
		if _, err := m.TestSaved(ctx, save(ok, mongoSecret)); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}

	// A link naming files on this PC is not even checked in the keeper (the driver would open them while checking).
	for _, opt := range []string{"tlsCAFile=C:/ProgramData/Unravis/keeper/connections.dat", "tlsCertificateKeyFile=C:/k.pem", "tlsCertificateKeyFilePassword=x", "sslCAFile=C:/a.pem", "SSLPEMKeyFile=C:/a.pem", "tlsCRLFile=C:/a.crl"} {
		link := audit.ConnectionInput{URI: "mongodb://m8.example.test:27017/?tls=true&" + opt, Username: "reader", Password: mongoSecret, TLSMode: "uri"}
		if _, err := m.Remember("files", link, "events", true); wireCode(err) != CodeRefused {
			t.Errorf("%s: %v", opt, err)
		}
	}
	// One stored before this check (an older file) is refused when used.
	stored, err := h.backend.Mongo.Remember("old", audit.ConnectionInput{URI: "mongodb://m9.example.test:27017/?tlsCAFile=C:/a.pem", Username: "reader", Password: mongoSecret, TLSMode: "uri"}, "events", true)
	if err != nil {
		t.Fatal(err)
	}
	refused("file option stored earlier", audit.ConnectionInput{URI: "mongodb://m9.example.test:27017/?tlsCAFile=C:/a.pem", Username: "reader", TLSMode: "uri", SavedConnectionID: stored.ID})
	h.noSecrets()
}

/* ------------------------------------------------------------- Blackbox */

const bbFile = "bb-20261001-10.ndjson"

type fakeBlackbox struct {
	key  string
	seen []string
	mu   sync.Mutex
	big  []byte
}

func (f *fakeBlackbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r.URL.Path)
	f.mu.Unlock()
	if r.Header.Get(blackbox.KeyHeader) != f.key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set(blackbox.TimeHeader, "2026-10-01T10:30:00Z")
	switch r.URL.Path {
	case "/status":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"format":1}`))
	case "/files/" + bbFile:
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write(f.big)
	case "/redirect":
		http.Redirect(w, r, "http://elsewhere.example.test/", http.StatusFound)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestBlackboxKeyedReadsGoThroughTheKeeper(t *testing.T) {
	h := newHarness(t, nil)
	big := bytes.Repeat([]byte(`{"t":"2026-10-01T10:00:00Z","cpu":12}`+"\n"), 100000) // about 3.7 MB: many chunks
	fb := &fakeBlackbox{key: bbSecret, big: big}
	srv := httptest.NewServer(fb)
	defer srv.Close()
	h.backend.HTTP = srv.Client()

	loc, err := blackbox.ParseLocation(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	bb := NewBBStore(h.client)
	e, err := bb.Add(bbsaved.AddInput{Name: "Box", Location: loc, Key: bbSecret})
	if err != nil || !e.HasKey {
		t.Fatalf("add: %+v %v", e, err)
	}
	if _, err := bb.Add(bbsaved.AddInput{Name: "Box", Location: loc, Key: bbSecret}); !errors.Is(err, bbsaved.ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}

	src := bb.KeyedSource(e.ID, loc)
	rc, err := src.OpenFile(context.Background(), bbFile)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("file read through the keeper: %d bytes, %v", len(got), err)
	}
	if _, err := src.Status(context.Background()); err != nil {
		// The fake status is minimal; only a transport-level failure matters here.
		var re *blackbox.ReadError
		if !errors.As(err, &re) || re.Kind == blackbox.ReadUnreachable {
			t.Fatalf("status: %v", err)
		}
	}

	// Only the manifest, the status and recording files are read.
	for _, p := range []string{"/redirect", "/files/../secret", "/admin", "/files/notes.txt"} {
		_, err := h.client.Open(context.Background(), OpBBHTTP, BBHTTPArgs{ID: e.ID, Path: p}, nil)
		if wireCode(err) != CodeRefused {
			t.Fatalf("%s: want refused, got %v", p, err)
		}
	}
	for _, p := range fb.seen {
		if p != "/files/"+bbFile && p != "/status" {
			t.Fatalf("the keeper asked the Blackbox for %s", p)
		}
	}

	// A wrong key is worded as the Blackbox's refusal.
	if err := bb.SetKey(e.ID, "wrong-key"); err != nil {
		t.Fatal(err)
	}
	_, err = src.OpenFile(context.Background(), bbFile)
	var re *blackbox.ReadError
	if !errors.As(err, &re) || re.Kind != blackbox.ReadKeyRefused {
		t.Fatalf("wrong key: %v", err)
	}

	// A folder Blackbox is never read through the keeper.
	floc, _ := blackbox.ParseLocation(`C:\Recordings\Box`)
	fe, err := bb.Add(bbsaved.AddInput{Name: "Folder", Location: floc})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.client.Open(context.Background(), OpBBHTTP, BBHTTPArgs{ID: fe.ID, Path: "/manifest"}, nil); wireCode(err) != CodeRefused {
		t.Fatalf("folder: %v", err)
	}

	// Unreachable: worded without the address.
	srv.Close()
	if err := bb.SetKey(e.ID, bbSecret); err != nil {
		t.Fatal(err)
	}
	_, err = src.OpenFile(context.Background(), bbFile)
	if !errors.As(err, &re) || re.Kind != blackbox.ReadUnreachable || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("unreachable: %v", err)
	}
	h.noSecrets()
}

func TestKeeperStoppedIsWordedAsSuch(t *testing.T) {
	c := NewClient(StoppedDialer{})
	if _, err := NewSQLStore(c).List(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("sql: %v", err)
	}
	loc, _ := blackbox.ParseLocation("http://127.0.0.1:1")
	_, err := NewBBStore(c).KeyedSource("x", loc).OpenFile(context.Background(), bbFile)
	if !errors.Is(err, ErrNotRunning) || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("bb: %v", err)
	}
	var nilClient *Client
	if err := nilClient.Ping(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("nil client: %v", err)
	}
}

func TestAuditLogRecordsWhoAndWhatButNoArguments(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.saveSQL(sqlconn.AuthSQL)
	if _, _, err := NewSQLStore(h.client).TestSaved(context.Background(), SQLRef{ID: rec.ID, Password: "typed-" + sqlSecret}); err != nil {
		t.Fatal(err)
	}
	// The entry is written once the answer has gone out, so wait for it.
	var last AuditEntry
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		lines := strings.Split(strings.TrimSpace(h.log.String()), "\n")
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
			t.Fatal(err)
		}
		if last.Op == OpSQLTest {
			break
		}
	}
	if last.SID != testSID || last.Op != OpSQLTest || last.Record != rec.ID || last.Outcome != "ok" || last.PID != 4242 {
		t.Fatalf("entry: %+v", last)
	}
	if strings.Contains(h.log.String(), "sql01.example.test") || strings.Contains(h.log.String(), "auditor") {
		t.Fatal("the audit log holds an address or a login")
	}
	h.noSecrets()

	// The file log rotates.
	path := filepath.Join(t.TempDir(), "audit.log")
	l := NewAuditLog(path, 300)
	for i := 0; i < 10; i++ {
		l.Write(AuditEntry{SID: testSID, Op: OpPing, Outcome: "ok"})
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("no rotation: %v", err)
	}
	if info, _ := os.Stat(path); info.Size() > 300 {
		t.Fatalf("log too big: %d", info.Size())
	}
}

/* ------------------------------------------------------------ migration */

func TestMigrationMovesOldFilesAndRemovesThemOnlyWhenVerified(t *testing.T) {
	h := newHarness(t, nil)
	dir := t.TempDir()
	oldSQL, _ := sqlconn.New(filepath.Join(dir, "sqlconnections.dat"), xorProtector{})
	oldMongo, _ := history.New(filepath.Join(dir, "connections.dat"), xorProtector{})
	oldBB, _ := bbsaved.New(filepath.Join(dir, "blackboxes.dat"), xorProtector{})
	if _, err := oldSQL.Save(sqlconn.Input{Server: "sql01.example.test", Auth: "sql", Username: "auditor", Password: sqlSecret, RememberPassword: true}); err != nil {
		t.Fatal(err)
	}
	link := mongoLink
	link.Password = mongoSecret
	if _, err := oldMongo.Remember("Audit prod", link, "events", true); err != nil {
		t.Fatal(err)
	}
	loc, _ := blackbox.ParseLocation("http://box01.example.test:8765")
	if _, err := oldBB.Add(bbsaved.AddInput{Name: "Box", Location: loc, Key: bbSecret}); err != nil {
		t.Fatal(err)
	}

	m := &Migration{Legacy: Legacy{SQL: oldSQL, Mongo: oldMongo, BB: oldBB}, Client: h.client}
	if !m.OldFilesPresent() {
		t.Fatal("old files present")
	}
	p, err := m.Pending()
	if err != nil || p != (Pending{SQL: 1, Mongo: 1, Blackbox: 1}) {
		t.Fatalf("pending: %+v %v", p, err)
	}
	if err := m.RemoveOld(false); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("remove before the move: %v", err)
	}
	if !m.OldFilesPresent() {
		t.Fatal("old files removed before the move")
	}
	p, err = m.Migrate(context.Background())
	if err != nil || p.Total() != 0 {
		t.Fatalf("migrate: %+v %v", p, err)
	}
	// Moving twice adds nothing.
	if p, err = m.Migrate(context.Background()); err != nil || p.Total() != 0 {
		t.Fatalf("second move: %+v %v", p, err)
	}
	if n, _ := h.backend.SQL.List(); len(n) != 1 {
		t.Fatalf("keeper sql: %d", len(n))
	}
	if k, _ := h.backend.BB.List(); len(k) != 1 || !k[0].HasKey {
		t.Fatalf("keeper bb: %+v", k)
	}
	if err := m.RemoveOld(false); err != nil {
		t.Fatal(err)
	}
	if m.OldFilesPresent() {
		t.Fatal("old files still there")
	}
	if p, err := m.Pending(); err != nil || p.Total() != 0 {
		t.Fatalf("after removal: %+v %v", p, err)
	}
	h.noSecrets()

	stopped := &Migration{Legacy: Legacy{SQL: oldSQL}, Client: NewClient(StoppedDialer{})}
	if _, err := oldSQL.Save(sqlconn.Input{Server: "sql03.example.test", Auth: "windows"}); err != nil {
		t.Fatal(err)
	}
	if _, err := stopped.Pending(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stopped: %v", err)
	}
	if err := stopped.RemoveOld(false); err == nil || !m.OldFilesPresent() {
		t.Fatalf("a stopped keeper must keep the old file: %v", err)
	}
}

func TestMigrationKeepsOldFilesWhenTheKeeperLostASecret(t *testing.T) {
	h := newHarness(t, nil)
	oldSQL, _ := sqlconn.New(filepath.Join(t.TempDir(), "sqlconnections.dat"), xorProtector{})
	if _, err := oldSQL.Save(sqlconn.Input{Server: "sql01.example.test", Auth: "sql", Username: "auditor", Password: sqlSecret, RememberPassword: true}); err != nil {
		t.Fatal(err)
	}
	// The keeper has the same login but without a password.
	if _, err := h.backend.SQL.Save(sqlconn.Input{Server: "sql01.example.test", Auth: "sql", Username: "auditor"}); err != nil {
		t.Fatal(err)
	}
	m := &Migration{Legacy: Legacy{SQL: oldSQL}, Client: h.client}
	if p, err := m.Pending(); err != nil || p.SQL != 1 {
		t.Fatalf("pending: %+v %v", p, err)
	}
	if err := m.RemoveOld(false); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("remove: %v", err)
	}
}

// Another person on this PC saved the same address first: the move skips this person's copy, and that other record (whose password
// may differ) must not count as this person's being verified. Removing the old files then needs their explicit confirmation.
func TestMigrationDoesNotTakeSomeoneElsesRecordAsVerified(t *testing.T) {
	h := newHarness(t, nil)
	dir := t.TempDir()
	oldSQL, _ := sqlconn.New(filepath.Join(dir, "sqlconnections.dat"), xorProtector{})
	oldBB, _ := bbsaved.New(filepath.Join(dir, "blackboxes.dat"), xorProtector{})
	if _, err := oldSQL.Save(sqlconn.Input{Server: "sql01.example.test", Auth: "sql", Username: "auditor", Password: sqlSecret, RememberPassword: true}); err != nil {
		t.Fatal(err)
	}
	loc, _ := blackbox.ParseLocation("http://box01.example.test:8765")
	if _, err := oldBB.Add(bbsaved.AddInput{Name: "Box", Location: loc, Key: bbSecret}); err != nil {
		t.Fatal(err)
	}
	// Someone else's records at the same addresses, with their own secrets, under other ids.
	if _, err := h.backend.SQL.Save(sqlconn.Input{Server: "sql01.example.test", Auth: "sql", Username: "auditor", Password: "someone-elses-password", RememberPassword: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.backend.BB.Add(bbsaved.AddInput{Name: "Their box", Location: loc, Key: "someone-elses-read-key-000000000000"}); err != nil {
		t.Fatal(err)
	}
	m := &Migration{Legacy: Legacy{SQL: oldSQL, BB: oldBB}, Client: h.client}
	p, err := m.Migrate(context.Background())
	if err != nil || p.Total() != 0 || p.Duplicates != 2 {
		t.Fatalf("migrate: %+v %v", p, err)
	}
	if n, _ := h.backend.SQL.List(); len(n) != 1 {
		t.Fatalf("the person's copy was added beside the other: %d", len(n))
	}
	if err := m.RemoveOld(false); !errors.Is(err, ErrDuplicatesUnconfirmed) || !m.OldFilesPresent() {
		t.Fatalf("removed without confirming someone else's copies: %v", err)
	}
	if err := m.RemoveOld(true); err != nil || m.OldFilesPresent() {
		t.Fatalf("confirmed removal: %v", err)
	}
}
