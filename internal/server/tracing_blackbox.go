package server

import (
	"context"
	"math"
	"strings"
	"time"

	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
)

// BBX-5-16 (BL-103): the Blackbox source of the database lane. When the case
// uses a Blackbox whose samples carry SQL host counters for the window, the SQL
// lane is drawn from that sampling: activity blocks from BatchRequestsPerSec,
// source key "blackbox", the fixed label and the first-sample timestamp. No
// query text, no instance names, no error text. Without such data the lane falls
// through to the existing held / qs-file / api-seen / not-recorded chain
// unchanged; it is never a zero lane. The samples are read on demand through the
// same sealed-list reader the host lane uses; nothing here writes or polls.

const (
	tracingReasonBlackbox = "blackbox"
	// tracingBlackboxLabel is the fixed sentence; the date is the recording's
	// first sample. The sampling cadence is the Blackbox's fixed 15 s.
	tracingBlackboxLabel = "Blackbox sampling every 15 s since "
)

// bbSQLRate sums the batch requests/sec of the instances that recorded it. ok is
// false when no instance carries the reading: a missing reading is never a 0.
func bbSQLRate(s blackbox.Sample) (float64, bool) {
	if s.SQLHost == nil {
		return 0, false
	}
	rate, any := 0.0, false
	for _, inst := range s.SQLHost.Instances {
		v := inst.BatchRequestsPerSec
		if v == nil || *v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0) {
			continue
		}
		rate += *v
		any = true
	}
	return rate, any
}

// bbLaneEntry finds the saved Blackbox the case names: the first blackbox source
// tag, matched by name in the saved list (the host lane's rule). A case without
// one, or a name no longer listed, has no Blackbox lane source.
func (h *handler) bbLaneEntry(ctx context.Context, c casestore.Case) (bbsaved.Entry, bool) {
	if h.bb == nil {
		return bbsaved.Entry{}, false
	}
	t, ok := c.(casestore.Tagger)
	if !ok {
		return bbsaved.Entry{}, false
	}
	tags, err := t.Sources(ctx)
	if err != nil {
		return bbsaved.Entry{}, false
	}
	name := ""
	for _, tag := range tags {
		if tag.Kind == blackboxTagKind {
			if n := strings.TrimSpace(tag.Label); n != "" {
				name = n
				break
			}
		}
	}
	if name == "" {
		return bbsaved.Entry{}, false
	}
	entries, err := h.bb.deps.Store.List()
	if err != nil {
		return bbsaved.Entry{}, false
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name, name) {
			return e, true
		}
	}
	return bbsaved.Entry{}, false
}

// bbLaneSamples reads the window's samples of one saved Blackbox and keeps only
// those carrying a batch-request reading. first is the recording's first sample
// (the manifest's, else the window's oldest). ok is false when nothing could be
// read or no sample carries the reading: the lane then falls through, quietly.
func (h *handler) bbLaneSamples(ctx context.Context, e bbsaved.Entry, axis tracingAxis) (kept []blackbox.Sample, interval int, first time.Time, ok bool) {
	ctx, cancel := context.WithTimeout(ctx, bbReadTimeout)
	defer cancel()
	src, err := h.bb.source(e)
	if err != nil {
		return nil, 0, time.Time{}, false
	}
	rd, err := blackbox.ProbeSource(ctx, src, h.bb.now)
	if err != nil {
		return nil, 0, time.Time{}, false
	}
	samples, _, err := blackbox.ReadSamples(ctx, src, rd.Manifest, axis.Start, axis.End)
	if err != nil {
		return nil, 0, time.Time{}, false
	}
	for _, s := range samples {
		if _, has := bbSQLRate(s); has {
			kept = append(kept, s)
		}
	}
	if len(kept) == 0 {
		return nil, 0, time.Time{}, false
	}
	interval = rd.Manifest.IntervalSeconds
	if interval <= 0 {
		interval = blackbox.DefaultIntervalSecond
	}
	first = kept[0].T
	if rd.Manifest.FirstSample != nil && !rd.Manifest.FirstSample.IsZero() {
		first = rd.Manifest.FirstSample.UTC()
	}
	return kept, interval, first, true
}

// blackboxSQLLane fills the SQL lane from the case's Blackbox sampling and
// reports true, or leaves the lane untouched and reports false so the existing
// source chain runs unchanged. Each block counts the batch requests of its
// samples: the recorded per-second rate times the sampling interval, summed
// over the instances that recorded it. A read is bounded by bbReadTimeout and
// the reader's own file cap; nothing is kept between requests.
func (h *handler) blackboxSQLLane(ctx context.Context, c casestore.Case, axis tracingAxis, lane tracingLane) (tracingLane, bool) {
	if axis.WidthMs <= 0 {
		return lane, false
	}
	e, ok := h.bbLaneEntry(ctx, c)
	if !ok {
		return lane, false
	}
	kept, interval, first, ok := h.bbLaneSamples(ctx, e, axis)
	if !ok {
		return lane, false
	}
	perCell := map[int]int64{}
	var total int64
	for _, s := range kept {
		rate, _ := bbSQLRate(s)
		n := int64(math.Round(rate * float64(interval)))
		k := int((s.T.UnixMilli() - axis.AxisStart.UnixMilli()) / axis.WidthMs)
		if k < 0 || k >= axis.Buckets {
			continue
		}
		perCell[k] += n
		total += n
	}
	since := first.UTC()
	lane.Label = "SQL Server"
	lane.State = calltrace.StateEvents
	lane.Origin, lane.Consistency = tracingOriginCase, tracingConsStored
	lane.AsOf = kept[len(kept)-1].T.UTC()
	lane.Complete, lane.Truncated = true, false
	lane.ReasonCode, lane.Reason = tracingReasonBlackbox, ""
	lane.Scanned = total
	lane.Sources = []tracingRef{{SourceID: tracingReasonBlackbox, Label: tracingBlackboxLabel + since.Format("2 Jan 2006")}}
	lane.Buckets = tracingBuckets(axis, perCell, nil)
	lane.Events = []tracingEvent{} // numeric activity only: no query text
	lane.Hidden = 0
	lane.BlockSeconds = 0
	lane.Since = &since
	lane.ImportedFrom, lane.ImportedTo, lane.ImportedPartial = nil, nil, false
	return lane, true
}