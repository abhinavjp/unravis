## Executive Risk Summary

**Risk: High.** Canonical wave 3, BBX-1-07/08/09/12, reviewed in the main repository at **HEAD 33c219b4b270229f44cc2b09ab234b1c6b0c72dc**. Scope: recorder/host budget integration and selfstat twins, resolver/process inventory and runtime detection twins, footprint benchmark, target picker/client/copy, related tests and immediate callers. Full scoped source files were read. The real UI → app route → elevated helper → CLI → installed configuration → recorder startup path was traced; SQL and later counter-group implementations are not independently reviewed here.

Own packets read: [budget loop](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-1-07-budget-loop.md#L1), [resolver](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-1-08-resolver-proc.md#L1), [benchmark](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-1-09-benchmark-gate.md#L1), [picker](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-1-12-wizard-picker.md#L1). The [handoff](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-expansion-handoff.md#L18) requires real-data proof. Existing gate reports are claims, not verification performed by this review.

Top three blockers:
1. W3-01: Apply reports success but never copies the new target selection into the service's installed configuration.
2. W3-02: successfully observed “no runtime loaded yet” is cached forever for that process identity.
3. W3-03: process changes that occur during budget skips are forgotten before the affected group resumes.

**Unverified Assumption:** the actual service stays under 0.5% of one core, 50 MiB and one second per group over a representative ten-minute run. W3-07 explains why the fake gate cannot establish this.

Positive source findings: [selfstat_windows.go:47-79](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/selfstat_windows.go#L47) reads the current-process pseudo-handle and correctly computes percent of one core, without the historical core-count division. [selfstat_other.go:7-19](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/selfstat_other.go#L7) explicitly reports measurement unavailable. [recorder.go:157-168,200-205,259-269,347-365](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/recorder.go#L157) reuses one ticker, feeds budget before collection and uses effective interval for recorder lateness. [store.go:314-333](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/store.go#L314) persists effective cadence. The old simple widened-interval false-lateness defect is not refiled. No concurrent writer to non-atomic Budget fields was found on the traced single-recorder path; race-detector testing was not performed.

## Findings Table

| ID | Severity | Category | Component/Location | Summary |
|---|---|---|---|---|
| W3-01 | High | Logic / persistence / owner control | [internal/blackbox/thispc.go:214-232,284-288](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/thispc.go#L214); [internal/blackbox/service.go:472-507](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/service.go#L472) | Apply validates a temporary file and restarts the old installed configuration without saving the new selection. |
| W3-02 | High | Logic / runtime detection | [internal/blackbox/resolve.go:137-173](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve.go#L137) | Runtime 0 from a successful pre-initialization module snapshot is permanently cached. |
| W3-03 | High | Architecture / budget interaction | [internal/blackbox/host.go:94-101,144-146](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/host.go#L94) | Per-cycle change boolean is overwritten even when skipped groups have not consumed it. |
| W3-04 | Medium | Identity / PID reuse | [internal/blackbox/resolve_proc_windows.go:52-64](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_proc_windows.go#L52) | Creation time is truncated to seconds, allowing distinct same-PID lifetimes within one second to alias. |
| W3-05 | Medium | Performance / consistency | host.go:98-101; resolve.go:127-134 | Targets callback re-inventories the host on every invocation; a normal tick can take many snapshots. |
| W3-06 | Medium | UI logic / missing target state | [web/src/components/BlackboxSettings.svelte:34-37,82-110](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/web/src/components/BlackboxSettings.svelte#L34) | Successful partial/capped discovery hides manual entry, making absent targets unselectable and existing absent selections unlabeled. |
| W3-07 | High | Verification / Unverified Assumption | [internal/blackbox/budget_bench_test.go:218-266,299-331](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/budget_bench_test.go#L218) | The fake gate omits major production costs and has no working-set assertion; release-level footprint safety remains unverified. |

## Detailed Findings

### W3-01 — Target changes do not reach the installed recorder

**The Flaw:** The search question was checked against actual wiring, not accepted as evidence. [cmd/audit-explorer/blackbox.go:35-49](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/audit-explorer/blackbox.go#L35) installs the real ThisPC helper. [BlackboxPanel.svelte:172-203](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/web/src/components/BlackboxPanel.svelte#L172) copies the loaded settings and submits the whole object; [blackbox.ts:197-214,232-236](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/web/src/lib/blackbox.ts#L197) serializes it with the application token. [internal/server/blackbox.go:1035-1062](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/server/blackbox.go#L1035) validates and forwards cfg unchanged under the this-PC mutation lock.

ThisPC.do marshals cfg into a temporary file and removes that directory after the helper call. HelperArgs passes `apply --json --config <temporary file>` (thispc.go:110-115). The real [elevate_windows.go:88-101](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/elevate_windows.go#L88) executes those arguments; it does not save configuration. [cmd/unravis-blackbox/main.go:215-227,253-261](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/main.go#L215) honors the explicit file and calls Manager.Apply. Manager.Apply only LoadConfigs that file, then stops/starts the existing service. There is no SaveConfig or service-argument update in Apply.

Meanwhile installation registers a fixed installed config path (service.go:328-336), and [cmd/unravis-blackbox/service_windows.go:64-75](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/service_windows.go#L64) reloads that installed file at service start. This is an inherited helper integration defect exposed by the new target controls, not a SQL-probe restriction.

**Exploit / Failure Scenario:**
1. Have a working installed recorder with selected target A.
2. In Apply settings remove A and select B; submit a valid configuration and approve the helper normally.
3. The temporary configuration validates. Manager.Apply restarts the recorder and returns Restarted=true, but the service loads its unchanged installed file: A remains collected and B is absent.
4. The temporary file is deleted; reloading settings shows the old selection. If the recorder was stopped, Apply returns success without a restart or a save, and its next start also uses the old list.

The UI's [blackboxCopy.ts:103-107](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/web/src/lib/copy/blackboxCopy.ts#L103) claims the opposite. [thispc_test.go:120-140](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/thispc_test.go#L120) fakes the helper answer; [main_test.go:203-221](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/cmd/unravis-blackbox/main_test.go#L203) tests bare apply, not a temporary changed configuration.

**Remediation:** Validate first, atomically save the new settings to the service's actual installed config path under the approved elevated operation, then restart that recorder only. Define rollback/error reporting when save or restart fails. Add an integration test with fake SCM but real temporary files connecting ThisPC arguments to the CLI and Manager: install A, apply B from a different file, inspect the installed config and the next HostOptions, and verify A is removed. Cover a stopped recorder and failure paths. No target service should be restarted.

### W3-02 — Early unknown-runtime observation is permanent

**The Flaw:** On a successful loadModules call with no CLR module, classifyRuntime returns 0 and Inventory stores that result in cached. With a real Start, stable inventories reuse it; even an unrelated process-set change reuses the cached 0. Runtime loading is not synonymous with process creation. A failed module read is not cached, but retries still depend on another process-set change; that does not repair the successful-empty case. The historical Start==0 cache bug is fixed and is not this finding.

**Exploit / Failure Scenario:**
1. Snapshot a newly created selected process with a valid creation time before clr.dll/coreclr.dll/hostfxr.dll appears. Module enumeration succeeds with other modules only.
2. Inventory caches runtime=0 for its (PID, Start).
3. The same process finishes runtime initialization. Subsequent stable snapshots return cached 0; unrelated process arrivals also leave that entry unchanged.
4. Runtime-specific consumers omit it indefinitely. For example, [proc10.go:145-147](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/proc10.go#L145) requires runtime 2, and [clr.go:180-188](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/clr.go#L180) requires runtime 1. A late-initializing out-of-process child can likewise remain undiscovered.

[resolve_test.go:96-156](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_test.go#L96) begins with runtime modules already present. **Unverified Assumption:** the first successful module enumeration occurs only after runtime initialization is complete.

**Remediation:** Distinguish confirmed runtime from unresolved/pending state. Retry unresolved selected identities on a bounded schedule independently of process-set change, retaining the no-per-tick-full-module-scan constraint. Test stable-identity transitions empty→Framework and empty→CoreCLR, denied→readable, and delayed out-of-process initialization. Known classifications and retired identities must still have bounded cache lifetimes.

### W3-03 — A skipped group loses its refresh notification

**The Flaw:** processChangeGate updates changed from Resolver.ProcessSetChanged every collection cycle. That resolver advances its baseline on each successful snapshot (resolve.go:184-195), regardless of whether each group runs. [groups.go:161-174](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/groups.go#L161) bypasses skipped groups entirely. On resumption, an unchanged host yields false, so callers such as proc10.go:108-115 never refresh instances for the missed change.

**Exploit / Failure Scenario:**
1. Configure a selected service that is initially stopped; let the Process-counter group initialize while other processes exist.
2. Budget pressure skips optional groups. During that skip, the selected service starts with a new instance name not in the group's paths; allow its runtime to initialize before resolution, isolating W3-02.
3. The gate observes changed=true, advances its baseline, but the skipped group cannot consume that notification.
4. Keep the process set stable and allow the budget to re-enable the group. changed=false now; the old source lacks the new instance and detail remains absent until some later unrelated change triggers a refresh.

[host_test.go:323-359](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/host_test.go#L323) proves one shared boolean per cycle, not eventual delivery to skipped consumers.

**Remediation:** Publish a monotonic inventory generation, with per-group last-successfully-refreshed generation, rather than an edge-triggered boolean. Retain dirty state through skips and failed refreshes; acknowledge only successful reconciliation. Add a fake end-to-end skip→change→stable→resume test asserting the new instance becomes readable without another process change. Coordinate with W1-02 so this does not repeatedly reset unrelated rates.

### W3-04 — Creation-time precision is insufficient for the identity claim

**The Flaw:** processStart divides FILETIME nanoseconds by 1,000,000,000. Resolver cache and process-set identity compare only PID plus that truncated value. Full creation times that differ within a second are collapsed. The shared helpers correctly compare the keys supplied; the platform boundary has already thrown precision away.

**Exploit / Failure Scenario:**
1. A process with PID P starts during second S and is observed by the resolver.
2. It exits and Windows reuses P for a different process also created within second S; the two distinct native creation timestamps are the required precondition.
3. Both identities become (P,S). If the other observed keys are unchanged, ProcessSetChanged returns false and Inventory reuses the old runtime. Even with other set changes, cached[P,S] can survive.

No same-second reuse was induced; its frequency on the deployment is an **Unverified Assumption**. The collision in the key function is confirmed, and existing tests only change Start to a different integer second. This is distinct from already-fixed handling of unreadable Start==0.

**Remediation:** Use full FILETIME ticks or lossless nanoseconds for internal identity; separate display/elapsed-time seconds from identity. Update immediate consumers consistently rather than silently changing the meaning of a wire field. Add two fixtures with identical PID and creation times within the same second that must invalidate cache and refresh state. At module-read boundaries, also confirm that a queried PID still represents the expected creation identity before accepting its classification.

### W3-05 — The shared callback still repeats whole-host inventory work

**The Flaw:** host.go:98-101 makes Targets a call to ResolveTargets, not a per-tick snapshot. ResolveTargets immediately calls Inventory ([resolve_map.go:41-55](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_map.go#L41)), which always snapshots processes. The Windows implementation opens every PID to obtain Start (resolve_proc_windows.go:33-41,54-64). Caching module classifications does not cache that work.

**Exploit / Failure Scenario:**
1. Enable the process list and the four selected-target groups, with readable sources and resolved targets, without budget skips.
2. The host gate snapshots once. [collector.go:225-226](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/collector.go#L225) calls Targets again; [iis.go:134-148](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/iis.go#L134) calls it once. CLR and proc10 each call it twice (clr.go:145,182; proc10.go:108,145); [procio.go:137-138,185-195](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/procio.go#L137) adds two more on a successful path.
3. These callers alone can cause nine resolver snapshots per tick, in addition to the sampler's process scan. Inventory initially enumerates modules for all uncached processes, not just selected ones; unknown-Start processes repeat module work on each inventory.
4. The work scales with host process count, and independently collected snapshots can describe different process generations within a single Sample.

**Unverified Assumption:** this amplification still fits the stated live budget. No actual CPU/working-set excess is asserted without measurement.

**Remediation:** Resolve one immutable inventory/target result per tick and share it, including its generation and unavailable-target state, among the host and groups. Restrict fresh module classification to the processes necessary for selected targets/children. Add a counting fake test of the real composed path: one resolver process snapshot per tick regardless of consumer count, with bounded targeted module reads.

### W3-06 — Successful discovery hides the escape hatch for absent targets

**The Flaw:** showLists requires only detected=true and at least one entry. The manual add row exists exclusively in its else branch. The endpoint offers running services only ([internal/server/blackbox.go:572-585](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/server/blackbox.go#L572)) and caps lists at 256 entries (lines 474-486). Thus a successful response need not contain the desired target. Already-selected names absent from the list become extraPicked chips whose template carries no running/unknown state. The picker reads only once on mount.

**Exploit / Failure Scenario:**
1. In this-PC mode, receive successful detection containing other pools/services but not the desired stopped service, or not a running service beyond the list cap.
2. showLists is true, so the user has neither its checkbox nor a manual entry row. They cannot select it in the current form despite knowing its valid name.
3. If it was selected previously, it appears only as an unlabeled chip. The “not running” text at lines 86/92 only applies to entries returned with running=false; the real service enumerator never returns such an entry.

The executed [web/tests/blackbox.targets.test.cjs:125-149](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/web/tests/blackbox.targets.test.cjs#L125) checks source strings, not this rendered interaction. Missing from a capped/unreadable list must not itself be guessed to mean stopped.

**Remediation:** Keep manual add available alongside detected lists, with the same server validation and target cap. Represent selected targets' known-running, stopped, unavailable and not-detected states honestly, using per-source detection/truncation metadata. Add DOM-level tests for successful-but-incomplete discovery, a selected stopped service and a capped-away name; verify their selection survives review/install/apply.

### W3-07 — The gate does not prove the claimed production footprint

**The Flaw:** This is a confirmed test-coverage limitation and an **Unverified Assumption** about deployment safety, not a measured production overrun. newBenchRig constructs six substitute groups with about 200 fixed readings and disables the host process list. It does not build the production registry, use the resolver, create native queries or execute Recorder.Tick/store writes/housekeeping/log catalogue. rig.tick calls collect then Cycle, whereas the real recorder feeds/cycles before collect. checkGate logs bytesPerTick but does not constrain it; it asserts wall time and allocation count only, not retained memory or process working set. SelfUsage is fixed to 0.1%/30 MiB.

**Exploit / Failure Scenario:**
1. Run the existing fake gate while production wildcard cardinality, resolver scans or native query memory is substantially higher than the fixed fixture.
2. The substitute groups still process exactly the same fixture, so those production regressions need not change the gate result at all.
3. A passing result is interpreted as approval of the real one-second/50 MiB/CPU limits. It provides no such evidence. The slow-group test directly injects a duration (lines 370-372), so it also cannot establish timeout/preemption safety.

**Remediation:** Keep this benchmark but label its narrower scope. Add fake-platform tests of production group/host/recorder composition, including maximum supported targets/cardinality, churn, missing self-stat readings and blocked providers. Assert bounded retained state and allocations/bytes, not allocation count alone. Separately require owner-authorized live CPU, working set, handles, goroutines and per-group duration evidence for at least ten minutes under representative load, recording environment and measurement method. A fresh failed self-reading after a calm measurement must not be represented as fresh evidence of safety.

## Cross-Wave Impact

- The target-persistence question is now resolved by source tracing: there is **no general JSON target rejection and no SQL-probe coupling**. [config.go:268-295](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/config.go#L268) validates targets separately from probe.enabled. Initial install saves the complete config at service.go:330; [pack.go:87-88,122-123](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pack.go#L87) includes the complete marshaled config. [BlackboxWizard.svelte:64-101,191-192](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/web/src/components/BlackboxWizard.svelte#L64) submits the bound target list and keeps pack mode manual. **Apply**, specifically, does not persist the new config. Live installation/reload was not attempted.
- Wave 1's rate lifetime and PDH-success-status defects, and wave 2's synchronous timeout, native percentage cap, cardinality and unavailable-group handling remain prerequisites. They are not duplicated here. W3-03 is a separate lost-notification issue: it persists even after a correct incremental source refresh is implemented.
- Module scans use query-only process opens and Toolhelp module snapshots; no target suspend/restart/dump call was found in the scoped resolver. **Unverified Assumption:** its actual target-side cost and runtime/hosting-model coverage are acceptable on the deployment. [resolve_other.go:7-13](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_other.go#L7) returns explicit Windows-only errors, not fabricated runtimes.
- Target-resolution missing names are discarded by host.go:99. Consequently, fixing the form alone cannot establish live per-target “not found/not running” evidence; a shared runtime-state contract is needed. This is a cross-wave integration consequence, not an assertion that absent measurements are zero-filled.
- **Tests actually run:** `node --test "C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/web/tests/blackbox.targets.test.cjs"` — **8 passed, 0 failed, 0 skipped**. It transpiles TypeScript in memory and mocks fetch; no browser, server or target was contacted. No Go tests/build/vet/benchmark, race run, broad npm suite or live checks were run. This avoided generated Go artifacts and OS-backed tests under the restricted review-only scope. Source inspection is not reported as executed test coverage.

## Go / No-Go Verdict

**NO-GO.** Required exit criteria:
1. Fake-SCM integration proves installed configuration and next recorder HostOptions change from A to B, including removal of A, for both running and stopped recorders; failure paths do not claim success. Then obtain owner-authorized real setup/apply/reload evidence.
2. Stable-identity delayed runtime initialization and denied→readable tests recover within a specified bounded retry window; known runtimes remain cached appropriately.
3. At least 20 skip/change/resume cycles deliver the newest generation to every group without requiring a second process change; failed refreshes retain pending state.
4. Same-second native creation fixtures do not alias, and the composed fake path takes one resolver snapshot per tick regardless of consumer count.
5. DOM-level tests demonstrate selection of a stopped/capped-away target alongside successful discovery, honest missing-state labels, and selection persistence through real client requests.
6. Close upstream wave-1/2 blockers and distinguish fake microbenchmark results from production proof. The live acceptance record must show average CPU below 0.5% of one core, working set below 50 MiB, each enabled group within one second, bounded handles/goroutines, and correct readings/recycles over at least ten minutes. Until observed with authorization, each such safety claim remains **Unverified Assumption**.
