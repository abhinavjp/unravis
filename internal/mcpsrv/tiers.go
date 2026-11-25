package mcpsrv

import "slices"

// Tiers (spec 03 section 3.4). The tier decides which tools a profile may be given; BL-348 enforces it on every call, BL-346 only
// refuses an approval that names a tool outside the tier.
const (
	TierAggregate = "aggregate"
	TierEvidence  = "evidence"
)

// MinCohort is the smallest count an aggregate-tier host is shown (spec 03 D13, set by the BL-354 spike on a real case; 5 until then).
// A count above zero and below it is shown as "<5".
const MinCohort = 5

var aggregateTools = []string{
	"audit_get_guide", "audit_list_sources", "audit_compare", "audit_get_groups", "audit_get_timeline", "audit_compare_timelines",
	"audit_get_breakdown",
}

var evidenceTools = []string{"audit_get_requests", "audit_get_slowest", "audit_get_activity"}

// ToolsForTier lists the tools a tier allows (a new slice), nil for an unknown tier.
func ToolsForTier(tier string) []string {
	switch tier {
	case TierAggregate:
		return slices.Clone(aggregateTools)
	case TierEvidence:
		return slices.Concat(aggregateTools, evidenceTools)
	}
	return nil
}

// GrantTools are the tools a grant may call: its own list (Profile.Tools) within its tier, in catalog order. The tier is the ceiling
// even if a stored list names more, and an unknown tier has none. An empty list has none here: Approve fills the list with the whole
// tier before it is stored, so an empty stored list never means "all".
func GrantTools(tier string, tools []string) []string {
	var out []string
	for _, t := range ToolsForTier(tier) {
		if slices.Contains(tools, t) {
			out = append(out, t)
		}
	}
	return out
}

// ToolAllowed is the one rule of "may this grant call this tool": the tool is in the grant's list and in the grant's tier. The
// dispatcher checks every call with it, and the code-mode broker builds the tool list of a run from the same rule (GrantTools).
func ToolAllowed(tier string, tools []string, tool string) bool {
	return slices.Contains(tools, tool) && slices.Contains(ToolsForTier(tier), tool)
}
