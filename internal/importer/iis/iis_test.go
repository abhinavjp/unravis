package iis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const sentinel = "SENTINEL-7f3a"

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hostsLookup(observed string) (importer.HostHint, bool) {
	switch strings.ToLower(observed) {
	case "web-a", "web-a.example.test":
		return importer.HostHint{Label: "web-a", Role: "iis"}, true
	}
	return importer.HostHint{}, false
}

func baseOpts() importer.Options {
	return importer.Options{
		CaseID:      "abcdefgh234567abcdefgh2345",
		ImportNonce: "nonce-1",
		FileName:    "u_ex010203.log",
		Host:        &importer.HostHint{Label: "web-a", Role: "iis", Runtime: "iis-10"},
		Hosts:       hostsLookup,
		Pseudonymize: func(kind, v string) string {
			// pure, so concurrent parses agree
			switch v {
			case "alice":
				return kind + "-0001"
			case "bob":
				return kind + "-0002"
			}
			return kind + "-9999"
		},
	}
}

func parse(t testing.TB, imp importer.Importer, data []byte, opts importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), imp, bytes.NewReader(data), opts)
}

func mustParse(t testing.TB, imp importer.Importer, data []byte, opts importer.Options) ([]evidence.Event, importer.Report) {
	t.Helper()
	evs, rep, err := parse(t, imp, data, opts)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, e := range evs {
		if err := e.Validate(imp.AllowedAttrs()); err != nil {
			t.Fatalf("event line %d invalid: %v", e.Line, err)
		}
	}
	return evs, rep
}

// dump renders everything the importer produced except ObservedHosts (which
// by contract carries raw host names for the service to map).
func dump(evs []evidence.Event, rep importer.Report) string {
	rep.ObservedHosts = nil
	return fmt.Sprintf("%#v\n%#v\n%v|%v|%v|%v|%v|%v|%v|%v", evs, rep.Errors, rep.Importer, rep.Rows, rep.Emitted,
		rep.Rejected, rep.Quality, rep.FieldRules, rep.KnownGaps, rep.First)
}

func wantQuarantine(t *testing.T, err error, reason importer.Reason, column string) {
	t.Helper()
	var q *importer.QuarantineError
	if !errors.As(err, &q) || !errors.Is(err, importer.ErrQuarantine) {
		t.Fatalf("want quarantine %s, got %v", reason, err)
	}
	if q.Reason != reason || q.Column != column {
		t.Fatalf("quarantine = %s/%q, want %s/%q", q.Reason, q.Column, reason, column)
	}
}

func w3cFile(good int, extra ...string) []byte {
	var b strings.Builder
	b.WriteString("#Software: Microsoft Internet Information Services 10.0\n")
	b.WriteString("#Fields: date time s-computername cs-method cs-uri-stem sc-status time-taken\n")
	for i := 0; i < good; i++ {
		fmt.Fprintf(&b, "2001-02-03 04:05:%02d WEB-A GET /api/v1/orders/%d 200 %d\n", i%60, 1000+i, 10+i)
	}
	for _, e := range extra {
		b.WriteString(e + "\n")
	}
	return []byte(b.String())
}

