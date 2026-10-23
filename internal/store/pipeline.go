package store

import (
	"math"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const maxBucketRows = audit.MaxSummaryRows + 1

// buildPipeline returns a bounded, read-only aggregation for one cohort interval.
func buildPipeline(q audit.Query, start, end time.Time) mongo.Pipeline {
	return buildSummaryPipeline(q, start, end, true)
}

// pendingAsyncExpr is the single definition of a pending async request, shared
// by the summary and request-page pipelines: the derived cohort kind is
// asynchronous (so a Hangfire user agent keeps a row in the hangfire cohort)
// and the status text is ACCEPTED. The legacy Go path applies the same rule to
// record.Kind.
func pendingAsyncExpr(kind, statusText bson.D) bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{kind, "asynchronous"}}},
		bson.D{{Key: "$eq", Value: bson.A{statusText, "ACCEPTED"}}},
	}}}
}

// auditDurationExpr is the metric duration of one row (the total from initiated
// to completed, or ApiTime for the api metric), null when either time is missing
// and for a pending async request, whose work has not run. The summary and the
// drill-down pipelines both derive a row's state from it, so a count and the rows
// behind it always agree.
func auditDurationExpr(metric string) bson.D {
	var duration any
	timed := auditTimedExpr()
	if metric == "api" {
		duration = bson.D{{Key: "$cond", Value: bson.A{timed,
			bson.D{{Key: "$convert", Value: bson.D{{Key: "input", Value: "$ApiTime"}, {Key: "to", Value: "double"}, {Key: "onError", Value: nil}, {Key: "onNull", Value: nil}}}}, nil,
		}}}
	} else {
		duration = bson.D{{Key: "$cond", Value: bson.A{timed,
			bson.D{{Key: "$subtract", Value: bson.A{"$_auditCompleted", "$_auditAt"}}}, nil,
		}}}
	}
	return bson.D{{Key: "$cond", Value: bson.A{"$_auditPending", nil, duration}}}
}

// auditTimedExpr is true when both effective timestamps (_auditAt, the start or
// its CreatedOn fallback, and _auditCompleted) are dates.
func auditTimedExpr() bson.D {
	return bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditAt"}}, "date"}}}, bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditCompleted"}}, "date"}}}}}}
}

// observedDurationExpr is the observed time of a row, completed minus start in
// milliseconds (AuditCreateTime + ApiTime), null when either timestamp is
// missing. The request pipeline shows it as durationMs and the observed range
// filter reads it; both use this one expression.
func observedDurationExpr() bson.D {
	return bson.D{{Key: "$cond", Value: bson.A{auditTimedExpr(), bson.D{{Key: "$subtract", Value: bson.A{"$_auditCompleted", "$_auditAt"}}}, nil}}}
}

