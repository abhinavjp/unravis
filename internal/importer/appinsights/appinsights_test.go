package appinsights

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

const sentinel = "SENTINEL-7f3a"

func read(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func opts() importer.Options {
	return importer.Options{
		CaseID: "abcdefghijklmnopqrstuvwxyz", ImportNonce: "nonce-1", FileName: "synthetic.csv",
		Hosts: func(o string) (importer.HostHint, bool) {
			if strings.EqualFold(o, "WEB-A") {
				return importer.HostHint{Label: "web-a"}, true
			}
			return importer.HostHint{}, false
		},
	}
}

func parse(data string, o importer.Options) ([]evidence.Event, importer.Report, error) {
	return importer.Collect(context.Background(), New(), strings.NewReader(data), o)
}

const hdr = "timestamp [UTC],itemType,name,duration,resultCode,success,itemCount,operation_Id,cloud_RoleInstance,type,data\n"

func goodRows(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "2001-02-03T04:05:%02dZ,request,GET /a,10,200,True,1,op-%d,WEB-A,,x\n", i%60, i)
	}
	return sb.String()
}

func TestConformance(t *testing.T) {
	importertest.Run(t, New(), importertest.Fixtures{Good: read(t, "good.csv"), Bad: read(t, "bad.csv")})
}