func utf16le(b []byte) []byte {
	u := utf16.Encode([]rune(string(b)))
	out := []byte{0xFF, 0xFE}
	for _, c := range u {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

// ---------------------------------------------------------------- W3C

func TestW3CGood(t *testing.T) {
	evs, rep := mustParse(t, NewW3C(), fixture(t, "w3c_good.log"), baseOpts())
	if len(evs) != 6 || rep.Rows != 6 || rep.Emitted != 6 || rep.Rejected != 0 {
		t.Fatalf("counts: events %d rows %d emitted %d rejected %d", len(evs), rep.Rows, rep.Emitted, rep.Rejected)
	}
	e := evs[0]
	ts := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if e.Kind != "request" || e.Source != "iis-w3c" || e.Host != "web-a" || e.Line != 5 || e.TZRule != clock.RuleUTCField {
		t.Fatalf("event 0: %+v", e)
	}
	if e.DurationMs == nil || *e.DurationMs != 125 {
		t.Fatalf("duration: %v", e.DurationMs)
	}
	if w3cTimeIsEnd {
		if !e.End.Equal(ts) || !e.Start.Equal(ts.Add(-125*time.Millisecond)) {
			t.Fatalf("w3cTimeIsEnd: start %v end %v", e.Start, e.End)
		}
	} else if !e.Start.Equal(ts) || !e.End.Equal(ts.Add(125*time.Millisecond)) {
		t.Fatalf("start semantics: start %v end %v", e.Start, e.End)
	}
	want := map[string]string{"method": "GET", "stem": "/api/v1/orders/{id}", "status": "200", "substatus": "0",
		"win32": "0", "port": "443", "site": "W3SVC1", "user": "user-0001"}
	for k, v := range want {
		if e.Attrs[k] != v {
			t.Errorf("attr %s = %q, want %q", k, e.Attrs[k], v)
		}
	}
	if len(e.Attrs) != len(want) {
		t.Errorf("attrs %v", e.Attrs)
	}
	if got := evs[1].Attrs["stem"]; got != "/api/v1/items/{id}/lines" {
		t.Errorf("guid stem = %q", got)
	}
	if evs[1].Attrs["substatus"] != "13" || evs[1].Attrs["win32"] != "2" || evs[1].Attrs["user"] != "user-0002" {
		t.Errorf("event 1 attrs %v", evs[1].Attrs)
	}
	// time-taken "-" gives a nil duration and a point event
	if evs[2].DurationMs != nil || !evs[2].Start.Equal(evs[2].End) {
		t.Errorf("dash time-taken: %+v", evs[2])
	}
	if _, ok := evs[2].Attrs["user"]; ok {
		t.Errorf("dash username kept")
	}
	// re-declared #Fields with fewer columns and an unknown one
	if evs[4].Line != 10 || evs[4].Attrs["stem"] != "/api/v1/orders/{id}" || evs[4].Attrs["status"] != "201" || evs[4].Host != "web-a" {
		t.Errorf("redeclared event: %+v", evs[4])
	}
	if _, ok := evs[4].Attrs["x-custom"]; ok {
		t.Errorf("unknown field kept")
	}
	if rep.Quality.SourceVersion != "iis-10" || rep.Quality.TimezoneRule != clock.RuleUTCField {
		t.Errorf("quality %+v", rep.Quality)
	}
	if rep.FieldRules["date+time"] != clock.RuleUTCField {
		t.Errorf("field rules %v", rep.FieldRules)
	}
	// coverage starts at the earliest request start: row 2 ends 04:05:07 after 3000 ms
	if rep.Quality.CoverageFirst == nil || !rep.First.Equal(time.Date(2001, 2, 3, 4, 5, 4, 0, time.UTC)) || !rep.Last.Equal(time.Date(2001, 2, 3, 4, 6, 1, 0, time.UTC)) {
		t.Errorf("coverage %v %v", rep.First, rep.Last)
	}
	if len(rep.ObservedHosts) != 1 || rep.ObservedHosts[0] != "WEB-A" {
		t.Errorf("observed hosts %v", rep.ObservedHosts)
	}
	gaps := strings.Join(rep.KnownGaps, "\n")
	for _, g := range []string{"W3C time semantics unverified", "time outside the pipeline"} {
		if !strings.Contains(gaps, g) {
			t.Errorf("gap %q missing in %q", g, gaps)
		}
	}
	// IDs are stable per (case, importer, nonce, line)
	if e.EvidenceID != evidence.EventID(baseOpts().CaseID, "iis-w3c", "nonce-1", 5, 0) {
		t.Errorf("event id %s", e.EvidenceID)
	}
}

func TestW3CDropsSensitiveColumns(t *testing.T) {
	evs, rep := mustParse(t, NewW3C(), fixture(t, "w3c_good.log"), baseOpts())
	out := dump(evs, rep)
	for _, banned := range []string{sentinel, "192.0.2.", "Mozilla", "curl/", "session=", "ref.example.test", "alice", "bob", "WEB-A", "q="} {
		if strings.Contains(out, banned) {
			t.Errorf("output contains %q", banned)
		}
	}
	// without a pseudonymiser the username is dropped, never stored raw
	o := baseOpts()
	o.Pseudonymize = nil
	evs, _ = mustParse(t, NewW3C(), fixture(t, "w3c_good.log"), o)
	for _, e := range evs {
		if _, ok := e.Attrs["user"]; ok {
			t.Fatalf("user kept without pseudonymiser: %v", e.Attrs)
		}
	}
}

func TestW3CTimeIsEndGap(t *testing.T) {
	_, rep := mustParse(t, NewW3C(), w3cFile(3), baseOpts())
	want := gapW3CTimeEnd
	if !w3cTimeIsEnd {
		want = gapW3CTimeStart
	}
	if rep.KnownGaps[0] != want {
		t.Fatalf("gap = %q", rep.KnownGaps[0])
	}
}

func TestW3CNoFieldsQuarantines(t *testing.T) {
	for name, data := range map[string][]byte{
		"fixture":    fixture(t, "w3c_nofields.log"),
		"empty":      nil,
		"one byte":   []byte("x"),
		"comments":   []byte("#Software: Microsoft Internet Information Services 10.0\n#Version: 1.0\n"),
		"rows first": []byte("2001-02-03 04:05:06 GET /a 200\n#Fields: date time\n"),
	} {
		_, _, err := parse(t, NewW3C(), data, baseOpts())
		if name == "rows first" {
			wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "")
			continue
		}
		wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "")
	}
}

