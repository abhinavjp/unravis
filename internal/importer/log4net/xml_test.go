package log4net

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

func xmlFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:       fixture(t, "good.xml"),
		Bad:        fixture(t, "bad.xml"),
		Options:    baseOpts(t),
		NoFreeText: true,
	}
}

func TestXMLConformance(t *testing.T) {
	importertest.Run(t, NewXML(), xmlFixtures(t))
}

func FuzzXMLParse(f *testing.F) {
	importertest.FuzzParse(f, NewXML(), xmlFixtures(f))
}

func TestXMLGoodFixture(t *testing.T) {
	evs, rep, err := parse(t, NewXML(), fixture(t, "good.xml"), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 3 || rep.Rejected != 0 {
		t.Fatalf("rows %d rejected %d", rep.Rows, rep.Rejected)
	}
	if rep.Quality.TimezoneRule != clock.RuleUTCField {
		t.Errorf("rule %q", rep.Quality.TimezoneRule)
	}
	// 1003971751400 ms is 2001-10-25T01:02:31.400Z
	if want := time.Date(2001, 10, 25, 1, 2, 31, 123_000_000, time.UTC); !rep.First.Equal(want) {
		t.Errorf("first %v, want %v", rep.First, want)
	}
	if want := time.Date(2001, 10, 25, 1, 2, 35, 500_000_000, time.UTC); !rep.Last.Equal(want) {
		t.Errorf("last %v, want %v", rep.Last, want)
	}
	if len(rep.ObservedHosts) != 1 || rep.ObservedHosts[0] != "web-a" {
		t.Errorf("observed hosts %v", rep.ObservedHosts)
	}
	var sawExc bool
	for _, e := range evs {
		if e.Host != "web-a" {
			t.Errorf("host %q", e.Host)
		}
		if e.Attrs["exception"] != "" {
			sawExc = true
		}
	}
	if !sawExc {
		t.Error("no event carries the throwable type")
	}
}

func TestXMLNative(t *testing.T) {
	evs, rep, err := parse(t, NewXML(), fixture(t, "native.xml"), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 2 || len(evs) == 0 {
		t.Fatalf("rows %d events %d", rep.Rows, len(evs))
	}
	if rep.Quality.TimezoneRule != clock.RuleOffsetInField {
		t.Errorf("rule %q", rep.Quality.TimezoneRule)
	}
	if want := time.Date(2001, 10, 25, 1, 2, 31, 400_000_000, time.UTC); !rep.First.Equal(want) {
		t.Errorf("first %v, want %v", rep.First, want)
	}
	if len(rep.ObservedHosts) != 1 || rep.ObservedHosts[0] != "web-a" {
		t.Errorf("observed hosts %v", rep.ObservedHosts)
	}
}

func TestXMLNativeZIsUTC(t *testing.T) {
	data := []byte(`<log4net:event logger="Synthetic.Api.A" timestamp="2001-10-25T01:02:31.400Z" level="INFO"><log4net:message>m</log4net:message></log4net:event>` + "\n")
	_, rep, err := parse(t, NewXML(), data, baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Quality.TimezoneRule != clock.RuleUTCField {
		t.Errorf("rule %q", rep.Quality.TimezoneRule)
	}
}

func TestXMLDoctypeRefused(t *testing.T) {
	cases := map[string][]byte{
		"file":   fixture(t, "doctype.xml"),
		"inside": []byte(`<log4j:event logger="A" timestamp="1003971751400" level="INFO"><log4j:message>x</log4j:message><!DOCTYPE x></log4j:event>` + "\n"),
		"after": []byte(`<log4j:event logger="A" timestamp="1003971751400" level="INFO"><log4j:message>x</log4j:message></log4j:event>` + "\n" +
			`<!DOCTYPE x [<!ENTITY e SYSTEM "http://192.0.2.1/x">]>` + "\n"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			evs, _, err := parse(t, NewXML(), data, baseOpts(t))
			var qe *importer.QuarantineError
			if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat || qe.Column != whyDoctype {
				t.Fatalf("err %v, want unsupported-format with %s", err, whyDoctype)
			}
			if len(evs) != 0 {
				t.Errorf("%d events from a refused file", len(evs))
			}
		})
	}
}

func TestXMLLargeElementIsLineLimit(t *testing.T) {
	for name, body := range map[string]string{
		"message":   `<log4j:message>` + strings.Repeat("x", 300<<10) + `</log4j:message>`,
		"throwable": `<log4j:throwable>` + strings.Repeat("y", 300<<10) + `</log4j:throwable>`,
		"data":      `<log4j:properties><log4j:data name="k" value="` + strings.Repeat("z", 300<<10) + `"/></log4j:properties>`,
	} {
		t.Run(name, func(t *testing.T) {
			data := `<log4j:event logger="A" timestamp="1003971751400" level="INFO">` + body + `</log4j:event>` + "\n"
			_, _, err := parse(t, NewXML(), []byte(data), baseOpts(t))
			var le *importer.LimitError
			if !errors.As(err, &le) || le.Limit != "line-bytes" {
				t.Fatalf("err %v, want line-bytes limit", err)
			}
		})
	}
}

func TestXMLHugeCommentIsLineLimit(t *testing.T) {
	data := "<!--" + strings.Repeat("c", 300<<10) + "-->\n" +
		`<log4j:event logger="A" timestamp="1003971751400" level="INFO"><log4j:message>x</log4j:message></log4j:event>` + "\n"
	_, _, err := parse(t, NewXML(), []byte(data), baseOpts(t))
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "line-bytes" {
		t.Fatalf("err %v, want line-bytes limit", err)
	}
}

func TestXMLBadStampRejectedNotLeaked(t *testing.T) {
	o := baseOpts(t)
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := parse(t, NewXML(), fixture(t, "bad.xml"), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonBadTime || rep.Errors[0].Line != 2 {
		t.Fatalf("rejected %d errors %v", rep.Rejected, rep.Errors)
	}
	for _, e := range evs {
		for k, v := range e.Attrs {
			if strings.Contains(v, importertest.Sentinel) {
				t.Errorf("sentinel in attribute %s", k)
			}
		}
	}
}

func TestXMLTruncatedKeepsEarlierEvents(t *testing.T) {
	good := fixture(t, "good.xml")
	cut := good[:bytes.LastIndex(good, []byte("<log4j:event"))+30]
	evs, rep, err := parse(t, NewXML(), cut, baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 2 || len(evs) == 0 {
		t.Fatalf("rows %d events %d", rep.Rows, len(evs))
	}
	if !hasGap(rep, GapXMLCut) {
		t.Errorf("gaps %v lack the cut sentence", rep.KnownGaps)
	}
}

func TestXMLNoEventRefused(t *testing.T) {
	_, _, err := parse(t, NewXML(), []byte("<a><b/></a>\n"), baseOpts(t))
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonUnsupportedFormat {
		t.Fatalf("err %v, want unsupported-format", err)
	}
	if _, _, err = parse(t, NewXML(), []byte("<log4j:event "), baseOpts(t)); err == nil {
		t.Fatal("a file cut before the first event was accepted")
	}
}

func TestXMLDeepNestingRefused(t *testing.T) {
	data := strings.Repeat("<a>", maxXMLDepth+5) + strings.Repeat("</a>", maxXMLDepth+5) + "\n"
	_, _, err := parse(t, NewXML(), []byte(data), baseOpts(t))
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Column != whyDepth {
		t.Fatalf("err %v, want %s", err, whyDepth)
	}
}

func hostMap(name string) (importer.HostHint, bool) {
	switch name {
	case "web-a":
		return importer.HostHint{Label: "host-1", Role: "app"}, true
	case "web-b":
		return importer.HostHint{Label: "host-2", Role: "app"}, true
	}
	return importer.HostHint{}, false
}

func ev(host string, ms int) string {
	h := ""
	if host != "" {
		h = `<log4j:properties><log4j:data name="log4jmachinename" value="` + host + `"/></log4j:properties>`
	}
	return `<log4j:event logger="A" timestamp="` + itoa(1003971751400+ms) + `" level="INFO"><log4j:message>m</log4j:message>` + h + `</log4j:event>` + "\n"
}

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			return string(b[i:])
		}
	}
}

func TestXMLHostMap(t *testing.T) {
	o := baseOpts(t)
	o.Host = nil
	o.Hosts = hostMap
	data := ev("web-a", 0) + ev("web-b", 1000) + ev("web-a", 2000)
	evs, rep, err := parse(t, NewXML(), []byte(data), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.ObservedHosts) != 2 {
		t.Errorf("observed %v", rep.ObservedHosts)
	}
	seen := map[string]bool{}
	for _, e := range evs {
		seen[e.Host] = true
	}
	if !seen["host-1"] || !seen["host-2"] || seen["web-a"] || seen["web-b"] {
		t.Errorf("hosts in events %v", seen)
	}
}

func TestXMLUnmappedMachineRefused(t *testing.T) {
	o := baseOpts(t)
	o.Host = nil
	o.Hosts = hostMap
	_, _, err := parse(t, NewXML(), []byte(ev("web-z", 0)), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("err %v, want ErrUnknownHost", err)
	}
}

func TestXMLNoMachineNeedsHost(t *testing.T) {
	o := baseOpts(t)
	o.Host = nil
	_, _, err := parse(t, NewXML(), []byte(ev("", 0)), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("err %v, want ErrUnknownHost", err)
	}
}

func TestXMLSecondMachineWithoutMapRefused(t *testing.T) {
	data := ev("web-a", 0) + ev("web-b", 1000)
	_, _, err := parse(t, NewXML(), []byte(data), baseOpts(t))
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("err %v, want ErrUnknownHost", err)
	}
}

