# mcp-scope: BL-348 source scope (#265, merged), tiers and suppression (#266), BL-350 budgets and BL-351 prune (#268)

All three PRs are SECURITY-SENSITIVE (Opus review). Base develop, assignee set; the reviewer request fails (same account as author).

## #265 (merged, Opus review CLEAN)
Source scope (`mcp.WithSources`, checked in `Bridge.request`, fail closed, no echo, list filtered), tier as ceiling on the tool list, `RecheckIdentifiers` (review S1 of #264), tests for deadline cap and unknown tool name (S2), `userId` filter of `audit_get_activity` needs identifiers.

## #266 claude/mcp-tiers: BL-348 slice 2 (head 104a6fbf, CI green; review 1 MUST M1/M2 and SHOULD S1 to S3 fixed)
- Review 1 fixes: hidden timeline slots leave no mark (left out; a missing cell in a comparison), rest kept at least 5 and below 4 times the positions else no timeline; breakdown refused when hidden groups sum to 1 to 4, to k or 4k, and always for statusCode/pollingType while a group is hidden; slots with only failures are small; Other has no `values`; tests for row timings, tier default, hidden cells; `TestTimelineAttackRecoversNothing` and the breakdown sweep run the reviewer's attack on the demo (two sources give six groups of 1).
- `internal/mcp/suppress.go`: aggregate tier (any tier but evidence) never shows a count of 1 to 4 (shown as `"<5"`; `mcpsrv.MinCohort = 5`; zero is not small). Groups resting on a small count become an allow-listed view (no timings, ratios, percentiles); a selection of 1 to 4 events lists no group; timeline slots below 5 hidden; breakdown rows below 5 fold into Other. No host without identifiers gets `id` in requests/slowest rows.
- Differencing, each tested: (1) slots plus the group count (smallest shown slot also hidden, exhaustive over up to five slots of 0 to 8), (2) breakdown over a comparison whose hidden groups hold 1 to 4 events is refused, (3) Other rows of any two `top` values differ by whole rows of at least 5 (3000 random sets), (4) `recordedEvents` excludes hidden groups.
- Nits of #265: neutral identifiers refusal text on scoped bridges (N1), blank source id gets `permission_required` (N2).
- Tests: real HTTP + SQLite + real dashboard handler on the demo repo; window and filter sweep compares an aggregate host with an evidence host on the same query. The demo has equal counts in every group, so mixed-size groups are covered by typed unit tests.
- Mutation-checked (about 20 mutations, all caught).

## #268 claude/mcp-budgets: BL-350 budgets, BL-351 prune (head c598d62d, CI green, includes #266 head 104a6fbf)
`internal/mcpsrv/budget.go` (1 comparison at once per host and 2 in all, 12 comparisons and 120 other live calls an hour, 2 MiB per 10 minutes; 429 `query_budget_exceeded`, denied ledger row), `mcpaudit.Prune` (90 days, 100k cap; at open and every 1000 rows). Tested on real HTTP routes and the real handler, mutation-checked.

## Verified (Linux) / NOT verified
Verified: go test -race of mcp, mcpsrv, mcpaudit, clisrv, clicore, pipesrv, cmd/audit-explorer; `GOOS=windows go vet` on mcp and mcpsrv (#266). NOT verified: Windows, live PC, real hosts (no BL-345 bridge, no BL-347 approval, so nothing calls the endpoint yet).

## Decisions
- Zero is not a small count. Suppression is by group, slot and breakdown row (spec D13); nested counts inside a shown group (errors, incomplete) and `quality` totals are not suppressed.
- Breakdown is refused (not masked) when its total would reveal hidden groups.
- Budgets are in memory per profile id; a reissue does not reset them.

## Leftovers
- Cross-window differencing (same question, windows a few seconds apart): only the budgets limit it. Needs an owner decision if it must be closed (coarser time alignment).
- Leak review file `docs/desktop/mcp-leak-review.md` and fixed-code replacement of upstream `warnings` and `quality.error` text: not done.
- BL-349 aliases/`evidenceRef` (needs the BL-347 resolver route), signed cursors, per-profile handle limits, in-flight dedupe, ledger screen, 24 h prune timer, budgets from the screen, BL-355 numbers.
- `unravis.d.ts` bindings (BL-364) describe counts as numbers; an aggregate host can get `"<5"` and `suppressed` rows: add union types.
- #264 NITs N1, N3 to N6 still open (Forget vs bridgeFor, shutdown order, global counter, legacy path note).
