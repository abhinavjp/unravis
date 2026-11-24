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
