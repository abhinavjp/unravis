# R2 and R3: a true P95 across minutes, and "earliest layer goes red"

Read-only research. Nothing outside this file was changed. `sql/` was not read. Line numbers are as seen on branch `prototype/tracing-paper-e2e` on 2026-11-02. Anything not checked is under "Unverified" at the end.

---

## R2. A true P95 across minutes

### R2.1 What the prototype does today

The prototype never holds individual calls. Each synthetic minute carries a P95 and a call count, and every longer period is built from those.

| What | Where | Method |
|---|---|---|
| Per-minute P95 | `prototype/tracing-paper-PROTOTYPE.html:234` (`opMinute`) | Synthetic: `p95 = op.p95 * f * noise`, `p50 = 0.42 * p95`. Not computed from calls. |
| Minutes carry at least 20 samples | `:225` (`SYN_VOL = 1.7`) | Synthetic volume. |
| "Observed P95" for a window | `:241-245` (`auditWindow`) | `w95 += v.p95 * v.n`, then `p95 = s.w95 / s.n`. This is a **call-weighted mean of per-minute P95 values**. |
| Chart buckets | `:250-251` (`auditBuckets`) | Same weighting. `max` is `max(s.max, v.p95 * 1.6)`, a made-up value. |
| IIS and SQL lanes | `:259-263` (`laneBuckets`, `laneStats`) | Same weighting. |

The review document already says this is wrong. `prototype/CHARTS-AND-STATS-REVIEW.md` item A3 (:101-110): a weighted mean of minute P95s "is not a percentile" and "the real app must not ship this method". B1 (:214-232): the P95 card tooltip says "Estimated from per-minute figures. The product will compute it from every request's duration." §5.3 item 8 (:509) repeats the point. Owner decision: the prototype keeps its label; the product uses a real percentile. This note does not change that.

### R2.2 What the real Go code does

The product already has two real percentile methods. Neither averages percentiles.

1. **Log histogram, merged by summing counts** (`internal/audit/`).
   - `histogram.go`: bin 0 is under 1 ms, 192 bins each 10% wider than the last (`BinBounds`, 1.1^n), `overflowBin = 193`. `quantile` takes rank = ceil(p * count), walks the bins, and returns the bin midpoint clamped to the observed min and max. `stats()` reports P99 only when `count >= P99MinSampleCount`.
   - Merging is plain addition of bin counts: per-slot accumulators merged at `analyze.go:329` and `:394`; day histograms merged at `dailyseries.go:108-164`.
   - Error is stated in the code: "accurate to about 5%" (`endpoint_daily.go:38`). The user-facing warning is at `analyze.go:142`: "Latency quantiles are estimated from logarithmic histogram buckets; compared does not mean healthy." `internal/mcp/schema.go:17` says the same to MCP clients.
   - The binning is done on the MongoDB server. `internal/store/pipeline.go` (~:87-95, `auditBinExpr`) is a 192-branch `$switch`, so the server returns (bin, slot) counts, not raw documents. Row cap: `maxBucketRows = audit.MaxSummaryRows + 1`, `MaxSummaryRows = 40000` (`internal/audit/models.go:15`).
2. **Exact nearest-rank over raw durations** (`internal/investigate/`).
   - `stats.go`: `nearestRank` (rank = ceil(q * n)). `summarize` shows P95 only with at least `GroupSizeP95` = 20 samples and P99 with at least `GroupSizeP99` = 100.
   - `facts.go` `minuteP95` (~:679-686): a minute with fewer than `OnsetMinSamples` (5) calls gives nil; otherwise exact nearest-rank over that minute's sorted raw durations. `FromRecords` (`fromaudit.go`) turns audit records into raw `Request`s.
   - Limits: `internal/server/investigation.go:26` `investigationMaxRows = 30000`, `:27` `investigationMaxSpan = 7 days`. Above the cap (:325-345) the run marks the audit as failed and uses no rows ("There are too many audit calls in these windows to read in full, so none were used").
3. Unrelated: `internal/resource/rules/rc6789.go:349-375` takes a nearest-rank 95th percentile of ERROR counts per minute, not of latency.

