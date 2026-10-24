package schedule

import (
	"context"
	"errors"
	"io"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Hangfire is the importer for a Hangfire recurring-job CSV export.
//
// Columns: JobId, Queue, Cron, TimeZoneId, LastExecution, NextExecution,
// CreatedAt. Required: JobId, Cron, TimeZoneId. Times are ISO 8601 with Z or an
// offset, or epoch seconds or milliseconds; a zone-less time is rejected.
// A Job or Args column (or the other payload columns below) quarantines the
// file: those hold method names and arguments, which are never read.
type Hangfire struct{}

// NewHangfire returns the Hangfire schedule importer.
func NewHangfire() *Hangfire { return &Hangfire{} }

var hangfireUnscrubbed = []string{"job", "args", "arguments", "invocationdata", "payload"}

func (*Hangfire) Name() string                   { return NameHangfire }
func (*Hangfire) AllowedAttrs() map[string]bool  { return copyAttrs() }
func (*Hangfire) DefaultLimits() importer.Limits { return defaultLimits() }

func copyAttrs() map[string]bool {
	m := make(map[string]bool, len(attrs))
	for k, v := range attrs {
		m[k] = v
	}
	return m
}

// Detect scores a file by its header row only.
func (*Hangfire) Detect(head []byte, fileName string) int {
	cells := headerCells(decodeHead(head))
	if cells["jobid"] && cells["cron"] && cells["timezoneid"] {
		return 90
	}
	return 0
}

// Parse implements importer.Importer.
func (h *Hangfire) Parse(ctx context.Context, rd io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	r := newRun(ctx, NameHangfire, opts, emit)
	cr, tb, err := openCSV(rd, r.lim)
	if err != nil {
		return r.rep, err
	}
	if err := tb.refuseColumns(hangfireUnscrubbed); err != nil {
		return r.rep, err
	}
	if err := tb.require("jobid", "cron", "timezoneid"); err != nil {
		return r.rep, err
	}
	r.rep.FieldRules["TimeZoneId"] = clock.RuleOffsetInField
	zones := map[string]*time.Location{}
	rule := clock.RuleUTCField
	for {
		if err := ctx.Err(); err != nil {
			return r.rep, err
		}
		rec, line, reason, err := nextRow(cr, tb.n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return r.rep, err
		}
		r.rep.Rows++
		if r.rep.Rows > r.lim.MaxRows {
			return r.rep, &importer.LimitError{Limit: "rows", Max: r.lim.MaxRows, At: r.rep.Rows}
		}
		if reason != "" {
			r.reject(line, reason)
			continue
		}
		used, err := h.row(r, tb, rec, line, zones)
		if err != nil {
			return r.rep, err
		}
		if used != "" {
			rule = used
		}
	}
	return r.finish(rule, []string{gapHangfireSnap, gapHangfireLast})
}

// row validates and then emits one record. It returns the zone rule used when
// the row expanded a non-UTC schedule, so the file-level rule can say so.
func (h *Hangfire) row(r *run, tb *table, rec []string, line int64, zones map[string]*time.Location) (string, error) {
	job := tb.get(rec, "jobid")
	if job == "" {
		r.reject(line, importer.ReasonFieldCount)
		return "", nil
	}
	spec, cronUnknown, cerr := parseCron(tb.get(rec, "cron"))
	if cerr != nil {
		r.reject(line, importer.ReasonBadNumber)
		return "", nil
	}
	var last time.Time
	var lastRule string
	for _, col := range []string{"lastexecution", "nextexecution", "createdat"} {
		v := tb.get(rec, col)
		if v == "" {
			continue
		}
		t, rl, reason := parseInstant(v)
		if reason != "" {
			r.reject(line, reason)
			return "", nil
		}
		if col == "lastexecution" {
			last, lastRule = t, rl
		}
	}
	if _, ok := r.rep.FieldRules["LastExecution"]; !ok && lastRule != "" {
		r.rep.FieldRules["LastExecution"] = lastRule
	}

	base := map[string]string{"job": cleanAttr(job)}
	if q := tb.get(rec, "queue"); queueRE.MatchString(q) {
		base["queue"] = q
	}
	ord := 1
	used := ""
	loc := resolveZone(tb.get(rec, "timezoneid"), zones)
	switch {
	case cronUnknown:
		if err := r.sendUnknown(line, ord, base["job"], reasonCron, clock.RuleOffsetInField); err != nil {
			return "", err
		}
	case loc == nil:
		if err := r.sendUnknown(line, ord, base["job"], reasonTZ, clock.RuleOffsetInField); err != nil {
			return "", err
		}
	default:
		rule := clock.RuleOffsetInField
		if loc == time.UTC || loc.String() == "UTC" {
			rule = clock.RuleUTCField
		} else {
			used = clock.RuleOffsetInField
		}
		base["tz"] = loc.String()
		if err := r.expandCron(line, &ord, spec, loc, rule, base); err != nil {
			return "", err
		}
	}
	if !last.IsZero() && r.inWindow(last) {
		a := map[string]string{"job": base["job"], "basis": "last-execution"}
		if q, ok := base["queue"]; ok {
			a["queue"] = q
		}
		if err := r.send(line, 0, KindJobRun, last, last, nil, "info", lastRule, a); err != nil {
			return "", err
		}
	}
	return used, nil
}

var _ importer.Importer = (*Hangfire)(nil)
