package bbsql

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// requestRow is one full bb:requests row (the exact SELECTed columns).
func requestRow(waitType string) Row {
	return Row{
		"wait_type": waitType, "tasks": int64(2), "max_wait_ms": int64(50),
		"blocked": int64(1), "head_blockers": int64(1), "max_block_ms": int64(40),
	}
}

// waitsRow is one full bb:waits row (the exact SELECTed columns).
func waitsRow(waitType string) Row {
	return Row{"wait_type": waitType, "wait_time_ms": int64(100), "waiting_tasks_count": int64(7)}
}

// TestDecodeRejectsUnknownField proves C9: a row carrying any column outside
// the fixed struct is rejected, and dst is left untouched on error.
func TestDecodeRejectsUnknownField(t *testing.T) {
	for _, extra := range []string{"sql_text", "physical_name", "login", "evil", "waitType"} {
		row := requestRow("WRITELOG")
		row[extra] = "SELECT secret"
		var out []Requests
		if _, err := decodeRows([]Row{row}, &out, CapRequests); err == nil {
			t.Errorf("extra column %q accepted; want rejection", extra)
		}
		if len(out) != 0 {
			t.Errorf("extra column %q: dst modified on error", extra)
		}
	}
	// A bad numeric value (negative count into uint) fails closed too.
	row := requestRow("WRITELOG")
	row["tasks"] = int64(-1)
	var out []Requests
	if _, err := decodeRows([]Row{row}, &out, CapRequests); err == nil {
		t.Error("negative tasks accepted; want rejection")
	}
}

// TestDecodeWaitTypeRegex proves the C9 wait-type shape: uppercase/digits/_
// up to 60 chars; anything else is rejected. The regex is never loosened.
func TestDecodeWaitTypeRegex(t *testing.T) {
	for _, ok := range []string{"PAGEIOLATCH_SH", "WRITELOG", "CXPACKET_123", strings.Repeat("A", 60)} {
		var out []Waits
		if _, err := decodeRows([]Row{waitsRow(ok)}, &out, CapWaits); err != nil {
			t.Errorf("valid wait type %q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"bad;type", "pageiolatch_sh", strings.Repeat("A", 61), "WAIT TYPE", "'; DROP TABLE x", ""} {
		var out []Waits
		if _, err := decodeRows([]Row{waitsRow(bad)}, &out, CapWaits); err == nil {
			t.Errorf("invalid wait type %q accepted; want rejection", bad)
		}
	}
	if waitTypeRe.MatchString("") {
		t.Error("waitTypeRe accepts the empty string; the regex must stay strict")
	}
}

// TestDecodeEmptyWaitTypeRunning pins the review requirement: bb:requests
// emits '' for running requests (ISNULL(wait_type,'')); the requests decode
// path maps '' to the fixed sentinel RUNNING before validation, without
// loosening the general regex.
func TestDecodeEmptyWaitTypeRunning(t *testing.T) {
	var out []Requests
	truncated, err := decodeRows([]Row{requestRow("")}, &out, CapRequests)
	if err != nil {
		t.Fatalf("empty requests wait type rejected: %v", err)
	}
	if truncated || len(out) != 1 {
		t.Fatalf("truncated=%v len=%d; want false/1", truncated, len(out))
	}
	if WaitTypeRunning != "RUNNING" || out[0].WaitType != "RUNNING" {
		t.Errorf("WaitType = %q; want RUNNING", out[0].WaitType)
	}
	if !waitTypeRe.MatchString(WaitTypeRunning) {
		t.Error("sentinel does not pass the strict regex")
	}
	// The sentinel is requests-only: waits with '' stay rejected.
	var w []Waits
	if _, err := decodeRows([]Row{waitsRow("")}, &w, CapWaits); err == nil {
		t.Error("empty waits wait type accepted; want rejection")
	}
}

// TestDecodeRowCapTruncated proves C6: the reader stops at cap+1, keeps cap
// rows and marks truncated; exactly cap rows is not truncated. The per-kind
// caps are small — never dbhealth's 5000.
func TestDecodeRowCapTruncated(t *testing.T) {
	mk := func(n int) []Row {
		rows := make([]Row, n)
		for i := range rows {
			rows[i] = waitsRow("WRITELOG")
		}
		return rows
	}
	var out []Waits
	truncated, err := decodeRows(mk(4), &out, 3)
	if err != nil || !truncated || len(out) != 3 {
		t.Fatalf("cap+1 rows: truncated=%v len=%d err=%v; want true/3/nil", truncated, len(out), err)
	}
	out = nil
	truncated, err = decodeRows(mk(3), &out, 3)
	if err != nil || truncated || len(out) != 3 {
		t.Fatalf("exactly cap rows: truncated=%v len=%d err=%v; want false/3/nil", truncated, len(out), err)
	}
	for _, c := range []int{CapRequests, CapWaits, CapSchedulers, CapSemaphores, CapTempdb, CapFileStats, CapAG} {
		if c <= 0 || c > 256 {
			t.Errorf("cap %d out of the small per-kind range (never 5000)", c)
		}
	}
	if CapRequests != 256 || CapWaits != 20 || CapFileStats != 256 {
		t.Errorf("pinned caps changed: requests=%d waits=%d filestats=%d", CapRequests, CapWaits, CapFileStats)
	}
}

// TestOutputNoNames proves the fixed structs carry no names/paths/logins/text:
// every field except WaitType is numeric, and the marshaled JSON keys are
// exactly the allowed set (no name, login, host, program, path, text, replica
// or server key can appear).
func TestOutputNoNames(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(Requests{}), reflect.TypeOf(Waits{}), reflect.TypeOf(Schedulers{}),
		reflect.TypeOf(Semaphores{}), reflect.TypeOf(Tempdb{}), reflect.TypeOf(FileStats{}), reflect.TypeOf(AG{}),
	}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.Name == "WaitType" {
				continue // the only string field, regex-validated
			}
			switch f.Type.Kind() {
			case reflect.Uint8, reflect.Uint32, reflect.Uint64, reflect.Int32:
			default:
				t.Errorf("%s.%s is %s; want numeric only", typ.Name(), f.Name, f.Type.Kind())
			}
		}
	}
	allowed := map[string]bool{}
	for _, key := range columnKeys {
		allowed[key] = true
	}
	banned := []string{"name", "login", "host", "program", "path", "text", "replica", "server", "query"}
	samples := []any{
		Requests{WaitType: "RUNNING", Tasks: 1, MaxWaitMs: 1, Blocked: 1, HeadBlockers: 1, MaxBlockMs: 1},
		Waits{WaitType: "WRITELOG", DeltaMs: 1, DeltaTasks: 1},
		Schedulers{CurrentTasks: 1, RunnableTasks: 1},
		Semaphores{ResourceSemaphoreID: 1, Grantees: 1, Waiters: 1, Forced: 1, Timeouts: 1},
		Tempdb{InternalPages: 1, UserPages: 1},
		FileStats{DatabaseID: 1, FileID: 1, ReadStallMs: 1, WriteStallMs: 1, Reads: 1, Writes: 1, SizeOnDiskBytes: 1},
		AG{DatabaseID: 1, SyncState: 1},
	}
	for _, s := range samples {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal %T: %v", s, err)
		}
		var keys map[string]any
		if err := json.Unmarshal(data, &keys); err != nil {
			t.Fatalf("unmarshal %T: %v", s, err)
		}
		for k := range keys {
			if !allowed[k] {
				t.Errorf("%T: JSON key %q is not an allowed output key", s, k)
			}
			lk := strings.ToLower(k)
			for _, b := range banned {
				if strings.Contains(lk, b) {
					t.Errorf("%T: JSON key %q contains banned %q", s, k, b)
				}
			}
		}
	}
}

