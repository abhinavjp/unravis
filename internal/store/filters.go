package store

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// recordMatch returns the raw-field conditions of q's record filters. They go
// into the first $match of every pipeline, beside the indexed time range, so the
// server narrows documents before any $set stage runs and MongoDB 3.4 applies
// them too. Only $in, $regex and $options are used: no $expr, $regexMatch,
// $convert or other operator 3.4 lacks. Operation is not read here; Normalized
// folds it into ControllerName and ActionName.
func recordMatch(q audit.Query) bson.D {
	match := bson.D{}
	if len(q.UserIDs) > 0 {
		// UserId is stored as a number in current data and as numeric text in
		// older rows, so both forms are matched.
		ids := make(bson.A, 0, 2*len(q.UserIDs))
		for _, id := range q.UserIDs {
			ids = append(ids, id, strconv.Itoa(id))
		}
		match = append(match, bson.E{Key: "UserId", Value: bson.D{{Key: "$in", Value: ids}}})
	}
	if len(q.UIVersions) > 0 {
		match = append(match, bson.E{Key: "PayrollUIVersion", Value: bson.D{{Key: "$in", Value: stringsToBSON(q.UIVersions...)}}})
	}
	if q.UserAgentContains != "" {
		match = append(match, bson.E{Key: "UserAgent", Value: containsMatch(q.UserAgentContains)})
	}
	if len(q.UserIPs) > 0 {
		match = append(match, bson.E{Key: "UserIp", Value: bson.D{{Key: "$in", Value: stringsToBSON(q.UserIPs...)}}})
	}
	if q.URLContains != "" {
		match = append(match, bson.E{Key: "RequestMainUrl", Value: containsMatch(q.URLContains)})
	}
	if q.AuditID != "" {
		if id, err := bson.ObjectIDFromHex(q.AuditID); err == nil {
			match = append(match, bson.E{Key: "_id", Value: id})
		} else {
			// Validation already rejects this. If one reaches here, match
			// nothing instead of dropping the condition and widening the query.
			match = append(match, bson.E{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{}}}})
		}
	}
	if len(q.StatusCodes) > 0 {
		// StatusCode holds the .NET enum name or its number. A name matches
		// case-insensitively, as text; a number also matches a numeric value,
		// but only when written canonically. "0404" or "+404" would otherwise
		// match the number 404 here and nothing on the Go paths, which compare
		// the text.
		codes := make(bson.A, 0, 2*len(q.StatusCodes))
		for _, code := range q.StatusCodes {
			code = strings.TrimSpace(code)
			codes = append(codes, bson.Regex{Pattern: `^\s*` + regexp.QuoteMeta(code) + `\s*$`, Options: "i"})
			if number, err := strconv.Atoi(code); err == nil && strconv.Itoa(number) == code {
				codes = append(codes, number)
			}
		}
		match = append(match, bson.E{Key: "StatusCode", Value: bson.D{{Key: "$in", Value: codes}}})
	}
	return match
}

// containsMatch is a case-insensitive substring condition. The text is escaped,
// so a dot or parenthesis in it is literal and cannot change the pattern.
func containsMatch(text string) bson.D {
	return bson.D{{Key: "$regex", Value: regexp.QuoteMeta(text)}, {Key: "$options", Value: "i"}}
}

// recordPostMatch returns the conditions on derived fields, which exist only
// after the pipeline's $set stages. It must run after _auditState and
// _auditPending are set. The MongoDB 3.4 adapter does not use it; it applies the
// same rule in Go with audit.StateMatches.
func recordPostMatch(q audit.Query) bson.D {
	post := bson.D{}
	// A latency range implies the complete state and an age range the incomplete
	// one; EffectiveCompletion returns the requested Completion when there is one.
	if stage, ok := stateMatchStage(audit.EffectiveCompletion(q)); ok {
		post = append(post, stage...)
	}
	for _, field := range rangeFields(q) {
		post = append(post, bson.E{Key: field.name, Value: rangeBounds(field.r)})
	}
	return post
}

// rangeField is one bounded range filter and the derived field it is matched on.
type rangeField struct {
	name string
	r    *audit.Range
	// expr computes the field from the document and the stages' derived fields.
	expr func(q audit.Query) any
}

