package store

import (
	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// drillMatchStages are the $match documents that narrow a request page to the
// rows an aggregate counted. They run after the $set stages that derive the
// row's kind, outcome, status class and state.
//
// With summary false the page is the exact cohort (kind members and outcome of
// cohort), the scope the activity and slow-request lists use.
//
// With summary true the page follows the summary pipeline: the kind and outcome
// filters keep the rows the summary keeps, including incomplete rows of unknown
// kind and rows of unknown outcome that the exact scope would drop. A query-wide
// page (no source in the cohort) filters by the cohort's kind and outcome, which
// the caller sets to the query's own. A keyed page filters by the query and then
// by the cohort's exact kind and outcome. state narrows the rows to one quality
// state last.
func drillMatchStages(q audit.Query, cohort [5]string, state string, summary bool) []bson.D {
	inWindow := bson.D{{Key: "_auditAt", Value: bson.D{{Key: "$type", Value: "date"}}}}
	hasFilter := func(value string) bool { return value != "" && value != "all" }
	if !summary {
		if hasFilter(cohort[3]) {
			inWindow = append(inWindow, bson.E{Key: "_auditKind", Value: bson.D{{Key: "$in", Value: stringsToBSON(audit.KindMembers(cohort[3])...)}}})
		}
		if hasFilter(cohort[4]) {
			inWindow = append(inWindow, outcomeFilterElem(cohort[4]))
			if q.Outcome != cohort[4] && audit.IsStatusClassOutcome(q.Outcome) {
				inWindow = append(inWindow, outcomeFilterElem(q.Outcome))
			}
		}
		return []bson.D{inWindow}
	}
	stages := []bson.D{inWindow}
	keyed := cohort[0] != ""
	kind, outcome := cohort[3], cohort[4]
	if keyed {
		kind, outcome = q.Kind, q.Outcome
	}
	if hasFilter(kind) {
		stages = append(stages, kindFilterMatch(kind))
	}
	if hasFilter(outcome) {
		stages = append(stages, outcomeFilterMatch(outcome))
	}
	if keyed {
		exact := bson.D{}
		if hasFilter(cohort[3]) {
			exact = append(exact, bson.E{Key: "_auditKind", Value: bson.D{{Key: "$in", Value: stringsToBSON(audit.KindMembers(cohort[3])...)}}})
		}
		if hasFilter(cohort[4]) {
			exact = append(exact, outcomeFilterElem(cohort[4]))
		}
		if len(exact) > 0 {
			stages = append(stages, exact)
		}
	}
	if stage, ok := stateMatchStage(state); ok {
		stages = append(stages, stage)
	}
	return stages
}

// stateFingerprint makes a page cursor specific to its state filter, so a cursor
// from one state cannot page another. No state filter adds nothing.
func stateFingerprint(state string) string {
	if state == "" || state == audit.StateAll {
		return ""
	}
	return "\x00state:" + state
}
