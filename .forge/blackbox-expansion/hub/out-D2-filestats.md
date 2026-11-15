internal/bbsql/filestats.go
```go
package bbsql

import "time"

// stateful samplers are not concurrent; the runner serialises them.

type fileKey struct {
	databaseID uint32
	fileID     uint32
}

type filestatsCumulative struct {
	readStallMs  uint64
	writeStallMs uint64
	reads        uint64
	writes       uint64
}

type filestatsSampler struct {
	prev map[fileKey]filestatsCumulative
}

func (filestatsSampler) ID() string             { return "bb:filestats" }
func (filestatsSampler) Kind() string           { return "filestats" }
func (filestatsSampler) Interval() time.Duration { return DemotedInterval }

func (s *filestatsSampler) Decode(rows []Row) (any, bool, error) {
	var cur []FileStats
	truncated, err := decodeRows(rows, &cur, CapFileStats)
	if err != nil {
		return nil, false, err
	}
	if len(cur) == 0 {
		if s.prev == nil {
			return nil, false, nil
		}
		s.prev = map[fileKey]filestatsCumulative{}
		return nil, truncated, nil
	}

	nextPrev := make(map[fileKey]filestatsCumulative, len(cur))
	firstCycle := s.prev == nil
	out := make([]FileStats, 0, len(cur))

	for _, r := range cur {
		k := fileKey{databaseID: r.DatabaseID, fileID: r.FileID}
		cum := filestatsCumulative{
			readStallMs:  r.ReadStallMs,
			writeStallMs: r.WriteStallMs,
			reads:        r.Reads,
			writes:       r.Writes,
		}
		nextPrev[k] = cum

		if firstCycle {
			continue
		}
		prev, ok := s.prev[k]
		if !ok {
			continue
		}
		// Counter went backwards (SQL restart/role change): reset baseline, skip.
		if cum.readStallMs < prev.readStallMs ||
			cum.writeStallMs < prev.writeStallMs ||
			cum.reads < prev.reads ||
			cum.writes < prev.writes {
			continue
		}

		dReadStall := cum.readStallMs - prev.readStallMs
		dWriteStall := cum.writeStallMs - prev.writeStallMs
		dReads := cum.reads - prev.reads
		dWrites := cum.writes - prev.writes
		if dReadStall == 0 && dWriteStall == 0 && dReads == 0 && dWrites == 0 {
			continue
		}

		out = append(out, FileStats{
			DatabaseID:      r.DatabaseID,
			FileID:          r.FileID,
			ReadStallMs:     dReadStall,
			WriteStallMs:    dWriteStall,
			Reads:           dReads,
			Writes:          dWrites,
			SizeOnDiskBytes: r.SizeOnDiskBytes, // gauge: current value, not a delta
		})
	}

	s.prev = nextPrev
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out, truncated, nil
}

type agSampler struct{}

type hadrFlag struct {
	Hadr int64 `json:"hadr"`
}

func (agSampler) ID() string             { return "bb:ag" }
func (agSampler) Kind() string           { return "ag" }
func (agSampler) Interval() time.Duration { return DemotedInterval }
func (agSampler) GateID() string         { return "bb:hadr" }

func (agSampler) GateOpen(rows []Row) bool {
	var out []hadrFlag
	_, err := decodeRows(rows, &out, 1)
	if err != nil {
		return false // fail closed
	}
	if len(out) != 1 {
		return false
	}
	return out[0].Hadr == 1
	// A Standard-edition instance (or any case where the gate cannot be read)
	// therefore produces no "ag" payload instead of an error.
}

func (agSampler) Decode(rows []Row) (any, bool, error) {
	var out []AG
	truncated, err := decodeRows(rows, &out, CapAG)
	if err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	return out, truncated, nil
}

func init() {
	RegisterSampler(&filestatsSampler{})
	RegisterSampler(&agSampler{})
}
```

