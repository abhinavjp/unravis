# Handoff: codemode-broker (BL-363), PR #267 (base develop, branch claude/codemode-broker)

**SECURITY-SENSITIVE: fresh Opus review needed.** Not merged. Stopped at the worker interface (BL-362 worker and BL-366/367 spawner are unbuilt), so NO script runs yet.

## Built
- `internal/codemode`: `Broker` (`New`, `Run`, `Cancel`, `OnCaseOpen`), `Config` (Spawner, Hosts, Cases resolver, Settings, Authorize, operator AllowIdentifiers, RequiredMitigations), `wrap.go` (esbuild v0.28.2, MIT, now a direct dep), `host.go` (`DashboardHosts`: `mcp.NewLocal` per run, no mcp/mcpsrv edits), `spawn.go` (`Spawner`/`Worker`, `ErrDegradedRefused`), `run.go` (engine), `limits.go` (constants from `bindings`), `errors.go`.
- `internal/codemode/wire`: 4-byte LE frames, 1 MiB+4 KiB cap, strict decode (unknown/repeated/case-folded-repeated field, trailing data, bad UTF-8 refused).
- Flow: refuse-before-write checks -> BeginRun -> strip -> Start -> hello (version, runtime shape, mitigations) -> run frame -> calls -> done -> EndRun. Every call: host -> Guard (HostResult / HostError rebuilt as `mcp.ErrorEnvelope`) -> `RecordCall` -> answer, under one lock, so audit order = answer order. Final result `Output`, logs `Log` (64 KiB, 2000 lines), errors `ScriptError`, last `ContainsIP` net in hidden mode.
- Untrusted worker: broker counts calls (50) and in-flight (4) itself; 51st / 5th in flight / unknown tool / bad id / bad frame recorded `denied` and end the run; args over 64 KiB or not canonical JSON are denied with a marker row; worker error codes whitelisted (script_error, ticks, oom, stack, output_too_large); mode and sha256 shape validated.
- Clocks: script time pauses while a call is in flight (wall, 1-120 s, default 30), 280 s run cap, `Cancel`, caller ctx; EndRun uses a context that survives cancel; refused end field falls back to a minimal sealed end (never leaves a run open).
- `CloseOrphanRuns`: once per case before first run + `OnCaseOpen`.
- Docs: spec 04 "As built" under BL-363; BACKLOG note and new BL-574.

## Verified (Linux, -race)
Real sealed SQLite case + real ten tools through the demo dashboard handler; scripted fake worker over the wire protocol (no JS). `TestHostMatchesDirectTool`, audit-before-answer, hidden-mode sweep over everything stored, allow mode, hostile host text (address, `Password=`, token look-alike), error scrub, budgets (50/51, 4/5 in flight, 1 MiB exact/+1, source 256 KiB exact/+1), wall vs slow tool, run cap, cancel, crash, 20 hostile frames, hello checks, degraded mode, audit failure fail-closed, orphan sweep, 2-run limit, goroutine reaping, no `os/exec`/`syscall` import. Mutations checked. Real bug found and fixed by the tests: run cap read before its cause was set gave status ok.

## NOT verified
No real worker, no Windows, no live "Brain Sandbox Slowdown". golangci-lint not run locally (CI). `identifier_limit` path is tested only through an unexported test seam (`Broker.learn`): in production the broker passes no ids (hidden mode tools already hide them; the sweep finds addresses).

## Decisions
- Refusals (off, no spawner, no case, too large, busy, denied) write nothing; everything after BeginRun is a `RunResult` and audited. Missing spawner is refused before the begin row.
- Host answers/errors always reach the script as `ok:true` with the tool envelope (matches the `.d.ts` union); `ok:false` unused.
- `worker_crashed` -> status `crashed`; call_budget/ticks/oom/stack/output/result too large/identifier_limit -> `limit_exceeded`.
- Timeout out of range is clamped, not refused. Reason/callerId have control chars dropped and are cut.
- `import()` and `import.meta` refused by scanning esbuild's printed output (may refuse a string that spells it: safe side). Decorators/enums compile.
- A script that ends without awaiting a call: run ok, call recorded as abandoned error.

## Leftovers for review
In-flight count is held until the answer is written; `Guard` numbering depends on answer order; `RecordCall` can wait up to 2 min during an import (then audit_failed); pre-wrapper breakout (`}); evil(); (async()=>{`) stays inside the same sandbox by design. BL-574 (routes, settings, MCP tools, CLI, `OnCaseOpen` hook, replay).

## Round 2 (CI + review 1 of #267), head 1312634d, fast-build/go/lint/web green
- CI: ticker rule (BL-424) fixed by replacing the 20 ms poll with one timer + wake channel; `secretscrub.TestMaskHostileInputIsFast` failure on ae178d22/6b5c1e63 was a CI-load timing flake (secretscrub untouched, passes alone, green on the next run).
- M1: `RunRequest.Scoped/Sources` -> `HostScope` -> `mcp.WithSources` in `DashboardHosts`; surface `mcp` without a scope is refused; scope written to the run's `reason` (` [sources: ...]`), a store field is in BL-574. Tests: scoped run cannot list or query another source, empty scope, long scope cut.
- S1 recover in handleCall and the run (`internal_error`, still sealed); S2 `ContainsIP`/`leaks` on logs of every path, host error envelopes, parser text, each proved by a `tamper` test seam (mutation-checked); S3 requirement written into spec BL-362 and BACKLOG, scan also refuses `eval(`/`Function(` (hygiene only); S4 no frame read after a stopping call (5 rows, 1 denied).
- Nits: wire fold now `unicode.SimpleFold`, no HTML escaping in frames, logs cleared when the end is not sealed, run frame too large -> `source_too_large`, `runCount` removed.
- Not verified: unchanged (no worker, Windows, live case).