Disagreement to fix: the prototype's `SUS_TH.onsetMinSamples` is 20 (`:1487`); the Go default `OnsetMinSamples` is 5 (`thresholds.go:160`). Not a percentile issue, but the prototype will show "no onset" in minutes where the product would show one.

### R2.3 Why averaging per-minute P95s is wrong

A percentile is a position in the sorted list of all calls. Per-minute P95s discard everything except one point per minute, and the position of the combined 95th call depends on how the slow calls are spread, not on the minute P95s.

Two periods, two minutes each, 100 calls per minute. Fast calls take 100 ms, slow calls 5,000 ms.

| | Minute A | Minute B | Mean of minute P95s (equal weights) | True P95 of all 200 calls |
|---|---|---|---|---|
| Case 1 | 0 slow. P95 = 100 | 6 slow. Sorted: 94 fast then 6 slow, 95th value is slow, so P95 = 5,000 | (100 + 5,000) / 2 = **2,550 ms** | 6 slow of 200 = 3%. The 190th value is fast, so **100 ms** |
| Case 2 | 0 slow. P95 = 100 | 11 slow. P95 = 5,000 | **2,550 ms** | 11 slow of 200 = 5.5%. 189 fast, so the 190th value is slow: **5,000 ms** |

Same mean (2,550 ms), true answers 100 ms and 5,000 ms. The mean overstates in case 1 by 25 times and understates in case 2 by half. It is not a biased-in-one-direction error that can be corrected with a factor. Weighting by call count does not help (the example is already equal weight). The mean also invents a value (2,550 ms) that no call came near.

### R2.4 The four options

Store facts used: sandbox is MongoDB 7.0.16 with about 778,618 audit documents, only the `_id_` index, shared with live client databases (`docs/superpowers/plans/2026-10-24-unravis-post-mvp-roadmap.md:139`), so extra load is not "sandbox only". Design: the account is read-only, writes nothing, creates no indexes (`README.md:35-37`); supported servers are MongoDB 3.4 and 4.4 or newer; 3.4 goes through a separate legacy driver that streams metadata, capped at 100,000 scanned records and 16 MiB. Live sources refuse broad scans beyond 24 h (`internal/audit/rollup.go:17`). Limits: 8 sources, 40,000 summary rows, 120 slots, 7-day period (`docs/superpowers/plans/2026-10-23-unravis-mvp-gaps.md:29`). Production volume is unverified; `buildout/plan/plan.md:397` uses 40 calls/s in a Little's-law example, which would be about 2,400 per minute and about 3.5 million per day (my arithmetic, not a measured store size).

| | (a) Merge raw samples | (b) Fixed-bucket histogram | (c) t-digest / DDSketch | (d) MongoDB `$percentile` |
|---|---|---|---|---|
| Accuracy | Exact (nearest-rank). | About +/-5% of the value (bin midpoint of 10%-wide bins). Rank is exact; only the value inside the bin is estimated. | DDSketch has a configurable relative error (1% is common); t-digest is best at the tails but has no fixed bound. Both better than (b) per byte. | Approximate, t-digest, "can differ run to run" (manual). No error bound given. |
| Merge across minutes | Concatenate and sort. | Add counts bin by bin. Exact, order-independent. | Mergeable, but result depends on merge order for t-digest. | Not mergeable. Each `$group` result is a final number. You must ask for the whole period in one `$group`. |
| Cost at these sizes | Needs every duration in memory. At about 2,400/min that is 3.5 million per day; the investigation engine already caps at 30,000 rows and fails above it. Fine for an incident-sized window, not for a day or 7 days. | One 194-element count array per slot and per group. Server returns (bin, slot) counts, row count bounded by the 40,000 cap. Already built and tested. | Small per slot, but needs a new dependency or hand-written code, plus a serialisation format. Client still has to scan every document to build it (or the server must do it, which only (d) can). | One pass on the server, nothing sent back but the answer. But the scan hits the live collection with no index. |
| Data to keep | Raw durations with timestamps. | Counts per bin per slot. | Sketch per slot. | Nothing; computed on demand. |
| Fits read-only, no extra storage | Yes, if the rows are already being read. Memory-bound. | **Yes.** It is what the product does now. Nothing written, nothing created. | Yes in principle (the sketch is rebuilt per run), but adds a new format and no gain over (b) for latency. | Read-only, yes. **Availability fails** (see below). |

