package server

import (
	"context"
	"errors"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
)

// The audit lane of the tracing route (TR-04, TR-10): one server-side
// $match+$group aggregation per source on the UTC grid, never raw rows. A failed
// or over-budget read is complete:false with a reason code, never a short or
// silent window. No raw cookie, IP, query string or user agent is read, and no
// error text, connection string or source name reaches a reason.

const (
	tracingAuditLabel    = "API requests (audit log)"
	tracingConsLive      = "live-non-snapshot"
	tracingAuditCapWarn  = "Only the first 50 audit sources are shown."
	tracingAuditListWarn = "The audit sources could not be listed, so the audit lane is not shown."

	tracingAuditNoReaderMsg = "The audit source cannot be read here."
	tracingAuditBroadMsg    = "The audit source reads at most 24 hours unless broad scans are allowed, so this window was not read."
	tracingAuditTimeoutMsg  = "The audit read ran out of time; nothing was shortened."
	tracingAuditRowCapMsg   = "The audit read reached its row limit; nothing was shortened."
	tracingAuditErrorMsg    = "The audit read failed; nothing was shortened."

	auditCodeTimeout = "timeout"
	auditCodeRowCap  = "rowcap"
	auditCodeError   = "error"
)

// errUnknownAuditSource is returned by selectAuditSources when an explicit id is
// not one of the listed audit sources. It carries no id.
var errUnknownAuditSource = errors.New("unknown audit source")

// auditBucket is one axis cell of the audit lane (UTC).
type auditBucket struct {
	Start, End    time.Time
	Clipped       bool
	Count, Errors int64
}

// auditLaneRead is what readAuditLane found. ReasonCode is timeout, rowcap,
// error or empty. Truncated is always false: counts are aggregates. Sources
// holds the per-source outcome without any error text.
type auditLaneRead struct {
	Buckets             []auditBucket
	Complete, Truncated bool
	Reason, ReasonCode  string
	AsOf                time.Time
	Scanned             int64
	Sources             []audit.SourceResult
}

// auditReadWidth is min(axis width, 1 h): the slot width the read uses. The
// slots of a wider axis are folded into its cells afterwards.
func auditReadWidth(axis tracingAxis) time.Duration {
	w := time.Duration(axis.WidthMs) * time.Millisecond
	if w <= 0 || w > time.Hour {
		return time.Hour
	}
	return w
}

func auditReasonText(code string) string {
	switch code {
	case auditCodeTimeout:
		return tracingAuditTimeoutMsg
	case auditCodeRowCap:
		return tracingAuditRowCapMsg
	}
	return tracingAuditErrorMsg
}

func auditSourceCode(errorCode string) string {
	switch errorCode {
	case "query_timeout":
		return auditCodeTimeout
	case "query_memory_limit":
		return auditCodeRowCap
	}
	return auditCodeError
}

// auditFailure is the empty, incomplete answer of a read that failed as a whole.
func auditFailure(asOf time.Time, code, reason string) auditLaneRead {
	return auditLaneRead{Buckets: []auditBucket{}, Complete: false, ReasonCode: code, Reason: reason, AsOf: asOf, Sources: []audit.SourceResult{}}
}

// readAuditLane reads the audit lane with one Rollup call. now is taken once,
// before the read starts.
func readAuditLane(ctx context.Context, reader audit.RollupReader, sourceIDs []string, axis tracingAxis, now func() time.Time) auditLaneRead {
	asOf := now().UTC()
	stepSec := axis.WidthMs / 1000
	if reader == nil || stepSec <= 0 {
		return auditFailure(asOf, auditCodeError, tracingAuditNoReaderMsg)
	}
	plan := audit.RollupPlan{
		SourceIDs: append([]string(nil), sourceIDs...),
		From:      axis.Start, To: axis.End,
		Zone: "UTC", Loc: time.UTC,
		SlowMs: audit.DefaultRollupSlowMs,
		Width:  auditReadWidth(axis),
	}
	slots, err := reader.Rollup(ctx, plan)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, audit.ErrDocsExaminedBudget):
			return auditFailure(asOf, auditCodeTimeout, tracingAuditTimeoutMsg)
		case errors.Is(err, audit.ErrRollupGroupLimit), errors.Is(err, audit.ErrLimit):
			return auditFailure(asOf, auditCodeRowCap, tracingAuditRowCapMsg)
		case errors.Is(err, audit.ErrBroadScanRefused):
			return auditFailure(asOf, auditCodeError, tracingAuditBroadMsg)
		case errors.Is(err, context.Canceled):
			// The request was dropped; the handler writes nothing for it. A cancel
			// the request did not cause is an ordinary read failure.
			if ctx.Err() != nil {
				return auditFailure(asOf, auditCodeError, "")
			}
			return auditFailure(asOf, auditCodeError, tracingAuditErrorMsg)
		}
		return auditFailure(asOf, auditCodeError, tracingAuditErrorMsg)
	}

	out := auditLaneRead{Buckets: []auditBucket{}, AsOf: asOf, Sources: make([]audit.SourceResult, 0, len(slots.Sources))}
	type cell struct{ count, errs int64 }
	cells := map[int]*cell{}
	if slots.Builder != nil {
		axisStart := axis.AxisStart.Unix()
		for _, s := range slots.Builder.Wire().Slots {
			out.Scanned += s.Counts.Total
			k := (s.Start - axisStart) / stepSec
			if s.Start < axisStart || k < 0 || k >= int64(axis.Buckets) {
				continue
			}
			c := cells[int(k)]
			if c == nil {
				c = &cell{}
				cells[int(k)] = c
			}
			c.count += s.Counts.Total
			c.errs += s.Counts.C5xx
		}
	}
	for k := 0; k < axis.Buckets; k++ {
		c := cells[k]
		if c == nil {
			continue
		}
		from, to, clipped, ok := axis.Bucket(k)
		if !ok {
			continue
		}
		out.Buckets = append(out.Buckets, auditBucket{Start: from, End: to, Clipped: clipped, Count: c.count, Errors: c.errs})
	}

	failedCode, failed := "", false
	okIDs := make(map[string]bool, len(slots.Sources))
	for _, sr := range slots.Sources {
		// Error and IndexWarning are source driver text; they stop here.
		out.Sources = append(out.Sources, audit.SourceResult{SourceID: sr.SourceID, OK: sr.OK, ErrorCode: sr.ErrorCode})
		if sr.OK {
			okIDs[sr.SourceID] = true
		} else if !failed {
			failed, failedCode = true, auditSourceCode(sr.ErrorCode)
		}
	}
	// Every requested source must report an OK result; a source with no result
	// (or only a failed one) leaves the lane partial, never complete.
	if !failed {
		for _, id := range sourceIDs {
			if !okIDs[id] {
				failed, failedCode = true, auditCodeError
				break
			}
		}
	}
	switch {
	case failed:
		out.ReasonCode, out.Reason = failedCode, auditReasonText(failedCode)
	case len(slots.Sources) == 0:
		out.ReasonCode, out.Reason = auditCodeError, tracingAuditErrorMsg
	default:
		out.Complete = true
	}
	return out
}

