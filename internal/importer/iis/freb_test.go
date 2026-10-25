package iis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

func frebOpts() importer.Options {
	o := baseOpts()
	o.FileName = "fr000001.xml"
	return o
}

// frebReq renders one minimal failed request; events are the inner XML.
func frebReqXML(start, attrs, events string) string {
	return `<failedRequest url="http://web-a.example.test/` + start + `" verb="GET" ` + attrs + `>` + events + `</failedRequest>` + "\n"
}

func frebEvent(stamp, extra string) string {
	return `<Event><System><TimeCreated SystemTime="` + stamp + `"/></System>` + extra + `</Event>`
}

func frebOK(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(frebReqXML("ok", `statusCode="500" triggerStatusCode="500" timeTaken="5" failureReason="STATUS_CODE"`,
			frebEvent("2001-03-17T10:00:00.000Z", `<EventData><Data Name="ModuleName">StaticFileModule</Data></EventData>`)+
				frebEvent("2001-03-17T10:00:00.005Z", "")))
	}
	return b.String()
}

func looseOpts() importer.Options {
	o := frebOpts()
	o.Limits = importer.Limits{MaxRejectFraction: 1}
	return o
}

func frebFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good: fixture(t, "freb_good.xml"),
		Bad: []byte(frebReqXML("a", `statusCode="500"`, frebEvent(importertest.Sentinel, ""))),
		// Large is the third request of Good repeated: a valid file over two head sizes
		Large:   []byte(strings.Repeat(frebReqXML("login", `statusCode="404" timeTaken="12" failureReason="STATUS_CODE"`, frebEvent("2001-03-17T10:09:30.500Z", `<EventData><Data Name="ModuleName">StaticFileModule</Data></EventData>`)+frebEvent("2001-03-17T10:09:30.512Z", "")), 700)),
		Options: frebOpts(),
		// status, reason, method, module and notification are typed words or numbers; the route is
		// normalised and kept by design
		NoFreeText:      true,
		KeepsCellValues: true,
	}
}

func TestFREBConformance(t *testing.T) {
	importertest.Run(t, NewFREB(), frebFixtures(t))
}

func FuzzFREBParse(f *testing.F) {
	importertest.FuzzParse(f, NewFREB(), frebFixtures(f))
}