func TestXMLMachineNameCaseKept(t *testing.T) {
	data := ev("web-a", 0) + ev("WEB-A", 1000)
	_, rep, err := parse(t, NewXML(), []byte(data), baseOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 2 {
		t.Errorf("rows %d", rep.Rows)
	}
}

func TestXMLEncodings(t *testing.T) {
	good := fixture(t, "good.xml")
	bom := append([]byte{0xEF, 0xBB, 0xBF}, good...)
	evs, _, err := parse(t, NewXML(), bom, baseOpts(t))
	if err != nil || len(evs) == 0 {
		t.Fatalf("utf-8 BOM: %d events, err %v", len(evs), err)
	}
}

func TestXMLDetect(t *testing.T) {
	m := NewXML()
	for _, name := range []string{"good.xml", "native.xml"} {
		if s := m.Detect(fixture(t, name), "x.xml"); s < 50 {
			t.Errorf("%s scored %d", name, s)
		}
	}
	if s := m.Detect(fixture(t, "good.log"), "app.log"); s != 0 {
		t.Errorf("text log scored %d as xml", s)
	}
	if s := m.Detect([]byte("<html><body>x</body></html>"), "x.html"); s != 0 {
		t.Errorf("html scored %d", s)
	}
}

func TestXMLParseStamp(t *testing.T) {
	good := []string{"1003971751400", "2001-10-25T14:02:31.400+13:00", "2001-10-25T01:02:31Z"}
	for _, s := range good {
		if _, _, ok := parseStamp(s); !ok {
			t.Errorf("%q refused", s)
		}
	}
	bad := []string{"", "x", "0", "99999999999999999999", "1003971751400000", "2001-10-25 14:02:31", "9999999999999"}
	for _, s := range bad {
		if _, _, ok := parseStamp(s); ok {
			t.Errorf("%q accepted", s)
		}
	}
}
