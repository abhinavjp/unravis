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

// Reference statistics use the whole interval; only current results need time slots.
func buildSummaryPipeline(q audit.Query, start, end time.Time, timeline bool) mongo.Pipeline {
	intervalMS := int64(q.BucketSeconds) * 1000
	timingMatch := bson.D{{Key: "RequestInitiatedAt", Value: bson.D{{Key: "$gte", Value: start}, {Key: "$lt", Value: end}}}}
	legacyMatch := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "RequestInitiatedAt", Value: bson.D{{Key: "$exists", Value: false}}}}, bson.D{{Key: "RequestInitiatedAt", Value: nil}}}}},
		bson.D{{Key: "CreatedOn", Value: bson.D{{Key: "$gte", Value: start}, {Key: "$lt", Value: end}}}},
	}}}
	firstMatch := bson.D{{Key: "$or", Value: bson.A{timingMatch, legacyMatch}}}
	if q.Controller != "" {
		firstMatch = append(firstMatch, bson.E{Key: "ControllerName", Value: q.Controller})
	}
	if q.Action != "" {
		firstMatch = append(firstMatch, bson.E{Key: "ActionName", Value: q.Action})
	}

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
	statusSets := statusNames()
	// An asynchronous request that answered 202 Accepted has only been queued:
	// its work is not done, so it is neither a success nor a timing sample.
	pending := pendingAsyncExpr(kind, statusText)
	outcome := bson.D{{Key: "$switch", Value: bson.D{
		{Key: "branches", Value: bson.A{
			bson.D{{Key: "case", Value: "$_auditPending"}, {Key: "then", Value: "unknown"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{kind, "hangfire"}}}, bson.D{{Key: "$eq", Value: bson.A{statusText, "SUCCEEDED"}}}}}}}, {Key: "then", Value: "success"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{kind, "hangfire"}}}, bson.D{{Key: "$eq", Value: bson.A{statusText, "FAILED"}}}}}}}, {Key: "then", Value: "failure"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$in", Value: bson.A{statusText, statusSets.success}}}}, {Key: "then", Value: "success"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$gte", Value: bson.A{statusNumber, 200}}}, bson.D{{Key: "$lt", Value: bson.A{statusNumber, 400}}}}}}}, {Key: "then", Value: "success"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$in", Value: bson.A{statusText, statusSets.failure}}}}, {Key: "then", Value: "failure"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$gte", Value: bson.A{statusNumber, 400}}}, bson.D{{Key: "$lt", Value: bson.A{statusNumber, 600}}}}}}}, {Key: "then", Value: "failure"}},
		}},
		{Key: "default", Value: "unknown"},
	}}}
	var duration any
	missingDuration := any(bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "$ne", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditAt"}}, "date"}}},
		bson.D{{Key: "$ne", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditCompleted"}}, "date"}}},
		bson.D{{Key: "$in", Value: bson.A{bson.D{{Key: "$type", Value: "$ApiTime"}}, bson.A{"missing", "null"}}}},
	}}})
	if q.Metric == "api" {
		duration = bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditAt"}}, "date"}}}, bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditCompleted"}}, "date"}}}}}},
			bson.D{{Key: "$convert", Value: bson.D{{Key: "input", Value: "$ApiTime"}, {Key: "to", Value: "double"}, {Key: "onError", Value: nil}, {Key: "onNull", Value: nil}}}}, nil,
		}}}
	} else {
		duration = bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditAt"}}, "date"}}}, bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditCompleted"}}, "date"}}}}}},
			bson.D{{Key: "$subtract", Value: bson.A{"$_auditCompleted", "$_auditAt"}}}, nil,
		}}}
	}
	duration = bson.D{{Key: "$cond", Value: bson.A{"$_auditPending", nil, duration}}}
	validDuration := bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$gte", Value: bson.A{"$_auditDuration", 0}}}, bson.D{{Key: "$lt", Value: bson.A{"$_auditDuration", math.MaxFloat64}}}}}}
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
	state := bson.D{{Key: "$switch", Value: bson.D{
		{Key: "branches", Value: bson.A{
			bson.D{{Key: "case", Value: "$_auditPending"}, {Key: "then", Value: "incomplete"}},
			bson.D{{Key: "case", Value: bson.D{{Key: "$eq", Value: bson.A{"$_auditDuration", nil}}}}, {Key: "then", Value: bson.D{{Key: "$cond", Value: bson.A{missingDuration, "incomplete", "invalid"}}}}},
			bson.D{{Key: "case", Value: validDuration}, {Key: "then", Value: "valid"}},
		}},
		{Key: "default", Value: "invalid"},
	}}}
	branches := bson.A{bson.D{{Key: "case", Value: bson.D{{Key: "$lt", Value: bson.A{"$_auditDuration", 1}}}}, {Key: "then", Value: 0}}}
	for i := 1; i <= 192; i++ {
		branches = append(branches, bson.D{{Key: "case", Value: bson.D{{Key: "$lt", Value: bson.A{"$_auditDuration", math.Pow(1.1, float64(i))}}}}, {Key: "then", Value: i}})
	}
	bin := bson.D{{Key: "$switch", Value: bson.D{{Key: "branches", Value: branches}, {Key: "default", Value: 193}}}}
	dateMillis := bson.D{{Key: "$toLong", Value: "$_auditAt"}}
	var bucketAt any = bson.D{{Key: "$toDate", Value: bson.D{{Key: "$subtract", Value: bson.A{dateMillis, bson.D{{Key: "$mod", Value: bson.A{dateMillis, intervalMS}}}}}}}}
	if !timeline {
		bucketAt = bson.D{{Key: "$literal", Value: start.UTC()}}
	}

	stages := mongo.Pipeline{
		bson.D{{Key: "$match", Value: firstMatch}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditAt", Value: dateOrFallback("RequestInitiatedAt", "CreatedOn")}, {Key: "_auditCompleted", Value: dateOrFallback("RequestCompletedAt", "RequestCompletedAt")}, {Key: "_auditKind", Value: kind}, {Key: "_auditPending", Value: pending}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditOutcome", Value: outcome}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditDuration", Value: duration}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditState", Value: state}, {Key: "_auditBin", Value: bin}, {Key: "_auditBucketAt", Value: bucketAt}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditAuditMs", Value: phaseValue("AuditCreateTime")}, {Key: "_auditApiMs", Value: phaseValue("ApiTime")}, {Key: "_auditPayloadMs", Value: phaseValue("PayloadRecordTime")}}}},
	}
	if q.Kind != "all" {
		stages = append(stages, bson.D{{Key: "$match", Value: bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "_auditKind", Value: q.Kind}}, bson.D{{Key: "_auditState", Value: bson.D{{Key: "$ne", Value: "valid"}}}}}}}}})
	}
	if q.Outcome != "all" {
		stages = append(stages, bson.D{{Key: "$match", Value: bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "_auditOutcome", Value: q.Outcome}}, bson.D{{Key: "_auditOutcome", Value: "unknown"}}, bson.D{{Key: "_auditState", Value: bson.D{{Key: "$ne", Value: "valid"}}}}}}}}})
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
		}}},
		bson.D{{Key: "$limit", Value: maxBucketRows}},
		bson.D{{Key: "$project", Value: bson.D{{Key: "_id", Value: 0}, {Key: "controller", Value: "$_id.controller"}, {Key: "action", Value: "$_id.action"}, {Key: "kind", Value: "$_id.kind"}, {Key: "outcome", Value: "$_id.outcome"}, {Key: "state", Value: "$_id.state"}, {Key: "at", Value: "$_id.at"}, {Key: "bin", Value: "$_id.bin"}, {Key: "count", Value: 1}, {Key: "sumMs", Value: 1}, {Key: "sumAuditMs", Value: 1}, {Key: "sumApiMs", Value: 1}, {Key: "sumPayloadMs", Value: 1}, {Key: "nAudit", Value: 1}, {Key: "nApi", Value: 1}, {Key: "nPayload", Value: 1}, {Key: "pendingAsync", Value: 1}, {Key: "observedPartial", Value: 1}, {Key: "minMs", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$minMs", 0}}}}, {Key: "maxMs", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$maxMs", 0}}}}}}},
	)
	return stages
}

