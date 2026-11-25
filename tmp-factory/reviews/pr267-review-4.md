# PR #267 review 4: the delta after review 3

**Delta reviewed:** `0d7c2d41..8e719fc8`. It holds the fix commit `a0b4771d`, four lint and gofmt commits, and a merge of `develop` that brings in BL-348 slice 2 (#266). I worked in `/home/claude/wt/rev267`, did not change the PR, and deleted my probe file.

## Verdict: FIX FIRST

Review 3's MUST is fixed for real. The `develop` merge adds a new requirement that the broker does not meet yet: one new MUST.

## Review 3 MUST (the scope note was cut): fixed

**What the fix does (`composeReason`):**
- The scope note now comes **first**, then a space, then the caller's text, which is cut to `200 - len(note) - 1` characters.
- The store replaces addresses and then cuts from the end, so it can only shorten the caller's part.
- The note's own addresses are replaced before it is measured.
- The note is at most about 75 characters, so it always fits.

**What I tested:**
- The real-store test `TestScopeNoteSurvivesTheStoresOwnCleaningOfAnyReason` runs 20,000 cases through `casestore.NewCodeRun`, the store's own cleaning.
  - The `::f` and `::ffff:` pieces and long padded reasons are included.
  - It fails when the order goes back to note-last.
- `TestReasonCannotForgeOrDropTheScopeNote` also fails when the order goes back to note-last.
- My own fuzz: 300,000 reasons of address pieces, `::ffff:`, brackets, fullwidth brackets, U+202E, U+2066, U+200B, U+2028, U+0085, NUL and newlines.
  - Scopes: none, empty, `demo-west2`, ids holding addresses, ids holding `::ffff:1.2.3.4` and `fe80::1`, and a 90-character id.
  - Both modes, with the store's step copied (replace addresses, then cut to 200).
  - Result: 0 failures. The note was always the exact prefix, no `[sources` appeared in the caller's part, no invisible character survived, and the reason was always at most 200 characters.

**Invisible and bidi characters:** `cleanText` now drops control characters, Cf, Zl and Zp. `TestInvisibleAndBidiCharactersAreDroppedFromTheCaller` fails when only control characters are dropped, so the rule is pinned by a test.

**Tests:** `go test -race -count=1 ./internal/codemode/... ./internal/mcp ./internal/mcpsrv` all pass.

## MUST

**M1 (new, from the develop merge): a code run's host does not apply the grant's tier rules from BL-348 slice 2.**

The problem:
- After #266, the MCP dispatcher builds every profile bridge with `mcp.WithHiddenRecordIDs(!identifiers)`. For any tier other than `evidence` it also adds `mcp.WithMinCohort(MinCohort)`, and an unknown tier fails closed (`internal/mcpsrv/backend.go`, `bridgeFor`).
- The tier also limits which tools an aggregate host may call (`aggregateTools` in `internal/mcpsrv/tiers.go`).
- The broker's `DashboardHosts` still applies only `WithIdentifiers` and `WithSources`.
- `HostScope` and `RunRequest` cannot carry a tier at all.

The effect: once BL-574 wires `unravis_execute`, an aggregate-tier MCP host can read through a script what its own tools/call refuses:
- counts below 5, which allow small-count and differencing attacks;
- audit record ids;
- the evidence-only tools.

This is the same class of problem as review 1's M1 (source scope). It is cheap to fix now, before the API has callers.

The fix:
1. Add `Tier` (or the resulting `MinCohort`, `HideRecordIDs` and allowed-tool set) to `HostScope` and `RunRequest`.
2. Have `DashboardHosts` build the bridge with the same options as `bridgeFor`, and fail closed on an unknown tier. Best: one shared helper in `mcp` or `mcpsrv` that both call, so the two cannot drift again.
3. Have `known()` in the broker refuse tools outside the tier.
4. Refuse an `mcp`-surface run that has no tier, as is already done for scope.
5. Record the tier in the scope note.
6. Tests: an aggregate-tier run sees `<5` and no record ids, and is denied `audit_get_requests` (or whatever tools are evidence-only).

## NIT

Fullwidth brackets (`［sources: all］`) still pass in the caller's text. They come after the real note, so a reader who takes the first note is not misled. Fine.
