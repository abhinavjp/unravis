## Executive Risk Summary

**Risk: High. Scope: canonical wave 5, HEAD `33c219b4b270229f44cc2b09ab234b1c6b0c72dc` (`33c219b`). Verdict: No-Go.**

Top three blockers:
1. CLR/proc10 counters can be assigned to the wrong process generation; IIS does not validate the worker generation at all (W5-01/W5-02).
2. Selected-only detail still collects and retains counter state for unselected processes; repeated whole-machine target resolution adds unmeasured work (W5-03).
3. Stable per-process rate histories are erased by unrelated process churn, inherited from the same refresh pattern documented as W4-04 (Cross-Wave Impact).

Read [CLAUDE.md](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/CLAUDE.md#L1), [handoff rules](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-expansion-handoff.md#L18) and owned BBX-2-02, BBX-3-01, BBX-3-02 and BBX-3-03 packets. Full current-source reads covered [iis.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/iis.go#L1), [clr.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/clr.go#L1), [proc10.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/proc10.go#L1), [procio.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/procio.go#L1), their direct dependencies/callers and their fake-reader tests. Historical reports and alternate worktrees were not used as proof.

**Evidence boundary:** these are source-established conditional traces, not observed live-server failures. **Unverified Assumption:** deployed W3SVC/WAS counter names, WAS latency units, CLR instance relationships across objects, runtime hosting layouts and localized PDH behavior match the hardcoded paths. `PdhAddEnglishCounter` is used, but [pdh_windows.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pdh_windows.go#L77), lines 77–109, expands English wildcard strings through `PdhExpandWildCardPathW` without an explicit localization step. No non-English server was tested. Fake values above 100 prove only mapping behavior: the native formatter uses `pdhFmtDouble` alone (pdh_windows.go:138), so native uncapped percentage behavior is also **Unverified Assumption** here.

BBX-3-04 EventPipe and BBX-6-03 allocation sampling remain blocked/not built. This review assesses the shipped Process-counter fallback; absence of EventPipe heap/allocation evidence is not a missing-feature defect.

## Findings Table

| ID | Severity | Category | Component/Location | Summary |
|---|---|---|---|---|
| W5-01 | High | Process identity / sample consistency | clr.go:144–190; proc10.go:107–170 | PID joins do not bind readings to creation time, and targets are resolved again after collection. |
| W5-02 | High | Worker identity | iis.go:134–177, 205–217 | Pool-name membership and an unrelated nonzero Start authorize any matching instance PID. |
| W5-03 | High | Selection boundary / resource footprint | clr.go:16–26, 67–77; proc10.go:12–16, 32–42; procio.go:18–25, 53–63; host.go:97–101 | Unselected detail is collected before filtering; target callbacks repeatedly scan the entire process set. |
| W5-04 | High | Approximate identity / stale timestamps | procio.go:185–209; groups.go:151–174 | A three-second creation-time tolerance accepts different generations and rejects valid delayed samples. |

## Detailed Findings

**W5-01 — Nonzero creation time is not a creation-time join**

**The Flaw:** CLR joins `instOf[t.PID]` and checks only runtime (clr.go:168–190); it never consults Start. Proc10 checks `t.Start != 0`, but still joins solely by PID (proc10.go:145–170). Neither source reads a creation-time discriminator. Both call `Targets()` before sampling and again afterward (CLR:145, 182; proc10:108, 145). The production callback is not a snapshot: [host.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/host.go#L97), lines 97–101, runs a fresh `ResolveTargets` on each invocation. Duplicate PID mappings also use first-wins rules, not ambiguity rejection (clr.go:168–171; proc10.go:137–142).

**Exploit / Failure Scenario:**
1. Before collection, the selected process is `(PID P, Start A)`. Its PDH instance returns valid heap/memory/CPU data.
2. After `src.Collect`, the process exits and a selected replacement receives P with Start B.
3. The later `Targets()` call returns `(P,B)`. The previously captured data joins by P and is emitted under the current selected key, despite belonging to A.
4. Conversely, a stale target plus a newly rebound PDH PID can attach a different process's data. Having any nonzero Start does not detect either mismatch.

The absence of a generation comparison is proven by the source. Actual same-tick PID reuse/provider rebinding on Windows is **Unverified Assumption**; this review did not provoke it. Output rows do not preserve PID/Start for downstream validation: [groups_types.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/groups_types.go#L75), lines 75–97. [clr_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/clr_test.go#L73), lines 73–103, and [proc10_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/proc10_test.go#L72), lines 72–109, keep target identities fixed; proc10's zero-Start test does not test two different nonzero starts.

**Remediation:** Capture one immutable target/inventory generation per cycle. Bind instance mappings and rate histories to exact process creation identity, and revalidate identity around collection without selecting a different generation afterward. Omit ambiguous mappings rather than choosing the first. Add fake transitions during `Collect` and duplicate-PID mappings; require no cross-generation emission and fresh baselines for the new generation.

**W5-02 — IIS records worker PIDs it never identity-checks**

**The Flaw:** `selectedPools` only verifies that a selected target has some nonzero Start (iis.go:134–144). `matchIISTarget` then checks pool name alone (205–217); `Collect` writes the PID parsed from the counter path (171–177). That PID need not equal the resolved PID, be in the inventory, or have a verified Start. Retargeting to an out-of-process child explains why target PID equality is insufficient; it does not establish the identity of the parent worker. `IISWorker` also omits creation time (groups_types.go:21–27).

**Exploit / Failure Scenario:**
1. The resolver returns selected pool A with application-child PID C and a nonzero Start.
2. An expanded/readable counter instance reports `P_A`. No relationship between P and C is presented to this group.
3. `matchIISTarget` accepts the name and records P's metrics. A stale/reused worker PID, old generation or unrelated supplied instance is indistinguishable from a verified current worker.
4. During a recycle, downstream records cannot distinguish two generations of the same worker PID because no worker creation time is recorded.

The acceptance of mismatched identities is demonstrated by the existing test contract itself: [iis_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/iis_test.go#L108), lines 108–141, supplies target PID 999 and expects workers 100 and 200 without inventory validation. Multiple legitimate pool workers are not themselves a bug; emitting them without identity proof is. A live provider returning stale-but-valid readings is **Unverified Assumption**, not a claimed observation.

**Remediation:** Extend resolution to retain verified worker `(PID, creation)` identities separately from application-child identities. Match each counter instance to that worker inventory and selected pool, including legitimate overlapping workers, and carry generation metadata. Test wrong PID, stale worker generation, overlap, out-of-process hosting and a disappeared parent with a surviving child.

**W5-03 — Filtering output does not make collection selected-only or bounded**

**The Flaw:** Factories create wildcard detail sources for all workers/processes (iis.go:16–19, 55–65; clr.go:16–26, 67–77; proc10.go:12–16, 32–42; procio.go:18–25, 53–63). Native `NewPDHSource` expands/adds every path and primes the query before any target check (pdh_windows.go:48–74). Once any target exists, each source formats all counters and the engine processes them before selection (CLR:153–190; proc10:116–170; procIO:146–175). Thus unselected processes' numeric detail enters memory and rate history, even though ordinary unmatched rows are not serialized. CLR/proc10 even do that when the selected targets are exclusively the other runtime.

The target callback amplifies cost: `ResolveTargets` calls `Inventory` every time, [resolve_map.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_map.go#L41), lines 41–55. [resolve.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve.go#L127), lines 127–145, still snapshots on cache hits. [resolve_proc_windows.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_proc_windows.go#L21), lines 21–64, opens every enumerated PID to read its creation time. Repeated guards and post-collection target callbacks therefore repeat whole-machine work; the change gate does not cache these inventories.

**Exploit / Failure Scenario:**
1. The owner selects one small service on a host with many unrelated processes. Alternatively, no target is selected at startup.
2. Startup still creates/primes the full wildcard detail queries; the empty-selection guard runs only in `Collect`.
3. With a selection, unrelated process detail is sampled and retained before output filtering, and each target callback repeats the inventory scan.
4. Process churn triggers further wildcard recreation and module discovery. Work/retained state scale with the host process population, not the selected set; merely skipping future optional collects leaves the created sources/history allocated.

The broad read/retention and repeated scans are source facts. Exceeding 0.5% CPU, 50 MB or one second on a particular server is **Unverified Assumption**, not measured here. The existing no-target tests use injected sources and only assert that `Collect` is not called; they do not exercise native factory priming (clr_test.go:319–350; proc10_test.go:302–332; [procio_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/procio_test.go#L367):367–400).

**Remediation:** Keep discovery minimal and separate from detail: use necessary mapping counters/inventory, then bind numeric detail only to verified selected instances and runtime-eligible targets. Do not create full detail queries when no eligible target exists. Share one cycle inventory/selection snapshot, bound expansion/cardinality and release unused sources. Add source-spy tests for which instance paths are added/read, not only which rows are emitted; measure the complete production path at a stated maximum host population.

**W5-04 — ProcIO's three-second approximation is neither exact identity nor a reliable sample clock**

**The Flaw:** `match` estimates creation as `now.Unix() - int64(elapsed)` and accepts differences within ±3 seconds (procio.go:31–33, 195–209). `now` is the recorder's earlier tick timestamp, not the PDH collection timestamp. [groups.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/groups.go#L151), lines 151–174, runs the host and groups serially with the same `now`; timing is recorded only after each group returns (182–196). [recorder.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/recorder.go#L186), lines 186–207, can also perform housekeeping before collection.

**Exploit / Failure Scenario:**
1. Identity case: the target has PID P, Start 900; readings at tick 1000 have PID P and elapsed 98, implying creation 902.
2. `d = 1000 - 98 - 900 = 2`; the different creation is accepted. A near-time PID reuse can therefore cross the supposed identity boundary.
3. Delay case: a valid process started at 900, tick time is 1000, but earlier serial work delays the source read to 1005. Its elapsed value is 105.
4. `d = -5`; the valid target is omitted. Several individually sub-second earlier operations can exceed the three-second accumulated allowance, so this need not require the ProcIO group itself to run over budget.

These are arithmetic traces, not executed new tests. The availability/precision of live `Elapsed Time` and the occurrence of near-time PID reuse are **Unverified Assumption**. Existing `TestProcIOStartMismatch` (procio_test.go:272–288) checks a large mismatch; its aging helper advances exactly with the supplied tick clock (48–81), hiding scheduling skew.

**Remediation:** Validate identity with a precise creation-time value tied to the actual process/collection, not an arbitrary age tolerance. Carry an actual collection timestamp for age metrics separately from tick time. Add near-boundary mismatches and delayed-source tests; require exact generation rejection without losing a valid process merely because earlier groups ran slowly.

## Cross-Wave Impact

- The resolver's stale-map and first-child problems (W4-01/W4-02), plus cached runtime 0 (W4-03), feed these groups before their local checks. Fixing only downstream PID joins is insufficient. These findings are detailed in the companion wave-4 report, not asserted from historical summaries.
- **Rate starvation dependency (W4-04):** CLR rebuild creates a fresh engine at clr.go:94–107 and refreshes on every shared process-set change at 148–151; proc10 does so at 83–103, 111–114; procIO at 105–133, 141–144. [engine.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/engine.go#L91), lines 91–112, needs two valid baseline samples before emission. Repeated unrelated churn can prevent all these rates indefinitely. Changing an instance's actual identity must still reset that instance; preserving history by path alone would be unsafe.
- A missing CLR/WAS object/counter at construction can cause lifetime omission through the all-or-nothing PDH factory and one-shot registry (W4-06; pdh_windows.go:56–70 and groups.go:74–80). This must be distinguished from a legitimately unsupported runtime.
- The footprint gate is not a measurement of this fanout: [budget_bench_test.go](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/budget_bench_test.go#L208), lines 208–248, uses substitute groups and no host process collection. The one-second timer does not interrupt native calls (groups.go:189–196). No production footprint count or live overhead is claimed.
- No request/query/command-line text or target memory payload is read by the owned collectors. Normal field failures remain nil, not zero. CLR does emit a target-label-only row when Process ID is valid but every metric is absent (clr.go:186–190); that is not proof of successful CLR measurements and must not be treated as coverage. Group errors are reduced to fixed group names by groups.go:192–194; no error-text spill was established in these paths.
- Wave-6 busiest-process inclusion receives the same resolver selection and can list the wrong process or omit the intended worker when the resolver returns only a child. The legitimate top-CPU/top-memory machine-wide exception does not authorize these full-detail wildcard reads.

## Go / No-Go Verdict

**No-Go** until identity, selection-boundary and rate-continuity failures are resolved.

Measurable release conditions:
- Same-PID/different-creation and mid-collection target changes produce no misattributed row; ambiguous mappings fail closed. IIS records only independently verified worker generations, including valid overlapping workers and out-of-process parent/child layouts.
- ProcIO rejects a different creation even within three seconds, while delayed reads of the same generation remain correctly identified.
- Source-level tests show zero detail collection for unselected/ineligible instances, including factory priming with no selection. One immutable inventory feeds a cycle; real fanout has a declared bound.
- Stable process identities retain usable rates during unrelated process churn, while recycled identities restart their own baselines. Runtime late-load and unavailable-provider recovery work without restarting the recorder.
- On authorized real servers, confirm English/non-English paths, PDH type/units, uncapped native CPU formatting, CLR cross-object instance identity, in-process/out-of-process hosting and default service permissions. Measure the actual registered groups and resolver together against under 0.5% of one core, under 50 MB working set and the bounded one-second group policy. No EventPipe proof is required to certify this fallback, and no fallback test certifies EventPipe.

**Validation performed:** bounded offline fake-reader selection in `go test ./internal/blackbox`, including all owned wave-5 test-name families and the other reviewed-wave families plus `TestStep9StillFindsWebServer`, `-count=1 -timeout=60s`: PASS (`ok`, 0.822s). Separate fake-budget/W3WP-refresh selection, `-count=1 -timeout=30s`: PASS (`ok`, 0.552s). Windows and `GOOS=linux` `go vet ./internal/blackbox`: PASS. Used the supplied Go binary with `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`. No asserted test-count totals, live counters, full application build/full suite, new source/test files or live PID-reuse experiments. New adversarial cases above are specified, not falsely reported as executed. Only the three authorized Markdown reports were written.
