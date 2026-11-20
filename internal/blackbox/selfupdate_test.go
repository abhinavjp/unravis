package blackbox

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/update"
)

type suRig struct {
	u                  *SelfUpdater
	checks, stages     int
	applies            int
	appliedPath, sha   string
	res                update.Result
	stageErr, applyErr error
}

func newSURig(t *testing.T, mode string) *suRig {
	t.Helper()
	r := &suRig{}
	r.res = update.Result{Status: update.StatusAvailable, Manifest: &update.Manifest{Version: "1.2.0"}}
	r.u = &SelfUpdater{
		Mode: mode, Interval: time.Hour, Version: "1.1.0", Dir: t.TempDir(),
		Check: func(context.Context) update.Result { r.checks++; return r.res },
		Stage: func(_ context.Context, m *update.Manifest) (string, string, error) {
			r.stages++
			return "/stage/" + m.Version + "/p.exe", strings.Repeat("c", 64), r.stageErr
		},
		Apply: func(p, s string) error { r.applies++; r.appliedPath, r.sha = p, s; return r.applyErr },
	}
	return r
}

func (r *suRig) marker(t *testing.T) (pending, bool) {
	t.Helper()
	var p pending
	b, err := os.ReadFile(filepath.Join(r.u.Dir, pendingName))
	if err != nil {
		return p, false
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p, true
}

func TestInstallModeStagesAppliesAndWritesTheMarker(t *testing.T) {
	r := newSURig(t, UpdateInstall)
	r.u.CheckOnce(context.Background())
	if r.stages != 1 || r.applies != 1 || r.appliedPath != "/stage/1.2.0/p.exe" || r.sha != strings.Repeat("c", 64) {
		t.Fatalf("%+v", r)
	}
	if p, ok := r.marker(t); !ok || p.From != "1.1.0" || p.To != "1.2.0" {
		t.Fatalf("marker %+v %v", p, ok)
	}
	if s := r.u.Status(); s.State != "installing" || s.Latest != "1.2.0" {
		t.Fatalf("%+v", s)
	}
}

func TestNotifyModeInstallsNothing(t *testing.T) {
	r := newSURig(t, UpdateNotify)
	r.u.CheckOnce(context.Background())
	if r.stages != 0 || r.applies != 0 {
		t.Fatal("notify mode downloaded or installed")
	}
	if s := r.u.Status(); s.State != "available" || s.Latest != "1.2.0" {
		t.Fatalf("%+v", s)
	}
}

func TestOffModeMakesNoCall(t *testing.T) {
	r := newSURig(t, UpdateOff)
	r.u.CheckOnce(context.Background())
	if r.checks != 0 {
		t.Fatal("off mode checked")
	}
}

func TestNothingNewIsIdle(t *testing.T) {
	r := newSURig(t, UpdateInstall)
	r.res = update.Result{Status: update.StatusLatest}
	r.u.CheckOnce(context.Background())
	if r.stages != 0 || r.u.Status().State != "idle" {
		t.Fatalf("%+v", r.u.Status())
	}
}

func TestAFailedPrepareStartsNothingAndLeavesNoMarker(t *testing.T) {
	r := newSURig(t, UpdateInstall)
	r.stageErr = errors.New("hash differs")
	r.u.CheckOnce(context.Background())
	if r.applies != 0 {
		t.Fatal("applied a program that was not prepared")
	}
	if _, ok := r.marker(t); ok {
		t.Fatal("marker written")
	}
	if s := r.u.Status(); s.State != "failed" {
		t.Fatalf("%+v", s)
	}
}

func TestAnApplyThatCannotStartRemovesTheMarker(t *testing.T) {
	r := newSURig(t, UpdateInstall)
	r.applyErr = errors.New("no")
	r.u.CheckOnce(context.Background())
	if _, ok := r.marker(t); ok {
		t.Fatal("marker left after a failed start")
	}
}

func TestReconcileSuccessAndFailure(t *testing.T) {
	r := newSURig(t, UpdateInstall)
	r.u.CheckOnce(context.Background()) // marker 1.1.0 -> 1.2.0
	r.u.Version = "1.2.0"               // the service restarted as the new version
	r.u.Reconcile(context.Background())
	if _, ok := r.marker(t); ok || r.u.tracker().Skipped("1.2.0") {
		t.Fatal("a finished update must clear the marker and not skip the version")
	}

	r2 := newSURig(t, UpdateInstall)
	r2.u.CheckOnce(context.Background())
	r2.u.Reconcile(context.Background()) // still 1.1.0: the update was rolled back
	if _, ok := r2.marker(t); ok || !r2.u.tracker().Skipped("1.2.0") {
		t.Fatal("a failed update must skip that version")
	}
	// the next check does not try it again
	r2.stages = 0
	r2.u.CheckOnce(context.Background())
	if r2.stages != 0 || r2.u.Status().State != "skipped" {
		t.Fatalf("tried a skipped version again: %+v", r2.u.Status())
	}
	// a newer version is offered
	r2.res.Manifest = &update.Manifest{Version: "1.3.0"}
	r2.u.CheckOnce(context.Background())
	if r2.stages != 1 {
		t.Fatal("the next version was not tried")
	}
}

func TestReconcileDropsAGarbageMarkerAndCleansStaging(t *testing.T) {
	r := newSURig(t, UpdateInstall)
	os.WriteFile(filepath.Join(r.u.Dir, pendingName), []byte("not json"), 0o600)
	os.MkdirAll(filepath.Join(r.u.Dir, "stage-1.2.0"), 0o700)
	os.WriteFile(filepath.Join(r.u.Dir, "unravis-blackbox-v1.2.0-windows-x64.zip"), []byte("z"), 0o600)
	os.WriteFile(filepath.Join(r.u.Dir, "keep.txt"), []byte("k"), 0o600)
	r.u.Reconcile(context.Background())
	if _, ok := r.marker(t); ok {
		t.Fatal("garbage marker kept")
	}
	// a valid marker run also cleans
	r.u.CheckOnce(context.Background())
	r.u.Reconcile(context.Background())
	if _, err := os.Stat(filepath.Join(r.u.Dir, "stage-1.2.0")); err == nil {
		t.Fatal("staging folder left")
	}
	if _, err := os.Stat(filepath.Join(r.u.Dir, "keep.txt")); err != nil {
		t.Fatal("cleanup touched a file it did not make")
	}
}

func TestStatusNeverCarriesAPathOrAnAddress(t *testing.T) {
	r := newSURig(t, UpdateInstall)
	r.stageErr = errors.New("the download answered 404")
	r.u.CheckOnce(context.Background())
	b, _ := json.Marshal(r.u.Status())
	for _, bad := range []string{"http", "/stage", r.u.Dir} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("status leaks %q: %s", bad, b)
		}
	}
}

