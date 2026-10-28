package server

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const (
	seriesFrom = "2001-01-01T00:00:00Z"
	seriesTo   = "2001-01-01T00:01:00Z"
)

type seriesAnswer struct {
	From     string `json:"from"`
	To       string `json:"to"`
	BucketMs int64  `json:"bucketMs"`
	Buckets  int    `json:"buckets"`
	Partial  bool   `json:"partial"`
	Scanned  int64  `json:"scanned"`
	Series   []struct {
		Sel       string `json:"sel"`
		Unit      string `json:"unit"`
		UnitMixed bool   `json:"unitMixed"`
		Count     int64  `json:"count"`
		Points    []struct {
			T     int64   `json:"t"`
			Min   float64 `json:"min"`
			Mean  float64 `json:"mean"`
			Max   float64 `json:"max"`
			Count int64   `json:"count"`
		} `json:"points"`
	} `json:"series"`
}

func seriesURL(id, from, to, bucket string, sels ...string) string {
	q := url.Values{}
	q.Set("from", from)
	q.Set("to", to)
	if bucket != "" {
		q.Set("bucket", bucket)
	}
	for _, s := range sels {
		q.Add("sel", s)
	}
	return "/api/cases/" + id + "/series?" + q.Encode()
}

// seriesCase makes a case with seven toy events on web-a: five "toy" (ms 10,
// 20, 30 in the first ten seconds, 50 at second 12, 90 at second 59) and two
// "toy-slow" (200 at second 5, 400 at second 15).
func seriesCase(t *testing.T, e *caseEnv) string {
	t.Helper()
	id := e.newCase("Series case")
	file := &upload{"a.csv", toyCSV(
		toyRow(1, "web-a", "a", 10), toyRow(2, "web-a", "b", 20), toyRow(3, "web-a", "c", 30),
		toyRow(5, "web-a", "d", 200), toyRow(12, "web-a", "e", 50), toyRow(15, "web-a", "f", 400),
		toyRow(59, "web-a", "g", 90),
	)}
	if res := e.upload(commitPath(id), ackFields, file); res.Code != http.StatusCreated {
		t.Fatalf("import = %d %s", res.Code, res.Body)
	}
	return id
}

func TestSeriesAnswersBucketStatistics(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	res := e.get(seriesURL(id, seriesFrom, seriesTo, "10000", "kind=toy;field=duration", "kind=toy-slow;host=web-a;source=toy;attr.note=f;field=duration", "kind=toy"))
	if res.Code != http.StatusOK {
		t.Fatalf("series = %d %s", res.Code, res.Body)
	}
	if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type %q", ct)
	}
	var out seriesAnswer
	decodeBody(t, res, &out)
	if out.From != seriesFrom || out.To != seriesTo || out.BucketMs != 10000 || out.Buckets != 7 || out.Partial || out.Scanned == 0 {
		t.Fatalf("header: %+v", out)
	}
	if len(out.Series) != 3 || out.Series[0].Sel != "kind=toy;field=duration" || out.Series[1].Sel != "kind=toy-slow;host=web-a;source=toy;attr.note=f;field=duration" {
		t.Fatalf("series: %+v", out.Series)
	}
	start := int64(978307200000) // 2001-01-01T00:00:00Z
	// kind=toy: bucket 0 holds 10, 20, 30; bucket 1 holds 50; bucket 5 holds 90.
	a := out.Series[0]
	if a.Count != 5 || len(a.Points) != 3 || a.Unit != "ms" || a.UnitMixed {
		t.Fatalf("kind=toy: %+v", a)
	}
	for i, want := range []struct {
		t             int64
		min, mean, mx float64
		count         int64
	}{{start, 10, 20, 30, 3}, {start + 10000, 50, 50, 50, 1}, {start + 50000, 90, 90, 90, 1}} {
		p := a.Points[i]
		if p.T != want.t || p.Min != want.min || p.Mean != want.mean || p.Max != want.mx || p.Count != want.count {
			t.Errorf("point %d = %+v, want %+v", i, p, want)
		}
	}
	// The attribute filter picks the one slow event whose note is f.
	b := out.Series[1]
	if b.Count != 1 || len(b.Points) != 1 || b.Points[0].T != start+10000 || b.Points[0].Mean != 400 {
		t.Errorf("filtered series: %+v", b)
	}
	// Without field= the aggregate is the event value, which toy events do not carry.
	if c := out.Series[2]; c.Count != 0 || len(c.Points) != 0 {
		t.Errorf("default field series: %+v", c)
	}
}

