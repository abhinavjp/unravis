package store

import (
	"regexp"
	"strconv"
	"strings"

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
		}
	}
	if len(q.StatusCodes) > 0 {
		// StatusCode holds the .NET enum name or its number. A name matches
		// case-insensitively, as text; a number also matches a numeric value.
		codes := make(bson.A, 0, 2*len(q.StatusCodes))
		for _, code := range q.StatusCodes {
			code = strings.TrimSpace(code)
			codes = append(codes, bson.Regex{Pattern: `^\s*` + regexp.QuoteMeta(code) + `\s*$`, Options: "i"})
			if number, err := strconv.Atoi(code); err == nil {
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
	switch q.Completion {
	case audit.StateComplete:
		return bson.D{{Key: "_auditState", Value: "valid"}}
	case audit.StateIncomplete, audit.StateInvalid:
		return bson.D{{Key: "_auditState", Value: q.Completion}}
	case audit.StatePending:
		return bson.D{{Key: "_auditPending", Value: true}}
	}
	return bson.D{}
}

// appendRecordPostMatch adds recordPostMatch as a $match stage when it is not
// empty.
func appendRecordPostMatch(stages []bson.D, q audit.Query) []bson.D {
	if post := recordPostMatch(q); len(post) > 0 {
		stages = append(stages, bson.D{{Key: "$match", Value: post}})
	}
	return stages
}