func TestGoodFixture(t *testing.T) {
	ev, rep, err := importer.Collect(context.Background(), New(), bytes.NewReader(read(t, "good.csv")), opts())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 7 || rep.Emitted != 7 || rep.Rejected != 0 || len(ev) != 7 {
		t.Fatalf("rows %d emitted %d rejected %d events %d", rep.Rows, rep.Emitted, rep.Rejected, len(ev))
	}
	e := ev[0]
	if e.Kind != "request" || e.Host != "web-a" || e.Source != Name || e.TZRule != clock.RuleUTCField || e.Line != 2 {
		t.Fatalf("event 0: %+v", e)
	}
	wantStart := time.Date(2001, 2, 3, 4, 5, 6, 123456700, time.UTC)
	if !e.Start.Equal(wantStart) || e.End.Sub(e.Start) != 250500*time.Microsecond {
		t.Fatalf("times: %v %v", e.Start, e.End)
	}
	if e.Attrs["name"] != "GET /api/orders/{id}" || e.Attrs["success"] != "true" || e.Attrs["resultCode"] != "200" || e.Attrs["operation_Id"] != "op-0001" {
		t.Fatalf("attrs: %v", e.Attrs)
	}
	if ev[1].Kind != "dependency" || ev[1].Attrs["dependencyType"] != "SQL" || ev[1].Attrs["success"] != "false" {
		t.Fatalf("dependency: %+v", ev[1])
	}
	if ev[2].Attrs["name"] != "GET /api/items/{id}" || *ev[2].Value != 5 {
		t.Fatalf("sampled request: %+v", ev[2])
	}
	if ev[3].DurationMs != nil || ev[3].Attrs["name"] != "HTTP GET /v1/ping" {
		t.Fatalf("no duration / query cut: %+v", ev[3])
	}
	if ev[4].Kind != "exception" {
		t.Fatalf("exception kind: %+v", ev[4])
	}
	if _, has := ev[4].Attrs["name"]; has {
		t.Fatal("exception name must be dropped")
	}
	if _, has := ev[4].Attrs["dependencyType"]; has {
		t.Fatal("dependencyType only for dependencies")
	}
	if ev[5].Attrs["name"] != "=SUM(1+1)" {
		t.Fatalf("formula cell must stay verbatim: %q", ev[5].Attrs["name"])
	}
	m := ev[6]
	if m.Kind != "other" || m.Attrs["resultCode"] != "other" || m.Attrs["success"] != "unknown" {
		t.Fatalf("unknown values: %+v", m)
	}
	if _, has := m.Attrs["operation_Id"]; has {
		t.Fatal("invalid operation id must be dropped")
	}
	if got := EstimatedCount(ev); got != 12 {
		t.Fatalf("sum(itemCount) = %d", got)
	}
	if !contains(rep.KnownGaps, GapSampled) || !contains(rep.KnownGaps, GapMissingRecord) || rep.Quality.Sampling != GapSampled {
		t.Fatalf("gaps/quality: %v %+v", rep.KnownGaps, rep.Quality)
	}
	if rep.Quality.CoverageFirst == nil || !rep.Quality.CoverageFirst.Equal(wantStart) || rep.Quality.TimezoneRule != clock.RuleUTCField {
		t.Fatalf("coverage: %+v", rep.Quality)
	}
	if len(rep.ObservedHosts) != 1 || rep.ObservedHosts[0] != "WEB-A" {
		t.Fatalf("observed hosts: %v", rep.ObservedHosts)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// Card negative: nothing on the drop list reaches an event, report or error.
func TestDropList(t *testing.T) {
	ev, rep, err := importer.Collect(context.Background(), New(), bytes.NewReader(read(t, "good.csv")), opts())
	if err != nil {
		t.Fatal(err)
	}
	out := fmt.Sprintf("%+v %+v", ev, rep)
	for _, banned := range []string{"DROPPED", "192.0.2.", "token=", "key=", "customDimensions", "web-a.example.test", "select "} {
		if strings.Contains(out, banned) {
			t.Errorf("output contains %q", banned)
		}
	}
	for _, e := range ev {
		for k := range e.Attrs {
			if !attrAllow[k] {
				t.Errorf("attr %q not allowlisted", k)
			}
		}
	}
}

// Card negative: a sentinel in `data` (and every other dropped column) never
// appears in output, including when the rest of the file is rejected.
func TestSentinelInData(t *testing.T) {
	file := "timestamp [UTC],itemType,name,duration,itemCount,data,customDimensions,client_IP,user_Id,url\n" +
		"2001-02-03T04:05:06Z,dependency,SQL: sql-a,5,1," + sentinel + "," + sentinel + "," + sentinel + "," + sentinel + ",https://a.example.test/?q=" + sentinel + "\n"
	ev, rep, err := parse(file, opts())
	if err != nil || len(ev) != 1 {
		t.Fatalf("err %v events %d", err, len(ev))
	}
	if out := fmt.Sprintf("%+v %+v", ev, rep); strings.Contains(out, sentinel) {
		t.Fatalf("sentinel leaked: %s", out)
	}
	// And in a rejected row and in a quarantined file's error text.
	_, rep, err = parse(hdr+"2001-02-03T04:05:06Z,request,GET /a,10,200,True,0,op,WEB-A,,"+sentinel+"\n", opts())
	if out := fmt.Sprintf("%+v %v", rep, err); strings.Contains(out, sentinel) {
		t.Fatalf("sentinel leaked: %s", out)
	}
	_, _, err = parse("timestamp,"+sentinel+"\n1,2\n", opts())
	if err == nil || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("quarantine error: %v", err)
	}
	_, _, err = parse("timestamp [UTC],"+sentinel+","+sentinel+"\n", opts())
	if err == nil || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("duplicate error: %v", err)
	}
}