func TestSeriesEmptyRangeIsAnEmptyListNotNull(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	res := e.get(seriesURL(id, "2001-02-01T00:00:00Z", "2001-02-01T00:01:00Z", "1000", "kind=toy", "kind=nothing"))
	if res.Code != http.StatusOK {
		t.Fatalf("series = %d %s", res.Code, res.Body)
	}
	if !strings.Contains(res.Body.String(), `"points":[]`) || strings.Contains(res.Body.String(), `"points":null`) {
		t.Errorf("body: %s", res.Body)
	}
	var out seriesAnswer
	decodeBody(t, res, &out)
	if len(out.Series) != 2 || out.Series[0].Count != 0 || out.Series[1].Count != 0 || out.Partial {
		t.Errorf("empty answer: %+v", out)
	}
}

func TestSeriesRefusals(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	base := "/api/cases/" + id + "/series?"
	many := make([]string, casestore.MaxSeriesCount+1)
	for i := range many {
		many[i] = "kind=toy"
	}
	for _, c := range []struct {
		name   string
		path   string
		reason string
	}{
		{"no time", base + "bucket=1000&sel=kind%3Dtoy", "bad-time"},
		{"no from", base + "to=" + seriesTo + "&bucket=1000&sel=kind%3Dtoy", "bad-time"},
		{"no to", base + "from=" + seriesFrom + "&bucket=1000&sel=kind%3Dtoy", "bad-time"},
		{"words for from", seriesURL(id, "yesterday", seriesTo, "1000", "kind=toy"), "bad-time"},
		{"no zone", seriesURL(id, "2001-01-01T00:00:00", seriesTo, "1000", "kind=toy"), "bad-time"},
		{"to before from", seriesURL(id, seriesTo, seriesFrom, "1000", "kind=toy"), "bad-time"},
		{"year 9999 to year 0001", seriesURL(id, "0001-01-01T00:00:00Z", "9999-01-01T00:00:00Z", "1000", "kind=toy"), "bad-time"},
		{"no bucket", seriesURL(id, seriesFrom, seriesTo, "", "kind=toy"), "bad-bucket"},
		{"word bucket", seriesURL(id, seriesFrom, seriesTo, "ten", "kind=toy"), "bad-bucket"},
		{"fraction bucket", seriesURL(id, seriesFrom, seriesTo, "1.5", "kind=toy"), "bad-bucket"},
		{"zero bucket", seriesURL(id, seriesFrom, seriesTo, "0", "kind=toy"), "bad-bucket"},
		{"negative bucket", seriesURL(id, seriesFrom, seriesTo, "-5", "kind=toy"), "bad-bucket"},
		{"bucket over int64", seriesURL(id, seriesFrom, seriesTo, "99999999999999999999", "kind=toy"), "bad-bucket"},
		{"too many buckets", seriesURL(id, seriesFrom, seriesTo, "10", "kind=toy"), "too-many-buckets"},
		{"no selector", seriesURL(id, seriesFrom, seriesTo, "1000"), "bad-selector"},
		{"empty selector", seriesURL(id, seriesFrom, seriesTo, "1000", ""), "bad-selector"},
		{"selector without kind", seriesURL(id, seriesFrom, seriesTo, "1000", "host=web-a"), "bad-selector"},
		{"unknown term", seriesURL(id, seriesFrom, seriesTo, "1000", "kind=toy;colour=red"), "bad-selector"},
		{"long selector", seriesURL(id, seriesFrom, seriesTo, "1000", "kind="+strings.Repeat("k", 600)), "bad-selector"},
		{"bad utf-8 selector", seriesURL(id, seriesFrom, seriesTo, "1000", "kind=\xff"), "bad-selector"},
		{"51 selectors", seriesURL(id, seriesFrom, seriesTo, "1000", many...), "too-many-series"},
	} {
		res := e.get(c.path)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", c.name, res.Code, res.Body)
			continue
		}
		if f := failureOf(t, res); f.Reason != c.reason || f.Error == "" {
			t.Errorf("%s: reason %q (%s), want %q", c.name, f.Reason, f.Error, c.reason)
		}
	}
	// The limit itself is accepted: 50 selectors over a range that fits 2 000 buckets.
	if res := e.get(seriesURL(id, seriesFrom, seriesTo, "31", many[:casestore.MaxSeriesCount]...)); res.Code != http.StatusOK {
		t.Errorf("50 selectors = %d %s", res.Code, res.Body)
	}
}

