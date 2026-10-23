package audit

import "fmt"

// Row states a request drill-down can be narrowed to. Every aggregate and
// quality count must be drillable to exactly the rows it counted, so each count
// has one state: complete (valid, timed rows), incomplete (no completion time
// yet, pending async rows included), invalid (a timing that cannot be used),
// pending (asynchronous requests that only answered Accepted) and unclassified
// (rows whose outcome is unknown, whatever their state). Empty and "all" select
// every row.
const (
	StateAll          = "all"
	StateComplete     = "complete"
	StateIncomplete   = "incomplete"
	StateInvalid      = "invalid"
	StatePending      = "pending"
	StateUnclassified = "unclassified"
)

// ValidateState rejects a state filter that is not one of the values above.
func ValidateState(state string) error {
	switch state {
	case "", StateAll, StateComplete, StateIncomplete, StateInvalid, StatePending, StateUnclassified:
		return nil
	}
	return fmt.Errorf("%w: state must be complete, incomplete, invalid, pending, unclassified or all", ErrInvalidInput)
}

// StateMatches applies a state filter to one row. rowState is the row's own
// state (valid, incomplete or invalid), pendingAsync says whether it is a
// pending asynchronous request and outcome is its coarse outcome.
func StateMatches(filter, rowState string, pendingAsync bool, outcome string) bool {
	switch filter {
	case "", StateAll:
		return true
	case StateComplete:
		return rowState == "valid"
	case StateIncomplete:
		return rowState == "incomplete"
	case StateInvalid:
		return rowState == "invalid"
	case StatePending:
		return pendingAsync
	case StateUnclassified:
		return outcome == "unknown"
	}
	return false
}

// RowInSummary reports whether the summary counts a row under the given kind
// and outcome filters (the same rule the summary pipelines and the legacy
// reader apply): a valid row needs an exact kind, any other row follows the
// compatible-kind rule, and a row of unknown outcome always stays in so the
// quality counts remain honest. Under success and failure every row that is not
// valid also stays in; under client_error and server_error only a status class
// match does. An empty or "all" filter selects everything.
func RowInSummary(kindFilter, outcomeFilter, kind, state, outcome, class string) bool {
	if kindFilter != "" && !KindMatches(kindFilter, kind, state == "valid") {
		return false
	}
	if outcome == "unknown" || outcomeFilter == "" || outcomeFilter == "all" {
		return true
	}
	if state == "valid" || IsStatusClassOutcome(outcomeFilter) {
		return OutcomeFilterMatches(outcomeFilter, outcome, class)
	}
	return true
}

// CohortMatchesQuery reports whether a cohort of the given kind and outcome can
// hold rows the summary counted under the query. The kind follows the
// compatible-kind rule, so an unknown-kind cohort is reachable under a
// synchronous, asynchronous or http query, never a hangfire one. Under success
// and failure the summary also keeps every non-valid row, so a cohort of any
// outcome can exist; under client_error and server_error only failure and
// unknown cohorts can.
func CohortMatchesQuery(q Query, kind, outcome string) bool {
	if !KindMatches(q.Kind, kind, false) {
		return false
	}
	switch outcome {
	case "success", "failure", "unknown":
	default:
		return false
	}
	if IsStatusClassOutcome(q.Outcome) {
		return outcome == "failure" || outcome == "unknown"
	}
	return true
}
