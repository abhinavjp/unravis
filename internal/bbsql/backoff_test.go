package bbsql

import (
	"encoding/json"
	"io/fs"
	"testing"
	"time"
)

// fakeDisk swaps the persistence seam for an in-memory map (no real disk).
func fakeDisk(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	oldRead, oldWrite := readFile, writeFile
	readFile = func(p string) ([]byte, error) {
		b, ok := files[p]
		if !ok {
			return nil, fs.ErrNotExist
		}
		return b, nil
	}
	writeFile = func(p string, b []byte) error {
		files[p] = append([]byte(nil), b...)
		return nil
	}
	t.Cleanup(func() { readFile, writeFile = oldRead, oldWrite })
	return files
}

func TestBackoffDoubles(t *testing.T) {
	s := NewState()
	want := []time.Duration{
		30 * time.Second, 60 * time.Second, 120 * time.Second,
		240 * time.Second, 480 * time.Second,
		15 * time.Minute, 15 * time.Minute, 15 * time.Minute, 15 * time.Minute,
	}
	for i, w := range want {
		s.OnFailure(false, false)
		if s.Interval != w {
			t.Fatalf("failure %d: interval = %v, want %v", i+1, s.Interval, w)
		}
		if s.Interval < BaseInterval || s.Interval > MaxInterval {
			t.Fatalf("failure %d: interval %v out of bounds", i+1, s.Interval)
		}
		if s.State != StateBackingOff || s.AutoOff {
			t.Fatalf("failure %d: state = %q autoOff = %v", i+1, s.State, s.AutoOff)
		}
	}
}

func TestBackoffTenFailuresAutoOff(t *testing.T) {
	s := NewState()
	for i := 0; i < 9; i++ {
		s.OnFailure(false, false)
	}
	if s.AutoOff {
		t.Fatal("auto-off before ten failures")
	}
	s.OnFailure(false, false)
	if !s.AutoOff || s.State != StateAutoOff {
		t.Fatalf("after ten failures: autoOff = %v state = %q", s.AutoOff, s.State)
	}
	if s.Interval != MaxInterval {
		t.Fatalf("interval = %v, want cap %v", s.Interval, MaxInterval)
	}
}

func TestBackoffPermissionAutoOff(t *testing.T) {
	s := NewState()
	s.OnFailure(true, false)
	if !s.AutoOff || s.State != StateDenied {
		t.Fatalf("permission failure: autoOff = %v state = %q", s.AutoOff, s.State)
	}
}

func TestBackoffCeilingAutoOff(t *testing.T) {
	s := NewState()
	s.OnFailure(false, true)
	if !s.AutoOff || s.State != StateDenied {
		t.Fatalf("ceiling failure: autoOff = %v state = %q", s.AutoOff, s.State)
	}
}

func TestAutoOffSurvivesReload(t *testing.T) {
	files := fakeDisk(t)
	path := StatePath(`C:\svc`)
	s := NewState()
	s.OnFailure(true, false) // immediate denied auto-off
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	// Only fixed fields are persisted (no error text or instance names).
	var raw map[string]any
	if err := json.Unmarshal(files[path], &raw); err != nil {
		t.Fatal(err)
	}
	for k := range raw {
		switch k {
		case "interval", "failures", "autoOff", "slowStrikes", "state":
		default:
			t.Fatalf("unexpected persisted field %q", k)
		}
	}
	// Two "restarts": Load keeps auto-off; success cannot clear it either.
	for i := 0; i < 2; i++ {
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if !got.AutoOff || got.State != StateDenied {
			t.Fatalf("reload %d: autoOff = %v state = %q", i, got.AutoOff, got.State)
		}
		got.OnSuccess()
		if !got.AutoOff {
			t.Fatal("OnSuccess cleared persisted auto-off")
		}
	}
	// Ten-failure auto-off also survives, and a tampered "on" label cannot
	// re-enable a persisted auto-off.
	s2 := NewState()
	for i := 0; i < 10; i++ {
		s2.OnFailure(false, false)
	}
	if err := Save(path, s2); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !got.AutoOff || got.State != StateAutoOff {
		t.Fatalf("ten-failure reload: %+v err = %v", got, err)
	}
	files[path] = []byte(`{"autoOff":true,"state":"on","interval":15000000000}`)
	got, err = Load(path)
	if err != nil || !got.AutoOff || got.State != StateAutoOff {
		t.Fatalf("tampered reload: %+v err = %v", got, err)
	}
}