**`$percentile` facts (MongoDB manual, fetched this session):**
- Introduced in **MongoDB 7.0**. It is an accumulator in `$group`, `$bucket` and `$bucketAuto`; the window-stage version is separate.
- `method` must be `"approximate"`.
- It always uses t-digest. Results are approximate and can differ between runs of the same query (pseudorandom).
- Non-numeric input is ignored. `p` values are 0.0 to 1.0.
- Therefore unavailable on 3.4, 4.4, 5.x and 6.x, all of which the repo targets or may meet. The repo's own pipeline test forbids it: `internal/store/pipeline_test.go:58` rejects `$percentile`, `$push` and `$dateTrunc`.

### R2.5 Recommendation (R2)

**Use option (b), the fixed-bucket log histogram, merged across minutes by summing counts.** This is the Go app's existing method.

- It is the only option that is accurate enough, mergeable, read-only, needs no new storage or dependency, and works on every supported server version (server-side binning on 4.4 and up; client-side binning of the capped record stream on 3.4).
- Use the exact nearest-rank value only where raw rows are already in memory and under the 30,000-row cap (the investigation engine), and say which method produced the number. Two methods giving 5% different numbers for the same period would otherwise look like a bug.
- Keep guards 15 and 16 of `buildout/research/investigation-walkthrough.md:422`: P95 only above 20 samples, P99 only above 100.
- Do not use `$percentile` except, if ever wanted, as an optional fast path on 7.0+ that is labelled approximate and never mixed into merged slots.
- Consequence for the owner: any "ratio versus reference" display near a threshold (for example 1.5x) has a resolution of about 10% either side. A displayed 1.48x and 1.52x are not distinguishable. Show thresholds with that tolerance or use exact values where rows are loaded.
- For the prototype: leave the label as decided, but drop the invented `max = v.p95 * 1.6` (`:250-251`), since the product will show a real maximum.

---

## R3. Is "earliest layer goes red" safe?

### R3.1 The rule

- Prototype (`prototype/tracing-paper-PROTOTYPE.html`): `SUS_TH` (:1487): `onsetFactor 2`, `onsetRun 5`, `onsetMinSamples 20`, `bucketMs 60000`, `clockMs 1000`. `susOnset` (:1596-1601). Colour rule (:1683-1693): "only the earliest tier among the consistent suspects is red, and only when it is clearly first". `sep = bucketMs + clockMs` (61 s). If the earliest tier is unique but another tier is unread, it goes orange. Unique and clear: red. A tie: orange. Later tiers: orange. `susCompare` and `susGroups` at :1699-1710. Review items B9 (:300-312) and B10 (:314-368); question Q2 at :514 asks exactly this.
- Go (`internal/investigate/origin.go`, `ComputeOrigin` :81-169):
  1. No audit, or no onset found: **not enough data**.
  2. Exactly one dated tier and every other tier read in every incident minute and flat (`othersHeldFlat`): that tier is **named**.
  3. Fewer than two dated tiers otherwise: **not enough data**.
  4. Otherwise sort the dated tiers by onset. If more than one tier lies within `sep` of the first (`sep = OriginBucketSeconds 60 s + ClockToleranceMs 1000 ms = 61 s`, `thresholds.go:154,187`): **no clear origin**.
  5. Else if any undated tier was not read in every minute (`unreadTiers`): **not enough data**.
  6. Else the first tier is **named**.
- Onset per tier (A24, `buildout/plan/decisions.md:39`): the first minute whose value stays above 2x the reference (and above it by at least `ChangeMinMs`) for 5 consecutive minutes. `facts_parts.go:532-584` (`metricOnset`, `loadOnset`): audit parts A, C and O use per-minute **medians**; the load tier uses arrivals at or above 1.3x the reference median. `findRun` (facts.go ~:640-670) skips a thin or short-gap minute without extending or breaking the run; a long gap or a miss resets it.
- Tier list (`facts_parts.go:586-611`, `tiers()`): lane tiers (sql, mongo, host) are added **only for lanes present in `Input.LaneOnsets`**, always marked Tested. `internal/server/lanes.go:471-476` sets `LaneOnsets[kind]` only when a day's onset is found inside the incident, keeping the earliest if there are several; `internal/investigate/hostlane.go:333-349` does the same for host. A lane that is missing, sparse, or has no onset never becomes a tier.

