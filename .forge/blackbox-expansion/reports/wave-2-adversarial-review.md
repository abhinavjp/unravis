## Executive Risk Summary

**Risk: High.** Canonical wave 2, BBX-1-05/06/10, reviewed in the main repository at **HEAD 33c219b4b270229f44cc2b09ab234b1c6b0c72dc**. Scope: group registry/execution, Windows PDH source and non-Windows twin, app-server wizard-target endpoint, real discovery dependencies and the complete authentication/route path. Full scoped files and related tests were read; later group implementations were read only as immediate callers, not as separately reviewed waves.

Own packets read: [registry](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-1-05-group-registry.md#L1), [PDH](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-1-06-pdh-source.md#L1), [wizard endpoint](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-1-10-wizard-endpoint.md#L1). Assessment follows [handoff safety rules](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-expansion-handoff.md#L18), not assertions in existing gate reports.

Top three blockers:
1. W2-01: the one-second group budget cannot interrupt collection; a delayed provider delays the entire recorder and shutdown.
2. W2-02: PDH formats percentages with its default 100 cap, before the supposedly uncapped engine sees them.
3. W2-04: native wildcard allocation and handle creation have no cardinality/byte cap, including at startup when no targets are selected.

W2-03 and W2-05 additionally block reliable collection on localized or initially unavailable providers. **Unverified Assumption:** real Windows providers meet the latency/footprint limits and all required paths exist. No live provider, target or service was exercised.

Security trace: [internal/server/httplog.go:274-294](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/server/httplog.go#L274) calls serve; [internal/server/server.go:195-229](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/server/server.go#L195) applies host/origin restrictions ([security.go:9-74](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/server/security.go#L9) permits loopback Host and absent or same-host HTTP Origin) and implements constant-time token comparison; [internal/server/routes.go:428-463](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/server/routes.go#L428) checks authorization before dispatch. The [targets route](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/server/blackbox.go#L138) is non-public, exact-path, GET-only (lines 138-156). No token bypass was found in this trace. The handler neither selects targets nor persists settings and invokes no elevation. Successful response bodies are not logged by the traced middleware. This finding is limited to the reviewed source path, not a live penetration-test result.

## Findings Table

| ID | Severity | Category | Component/Location | Summary |
|---|---|---|---|---|
| W2-01 | High | Architecture / availability | [internal/blackbox/groups.go:182-196](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/groups.go#L182) | GroupTiming runs only after synchronous Collect returns; it is not a timeout. |
| W2-02 | High | Logic / measurement integrity | [internal/blackbox/pdh_windows.go:135-150](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pdh_windows.go#L135) | Missing PDH_FMT_NOCAP100 truncates multi-core percentages upstream of Engine. |
| W2-03 | Medium | Portability / API contract | pdh_windows.go:56-70,92-109 | English wildcard paths are expanded without the documented localization step. |
| W2-04 | High | Performance / resource exhaustion | pdh_windows.go:66-73,98-118,155-179 | Provider-sized buffers and every expanded instance become retained state/handles without a limit. |
| W2-05 | High | Lifecycle / missing evidence | groups.go:59-81,151-197 | Factory failures permanently remove groups; nil successful results do not enter Missing. |
| W2-06 | Medium | Logic / discovery availability | internal/server/blackbox.go:521-534 | No readable IIS config suppresses service discovery, and a failed service list discards readable pools. |
| W2-07 | Medium | Logic / misleading state | internal/server/blackbox.go:537-565 | Every configured app pool is reported running without reading its runtime state. |

## Detailed Findings

### W2-01 — Retrospective timing cannot protect the recorder

**The Flaw:** runGroup calls g.Collect synchronously, then charges elapsed time. Group has no cancellation/deadline argument. [recorder.go:149-169,186-207](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/recorder.go#L149) runs Tick inline with the same loop that observes shutdown; housekeeping only runs on entering a subsequent tick. PDH collection and expansion are synchronous native calls (pdh_windows.go:99-107,132-139). The inner host collector is also outside the per-group timing at groups.go:152. No new worker goroutine leak was found here: the problem is the single blocked owner goroutine.

**Exploit / Failure Scenario:**
1. A group/provider call takes two seconds instead of one, or does not return while the provider is stalled.
2. The collector remains at groups.go:190. The current sample, later groups, log catalogue and the next housekeeping pass cannot proceed; no slow mark exists yet.
3. Cancel the recorder context during the call. Run cannot reach its select until Tick returns. [cmd/unravis-blackbox/service_windows.go:92-96](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/service_windows.go#L92) also waits for that recorder after requesting Stop.
4. If the call finally returns, only a later cycle skips it. Its over-budget result can still be applied in the current cycle.

The control-flow failure is confirmed. An actual stuck PDH provider was not induced. **Unverified Assumption:** every native call always returns quickly enough that post-call timing is equivalent to enforcing a deadline. [groups_test.go:147-179](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/groups_test.go#L147) injects an already-existing slow mark; it does not prove timeout or housekeeping liveness.

**Remediation:** Define an enforceable execution boundary: cancellation for operations that support it and bounded isolation for blocking native work. Keep housekeeping/shutdown independent of provider completion. Do not create an unbounded new goroutine each timeout or close a live native query concurrently; allow at most one outstanding owner per source, quarantine timed-out work, and use process isolation if hard interruption is required. Add a fake blocked-call test proving housekeeping and shutdown progress within an explicit bound and repeated timeouts do not grow outstanding workers/handles.

### W2-02 — Native formatting defeats the uncapped-CPU rule

**The Flaw:** Collect passes only pdhFmtDouble. Its declaration is [extras_windows.go:34-37](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/extras_windows.go#L34), value 0x200, with no NOCAP100 bit. [Microsoft's formatting contract](https://learn.microsoft.com/en-us/windows/win32/api/pdh/nf-pdh-pdhgetformattedcountervalue) states that PDH_FMT_NOCAP100 is required to avoid the default percentage cap. This matters to the immediate caller [proc10.go:12-16,145-156](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/proc10.go#L12), which reads per-process percent CPU.

**Exploit / Failure Scenario:**
1. An owner-selected runtime-2 process consumes more than one core, e.g. 350% of one core.
2. After successful native sampling and baseline warm-up, PDH formats its percentage with the default cap, returning 100 rather than 350. This scenario assumes successful data statuses; W1-01 is an independent earlier obstruction.
3. Engine forwards the already-capped value unchanged. A fake that directly injects 350 never crosses the native formatter and cannot detect this bug.

**Remediation:** Use PDH_FMT_DOUBLE | PDH_FMT_NOCAP100 at the formatting boundary. Add a fake native-call seam asserting the actual flag bits, then an owner-authorized multi-core live comparison against an independent measurement. **Unverified Assumption:** passing the existing engine-only 350% test proves the Windows path is uncapped; it does not.

### W2-03 — English wildcard expansion is not language-neutral

**The Flaw:** NewPDHSource sends the English wildcard directly to PdhExpandWildCardPathW and only then calls AddEnglishCounter. The [documented wildcard procedure](https://learn.microsoft.com/en-us/windows/win32/api/pdh/nf-pdh-pdhaddenglishcounterw) is to add the English wildcard, obtain its localized full path with GetCounterInfo, expand that localized pattern, and add the resulting localized paths. The implementation omits the translation boundary entirely.

**Exploit / Failure Scenario:**
1. Run on a Windows installation where the required counter object/counter names are localized rather than English.
2. Construct a group with an English wildcard, such as the Process or PhysicalDisk patterns.
3. Expansion is attempted before any English-to-localized conversion. A failed expansion aborts the source; BuildGroups silently excludes that group. Successful direct English counters do not validate this wildcard path.

This is a source/API-contract incompatibility; a localized server was not tested. **Unverified Assumption:** every supported deployment exposes English object names to the wildcard expander.

**Remediation:** Implement the documented language-neutral-to-localized wildcard procedure, or a supported equivalent, while retaining a separate stable English logical identifier for Engine specs. Do not simply pass localized expanded paths into English-prefix parsers. Test translated names through a fake native seam and validate on one supported non-English Windows image.

### W2-04 — Target count does not bound native work or retained memory

**The Flaw:** expandWildCard allocates make([]uint16, n) using the provider's size and materializes every path. The constructor and RefreshInstances create a handle for every expanded path; Paths and Collect allocate proportional copies. No maximum bytes, counters, instances or total handles is enforced. Refresh removes/re-adds all wildcard handles rather than a bounded delta. [host.go:89-109](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/host.go#L89) builds all registered groups before the recorder budget starts; optional groups retain their sources when skipped.

Immediate callers prove that this is not a merely hypothetical public API: proc10.go:32-42 creates four Process wildcards before checking selection in Collect; [procio.go:17-26,53-64](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/procio.go#L17) creates six, likewise before selection. Filtering emitted rows does not constrain these native resources.

**Exploit / Failure Scenario:**
1. Start the recorder on a process-dense host, even with an empty targets list.
2. If expansion succeeds, those two groups alone allocate roughly ten counters per Process instance, plus paths/specs and other groups' sources.
3. Increase observed process cardinality or churn. Allocation/handle work scales with the whole inventory; refresh repeats it. A later optional skip does not free already-retained query state.
4. If memory remains over the threshold after widening to 60 seconds, the budget has no mechanism to reclaim these sources. No specific host count or measured 50 MiB breach is claimed here.

**Unverified Assumption:** provider-sized expansion and retained native state remain below 50 MiB and one second for every supported host. The fixed fake workload is not evidence for that claim.

**Remediation:** Bound expansion bytes before allocation and bound aggregate handles/instances before addition; report truncation/unavailability explicitly. Avoid creating per-target sources for no selected targets; limit detail acquisition to selected identities where the API permits. Reconcile handles incrementally and reclaim optional source state when necessary. Add fake large-cardinality/oversized-size/partial-refresh-failure tests with exact caps, and measure process working set plus native handle count on the approved live host.

### W2-05 — Unavailable groups vanish permanently and without a reason

**The Flaw:** BuildGroups is called once at startup and drops any factory error. It retains no failed registration to retry or report. For a built group, runGroup only adds Missing on error/panic; nil with nil error is silently passed to applyGroup. The actual [http.go:126-139](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/http.go#L126) and [iis.go:147-164](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/iis.go#L147) return nil when no valid fields exist, despite comments claiming Missing will be recorded. Slow skips also replace the name with the shared “skipped: slow” token (groups.go:161-176), losing which group was slow.

**Exploit / Failure Scenario:**
1. A required wildcard expansion fails during startup, e.g. its provider/instances are not available yet. NewPDHSource returns an error.
2. BuildGroups excludes the group. The provider later becomes available, but no instance of that group remains to receive the process-change signal or retry; collection stays absent until recorder restart.
3. Alternatively, a successfully built group returns no valid readings. Its Sample field is absent but so is its Missing entry. Consumers cannot distinguish unavailable/slow detail from intentional non-selection using the intended metadata.

**Remediation:** Keep bounded lifecycle state for registered groups, with fixed failure codes and bounded retry/backoff for temporary construction failures. Distinguish not-selected, warming-up, unavailable and skipped-slow outcomes explicitly, always retaining the group identifier. Handle typed nils as well as nil interface results. Add factory-fail-then-recover and all-invalid-result tests through the actual group collector, not only individual groups. Do not log raw provider error text.

### W2-06 — Independent discovery sources are incorrectly coupled

**The Flaw:** detectTargets returns immediately when bbAppPools fails; service enumeration is not attempted. A failed service enumeration similarly returns nil for both lists. This contradicts the packet's goal of offering whatever can be read. The real [discover.NewSystemEnv](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/discover/winenv_windows.go#L24) wires independent filesystem and service capabilities; the coupling is in this endpoint, not required by Windows.

**Exploit / Failure Scenario:**
1. Use a Windows service-only host, or an account unable to read applicationHost.config, while service enumeration is allowed.
2. Request targets with a valid application token.
3. bbAppPools returns false and detectTargets never calls bbRunningServices. The response is detected=false with two empty lists, even though services were discoverable. The UI falls back to typing everything.

**Remediation:** Collect each source independently. Preserve successful results and return per-source detection/unavailable state, including truncation where applicable. Add tests for readable-services/unreadable-IIS and readable-IIS/denied-services at the orchestrating function level. The existing [blackbox_test.go:1333-1372](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/server/blackbox_test.go#L1333) tests helpers separately, not partial success of detectTargets.

### W2-07 — Configured is not running

**The Flaw:** bbAppPools unconditionally sets Running:true from each parsed pool declaration. [configparse/apphost.go:23-28,133-150](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/discover/configparse/apphost.go#L23) extracts name/runtime configuration, not operational status. No worker/pool-state lookup occurs in this path.

**Exploit / Failure Scenario:**
1. A configured pool is stopped or has no active worker; its declaration remains in applicationHost.config.
2. With both discovery sources readable, GET targets returns that pool with running=true.
3. [BlackboxSettings.svelte:82-92](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/web/src/components/BlackboxSettings.svelte#L82) only shows “not running” for false, so the actual stopped target receives no warning.

**Remediation:** Separate “configured/offered” from measured running state. Obtain running state through a read-only operational source, or represent it as unknown when unavailable; do not guess true or false from configuration. Add a configured-but-stopped test against the complete endpoint-to-picker contract.

## Cross-Wave Impact

- Canonical wave 1 owns W1-01 (NEW_DATA rejected at both boundaries) and W1-02 (baseline starvation after wholesale refresh). They are dependencies, not duplicated findings here.
- Canonical wave 3 owns the transient change signal lost while a group is skipped, the runtime cache, the incomplete footprint gate and the actual Apply persistence failure. Fixing a group's local Collect does not close those integration gaps.
- The real endpoint is authenticated and this-PC-only; it is not the Blackbox service status API. Its response cap (256 entries/list and 256 KiB) bounds output, not all work: the traced discovery service enumerator visits its full service list before projection/capping. **Unverified Assumption:** repeated authenticated discovery requests have acceptable cost; no request-load test was run, and no unauthenticated DoS is asserted.
- [pdh_other.go:15-20](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pdh_other.go#L15) reports Windows-only errors rather than fake readings. That source behavior is appropriate; silently hiding its construction failure is W2-05.
- Verification: scoped files, source twins, related tests and direct callers read in full; the three Microsoft PDH API/status pages were fetched read-only. No Go build/test/vet, native calls, live service checks or load tests were run. The session's eight passing in-memory web target tests do not exercise native formatting, discovery or authentication. Existing live PDH tests are explicitly skipped at [pdh_windows_test.go:85-110](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pdh_windows_test.go#L85).

## Go / No-Go Verdict

**NO-GO.** Measurable exit criteria:
1. A blocked fake provider cannot stop housekeeping/shutdown; demonstrate an explicit deadline and a fixed maximum outstanding worker/handle count over 100 timeout cycles.
2. Native formatting passes NOCAP100, and coordinated W1-01 tests accept both successful PDH statuses. A live selected process above 100% must retain that value within the declared measurement tolerance.
3. Expansion has enforced byte/instance/handle caps and a deterministic over-cap outcome; no-target startup avoids unnecessary per-target sources. Confirm working set below 50 MiB and average CPU below 0.5% of one core for at least ten minutes at supported maximum cardinality.
4. English and one supported non-English Windows deployment both collect the required counters; unchanged identities preserve baselines during refresh.
5. Temporary construction failure recovers without recorder restart; unavailable and slow groups have named missing-state evidence.
6. Partial discovery preserves readable lists, and configured-but-stopped pools are not reported running. Until the owner-authorized live checks exist, the corresponding deployment-safety claims remain **Unverified Assumption**.