// auditLaneToWire is the answer form of the audit lane. sourceLabels are the
// audit sources that were read.
func auditLaneToWire(read auditLaneRead, sourceLabels []tracingRef) tracingLane {
	state := calltrace.StateEvents
	switch {
	case !read.Complete:
		state = calltrace.StateIncomplete
	case len(read.Buckets) == 0:
		state = calltrace.StateNoneSeen
	}
	lane := tracingLane{
		Key: "audit", Label: tracingAuditLabel, State: state,
		Origin: tracingOriginLive, AsOf: read.AsOf.UTC(),
		Complete: read.Complete, Truncated: false,
		ReasonCode: read.ReasonCode, Reason: read.Reason,
		Scanned: read.Scanned, Consistency: tracingConsLive,
		Sources: append([]tracingRef{}, sourceLabels...),
		Buckets: make([]tracingBucket, 0, len(read.Buckets)),
		Events:  []tracingEvent{},
	}
	for _, b := range read.Buckets {
		errs := b.Errors
		lane.Buckets = append(lane.Buckets, tracingBucket{
			Start: b.Start.UTC(), End: b.End.UTC(), Count: b.Count, Errors: &errs,
			Clipped: b.Clipped, Complete: read.Complete,
		})
	}
	return lane
}

// selectAuditSources decides which audit sources one request reads.
//
// explicit false (the default call): every listed source in listed order, empty
// ids dropped, de-duplicated, at most 50 (more than that adds the cap warning).
// explicit true: the ids as sent, each checked against the listing; an id that is
// not listed returns errUnknownAuditSource, and with no ids nothing is listed.
// A nil repo is an empty listing. An error from the listing is returned as is
// (never errUnknownAuditSource). Labels come from the listing, else the id.
func selectAuditSources(ctx context.Context, repo audit.Repository, explicit bool, ids []string) (refs []tracingRef, warning string, err error) {
	refs = []tracingRef{}
	var want []string
	if explicit {
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			want = append(want, id)
		}
		if len(want) == 0 {
			return refs, "", nil
		}
	}
	var items []audit.Source
	if repo != nil {
		if items, err = repo.Sources(ctx); err != nil {
			return nil, "", err
		}
	}
	if explicit {
		byID := make(map[string]audit.Source, len(items))
		for _, s := range items {
			if _, dup := byID[s.ID]; !dup {
				byID[s.ID] = s
			}
		}
		for _, id := range want {
			s, listed := byID[id]
			if !listed {
				return nil, "", errUnknownAuditSource
			}
			refs = append(refs, tracingRef{SourceID: id, Label: tracingLabel(s.Label, id)})
		}
		return refs, "", nil
	}
	seen := make(map[string]bool, len(items))
	for _, s := range items {
		if s.ID == "" || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		if len(refs) == tracingMaxSources {
			return refs, tracingAuditCapWarn, nil
		}
		refs = append(refs, tracingRef{SourceID: s.ID, Label: tracingLabel(s.Label, s.ID)})
	}
	return refs, "", nil
}

// auditSourceIDs is selectAuditSources reduced to the ids.
func auditSourceIDs(ctx context.Context, repo audit.Repository, explicit bool, ids []string) (out []string, warning string, err error) {
	refs, warning, err := selectAuditSources(ctx, repo, explicit, ids)
	if err != nil {
		return nil, "", err
	}
	out = make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.SourceID)
	}
	return out, warning, nil
}