func TestW3CMissingRequiredColumn(t *testing.T) {
	data := []byte("#Fields: date cs-method\n2001-02-03 GET\n")
	_, _, err := parse(t, NewW3C(), data, baseOpts())
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "time")
}

func TestW3CDuplicateKeptColumn(t *testing.T) {
	data := []byte("#Fields: date time time cs-method\n2001-02-03 04:05:06 04:05:07 GET\n")
	_, _, err := parse(t, NewW3C(), data, baseOpts())
	wantQuarantine(t, err, importer.ReasonFieldCount, "time")
}

func TestW3CHugeHeaderQuarantines(t *testing.T) {
	data := []byte("#Fields: date time" + strings.Repeat(" x-col", 5000) + "\n")
	_, _, err := parse(t, NewW3C(), data, baseOpts())
	wantQuarantine(t, err, importer.ReasonFieldCount, "")
}

func TestW3CWrongFieldCountRejectsRow(t *testing.T) {
	data := w3cFile(40, "2001-02-03 04:05:59 WEB-A GET /short "+sentinel)
	evs, rep, err := parse(t, NewW3C(), data, baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 40 || rep.Rows != 41 || rep.Rejected != 1 {
		t.Fatalf("events %d rows %d rejected %d", len(evs), rep.Rows, rep.Rejected)
	}
	if len(rep.Errors) != 1 || rep.Errors[0].Reason != importer.ReasonFieldCount || rep.Errors[0].Line != 43 {
		t.Fatalf("errors %+v", rep.Errors)
	}
	if strings.Contains(dump(evs, rep), sentinel) {
		t.Fatal("sentinel leaked")
	}
}

func TestW3CRowRejections(t *testing.T) {
	cases := map[string]struct {
		row    string
		reason importer.Reason
	}{
		"bad time":        {"2001-13-03 04:05:06 WEB-A GET /a 200 5", importer.ReasonBadTime},
		"bad time text":   {"2001-02-03 xx:05:06 WEB-A GET /a 200 5", importer.ReasonBadTime},
		"bad status":      {"2001-02-03 04:05:06 WEB-A GET /a 20x 5", importer.ReasonBadNumber},
		"status range":    {"2001-02-03 04:05:06 WEB-A GET /a 99 5", importer.ReasonBadNumber},
		"bad time-taken":  {"2001-02-03 04:05:06 WEB-A GET /a 200 -5", importer.ReasonBadNumber},
		"huge time-taken": {"2001-02-03 04:05:06 WEB-A GET /a 200 99999999999", importer.ReasonBadNumber},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			evs, rep, err := parse(t, NewW3C(), w3cFile(40, c.row), baseOpts())
			if err != nil {
				t.Fatal(err)
			}
			if len(evs) != 40 || rep.Rejected != 1 || rep.Errors[0].Reason != c.reason {
				t.Fatalf("events %d rejected %d errors %+v", len(evs), rep.Rejected, rep.Errors)
			}
		})
	}
	// invalid UTF-8 rejects the row as an encoding error
	bad := append([]byte("2001-02-03 04:05:06 WEB-A GET /a\xff"), " 200 5"...)
	data := append(w3cFile(40), append(bad, '\n')...)
	_, rep, err := parse(t, NewW3C(), data, baseOpts())
	if err != nil || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonEncoding {
		t.Fatalf("encoding: %v %+v", err, rep)
	}
}

