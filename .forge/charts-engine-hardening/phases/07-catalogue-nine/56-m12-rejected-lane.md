# TSK-56 m12-rejected-lane

risk: medium — a second metric-fed lane answer; a No from a series with gaps or a counter reset would be a false green.

## Outcome

When the case holds the `mongo.conns_rejected` series (TSK-55) for the incident window, M12 gets a lane answer:

- **Yes** when the deltas over the incident add up to more than 0;
- **No** when every delta is 0, no sample is a `Reset`, and the series covers at least 80 in 100 of the incident window;
- otherwise no answer.

A server before 6.3 emits no series, so M12 stays Grey through its `Needs` line with no version parsing.

## Write scope

- `internal/server/investigation_metrics.go` (append; TSK-42 created it)
- `internal/server/investigation_metrics_test.go` (append)

## Must not change

- The D6 rule from TSK-42 and its consts.
- `internal/resource/*` (read-only use).
- `investigation.go` (the `readMetricLanes` call is already there).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. TSK-42 returns early when the D6 series is empty. Turn that early `return` into a skip of the D6 block only (an `if len(ss) > 0 { … }` or a small `memGrantsLane(x)` function), so a case with Mongo counters and no SQL counters still reaches M12. The `!in.Incident.Valid()`, catalogue-error and `x.Partial()` returns stay; reword the `Partial` warning to "The resource counters were too many to read in full, so the counter-based lane readings were not used." and update the TSK-42 test that checks it, if one does.
   Then, in `readMetricLanes`, after the D6 block and still using the same catalogue `x` (do not build a second one):
   ```go
   if v := mongoRejectedLane(x); v != investigate.Unknown {
       setMetricLane(in, "M12", v, "MongoDB server status")
   }
   ```
2. Add:
   ```go
   // mongoRejectedLane reads connections.rejected (M12). Yes when any connection
   // was refused in the incident; No only on a full, unbroken series of zeros.
   func mongoRejectedLane(x *resource.Index) investigate.Tri
   ```
   Body: `ss := x.Series(resource.MongoConnsRejected, resource.Filter{})`; empty → `Unknown`. Over every series and sample: `sum += s.Delta` when `s.HasDelta`; `reset = reset || s.Reset`; `cov = max(cov, x.Coverage(series))`. `sum > 0` → `Yes`; `!reset && cov >= metricLaneCoverage` → `No`; else `Unknown`. Use the real type names (`*resource.Index` may differ; read what TSK-42 used for `x`).
3. If TSK-42 wrote the D6 "set lane, add source once" lines inline, lift them into `func setMetricLane(in *investigate.Input, id string, v investigate.Tri, source string)` (never overwrite a `Yes`; make `Lanes` if nil; add `source` to `LaneSources` once) and call it from both D6 and M12. The D6 tests must still pass unchanged.
4. Tests, appended to `investigation_metrics_test.go` (reuse TSK-42's case builder; build `mongo-serverstatus` metric events with `Metric: "connections.rejected"`, one per minute over a 30-minute incident):
   - `TestReadMetricLanesM12None`: no such events → no `M12`.
   - `TestReadMetricLanesM12Yes`: values 0,0,3,3,… → `M12 == Yes`, `LaneSources` holds `MongoDB server status` once.
   - `TestReadMetricLanesM12No`: all 0, full coverage → `No`.
   - `TestReadMetricLanesM12Gappy`: all 0, 10 of 30 minutes → absent.
   - `TestReadMetricLanesM12Reset`: values 7,7,0,0 (a restart) with no positive delta → absent.
   - `TestReadMetricLanesM12KeepsYes`: `Lanes["M12"] = Yes` before, all 0 → stays `Yes`.
5. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/server/... -run "TestReadMetricLanes"
.dev\toolchains\go\bin\go.exe test ./internal/server/... ./internal/investigate/... -skip "^TestGolden$"
.dev\toolchains\go\bin\go.exe build ./...
```

PASS.

## Depends on

55 (`MongoConnsRejected`), 42 (`readMetricLanes`, same file in order), 44 (M12 in the catalogue).
