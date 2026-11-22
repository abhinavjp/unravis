# Groom 03: MCP server (BL-340 to BL-355)

Area: `unravis.exe --mcp` (stdio), the backend MCP endpoint on the pipe, grants, disclosure tiers, audit, client install paths. Stage 1 (groom only). Sources: research report 04 (`04-mcp-server-design.md`, read in full, 250 lines), `DECISIONS-draft.md` (sections 5, 12), `CLAUDE.md`, `docs/BACKLOG.md`.

Notes
- Acceptance tags: **CI**, **live PC (nachi)**, **elevated/UAC**.
- Owner rule: proof is on the real path: real Go backend, real pipe, real MCP host process, real cases. Fixtures are extra, never the proof. Report 04 itself says client coverage is documentation-based; runtime interop is unproven until tested.
- Report numbers (versions, dates) are the report's, not re-verified. Tags in the report: `[SOURCE]` vs `[INFERRED]`. Almost all of the design is `[INFERRED]`.
- Not duplicated: auto-updater (BL-149 to BL-151, BL-168, BL-194) and BL-318 (CLI and MCP attach via locator, area 01). Here BL-318 is a dependency, not rebuilt.
- Not built (deferred, needs owner go, per BACKLOG rule for new collectors/agents and per the report): Streamable HTTP, Tasks, resources, prompts, sampling, elicitation, dynamic catalogs, OAuth. Listed under Questions.
- Code mode (`unravis_execute`, `unravis_types`) is another area. Only the shared-policy hook is noted (BL-348, BL-350).

## Summary table

| ID | Title | Phase | Size | Owner action |
|---|---|---|---|---|
| BL-340 | Baseline: current MCP bridge on six real hosts | P0 | M | maybe (host accounts) |
| BL-341 | Spike: official Go SDK parity, both wire lifecycles | Parallel A | M | no |
| BL-342 | Move MCP dispatch to the Go SDK, ten tools unchanged | Parallel A | M | no |
| BL-343 | Structured results, compatible text, stable error codes | Parallel A | M | no |
| BL-344 | Backend MCP endpoint on the pipe, audit-only dispatcher | Parallel A (needs P1) | L | no |
| BL-345 | Bridge client: rendezvous, peer proof, fail closed, reconnect | Parallel A (needs P1) | M | no |
| BL-346 | Installation grants: issue, store, revoke, legacy default | Parallel A | L | no |
| BL-347 | Grants screen in the app | Parallel A (needs P3 for desktop; browser first) | M | no |
| BL-348 | Disclosure tiers enforced in the backend | Parallel A | L | no |
| BL-349 | Opaque evidence aliases instead of raw record IDs | Parallel A | M | no |
| BL-350 | Budgets, cursors, handles, cancel on disconnect | Parallel A | M | no |
| BL-351 | MCP audit store and call ledger | Parallel A | M | no |
| BL-352 | Host install entry points (config text, absolute path, MCPB) | Parallel A (after P4 installer) | M | no |
| BL-353 | Update and rollback with MCP clients attached | P4 | S | no |
| BL-354 | Injection and privacy regression suite | Parallel A | M | no |
| BL-355 | Real-host acceptance gate (six hosts, both lifecycles) | Parallel A exit | M | maybe (host accounts) |

16 items. No item needs a secret, signing passphrase, money or deleting user data. "maybe (host accounts)": running some hosts may need a login or licence; the report does not say. Elevated steps appear only as test steps.

---

