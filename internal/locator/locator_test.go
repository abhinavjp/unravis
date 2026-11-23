package locator

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/ownerlease"
)

const (
	uiName = `\\.\pipe\unravis.ui.0123456789abcdef0123456789abcdef`
	agName = `\\.\pipe\unravis.ag.0123456789abcdef0123456789abcdef`
)

var t0 = time.Date(2026, 11, 22, 10, 0, 0, 123000000, time.UTC)

func rec() Record {
	return Record{Version: Schema, PID: 4242, StartTime: t0, Exe: `C:\Program Files\Unravis\unravis.exe`, AppVersion: "1.2.3", UI: uiName, AG: agName}
}

type fakeProber struct {
	start map[int]time.Time
	pipes map[string]bool
}

func (f fakeProber) ProcessStart(pid int) (time.Time, error) {
	if s, ok := f.start[pid]; ok {
		return s, nil
	}
	return time.Time{}, errors.New("no such process")
}
func (f fakeProber) PipeExists(n string) bool { return f.pipes[n] }

func alive() fakeProber {
	return fakeProber{start: map[int]time.Time{4242: t0}, pipes: map[string]bool{uiName: true, agName: true}}
}

func TestLocatorRoundTrip(t *testing.T) {
	path := FilePath(t.TempDir(), false)
	if filepath.Base(path) != "backend.json" {
		t.Fatalf("name = %s, want backend.json", filepath.Base(path))
	}
	var secured []string
	o := Options{SecureDir: func(d string) error { secured = append(secured, d); return nil }, SecureFile: func(p string) error { secured = append(secured, p); return nil }}
	if err := Write(path, rec(), o); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 4242 || got.UI != uiName || got.AG != agName || !got.StartTime.Equal(t0) || got.Exe == "" || got.AppVersion != "1.2.3" {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if len(secured) != 2 {
		t.Fatalf("secure calls = %v, want dir then file before content", secured)
	}
	if left, _ := filepath.Glob(path + ".*.tmp"); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	// Replace, not append.
	r2 := rec()
	r2.PID = 7
	if err := Write(path, r2, o); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read(path); got.PID != 7 {
		t.Fatalf("second write not visible: %+v", got)
	}
}

func TestLocatorHasNoTokenField(t *testing.T) {
	path := FilePath(t.TempDir(), false)
	if err := Write(path, rec(), Options{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path) //nolint:gosec // test path
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		if strings.Contains(strings.ToLower(k), "token") || strings.Contains(strings.ToLower(k), "secret") {
			t.Fatalf("locator has a credential field %q", k)
		}
	}
	for _, k := range []string{"version", "pid", "startTime", "exe", "appVersion", "ui", "ag"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing field %q", k)
		}
	}
}

func TestLocatorDemoFileIsSeparate(t *testing.T) {
	if FilePath("x", true) == FilePath("x", false) {
		t.Fatal("demo and real locators share a file")
	}
}

func TestLocatorAliveDeadPid(t *testing.T) {
	p := alive()
	if !Alive(rec(), p) {
		t.Fatal("a live owner was reported dead")
	}
	delete(p.start, 4242)
	if Alive(rec(), p) {
		t.Fatal("a dead pid was reported alive")
	}
	p = alive()
	delete(p.pipes, agName)
	if Alive(rec(), p) {
		t.Fatal("a missing pipe was reported alive")
	}
}

func TestLocatorAliveReusedPid(t *testing.T) {
	p := alive()
	p.start[4242] = t0.Add(3 * time.Hour) // another process got the pid later
	if Alive(rec(), p) {
		t.Fatal("a reused pid was reported alive")
	}
}

func TestLocatorRejectsUnknownVersion(t *testing.T) {
	path := FilePath(t.TempDir(), false)
	r := rec()
	r.Version = 3
	b, _ := json.Marshal(r)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrUnknownSchema) {
		t.Fatalf("err = %v, want ErrUnknownSchema", err)
	}
	if err := Write(path, r, Options{}); !errors.Is(err, ErrUnknownSchema) {
		t.Fatalf("write err = %v, want ErrUnknownSchema", err)
	}
}

