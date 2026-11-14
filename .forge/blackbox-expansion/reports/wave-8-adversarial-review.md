## Executive Risk Summary

**Overall risk: High. Critical: 0; High: 3; Medium: 5; Low: 1. Verdict: NO-GO.**

Scope: canonical wave 8, BBX-5-02, BBX-5-04, BBX-5-06 and BBX-5-09, at **HEAD 33c219b4b270229f44cc2b09ab234b1c6b0c72dc**. Complete scoped files/tests, platform entry points, direct callers, configuration, runner and relevant local go-mssqldb/Go standard-library sources were inspected. Review inputs: [CLAUDE.md](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/CLAUDE.md#L1), [handoff section 3](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-expansion-handoff.md#L18), [C1–C14](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-sql-security-review.md#L22) and this wave's four packets. No fixes or live-service operations were performed.

**Top three wave-8 blockers:**
1. **W8-01:** nominal query/connect deadlines do not bound the real driver's handshake/cancellation path or propagate service cancellation end to end.
2. **W8-02:** physical pooled reconnections bypass the application-level rights-ceiling hook.
3. **W8-03:** installation starts sampling before printing the owner grant, readily latching denial without a supported owner re-enable command.

These problems are additional to wave 7's executable/configuration ACL blocker and invalid requests/semaphore SQL. Fixing only the service launch does not make this wave shippable.

Tests actually run here, with the supplied Go 1.27.3 binary, network module fetching disabled (`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`), `-count=1 -timeout=60s`: selected `internal/bbsql` control tests **PASS, 0.622 s**; selected `internal/blackbox` SQL install/redaction/status tests **PASS, 2.211 s**; selected `cmd/unravis-blackbox` SQL CLI tests **PASS, 2.084 s**. The CLI selection was `TestGrantScriptPrintedNotExecuted`, `TestRevokeScriptPrinted`, `TestSQLServiceModeNoHTTP`, `TestInstallSQLNoElevateGrant`, `TestGrantScriptLint`, `TestInstallSQLSpecAccountAndSID`, `TestInstallSQLConfigPathBoundToService`, `TestSQLCommandsFixedReasons`, `TestSQLDiskBounds`. SQL tests used stub connectors, and status tests used temporary local httptest servers; no SQL Server, SCM service or real grant was contacted. The real Windows service-handler test was not run. Existing packet gates are not evidence of the missing cases below.

## Findings Table

| ID | Severity | Category | Component/Location | Summary |
|---|---|---|---|---|
| W8-01 | High | C4 deadlines / shutdown | [internal/bbsql/conn.go:259–277,317–351,374–393](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/conn.go#L259), `Open`, `DBQueryer.Query`, `Query` | Context budgets stop neither all connect work nor driver cancel-drain work; parent cancellation is lost. |
| W8-02 | High | C5/C8 connection identity | `internal/bbsql/conn.go:202–205,271–277,309–317`; [internal/bbsql/runner.go:329–355](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner.go#L329), `connExpired`, `reconnect` | Ceiling proof belongs to a DB wrapper, not each physical connection. |
| W8-03 | High | Installation / C7 owner lifecycle | [cmd/unravis-blackbox/main.go:292–317](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/main.go#L292); [internal/blackbox/sqlservice.go:150–164](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/sqlservice.go#L150) | Service starts before the grant is printed; an initial login failure latches permanent auto-off without a CLI re-arm flow. |
| W8-04 | Medium | C11 status integrity | [internal/blackbox/server.go:254–290](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/server.go#L254), `readSQLStatus` | Old status survives stop/remove indefinitely; malformed state can retain `enabled:true`. |
| W8-05 | Medium | C6 completeness signaling | [internal/bbsql/output.go:17–27,167–198](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/output.go#L17), `decodeRows`; `conn.go:313–345` | SQL TOP n prevents the n+1 sentinel from ever arriving, so truncation is invisible on real over-cap results. |
| W8-06 | Medium | C1/C3 send boundary | `internal/bbsql/conn.go:275–277`, `Open` | Driver Ping sends an untagged, unprefixed `select 1;` outside the allowlist. |
| W8-07 | Low | C10 CLI redaction | `cmd/unravis-blackbox/main.go:144–168,297–312,428–443`, `answer.fail` | Fixed reason codes accompany raw OS/configuration error text in SQL CLI answers. |
| W8-08 | Medium | C9 typed output / missing data | `internal/bbsql/output.go:86–142,178–224`, `decodeRows`, `decodeRow` | Unknown-field rejection is not required-field/null/enum validation; corrupt rows can become factual zero values. |
| W8-09 | Medium | Configuration path identity | `cmd/unravis-blackbox/main.go:215–227,297–301`; `internal/blackbox/sqlservice.go:99–100,145–149` | Relative `--config` paths are validated in the installer directory but persisted unchanged for SCM startup. |

## Detailed Findings

**W8-01 — C4 is not enforced across the real driver lifecycle**

**The Flaw:** `Open` accepts no parent context, parses certificate paths before creating its timeout, then derives the ping timeout from `context.Background()` (`conn.go:259–277`). The runner creates another independent background tick context and its `ConnectFunc` has no context parameter (`runner.go:98–107,255–258,342–352`). Service Stop cancels the service context, but [cmd/unravis-blackbox/sqlservice_windows.go:78–82](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/sqlservice_windows.go#L78) then waits for the running work; that cancellation does not reach the in-progress tick/connection.

Dependency inspection confirms this is more than an API-style concern:
- [go-mssqldb v1.11.2/protocol.go:69–96](file:///C:/Users/nachi/go/pkg/mod/github.com/microsoft/go-mssqldb@v1.11.2/protocol.go#L69) calls `net.LookupIP` before this application's custom dialer; the application's context-aware resolver seam does not cover that lookup.
- [go-mssqldb/tds.go:1199–1255,1286–1298](file:///C:/Users/nachi/go/pkg/mod/github.com/microsoft/go-mssqldb@v1.11.2/tds.go#L1199) applies the context to dialing, then reads prelogin and calls `tlsConn.Handshake()` without a context. [go-mssqldb/net.go:21–38](file:///C:/Users/nachi/go/pkg/mod/github.com/microsoft/go-mssqldb@v1.11.2/net.go#L21) resets the socket deadline to **now + ConnTimeout on every I/O**, not to the absolute connect/tick deadline. A peer making partial progress can exceed the advertised five seconds.
- On query cancellation, [go-mssqldb/token.go:1296–1357](file:///C:/Users/nachi/go/pkg/mod/github.com/microsoft/go-mssqldb@v1.11.2/token.go#L1296) sends attention and drains with fresh background-context timeouts; `cancelDrainTimeout` is five seconds (line 118). This cleanup can extend a three-second query beyond the five-second whole-tick limit. `DBQueryer.Query` waits synchronously for row closure; a context deadline is not itself a hard return bound.

**Exploit / Failure Scenario:**
1. Preconditions: service is otherwise runnable. A busy/broken SQL instance delays cancel acknowledgement; alternatively, an ordinary local process owns the unused SQL TCP port while SQL is stopped and slowly supplies prelogin bytes. The latter requires no trusted TLS certificate because it occurs before TLS authentication, and does not imply disclosure of authenticated data.
2. A tick starts connecting or querying. Its advertised deadline expires during prelogin, handshake or cancellation cleanup.
3. The dependency continues using per-I/O deadlines or a fresh drain timeout instead of the remaining tick budget.
4. Sampling stalls past C4; Stop waits for that same work. Host sampling remains in a separate process, so this is not a claim that its collector is synchronously blocked.

**Remediation:** thread a service/tick context through `Open`, `ConnectFunc`, `Tick` and query closure. Bound certificate loading/resolution and the entire connect lifecycle, not just TCP dial. Use or adapt a connector/transport whose absolute deadlines cannot be extended by driver `SetDeadline` calls and whose cancellation closes/invalidates the active physical connection within budget. Test prelogin partial reads, TLS stalls, missing attention acknowledgement, certificate read stalls and service Stop during connection/query work. **Unverified Assumption:** actual Windows/SQL wall times and cancellation behavior require isolated fault-injected driver/live tests; none were run. Source proves the independent waits and lost context, not a measured latency.

**W8-02 — One logical DB is not one continuously ceiling-checked physical connection**

**The Flaw:** pool size one and one-hour lifetime are set on `*sql.DB`, but the unwrapped mssql connector is supplied to `sql.OpenDB`. The only `CheckCeiling` invocation is after the runner's own `reconnect`. A `DB.QueryContext` can acquire a replacement physical connection without invoking that function. The runner's hour timer is measured after `Open`/Ping and checked only at tick start; a physical connection can expire between statements or slightly before that timer.

Verified dependency chain: [Go database/sql/sql.go:1344–1355](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/src/database/sql/sql.go#L1344) discards expired or bad-reset connections; lines 1434–1452 call `connector.Connect`; lines 1574–1588 automatically retry `ErrBadConn`. [go-mssqldb/mssql_go110.go:15–43](file:///C:/Users/nachi/go/pkg/mod/github.com/microsoft/go-mssqldb@v1.11.2/mssql_go110.go#L15) performs session reset but has no Blackbox ceiling hook. [go-mssqldb/mssql.go:274–300](file:///C:/Users/nachi/go/pkg/mod/github.com/microsoft/go-mssqldb@v1.11.2/mssql.go#L274) can produce retryable broken-connection errors. The existing [runner test:344–387](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/runner_test.go#L344) tests an explicit runner failure/reconnect using a fake Queryer, not a silent pool replacement.

**Exploit / Failure Scenario:**
1. Preconditions: a compliant connection passes C8; the login later gains excessive rights through an administrative change. This is a configuration-policy failure, not an ordinary user's power to grant themselves SQL rights.
2. A tick starts just before the physical connection reaches one hour, or a retryable connection failure occurs during a send.
3. `database/sql` replaces the physical connection and retries the sampler statement directly.
4. Sampling occurs under a connection whose rights have not been proved. Even a corrected wave-9 ceiling implementation is bypassed for this connection.

**Remediation:** bind ceiling validation to each physical `Connector.Connect` before exposing the connection, or explicitly own a pinned connection and replace/revalidate it without invisible pool retries. Both ceiling statements and subsequent samples must refer to the same established session. Tests must record physical connection generations and show that every generation receives the ceiling checks before any sampler batch, including expiry between statements and retryable `ErrBadConn`. Wave 9 owns what rights are checked; this finding owns when/where the connection control calls it.

**W8-03 — First installation races the human grant and has no owner re-enable workflow**

**The Flaw:** `install-sql` invokes `InstallSQL` before printing the grant. `InstallSQL` starts the service and waits for `SvcRunning`; [cmd/unravis-blackbox/sqlservice_windows.go:59–62](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/sqlservice_windows.go#L59) reports Running before configuration/runner readiness. `Runner.Run` waits only the initial 15 s then connects (`runner.go:574–592`). With TLS working but no login/grant yet, `Open` preserves login/permission classification, `Runner.fail` latches denied and saves it. [internal/bbsql/backoff.go:69–102,130–144](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/backoff.go#L69) intentionally keeps that latch across restarts.

`OwnerEnable` has no production caller: its uses are in backoff/runner tests. The CLI switch has no SQL start/re-enable command. Ordinary `start`/`apply` target the host service. Remove/reinstall retains SQL state by default, so it does not re-arm either. Printed grant/revoke scripts remain owner-run; the app was not found executing them. The defect is ordering and missing owner activation, not an unauthorized grant.

**Exploit / Failure Scenario:**
1. Preconditions: launch ACLs are fixed, a local instance has usable TLS trust, and this is the first SQL installation with no virtual-account login. No attacker is needed.
2. Owner runs `install-sql`, then takes more than approximately 15 s to open SSMS and run the printed grant.
3. The first connection fails for missing login/permission and persists denied/auto-off.
4. Owner completes the grant and restarts the service, but sampling remains off. Recovery requires undocumented manual state surgery; the successful install answer did not prove readiness.

**Remediation:** separate install from explicit owner activation. Create the service stopped (or explicitly unarmed), print the grant, then offer an administrator-only `enable-sql`/activation operation after the owner executes it. Activation must confirm the installed configuration/folder, atomically clear only the intended latch, and start/check the SQL service. Never auto-clear a genuine C7 denial on routine restart. Add a fake-SCM test with a delayed owner grant and a production-entry test that fails configuration before reporting ready. Coordinate re-arm persistence with wave 7 and runner state handling with wave 10.

**W8-04 — `/status` trusts historical and inconsistent SQL status indefinitely**

**The Flaw:** `readSQLStatus` has no age/liveness check; it copies an old `enabled:true,state:on` record unchanged. `Runner.Run` closes the connection without publishing off on exit (`runner.go:576–583`); `RemoveSQL` retains the SQL folder (`sqlservice.go:196–205`). Consequently clean Stop/remove can leave the host advertising on indefinitely. `lastOK` is exposed but nothing reconciles it with the claimed state.

For a corrupt state string, `server.go:271–276` changes only State to `off`, retaining Enabled, Failures and LastOK. A file such as `{"enabled":true,"state":"unknown","failures":-1}` becomes enabled/off/-1. Known states also accept contradictory Enabled values. `dec.More()` at line 268 is not an end-of-document check: a closing array/object delimiter after a complete object can return false rather than rejecting trailing data. [internal/blackbox/server_test.go:515–537](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/server_test.go#L515) checks state spelling, not consistency/freshness.

**Exploit / Failure Scenario:**
1. Preconditions: one healthy status file exists; an authenticated status reader polls the host. No file attacker is required.
2. SQL service stops, is removed, fails startup, or can no longer update status.
3. Host remains running and continues returning the historical SQL `on` record.
4. Separately, a malformed local status document can present contradictory enabled/off or negative failure information instead of the intended safe fallback.

**Remediation:** publish off on clean stop/remove and reject stale status through a documented bounded freshness/liveness rule, accounting for 15-minute backoff. Keep the public C11 field set unchanged; use private heartbeat/file metadata or an internal generation marker if needed. Validate the complete document with a second decode requiring EOF, required fields, nonnegative failures, valid timestamps and state/enabled consistency; return the entire off object on failure. Bound status file size before allocating. Add stop/remove/stale/crash, invalid-state-enabled, trailing delimiter and negative-value tests. Wave 10 owns heartbeat/terminal status writing; wave 8 owns honest endpoint projection.

**W8-05 — Real SQL truncation cannot set the advertised truncated flag**

**The Flaw:** `decodeRows` flags truncation only upon receiving row n+1; `DBQueryer` reads n+1, but [internal/bbsql/allowlist.go:15,29,49,60,67](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist.go#L15) returns `TOP n` matching the output cap. For example TOP 256 filestats can never return the 257th row needed to signal incompleteness. The same mismatch affects requests, waits, semaphores and AG. Current cap tests feed an impossible over-TOP fake result; the acceptance test explicitly requires SQL TOP and output cap to match ([internal/bbsql/allowlist_test.go:266–300](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/allowlist_test.go#L266)). This is not an unbounded-read allegation: the rows are bounded, but completeness is misrepresented.

**Exploit / Failure Scenario:**
1. Preconditions: an instance has more than 256 visible database files or more than another sampler's cap; ordinary workload/configuration suffices.
2. SQL returns precisely its TOP n rows and conceals the excess.
3. The Go decoder sees exactly n and returns `truncated:false`.
4. The stored sample appears complete while files/waits/replicas are omitted, potentially hiding the affected database during an investigation.

**Remediation:** distinguish output cap n from fetch cap n+1; request the bounded sentinel row in SQL, keep at most n, and preserve the truncation flag through baseline-only/empty-delta results. Alternatively supply a separately reviewed bounded completeness signal. Update the tests to model the SQL TOP semantics and cover n-1, n, n+1 and larger logical result sets. The producer cap adjustment belongs with wave 7; the cap contract and signal are BBX-5-09 ownership. Wave 11 must carry the signal without manufacturing data.

**W8-06 — Ping bypasses C1/C3 with an extra SQL batch**

**The Flaw:** `Open` calls `db.PingContext` rather than acquiring a connection without SQL or using an approved tagged control statement. [go-mssqldb/mssql.go:1266–1273](file:///C:/Users/nachi/go/pkg/mod/github.com/microsoft/go-mssqldb@v1.11.2/mssql.go#L1266) implements Ping with the literal **`select 1;`** via `Stmt.ExecContext`. This has no fixed SET prefix/tag, is not in `Statements`, and is sent before the runner's ceiling check. [internal/bbsql/conn_test.go:79–135](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/conn_test.go#L79) replaces the driver connector with a stub Pinger, so its one-connection assertion never observes this batch.

**Exploit / Failure Scenario:**
1. Preconditions: a real connection successfully authenticates.
2. Open pings it; the driver sends its own unapproved batch.
3. Fake Queryer transcripts still contain only allowlisted statements because they sit above this driver path, leaving the C1/C3 assertion false for production.

**Remediation:** acquire/connect without driver Ping, then run the approved ceiling/control batches under their fixed prefixes and budgets. If a health statement is retained, explicitly approve/tag/prefix it and prove it cannot precede the required rights policy. Add a driver-level wire/transcript test including startup and replacement connections. `select 1` itself does not leak business data or mutate a target; this finding is a mandatory-boundary violation, not SQL injection.

**W8-07 — SQL CLI reason codes do not redact the accompanying message**

**The Flaw:** `answer.fail` selects a fixed reason but emits `err.Error()` into JSON `message` or stderr (`main.go:144–168`). SQL install/remove and foreground service configuration failures use that function. `SQLManager.InstallSQL` constructs error messages with folder paths and underlying errors (`sqlservice.go:105–107,132–134,150–162`); `LoadConfig` can return raw path errors or JSON parser text ([internal/blackbox/config.go:346–356,368–373](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/config.go#L346)). The SQL CLI tests assert reason values, not absence of sensitive injected messages. Real SQL driver errors are separately classified in conn.go; no remote `/status` leak is inferred from this CLI flaw.

**Exploit / Failure Scenario:**
1. Preconditions: SQL configuration is unreadable/invalid or installation hits an OS error. A wrapper captures `--json` or terminal logs for support.
2. The command returns a fixed reason alongside the raw path/error message.
3. Internal path/configuration text leaves the fixed-code boundary in those diagnostic artifacts.

**Remediation:** introduce fixed SQL-command error sentences/codes at the answer boundary; do not serialize wrapped OS/config parser text. Keep any privileged local diagnostics explicitly separate from machine-readable/public answers. Test injected path and configuration-key text across install, remove and SQL foreground startup. Severity is Low because the proven sink is the invoking operator's output, not an unauthenticated endpoint.

**W8-08 — The shared decoder accepts absent/NULL required fields and arbitrary numeric enums**

**The Flaw:** `decodeRow` marshals the provided map and uses `DisallowUnknownFields`; that rejects extra keys but not absent keys or JSON null for scalar destinations. `decodeRows` creates zero-valued structs and only validates wait-type strings. Thus a nil/empty requests row can become `RUNNING` with zero tasks; a row containing `tasks:nil` becomes zero; an AG row missing `synchronization_state` becomes state zero, and a value of 255 fits `uint8` despite not being a valid SQL synchronization state. The NULL behavior is specified by [Go encoding/json/decode.go:94–97](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/.dev/toolchains/go/src/encoding/json/decode.go#L94).

Direct requests and AG callers perform no further required-field validation ([internal/bbsql/requests.go:42–51](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/requests.go#L42); [filestats.go:127–136](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/filestats.go#L127)). [internal/bbsql/schedulers.go:19–51,66–75,149–165](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/schedulers.go#L19) does add NULL checks for its own samplers; that local mitigation must not be generalized to the shared decoder. Current [output tests:23–43,224–249](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/bbsql/output_test.go#L23) exercise unknown keys, negative uint values and complete fabricated rows, not presence/null/enum domains.

**Exploit / Failure Scenario:**
1. Preconditions: an unexpected driver/result-shape fault or later statement/decoder drift omits/nulls a required field, or returns an out-of-domain enum. Normal documented requests rows are not claimed to have NULL task counts, and no ordinary user is assumed able to forge authenticated TLS results.
2. The incomplete row is admitted because every supplied key is known.
3. Zero initialization or a merely width-valid enum is serialized as factual data, violating “unreadable means missing.”

**Remediation:** define exact required column sets and nullability per output type, validate before JSON/scalar conversion, and enforce enum domains. Distinguish requests' explicitly present empty wait type (RUNNING) from a missing/null wait_type. Use nullable/presence-aware internal fields for legitimately nullable DMV columns; emit missing, not invented zero. Add nil-map, empty-map, per-field omission/null, mixed valid/invalid and enum-domain cases. Wave 11 owns sampler-specific delta/null policy; this finding is the shared BBX-5-09 decoder contract.

**W8-09 — The installed config identity changes with working directory**

**The Flaw:** `pick` returns a nonempty supplied `--config` string unchanged (`main.go:215–218`). The CLI loads it relative to its current directory, while `InstallSQL` only rejects blank paths and persists the original string in service arguments (`sqlservice.go:99–100,148`). The service independently opens that argument at startup. Binding the same string is not binding the same file when it is relative. Tests of custom paths exclusively use absolute temporary paths ([cmd/unravis-blackbox/sqlcli_test.go:331–354](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/sqlcli_test.go#L331)).

**Exploit / Failure Scenario:**
1. Preconditions: the owner installs with `--config ./other-blackbox.json` from a staging directory; service startup has a different working directory. No malicious filesystem replacement is needed.
2. Installer validates the staging file and locks its SQL folder.
3. SCM service opens the same relative string against its own directory, finding no configuration or a different one.
4. Startup fails or reads settings other than the validated file. The root folder checker may refuse a different destination, but cannot restore the intended config identity.

**Remediation:** resolve and validate an absolute canonical configuration path before both loading and registration, or copy the validated configuration into a protected installed path with explicit SQL-SID read access. Check ownership/write access for adopted external configurations; do not rely on path text alone. Add a test changing working directory between install and startup and assert the same file is opened. **Unverified Assumption:** the actual target SCM working directory was not inspected; the source-level mismatch applies whenever it differs.

## Cross-Wave Impact

- [Wave 7 report](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/reports/wave-7-adversarial-review.md#L1) owns executable/config ACL access, installation adoption/ownership, backoff persistence, lint and invalid SQL source columns. **`r.wait_time_ms` must be `r.wait_time`; semaphore `timeout_count` must source `timeout_error_count`.** They are not decoder faults. W8-03 is a separate activation-order problem after launch is repaired.
- **Wave 9:** C8 effective permission/role reasoning and classification remain there. W8-02 is the physical-connection integration bypass and still applies if every rights query is corrected. W8-06 is an actual driver-originated extra send, not a fabricated arbitrary-SQL pathway.
- **Wave 10:** context plumbing, hard tick/skip semantics, save failures, live folder checking and terminal/heartbeat status writing require coordination. This report owns conn.go/driver behavior and endpoint truth, not a duplicate runner audit. In particular, no assertion is made that ordinary users can replace a protected SQL folder after a sound fresh install; wave 7 documents the specific preexisting-owner case.
- **Wave 11:** carries row caps/truncation and absence through samplers/deltas. SQL schema reference confirms semaphore IDs are nonunique across pools, cumulative counters are bigint and small-query timeout/forced counters are NULL; review their downstream interpretation independently of W8-08's shared validator.
- **Security boundaries checked, not assumed:** `probe.enabled:true` is still rejected (`config.go:289–291`), and `/status` explicitly constructs `Probe{Enabled:false}` (`server.go:289`). However explicit `install-sql`/`sql-service` reaches [sqlrunner.go:51–74](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/sqlrunner.go#L51) even with that field false. The old probe gate is not a release interlock for this separate service. SQL service mode starts no HTTP server; the host file handler restricts basename and recording-name shape (`server.go:310–329`), excluding ordinary SQL-subdirectory requests. The host remains plain HTTP: this review does not mislabel it as TLS-protected.
- **TLS/auth/local-only:** DSN construction requests encryption and no username/password (`conn.go:158–184`); Windows [driver auth registration:6–18](file:///C:/Users/nachi/go/pkg/mod/github.com/microsoft/go-mssqldb@v1.11.2/auth_windows.go#L6) selects WinSSPI, and [winsspi.go:136–138,153–177](file:///C:/Users/nachi/go/pkg/mod/github.com/microsoft/go-mssqldb@v1.11.2/integratedauth/winsspi/winsspi.go#L136) uses the current identity when user is empty. [msdsn/conn_str.go:208–286](file:///C:/Users/nachi/go/pkg/mod/github.com/microsoft/go-mssqldb@v1.11.2/msdsn/conn_str.go#L208) validates CA/hostname or an exact certificate pin; pin mode intentionally skips ordinary expiry/hostname checks but installs byte comparison, not trust-any. The loopback dialer rejects nonloopback addresses before TDS/auth (`conn.go:228–254`); driver TCP and browser paths use it, including routing reconnects. No remote-auth bypass was demonstrated. The driver resolves hostnames before handing individual addresses to this dialer, so the test seam's whole-DNS-answer rejection is not proof of the complete production resolver path. The wired target is only `(local)`, and `lpc:` support is converted to TCP, not shared memory.
- **Unverified Assumptions / limits:** actual virtual-account login mapping, certificate deployment/read permissions (including external/UNC anchors), TCP enablement/default instance availability, SQL grants/revokes on each supported version, live C3 session options/C5 session count, driver wall-time faults, Windows recovery/ACL/reparse behavior and C13 budgets were not live-tested. No secrets were read or uploaded; public SQL reference retrieval was documentation-only. No commits, permission/config changes or production/test edits were made.

## Go / No-Go Verdict

**NO-GO at the scoped HEAD.** Measurable exit conditions:

1. Resolve wave-7 launch/schema blockers; run a clean installation through explicit owner grant and activation, with no premature denial and no SQL execution of grant/revoke by the app. A denied service must remain off across restart and re-arm only through a tested owner action.
2. Driver-level fault tests demonstrate connect <=5 s, quick query <=3 s, long query <=5 s and whole tick <=5 s within an explicitly small scheduling tolerance, including cancellation cleanup and Stop during active work. No test may substitute a context-cooperative fake for the handshake/drain cases.
3. Instrument physical connection generations: every replacement, including mid-tick expiry/retry, must execute ceiling checks before samples; keep at most one live SQL session and retire it at the intended lifetime. Startup transcripts contain only approved prefixed/tagged SQL, not driver Ping's extra batch.
4. Stop/remove/crash/stale/corrupt status cases cannot indefinitely report SQL on or contradictory values; the public SQL object remains the C11 fields only, and SQL sample files remain inaccessible over HTTP.
5. Realistic TOP-boundary tests expose truncation; malformed/NULL/missing rows never fabricate zeros or RUNNING, and invalid numeric enums are rejected/omitted.
6. SQL CLI failures contain fixed redacted messages; relative configuration installation proves stable file identity across startup.
7. Re-run targeted regression tests and complete the separately owned wave-9/10/11 controls plus authorized live owner verification. The unit-test passes recorded above are not release approval.