func TestSeriesTooManyBucketsNamesTheLeastBucket(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	res := e.get(seriesURL(id, seriesFrom, seriesTo, "10", "kind=toy"))
	if f := failureOf(t, res); f.Reason != "too-many-buckets" || !strings.Contains(f.Error, "31 ms") {
		t.Fatalf("%d %+v", res.Code, f)
	}
	// The bucket the message names is accepted.
	if res := e.get(seriesURL(id, seriesFrom, seriesTo, "31", "kind=toy")); res.Code != http.StatusOK {
		t.Errorf("named bucket = %d %s", res.Code, res.Body)
	}
}

func TestSeriesNeverEchoesFileContents(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Series secret")
	file := &upload{"a.csv", toyCSV(toyRow(1, "web-a", secretCell, 10))}
	if res := e.upload(commitPath(id), ackFields, file); res.Code != http.StatusCreated {
		t.Fatalf("import = %d %s", res.Code, res.Body)
	}
	// An attribute filter that does not match, and a selector that fails, say nothing about the cell.
	for _, path := range []string{
		seriesURL(id, seriesFrom, seriesTo, "1000", "kind=toy;attr.note=other"),
		seriesURL(id, seriesFrom, seriesTo, "1000", "kind=toy;attr.note="+secretCell+"x;colour=1"),
	} {
		if body := e.get(path).Body.String(); strings.Contains(body, secretCell) {
			t.Errorf("response carries a cell: %s", body)
		}
	}
}

func TestSeriesNeedsTheTokenAndTheRightMethod(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	path := seriesURL(id, seriesFrom, seriesTo, "10000", "kind=toy")
	for name, header := range map[string]string{"none": "", "wrong": "not-the-token"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if header != "" {
			req.Header.Set("X-Audit-Token", header)
		}
		if res := e.serve(req); res.Code != http.StatusUnauthorized {
			t.Errorf("%s token = %d %s, want 401", name, res.Code, res.Body)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-Audit-Token", testToken)
		if res := e.serve(req); res.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d %s, want 405", method, res.Code, res.Body)
		}
	}
}

func TestSeriesUnknownAndBadCaseIDs(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	unknown := strings.Repeat("a", 26)
	res := e.get(seriesURL(unknown, seriesFrom, seriesTo, "1000", "kind=toy"))
	if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "case-not-found" {
		t.Errorf("unknown case = %d %s", res.Code, res.Body)
	}
	for _, id := range []string{"BAD", "short", strings.Repeat("a", 27), strings.Repeat("A", 26), strings.Repeat("a", 25) + "-"} {
		res := e.get(seriesURL(id, seriesFrom, seriesTo, "1000", "kind=toy"))
		if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "bad-case-id" {
			t.Errorf("id %q = %d %s", id, res.Code, res.Body)
		}
	}
	// A bad case id is refused before the query is looked at.
	res = e.get("/api/cases/BAD/series")
	if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "bad-case-id" {
		t.Errorf("bad id, no query = %d %s", res.Code, res.Body)
	}
}

func TestSeriesWithoutAStoreIs503(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, false)
	res := request(h, http.MethodGet, seriesURL(strings.Repeat("a", 26), seriesFrom, seriesTo, "1000", "kind=toy"), "", true)
	if res.Code != http.StatusServiceUnavailable {
		t.Errorf("series without a store = %d", res.Code)
	}
}

