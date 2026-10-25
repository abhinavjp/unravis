package accesslog

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

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func baseOpts(supplied map[string]string) importer.Options {
	return importer.Options{
		CaseID:       "abcdefgh234567abcdefgh2345",
		ImportNonce:  "nonce-1",
		FileName:     "access.log",
		Host:         &importer.HostHint{Label: "web-a", Role: "web", Runtime: "synthetic-1.0"},
		Supplied:     supplied,
		Pseudonymize: func(kind, v string) string { return kind + "-0001" },
	}
}

var apacheUnit = map[string]string{SuppliedDurationUnit: "microseconds"}

func parse(t testing.TB, imp importer.Importer, data []byte, opts importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return importer.Collect(ctx, imp, bytes.NewReader(data), opts)
}

// lines builds a file of n good nginx lines then extra.
func nginxFile(n int, extra ...string) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "192.0.2.10 - - [03/Feb/2001:04:05:%02d +0000] \"GET /api/v1/orders/%d HTTP/1.1\" 200 100 \"-\" \"synthetic-agent/1.0\" 0.100 0.090\n", i%60, 1000+i)
	}
	for _, e := range extra {
		b.WriteString(e + "\n")
	}
	return []byte(b.String())
}

func apacheFile(n int, extra ...string) []byte {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "192.0.2.10 - - [03/Feb/2001:04:05:%02d +0000] \"GET /api/v1/orders/%d HTTP/1.1\" 200 100 \"-\" \"synthetic-agent/1.0\" 100000\n", i%60, 1000+i)
	}
	for _, e := range extra {
		b.WriteString(e + "\n")
	}
	return []byte(b.String())
}

func nginxFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:    fixture(t, "nginx_good.log"),
		Bad:     nginxFile(40, `192.0.2.10 - - [03/Feb/2001:04:05:59 +0000] "GET /a HTTP/1.1" `+importertest.Sentinel+` 100 "-" "x" 0.1`),
		Options: baseOpts(nil),
		// every attribute is a typed token, number, normalised route or pseudonym
		NoFreeText: true,
		// a plain word in the request path is kept as a route word by design
		KeepsCellValues: true,
	}
}

func apacheFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:            fixture(t, "apache_good.log"),
		Bad:             apacheFile(40, `192.0.2.10 - - [03/Feb/2001:04:05:59 +0000] "GET /a HTTP/1.1" `+importertest.Sentinel+` 100 "-" "x" 1000`),
		Options:         baseOpts(apacheUnit),
		NoFreeText:      true,
		KeepsCellValues: true,
	}
}

func TestNginxConformance(t *testing.T)  { importertest.Run(t, NewNginx(), nginxFixtures(t)) }
func TestApacheConformance(t *testing.T) { importertest.Run(t, NewApache(), apacheFixtures(t)) }

func FuzzNginxParse(f *testing.F)  { importertest.FuzzParse(f, NewNginx(), nginxFixtures(f)) }
func FuzzApacheParse(f *testing.F) { importertest.FuzzParse(f, NewApache(), apacheFixtures(f)) }

func hasGap(rep importer.Report, g string) bool {
	for _, k := range rep.KnownGaps {
		if k == g {
			return true
		}
	}
	return false
}

func quarantineReason(err error) importer.Reason {
	var q *importer.QuarantineError
	if errors.As(err, &q) {
		return q.Reason
	}
	return ""
}

