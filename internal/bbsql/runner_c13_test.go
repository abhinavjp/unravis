package bbsql

// The C13 half of the wave-10 fix round: the free-space floor, the byte cap and
// the rotation sweep must all fail closed, and C5's connection lifetime must
// re-prove the C8 ceiling even when database/sql recycles the connection under
// the runner's feet.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConnectionLifetimeReprovesCeiling(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	closes := 0
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
		closeFn:  func() error { closes++; return nil },
	})
	clock := now
	r.Now = func() time.Time { return clock }

	if err := r.Tick(clock); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if got := q.countID("bb:ceiling_role"); got != 1 {
		t.Fatalf("ceiling checked %d times on the first connect, want 1", got)
	}

	// Inside the lifetime: the connection is reused, so the ceiling is not sent
	// again - and the sample still runs.
	clock = now.Add(ConnMaxLifetime - time.Minute)
	if err := r.Tick(clock); err != nil {
		t.Fatalf("tick inside the lifetime: %v", err)
	}
	if got := q.countID("bb:ceiling_role"); got != 1 {
		t.Errorf("ceiling re-checked inside the lifetime (%d sends), want 1", got)
	}
	if closes != 0 {
		t.Errorf("connection closed %d times inside the lifetime, want 0", closes)
	}

	// Past the lifetime: database/sql has already thrown the physical
	// connection away, so the runner must treat this as a new connect - close
	// the old handle and prove the rights ceiling before sampling again.
	clock = now.Add(ConnMaxLifetime + time.Minute)
	if err := r.Tick(clock); err != nil {
		t.Fatalf("tick past the lifetime: %v", err)
	}
	if got := q.countID("bb:ceiling_role"); got != 2 {
		t.Errorf("ceiling sends = %d, want 2 (a rights change at the recycle must be caught)", got)
	}
	if closes != 1 {
		t.Errorf("closer ran %d times at the recycle, want 1 (still exactly one connection)", closes)
	}
	if !r.connExpired(clock.Add(ConnMaxLifetime)) {
		t.Error("the connection still live after its lifetime was reported as fresh")
	}
	if r.connExpired(clock.Add(ConnMaxLifetime - time.Second)) {
		t.Error("connExpired reported the live connection as expired a second early")
	}
}

// An unreadable volume is not "plenty of room": the floor exists to protect the
// operator's disk, so a reading that cannot be taken must stop the tick.
func TestUnreadableFreeSpaceStopsTheTick(t *testing.T) {
	now := time.Date(2026, 3, 4, 11, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
	})
	r.MinFreePercent = 10
	r.FreePercent = func(string) (float64, error) { return 0, errors.New("GetVolumePathName failed") }

	err := r.Tick(now)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeDiskLow {
		t.Fatalf("tick error = %v, want code %q", err, CodeDiskLow)
	}
	if strings.Contains(err.Error(), "GetVolumePathName") {
		t.Errorf("the raw volume error survived: %v", err)
	}
	if len(q.texts()) != 0 {
		t.Errorf("statements sent = %d, want 0 (no room proven means no sampling)", len(q.texts()))
	}
	if len(readSampleLines(t, r.Dir)) != 0 {
		t.Error("samples were written on a volume whose free space cannot be read")
	}
	if r.CurrentState().Failures != 0 {
		t.Errorf("failures = %d, want 0 (free space is not an instance failure)", r.CurrentState().Failures)
	}
}

// Age retention alone does not bound the folder: at the measured worst case the
// week is ~2.4 GB. MaxBytes drops whole hours, oldest first, and never the hour
// the service is writing into.
func TestByteCapDropsOldestHours(t *testing.T) {
	now := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
	})
	r.KeepHours = 24 // age alone would keep every file below

	write := func(minutes int, payload string) string {
		p := SamplePath(r.Dir, now.Add(-time.Duration(minutes)*time.Minute))
		if err := os.WriteFile(p, []byte(payload), 0o600); err != nil {
			t.Fatalf("seed %s: %v", filepath.Base(p), err)
		}
		return filepath.Base(p)
	}
	oldest := write(300, strings.Repeat("a", 900)+"\n")
	middle := write(180, strings.Repeat("b", 900)+"\n")
	keptOld := write(120, strings.Repeat("c", 900)+"\n")
	keptNew := write(60, strings.Repeat("d", 900)+"\n")

	// Room for two of the four hours; the oldest two must go.
	r.MaxBytes = int64(2*901 + 200)
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	for _, gone := range []string{oldest, middle} {
		if _, err := os.Stat(filepath.Join(r.Dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived the byte cap, want dropped (oldest hours go first)", gone)
		}
	}
	for _, kept := range []string{keptOld, keptNew} {
		if _, err := os.Stat(filepath.Join(r.Dir, kept)); err != nil {
			t.Errorf("%s was dropped: %v", kept, err)
		}
	}

	// A cap smaller than one hour still keeps the newest hour: the reader must
	// always find a file, and the cap cannot eat the folder down to nothing.
	// The tick itself has just written this hour, so that is the survivor.
	r.MaxBytes = 1
	r.rotated = ""
	if err := r.Tick(now.Add(BaseInterval)); err != nil {
		t.Fatalf("tick with an impossible cap: %v", err)
	}
	left, err := filepath.Glob(filepath.Join(r.Dir, "bb-sql-*.ndjson"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(left) != 1 {
		t.Errorf("hours left after an impossible cap = %d, want 1 (never zero, never all)", len(left))
	}
	if _, err := os.Stat(SamplePath(r.Dir, now)); err != nil {
		t.Errorf("this hour's file was dropped by the byte cap: %v", err)
	}

	// The end state above cannot tell "kept the newest hour" from "deleted
	// everything and let the tick rewrite it", so the sweep itself is called
	// with room for nothing: the newest hour must survive the delete loop, not
	// be re-created afterwards.
	if err := r.rotate(now.UTC()); err != nil {
		t.Fatalf("rotate with an impossible cap: %v", err)
	}
	left, err = filepath.Glob(filepath.Join(r.Dir, "bb-sql-*.ndjson"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(left) != 1 {
		t.Errorf("hours left by the sweep = %d, want 1 (the cap may not empty the folder)", len(left))
	}
	if len(left) == 1 && filepath.Base(left[0]) != filepath.Base(SamplePath(r.Dir, now)) {
		t.Errorf("the sweep kept %q, want the newest hour %q", filepath.Base(left[0]),
			filepath.Base(SamplePath(r.Dir, now)))
	}
}

// A sweep that cannot run must be retried, not marked as done: one unreadable
// directory entry must not leave the folder unbounded for the rest of the hour
// with no signal at all.
func TestSweepFailureIsRetriedAndFixed(t *testing.T) {
	now := time.Date(2026, 3, 4, 13, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
	})
	// A file where the sample folder should be: os.ReadDir cannot list it.
	blocked := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.Dir = blocked

	err := r.Tick(now)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeDiskLow {
		t.Fatalf("tick error = %v, want code %q", err, CodeDiskLow)
	}
	if !strings.Contains(e.Message, "swept") {
		t.Errorf("message = %q, want the sweep sentence, not a raw error", e.Message)
	}
	if r.rotated != "" {
		t.Errorf("rotated = %q after a failed sweep, want still unset so the next tick retries", r.rotated)
	}
	if len(q.texts()) != 0 {
		t.Errorf("statements sent = %d, want 0 (the folder is not usable)", len(q.texts()))
	}
}
