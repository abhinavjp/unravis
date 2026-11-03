# TSK-41 hostlane-d9-d10

risk: medium — the host lane starts answering two SQL suspects; answering them from the wrong machine would mislead.

## Outcome

When the Blackbox recording is on the SQL Server machine (a process named `sqlservr` is in its incident samples), the host lane also answers:

- **D9 Disk IO latency**: Yes when the incident mean disk latency is at least 15 ms and (there is no reference, or it is at least 1.3× and at least 5 ms above the reference mean); No when it is below that and disk readings cover at least half the incident minutes.
- **D10 SQL CPU saturation**: Yes when H1 is Yes and the busiest process is `sqlservr`; No when H1 is No.

On any other machine D9 and D10 stay unset. H2–H6 stay unanswered (no counters; see `phase.md` out of scope). H1 is unchanged.

## Write scope

- `internal/investigate/hostlane.go`
- `internal/investigate/hostlane_test.go` (append)

## Must not change

- The H1 rules and their consts (~16–35), `HostGaps`, `topProcess`.
- `catalog.go`, `thresholds.go` (marks are lane-local).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. Const block (~16–35): add
   ```go
   // Disk latency marks for D9 (FETCHABILITY-AUDIT 6c), local to this lane.
   hostDiskSlowMs  = 15.0
   hostDiskRiseMs  = 5.0
   hostDiskRisePct = 0.3
   ```
2. `HostResult.Lanes` comment (~63): "holds H1, and D9 and D10 when the recording is on the SQL Server machine; the Blackbox has no counters for H2–H6."
3. Add helpers after `topProcess` (~320):
   ```go
   // onSQLHost is true when any incident sample lists a process named sqlservr.
   func onSQLHost(ss []HostSample, w Window) bool
   // isSQLProc folds case and drops ".exe": "SQLSERVR.EXE" → true.
   func isSQLProc(name string) bool
   // diskMean is the mean DiskMs of samples in w and the number of distinct
   // minutes that hold a reading.
   func diskMean(ss []HostSample, w Window) (mean float64, minutes int, ok bool)
   ```
   `diskMean` uses `minuteOf(s.T)` (already in the package) to count minutes; `ok` is false when no sample has `DiskMs`.
4. In `HostLane`, after the H1 `switch` (~280–299) and before `return res` (~300), add:
   ```go
   if onSQLHost(h.Samples, in.Incident) {
       hostSQLLanes(&res, in, h, total)
   }
   ```
   and the function:
   ```go
   func hostSQLLanes(res *HostResult, in Input, h HostInput, total int) {
       if m, n, ok := diskMean(h.Samples, in.Incident); ok {
           r, _, rok := diskMean(h.Samples, in.Reference)
           rose := !rok || (m >= r*(1+hostDiskRisePct) && m-r >= hostDiskRiseMs)
           switch {
           case m >= hostDiskSlowMs && rose:
               res.Lanes["D9"] = Yes
           case total > 0 && float64(n)/float64(total) >= hostMinCoverage:
               res.Lanes["D9"] = No
           }
       }
       switch res.Lanes["H1"] {
       case Yes:
           if isSQLProc(res.TopProcess) { res.Lanes["D10"] = Yes }
       case No:
           res.Lanes["D10"] = No
       }
   }
   ```
   Note: the early returns at ~220–231 (invalid incident, low CPU coverage) return before this point, so D9/D10 stay unset there. That is intended: no CPU coverage means no host reading.
   Edge: when the D9 mean is ≥ 15 ms but did not rise, neither case holds only if coverage is low; with coverage it reads No — a steady 20 ms disk did not start the slowdown. State this in the D9 comment.
5. `ApplyHost` (~329–331): replace the copy loop with one that never overwrites a `Yes` set by another reader and skips `Unknown` for every id except `H1`:
   ```go
   for id, v := range res.Lanes {
       if id != "H1" && (v == Unknown || in.Lanes[id] == Yes) {
           continue
       }
       in.Lanes[id] = v
   }
   ```
6. `hostlane_test.go`: copy the sample-building helper of an existing `HostLane` test. Add `TestHostLaneD9D10`:
   - samples with procs `{sqlservr.exe 80}` busy (CPU 95) for 10 incident minutes, `DiskMs` 25 in incident and 4 in reference → H1 Yes, D9 Yes, D10 Yes;
   - same but `DiskMs` 6 in incident → D9 No;
   - incident `DiskMs` 18, reference 16 (rise under 5 ms) → D9 No;
   - no `DiskMs` anywhere → D9 absent;
   - CPU 30 all minutes → H1 No, D10 No;
   - procs `{w3wp 80}` only (not the SQL machine) → D9 and D10 absent, H1 as before;
   - `ApplyHost` on an `Input` whose `Lanes["D9"] == Yes` with a recording that reads D9 No → stays Yes.
   Run the existing `HostLane` tests unchanged.
7. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/investigate -run "TestHostLane|TestApplyHost|TestHostGaps"
.dev\toolchains\go\bin\go.exe test ./internal/investigate/...
```

PASS; goldens unchanged (no golden has a Blackbox recording; if one moves, BLOCKED with the diff in `progress.md`).

## Depends on

40 (`HostSample.DiskMs`).