func TestNginxGoodFixture(t *testing.T) {
	evs, rep, err := parse(t, NewNginx(), fixture(t, "nginx_good.log"), baseOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Importer != NameNginx || rep.Rows != 8 || rep.Rejected != 0 || len(evs) != 8 {
		t.Fatalf("importer %q rows %d rejected %d events %d", rep.Importer, rep.Rows, rep.Rejected, len(evs))
	}
	if rep.Quality.TimezoneRule != clock.RuleOffsetInField || rep.FieldRules["time"] != clock.RuleOffsetInField {
		t.Errorf("timezone rule %q", rep.Quality.TimezoneRule)
	}
	if rep.Quality.SourceVersion != "synthetic-1.0" {
		t.Errorf("source version %q", rep.Quality.SourceVersion)
	}
	e := evs[0]
	// the nginx time is the request end: 0.125 s before 04:05:06Z
	wantEnd := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if !e.End.Equal(wantEnd) || !e.Start.Equal(wantEnd.Add(-125*time.Millisecond)) {
		t.Errorf("start %v end %v", e.Start, e.End)
	}
	if e.DurationMs == nil || *e.DurationMs != 125 {
		t.Errorf("duration %v", e.DurationMs)
	}
	if e.Kind != "request" || e.Host != "web-a" || e.Source != NameNginx || e.TZRule != clock.RuleOffsetInField {
		t.Errorf("event %+v", e)
	}
	if e.Attrs["method"] != "GET" || e.Attrs["stem"] != "/api/v1/orders/{id}" || e.Attrs["status"] != "200" || e.Attrs["bytes-out"] != "1200" {
		t.Errorf("attrs %v", e.Attrs)
	}
	if _, ok := e.Attrs["user"]; ok {
		t.Errorf("a '-' user was stored: %v", e.Attrs)
	}
	if evs[1].Attrs["user"] != "user-0001" {
		t.Errorf("user not pseudonymized: %v", evs[1].Attrs)
	}
	// the +1100 line: 15:05:08 +1100 is 04:05:08Z; the upstream "-" is accepted
	if !evs[2].End.Equal(time.Date(2001, 2, 3, 4, 5, 8, 0, time.UTC)) {
		t.Errorf("+1100 line end %v", evs[2].End)
	}
	// the -0700 line: 21:05:10 on the 2nd is 04:05:10Z on the 3rd
	if !evs[4].End.Equal(time.Date(2001, 2, 3, 4, 5, 10, 0, time.UTC)) {
		t.Errorf("-0700 line end %v", evs[4].End)
	}
	if !hasGap(rep, gapNginxTimeEnd) || !hasGap(rep, gapDelivery) || !hasGap(rep, gapResolution) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
	for _, g := range rep.KnownGaps {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("gap %q: %v", g, err)
		}
	}
}

func TestApacheGoodFixture(t *testing.T) {
	evs, rep, err := parse(t, NewApache(), fixture(t, "apache_good.log"), baseOpts(apacheUnit))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 8 || rep.Rejected != 0 || len(evs) != 8 {
		t.Fatalf("rows %d rejected %d events %d", rep.Rows, rep.Rejected, len(evs))
	}
	e := evs[0]
	// the Apache time is the request start; %D 125000 us is 125 ms
	wantStart := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if !e.Start.Equal(wantStart) || !e.End.Equal(wantStart.Add(125*time.Millisecond)) {
		t.Errorf("start %v end %v", e.Start, e.End)
	}
	if e.DurationMs == nil || *e.DurationMs != 125 {
		t.Errorf("duration %v", e.DurationMs)
	}
	if _, ok := evs[2].Attrs["bytes-out"]; ok {
		t.Errorf("a '-' size was stored: %v", evs[2].Attrs)
	}
	if !hasGap(rep, gapApacheTimeStart) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
}

func TestApacheDurationUnit(t *testing.T) {
	data := fixture(t, "apache_good.log")
	// seconds: %T 125 is 125000 ms
	evs, _, err := parse(t, NewApache(), apacheFile(2), baseOpts(map[string]string{SuppliedDurationUnit: "seconds"}))
	if err != nil {
		t.Fatal(err)
	}
	if *evs[0].DurationMs != 100000000 {
		t.Errorf("seconds: %v ms", *evs[0].DurationMs)
	}
	// no unit stated: the field is skipped, never guessed, and a gap says so
	evs, rep, err := parse(t, NewApache(), data, baseOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.DurationMs != nil || !e.Start.Equal(e.End) {
			t.Fatalf("duration read without a unit: %+v", e)
		}
	}
	if !hasGap(rep, gapUnitMissing) || hasGap(rep, gapDelivery) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
	if rep.Rejected != 0 {
		t.Errorf("rejected %d", rep.Rejected)
	}
}

func TestCommonFormat(t *testing.T) {
	for _, imp := range []importer.Importer{NewNginx(), NewApache()} {
		evs, rep, err := parse(t, imp, fixture(t, "common_good.log"), baseOpts(apacheUnit))
		if err != nil {
			t.Fatalf("%s: %v", imp.Name(), err)
		}
		if len(evs) != 3 || rep.Rejected != 0 {
			t.Fatalf("%s: events %d rejected %d", imp.Name(), len(evs), rep.Rejected)
		}
		for _, e := range evs {
			if e.DurationMs != nil || !e.Start.Equal(e.End) {
				t.Errorf("%s: a line without a duration has one: %+v", imp.Name(), e)
			}
		}
		if !hasGap(rep, gapNoDuration) {
			t.Errorf("%s: gaps %v", imp.Name(), rep.KnownGaps)
		}
	}
}

