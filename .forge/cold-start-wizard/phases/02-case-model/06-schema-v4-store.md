# TSK-06: Schema v4 and store methods for environments and source tags
risk: high — migrates encrypted case data; additive only.

## Outcome
`schemaVersion` becomes 4. A new table `case_sources(id INTEGER PRIMARY KEY, import_id INTEGER, kind TEXT NOT NULL, label TEXT, env TEXT, portal TEXT, api TEXT, classification TEXT, created_ms INTEGER NOT NULL)` and `case_envs(name TEXT PRIMARY KEY, ord INTEGER)` exist. The store can add, list and remove tags and environments (R5, R13). A v3 case opens, migrates once, and every existing read returns exactly what it did before.

## Write scope
- `internal/casestore/store.go` (add a `Tagger` interface: `SetEnvs`, `Envs`, `AddSource`, `Sources`, `RemoveSource`; keep `Store`/`Case` unchanged unless `Case` embeds `Tagger`)
- `internal/casestore/sqlite/schema.go`, `internal/casestore/sqlite/discover.go` (migrate), new `internal/casestore/sqlite/tags.go`
- `internal/casestore/memory.go` (same methods in the in-memory store)
- `internal/casestore/storetest/` (contract tests that both stores must pass)
- `internal/casestore/sqlite/tags_test.go`, `internal/casestore/sqlite/testdata/` (a v3 case fixture, if the existing tests do not already create one)

## Must not change
- Existing tables, columns and the meaning of existing reads; case id format; encryption; `CaseInfo` fields already serialised to the client (new fields are additive).
- The lazy attribute-index build (`ensureAttrIndexes`) and open-time cost: the migration must add only two small tables.

## Changes
1. `schemaSQL` creates both tables for new cases; `migrate` adds them for `from < 4` using `CREATE TABLE IF NOT EXISTS`, then writes version 4 in the same transaction.
2. Bounds: env name ≤ 60 chars, label/portal/api ≤ 80 chars, ≤ 32 environments, ≤ 200 sources per case; control characters rejected; leading/trailing spaces trimmed; duplicate env names (case-insensitive) collapse to the first spelling; classification is one of `""`, `audit`, `other`, `unused`.
3. A source row stores no path, connection string, host credential or secret — only the words the user chose (R14). Document that in the type comment.
4. `Info()` gains `Envs int` and `Sources int` counts (additive fields).

## Proof
- Migration test: create a v3 case (fixture or by pinning schema version in a helper), open on the new build, assert version 4, existing imports/events unchanged, tables present; reopen is a no-op.
- Contract tests pass for memory and sqlite stores: add/list/remove, bounds, duplicates, ordering, closed case refuses writes.
- Rollback note verified: a v4 case opened by a build that only knows v3 is refused by the existing "newer build" guard (no silent corruption) — assert in test.
- `go test ./internal/casestore/...` green.

## Depends on
none
