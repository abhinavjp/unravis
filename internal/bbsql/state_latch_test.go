package bbsql

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// W7-04: only a complete, consistent, single JSON record may re-enable sampling.
// Every one of these used to normalize into an enabled state.
func TestParseStateRefusesUntrustedRecords(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"null", `null`},
		{"empty object", `{}`},
		{"missing autoOff", `{"interval":15000000000,"failures":0,"slowStrikes":0,"state":"on"}`},
		{"missing failures", `{"interval":15000000000,"autoOff":false,"slowStrikes":0,"state":"on"}`},
		{"missing state", `{"interval":15000000000,"failures":0,"autoOff":false,"slowStrikes":0}`},
		{"unknown key", `{"interval":15000000000,"failures":0,"autoOff":false,"slowStrikes":0,"state":"on","extra":1}`},
		{"trailing data", `{"interval":15000000000,"failures":0,"autoOff":false,"slowStrikes":0,"state":"on"}{}`},
		{"negative failures", `{"interval":15000000000,"failures":-1,"autoOff":false,"slowStrikes":0,"state":"on"}`},
		{"negative strikes", `{"interval":15000000000,"failures":0,"autoOff":false,"slowStrikes":-1,"state":"on"}`},
		{"zero interval", `{"interval":0,"failures":0,"autoOff":false,"slowStrikes":0,"state":"on"}`},
		{"unknown label", `{"interval":15000000000,"failures":0,"autoOff":false,"slowStrikes":0,"state":"maybe"}`},
		{"latch without label", `{"interval":15000000000,"failures":0,"autoOff":true,"slowStrikes":0,"state":"on"}`},
		{"label without latch", `{"interval":15000000000,"failures":0,"autoOff":false,"slowStrikes":0,"state":"auto-off"}`},
		{"denied without latch", `{"interval":15000000000,"failures":0,"autoOff":false,"slowStrikes":0,"state":"denied"}`},
		{"threshold not latched", `{"interval":900000000000,"failures":10,"autoOff":false,"slowStrikes":0,"state":"backing-off"}`},
		{"not json", `garbage`},
	} {
		if _, err := parseState([]byte(tc.body)); err == nil {
			t.Errorf("%s: accepted, want a refusal", tc.name)
		}
	}
}

func TestParseStateAcceptsACompleteRecord(t *testing.T) {
	s := State{Interval: 30 * time.Second, Failures: 2, AutoOff: false, SlowStrikes: 1, State: StateBackingOff}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseState(data)
	if err != nil {
		t.Fatalf("a complete record was refused: %v", err)
	}
	if got.Interval != s.Interval || got.Failures != s.Failures || got.SlowStrikes != s.SlowStrikes || got.State != s.State || got.AutoOff {
		t.Fatalf("round trip = %+v, want %+v", got, s)
	}
	// A genuine denied latch round-trips too.
	latched := State{Interval: BaseInterval, AutoOff: true, State: StateDenied}
	data, _ = json.Marshal(latched)
	got, err = parseState(data)
	if err != nil || !got.AutoOff || got.State != StateDenied {
		t.Fatalf("denied latch = %+v err = %v, want auto-off denied", got, err)
	}
}

// W10-04: the state is replaced atomically and leaves no temp file behind.
func TestSaveIsAtomicAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Save(path, NewState()); err != nil {
		t.Fatalf("first save: %v", err)
	}
	latched := State{Interval: BaseInterval, AutoOff: true, State: StateDenied}
	if err := Save(path, latched); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err := Load(path)
	if err != nil || !got.AutoOff || got.State != StateDenied {
		t.Fatalf("reload = %+v err = %v, want the denied latch", got, err)
	}
	left, err := filepath.Glob(filepath.Join(dir, ".state-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// W10-04: a latch that never reached disk is retried even while sampling is off,
// and a restart after it lands must still be auto-off.
func TestAutoOffLatchRetriesUntilItLands(t *testing.T) {
	now := time.Date(2026, 3, 4, 15, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{baseSampler("bb:waits", "waits")}})
	r.state = State{Interval: BaseInterval, AutoOff: true, State: StateDenied}

	statePath := filepath.Join(r.Dir, "state.json")
	// A directory where the state file belongs makes the atomic rename fail: a
	// real filesystem fault, not a mocked error.
	if err := os.MkdirAll(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := r.persist(); err == nil {
		t.Fatal("persist succeeded despite an unwritable state path")
	}
	if !r.stateDirty {
		t.Fatal("a failed persist did not mark the state dirty")
	}
	// While auto-off the tick must keep trying, not silently give up.
	if err := r.Tick(now); err != nil {
		t.Fatalf("auto-off tick: %v", err)
	}
	if !r.stateDirty {
		t.Fatal("the auto-off tick dropped the pending latch")
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state path changed shape: %v", err)
	}

	// Once the fault clears, the very next tick lands the latch.
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := r.Tick(now.Add(BaseInterval)); err != nil {
		t.Fatalf("recovered tick: %v", err)
	}
	if r.stateDirty {
		t.Fatal("the latch was written but the dirty flag was not cleared")
	}
	got, err := Load(statePath)
	if err != nil || !got.AutoOff || got.State != StateDenied {
		t.Fatalf("reload after recovery = %+v err = %v, want the denied latch", got, err)
	}
}
