package server

import (
	"context"
	"errors"
	"strings"
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
	tracingAuditListWarn = "The audit sources could not be listed, so the audit lane was not read."

	tracingAuditSavedOnlyMsg = "This case saved audit databases that are not connected on this computer, so no API requests were read. Unravis reconnects a saved database by itself only when its password is saved. Otherwise press Connect next to it in the Sources row, enter the password, then press Start tracing again."
	tracingAuditNoReaderMsg  = "The audit source cannot be read here."
	tracingAuditBroadMsg     = "The audit source reads at most 24 hours unless broad scans are allowed, so this window was not read."
	tracingAuditTimeoutMsg   = "The audit read ran out of time; nothing was shortened."
	tracingAuditRowCapMsg    = "The audit read reached its row limit; nothing was shortened."
	tracingAuditErrorMsg     = "The audit read failed; nothing was shortened."
	// A window over 24 hours is read one chunk at a time; these say a read stopped
	// after some chunks. The chunks read are shown and the rest were not read.
	tracingAuditPartialTimeoutMsg = "The audit read ran out of time; the days read are shown and the rest were not read."
	tracingAuditPartialRowCapMsg  = "The audit read reached its row limit; the days read are shown and the rest were not read."
	tracingAuditPartialErrorMsg   = "The audit read failed part way; the days read are shown and the rest were not read."

	auditCodeTimeout = "timeout"
	auditCodeRowCap  = "rowcap"
	auditCodeError   = "error"
	// auditCodeBroadScan is the reason code of a window the live source refuses
	// without allowed broad scans (audit.ErrBroadScanRefused).
	auditCodeBroadScan = "broad-scan"
)

// errUnknownAuditSource is returned by selectAuditSources when an explicit id is
// not one of the listed audit sources. It carries no id.
var errUnknownAuditSource = errors.New("unknown audit source")

// auditBucket is one axis cell of the audit lane (UTC). Timed is how many of
// the Count requests had a timing, and P50Ms, P95Ms and MaxMs their response
// times (nil when none did).
type auditBucket struct {
	Start, End          time.Time
	Clipped             bool
	Count, Errors       int64
	Timed               int64
	P50Ms, P95Ms, MaxMs *float64
	// BusyMs is the summed duration in ms of the timed calls (nil when none).
	BusyMs *float64
}

// auditLaneRead is what readAuditLane found. ReasonCode is empty for a complete
// read, else timeout, rowcap, broad-scan or error. Truncated is always false:
// counts are aggregates. Sources holds the per-source outcome without any error
// text.
//
// The window is read in consecutive chunks of at most audit.LiveRollupMaxWindow
// (24 h), cut on the UTC slot grid. ChunksTotal is how many chunks the window needs
// (1 for a window of 24 h or less) and ChunksRead how many finished with every
// source OK. ReadThrough is the end of the last finished chunk: everything in the
// window before it is read, everything after it is not (zero until a chunk
// finishes). A cell that ends after ReadThrough is incomplete.
type auditLaneRead struct {
	Buckets                 []auditBucket
	Complete, Truncated     bool
	Reason, ReasonCode      string
	AsOf                    time.Time
	Scanned                 int64
	Sources                 []audit.SourceResult
	ChunksRead, ChunksTotal int
	ReadThrough             time.Time
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

// auditSoftShare is the share of the time left (to the request deadline) after
// which readAuditLane starts no new chunk, as readDays does for a daily series.
const auditSoftShare = 0.8

// auditSoftNow is the clock of the soft deadline. A variable so a test can step it.
var auditSoftNow = time.Now

// auditChunk is one read of the audit lane: the half-open window [From, To).
type auditChunk struct{ From, To time.Time }

// auditChunks cuts the axis window into consecutive reads of at most
// audit.LiveRollupMaxWindow. Every cut is on the UTC slot grid of width (a
// multiple of width since the epoch), so a slot never falls in two reads and the
// counts stay additive. A window of 24 h or less is one read of exactly the axis
// window.
func auditChunks(axis tracingAxis, width time.Duration) []auditChunk {
	from, to := axis.Start.UnixMilli(), axis.End.UnixMilli()
	maxMs, stepMs := audit.LiveRollupMaxWindow.Milliseconds(), width.Milliseconds()
	if stepMs <= 0 || to-from <= maxMs {
		return []auditChunk{{From: axis.Start, To: axis.End}}
	}
	var out []auditChunk
	for cur := from; cur < to; {
		end := to
		if to-cur > maxMs {
			if end = floorMs(cur+maxMs, stepMs); end <= cur {
				end = cur + maxMs
			}
		}
		out = append(out, auditChunk{From: time.UnixMilli(cur).UTC(), To: time.UnixMilli(end).UTC()})
		cur = end
	}
	return out
}

// auditReadError maps a failed Rollup call to a reason code and its text. dropped
// is true when the request itself was cancelled: the handler writes nothing for
// it, so there is no text.
func auditReadError(ctx context.Context, err error) (code, reason string, dropped bool) {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, audit.ErrDocsExaminedBudget):
		return auditCodeTimeout, tracingAuditTimeoutMsg, false
	case errors.Is(err, audit.ErrRollupGroupLimit), errors.Is(err, audit.ErrLimit):
		return auditCodeRowCap, tracingAuditRowCapMsg, false
	case errors.Is(err, audit.ErrBroadScanRefused):
		return auditCodeBroadScan, tracingAuditBroadMsg, false
	case errors.Is(err, context.Canceled):
		// The request was dropped; the handler writes nothing for it. A cancel
		// the request did not cause is an ordinary read failure.
		if ctx.Err() != nil {
			return auditCodeError, "", true
		}
	}
	return auditCodeError, tracingAuditErrorMsg, false
}

