## Executive Risk Summary

**Overall risk: High. Critical: 0; High: 3; Medium: 2; Low: 1. Verdict: NO-GO.**

Scope: canonical wave 7, BBX-5-01, BBX-5-03, BBX-5-05 and BBX-5-08, at **HEAD 33c219b4b270229f44cc2b09ab234b1c6b0c72dc**. Review covered complete scoped implementations/tests, platform twins, and installation, configuration, recorder, runner, CLI and driver call paths. The review used [CLAUDE.md](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/CLAUDE.md#L1), [handoff section 3](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-expansion-handoff.md#L18), [C1–C14](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-sql-security-review.md#L22), and the four wave packets. This is a report, not a fix or release certification.

**Top three blockers:**
1. **W7-01:** the installed executable/configuration are inaccessible to the new virtual account under the normal host-install ACL.
2. **W7-02:** two production sampler statements reference nonexistent DMV columns: requests uses `r.wait_time_ms`, and semaphores uses `timeout_count`.
3. **W7-03:** securing an existing SQL directory does not replace its owner or remove explicit/protected permissions on existing children.

Bounded tests actually run in this review: selected SQL control tests passed (`internal/bbsql`, 0.622 s); fake installation, recorder redaction and local HTTP status tests passed (`internal/blackbox`, 2.211 s). Both used the supplied Go 1.27.3 binary, `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`, `-count=1 -timeout=60s`. SQL selection covered allowlist/lint, backoff/load, connection settings, reader caps, classification and output decoding; blackbox selection covered SQL SID/SDDL, install/remove, recovery configuration, fixed event errors and SQL status. These tests do **not** execute the SQL statements against SQL Server or start an SCM service. Prior packet reports/gates were not accepted as proof.

## Findings Table

| ID | Severity | Category | Component/Location | Summary |
|---|---|---|---|---|
| W7-01 | High | Availability / service identity | [internal/blackbox/sqlservice.go:120–162](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/sqlservice.go#L120), `SQLManager.InstallSQL`; [service.go:299–330](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/service.go#L299) | New account receives data-directory access, but no executable/configuration access. |
| W7-02 | High | SQL correctness / service-wide failure | [internal/bbsql/allowlist.go:15–25,49–51](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L15), `stmtRequests`, `stmtSemaphores` | Invalid source column names cause deterministic batch failures and eventual global auto-off. |
| W7-03 | High | Filesystem authorization | [internal/blackbox/sqlservice_windows.go:14–25](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/sqlservice_windows.go#L14), `secureSQLDir`; `InstallSQL:131–143` | Existing untrusted ownership/child ACEs survive the advertised lock. |
| W7-04 | Medium | C7 fail-closed persistence | [internal/bbsql/backoff.go:130–176](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/backoff.go#L130), `Load`, `normalize` | Syntactically valid but incomplete/inconsistent state silently becomes enabled. |
| W7-05 | Medium | C2/C3 assurance boundary | [internal/bbsql/allowlist_test.go:25–85](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist_test.go#L25), `bannedToken`, `TestLintC2` | Lint does not prove exactly the fixed SET prefix followed by one read-only SELECT. |
| W7-06 | Low | C7 sampling cadence | `internal/bbsql/backoff.go:50–63,90–98`, `OnSuccess`, `OnSlow` | One slow strike can demote the cadence, despite the specified three-strike threshold. |

## Detailed Findings

**W7-01 — The virtual account cannot consume the installed program/configuration**

**The Flaw:** `Manager.Install` calls `SecureDir(progDir, false)` before copying the executable and saving configuration (`service.go:299–330`). [internal/blackbox/service_windows.go:24–46](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/service_windows.go#L24) defines that protected, inheritable DACL as only SYSTEM and Administrators. `InstallSQL` subsequently registers that same executable, explicitly selects `NT SERVICE\UnravisBlackboxSql`, and grants its SID only `<folder>\sql` (`sqlservice.go:131–149`). It never adds read/execute rights to the program or read rights to configuration. The account/SID plumbing itself reaches `mgr.Config` (`service_windows.go:120–129`); an unrestricted service SID is not membership in Administrators or SYSTEM.

The default configuration is inside the same protected program directory. Even if process creation happens to get past executable access on a particular setup, [cmd/unravis-blackbox/main.go:428–433](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/main.go#L428) calls `LoadConfig` as the SQL service identity. The test environment's `SecureDir` records text rather than applying permissions ([internal/blackbox/service_test.go:85–92](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/service_test.go#L85)); SQL install tests do not exercise token access.

**Exploit / Failure Scenario:**
1. Preconditions: a fresh normal host installation; no manually added permissions; the SQL virtual account has no unrelated administrative membership. No attacker is required.
2. Host installation creates executable/configuration under the SYSTEM/Administrators-only DACL.
3. Owner runs `install-sql`. The SQL service is created under the separate account and pointed at those files.
4. Service launch or configuration loading is denied; installation fails/rolls back or the service fails startup. Repeated service recovery cannot repair the access list.

**Remediation:** provision explicit least-privilege access for the SQL SID to the installed executable (read/execute) and configuration (read), and necessary directory traversal; keep write/owner/DACL rights administrator-only. Do not solve this by running SQL as SYSTEM or making the program/config writable. Validate the installed executable exists rather than assuming an arbitrary supplied configuration proves a host installation. Add an installation access check and an isolated Windows test under the actual virtual-account token. **Unverified Assumption:** the precise SCM error/launch stage depends on the deployed ACL/token; no service was installed here. The missing ACL grant is source-verified.

**W7-02 — Requests and semaphores cannot compile against the documented DMVs**

**The Flaw:** `stmtRequests` has `MAX(r.wait_time_ms)` at line 17 and `r.wait_time_ms` again at line 20, with `r` bound to `sys.dm_exec_requests` at line 21. Microsoft's [sys.dm_exec_requests reference](https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-objects/sys-dm-exec-requests-transact-sql?view=sql-server-ver17) specifies **`wait_time` (int)**: “duration in milliseconds, of the current wait”; it does not define `wait_time_ms` in this DMV. `wait_time_ms` belongs to the separate wait-stats DMV and is valid in `stmtWaits`.

There is a second independently invalid statement: `stmtSemaphores` selects `timeout_count` at line 49. Microsoft's [sys.dm_exec_query_resource_semaphores reference](https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-objects/sys-dm-exec-query-resource-semaphores-transact-sql?view=sql-server-ver17) defines **`timeout_error_count` (bigint)**, not `timeout_count`. Both source-column checks were made against the public reference during this review; no live SQL was run.

The downstream blast radius is verified in [internal/bbsql/runner.go:292–305,383–390](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L292): a query error aborts the whole tick, drops the connection and increments the shared failure state. `backoff.go:69–85` latches global auto-off at ten consecutive failures. With the registry sorted by kind, requests fails before schedulers/semaphores/tempdb/waits; fixing requests alone exposes the semaphore failure next. Tests generating rows from output aliases never ask SQL Server to resolve source columns.

**Exploit / Failure Scenario:**
1. Preconditions: W7-01 is addressed, TLS and login work, rights satisfy the ceiling, and the normal sampler registry is used. No malicious SQL input is needed.
2. The requests batch is submitted and SQL Server rejects its unknown source column.
3. Every subsequent retry hits the same batch error; after ten failing ticks all SQL sampling is auto-off.
4. Correct only requests and the separate semaphore typo continues the service-wide failure cycle.

**Remediation:** change both requests expressions to `r.wait_time`, preserving existing output aliases. Select `timeout_error_count AS timeout_count` for semaphores (or deliberately migrate the column mapping and all consumers together). Add source-column contract coverage and execute every exact allowlisted batch under the intended grants on supported SQL Server versions. Attribute these invalid statements to **wave 7 / BBX-5-05**, not the wave-11 decoders. The packet itself repeats the incorrect names ([BBX-5-05 packet:20–24](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-5-05-allowlist.md#L20)); following it does not make the SQL valid.

**W7-03 — An existing folder is not made exclusively trusted by replacing only its DACL**

**The Flaw:** `ensureDir` accepts existing directories with `os.Stat`/`MkdirAll` (`service.go:190–205`), and `secureSQLDir` sets only `DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION`, passing a nil owner. An existing ordinary-user owner stays owner and can change the DACL. Existing children also are not enumerated/resecured: Windows inheritance adds inherited ACEs after noninherited child ACEs; it does not remove those explicit permissions. This behavior is documented in [SetNamedSecurityInfoW](https://learn.microsoft.com/en-us/windows/win32/api/aclapi/nf-aclapi-setnamedsecurityinfow). The comment claiming everything inside gets the same rights is therefore too strong.

The startup checker is not a mitigation for ownership/child permissions: [internal/bbsql/folder_windows.go:35–84](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/folder_windows.go#L35) reads the named directory's DACL, not its owner or existing files. Existing state/sample files are opened in place; `Save` uses `os.WriteFile` (`backoff.go:109–115`) and `appendSample` uses `O_CREATE|O_APPEND|O_WRONLY` (`runner.go:555–570`). A `0600` argument does not replace an existing Windows DACL.

**Exploit / Failure Scenario:**
1. Preconditions: before installation, an ordinary local user could create/own `<recordings>\sql` or its children in an owner-selected, previously writable location. Alternatively, retained files already contain explicit user ACEs. This is **not** an assertion that an ordinary user can write into a correctly established fresh protected directory.
2. Owner installs/reuses the location. The installer replaces the top directory DACL but preserves ownership and child explicit/protected permissions.
3. The retained directory owner restores their own access after the one-time startup check, or opens a known child with a retained read ACE and ordinary traversal privileges.
4. SQL data becomes readable or an existing state/sample file remains alterable despite the claimed three-principal boundary. No SQL grant or administrator token is needed for the preexisting owner to exercise ownership rights.

**Remediation:** require a trusted owner and trusted ancestors, securely create the directory without accepting unexpected existing objects, or explicitly migrate existing contents through handle-based ownership/DACL verification. Reject reparse points/unsafe redirection before privileged ACL mutation and verify every existing state/sample file that will be reused. Keep safe rollback semantics for preexisting data. Coordinate the runtime owner/child/reparse checks with wave 10 rather than assuming its current root-DACL check proves this installation invariant. **Unverified Assumption:** exact junction traversal/race behavior and effective inherited/protected child access on target Windows must be tested in an isolated fixture. No junction-based privilege escalation is claimed here without that test.

**W7-04 — Corrupt-state handling accepts semantically empty or contradictory state as enabled**

**The Flaw:** `Load` only treats a JSON syntax/type decoding error as corrupt. `json.Unmarshal` accepts `null`, `{}`, missing keys and unknown keys; `normalize` then promotes the zero value to interval 15 s and state `on`. It also accepts a denied/auto-off label without `autoOff:true`, and a failure count of ten without the latch (`backoff.go:139–176`). These are source-level counterexamples, not newly executed test fixtures: `null`, `{}`, and `{"state":"denied","failures":10}` each lack the information needed to authorize resumption yet reach an enabled state. [internal/bbsql/backoff_test.go:260–270](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/backoff_test.go#L260) covers only syntactically invalid JSON.

**Exploit / Failure Scenario:**
1. Preconditions: an existing state file is replaced by a syntactically valid incomplete/inconsistent record during corruption, recovery or an incompatible writer. No general ability for ordinary users to edit protected state is assumed.
2. Restart loads that record; missing boolean fields become false.
3. Normalization converts the record to `on`/`backing-off`, and the runner is allowed to reconnect without an explicit owner re-enable.

**Remediation:** decode a versioned, bounded, strict state envelope with required/presence-checked fields; reject `null`, empty objects, duplicate/unknown keys and impossible state/latch/failure combinations. Treat any ambiguous existing state as auto-off. Test all those cases through `NewRunner`, including zero connects after load. Keep an explicit owner operation as the sole intentional reset. The non-atomic, non-flushed `Save` at lines 109–124 additionally needs durable replacement/fault-injection coverage; platform crash durability has **not** been established here, and ignored save failures in `Runner.fail` remain wave-10 integration ownership.

**W7-05 — The lint accepts session-option overrides and SELECT-with-side-effects forms**

**The Flaw:** `TestLintC2` checks a string prefix, substring count of SELECT, presence of a tag, and a blacklist. It does not parse the remainder as exactly one permitted SELECT. Inserting `SET LOCK_TIMEOUT -1;` immediately after the required prefix leaves all these checks satisfied and overrides C3. `SELECT ... INTO #bb ...` is also not rejected by the listed keywords; INTO is absent. These examples establish an acceptance-boundary defect in the mandatory regression control, not remotely supplied SQL injection. Literal removal for `N'WAITFOR'`/`N'SP_SERVER_DIAGNOSTICS_SLEEP'` is reasonable; it is not the problem.

`Statements` is also an exported mutable map (`allowlist.go:115–143`), so exact-match admission is relative to the current map. No production mutation was found: an attacker with only HTTP access cannot use this fact to insert a statement. It nevertheless must not be described as a mechanically immutable send policy.

**Exploit / Failure Scenario:**
1. Preconditions: a later code change introduces a fixed batch containing an extra SET or a SELECT INTO; no arbitrary SQL input path is assumed.
2. The changed entry still starts with the fixed prefix, contains one SELECT and its tag, and avoids the blacklist.
3. C2 lint passes; the real send gate admits the altered entry. Lock-timeout protection or read-only semantics can regress without the promised gate detecting it.

**Remediation:** validate a restricted SQL grammar/token sequence: exact prefix, one SELECT, no trailing statement or SELECT INTO, and only approved source/output expressions. Add negative tests for post-prefix SET, SELECT INTO, extra semicolons/statements and comment/quoted-token variants. Make the production registry private/read-only to callers. Keep the benign literal exceptions narrow. Schema execution tests required by W7-02 are separate from syntax lint.

**W7-06 — OnSuccess applies a slow floor before three strikes**

**The Flaw:** `OnSlow` waits for `SlowStrikeLimit == 3`, but `OnSuccess` imposes a 60 s floor whenever `SlowStrikes > 0` (`backoff.go:55–61`). From `NewState`, one `OnSlow(2s,3s)` followed by `OnSuccess` yields 60 s, not 15 s. The runner calls those methods in that order (`runner.go:311–317`). Existing slow tests check three strikes before success, not one/two strikes followed by success.

**Exploit / Failure Scenario:**
1. Preconditions: an otherwise successful tick ends with one query just over half its limit.
2. `SlowStrikes` becomes one; `OnSuccess` immediately applies the demoted floor.
3. The next sampling opportunity is delayed to 60 s without the required three consecutive slow observations, reducing evidence resolution after a transient delay.

**Remediation:** model demotion separately from a pending strike streak, or apply the floor only once the documented threshold is reached, with explicitly specified recovery. Add one/two/three-strike tests using the production `OnSlow` then `OnSuccess` order. Wave 10 owns the separate problem of attributing consecutive slow observations per sampler rather than globally.

## Cross-Wave Impact

- **Wave 8:** W7-01 blocks its real service entry before connection controls can be exercised. W7-02 causes its connection/query error paths to enter C7 auto-off. See [wave-8-adversarial-review.md](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/reports/wave-8-adversarial-review.md#L1) for driver deadlines, physical reconnection, install/grant ordering, status and caps.
- **Wave 9:** effective-rights ceiling semantics/classification remain that review's ownership. This report does not treat the present C8 query result as proof of least privilege.
- **Wave 10:** startup folder identity/owner/child verification, save-error handling, tick cancellation/skip accounting and per-sampler slow scheduling remain downstream. W7-03 owns the unsafe installation/adoption operation; do not count the same root-DACL assumption twice.
- **Wave 11:** invalid SQL source names are W7-02. Once corrected, the semaphore reference also establishes nonunique `resource_semaphore_id` across resource pools, bigint cumulative counters, and NULL small-query forced/timeout counters. Pool-key collisions, nullable-counter interpretation and deltas need downstream review. The [session-space-usage reference](https://learn.microsoft.com/en-us/sql/relational-databases/system-dynamic-management-objects/sys-dm-db-session-space-usage-transact-sql?view=sql-server-ver17) describes lifetime allocations from completed tasks, not current live-task occupancy; coordinate tempdb meaning there, without silently relabeling the series.
- **Fixed error boundary:** [internal/blackbox/recorder.go:207–245,301–314](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/recorder.go#L207) and [store.go:414–425](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/store.go#L414) replace the reviewed raw collector/write/housekeeping/rollback errors with fixed codes. Existing housekeeping messages still contain recorder filenames, and `Store.Event` remains a general string API; this is not a claim that all event text everywhere is gone. No additional current SQL driver-text leak through this recorder boundary was established.
- **Unverified Assumptions / limitations:** no actual SCM install/run/recovery, effective token/access check, reparse-point attack, SQL execution, live C3/C5 session inspection, disk-crash persistence or CPU/memory budget measurement was performed. Public SQL schema documentation and local dependency source are evidence of contracts, not live results. Concurrent other-wave report files were left untouched.

## Go / No-Go Verdict

**NO-GO at the scoped HEAD.** Exit conditions:

1. A clean host-then-SQL installation runs as the exact virtual account and can read/execute only the needed program/configuration files; ordinary users and the SQL account cannot change program/configuration or read the read-key hash. Restart/recovery works without ACL widening.
2. Every exact allowlisted batch executes under the minimum owner grants on the supported server matrix, including both corrected source columns; ten normal ticks do not auto-off from schema errors.
3. Isolated Windows tests cover preowned SQL directories, explicit/protected child ACEs and reparse objects; unsafe adoption is refused or safely repaired before data is written.
4. Existing incomplete/inconsistent persisted state always produces auto-off and zero connects; persistence-failure/crash tests establish the intended durable latch, jointly with wave 10.
5. Lint rejects all demonstrated post-prefix/side-effect mutants, and one/two slow strikes do not demote while the third does.
6. The wave-8 blockers and downstream C8/runner/decoder gates are resolved with new evidence. Passing the already-run fake tests alone does not satisfy these conditions.
