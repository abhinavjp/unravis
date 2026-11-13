# P-36 Project the light totals in list queries (live path)

model: sonnet, effort: high. Reason: changes the main list pipeline that runs on the customer's MongoDB 3.4.
risk: high. Review batch: RB-3, then RB-6. Wave: W3. Requirements: R-08, R-11 (BL-012).

BLOCKED UNTIL: P-34 wired at least one of `_perfSqlMs`, `_perfRedisMs`, `_perfHttpMs`. If P-34 reported "nothing to wire", skip this packet and report "skipped: no live field"; rows then have no `light` block and the UI says "not recorded".

## Outcome
Rows returned by the list pipeline (Nearby calls, slowest) carry `light` with the totals the record stores. A record without the fields has no `light` block.

## Write scope
- `internal/store/requests.go` (`requestPipelineOrdered`, lines 411-489, and the row decoder it feeds)
- `internal/store/record_perf.go` (one small exported-in-package helper only, no table change)
- `internal/store/requests_test.go`

## Must not change
- Sort, cursor, limit and match stages of the pipeline (paging must stay byte-identical for rows without the fields). The projection of text fields: no payload, header or SQL text is added. Mongo 3.4 only: no `$toDouble`, `$convert`, `$expr` in `$match`, no `$lookup` pipeline form.

## Changes
1. `record_perf.go`: add `func lightProjection() bson.D` that returns the projection entries for the keys `_perfSqlMs`, `_perfRedisMs`, `_perfHttpMs` only, built by the SAME per-field code the detail projection uses at line 140 (extract the loop body into a helper and call it from both; do not copy it). An unwired key projects a literal null, as today.
2. `requests.go`: in the final `$project` (or `$addFields`-free equivalent; 3.4 has `$addFields`, check which this pipeline already uses) of `requestPipelineOrdered`, append `lightProjection()` when `summary` is false or always if the row decoder ignores unknown keys (read the decoder and decide; say which).
3. Row decoder: after decoding the row, read the three `_perf*` keys with the existing number reader used at line 223 (number types only; string, missing, NaN, negative → nil). If all three are nil → `row.Light = nil`; otherwise `row.Light = &audit.RecordLight{…}`.
4. Cost check: the three fields are top-level scalars read from documents the pipeline already fetches; no new stage that scans. Write one sentence in the report that says this, with the stage list before and after.
5. Tests (no live DB; use the package's existing pipeline-shape tests and decoder tests): pipeline contains the three keys in its last stage; a decoded row with numbers has `Light` set; with strings → nil; with none → nil and marshalled JSON has no `light`; the cursor test still passes unchanged.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/store/` → no output.
- [ ] `go test ./internal/store/ -run 'Request|Perf|Activity' -count=1` → `ok`.
- [ ] `go test ./internal/store/ ./internal/server/ -count=1` (server in background) → `ok`.
- [ ] `grep -n "toDouble\|\$convert\|\$expr" internal/store/requests.go internal/store/record_perf.go` → no new hit.
- [ ] Live check is P-46 (a Nearby row shows a database time that equals the same record's detail). Not live-verified here.

## Depends on
P-34, P-35. Store package order: after P-34, before P-38.
