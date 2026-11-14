# TASK — BBX-5-14: `internal/bbsql/filestats.go` + `internal/bbsql/filestats_test.go`

Two samplers (BL-119): file I/O stall deltas by (database_id, file_id) every 60 s, and AG
replica state every 60 s but ONLY when the instance has HADR enabled. No paths, no database
names, no replica server names.

## NEW CONTRACT ADDITION — the gate (implemented by the orchestrator in runner.go; code
## against it exactly, it is not in the static block)

```go
// Gate is an optional Sampler interface. A gated sampler names an allowlisted control
// statement through GateID; the runner sends it BEFORE the sampler's own statement and
// calls GateOpen(rows) on the result. When GateOpen is false the sampler is skipped for
// this tick (nothing is written, lastRun still advances) — used for features that are
// absent on this instance, so their DMV is never queried. GateOpen must fail closed.
type Gate interface {
	GateID() string
	GateOpen(rows []Row) bool
}
```
The gate statement is id `"bb:hadr"`: one row, one column `hadr`, value `1` when
`SERVERPROPERTY('IsHadrEnabled')` is on. The orchestrator adds it to the allowlist and adds
the column key `"hadr" -> "hadr"`; you must not edit allowlist.go.

File 1 (`filestats.go`, ~200 lines). Exact names:
```go
type filestatsSampler struct{ prev map[fileKey]filestatsCumulative }
type fileKey struct{ databaseID, fileID uint32 }
type filestatsCumulative struct{ readStallMs, writeStallMs, reads, writes uint64 }
type agSampler struct{}
type hadrFlag struct{ Hadr int64 `json:"hadr"` }   // decode target for the gate row
func (filestatsSampler) ID() string { return "bb:filestats" } // Kind "filestats", DemotedInterval
func (agSampler) ID() string { return "bb:ag" }               // Kind "ag", DemotedInterval
func (agSampler) GateID() string { return "bb:hadr" }
```
- `filestatsSampler.Decode`: cumulative values via `decodeRows` into `[]FileStats`
  (`CapFileStats`), then per (database_id, file_id): first sighting sets the baseline and is
  skipped; current < prev → reset the baseline, skip the key (SQL restart/role change);
  otherwise emit deltas for `ReadStallMs`, `WriteStallMs`, `Reads`, `Writes` while
  `SizeOnDiskBytes` stays the CURRENT value (a gauge, not a counter — comment it). `prev` is
  rebuilt from this cycle's rows so it is bounded by `CapFileStats`; keys that disappear
  (offline or dropped files) drop out. Nil payload on the first cycle; truncated passed through.
- `agSampler.Decode`: `decodeRows` into `[]AG` with `CapAG`; pass through unchanged (state
  is an enum, no delta); empty → nil.
- `agSampler.GateOpen(rows)`: decode `[]hadrFlag` with cap 1 through `decodeRows`; return
  `len(out) == 1 && out[0].Hadr == 1`. Any error, empty rows or an unreadable value → false
  (fail closed: no AG query is ever sent when the gate cannot be read). Document that a
  Standard-edition instance therefore produces NO `ag` payload instead of an error.
- `init()` registers both. One line: "stateful samplers are not concurrent; the runner
  serialises them."

File 2 (`filestats_test.go`, ~200 lines, `package bbsql`, own helpers `fileStatsRows`,
`agRows`, `hadrRows`):
- `TestFileStatsFirstCycleNil` — first Decode → nil payload; second identical Decode → nil
  too (all deltas zero ⇒ keys with zero deltas in all four counters are dropped, never
  emitted as zeros) — assert that rule explicitly.
- `TestFileStatsDelta` — +1000 read ms, +50 writes etc. → exact deltas, `SizeOnDiskBytes`
  equal to the current value (not a delta).
- `TestFileStatsReset` — a key whose cumulative value goes backwards is skipped for the
  cycle and its baseline is replaced.
- `TestFileStatsTop256Bounded` — `CapFileStats+1` rows → truncated true, exactly
  `CapFileStats` kept; after many cycles with shifting key sets `len(prev) <= CapFileStats`.
- `TestFileStatsNoPaths` — a row carrying `physical_name`, `database_name`, `name`,
  `file_handle` or `type_desc` → `errUnknownColumn`, nil payload.
- `TestAGGatedOff` — `hadrRows(0)` → `GateOpen` false; and no `bb:ag` decoding attempted.
- `TestAGGateFailClosed` — empty gate rows, a `nil`/unreadable `hadr` value, and an unknown
  column → `GateOpen` false in every case.
- `TestAGGatedOn` — `hadrRows(1)` → true, and `AG` rows decode to `{databaseId, syncState}`.
- `TestAGNoReplicaNames` — a row carrying `replica_server_name`, `role_desc`,
  `availability_group_id`... use `replica_server_name` and `joined_state_desc`: rejected.
- `TestFileStatsRegistered` — kinds "filestats" and "ag", both `DemotedInterval`, IDs
  resolve through `Statement`, and `var _ Gate = (*agSampler)(nil)` compiles.

Reply with TWO fenced ```go blocks — first `filestats.go`, then `filestats_test.go` — each
preceded by one line giving the path. No third block. (Overrides the one-block rule.)
