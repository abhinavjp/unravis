package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func openT(t *testing.T, dir, run string, c *clock) *Store {
	t.Helper()
	s, err := OpenStore(dir, StoreOptions{Name: "box", Version: "test", Host: "H", IntervalSeconds: 15, RunID: run, Now: c.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.cur != nil {
			_ = s.cur.Close()
		}
	})
	return s
}

func smp(run string, seq uint64, at time.Time) Sample {
	v := 1.0
	return Sample{Run: run, Seq: seq, T: at, CPU: &v}
}

func TestStoreAppendWritesHourlyFilesAndManifest(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	at := t0.Add(59*time.Minute + 50*time.Second)
	for i := 0; i < 3; i++ {
		c.t = at
		if err := s.Append(smp("r1", uint64(i+1), at), ""); err != nil {
			t.Fatal(err)
		}
		at = at.Add(5 * time.Second)
	}
	if err := s.Close("stopped"); err != nil {
		t.Fatal(err)
	}
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != ManifestStopped || len(m.Files) != 2 || m.SampleCount() != 3 {
		t.Fatalf("%+v", m)
	}
	if m.Files[0].Name != "bb-20261027-10.ndjson" || m.Files[1].Name != "bb-20261027-11.ndjson" {
		t.Fatalf("names %v", m.Files)
	}
	if m.Files[0].Samples != 2 || m.Files[1].Samples != 1 {
		t.Fatalf("samples %+v", m.Files)
	}
	st, _ := os.Stat(filepath.Join(dir, m.Files[0].Name))
	if st.Size() != m.Files[0].Bytes {
		t.Fatal("manifest bytes differ from the file")
	}
	if m.FirstSample == nil || !m.FirstSample.Equal(t0.Add(59*time.Minute+50*time.Second)) {
		t.Fatal("first sample")
	}
	if m.Runs[0].Reason != "stopped" || m.Runs[0].Stop == nil {
		t.Fatalf("run %+v", m.Runs[0])
	}
	if m.Probe.Enabled {
		t.Fatal("probe is off")
	}
}

func TestStoreGapInsideARunIsLate(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	_ = s.Append(smp("r1", 1, t0), "")
	_ = s.Append(smp("r1", 2, t0.Add(15*time.Second)), "")
	_ = s.Append(smp("r1", 3, t0.Add(100*time.Second)), "")
	_ = s.Append(smp("r1", 4, t0.Add(300*time.Second)), CauseDiskLow)
	m := s.Manifest()
	if len(m.Gaps) != 2 || m.Gaps[0].Cause != CauseLate || m.Gaps[1].Cause != CauseDiskLow {
		t.Fatalf("%+v", m.Gaps)
	}
	if m.Gaps[0].Seconds != 85 {
		t.Fatalf("%+v", m.Gaps[0])
	}
}

func TestStoreCleanRestartGapAndCrashGap(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	_ = s.Append(smp("r1", 1, t0), "")
	if err := s.Close("stopped"); err != nil {
		t.Fatal(err)
	}

	c.t = t0.Add(10 * time.Minute)
	s2 := openT(t, dir, "r2", c)
	_ = s2.Append(smp("r2", 1, c.t), "")
	// no Close: the process dies here.

	c.t = t0.Add(30 * time.Minute)
	s3 := openT(t, dir, "r3", c)
	_ = s3.Append(smp("r3", 1, c.t), "")

	m := s3.Manifest()
	if len(m.Gaps) != 2 || m.Gaps[0].Cause != CauseRestart || m.Gaps[1].Cause != CauseCrash {
		t.Fatalf("%+v", m.Gaps)
	}
	if len(m.Runs) != 3 || m.Runs[1].Reason != "crash" || m.Runs[0].Reason != "stopped" {
		t.Fatalf("%+v", m.Runs)
	}
}

func TestStoreClockGoingBackIsAnEventNotAGap(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	_ = s.Append(smp("r1", 1, t0.Add(time.Hour+30*time.Second)), "")
	_ = s.Append(smp("r1", 2, t0.Add(time.Hour+10*time.Second)), "")
	m := s.Manifest()
	if len(m.Gaps) != 0 {
		t.Fatal("no gap")
	}
	var ev bool
	for _, e := range m.Events {
		ev = ev || e.Kind == EventClockBack
	}
	if !ev {
		t.Fatal("clock-back event missing")
	}
	if m.SampleCount() != 2 {
		t.Fatal("both samples kept")
	}
}

func TestStoreTornTailIsRepaired(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	for i := 0; i < 3; i++ {
		_ = s.Append(smp("r1", uint64(i+1), t0.Add(time.Duration(i)*15*time.Second)), "")
	}
	name := s.CurrentFile()
	// Crash in the middle of a write: half a line, no newline.
	_ = s.cur.Close()
	f, _ := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString(`{"v":1,"run":"r1","seq":4,"t":"2026-10-27T10:0`)
	f.Close()

	c.t = t0.Add(5 * time.Minute)
	s2 := openT(t, dir, "r2", c)
	m := s2.Manifest()
	if m.SampleCount() != 3 {
		t.Fatalf("samples = %d", m.SampleCount())
	}
	data, _ := os.ReadFile(filepath.Join(dir, name))
	if !strings.HasSuffix(string(data), "}\n") || strings.Count(string(data), "\n") != 3 {
		t.Fatalf("file:\n%s", data)
	}
	var rep bool
	for _, e := range m.Events {
		rep = rep || e.Kind == EventFileRepaired
	}
	if !rep {
		t.Fatal("file-repaired event missing")
	}
	// A new sample after repair lands on its own line.
	_ = s2.Append(smp("r2", 1, c.t), "")
	data, _ = os.ReadFile(filepath.Join(dir, name))
	if strings.Count(string(data), "\n") != 4 {
		t.Fatal("append after repair")
	}
}