## BL-340 Baseline: current MCP bridge on six real hosts
- Outcome: recorded numbers and behaviour of today's handwritten bridge on real Windows hosts, so the migration has something to compare against.
- Scope (in): pin and record the real Windows build of Claude Code, Claude Desktop, Codex CLI, Cursor, VS Code (Copilot), Gemini CLI (report's list; versions in the report are examples, not to trust). For each: initialize, tool list, one call per tool on a real case, cancel, deadline, EOF, backend offline then started. Record cold launch time, idle working set, handles, latency of a heavy call (comparison can run to 5 min per report). Record whether the host shows text, structured data or both to the model (it cannot be seen today: text only). Name the prior release for rollback.
- Out of scope: any code change; SDK work (BL-341); measuring Tauri.
- Depends on: P0.
- Risks / unknowns: report open item "Protocol and host uptake" and "Resource budget": no Unravis MCP measurements exist. Claude Desktop exact stable build "unverified" in the report. Per-host deadlines unknown.
- Acceptance:
  - A table of host, build, result for each of the ten tools on a real case, saved in `docs/desktop/` (spec stage picks the file). (live PC (nachi))
  - Cold launch, idle RSS, handles, p50/p95 latency numbers saved with hardware noted. (live PC (nachi))
  - EOF, cancel and offline-then-start behaviour recorded per host. (live PC (nachi))
- Size: M. Phase: P0.
- Needs owner action? Maybe: a host may need an account or licence. Ask which hosts nachi has.

## BL-341 Spike: official Go SDK parity, both wire lifecycles
- Outcome: a go/no-go on the official Go SDK (report: v1.8.0) behind the existing tool names, with a named fallback.
- Scope (in): throwaway branch, one `unravis.exe --mcp` that speaks the 2026-07-28 per-request lifecycle and the older handshake (to `2025-11-25`). Check against today's limits: 1 MiB wire message, 16 MiB upstream body, 4 in flight, 10 min comparison cache, scrubbed errors. Run the official conformance fixtures for both revisions. Test oversize frames, cancel during blocked read, malformed arguments, duplicate JSON keys. Check the SDK's inferred capabilities (logging, list-changed) can be turned off and cache scope is not public by default (report: v1.8.0 defaults to public).
- Out of scope: shipping it (BL-342); HTTP transport; Tasks.
- Depends on: BL-340 (to compare). 
- Risks / unknowns: report says the SDK bounded-I/O may differ from today's; if so the bounds are enforced before the SDK. Go SDK Tasks runtime absent (not needed). Report names mark3labs v1.6.0 as fallback; no Unravis workload comparison exists. Go 1.27 mention in the report: the repo's Go version is not checked here.
- Acceptance:
  - Conformance fixtures pass for both lifecycles. (CI)
  - Oversize, cancel and malformed-input tests behave as today's bridge (same error class, no crash, no hang). (CI)
  - Both lifecycles work against the real running backend from at least two real hosts from BL-340. (live PC (nachi))
  - Written verdict: SDK, or fallback, with the blocker if any. (CI: file exists; decision is owner/spec)
- Size: M. Phase: Parallel A.
- Needs owner action? No.

## BL-342 Move MCP dispatch to the Go SDK, ten tools unchanged
- Outcome: the same ten tool names and inputs, served by the SDK, with the handwritten parser gone.
- Scope (in): keep names, input schemas, fixed catalog order, static reviewed descriptions; tools capability only; explicit capabilities; private, zero-TTL cache metadata for catalogs that depend on grants; `readOnlyHint` accurate; live-query tools described as live; `audit_get_guide` static with no tenant text. Keep the allowlist of backend paths. Keep `--mcp-allow-identifiers` flag working (it only requests, see BL-348).
- Out of scope: new transport; structured output (BL-343); the pipe (BL-344/345). Still talks to the current loopback bridge underneath.
- Depends on: BL-341 (go).
- Risks / unknowns: report says a legacy-only release is acceptable during migration if modern-client tests stay on the checklist. SDK default drift across updates (advisories listed in the report): pin and re-test on every bump.
- Acceptance:
  - Existing `internal/mcp` tests and output-audit tests pass unchanged or are ported with the same cases. (CI)
  - Draft exit for Parallel A: the ten tools pass on old and new lifecycle. (CI) with a fake host; the real proof: same ten calls on a real case from two real hosts. (live PC (nachi))
  - Catalog diff test: names, order and schemas equal the pre-migration catalog. (CI)
- Size: M. Phase: Parallel A.
- Needs owner action? No.

## BL-343 Structured results, compatible text, stable error codes
- Outcome: each tool returns typed data plus an equivalent text form, with provenance and clear errors.
- Scope (in): `outputSchema` and `structuredContent` for object-shaped results; text carries the same scrubbed data, provenance, continuation and truncation (never rely on a host merging the two; report cites Codex behaviour). Fields: `schemaVersion`, provenance, partial-data flag, `nextCursor` where needed, `untrusted.label` for strings from audit data. Errors: JSON-RPC failure for malformed requests, tool `isError` for domain errors with codes `backend_unavailable`, `permission_required`, `result_expired`, `query_budget_exceeded`, `source_unavailable` and short safe recovery text. One compatibility fixture per public shape; additive changes only.
- Out of scope: cursor binding and handle rules (BL-350); disclosure tiers (BL-348).
- Depends on: BL-342.
- Risks / unknowns: whether each host shows text, structured, or both to the model is unknown (report: must be tested). Page budget optimum has no measurement (report: compare 20-row defaults vs smaller). The July spec's arbitrary-JSON structured results vs older "object only" hosts.
- Acceptance:
  - Each tool's real result validates against its output schema and equals its text form field by field. (CI) against output from a real backend test case.
  - Every error code is reachable on the real path (offline backend, no grant, expired handle, over budget, source down). (CI) and one of each from a real host. (live PC (nachi))
  - Frame and cumulative budget test: a result at the cap is truncated with a marker, never over 1 MiB incl. text copy. (CI)
- Size: M. Phase: Parallel A.
- Needs owner action? No.

## BL-344 Backend MCP endpoint on the pipe, audit-only dispatcher
- Outcome: the backend has its own read-only MCP listener and principal, so a stolen bridge credential is limited by the backend, not by the bridge's route list.
- Scope (in): second pipe (distinct from the GUI pipe of BL-301), typed dispatcher or dedicated router for the ten read operations only; reuse existing read services with their query, timeout, resource and read-only DB limits; no GUI, admin, Keeper, filesystem or generic proxy routes; per-call policy, budget, projection, audit hook. `PIPE_REJECT_REMOTE_CLIENTS`, first-instance flag, explicit DACL tested for effective rights. Full MCP is off in the services-only Server 2016+ install.
- Out of scope: grant issuing (BL-346); tiers (BL-348); bridge side (BL-345).
- Depends on: BL-301 (pipe server), P1; BL-318 (locator attach).
- Risks / unknowns: report open item "Windows boundary": ACLs on Win 10/11, RDS, multi-user and Server editions are untested. `go-winio` default dial asks generic read/write, which includes create-instance (report): test with v0.6.4 or write a narrow wrapper. Single-instance contract vs per-logon-session discovery needs a decision (see Conflicts).
- Acceptance:
  - A request for any non-MCP route on this pipe is refused. (CI)
  - A second local user and a remote client cannot open it; a pre-created same-name pipe is refused. (CI) (second account on runner; else live PC (nachi))
  - Two logon sessions of one user (RDS) do not cross-attach. (live PC (nachi)) (needs a test setup; unknown if nachi has one)
  - Real tool call on a real case works through this pipe with no dashboard token present. (live PC (nachi))
  - Windows Server 2016 headless (Blackbox only): MCP endpoint is absent. (elevated/UAC) test on a real server box if available.
- Size: L (split if the spec wants: listener vs dispatcher). Phase: Parallel A, needs P1.
- Needs owner action? No.

## BL-345 Bridge client: rendezvous, peer proof, fail closed, reconnect
- Outcome: `unravis.exe --mcp` finds and verifies the backend on the pipe, never sends a credential to an unverified peer, and recovers when the app starts later.
- Scope (in): atomic ACL-restricted rendezvous record (generated pipe name, protocol generation, process info), reparse-point aware; scoped to the logon session; verify the connected peer (PID, live handle, expected image, generation; challenge if needed); pipe client SQOS at identification level; tool list works with backend offline; data calls return `backend_unavailable` with an action message; no auto-repeat of a heavy query with unknown outcome; stdout is JSON-RPC only, stderr short and scrubbed. Remove the dashboard-token path after migration; keep the old HMAC loopback path as fallback during migration.
- Out of scope: backend side (BL-344); Streamable HTTP.
- Depends on: BL-344, BL-318, BL-342.
- Risks / unknowns: report: per-user installs with a writable exe path give weaker provenance than per-machine; trusted manifest/publisher check depends on signing (BL-312). Same-user malware can copy credentials (report states it; mitigation is a dedicated account, outside scope).
- Acceptance:
  - Stale rendezvous, wrong peer, restarted backend: no credential sent, clean error, later call reconnects. (CI) with a fake peer; plus real backend kill and restart. (live PC (nachi))
  - Start host first, start Unravis later, next data call succeeds without restarting the host. (live PC (nachi))
  - Stdout contains only valid JSON-RPC lines across a 1,000-call run. (CI)
  - Old loopback-bridge route still works while flag is on. (CI)
- Size: M. Phase: Parallel A, needs P1.
- Needs owner action? No.

## BL-346 Installation grants: issue, store, revoke, legacy default
- Outcome: each approved MCP installation has its own backend-issued revocable credential and policy revision.
- Scope (in): backend issues a random credential per installation profile; non-secret profile selector in launch config; bridge stores its credential user-protected (DPAPI or OS store, per report); backend keeps digests, compares in constant time; scoped expiry; revoke by generation on logout, case lock, explicit revoke, global stop; revoke cancels queued and running jobs, clears handles and projected caches, rechecks policy before emitting a result. Legacy `--mcp` with no profile gets an explicitly approved redacted default profile, or a "enrollment required" error on data calls.
- Out of scope: the UI (BL-347); tier rules (BL-348); OAuth.
- Depends on: BL-344.
- Risks / unknowns: report: a copied grant can impersonate its installation; DPAPI ties to user credentials only. Where the "default redacted profile" gets approved for existing users is a spec question. Case lock semantics exist in the app today (not checked here).
- Acceptance:
  - Revoke during a running real comparison: the call ends, no result is delivered after revoke. (live PC (nachi))
  - Grant file copied to another Windows user fails to decrypt. (CI) (Windows runner, second account) or live PC (nachi)
  - Restart backend: grants survive; revoked ones stay revoked. (live PC (nachi))
  - Constant-time compare and digest-only storage verified by test and by reading the store on disk (no raw credential). (CI)
  - Legacy `--mcp` with no profile gets the documented redacted default or the enrollment error, never full access. (CI) and one real host. (live PC (nachi))
- Size: L (spec may split issue/store vs revoke). Phase: Parallel A.
- Needs owner action? No. (Approving a profile is the user's click in the app, not an owner action.)

## BL-347 Grants screen in the app
- Outcome: the user can see, approve, narrow and revoke MCP access in Unravis.
- Scope (in): enable MCP; approve a profile showing exe path, host label, source/case scope, tools chosen, and a plain note that the host may send results to a hosted model; per-tool toggle; global stop; revoke each; list of current sessions. Works in browser mode first and the desktop shell later (same Svelte UI). Widening scope, identifiers and code runs ask here, never in the model's context.
- Out of scope: audit ledger (BL-351); code-mode UI inspector.
- Depends on: BL-346.
- Risks / unknowns: how a host name is established (host-reported name is advisory, per report). Desktop shell not needed to ship this (browser mode keeps working).
- Acceptance:
  - Approve a profile in the real UI, then a real host gets only the approved tools and sources; a non-approved source returns `permission_required`. (live PC (nachi))
  - Revoke in the UI cuts a connected host on its next call. (live PC (nachi))
  - UI flow tests for approve, narrow, revoke, global stop. (CI) (Playwright, per existing suite)
- Size: M. Phase: Parallel A (desktop later via P3).
- Needs owner action? No.

## BL-348 Disclosure tiers enforced in the backend
- Outcome: aggregates are the default; row-level timing and identifiers are separate grants checked on every call.
- Scope (in): tiers per report: aggregates (redacted, finite windows, both current and reference period and every source authorized), row-level evidence (request timing, activity pages), identifiers (off by default, scope+source+expiry), expensive/live queries. `--mcp-allow-identifiers` is only a request on an approved profile. Restrict identifier-based filters as well as output. Never return raw payloads, headers, credentials, URLs. Review source aliases, status, controller/action, job names, error text and query echo for identifier or tenant leaks. Low-count and intersecting-query policy (coarsen or suppress) where it keeps investigation useful. Same policy object is the hook code mode will reuse.
- Out of scope: alias format (BL-349); the grant UI (BL-347); code-mode implementation.
- Depends on: BL-344, BL-346.
- Risks / unknowns: report: current projection still emits a record ID and the guide tells the model to cite one; suppression thresholds are not given by the report (spec question). Utility vs leakage trade-off unmeasured (AgentDojo/CaMeL numbers are others' settings).
- Acceptance:
  - On a real case with real identifiers in the data: aggregate-only profile returns none of them in any field, filter, error or log. (live PC (nachi)) plus exact-shape tests. (CI)
  - Identifier filter without identifier grant is refused. (CI)
  - Intersecting-query test: two aggregates that would isolate one record are coarsened or refused per the spec rule. (CI)
  - Existing `output_audit_test.go` coverage still passes. (CI)
- Size: L (split by tier if the spec wants). Phase: Parallel A.
- Needs owner action? No.

## BL-349 Opaque evidence aliases instead of raw record IDs
- Outcome: evidence the model cites is a scoped alias the dashboard can resolve for an authorized user.
- Scope (in): alias bound to principal, case/source, policy revision, expiry; resolver in the app (first-party UI only, not an MCP tool); update `audit_get_guide` workflow text to cite aliases; keep compatibility for old callers on stable cached results.
- Out of scope: new MCP resource links (deferred); raw-ID grant (BL-348).
- Depends on: BL-348.
- Risks / unknowns: report open item "Privacy regression": which aliases are durable enough to investigate but scoped enough for privacy is undecided. Resolver UI location in the dashboard is a spec question.
- Acceptance:
  - A real alias from a real result opens the right row in the app; same alias under another profile or after revoke fails. (live PC (nachi))
  - Alias contains no raw ID or recoverable key (test by inspection and fuzz). (CI)
  - Guide text contains no tenant data (golden test). (CI)
- Size: M. Phase: Parallel A.
- Needs owner action? No.

## BL-350 Budgets, cursors, handles, cancel on disconnect
- Outcome: one noisy client or long query cannot take over the backend, and handles cannot be stolen.
- Scope (in): per-profile and global concurrency (today: 4 in flight per bridge), per-source cost budgets, window and row limits, rate limit; dedupe equal in-flight requests in one scope; cancel expensive work when the caller disconnects; no new auto-query after a deadline. Page budgets below 1 MiB (fields, rows, frame, cumulative). Server cursors bound to endpoint, principal, case/source, query fingerprint, policy revision, expiry, generation. Result handles: owner, TTL, max count and bytes, explicit expiry error, reauthorize on each use (replaces the single 10 min in-process comparison cache). Bounded synchronous calls first.
- Out of scope: MCP Tasks (deferred); start/status/cancel job contract (only if BL-340/355 show host deadlines break real investigations; then a new item).
- Depends on: BL-344, BL-346.
- Risks / unknowns: report open item "Task adoption": a 5 min comparison may exceed host deadlines; unmeasured. No measured budget numbers in the report.
- Acceptance:
  - Real heavy comparison on a real case: kill the host mid-run, backend work stops and memory returns (no leaked goroutines, matches the draft's 0 leak budget). (live PC (nachi)) and goroutine-leak test. (CI)
  - Two hosts hammering: caps hold, the GUI stays responsive. (live PC (nachi))
  - Handle from profile A fails for B, after expiry, and after revoke. (CI)
  - Cursor replay with changed query or policy is refused. (CI)
- Size: M. Phase: Parallel A.
- Needs owner action? No.

## BL-351 MCP audit store and call ledger
- Outcome: every attempted tool call is recorded locally and the user can read it.
- Scope (in): app-owned local structured store (reuse the app's SQLite if the spec agrees); record UTC time, installation id, policy revision, tool, sanitized case/source aliases, operation class, correlation id, duration, outcome, rows and bytes; include denials, timeouts, cancels, errors; admission record plus terminal record for work that may outlive the response; scope changes, enrollment, revoke logged; no credentials and no raw arguments or results; keyed digest of a canonical query fingerprint if needed; bounded fields and retention; sensitive work fails closed if audit cannot start; no tool that reads the audit trail; ledger screen with current sessions and stop/revoke.
- Out of scope: sending logs anywhere (no telemetry, per draft); code-run hash-chain store (code-mode area).
- Depends on: BL-344, BL-346; ledger UI with BL-347.
- Risks / unknowns: retention default not given by the report (spec question). Purging old audit rows would be deleting user data: any purge must be automatic by the stated retention and shown, not an owner prompt; the spec should confirm.
- Acceptance:
  - Run a mixed real session (ok, denied, cancelled, timed out): each shows as one correct ledger row. (live PC (nachi))
  - Grep of the store file after the session finds no credential and no raw argument text. (CI)
  - Audit store made read-only: a sensitive call is refused, a cheap aggregate behaves per spec. (CI)
- Size: M. Phase: Parallel A.
- Needs owner action? No.

## BL-352 Host install entry points (config text, absolute path, MCPB)
- Outcome: the user can register Unravis in each host from the app without hand-editing and with no secret in the config.
- Scope (in): app-generated, copyable config using the real installed absolute path of `unravis.exe` plus `--mcp` and the profile selector, for: Claude Code (`claude mcp add`), Codex (`codex mcp add`), Gemini (`gemini mcp add`), and `mcpServers` JSON for Claude Desktop, Cursor, VS Code. Merge one entry into an existing file (never overwrite). Test private MCPB for Claude Desktop (manifest 0.3 per report) only after the exe lifecycle check; Cursor/VS Code reviewed install links as optional. No download-and-run, no package-manager launch, no remote launcher in links.
- Out of scope: public directory listing (report: submissions closed or unclear); Streamable HTTP config.
- Depends on: BL-347; P4 installer for the final path (per-machine vs per-user).
- Risks / unknowns: report: Claude Desktop stable build unverified; MCPB native-binary support depends on exact host manifest; deep-link behaviour unverified on real hosts; the exe path changes if BL-305 splits the exe or the per-user install path differs.
- Acceptance:
  - For each of the six hosts: follow the generated text on a real PC, host lists the ten tools and runs one real call. (live PC (nachi))
  - Merge test: an existing config with other servers keeps them. (CI)
  - Generated text has no secret and no relative path. (CI)
  - MCPB: private install runs a real call, or a written "not supported" note. (live PC (nachi))
- Size: M. Phase: Parallel A, final path after P4.
- Needs owner action? No.

## BL-353 Update and rollback with MCP clients attached
- Outcome: an OTA update or rollback does not corrupt or strand a host that has `unravis.exe --mcp` running.
- Scope (in): test and fix: exe file lock while MCP runs; drain or safe replace; host keeps a stable config path; rollback restores a working MCP. Feeds the BL-305 decision.
- Out of scope: the updater itself (BL-149 to BL-151, BL-168).
- Depends on: BL-305, BL-345; P4.
- Risks / unknowns: report open item "Host and update boundary"; whether hosts restart a dead server is unknown per host. Path may change if exe is split.
- Acceptance:
  - Real OTA update N-1 to N with two real hosts attached: updater finishes, hosts reconnect or show a clear error, first call after works. (elevated/UAC) (live PC (nachi))
  - Forced bad version: rollback leaves MCP working. (elevated/UAC) (live PC (nachi))
  - No MCP process holds the old exe after the hand-off. (live PC (nachi))
- Size: S. Phase: P4.
- Needs owner action? UAC prompt only on test runs (nachi).

## BL-354 Injection and privacy regression suite
- Outcome: tests that prove hostile strings in audit data cannot widen a grant or smuggle instructions, and data leaks stay closed.
- Scope (in): fixtures of untrusted labels (controller, job, action, source description, error text) with injection phrasing and unusual-but-legal values; assert they only appear in `untrusted.*` fields, never in tool names, descriptions, authz inputs or prompts; exact-shape contract tests for rows, labels, filters, errors, logs, cursors and ownership; catalog digest test (changes need approval); run through the real host path and record model and client versions, utility and disallowed-action rates (report: measure, do not claim a rate). Denial and projection are pass/fail backend checks, independent of any model.
- Out of scope: claims about model resistance; red-teaming the model.
- Depends on: BL-343, BL-348.
- Risks / unknowns: report says no Unravis-specific attack numbers exist; benchmark numbers are others' settings. Synthetic fixtures are test-only; real-data check must use real cases the owner already has (read-only).
- Acceptance:
  - Backend invariants (no grant widening, no cross-source read) hold with injection fixtures. (CI)
  - Same fixtures loaded into a real case and queried through a real host: record what the model saw and did; no unauthorized call. (live PC (nachi))
  - Catalog digest changes fail CI until reviewed. (CI)
- Size: M. Phase: Parallel A.
- Needs owner action? No.

## BL-355 Real-host acceptance gate (six hosts, both lifecycles)
- Outcome: the exit check for Parallel A: the new MCP path works end to end on the real hosts.
- Scope (in): repeat BL-340 table on the new path: six hosts, ten tools, real case, old and new lifecycle, deadline, cancel, EOF, offline-then-start, revoke, restart generation, two logons, update with host attached. Compare to BL-340 numbers (launch, RSS, latency). Record which hosts do not support the July lifecycle yet.
- Out of scope: new features; HTTP.
- Depends on: BL-342 to BL-346, BL-348, BL-350, BL-351, BL-352; BL-353 for the update row.
- Risks / unknowns: report open item "Protocol and host uptake" (July 2026 client support); a host may need a legacy-only release (report allows it). Cloud jobs and remote workspaces cannot reach the local process (report): list them as not supported.
- Acceptance:
  - Full matrix filled, all rows pass or are marked "host limit" with evidence. (live PC (nachi))
  - No regression vs BL-340 on launch and idle memory beyond the number the spec sets. (live PC (nachi))
  - Release-checklist entry added so the matrix is re-run on each SDK bump. (CI: file exists)
- Size: M. Phase: Parallel A exit.
- Needs owner action? Maybe (host accounts).

---

## Conflicts with other areas
1. **BL-318 (area 01) vs BL-345.** BL-318 gives CLI and MCP the locator attach. BL-345 adds peer proof and grants on top. Spec must make one owner of the locator code.
2. **Single instance (BL-302/BL-309) vs per-logon-session rendezvous.** Report 04 scopes discovery to the logon session and says to reconcile with the present single-instance contract. Area 01 says single instance per session. Probably consistent; confirm.
3. **Exe split (BL-305).** If the sidecar becomes `unravis-core.exe`, the MCP launch path, configs from BL-352 and the update lock (BL-353) change. The draft keeps `unravis.exe` for MCP.
4. **Two pipes.** Draft section 3 shows MCP using "the same pipe, own grant". Report 04 wants a distinct MCP-facing pipe and dispatcher. This file follows the report (BL-344). Spec must pick one; area 01 BL-301 and area 02 pipe SDDL tests must cover both.
5. **SDDL choice** (area 01 notes the OW vs user-SID difference) applies to the MCP pipe too.
6. **Go binary mitigations (area 02 BL-327).** SDK and DPAPI use need to run under the chosen mitigations (ACG decision depends on wazero JIT, a code-mode matter).
7. **Authenticode (BL-312) vs BL-345 peer image check.** Publisher verification only works once signing exists; until then the check is path and generation only.
8. **Code mode area.** `unravis_execute` / `unravis_types` must reuse BL-346/348/350 policy and budgets and need their own local approval; its audit chain differs from BL-351.
9. **CLI area.** The CLI capability matrix and one operation layer: MCP, CLI and code mode must call the same typed layer; do not fork result shapes (BL-343).
10. **Installer and updater.** BL-352 needs installer-provided paths; BL-353 touches the OTA hand-off. Draft says MCP is on the console exe, not WebView2, so Lite and services-only installs have no MCP.
11. **Draft section 1 "no loopback port in desktop mode"** vs report 04's "use existing loopback bridge during migration". Kept as a migration fallback only (BL-345).

## Questions for spec stage
1. Which hosts does nachi actually use, and on which Windows builds? (decides BL-340 and BL-355 scope)
2. SDK choice gate: if the Go SDK fails parity, accept mark3labs v1.6.0, or ship legacy-only for a release?
3. One pipe or two (conflict 4)? Same exe or split (BL-305)?
4. Default profile for existing `--mcp` users: redacted default auto-approved once, or enrollment required?
5. Suppression or coarsening thresholds for low-count and intersecting aggregates (report gives no numbers).
6. Alias format and lifetime (BL-349); where the resolver lives in the dashboard.
7. Audit retention length and purge rule (BL-351); whether the audit store reuses the app SQLite.
8. Page and cumulative output budgets; do real host deadlines force a start/status/cancel job contract (would be a new item and deferred until measured)?
9. Streamable HTTP, Tasks, MCP resources/prompts/elicitation: all stay deferred until a required host needs them (needs owner go).
10. Same-user malware is out of the threat model per report: confirm, and whether any deployment needs a dedicated account or offline model.
11. Does the Server 2016+ services-only install ship `--mcp` at all (report says no)?
12. Is MCPB worth shipping, given the report's mixed evidence on directory status and native-binary manifest support?
