// Package mcpsrv is the backend side of MCP access (spec 03). BL-346 builds the installation grants here: a host (Claude Code, Codex,
// and so on) is a profile; a profile is enrolled, approved by the person in the app, claims its credential once, and can be revoked,
// expired or stopped. The dispatcher that runs a tool for an authenticated profile (BL-344), the tier rules (BL-348), budgets and
// handles (BL-350) and the ledger (BL-351) call into Grants; none of them is in this package yet.
package mcpsrv