func TestW3CRejectFractionQuarantines(t *testing.T) {
	data := w3cFile(2, "short row")
	_, rep, err := parse(t, NewW3C(), data, baseOpts())
	wantQuarantine(t, err, importer.ReasonRejectFraction, "")
	if rep.Rejected != 1 || rep.Rows != 3 {
		t.Fatalf("report %+v", rep)
	}
}

func TestW3CHosts(t *testing.T) {
	// an observed name the host map does not know refuses the whole file
	data := w3cFile(3, "2001-02-03 04:05:59 WEB-Z GET /a 200 5")
	_, rep, err := parse(t, NewW3C(), data, baseOpts())
	if !errors.Is(err, importer.ErrUnknownHost) || err.Error() != "unknown host" {
		t.Fatalf("err %v", err)
	}
	found := false
	for _, h := range rep.ObservedHosts {
		found = found || h == "WEB-Z"
	}
	if !found {
		t.Fatalf("observed %v", rep.ObservedHosts)
	}
	// alias lookup
	evs, _ := mustParse(t, NewW3C(), []byte(strings.ReplaceAll(string(w3cFile(2)), "WEB-A", "web-a.example.test")), baseOpts())
	if evs[0].Host != "web-a" {
		t.Fatalf("host %q", evs[0].Host)
	}
	// no host column, no picked host
	nohost := []byte("#Fields: date time cs-method\n2001-02-03 04:05:06 GET\n")
	o := baseOpts()
	o.Host = nil
	if _, _, err := parse(t, NewW3C(), nohost, o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("err %v", err)
	}
	// no host column, picked host used
	evs, _ = mustParse(t, NewW3C(), nohost, baseOpts())
	if len(evs) != 1 || evs[0].Host != "web-a" {
		t.Fatalf("picked host: %+v", evs)
	}
	// the picked host is used when no lookup is wired
	o = baseOpts()
	o.Hosts = nil
	evs, _ = mustParse(t, NewW3C(), w3cFile(2), o)
	if evs[0].Host != "web-a" {
		t.Fatalf("host %q", evs[0].Host)
	}
}