// auditStateExpr derives a row's state from its metric duration field: a pending
// async request or a row that lacks a completion time or ApiTime is incomplete, a
// row whose duration is unusable is invalid, and the rest are valid.
func auditStateExpr(durationField string) bson.D {
	missingDuration := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "$ne", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditAt"}}, "date"}}},
		bson.D{{Key: "$ne", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditCompleted"}}, "date"}}},
		bson.D{{Key: "$in", Value: bson.A{bson.D{{Key: "$type", Value: "$ApiTime"}}, bson.A{"missing", "null"}}}},
	}}}
	validDuration := bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$gte", Value: bson.A{durationField, 0}}}, bson.D{{Key: "$lt", Value: bson.A{durationField, math.MaxFloat64}}}}}}
	return bson.D{{Key: "$switch", Value: bson.D{
		{Key: "branches", Value: bson.A{
			bson.D{{Key: "case", Value: "$_auditPending"}, {Key: "then", Value: "incomplete"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$eq", Value: bson.A{durationField, nil}}}}, {Key: "then", Value: bson.D{{Key: "$cond", Value: bson.A{missingDuration, "incomplete", "invalid"}}}}},
			bson.D{{Key: "case", Value: validDuration}, {Key: "then", Value: "valid"}},
		}},
		{Key: "default", Value: "invalid"},
	}}}
}

// auditBinExpr is the histogram bin of a duration field: bin 0 below one
// millisecond, then 192 bins growing by 10 percent, and a last bin above them.
// The summary and the breakdown both bin through it, so a latency percentile
// means the same in either.
func auditBinExpr(durationField string) bson.D {
	branches := bson.A{bson.D{{Key: "case", Value: bson.D{{Key: "$lt", Value: bson.A{durationField, 1}}}}, {Key: "then", Value: 0}}}
	for i := 1; i <= 192; i++ {
		branches = append(branches, bson.D{{Key: "case", Value: bson.D{{Key: "$lt", Value: bson.A{durationField, math.Pow(1.1, float64(i))}}}}, {Key: "then", Value: i}})
	}
	return bson.D{{Key: "$switch", Value: bson.D{{Key: "branches", Value: branches}, {Key: "default", Value: 193}}}}
}
// Reference statistics use the whole interval; only current results need time slots.
func buildSummaryPipeline(q audit.Query, start, end time.Time, timeline bool) mongo.Pipeline {
	intervalMS := int64(q.BucketSeconds) * 1000
	timingMatch := bson.D{{Key: "RequestInitiatedAt", Value: bson.D{{Key: "$gte", Value: start}, {Key: "$lt", Value: end}}}}
	legacyMatch := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "RequestInitiatedAt", Value: bson.D{{Key: "$exists", Value: false}}}}, bson.D{{Key: "RequestInitiatedAt", Value: nil}}}}},
		bson.D{{Key: "CreatedOn", Value: bson.D{{Key: "$gte", Value: start}, {Key: "$lt", Value: end}}}},
	}}}
	q = q.Normalized()
	firstMatch := bson.D{{Key: "$or", Value: bson.A{timingMatch, legacyMatch}}}
	if q.Controller != "" {
		firstMatch = append(firstMatch, bson.E{Key: "ControllerName", Value: q.Controller})
	}
	if q.Action != "" {
		firstMatch = append(firstMatch, bson.E{Key: "ActionName", Value: q.Action})
	}
	firstMatch = append(firstMatch, recordMatch(q)...)

	dateOrFallback := func(primary, fallback string) bson.D {
		return bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + primary}}, "date"}}}, "$" + primary,
			bson.D{{Key: "$cond", Value: bson.A{
				bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + fallback}}, "date"}}}, "$" + fallback, nil,
			}}},
		}}}
	}
	polling := bson.D{{Key: "$toLower", Value: bson.D{{Key: "$trim", Value: bson.D{{Key: "input", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$RequestPollingType", ""}}}}}}}}}
	userAgent := bson.D{{Key: "$toLower", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$UserAgent", ""}}}}}
	kind := bson.D{{Key: "$switch", Value: bson.D{
		{Key: "branches", Value: bson.A{
			bson.D{{Key: "case", Value: bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{polling, "hangfire"}}}, bson.D{{Key: "$eq", Value: bson.A{userAgent, "hangfire"}}}}}}}, {Key: "then", Value: "hangfire"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$eq", Value: bson.A{polling, "synchronous"}}}}, {Key: "then", Value: "synchronous"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$eq", Value: bson.A{polling, "asynchronous"}}}}, {Key: "then", Value: "asynchronous"}},
		}},
		{Key: "default", Value: "unknown"},
	}}}
	statusText := bson.D{{Key: "$toUpper", Value: bson.D{{Key: "$convert", Value: bson.D{{Key: "input", Value: "$StatusCode"}, {Key: "to", Value: "string"}, {Key: "onError", Value: ""}, {Key: "onNull", Value: ""}}}}}}
	statusNumber := bson.D{{Key: "$convert", Value: bson.D{{Key: "input", Value: "$StatusCode"}, {Key: "to", Value: "int"}, {Key: "onError", Value: nil}, {Key: "onNull", Value: nil}}}}
	// An asynchronous request that answered 202 Accepted has only been queued:
	// its work is not done, so it is neither a success nor a timing sample.
	pending := pendingAsyncExpr(kind, statusText)
	statusClass := statusClassExpr(kind, statusText, statusNumber)
	outcome := outcomeFromClassExpr()
	duration := auditDurationExpr(q.Metric)
	// Phase times use the same $convert-to-double extraction as ApiTime (the
	// writer stores TimeSpan.TotalMilliseconds as a double), so the sums are in
	// milliseconds like the total. A phase counts only for a valid document and
	// only when it is a finite non-negative number; null and missing never are.
	phaseValue := func(field string) bson.D {
		return bson.D{{Key: "$convert", Value: bson.D{{Key: "input", Value: "$" + field}, {Key: "to", Value: "double"}, {Key: "onError", Value: nil}, {Key: "onNull", Value: nil}}}}
	}
	phasePresent := func(field string) bson.D {
		return bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$_auditState", "valid"}}},
			bson.D{{Key: "$gte", Value: bson.A{field, 0}}},
			bson.D{{Key: "$lt", Value: bson.A{field, math.MaxFloat64}}},
		}}}
	}
	phaseSum := func(field string) bson.D {
		return bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{phasePresent(field), field, 0}}}}}
	}
	phaseCount := func(field string) bson.D {
		return bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{phasePresent(field), 1, 0}}}}}
	}
	// Observed time is AuditCreateTime + ApiTime = completed - initiated. A valid
	// document without RequestInitiatedAt fell back to CreatedOn, so its figure
	// is partial. The api metric never measures that interval.
	observedPartial := bson.D{{Key: "$sum", Value: 0}}
	if q.Metric != "api" {
		observedPartial = bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$and", Value: bson.A{
				bson.D{{Key: "$eq", Value: bson.A{"$_auditState", "valid"}}},
				bson.D{{Key: "$ne", Value: bson.A{bson.D{{Key: "$type", Value: "$RequestInitiatedAt"}}, "date"}}},
			}}}, 1, 0,
		}}}}}
	}
	state := auditStateExpr("$_auditDuration")
	bin := auditBinExpr("$_auditDuration")
	dateMillis := bson.D{{Key: "$toLong", Value: "$_auditAt"}}
	var bucketAt any = bson.D{{Key: "$toDate", Value: bson.D{{Key: "$subtract", Value: bson.A{dateMillis, bson.D{{Key: "$mod", Value: bson.A{dateMillis, intervalMS}}}}}}}}
	if !timeline {
		bucketAt = bson.D{{Key: "$literal", Value: start.UTC()}}
	}

	stages := mongo.Pipeline{
		bson.D{{Key: "$match", Value: firstMatch}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditAt", Value: dateOrFallback("RequestInitiatedAt", "CreatedOn")}, {Key: "_auditCompleted", Value: dateOrFallback("RequestCompletedAt", "RequestCompletedAt")}, {Key: "_auditKind", Value: kind}, {Key: "_auditPending", Value: pending}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_statusClass", Value: statusClass}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditOutcome", Value: outcome}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditDuration", Value: duration}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditState", Value: state}, {Key: "_auditBin", Value: bin}, {Key: "_auditBucketAt", Value: bucketAt}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditAuditMs", Value: phaseValue("AuditCreateTime")}, {Key: "_auditApiMs", Value: phaseValue("ApiTime")}, {Key: "_auditPayloadMs", Value: phaseValue("PayloadRecordTime")}}}},
	}
	stages = appendRecordPostMatch(stages, q)
	if q.Kind != "all" {
		stages = append(stages, bson.D{{Key: "$match", Value: kindFilterMatch(q.Kind)}})
	}
	if q.Outcome != "all" {
		stages = append(stages, bson.D{{Key: "$match", Value: outcomeFilterMatch(q.Outcome)}})
	}
	stages = append(stages,
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: bson.D{{Key: "controller", Value: "$ControllerName"}, {Key: "action", Value: "$ActionName"}, {Key: "kind", Value: "$_auditKind"}, {Key: "outcome", Value: "$_auditOutcome"}, {Key: "state", Value: "$_auditState"}, {Key: "at", Value: "$_auditBucketAt"}, {Key: "bin", Value: "$_auditBin"}}},
			{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
			{Key: "sumMs", Value: bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$_auditState", "valid"}}}, "$_auditDuration", 0}}}}}},
			{Key: "minMs", Value: bson.D{{Key: "$min", Value: bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$_auditState", "valid"}}}, "$_auditDuration", nil}}}}}},
			{Key: "maxMs", Value: bson.D{{Key: "$max", Value: bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$_auditState", "valid"}}}, "$_auditDuration", nil}}}}}},
			{Key: "sumAuditMs", Value: phaseSum("$_auditAuditMs")},
			{Key: "sumApiMs", Value: phaseSum("$_auditApiMs")},
			{Key: "sumPayloadMs", Value: phaseSum("$_auditPayloadMs")},
			{Key: "nAudit", Value: phaseCount("$_auditAuditMs")},
			{Key: "nApi", Value: phaseCount("$_auditApiMs")},
			{Key: "nPayload", Value: phaseCount("$_auditPayloadMs")},
			{Key: "pendingAsync", Value: bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{"$_auditPending", 1, 0}}}}}},
			{Key: "observedPartial", Value: observedPartial},
			{Key: "c4xx", Value: statusClassCount(audit.StatusClientError)},
			{Key: "c5xx", Value: statusClassCount(audit.StatusServerError)},
			{Key: "cHangfireFailed", Value: statusClassCount(audit.StatusHangfireFailed)},
		}}},
		bson.D{{Key: "$limit", Value: maxBucketRows}},
		bson.D{{Key: "$project", Value: bson.D{{Key: "_id", Value: 0}, {Key: "controller", Value: "$_id.controller"}, {Key: "action", Value: "$_id.action"}, {Key: "kind", Value: "$_id.kind"}, {Key: "outcome", Value: "$_id.outcome"}, {Key: "state", Value: "$_id.state"}, {Key: "at", Value: "$_id.at"}, {Key: "bin", Value: "$_id.bin"}, {Key: "count", Value: 1}, {Key: "sumMs", Value: 1}, {Key: "sumAuditMs", Value: 1}, {Key: "sumApiMs", Value: 1}, {Key: "sumPayloadMs", Value: 1}, {Key: "nAudit", Value: 1}, {Key: "nApi", Value: 1}, {Key: "nPayload", Value: 1}, {Key: "pendingAsync", Value: 1}, {Key: "observedPartial", Value: 1}, {Key: "c4xx", Value: 1}, {Key: "c5xx", Value: 1}, {Key: "cHangfireFailed", Value: 1}, {Key: "minMs", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$minMs", 0}}}}, {Key: "maxMs", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$maxMs", 0}}}}}}},
	)
	return stages
}

