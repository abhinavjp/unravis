// output.go is the C9 data-shape control: fixed output structs for every SQL
// sampler, a column allowlist mapping the SELECTed columns onto them, and
// decodeRows, which rejects unknown columns, validates wait types and enforces
// the C6 per-kind row caps (stop at cap+1, mark truncated). Only numeric, enum
// and wait-type data can be represented: no database names, paths, logins,
// hosts, programs, replica names or query text.
package bbsql

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
)

// C6 per-kind row caps. Every row-returning statement carries TOP n in SQL
// (defense in depth); the reader stops at n+1 and decodeRows keeps n and
// marks truncated. Never dbhealth's 5000 cap.
const (
	CapRequests   = 256 // TOP 256 in bb:requests
	CapWaits      = 20  // TOP 20 in bb:waits
	CapSchedulers = 1   // summed in SQL
	CapSemaphores = 16  // TOP 16 in bb:semaphores
	CapTempdb     = 1   // summed in SQL
	CapFileStats  = 256 // TOP 256 in bb:filestats
	CapAG         = 64  // TOP 64 in bb:ag
)

// statementCap maps an allowlisted statement to its C6 row cap. Unknown text
// gets the smallest cap (fail closed); the send gates only pass known text.
func statementCap(text string) int {
	switch text {
	case stmtRequests:
		return CapRequests
	case stmtWaits:
		return CapWaits
	case stmtSchedulers:
		return CapSchedulers
	case stmtSemaphores:
		return CapSemaphores
	case stmtTempdb:
		return CapTempdb
	case stmtFilestats:
		return CapFileStats
	case stmtAG:
		return CapAG
	default:
		return 1
	}
}

// WaitTypeRunning is the fixed sentinel for running (non-waiting) requests:
// bb:requests emits '' (ISNULL(wait_type,'')), which the strict C9 regex
// rejects. Only the requests decode path maps '' to it before validation;
// the regex itself is never loosened.
const WaitTypeRunning = "RUNNING"

// waitTypeRe is the C9 wait-type shape (uppercase letters, digits, underscore).
var waitTypeRe = regexp.MustCompile("^[A-Z0-9_]{1,60}$")

// Fixed decode errors: no column names, row values or driver text ever leak.
var (
	errDst           = errors.New("bbsql: dst must be a pointer to a struct slice")
	errUnknownColumn = errors.New("bbsql: unknown column")
	errWaitType      = errors.New("bbsql: invalid wait type")
	errDecode        = errors.New("bbsql: row decode failed")
)

// Requests (bb:requests, BL-101): running-at-once grouped by wait type.
type Requests struct {
	WaitType     string `json:"waitType"`
	Tasks        uint32 `json:"tasks"`
	MaxWaitMs    uint64 `json:"maxWaitMs"`
	Blocked      uint32 `json:"blocked"`
	HeadBlockers uint32 `json:"headBlockers"`
	MaxBlockMs   uint64 `json:"maxBlockMs"`
}

// Waits (bb:waits, BL-102): decode holds the cumulative wait_time_ms /
// waiting_tasks_count; the sampler (BBX-5-12) turns them into deltas in Go.
type Waits struct {
	WaitType   string `json:"waitType"`
	DeltaMs    uint64 `json:"deltaMs"`
	DeltaTasks uint64 `json:"deltaTasks"`
}

// Schedulers (bb:schedulers, BL-118): VISIBLE ONLINE totals summed in SQL.
type Schedulers struct {
	CurrentTasks  uint32 `json:"currentTasks"`
	RunnableTasks uint32 `json:"runnableTasks"`
}

// Semaphores (bb:semaphores, BL-118): counts per resource semaphore; the
// sampler (BBX-5-13) computes deltas in Go keyed by ResourceSemaphoreID.
type Semaphores struct {
	ResourceSemaphoreID int32 `json:"resourceSemaphoreId"`
	Grantees            int32 `json:"grantees"`
	Waiters             int32 `json:"waiters"`
	Forced              int32 `json:"forced"`
	Timeouts            int32 `json:"timeouts"`
}

