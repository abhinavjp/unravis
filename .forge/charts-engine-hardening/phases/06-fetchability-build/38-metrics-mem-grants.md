# TSK-38 metrics-mem-grants

risk: low — one new metric rule and its tests.

## Outcome

A PDH counter `SQLServer:Memory Manager\Memory Grants Pending` (or a named instance `MSSQL$X:Memory Manager`) becomes the catalogue metric `sql.mem.grants_pending`, so D6 can read it (TSK-42).

## Write scope

- `internal/resource/metrics.go`
- `internal/resource/resource_test.go`

## Must not change

- Every existing rule and metric id.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `metrics.go` const block (~16–58): add `SQLMemGrantsPending = "sql.mem.grants_pending"` after `MongoWTDirty` (~45).
2. After `objMemory` (~142) add:
   ```go
   // objectSQLMemMgr holds for "SQLServer:Memory Manager" and a named instance's
   // "MSSQL$NAME:Memory Manager".
   func objectSQLMemMgr(a map[string]string) bool {
       return strings.HasSuffix(strings.ToLower(a["object"]), ":memory manager")
   }
   ```
   (place it as a top-level func next to `objectASPNET` ~121 if a func inside a `var` block does not compile; it must not be inside the `var (...)` block.)
3. `rules` (~153–234): after the Mongo block (~227) add:
   ```go
   // SQL Server memory grants waiting (D6).
   {id: SQLMemGrantsPending, sources: pdh, kind: "counter", unit: "count", when: []cond{objectSQLMemMgr, counter("Memory Grants Pending")}},
   ```
4. `resource_test.go` lookup table (~180–235): add
   ```go
   {"sql grants pending", pdh("sql-a", tm, "SQLServer:Memory Manager", "", "Memory Grants Pending", 12, ""),
       resource.Metric{ID: resource.SQLMemGrantsPending, Unit: "count", Scale: 1}, true},
   {"sql grants pending named", pdh("sql-a", tm, "MSSQL$PAYROLL:Memory Manager", "", "Memory Grants Pending", 0, ""),
       resource.Metric{ID: resource.SQLMemGrantsPending, Unit: "count", Scale: 1}, true},
   {"sql grants outstanding not mapped", pdh("sql-a", tm, "SQLServer:Memory Manager", "", "Memory Grants Outstanding", 3, ""),
       resource.Metric{}, false},
   ```
5. `resource_test.go` ~252–256 `want` list: insert `"sql.mem.grants_pending"` after `"redis.ops"` (the list is sorted).
6. gofmt both files.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/resource/...
```

PASS.

## Depends on

Phase 02 complete (ordering only).
