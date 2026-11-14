## Executive Risk Summary

**Overall risk: High. Verdict: NO-GO. Scope: canonical wave 9, BBX-5-07 ceiling/errors, at HEAD `33c219b4b270229f44cc2b09ab234b1c6b0c72dc`.** Reviewed complete `ceiling.go`, `errors.go`, `allowlist.go`, their tests, connection/runner/back-off dependencies and relevant tests. Read [CLAUDE.md](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/CLAUDE.md#L1), [handoff §3](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-expansion-handoff.md#L18), [security controls C1–C14](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-sql-security-review.md#L22), and the [BBX-5-07 packet](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-5-07-ceiling-errors.md#L1).

**Critical: 0; High: 2; Medium: 1; Low: 0. Top three blockers:**
1. The permission query checks direct catalog rows, not effective rights from roles/groups.
2. C8 is attached to a pool wrapper, not every physical connection.
3. An unreadable/NULL ceiling does not immediately latch auto-off.

The previous principal-ID correction is insufficient: [BBX-5-07 report, residual gap](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/reports/BBX-5-07.md#L139) itself leaves inherited permissions unresolved. An earlier review's completion statement is not closure evidence.

**Evidence level:** code-confirmed control gaps; no live SQL login, permission, service, or ACL changes. Bounded offline existing tests passed (Windows/amd64, 17.936 s, shared wave-9/10 run). Their passing result does not simulate SQL Server effective permissions or physical pool replacement. The full command is recorded in the [wave-10 report](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/reports/wave-10-adversarial-review.md#L1).

**Reachability qualification:** wave 10 identifies an independent Windows folder-reader defect that rejects ordinary explicitly secured folders before connecting. Findings below remain defects in the shipped ceiling/connection path and blockers to enabling that path; they are not claims of successful exploitation of a running service at this HEAD.

## Findings Table

| ID | Severity | Category | Component/Location | Summary |
|---|---|---|---|---|
| W9-01 | High | C8 / effective authorization | `allowlist.go:84–110`, `CheckCeiling` | Custom roles, Windows-group grants, public grants and newer fixed roles are outside the proof. |
| W9-02 | High | C8 / connection lifecycle | `runner.go:264–267,334–355`; `conn.go:202–205,309–317` | Pool replacement can execute a sampler without a ceiling check on that physical session. |
| W9-03 | Medium | C7/C8 / fail-closed state | `ceiling.go:25–26,67–75`; `runner.go:352–353,383–388` | An unreadable ceiling becomes an ordinary retryable failure rather than immediate persisted auto-off. |

## Detailed Findings

### W9-01 — Direct grants are not an effective-rights ceiling

**The Flaw:** [internal/bbsql/allowlist.go:106–110, `stmtCeilingPerms`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L106) restricts `sys.server_permissions` to `grantee_principal_id = SUSER_ID()`. It does not enumerate rights through custom/nested server roles, Windows token groups, or public. [internal/bbsql/allowlist.go:84–91, `stmtCeilingRole`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L84) checks only eight legacy fixed roles. This also leaves SQL Server 2022 `##MS_*##` roles outside the membership check. An empty permission result is unconditionally accepted by [internal/bbsql/ceiling.go:85–101, `CheckCeiling`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/ceiling.go#L85).

**Exploit / Failure Scenario:**
1. The service login has its minimal direct grants and belongs to a custom server role carrying `CONTROL SERVER` (or another disallowed right); it is in none of the eight checked fixed roles. These are preconditions, not operations performed in this review.
2. All selected role flags are zero. The offending grant belongs to the role's principal ID, so the login-only predicate returns no offending row.
3. `CheckCeiling` returns nil and sampling starts under an overprivileged identity. A Windows-group grant or a disallowed public grant has the same catalog-scope mismatch.
4. [internal/bbsql/ceiling_test.go:118–138, `TestCeilingRefusesEveryPrivilegedRole`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/ceiling_test.go#L118) injects flags directly. [internal/bbsql/ceiling_test.go:165–180, `TestCeilingPermsUsesServerPrincipal`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/ceiling_test.go#L165) pins the incomplete predicate rather than evaluating inherited authorization. Neither can catch this bypass.

**Remediation:** use a reviewed, fixed, allowlisted effective-permission query, such as a filtered server-scope effective-permission function after confirming its supported-version semantics; retain explicit checks for privileged fixed roles not fully represented there. Do not replace the direct predicate with only a one-level role join. Cover nested roles, public, Windows token groups, newer fixed roles and denied rights. Return a positive indication that the principal/permission proof was readable; do not let an unresolved principal become an empty successful result. Preserve C2 and truncation safety by returning only offending rights plus an explicit proof signal.

**Unverified Assumption:** exact Windows-group resolution, NULL behavior and newer-role permission expansion on each supported SQL version require owner-authorized platform tests. The custom-role/direct-row mismatch is visible in the query itself; uncertainty about those additional cases does not make that mismatch hypothetical. The ceiling does not grant new rights or permit arbitrary SQL; the failure is acceptance of rights already present.

### W9-02 — The ceiling is checked per wrapper reconnect, not per physical connect

**The Flaw:** [internal/bbsql/runner.go:334–355, `connExpired`/`reconnect`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L334) use a wall-clock timestamp and a `connected` boolean. The real query object remains a `*sql.DB` pool, and [internal/bbsql/conn.go:309–317, `DBQueryer.Query`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/conn.go#L309) calls `DB.QueryContext`. [internal/bbsql/conn.go:202–205, `applyPool`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/conn.go#L202) explicitly enables one-hour physical expiry. There is no connection-generation authorization hook.

The installed Go source confirms this is not speculation about a pool API: [`database/sql.(*DB).QueryContext`, SDK source:1742–1748](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/src/database/sql/sql.go#L1742) calls [`retry`:1579–1588](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/src/database/sql/sql.go#L1579); [`conn`:1344–1355](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/src/database/sql/sql.go#L1344) discards expired/bad physical connections. These SDK links are outside the repo and supplement the repository evidence above.

**Exploit / Failure Scenario:**
1. A pool connection passes both ceiling statements. Later the login gains excessive rights.
2. A tick starts just before the physical connection's one-hour expiry. `connExpired(now)` is false at admission. Physical age also begins before the post-ping `connectedAt` stamp at [internal/bbsql/runner.go:350–351](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L350).
3. A later query in the same tick occurs after physical expiry. `database/sql` replaces the connection and retries the sampler statement on the new session. No call to `Runner.reconnect` or `CheckCeiling` intervenes.
4. A successfully retried bad-connection error is likewise invisible to `Runner.fail`. The next explicit wrapper reconnect may be much later.
5. [internal/bbsql/runner_c13_test.go:17–68, `TestConnectionLifetimeReprovesCeiling`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner_c13_test.go#L17) advances a fake clock around a fake `Queryer`; it never expires or replaces a physical `database/sql` connection.

**Remediation:** bind authorization to a pinned physical connection/generation, or perform the proof in a connector-level admission step before exposing each new connection to sampling. Do not rely on another wall-clock comparison or checking a pool once before queries. Ensure automatic retry cannot send a sampler on an unproved generation. Add an in-process fake `database/sql/driver` that records connection IDs, expires/rejects a connection between batches, and proves both ceiling queries precede the first sampler on every ID.

**Unverified Assumption:** live transport/retry behavior and actual elevated-login refusal have not been exercised. The expiry/retry mechanism and missing admission gate are directly verified; no real SQL session was opened.

### W9-03 — NULL/unreadable ceiling results do not latch the required denial

**The Flaw:** NULL/missing flags correctly fail the current check, but [internal/bbsql/ceiling.go:25–26, `errCeilingUnreadable`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/ceiling.go#L25) uses generic `CodeFailed`. [internal/bbsql/runner.go:352–353, `reconnect`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L352) loses the fact that the error came from C8. [internal/bbsql/runner.go:383–388, `fail`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L383) sets its ceiling flag only for `CodeTooManyRights`. Under [internal/bbsql/backoff.go:69–85, `OnFailure`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/backoff.go#L69), a generic C8 failure retries until ten failures.

**Exploit / Failure Scenario:**
1. A membership result contains SQL NULL, or no role row is returned. [internal/bbsql/ceiling.go:67–75](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/ceiling.go#L67) returns `errCeilingUnreadable`; no sampler runs in that tick.
2. `fail` calls `OnFailure(false, false)`. State becomes enabled/backing-off at 30 seconds, not denied/auto-off.
3. A later readable response resumes sampling automatically. This violates C7's immediate latch for a C8 failure, although it is not a NULL-as-zero immediate privilege bypass.
4. [internal/bbsql/ceiling_test.go:238–281, `TestCeilingFailsClosed`](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/ceiling_test.go#L238) checks only that an error is returned. It never asserts persisted runner auto-off for those cases.

**Remediation:** retain error origin independently of its safe public code. Every failed C8 proof must take the ceiling-failure path, persist the latch, and require explicit owner re-enable. Add runner-level NULL, missing-column, no-row and failed-control-query cases, asserting zero sampler sends after subsequent ticks and reload. Keep error output fixed; do not solve this by copying driver messages.

## Cross-Wave Impact

- Canonical ownership is [GRAPH.md:42–49](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/GRAPH.md#L42). **Wave 9 is ceiling/errors only.** BBX-5-16 lane server and BBX-6-02 dump catalogue ran early alongside wave 9 but are reassigned to the separate wave-12 reviewer. See [BBX-5-16 report](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/reports/BBX-5-16.md#L1) and [BBX-6-02 report](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/reports/BBX-6-02.md#L1); neither is duplicated or signed off here.
- W9-02 requires the wave-8 connection boundary and wave-10 runner to change together. W9-03 requires preserving ceiling provenance through wave-10 failure handling and wave-7 persisted state. The [wave-10 review](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/reports/wave-10-adversarial-review.md#L1) owns folder startup, persistence reliability, scheduling and C13.
- No new raw-driver-text leak was established in the real `Classify` path. [internal/bbsql/errors.go:57–99](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/errors.go#L57) passes preconstructed `*Error` values through; production constructors inspected use fixed strings. Arbitrary caller-created `*Error` values are not evidence of a live input exploit.
- Direct `DENY` rows are also treated as excess rights because `stmtCeilingPerms` does not inspect permission state. This is conservative over-refusal, not extra privilege. Effective-permission remediation must not instruct owners to remove protective DENYs merely to make startup pass.

## Go / No-Go Verdict

**NO-GO for enabling the SQL subsystem.** Required closure evidence:
1. On each supported SQL generation, minimal effective rights pass, while direct/custom/nested/public/Windows-group excess rights and all privileged supported fixed roles refuse. Unknown/NULL proof outcomes immediately persist auto-off. No grants are performed by the application.
2. A connection-generation driver test records **zero sampler sends before a successful ceiling on that same physical connection**, including expiry between queries, bad-connection retry and wall-clock changes.
3. Each C8 failure produces **one-tick immediate auto-off**, survives reload, and cannot resume without the owner path; satisfy wave-10 persistence/recovery requirements too.
4. Repeat C1/C2/C10 tests after changes and obtain owner-authorized Windows/SQL end-to-end evidence. **Unverified Assumption:** neither passing injected rows nor successful compilation proves those platform behaviors.

This reviewer wrote only this report and the wave-10 report. Final HEAD remained unchanged and `git diff --stat` was empty; other reviewers' untracked wave reports appeared concurrently and were left untouched. No source/tests/config/ACLs were edited, no commit/push occurred, and no real services or external uploads were used. Reassigned findings belong to [.forge/blackbox-expansion/reports/wave-12-adversarial-review.md](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/reports/wave-12-adversarial-review.md#L1).