func TestRunStopsWithItsContext(t *testing.T) {
	r := newSURig(t, UpdateInstall)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.u.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	if r.checks != 0 {
		t.Fatal("checked before its first delay")
	}
}

func TestAutoUpdateConfig(t *testing.T) {
	c := Default()
	c.Name = "box"
	if c.UpdateMode() != UpdateInstall || c.UpdateInterval() != 30*time.Minute {
		t.Fatal("defaults")
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), "autoUpdate") {
		t.Fatal("a default config must not write the block: an older Blackbox rejects unknown keys")
	}
	for _, ok := range []*AutoUpdate{nil, {}, {Mode: UpdateOff}, {Mode: UpdateNotify, IntervalMinutes: 30}, {IntervalMinutes: 1440}} {
		if err := ok.validate(); err != nil {
			t.Errorf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []*AutoUpdate{{Mode: "yes"}, {IntervalMinutes: 29}, {IntervalMinutes: 1441}, {IntervalMinutes: -5}} {
		if bad.validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	c.AutoUpdate = &AutoUpdate{Mode: UpdateNotify, IntervalMinutes: 60}
	if c.UpdateMode() != UpdateNotify || c.UpdateInterval() != time.Hour {
		t.Fatal("values")
	}
}

func makeZip(t *testing.T, files map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, d := range files {
		w, _ := zw.Create(n)
		w.Write(d)
	}
	zw.Close()
	p := filepath.Join(t.TempDir(), "p.zip")
	os.WriteFile(p, buf.Bytes(), 0o600)
	return p
}

func TestStageFromZip(t *testing.T) {
	prog := []byte("the new program")
	sum := sha256.Sum256(prog)
	want := hex.EncodeToString(sum[:])
	stage := filepath.Join(t.TempDir(), "stage-1.2.0")
	path, got, err := stageFromZip(makeZip(t, map[string][]byte{"unravis-blackbox.exe": prog, "README.txt": []byte("x")}), stage, want)
	if err != nil || got != want || filepath.Base(path) != "unravis-blackbox.exe" {
		t.Fatalf("%q %q %v", path, got, err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, prog) {
		t.Fatal("content")
	}
	for name, files := range map[string]map[string][]byte{
		"wrong program": {"unravis-blackbox.exe": []byte("other")},
		"no program":    {"README.txt": []byte("x")},
		"empty program": {"unravis-blackbox.exe": {}},
		"path in name":  {"sub/unravis-blackbox.exe": prog},
	} {
		st := filepath.Join(t.TempDir(), "stage-x")
		if _, _, err := stageFromZip(makeZip(t, files), st, want); err == nil {
			t.Errorf("%s accepted", name)
		}
		if _, err := os.Stat(st); err == nil && name == "wrong program" {
			t.Errorf("%s left a staged file", name)
		}
	}
	if _, _, err := stageFromZip(filepath.Join(t.TempDir(), "missing.zip"), stage, want); err == nil {
		t.Fatal("missing zip accepted")
	}
}

func TestManagedMarkerStopsChecksEvenWhenItAppearsLater(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "unravis-blackbox.exe")
	logf := func(string, ...any) {}
	u, err := NewServiceUpdater(Config{}, "1.2.0", exe, logf)
	if err != nil && !strings.Contains(err.Error(), "trusted update keys") {
		t.Fatalf("unexpected: %v", err)
	}
	if err != nil {
		t.Skip("no trusted keys in this build; the guard is tested below with a rig")
	}
	if u.Managed == nil || u.Managed() {
		t.Fatal("no marker yet: must not be managed")
	}
	if err := MarkManaged(dir); err != nil {
		t.Fatal(err)
	}
	if !u.Managed() {
		t.Fatal("marker written after start must count")
	}
	// Explicit settings win over the marker.
	u2, err := NewServiceUpdater(Config{AutoUpdate: &AutoUpdate{Mode: UpdateInstall}}, "1.2.0", exe, logf)
	if err == nil && u2.Managed != nil {
		t.Fatal("explicit autoUpdate must ignore the marker")
	}
}

func TestManagedUpdaterChecksAndInstallsNothing(t *testing.T) {
	r := newSURig(t, UpdateInstall)
	r.u.Managed = func() bool { return true }
	r.u.CheckOnce(context.Background())
	if r.checks != 0 || r.stages != 0 || r.applies != 0 {
		t.Fatalf("managed: checks=%d stages=%d applies=%d", r.checks, r.stages, r.applies)
	}
	if st := r.u.Status(); st.State != "managed" {
		t.Fatalf("state %q", st.State)
	}
}

func TestSuccessIsDecidedOnlyAfterTheConfirmTime(t *testing.T) {
	r := newSURig(t, UpdateInstall)
	r.u.Confirm = time.Hour
	b, _ := json.Marshal(pending{From: "1.0.0", To: "1.1.0"})
	path := filepath.Join(r.u.Dir, pendingName)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.u.Reconcile(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("marker removed before the confirm time")
	}
	cancel()
	<-done
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a stop before the confirm time must leave the marker for the next start")
	}
}