// Tempdb (bb:tempdb, BL-118): page totals summed in SQL.
type Tempdb struct {
	InternalPages uint64 `json:"internalPages"`
	UserPages     uint64 `json:"userPages"`
}

// FileStats (bb:filestats, BL-119): per-file I/O, ids only (no paths/names).
type FileStats struct {
	DatabaseID      uint32 `json:"databaseId"`
	FileID          uint32 `json:"fileId"`
	ReadStallMs     uint64 `json:"readStallMs"`
	WriteStallMs    uint64 `json:"writeStallMs"`
	Reads           uint64 `json:"reads"`
	Writes          uint64 `json:"writes"`
	SizeOnDiskBytes uint64 `json:"sizeOnDiskBytes,omitempty"`
}

// AG (bb:ag, BL-119): AG database replica state, ids/enum only (no replica
// server names).
type AG struct {
	DatabaseID uint32 `json:"databaseId"`
	SyncState  uint8  `json:"syncState"`
}

// columnKeys maps every SELECTed column of the allowlisted statements to its
// fixed struct's JSON key. A column absent here is rejected outright; the
// struct's fixed fields (DisallowUnknownFields) are the second gate.
var columnKeys = map[string]string{
	"wait_type": "waitType", "tasks": "tasks", "max_wait_ms": "maxWaitMs",
	"blocked": "blocked", "head_blockers": "headBlockers", "max_block_ms": "maxBlockMs",
	"wait_time_ms": "deltaMs", "waiting_tasks_count": "deltaTasks",
	"current_tasks": "currentTasks", "runnable_tasks": "runnableTasks",
	"resource_semaphore_id": "resourceSemaphoreId", "grantee_count": "grantees",
	"waiter_count": "waiters", "forced_grant_count": "forced", "timeout_count": "timeouts",
	"internal_pages": "internalPages", "user_pages": "userPages",
	"database_id": "databaseId", "file_id": "fileId",
	"io_stall_read_ms": "readStallMs", "io_stall_write_ms": "writeStallMs",
	"num_of_reads": "reads", "num_of_writes": "writes", "size_on_disk_bytes": "sizeOnDiskBytes",
	"synchronization_state": "syncState",
}

// decodeRows decodes rows into the fixed struct slice pointed to by dst,
// rejecting any unknown column (C9). It stops at cap+1 rows, keeps cap and
// reports truncated (C6). WaitType fields are validated against waitTypeRe;
// only Requests maps the empty wait type to WaitTypeRunning first. dst is
// untouched on error. Bounded decode: no goroutines, no unbounded state.
func decodeRows(rows []Row, dst interface{}, cap int) (truncated bool, err error) {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Slice || rv.Elem().Type().Elem().Kind() != reflect.Struct {
		return false, errDst
	}
	out := rv.Elem()
	for i, row := range rows {
		if i >= cap {
			truncated = true
			break
		}
		elem := reflect.New(out.Type().Elem())
		if err := decodeRow(row, elem.Interface()); err != nil {
			return false, err
		}
		switch v := elem.Interface().(type) {
		case *Requests:
			if v.WaitType == "" {
				v.WaitType = WaitTypeRunning
			}
			if !waitTypeRe.MatchString(v.WaitType) {
				return false, errWaitType
			}
		case *Waits:
			if !waitTypeRe.MatchString(v.WaitType) {
				return false, errWaitType
			}
		}
		out = reflect.Append(out, elem.Elem())
	}
	rv.Elem().Set(out)
	return truncated, nil
}

// decodeRow maps one row's columns through columnKeys and decodes it into a
// fixed struct with DisallowUnknownFields. Errors are fixed codes only.
func decodeRow(row Row, dst interface{}) error {
	keyed := make(map[string]any, len(row))
	for col, v := range row {
		key, ok := columnKeys[col]
		if !ok {
			return errUnknownColumn
		}
		if b, ok := v.([]byte); ok {
			v = string(b)
		}
		keyed[key] = v
	}
	data, err := json.Marshal(keyed)
	if err != nil {
		return errDecode
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errDecode
	}
	return nil
}