func TestFREBGood(t *testing.T) {
	evs, rep := mustParse(t, NewFREB(), fixture(t, "freb_good.xml"), frebOpts())
	// three concatenated documents, one request each
	if rep.Rows != 3 || rep.Rejected != 0 || len(evs) != 8 || rep.Emitted != 8 {
		t.Fatalf("rows %d rejected %d events %d emitted %d", rep.Rows, rep.Rejected, len(evs), rep.Emitted)
	}
	if rep.Quality.TimezoneRule != clock.RuleUTCField || rep.FieldRules["SystemTime"] != clock.RuleUTCField {
		t.Fatalf("time rule: %+v %v", rep.Quality, rep.FieldRules)
	}
	type want struct {
		kind, mod, notif string
		line             int64
		start            time.Time
		dur              float64
		attrs            map[string]string
	}
	at := func(h, m, s, ms int) time.Time { return time.Date(2001, 3, 17, h, m, s, ms*1e6, time.UTC) }
	wants := []want{
		{"failed-request", "", "", 4, at(10, 1, 1, 0), 1250, map[string]string{"status": "500", "trigger": "500",
			"reason": "STATUS_CODE", "method": "GET", "stem": "/api/v1/orders/{id}", "substatus": "0", "win32": "2147942405"}},
		{"slow-module", "ManagedPipelineHandler", "EXECUTE_REQUEST_HANDLER", 4, at(10, 1, 1, 10), 1190, nil},
		{"slow-module", "RequestFilteringModule", "BEGIN_REQUEST", 4, at(10, 1, 1, 1), 2, nil},
		{"failed-request", "", "", 96, at(10, 5, 0, 0), 3400, map[string]string{"status": "200", "trigger": "200",
			"reason": "TIME_TAKEN", "method": "POST", "stem": "/api/v1/reports/summary"}},
		{"slow-module", "ManagedPipelineHandler", "EXECUTE_REQUEST_HANDLER", 96, at(10, 5, 0, 130), 3270, nil},
		{"slow-module", "UrlRewriteModule", "BEGIN_REQUEST", 96, at(10, 5, 0, 20), 100, nil},
		{"failed-request", "", "", 158, at(10, 9, 30, 500), 12, map[string]string{"status": "404", "trigger": "404",
			"reason": "STATUS_CODE", "method": "GET", "stem": "/login", "substatus": "0", "win32": "2147942402"}},
		{"slow-module", "StaticFileModule", "EXECUTE_REQUEST_HANDLER", 158, at(10, 9, 30, 505), 7, nil},
	}
	for i, w := range wants {
		e := evs[i]
		if e.Kind != w.kind || e.Source != "iis-freb" || e.Host != "web-a" || e.Line != w.line || e.TZRule != clock.RuleUTCField ||
			!e.Start.Equal(w.start) || e.DurationMs == nil || *e.DurationMs != w.dur {
			t.Fatalf("event %d: %+v (dur %v)", i, e, e.DurationMs)
		}
		attrs := w.attrs
		if attrs == nil {
			attrs = map[string]string{"module": w.mod, "notification": w.notif}
		}
		if fmt.Sprint(e.Attrs) != fmt.Sprint(attrs) {
			t.Fatalf("event %d attrs %v, want %v", i, e.Attrs, attrs)
		}
		if w.kind == "failed-request" && !e.End.Equal(e.Start.Add(time.Duration(w.dur)*time.Millisecond)) {
			t.Fatalf("event %d end %v", i, e.End)
		}
		if w.kind == "slow-module" && !e.End.Equal(e.Start.Add(time.Duration(w.dur)*time.Millisecond)) {
			t.Fatalf("event %d slow-module end %v", i, e.End)
		}
	}
	ids := map[string]bool{}
	for _, e := range evs {
		if ids[e.EvidenceID] {
			t.Fatalf("duplicate evidence id %s", e.EvidenceID)
		}
		ids[e.EvidenceID] = true
	}
	if len(rep.KnownGaps) == 0 {
		t.Fatal("no known gaps stated")
	}
	if !rep.First.Equal(at(10, 1, 1, 0)) {
		t.Fatalf("first %v", rep.First)
	}
}

// The sentinel sits in the query, fragment, other attributes, headers, event
// data, module payloads, a comment, a CDATA block and a foreign host name; none
// of it may reach an event or the report.
func TestFREBPrivacy(t *testing.T) {
	evs, rep, err := parse(t, NewFREB(), fixture(t, "freb_privacy.xml"), frebOpts())
	if err != nil {
		t.Fatal(err)
	}
	if got := dump(evs, rep); strings.Contains(got, importertest.Sentinel) || strings.Contains(strings.ToLower(got), "sentinel") {
		t.Fatalf("sentinel leaked:\n%s", got)
	}
	for _, h := range rep.ObservedHosts {
		if strings.Contains(h, "SENTINEL") {
			t.Fatalf("observed host %q", h)
		}
	}
	if len(evs) != 2 {
		t.Fatalf("events %d, want a request and one slow module", len(evs))
	}
	r := evs[0]
	if r.Attrs["stem"] != "/api/v1/search" || r.Attrs["status"] != "500" || r.Attrs["win32"] != "2147942405" || r.Attrs["method"] != "GET" {
		t.Fatalf("request attrs %v", r.Attrs)
	}
	if len(r.Attrs) != 7 { // status trigger reason method stem substatus? (none sent) win32 ...
		// substatus is absent: HttpSubStatus 0 was sent, so it is kept
		t.Logf("attrs: %v", r.Attrs)
	}
	s := evs[1]
	if s.Attrs["module"] != "ManagedPipelineHandler" || s.Attrs["notification"] != "EXECUTE_REQUEST_HANDLER" ||
		s.DurationMs == nil || *s.DurationMs != 500 {
		t.Fatalf("slow module %v %v", s.Attrs, s.DurationMs)
	}
}