func statusNames() struct{ success, failure bson.A } {
	return struct{ success, failure bson.A }{
		success: stringsToBSON("OK", "CREATED", "ACCEPTED", "NONAUTHORITATIVEINFORMATION", "NOCONTENT", "RESETCONTENT", "PARTIALCONTENT", "MULTISTATUS", "ALREADYREPORTED", "IMUSED", "MULTIPLECHOICES", "MOVEDPERMANENTLY", "FOUND", "SEEOTHER", "NOTMODIFIED", "USEPROXY", "TEMPORARYREDIRECT", "PERMANENTREDIRECT"),
		failure: stringsToBSON("BADREQUEST", "UNAUTHORIZED", "PAYMENTREQUIRED", "FORBIDDEN", "NOTFOUND", "METHODNOTALLOWED", "NOTACCEPTABLE", "PROXYAUTHENTICATIONREQUIRED", "REQUESTTIMEOUT", "CONFLICT", "GONE", "LENGTHREQUIRED", "PRECONDITIONFAILED", "PAYLOADTOOLARGE", "URITOOLONG", "UNSUPPORTEDMEDIATYPE", "RANGENOTSATISFIABLE", "EXPECTATIONFAILED", "IMATEAPOT", "MISDIRECTEDREQUEST", "UNPROCESSABLEENTITY", "LOCKED", "FAILEDDEPENDENCY", "TOOEARLY", "UPGRADEREQUIRED", "PRECONDITIONREQUIRED", "TOOMANYREQUESTS", "REQUESTHEADERFIELDSTOOLARGE", "UNAVAILABLEFORLEGALREASONS", "INTERNALSERVERERROR", "NOTIMPLEMENTED", "BADGATEWAY", "SERVICEUNAVAILABLE", "GATEWAYTIMEOUT", "HTTPVERSIONNOTSUPPORTED", "VARIANTALSONEGOTIATES", "INSUFFICIENTSTORAGE", "LOOPDETECTED", "NOTEXTENDED", "NETWORKAUTHENTICATIONREQUIRED"),
	}
}

func stringsToBSON(values ...string) bson.A {
	result := make(bson.A, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
