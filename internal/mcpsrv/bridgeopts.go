package mcpsrv

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"time"

	"api-audit-explorer/internal/mcp"
)

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

// RunCursors is the cursor option of a scoped code run (BL-363, after the cursor seal of BL-349 landed): the script gets sealed cursors,
// never the dashboard's own text, which holds a position in a record list (the audit record id and the source of a row). The key is
// made for this run, so a cursor works for the run's own paging and for nothing else; it is not valid after the run, for another run,
// another scope, another tier or another identifiers state.
func RunCursors(sources []string, tier string, identifiers bool) mcp.Option {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sign := func(domain string, data []byte) []byte {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(domain))
		m.Write([]byte{0})
		m.Write(data)
		return m.Sum(nil)
	}
	return mcp.WithCursors(&cursorCodec{sign: sign, now: time.Now, profile: "code-run", binding: newBinding(sources, tier, identifiers, newGen())})
}