## Round 3 (review 2 of #267), head 0d7c2d41, all four checks green
- M1: reason is composed by `composeReason`: `[`/`]` in the caller's text become `(`/`)`, hidden mode replaces addresses before measuring (store replace grew `::1` to `[ip]` and cut the note), caller part cut to leave room, note last and whole. Probe of the review plus forged notes are in `TestReasonCannotForgeOrDropTheScopeNote` (fails without either half of the fix). A real scope field in the begin record stays BL-574.
- S1: a call whose handler panics after the tool ran gets a fixed error row (`recordPanicked`). NIT 1: the host is made before BeginRun (a panicking factory leaves nothing open). NIT 2: dead clause removed.

## Round 4 (review 3 of #267), head 8e719fc8, all four checks green
- The scope note is now the FIRST words of the stored reason (store cuts from the end, so only caller text can be lost); caller text is bracket-neutralised, invisible/bidi/control characters dropped, addresses replaced before measuring. Probe `x*174 + " ::ffff:1.2.3.4"` and a 20,000-reason seeded fuzz through the store's own `NewCodeRun` cleaning (`TestScopeNoteSurvivesTheStoresOwnCleaningOfAnyReason`); fails with the note at the end. A real field stays BL-574.

## Round 5 (review 4 of #267): tier rules for the code-run host
- M1: a code run now applies the grant's tier as the MCP dispatcher does. New `mcpsrv.BridgeOptions(identifiers, sources, tier)` is the one place that builds the bridge options (source scope, record ids hidden without identifiers, `MinCohort` 5 unless tier is evidence, unknown tier fails closed); `LocalBackend.bridgeFor` and `codemode.DashboardHosts` both call it (no copy). `RunRequest.Tier` -> `HostScope.Tier`; a scoped run without a known tier is refused (`denied`), a tier without a scope is `bad_request`; the run frame and `known()` use the tier's tools (`toolsFor`); a call to a real tool outside the tier is recorded `denied` under its own name and ends the run (`bad_frame`); the scope note reads `[sources: a; tier: aggregate]`.
- Tests (`tier_test.go`, fail without the fix, each mutation-checked: host without tier options, all tools for every run, no tier validation): aggregate run gets `<5` and no figures of small groups, evidence run gets the real 2; script answer equals the answer of a bridge built from `BridgeOptions` for the same grant; the three evidence tools are denied to an aggregate run and allowed to an evidence run; record ids absent without and present with identifiers; unknown/missing tier refused with no rows; note carries tier; a custom host factory receives the tier.
- BL-574 must pass the grant's tier and filter `unravis_types` by tier. Not verified: no worker, no Windows, no live host.
- Round 5 finish: the answer-equality test left out `provenance` (query elapsed ms) as well as `resultId`, it was flaky under -race. golangci-lint here is built with Go 1.25 and cannot load the repo config (CI lints). Tests -race, vet, gofmt clean for codemode, mcp, mcpsrv.

## Round 6 (review 5 of #267): tool list and budgets
- M1: `RunRequest.Tools/ProfileID/ProfileRev`; `mcpsrv.GrantTools` and `ToolAllowed` are the one rule (list within tier); `dispatch.go` and the broker (`toolsFor`, run frame, `known()`) both use them. A scoped run with no usable tool, no profile or no `Config.Admit` is refused. Test `TestGrantToolListLimitsTheRun` (evidence grant with two tools denied `audit_get_requests` etc.), `TestGrantNeedsItsToolListAndProfile`, `TestToolAllowedIsTheGrantsListWithinItsTier`.
- M2: `Dispatcher.AdmitCodeCall(ctx, principal, tool, args)` charges the same limiter (in flight, comparisons at once and per hour, live reads, bytes per 10 min) and writes the same ledger rows (admit/end/denied); `Config.Admit` calls it for every call of a scoped run; refusal = denied row + `query_budget_exceeded` answer, run goes on. `Dispatcher.Handler()` added so the app can build the dispatcher and keep it (`Handler(deps)` unchanged). Tests: `TestScopedCallsAreChargedToTheHostBudgets` (codemode, fails without the hook), `TestCodeRunCallsShareTheHostBudgets` (real dispatcher: 10 direct + 2 script = 12, 13th refused from both doors, ledger counts 12/12/2), `TestCodeRunCallsHonourAtOnceAndBytes`.
- Test nit: answer-equality test compares provenance with `elapsedMs` zeroed.
- BL-574 must: set `Config.Admit = disp.AdmitCodeCall`, pass tool list/profile, cancel runs on revoke (`AdmitCodeCall` does not use `BeginCall`). Out of this PR (route to BL-348): aggregate answers leave `provenance.sampleSize/docsReturned` unsuppressed (review 5).