func TestSeriesAfterEraseIs404(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	path := seriesURL(id, seriesFrom, seriesTo, "10000", "kind=toy")
	if res := e.get(path); res.Code != http.StatusOK {
		t.Fatalf("before erase = %d %s", res.Code, res.Body)
	}
	if res := e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`); res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	res := e.get(path)
	if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "case-not-found" {
		t.Errorf("after erase = %d %s", res.Code, res.Body)
	}
}

// Series reads that race an erase end in 200 (read first) or 404 (erase first),
// never in a server error, and every read after the erase is 404.
func TestSeriesRacingAnEraseEndsCleanly(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	path := seriesURL(id, seriesFrom, seriesTo, "1000", "kind=toy", "kind=toy-slow")
	const readers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make(chan int, readers*20)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 20; j++ {
				codes <- e.get(path).Code
			}
		}()
	}
	eraseDone := make(chan int, 1)
	go func() {
		<-start
		eraseDone <- e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`).Code
	}()
	close(start)
	wg.Wait()
	close(codes)
	if code := <-eraseDone; code != http.StatusNoContent {
		t.Fatalf("erase = %d", code)
	}
	for code := range codes {
		// 409 is M5: the erase was waiting for the readers in flight.
		if code != http.StatusOK && code != http.StatusNotFound && code != http.StatusConflict {
			t.Errorf("a series read racing an erase answered %d", code)
		}
	}
	if res := e.get(path); res.Code != http.StatusNotFound {
		t.Errorf("after the race = %d", res.Code)
	}
}

// A series read holds a read share, so an erase waits for it; the read ends
// with its answer and the erase then finishes.
func TestEraseWaitsForASeriesReadInFlight(t *testing.T) {
	var g caseGates
	id := casestore.CaseID(strings.Repeat("a", 26))
	// The series handler holds a read share the way acquireRead gives one.
	release, ok := g.acquireRead(id)
	if !ok {
		t.Fatal("read share refused")
	}
	erased := make(chan error, 1)
	go func() {
		done, err := g.acquireErase(context.Background(), id)
		if err == nil {
			done()
		}
		erased <- err
	}()
	select {
	case err := <-erased:
		t.Fatalf("erase got the case (%v) while a read share was held", err)
	case <-time.After(60 * time.Millisecond):
	}
	// A new read that arrives while the erase waits is told the case is gone.
	if _, ok := g.acquireRead(id); ok {
		t.Error("a read started during a pending erase")
	}
	release()
	select {
	case err := <-erased:
		if err != nil {
			t.Fatalf("erase: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("erase did not finish after the read ended")
	}
}

/* ------------------------------------------------------------ supplied */

// suppliedImporter is the toy importer with declared settings. It keeps what it
// was given in Options.Supplied.
type suppliedImporter struct {
	toyImporter
	mu   *sync.Mutex
	seen *[]map[string]string
}

func (s suppliedImporter) SuppliedKeys() map[string]importer.SuppliedSpec {
	return map[string]importer.SuppliedSpec{
		"log-time": {Kind: importer.SuppliedEnum, Values: []string{"utc", "local"}},
		"slowms":   {Kind: importer.SuppliedNumber, Min: 0, Max: 600000, Integer: true},
		"pattern":  {Kind: importer.SuppliedText, MaxLen: 60, Check: func(v string) bool { return strings.Contains(v, "%m") }},
	}
}

func (s suppliedImporter) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	s.mu.Lock()
	var copyOf map[string]string
	if opts.Supplied != nil {
		copyOf = make(map[string]string, len(opts.Supplied))
		for k, v := range opts.Supplied {
			copyOf[k] = v
		}
	}
	*s.seen = append(*s.seen, copyOf)
	s.mu.Unlock()
	return s.toyImporter.Parse(ctx, r, opts, emit)
}

type suppliedEnv struct {
	*caseEnv
	mu   sync.Mutex
	seen []map[string]string
}

func newSuppliedEnv(t *testing.T) *suppliedEnv {
	t.Helper()
	se := &suppliedEnv{}
	imp := suppliedImporter{toyImporter: toyImporter{name: "toy", score: 90}, mu: &se.mu, seen: &se.seen}
	se.caseEnv = newCaseEnv(t, envConfig{importers: []importer.Importer{imp}})
	return se
}

func (se *suppliedEnv) parses() []map[string]string {
	se.mu.Lock()
	defer se.mu.Unlock()
	return append([]map[string]string(nil), se.seen...)
}

var suppliedFile = &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))}