func TestOwnerEnableClears(t *testing.T) {
	fakeDisk(t)
	path := StatePath(`C:\svc`)
	s := NewState()
	s.OnFailure(true, false)
	s.OnSuccess()
	if !s.AutoOff {
		t.Fatal("success cleared auto-off")
	}
	OwnerEnable(&s)
	if s.AutoOff || s.State != StateOn || s.Interval != BaseInterval || s.Failures != 0 {
		t.Fatalf("after OwnerEnable: %+v", s)
	}
	if err := Save(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.AutoOff || got.State != StateOn {
		t.Fatalf("reload after owner enable: %+v err = %v", got, err)
	}
}

func TestSlowDemotion(t *testing.T) {
	limit := 15 * time.Second
	s := NewState()
	s.OnSlow(8*time.Second, limit)
	s.OnSlow(8*time.Second, limit)
	if s.Interval != BaseInterval {
		t.Fatalf("demoted early: %v", s.Interval)
	}
	s.OnSlow(8*time.Second, limit)
	if s.Interval != 60*time.Second {
		t.Fatalf("after three slow strikes: interval = %v, want 60s", s.Interval)
	}
	// A fast query resets the strike count.
	s2 := NewState()
	s2.OnSlow(8*time.Second, limit)
	s2.OnSlow(8*time.Second, limit)
	s2.OnSlow(7*time.Second, limit) // <= 50%: reset
	s2.OnSlow(8*time.Second, limit)
	s2.OnSlow(8*time.Second, limit)
	if s2.SlowStrikes != 2 || s2.Interval != BaseInterval {
		t.Fatalf("strike reset failed: %+v", s2)
	}
	// Exactly 50% is not a strike.
	s3 := NewState()
	s3.OnSlow(limit/2, limit)
	if s3.SlowStrikes != 0 {
		t.Fatalf("exact half counted as strike: %d", s3.SlowStrikes)
	}
	// Demotion never lowers a longer back-off interval.
	s4 := NewState()
	for i := 0; i < 3; i++ {
		s4.OnFailure(false, false)
	}
	for i := 0; i < 3; i++ {
		s4.OnSlow(8*time.Second, limit)
	}
	if s4.Interval != 120*time.Second {
		t.Fatalf("demotion lowered back-off interval: %v", s4.Interval)
	}
}

func TestSuccessRecoversBackoff(t *testing.T) {
	s := NewState()
	for i := 0; i < 3; i++ {
		s.OnFailure(false, false)
	}
	if s.Interval != 120*time.Second || s.State != StateBackingOff {
		t.Fatalf("setup: %+v", s)
	}
	want := []time.Duration{60 * time.Second, 30 * time.Second, 15 * time.Second, 15 * time.Second}
	for _, w := range want {
		s.OnSuccess()
		if s.Interval != w {
			t.Fatalf("interval = %v, want %v", s.Interval, w)
		}
		if s.AutoOff || s.State != StateOn || s.Failures != 0 {
			t.Fatalf("unexpected state: %+v", s)
		}
	}
}

func TestLoadMissingFileStartsOn(t *testing.T) {
	fakeDisk(t)
	s, err := Load(StatePath(`C:\svc`))
	if err != nil || s.AutoOff || s.State != StateOn || s.Interval != BaseInterval {
		t.Fatalf("fresh load: %+v err = %v", s, err)
	}
}

func TestLoadCorruptFailsClosed(t *testing.T) {
	files := fakeDisk(t)
	path := StatePath(`C:\svc`)
	files[path] = []byte("{not json")
	s, err := Load(path)
	if err == nil {
		t.Fatal("expected error for corrupt state")
	}
	if !s.AutoOff || s.State != StateAutoOff {
		t.Fatalf("corrupt state must fail closed (auto-off): %+v", s)
	}
}