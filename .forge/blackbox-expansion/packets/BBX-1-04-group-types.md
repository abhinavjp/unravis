# BBX-1-04 Counter-group types and Sample fields (wire format)
Backlog: BL-104/106/108/100/107/109/110/116/111/112/114/115/113/120/125 | Group: 1 | Model: efficient | Reason: pure type declarations + struct fields, no logic; pre-created so later group packets touch disjoint files.
Depends on: none | Blocks: BBX-1-05, all counter-group packets

## Goal
Pre-create every host counter-group result struct and add the matching optional pointer fields to `Sample`, so each later group packet only writes its own group file. Missing groups stay nil (`omitempty`), never zero-filled.

## Context
- New file `internal/blackbox/groups_types.go` (all structs below). Edit `internal/blackbox/sample.go` (Sample struct lines 25-43) to add the pointer fields.
- Existing convention (`sample.go`): nil pointers + `omitempty`; a group that cannot be read is left out and its name appended to `Sample.Missing`. Follow it exactly.
- Rules that apply: **no text** (no query/request text, payloads, env vars, command lines; event log = IDs and numbers only); **unreadable means missing, never 0**. Pool/service/instance names and dump file names are fixed identifiers explicitly allowed by BL-104/117/120.
- These types are the contract; do not rename fields later without a format-version bump (`FormatVersion`, sample.go:9).

## Do (exact code)
1. Create `groups_types.go`, package `blackbox`, with these types (json tags exactly as shown; all measurement fields are pointers + omitempty so missing stays missing):
   ```go
   type HTTPGroup struct{ Queues []HTTPQueue `json:"queues,omitempty"` }
   type HTTPQueue struct {
       Pool             string   `json:"pool"`
       CurrentQueueSize *float64 `json:"currentQueueSize,omitempty"`
       MaxQueueItemAge  *float64 `json:"maxQueueItemAge,omitempty"`
       RejectedRequests *float64 `json:"rejectedRequests,omitempty"`
       ArrivalRate      *float64 `json:"arrivalRate,omitempty"`
   }
   type IISGroup struct{ Workers []IISWorker `json:"workers,omitempty"` }
   type IISWorker struct {
       Pool           string   `json:"pool"`
       PID            uint32   `json:"pid"`
       ActiveRequests *float64 `json:"activeRequests,omitempty"`
       ActiveThreads  *float64 `json:"activeThreads,omitempty"`
       PingLatencyMs  *float64 `json:"pingLatencyMs,omitempty"`
   }
   type DiskCPUGroup struct {
       Disks []DiskSplit `json:"disks,omitempty"`
       CPU   *CPUSplit   `json:"cpu,omitempty"`
   }
   type DiskSplit struct {
       Name           string   `json:"name"`
       ReadMs         *float64 `json:"readMs,omitempty"`
       WriteMs        *float64 `json:"writeMs,omitempty"`
       ReadsPerSec    *float64 `json:"readsPerSec,omitempty"`
       WritesPerSec   *float64 `json:"writesPerSec,omitempty"`
       SplitIOPerSec  *float64 `json:"splitIOPerSec,omitempty"`
   }
   type CPUSplit struct {
       QueueLength   *float64 `json:"queueLength,omitempty"`
       PrivilegedPct *float64 `json:"privilegedPct,omitempty"`
       DPCPct        *float64 `json:"dpcPct,omitempty"`
       InterruptPct  *float64 `json:"interruptPct,omitempty"`
   }
   type SQLHostGroup struct{ Instances []SQLHostInstance `json:"instances,omitempty"` }
   type SQLHostInstance struct {
       Instance               string   `json:"instance"`
       BatchRequestsPerSec    *float64 `json:"batchRequestsPerSec,omitempty"`
       UserConnections        *float64 `json:"userConnections,omitempty"`
       LockWaitsPerSec        *float64 `json:"lockWaitsPerSec,omitempty"`
       ProcessesBlocked       *float64 `json:"processesBlocked,omitempty"`
       MemoryGrantsPending    *float64 `json:"memoryGrantsPending,omitempty"`
       PageLifeExpectancy     *float64 `json:"pageLifeExpectancy,omitempty"`
       FreeListStallsPerSec   *float64 `json:"freeListStallsPerSec,omitempty"`
       CompilationsPerSec     *float64 `json:"compilationsPerSec,omitempty"`
       ReCompilationsPerSec   *float64 `json:"reCompilationsPerSec,omitempty"`
       DeadlocksPerSec        *float64 `json:"deadlocksPerSec,omitempty"`
       LockWaitTimeMs         *float64 `json:"lockWaitTimeMs,omitempty"`
       LogFlushWaitTimeMs     *float64 `json:"logFlushWaitTimeMs,omitempty"`
       ForwardedRecordsPerSec *float64 `json:"forwardedRecordsPerSec,omitempty"`
   }
   type CLRGroup struct{ Targets []CLRTarget `json:"targets,omitempty"` }
   type CLRTarget struct {
       Target           string   `json:"target"`
       Gen0Bytes        *uint64  `json:"gen0Bytes,omitempty"`
       Gen1Bytes        *uint64  `json:"gen1Bytes,omitempty"`
       Gen2Bytes        *uint64  `json:"gen2Bytes,omitempty"`
       LOHBytes         *uint64  `json:"lohBytes,omitempty"`
       PctTimeInGC      *float64 `json:"pctTimeInGC,omitempty"`
       AllocBytesPerSec *float64 `json:"allocBytesPerSec,omitempty"`
       ContentionPerSec *float64 `json:"contentionPerSec,omitempty"`
       Threads          *float64 `json:"threads,omitempty"`
       ExceptionsPerSec *float64 `json:"exceptionsPerSec,omitempty"`
   }
   type Proc10Group struct{ Targets []Proc10Target `json:"targets,omitempty"` }
   type Proc10Target struct {
       Target       string   `json:"target"`
       CPUPct       *float64 `json:"cpuPct,omitempty"`
       WorkingSet   *uint64  `json:"workingSet,omitempty"`
       PrivateBytes *uint64  `json:"privateBytes,omitempty"`
   }
   type ProcIOGroup struct{ Targets []ProcIOTarget `json:"targets,omitempty"` }
   type ProcIOTarget struct {
       Target            string   `json:"target"`
       IOReadBytesPerSec *float64 `json:"ioReadBytesPerSec,omitempty"`
       IOWriteBytesPerSec *float64 `json:"ioWriteBytesPerSec,omitempty"`
       PageFileBytes     *uint64  `json:"pageFileBytes,omitempty"`
       VirtualBytes      *uint64  `json:"virtualBytes,omitempty"`
       ElapsedSec        *float64 `json:"elapsedSec,omitempty"`
   }
   type EventsGroup struct{ Events []EventEntry `json:"events,omitempty"` }
   type EventEntry struct {
       Channel uint8     `json:"channel"` // fixed enum: 1=System, 2=Application
       EventID uint32    `json:"eventId"`
       Level   uint8     `json:"level"`
       Count   uint32    `json:"count"`
       T       time.Time `json:"t"`
   }
   type TCPGroup struct {
       RetransmitsPerSec *float64        `json:"retransmitsPerSec,omitempty"`
       ResetsPerSec      *float64        `json:"resetsPerSec,omitempty"`
       OutboundQueue     *float64        `json:"outboundQueue,omitempty"`
       States            []TCPStateCount `json:"states,omitempty"`
   }
   type TCPStateCount struct {
       State uint8  `json:"state"` // numeric MIB_TCP_STATE, never an address
       Count uint32 `json:"count"`
   }
   type ASPNETGroup struct {
       RequestsQueued     *float64 `json:"requestsQueued,omitempty"`
       RequestsCurrent    *float64 `json:"requestsCurrent,omitempty"`
       RequestsRejected   *float64 `json:"requestsRejected,omitempty"`
       RequestWaitTimeMs  *float64 `json:"requestWaitTimeMs,omitempty"`
       ApplicationRestarts *float64 `json:"applicationRestarts,omitempty"`
   }
   type MemPressureGroup struct {
       HardFaultsPerSec  *float64 `json:"hardFaultsPerSec,omitempty"`
       PoolPagedBytes    *uint64  `json:"poolPagedBytes,omitempty"`
       PoolNonPagedBytes *uint64  `json:"poolNonPagedBytes,omitempty"`
       PageFileUsagePct  *float64 `json:"pageFileUsagePct,omitempty"`
   }
   type VMGroup struct {
       StealPct     *float64 `json:"stealPct,omitempty"`
       BalloonBytes *uint64  `json:"balloonBytes,omitempty"`
   }
   type DumpsGroup struct{ Dumps []DumpEntry `json:"dumps,omitempty"` }
   type DumpEntry struct {
       Name   string    `json:"name"` // file name only, never content
       Size   int64     `json:"size"`
       MTime  time.Time `json:"mtime"`
       Target string    `json:"target,omitempty"`
   }
   ```
   Add `import "time"` to groups_types.go.
