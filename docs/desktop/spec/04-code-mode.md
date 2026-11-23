# Spec 04: code mode (BL-360 to BL-376)

Status: spec v1, 2026-11-21. Input: `groom/04-code-mode.md`, `DECISIONS-draft.md` sections 6 and 12, research report 05, specs 01 and 02, and the code at `/home/claude/unravis`. Stage: spec only. No code was changed.

## 0. Where the code differs from the report and the groom (read first)

| Claim | What the repo shows | Effect |
|---|---|---|
| "wazero already in `unravis.exe`" | `go.mod` and `go.sum` have no `wazero` and no `tetratelabs`. `ncruces/go-sqlite3 v0.35.6` + `go-sqlite3-wasm/v6` (indirect) is used, not wazero. | wazero is a NEW direct dependency. Its size cost goes into the worker only, measured in BL-360. The "1.5 MB" and "4 to 6 MB" figures are unproven. |
| Ten tools are `unravis.*` (example `list_slow_requests`) | The ten tools are `audit_get_activity`, `audit_get_guide`, `audit_list_sources`, `audit_compare`, `audit_get_groups`, `audit_get_timeline`, `audit_compare_timelines`, `audit_get_requests`, `audit_get_slowest`, `audit_get_breakdown` (`internal/mcp/schema.go`). | The script API keeps these exact names: `unravis.audit_compare(...)`. No renaming layer. |
| Tools run "against the sealed case" with "active case permissions" | The tools are SOURCE-scoped. `mcp.Bridge` (`internal/mcp/bridge.go`) is an HTTP client of the running dashboard (`/api/sources`, `/api/analyze`, `/api/requests`, `/api/activity`, `/api/slowest`, `/api/breakdown`, `/api/info`) with `X-Audit-Token`. A cache of the latest comparison (`resultId`, 10 min) lives inside each `Bridge`. | The broker lives in the dashboard process (the one that owns the sources and the cases). Only the AUDIT RECORD goes to a case. See D9. |
| Identifiers need new tokenization (`[USER_1]`) | Already done by the tools: with `--mcp-allow-identifiers` off, user ids and user agents come back as `hidden-<rank>`, IPs, URLs and headers are never returned, identifier filters are refused (`breakdown.go hideIdentifiers`, `checkIdentifierFilters`). | BL-365 shrinks: reuse that, add an output sweep and error scrubbing. The sweep gives the values it finds per-run tokens (`[IP_1]`, `[USER_1]`, `[UA_1]`) numbered by first sight in the text, never by value. See D11. |
| 1 MiB cap "already in `--mcp`" | True for the stdio message (`maxMessage = 1<<20`, `protocol.go`). The HTTP body cap is 16 MiB (`maxBody`). | Broker enforces its own 1 MiB per call and per result. |
| Run limit 30 s wall | A single tool call may run 5 minutes (`audit.OperationTimeout`); the HTTP client timeout is 305 s. | Two clocks. See D3. |
| MCP says "no code execution exposed" | `initialize` instructions in `protocol.go` say exactly that. | The text changes only when code tools are switched on (D7). |
| CI `windows-2025` | `ci.yml` pins `windows-2022` (spec 01 keeps it). | Sandbox suite gets its own workflow on `windows-2025` (D18). |
| Pin helper by hash | `build.ps1 -Release` builds the Blackbox first and pins `unravis.exe` to its SHA-256 (SEC-FIX-01); `release.yml` checks the pin. | Same pattern for the worker (D5). Reuse, do not invent. |
| Case DB migration | `casestore/sqlite/schema.go` `schemaVersion = 4`; blobs such as `workspace_blob` use `CREATE TABLE IF NOT EXISTS` on first write and add no schema version. | Audit tables follow that pattern (D10): an old build opens the case and ignores them. |
| `unravis.exe` is a console exe | Built `-H=windowsgui` (spec 01 section 0). | Worker is built `-H=windowsgui` too (no conhost). |
| Existing Server tests | Linux baseline for `internal/server` is the 19 known Windows-only failures. | New server tests must not add to it; Windows-only code carries build tags. |

## 1. Purpose and boundaries

Owns: the sandbox worker `cmd/unravis-sandbox`, the broker `internal/codemode` (TS strip, run pipeline, host-call dispatch, budgets), the Windows spawn hardening `internal/codemode/winsandbox`, the type generator `internal/codemode/tsgen`, outputSchema for the ten tools, the audit store and hash chain for runs, replay and verify, the routes `/api/code/*`, `unravis code ...` logic (`internal/codecli`), the two MCP tools, the read-only UI tab, the escape test suite, the worker's installer ACL requirement.

Does not own: MCP protocol, SDK choice, grants, pipe roles (areas 03, 01, 02); the CLI router, exit-code map, JSON envelope rules (area 05); installer framework, OTA, signing (area 06); WebView2 and CSP (area 02); budgets and CI cost gates (area 07, 08). Linux and macOS sandbox, Wasmtime/Rust worker, scratchpad (BL-374): deferred, owner go needed.

Nothing here needs a new collector or agent. Everything reads through the ten existing read-only tools.

## 2. Resolved decisions

