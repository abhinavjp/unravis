# PR #267 review 1: code-mode broker (BL-363)

Reviewer: fresh security review, no earlier context. Head `ae178d22`, base `develop`. Worktree `/home/claude/wt/rev267`. I did not change the PR. My probe file (`zz_review_test.go`) was deleted after use.

Read: CLAUDE.md, the handoffs (codemode-broker, -privacy, -auditstore, auditstore-hardening, -bindings), the reviews of #242, #251, #256 and #261, all new code in `internal/codemode` (`broker.go`, `run.go`, `host.go`, `spawn.go`, `limits.go`, `errors.go`, `wrap.go`, `wire/wire.go`), and the parts of `internal/mcp` the broker calls (`envelope.go`, `bridge.go`, `sdkline.go` strictShape) plus `internal/mcpsrv/backend.go`.

## Verdict: FIX FIRST (one MUST: host calls ignore the grant's source scope)

The core gates hold. Every host answer goes through `Guard.HostResult`. Every host error goes through `HostError` and is rebuilt as an `ErrorEnvelope`, which also forces the code into the `wireCodes` list. Every final result goes through `Output`, every log line through `Log`, and every script error through `ScriptError`. Each answer is recorded with `RecordCall` before its `ret` frame is written, under one lock, so the audit order is the order the script saw. A failed audit write ends the run without sending the answer. EndRun runs on a context that survives cancel, and the cap and the cancel paths are sealed. I found no path where a tool result reaches the script or the caller without the Guard. I also found no token reuse across runs (one Guard and one Host per run, and the out side mints no numbered token) and no change to the MCP catalog or tools/list (the diff has no `internal/mcp` file).

## Tests run

- `go test -race -count=1 ./internal/codemode/... ./internal/casestore/... ./internal/mcp`: all pass except `casestore/sqlite` `TestCapKinds/free_disk_floor` and `TestCapsRefusedWithCaseUntouched/free_disk_floor`. Both are in `docs/desktop/baseline-go-tests.txt`.
- Mutations on `run.go`, each run against `go test ./internal/codemode/`:

| Mutation | Caught by |
|---|---|
| raw envelope sent in place of the `HostResult` output | TestHostTextIsSweptScrubbedAndEscaped |
| raw host error message and action (no `HostError`) | TestHostTextIsSweptScrubbedAndEscaped |
| `Guard.Log` skipped | TestHostTextIsSweptScrubbedAndEscaped |
| `Guard.Output` skipped (raw final result) | TestHostTextIsSweptScrubbedAndEscaped |
| `ScriptError` skipped | TestScriptErrorTextIsScrubbed |
| RecordCall error ignored | TestAuditFailureIsFailClosed |
| RecordCall made asynchronous, after the answer | TestCallIsAuditedBeforeTheAnswer, TestAuditFailureIsFailClosed |
| denial with stop still answered | TestCallBudgetFiftyThenDenied |
| `serial` lock removed | TestFourInFlightAreFineAndFiveIsAViolation (race) |
| `Incomplete` case in `answer` removed | not caught, but still safe: the `err != nil` branch gives a fixed answer, and takeDone ends the run with `identifier_limit` |
| `leaks()` final net off | **not caught** (NIT 1) |
| `ContainsIP` net in `answer` off | **not caught** (NIT 1) |

- Probes, which I deleted after use:
  - a call flood past the in-flight limit with 60 KB arguments;
  - a Unicode case-fold duplicate field in a `done` frame;
  - addresses in the logs and error of a failed script;
  - a host error with an address, `Password=`, a user path and an invalid code (result: `[IP_1]`, `[scrubbed]`, and the code was forced to `internal`).

## MUST

**M1. Host calls of a run are not limited to the caller's source scope (grants, BL-348).**

