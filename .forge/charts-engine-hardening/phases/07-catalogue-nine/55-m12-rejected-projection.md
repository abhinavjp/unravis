# TSK-55 m12-rejected-projection

risk: medium — a new serverStatus metric through the importer, the export policy and the resource catalogue; the fixture test requires line 1 to emit every metric.

## Outcome

The `mongo-serverstatus` importer emits `connections.rejected` (a cumulative count, MongoDB 6.3 and later), the case-export policy allows the metric name, and the resource catalogue exposes it as `mongo.conns_rejected` with per-sample deltas. Servers before 6.3 emit nothing for it.

## Write scope

- `internal/importer/mongoexports/serverstatus.go`
- `internal/importer/mongoexports/testdata/serverstatus-good.jsonl`
- `internal/importer/mongoexports/values_test.go`
- `internal/evidence/caseexport_enums.go` (the serverStatus metric list only, ~525)
- `internal/resource/metrics.go`
- `internal/resource/resource_test.go`

## Must not change

- Any other metric, rule or enum.
- `internal/server/*` (TSK-56).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `serverstatus.go` `serverStatusMetrics` (~28–52): after the `connections.current` / `connections.available` lines (~31) add
   ```go
   {"connections.rejected", []string{"connections", "rejected"}, "count", 1},
   ```
   Match the element shape of the neighbours exactly (read two lines; the last field may be a scale or a flag).
2. `testdata/serverstatus-good.jsonl`:
   - line 1: `"connections":{"current":41,"available":819}` → `"connections":{"current":41,"available":819,"rejected":0}`;
   - line 2: the `"available":816` object gains `,"rejected":5`.
   Change nothing else on either line. Keep the file's line endings.
3. `values_test.go`: the test at ~213 that requires line 1 to emit every metric now also covers `connections.rejected` with no edit. Add one assertion beside ~226: `byMetric(2, "connections.rejected")` is non-nil with `*e.Value == 5` and `e.Unit == "count"`.
4. `internal/evidence/caseexport_enums.go` ~525: add `"connections.rejected"` to the serverStatus metric fold, in the same sorted or grouped position as `connections.current`.
5. `internal/resource/metrics.go`:
   - after the `MongoConns` const (~44): `MongoConnsRejected = "mongo.conns_rejected"` with the comment "incoming connections the server refused (cumulative, 6.3 and later)".
   - after the `MongoConns` rule (~224):
     ```go
     {id: MongoConnsRejected, sources: mongoSS, kind: "metric", unit: "count", cumulative: true, when: []cond{metricIs("connections.rejected")}},
     ```
     Read the `MongoConns` rule first and copy its field names; `cumulative` is a field of the rule struct (~84–101).
6. `internal/resource/resource_test.go`:
   - the lookup table near ~227: add a row for `connections.rejected` → `MongoConnsRejected`, shaped like the `connections.current` row;
   - the sorted id list near ~252–256: insert `"mongo.conns_rejected"` right after `"mongo.conns"`.
   - add `TestMongoConnsRejectedDeltas`: two serverStatus metric events 1 minute apart with values 0 and 5 → the series has two samples; the second has `HasDelta` and `Delta == 5`, no `Reset`. Copy the event-building helper the file already uses for a cumulative metric (grep `cumulative` or `HasDelta` in the test file).
7. gofmt every touched Go file.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/importer/mongoexports/... ./internal/evidence/... ./internal/resource/...
.dev\toolchains\go\bin\go.exe build ./...
```

PASS.

## Depends on

38 (last earlier writer of `metrics.go` and `resource_test.go`).