The rule is already conservative. It says nothing when the data is thin, nothing when tiers tie, and nothing when a tier was unread. Red requires the engine to have named the origin.

### R3.2 Golden cases

`testdata/investigate/*.golden.json`, `origin` section (checked this session):

| Case | Origin status | Meaning |
|---|---|---|
| demo-patterns | not enough data | No tier moved by the 100 ms mark, so none dated. |
| no-incident | not enough data | Nothing to order. |
| missing-iis-logs | not enough data | O unread, so no naming. |
| gap-in-data | not enough data | Gap makes tiers unread. |
| sql-pool-exhausted | **named, tier c, 2026-10-20T10:20:00Z** | The one positive case (`golden_test.go:132-150`). |

So four of five golden days test only that the rule **refuses** to name. One tests the happy path, with a clean step change and no lane data (the SQL lane is absent from `LaneOnsets`, so it cannot block naming). No golden case has two tiers with different onsets, noise, a lane, skew or retries.

### R3.3 Scenarios where the earliest red layer is not the cause

| Scenario | How it would fool the rule | Existing coverage | Gap |
|---|---|---|---|
| **Noisy earlier layer** | An earlier tier has a transient blip that satisfies the 5-minute run (or the thin-minute skip lets non-contiguous hits count), then the real cause starts later. The rule names the noisy tier first. | None. | No test of a blip, of onset jitter by one or two minutes, or of the thin-minute `stSkip` path. Nothing requires the earlier tier to stay up, so a 5-minute blip that recovers still dates the tier. |
| **Clock skew between sources** | Lane onsets (SQL, host, imported IIS files) are compared to audit onsets with 61 s of tolerance, of which 60 s is the bin width. Real skew between servers or time zones can be minutes or hours. | Partial. `clumsy_test.go:175` `TestClumsyClocksTwoSecondsApart`: IIS 2 s skew still matches, 5 s makes O untested, S1 and S2 not ruled out; C and O together give no origin; SQL 2 s after C gives C not named. | Nothing above 5 s. No 90 s, no 1 h, no time zone or DST shift of a lane. A lane 3 minutes early would be named the origin. |
| **Sparse-data layer** | A layer with too few calls never reaches the minimum, so it has no onset. If it is also missing from the tier list it cannot block naming. | Partial. `fix_test.go` F6 (:220): an unread O blocks naming A. `facts_test.go:90,107` for short and long gaps. | A lane present but sparse, or configured but empty, is not an "unread tier": it is simply absent from `tiers()`. The sql-pool golden names `c` with no SQL data at all. No test of this. |
| **Symmetric slowdown** (everything slows at once, for example a shared dependency) | All tiers step within one minute bin, so the rule reports "no clear origin". That is the right answer. The danger is when they differ by two bins (120 s): then the rule names one. | Partial. `evaluate_test.go:166` `TestNoClearOriginWhenTiersMoveTogether` (sql 30 s after); `clumsy_test.go` C and O together; `:186` `TestTiersFarApartAreSeparable` (sql 5 min later, c named). | No test with all of A, C, O and a lane stepping within 61 s; none at exactly 60 s (one bin) or 120 s (two bins) apart, which is where the boundary lies. |
| **Retries and timeouts** | Retries add load after the cause; a retry storm can raise arrivals (load tier) before the real tier clears its 5-minute run, so `load` is "first". | Not for origin. `signals_test.go:79` `TestSignalsRetriesAndErrors` covers only the retries-up and errors-precede signals. `scenario_test.go` `genCfg` has `retryEvery` and `retryWindow` but there is no origin test using them. | `load` is handled as an Amplifier only when it comes after the named tier (origin.go:150). If `load` is first, it is simply named. |

Other covered cases: `evaluate_test.go:143` `TestLaneOnsetsMakeTheOrigin` (SQL 6 minutes before C is named), `fix_test.go` F2 (:97, step 10 agrees with the origin), F3 (:143, no dated onset gives no red), N1 (:311, no IIS gives no red). `clumsy_test.go` `TestClumsyReferenceAfterTheIncident` and `TestClumsyEverythingAtOnce` exist (contents not re-read after this summary).

