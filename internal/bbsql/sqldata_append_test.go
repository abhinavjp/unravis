package bbsql

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// W10-10: a partial line must never be glued to the next record, and a filesystem
// fault must be a fixed write code tracked apart from SQL authorization/instance
// state — never a "permission" classification that denies a healthy instance.

func TestAppendRepairsTornTail(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir, ConnConfig{})
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	path := SamplePath(dir, now)
	complete := `{"v":1,"t":"2026-03-04T12:00:00Z","kind":"waits","rows":null}`

	// A complete line followed by a torn partial line (no final newline).
	if err := os.WriteFile(path, []byte(complete+"\n"+`{"v":1,"part`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.appendSample(path, SampleDoc{V: 1, T: now, Kind: "waits"}); err != nil {
		t.Fatalf("append after torn tail: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d (%q), want 2 (the torn partial removed, the new record added)", len(lines), data)
	}
	if lines[0] != complete {
		t.Errorf("the previously complete line was altered: %q", lines[0])
	}
	for i, ln := range lines {
		if !json.Valid([]byte(ln)) {
			t.Errorf("line %d is not valid JSON (partial glued?): %q", i, ln)
		}
	}
}

func TestAppendRepairsInvalidLastLine(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(dir, ConnConfig{})
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	path := SamplePath(dir, now)
	complete := `{"v":1,"t":"2026-03-04T12:00:00Z","kind":"waits","rows":null}`
	// A last line that ends in a newline but is not valid JSON.
	if err := os.WriteFile(path, []byte(complete+"\n"+"not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.appendSample(path, SampleDoc{V: 1, T: now, Kind: "waits"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 || lines[0] != complete {
		t.Fatalf("lines = %q, want the complete line preserved and the invalid line removed", lines)
	}
	for _, ln := range lines {
		if !json.Valid([]byte(ln)) {
			t.Errorf("invalid line survived: %q", ln)
		}
	}
}

func TestAppendRepairIsBoundedAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	path := SamplePath(dir, now)
	if err := os.WriteFile(path, []byte(`{"v":1,"t":"2026-03-04T12:00:00Z","kind":"waits","rows":null}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A clean file is left byte-for-byte unchanged by repair.
	before, _ := os.ReadFile(path)
	if err := repairTornTail(path); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("repair altered a clean file: %q -> %q", before, after)
	}
	// A missing file is fine.
	if err := repairTornTail(SamplePath(dir, now.Add(time.Hour))); err != nil {
		t.Errorf("repair of a missing file: %v", err)
	}
}

// TestFilesystemFailureIsNotSQLFailure: a write fault on the sample file returns
// the fixed write code, leaves State (instance failures) untouched, keeps the
// healthy connection, and is counted separately (W10-10).
func TestFilesystemFailureIsNotSQLFailure(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{baseSampler("bb:waits", "waits")}})
	// A directory where the sample file belongs makes the append fail.
	if err := os.MkdirAll(SamplePath(r.Dir, now), 0o700); err != nil {
		t.Fatal(err)
	}
	err := r.Tick(now)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeWriteFailed {
		t.Fatalf("tick error = %v, want fixed code %q", err, CodeWriteFailed)
	}
	if strings.Contains(strings.ToLower(err.Error()), "permission") {
		t.Errorf("the write error carries permission wording that could be misclassified: %v", err)
	}
	if r.CurrentState().Failures != 0 {
		t.Errorf("instance failures = %d, want 0 (a disk fault is not a SQL failure)", r.CurrentState().Failures)
	}
	if r.CurrentState().AutoOff {
		t.Error("a disk fault latched auto-off, permanently denying a healthy instance")
	}
	if r.FSWriteFailures() != 1 {
		t.Errorf("fs write failures = %d, want 1 (tracked separately)", r.FSWriteFailures())
	}
	if !r.connected {
		t.Error("the healthy SQL connection was dropped by a filesystem fault")
	}
}