func TestHeaderMustBeDocumentedQuery(t *testing.T) {
	cases := map[string]struct {
		header string
		reason importer.Reason
	}{
		"locale timestamp":     {"timestamp,itemType,name,duration\n", importer.ReasonTZUnlabelled},
		"local zone timestamp": {"timestamp [Europe/Paris],itemType,name,duration\n", importer.ReasonTZUnlabelled},
		"no timestamp":         {"itemType,name,duration\n", importer.ReasonUnsupportedFormat},
		"empty header cells":   {",,\n", importer.ReasonUnsupportedFormat},
	}
	for name, c := range cases {
		_, _, err := parse(c.header+"x,y,z,1\n", opts())
		var qe *importer.QuarantineError
		if !errors.Is(err, importer.ErrQuarantine) || !errors.As(err, &qe) || qe.Reason != c.reason {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.Contains(err.Error(), "use the documented query") {
			t.Errorf("%s: message lacks guidance: %v", name, err)
		}
	}
}

func TestDuplicateColumns(t *testing.T) {
	for _, h := range []string{
		"timestamp [UTC],itemType,itemType\n",
		"timestamp [UTC],itemCount,ITEMCOUNT\n",
		"timestamp [UTC],timestamp [UTC]\n",
		"timestamp [UTC],mystery, mystery\n",
	} {
		_, _, err := parse(h+"2001-02-03T04:05:06Z,1,1\n", opts())
		var qe *importer.QuarantineError
		if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat {
			t.Errorf("%q: %v", h, err)
		}
	}
}

func TestItemCountZeroOrLessRejectsRow(t *testing.T) {
	for _, bad := range []string{"0", "-3", "2.5", "", "abc", "1e3"} {
		file := hdr + goodRows(60) + "2001-02-03T04:06:00Z,request,GET /a,10,200,True," + bad + ",op,WEB-A,,x\n"
		ev, rep, err := parse(file, opts())
		if err != nil {
			t.Fatalf("itemCount %q: %v", bad, err)
		}
		if rep.Rejected != 1 || len(ev) != 60 || len(rep.Errors) != 1 ||
			rep.Errors[0].Reason != importer.ReasonBadNumber || rep.Errors[0].Line != 62 {
			t.Fatalf("itemCount %q: rejected %d events %d errors %v", bad, rep.Rejected, len(ev), rep.Errors)
		}
	}
}

func TestMissingItemCountColumn(t *testing.T) {
	ev, rep, err := parse("timestamp [UTC],itemType\n2001-02-03T04:05:06Z,request\n", opts())
	if err != nil || len(ev) != 1 || *ev[0].Value != 1 || !contains(rep.KnownGaps, GapNoItemCount) || !contains(rep.KnownGaps, GapNoHost) {
		t.Fatalf("err %v ev %+v gaps %v", err, ev, rep.KnownGaps)
	}
	if ev[0].Host != "" {
		t.Fatalf("host: %q", ev[0].Host)
	}
}

func TestTimestampStrictness(t *testing.T) {
	good := []string{"2001-02-03T04:05:06Z", "2001-02-03T04:05:06.1234567Z", "2001-02-03T04:05:06+00:00"}
	bad := []string{
		"2001-02-03T04:05:06+02:00", "2001-02-03 04:05:06", "2/3/2001 4:05:06 AM", "03/02/2001 04:05:06",
		"2001-02-03T04:05:06", "2001-02-03", "1960-01-01T00:00:00Z", "", "-",
	}
	for _, g := range good {
		if _, ok := parseUTC(g); !ok {
			t.Errorf("rejected %q", g)
		}
	}
	for _, b := range bad {
		if _, ok := parseUTC(b); ok {
			t.Errorf("accepted %q", b)
		}
	}
}

// A file whose rows carry locale-formatted times has the right header but the
// wrong query: it is quarantined with the same guidance, not partly imported.
func TestLocaleTimesQuarantine(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(hdr)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&sb, "2/3/2001 4:05:%02d AM,request,GET /a,10,200,True,1,op,WEB-A,,x\n", i)
	}
	_, _, err := parse(sb.String(), opts())
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonRejectFraction || !strings.Contains(err.Error(), "use the documented query") {
		t.Fatalf("%v", err)
	}
	// Mostly non-time rejects do not claim the query is wrong.
	_, _, err = parse(hdr+strings.Repeat("2001-02-03T04:05:06Z,request,GET /a,10,200,True,0,op,WEB-A,,x\n", 20), opts())
	if !errors.Is(err, importer.ErrQuarantine) || strings.Contains(err.Error(), "documented query") {
		t.Fatalf("%v", err)
	}
}

func TestEmptyResult(t *testing.T) {
	ev, rep, err := parse(hdr, opts())
	if err != nil || len(ev) != 0 || rep.Rows != 0 {
		t.Fatalf("err %v events %d", err, len(ev))
	}
	want := evidence.NotObservedGap("empty result; possibly no access")
	if !contains(rep.KnownGaps, want) {
		t.Fatalf("gaps %v want %q", rep.KnownGaps, want)
	}
	if Absence(0) != want {
		t.Fatalf("Absence(0) = %q", Absence(0))
	}
	if a := Absence(5); !strings.Contains(a, "not counter-evidence") || strings.Contains(a, "empty result") {
		t.Fatalf("Absence(5) = %q", a)
	}
	for _, s := range append(rep.KnownGaps, Absence(0), Absence(3)) {
		for _, w := range []string{"caused", "because", "waiting on", "blocked by", "due to", "proven"} {
			if strings.Contains(strings.ToLower(s), w) {
				t.Errorf("causal wording %q in %q", w, s)
			}
		}
	}
}

