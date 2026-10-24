package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/sqlserver"
	"api-audit-explorer/internal/ledger"
)

// Tests for the batch-2 fix round on the case routes. Each runs on the SQLite
// store (the production one) as well as the memory fake where the two differ.

func withMaxImports(n int) Option { return func(h *handler) { h.imports.max = n } }

type fix2Log struct {
	mu    sync.Mutex
	lines []string
}

func (l *fix2Log) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, strings.TrimSpace(format))
}

func (l *fix2Log) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = nil
}

func (l *fix2Log) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func fix2SQLite(t *testing.T, log *fix2Log) casestore.Store {
	t.Helper()
	opt := sqlite.Options{Root: filepath.Join(t.TempDir(), "cases"), Protector: storetest.RandomProtector(), MinFreeBytes: -1}
	if log != nil {
		opt.Logf = log.logf
	}
	s, err := sqlite.New(opt)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fix2Stores(t *testing.T) map[string]casestore.Store {
	return map[string]casestore.Store{"memory": casestore.NewMemory(), "sqlite": fix2SQLite(t, nil)}
}

func fix2Env(t *testing.T, store casestore.Store, led ledger.Store, imps []importer.Importer, opts ...Option) *caseEnv {
	t.Helper()
	if len(imps) == 0 {
		imps = []importer.Importer{toyImporter{name: "toy", score: 90}}
	}
	options := []Option{WithCases(store, mustHosts(t, hostsDoc), importer.NewRegistry(imps...))}
	if led != nil {
		options = append(options, WithLedger(led))
	}
	options = append(options, opts...)
	return &caseEnv{t: t, store: store, ledger: led, h: New(&fakeRepository{}, testAssets(), testToken, false, options...)}
}

func fix2Quarantines(t *testing.T, store casestore.Store, id string) []casestore.QuarantineRecord {
	t.Helper()
	cid, err := casestore.ParseID(id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(context.Background(), cid)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	q, ok := c.(casestore.Quarantiner)
	if !ok {
		t.Fatal("the store keeps no quarantine records")
	}
	recs, err := q.Quarantines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

// columnImporter refuses every file, naming the column the test gives it.
type columnImporter struct{ column string }

func (columnImporter) Name() string                   { return "toy" }
func (columnImporter) Detect([]byte, string) int      { return 90 }
func (columnImporter) AllowedAttrs() map[string]bool  { return nil }
func (columnImporter) DefaultLimits() importer.Limits { return importer.NewLimits(4096, 50) }
func (c columnImporter) Parse(_ context.Context, r io.Reader, _ importer.Options, _ func(evidence.Event) error) (importer.Report, error) {
	_, _ = io.Copy(io.Discard, r)
	return importer.Report{Importer: "toy", Rows: 1}, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: c.column}
}

// blockingImporter holds every parse until release closes (or three seconds pass,
// so a failing test ends rather than hangs).
func blockingImporter(entered chan<- struct{}, release <-chan struct{}) toyImporter {
	var once sync.Once
	return toyImporter{name: "toy", score: 90, onParse: func() {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
	}}
}

/* ------------------------------------------------------------ M1 */

// A preview on the SQLite store carries the importer's report (batch-2 M1).
func TestFix2PreviewReportOnSQLite(t *testing.T) {
	e := fix2Env(t, fix2SQLite(t, nil), nil, nil)
	id := e.newCase("Preview report")
	file := &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5), toyRow(2, "web-a", "beta", 500), "not-a-row")}
	res := e.upload(commitPath(id)+"&preview=1", nil, file)
	if res.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Code, res.Body)
	}
	var out struct {
		Report *reportDTO `json:"report"`
	}
	decodeBody(t, res, &out)
	if out.Report == nil || out.Report.Rows != 3 || out.Report.Emitted != 2 || out.Report.Rejected != 1 {
		t.Fatalf("SQLite preview report = %+v", out.Report)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("preview stored %d imports", n)
	}
}