func TestW3CDetect(t *testing.T) {
	w3c, he := NewW3C(), NewHTTPERR()
	good, herr := fixture(t, "w3c_good.log"), fixture(t, "httperr_good.log")
	if s := w3c.Detect(good, "u_ex010203.log"); s < 90 {
		t.Errorf("w3c good score %d", s)
	}
	if s := w3c.Detect(utf16le(good), "u_ex010203.log"); s < 90 {
		t.Errorf("w3c utf-16 score %d", s)
	}
	if s := he.Detect(good, "x.log"); s != 0 {
		t.Errorf("httperr on w3c: %d", s)
	}
	if s := he.Detect(herr, "httperr1.log"); s < 90 {
		t.Errorf("httperr score %d", s)
	}
	if s := w3c.Detect(herr, "httperr1.log"); s != 0 {
		t.Errorf("w3c on httperr: %d", s)
	}
	for _, name := range []string{"iis_format.log", "ncsa_format.log", "w3c_nofields.log"} {
		d := fixture(t, name)
		if s := w3c.Detect(d, name); s != 0 {
			t.Errorf("w3c on %s: %d", name, s)
		}
		if s := he.Detect(d, name); s != 0 {
			t.Errorf("httperr on %s: %d", name, s)
		}
	}
	for _, d := range [][]byte{nil, {'P', 'K', 3, 4}, []byte("hello")} {
		if w3c.Detect(d, "a.zip") != 0 || he.Detect(d, "a.zip") != 0 {
			t.Errorf("non-log detected: %q", d)
		}
	}
}