// statusClassNameSets holds the upper-cased .NET status names of each numeric class,
// taken from audit.StatusNames so the server pipeline and the Go classifier share
// one table.
type statusClassNameSets struct{ success, redirect, clientError, serverError bson.A }

func statusClassSets() statusClassNameSets {
	toBSON := func(class string) bson.A {
		names := audit.StatusNames(class)
		return stringsToBSON(names...)
	}
	return statusClassNameSets{
		success:     toBSON(audit.StatusSuccess),
		redirect:    toBSON(audit.StatusRedirect),
		clientError: toBSON(audit.StatusClientError),
		serverError: toBSON(audit.StatusServerError),
	}
}

// statusClassExpr is the server-side twin of audit.StatusClass: pending first,
// then the hangfire Succeeded/Failed statuses, then the status name or number by
// range. Anything unmatched, including a missing status, is unknown. It reads
// $_auditPending, so it must run in a $set after the stage that defines it.
func statusClassExpr(kind, statusText, statusNumber bson.D) bson.D {
	sets := statusClassSets()
	hangfire := func(status string) bson.D {
		return bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{kind, "hangfire"}}}, bson.D{{Key: "$eq", Value: bson.A{statusText, status}}}}}}
	}
	named := func(set bson.A) bson.D { return bson.D{{Key: "$in", Value: bson.A{statusText, set}}} }
	ranged := func(from, to int) bson.D {
		return bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$gte", Value: bson.A{statusNumber, from}}}, bson.D{{Key: "$lt", Value: bson.A{statusNumber, to}}}}}}
	}
	branch := func(condition any, class string) bson.D {
		return bson.D{{Key: "case", Value: condition}, {Key: "then", Value: class}}
	}
	return bson.D{{Key: "$switch", Value: bson.D{
		{Key: "branches", Value: bson.A{
			branch("$_auditPending", audit.StatusPending),
			branch(hangfire("SUCCEEDED"), audit.StatusSuccess),
			branch(hangfire("FAILED"), audit.StatusHangfireFailed),
			branch(named(sets.success), audit.StatusSuccess),
			branch(ranged(200, 300), audit.StatusSuccess),
			branch(named(sets.redirect), audit.StatusRedirect),
			branch(ranged(300, 400), audit.StatusRedirect),
			branch(named(sets.clientError), audit.StatusClientError),
			branch(ranged(400, 500), audit.StatusClientError),
			branch(named(sets.serverError), audit.StatusServerError),
			branch(ranged(500, 600), audit.StatusServerError),
		}},
		{Key: "default", Value: audit.StatusUnknown},
	}}}
}