The problem:
- `HostFactory` is `func(allowIdentifiers bool) Host`.
- `DashboardHosts` builds `mcp.NewLocal(dashboard, token, mcp.WithIdentifiers(...))` and never uses `mcp.WithSources`.
- `RunRequest` has no field that carries a scope.
- The MCP dispatcher, merged just before this PR (#265, "grant source scope"), builds every bridge with `mcp.WithSources(p.Sources)` (`internal/mcpsrv/backend.go:56`).

The effect:
- Once BL-574 wires `unravis_execute` on the MCP surface, a profile whose grant covers source A can run a script that calls `audit_list_sources` and `audit_compare` on every source.
- The broker API cannot even say "this run may see only A". `Authorize` can only refuse the whole run.

Spec 04 step 1 says "grant (when it exists)". The grant scope now exists.

The fix (cheap, and it changes the API now, before callers depend on it):
- Add the source scope (and the identifier tier, if the grant carries one) to `RunRequest` or to the `HostFactory` arguments.
- Pass it to `mcp.WithSources` in `DashboardHosts`.
- Record it in the begin record (for example in `CallerID` or a new field), so the audit trail says what the run could reach.
- Add a test that a scoped run cannot list or query a source outside its scope.
- If this PR keeps the API as it is, it must at least refuse `surface: "mcp"` until the scope is passed, and write that down in BL-574. Otherwise the BL-574 builder inherits an unscoped host without seeing it.

## SHOULD

**S1. A panic in the broker crashes the app and leaves the run open.**
- `safeHostCall` and `strip` recover, but nothing else does.
- `handleCall` runs `Guard.HostResult` and `HostError` on hostile audit data (user agents, URLs) in its own goroutine. A panic there (a Guard bug that the fuzzers missed) kills the whole dashboard process, not just the run.
- A panic in `runWorker` or `takeDone` skips `EndRun`. The run then stays `running` until `CloseOrphanRuns` (15 minutes of grace, at the next case open).
- Fix: recover in `handleCall` (`r.fail(CodeAuditFailed or internal)`, no answer) and in `execute`/`runWorker` (seal the run as `crashed` or `error` with a fixed text). Test both with a Host or Guard seam that panics.

**S2. The last `ContainsIP` net covers only the success path.**
- `leaks()` runs only on an `ok` result.
- On a failed script (`!d.OK`), the logs are returned and stored without the net. `ScriptError` text is checked, but the logs are not.
- The net is also missing on host error envelopes (`answer`, isErr branch) and on strip error text.
- The Guard itself handled every case I tried: `[IP_OUT]` in logs and errors, `[IP_1]` in host errors. So this is defense in depth only, but the handoff describes the net as covering everything stored.
- Fix: apply `leaks()` to the logs on every path that returns them, and `ContainsIP` to the rebuilt error envelope.

**S3. The dynamic import check is not a security boundary.**
- `dynamicImport` scans esbuild's output. `new Function("return import('x')")()` or `eval(...)` builds an import at run time that the scan never sees.
- This is fine only if the worker (BL-362) has no module loader, or turns off `eval`/`Function`.
- Fix: write this into BL-362 as a requirement (QuickJS: no module loader hook, or a host loader that always refuses), and add a test there. Today the comment says only "the worker removes import() anyway".

**S4. Calls past a stopping violation keep being read and recorded.**
- `onCall` queues the stopping denial (51st call, 5th call in flight, unknown tool), but the read loop keeps reading until the denial's goroutine has written its row and called `fail`.
- In my probe (synchronous `io.Pipe`), 14 frames were sent after 4 stalled calls, and 13 rows were recorded instead of 5.
- With a real buffered OS pipe the window is one `synchronous=FULL` write long. Each extra frame becomes a goroutine plus a durable row, bounded by the store's 256-call cap, after which the run ends with `audit_failed`.
- Fix: once `onCall` produces a denial with `stop`, stop reading frames: set a flag, and have the loop return right after that frame. Then the record holds exactly one denied row.

## NIT

1. The second nets (`leaks`, `ContainsIP` in `answer`) are not covered by any test: turning either off fails nothing. A test seam that makes the Guard return an address (for example a `guardHook` in tests) would pin them.
2. `wire.checkObject` folds ASCII only. Go's decoder also folds `ſ` to `s` and the Kelvin sign to `k`: the probe `{"t":"done","ok":true,"result":1,"reſult":2}` is accepted and gives result `2`. The broker is the only reader, so nothing can exploit the difference. But the handoff's claim ("case-folded-repeated field refused") is not true. Reuse the `unicode.SimpleFold` logic of `mcp.foldKey`.
3. When `EndRun` fails (or only the minimal fallback record is written), `res.Logs` are still returned even though the record does not hold them. Clear the logs in both branches, so the answer says what the record says.
4. `wire.Marshal(wire.Ret{Result: reply})` HTML-escapes the RawMessage (`<` becomes `<`). The script gets the same value, but different bytes than the recorded blob. Replay (BL-370) should compare parsed values, or use an encoder with `SetEscapeHTML(false)`.
5. A run frame whose stripped JS is close to 1 MiB, with many escaped characters, can go over `MaxFrame`. It then ends as `worker_crashed` where `source_too_large` is meant: check the size after JSON encoding.
6. `Broker.runCount` is unused.

## What I checked and found sound

- **Guard on every path.**
  - Denials get fixed text, never the Guard.
  - An oversized envelope is turned away before the Guard and again after it.
  - When the Guard is Incomplete, the call gets a fixed answer, the run ends with `identifier_limit`, and Withheld text is returned.
  - Host error codes pass the `wireCodes` whitelist, and the message is cut after sweeping.
  - Abandoned calls are recorded and get no answer.
  - After `cause` is set, no answer is sent.
- **Audit.**
  - One `RecordCall` per call frame.
  - The audit write runs on `WithoutCancel(parent)` with `AuditWait`.
  - The end record waits on `wg.Wait`.
  - EndRun uses `InflightWait + 10 s` after a cancel.
  - A refused end field falls back to a minimal sealed end.
- **Script input.**
  - Tool names are checked against the catalog and recorded as `unknown` otherwise.
  - Arguments must be at most 64 KiB, JCS-canonicalisable and `StrictJSON` (Unicode-folded duplicate keys, nesting bound).
  - Call ids are checked for range and for reuse.
  - Worker error codes are whitelisted.
  - Hello is checked for runtime shape, version and the required mitigations.
  - Mode and SHA256 are validated.
- **Isolation.**
  - Each run has its own Guard, Host (bridge with its own resultId cache), store handle and UUIDv7 run id.
  - `active` is keyed by run id.
  - At most 2 slots, and a refusal writes nothing.
- **Resources.**
  - Frames are capped at 1 MiB + 4 KiB before allocation.
  - Logs: at most 2000 lines and 64 KiB, with the pre-Guard size bound at +1 KiB.
  - Calls: at most 50 calls and 4 in flight.
  - Host calls: at most min(5 min, run deadline).
  - Run cap 280 s.
  - The killer, reader and clock goroutines end with the run context. Leak test present.
- **No secret or hash key in the outputs.** Only the first 8 hex of the worker hash appear, and the dashboard token never reaches an envelope.

## Not verified

No real worker, no Windows, no live case. golangci-lint was not run (CI is the check).