// rangeFields lists the bounded ranges of q in a fixed order. A range that
// constrains nothing is skipped.
func rangeFields(q audit.Query) []rangeField {
	all := []rangeField{
		{"_auditRangeObserved", q.ObservedMs, func(audit.Query) any { return observedDurationExpr() }},
		{"_auditRangeApi", q.ApiTimeMs, func(audit.Query) any { return numberExpr("$ApiTime") }},
		{"_auditRangeAuditCreate", q.AuditCreateTimeMs, func(audit.Query) any { return numberExpr("$AuditCreateTime") }},
		// A payload size that is missing or not a number stays null here, never 0,
		// so the record cannot match a bounded range.
		{"_auditRangeReq", q.RequestKB, func(audit.Query) any { return numberExpr("$RequestLengthInKiloBytes") }},
		{"_auditRangeResp", q.ResponseKB, func(audit.Query) any { return numberExpr("$ResponseLengthInKiloBytes") }},
		{"_auditRangeAge", q.IncompleteAgeMs, incompleteAgeExpr},
	}
	out := make([]rangeField, 0, len(all))
	for _, f := range all {
		if f.r != nil && (f.r.Min != nil || f.r.Max != nil) {
			out = append(out, f)
		}
	}
	return out
}

// rangeBounds is the $match condition of one range on a derived numeric field.
// The lower bound is always present (zero when the range has no minimum) and
// the upper bound falls back to below MaxFloat64, so null, missing, negative,
// NaN and infinite values never match, as audit.Range.contains reads them.
func rangeBounds(r *audit.Range) bson.D {
	lower := 0.0
	if r.Min != nil && *r.Min > 0 {
		lower = *r.Min
	}
	if r.Max != nil {
		return bson.D{{Key: "$gte", Value: lower}, {Key: "$lte", Value: *r.Max}}
	}
	return bson.D{{Key: "$gte", Value: lower}, {Key: "$lt", Value: math.MaxFloat64}}
}

// incompleteAgeExpr is the age of a record that has not completed, in
// milliseconds: the injected clock minus its start (CreatedOn when it has no
// start). A completed record has no age, except a pending async request, which
// was accepted but not run. The clock is a literal date, never the server's.
func incompleteAgeExpr(q audit.Query) any {
	now := q.Now
	if now.IsZero() {
		now = time.Now()
	}
	started := bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditAt"}}, "date"}}}
	notCompleted := bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "$ne", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditCompleted"}}, "date"}}},
		"$_auditPending",
	}}}
	elapsed := bson.D{{Key: "$subtract", Value: bson.A{bson.D{{Key: "$literal", Value: now.UTC().Truncate(time.Millisecond)}}, "$_auditAt"}}}
	return bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$and", Value: bson.A{started, notCompleted}}}, elapsed, nil}}}
}

// recordRangeSet is the $set stage that derives each bounded range's field. It is
// empty when q has no range, and must run after _auditAt, _auditCompleted and
// _auditPending are set.
func recordRangeSet(q audit.Query) bson.D {
	set := bson.D{}
	for _, f := range rangeFields(q) {
		set = append(set, bson.E{Key: f.name, Value: f.expr(q)})
	}
	return set
}

// stateMatchStage is the one Mongo form of a row-state filter on the derived
// fields, used by the request drill-down and by the completion record filter.
// It must select the rows audit.StateMatches selects. Empty, "all" and unknown
// states have no stage.
func stateMatchStage(state string) (bson.D, bool) {
	switch state {
	case audit.StateComplete:
		return bson.D{{Key: "_auditState", Value: "valid"}}, true
	case audit.StateIncomplete, audit.StateInvalid:
		return bson.D{{Key: "_auditState", Value: state}}, true
	case audit.StatePending:
		return bson.D{{Key: "_auditPending", Value: true}}, true
	case audit.StateUnclassified:
		return bson.D{{Key: "_auditOutcome", Value: "unknown"}}, true
	}
	return nil, false
}

// appendRecordPostMatch adds recordPostMatch as a $match stage when it is not
// empty.
func appendRecordPostMatch(stages []bson.D, q audit.Query) []bson.D {
	if set := recordRangeSet(q); len(set) > 0 {
		stages = append(stages, bson.D{{Key: "$set", Value: set}})
	}
	if post := recordPostMatch(q); len(post) > 0 {
		stages = append(stages, bson.D{{Key: "$match", Value: post}})
	}
	return stages
}
