# PR #267 review 5: the delta after review 4

**Delta reviewed:** `8e719fc8..4f9464a0`. It holds fix commit `4f9464a0` and a merge of `develop` that brings in #268 (BL-350 budgets, BL-351 prune). I worked in `/home/claude/wt/rev267`, did not change the PR, and deleted my probe file.

## Verdict: FIX FIRST

The tier fix is real and is pinned by tests. A run still gets more than the host's own calls in two ways the dispatcher checks:
- the grant's own tool list;
- the per-host budgets that the merge just added.

## Review 4 MUST (tier): fixed as far as it goes

**What the fix does.**
- `mcpsrv.BridgeOptions(identifiers, sources, tier)` is now the one place that builds a grant's bridge options. Both `bridgeFor` and `DashboardHosts` call it. It sets:
  - `WithIdentifiers`;
  - `WithSources`;
  - `WithHiddenRecordIDs(!identifiers)`;
  - `WithMinCohort(5)` for every tier except `evidence`, so an unknown tier fails closed.
- `cleanRequest` refuses:
  - an MCP run without a scope;
  - a scoped run whose tier `ToolsForTier` does not know (`""` included);
  - a tier on a run without a scope.

**No bypass of the tier rules.**
- Only a run with `Scoped: true` gets grant options. An unscoped run is the owner's own `cli` or `ui` run, and the `mcp` surface cannot be unscoped.
- The `Surface` field is set by the app, not by the script.
- `toolsFor` limits the run frame and `known()` to the tier's tools.
- A real tool outside the tier is recorded as denied under its own name and ends the run (`bad_frame`). Nothing reaches the host.

**Mutations (each one makes tests fail).**

| Mutation | Tests that fail |
|---|---|
| `DashboardHosts` goes back to `WithSources` only | `TestAggregateRunSeesNoSmallCountsAndEvidenceRunSeesThem`, `TestRunAnswerEqualsTheDispatcherBridgeAnswerForTheGrant` |
| `toolsFor` returns all ten tools | `TestTierLimitsTheToolsOfARun` |
| the unknown-tier refusal is removed | `TestARunWithoutAKnownTierIsRefusedAndWritesNothing` |

**Test run:** `go test -race -count=1 ./internal/codemode/... ./internal/mcpsrv ./internal/mcp` all pass.

## MUST

### M1. The grant's tool list is not applied: only the tier is.

**The problem.**
- The dispatcher allows a call only when the tool is both in the profile's own list and in the tier: `slices.Contains(p.Tools, req.Tool) && slices.Contains(ToolsForTier(p.Tier), req.Tool)` (`internal/mcpsrv/dispatch.go:236`).
- `Profile.Tools` is "a subset of the tier's tools" (`grants.go:62`).
- `RunRequest`, `HostScope` and `toolsFor` carry only the tier.

**The effect.** A host whose evidence-tier grant names only `audit_get_guide` and `audit_compare` can call `audit_get_requests` (raw rows) through a script.

**The fix.**
1. Add `Tools []string` (the profile's list) to `RunRequest`.
2. Make `toolsFor` return the overlap of the catalog, the tier and that list.
3. Refuse a scoped run whose list is empty, or follow the grant's own rule ("empty means all of the tier") through a shared helper in `mcpsrv`, so the two cannot drift.
4. Test: an evidence grant with a narrow tool list is denied a tool outside that list.

### M2 (new, from the #268 merge). Script calls do not count against the host's budgets.

**The problem.**
- `Dispatcher.CallAs` now charges every call to `lim.admit(profile, class)`: 1 comparison at a time per host, 12 comparisons and 120 live calls an hour, 2 MiB in 10 minutes.
- A code run's calls go through `DashboardHosts` and never pass that limiter.

**The effect.**
- A host can make 50 calls per run (2 runs at once), for example 50 `audit_compare`, and repeat with no hourly limit.
- The limits exist so a host cannot loop on the repositories.
- Many queries are also the raw material of the differencing attacks that BL-348 slice 2 hides counts against.

**The fix.**
1. Give `Config` (or the HostFactory) a per-call admit hook that the app wires to the same limiter, keyed by the grant's profile id.
2. A refused call becomes a recorded `denied` row and gets a `budget_exceeded` answer. The run can go on, the same as for bad arguments.
3. Have the dispatcher expose the limiter for this.
4. Test that the 13th comparison in an hour is refused across a direct call plus a script.

## Provenance skip in the answer-equality test

The skip is only partly justified.
- `TestRunAnswerEqualsTheDispatcherBridgeAnswerForTheGrant` drops `provenance` whole. The stated reason is `elapsedMs`.
- In the demo `elapsedMs` was 0 in my probe, but it can differ on a live source, so leaving out that one field is fair.
- Dropping the whole block also hides `sampleSize`, `docsReturned` and `docsExamined`.
- NIT: zero `elapsedMs` in both documents, and compare the rest of provenance.

**Finding outside this PR (for BL-348 / #266, please route).** `suppress.go` does not touch provenance.
- In an aggregate-tier answer on the narrow query, every group shows `"<5"`.
- Provenance still says `"docsReturned":6,"sampleSize":6` for each window.
- With a query narrowed to one group, `sampleSize` would likely be that group's small count. I did not try a filter. That would defeat the cohort floor on MCP itself and on code mode alike.
- This is BL-348's suppression code, not this PR's.

## Not verified

No real worker, no Windows, no live case. golangci-lint was not run locally.
