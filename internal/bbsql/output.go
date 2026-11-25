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

// C6 per-kind row caps: the number of rows KEPT. Each sample statement fetches
// TOP n+1 in SQL so the extra row is a real truncation witness; the reader stops
// at n+1 and decodeRows keeps n and marks truncated. With TOP n the witness could
// never arrive and an over-cap result looked complete. Never dbhealth's 5000 cap.
const (
	CapRequests   = 256 // bb:requests fetches TOP 257, keeps 256
	CapWaits      = 20  // bb:waits fetches TOP 21, keeps 20
	CapSchedulers = 1   // summed in SQL
	CapSemaphores = 16  // bb:semaphores fetches TOP 17, keeps 16
	CapTempdb     = 1   // summed in SQL
	CapFileStats  = 256 // bb:filestats fetches TOP 257, keeps 256
	CapAG         = 64  // bb:ag fetches TOP 65, keeps 64
	// CapCeilingPerms mirrors the TOP 2 in bb:ceiling_perms: two permissions
	// outside the ceiling are already a refusal, so the query never needs to
	// report more, and the reader must not silently stop below what is sent.
	CapCeilingPerms = 2
	// CapSingleRow is the bound of a statement that answers with exactly one
	// row by construction (a TOP 1 guard, or a scalar/SUM select with no GROUP
	// BY). Listed per statement, never by falling through: a statement that
	// forgets its own cap would otherwise be read as one row and silently
	// truncated.
	CapSingleRow = 1
)

// statementCap maps an allowlisted statement to its C6 row cap. Unknown text
// gets the smallest cap (fail closed); the send gates only pass known text.
// TestEveryStatementHasItsOwnRowCap proves this table agrees with the TOP n+1
// written into each sample statement (TOP n for the control statements).
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
	case stmtCeilingPerms:
		return CapCeilingPerms
	case stmtHadr, stmtCeilingRole:
		return CapSingleRow
	default:
		// Extras (extras.go) register their own cap next to their statement.
		if c, ok := extraCaps[text]; ok {
			return c
		}
		return CapSingleRow
	}
}

// WaitTypeRunning is the fixed sentinel for running (non-waiting) requests:
// bb:requests emits ” (ISNULL(wait_type,”)), which the strict C9 regex
// rejects. Only the requests decode path maps ” to it before validation;
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
	// errMissingColumn: a required column is absent from the row. DisallowUnknownFields
	// rejects extras but not absences, so a missing reading would otherwise decode
	// to a factual zero (W11-04).
	errMissingColumn = errors.New("bbsql: required column missing")
	// errNullColumn: a non-nullable required column carried SQL NULL.
	errNullColumn = errors.New("bbsql: required column is null")
	// errEnumDomain: a value is outside its confirmed enum domain (AG sync state).
	errEnumDomain = errors.New("bbsql: value outside enum domain")
)

// agSyncStateMax is the highest confirmed sys.dm_hadr_database_replica_states
// synchronization_state: 0 NOT_SYNCHRONIZING, 1 SYNCHRONIZING, 2 SYNCHRONIZED,
// 3 REVERTING, 4 INITIALIZING. A future/unsupported state is refused with a
// fixed code rather than persisted as an arbitrary number (W11-04).
const agSyncStateMax = 4

// colRule is the per-column contract of one output type: required columns must
// be present, and a non-nullable column must not carry SQL NULL. Validation runs
// BEFORE conversion so an absent/null reading can never become a struct zero.
type colRule struct{ required, nullable bool }

// rowSpec is the exact column contract of one output type. summed marks the
// statements that SUM in SQL (schedulers, tempdb): an empty set returns ONE row
// of NULLs, which is a legitimate "nothing to add up" (missing), not an error —
// but a PARTIAL row (some required columns absent) is still a schema defect.
type rowSpec struct {
	cols   map[string]colRule
	summed bool
}

func req() colRule     { return colRule{required: true} }
func reqNull() colRule { return colRule{required: true, nullable: true} }

// rowSpecs pins the required columns and per-column nullability of every output
// type, keyed by the struct the row decodes into. semaphoresRaw is the wide
// cumulative input type (W11-01); its counters are nullable int64.
var rowSpecs = map[reflect.Type]rowSpec{
	reflect.TypeOf(Requests{}): {cols: map[string]colRule{
		"waitType": req(), "tasks": req(), "maxWaitMs": req(),
		"blocked": req(), "headBlockers": req(), "maxBlockMs": req(),
	}},
	reflect.TypeOf(Waits{}): {cols: map[string]colRule{
		"waitType": req(), "deltaMs": req(), "deltaTasks": req(),
	}},
	reflect.TypeOf(Schedulers{}): {summed: true, cols: map[string]colRule{
		"currentTasks": reqNull(), "runnableTasks": reqNull(),
	}},
	reflect.TypeOf(Tempdb{}): {summed: true, cols: map[string]colRule{
		"internalPages": reqNull(), "userPages": reqNull(),
	}},
	reflect.TypeOf(Semaphores{}): {cols: map[string]colRule{
		"poolId": {nullable: true}, "resourceSemaphoreId": req(), "grantees": req(), "waiters": req(),
		"forced": reqNull(), "timeouts": reqNull(),
	}},
	reflect.TypeOf(semaphoresRaw{}): {cols: map[string]colRule{
		"poolId": {nullable: true}, "resourceSemaphoreId": req(), "grantees": req(), "waiters": req(),
		"forced": reqNull(), "timeouts": reqNull(),
	}},
	reflect.TypeOf(FileStats{}): {cols: map[string]colRule{
		"databaseId": req(), "fileId": req(), "readStallMs": req(),
		"writeStallMs": req(), "reads": req(), "writes": req(), "sizeOnDiskBytes": req(),
	}},
	reflect.TypeOf(AG{}): {cols: map[string]colRule{
		"databaseId": req(), "syncState": req(),
	}},
	reflect.TypeOf(hadrFlag{}): {cols: map[string]colRule{
		"hadr": req(),
	}},
}