func TestLocatorRejectsBadContent(t *testing.T) {
	dir := t.TempDir()
	path := FilePath(dir, false)
	if _, err := Read(path); !errors.Is(err, ErrNoLocator) {
		t.Fatalf("missing file: %v", err)
	}
	bad := []Record{}
	for _, mut := range []func(*Record){
		func(r *Record) { r.PID = 0 },
		func(r *Record) { r.UI = `\\.\pipe\evil` },
		func(r *Record) { r.UI, r.AG = r.AG, r.UI }, // roles swapped
		func(r *Record) { r.AG = `\\.\pipe\unravis.ag.XYZ` },
		func(r *Record) { r.StartTime = time.Time{} },
		func(r *Record) { r.AG = `\\.\pipe\unravis.ag.fedcba9876543210fedcba9876543210` }, // different nonce
	} {
		r := rec()
		mut(&r)
		bad = append(bad, r)
	}
	for i, r := range bad {
		b, _ := json.Marshal(r)
		_ = os.WriteFile(path, b, 0o600)
		if _, err := Read(path); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: err = %v, want ErrInvalid", i, err)
		}
	}
	_ = os.WriteFile(path, []byte("{not json"), 0o600)
	if _, err := Read(path); !errors.Is(err, ErrInvalid) {
		t.Errorf("garbage: %v", err)
	}
	_ = os.WriteFile(path, []byte(strings.Repeat("a", maxFileBytes+10)), 0o600)
	if _, err := Read(path); !errors.Is(err, ErrInvalid) {
		t.Errorf("oversize: %v", err)
	}
}

func TestLocatorRemoveOnlyOwn(t *testing.T) {
	path := FilePath(t.TempDir(), false)
	_ = Write(path, rec(), Options{})
	other := rec()
	other.PID = 99
	if err := Remove(path, other); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err != nil {
		t.Fatal("a foreign owner removed the file")
	}
	if err := Remove(path, rec()); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrNoLocator) {
		t.Fatalf("own file not removed: %v", err)
	}
	if err := Remove(path, rec()); err != nil {
		t.Fatalf("remove of a missing file: %v", err)
	}
}

func TestAttachExistingOwnerDoesNotStart(t *testing.T) {
	path := FilePath(t.TempDir(), false)
	_ = Write(path, rec(), Options{})
	started := false
	r, st, err := Attach(AttachOptions{Path: path, Prober: alive(), Start: func() error { started = true; return nil }, Sleep: func(time.Duration) {}})
	if err != nil || st || started || r.PID != 4242 {
		t.Fatalf("r=%+v started=%v/%v err=%v", r, st, started, err)
	}
}

func TestStaleLocatorReplacedAfterStart(t *testing.T) {
	path := FilePath(t.TempDir(), false)
	_ = Write(path, rec(), Options{}) // owner 4242 is dead (no prober entry)
	p := fakeProber{start: map[int]time.Time{}, pipes: map[string]bool{}}
	newRec := rec()
	newRec.PID = 5555
	sleeps := 0
	start := func() error { // the new owner publishes after a couple of polls
		return nil
	}
	sleep := func(time.Duration) {
		sleeps++
		if sleeps == 2 {
			_ = Write(path, newRec, Options{})
			p.start[5555] = t0
			p.pipes[uiName], p.pipes[agName] = true, true
		}
	}
	r, st, err := Attach(AttachOptions{Path: path, Prober: p, Start: start, Sleep: sleep, Tries: 10, Wait: time.Millisecond})
	if err != nil || !st || r.PID != 5555 {
		t.Fatalf("r=%+v started=%v err=%v", r, st, err)
	}
}

func TestAttachTimesOutAndWithoutStart(t *testing.T) {
	path := FilePath(t.TempDir(), false)
	p := fakeProber{start: map[int]time.Time{}, pipes: map[string]bool{}}
	if _, _, err := Attach(AttachOptions{Path: path, Prober: p}); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("attach only: %v", err)
	}
	sleeps := 0
	_, st, err := Attach(AttachOptions{Path: path, Prober: p, Start: func() error { return nil }, Sleep: func(time.Duration) { sleeps++ }, Tries: 5, Wait: time.Millisecond})
	if !errors.Is(err, ErrNoOwner) || !st || sleeps != 4 {
		t.Fatalf("timeout: err=%v started=%v sleeps=%d", err, st, sleeps)
	}
	if _, _, err := Attach(AttachOptions{Path: path, Prober: p, Start: func() error { return errors.New("boom") }}); err == nil {
		t.Fatal("start error swallowed")
	}
}

// SecureDir sets an inheriting DACL, so Write must not call it on a folder that holds other files (UNRAVIS_RUN_DIR can point anywhere).
func TestWriteRefusesToSecureAForeignFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	o := Options{SecureDir: func(string) error { called = true; return nil }}
	if err := Write(FilePath(dir, false), rec(), o); err == nil {
		t.Fatal("Write restricted a folder that holds a foreign file")
	}
	if called {
		t.Fatal("SecureDir was called on a foreign folder")
	}
	// A folder with only locator files (a previous owner's file and temp file) and the owner lease files is fine.
	clean := t.TempDir()
	for _, n := range []string{FileName, DemoFileName, FileName + ".1.tmp", ownerlease.FileName, ownerlease.DemoFileName} {
		if err := os.WriteFile(filepath.Join(clean, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Write(FilePath(clean, false), rec(), o); err != nil || !called {
		t.Fatalf("locator-only folder: err=%v called=%v", err, called)
	}
}