func TestStoreTornTailWithNewlineButNotJSON(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	_ = s.Append(smp("r1", 1, t0), "")
	name := s.CurrentFile()
	_ = s.cur.Close()
	f, _ := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("\x00\x00\x00\x00\n")
	f.Close()
	s2 := openT(t, dir, "r2", c)
	if s2.Manifest().SampleCount() != 1 {
		t.Fatal("garbage line must be cut")
	}
}

func TestStoreMissingManifestIsRebuilt(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	for i := 0; i < 5; i++ {
		_ = s.Append(smp("r1", uint64(i+1), t0.Add(time.Duration(i)*15*time.Second)), "")
	}
	_ = s.Close("stopped")
	if err := os.Remove(filepath.Join(dir, ManifestName)); err != nil {
		t.Fatal(err)
	}
	s2 := openT(t, dir, "r2", c)
	m := s2.Manifest()
	if m.SampleCount() != 5 || len(m.Files) != 1 {
		t.Fatalf("%+v", m.Files)
	}
	if !hasEvent(m, EventManifestRebuilt) {
		t.Fatal("rebuilt event")
	}
}

func hasEvent(m Manifest, kind string) bool {
	for _, e := range m.Events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func TestStoreCorruptManifestIsKeptAndRebuilt(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	_ = s.Append(smp("r1", 1, t0), "")
	_ = s.Close("stopped")
	_ = os.WriteFile(filepath.Join(dir, ManifestName), []byte("{not json"), 0o644)
	s2 := openT(t, dir, "r2", c)
	if !hasEvent(s2.Manifest(), EventManifestRebuilt) || s2.Manifest().SampleCount() != 1 {
		t.Fatal("rebuild")
	}
	bad, _ := filepath.Glob(filepath.Join(dir, ManifestName+".bad-*"))
	if len(bad) != 1 {
		t.Fatalf("bad copy = %v", bad)
	}
}

func TestStoreNewerFormatIsRefused(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ManifestName), []byte(`{"format":99}`), 0o644)
	_, err := OpenStore(dir, StoreOptions{IntervalSeconds: 15, RunID: "r"})
	if !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("err = %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ManifestName))
	if string(b) != `{"format":99}` {
		t.Fatal("must not touch the manifest")
	}
}

func TestStoreMissingFileIsDroppedAndExtraFileAdopted(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	_ = s.Append(smp("r1", 1, t0), "")
	_ = s.Append(smp("r1", 2, t0.Add(time.Hour)), "")
	_ = s.Close("stopped")
	_ = os.Remove(filepath.Join(dir, "bb-20261027-10.ndjson"))
	_ = os.WriteFile(filepath.Join(dir, "bb-20261027-13.ndjson"), []byte(`{"v":1,"run":"x","seq":1,"t":"2026-10-27T13:00:00Z"}`+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "manifest.json.tmp-123"), []byte("x"), 0o644)
	s2 := openT(t, dir, "r2", c)
	m := s2.Manifest()
	if len(m.Files) != 2 || m.Files[0].Name != "bb-20261027-11.ndjson" || m.Files[1].Name != "bb-20261027-13.ndjson" {
		t.Fatalf("%+v", m.Files)
	}
	if !hasEvent(m, EventFileMissing) {
		t.Fatal("file-missing event")
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json.tmp-123")); err == nil {
		t.Fatal("stray tmp not cleaned")
	}
}

func TestStoreRemoveRefusesCurrentFile(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	_ = s.Append(smp("r1", 1, t0), "")
	_ = s.Append(smp("r1", 2, t0.Add(time.Hour)), "")
	if err := s.Remove(s.CurrentFile()); !errors.Is(err, ErrCurrentFile) {
		t.Fatalf("err = %v", err)
	}
	if err := s.Remove("bb-20261027-10.ndjson"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("../x"); err == nil {
		t.Fatal("bad name")
	}
	if len(s.Manifest().Files) != 1 {
		t.Fatal("manifest not updated")
	}
}

func TestStoreManifestWriteFailureDoesNotStopRecording(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: t0}
	s := openT(t, dir, "r1", c)
	// Something sits where the manifest goes, so the rename cannot succeed.
	_ = os.Remove(filepath.Join(dir, ManifestName))
	if err := os.MkdirAll(filepath.Join(dir, ManifestName, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(smp("r1", 1, t0), ""); err != nil {
		t.Fatalf("append must still work: %v", err)
	}
	if s.ManifestWritable() {
		t.Fatal("writable should be false")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "bb-20261027-10.ndjson"))
	if strings.Count(string(data), "\n") != 1 {
		t.Fatal("sample lost")
	}
}
