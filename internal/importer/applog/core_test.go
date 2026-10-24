package applog

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const heartbeatMsg = `As of "10/25/2001 01:02:31 +00:00", the heartbeat has been running for "00:00:02.5" which is longer than "00:00:01".`

func sampleRecords() []Record {
	at := func(r Record, d time.Duration) Record { r.Time = t0.Add(d); return r }
	reqFast := rec("Information", catHostingDiagnostics, "Request finished HTTP/1.1 GET http://web-a.example.test/a - 200 5 text/plain 200.5ms")
	reqFast.EventID = 2
	reqSlow := rec("Information", catHostingDiagnostics, "Request finished HTTP/1.1 GET http://web-a.example.test/b?q="+sentinel+
		" - 200 512 application/json 1834.2000ms")
	reqSlow.EventID = 2
	hb := rec("Warning", catKestrel, heartbeatMsg)
	hb.EventID = 22
	errRec := rec("Error", "Synthetic.Api.OrdersController", "Order "+sentinel+" failed")
	errRec.Exception = "System.InvalidOperationException: outer ---> Synthetic.Api.QuotaException: " + sentinel + "\n   at Synthetic.Api.Run()"
	return []Record{
		reqFast,
		at(reqSlow, time.Second),
		at(rec("WARN", "StackExchange.Redis", redisMsg), 2*time.Second),
		at(rec("Information", catLifetime, "Now listening on: http://[::]:80"), 3*time.Second),
		at(rec("Information", catLifetime, "Application started. Press Ctrl+C to shut down."), 4*time.Second),
		at(rec("Information", catLifetime, "Application is shutting down..."), 50*time.Second),
		at(hb, 70*time.Second),
		at(errRec, 71*time.Second),
	}
}

func TestSinkEmits(t *testing.T) {
	res := mustRun(t, testOpts(), sampleRecords())
	count := map[string]int{}
	ids := map[string]bool{}
	for _, e := range res.evs {
		count[e.Kind]++
		if ids[e.EvidenceID] {
			t.Errorf("evidence id %s repeated", e.EvidenceID)
		}
		ids[e.EvidenceID] = true
		if e.Host != "web-a" {
			t.Errorf("host = %q", e.Host)
		}
	}
	// log-entry: redis, both lifetime start lines, the stop line, heartbeat
	// and the error; the requests below warning make none.
	want := map[string]int{KindLogEntry: 6, KindAppRequest: 1, KindAppStart: 1, KindAppStop: 1, KindHeartbeatLate: 1}
	for k, n := range want {
		if count[k] != n {
			t.Errorf("%s events = %d, want %d (all: %v)", k, count[k], n, count)
		}
	}
	// log-rate counts every line exactly once.
	var total float64
	for _, e := range byKind(res.evs, KindLogRate) {
		total += *e.Value
		if e.Unit != "count" || !e.Start.Equal(e.End) || e.Start.Second() != 0 {
			t.Errorf("log-rate point %+v", e)
		}
	}
	if total != 8 {
		t.Errorf("log-rate total = %v, want 8", total)
	}
	if res.rep.Emitted != int64(len(res.evs)) {
		t.Errorf("Emitted = %d, events = %d", res.rep.Emitted, len(res.evs))
	}

	ar := byKind(res.evs, KindAppRequest)[0]
	if *ar.DurationMs != 1834.2 || !ar.End.Equal(t0.Add(time.Second)) || !ar.Start.Equal(ar.End.Add(-1834200*time.Microsecond)) ||
		ar.Attrs[AttrMethod] != "GET" || ar.Attrs[AttrStatus] != "200" || ar.Line != 2 {
		t.Errorf("app-request = %+v %v", ar, ar.Attrs)
	}
	hb := byKind(res.evs, KindHeartbeatLate)[0]
	if *hb.DurationMs != 2500 || hb.Severity != "warning" || !hb.Start.Equal(hb.End.Add(-2500*time.Millisecond)) {
		t.Errorf("heartbeat-late = %+v", hb)
	}
	st := byKind(res.evs, KindAppStart)[0]
	if st.Line != 4 || !st.Start.Equal(st.End) {
		t.Errorf("app-start = %+v", st)
	}
	var errEntry evidence.Event
	for _, e := range byKind(res.evs, KindLogEntry) {
		if e.Line == 8 {
			errEntry = e
		}
	}
	if errEntry.Attrs[AttrException] != "System.InvalidOperationException,"+AppException ||
		!loggerPseudonym.MatchString(errEntry.Attrs[AttrLogger]) || errEntry.Attrs[AttrFamily] != FamilyOther ||
		errEntry.Severity != "error" || errEntry.Attrs[AttrLevel] != "Error" {
		t.Errorf("error entry = %+v", errEntry)
	}
	// The slow request (line 2) started before the first line's time.
	if !res.rep.First.Equal(ar.Start) || !res.rep.Last.Equal(t0.Add(71*time.Second)) {
		t.Errorf("coverage %v..%v", res.rep.First, res.rep.Last)
	}
	for _, g := range []string{GapDropped, GapRequestThreshold} {
		if !hasGap(res.rep, g) {
			t.Errorf("gap missing: %q", g)
		}
	}
	if hasGap(res.rep, GapEntryCap) || hasGap(res.rep, GapUnknownLevel) {
		t.Errorf("unexpected gaps %v", res.rep.KnownGaps)
	}
	// Same input, same events.
	again := mustRun(t, testOpts(), sampleRecords())
	if !reflect.DeepEqual(res.evs, again.evs) {
		t.Error("events differ between two runs")
	}
}

