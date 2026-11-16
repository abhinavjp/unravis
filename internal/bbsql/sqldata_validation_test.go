package bbsql

import "testing"

// W11-04: DisallowUnknownFields rejects extras but not absences or NULL. These
// table tests delete each required column and null each column for every output
// type, and prove a missing/null reading is a fixed-code refusal (or, for the
// summed statements, a missing payload) — never a fabricated zero or RUNNING.

func reqRowFull() Row {
	return Row{"wait_type": "WRITELOG", "tasks": int64(2), "max_wait_ms": int64(50),
		"blocked": int64(1), "head_blockers": int64(1), "max_block_ms": int64(40)}
}

func waitsRowFull() Row {
	return Row{"wait_type": "WRITELOG", "wait_time_ms": int64(100), "waiting_tasks_count": int64(7)}
}

func semRowFull() Row {
	return Row{"resource_semaphore_id": int64(0), "grantee_count": int64(1), "waiter_count": int64(0),
		"forced_grant_count": int64(10), "timeout_count": int64(2)}
}

func fsRowFull() Row {
	return Row{"database_id": int64(5), "file_id": int64(1), "io_stall_read_ms": int64(100),
		"io_stall_write_ms": int64(200), "num_of_reads": int64(10), "num_of_writes": int64(20),
		"size_on_disk_bytes": int64(4096)}
}

// TestRequiredColumnMissingIsRefused deletes each required column in turn and
// expects the fixed errMissingColumn (never a zero-filled decode).
func TestRequiredColumnMissingIsRefused(t *testing.T) {
	cases := []struct {
		name string
		row  Row
		dec  func([]Row) (any, bool, error)
	}{
		{"requests", reqRowFull(), func(r []Row) (any, bool, error) { var s requestsSampler; return s.Decode(r) }},
		{"waits", waitsRowFull(), func(r []Row) (any, bool, error) { var s waitsSampler; return s.Decode(r) }},
		{"semaphores", semRowFull(), func(r []Row) (any, bool, error) { s := &semaphoresSampler{}; return s.Decode(r) }},
		{"filestats", fsRowFull(), func(r []Row) (any, bool, error) { s := &filestatsSampler{}; return s.Decode(r) }},
		{"ag", Row{"database_id": int64(7), "synchronization_state": int64(2)}, func(r []Row) (any, bool, error) { var s agSampler; return s.Decode(r) }},
	}
	for _, tc := range cases {
		for col := range tc.row {
			row := Row{}
			for k, v := range tc.row {
				if k != col {
					row[k] = v
				}
			}
			payload, _, err := tc.dec([]Row{row})
			if err != errMissingColumn {
				t.Errorf("%s missing %q: err=%v, want %v", tc.name, col, err, errMissingColumn)
			}
			if payload != nil {
				t.Errorf("%s missing %q: payload=%#v, want nil", tc.name, col, payload)
			}
		}
	}
}

// TestRequiredColumnNullIsRefused nulls each non-nullable column and expects the
// fixed errNullColumn. The nullable semaphore counters are excluded (covered by
// TestSemaphoreNullCounterKeepsGauges).
func TestRequiredColumnNullIsRefused(t *testing.T) {
	cases := []struct {
		name     string
		row      Row
		nullable map[string]bool
		dec      func([]Row) (any, bool, error)
	}{
		{"requests", reqRowFull(), nil, func(r []Row) (any, bool, error) { var s requestsSampler; return s.Decode(r) }},
		{"waits", waitsRowFull(), nil, func(r []Row) (any, bool, error) { var s waitsSampler; return s.Decode(r) }},
		{"semaphores", semRowFull(), map[string]bool{"forced_grant_count": true, "timeout_count": true},
			func(r []Row) (any, bool, error) { s := &semaphoresSampler{}; return s.Decode(r) }},
		{"filestats", fsRowFull(), nil, func(r []Row) (any, bool, error) { s := &filestatsSampler{}; return s.Decode(r) }},
		{"ag", Row{"database_id": int64(7), "synchronization_state": int64(2)}, nil, func(r []Row) (any, bool, error) { var s agSampler; return s.Decode(r) }},
	}
	for _, tc := range cases {
		for col := range tc.row {
			if tc.nullable[col] {
				continue
			}
			row := Row{}
			for k, v := range tc.row {
				row[k] = v
			}
			row[col] = nil
			payload, _, err := tc.dec([]Row{row})
			if err != errNullColumn {
				t.Errorf("%s null %q: err=%v, want %v", tc.name, col, err, errNullColumn)
			}
			if payload != nil {
				t.Errorf("%s null %q: payload=%#v, want nil", tc.name, col, payload)
			}
		}
	}
}

