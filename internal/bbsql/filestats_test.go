package bbsql

import "testing"

// Rows are keyed by the SQL column names the allowlisted statements SELECT,
// which is what decodeRows maps through columnKeys.

func fileStatsRows(entries ...FileStats) []Row {
	rows := make([]Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, Row{
			"database_id":        int64(e.DatabaseID),
			"file_id":            int64(e.FileID),
			"io_stall_read_ms":   int64(e.ReadStallMs),
			"io_stall_write_ms":  int64(e.WriteStallMs),
			"num_of_reads":       int64(e.Reads),
			"num_of_writes":      int64(e.Writes),
			"size_on_disk_bytes": int64(e.SizeOnDiskBytes),
		})
	}
	return rows
}

func fileStatsRowCount(n int, databaseID int) []Row {
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, Row{
			"database_id":        int64(databaseID),
			"file_id":            int64(i + 1),
			"io_stall_read_ms":   int64(0),
			"io_stall_write_ms":  int64(0),
			"num_of_reads":       int64(0),
			"num_of_writes":      int64(0),
			"size_on_disk_bytes": int64(1),
		})
	}
	return rows
}

func agRows(entries ...AG) []Row {
	rows := make([]Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, Row{
			"database_id":           int64(e.DatabaseID),
			"synchronization_state": int64(e.SyncState),
		})
	}
	return rows
}

func hadrRows(v any) []Row { return []Row{{"hadr": v}} }

func TestFileStatsFirstCycleIsBaselineOnly(t *testing.T) {
	s := &filestatsSampler{}
	rows := fileStatsRows(FileStats{DatabaseID: 1, FileID: 1,
		ReadStallMs: 10, WriteStallMs: 20, Reads: 3, Writes: 4, SizeOnDiskBytes: 100})

	payload, truncated, err := s.Decode(rows)
	if err != nil || truncated || payload != nil {
		t.Fatalf("first cycle: payload=%v truncated=%v err=%v, want nil,false,nil", payload, truncated, err)
	}
	if len(s.prev) != 1 {
		t.Fatalf("baseline not recorded: prev=%d rows, want 1", len(s.prev))
	}

	// The identical sighting has nothing to report: zero deltas are dropped
	// rather than persisted as zeros.
	payload, truncated, err = s.Decode(rows)
	if err != nil || truncated || payload != nil {
		t.Fatalf("second identical cycle: payload=%v truncated=%v err=%v, want nil,false,nil", payload, truncated, err)
	}
}

func TestFileStatsDeltaAndGauge(t *testing.T) {
	s := &filestatsSampler{}
	base := FileStats{DatabaseID: 5, FileID: 2,
		ReadStallMs: 1000, WriteStallMs: 2000, Reads: 100, Writes: 50, SizeOnDiskBytes: 4096}
	if payload, _, err := s.Decode(fileStatsRows(base)); err != nil || payload != nil {
		t.Fatalf("baseline: payload=%v err=%v, want nil,nil", payload, err)
	}

	cur := FileStats{DatabaseID: 5, FileID: 2,
		ReadStallMs: 2000, WriteStallMs: 2100, Reads: 130, Writes: 100, SizeOnDiskBytes: 8192}
	payload, truncated, err := s.Decode(fileStatsRows(cur))
	if err != nil || truncated {
		t.Fatalf("delta: truncated=%v err=%v", truncated, err)
	}
	got, ok := payload.([]FileStats)
	if !ok || len(got) != 1 {
		t.Fatalf("delta payload=%T %#v, want []FileStats with 1 row", payload, payload)
	}
	want := FileStats{DatabaseID: 5, FileID: 2,
		ReadStallMs: 1000, WriteStallMs: 100, Reads: 30, Writes: 50,
		SizeOnDiskBytes: 8192} // the gauge is the current size, never a difference
	if got[0] != want {
		t.Fatalf("delta=%#v, want %#v", got[0], want)
	}
}

func TestFileStatsCounterResetReplacesBaseline(t *testing.T) {
	s := &filestatsSampler{}
	base := FileStats{DatabaseID: 1, FileID: 1,
		ReadStallMs: 100, WriteStallMs: 100, Reads: 100, Writes: 100, SizeOnDiskBytes: 10}
	if _, _, err := s.Decode(fileStatsRows(base)); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// A counter going backwards (instance restart, role change) is never turned
	// into a negative or a faked delta: the cycle is skipped.
	reset := FileStats{DatabaseID: 1, FileID: 1,
		ReadStallMs: 50, WriteStallMs: 100, Reads: 100, Writes: 100, SizeOnDiskBytes: 20}
	payload, _, err := s.Decode(fileStatsRows(reset))
	if err != nil || payload != nil {
		t.Fatalf("reset cycle: payload=%v err=%v, want nil,nil", payload, err)
	}

	after := FileStats{DatabaseID: 1, FileID: 1,
		ReadStallMs: 70, WriteStallMs: 110, Reads: 105, Writes: 101, SizeOnDiskBytes: 30}
	payload, _, err = s.Decode(fileStatsRows(after))
	if err != nil {
		t.Fatalf("after reset: %v", err)
	}
	got, ok := payload.([]FileStats)
	if !ok || len(got) != 1 {
		t.Fatalf("after reset payload=%T %#v, want []FileStats with 1 row", payload, payload)
	}
	want := FileStats{DatabaseID: 1, FileID: 1,
		ReadStallMs: 20, WriteStallMs: 10, Reads: 5, Writes: 1, SizeOnDiskBytes: 30}
	if got[0] != want {
		t.Fatalf("after reset delta=%#v, want %#v (baseline must be the reset value)", got[0], want)
	}
}