/* ------------------------------------------------------------ M2 */

// A preview of a file the importer refuses answers the refusal and stores
// nothing: no quarantine record, no log line (batch-2 M2).
func TestFix2PreviewOfRefusedFileStoresNothing(t *testing.T) {
	log := &fix2Log{}
	for name, store := range map[string]casestore.Store{"memory": casestore.NewMemory(), "sqlite": fix2SQLite(t, log)} {
		t.Run(name, func(t *testing.T) {
			log.reset() // the map visits memory and sqlite in either order
			e := fix2Env(t, store, nil, nil)
			id := e.newCase("Preview refused")
			for label, c := range map[string]struct {
				path, body, reason string
			}{
				"wrong header": {commitPath(id) + "&preview=1", "not,the,header\n1,2,3\n", string(importer.ReasonUnsupportedFormat)},
				"unknown host": {"/api/cases/" + id + "/imports?importer=toy&preview=1", toyCSV(toyRow(1, "web-z", "alpha", 5)), string(importer.ReasonUnknownHost)},
			} {
				res := e.upload(c.path, nil, &upload{"bad.csv", c.body})
				if res.Code != http.StatusUnprocessableEntity || failureOf(t, res).Reason != c.reason {
					t.Errorf("%s: preview = %d %s", label, res.Code, res.Body)
				}
			}
			if recs := fix2Quarantines(t, store, id); len(recs) != 0 {
				t.Errorf("preview stored %d quarantine records: %+v", len(recs), recs)
			}
			if s := log.text(); strings.Contains(s, "quarantine") {
				t.Errorf("preview wrote a quarantine log line:\n%s", s)
			}
			if n := len(e.importsOf(id)); n != 0 {
				t.Errorf("preview stored %d imports", n)
			}
			// A real import of the same file is still recorded as a refusal.
			res := e.upload(commitPath(id), ackFields, &upload{"bad.csv", "not,the,header\n1,2,3\n"})
			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("commit of a refused file = %d %s", res.Code, res.Body)
			}
			if recs := fix2Quarantines(t, store, id); len(recs) != 1 {
				t.Errorf("a refused commit left %d quarantine records, want 1", len(recs))
			}
		})
	}
}

/* ------------------------------------------------------------ M3 */

// The Column an importer names never reaches the client unless it is a short
// canonical name (batch-2 M3 backstop).
func TestFix2ColumnBackstopOnTheResponse(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"canonical", "wait_type", "wait_type"},
		{"host name", "sql-a.example.test hostname", ""},
		{"comma", "web-a,job-alpha", ""},
		{"too long", strings.Repeat("a", 65), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := fix2Env(t, fix2SQLite(t, nil), nil, []importer.Importer{columnImporter{column: c.in}})
			id := e.newCase("Column case")
			for label, res := range map[string]*httptest.ResponseRecorder{
				"commit":  e.upload(commitPath(id), ackFields, &upload{"x.csv", "some bytes\n"}),
				"preview": e.upload(commitPath(id)+"&preview=1", nil, &upload{"x.csv", "other bytes\n"}),
			} {
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("%s = %d %s", label, res.Code, res.Body)
				}
				if got := failureOf(t, res).Column; got != c.want {
					t.Errorf("%s: Column %q, want %q", label, got, c.want)
				}
				if strings.Contains(res.Body.String(), "sql-a") {
					t.Errorf("%s: body echoes the header text: %s", label, res.Body)
				}
			}
			for _, rec := range fix2Quarantines(t, e.store, id) {
				if rec.Column != c.want {
					t.Errorf("stored Column %q, want %q", rec.Column, c.want)
				}
			}
		})
	}
}