2. In `sample.go`, add these fields to `Sample` (after `Logs`, before `Missing`), each a nil-able pointer with omitempty:
   ```go
   HTTP        *HTTPGroup        `json:"http,omitempty"`
   IIS         *IISGroup         `json:"iis,omitempty"`
   DiskCPU     *DiskCPUGroup     `json:"diskCpu,omitempty"`
   SQLHost     *SQLHostGroup     `json:"sqlHost,omitempty"`
   CLR         *CLRGroup         `json:"clr,omitempty"`
   Proc10      *Proc10Group      `json:"proc10,omitempty"`
   ProcIO      *ProcIOGroup      `json:"procIO,omitempty"`
   Events      *EventsGroup      `json:"events,omitempty"`
   TCP         *TCPGroup         `json:"tcp,omitempty"`
   ASPNET      *ASPNETGroup      `json:"aspnet,omitempty"`
   MemPressure *MemPressureGroup `json:"memPressure,omitempty"`
   VM          *VMGroup          `json:"vm,omitempty"`
   Dumps       *DumpsGroup       `json:"dumps,omitempty"`
   ```

## Do not
- Do not add any logic, methods, or constructors here — types only.
- Do not use non-pointer measurement fields (would write 0 for missing).
- Do not add text fields beyond the fixed identifiers shown (pool/target/instance/name).
- Do not bump FormatVersion (additive omitempty fields are backward compatible).
- Do not touch existing Sample fields or ProcRow.

## Tests to write first
- `internal/blackbox/groups_types_test.go`: `TestSampleGroupsOmitWhenNil` — marshal a Sample with all group pointers nil → JSON contains none of the group keys; `TestSampleGroupRoundTrip` — set one group (e.g. TCP with a state count), marshal/unmarshal, assert equality and that a nil measurement field stays absent.

## Verification checklist
- [ ] go build ./... passes
- [ ] go vet ./... passes
- [ ] go test ./internal/blackbox/... -count=1 passes, tests above included
- [ ] Both build-tag variants compile (GOOS=windows and GOOS=linux go vet ./internal/blackbox/...)
- [ ] Footprint: types only, no goroutine/ticker
- [ ] Missing reading is omitted, never 0 (TestSampleGroupsOmitWhenNil; all measurement fields are pointers)
- [ ] No text captured (only fixed identifiers; EventEntry is IDs/numbers/time only)
- [ ] Existing sample/store tests still pass (additive fields)

## Live-check needed?
No. Wire format only.

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-1-04.md`.