func TestFileStatsPrevStaysBounded(t *testing.T) {
	s := &filestatsSampler{}
	// Cap+1 rows: decodeRows keeps Cap and reports truncated (C6).
	payload, truncated, err := s.Decode(fileStatsRowCount(CapFileStats+1, 1))
	if err != nil || !truncated {
		t.Fatalf("decode: truncated=%v err=%v, want truncated true", truncated, err)
	}
	if payload != nil {
		t.Error("first cycle must emit nothing")
	}
	if len(s.prev) != CapFileStats {
		t.Fatalf("len(prev) = %d, want %d", len(s.prev), CapFileStats)
	}

	// Whole key sets changing between cycles must not grow the map.
	for cycle := 0; cycle < 3; cycle++ {
		if _, _, err := s.Decode(fileStatsRowCount(CapFileStats, cycle+2)); err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		if len(s.prev) > CapFileStats {
			t.Fatalf("cycle %d: len(prev) = %d, want <= %d", cycle, len(s.prev), CapFileStats)
		}
	}

	// An empty reply clears the baselines: nothing may be remembered forever.
	if _, _, err := s.Decode(nil); err != nil {
		t.Fatalf("empty reply: %v", err)
	}
	if len(s.prev) != 0 {
		t.Errorf("len(prev) = %d after an empty reply, want 0", len(s.prev))
	}
}

func TestFileStatsRejectsForbiddenColumns(t *testing.T) {
	for _, col := range []string{"physical_name", "database_name", "name", "file_handle", "type_desc"} {
		rows := Row{
			"database_id": int64(1), "file_id": int64(1),
			"io_stall_read_ms": int64(0), "io_stall_write_ms": int64(0),
			"num_of_reads": int64(0), "num_of_writes": int64(0),
			"size_on_disk_bytes": int64(0),
			col:                  "forbidden",
		}
		s := &filestatsSampler{}
		payload, _, err := s.Decode([]Row{rows})
		if err != errUnknownColumn || payload != nil {
			t.Errorf("column %q: payload=%v err=%v, want nil,%v", col, payload, err, errUnknownColumn)
		}
	}
}

func TestAGGateOpenFailsClosed(t *testing.T) {
	var ag agSampler
	for name, rows := range map[string][]Row{
		"zero":         hadrRows(int64(0)),
		"nil value":    hadrRows(nil),
		"no rows":      nil,
		"empty rows":   {},
		"extra column": {{"hadr": int64(1), "replica_server_name": "corp-node-1"}},
		"not a number": hadrRows("yes"),
		"two rows":     {{"hadr": int64(1)}, {"hadr": int64(1)}},
	} {
		if ag.GateOpen(rows) {
			t.Errorf("GateOpen(%s) = true, want false", name)
		}
	}
	if !ag.GateOpen(hadrRows(int64(1))) {
		t.Error("GateOpen(1) = false, want true")
	}
	if _, ok := Statement(ag.GateID()); !ok {
		t.Errorf("gate id %q is not allowlisted", ag.GateID())
	}
}

func TestAGDecodeIsIdsAndEnumOnly(t *testing.T) {
	var ag agSampler
	payload, truncated, err := ag.Decode(agRows(AG{DatabaseID: 7, SyncState: 2}))
	if err != nil || truncated {
		t.Fatalf("decode: truncated=%v err=%v", truncated, err)
	}
	got, ok := payload.([]AG)
	if !ok || len(got) != 1 || got[0] != (AG{DatabaseID: 7, SyncState: 2}) {
		t.Fatalf("payload=%T %#v, want []AG{{7 2}}", payload, payload)
	}
	if payload, _, err := ag.Decode(nil); err != nil || payload != nil {
		t.Errorf("empty rows: payload=%v err=%v, want nil,nil", payload, err)
	}
	for _, col := range []string{"replica_server_name", "joined_state_desc", "group_database_id"} {
		rows := Row{"database_id": int64(1), "synchronization_state": int64(1), col: "forbidden"}
		payload, _, err := ag.Decode([]Row{rows})
		if err != errUnknownColumn || payload != nil {
			t.Errorf("column %q: payload=%v err=%v, want nil,%v", col, payload, err, errUnknownColumn)
		}
	}
}

func TestFileStatsAndAGRegistered(t *testing.T) {
	found := map[string]Sampler{}
	for _, s := range Samplers() {
		found[s.Kind()] = s
	}
	fs, ok := found["filestats"]
	if !ok {
		t.Fatal("filestats is not registered")
	}
	if fs.ID() != "bb:filestats" || fs.Interval() != DemotedInterval {
		t.Errorf("filestats = %s/%v, want bb:filestats/%v", fs.ID(), fs.Interval(), DemotedInterval)
	}
	ag, ok := found["ag"]
	if !ok {
		t.Fatal("ag is not registered")
	}
	if ag.ID() != "bb:ag" || ag.Interval() != DemotedInterval {
		t.Errorf("ag = %s/%v, want bb:ag/%v", ag.ID(), ag.Interval(), DemotedInterval)
	}
	if _, ok := ag.(Gate); !ok {
		t.Error("ag does not implement Gate, so its DMV would be queried everywhere")
	}
}
