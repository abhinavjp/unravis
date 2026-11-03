package iis

import (
	"context"
	"io"
	"net"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Time zone. The HTTP.sys error log Date and Time fields are UTC. Microsoft
// documents this in "Error logging in HTTP APIs" (Date is W3C format and UTC,
// Time is UTC; 5:30 PM UTC is written 17:30:00) and in "Format of the HTTP
// Server API Error Logs". The host zone therefore plays no part: no zone is
// required, and a row cannot fall in a clock change.

// httperrReasons is the allowlist for s-reason, authored from the public
// HTTP.sys error log documentation. It is not exhaustive: anything else is
// kept only as "other". Keys are lowercase; values are the canonical form.
var httperrReasons = func() map[string]string {
	m := map[string]string{}
	for _, r := range []string{
		"AppOffline", "AppPoolTimer", "AppShutdown", "BadRequest", "Client_Reset",
		"Connection_Abandoned_By_AppPool", "Connection_Abandoned_By_ReqQueue",
		"Connection_Dropped", "ConnLimit", "Disabled", "EntityTooLarge", "FieldLength",
		"Forbidden", "Header", "Hostname", "Internal", "N/A", "Number", "Precondition",
		"QueueFull", "RequestLength", "Timer_AppPool", "Timer_ConnectionIdle",
		"Timer_EntityBody", "Timer_HeaderWait", "Timer_MinBytesPerSecond", "URL",
		"UrlLength", "Verb", "Version_N/P",
	} {
		m[strings.ToLower(r)] = r
	}
	return m
}()

// KnownHTTPERRReason reports whether s is a reason this importer itself can
// produce: one of the canonical values of httperrReasons, compared exactly
// and case-sensitively, or the literal "other". It exists so a caller outside
// this package (the tracing server) can show a stored reason only when the
// importer, not the raw log file, is its source.
func KnownHTTPERRReason(s string) bool {
	if s == "other" {
		return true
	}
	for _, canon := range httperrReasons {
		if s == canon {
			return true
		}
	}
	return false
}

// HTTPERR imports HTTP.sys error logs.
type HTTPERR struct{}

// NewHTTPERR returns the iis-httperr importer.
func NewHTTPERR() importer.Importer { return HTTPERR{} }

func (HTTPERR) Name() string { return "iis-httperr" }

func (HTTPERR) DefaultLimits() importer.Limits {
	return importer.Limits{MaxBytes: 64 << 20, MaxRows: 1_000_000, MaxLineBytes: 64 << 10,
		MaxFields: 4096, MaxRejectFraction: 0.05}
}

func (HTTPERR) AllowedAttrs() map[string]bool {
	return map[string]bool{"reason": true, "status": true, "method": true, "stem": true,
		"queue": true, "siteid": true, "client-ip": true}
}

func (HTTPERR) Detect(head []byte, fileName string) int {
	software, fields := headerInfo(head)
	if len(fields) == 0 || !isHTTPERRHeader(software, fields) {
		return 0
	}
	if strings.Contains(software, "Microsoft HTTP API") {
		return 95
	}
	return 85
}

var httperrSpec = &spec{
	name:     "iis-httperr",
	limits:   HTTPERR{}.DefaultLimits(),
	zoneRule: clock.RuleUTCField,
	keep: []string{"date", "time", "cs-method", "cs-uri", "sc-status", "s-siteid",
		"s-reason", "s-queuename", "c-ip"},
	required: []string{"date", "time"},
	// HTTPERR rows carry no machine name, so the picked host labels them.
	preflight: func(opts importer.Options) error {
		if opts.Host == nil || opts.Host.Label == "" {
			return importer.ErrUnknownHost
		}
		return nil
	},
	row: httperrRow,
	gaps: func(st *runState, rep *importer.Report) []string {
		gaps := []string{gapW3CResolution}
		if !st.declared["c-ip"] {
			gaps = append(gaps, gapHTTPERRNoClientIP)
		}
		return gaps
	},
}

// gapHTTPERRNoClientIP holds when the file's #Fields header has no c-ip
// column, so no event can carry a client-ip attribute.
const gapHTTPERRNoClientIP = "HTTP error log has no client IP column; client addresses are not available for this file."

func (HTTPERR) Parse(ctx context.Context, r io.Reader, opts importer.Options,
	emit func(evidence.Event) error) (importer.Report, error) {
	return run(ctx, r, opts, emit, httperrSpec)
}

// httperrClientIP reports whether s is usable as a client-ip attribute. It
// has its own limit rather than sharing one with the W3C importer's IP check.
func httperrClientIP(s string) bool {
	if len(s) == 0 || len(s) > 64 || strings.ContainsAny(s, " /") {
		return false
	}
	return net.ParseIP(s) != nil
}

func httperrRow(c *rowCtx) (evidence.Event, importer.Reason, error) {
	var ev evidence.Event
	d, _ := c.get("date")
	t, _ := c.get("time")
	ts, err := time.Parse("2006-01-02 15:04:05", d+" "+t) // UTC: see the note at the top of this file
	if err != nil || !saneLogTime(ts) {
		return ev, importer.ReasonBadTime, nil
	}
	ts = ts.UTC()

	host, reason, err := c.resolveHost("")
	if err != nil || reason != "" {
		return ev, reason, err
	}

	attrs := make(map[string]string, 8)
	sreason := "other"
	if v, ok := c.get("s-reason"); ok {
		if canon, ok := httperrReasons[strings.ToLower(v)]; ok {
			sreason = canon
		}
	}
	attrs["reason"] = sreason
	if v, ok := c.get("cs-method"); ok {
		if m, ok := cleanToken(v, 20); ok {
			attrs["method"] = m
		}
	}
	if v, ok := c.get("cs-uri"); ok {
		if s, ok := normaliseStem(v); ok {
			attrs["stem"] = s
		}
	}
	if v, ok := c.get("sc-status"); ok {
		n, ok := parseUint(v, 599)
		if !ok || n < 100 {
			return ev, importer.ReasonBadNumber, nil
		}
		attrs["status"] = v
	}
	if v, ok := c.get("s-siteid"); ok {
		if _, ok := parseUint(v, 4294967295); !ok {
			return ev, importer.ReasonBadNumber, nil
		}
		attrs["siteid"] = v
	}
	if v, ok := c.get("s-queuename"); ok {
		if q, ok := cleanToken(v, 64); ok {
			attrs["queue"] = q
		}
	}
	if v, ok := c.get("c-ip"); ok && httperrClientIP(v) {
		attrs["client-ip"] = v
	}
	return evidence.Event{Kind: "httperr", Host: host, Start: ts, End: ts, Attrs: attrs}, "", nil
}