// outcomeFromClassExpr folds $_statusClass into the coarse cohort outcome the
// same way audit.ClassOutcome does: success and redirect succeed; 4xx, 5xx and a
// failed hangfire job fail; pending and unrecognised statuses are unknown.
func outcomeFromClassExpr() bson.D {
	class := "$_statusClass"
	return bson.D{{Key: "$switch", Value: bson.D{
		{Key: "branches", Value: bson.A{
			bson.D{{Key: "case", Value: bson.D{{Key: "$in", Value: bson.A{class, bson.A{audit.StatusSuccess, audit.StatusRedirect}}}}}, {Key: "then", Value: "success"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$in", Value: bson.A{class, bson.A{audit.StatusClientError, audit.StatusServerError, audit.StatusHangfireFailed}}}}}, {Key: "then", Value: "failure"}},
		}},
		{Key: "default", Value: "unknown"},
	}}}
}

// statusClassCount is a $group accumulator that counts rows of one status class.
// It is never a group key, so the cohort key stays (source, controller, action,
// kind, outcome).
func statusClassCount(class string) bson.D {
	return bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$_statusClass", class}}}, 1, 0}}}}}
}

// kindFilterMatch is the summary $match for a query kind. A valid row (a timed
// sample) needs a derived kind that belongs to the query kind. A row that is not
// valid is kept when its kind can belong to it: an unknown kind, usually an
// incomplete row without a polling type yet, stays under synchronous,
// asynchronous and http but never under hangfire. Hangfire detection itself
// is unchanged.
func kindFilterMatch(kind string) bson.D {
	inKinds := func(kinds []string) bson.D {
		return bson.D{{Key: "$in", Value: stringsToBSON(kinds...)}}
	}
	return bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "_auditState", Value: "valid"}, {Key: "_auditKind", Value: inKinds(audit.KindMembers(kind))}},
		bson.D{{Key: "_auditState", Value: bson.D{{Key: "$ne", Value: "valid"}}}, {Key: "_auditKind", Value: inKinds(audit.KindCompatibleMembers(kind))}},
	}}}
}

