# Groom 04: code mode (BL-360 to BL-379, 16 used)

Area: Parallel C (code mode sandbox). Source: research report 05 (`05_code_mode_sandboxing.md`) and `docs/desktop/DECISIONS-draft.md` sections 6 and 12. Stage 1 only: no spec, no plan, no code.

Rules used here:
- Every acceptance names its place: **CI** (GitHub runner, Linux or `windows-2025`), **live PC (nachi)** (owner's Windows PC, real case data, e.g. "Brain Sandbox Slowdown"), **elevated/UAC** (needs an admin prompt).
- "Real data path" = real sealed case, real read-only tool handlers in `internal/mcp`, real `unravis-sandbox.exe`. No mock host calls except where a test says "hostile fixture" (a hostile script is the test input, not fake data).
- Report tags: `[INFERRED]` means the report's own reasoning, not a source. Most design numbers (limits, ACL, LPAC flags) are `[INFERRED]`; treat them as proposals to verify.
- Gate: report 05 says the Windows x64 benchmark must run before the runtime is locked. DECISIONS says the same ("Gate: Windows x64 benchmark plan"). So BL-360 and BL-361 come first; BL-362 onward must not lock the runtime until BL-360 passes.
- Nothing here needs a new collector or agent. All items work on the existing read-only tools and the sealed case store.

Repo facts checked (read-only):
- `internal/mcp/schema.go` lists tools with `name`, `description`, `inputSchema`, read-only annotations. No `outputSchema` or `structuredContent` today.
- `go.mod` has `github.com/ncruces/go-sqlite3 v0.35.6` and an indirect `go-sqlite3-wasm`. There is NO direct `wazero` line. The report says "wazero already in unravis.exe". Not confirmed; see BL-360 risks.
- `esbuild` is not in `go.mod` yet.
- `--mcp-allow-identifiers` exists (`cmd/audit-explorer/main.go`, `internal/mcp/identifier_filters_test.go`).
- `internal/casestore` holds the sealed case file (Argon2id per report; not re-verified here).

---

## BL-360 Spike: Windows x64 benchmark of sandbox runtime candidates

- Outcome: measured numbers and a written decision: wazero interpreter + ACG (C1-Interp), wazero compiler (C1-Comp), or Wasmtime (C2). Runtime is locked only after this.
- Scope (in):
  - Build candidates C1-Interp and C1-Comp (Go worker). C2-Wasmtime and C3-rquickjs only if C1 fails the gate (they need a Rust toolchain; Tauri already brings one).
  - Run report workloads W1 (cold spawn + `return 1+1`), W2 (10 host calls x 50 KB, group/percentile, 10 KB result), W3 (hostile DoS suite). 100 runs after 5 warmup; p50/p95/p99.
  - Record peak working set and peak commit (`PeakProcessMemoryUsed`), binary size, Defender on, signed vs unsigned worker.
  - Hardware: Win 11 x64 desktop, Server 2022/2025 VM. Save numbers and hardware in the repo (same style as BL-300).
  - Decide: does the Job Object commit cap of 128 MiB hold, or set 2x measured p99.
- Out of scope: any product code, LPAC, MCP wiring, audit. Rust candidates unless needed.
- Depends on: P0 (BL-300 measurement style). Soft: BL-361.
- Risks / unknowns: report open items 2 and 3 (commit charge vs 128 MiB cap; interpreter speed on 500 KB to 1 MiB JSON). Report says no verified Windows x64 data exists for any candidate. Report 05 claims wazero is "already in unravis.exe"; `go.mod` shows no direct wazero. Check what `ncruces/go-sqlite3` pulls in and the real size delta before trusting the "1.5 MB" claim. Server 2016 has no data at all. Defender timing needs an unsigned vs signed pair (signing is BL-312, not ours).
- Acceptance:
  1. Gate numbers recorded: W1 p95 < 150 ms cold, < 30 ms pre-warmed; W2 p95 < 250 ms; peak commit < 96 MiB for C1-Interp. (live PC (nachi))
  2. Same suite on Server 2022/2025 VM. (live PC (nachi); needs a VM the owner has)
  3. Benchmark harness builds and runs a smoke pass (3 iterations) on `windows-2025`; numbers there are NOT used for the gate. (CI)
  4. Decision note names the chosen mode and the commit cap, with numbers. (CI: file exists check only)
- Size: M. Phase: P0 / Parallel C (first item).
- Needs owner action? No (a Server VM is needed; owner supplies if wanted).

## BL-361 Spike: QuickJS-ng WASI async host-call bridge

- Outcome: proof that `qjs-wasi-reactor.wasm` (quickjs-ng v0.18.0) can run an `async` script that awaits host calls, with an interrupt tick counter and memory limit that really fire.
- Scope (in):
  - Small C/WASI glue (about 150 LOC per the report) built with `wasi-sdk` in CI, or an audit of `fastschema/qjs`'s wasm build. Pick one, record why.
  - Prove: `__hostCall` Promise resolve/reject, `Promise.all` up to 4 in flight, `JS_SetInterruptHandler` tick counting, `JS_SetMemoryLimit`, stack cap, uncatchable OOM ends the run cleanly.
  - Pin the wasm by SHA-256; reproducible build recipe.
- Out of scope: OS sandbox, broker, audit, TS stripping.
- Depends on: none. Feeds BL-360 and BL-362.
- Risks / unknowns: report open item 4 (official reactor has no ready async bridge; custom glue needed). Report says do NOT use `fastschema/qjs` v0.0.6 (old). quickjs-ng C CVEs (CVE-2026-120438, -0821, -1144) are contained by the wasm layer only if the glue adds no unsafe host memory access. Supply chain: who builds the wasm, where is it vendored (links to BL-335/336 in the security area).
- Acceptance:
  1. Test scripts: `await` of 3 host calls returns in order; `Promise.all` of 4; a 5th concurrent call queues or is refused as designed. (CI)
  2. `while(true){}` is stopped by the tick counter within the limit; a 1 MB x N string loop hits the heap cap and the worker returns an error, host process survives. (CI)
  3. Wasm file hash in repo matches the CI rebuild. (CI)
  4. Same tests pass inside the real worker exe on Windows x64. (CI on `windows-2025`; repeat on live PC (nachi))
- Size: M. Phase: Parallel C (before BL-362).
- Needs owner action? No.

## BL-362 `unravis-sandbox.exe` worker: wazero + QuickJS, stdio protocol

- Outcome: a minimal static pure-Go worker (`CGO_ENABLED=0`) that runs one JS program in wazero + quickjs-ng with hard limits and talks NDJSON/length-prefixed JSON-RPC on stdin/stdout only.
- Scope (in):
  - Own `cmd/` entry. Contains only wazero, the embedded wasm, the stdio bridge. No SQL driver, no Key Keeper code, no DPAPI, no UI assets (report: about 4 to 6 MB, `[INFERRED]`).
  - Limits: 64 MiB linear memory (1024 pages), 32 MiB heap, 512 KiB stack, 10 s CPU / 500k tick, 30 s wall (broker side), 1 MiB per message, `console.log` 64 KiB.
  - Zero WASI preopens, no env, no net. Frozen `globalThis.unravis`; `eval`, `Function`, `fetch` removed; `SharedArrayBuffer` off; `Date.now`/`performance.now` quantized to 10 ms.
  - One run per process, then exit.
  - Chosen wazero mode from BL-360.
- Out of scope: Job Object, LPAC, mitigation policies (BL-366, BL-367); TS stripping (BL-363); real tool dispatch.
- Depends on: BL-360, BL-361. P1 not needed.
- Risks / unknowns: ACG vs wazero compiler mode (report open item 3; DECISIONS says ACG on Go exe is off "while wazero JIT is on", and security groom BL-327 depends on this answer). Wazero has no fuel (wazero issue #422), so ticks are the only deterministic cap.
- Acceptance:
  1. `go build` with `CGO_ENABLED=0` on Windows and Linux; binary size recorded and under the BL-360 budget. (CI)
  2. Protocol tests: oversized message refused; bad frame ends the run; worker exits after one run. (CI)
  3. Limits tests: memory, stack, ticks, wall all end the run with distinct error codes and no hang. (CI)
  4. `go list -deps` shows no sqlite, mongo, keeper or DPAPI package. (CI)
  5. Worker runs a script on the live PC with real `unravis.d.ts` types erased (no host calls yet). (live PC (nachi))
- Size: L (split if wasm glue grows). Phase: Parallel C.
- Needs owner action? No.

## BL-363 Broker run pipeline: TS strip, wrapper, host-call dispatch

- Outcome: the Go broker in `unravis.exe` takes TypeScript, strips it, spawns the worker, serves host calls from the REAL read-only handlers, enforces budgets, and returns the result.
- Scope (in):
  - Source cap 256 KiB. `esbuild` v0.28.2 `api.Transform` (erasable TS subset, ES2023). Reject non-erasable TS (enums, namespaces with runtime, decorators) with a plain error.
  - Wrap in an async IIFE so top-level `await` and `return` work.
  - Host-call dispatch to existing handlers in `internal/mcp` (same code path as the ten MCP tools, same case permissions). Budget: 50 calls/run, 4 in flight, 1 MiB per call result and final result, truncation flag.
  - Wall-clock timeout (30 s default, up to 120 s) with kill.
  - Run result shape `{ run_id, result, logs, stats:{calls,duration_ms,truncated}, error? }`.
  - Non-OS-hardened worker spawn is allowed here only behind a dev flag; production spawn comes from BL-366/367.
- Out of scope: tokenization (BL-365), audit (BL-369), CLI/MCP/UI surfaces, OS sandbox.
- Depends on: BL-362; P1 for the pipe if the broker lives behind it (BL-301, BL-318); BL-333 for grants (security area).
- Risks / unknowns: esbuild is a new dependency (`go.mod` has none); size and licence check. Which erasable subset exactly is `[INFERRED]` in the report. Handler reuse: do the ten tools return JSON that is safe to give raw to a sandbox before BL-365? Where the broker lives when the desktop sidecar owns the case data (DECISIONS: attach-or-start).
- Acceptance:
  1. Table tests: valid TS, invalid TS, non-erasable TS, 256 KiB + 1 source, 51st call, 5th in-flight, 1 MiB + 1 result. (CI)
  2. A script calling a real handler against a real sealed test case file returns the same rows as the direct tool call. (CI with a real case file; live PC (nachi) on "Brain Sandbox Slowdown")
  3. Killed worker mid-run: broker returns an error, no zombie process, no leaked file handle. (CI on `windows-2025`)
- Size: L. Phase: Parallel C.
- Needs owner action? No.

## BL-364 Typed bindings: `.d.ts` from MCP schemas plus `outputSchema` on the ten tools

- Outcome: `unravis.d.ts` is generated from the same Go tool registry as `tools/list`, and every one of the ten tools declares an `outputSchema` (and `structuredContent`) so types are concrete.
- Scope (in):
  - About 250-line Go walker (no dependencies): `inputSchema`/`outputSchema` to TS; `$ref` depth cap 12, visited set; unsupported constructs (`patternProperties`, complex `oneOf`) become `unknown`.
  - Sanitize tool names. Emit `schema_sha256` header.
  - Add `outputSchema` to all ten tools in `internal/mcp/schema.go` and structured output in the responses, without changing tool names or existing text results.
  - Golden `.d.ts` test.
- Out of scope: dynamic tool catalogs, `notifications/tools/list_changed` (DECISIONS: no dynamic catalogs yet); new tools.
- Depends on: none for the walker. BL-333 and the MCP area (Parallel A) own the MCP contract; this item must follow their structured-output decision.
- Risks / unknowns: conflict with Parallel A: DECISIONS section 5 says "Keep the ten tool names, add structured output" under that area. Who writes the `outputSchema` PR? Existing results might not match a clean schema (nullable fields, redacted fields). Report example uses a made-up `list_slow_requests`; real tool names must be read from the repo.
- Acceptance:
  1. Golden test: generated `.d.ts` for the ten real tools is stable; recursive schema fixture does not loop. (CI)
  2. Every real tool response from a real case validates against its own `outputSchema`. (CI with a real case file; live PC (nachi) on a live case)
  3. Generated `.d.ts` compiles with the pinned TypeScript checker in CI only (not shipped; report says never ship `tsc`). (CI)
- Size: M. Phase: Parallel C (shares work with Parallel A).
- Needs owner action? No.

## BL-365 Privacy: identifier tokenization, error scrubbing, clock

- Outcome: by default the sandbox never sees raw user IDs, IPs or user agents; errors leaving host calls contain no connection strings, paths or SQL literals.
- Scope (in):
  - Deterministic per-run tokens (`[USER_1]`, `[IP_1]`, `[UA_1]`) applied in the broker before results enter the worker; mirror of `--mcp-allow-identifiers` (same flag semantic, same switch for CLI `--allow-identifiers`).
  - Same token map used consistently inside one run so group/join/count still work.
  - Error scrubbing for connection strings, file paths, raw SQL literals.
  - Output caps: `console.log` 64 KiB, per-call and final 1 MiB (already in BL-363; test here).
- Out of scope: re-identification UI; LLM-side prompt injection defence.
- Depends on: BL-363. Reuses existing identifier filtering in `internal/mcp` (read it first).
- Risks / unknowns: which fields count as identifiers is decided by the existing MCP filter, not the report; tokens might leak through free-text fields (SQL text, paths, URLs with IDs). Report marks the tokenization design `[INFERRED]` (precedent is Anthropic's post, not an Unravis fact). Return values leave the sandbox with tokens, so the audit store (BL-369) must store tokens, not raw values, unless allowed.
- Acceptance:
  1. Fixture run with a real case containing real-shaped IDs/IPs: default output has zero raw identifiers (regex sweep over result, logs, errors); `--allow-identifiers` shows them. (CI with a real case file; live PC (nachi) on "Brain Sandbox Slowdown")
  2. Same script grouped by `[USER_1]` gives the same counts as grouping by raw ID in the allow mode. (CI)
  3. Forced host error with a real connection string in it: scrubbed. (CI)
- Size: M. Phase: Parallel C.
- Needs owner action? No.

## BL-366 Windows hardening layer A: Job Object, handle list, mitigation policies

- Outcome: the broker spawns the worker unelevated, in a Job Object, with only the two pipe handles inherited, and with process mitigation policies on.
- Scope (in):
  - Job: `KILL_ON_JOB_CLOSE`, `ACTIVE_PROCESS=1`, process and job memory cap (from BL-360), CPU hard cap 50% (ignore non-fatal failure when RDS DFSS is on), all 8 UI restrictions; assignment through `PROC_THREAD_ATTRIBUTE_JOB_LIST`.
  - `PROC_THREAD_ATTRIBUTE_HANDLE_LIST` with `bInheritHandles=TRUE` and only the stdio pipes. `DETACHED_PROCESS` so `conhost.exe` does not join the job.
  - Mitigations: `WIN32K_SYSTEM_CALL_DISABLE`, `CHILD_PROCESS_RESTRICTED`, `STRICT_HANDLE_CHECKS`, `FONT_DISABLE`, `IMAGE_LOAD_NO_REMOTE/NO_LOW_LABEL/PREFER_SYSTEM32`, `EXTENSION_POINT_DISABLE`, `FSCTL_SYSTEM_CALL_DISABLE`; ACG per BL-360 decision.
  - Fresh process per run; optional one pre-warmed worker, killed after one run.
  - Windows only; Linux/macOS delta stays deferred (see Out of scope).
- Out of scope: LPAC token (BL-367); Linux seccomp/namespaces and macOS Seatbelt (report `[INFERRED]` delta, Windows first per owner goals; add later items on the owner's go); this app's own Tauri Job Object (BL-306).
- Depends on: BL-362, BL-360 (cap, ACG). BL-327 (security area) for how `unravis.exe` itself is mitigated.
- Risks / unknowns: Job memory limits commit not working set (report open item 2). A worker inside a job already inside the Tauri sidecar job: nested job support is Windows 8+, but exact behaviour with `KILL_ON_JOB_CLOSE` when the parent job closes is not in the report; test it. `conhost` attach quirk is from a Q&A post (2026-10-12), single source.
- Acceptance:
  1. Test worker that tries to spawn a child, open a window, read the clipboard: each fails. (CI on `windows-2025`)
  2. Handle audit: the worker holds only the two pipes plus system handles (NtQuery or Process Explorer-style listing in a test); no SQLite, keeper or listener handle. (CI on `windows-2025`; repeat on live PC (nachi) with the real app and case open)
  3. Kill the broker with Task Manager mid-run: worker dies (job close). (live PC (nachi))
  4. Process Explorer / `Get-ProcessMitigation` shows the policies on the real worker. (live PC (nachi))
- Size: L. Phase: Parallel C.
- Needs owner action? No.

## BL-367 Windows hardening layer B: LPAC token, verification, restricted-token fallback

- Outcome: the worker runs in a zero-capability Less Privileged AppContainer where available; where not, in a Restricted Token + Untrusted IL fallback that is clearly flagged as degraded.
- Scope (in):
  - `CreateAppContainerProfile("Unravis.CodeSandbox.v1")` with `ERROR_ALREADY_EXISTS` handling; `SECURITY_CAPABILITIES` with zero capabilities; `ALL_APPLICATION_PACKAGES_POLICY` opt-out (LPAC).
  - Create suspended, check `TokenIsLessPrivilegedAppContainer`, then resume; refuse to run if the check fails.
  - Fallback: `CreateRestrictedToken` + Untrusted IL (S-1-16-0); set `lpCurrentDirectory` to System32.
  - Degraded fallback recorded in the audit field `sandbox_mode` and shown in CLI/UI; Server 2016 requires `--allow-degraded-sandbox` (report suggestion `[INFERRED]`).
  - Network test: no outbound TCP/UDP, no localhost pivot to the Unravis HTTP API or pipe.
- Out of scope: installer ACLs (BL-368); WFP rules the OS already applies to AppContainers.
- Depends on: BL-366, BL-368 (worker must be readable by `S-1-15-2-2`).
- Risks / unknowns: report open items 5 and 6. LPAC needs Win10 1703+ / Server 2019+; fallback does NOT block network on Server 2016 (no WFP). Session 0 / service launch fails for AppContainer (Q&A 2021, `[OLD]`); is code mode ever started from a service? Desktop and CLI are user context; confirm. Kernel escapes exist (CVE-2026-85880); that is why layers 1 and 2 are mandatory. Whether Blackbox hosts (Server 2016) need code mode at all is a spec question.
- Acceptance:
  1. Token check test: worker token has `TokenIsLessPrivilegedAppContainer` true on Win 11. (CI on `windows-2025`; live PC (nachi))
  2. From inside the worker (hostile fixture): `connect()` to 127.0.0.1 and to the real Unravis port/pipe fails; open `session.json`, case file, Key Keeper pipe: access denied. Run against the real running app. (live PC (nachi))
  3. Fallback path forced by a test switch: runs, writes `restricted_token_fallback`, banner shown. (CI on `windows-2025`)
  4. Fallback on a real Server 2016 host: runs only with the explicit flag; network is NOT blocked, and the test documents that. (live PC (nachi); needs a Server 2016 box the owner may not have)
- Size: L. Phase: Parallel C.
- Needs owner action? No (Server 2016 box if wanted).

## BL-368 Installer: sandbox exe placement and `S-1-15-2-2` ACL, kept through OTA and recover

- Outcome: setup installs `unravis-sandbox.exe`, grants it Read and Execute to `ALL RESTRICTED APPLICATION PACKAGES`, and re-applies that on every upgrade, rollback and `--recover`.
- Scope (in):
  - Add the file to the release set and `unravis-setup.exe` layout; ACL set on the exe and its folder path as needed (find the minimal set; report only names the exe).
  - Re-apply on OTA, rollback (BL-169) and `--recover` (BL-170).
  - Verify step: a self-check (`unravis code doctor` or part of BL-371) says "ACL ok / missing".
  - `verify-release` (BL-153) checks the worker runs after upgrade.
  - Release manager (BL-194) signs and lists the new file; hash chain unaffected.
- Out of scope: Authenticode (BL-312); changing the OTA design.
- Depends on: BL-362 (a file to ship), BL-149 line (updater), BL-194, P4 for the installer integration with the desktop exe. Parallel with BL-367.
- Risks / unknowns: report open item 7. ACL on a per-user install path vs Program Files; setup elevated vs not (per-user install may have no admin step). Defender/unsigned worker friction (report risk 1); signing is owner-side BL-312. File-lock during OTA if the worker is running (it is one-shot, so likely free; verify).
- Acceptance:
  1. Setup test on a clean `windows-2025` runner (standard user, per-user install): file present, ACL contains `S-1-15-2-2` Read and Execute, LPAC launch succeeds. (CI)
  2. Upgrade N-1 to N, forced rollback, and `--recover` each leave the ACL correct and a code run succeeds. (CI; repeat on live PC (nachi))
  3. Per-machine install path (Program Files): ACL set by the elevated setup. (elevated/UAC)
- Size: M. Phase: Parallel C, joins P4 (installer).
- Needs owner action? Yes, only the UAC prompt for the per-machine install test.

## BL-369 Audit store: `code_runs` and `code_run_calls` in the sealed case DB with hash chain

- Outcome: every run is recorded in the sealed case database before it starts, then finished, in a tamper-evident chain.
- Scope (in):
  - Schema as in the report (run row with `status='running'` before spawn; call rows with canonical args JSON, hashes, bytes, optional compressed result blob, status, duration).
  - RFC 8785 JCS canonical JSON; `record_hash = SHA-256(JCS(record))`; `prev_hash` link per case.
  - Crash recovery: a row left `running` is closed as `crashed` at next open.
  - Retention: result blobs kept for the last 100 runs or 50 MiB (report example, `[INFERRED]`); setting to disable blobs (hash-only).
  - Migration through the existing case store version path; works on sealed files.
  - Stored values follow tokenization (BL-365); raw values only in allow mode, flagged in the row.
- Out of scope: replay (BL-370); UI (BL-373); cross-case chain.
- Depends on: BL-363, BL-365. Existing `internal/casestore` (read its migration rules first).
- Risks / unknowns: report marks this decision "Medium" confidence and cites an IETF draft (`draft-sharif-agent-audit-trail-10`), an unfinished standard; we copy ideas, not compliance. Result blobs enlarge sealed case files and backups; export/archive behaviour of `casefile_archive.go` is unchecked. Does a chain inside the case DB survive case archive/restore and key rewrap (`rewrap.go`)? Who may write: only the broker. Owner rule on deleting user data: retention pruning of blobs deletes data the user did not choose to delete. Needs a spec decision and a visible setting.
- Acceptance:
  1. Unit tests: chain verifies; editing one byte of any row breaks `--verify`; kill during run leaves a closed `crashed` row. (CI)
  2. Real sealed case file: migrate, run 3 scripts through the real pipeline, close, reopen with the real key: rows and chain intact. (CI with a real case file; live PC (nachi) on "Brain Sandbox Slowdown")
  3. Old Unravis build opens a migrated case without data loss (or refuses clearly). (CI)
- Size: L. Phase: Parallel C.
- Needs owner action? Yes, only to confirm the retention default (deleting stored results is user data deletion; ask in spec stage).

## BL-370 Replay and verify (`replay-divergence`)

- Outcome: a recorded run can be replayed offline from its stored call results, and the chain checked.
- Scope (in):
  - `replay <run-id>`: re-run `source_ts` in a fresh worker with frozen clock; serve each `seq` from the stored blob; abort with `replay-divergence` if `(tool_name, args_hash)` differs.
  - `--verify`: check JCS hash chain and per-call hashes; report first broken row.
  - Hash-only runs (blob off): verify only; replay says "not replayable".
- Out of scope: re-running against live data (that is a normal run); UI button (BL-373).
- Depends on: BL-369, BL-363.
- Risks / unknowns: determinism: scripts that use `Date.now`, `Math.random`, or object key order from the host. Report says quantize clock and use a "deterministic replay clock" but gives no RNG rule; spec must decide (freeze or seed `Math.random`). Replay of a run recorded in allow-identifiers mode must not leak into a deny-mode context.
- Acceptance:
  1. Replay of a real recorded run returns the same result hash. (CI with a real case file; live PC (nachi))
  2. Tampered blob, tampered source, changed call order: each gives the right error (`verify-failed` / `replay-divergence`). (CI)
  3. Script using `Math.random`/`Date.now` replays identically (per spec decision). (CI)
- Size: M. Phase: Parallel C.
- Needs owner action? No.

## BL-371 CLI: `unravis code types | run | replay`

- Outcome: the three subcommands from the report run through the same broker.
- Scope (in):
  - `code types` (print `.d.ts` + limit summary), `code run [--file|--stdin] [--case <id>] [--allow-identifiers] [--json]`, `code replay <run-id> [--verify]`, plus `code doctor` (sandbox mode, ACL, versions) as a small extra.
  - Stable JSON output and exit codes; follows the CLI contract of Parallel B (Cobra, versioned JSON, JSON Lines, error codes). Until that lands, ship with the same flags and a plain JSON shape, and adopt the contract when ready.
  - Console-subsystem `unravis.exe`; never starts the GUI; works with the sidecar owner or offline reader per DECISIONS section 7.
- Out of scope: the CLI framework itself, exit-code map, completion (Parallel B).
- Depends on: BL-363, BL-364, BL-365, BL-369 (audit), BL-370 for replay. Parallel B contract.
- Risks / unknowns: CLI contract not yet defined; this item may need rework after Parallel B. Behaviour when no owner process exists (headless owner vs offline reader) needs the Parallel B decision. `--allow-identifiers` on a CLI: the user is the operator, so it is allowed, but audit must record it.
- Acceptance:
  1. Golden JSON output and exit codes for success, script error, timeout, limit, denied. (CI)
  2. `code run` on a real case on a real PC: result matches direct tool calls; the run appears in the case audit table. (live PC (nachi))
  3. `code types` output equals the BL-364 golden. (CI)
- Size: M. Phase: Parallel C (after Parallel B contract; else P-flexible).
- Needs owner action? No.

## BL-372 MCP: `unravis_types` and `unravis_execute`

- Outcome: AI hosts can discover types and run code through the existing stdio MCP server; the ten direct tools stay unchanged.
- Scope (in):
  - Two new tools in `unravis --mcp`; `unravis_execute` input `{code, timeout_ms?, reason?}`, output `{run_id, result, logs, stats, error?}`; `unravis_types` returns the `.d.ts` and a limit summary. Keep `unravis_execute` schema byte-stable (prompt cache).
  - Access through the per-installation revocable grant from BL-333 (a separate "code" grant is the proposal; the report does not say, so it is a spec question).
  - Identifiers off unless the operator started `--mcp-allow-identifiers`. The model cannot turn it on.
  - Tool description warns that data from the case is untrusted (prompt injection).
  - Audit surface `mcp`, caller id from the grant.
- Out of scope: removing direct tools (report keeps all ten, to avoid the +21 to +38% small-task cost at N=5, per Pydantic's benchmark, vendor-reported); dynamic catalogs; sampling/elicitation.
- Depends on: BL-363, BL-364, BL-365, BL-369; BL-333 (grants), BL-318 (MCP over pipe); Parallel A SDK parity spike.
- Risks / unknowns: Parallel A owns the MCP protocol/SDK choice (official Go SDK; two wire lifecycles); this item may need rework. MCP host timeouts vs 30 s run. Whether "read-only annotations" are still true for a tool that runs code (annotations are hints; the sandbox is the enforcement). Benchmark claims in the report are third-party and not measured on Unravis tasks.
- Acceptance:
  1. Protocol tests on both MCP lifecycles: both tools listed, schema stable across runs, errors well-formed, grant revoked means denied. (CI)
  2. Real MCP client (Claude Code or Claude Desktop on the owner's PC) calls `unravis_types` then `unravis_execute` against a real case, with a multi-step script that the direct tools need 5+ calls for. (live PC (nachi))
  3. Prompt-injection fixture: a real case record containing instructions in a field does not change the sandbox policy; output has no raw identifiers. (CI with a real case file)
- Size: M. Phase: Parallel C (after Parallel A).
- Needs owner action? No (owner runs the real client check).

## BL-373 UI: read-only Code Runs and Audit inspector

- Outcome: a tab that lists every run for the case: code, host-call timeline, redaction badges, sandbox mode, chain status; with a Replay / Verify button.
- Scope (in):
  - New Svelte 5 view reading run data through the existing API (browser mode and desktop mode both). Highlighted source shown as plain text with a safe highlighter (no `{@html}`; strict CSP from BL-322/323).
  - Timeline of calls (tool, duration, bytes, status). Badge for `restricted_token_fallback` and for allow-identifiers.
  - Replay / Verify button calls BL-370.
  - Plain-words copy. Phone-width layout (no sideways scroll, like BL-050).
- Out of scope: editing or deleting runs; the scratchpad (BL-374).
- Depends on: BL-369, BL-370, a backend read endpoint for runs; BL-323 (CSP rules) for desktop.
- Risks / unknowns: highlighter must work under strict CSP (no inline style/eval) and treat code as untrusted text. Large `result_preview` and console logs must be capped in the UI. The "Brain Sandbox Slowdown" case is the live case; it will need real runs first (BL-371).
- Acceptance:
  1. Component and API tests; XSS fixture: source containing `<img onerror>` renders as text. (CI)
  2. After real runs from BL-371/372 on the live case, the tab lists them and Verify says OK; after a DB edit, Verify shows broken. (live PC (nachi))
  3. Playwright check at 1280 px and 400 px (no horizontal scroll). (CI; repeat on live PC (nachi))
- Size: M. Phase: Parallel C (UI can ship in browser mode before P3).
- Needs owner action? No.

## BL-374 UI: manual script runner (scratchpad)

- Outcome: the person can type a script in the app and run it through the same broker (the report calls this "optional").
- Scope (in):
  - Textarea editor, `unravis.d.ts` shown as help, Run button, result and logs panel, link to the new run in the inspector.
  - Allow-identifiers toggle is a clear per-run switch (user is the operator) and recorded.
  - Cancel button (kills the worker).
- Out of scope: auto-complete/Monaco (large, CSP-heavy); saving script libraries.
- Depends on: BL-373, BL-363, BL-365.
- Risks / unknowns: report says "optional"; the owner has not asked for it. Per the repo's "Backlog (deferred)" rule, do not build without the owner's go. Marked here so spec stage can drop it. Typing code into a desktop WebView adds an input surface to the strict CSP shell; no `eval` is needed since the code runs in the Go-side sandbox.
- Acceptance:
  1. API test: UI run goes through the same broker and audit path as CLI. (CI)
  2. Real case on a real PC: run a script, cancel a `while(true){}` run, see both in the inspector. (live PC (nachi))
- Size: S. Phase: Parallel C (last; needs owner go).
- Needs owner action? Yes: owner go to build it (optional item).

## BL-375 Escape, exfiltration and limits test suite (hostile fixtures, real worker)

- Outcome: one repeatable suite that proves the sandbox claims against the real worker exe, and it runs in CI and on the live PC.
- Scope (in):
  - Report W3 cases: infinite loop (stops within timeout + 50 ms), memory bomb (clean OOM, `unravis.exe` survives), `fetch` and raw socket to 127.0.0.1 (blocked), 51st call (abort at 51).
  - More: `eval`/`Function`/`import()`/`WebAssembly` access, prototype pollution of `unravis`, `Proxy`/TypedArray crash fixtures for known quickjs-ng CVE classes, huge JSON, deep recursion, timing precision, console flood, output over 1 MiB.
  - Handle, token, mitigation, ACL, job checks reused from BL-366/367/368 as one suite.
  - Report-style scorecard saved with the build (pass/fail per control).
  - `cargo-mutants`-style diff mutation is already a DECISIONS gate for the "sandbox core"; define the Go equivalent (`go-mutesting` or similar) for the broker limits code. Tool choice is open.
- Out of scope: fuzzing quickjs-ng itself; kernel exploits.
- Depends on: BL-362, BL-363, BL-366, BL-367; BL-368 for installed-path runs.
- Risks / unknowns: LPAC tests need a real Windows runner (`windows-2025`); network test may behave differently in CI (service session). Report names CVE ids with heavy 2026 numbers that I could not re-check; treat them as "claims in the report". AppContainer kernel escapes cannot be tested; layers 1 and 2 are the answer.
- Acceptance:
  1. Full suite green on `windows-2025`; failing any control fails the build. (CI)
  2. Same suite against the installed build on the owner's PC with the real app and real case running, including the check that the real Unravis HTTP/pipe endpoints are unreachable from the worker. (live PC (nachi))
  3. Fallback-mode suite (restricted token) shows exactly which controls drop (network) and that all others still pass. (CI on `windows-2025`; Server 2016 part on live PC (nachi))
- Size: M. Phase: Parallel C (exit gate: "escape tests and limits tests green").
- Needs owner action? No.

---

## Order and gates

1. BL-360 and BL-361 first (decide runtime).
2. BL-362 then BL-363; BL-364, BL-365 in parallel.
3. BL-366, BL-367, BL-368 (OS layer) can start once BL-362 exists.
4. BL-369, BL-370, then surfaces BL-371, BL-372, BL-373; BL-374 last and only with owner go.
5. BL-375 is the exit gate. Do not ship `unravis_execute` to users before BL-375 is green on a live PC.

Phase summary (all Parallel C unless noted): P0: BL-360. P4 join: BL-368.
Blocks other areas: none. Items deferred and needing owner go: BL-374; Linux/macOS sandbox; Wasmtime/Rust worker (only if BL-360 fails).

## Conflicts with other areas

1. **MCP (Parallel A):** `outputSchema`/structured output (BL-364) and the new tools (BL-372) change the MCP contract that area owns; the SDK parity spike and the two wire lifecycles come first. Grants (BL-333) must include a code grant.
2. **CLI (Parallel B):** BL-371 needs the CLI contract (Cobra, JSON, exit map). Order: B first, or accept rework.
3. **Security (groom 02):** BL-327 (ACG on `unravis.exe`, "off while wazero JIT is on") depends on the BL-360 wazero mode. Report says ACG on the worker is wanted; DECISIONS says ACG off for the Go exe. These can both hold (different exe) but must be written down. BL-366 job rules must nest under the sidecar job from BL-306 (groom 01 already flags this). The worker is spawned by the Go broker, not Rust.
4. **Shell/sidecar (groom 01):** broker location when the desktop sidecar owns the case data (attach-or-start, BL-302). The code-mode broker must run in the process that owns the sealed case, so CLI/MCP code runs go through the pipe (BL-318) or an offline reader.
5. **Installer/OTA (BL-149 to BL-194):** new signed file, ACL re-apply on upgrade/rollback/recover (BL-368), `verify-release` extension (BL-153), release manager file lists. Owner-side signing (BL-312) is needed before shipping widely (report risk 1: unsigned worker and Defender).
6. **Performance (DECISIONS section 9):** worker adds installer size (budget 25 MB total; worker 4 to 6 MB is `[INFERRED]`) and a per-run process; no tray-idle cost if no worker is pre-warmed while idle. Pre-warm must be off when tray-idle.
7. **Case store / data:** `code_runs` adds data to sealed case files; archive/rewrap/backup size and the "never delete user data" rule need a decision (BL-369).
8. **Supply chain (BL-335/336):** new deps (`esbuild`, `wazero` direct, a pinned wasm) and a `wasi-sdk` build step need cargo-deny/govulncheck/SBOM coverage.
9. **Testing area:** needs `windows-2025` runners; LPAC and Server 2016 checks may need boxes the owner lacks.
10. **Data folder move (BL-316):** the LPAC ACL and worker path must follow the new `%LOCALAPPDATA%\Unravis` layout if the exe moves.

## Questions for spec stage

1. After BL-360: interpreter + ACG, or compiler mode (and what is the commit cap)? Is Wasmtime worth a Rust worker at all?
2. Is `wazero` really already linked (report says yes; `go.mod` shows no direct line)? What does it cost in size?
3. Which erasable TS subset is allowed; is plain JS also accepted?
4. Separate "code" grant in MCP, or the same grant as the ten tools? Can a grant allow code but not identifiers (yes per report, confirm)?
5. Result blob retention default (100 runs or 50 MiB) and the pruning rule; do blobs go into case archives and backups?
6. Replay clock and `Math.random` rules.
7. Server 2016 and Session 0: is code mode offered there at all, or desktop/CLI user context only? Allow degraded mode with a flag?
8. Pre-warmed worker: on or off (cold p95 vs idle cost)?
9. Does code mode ever run against live sources (SQL/Mongo) or only the ten read-only tools over the sealed case? The report says "existing read-only handlers"; Blackbox/SQL access details are not in the report.
10. Which release does code mode wait for: does it ship behind an opt-in until signing (BL-312) lands?
11. Linux/macOS delta: needed now, or deferred (owner goal says Windows first)?
12. Who pays for the Windows x64 benchmark time and a Server VM (owner hardware)?
13. Do we want `unravis code doctor` (my addition, not in report)?
14. Are UI scratchpad (BL-374) and the optional Wasmtime path wanted at all?