// auditPartialText is the reason of a read that finished some chunks and then
// stopped: the days read are shown, the rest were not read.
func auditPartialText(code string) string {
	switch code {
	case auditCodeTimeout:
		return tracingAuditPartialTimeoutMsg
	case auditCodeRowCap:
		return tracingAuditPartialRowCapMsg
	}
	return tracingAuditPartialErrorMsg
}

// auditSourceOutcome says whether one Rollup answer is short, and why. Every
// requested source must report an OK result; a source with no result (or only a
// failed one) leaves the chunk partial, never complete.
func auditSourceOutcome(slots audit.RollupSlots, sourceIDs []string) (failed bool, code string) {
	okIDs := make(map[string]bool, len(slots.Sources))
	for _, sr := range slots.Sources {
		if sr.OK {
			okIDs[sr.SourceID] = true
		} else if !failed {
			failed, code = true, auditSourceCode(sr.ErrorCode)
		}
	}
	if !failed {
		for _, id := range sourceIDs {
			if !okIDs[id] {
				return true, auditCodeError
			}
		}
	}
	if !failed && len(slots.Sources) == 0 {
		return true, auditCodeError
	}
	return failed, code
}

// readAuditLane reads the audit lane. A window of 24 h or less is one Rollup call.
// A longer one (the live source refuses more than 24 h per call) is read as
// consecutive chunks of at most 24 h, cut on the slot grid, one after the other
// (never in parallel), and the slots are folded into the same cells. now is
// taken once, before the read starts.
//
// Time budget, as readDays: when ctx has a deadline the read starts no new chunk
// once auditSoftShare of the time left at the start has passed, and a chunk that
// fails because the deadline passed ends the read the same way. The chunks
// finished so far (the oldest ones) come back with Complete false and reason
// timeout; the cells that end after ReadThrough are incomplete, and every unread
// cell is listed with a zero count so the caller can mark it. A chunk error (or a
// chunk with a failed source) ends the read the same way with its own reason code.
// A first chunk that fails as a whole is an empty, incomplete answer, as before.
func readAuditLane(ctx context.Context, reader audit.RollupReader, sourceIDs []string, axis tracingAxis, now func() time.Time) auditLaneRead {
	asOf := now().UTC()
	stepSec := axis.WidthMs / 1000
	if reader == nil || stepSec <= 0 {
		return auditFailure(asOf, auditCodeError, tracingAuditNoReaderMsg)
	}
	width := auditReadWidth(axis)
	chunks := auditChunks(axis, width)
	fail := func(code, reason string) auditLaneRead {
		f := auditFailure(asOf, code, reason)
		f.ChunksTotal = len(chunks)
		return f
	}
	var softStop time.Time
	if dl, ok := ctx.Deadline(); ok && len(chunks) > 1 {
		n := auditSoftNow()
		softStop = n.Add(time.Duration(float64(dl.Sub(n)) * auditSoftShare))
	}

	out := auditLaneRead{Buckets: []auditBucket{}, AsOf: asOf, Sources: []audit.SourceResult{}, ChunksTotal: len(chunks)}
	type cell struct {
		count, errs int64
		lat         latencyAcc
	}
	cells := map[int]*cell{}
	axisStart := axis.AxisStart.Unix()
	sourceAt := map[string]int{}
	stopCode := ""
	for i, ch := range chunks {
		if i > 0 {
			if err := ctx.Err(); err != nil {
				if !errors.Is(err, context.DeadlineExceeded) {
					return fail(auditCodeError, "") // the request was dropped
				}
				stopCode = auditCodeTimeout
				break
			}
			if !softStop.IsZero() && !auditSoftNow().Before(softStop) {
				stopCode = auditCodeTimeout
				break
			}
		}
		plan := audit.RollupPlan{
			SourceIDs: append([]string(nil), sourceIDs...),
			From:      ch.From, To: ch.To,
			Zone: "UTC", Loc: time.UTC,
			SlowMs: audit.DefaultRollupSlowMs,
			Width:  width,
		}
		slots, err := reader.Rollup(ctx, plan)
		if err != nil {
			code, reason, dropped := auditReadError(ctx, err)
			if i == 0 || dropped {
				return fail(code, reason)
			}
			stopCode = code // keep the chunks already read
			break
		}
		if slots.Builder != nil {
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
				c.lat.add(s.Bins)
			}
		}
		seenNow := map[string]bool{}
		for _, sr := range slots.Sources {
			seenNow[sr.SourceID] = true
			// Error and IndexWarning are source driver text; they stop here. A source
			// is OK only when it was OK in every chunk; its first failure code stays.
			if at, seen := sourceAt[sr.SourceID]; seen && i > 0 {
				if !sr.OK && out.Sources[at].OK {
					out.Sources[at].OK, out.Sources[at].ErrorCode = false, sr.ErrorCode
				}
				continue
			}
			sourceAt[sr.SourceID] = len(out.Sources)
			out.Sources = append(out.Sources, audit.SourceResult{SourceID: sr.SourceID, OK: sr.OK, ErrorCode: sr.ErrorCode})
		}
		if i > 0 {
			// A source absent from this chunk was not read by it, so it was not OK in
			// every chunk: it is no longer one of the sources read.
			for at := range out.Sources {
				if out.Sources[at].OK && !seenNow[out.Sources[at].SourceID] {
					out.Sources[at].OK, out.Sources[at].ErrorCode = false, auditCodeError
				}
			}
		}
		if failed, code := auditSourceOutcome(slots, sourceIDs); failed {
			stopCode = code // this chunk's counts are kept but it is not a finished one
			break
		}
		out.ChunksRead++
		out.ReadThrough = ch.To
	}
	switch {
	case stopCode == "":
		out.Complete = true
	case out.ChunksRead > 0:
		out.ReasonCode, out.Reason = stopCode, auditPartialText(stopCode)
	default:
		out.ReasonCode, out.Reason = stopCode, auditReasonText(stopCode)
	}

	// A chunked read that stopped lists every cell it did not finish, even with no
	// count, so the caller can mark it incomplete.
	listUnread := len(chunks) > 1 && !out.Complete
	for k := 0; k < axis.Buckets; k++ {
		from, to, clipped, ok := axis.Bucket(k)
		if !ok {
			continue
		}
		c := cells[k]
		if c == nil {
			if !listUnread || !to.After(out.ReadThrough) {
				continue
			}
			c = &cell{}
		}
		b := auditBucket{Start: from, End: to, Clipped: clipped, Count: c.count, Errors: c.errs}
		b.Timed, b.P50Ms, b.P95Ms, b.MaxMs = c.lat.figures()
		b.BusyMs = c.lat.busy()
		out.Buckets = append(out.Buckets, b)
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
	// The sources the read finished on are named; a source that failed is not.
	lane.SourcesRead = auditSourcesRead(read.Sources, sourceLabels)
	// A window read in chunks says how far the read got (ChunksRead of ChunksTotal,
	// each chunk at most one day). A single-chunk read adds nothing to the lane.
	if read.ChunksTotal > 1 {
		lane.ChunksTotal, lane.ChunksRead = read.ChunksTotal, read.ChunksRead
		if !read.Complete && !read.ReadThrough.IsZero() {
			through := read.ReadThrough.UTC()
			lane.ReadThrough = &through
		}
	}
	for _, b := range read.Buckets {
		errs := b.Errors
		lane.Buckets = append(lane.Buckets, tracingBucket{
			Start: b.Start.UTC(), End: b.End.UTC(), Count: b.Count, Errors: &errs,
			TimedCount: b.Timed, P50Ms: b.P50Ms, P95Ms: b.P95Ms, MaxMs: b.MaxMs, BusyMs: b.BusyMs,
			Clipped: b.Clipped, Complete: read.Complete || !b.End.After(read.ReadThrough),
		})
	}
	return lane
}