func TestNormaliseStem(t *testing.T) {
	long := "/" + strings.Repeat("a", 400)
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"/api/v1/orders/12345", "/api/v1/orders/{id}", true},
		{"/api/orders/123e4567-e89b-12d3-a456-426614174000/lines", "/api/orders/{id}/lines", true},
		{"/api/orders/{123e4567-e89b-12d3-a456-426614174000}", "/api/orders/{id}", true},
		{"/api/orders/123e4567e89b12d3a456426614174000", "/api/orders/{id}", true},
		{"/api/orders/v1x", "/api/orders/v1x", true},
		{"/a/b?q=" + sentinel, "/a/b", true},
		{"/a/b#frag", "/a/b", true},
		{"/", "/", true},
		{"-", "", false},
		{"", "", false},
		{"http://web-a.example.test/a", "", false},
		{"*", "", false},
	}
	for _, c := range cases {
		got, ok := normaliseStem(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("normaliseStem(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
	// a long junk segment is not a route word: it collapses to {id}
	got, ok := normaliseStem(long)
	if !ok || got != "/{id}" {
		t.Errorf("long stem %q", got)
	}
	got, _ = normaliseStem("/" + strings.Repeat("é", 200))
	if got != "/{id}" {
		t.Errorf("multibyte stem %q", got)
	}
	// many route segments stay within the length cap
	got, _ = normaliseStem(strings.Repeat("/abc", 200))
	if len(got) > 256 {
		t.Errorf("stem length %d", len(got))
	}
}

func TestAttrsWithinAllowlist(t *testing.T) {
	for _, imp := range []importer.Importer{NewW3C(), NewHTTPERR()} {
		if n := len(imp.AllowedAttrs()); n == 0 || n > 16 {
			t.Errorf("%s allowlist size %d", imp.Name(), n)
		}
		l := imp.DefaultLimits()
		if l.MaxLineBytes != 64<<10 || l.MaxFields != 4096 || l.MaxRejectFraction != 0.05 {
			t.Errorf("%s limits %+v", imp.Name(), l)
		}
	}
	if l := NewW3C().DefaultLimits(); l.MaxBytes != 2<<30 || l.MaxRows != 10_000_000 {
		t.Errorf("w3c caps %+v", l)
	}
	if l := NewHTTPERR().DefaultLimits(); l.MaxBytes != 64<<20 || l.MaxRows != 1_000_000 {
		t.Errorf("httperr caps %+v", l)
	}
}

// ---------------------------------------------------------------- HTTPERR

func httperrOpts() importer.Options {
	o := baseOpts()
	loc, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		panic(err)
	}
	o.Host = &importer.HostHint{Label: "web-a", Role: "iis", TZ: loc, Runtime: "iis-10"}
	return o
}

func TestHTTPERRGood(t *testing.T) {
	evs, rep := mustParse(t, NewHTTPERR(), fixture(t, "httperr_good.log"), httperrOpts())
	if len(evs) != 4 || rep.Rejected != 0 {
		t.Fatalf("events %d rejected %d", len(evs), rep.Rejected)
	}
	e := evs[0]
	// HTTPERR time is UTC whatever the host zone: 10:00:01 stays 10:00:01Z
	if e.Kind != "httperr" || e.Source != "iis-httperr" || e.TZRule != clock.RuleUTCField || e.Host != "web-a" ||
		!e.Start.Equal(time.Date(2001, 3, 17, 10, 0, 1, 0, time.UTC)) || !e.End.Equal(e.Start) || e.DurationMs != nil {
		t.Fatalf("event 0: %+v", e)
	}
	want := map[string]string{"reason": "QueueFull", "method": "GET", "stem": "/api/v1/orders/{id}",
		"status": "503", "siteid": "1", "queue": "DefaultAppPool"}
	for k, v := range want {
		if e.Attrs[k] != v {
			t.Errorf("attr %s = %q want %q", k, e.Attrs[k], v)
		}
	}
	if evs[1].Attrs["reason"] != "Connection_Dropped" || len(evs[1].Attrs) != 1 {
		t.Errorf("event 1 attrs %v", evs[1].Attrs)
	}
	if evs[2].Attrs["reason"] != "other" || evs[2].Attrs["stem"] != "/api/v1/items/{id}" {
		t.Errorf("event 2 attrs %v", evs[2].Attrs)
	}
	if evs[3].Attrs["reason"] != "Timer_ConnectionIdle" {
		t.Errorf("case-insensitive reason: %v", evs[3].Attrs)
	}
	out := dump(evs, rep)
	for _, banned := range []string{sentinel, "192.0.2.", "51000", "Made_Up_Reason", "HTTP/1.1"} {
		if strings.Contains(out, banned) {
			t.Errorf("output contains %q", banned)
		}
	}
	if strings.Contains(strings.Join(rep.KnownGaps, "\n"), "time zone") {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
	if rep.FieldRules["date+time"] != clock.RuleUTCField || rep.Quality.TimezoneRule != clock.RuleUTCField {
		t.Errorf("rules %v %+v", rep.FieldRules, rep.Quality)
	}
}

func httperrFile(rows ...string) []byte {
	var b strings.Builder
	b.WriteString("#Software: Microsoft HTTP API 2.0\n#Fields: date time c-ip cs-method cs-uri sc-status s-reason s-queuename\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "2001-03-17 10:00:%02d 192.0.2.50 GET /a 503 QueueFull pool-alpha\n", i)
	}
	for _, r := range rows {
		b.WriteString(r + "\n")
	}
	return []byte(b.String())
}

func TestHTTPERRBadTimeRows(t *testing.T) {
	// Times are UTC, so only an unparseable time is rejected (the DST cases
	// are covered by TestHTTPERRNoDSTRejects).
	cases := map[string]struct {
		row    string
		reason importer.Reason
	}{
		"bad": {"2001-03-32 02:30:00 192.0.2.50 GET /a 503 QueueFull pool-alpha", importer.ReasonBadTime},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			evs, rep, err := parse(t, NewHTTPERR(), httperrFile(c.row), httperrOpts())
			if err != nil {
				t.Fatal(err)
			}
			if len(evs) != 40 || rep.Rejected != 1 || rep.Errors[0].Reason != c.reason {
				t.Fatalf("events %d rejected %d errors %+v", len(evs), rep.Rejected, rep.Errors)
			}
		})
	}
}

func TestHTTPERRNeedsHost(t *testing.T) {
	o := httperrOpts()
	o.Host = nil
	_, _, err := parse(t, NewHTTPERR(), fixture(t, "httperr_good.log"), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("no host: %v", err)
	}
}