// A real importer, a header that holds a host name: the name is in neither the
// response nor the stored quarantine record (batch-2 M3).
func TestFix2XEHeaderTextIsNotEchoed(t *testing.T) {
	e := fix2Env(t, fix2SQLite(t, nil), nil, []importer.Importer{sqlserver.NewXE()})
	id := e.newCase("XE case")
	csv := "timestamp_utc,name,sql-a.example.test hostname\n2001-01-01T00:00:00Z,wait_info,x\n"
	path := "/api/cases/" + id + "/imports?importer=" + sqlserver.XEName + "&host=web-a"
	for label, res := range map[string]*httptest.ResponseRecorder{
		"preview": e.upload(path+"&preview=1", nil, &upload{"xe.csv", csv}),
		"commit":  e.upload(path, ackFields, &upload{"xe.csv", csv}),
	} {
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d %s", label, res.Code, res.Body)
		}
		if strings.Contains(res.Body.String(), "sql-a") {
			t.Errorf("%s: body echoes the header text: %s", label, res.Body)
		}
	}
	recs := fix2Quarantines(t, e.store, id)
	if len(recs) != 1 {
		t.Fatalf("%d quarantine records, want 1 (the commit only)", len(recs))
	}
	if strings.Contains(recs[0].Column, "sql-a") || strings.Contains(recs[0].FileName+recs[0].Reason.Text(), "sql-a") {
		t.Errorf("stored record holds the header text: %+v", recs[0])
	}
}

/* ------------------------------------------------------------ L1 */

// Close takes the same per-case gate as import and erase: it waits for an import
// in flight, and the import then succeeds (batch-2 L1).
func TestFix2CloseWaitsForAnImportInFlight(t *testing.T) {
	for name, store := range fix2Stores(t) {
		t.Run(name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			e := fix2Env(t, store, nil, []importer.Importer{blockingImporter(entered, release)})
			id := e.newCase("Close case")
			importDone := make(chan int, 1)
			go func() {
				importDone <- e.upload(commitPath(id), ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))}).Code
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("import did not start")
			}
			closeDone := make(chan int, 1)
			go func() { closeDone <- e.json(http.MethodPost, "/api/cases/"+id+"/close", "").Code }()
			select {
			case code := <-closeDone:
				t.Fatalf("close finished (%d) while an import was running", code)
			case <-time.After(150 * time.Millisecond):
			}
			close(release)
			if code := <-importDone; code != http.StatusCreated {
				t.Fatalf("import = %d, want 201", code)
			}
			if code := <-closeDone; code != http.StatusOK {
				t.Fatalf("close = %d, want 200", code)
			}
			res := e.upload(commitPath(id), ackFields, &upload{"b.csv", toyCSV(toyRow(2, "web-a", "beta", 5))})
			if res.Code != http.StatusConflict || failureOf(t, res).Reason != "case-closed" {
				t.Fatalf("import after close = %d %s", res.Code, res.Body)
			}
			if n := len(e.importsOf(id)); n != 1 {
				t.Fatalf("imports = %d, want 1", n)
			}
		})
	}
}

// A store that refuses with ErrCaseClosed (a close won the race) or
// ErrAckTooLong answers with its own word, never the generic 500.
func TestFix2StoreRefusalsHaveTheirOwnWords(t *testing.T) {
	h := &handler{}
	for _, c := range []struct {
		err    error
		status int
		reason string
	}{
		{casestore.ErrCaseClosed, http.StatusConflict, "case-closed"},
		{casestore.ErrAckTooLong, http.StatusBadRequest, "ack-too-long"},
	} {
		res := httptest.NewRecorder()
		h.writeImportError(res, c.err, nil, casestore.ImportResult{})
		if res.Code != c.status || failureOf(t, res).Reason != c.reason {
			t.Errorf("import error %v = %d %s", c.err, res.Code, res.Body)
		}
		res = httptest.NewRecorder()
		writeStoreError(res, c.err)
		if res.Code != c.status || failureOf(t, res).Reason != c.reason {
			t.Errorf("store error %v = %d %s", c.err, res.Code, res.Body)
		}
	}
}

/* ------------------------------------------------------------ L2 */