### R3.4 Recommendation (R3)

**Keep the rule, add guards.** "Earliest tier" is a reasonable heuristic for an investigation aid because it already refuses to answer in most doubtful cases. The failures are all of the "confidently wrong" kind, and they come from inputs the rule does not check, not from the ordering itself.

Guards to add, in order of value:

1. **Treat a configured-but-unusable lane as unread.** If a lane source is imported for the incident but yields no onset because it is sparse or flat-by-lack-of-data, add it to `tiers()` with `Tested=false` so `unreadTiers` blocks naming. Today lane tiers exist only if an onset was found (`facts_parts.go:586-611`).
2. **Persistence check on the earliest tier.** Require the earliest tier to still be elevated for most of the incident window (or at least while the later tiers are elevated) before naming it. A blip that recovers is not an origin. Otherwise report "no clear origin".
3. **Wider margin for cross-source comparisons.** Keep 61 s for audit parts (same host, same bins). For lane-versus-audit comparisons use a separate, larger tolerance (a configurable value, with the imported file's own clock offset shown) because the 1 s `ClockToleranceMs` is a same-host figure. Unverified what offset is realistic in the customer's environment; make it an owner question.
4. **Do not name `load` as origin when retries or errors precede it.** If the retries or errors signal fires, label load Amplifier or leave it undated.
5. **Boundary behaviour.** Document and test that exactly one bin (60 s) apart counts as together and two bins (120 s) apart counts as separable. With a 10% resolution (R2) these onsets are noisier than they look.

### R3.5 Test cases to add

All in `internal/investigate`, using the existing `gen`/`genCfg`/`mods` helpers and the `Input.LaneOnsets` field.

1. **Noisy earlier tier**: A (or O) raised for 5 minutes then recovers; C steps 10 minutes later and stays. Expect the origin to be C, or "no clear origin". Never A.
2. **One-bin jitter**: the same scenario run with the earlier tier's onset moved by +/-60 s. Origin must not flip between named tiers.
3. **Thin-minute run (`stSkip`)**: a tier with every other minute below `OnsetMinSamples`. Expect no onset or "unread", not an onset from the isolated hits.
4. **Lane skew**: SQL onset 90 s earlier than C because of clock; same at 1 h. Expect not named without a stated offset.
5. **Sparse lane present**: SQL lane imported but with no onset; C moves. Expect "not enough data" under guard 1, and a golden for it.
6. **Symmetric slowdown**: A, C, O and a lane all stepping within 40 s. Expect "no clear origin" and no red suspect.
7. **Boundary**: onsets exactly 60 s apart (together) and 120 s apart (separable).
8. **Retry storm**: load steps first because retries amplify; C follows. Expect load as Amplifier or undated, never the origin.
9. **Golden additions**: one golden with a lane and two tiers at different onsets, and one with a retry storm, so the origin JSON is locked for more than one positive case.

---

## Unverified

- Real production volume (calls per minute, documents per day). The 2,400 per minute and 3.5 million per day figures are my arithmetic from a 40 calls/s example in `buildout/plan/plan.md:397`.
- How the 3.4 path bins durations: I understood it streams metadata in the legacy driver (capped at 100,000 records and 16 MiB); I did not confirm by reading that client-side code that binning is used there.
- That `$percentile` is rejected by `pipeline_test.go:58` as a deliberate rule rather than a coincidence of the test list; I read only the forbidden-token list.
- Contents of `clumsy_test.go` tests other than `TestClumsyClocksTwoSecondsApart`, and `scenario_test.go` helpers beyond the `retryEvery`/`retryWindow` fields. No per-tier noise or jitter generator was found; one may exist under another name.
- Behaviour of the real MongoDB `$percentile` on a large unindexed live collection (cost, run-to-run variation size). Not tested; the repo only has a Mongo 7.0.16 sandbox.
- Whether a realistic lane-to-audit clock offset in the customer's environment is seconds or minutes. This decides guard 3's value.
- No code was run and no test was executed for this note. All statements about test outcomes come from reading the tests.