internal/bbsql/filestats_test.go
```go
package bbsql

import "testing"

func fileStatsRows(entries ...FileStats) []Row {
	rows := make([]Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, Row{
			"databaseId":      int64(e.DatabaseID),
			"fileId":          int64(e.FileID),
			"readStallMs":     int64(e.ReadStallMs),
			"writeStallMs":    int64(e.WriteStallMs),
			"reads":           int64(e.Reads),
			"writes":          int64(e.Writes),
			"sizeOnDiskBytes": int64(e.SizeOnDiskBytes),
		})
	}
	return rows
}

func agRows(entries ...AG) []Row {
	rows := make([]Row, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, Row{
			"databaseId": int64(e.DatabaseID),
			"syncState":  int64(e.SyncState),
		})
	}
	return rows
}

func hadrRows(v any) []Row {
	if v == nil {
		return []Row{{"hadr": nil}}
	}
	return []Row{{"hadr": v}}
}

func TestFileStatsFirstCycleNil(t *testing.T) {
	s := &filestatsSampler{}
	rows := fileStatsRows(FileStats{
		DatabaseID: 1, FileID: 1,
		ReadStallMs: 10, WriteStallMs: 20, Reads: 3, Writes: 4, SizeOnDiskBytes: 100,
	})

	payload, truncated, err := s.Decode(rows)
	if err != nil || truncated || payload != nil {
		t.Fatalf("first cycle: payload=%v truncated=%v err=%v, want nil,false,nil", payload, truncated, err)
	}

	// Second identical decode: all deltas zero => keys dropped, never emitted as zeros.
	payload, truncated, err = s.Decode(rows)
	if err != nil || truncated || payload != nil {
		t.Fatalf("second identical: payload=%v truncated=%v err=%v, want nil,false,nil", payload, truncated, err)
	}
}

func TestFileStatsDelta(t *testing.T) {
	s := &filestatsSampler{}
	base := FileStats{
		DatabaseID: 5, FileID: 2,
		ReadStallMs: 1000, WriteStallMs: 2000, Reads: 100, Writes: 50, SizeOnDiskBytes: 4096,
	}
	if payload, _, err := s.Decode(fileStatsRows(base)); err != nil || payload != nil {
		t.Fatalf("baseline: payload=%v err=%v, want nil,nil", payload, err)
	}

	cur := FileStats{
		DatabaseID: 5, FileID: 2,
		ReadStallMs: 2000, WriteStallMs: 2100, Reads: 130, Writes: 100, SizeOnDiskBytes: 8192,
	}
	payload, truncated, err := s.Decode(fileStatsRows(cur))
	if err != nil || truncated {
		t.Fatalf("delta: truncated=%v err=%v", truncated, err)
	}
	got, ok := payload.([]FileStats)
	if !ok || len(got) != 1 {
		t.Fatalf("delta payload=%T %#v, want []FileStats len 1", payload, payload)
	}
	want := FileStats{
		DatabaseID: 5, FileID: 2,
		ReadStallMs: 1000, WriteStallMs: 100, Reads: 30, Writes: 50,
		SizeOnDiskBytes: 8192, // current value (gauge), not a delta
	}
	if got[0] != want {
		t.Fatalf("delta=%#v, want %#v", got[0], want)
	}
}

func TestFileStatsReset(t *testing.T) {
	s := &filestatsSampler{}
	base := FileStats{DatabaseID: 1, FileID: 1, ReadStallMs: 100, WriteStallMs: 100, Reads: 100, Writes: 100, SizeOnDiskBytes: 10}
	_, _, _ = s.Decode(fileStatsRows(base))

	// Counter goes backwards: skip for this cycle and replace baseline.
	reset := FileStats{DatabaseID: 1, FileID: 1, ReadStallMs: 50, WriteStallMs: 100, Reads: 100, Writes: 100, SizeOnDiskBytes: 20}
	payload, _, err := s.Decode(fileStatsRows(reset))
	if err != nil || payload != nil {
		t.Fatalf("reset cycle: payload=%v err=%v, want nil,nil", payload, err)
	}

	// Next cycle computes delta from the replaced baseline.
	after := FileStats{DatabaseID: 1, FileID: 1, ReadStallMs: 70, WriteStallMs: 110, Reads: 105, Writes: 101, SizeOnDiskBytes: 30}
	payload, _, err = s.Decode(fileStatsRows(after))
	if err != nil {
		t.Fatalf("after reset: err=%v", err)
	}
	got, ok := payload.([]FileStats)
	if !ok || len(got) != 1 {
		t.Fatalf("after reset payload=%T %#v, want []FileStats len 1", payload, payload)
	}
	want := FileStats{DatabaseID: 1, FileID: 1, ReadStallMs: 20, WriteStallMs: 10, Reads: 5, Writes: 1, SizeOnDiskBytes: 30}
	if got[0] != want {
		t.Fatalf("after reset delta=%#v, want %#v", got[0], want)
	}
}

func TestFileStatsTop256Bounded(t *testing.T) {
	s := &filestatsSampler{}
	rows := make([]Row, 0, CapFileStats+1)
	for i := 0; i < CapFileStats+1; i++ {
		rows = append(rows, Row{
			"databaseId":      int64(1),
			"fileId":          int64(i + 1),
			"readStallMs":     int64(0),
			"writeStallMs":    int64(0),
			"reads":           int64(0),
			"writes":          int64(0),
			"sizeOnDiskBytes": int64(1),
		})
	}
	_, truncated, err := s.Decode(rows)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !truncated {
		t.Fatalf("truncated=false, want true")
	}
	if len(s.prev) != CapFileStats {
		t.Fatalf("len(prev)=%d, want %d", len(s.prev), CapFileStats)
	}

	// Shift key sets across cycles; prev must remain bounded.
	for cycle := 0; cycle < 3; cycle++ {
		shifted := make([]Row, 0, CapFileStats)
		base := cycle * 10
		for i := 0; i < CapFileStats; i++ {
			shifted = append(shifted, Row{
				"databaseId":      int64(2),
				"fileId":          int64(base + i + 1),
				"readStallMs":     int64(0),
				"writeStallMs":    int64(0),
				"reads":           int64(0),
				"writes":          int64(0),
				"sizeOnDiskBytes": int64(1),
			})
		}
		_, _, err := s.Decode(shifted)
		if err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		if len(s.prev) > CapFileStats {
			t.Fatalf("cycle %d: len(prev)=%d, want <= %d", cycle, len(s.prev), CapFileStats)
		}
	}
}

func TestFileStatsNoPaths(t *testing.T) {
	for _, col := range []string{"physical_name", "database_name", "name", "file_handle", "type_desc"} {
		s := &filestatsSampler{}
		rows := []Row{{
			"databaseId":      int64(1),
			"fileId":          int64(1),
			"readStallMs":     int64(0),
			"writeStallMs":    int64(0),
			"reads":           int64(0),
			"writes":          int64(0),
			"sizeOnDiskBytes": int64(0),
			col:               "forbidden",
		}}
		payload, _, err := s.Decode(rows)
		if err != errUnknownColumn || payload != nil {
			t.Fatalf("column %q: payload=%v err=%v, want nil,errUnknownColumn", col, payload, err)
		}
	}
}

func TestAGGatedOff(t *testing.T) {
	var ag agSampler
	if ag.GateOpen(hadrRows(int64(0))) {
		t.Fatalf("GateOpen(0)=true, want false")
	}
}

func TestAGGateFailClosed(t *testing.T) {
	var ag agSampler

	// Empty gate rows.
	if ag.GateOpen(nil) {
		t.Fatalf("GateOpen(nil)=true, want false")
	}
	if ag.GateOpen([]Row{}) {
		t.Fatalf("GateOpen(empty)=true, want false")
	}

	// Nil/unreadable hadr value.
	if ag.GateOpen(hadrRows(nil)) {
		t.Fatalf("GateOpen(hadr=nil)=true, want false")
	}

	// Unknown column.
	if ag.GateOpen([]Row{{"hadr": int64(1), "extra": int64(1)}}) {
		t.Fatalf("GateOpen(unknown column)=true, want false")
	}
}

func TestAGGatedOn(t *testing.T) {
	var ag agSampler
	if !ag.GateOpen(hadrRows(int64(1))) {
		t.Fatalf("GateOpen(1)=false, want true")
	}

	payload, truncated, err := ag.Decode(agRows(AG{DatabaseID: 7, SyncState: 2}))
	if err != nil || truncated {
		t.Fatalf("decode: truncated=%v err=%v", truncated, err)
	}
	got, ok := payload.([]AG)
	if !ok || len(got) != 1 {
		t.Fatalf("payload=%T %#v, want []AG len 1", payload, payload)
	}
	if got[0].DatabaseID != 7 || got[0].SyncState != 2 {
		t.Fatalf("ag=%#v, want {DatabaseID:7 SyncState:2}", got[0])
	}
}

func TestAGNoReplicaNames(t *testing.T) {
	var ag agSampler
	for _, col := range []string{"replica_server_name", "joined_state_desc"} {
		rows := []Row{{
			"databaseId": int64(1),
			"syncState":  int64(1),
			col:         "forbidden",
		}}
		payload, _, err := ag.Decode(rows)
		if err != errUnknownColumn || payload != nil {
			t.Fatalf("column %q: payload=%v err=%v, want nil,errUnknownColumn", col, payload, err)
		}
	}
}

func TestFileStatsRegistered(t *testing.T) {
	var fs filestatsSampler
	var ag agSampler

	if fs.Kind() != "filestats" {
		t.Fatalf("filestats kind=%q, want filestats", fs.Kind())
	}
	if ag.Kind() != "ag" {
		t.Fatalf("ag kind=%q, want ag", ag.Kind())
	}
	if fs.Interval() != DemotedInterval {
		t.Fatalf("filestats interval=%v, want %v", fs.Interval(), DemotedInterval)
	}
	if ag.Interval() != DemotedInterval {
		t.Fatalf("ag interval=%v, want %v", ag.Interval(), DemotedInterval)
	}
	if _, ok := Statement(fs.ID()); !ok {
		t.Fatalf("statement for filestats id %q not found", fs.ID())
	}
	if _, ok := Statement(ag.ID()); !ok {
		t.Fatalf("statement for ag id %q not found", ag.ID())
	}
	if _, ok := Statement(ag.GateID()); !ok {
		t.Fatalf("statement for ag gate id %q not found", ag.GateID())
	}

	var _ Gate = (*agSampler)(nil)
}
```