// auditSavedOnlyLane is the audit lane of a case that saved audit databases while
// none of them is open in this process: nothing was read, and it says why.
func auditSavedOnlyLane(now time.Time, saved []string) tracingLane {
	return tracingLane{
		Key: "audit", Label: tracingAuditLabel, State: calltrace.StateNoData,
		Origin: tracingOriginLive, AsOf: now.UTC(), Complete: true,
		Reason: tracingAuditSavedOnlyMsg, Consistency: tracingConsLive,
		Sources: []tracingRef{}, Buckets: []tracingBucket{}, Events: []tracingEvent{},
		SavedNotConnected: append([]string{}, saved...),
	}
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
	refs, _, warning, err = selectAuditSourcesListed(ctx, repo, explicit, ids)
	return refs, warning, err
}

// selectAuditSourcesListed is selectAuditSources that also returns the listing it
// read (nil when no listing call was made), so the caller can name the open
// sources and tell which saved databases are not connected without a second call.
func selectAuditSourcesListed(ctx context.Context, repo audit.Repository, explicit bool, ids []string) (refs []tracingRef, items []audit.Source, warning string, err error) {
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
			return refs, nil, "", nil
		}
	}
	if repo != nil {
		if items, err = repo.Sources(ctx); err != nil {
			return nil, nil, "", err
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
				return nil, nil, "", errUnknownAuditSource
			}
			refs = append(refs, tracingRef{SourceID: id, Label: tracingLabel(s.Label, id)})
		}
		return refs, items, "", nil
	}
	seen := make(map[string]bool, len(items))
	for _, s := range items {
		if s.ID == "" || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		if len(refs) == tracingMaxSources {
			return refs, items, tracingAuditCapWarn, nil
		}
		refs = append(refs, tracingRef{SourceID: s.ID, Label: tracingLabel(s.Label, s.ID)})
	}
	return refs, items, "", nil
}

