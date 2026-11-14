# STATIC CONTEXT — unravis Blackbox, package `internal/bbsql` (Go 1.27), SQL samplers

You write ONE sampler file for a Go repository. You cannot see the repo and cannot run Go,
so code strictly against the surface below. Output must compile as-is.

## Existing surface (EXACT, already committed)

```go
type Row = map[string]any   // conn.go: type Row map[string]any

// the runner (internal/bbsql/runner.go, merged) owns the tick loop, the single
// connection, timeouts, back-off and NDJSON writing. A sampler is state-light:
// it decodes rows and may keep the previous cumulative snapshot for deltas.
type Sampler interface {
	ID() string                             // allowlisted statement id, e.g. "bb:waits"
	Kind() string                           // NDJSON key, e.g. "waits"
	Interval() time.Duration                // BaseInterval (15s) or DemotedInterval (60s)
	Decode(rows []Row) (any, bool, error)   // payload, truncated, fixed error
}
func RegisterSampler(s Sampler)  // call from init(); bad or duplicate entries are ignored

const ( BaseInterval = 15 * time.Second; DemotedInterval = 60 * time.Second )
func Statement(id string) (string, bool)
var Statements map[string]string   // ids: bb:requests, bb:waits, bb:schedulers,
                                   // bb:semaphores, bb:tempdb, bb:filestats, bb:ag
func decodeRows(rows []Row, dst interface{}, cap int) (truncated bool, err error)
   // dst: pointer to a slice of the fixed struct. Rejects unknown columns (C9),
   // validates wait types, keeps at most cap rows and reports truncated (C6).

const ( CapRequests=256; CapWaits=20; CapSchedulers=1; CapSemaphores=16;
        CapTempdb=1; CapFileStats=256; CapAG=64 )
const WaitTypeRunning = "RUNNING"   // decodeRows already maps '' -> RUNNING for Requests

type Requests   struct { WaitType string `json:"waitType"`;   Tasks uint32 `json:"tasks"`;
                         MaxWaitMs uint64 `json:"maxWaitMs"`;  Blocked uint32 `json:"blocked"`;
                         HeadBlockers uint32 `json:"headBlockers"`; MaxBlockMs uint64 `json:"maxBlockMs"` }
type Waits      struct { WaitType string `json:"waitType"`; DeltaMs uint64 `json:"deltaMs"`;
                         DeltaTasks uint64 `json:"deltaTasks"` }
type Schedulers struct { CurrentTasks uint32 `json:"currentTasks"`; RunnableTasks uint32 `json:"runnableTasks"` }
type Semaphores struct { ResourceSemaphoreID int32 `json:"resourceSemaphoreId"`; Grantees int32 `json:"grantees"`;
                         Waiters int32 `json:"waiters"`; Forced int32 `json:"forced"`; Timeouts int32 `json:"timeouts"` }
type Tempdb     struct { InternalPages uint64 `json:"internalPages"`; UserPages uint64 `json:"userPages"` }
type FileStats  struct { DatabaseID uint32 `json:"databaseId"`; FileID uint32 `json:"fileId"`;
                         ReadStallMs uint64 `json:"readStallMs"`; WriteStallMs uint64 `json:"writeStallMs"`;
                         Reads uint64 `json:"reads"`; Writes uint64 `json:"writes"`;
                         SizeOnDiskBytes uint64 `json:"sizeOnDiskBytes,omitempty"` }
type AG         struct { DatabaseID uint32 `json:"databaseId"`; SyncState uint8 `json:"syncState"` }
```

`decodeRows` returns its error values as package-level fixed errors; never wrap, never add
text. Numeric SQL values arrive as `int64`, `float64`, `bool`, `[]byte`, `nil` or
`json.Number` depending on the driver, so decoding goes through JSON in `decodeRows` — your
code must only pass `rows` to it, never type-assert driver values yourself.

## Rules that bind every sampler (the reviewer checks each)

- Never build, mutate or parameterise SQL. Use `Statement(id)`/`ID()` only.
- Never log or persist anything that is not a number, an enum or a validated wait type.
  Forbidden columns even as INPUT: session_id, login_name, host_name, program_name,
  text, sql_text, query_plan, handles, statement offsets, resource_description,
  physical_name, database names, replica_server_name. Unknown columns are rejected by
  `decodeRows` — that is the gate, do not bypass it by pre-filtering the map.
- Deltas are computed in Go, never in SQL. `prev` state must be BOUNDED by the statement's
  row cap; drop keys not seen this cycle. A counter going backwards (SQL restart /
  rollover) resets the baseline and that key is SKIPPED for that cycle — never a negative
  or zero delta faked from nothing.
- First sample with no baseline returns `nil` (the field is omitted), never zeros.
- Empty rows -> `nil, false, nil`.
- No goroutines, no tickers, no time, no context in a sampler. The runner owns scheduling.
- Samplers are NOT safe for concurrent use and are never called concurrently: guard nothing
  with a mutex, and say so in one comment line.
- Register in `init()` with `RegisterSampler(&x{})`.

## Output format (STRICT)

Reply with exactly one fenced ```go block containing the complete file (package `bbsql`,
full import block, no placeholders). Then at most 8 short bullets: assumptions and the
points a reviewer must double-check.
