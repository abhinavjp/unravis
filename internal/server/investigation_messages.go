package server

import (
	"fmt"
	"net/http"
	"strings"

	"api-audit-explorer/internal/audit"
)

// plainValidationText is what a check says when nothing more specific is known.
const plainValidationText = "Request validation failed."

// queryProblemRules turns the audit package's validation reasons into fixed
// sentences. The first rule whose fragment is inside the error text wins, so
// the specific rules come before the general ones at the end. No sentence
// repeats anything the caller sent.
var queryProblemRules = []struct{ fragment, text string }{
	{"sourceIds must contain 1 to", fmt.Sprintf("Pick 1 to %d sources.", audit.MaxSources)},
	{"sourceIds must not contain blank", "One of the chosen sources is blank. Choose the sources again."},
	{"sourceIds must not contain duplicates", "A source is chosen twice. Choose each source once."},
	{"all interval times are required", "Every start and end time is needed."},
	{"interval end must be after start", "The end of a time window must be after its start."},
	{"interval must not exceed 7 days", "A time window can be at most 7 days long."},
	{"baseline interval must end at or before current start", "The normal window must end before the slow window starts."},
	{"bucketSeconds must be between", "The interval length must be between 10 seconds and 1 day."},
	{"time buckets", "That window has too many time steps. Use a longer interval or a shorter window."},
	{"kind must be", "One of the filter choices is not valid."},
	{"outcome must be", "One of the filter choices is not valid."},
	{"metric must be", "One of the filter choices is not valid."},
	{"queryMode must be", "One of the filter choices is not valid."},
	{"completion must be", "One of the filter choices is not valid."},
	{"controller must be at most", "The controller or action filter is too long."},
	{"action must be at most", "The controller or action filter is too long."},
	{"auditId must be 24", "An audit ID must be 24 hexadecimal characters."},
	{"operation must be Controller.Action", "An operation must be written as Controller.Action."},
	{"userIds", "A filter value is too long, blank or not valid. Check the filters."},
	{"uiVersions", "A filter value is too long, blank or not valid. Check the filters."},
	{"userIps", "A filter value is too long, blank or not valid. Check the filters."},
	{"statusCodes", "A filter value is too long, blank or not valid. Check the filters."},
	{"userAgentContains", "A filter value is too long, blank or not valid. Check the filters."},
	{"urlContains", "A filter value is too long, blank or not valid. Check the filters."},
	{"operation", "A filter value is too long, blank or not valid. Check the filters."},
	{"auditId", "A filter value is too long, blank or not valid. Check the filters."},
}

// queryProblem is the sentence for a failed audit.ValidateQuery. An error it
// does not recognise gets the plain sentence.
func queryProblem(err error) string {
	if err == nil {
		return plainValidationText
	}
	text := err.Error()
	for _, rule := range queryProblemRules {
		if strings.Contains(text, rule.fragment) {
			return rule.text
		}
	}
	return plainValidationText
}

// writeQueryProblem answers 400 with queryProblem(err).
func writeQueryProblem(w http.ResponseWriter, err error) {
	writeError(w, http.StatusBadRequest, queryProblem(err))
}
