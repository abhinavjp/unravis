# P-34 Wire the live field names for database, Redis and web-call totals

model: sonnet, effort: medium. Reason: a table edit, but each path must be proven from the probe file and is read from customer data.
risk: high. Review batch: RB-3, then RB-6. Wave: W2. Requirement: R-08 (BL-012).

BLOCKED UNTIL: `screen/probe/fields.md` (P-01) exists and says "live-verified". If it says BLOCKED or every role is "none found", do NOT edit code: report "nothing to wire" and stop. The columns then keep saying "not recorded", which is the correct live behaviour.

## Outcome
One record's detail (`perf` block) returns real `sqlMs`, `sqlCount`, `redisMs`, `redisCount`, `httpMs`, `httpCount` (and `hitCount` if found) for live audit records that store them.

## Write scope
- `internal/store/record_perf.go` (table `recordPerfFields`, lines 42-57, only)
- `internal/store/record_perf_test.go`

## Must not change
- The `perfField` struct, the projection builder (line 140), the decoder (line 223), `perfLimit`. Rows already wired (`_perfFilterTotalMs`, `_perfFilters`). `_perfSlowSql` / `_perfSlowHttp` / `_perfTextStored` stay unwired here (text fields; D3 / L-23 owns them). No text subfield is ever projected.

## Changes
1. Read `screen/probe/fields.md`, table `role | chosen path | why`.
2. For each of `_perfHitCount, _perfSqlCount, _perfSqlMs, _perfRedisCount, _perfRedisMs, _perfHttpCount, _perfHttpMs` whose chosen path is a real path with a number type: set `mongoPath: "<path from probe/fields.md>"` on that row. A role with `none found` is left as it is. Do not invent or "fix" a path.
3. If the stored unit is not milliseconds (the probe's "why" column says seconds or ticks), STOP and report: a unit conversion is a separate decision.
4. `_perfSteps` / `_perfPressure`: wire only if the probe names the list path AND both element fields (`subA` name, `subB` number). Otherwise leave.
5. Tests in `record_perf_test.go`: follow the existing table test at line 99; for each newly wired row add a document with the field as a number (decoded value equals it), as a string (decoded nil), and missing (decoded nil). No real customer value in fixtures: use made-up numbers.
6. Report the rows you wired as `key → path` and the rows left unwired.

## Proof
- [ ] `export PATH="$PWD/.dev/toolchains/go/bin:$PATH" && go build ./... && go vet ./internal/store/` → no output.
- [ ] `go test ./internal/store/ -run 'Perf' -count=1` → `ok`.
- [ ] `go test ./internal/store/ -count=1` → `ok`.
- [ ] `git diff -- internal/store/record_perf.go` shows only `mongoPath:` additions inside `recordPerfFields`.
- [ ] Every wired path appears verbatim in `probe/fields.md`. Report says "paths from a live probe"; the value check on live data is P-46.

## Depends on
P-01 (result), P-17 (store package order: P-17 → P-34 → P-36 → P-38).