// TestFileStatsIDsOnly proves bb:filestats decodes ids + stalls + size (the
// pinned size_on_disk_bytes column) and rejects any path/name column.
func TestFileStatsIDsOnly(t *testing.T) {
	row := Row{
		"database_id": int64(5), "file_id": int64(1),
		"io_stall_read_ms": int64(100), "io_stall_write_ms": int64(200),
		"num_of_reads": int64(10), "num_of_writes": int64(20),
		"size_on_disk_bytes": int64(1048576),
	}
	var out []FileStats
	truncated, err := decodeRows([]Row{row}, &out, CapFileStats)
	if err != nil || truncated || len(out) != 1 {
		t.Fatalf("filestats row: truncated=%v len=%d err=%v; want false/1/nil", truncated, len(out), err)
	}
	got := out[0]
	if got.DatabaseID != 5 || got.FileID != 1 || got.ReadStallMs != 100 || got.WriteStallMs != 200 ||
		got.Reads != 10 || got.Writes != 20 || got.SizeOnDiskBytes != 1048576 {
		t.Errorf("filestats decoded wrong: %+v", got)
	}
	data, _ := json.Marshal(got)
	var keys map[string]any
	_ = json.Unmarshal(data, &keys)
	for _, want := range []string{"databaseId", "fileId", "sizeOnDiskBytes"} {
		if _, ok := keys[want]; !ok {
			t.Errorf("filestats JSON missing %q", want)
		}
	}
	for k := range keys {
		if strings.Contains(strings.ToLower(k), "name") || strings.Contains(strings.ToLower(k), "path") {
			t.Errorf("filestats JSON carries name/path key %q", k)
		}
	}
	row["physical_name"] = `C:\data\x.mdf`
	var out2 []FileStats
	if _, err := decodeRows([]Row{row}, &out2, CapFileStats); err == nil {
		t.Error("physical_name column accepted; want rejection")
	}
}

// TestDecodeAllStatementColumns ties the structs to the allowlist: every
// column SELECTed by each fixed statement decodes into its fixed struct with
// no rejection (the exact-match guard for C9).
func TestDecodeAllStatementColumns(t *testing.T) {
	kinds := []struct {
		id  string
		dst interface{}
	}{
		{"bb:requests", &[]Requests{}},
		{"bb:waits", &[]Waits{}},
		{"bb:schedulers", &[]Schedulers{}},
		{"bb:semaphores", &[]Semaphores{}},
		{"bb:tempdb", &[]Tempdb{}},
		{"bb:filestats", &[]FileStats{}},
		{"bb:ag", &[]AG{}},
	}
	for _, k := range kinds {
		cols := outputColumns(t, Statements[k.id])
		row := Row{}
		for _, c := range cols {
			if c == "wait_type" {
				row[c] = "PAGEIOLATCH_SH"
			} else {
				row[c] = int64(1)
			}
		}
		if _, err := decodeRows([]Row{row}, k.dst, 256); err != nil {
			t.Errorf("%s: SELECTed columns %v rejected: %v", k.id, cols, err)
		}
	}
}