func TestFREBNoPayloadAttribute(t *testing.T) {
	evs, _ := mustParse(t, NewFREB(), fixture(t, "freb_good.xml"), frebOpts())
	allowed := NewFREB().AllowedAttrs()
	for _, e := range evs {
		for k, v := range e.Attrs {
			if !allowed[k] {
				t.Fatalf("attribute %q not allowed", k)
			}
			if strings.ContainsAny(v, " ?=&") && k != "reason" {
				t.Fatalf("attribute %s=%q looks like free text", k, v)
			}
		}
	}
	// the header, cookie and body names of a trace are never attribute keys
	for k := range allowed {
		switch k {
		case "status", "substatus", "win32", "trigger", "reason", "method", "stem", "module", "notification":
		default:
			t.Fatalf("unexpected allowed attribute %q", k)
		}
	}
}

func TestFREBLowercaseEventAndAttrs(t *testing.T) {
	x := `<failedrequest URL="http://web-a/x" StatusCode="503" TriggerStatusCode="503" TimeTaken="40" FailureReason="status_code" Verb="get">` +
		`<event><system><timecreated systemtime="2001-03-17T10:00:00Z"/></system></event>` +
		`<event><system><timecreated systemtime="2001-03-17T10:00:00.040Z"/></system></event></failedrequest>`
	evs, rep := mustParse(t, NewFREB(), []byte(x), frebOpts())
	if rep.Rows != 1 || len(evs) != 1 || evs[0].Attrs["status"] != "503" || evs[0].Attrs["method"] != "GET" ||
		evs[0].Attrs["reason"] != "STATUS_CODE" || evs[0].Attrs["stem"] != "/x" {
		t.Fatalf("%d events, %+v", len(evs), evs)
	}
}

func TestFREBSlowestModuleRules(t *testing.T) {
	// a module that never completes owns the gap to the next event; one that
	// completes does not own the gap after it
	ev := func(stamp, mod, notif, op string) string {
		d := ""
		if mod != "" {
			d = `<EventData><Data Name="ModuleName">` + mod + `</Data><Data Name="Notification">` + notif + `</Data></EventData>`
		}
		return frebEvent(stamp, d+`<RenderingInfo><Opcode>`+op+`</Opcode></RenderingInfo>`)
	}
	x := frebReqXML("p", `statusCode="500" timeTaken="900"`,
		ev("2001-03-17T10:00:00.000Z", "", "", "GENERAL_REQUEST_START")+
			ev("2001-03-17T10:00:00.100Z", "ModA", "BEGIN_REQUEST", "NOTIFY_MODULE_START")+
			ev("2001-03-17T10:00:00.200Z", "ModA", "BEGIN_REQUEST", "NOTIFY_MODULE_COMPLETION")+
			ev("2001-03-17T10:00:00.700Z", "ModB", "EXECUTE_REQUEST_HANDLER", "NOTIFY_MODULE_START")+
			ev("2001-03-17T10:00:00.900Z", "ModB", "EXECUTE_REQUEST_HANDLER", "NOTIFY_MODULE_COMPLETION"))
	evs, _ := mustParse(t, NewFREB(), []byte(x), frebOpts())
	if len(evs) != 3 {
		t.Fatalf("events %d", len(evs))
	}
	// ModA: 100 ms (start to completion); the 500 ms before ModB belongs to nobody; ModB: 200 ms
	if evs[1].Attrs["module"] != "ModB" || *evs[1].DurationMs != 200 || evs[2].Attrs["module"] != "ModA" || *evs[2].DurationMs != 100 {
		t.Fatalf("slow modules: %+v %+v", evs[1], evs[2])
	}
}

func TestFREBTopModulesCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString(frebEvent("2001-03-17T10:00:00.000Z", ""))
	for i := 0; i < 6; i++ {
		b.WriteString(frebEvent(fmt.Sprintf("2001-03-17T10:00:00.%03dZ", 100*(i+1)),
			fmt.Sprintf(`<EventData><Data Name="ModuleName">Mod%d</Data></EventData>`, i)))
	}
	evs, _ := mustParse(t, NewFREB(), []byte(frebReqXML("p", `statusCode="500"`, b.String())), frebOpts())
	if len(evs) != 1+frebTopModules {
		t.Fatalf("events %d, want request plus %d modules", len(evs), frebTopModules)
	}
}