// The API and the store agree on the acknowledgement cap, 4 KiB, and a longer
// text is a 400 with its own word, not a 500 (batch-2 L2).
func TestFix2AckTextLimit(t *testing.T) {
	for name, store := range fix2Stores(t) {
		t.Run(name, func(t *testing.T) {
			e := fix2Env(t, store, nil, nil)
			id := e.newCase("Ack case")
			fields := func(n int) []formField {
				return []formField{{"ackGating", "true"}, {"ackText", strings.Repeat("a", n)}}
			}
			res := e.upload(commitPath(id), fields(casestore.MaxAckText), &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
			if res.Code != http.StatusCreated {
				t.Fatalf("ackText of exactly the cap = %d %s", res.Code, res.Body)
			}
			for _, n := range []int{casestore.MaxAckText + 1, 5000, 9000} {
				res = e.upload(commitPath(id), fields(n), &upload{"b.csv", toyCSV(toyRow(2, "web-a", "beta", 5))})
				if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "ack-too-long" {
					t.Errorf("ackText of %d bytes = %d %s", n, res.Code, res.Body)
				}
			}
			if n := len(e.importsOf(id)); n != 1 {
				t.Errorf("imports = %d, want 1", n)
			}
		})
	}
}

/* ------------------------------------------------------------ L6 */

// Imports in different cases run in parallel up to a cap; one more is refused
// with a stable word and a retry hint, and works once a slot frees (batch-2 L6).
func TestFix2ImportCapAnswersBusy(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	e := fix2Env(t, fix2SQLite(t, nil), nil, []importer.Importer{blockingImporter(entered, release)}, withMaxImports(1))
	a, b := e.newCase("Case A"), e.newCase("Case B")
	first := make(chan int, 1)
	go func() {
		first <- e.upload(commitPath(a), ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))}).Code
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first import did not start")
	}
	res := e.upload(commitPath(b), ackFields, &upload{"b.csv", toyCSV(toyRow(2, "web-a", "beta", 5))})
	if res.Code != http.StatusTooManyRequests || failureOf(t, res).Reason != "import-busy" || res.Header().Get("Retry-After") == "" {
		t.Fatalf("second import = %d %v %s", res.Code, res.Header(), res.Body)
	}
	// A preview parses the whole file too, so it counts.
	res = e.upload(commitPath(b)+"&preview=1", nil, &upload{"b.csv", toyCSV(toyRow(2, "web-a", "beta", 5))})
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("preview while busy = %d %s", res.Code, res.Body)
	}
	if n := len(e.importsOf(b)); n != 0 {
		t.Fatalf("a refused import stored %d records", n)
	}
	close(release)
	if code := <-first; code != http.StatusCreated {
		t.Fatalf("first import = %d", code)
	}
	res = e.upload(commitPath(b), ackFields, &upload{"b.csv", toyCSV(toyRow(2, "web-a", "beta", 5))})
	if res.Code != http.StatusCreated {
		t.Fatalf("import after a slot freed = %d %s", res.Code, res.Body)
	}
}

func TestFix2ImportLimiterDefaultIsTwo(t *testing.T) {
	var l importLimiter
	var releases []func()
	for i := 0; i < 2; i++ {
		rel, ok := l.tryAcquire()
		if !ok {
			t.Fatalf("slot %d refused", i+1)
		}
		releases = append(releases, rel)
	}
	if _, ok := l.tryAcquire(); ok {
		t.Fatal("a third slot was given")
	}
	releases[0]()
	rel, ok := l.tryAcquire()
	if !ok {
		t.Fatal("a freed slot was not reusable")
	}
	rel()
	releases[1]()
	if l.running != 0 {
		t.Fatalf("%d slots leaked", l.running)
	}
}

// Imports queued behind one case's gate hold no slot while they wait, so a busy
// case does not starve the others.
func TestFix2QueuedImportsHoldNoSlot(t *testing.T) {
	e := fix2Env(t, casestore.NewMemory(), nil, nil, withMaxImports(1))
	id := e.newCase("Queue case")
	const n = 4
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = e.upload(commitPath(id), ackFields, &upload{"c.csv", toyCSV(toyRow(i+1, "web-a", "alpha", 5))}).Code
		}(i)
	}
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusCreated {
			t.Errorf("import %d = %d", i, code)
		}
	}
}

