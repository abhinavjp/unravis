# PR #267 review 2: the delta after review 1

This review covers the delta `ae178d22..1312634d`: commits `6b5c1e63` (the wall-clock watcher now sleeps on one timer) and `1312634d` (the review 1 fixes). Worktree: `/home/claude/wt/rev267`. I did not change the PR, and I deleted my probe file after use.

## Verdict: FIX FIRST (one MUST: the scope note in the reason can be spoofed)

Review 1's M1 and S1 to S4 are fixed in code, and each fix is pinned by a test that fails when the fix is reverted (table below). One problem remains, and it is the point the coordinator asked about: a reason string the caller controls can push the real scope note out of the record and leave a fake one at the end.

## Tests run

- `go test -race -count=1 ./internal/codemode/... ./internal/mcp`: all ok. `go vet ./internal/codemode/...`: clean. The casestore packages are unchanged in this delta.
- Mutations on `run.go`, each one run against `go test ./internal/codemode/`:

| Mutation | Caught by |
|---|---|
| scope not passed to the HostFactory (`Scoped: false`) | TestScopedRunReachesOnlyItsSources, TestEmptyScopeReachesNothingAndMCPNeedsAScope |
| `leaks()` check on the logs removed | TestSecondNetsStopWhatTheGuardMissed |
| `leaks()` check on the result removed | TestSecondNetsStopWhatTheGuardMissed |
| `ContainsIP` check after `HostResult` removed | TestSecondNetsStopWhatTheGuardMissed |
| `ContainsIP` check on the rebuilt host error removed | TestSecondNetsStopWhatTheGuardMissed |
| `ContainsIP` check on the script error removed | TestSecondNetsStopWhatTheGuardMissed |
| `ContainsIP` check on the strip error removed | TestSecondNetOnTheParsersWords |
| reading goes on after a stopping call | TestNoFramesAreReadAfterAStoppingCall (expects exactly 5 rows, 1 of them denied) |
| `recover` removed from `handleCall` | TestPanicInTheCallHandlerEndsOnlyTheRun (the test binary panics) |
| `recover` removed from `guardedRun` | TestPanicInTheRunIsSealed |

## Checked points

**Scope reaches the bridge on every host call path.** Holds.
- `HostScope{AllowIdentifiers, Scoped, Sources}` is fixed once per run, in `Run`, from the `RunRequest`. The script cannot set it, and the source list is cloned twice.
- `DashboardHosts` adds `mcp.WithSources` when `Scoped` is set. A scoped run with an empty list reaches no source.
- `safeHostCall` → `r.host.Call` is the only host path, and the run has no other bridge.
- A run with `surface: "mcp"` and no scope is refused before any row is written.
- `checkScope` and the filtered source list in `internal/mcp` are BL-348's code, which this PR does not change. The test covers both sources, the other source and the source list.

**Panics.**
- `guardedRun` recovers any panic in `runWorker` (spawner, strip, frame loop, `takeDone`), kills the worker, and the run is then sealed through `finishWatchers` and `EndRun`.
- `handleCall` recovers and calls `fail(internal_error)`; the call gets no answer.
- Not covered, see S1 and NIT 1: the handler that panics leaves no row for its call, and the code outside `guardedRun` can still panic.

**Second nets.** Every stage the tamper seam names (`hostresult`, `hosterror`, `output`, `log`, `scripterror`, `strip`) has a net, on both the ok path and the failed-script path. Each net fails its test when it is removed.

**No frames read after a stopping call.** Holds.
- `onCall` returns true for a bad id and for any denial that carries a stop code.
- The loop then waits only on `r.ctx.Done()`.
- Every handler path for a stopping denial calls `fail`: an audit error, the stop code, or the recover. So the wait always ends, within `AuditWait` at most.

**Other fixes.**
- The wire decoder now folds field names with `unicode.SimpleFold`, as encoding/json does. The `ſ`/`K` duplicates are refused (test present).
- `Marshal` no longer HTML-escapes, so the worker gets the same bytes the record holds.
- A run frame over `MaxFrame` now ends as `source_too_large`.
- Logs are cleared when the end record fails.
- `runtimeCode` is labelled as hygiene only. The spec names the worker as the boundary.
- The wall-clock watcher change looks correct: one timer, reset on `kick`, no polling while a call is in flight. The existing wall and run-cap tests pass under `-race`.

## MUST

**M1. The caller's reason can drop the real scope note and leave a fake `[sources: all]` in the record (hidden mode).**

How it happens:
- `cleanRequest` cuts the user's reason to `200 - len(suffix)` characters and then appends the suffix.
- In hidden mode, `casestore` then runs `ReplaceAddresses` on the whole reason. `::1` (3 characters) becomes `[ip]` (4 characters).
- The store then cuts back to 200 characters, from the end.
- So a reason that holds enough `::1 ` (or other short addresses) grows past 200 inside the store, and the cut removes the broker's suffix.

The reason is model-controlled text on the MCP surface (the `reason` parameter of `unravis_execute`, BL-574).

Probe (deleted after use):

```
RunRequest{Surface:"mcp", Scoped:true, Sources:["demo-west"],
  Reason: strings.Repeat("x",81) + strings.Repeat("::1 ",21) + "[sources: all]"}
```

The stored reason (200 characters) ends with `"... [ip] [ip] [sources: all] "`. The true `[sources: demo-west]` is gone. A scoped MCP run is on record as unscoped. The audit trail now lies about what the run could reach. The access itself is still scoped.

Even without the cut, a reason that reads `... [sources: all]` comes right before the real note. That is ambiguous to anyone who does not know that the last note is the real one.

Fix (any of these, cheapest first):
- (a) Neutralise the marker in the user's part before appending: replace `[sources:` (any case, and after `normalize`) with `(sources:`, or drop `[` and `]` from the reason.
- (b) Keep the note out of reach of the cut: in hidden mode, run `casestore.ReplaceAddresses` on the user's reason in the broker before measuring it, or have the store cut from the front of the user's part instead of from the end.
- (c) Best: give the scope its own begin-record field. That is a store change, so it can become a backlog item if (a) and (b) land now.

Add a test with the probe above that asserts the stored reason ends with the real note. Also test a reason that already holds `[sources: all]`.

## SHOULD

**S1. A call whose handler panics after the host ran has no audit row.**
- In `TestPanicInTheCallHandlerEndsOnlyTheRun`, the panic hits inside `answer` (the Guard stage), after the tool has already read the data.
- The recover only fails the run, so this call frame never gets a row. The rule "every call is audited" is broken on this path. The script got nothing, so this is not a leak.
- Fix: in the recover, make a best-effort `RecordCall` with a fixed `error` envelope (`internal`) and `done_seq` taken under `serial`, if the row has not been written yet. Then `TestPanicInTheCallHandlerEndsOnlyTheRun` can check that one row exists for the call.

## NIT

1. Some code that runs after `BeginRun` is still outside `guardedRun`. A panicking `HostFactory` (`b.cfg.Hosts(...)` in `Run`), the killer goroutine and `readFrames` would each leave the run open until the orphan sweep (or crash the app). Move the `Hosts` call inside the guarded part, or before `BeginRun`.
2. Line 150 of `review1_test.go` has a dead clause (`stage == "hosterror" && !ok && false`).
3. The source ids in the scope note are cut to 60 characters followed by `,...`, so a long scope is recorded only in part. Fine for now; option (c) of M1 would fix it.

## Not verified

No real worker, no Windows run, no live case. golangci-lint was not run locally (CI is the check).
