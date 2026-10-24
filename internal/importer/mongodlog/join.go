package mongodlog

import (
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/join"
)

// m1Tolerance is the 1 s join tolerance of rule M1: audit CreatedOn has second
// resolution, so a slow operation ending or starting within 1 s of the audit interval
// still counts as overlapping it.
var m1Tolerance = join.Tolerance{Resolution: time.Second}

// M1 joins audit intervals, [RequestInitiatedAt, CreatedOn], to slow operations from
// this importer with a 1 s tolerance. The class is unique, ambiguous (every candidate
// listed, never counted) or none. The result is a Correlation at most, never a cause.
func M1(audit []join.Interval, ev []evidence.Event) []join.Match {
	return join.Overlap(audit, ev, m1Tolerance, qualifiesM1)
}

func qualifiesM1(e evidence.Event) bool {
	return e.Source == Name && e.Kind == KindSlowOp
}

// Absence words the "no event seen" case for an audit interval. The threshold wording
// (NotObservedThreshold) is returned only when the source version, slowms, an offset
// timestamp rule and full coverage of the interval are all known, and the file shows no
// rotation, restart, sampling or threshold-change marker. Otherwise it returns the gap
// wording with a fixed sentence naming the first thing missing. A missing slow
// operation is never evidence that no operation ran.
func Absence(q evidence.SourceQuality, iv join.Interval) string {
	if gap := AbsenceGap(q, iv); gap != "" {
		return evidence.NotObservedGap(gap)
	}
	return evidence.NotObservedThreshold(SourceLabel, AbsenceThreshold(q))
}

// AbsenceGap returns the fixed sentence naming the first thing that stops a file from
// supporting the threshold wording for iv, or "" when it supports it.
func AbsenceGap(q evidence.SourceQuality, iv join.Interval) string {
	switch {
	case q.SourceVersion == "":
		return "mongod version unknown: no Build Info line in the file"
	case q.Threshold == "":
		return "slowms unknown"
	case q.TimezoneRule != clock.RuleOffsetInField:
		return "timestamp offset rule not established"
	case q.Sampling != "":
		return q.Sampling
	case q.CoverageFirst == nil || q.CoverageLast == nil ||
		iv.Start.Before(*q.CoverageFirst) || iv.End.After(*q.CoverageLast):
		return "the interval is not fully inside the log coverage"
	}
	return ""
}

// AbsenceThreshold is the threshold phrase for the absence wording. From MongoDB 8.0 a
// slow operation is logged when workingMillis, not the total duration, passes slowms, so
// the phrase says which time is compared. Earlier versions compare the total duration.
func AbsenceThreshold(q evidence.SourceQuality) string {
	const prefix = "operations slower than "
	if major(q.SourceVersion) >= 8 && strings.HasPrefix(q.Threshold, prefix) {
		return "operations whose working time (workingMillis) is over " + strings.TrimPrefix(q.Threshold, prefix)
	}
	return q.Threshold
}

// major returns the leading number of a version such as "8.0.1"; 0 when it has none.
func major(version string) int {
	n, _, _ := strings.Cut(version, ".")
	v, err := strconv.Atoi(n)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