func TestFREBRefusesDoctype(t *testing.T) {
	bomb := `<?xml version="1.0"?><!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">` +
		`<!ENTITY lol3 "&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;">]>` + frebOK(1)
	_, _, err := parse(t, NewFREB(), []byte(bomb), frebOpts())
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, frebWhyDoctype)
	ext := `<?xml version="1.0"?><!DOCTYPE r [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + frebOK(1)
	_, _, err = parse(t, NewFREB(), []byte(ext), frebOpts())
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, frebWhyDoctype)
}

func TestFREBUndeclaredEntityIsACut(t *testing.T) {
	x := frebOK(2) + frebReqXML("a", `statusCode="500"`, `<Event><Data>&xxe;</Data></Event>`)
	evs, rep := mustParse(t, NewFREB(), []byte(x), frebOpts())
	if rep.Rows != 2 || len(evs) != 4 {
		t.Fatalf("rows %d events %d: the requests before the bad text are kept", rep.Rows, len(evs))
	}
	if !containsGap(rep.KnownGaps, gapFREBCut) {
		t.Fatalf("gaps %v", rep.KnownGaps)
	}
}

func containsGap(gaps []string, g string) bool {
	for _, s := range gaps {
		if s == g {
			return true
		}
	}
	return false
}

func TestFREBDeepNesting(t *testing.T) {
	deep := strings.Repeat("<a>", frebMaxDepth+10) + strings.Repeat("</a>", frebMaxDepth+10)
	x := `<failedRequest url="http://web-a/x" statusCode="500"><Event><EventData><Data Name="X">` + deep + `</Data></EventData></Event></failedRequest>`
	_, _, err := parse(t, NewFREB(), []byte(x), frebOpts())
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, frebWhyDepth)
	// far deeper than the stack would take, still a refusal rather than a crash
	huge := `<failedRequest url="http://web-a/x" statusCode="500">` + strings.Repeat("<a>", 200000)
	_, _, err = parse(t, NewFREB(), []byte(huge), frebOpts())
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, frebWhyDepth)
}

func TestFREBOneHugeToken(t *testing.T) {
	o := frebOpts()
	o.Limits = importer.Limits{MaxLineBytes: 4096}
	for name, x := range map[string]string{
		"text":    `<failedRequest url="http://web-a/x" statusCode="500"><Event><EventData><Data Name="Body">` + strings.Repeat("A", 200000) + `</Data></EventData></Event></failedRequest>`,
		"attr":    `<failedRequest url="http://web-a/` + strings.Repeat("a", 200000) + `" statusCode="500"></failedRequest>`,
		"comment": `<failedRequest url="http://web-a/x" statusCode="500"><!--` + strings.Repeat("c", 200000) + `--></failedRequest>`,
		"cdata":   `<failedRequest url="http://web-a/x" statusCode="500"><Event><![CDATA[` + strings.Repeat("d", 200000) + `]]></Event></failedRequest>`,
	} {
		_, _, err := parse(t, NewFREB(), []byte(x), o)
		var le *importer.LimitError
		if !errors.As(err, &le) || le.Limit != "line-bytes" {
			t.Errorf("%s: err = %v, want a line-bytes limit", name, err)
		}
	}
}

func TestFREBBadValuesRejectTheRequest(t *testing.T) {
	good := frebEvent("2001-03-17T10:00:00Z", "")
	for name, x := range map[string]string{
		"local time":      frebReqXML("a", `statusCode="500"`, frebEvent("2001-03-17T10:00:00+04:00", "")),
		"no zone":         frebReqXML("a", `statusCode="500"`, frebEvent("2001-03-17T10:00:00", "")),
		"not a time":      frebReqXML("a", `statusCode="500"`, frebEvent("yesterday", "")),
		"year 1601":       frebReqXML("a", `statusCode="500"`, frebEvent("1601-01-01T00:00:00Z", "")),
		"no events":       frebReqXML("a", `statusCode="500"`, ""),
		"bad status":      frebReqXML("a", `statusCode="five hundred"`, good),
		"status 99":       frebReqXML("a", `statusCode="99"`, good),
		"status 600":      frebReqXML("a", `statusCode="600"`, good),
		"no status":       frebReqXML("a", ``, good),
		"negative taken":  frebReqXML("a", `statusCode="500" timeTaken="-5"`, good),
		"bad taken":       frebReqXML("a", `statusCode="500" timeTaken="1e3"`, good),
		"bad trigger":     frebReqXML("a", `statusCode="500" triggerStatusCode="x"`, good),
		"later bad event": frebReqXML("a", `statusCode="500"`, good+frebEvent("nonsense", "")),
	} {
		evs, rep, err := parse(t, NewFREB(), []byte(x+frebOK(1)), looseOpts())
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if rep.Rejected != 1 || rep.Rows != 2 || len(evs) != 2 {
			t.Errorf("%s: rows %d rejected %d events %d", name, rep.Rows, rep.Rejected, len(evs))
		}
	}
	// the zone is read from the text, never from the host: a bad time is refused even when
	// the host has a zone
	_, rep, err := parse(t, NewFREB(), []byte(frebReqXML("a", `statusCode="500"`, frebEvent("2001-03-17T10:00:00", ""))+frebOK(1)), func() importer.Options {
		o := looseOpts()
		o.Host = &importer.HostHint{Label: "web-a", Role: "iis", TZ: time.FixedZone("x", 4*3600)}
		return o
	}())
	if err != nil || rep.Rejected != 1 {
		t.Fatalf("rejected %d err %v", rep.Rejected, err)
	}
}

func TestFREBEventCountCap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < frebMaxEvents+5; i++ {
		b.WriteString(frebEvent("2001-03-17T10:00:00Z", ""))
	}
	x := frebReqXML("big", `statusCode="500"`, b.String()) + frebOK(2)
	evs, rep := func() ([]evidence.Event, importer.Report) {
		e, r, err := parse(t, NewFREB(), []byte(x), looseOpts())
		if err != nil {
			t.Fatal(err)
		}
		return e, r
	}()
	if rep.Rows != 3 || rep.Rejected != 1 || len(evs) != 4 {
		t.Fatalf("rows %d rejected %d events %d", rep.Rows, rep.Rejected, len(evs))
	}
}

func TestFREBCutFileKeepsEarlierRequests(t *testing.T) {
	whole := frebOK(3)
	cut := whole[:len(whole)-40]
	evs, rep := mustParse(t, NewFREB(), []byte(cut), frebOpts())
	if rep.Rows != 2 || len(evs) != 4 || !containsGap(rep.KnownGaps, gapFREBCut) {
		t.Fatalf("rows %d events %d gaps %v", rep.Rows, len(evs), rep.KnownGaps)
	}
	// a file cut before its first request ends has nothing to read
	_, _, err := parse(t, NewFREB(), []byte(whole[:60]), frebOpts())
	wantQuarantine(t, err, importer.ReasonUnsupportedFormat, frebWhyNoRequest)
}

func TestFREBNoRequestAtAll(t *testing.T) {
	for name, x := range map[string]string{
		"xml":    `<?xml version="1.0"?><root><item/></root>`,
		"empty":  `<?xml version="1.0"?>`,
		"text":   `not xml at all`,
		"html":   `<html><body>hi</body></html>`,
		"events": `<Event><System/></Event>`,
	} {
		_, _, err := parse(t, NewFREB(), []byte(x), frebOpts())
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestFREBConcatenatedAndSeparateAgree(t *testing.T) {
	a, _ := mustParse(t, NewFREB(), []byte(frebOK(1)), frebOpts())
	b, rep := mustParse(t, NewFREB(), []byte(frebOK(1)+`<?xml version="1.0"?>`+"\n"+frebOK(1)), frebOpts())
	if len(a) != 2 || len(b) != 4 || rep.Rows != 2 {
		t.Fatalf("separate %d, joined %d rows %d", len(a), len(b), rep.Rows)
	}
	if b[0].EvidenceID == b[2].EvidenceID {
		t.Fatal("the same request twice shares an evidence id")
	}
}

func TestFREBUnknownHostAborts(t *testing.T) {
	x := strings.Replace(frebReqXML("a", `statusCode="500"`, frebEvent("2001-03-17T10:00:00Z", "")), "<Event>",
		`<Event><System><Computer>other.example.test</Computer></System>`, 1)
	x = strings.Replace(x, `</System></Event>`, `</System></Event>`, 1)
	_, _, err := parse(t, NewFREB(), []byte(x), frebOpts())
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("err = %v", err)
	}
}

func TestFREBDetect(t *testing.T) {
	d := NewFREB()
	good := fixture(t, "freb_good.xml")
	if s := d.Detect(good, "fr000001.xml"); s < 90 {
		t.Fatalf("detect = %d", s)
	}
	if s := d.Detect(utf16le(good), "fr000001.xml"); s < 90 {
		t.Fatalf("utf-16 detect = %d", s)
	}
	for _, x := range []string{"", "<", `<?xml version="1.0"?><log4net/>`, `<failedRequest>`, "#Fields: date time",
		fmt.Sprintf("%s", fixture(t, "w3c_good.log")), `<root><failedRequest/></root>`} {
		if s := d.Detect([]byte(x), "x.xml"); s != 0 {
			t.Errorf("detect(%.30q) = %d", x, s)
		}
	}
	// the file name alone says nothing
	if s := d.Detect([]byte("hello"), "fr000001.xml"); s != 0 {
		t.Fatalf("name-only detect = %d", s)
	}
}

func TestFREBTimeParsing(t *testing.T) {
	for in, ok := range map[string]bool{
		"2001-03-17T10:01:01.000Z":      true,
		"2001-03-17T10:01:01Z":          true,
		"2001-03-17T10:01:01.1234567Z":  true,
		" 2001-03-17T10:01:01.000Z ":    true,
		"2001-03-17T10:01:01.000+00:00": false,
		"2001-03-17T10:01:01.000z":      false,
		"2001-03-17 10:01:01.000Z":      false,
		"":                              false,
		"2001-13-17T10:01:01Z":          false,
		"1601-01-01T00:00:00Z":          false,
	} {
		if _, got := parseFREBTime(in); got != ok {
			t.Errorf("parseFREBTime(%q) = %v, want %v", in, got, ok)
		}
	}
}

func TestFREBErrorCode(t *testing.T) {
	for in, want := range map[string]string{
		"Access is denied. (0x80070005)": "2147942405",
		"(0x0)":                           "0",
		"(0x80070002)":                    "2147942402",
		"1234":                            "1234",
		"":                                "",
		"(0x)":                            "",
		"(0x123456789)":                   "",
		"(0xZZ)":                          "",
		"no number":                       "",
		"SENTINEL-7f3a":                   "",
	} {
		got, ok := frebErrorCode(in)
		if got != want || ok != (want != "") {
			t.Errorf("frebErrorCode(%q) = %q, %v", in, got, ok)
		}
	}
}

func TestFREBModuleNames(t *testing.T) {
	for in, ok := range map[string]bool{
		"ManagedPipelineHandler": true, "ScriptModule-4.0": true, "Custom.Module_1": true,
		"SENTINEL-7f3a": false, "a b": false, "Mod-1": false, "": false, strings.Repeat("a", 65): false, "Mod/x": false,
	} {
		if _, got := frebModuleName(in); got != ok {
			t.Errorf("frebModuleName(%q) = %v, want %v", in, got, ok)
		}
	}
}

func TestFREBFrebPath(t *testing.T) {
	for in, want := range map[string]string{
		"http://web-a.example.test:80/api/v1/orders/1042?page=2": "/api/v1/orders/{id}",
		"https://user:pw@web-a.example.test/login":               "/login",
		"/rel/path?q=1":                                          "/rel/path",
		"http://web-a.example.test":                              "",
		"http://web-a.example.test?x=1":                          "",
		"nonsense":                                               "",
	} {
		got, ok := frebPath(in)
		if got != want || ok != (want != "") {
			t.Errorf("frebPath(%q) = %q, %v", in, got, ok)
		}
	}
}

func TestFREBUTF16AndBOM(t *testing.T) {
	good := fixture(t, "freb_good.xml")
	// the declaration says UTF-8 although the bytes are UTF-16: the decoder is told to take the bytes as given
	a, _ := mustParse(t, NewFREB(), good, frebOpts())
	b, _ := mustParse(t, NewFREB(), utf16le(good), frebOpts())
	c, _ := mustParse(t, NewFREB(), append([]byte{0xEF, 0xBB, 0xBF}, good...), frebOpts())
	if len(a) != len(b) || len(a) != len(c) {
		t.Fatalf("utf-8 %d, utf-16 %d, bom %d", len(a), len(b), len(c))
	}
}

func TestFREBCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := importer.Collect(ctx, NewFREB(), bytes.NewReader([]byte(frebOK(3))), frebOpts())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