func TestSuppliedValuesReachTheImporterAndTheRecord(t *testing.T) {
	se := newSuppliedEnv(t)
	id := se.newCase("Supplied case")
	res := se.upload(commitPath(id)+"&s.slowms=0250&s.log-time=utc&s.pattern="+url.QueryEscape("%d %m"), ackFields, suppliedFile)
	if res.Code != http.StatusCreated {
		t.Fatalf("import = %d %s", res.Code, res.Body)
	}
	got := se.parses()
	if len(got) != 1 || len(got[0]) != 3 || got[0]["slowms"] != "250" || got[0]["log-time"] != "utc" || got[0]["pattern"] != "%d %m" {
		t.Fatalf("Options.Supplied = %v", got)
	}
	var created struct {
		Record importRecordDTO `json:"record"`
	}
	decodeBody(t, res, &created)
	want := map[string]string{"slowms": "250", "log-time": "utc", "pattern": "%d %m"}
	check := func(where string, have map[string]string) {
		t.Helper()
		if len(have) != len(want) {
			t.Errorf("%s supplied = %v", where, have)
			return
		}
		for k, v := range want {
			if have[k] != v {
				t.Errorf("%s supplied[%s] = %q, want %q", where, k, have[k], v)
			}
		}
	}
	check("create response", created.Record.Supplied)
	if recs := se.importsOf(id); len(recs) != 1 {
		t.Fatalf("imports = %d", len(recs))
	} else {
		check("listing", recs[0].Supplied)
	}
}

func TestImportWithoutSuppliedKeepsAnEmptyObject(t *testing.T) {
	se := newSuppliedEnv(t)
	id := se.newCase("No supplied")
	res := se.upload(commitPath(id), ackFields, suppliedFile)
	if res.Code != http.StatusCreated {
		t.Fatalf("import = %d %s", res.Code, res.Body)
	}
	if got := se.parses(); len(got) != 1 || got[0] != nil {
		t.Errorf("Options.Supplied = %v, want nil", got)
	}
	if !strings.Contains(res.Body.String(), `"supplied":{}`) {
		t.Errorf("record has no empty supplied object: %s", res.Body)
	}
	recs := se.importsOf(id)
	if len(recs) != 1 || recs[0].Supplied == nil || len(recs[0].Supplied) != 0 {
		t.Errorf("listing: %+v", recs)
	}
	body := se.get("/api/cases/" + id + "/imports").Body.String()
	if !strings.Contains(body, `"supplied":{}`) || strings.Contains(body, `"supplied":null`) {
		t.Errorf("listing body: %s", body)
	}
}

func TestSuppliedWorksWithPreviewAndStoresNothing(t *testing.T) {
	se := newSuppliedEnv(t)
	id := se.newCase("Supplied preview")
	res := se.upload(commitPath(id)+"&preview=1&s.slowms=100", nil, suppliedFile)
	if res.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Code, res.Body)
	}
	if got := se.parses(); len(got) != 1 || got[0]["slowms"] != "100" {
		t.Errorf("Options.Supplied = %v", got)
	}
	if n := len(se.importsOf(id)); n != 0 {
		t.Errorf("preview stored %d imports", n)
	}
	// A bad value is refused in preview as well, before the file is read.
	before := len(se.parses())
	res = se.upload(commitPath(id)+"&preview=1&s.slowms=fast", nil, suppliedFile)
	if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "bad-supplied" {
		t.Errorf("bad preview value = %d %s", res.Code, res.Body)
	}
	if len(se.parses()) != before {
		t.Error("the importer ran for a refused setting")
	}
}