func TestHosts(t *testing.T) {
	file := hdr + "2001-02-03T04:05:06Z,request,GET /a,10,200,True,1,op,db-9,,x\n"
	if _, _, err := parse(file, opts()); !errors.Is(err, importer.ErrUnknownHost) || strings.Contains(err.Error(), "db-9") {
		t.Fatalf("unknown host: %v", err)
	}
	// No map: the host picked for the file labels every row; raw names are
	// reported for the service to map or refuse, never stored on events.
	o := opts()
	o.Hosts = nil
	o.Host = &importer.HostHint{Label: "web-a"}
	ev, rep, err := parse(file, o)
	if err != nil || ev[0].Host != "web-a" || len(rep.ObservedHosts) != 1 {
		t.Fatalf("picked host: %v %+v %v", err, ev, rep.ObservedHosts)
	}
	for _, e := range ev {
		if strings.Contains(fmt.Sprintf("%+v", e), "db-9") {
			t.Fatal("raw host name stored on event")
		}
	}
}

func TestTimestampAsStartAndDuration(t *testing.T) {
	ev, _, err := parse(hdr+"2001-02-03T04:05:06Z,request,GET /a,1500,200,True,1,op,WEB-A,,x\n", opts())
	if err != nil {
		t.Fatal(err)
	}
	if !timestampIsStart || ev[0].End.Sub(ev[0].Start) != 1500*time.Millisecond || *ev[0].DurationMs != 1500 {
		t.Fatalf("%+v", ev[0])
	}
	for _, bad := range []string{"-1", "NaN", "Inf", "1,5", "abc", "99999999999999"} {
		_, rep, _ := parse(hdr+goodRows(60)+"2001-02-03T04:06:00Z,request,GET /a,"+bad+",200,True,1,op,WEB-A,,x\n", opts())
		if rep.Rejected != 1 {
			t.Errorf("duration %q: rejected %d", bad, rep.Rejected)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	for in, want := range map[string]string{
		"GET /orders/123":                              "GET /orders/{id}",
		"GET /orders/123/items/9":                      "GET /orders/{id}/items/{id}",
		"POST /x/3fa85f64-5717-4562-b3fc-2c963f66afa6": "POST /x/{id}",
		"POST /x/3fa85f6457174562b3fc2c963f66afa6":     "POST /x/{id}",
		"GET /search?q=jo@example.test":                "GET /search",
		"GET /v1":                                      "GET /v1",
		"=cmd|' /C calc'!A0":                           "=cmd|' /C calc'!A0",
		"?only":                                        "",
	} {
		if got := sanitizeName(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	long := "GET /" + strings.Repeat("é", 300)
	if got := sanitizeName(long); len(got) > 256 || !strings.HasPrefix(got, "GET /é") {
		t.Errorf("long name: %d bytes", len(got))
	}
}

func TestDetect(t *testing.T) {
	im := New()
	if s := im.Detect(read(t, "good.csv"), "x.csv"); s < 80 {
		t.Errorf("good: %d", s)
	}
	if s := im.Detect([]byte("timestamp [UTC],foo\n"), ""); s == 0 || s >= 80 {
		t.Errorf("bare header: %d", s)
	}
	if s := im.Detect([]byte("timestamp,itemType,operation_Id\n"), ""); s == 0 || s >= 50 {
		t.Errorf("locale header should be claimable but low: %d", s)
	}
	for _, other := range []string{
		"timestamp_utc,event_name,duration\n", "date time cs-method\n", "\"(PDH-CSV 4.0)\",\"x\"\n", "", "\x00\x01\x02",
		"PK\x03\x04", "{\"t\":{\"$date\":\"2001-02-03T04:05:06Z\"}}\n",
	} {
		if s := im.Detect([]byte(other), ""); s != 0 {
			t.Errorf("%q: %d", other, s)
		}
	}
	u16 := []byte{0xFF, 0xFE}
	for _, r := range "timestamp [UTC],itemType\n" {
		u16 = append(u16, byte(r), 0)
	}
	if s := im.Detect(u16, ""); s < 80 {
		t.Errorf("utf-16: %d", s)
	}
}

func TestCaps(t *testing.T) {
	l := New().DefaultLimits()
	if l.MaxBytes != 512<<20 || l.MaxRows != 5_000_000 || l.MaxLineBytes != 64<<10 {
		t.Fatalf("caps: %+v", l)
	}
	o := opts()
	o.Limits = importer.Limits{MaxFields: 3}
	_, _, err := parse(hdr, o)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "fields" {
		t.Fatalf("fields: %v", err)
	}
}

func TestLineGuard(t *testing.T) {
	o := opts()
	o.Limits = importer.Limits{MaxLineBytes: 200}
	long := strings.Repeat("a", 500)
	_, _, err := parse(hdr+"2001-02-03T04:05:06Z,request,"+long+",10,200,True,1,op,WEB-A,,x\n", o)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "line" {
		t.Fatalf("long line: %v", err)
	}
	// A quoted field that spans lines is still one bounded record.
	_, _, err = parse(hdr+"2001-02-03T04:05:06Z,request,\""+strings.Repeat("a\n", 300)+"\",10,200,True,1,op,WEB-A,,x\n", o)
	if !errors.As(err, &le) || le.Limit != "line" {
		t.Fatalf("multi-line record: %v", err)
	}
	// A stray quote mid-field must not turn into an unbounded record.
	file := hdr + "2001-02-03T04:05:06Z,request,GET /a\"b,10,200,True,1,op,WEB-A,,x\n" + goodRows(40)
	if _, _, err := parse(file, o); err != nil {
		t.Fatalf("bare quote: %v", err)
	}
}

func TestTooManyHosts(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(hdr)
	for i := 0; i <= maxObservedHosts; i++ {
		fmt.Fprintf(&sb, "2001-02-03T04:05:06Z,request,GET /a,1,200,True,1,op,host-%d,,x\n", i)
	}
	o := opts()
	o.Hosts = func(string) (importer.HostHint, bool) { return importer.HostHint{Label: "web-a"}, true }
	_, _, err := parse(sb.String(), o)
	if !errors.Is(err, importer.ErrLimit) {
		t.Fatalf("%v", err)
	}
}

func TestNoNetwork(t *testing.T) {
	old := http.DefaultTransport
	http.DefaultTransport = failingTransport{t}
	defer func() { http.DefaultTransport = old }()
	if _, _, err := parse(hdr+goodRows(5), opts()); err != nil {
		t.Fatal(err)
	}
}

type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Error("importer made a network call")
	return nil, io.ErrClosedPipe
}

func TestEmitErrorStops(t *testing.T) {
	boom := errors.New("boom")
	n := 0
	_, err := New().Parse(context.Background(), strings.NewReader(hdr+goodRows(10)), opts(), func(evidence.Event) error {
		n++
		return boom
	})
	if !errors.Is(err, boom) || n != 1 {
		t.Fatalf("err %v n %d", err, n)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(read(f, "good.csv"))
	f.Add(read(f, "bad.csv"))
	f.Add([]byte(hdr))
	f.Add([]byte("\xef\xbb\xbf" + hdr + goodRows(3)))
	f.Add([]byte("timestamp [UTC],\"a\nb\"\n\"x"))
	f.Fuzz(func(t *testing.T, data []byte) {
		o := opts()
		o.Limits = importer.Limits{MaxBytes: 1 << 20, MaxRows: 10_000, MaxLineBytes: 4096}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ev, _, err := importer.Collect(ctx, New(), bytes.NewReader(data), o)
		for _, e := range ev {
			if verr := e.Validate(New().AllowedAttrs()); verr != nil {
				t.Fatalf("invalid event: %v", verr)
			}
		}
		if err != nil && bytes.Contains(data, []byte("QQ-secret-QQ")) && strings.Contains(err.Error(), "QQ-secret-QQ") {
			t.Fatal("error text carries content")
		}
	})
}