- **Assumptions:** `decodeRows` exposes `errUnknownColumn` (unexported, same package) as a fixed error value; `Gate` interface is added in `runner.go` exactly as specified (`GateID() string`, `GateOpen(rows []Row) bool`). Used `int64` values in test row helpers since `decodeRows` handles numeric normalization via JSON.
- **Reviewer check:** `filestatsSampler.Decode` emits no entry when all four deltas are zero (explicitly asserted in `TestFileStatsFirstCycleNil`) and keeps `SizeOnDiskBytes` as the current gauge value, not a delta.
- **Reviewer check:** Counter rollback on any of the four cumulative fields resets baseline for that key and skips emission for that cycle; `prev` is rebuilt from the current (capped/truncated) rows each cycle so it stays bounded and drops disappeared keys.
- **Reviewer check:** `agSampler.GateOpen` is fail-closed for decode errors, empty/nil rows, unreadable `hadr`, and unknown columns (all covered in `TestAGGateFailClosed`); it never queries/passes AG DMV data when gated off.
- **Reviewer check:** No SQL construction/mutation, no forbidden columns handled outside `decodeRows`, no goroutines/tickers/time/context/mutex, and no driver-value type assertions in sampler code.
- **Reviewer check:** Both samplers register in `init()` with stable allowlisted IDs/kinds/`DemotedInterval`, and `agSampler` satisfies `Gate` (`var _ Gate = (*agSampler)(nil)`).
- **Reviewer check:** `TestFileStatsNoPaths` and `TestAGNoReplicaNames` assert rejection on the exact forbidden column names listed in the task.
- **Reviewer check:** Tests are in `package bbsql` with local helpers `fileStatsRows`, `agRows`, `hadrRows` and do not assume ordering beyond what `decodeRows`/samplers guarantee.