// outcomeFilterMatch is the summary $match for a query outcome filter. success
// and failure keep every row that is unknown or not valid, so the quality counts
// stay honest. client_error and server_error select by status class instead: a
// row whose class is known but outside the filter is dropped whatever its state,
// so an incomplete 500 never sits in a client_error cohort. Only rows whose
// outcome is unknown (class unknown or pending) are kept besides the match.
func outcomeFilterMatch(outcome string) bson.D {
	unknown := bson.D{{Key: "_auditOutcome", Value: "unknown"}}
	if audit.IsStatusClassOutcome(outcome) {
		return bson.D{{Key: "$or", Value: bson.A{bson.D{outcomeFilterElem(outcome)}, unknown}}}
	}
	return bson.D{{Key: "$or", Value: bson.A{bson.D{outcomeFilterElem(outcome)}, unknown, bson.D{{Key: "_auditState", Value: bson.D{{Key: "$ne", Value: "valid"}}}}}}}
}

// outcomeFilterElem is the $match condition for a query outcome filter. success
// and failure test the coarse outcome; client_error selects 4xx only and
// server_error selects 5xx plus failed hangfire jobs.
func outcomeFilterElem(outcome string) bson.E {
	switch outcome {
	case audit.StatusClientError:
		return bson.E{Key: "_statusClass", Value: audit.StatusClientError}
	case audit.StatusServerError:
		return bson.E{Key: "_statusClass", Value: bson.D{{Key: "$in", Value: bson.A{audit.StatusServerError, audit.StatusHangfireFailed}}}}
	}
	return bson.E{Key: "_auditOutcome", Value: outcome}
}

func stringsToBSON(values ...string) bson.A {
	result := make(bson.A, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