func TestHTTPERRNoFieldsAndBadRows(t *testing.T) {
	_, _, err := parse(t, NewHTTPERR(), []byte("2001-03-17 10:00:01 192.0.2.1 GET /a\n"), httperrOpts())
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "")
	_, _, err = parse(t, NewHTTPERR(), []byte("#Fields: cs-method\nGET\n"), httperrOpts())
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, "date")
	evs, rep, err := parse(t, NewHTTPERR(), httperrFile("2001-03-17 10:01:00 short "+sentinel,
		"2001-03-17 10:01:01 192.0.2.50 GET /a 9x QueueFull pool-alpha"), httperrOpts())
	if err != nil || len(evs) != 40 || rep.Rejected != 2 {
		t.Fatalf("%v events %d rejected %d", err, len(evs), rep.Rejected)
	}
	if rep.Errors[0].Reason != importer.ReasonFieldCount || rep.Errors[1].Reason != importer.ReasonBadNumber {
		t.Fatalf("errors %+v", rep.Errors)
	}
	if strings.Contains(dump(evs, rep), sentinel) {
		t.Fatal("sentinel leaked")
	}
}

// ---------------------------------------------------------------- WAS

func TestClassifyWAS(t *testing.T) {
	recycle := map[int]string{5074: "time-interval", 5075: "request-limit", 5076: "scheduled-time",
		5077: "virtual-memory", 5078: "isapi-unhealthy", 5079: "admin-requested", 5080: "config-change", 5117: "private-memory"}
	for id, reason := range recycle {
		kind, attrs, ok := ClassifyWAS("Microsoft-Windows-WAS", id)
		if !ok || kind != "apppool-recycle" || attrs["reason"] != reason || len(attrs) != 1 {
			t.Errorf("%d: %q %v %v", id, kind, attrs, ok)
		}
	}
	if k, a, ok := ClassifyWAS("microsoft-windows-was", 5074); !ok || k != "apppool-recycle" || a["reason"] != "time-interval" {
		t.Errorf("case-insensitive provider: %q %v %v", k, a, ok)
	}
	// 5074 is the time interval, never a CPU limit
	if _, a, _ := ClassifyWAS("Microsoft-Windows-WAS", 5074); strings.Contains(a["reason"], "cpu") {
		t.Errorf("5074 mapped to cpu")
	}
	if k, _, ok := ClassifyWAS("Microsoft-Windows-WAS", 5186); !ok || k != "apppool-idle-shutdown" {
		t.Errorf("5186: %q %v", k, ok)
	}
	for _, c := range []struct {
		p  string
		id int
	}{{"Microsoft-Windows-WAS", 5073}, {"Microsoft-Windows-WAS", 0}, {"Microsoft-Windows-WAS", -1},
		{"Some-Other-Provider", 5074}, {"", 5186}} {
		if k, a, ok := ClassifyWAS(c.p, c.id); ok || k != "" || a != nil {
			t.Errorf("%v classified: %q %v", c, k, a)
		}
	}
}

func TestRecycleGap(t *testing.T) {
	// 8.5 and later: the weak wording; earlier or unknown: the legacy wording.
	// No runtime returns "": a missing recycle event is never evidence of none.
	for _, rt := range []string{"iis-10", "iis-10.0", "IIS-11", " iis-10 ", "iis-8.5", "iis-9"} {
		g := RecycleGap(rt)
		if g != recycleGapText || !strings.Contains(g, "logEventOnRecycle") {
			t.Errorf("RecycleGap(%q) = %q", rt, g)
		}
	}
	for _, rt := range []string{"iis-8", "iis-7.5", "iis-8.0", "", "unknown", "sql-2019", "iis-", "iis-x", "iis-8.x"} {
		g := RecycleGap(rt)
		if g != recycleGapTextLegacy || !strings.Contains(g, "Time, Memory and PrivateMemory") {
			t.Errorf("RecycleGap(%q) = %q", rt, g)
		}
	}
}
