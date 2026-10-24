package iis

import (
	"context"
	"io"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// w3cTimeIsEnd records an UNVERIFIED reading of the W3C `time` field: true
// means `time` is when the request finished, so Start = time - time-taken.
// IIS documents the field as the time the request was completed, but no
// sample from the target servers has confirmed it; the IIS owner must confirm
// before this is flipped or the gap below is dropped (wave plan section 5).
const w3cTimeIsEnd = true

const (
	gapW3CTimeEnd   = "W3C time semantics unverified; the time field is assumed to be the request end."
	gapW3CTimeStart = "W3C time semantics unverified; the time field is assumed to be the request start."
	gapTimeTaken    = "time-taken includes response delivery to the client; the residual against pipeline time is time outside the pipeline."
	gapNoTimeTaken  = "time-taken is not logged; request durations are unavailable."
)

// W3C imports IIS W3C extended request logs.
type W3C struct{}

// NewW3C returns the iis-w3c importer.
func NewW3C() importer.Importer { return W3C{} }

func (W3C) Name() string { return "iis-w3c" }

func (W3C) DefaultLimits() importer.Limits {
	return importer.Limits{MaxBytes: 2 << 30, MaxRows: 10_000_000, MaxLineBytes: 64 << 10,
		MaxFields: 4096, MaxRejectFraction: 0.05}
}

func (W3C) AllowedAttrs() map[string]bool {
	return map[string]bool{"method": true, "stem": true, "status": true, "substatus": true,
		"win32": true, "port": true, "site": true, "user": true}
}

// Detect scores W3C extended logs. IIS-format (comma separated) and NCSA
// logs have no #Fields directive and score 0; HTTPERR logs are left to
// iis-httperr.
func (W3C) Detect(head []byte, fileName string) int {
	software, fields := headerInfo(head)
	if len(fields) == 0 || isHTTPERRHeader(software, fields) {
		return 0
	}
	if !fields["date"] || !fields["time"] {
		return 0
	}
	if !(fields["cs-uri-stem"] || fields["cs-method"] || fields["sc-status"]) {
		return 0
	}
	if strings.Contains(software, "Internet Information Services") {
		return 95
	}
	return 70
}

var w3cSpec = &spec{
	name:     "iis-w3c",
	limits:   W3C{}.DefaultLimits(),
	zoneRule: clock.RuleUTCField,
	keep: []string{"date", "time", "cs-method", "cs-uri-stem", "sc-status", "sc-substatus",
		"sc-win32-status", "time-taken", "s-port", "s-sitename", "s-computername", "cs-username"},
	required: []string{"date", "time"},
	row:      w3cRow,
	gaps: func(st *runState, rep *importer.Report) []string {
		gaps := []string{gapW3CTimeEnd}
		if !w3cTimeIsEnd {
			gaps = []string{gapW3CTimeStart}
		}
		if st.declared["time-taken"] {
			return append(gaps, gapTimeTaken)
		}
		return append(gaps, gapNoTimeTaken)
	},
}

// Parse reads an IIS W3C log. See the package comment for the drop list.
func (W3C) Parse(ctx context.Context, r io.Reader, opts importer.Options,
	emit func(evidence.Event) error) (importer.Report, error) {
	return run(ctx, r, opts, emit, w3cSpec)
}

func w3cRow(c *rowCtx) (evidence.Event, importer.Reason, error) {
	var ev evidence.Event
	d, _ := c.get("date")
	t, _ := c.get("time")
	ts, err := time.Parse("2006-01-02 15:04:05", d+" "+t)
	if err != nil {
		return ev, importer.ReasonBadTime, nil
	}
	ts = ts.UTC()

	observed, _ := c.get("s-computername")
	host, err := c.resolveHost(observed)
	if err != nil {
		return ev, "", err
	}

	attrs := make(map[string]string, 8)
	if v, ok := c.get("cs-method"); ok {
		if m, ok := cleanToken(v, 20); ok {
			attrs["method"] = m
		}
	}
	if v, ok := c.get("cs-uri-stem"); ok {
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
	if v, ok := c.get("sc-substatus"); ok {
		if _, ok := parseUint(v, 65535); !ok {
			return ev, importer.ReasonBadNumber, nil
		}
		attrs["substatus"] = v
	}
	if v, ok := c.get("sc-win32-status"); ok {
		if _, ok := parseUint(v, 4294967295); !ok {
			return ev, importer.ReasonBadNumber, nil
		}
		attrs["win32"] = v
	}
	if v, ok := c.get("s-port"); ok {
		n, ok := parseUint(v, 65535)
		if !ok || n == 0 {
			return ev, importer.ReasonBadNumber, nil
		}
		attrs["port"] = v
	}
	if v, ok := c.get("s-sitename"); ok {
		if s, ok := cleanToken(v, 64); ok {
			attrs["site"] = s
		}
	}
	if v, ok := c.get("cs-username"); ok && c.opts.Pseudonymize != nil {
		attrs["user"] = c.opts.Pseudonymize("user", v)
	}

	ev = evidence.Event{Kind: "request", Host: host, Start: ts, End: ts, Attrs: attrs}
	if v, ok := c.get("time-taken"); ok {
		n, ok := parseUint(v, 4294967295)
		if !ok {
			return ev, importer.ReasonBadNumber, nil
		}
		ms := float64(n)
		ev.DurationMs = &ms
		taken := time.Duration(n) * time.Millisecond
		if w3cTimeIsEnd {
			ev.Start = ts.Add(-taken)
		} else {
			ev.End = ts.Add(taken)
		}
	}
	return ev, "", nil
}
