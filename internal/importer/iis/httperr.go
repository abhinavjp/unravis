package iis

import (
	"context"
	"io"
	"strings"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// gapHTTPERRTime states the unverified time zone reading. HTTP.sys error logs
// are understood to stamp host-local time (unlike IIS W3C logs, which are
// UTC), but no sample has confirmed it, so a host zone is required and the
// file is refused without one.
const gapHTTPERRTime = "HTTPERR time zone unverified; timestamps are treated as host-local time."

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
		"queue": true, "siteid": true}
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
	zoneRule: clock.RuleHostLocal,
	keep: []string{"date", "time", "cs-method", "cs-uri", "sc-status", "s-siteid",
		"s-reason", "s-queuename"},
	required: []string{"date", "time"},
	preflight: func(opts importer.Options) error {
		if opts.Host == nil || opts.Host.Label == "" {
			return importer.ErrUnknownHost
		}
		if opts.Host.TZ == nil {
			return importer.ErrTimezoneUnlabelled
		}
		return nil
	},
	row: httperrRow,
	gaps: func(st *runState, rep *importer.Report) []string {
		return []string{gapHTTPERRTime}
	},
}

func (HTTPERR) Parse(ctx context.Context, r io.Reader, opts importer.Options,
	emit func(evidence.Event) error) (importer.Report, error) {
	return run(ctx, r, opts, emit, httperrSpec)
}

func httperrRow(c *rowCtx) (evidence.Event, importer.Reason, error) {
	var ev evidence.Event
	d, _ := c.get("date")
	t, _ := c.get("time")
	res, err := clock.ParseLocal("2006-01-02 15:04:05", d+" "+t, c.opts.Host.TZ)
	switch {
	case err != nil:
		return ev, importer.ReasonBadTime, nil
	case res.Ambiguous:
		return ev, importer.ReasonDSTAmbiguous, nil
	case res.Nonexistent:
		return ev, importer.ReasonDSTNonexistent, nil
	}
	ts := res.UTC.UTC()

	host, err := c.resolveHost("")
	if err != nil {
		return ev, "", err
	}

	attrs := make(map[string]string, 6)
	reason := "other"
	if v, ok := c.get("s-reason"); ok {
		if canon, ok := httperrReasons[strings.ToLower(v)]; ok {
			reason = canon
		}
	}
	attrs["reason"] = reason
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
	return evidence.Event{Kind: "httperr", Host: host, Start: ts, End: ts, Attrs: attrs}, "", nil
}