// TestRequestsMissingWaitTypeIsNotRunning distinguishes a PRESENT empty wait_type
// (RUNNING) from a missing/null one (refused, never RUNNING).
func TestRequestsMissingWaitTypeIsNotRunning(t *testing.T) {
	var s requestsSampler
	// Present empty -> RUNNING.
	payload, _, err := s.Decode([]Row{reqRowFull()})
	if err != nil {
		t.Fatalf("full row: %v", err)
	}
	_ = payload
	empty := reqRowFull()
	empty["wait_type"] = ""
	payload, _, err = s.Decode([]Row{empty})
	if err != nil {
		t.Fatalf("present empty wait_type: %v", err)
	}
	if rows := payload.([]Requests); rows[0].WaitType != WaitTypeRunning {
		t.Errorf("present empty wait_type = %q, want RUNNING", rows[0].WaitType)
	}
	// Missing wait_type -> refused, not RUNNING.
	missing := reqRowFull()
	delete(missing, "wait_type")
	if _, _, err := s.Decode([]Row{missing}); err != errMissingColumn {
		t.Errorf("missing wait_type: err=%v, want %v (never RUNNING)", err, errMissingColumn)
	}
	// Null wait_type -> refused, not RUNNING.
	null := reqRowFull()
	null["wait_type"] = nil
	if _, _, err := s.Decode([]Row{null}); err != errNullColumn {
		t.Errorf("null wait_type: err=%v, want %v (never RUNNING)", err, errNullColumn)
	}
}

// TestSummedPartialRowIsRefused: a summed statement (schedulers/tempdb) with a
// PARTIAL row (one required total absent) is a schema defect, refused; an empty
// row or an all/partial-NULL row is the legitimate empty-SUM case (missing, no
// error). Real zeros stay real zeros.
func TestSummedPartialRowIsRefused(t *testing.T) {
	var sched schedulersSampler
	var tempdb tempdbSampler
	// Partial: runnable_tasks absent.
	if _, _, err := sched.Decode([]Row{{"current_tasks": int64(8)}}); err != errMissingColumn {
		t.Errorf("schedulers partial row: err=%v, want %v", err, errMissingColumn)
	}
	if _, _, err := tempdb.Decode([]Row{{"internal_pages": int64(8)}}); err != errMissingColumn {
		t.Errorf("tempdb partial row: err=%v, want %v", err, errMissingColumn)
	}
	// Empty row and NULL rows: missing payload, no error.
	for _, rows := range [][]Row{{{}}, {schedulersRow(nil, nil)}, {schedulersRow(nil, int64(2))}} {
		if payload, _, err := sched.Decode(rows); err != nil || payload != nil {
			t.Errorf("schedulers empty-SUM %#v: payload=%v err=%v, want nil,nil", rows, payload, err)
		}
	}
	// Real zeros are a real sample.
	if payload, _, err := sched.Decode([]Row{schedulersRow(int64(0), int64(0))}); err != nil || payload != (Schedulers{}) {
		t.Errorf("schedulers real zeros: payload=%v err=%v, want Schedulers{0,0}", payload, err)
	}
}

// TestAGSyncStateEnumDomain enforces the confirmed synchronization-state domain
// (0..4); an out-of-domain value is a fixed-code refusal, not an arbitrary state.
func TestAGSyncStateEnumDomain(t *testing.T) {
	var s agSampler
	for state := int64(0); state <= 4; state++ {
		payload, _, err := s.Decode([]Row{{"database_id": int64(7), "synchronization_state": state}})
		if err != nil || payload == nil {
			t.Errorf("state %d: payload=%v err=%v, want a row", state, payload, err)
		}
	}
	for _, state := range []int64{5, 6, 255} {
		payload, _, err := s.Decode([]Row{{"database_id": int64(7), "synchronization_state": state}})
		if err != errEnumDomain || payload != nil {
			t.Errorf("state %d: payload=%v err=%v, want nil,%v", state, payload, err, errEnumDomain)
		}
	}
}