/* ------------------------------------------------------------ L7 */

// stripLedger counts the strips and can fail them.
type stripLedger struct {
	ledger.Store
	mu    sync.Mutex
	calls int
	fail  bool
}

func (l *stripLedger) StripEvidence(prefixes []string, replacement string) (int, error) {
	l.mu.Lock()
	l.calls++
	fail := l.fail
	l.mu.Unlock()
	if fail {
		return 0, errors.New("ledger disk full")
	}
	return l.Store.(ledger.Stripper).StripEvidence(prefixes, replacement)
}

func (l *stripLedger) setFail(v bool) {
	l.mu.Lock()
	l.fail = v
	l.mu.Unlock()
}

func (l *stripLedger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func newStripLedger(t *testing.T) *stripLedger {
	t.Helper()
	led, err := ledger.Open("")
	if err != nil {
		t.Fatal(err)
	}
	return &stripLedger{Store: led}
}

func eraseBody(id string) string { return `{"confirm":"` + id + `"}` }

// Erasing an id that names no case is a 404 and does not touch the ledger
// (batch-2 L7). A case erased once answers 404 the second time.
func TestFix2EraseOfUnknownCaseIs404(t *testing.T) {
	for name, store := range fix2Stores(t) {
		t.Run(name, func(t *testing.T) {
			led := newStripLedger(t)
			e := fix2Env(t, store, led, nil)
			missing := strings.Repeat("a", 26)
			res := e.json(http.MethodDelete, "/api/cases/"+missing, eraseBody(missing))
			if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "case-not-found" {
				t.Fatalf("erase of an unknown id = %d %s", res.Code, res.Body)
			}
			if led.count() != 0 {
				t.Fatalf("an unknown id ran the ledger strip %d times", led.count())
			}
			id := e.newCase("Real case")
			if res = e.json(http.MethodDelete, "/api/cases/"+id, eraseBody(id)); res.Code != http.StatusNoContent {
				t.Fatalf("erase = %d %s", res.Code, res.Body)
			}
			if led.count() != 1 {
				t.Fatalf("a real erase ran the strip %d times, want 1", led.count())
			}
			res = e.json(http.MethodDelete, "/api/cases/"+id, eraseBody(id))
			if res.Code != http.StatusNotFound || led.count() != 1 {
				t.Fatalf("second erase = %d, strips %d", res.Code, led.count())
			}
		})
	}
}

// When the ledger could not be stripped, the same erase request, repeated, finishes
// the job even though the case is already gone.
func TestFix2EraseRetryAfterLedgerFailureStillStrips(t *testing.T) {
	for name, store := range fix2Stores(t) {
		t.Run(name, func(t *testing.T) {
			led := newStripLedger(t)
			e := fix2Env(t, store, led, nil)
			id := e.newCase("Retry case")
			led.setFail(true)
			res := e.json(http.MethodDelete, "/api/cases/"+id, eraseBody(id))
			if res.Code != http.StatusInternalServerError || failureOf(t, res).Reason != "ledger-not-stripped" {
				t.Fatalf("erase with a failing ledger = %d %s", res.Code, res.Body)
			}
			led.setFail(false)
			if res = e.json(http.MethodDelete, "/api/cases/"+id, eraseBody(id)); res.Code != http.StatusNoContent {
				t.Fatalf("retry = %d %s", res.Code, res.Body)
			}
			if led.count() != 2 {
				t.Fatalf("strips = %d, want 2", led.count())
			}
			if res = e.json(http.MethodDelete, "/api/cases/"+id, eraseBody(id)); res.Code != http.StatusNotFound {
				t.Fatalf("erase after the retry = %d", res.Code)
			}
		})
	}
}
