## Executive Risk Summary

**Risk: High. Scope: canonical wave 4, HEAD `33c219b4b270229f44cc2b09ab234b1c6b0c72dc` (`33c219b`). Verdict: No-Go.**

Top three blockers:
1. Failed mapping refresh can assign an owner-selected name to an unrelated process that reused its PID (W4-01).
2. Unrelated process churn repeatedly destroys rate baselines for otherwise stable HTTP, disk/CPU and SQL counters (W4-04).
3. One unavailable SQL counter/instance can remove all SQL host evidence for the recorder lifetime (W4-06).

Read [CLAUDE.md](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/CLAUDE.md#L1), [handoff section 3](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-expansion-handoff.md#L18) and the owned BBX-1-11, BBX-2-01, BBX-2-03 and BBX-2-04 packets. Review used current source and direct callers/tests, not historical implementation reports. Full owned-source scope: [resolve.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve.go#L1), [resolve_map.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_map.go#L1), [resolve_map_windows.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_map_windows.go#L1), [resolve_map_other.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_map_other.go#L1), [http.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/http.go#L1), [diskcpu.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/diskcpu.go#L1), [sqlhost.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/sqlhost.go#L1), [sqlhost_windows.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/sqlhost_windows.go#L1) and [sqlhost_other.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/sqlhost_other.go#L1).

**Evidence boundary:** findings below are source-established conditional failure traces, not claims of observed production incidents. No live counters, services, network targets or SQL connections were exercised. **Unverified Assumption:** deployed HTTP.sys/IIS/SQL object names, instance syntax, counter types/units and non-English Windows behavior match these literals. English addition is implemented, but wildcard expansion uses `PdhExpandWildCardPathW` with the English pattern directly: [pdh_windows.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pdh_windows.go#L77), lines 77–109. Resolver enumeration likewise passes literal `W3SVC_W3WP` (resolve_map_windows.go:65–88); SQL discovery filters literal prefixes (sqlhost.go:212–235). These facts do not establish localized-server success. `RejectedRequests` as `KindPDHRate`, disk latency as `KindGauge`, and the SQL wait-time fields as gauge milliseconds require provider type/unit verification, not another fake value test.

## Findings Table

| ID | Severity | Category | Component/Location | Summary |
|---|---|---|---|---|
| W4-01 | High | Target identity / fail-open cache | resolve_map.go:62–82, 103–114, 121–128 | Last-good PID mappings survive failed enumeration and are rebound to a new creation time. |
| W4-02 | High | Ambiguous target selection | resolve_map.go:97–115; resolve.go:75–82 | First worker/managed child wins without ambiguity or parent-lifetime validation. |
| W4-03 | Medium | Runtime discovery lifecycle | resolve.go:137–174 | A successful pre-runtime module snapshot caches runtime 0 for the entire process lifetime. |
| W4-04 | High | Rate availability | http.go:116–136; diskcpu.go:98–111; sqlhost.go:123–155 | Any process-set change resets every rate, including unrelated stable counters. |
| W4-05 | Medium | Instance parsing / evidence loss | http.go:96–111 | Parenthesized pool names are rejected even though configuration permits them. |
| W4-06 | High | Partial availability / startup lifecycle | sqlhost.go:105–134; pdh_windows.go:48–74; groups.go:59–81 | One source-add failure suppresses the entire multi-instance SQL group permanently. |

## Detailed Findings

**W4-01 — Failed refresh permits cross-process rebinding**

**The Flaw:** `ResolveTargets` advances `mapLastInst`/`mapLastSvc` before attempting enumeration, retaining old lists on error (resolve_map.go:62–73). Those lists contain only PID/name, not the creation time when the mapping was verified. `matchPool` and `matchService` then obtain *today's* Start from `byPID` (103–114, 121–128). A nonzero Start establishes that some process exists, not that the cached mapping belongs to it. The Windows refresh failure correctly returns an error (resolve_map_windows.go:25–32); its caller defeats that fail-closed behavior.

**Exploit / Failure Scenario:**
1. A successful enumeration maps selected `svc:Mail` to PID P, creation A.
2. Mail exits; another process obtains P with creation B. Service enumeration now fails.
3. The inventory contains `(P,B)`, but `mapSvc` still contains `Mail -> P`.
4. `matchService` emits `svc:Mail` with `(P,B)` and its runtime. The unchanged next inventory does not retry the failed enumeration. The unrelated process is now eligible for selected-target detail.

This trace requires PID reuse plus an enumeration failure; it does not require B to have the same image. The frequency of that combination on a real server is **Unverified Assumption**. Existing [resolve_map_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_map_test.go#L146) tests successful reuse (146–169) and initially failing independent enumerators (274–363), not successful mapping followed by reuse and refresh failure.

**Remediation:** Bind each cached mapping to the confirmed process identity and its discovery generation. Invalidate affected mappings when enumeration fails or identity changes; report a fixed unavailable/ambiguous status. Use bounded retry/backoff independent of unrelated process churn. Add the last-good/reuse/failure regression for both services and pools.

**W4-02 — Enumeration order substitutes for ownership evidence**

**The Flaw:** `matchPool` returns from the first matching instance, then the first runtime-2 child with nonzero Start (resolve_map.go:97–115). `dotnetChildren` checks only `ParentPID` and runtime (resolve.go:75–82), never whether the child was created after the currently resolved parent. Multiple worker or child candidates are neither represented nor marked ambiguous.

**Exploit / Failure Scenario:**
1. A selected pool has two live workers during overlap/web-garden operation, or a runtime-0 worker has two runtime-2 children.
2. Both candidates satisfy the current checks. The first entry wins; reversing inventory/instance order changes which application's process counters are recorded.
3. The other candidate's resource consumption is silently absent while the selected key remains present.
4. Separately, if an orphan child retains an old ParentPID that is subsequently reused, a child older than the new parent is accepted by the same code.

The first-match behavior is certain for such inputs; whether a specific IIS hosting layout supplies multiple candidates, and Windows orphan-parent behavior in that layout, are **Unverified Assumption**, not demonstrated here. [resolve_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_test.go#L73), lines 73–90, even supplies two managed children to the helper, while mapping tests use a single child.

**Remediation:** Preserve worker identity separately from application-child identity. Resolve all legitimate selected workers or explicitly mark ambiguity; never silently choose the first. Validate child creation against parent creation and require an unambiguous supported hosting relationship without command-line capture. Test overlap, multiple managed children, reversed enumeration order and an older orphan child.

**W4-03 — Startup timing can permanently hide runtime-specific evidence**

**The Flaw:** A successful module read containing no recognized runtime produces 0 and is cached at resolve.go:161–170. Later inventory calls reuse that entry even when unrelated processes change (137–145, 159–160). Only a new identity forces another successful module read. The code distinguishes failed reads from successful unknown classification, but neither path guarantees timely runtime detection on a stable host.

**Exploit / Failure Scenario:**
1. A selected process starts; the resolver snapshots modules before `clr.dll`/`coreclr.dll` loads.
2. The module call succeeds with only native modules, so runtime 0 enters the cache.
3. The process loads its runtime without restarting. Subsequent calls retain 0.
4. Framework/proc10 collectors filter it out indefinitely; out-of-process child detection can also miss a child cached before runtime startup.

The cached-zero transition is source-established; observing that startup window on the deployed application is **Unverified Assumption**. Existing cache tests prove reuse of already classified modules, not late module loading.

**Remediation:** Recheck unknown runtimes with a bounded schedule, scoped preferentially to selected targets and candidate children. Cache positive classification separately from retryable unknown/error state. Add a same-PID/same-Start native-to-managed module transition test.

**W4-04 — Busy hosts can have no usable rates despite valid counters**

**The Flaw:** HTTP refresh always makes a fresh engine (http.go:78–92, 116–123); disk/CPU does so at diskcpu.go:101–105; SQL rebuild replaces both source and engine (sqlhost.go:123–134, 141–149). Their shared change signal tracks every process, not their counter-instance identity: [host.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/host.go#L94), lines 94–101 and 144–146. A new [engine.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/engine.go#L91) emits no rate for its first two valid samples (91–112).

**Exploit / Failure Scenario:**
1. HTTP queues, disks and SQL instances stay unchanged and return valid readings.
2. An unrelated scheduled job appears/disappears between collections, making the process-set flag true.
3. Each group discards all baseline history and records its first baseline again.
4. If a reset recurs before a third valid sample, rates never appear. Gauges can remain visible, masking the lost rate coverage. Even a single change creates another baseline gap.

No PDH timing assumption is needed for this engine-state trace. [http_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/http_test.go#L194), [diskcpu_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/diskcpu_test.go#L159) and [sqlhost_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/sqlhost_test.go#L150) do not combine persistent churn with stable rate paths.

**Remediation:** Reconcile paths and preserve source handles/history for unchanged counter identities. Reset only newly bound or demonstrably recycled instances; compare SQL prefix sets before rebuilding. Add continuous unrelated-churn tests requiring stable rates to continue after initial warmup.

**W4-05 — HTTP queue names containing parentheses disappear**

**The Flaw:** `parseHTTPPath` splits at the first `)` (http.go:101–105), not the terminating `)\` delimiter. [config.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/config.go#L271), lines 271–282, does not prohibit parentheses.

**Exploit / Failure Scenario:**
1. A counter path is `\HTTP Service Request Queues(MyApp (Staging))\CurrentQueueSize`.
2. The first `)` is inside the instance; the next character is another `)`, not a backslash.
3. The parser rejects the path, so it receives no engine spec and no queue row, even with a valid numeric reading.

This is a deterministic string-parser failure. Whether a particular provider exposes that exact legal pool label unchanged remains **Unverified Assumption**; it is not a claim that every deployment has such a pool.

**Remediation:** Parse the actual instance terminator or use structured PDH path parsing. Add parenthesized and underscore/space instance tests, mirroring the corrected parsers in later waves.

**W4-06 — A single SQL counter failure becomes lifetime-wide group loss**

**The Flaw:** `buildSQLHostTargets` combines every discovered instance and all configured counters into one source (sqlhost.go:74–85, 123–128). `NewPDHSource` closes the entire query on any expansion/add error (pdh_windows.go:56–70). [groups.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/groups.go#L59), lines 59–81, drops a failing factory, and `RunHost` builds groups only once (host.go:107–109). A group absent at startup cannot later execute its rediscovery code. This also affects disk/HTTP and dependent-wave factories using the same constructor.

**Exploit / Failure Scenario:**
1. Discovery returns default and named SQL instances, but adding one configured counter returns an error (unsupported counter, transient provider failure or instance disappearance).
2. All already added healthy-instance counters are closed and the factory fails.
3. The group is absent from the collector; no per-tick `Missing` entry or retry is produced for it.
4. Provider recovery or a later process-set change cannot restore SQL host evidence until another recorder construction.

The error-handling chain is verified. Which installed provider/version returns the triggering error is **Unverified Assumption**. Existing SQL tests inject already-created sources and do not cover partial constructor failure.

**Remediation:** Preserve readable counters/instances, expose fixed availability diagnostics and retain a dormant, bounded-retry group on transient initialization failures. Avoid retry storms and keep retry/discovery inside the footprint budget. Test one bad counter among healthy default/named instances and recovery without recorder restart.

## Cross-Wave Impact

- W4-01/W4-02 contaminate or omit wave-5 process detail and wave-6 selected-process inclusion. Carry both worker and application identities rather than repairing IIS with pool-name-only matching.
- W4-04 also applies to wave-5 CLR/proc10/procIO refreshes and wave-6 TCP; see their companion reviews. W4-06 is a shared-source/registry dependency, not a SQL-login issue.
- Footprint is not established by the existing gate: [budget_bench_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/budget_bench_test.go#L208), lines 208–248, constructs substitute groups and disables host process collection. Actual discovery/wildcard fanout is not exercised. `runGroup` times work only after return (groups.go:182–196), so it does not enforce a one-second deadline on native enumeration. These foundation dependencies require coordinated fixes, not invented live measurements.
- No query/request text, command line, payload or SQL connection was found in the owned implementations. Group errors are reduced to group names by groups.go:192–194; this is not evidence of text spilling through these paths. Native provider behavior and total resident memory remain unverified.
- BBX-3-04 EventPipe, BBX-6-01 steal and BBX-6-03 allocation sampling are blocked/not built; their absence is not a finding and none repairs the above defects.

## Go / No-Go Verdict

**No-Go** for enabling this wave as reliable production evidence.

Measurable release conditions:
- Reuse-plus-refresh-error tests emit no target for the unverified mapping; ambiguous workers/children never silently pick a candidate; late runtime loading is detected within a documented bounded retry interval.
- Stable counters keep rate output after initial warmup under unrelated process churn; parenthesized HTTP instances survive parsing.
- Healthy SQL counters remain available with one counter/instance unavailable, and recovery occurs without restarting the recorder; absence has a fixed diagnostic.
- Record real-server object/type/unit and English/non-English results, including default/named SQL and supported IIS hosting layouts. Verify HTTP rejection semantics, disk average latency baseline requirements and SQL wait-time meanings against provider metadata/perfmon.
- Measure the actual registered groups, selected-target resolution and enumeration churn: under 0.5% of one core on average, under 50 MB working set, and a bounded one-second group policy that does not depend on a slow native call returning first.

**Validation performed:** offline `go test ./internal/blackbox` with the bounded wave-specific selector (resolver, HTTP, diskCPU, SQLHost, IIS, CLR, proc10, procIO, events, TCP, ASP.NET, memory pressure, top-process and Step-9 fake tests), `-count=1 -timeout=60s`: PASS (`ok`, 0.822s). Separate fake-budget and fake-W3WP-refresh selection, `-count=1 -timeout=30s`: PASS (`ok`, 0.552s). `go vet ./internal/blackbox` for Windows and with `GOOS=linux`: PASS. Used the supplied absolute Go binary with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`. These are package results, not asserted test counts or real-server measurements. No new regression tests were written/executed for the static traces above; no full application build/full suite/live validation was run. Only the three authorized review reports were written.