func TestSuppliedRefusals(t *testing.T) {
	se := newSuppliedEnv(t)
	id := se.newCase("Supplied refusals")
	path := commitPath(id)
	var tooMany strings.Builder
	for i := 0; i <= importer.MaxSuppliedKeys; i++ {
		tooMany.WriteString("&s.k" + strconv.Itoa(i) + "=1")
	}
	for _, c := range []struct{ name, suffix string }{
		{"unknown key", "&s.surprise=1"},
		{"bad number", "&s.slowms=fast"},
		{"fraction in integer", "&s.slowms=1.5"},
		{"number over max", "&s.slowms=600001"},
		{"negative number", "&s.slowms=-1"},
		{"exponent", "&s.slowms=1e3"},
		{"empty number", "&s.slowms="},
		{"enum outside its words", "&s.log-time=UTC"},
		{"enum with a space", "&s.log-time=utc%20"},
		{"text fails its check", "&s.pattern=%25d"},
		{"text with a newline", "&s.pattern=%25m%0A"},
		{"text over its bound", "&s.pattern=" + strings.Repeat("%25m", 31)},
		{"upper-case key", "&s.Slowms=1"},
		{"empty key", "&s.=1"},
		{"duplicate key", "&s.slowms=1&s.slowms=2"},
		{"one good, one bad", "&s.slowms=1&s.surprise=1"},
		{"more than 16 keys", tooMany.String()},
		{"bad utf-8", "&s.pattern=%25m%FF"},
	} {
		res := se.upload(path+c.suffix, ackFields, suppliedFile)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", c.name, res.Code, res.Body)
			continue
		}
		if f := failureOf(t, res); f.Reason != "bad-supplied" {
			t.Errorf("%s reason = %q", c.name, f.Reason)
		}
	}
	if got := se.parses(); len(got) != 0 {
		t.Errorf("the importer ran %d times for refused settings", len(got))
	}
	if n := len(se.importsOf(id)); n != 0 {
		t.Errorf("%d imports stored by refused requests", n)
	}
	// None of those left the case locked or the import slot taken: a good import still runs.
	if res := se.upload(path+"&s.slowms=1", ackFields, suppliedFile); res.Code != http.StatusCreated {
		t.Errorf("import after refusals = %d %s", res.Code, res.Body)
	}
}

func TestSuppliedNeedsAnImporterThatDeclaresKeys(t *testing.T) {
	se := newSuppliedEnv(t)
	id := se.newCase("Supplied shape")
	// No importer named: nothing to check the key against.
	res := se.upload("/api/cases/"+id+"/imports?preview=1&s.slowms=1", nil, suppliedFile)
	if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "bad-supplied" {
		t.Errorf("preview without importer = %d %s", res.Code, res.Body)
	}
	// A commit without an importer is refused for that, ahead of the settings.
	res = se.upload("/api/cases/"+id+"/imports?host=web-a&s.slowms=1", ackFields, suppliedFile)
	if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "importer-required" {
		t.Errorf("commit without importer = %d %s", res.Code, res.Body)
	}
	// An importer that declares no keys accepts none.
	plain := newCaseEnv(t, envConfig{})
	pid := plain.newCase("Plain importer")
	res = plain.upload(commitPath(pid)+"&s.slowms=1", ackFields, suppliedFile)
	if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "bad-supplied" {
		t.Errorf("importer without keys = %d %s", res.Code, res.Body)
	}
	if n := len(plain.importsOf(pid)); n != 0 {
		t.Errorf("stored %d imports", n)
	}
	// The same file without settings imports as before.
	if res := plain.upload(commitPath(pid), ackFields, suppliedFile); res.Code != http.StatusCreated {
		t.Errorf("plain import = %d %s", res.Code, res.Body)
	}
}

// An unknown importer name is still reported as such, ahead of the settings.
func TestSuppliedWithUnknownImporterKeepsItsOwnReason(t *testing.T) {
	se := newSuppliedEnv(t)
	id := se.newCase("Unknown importer")
	res := se.upload("/api/cases/"+id+"/imports?importer=nope&s.slowms=1", ackFields, suppliedFile)
	if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "unknown-importer" {
		t.Errorf("= %d %s", res.Code, res.Body)
	}
}

func TestSuppliedAsMultipartFieldIsNotAForm(t *testing.T) {
	se := newSuppliedEnv(t)
	id := se.newCase("Multipart supplied")
	fields := append(append([]formField(nil), ackFields...), formField{"s.slowms", "100"})
	res := se.upload(commitPath(id), fields, suppliedFile)
	if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "bad-form" {
		t.Errorf("multipart s.* field = %d %s", res.Code, res.Body)
	}
	if got := se.parses(); len(got) != 0 {
		t.Errorf("importer ran %d times", len(got))
	}
}

