## Executive Risk Summary

**Risk: High.** Canonical wave 1, BBX-1-01/02/03/04, reviewed in the main repository at **HEAD 33c219b4b270229f44cc2b09ab234b1c6b0c72dc**. Scope: budget state machine, counter engine, target configuration, group wire types and Sample; complete source files and related tests were read, with immediate production callers traced. This is a review, not an implementation or release approval.

Rules: [CLAUDE.md](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/CLAUDE.md#L3), [handoff](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-expansion-handoff.md#L18) and [expansion conditions](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-expansion-prioritised.md#L7). Own packets read: [budget](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-1-01-budget.md#L1), [engine](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-1-02-engine.md#L1), [targets](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-1-03-config-targets.md#L1), [types](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/packets/BBX-1-04-group-types.md#L1). Existing gate reports were not accepted as proof.

Top blockers:
1. W1-01: the validity contract rejects PDH's successful NEW_DATA status at both source and engine boundaries.
2. W1-02: whole-engine recreation on unrelated process churn prevents stable counters from completing their rate baselines.
3. **Unverified Assumption:** the integrated implementation meets the 0.5%-of-one-core, 50 MiB and one-second group limits on a real server. Wave 1's pure state machine cannot establish that; canonical waves 2/3 own execution and measurement. No third independent High defect is asserted here.

Positive source findings: [budget.go:54-95](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/budget.go#L54) consumes slow marks and implements mandatory-group immediate widening; the old permanent-slow livelock is fixed. EffectiveInterval is atomic (lines 136-139); other state is documented as single-recorder-goroutine only. [groups_types.go:5-165](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/groups_types.go#L5) and [sample.go:27-63](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/sample.go#L27) use optional measurement/group pointers rather than inventing zero readings. No independent wire-type defect was confirmed.

## Findings Table

| ID | Severity | Category | Component/Location | Summary |
|---|---|---|---|---|
| W1-01 | High | Logic / API contract | [internal/blackbox/engine.go:24-30,78-80](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/engine.go#L24) | A valid NEW_DATA reading is treated as missing; useful changing counters can never establish baselines. |
| W1-02 | High | Architecture / rate lifetime | engine.go:54-65,99-104; [internal/blackbox/diskcpu.go:98-111](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/diskcpu.go#L98) | Refresh recreates all history, including unchanged direct counters, on any host process-set change. |
| W1-03 | Low | Logic / configuration identity | [internal/blackbox/config.go:268-287](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/config.go#L268) | Case-sensitive duplicate validation accepts aliases that resolve to the same Windows target. |
| W1-04 | Medium | Review provenance / Unverified Assumption | `.forge/blackbox-expansion/STATE.md:64–85,91,100–102`; `.qoder/agents/reviewer-ultimate.md:2–8` | Required reviewer tier is not proven by execution evidence, and contradictory resume status cannot establish gate closure. |

## Detailed Findings

### W1-01 — Valid changing data is rejected

**The Flaw:** The engine accepts only Status==0, and [pdh_windows.go:144-150](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pdh_windows.go#L144) sets OK only for CStatus==0. The [Microsoft counter-status contract](https://learn.microsoft.com/en-us/windows/win32/perfctrs/checking-pdh-interface-return-values) explicitly defines both VALID_DATA and NEW_DATA as successful; NEW_DATA can occur for a rate even when the computed rate is unchanged. API return status and counter-data status are different contracts. The packet's “any non-zero status invalid” instruction is itself incorrect for PDH, not proof of safety.

**Exploit / Failure Scenario:**
1. A successfully collected counter returns API success with CStatus=PDH_CSTATUS_NEW_DATA (1).
2. readingFrom preserves status 1 but sets OK=false. Process rejects it, including gauges.
3. If a rate continues returning NEW_DATA, its baseline count never advances; otherwise the stream develops artificial gaps precisely when values change. This conditional outcome follows from the source and documented API; it was not measured against a live provider.

The existing [engine_test.go:90-125](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/engine_test.go#L90) even uses status 1 as an invalid sample. [pdh_windows_test.go:33-44](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/pdh_windows_test.go#L33) checks status 0 and errors but not NEW_DATA.

**Remediation:** Introduce one explicit PDH data-status success predicate accepting VALID_DATA and NEW_DATA, still requiring API success. Use it consistently at both boundaries (or normalize at a clearly documented boundary). Replace the erroneous test status with an actual failure code; add NEW_DATA gauge and three-sample rate tests, plus API-error-with-success-CStatus tests. Preserve omission for genuine failures.

### W1-02 — Stable counters lose their baselines on unrelated churn

**The Flaw:** NewEngine always starts an empty history (engine.go:62-65), and there is no reconcile/reset-by-identity operation. Production refresh callers reconstruct the entire engine, not just new/changed instances. The immediate disk caller at diskcpu.go:101-105 resets even its direct processor counters. [http.go:76-92,114-136](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/http.go#L76) follows the same whole-engine pattern. This is a confirmed integration-level lifetime defect, not a claim that NewEngine alone should retain another object's state.

[host.go:94-101,134-146](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/host.go#L94) distributes a machine-wide process-change signal; [resolve.go:184-195](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve.go#L184) compares every snapshotted process, not only selected identities.

**Exploit / Failure Scenario:**
1. Keep the same physical disks and processor counters available, with valid status-0 readings; isolate this from W1-01.
2. At each sampling boundary, an unrelated process is added, removed or replaced in the observed process set.
3. Each changed=true call rebuilds the disk engine. Its PDH-rate fields only record their first baseline and remain absent indefinitely; gauges can still appear, making the partial recording look operational.
4. Less frequent churn still suppresses the first two valid samples after each rebuild. This does not require a selected target to recycle.

The two-baseline tests exercise a single enduring engine, not refresh of unchanged identities. **Unverified Assumption:** host process churn is rare enough to allow three valid samples between refreshes. That is not an acceptable correctness prerequisite.

**Remediation:** Reconcile specs and histories by durable counter/instance identity. Preserve both native handles and engine history for unchanged instances; invalidate replaced process identities using full creation time, and remove departed histories. Do not preserve a rate merely because a reusable instance string is unchanged. Add a fake integrated test with repeated unrelated churn and an unchanged direct counter: it must emit after its two initial baselines and continue thereafter. Recycled target identities must independently re-prime. Native refresh and resolver changes belong to their owning waves; this report identifies the foundation contract they need.

### W1-03 — Windows target aliases bypass duplicate detection

**The Flaw:** Config.Validate uses map[Target]bool, hence exact case-sensitive names. Its direct consumer [resolve_map.go:97-127](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/resolve_map.go#L97) resolves pool and service names with EqualFold, while preserving the input spelling in Key. Configurations accepted as distinct therefore identify the same process. The 32-entry bound remains intact; this is not an unbounded-list vulnerability.

**Exploit / Failure Scenario:**
1. Submit a valid configuration containing service names “EmailSvc” and “emailsvc”, with the same kind.
2. Both pass duplicate validation.
3. If that service resolves, ResolveTargets returns the same PID/start twice with differently cased output keys. Per-target consumers can emit duplicate rows, and one alias remaining selected defeats the apparent removal of the other.

[config_test.go:316-334](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/internal/blackbox/config_test.go#L316) tests exact duplicates and different kinds, not case aliases.

**Remediation:** Deduplicate names using the same equality rule as the Windows resolver, per kind; retain a display spelling separately if needed. With at most 32 entries, pairwise EqualFold is sufficient. Add mixed-case duplicate and single-removal tests across config and picker.

### W1-04 — Review labels and a contradictory ledger do not establish gate closure

**The Flaw:** The foundational process requires a specific high-risk review tier and an up-to-date ledger ([orchestration brief:15–23,83–96](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/docs/research/blackbox-qoder-orchestration.md#L15)). [STATE.md:85,91](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.forge/blackbox-expansion/STATE.md#L85) explicitly says the custom agent types could not be dispatched and the actual per-dispatch model could not be forced. The [reviewer definition:2–8](file:///C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/.qoder/agents/reviewer-ultimate.md#L2) calls itself Ultimate but declares `Qwen4-max-preview`, reflecting a recorded user-requested model change. Neither proves the model/tier that actually ran. This is an **Unverified Assumption about execution provenance**, not proof a lower-tier reviewer was used.

Separately, STATE.md:64–76 still says collect/review next, gate RUNNING and samplers/UI in-flight/pending, while its log at 100–102 says fixes, integration and delivery completed. The authoritative status is therefore ambiguous even before considering whether its review claims are sound. The principal and an independent read-only governance check confirmed both documentary inconsistencies.

**Exploit / Failure Scenario:**
1. A resumed orchestrator or release approver relies on the summary table and the role label as the required review evidence.
2. Depending on which section is read, already-delivered work appears pending or a claimed completed gate is treated as independently verified, despite missing execution provenance/closure evidence.
3. A later wave can be dispatched or accepted against an unproven prerequisite; alternatively, stale resume instructions can trigger duplicate work. No actual lower-tier dispatch or unauthorized release is asserted.

**Remediation:** Record required tier, requested model, observed execution model/tier when available, dispatch identifier, reviewed commit, report, verdict and high-severity closure evidence separately. Where actual runtime metadata is unavailable, mark it unverified and require an explicitly accepted alternative review rather than relabeling a prompt. Reconcile the resume table and log; distinguish implemented, reviewed, delivered and live-verified. Preserve historical exceptions rather than rewriting them as compliant. Wave 10 records the separate, concrete sampler-before-gate ordering breach.

## Cross-Wave Impact

- W1-01 needs a coordinated engine/source correction in canonical wave 2; fixing only one check still drops NEW_DATA. It is not duplicated as another finding there.
- W1-02 propagates into later counter consumers. Those callers were traced to establish actual foundation usage, not reviewed as separate later-wave deliverables. Canonical wave 3's transient change notification and canonical wave 2's handle replacement are dependencies of a correct lifetime design.
- Budget timing is retrospective, not preemption. The concrete execution defect is owned by canonical wave 2. The old slow-mark livelock and old CPU core-division defect must not be refiled.
- Target JSON serialization exists. The unconfirmed “targets are blocked by probe.enabled” theory is rejected: target validation and SQL-probe refusal are separate. The actual Apply persistence failure is documented once in canonical wave 3 after tracing the real helper, CLI and service startup.
- Verification performed: full-file review of scoped source/tests and direct callers; public Microsoft API documentation cross-check. No Go tests/build/vet/benchmarks or live target/service tests were run. The only executed suite in this review session was the eight-test in-memory web target-picker suite, reported with canonical wave 3; it does not prove wave 1 behavior.

## Go / No-Go Verdict

**NO-GO for release as the foundation of live collection.** Exit criteria:
1. Source and engine accept successful NEW_DATA and reject actual errors; the new status matrix and baseline tests pass.
2. An unchanged counter survives at least 20 fake process-set changes without repeated baseline starvation; replacing an identity still suppresses its first two valid samples.
3. Case-alias duplicate validation is aligned with resolution, or its Low-severity limitation is explicitly accepted.
4. Canonical waves 2/3 close their budget/identity/persistence blockers. Before release, owner-authorized live evidence must establish the stated CPU, memory and latency limits, real counter values and recycle behavior. Until then these are **Unverified Assumption**, not a passed gate.
5. Reconcile the execution ledger and attach verifiable review/closure evidence to the exact reviewed commit; unresolved model provenance must be explicitly disclosed or covered by an accepted alternative review. Green CI on the proposed merge tree is also required; the shared current delivery failure is documented as W13-05.