// Every value the core stores survives the case export.
func TestEventsSurviveExport(t *testing.T) {
	res := mustRun(t, testOpts(), sampleRecords())
	for _, e := range res.evs {
		got := exportAttrs(t, e)
		for k, v := range e.Attrs {
			if got[k] == "" || got[k] == removedVal {
				t.Errorf("%s line %d: %s=%q exported as %q", e.Kind, e.Line, k, v, got[k])
			}
		}
	}
}

func TestPrivacySentinel(t *testing.T) {
	s := sentinel
	recs := []Record{
		{Time: t0, Rule: clock.RuleUTCField, Level: s, Category: "Synthetic." + s, Message: s + " message",
			Exception: "System.InvalidOperationException: " + s + " ---> Synthetic." + s + "Exception: " + s + "\n   at " + s + "()",
			Num:       map[string]float64{s: 1, NumElapsed: 5000}},
		{Time: t0, Rule: clock.RuleUTCField, Level: "ERROR", Category: "Microsoft." + s, Message: "Timeout performing GET (5000ms), next: GET " + s +
			", qs: 1, in: 0", EventID: 2},
		{Time: t0, Rule: clock.RuleUTCField, Level: "WARN", Category: catHostingDiagnostics, EventID: 2,
			Message: "Request finished HTTP/1.1 POST http://" + s + ".example.test/" + s + " - 503 0 - 9000ms"},
		{Time: t0, Rule: clock.RuleUTCField, Level: "WARN", Category: s, Message: "HTTP GET /" + s + " responded 200 in 1500 ms",
			Exception: s},
		{Time: t0, Rule: clock.RuleUTCField, Level: "fatal", Category: "", Message: "",
			Exception: "Unhandled exception. " + s + ".Exception: " + s},
		{Time: time.Time{}, Rule: clock.RuleUTCField, Message: s},
	}
	res, err := runSink(t, testOpts(), recs)
	out := dump(t, res.evs, res.rep, errString(err))
	if strings.Contains(strings.ToLower(out), strings.ToLower(s)) {
		t.Fatalf("sentinel in output: %s", out)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(byKind(res.evs, KindLogEntry)) < 4 || len(byKind(res.evs, KindAppRequest)) != 2 {
		t.Errorf("events: %s", out)
	}
	if !hasGap(res.rep, GapUnknownLevel) || res.rep.Rejected != 1 {
		t.Errorf("report = %+v", res.rep)
	}
	// The unknown level is not stored and leaves no severity.
	for _, e := range res.evs {
		if e.Line == 1 && (e.Kind != KindLogRate || e.Attrs[AttrLevel] != "" || e.Severity != "") {
			t.Errorf("unknown level stored or line 1 made an entry: %+v", e)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Only the listed attributes ever appear, whatever the record carries.
func TestOnlyFamilyCapturesReachEvents(t *testing.T) {
	r := rec("WARN", "Synthetic.Api", "plain text")
	r.Num = map[string]float64{AttrQs: 7, AttrTimeoutMs: 9, NumStatus: 500, "anything": 1}
	r.EventID = 1234
	res := mustRun(t, testOpts(), []Record{r})
	le := byKind(res.evs, KindLogEntry)
	if len(le) != 1 {
		t.Fatalf("log-entry = %d", len(le))
	}
	want := map[string]bool{AttrLevel: true, AttrLogger: true, AttrFamily: true}
	for k := range le[0].Attrs {
		if !want[k] {
			t.Errorf("attr %q reached the event from Record.Num or EventID", k)
		}
	}
}

func TestStructuredRequestNums(t *testing.T) {
	put, _ := MethodNum("PUT")
	r := rec("Information", catHostingDiagnostics, "")
	r.EventID = 2
	r.Num = map[string]float64{NumElapsedMs: 2000, NumStatus: 503, NumMethod: put}
	bad := rec("Information", catSerilogRequests, "HTTP GET /a responded 200 in 5 ms")
	bad.Num = map[string]float64{NumElapsed: 3000, NumStatus: 700, NumMethod: 99}
	res := mustRun(t, testOpts(), []Record{r, bad})
	ar := byKind(res.evs, KindAppRequest)
	if len(ar) != 2 {
		t.Fatalf("app-request = %d", len(ar))
	}
	if *ar[0].DurationMs != 2000 || ar[0].Attrs[AttrMethod] != "PUT" || ar[0].Attrs[AttrStatus] != "503" {
		t.Errorf("structured request = %+v %v", ar[0], ar[0].Attrs)
	}
	if *ar[1].DurationMs != 3000 || ar[1].Attrs[AttrMethod] != "GET" || ar[1].Attrs[AttrStatus] != "200" {
		t.Errorf("bad properties kept: %+v %v", ar[1], ar[1].Attrs)
	}
	if len(byKind(res.evs, KindLogEntry)) != 0 {
		t.Error("request-finished below warning made a log-entry")
	}
}

func TestRequestMinMs(t *testing.T) {
	recs := []Record{rec("INFO", catSerilogRequests, "HTTP GET /a responded 200 in 600 ms")}
	o := testOpts()
	o.Supplied = map[string]string{SuppliedRequestMinMs: " 500 "}
	if res := mustRun(t, o, recs); len(byKind(res.evs, KindAppRequest)) != 1 || hasGap(res.rep, GapThresholdUnreadable) {
		t.Errorf("min 500: %v %v", res.evs, res.rep.KnownGaps)
	}
	for _, v := range []string{"abc", "-1", "NaN", "Inf", "1e13", ""} {
		o.Supplied = map[string]string{SuppliedRequestMinMs: v}
		res := mustRun(t, o, recs)
		if len(byKind(res.evs, KindAppRequest)) != 0 || !hasGap(res.rep, GapThresholdUnreadable) {
			t.Errorf("min %q: %d app-request, gaps %v", v, len(byKind(res.evs, KindAppRequest)), res.rep.KnownGaps)
		}
	}
	if res := mustRun(t, testOpts(), recs); len(byKind(res.evs, KindAppRequest)) != 0 {
		t.Error("default threshold 1000 ms kept a 600 ms request")
	}
}

func TestEntryCap(t *testing.T) {
	old := entryCap
	entryCap = 3
	t.Cleanup(func() { entryCap = old })
	var recs []Record
	for i := 0; i < 5; i++ {
		recs = append(recs, rec("WARN", "Synthetic.Api", "w"))
	}
	res := mustRun(t, testOpts(), recs)
	if n := len(byKind(res.evs, KindLogEntry)); n != 3 {
		t.Errorf("log-entry = %d, want 3", n)
	}
	rate := byKind(res.evs, KindLogRate)
	if len(rate) != 1 || *rate[0].Value != 5 {
		t.Errorf("log-rate = %+v", rate)
	}
	if !hasGap(res.rep, GapEntryCap) {
		t.Error("cap gap missing")
	}
}

// The plan's own case: 1,000,001 warnings give 1,000,000 entries, a gap and
// an exact count.
func TestEntryCapMillion(t *testing.T) {
	if testing.Short() {
		t.Skip("1M records")
	}
	var entries, rateSum int64
	var rep importer.Report
	s := NewSink(testSrc, testOpts(), func(ev evidence.Event) error {
		switch ev.Kind {
		case KindLogEntry:
			entries++
		case KindLogRate:
			rateSum += int64(*ev.Value)
		}
		return nil
	})
	r := rec("WARN", "System", "w")
	for i := int64(1); i <= MaxLogEntries+1; i++ {
		if err := s.Add(r, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Finish(&rep); err != nil {
		t.Fatal(err)
	}
	if entries != MaxLogEntries || rateSum != MaxLogEntries+1 || !hasGap(rep, GapEntryCap) || rep.Emitted != MaxLogEntries+1 {
		t.Errorf("entries %d, rate %d, emitted %d, gaps %v", entries, rateSum, rep.Emitted, rep.KnownGaps)
	}
}

func TestAppStartDedupe(t *testing.T) {
	a := rec("Information", catLifetime, "Now listening on: http://[::]:80")
	b := rec("Information", catLifetime, "Application started.")
	b.Time = t0.Add(30 * time.Second)
	c := rec("Information", catLifetime, "Application started.")
	c.Time = t0.Add(61 * time.Second)
	res := mustRun(t, testOpts(), []Record{a, b, c})
	if st := byKind(res.evs, KindAppStart); len(st) != 2 || st[1].Line != 3 {
		t.Errorf("app-start = %+v", st)
	}
}

func TestLocalTimeAndReject(t *testing.T) {
	akl, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Fatal(err)
	}
	const layout = "2006-01-02 15:04:05"
	cases := []struct {
		v      string
		loc    *time.Location
		reason importer.Reason
	}{
		{"2001-03-18 02:30:00", akl, importer.ReasonDSTAmbiguous},
		{"2001-10-07 02:30:00", akl, importer.ReasonDSTNonexistent},
		{"2001-10-25 14:02:03", nil, importer.ReasonTZUnlabelled},
		{"25/10/2001", akl, importer.ReasonBadTime},
	}
	for _, c := range cases {
		if got, reason := LocalTime(layout, c.v, c.loc); reason != c.reason || !got.IsZero() {
			t.Errorf("LocalTime(%q) = %v %q, want %q", c.v, got, reason, c.reason)
		}
	}
	got, reason := LocalTime(layout, "2001-10-25 14:02:03", akl)
	if reason != "" || !got.Equal(t0) || got.Location() != time.UTC {
		t.Errorf("LocalTime good = %v %q, want %v", got, reason, t0)
	}

	var rep importer.Report
	s := NewSink(testSrc, testOpts(), func(evidence.Event) error { return nil })
	s.Reject(7, importer.ReasonDSTAmbiguous)
	if err := s.Add(Record{Rule: clock.RuleHostLocal, Level: "WARN"}, 8); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(&rep); err != nil {
		t.Fatal(err)
	}
	want := []importer.RowError{{Line: 7, Reason: importer.ReasonDSTAmbiguous}, {Line: 8, Reason: importer.ReasonBadTime}}
	if rep.Rejected != 2 || !reflect.DeepEqual(rep.Errors, want) || rep.Emitted != 0 {
		t.Errorf("report = %+v", rep)
	}
}

func TestBadRecordAndPseudonym(t *testing.T) {
	s := NewSink(testSrc, testOpts(), func(evidence.Event) error { return nil })
	if err := s.Add(rec("INFO", "", "x"), 0); !errors.Is(err, ErrRecord) {
		t.Errorf("line 0: %v", err)
	}
	r := rec("INFO", "", "x")
	r.Rule = "guess"
	if err := s.Add(r, 1); !errors.Is(err, ErrRecord) {
		t.Errorf("bad rule: %v", err)
	}
	o := testOpts()
	o.Pseudonymize = func(string, string) string { return "" }
	if _, err := runSink(t, o, []Record{rec("INFO", "Synthetic.Api", "x")}); !errors.Is(err, ErrPseudonym) {
		t.Errorf("empty pseudonym: %v", err)
	}
	o.Pseudonymize = nil
	if _, err := runSink(t, o, []Record{rec("INFO", "System.Net.Http", "x")}); err != nil {
		t.Errorf("framework logger without pseudonymiser: %v", err)
	}
}

func TestWindow(t *testing.T) {
	o := testOpts()
	o.Window = importer.Window{From: t0.Add(10 * time.Second), To: t0.Add(60 * time.Second)}
	before := rec("WARN", "System", "a")
	inside := rec("WARN", "System", "b")
	inside.Time = t0.Add(20 * time.Second)
	slow := rec("INFO", catSerilogRequests, "HTTP GET /a responded 200 in 15000 ms")
	slow.Time = t0.Add(70 * time.Second) // ends after the window, starts inside
	after := rec("WARN", "System", "c")
	after.Time = t0.Add(90 * time.Second)
	res := mustRun(t, o, []Record{before, inside, slow, after})
	le := byKind(res.evs, KindLogEntry)
	if len(le) != 1 || le[0].Line != 2 {
		t.Errorf("log-entry = %+v", le)
	}
	if ar := byKind(res.evs, KindAppRequest); len(ar) != 1 {
		t.Errorf("app-request overlapping the window = %d", len(ar))
	}
	var total float64
	for _, e := range byKind(res.evs, KindLogRate) {
		total += *e.Value
	}
	if total != 1 {
		t.Errorf("log-rate total = %v, want 1 (only lines in the window)", total)
	}
}

func TestLimits(t *testing.T) {
	o := testOpts()
	o.Limits.MaxEvents = 2
	var recs []Record
	for i := 0; i < 3; i++ {
		r := rec("INFO", "System", "x")
		r.Time = t0.Add(time.Duration(i) * time.Minute)
		recs = append(recs, r)
	}
	_, err := runSink(t, o, recs)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "events" {
		t.Errorf("err = %v, want events LimitError", err)
	}

	boom := errors.New("emit failed")
	s := NewSink(testSrc, testOpts(), func(evidence.Event) error { return boom })
	if err := s.Add(rec("WARN", "System", "x"), 1); !errors.Is(err, boom) {
		t.Errorf("emit error = %v", err)
	}
}

func TestGapsPassLint(t *testing.T) {
	for _, g := range []string{GapDropped, GapEntryCap, GapRequestThreshold, GapThresholdUnreadable, GapUnknownLevel} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
		if strings.Contains(g, sentinel) {
			t.Error("gap holds input")
		}
	}
}
