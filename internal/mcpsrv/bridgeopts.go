package mcpsrv

import "api-audit-explorer/internal/mcp"

// BridgeOptions are the options of the bridge that serves a grant: the one place that says what a grant's identifiers switch, source
// scope and tier do to the tools. The MCP dispatcher (LocalBackend.bridgeFor) and the code-mode broker (codemode.DashboardHosts) both
// build their bridges from it, so a script run for a host sees exactly what the host's own tool calls see, and the two cannot drift.
//
//   - identifiers: the operator switch for user ids, user agents and addresses; without it no host sees the audit record id of a row.
//   - sources: the source scope of the grant (an empty list reaches no source).
//   - tier: an aggregate host never sees a count of fewer than MinCohort events. Any tier but evidence gets this, an unknown one
//     included: fail closed.
func BridgeOptions(identifiers bool, sources []string, tier string) []mcp.Option {
	opts := []mcp.Option{mcp.WithIdentifiers(identifiers), mcp.WithSources(sources), mcp.WithHiddenRecordIDs(!identifiers)}
	if tier != TierEvidence {
		opts = append(opts, mcp.WithMinCohort(MinCohort))
	}
	return opts
}