// auditListingRefs is the open audit sources of a listing as refs: listed order,
// empty ids dropped, de-duplicated, at most tracingMaxSources.
func auditListingRefs(items []audit.Source) []tracingRef {
	out := []tracingRef{}
	seen := make(map[string]bool, len(items))
	for _, s := range items {
		if s.ID == "" || seen[s.ID] || len(out) == tracingMaxSources {
			continue
		}
		seen[s.ID] = true
		out = append(out, tracingRef{SourceID: s.ID, Label: tracingLabel(s.Label, s.ID)})
	}
	return out
}

// savedNotConnectedNames are the saved audit database names (tags, in saved
// order, de-duplicated) that no open source carries. An open source carries a
// saved name when its database or its label is that name, ignoring case. Only the
// saved names go out; no live database name does.
func savedNotConnectedNames(saved []string, items []audit.Source) []string {
	open := make(map[string]bool, 2*len(items))
	for _, s := range items {
		if d := strings.ToLower(strings.TrimSpace(s.Database)); d != "" {
			open[d] = true
		}
		if l := strings.ToLower(strings.TrimSpace(s.Label)); l != "" {
			open[l] = true
		}
	}
	out := []string{}
	seen := make(map[string]bool, len(saved))
	for _, name := range saved {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || seen[key] || open[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
}

// auditSourcesRead are the audit sources a read finished on (OK in every chunk),
// named from labels, in the order the read reported them.
func auditSourcesRead(results []audit.SourceResult, labels []tracingRef) []tracingRef {
	byID := make(map[string]string, len(labels))
	for _, l := range labels {
		byID[l.SourceID] = l.Label
	}
	out := []tracingRef{}
	for _, r := range results {
		if !r.OK {
			continue
		}
		label := byID[r.SourceID]
		if label == "" {
			label = tracingLabel(r.SourceID)
		}
		out = append(out, tracingRef{SourceID: r.SourceID, Label: label})
	}
	return out
}