func TestTimeZoneNormalisedAndMalformedRefused(t *testing.T) {
	for in, want := range map[string]time.Time{
		"10/Oct/2000:13:55:36 -0700": time.Date(2000, 10, 10, 20, 55, 36, 0, time.UTC),
		"10/Oct/2000:13:55:36 +0530": time.Date(2000, 10, 10, 8, 25, 36, 0, time.UTC),
		"10/Oct/2000:13:55:36 +0000": time.Date(2000, 10, 10, 13, 55, 36, 0, time.UTC),
	} {
		got, ok := parseTime(in)
		if !ok || !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	for _, in := range []string{
		"", "10/Oct/2000:13:55:36", "10/Oct/2000:13:55:36 0700", "10/Oct/2000:13:55:36 -07",
		"10/Oct/2000:13:55:36 UTC", "10/Oct/2000:13:55:36 +1500", "10/Oct/2000:13:55:36 +0560",
		"10/Oct/2000:13:55:36 +1430", "10/Xxx/2000:13:55:36 +0000", "10/Oct/2000:25:55:36 +0000",
		"32/Oct/2000:13:55:36 +0000", "10/Oct/1980:13:55:36 +0000", "10/Oct/2200:13:55:36 +0000",
		"2000-10-10 13:55:36 +0000", "10/Oct/2000:13:55:36 +00:00",
	} {
		if _, ok := parseTime(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
	// a row with a malformed time is rejected as bad-time, not read as UTC
	data := nginxFile(40, `192.0.2.10 - - [03/Feb/2001:04:05:59] "GET /a HTTP/1.1" 200 1 "-" "x" 0.1`,
		`192.0.2.10 - - [03/Feb/2001:04:06:00 +2500] "GET /a HTTP/1.1" 200 1 "-" "x" 0.1`)
	_, rep, err := parse(t, NewNginx(), data, baseOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 2 {
		t.Fatalf("rejected %d, want 2", rep.Rejected)
	}
	for _, e := range rep.Errors {
		if e.Reason != importer.ReasonBadTime {
			t.Errorf("line %d reason %q", e.Line, e.Reason)
		}
	}
}

func TestCustomLogFormatRefused(t *testing.T) {
	for _, imp := range []importer.Importer{NewNginx(), NewApache()} {
		_, _, err := parse(t, imp, fixture(t, "customformat.log"), baseOpts(apacheUnit))
		if !errors.Is(err, importer.ErrQuarantine) || quarantineReason(err) != importer.ReasonUnsupportedFormat {
			t.Errorf("%s: err %v, want an unsupported-format quarantine", imp.Name(), err)
		}
		// a short file of the same shape is refused at its end
		_, _, err = parse(t, imp, []byte("192.0.2.10 [03/Feb/2001:04:05:06 +0000] GET /a 200 0.1\n"), baseOpts(apacheUnit))
		if quarantineReason(err) != importer.ReasonUnsupportedFormat {
			t.Errorf("%s: short custom file: %v", imp.Name(), err)
		}
	}
	// the other flavour's trailing field is not this importer's format
	if _, _, err := parse(t, NewNginx(), apacheFile(30), baseOpts(nil)); err == nil {
		t.Error("nginx read a file whose every line ends in a whole number")
	}
	if _, _, err := parse(t, NewApache(), nginxFile(30), baseOpts(apacheUnit)); err == nil {
		t.Error("apache read a file whose every line ends in decimal seconds")
	}
	// an extra field after the user agent (for example an X-Forwarded-For) is
	// not guessed at either
	extra := strings.ReplaceAll(string(nginxFile(30)), `0.100 0.090`, `0.100 0.090 "198.51.100.9"`)
	if _, _, err := parse(t, NewNginx(), []byte(extra), baseOpts(nil)); quarantineReason(err) != importer.ReasonUnsupportedFormat {
		t.Errorf("extra field: %v", err)
	}
}

func TestMissingHost(t *testing.T) {
	opts := baseOpts(nil)
	opts.Host = nil
	if _, _, err := parse(t, NewNginx(), nginxFile(3), opts); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("err %v", err)
	}
}

// The client address, ident, referrer, user agent and query values never
// reach an event, an attribute or the report. The user is only a pseudonym.
func TestPrivacySentinel(t *testing.T) {
	s := importertest.Sentinel
	for _, tc := range []struct {
		imp  importer.Importer
		line string
		opts importer.Options
	}{
		{NewNginx(), s + `-host ` + s + `-ident ` + s + `-user [03/Feb/2001:04:05:06 +0000] "GET /api/v1/orders/12345?token=` + s + `&x=1#` + s + ` HTTP/1.1" 200 12 "https://x.example.test/?q=` + s + `" "Agent ` + s + `" 0.100 0.090`, baseOpts(nil)},
		{NewApache(), s + `-host ` + s + `-ident ` + s + `-user [03/Feb/2001:04:05:06 +0000] "GET /api/v1/orders/12345?token=` + s + ` HTTP/1.1" 200 12 "https://x.example.test/?q=` + s + `" "Agent ` + s + `" 100000`, baseOpts(apacheUnit)},
	} {
		evs, rep, err := parse(t, tc.imp, []byte(tc.line+"\n"+string(nginxFile(0))+"\n"), tc.opts)
		if err != nil {
			t.Fatalf("%s: %v", tc.imp.Name(), err)
		}
		if len(evs) != 1 {
			t.Fatalf("%s: events %d", tc.imp.Name(), len(evs))
		}
		var all strings.Builder
		fmt.Fprintf(&all, "%+v %+v", evs, rep)
		if strings.Contains(all.String(), s) {
			t.Errorf("%s: sentinel stored: %s", tc.imp.Name(), all.String())
		}
		if evs[0].Attrs["stem"] != "/api/v1/orders/{id}" || evs[0].Attrs["user"] != "user-0001" {
			t.Errorf("%s: attrs %v", tc.imp.Name(), evs[0].Attrs)
		}
	}
	// a rejected row's text is not echoed either
	_, rep, err := parse(t, NewNginx(), nginxFile(40, `192.0.2.10 - - [03/Feb/2001:04:05:59 +0000] "GET /a HTTP/1.1" 99`+s+` 100 "-" "x" 0.1`), baseOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", rep), s) {
		t.Error("sentinel in the report")
	}
}

func TestOddRequestLines(t *testing.T) {
	data := nginxFile(40,
		`192.0.2.10 - - [03/Feb/2001:04:06:00 +0000] "-" 408 0 "-" "-" 0.100 -`,
		`192.0.2.10 - - [03/Feb/2001:04:06:01 +0000] "\x16\x03\x01" 400 150 "-" "-" 0.000 -`,
		`192.0.2.10 - - [03/Feb/2001:04:06:02 +0000] "GET /a%20b/../c HTTP/1.1" 200 1 "-" "-" 0.001 0.001`)
	evs, rep, err := parse(t, NewNginx(), data, baseOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 0 || len(evs) != 43 {
		t.Fatalf("rejected %d events %d", rep.Rejected, len(evs))
	}
	if _, ok := evs[40].Attrs["method"]; ok || evs[40].Attrs["status"] != "408" {
		t.Errorf("attrs %v", evs[40].Attrs)
	}
}

func TestBadValuesRejected(t *testing.T) {
	data := nginxFile(200,
		`192.0.2.10 - - [03/Feb/2001:04:06:00 +0000] "GET /a HTTP/1.1" 99 1 "-" "-" 0.1`,
		`192.0.2.10 - - [03/Feb/2001:04:06:01 +0000] "GET /a HTTP/1.1" 600 1 "-" "-" 0.1`,
		`192.0.2.10 - - [03/Feb/2001:04:06:02 +0000] "GET /a HTTP/1.1" 200 99999999999 "-" "-" 0.1`,
		`192.0.2.10 - - [03/Feb/2001:04:06:03 +0000] "GET /a HTTP/1.1" 200 1 "-" "-" 99999999.000`)
	_, rep, err := parse(t, NewNginx(), data, baseOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 4 {
		t.Logf("errors: %+v", rep.Errors)
	}
	if rep.Rejected < 4 {
		t.Errorf("rejected %d, want 4", rep.Rejected)
	}
}

func TestDetectMargins(t *testing.T) {
	nginxHead, apacheHead, commonHead := fixture(t, "nginx_good.log"), fixture(t, "apache_good.log"), fixture(t, "common_good.log")
	n, a := NewNginx(), NewApache()
	if s, o := n.Detect(nginxHead, ""), a.Detect(nginxHead, ""); s < 85 || o != 0 {
		t.Errorf("nginx sample: nginx %d apache %d", s, o)
	}
	if s, o := a.Detect(apacheHead, ""), n.Detect(apacheHead, ""); s < 85 || o != 0 {
		t.Errorf("apache sample: apache %d nginx %d", s, o)
	}
	// plain combined and common are one format: a deliberate tie
	if s, o := n.Detect(commonHead, ""), a.Detect(commonHead, ""); s != o || s < 55 || s >= 85 {
		t.Errorf("common: nginx %d apache %d", s, o)
	}
	// mixed trailing kinds, text and custom formats are claimed by neither
	mixed := append(append([]byte{}, nginxHead...), apacheHead...)
	for name, head := range map[string][]byte{
		"mixed": mixed, "custom": fixture(t, "customformat.log"), "text": []byte("hello world\nsecond line\n"),
		"empty": nil, "iis-ish": []byte("#Fields: date time s-ip cs-method\n2001-02-03 04:05:06 192.0.2.1 GET\n"),
	} {
		if s, o := n.Detect(head, ""), a.Detect(head, ""); s != 0 || o != 0 {
			t.Errorf("%s: nginx %d apache %d", name, s, o)
		}
	}
}