// validateRowSpec enforces presence and nullability on the JSON-keyed row before
// it is converted. Unknown columns are rejected separately (columnKeys +
// DisallowUnknownFields); this gate is about ABSENT and NULL required data.
func validateRowSpec(keyed map[string]any, spec rowSpec) error {
	if spec.summed && len(keyed) == 0 {
		return nil // empty-SUM row: the sampler reports it missing, never zero
	}
	for key, rule := range spec.cols {
		v, present := keyed[key]
		if !present {
			if rule.required {
				return errMissingColumn
			}
			continue
		}
		if v == nil && !rule.nullable {
			return errNullColumn
		}
	}
	return nil
}

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
// sampler (BBX-5-13) computes deltas in Go keyed by the composite
// (PoolID, ResourceSemaphoreID) identity (W11-02).
//
// PoolID is the numeric resource-pool id (the DMV's int NOT NULL pool_id). It is
// part of the identity because resource_semaphore_id repeats in every pool, so
// it is emitted beside the semaphore id. A numeric pool id is NOT a pool name:
// no pool name/text is ever read or stored (C9). It is nullable/omitted only
// when a reply carried no readable pool id (the fixed statement always projects
// it); an unreadable identity is missing, never a fabricated 0.
//
// Grantees/Waiters are instantaneous gauges and are always present. Forced and
// Timeouts are DELTAS of the cumulative DMV counters and are nullable: a nil
// pointer (omitted from the JSON) means the delta is unreadable this cycle —
// the cumulative counter was SQL NULL, went backwards, or its difference does
// not fit the fixed int32 field. The gauge beside an unreadable counter is still
// emitted (W11-04): a nonapplicable nullable counter must not blank out a
// readable grantee/waiter count. The emitted WIDTH is unchanged (int32); only
// missingness was added, because "unreadable means missing, never 0". A measured
// zero delta is a non-nil pointer to 0 and stays present.
type Semaphores struct {
	PoolID              *int32 `json:"poolId,omitempty"`
	ResourceSemaphoreID int32  `json:"resourceSemaphoreId"`
	Grantees            int32  `json:"grantees"`
	Waiters             int32  `json:"waiters"`
	Forced              *int32 `json:"forced,omitempty"`
	Timeouts            *int32 `json:"timeouts,omitempty"`
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
	"pool_id":               "poolId",
	"resource_semaphore_id": "resourceSemaphoreId", "grantee_count": "grantees",
	"waiter_count": "waiters", "forced_grant_count": "forced", "timeout_count": "timeouts",
	"internal_pages": "internalPages", "user_pages": "userPages",
	"database_id": "databaseId", "file_id": "fileId",
	"hadr":             "hadr",
	"io_stall_read_ms": "readStallMs", "io_stall_write_ms": "writeStallMs",
	"num_of_reads": "reads", "num_of_writes": "writes", "size_on_disk_bytes": "sizeOnDiskBytes",
	"synchronization_state": "syncState",
}

// decodeRows decodes rows into the fixed struct slice pointed to by dst,
// rejecting any unknown column (C9) and any absent/null required column or
// out-of-domain enum (W11-04). It stops at cap+1 rows, keeps cap and reports
// truncated (C6). WaitType fields are validated against waitTypeRe; only
// Requests maps a PRESENT empty wait type to WaitTypeRunning (a missing/null
// wait_type is refused before conversion, never turned into RUNNING). dst is
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
		case *AG:
			// Enforce the confirmed synchronization-state enum domain; an
			// out-of-domain value is a fixed-code refusal, never an arbitrary
			// persisted state (W11-04).
			if v.SyncState > agSyncStateMax {
				return false, errEnumDomain
			}
		}
		out = reflect.Append(out, elem.Elem())
	}
	rv.Elem().Set(out)
	return truncated, nil
}

// decodeRow maps one row's columns through columnKeys, validates the type's
// required-column/nullability contract BEFORE conversion, and decodes it into a
// fixed struct with DisallowUnknownFields. Errors are fixed codes only.
func decodeRow(row Row, dst interface{}) error {
	keyed := make(map[string]any, len(row))
	for col, v := range row {
		key, ok := columnKeys[col]
		if !ok {
			key, ok = extraColumnKeys[col]
		}
		if !ok {
			return errUnknownColumn
		}
		if b, ok := v.([]byte); ok {
			v = string(b)
		}
		keyed[key] = v
	}
	if spec, ok := rowSpecs[reflect.TypeOf(dst).Elem()]; ok {
		if err := validateRowSpec(keyed, spec); err != nil {
			return err
		}
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