// Neither a refusal nor a success repeats a supplied value, and nothing the
// server logs carries one.
func TestSuppliedValuesNeverReachAResponseOrALog(t *testing.T) {
	const secret = "SENTINEL-7f3a"
	var logs bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(flags) })

	se := newSuppliedEnv(t)
	id := se.newCase("Supplied secrets")
	pattern := url.QueryEscape("%m " + secret)
	var bodies []string
	for _, res := range []*httptest.ResponseRecorder{
		se.upload(commitPath(id)+"&s.pattern="+pattern, ackFields, suppliedFile), // accepted
		se.upload(commitPath(id)+"&s.pattern="+pattern, ackFields, suppliedFile), // duplicate bytes
		se.upload(commitPath(id)+"&s.slowms="+secret, ackFields, suppliedFile),   // bad number
		se.upload(commitPath(id)+"&s.log-time="+secret, ackFields, suppliedFile), // bad word
		se.upload(commitPath(id)+"&s."+secret+"="+secret, ackFields, suppliedFile),
		se.upload(commitPath(id)+"&preview=1&s.pattern="+url.QueryEscape(secret), nil, suppliedFile), // fails its check
		se.upload(commitPath(id)+"&preview=1&s.pattern="+pattern, nil, suppliedFile),
		se.get("/api/cases/" + id + "/imports"),
		se.get("/api/cases"),
	} {
		bodies = append(bodies, res.Body.String())
	}
	// The one accepted value is allowed in the record that holds it; nowhere else.
	for i, b := range bodies {
		allowed := i == 0 || i == 7
		if !allowed && strings.Contains(b, secret) {
			t.Errorf("response %d repeats a supplied value: %s", i, b)
		}
	}
	if !strings.Contains(bodies[0], secret) || !strings.Contains(bodies[7], secret) {
		t.Errorf("the record does not hold the value the owner supplied:\n%s\n%s", bodies[0], bodies[7])
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), "slowms") {
		t.Errorf("a log line carries a supplied setting: %q", logs.String())
	}
}

// The same bytes imported again with other settings are still a duplicate; the
// settings do not make a new import.
func TestSuppliedDoesNotMakeDuplicateBytesANewImport(t *testing.T) {
	se := newSuppliedEnv(t)
	id := se.newCase("Supplied duplicate")
	if res := se.upload(commitPath(id)+"&s.slowms=1", ackFields, suppliedFile); res.Code != http.StatusCreated {
		t.Fatalf("first = %d %s", res.Code, res.Body)
	}
	res := se.upload(commitPath(id)+"&s.slowms=2", ackFields, suppliedFile)
	if res.Code != http.StatusConflict || failureOf(t, res).Reason != "duplicate" {
		t.Errorf("second = %d %s", res.Code, res.Body)
	}
	if recs := se.importsOf(id); len(recs) != 1 || recs[0].Supplied["slowms"] != "1" {
		t.Errorf("records: %+v", recs)
	}
}

// A request that supplies settings while the case is being erased ends in a
// clean answer and leaves nothing behind.
func TestSuppliedImportRacingAnEraseEndsCleanly(t *testing.T) {
	se := newSuppliedEnv(t)
	id := se.newCase("Supplied erase race")
	var wg sync.WaitGroup
	codes := make(chan int, 6)
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			file := &upload{"a" + strconv.Itoa(i) + ".csv", toyCSV(toyRow(1+i, "web-a", "alpha", 5+i))}
			codes <- se.upload(commitPath(id)+"&s.slowms="+strconv.Itoa(i), ackFields, file).Code
		}(i)
	}
	eraseDone := make(chan int, 1)
	go func() {
		<-start
		eraseDone <- se.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`).Code
	}()
	close(start)
	wg.Wait()
	close(codes)
	if code := <-eraseDone; code != http.StatusNoContent {
		t.Fatalf("erase = %d", code)
	}
	for code := range codes {
		if code != http.StatusCreated && code != http.StatusNotFound {
			t.Errorf("an import racing an erase answered %d", code)
		}
	}
	if res := se.get("/api/cases/" + id + "/imports"); res.Code != http.StatusNotFound {
		t.Errorf("erased case imports = %d", res.Code)
	}
}