| # | Question or conflict | Decision | Reason |
|---|---|---|---|
| D1 | Runtime (Q1) | Default until BL-360: wazero **interpreter** + ACG on the worker (C1-Interp). Switch to C1-Comp (compiler, ACG off) only if W2 p95 > 250 ms or peak commit > 96 MiB. Wasmtime/Rust only if both fail; that needs an owner go. **Decided by spike BL-360.** | Report gate; keeps one toolchain. |
| D2 | Is wazero linked (Q2) | No. See section 0. It is added to `go.mod` by BL-361/362, only imported from `cmd/unravis-sandbox`. `go list -deps ./cmd/audit-explorer` must NOT show wazero (test `TestAppDoesNotLinkWazero`). | Keeps `unravis.exe` small. |
| D3 | Clocks | Three: (a) script time = wall time while no host call is in flight, default 30 s, max 120 s, set by `timeoutMs`; (b) tick budget 500,000 interrupt ticks (calibrated by BL-361 to about 10 s of JS); (c) run total = 280 s hard cap (under the 305 s HTTP client timeout and 5 min tool cap). One host call may use up to the remaining run time, max 5 min. | Tools can run 5 min; script CPU must still be bounded. |
| D4 | Language (Q3) | Plain JS and TS both accepted. esbuild strips types. No "erasable subset" gate: esbuild cannot enforce one and enums or namespaces compile to harmless plain JS. `import`/`export` are refused. Source is wrapped BEFORE the strip: `(async()=>{` + source + `\n})()` on the same first line, so `await` and `return` work and line numbers match. | Safe, simple, no false rejects. Report's "erasable" claim dropped. |
| D5 | Worker placement and trust | `<InstallDir>\sandbox\unravis-sandbox.exe`. `unravis.exe` is pinned to its SHA-256 like the Blackbox (`build.ps1` builds the worker first; ldflags `-X api-audit-explorer/internal/codemode.WorkerSHA256=<hex>`; empty pin fails `-Release`; `-Unpinned` for dev). Broker hashes the file before every spawn. | Stops a swapped worker. Pattern exists. |
| D6 | Which hosts run code mode (Q7, Q11) | Windows 10 1809+/11 and Server 2019+ in a user session only, never from a service or Session 0. Anything else (Server 2016, LPAC unavailable, profile create fails): refuse with `sandbox_unavailable`, unless setting `allowDegraded` is true; then restricted token + Untrusted IL, result flagged `restricted_token_fallback`, network NOT blocked, banner in CLI and UI. Default false. Linux and macOS: refuse (`sandbox_unavailable`); broker unit tests use the `codedev` build tag spawner only. | Fail closed; Windows first. |
| D7 | Grant and MCP exposure (Q4) | Two gates. (1) Operator opt-in: `unravis --mcp --mcp-code` adds the two tools to `tools/list`. Without it `tools/list` and `instructions` are byte-identical to today. (2) After area 03 BL-346: a separate `code` grant, which can be given without identifiers. Until grants exist only gate 1 applies. The model cannot turn identifiers on; `--mcp-allow-identifiers` stays operator-only. | Mirrors the existing switch; area 03 owns grant storage. |
| D8 | Default on or off (Q10) | Code mode is OFF by default (`code-mode.json` `enabled=false`), per user, opt-in in the UI and `unravis code enable`. It stays opt-in after signing lands; first release labels it "preview". Not shipped to users before BL-375 is green on the live PC. | Security first; Defender friction unsigned. |
| D9 | Case scope and audit home | A run MUST be attached to an open case; its audit rows go into that case. `caseId` is required, except: if exactly one case is open it is used. No open case: refuse `no_case` ("Open a case first, runs are recorded in it"). Tools stay source-scoped as today. | Audit before run, in sealed storage; honest to the code. A case-free log would be a second store: not built (see risk R9). |
| D10 | Audit tables and retention (Q5) | Tables `code_runs`, `code_run_calls`, `code_chain` created `IF NOT EXISTS` in the case DB; no schema version bump; old build opens the case and ignores them (test). Result blobs: kept for ALL runs by default (`keepResults:"all"`); setting `"off"` stores hashes only for NEW runs. No automatic deletion ever. Size shown in the inspector and `code doctor`. Manual prune is a separate item BL-376 (deferred, owner go; it deletes user data). Blobs live in the case DB, so archive/restore/backup carry them (test `TestCodeRunsSurviveArchiveRestore`); rewrap changes the file key only, rows stay. | Owner rule: never delete user data silently. |
| D11 | Identifier privacy (BL-365) | Reuse the tools' own hiding (section 0). Broker adds: output sweep (IPv4, IPv6, 24-hex ids not from `_id` fields are NOT swept; only IPs and strings equal to a known raw user agent are checked) as defense in depth, applied to final result and logs when identifiers are hidden (found values become per-run tokens, so grouping still works; secrets are scrubbed in every mode); error scrubbing for connection strings, Windows paths, SQL literals. In allow mode nothing is hidden, the run row has `allow_identifiers=1`, and the UI shows a badge. Replay of an allow-mode run is refused unless the caller also allows identifiers. | No second hiding system to keep in sync. |
| D12 | Replay clock and RNG (Q6) | Worker `Date.now()` and `performance.now()` are quantized to 10 ms from a host base. `Math.random` is a seeded xorshift128+; the seed is recorded in `code_runs.seed`. Replay uses the recorded start time as a frozen clock, the same seed, and releases call results in the recorded completion order (`done_seq`). A different `(tool, args_hash)` at any `seq` aborts `replay-divergence`. A different result hash with equal calls reports `replay-result-differs` (not an error exit). | Deterministic where it can be; honest where not. |
| D13 | Pre-warm (Q8) | Off. No idle worker, ever (tray-idle budget). Cold spawn must pass W1 p95 < 150 ms. If it fails, an opt-in pre-spawn only while an MCP or CLI code session is active is allowed. **Decided by spike BL-360.** | Footprint. |
| D14 | Live sources (Q9) | Only the ten read-only tools. No SQL, Mongo, files or network from scripts. | Report scope. |
| D15 | Doctor (Q13) | Yes: `unravis code doctor` and `GET /api/code/doctor`. It works offline (no dashboard, no case): checks worker file, pin, ACL, token check (`TokenIsLessPrivilegedAppContainer`), runs `return 1+1` through the real worker. | Needed to verify installs (BL-368). |
| D16 | Scratchpad, Wasmtime (Q14) | Not built. BL-374 and the Rust worker stay deferred. | Backlog rule. |
| D17 | Platform delta (Q11) | Deferred. | Owner goal. |
| D18 | CI (Q12) | New workflow `.github/workflows/code-sandbox.yml`: job `windows-2025` for LPAC suite and smoke bench; Linux job for broker and wire tests (tag `codedev`). `ci.yml` stays on `windows-2022`. Gate benchmark numbers come only from nachi's PC (and a Server VM if the owner has one; else recorded "not measured"). | Area 08 owns shared setup; this is one extra file. |
| D19 | Structured output ownership (conflict 1) | Area 04 writes the ten `outputSchema` values in a NEW file `internal/mcp/output_schemas.go` and adds `structuredContent` next to the existing text content (text result unchanged). Area 03 keeps the hook in `tools()` (3 lines) and in `protocol.go` `send`. Tool names and input schemas untouched. | New file avoids merge conflicts. |
| D20 | CLI contract (conflict 2) | Logic lives in `internal/codecli` as `Run(ctx, args, io, env) int`. Area 05's router calls it. Until then `cmd/audit-explorer/codecmd.go` dispatches `unravis code ...` before flag parsing. Exit codes and JSON envelope: section 4. | No rework when Cobra lands. |
| D21 | ACG vs spec 02 (conflict 3) | Different exes. `unravis.exe`: ACG off (spec 02 2.13). `unravis-sandbox.exe`: ACG per D1. Spawned by the Go broker, not Rust. Worker job nests under the sidecar job from BL-306 (Windows 8+ nested jobs; test BL-366 #5). | Written down as asked. |
| D22 | Broker location (conflict 4) | In the process that owns sources and the open case: the Go sidecar or the plain dashboard. CLI and MCP reach it through the same channel they use for `/api/*` (session.json loopback now; `ag`/`mcp` pipe after areas 01 and 03). Routes are mounted on the ordinary server handler, so no transport code is needed here. | One owner of data. |
| D23 | Installer (conflict 5) | Worker ships in the release set and in the setup payload. See BL-368. Authenticode (BL-312) not required to build, but unsigned worker spawn latency is measured (BL-360). | Area 06 owns the framework. |
| D24 | Supply chain (conflict 8) | New deps: `github.com/tetratelabs/wazero` (worker only), `github.com/evanw/esbuild` (broker), `github.com/klauspost/compress` promoted from indirect (zstd for blobs). Wasm vendored at `cmd/unravis-sandbox/qjs.wasm` with `qjs.wasm.sha256`; build recipe in `tools/codemode/BUILD-WASM.md`; CI job `wasm-reproducible` rebuilds with pinned `wasi-sdk` and compares. Area 02 BL-335 gates cover the Go modules. | Pin everything. |
| D25 | Data folder move (conflict 10) | No effect: worker path comes from the install dir, settings file from `internal/datadir`. | Spec 01 D8. |
| D26 | Perf (conflict 6) | No idle process, no pre-warm. Worker size budget set by BL-360 (target <= 7 MB stripped; must fit the 25 MB installer total, DECISIONS section 9). Worker RSS exists only during a run. | Footprint. |
| D27 | Response field names | camelCase (`runId`), like the existing tools (`resultId`, `nextCursor`). Report used snake_case. | Consistency. |
| D28 | Concurrency | Max 2 runs at the same time per dashboard, no queue; third gets `busy`. One in-flight `audit_compare` per run (existing `Bridge.comparing` rule applies). | Footprint, existing rule. |

## 3. Design

### 3.1 Components

```
caller (MCP client | unravis code run | UI)
   | HTTP /api/code/*   (loopback+token today; pipe after areas 01/03)
dashboard process (Go)
   internal/server/code.go   routes, settings, case lookup
   internal/codemode          Broker.Run: size check -> case check -> audit begin -> wrap+esbuild -> spawn -> serve calls -> audit end
   internal/codemode/host.go  Host over a per-run mcp.Bridge (in-process Upstream)
   internal/codemode/winsandbox  Spawner (Windows): job, handles, mitigations, LPAC/fallback
   internal/casestore/sqlite/coderuns.go   CodeRunStore
   | stdin/stdout anonymous pipes, length-prefixed JSON (package internal/codemode/wire)
unravis-sandbox.exe (cmd/unravis-sandbox): wazero + qjs.wasm, nothing else
```

Per run a NEW `mcp.Bridge` is made (own `resultId` cache, own identifier switch). Needed Go changes in `internal/mcp`:
- `type Upstream interface{ Do(ctx context.Context, path string, in, out any) error }`; `Bridge.request` calls it when set (`WithUpstream(u)`), else the existing HTTP path unchanged. The status-to-message switch moves to `statusError(code int) error`, shared.
- `func (b *Bridge) CallTool(ctx, name string, raw json.RawMessage) (any, error)` exported wrapper of `call`; `func ToolNames() []string`; `func ToolDefs(opts ...) []map[string]any` (same data as `tools()`).
- `internal/server/code_upstream.go`: `inprocUpstream{h http.Handler, token string}` runs the request through the server's own handler with a recorder, `User-Agent: unravis-mcp` (so the update busy-hook counts code runs as "in use"), same allow-list of paths as `request` (it keeps the allow-list; adds none).

### 3.2 Names, files, config

- Go: `cmd/unravis-sandbox/{main.go,runtime.go,bridge.go,glue.go?,qjs.wasm,qjs.wasm.sha256}`; `internal/codemode/{broker.go,run.go,wrap.go,host.go,limits.go,audit.go,replay.go,verify.go,errors.go,doctor.go}`; `internal/codemode/wire/wire.go` (stdlib only); `internal/codemode/winsandbox/{spawn_windows.go,job_windows.go,lpac_windows.go,token_windows.go,acl_windows.go,spawn_other.go,devspawn_codedev.go}`; `internal/codemode/tsgen/{tsgen.go,testdata/}`; `internal/jcs/jcs.go` (RFC 8785); `internal/codecli/{run.go,types.go,replay.go,doctor.go,exit.go}`; `internal/server/{code.go,code_upstream.go}`; `internal/mcp/{upstream.go,codetools.go,output_schemas.go}`; `internal/casestore/coderuns.go` (interface `CodeRunStore`), `internal/casestore/sqlite/coderuns.go`, memory impl in `memory.go` style file `memory_coderuns.go`, `storetest/coderuns.go`.
- Web: `web/src/lib/codeRunsApi.ts`, `web/src/lib/codeRuns.ts` (model), `web/src/lib/components/CodeRuns.svelte`, copy in `web/src/lib/copy/codeRunsCopy.ts`, tests `web/tests/codeRuns.*.test.cjs`.
- Tools and CI: `tools/codemode/{BUILD-WASM.md,quickjs-ng.version,Bench-Sandbox.ps1,Test-Sandbox.ps1}`, `docs/desktop/bench/code-mode/` (numbers), `.github/workflows/code-sandbox.yml`.
- Settings file `code-mode.json` in the per-user data folder (`internal/datadir`), written atomically with `casestore.SecureFile`: `{"v":1,"enabled":false,"allowDegraded":false,"keepResults":"all","scriptSeconds":30}`. Routes `GET/PUT /api/code/settings`.
- AppContainer profile name `Unravis.CodeSandbox.v1`. Deleted by uninstall (BL-368).

### 3.3 Wire protocol (broker and worker)

Frame: 4-byte little-endian length, then UTF-8 JSON. Max frame 1 MiB + 4 KiB. Any larger length, bad JSON or unknown `t`: the receiver ends the run (`bad_frame`). Worker stderr goes to NUL. One run per process.

Worker to broker: `{"t":"hello","v":1,"runtime":"wazero-interp","mitigations":{"acg":true,"win32kOff":true,"childRestricted":true},"wasmSha256":"<hex>"}` sent before any user code (only then the self-report is trusted).
Broker to worker: `{"t":"run","js":"...","tools":["audit_compare",...],"limits":{"memPages":1024,"heapBytes":33554432,"stackBytes":524288,"ticks":500000,"callBytes":1048576,"logBytes":65536,"maxCalls":50,"inFlight":4},"clock":{"mode":"live|frozen","baseMs":1790000000000},"seed":"<32 hex>"}`.
Worker to broker: `{"t":"call","id":1,"tool":"audit_compare","args":{...}}`.
Broker to worker: `{"t":"ret","id":1,"ok":true,"result":{...}}` or `{"t":"ret","id":1,"ok":false,"error":{"code":"result_too_large","message":"..."}}`.
Worker to broker, last frame: `{"t":"done","ok":true,"result":<json>,"logs":["..."],"logsTruncated":false,"ticks":1234,"heapPeak":1048576}` or `{"t":"done","ok":false,"error":{"code":"script_error|ticks|oom|stack|output_too_large","message":"...","stack":"..."}}`.
Worker queues calls FIFO when 4 are in flight (no error). Broker counts a call when it is received; the 51st ends the run with `call_budget`.
Script globals: frozen `unravis` with the ten functions, `console.log` (bounded). Removed or undefined: `eval`, `Function`, `fetch`, `WebAssembly`, `SharedArrayBuffer`, `Atomics`, dynamic `import()`, `std`, `os`. Test list in BL-375.

### 3.4 Run API

`POST /api/code/run` request: `{"v":1,"code":"...","caseId":"<id>","timeoutMs":30000,"reason":"<=200 chars","surface":"cli|mcp|ui","callerId":"<=64","allowIdentifiers":false}`.
Response (always HTTP 200 when the run was accepted, status in body; 4xx only for refusals before audit):
`{"v":1,"runId":"<uuidv7>","status":"ok|error|timeout|limit_exceeded|cancelled|crashed","result":<json|null>,"logs":["..."],"stats":{"calls":3,"durationMs":412,"scriptMs":38,"truncated":false,"logsTruncated":false},"sandbox":{"mode":"lpac|restricted_token_fallback","runtime":"wazero-interp","worker":"<sha8>"},"identifiers":"hidden|shown","error":{"code":"...","message":"..."}}`.
Other routes: `GET /api/code/types` (`{"v":1,"dts":"...","schemaSha256":"...","limits":{...}}`), `GET /api/code/runs?caseId=` (rows without source or blobs), `GET /api/code/runs/{id}` (full run + calls metadata, no blobs), `POST /api/code/runs/{id}/replay`, `POST /api/code/verify` (`{"caseId"}`), `POST /api/code/runs/{id}/cancel`, `GET /api/code/doctor`, `GET|PUT /api/code/settings`. All need the session token like every other route. All are POST/GET only, no run data in URLs except ids.

Error codes (stable): `code_mode_off, denied, no_case, busy, source_too_large, syntax_error, import_not_allowed, sandbox_unavailable, sandbox_degraded_refused, call_budget, result_too_large, output_too_large, script_error, ticks, wall, run_cap, oom, stack, worker_crashed, bad_frame, cancelled, replay_divergence, replay_not_possible, verify_failed`.

### 3.5 Main flow: run

1. Check setting `enabled`, grant (when it exists), source size <= 256 KiB, concurrency <= 2, case open (D9). Refuse before any write.
2. Build `Bridge` (identifiers per request), tool list, `tool_schema_hash` = SHA-256 of generated `.d.ts`.
3. Audit begin: one transaction inserts `code_runs` (`status='running'`, source, ids) and appends a `begin` event to `code_chain`.
4. Wrap + esbuild `api.Transform` (`LoaderTS`, `Target ES2023`). Error: end the run `error`/`syntax_error` (still audited).
5. Hash worker file, compare with pin; spawn suspended with job, handle list, mitigations, LPAC; check token; resume; read `hello`; compare mitigations.
6. Send `run`. Serve `call` frames: reserve budget, run `Bridge.CallTool` with a context of `min(remaining run, 5 min)`, scrub errors, sweep (hidden mode), cap 1 MiB, write `code_run_calls` row (args JCS, hashes, blob, status, duration, `seq`, `done_seq`), reply.
7. Script clock pauses while any call is in flight. On breach or cancel: `TerminateJobObject`, wait, close handles.
8. Audit end: update `code_runs`, append `end` event (contains `calls_hash`), commit. Return the response. The 1 MiB final cap and log cap apply here.
9. Crash recovery: at case open, every `running` run without an `end` event gets an `end` event `crashed` (`CloseOrphanRuns(ctx)`).

### 3.6 Audit schema (case DB)

`code_runs`: `run_id TEXT PK` (UUIDv7), `created_at_utc`, `surface`, `caller_id`, `caller_declared INTEGER` (1 = self-declared MCP clientInfo), `allow_identifiers`, `sandbox_mode`, `runtime`, `worker_sha256`, `tool_schema_hash`, `reason`, `source_ts`, `stripped_js_hash`, `seed`, `clock_ms`, `status`, `error_code`, `error_text` (scrubbed, 1 KiB), `result_hash`, `result_bytes`, `result_preview` (<= 4 KiB), `console_logs` (<= 64 KiB), `call_count`, `duration_ms`, `script_ms`. These extend the report's table with `seed`, `clock_ms`, `worker_sha256`, `runtime`, `reason`, `error_*`, `script_ms`, `caller_declared`.
`code_run_calls`: `(run_id, seq)` PK, `done_seq`, `tool_name`, `args_json` (JCS), `args_hash`, `result_hash`, `result_bytes`, `result_blob` (zstd), `blob_state` (`kept|off|oversize`), `status` (`ok|error|denied`), `duration_ms`.
`code_chain`: `n INTEGER PK` (monotonic), `run_id`, `phase` (`begin|end`), `record_json` (JCS), `prev_hash`, `record_hash`. Triggers `BEFORE UPDATE` and `BEFORE DELETE` raise ABORT (guards mistakes, not a key holder). First `prev_hash` = 64 zeros. `record_hash = hex(SHA256(JCS(record) ))` where record includes `prev_hash`. The `end` record carries `calls_hash = SHA256(JCS([{seq,done_seq,tool_name,args_hash,result_hash,status}...]))`, `result_hash`, `status`, `stripped_js_hash`, `source_sha256`.
Verify = replay the chain, recompute each record from the table rows, compare. Honest limit: someone holding the case key can rebuild the whole chain; `verify` prints the chain head so the user can note it. No claim of compliance with any draft standard.

### 3.7 Windows spawn (winsandbox)

`Spawner` interface: `Start(ctx, exe string, opts Opts) (Proc, error)`; `Proc` has `Stdin io.WriteCloser`, `Stdout io.ReadCloser`, `Mode() string`, `Kill()`, `Wait()`, `PeakCommit()`.
Steps: two anonymous pipes (child ends inheritable, parent ends not); `STARTUPINFOEX` attribute list with `HANDLE_LIST` (only the two child ends; stderr = NUL handle), `JOB_LIST`, `MITIGATION_POLICY` (win32k off, strict handle checks, font off, image load no-remote/no-low-label/prefer-system32, extension points off, FSCTL off, child process restricted via `CHILD_PROCESS_POLICY`, ACG per D1), `SECURITY_CAPABILITIES` (zero caps) + `ALL_APPLICATION_PACKAGES_POLICY` opt-out (LPAC). Flags `CREATE_SUSPENDED | EXTENDED_STARTUPINFO_PRESENT | DETACHED_PROCESS | CREATE_NO_WINDOW`. `lpCurrentDirectory = %SystemRoot%\System32`, empty environment block. Job: kill on close, active process 1, process and job memory = commit cap (D1, default 128 MiB until BL-360), CPU hard cap 50% (failure ignored on RDS), all 8 UI limits. Check `TokenIsLessPrivilegedAppContainer` on the suspended child token; mismatch kills it and returns `sandbox_unavailable`. Win32 calls through `golang.org/x/sys/windows` (v0.48.0 in `go.mod`) plus `userenv.dll` lazy procs for `CreateAppContainerProfile`, `DeriveAppContainerSidFromAppContainerName`. Fallback: `CreateRestrictedToken` + Untrusted IL, only when `allowDegraded`.

### 3.8 Failure modes and what the user sees

| Event | Result | Text (CLI/UI, plain words) |
|---|---|---|
| Code mode off | `code_mode_off` | "Code mode is switched off. Turn it on in Settings or run `unravis code enable`." |
| No case open | `no_case` | "Open a case first. Code runs are recorded in the case." |
| Source > 256 KiB | `source_too_large` | "The script is too big (limit 256 KB)." |
| Syntax error | `syntax_error`, line and column | Message from esbuild, first error only. |
| Worker missing, wrong hash, no LPAC | `sandbox_unavailable` | "The safe runner is missing or cannot start here. Run `unravis code doctor`." No silent fallback. |
| Fallback allowed and used | status as normal, `sandbox.mode=restricted_token_fallback` | Banner "Reduced isolation: the network is not blocked on this PC." |
| Infinite loop | `ticks` | "The script used too much processing." |
| Slow but waiting on tools | not an error until `run_cap` | n/a |
| 51st tool call | `call_budget` | "The script made more than 50 tool calls." |
| Out of memory | `oom` | "The script used too much memory. The app is unaffected." |
| Result > 1 MiB | `output_too_large` | "The result is bigger than 1 MB. Return a summary." |
| Worker dies | `worker_crashed` | Run closed and audited; app unaffected. |
| Broker/app killed mid run | worker dies (job close); row closed `crashed` at next case open | Inspector shows "crashed". |
| Tool error inside script | script sees a rejected promise with the scrubbed message of the existing tool error | n/a |

## 4. Interfaces other areas rely on (exact names)

Go packages: `api-audit-explorer/internal/codemode` (`Broker`, `Broker.Run(ctx, RunRequest) (RunResult, error)`, `RunRequest`, `RunResult`, const `WorkerSHA256` set by ldflags, `ErrCode` strings from 3.4), `internal/codemode/wire`, `internal/codemode/winsandbox` (`Spawner`, `New()`), `internal/codemode/tsgen` (`Generate(tools []map[string]any) (dts string, sha string, err error)`), `internal/jcs` (`Marshal(v any) ([]byte, error)`), `internal/codecli` (`Run`, exit constants), `internal/casestore` (`CodeRunStore` interface: `BeginRun`, `RecordCall`, `EndRun`, `CloseOrphanRuns`, `ListRuns`, `GetRun`, `RunCalls`, `ChainRows`), `internal/mcp` (`Upstream`, `WithUpstream`, `WithCode`, `Bridge.CallTool`, `ToolNames`).
Routes: section 3.4. Settings file: `code-mode.json`. Setup: `setup.SandboxExeName = "unravis-sandbox.exe"`, `Layout.SandboxExe` = `<InstallDir>\sandbox\unravis-sandbox.exe`, `setup.SandboxDirName = "sandbox"`.
MCP tools (stable text, byte-stable schema): `unravis_types` input `{}`; `unravis_execute` input `{code (string, required), timeout_ms (int 1000-120000), reason (string<=200), case_id (string)}` output `{runId,status,result,logs,stats,sandbox,identifiers,error}` (note `timeout_ms` snake, as the report; the MCP tools in this repo mix none, so this one pair follows the report, and the REST body maps it to `timeoutMs`). Description warns that data from audit sources is untrusted.
CLI: `unravis code enable|disable|types|run|replay|verify|doctor`. Flags: `run [--file P | --stdin] [--case ID] [--timeout-ms N] [--allow-identifiers] [--allow-degraded] [--json]`, `replay RUNID [--allow-identifiers] [--json]`, `verify [--case ID] [--json]`, `doctor [--json]`. JSON envelope `{"v":1,"ok":bool,"run":{...},"error":{"code","message"}}`. Exit codes (area 05 may remap by name): 0 ok, 1 script error, 2 usage, 3 timeout or limit, 4 denied or off, 5 Unravis not running, 6 sandbox unavailable, 7 replay divergence or verify failed.
Other areas must NOT: import `internal/codemode/winsandbox` outside `internal/codemode`; add wazero to `cmd/audit-explorer`; edit `internal/mcp/output_schemas.go` or `internal/mcp/codetools.go`.
CI: workflow `code-sandbox.yml`, scripts `tools/codemode/*.ps1`.

## 5. Per BL item

Order inside the area: BL-361 -> BL-360 -> BL-362 -> BL-364 -> BL-363 -> BL-365 -> BL-369 -> BL-370 -> BL-366 -> BL-367 -> BL-368 -> BL-375 (partial, grows with each) -> BL-371 -> BL-372 -> BL-373 -> BL-375 exit gate. BL-374 and BL-376 deferred. BL-366 may start once BL-362 has a runnable worker. BL-361 first because W2 needs the async bridge. Honest note: BL-360 uses the real tool data size, so W2 uses recorded real responses from the demo dashboard scaled to 50 KB, labelled "replayed fixtures", for the gate; live host calls come in BL-363 acceptance.

### BL-361 QuickJS-ng WASI async bridge spike
- Files: `cmd/unravis-sandbox/glue/` (C, about 150 LOC: `__hostCall`, tick interrupt, memory and stack limits, uncatchable-OOM exit), `tools/codemode/BUILD-WASM.md`, `tools/codemode/quickjs-ng.version` (tag and commit), `cmd/unravis-sandbox/qjs.wasm(.sha256)`, spike test package `cmd/unravis-sandbox/spike_test.go`. Choose own glue over `fastschema/qjs` (v0.0.6 is old, report says avoid); record the reason in `BUILD-WASM.md`.
- Tests: `TestAwaitThreeHostCallsInOrder`, `TestPromiseAllFour`, `TestFifthCallQueues`, `TestInfiniteLoopStopsOnTicks`, `TestHeapBombEndsRunHostSurvives`, `TestWasmHashMatchesRepo`; CI job `wasm-reproducible`.
- Acceptance (run: `go test ./cmd/unravis-sandbox/... -run Spike`): all pass on Linux and `windows-2025` (CI); repeat inside the real exe on the live PC (nachi). Records the tick-to-ms ratio used in D3.

### BL-360 Windows x64 benchmark
- Files: `tools/codemode/Bench-Sandbox.ps1`, `cmd/unravis-sandbox/bench/` (harness flags `--bench w1|w2|w3 --mode interp|comp`), results `docs/desktop/bench/code-mode/<date>-<host>.json` and `DECISION.md`.
- Build C1-Interp and C1-Comp. W1, W2, W3 as in report, 5 warmup + 100 runs, p50/p95/p99, peak working set and `PeakProcessMemoryUsed`, binary size, Defender on, unsigned worker (signed pair only if BL-312 exists). Also records size delta of wazero in the worker and the installer total.
- Decision rule is D1 plus: commit cap = max(128 MiB, 2 x measured p99 commit) only if the worker still fits the job cap; write the value to `winsandbox.CommitCapBytes`.
- Acceptance: gate numbers in `DECISION.md` (live PC (nachi)); same suite on a Server VM (live PC, only if the owner supplies one, else line "not measured"); smoke 3 iterations passes in `code-sandbox.yml` on `windows-2025` and is NOT used for the gate (CI); `DECISION.md` exists with chosen mode and cap (CI file check `Test-Sandbox.ps1 -Check DecisionNote`).

### BL-362 Worker `unravis-sandbox.exe`
- Files: `cmd/unravis-sandbox/*`, `internal/codemode/wire`. Build: `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H=windowsgui" ./cmd/unravis-sandbox` added to `build.ps1` BEFORE the app build (pin).
- Limits and globals as 3.3. Zero WASI preopens, no env, no args. One run then exit. Frozen `unravis`.
- Tests: `TestFrameTooLargeRefused`, `TestBadFrameEndsRun`, `TestOneRunThenExit`, `TestLimitsDistinctCodes` (mem, stack, ticks, output, log cap), `TestClockQuantized10ms`, `TestMathRandomSeeded`, `TestGlobalsRemoved`, `TestWorkerDepsAllowList` (`go list -deps` has no `sqlite`, `mongo`, `keeper`, `dpapi`, `casestore`, `server`, `mcp`), `TestAppDoesNotLinkWazero`.
- Acceptance: as groom 1 to 5 (CI; item 5 on the live PC with the real exe and no host calls).

### BL-364 Types and outputSchema
- Files: `internal/codemode/tsgen`, `internal/mcp/output_schemas.go`, hook in `tools()` and `protocol.go send` (D19), `internal/mcp/schemacheck_test.go` (small validator for the subset used; no new dependency).
- Output schemas are hand-written from the real Go response shapes (`groupPage`, `compareTimelines`, `slowest`, `breakdown`, `activity`, `projectRecord`, `audit.Comparison`); fields that can be null or absent are optional/nullable. Generator: depth cap 12, visited set, `unknown` for `patternProperties`/complex `oneOf`, sanitized names, header `// schema_sha256: <hex>`. Output namespace `declare namespace unravis`, one `function audit_x(input: AuditXInput): Promise<AuditXOutput>` per tool; comment lines carry the existing tool descriptions and the 4-in-flight, 50-call and 1 MiB rules.
- Tests: `TestGoldenDts` (golden `internal/codemode/tsgen/testdata/unravis.d.ts`), `TestRecursiveSchemaTerminates`, `TestEveryToolHasOutputSchema`, `TestResponsesValidateAgainstOutputSchema` (runs all ten tools through `--demo` dashboard sources, validates `structuredContent`), `TestTextContentUnchanged` (existing `tools_test.go` keeps passing byte for byte), `tools/codemode/Check-Dts.ps1` compiles the `.d.ts` with a pinned `typescript` npm package in CI only (never shipped; `ignore-scripts`).
- Acceptance: as groom. Real-data part: live PC (nachi) validates the live source responses with `unravis code types --check`. Limit: CI uses demo sources, labelled.

### BL-363 Broker run pipeline
- Files: `internal/codemode/{broker,run,wrap,host,limits,errors}.go`, `internal/server/code.go`, `internal/server/code_upstream.go`, `internal/mcp/upstream.go`, `internal/server/routes.go` entries for `/api/code/*`. Dev-only spawn: `devspawn_codedev.go` (tag `codedev`) so Linux CI works; production binary has no way to run unsandboxed (`TestProdBuildHasNoDevSpawner`).
- esbuild pinned (`github.com/evanw/esbuild` v0.28.2 as the report says; take the version available on the day and record; check licence MIT in `NOTICE`).
- Tests: `TestRunTable` (valid TS, syntax error, enum compiles, import refused, 256 KiB and 256 KiB+1, 50th and 51st call, 5th in flight queues, result 1 MiB and +1, per-call 1 MiB+1), `TestScriptClockPausesDuringCalls`, `TestRunCapAt280s` (fake clock), `TestCancelKillsWorker`, `TestKilledWorkerNoZombieNoHandleLeak` (windows), `TestHostMatchesDirectTool` (script result equals the direct `Bridge.call` for the same demo source), `TestInprocUpstreamAllowList`.
- Acceptance: groom 1 to 3. Item 2 runs on the real dashboard path: demo sources in CI; on the live PC with the real connected source and case "Brain Sandbox Slowdown" the script `audit_list_sources` + `audit_compare` returns the same JSON as the direct MCP call (compare SHA-256 of both).

### BL-365 Privacy
- Files: `internal/codemode/privacy/` (package `privacy`: `Guard`, `ScrubText`, `ScrubError`, `ScriptClock`, `Rand`; pure, built before the broker). The broker feeds data in with `Guard.HostResult`/`HostError` (identifiers found in the data go with it; token look-alikes are escaped once) and script output out with `Output`/`Log`/`ScriptError` (no escape). A replay serves stored swept blobs and never passes them through `HostResult` again. When `Guard.Incomplete()` is true (too many or too long identifiers, a text too costly to scan) the broker ends the run with its own code (suggested `identifier_limit`).
- Reuse of `Bridge` hiding (D11). Sweep patterns: IPv4, IPv6, strings equal to values the broker saw in allow-listed raw user-agent fields (none in hidden mode, so IP pattern is the practical check). Scrub: `Server=`/`Password=`/`mongodb://` forms, `[A-Za-z]:\...` paths, quoted SQL literals, to `[scrubbed]`.
- Tests: `TestHiddenModeNoRawIdentifiers` (regex sweep over result, logs, errors for a run that calls breakdown by userId, userAgent, userIp), `TestAllowModeShowsIdentifiers`, `TestScriptCannotEnableIdentifiers` (args `includeIp` ignored as today), `TestErrorScrub`, `TestReplayAllowModeRefusedWithoutAllow`.
- Acceptance: groom 1 and 3; item 2 (group by `hidden-N` equals group by raw) is replaced by "counts of `breakdown` userId rows equal in both modes" because ranks are per call (existing behavior, not changed; scripts cannot join users across calls in hidden mode, said in the `.d.ts` comment). Live PC (nachi): sweep on "Brain Sandbox Slowdown" output.

### BL-369 Audit store
- Files: section 3.2 casestore files, `internal/codemode/audit.go`, `internal/jcs`. Interface `casestore.CodeRunStore`, asserted by type like `WorkspaceBlobStore`; `memCase` implements it for tests; add to `storetest` contract.
- JCS: strings and integers only in records; args JSON may hold floats, so implement ES6 number serialization and test with RFC 8785 appendix B vectors.
- Tests: `TestChainVerifies`, `TestOneByteEditBreaksVerify` (each of run row, call row, blob, chain row), `TestChainTriggersBlockUpdateDelete`, `TestCrashLeavesCrashedRow`, `TestOldBuildOpensMigratedCase` (open with the previous release's schema constant and `refusesSchema`; no bump), `TestCodeRunsSurviveArchiveRestore`, `TestCodeRunsSurviveRewrap`, `TestKeepResultsOffStoresHashesOnly`, `TestNoAutoPrune`, storetest contract for sqlite and memory.
- Acceptance: groom 1 and 3 (CI). Item 2 on a real sealed case file: CI uses a case created by the test harness (a real sealed file, demo data); live PC (nachi) on "Brain Sandbox Slowdown": 3 real runs, close, reopen with the real key, `verify` OK. Owner confirmation of retention: not needed, D10 decided (keep all).

### BL-370 Replay and verify
- Files: `internal/codemode/{replay,verify}.go`; routes in `code.go`.
- Replay feeds `ret` frames from blobs in `done_seq` order, frozen clock, same seed. `blob_state != kept` gives `replay_not_possible`. Verify output: first broken `n`, which check failed.
- Tests: `TestReplaySameResultHash`, `TestReplayTamperedBlob` (`verify_failed`), `TestReplayChangedSource` (`replay_divergence` or `verify_failed`), `TestReplayChangedCallOrder`, `TestReplayMathRandomDateNow`, `TestReplayHashOnlyNotReplayable`, `TestReplayAllowRunRefusedWithoutFlag`.
- Acceptance: groom 1 to 3. Live PC (nachi): replay a run made in BL-371 acceptance.

### BL-366 Layer A (Job, handles, mitigations)
- Files: `winsandbox/{spawn,job}_windows.go`; hostile fixtures in `cmd/unravis-sandbox/testworker/` (a second tiny exe built only for tests, tag `sandboxtest`, never shipped).
- Tests (windows): `TestWorkerCannotSpawnChild`, `TestWorkerCannotOpenWindow`, `TestWorkerCannotReadClipboard`, `TestWorkerHandleAudit` (NtQuery of the child: only the two pipes, NUL, system handles), `TestKillBrokerKillsWorker` (job close), `TestNestedUnderOuterJob`, `TestMitigationsReadBack` (worker `hello` and `GetProcessMitigationPolicy` check), `TestNoConhostInJob`.
- Acceptance: as groom (items 1 and 2 CI `windows-2025`; 2 repeat, 3 and 4 live PC (nachi): End Task on the dashboard mid run, worker gone; `Get-ProcessMitigation -Id <worker>` shows policies).

### BL-367 Layer B (LPAC, fallback)
- Files: `winsandbox/{lpac,token,acl}_windows.go`.
- Tests: `TestTokenIsLPAC`, `TestProfileAlreadyExists`, `TestSuspendedTokenCheckRefuses` (injected bad token), `TestFallbackFlagged` (test switch), `TestFallbackRefusedByDefault`, `TestNoLoopbackNoPipeAccess` (hostile fixture does `connect()` to 127.0.0.1:<real port>, opens the real session.json, case file, Key Keeper pipe, locator pipe: all denied), `TestServer2016Documented` (test that prints the not-blocked network result, run only on such host).
- Acceptance: as groom. Item 4 depends on a Server 2016 box; if none, recorded "not verified" in `docs/desktop/bench/code-mode/NOT-VERIFIED.md`, and D6 refuses there anyway.

### BL-368 Installer and ACL
- Files: `internal/setup/layout.go` (`SandboxExeName`, `SandboxDirName`, `Layout.SandboxExe`), `internal/setup/state.go` `payloadNames`, `internal/setup/plan.go` place list (next to `KeeperExeName`), `internal/setup/acl_windows.go` (new, `grantRestrictedAppPackages(dir)`: ACE `(OI)(CI)RX` for `S-1-15-2-2` on the `sandbox\` folder only, so the ACE never reaches `unravis.exe` or the keeper), `internal/update` file list and `tools/release/Invoke-VerifyRelease.ps1` (worker present, pin matches, `unravis code doctor --json` ok), `build.ps1`, `release.yml` (include in zip and payload), uninstall removes the profile (`DeleteAppContainerProfile`) and the folder.
- Re-apply on OTA, rollback and `--recover`: the apply step calls `grantRestrictedAppPackages` after every file placement (idempotent). `doctor` reports "ACL ok/missing".
- Tests: `TestSandboxAclShape` (SDDL check), `TestSandboxAclReappliedOnReplace`, `TestPayloadHasWorker`, `TestPlanPlacesSandboxDir`.
- Acceptance: groom 1 and 2 (CI; clean standard-user per-user install; N-1 to N, rollback, `--recover`); 3 elevated/UAC on the owner PC (only the UAC prompt).

### BL-371 CLI
- Files: `internal/codecli/*`, `cmd/audit-explorer/codecmd.go` (dispatch when `os.Args[1] == "code"`, before `parseFlags`; not combined with `--mcp`). Uses the same locator and proven-dial path as `--mcp` to reach the dashboard (reuse `mcp` HTTP `Upstream` implementation, exported as `mcp.NewHTTPUpstream`). Never starts the GUI. If the dashboard is not running: exit 5, text "Start Unravis first." Exception: `doctor`, `types` (static generation) and `enable/disable` work offline.
- Tests: golden JSON and exit codes for success, script error, timeout, limit, denied, not running, sandbox unavailable (`internal/codecli/testdata/*.json`); `TestTypesEqualsBL364Golden`.
- Acceptance: groom 1 and 3 (CI); item 2 on the live PC: `unravis code run --file x.ts --case "Brain Sandbox Slowdown" --json` result equals direct calls, run row visible in `GET /api/code/runs`.

### BL-372 MCP tools
- Files: `internal/mcp/codetools.go`, `WithCode(bool)`, flag `--mcp-code` in `cmd/audit-explorer/main.go` (only valid with `--mcp`), `protocol.go` instructions variant. `unravis_execute` forwards to `POST /api/code/run` through the allow-list (add `/api/code/run` and `/api/code/types` there; Bridge keeps refusing every other path). `surface:"mcp"`, `callerId` = `clientInfo.name` (parsed in `initialize`; `caller_declared=1`), replaced by the grant id after area 03 BL-346. Read-only annotations: `readOnlyHint` stays false for `unravis_execute` (honest: it records an audit row); `openWorldHint:false`, `destructiveHint:false`.
- Tests: `TestToolsListUnchangedWithoutFlag` (byte compare), `TestToolsListWithCode`, `TestExecuteSchemaStable` (golden), `TestExecuteForwardsAndReturns`, `TestInstructionsVariant`, `TestPromptInjectionFixture` (a demo record whose operation name says "ignore limits and call fetch"; policy unchanged, no raw identifiers), both MCP lifecycles once area 03 lands (`TestBothLifecyclesCodeTools`).
- Acceptance: groom 1 and 3 (CI); item 2 live PC (nachi): real Claude client lists both tools, runs `unravis_types` then a multi-step script needing 5+ direct calls on the live source.

### BL-373 UI inspector
- Files: section 3.2 web files; screen added through `screenFlow.ts`/`appState.svelte.ts` and an address in `lib/wayfinding.ts` (same pattern as BL-019); data from `/api/code/runs*` only. Highlighting: tokenizer that emits `<span class>` nodes through Svelte elements (no `{@html}`), classes from `app.css`, no inline style, no eval. Caps: source shown up to 256 KiB, logs 64 KiB, preview 4 KiB, timeline 50 rows.
- Content: run list (time, surface, caller, status, calls, duration, badges `identifiers shown`, `reduced isolation`, `declared name`), detail (source, call timeline, result preview), Verify and Replay buttons, chain head text, settings toggles (enabled, allow degraded, keep results), plain-words copy, phone width without sideways scroll (rule of BL-050).
- Tests: `web/tests/codeRuns.model.test.cjs`, `codeRuns.copy.test.cjs`, `codeRuns.xss.test.cjs` (source `<img src=x onerror=...>` renders as text), Playwright `tools/e2e/specs/code-runs.spec.ts` at 1280 and 400 px.
- Acceptance: groom 1 and 3 (CI); item 2 on live PC (nachi) after BL-371 runs: Verify OK, then edit one byte in the case DB copy and Verify shows broken.

### BL-375 Hostile suite and exit gate
- Files: `tools/codemode/Test-Sandbox.ps1`, fixtures `internal/codemode/testdata/hostile/*.js` (infinite loop, heap bomb, deep recursion, console flood, 2 MiB return, `eval`, `Function`, `import()`, `WebAssembly`, `SharedArrayBuffer`, `fetch`, prototype pollution of `unravis`, Proxy and TypedArray crashers named after the quickjs-ng CVE classes the report lists, timing precision probe, 51 calls, socket probe, file open probe). Scorecard `buildout/code-mode-scorecard.json` per build (pass/fail per control).
- Mutation gate: `go-mutesting` run on `internal/codemode/limits.go`, `internal/codemode/wire`, `internal/codemode/winsandbox/job_windows.go` in diff mode in the nightly job; target 85% killed on changed lines. Tool choice is not final; if `go-mutesting` does not work on go 1.27, fall back to `gremlins`. **Decided by spike inside BL-375.**
- Acceptance: groom 1 (CI on `windows-2025`, failing control fails the build), 2 (live PC (nachi) installed build, real dashboard and case running; worker cannot reach the real port or pipes), 3 (fallback-mode run shows only the network control dropping). Do not ship `unravis_execute` to users before 1 and 2 are green.

### BL-374 Scratchpad (deferred)
Not built. Owner go needed. If given: Textarea in the inspector calling `POST /api/code/run` with `surface:"ui"`, same broker, a cancel button using `/cancel`, per-run allow-identifiers switch. Tests would be `TestUiRunUsesBroker`.

### BL-376 (new) Manual result prune (deferred)
`unravis code prune --case ID --keep-last N [--yes]` sets `blob_state='off'` and nulls blobs for older runs; rows, hashes and chain stay (chain unchanged, verify still OK, replay says not possible). Deletes user data, so needs owner go. Add to `docs/BACKLOG.md` deferred section when the plan stage writes the list.

## 6. Security and footprint checks (this area)

Security:
1. `go list -deps ./cmd/unravis-sandbox` allow-list test (BL-362); `go list -deps ./cmd/audit-explorer` has no wazero (D2).
2. Worker pin checked before every spawn; unpinned dev builds log loudly and are refused in `-Release`.
3. The worker holds only its two pipes (BL-366 audit), cannot reach loopback port, `session.json`, case file, Key Keeper pipe (BL-367, BL-375).
4. Script cannot raise its own limits or identifiers: limits are in the `run` frame from the broker; identifier mode is broker-side.
5. Prompt injection: tool text stays data; `.d.ts` and tool description say so; fixture test in BL-372.
6. Run records never hold secrets: connection strings scrubbed; allow-mode runs flagged.
7. Source of truth for tool list is one registry (`tools()`); worker `tools` list comes from it; unknown tool name at the broker returns `denied`.
8. Worker stderr is discarded; broker never logs script source or results to `applog` (only run id, status, durations).
9. Chain limits stated (section 3.6); no marketing wording.
10. Supply chain: wazero, esbuild, wasm hash, `wasi-sdk` pin under area 02 BL-335 gates (`govulncheck`, SBOM).

Footprint: no process or memory at idle; worker size and cold p95 budgets from BL-360; installer total stays <= 25 MB or the item is re-opened; `code_runs` growth shown in inspector; broker allocations per run bounded by caps (<= 1 MiB per frame, 64 KiB logs); no goroutine left after a run (`TestNoGoroutineLeakAfterRuns`, 100 runs, uses `goroutineleak` on Go 1.27 if present, else `runtime.NumGoroutine` delta).

## 7. Open risks left, each with an owner-free mitigation

| # | Risk | Mitigation |
|---|---|---|
| R1 | wazero interpreter too slow on 0.5 to 1 MiB JSON | BL-360 W2 decides; fallback C1-Comp without ACG; cap stays at 1 MiB per call, so worst case is bounded. |
| R2 | Job commit cap 128 MiB too small for Go worker | Cap is measured (2 x p99); the worker fails to start with `sandbox_unavailable`, never runs uncapped. |
| R3 | Unsigned worker slow or flagged by Defender | Measured in BL-360; `doctor` prints scan-delay hint; spawn latency counted inside the 30 s script clock only after `hello`; signing remains BL-312. |
| R4 | Tool calls up to 5 min exceed MCP host timeouts | Run cap 280 s; `unravis_types` text tells the model to narrow queries; no retry inside broker. |
| R5 | `hidden-<rank>` is per call, so scripts cannot join users across calls | Documented in `.d.ts`; allow mode available to the operator; consistent aliases would be a new item (area 03 BL-349 aliases) and are not built here. |
| R6 | esbuild accepts TS that Node's strip would refuse | Irrelevant: only esbuild output runs. |
| R7 | LPAC kernel escapes | Layers 1 and 2 are mandatory; ACL, job, mitigations all tested; no claim of immunity. |
| R8 | Server 2016 and Session 0 untested | D6 refuses by default; test marked "not verified" if no box. |
| R9 | Runs need an open case (D9); a no-case investigator is blocked | Message is plain; a case is cheap. A per-user sealed run log would be a new item if the owner dislikes it; not built to avoid two audit stores. |
| R10 | Area 03 changes `protocol.go` or `tools()` and breaks D19 hooks | Hooks are tested by `TestTextContentUnchanged` and `TestEveryToolHasOutputSchema`; failing tests name this spec. |
| R11 | Pin ordering: worker must be built before the app, signing changes bytes | `build.ps1` order and the release step "Check the app is pinned" extended to the worker; sign worker before the app is built (comment already in `release.yml`). |
| R12 | Nested job behavior under the sidecar's job unverified | `TestNestedUnderOuterJob` in BL-366; if it fails, BL-366 uses `JOB_OBJECT_LIMIT_BREAKAWAY_OK` only on the sidecar job (area 01 change, one line) and the worker keeps its own job. |
| R13 | CI cannot use real customer data | CI uses demo sources and harness-made sealed cases, labelled; live checks owed on nachi's PC and listed per item; nothing is ticked in `docs/BACKLOG.md` before the live check. |
