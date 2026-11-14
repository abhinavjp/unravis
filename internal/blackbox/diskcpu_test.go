package blackbox

import (
	"testing"
	"time"
)

// fakeDiskSource is a fake CounterSource with the PDH Paths/RefreshInstances
// surface; readings are canned and refreshes are counted.
type fakeDiskSource struct {
	paths     []string
	readings  []CounterReading
	refreshes int
}

func (f *fakeDiskSource) Collect() ([]CounterReading, error) { return f.readings, nil }
func (f *fakeDiskSource) Close() error                       { return nil }
func (f *fakeDiskSource) Paths() []string                    { return f.paths }
func (f *fakeDiskSource) RefreshInstances() error            { f.refreshes++; return nil }

func newTestDiskCPU(src *fakeDiskSource) *diskCPUGroup {
	return &diskCPUGroup{src: src, eng: NewEngine(diskCPUSpecs(src.paths), 4), cores: 4}
}

func okReading(path string, v float64) CounterReading {
	return CounterReading{Path: path, Value: v, Status: 0, OK: true}
}

func collectDiskCPU(t *testing.T, g *diskCPUGroup, now time.Time) *DiskCPUGroup {
	t.Helper()
	v, err := g.Collect(now, 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v == nil {
		return nil
	}
	grp, ok := v.(*DiskCPUGroup)
	if !ok {
		t.Fatalf("Collect returned %T, want *DiskCPUGroup", v)
	}
	return grp
}

// Avg. Disk sec/* are gauges in SECONDS: 0.002 s must become 2.0 ms.
func TestDiskCPULatencyMs(t *testing.T) {
	src := &fakeDiskSource{
		paths: []string{`\PhysicalDisk(0 C:)\Avg. Disk sec/Read`, `\PhysicalDisk(0 C:)\Avg. Disk sec/Write`},
		readings: []CounterReading{
			okReading(`\PhysicalDisk(0 C:)\Avg. Disk sec/Read`, 0.002),
			okReading(`\PhysicalDisk(0 C:)\Avg. Disk sec/Write`, 0.005),
		},
	}
	grp := collectDiskCPU(t, newTestDiskCPU(src), time.Unix(1000, 0))
	if grp == nil || len(grp.Disks) != 1 {
		t.Fatalf("group = %+v, want one disk row", grp)
	}
	d := grp.Disks[0]
	if d.ReadMs == nil || *d.ReadMs != 2.0 {
		t.Fatalf("ReadMs = %v, want 2.0 (0.002 s x 1000)", d.ReadMs)
	}
	if d.WriteMs == nil || *d.WriteMs != 5.0 {
		t.Fatalf("WriteMs = %v, want 5.0 (0.005 s x 1000)", d.WriteMs)
	}
}

// Two disk instances yield two rows; the _Total aggregate is excluded.
func TestDiskCPUPerDisk(t *testing.T) {
	src := &fakeDiskSource{
		paths: []string{
			`\PhysicalDisk(0 C:)\Avg. Disk sec/Read`,
			`\PhysicalDisk(1 D:)\Avg. Disk sec/Read`,
			`\PhysicalDisk(_Total)\Avg. Disk sec/Read`,
		},
		readings: []CounterReading{
			okReading(`\PhysicalDisk(0 C:)\Avg. Disk sec/Read`, 0.001),
			okReading(`\PhysicalDisk(1 D:)\Avg. Disk sec/Read`, 0.002),
			okReading(`\PhysicalDisk(_Total)\Avg. Disk sec/Read`, 0.003),
		},
	}
	grp := collectDiskCPU(t, newTestDiskCPU(src), time.Unix(1000, 0))
	if grp == nil || len(grp.Disks) != 2 {
		t.Fatalf("disks = %+v, want exactly 2 rows (_Total excluded)", grp)
	}
	if grp.Disks[0].Name != "0 C:" || grp.Disks[1].Name != "1 D:" {
		t.Fatalf("names = %q,%q; want 0 C:,1 D:", grp.Disks[0].Name, grp.Disks[1].Name)
	}
}

// ReadsPerSec is a rate: absent until two valid baselines, then emitted.
func TestDiskCPURateGated(t *testing.T) {
	p := `\PhysicalDisk(0 C:)\Disk Reads/sec`
	src := &fakeDiskSource{paths: []string{p}, readings: []CounterReading{okReading(p, 10)}}
	g := newTestDiskCPU(src)
	t0 := time.Unix(1000, 0)

	if grp := collectDiskCPU(t, g, t0); grp != nil && grp.Disks[0].ReadsPerSec != nil {
		t.Fatal("sample 1 emitted ReadsPerSec, want nil (first baseline)")
	}
	src.readings[0].Value = 20
	if grp := collectDiskCPU(t, g, t0.Add(15*time.Second)); grp != nil && grp.Disks[0].ReadsPerSec != nil {
		t.Fatal("sample 2 emitted ReadsPerSec, want nil (second baseline)")
	}
	src.readings[0].Value = 30
	grp := collectDiskCPU(t, g, t0.Add(30*time.Second))
	if grp == nil || grp.Disks[0].ReadsPerSec == nil || *grp.Disks[0].ReadsPerSec != 30 {
		t.Fatalf("sample 3 = %+v, want ReadsPerSec 30 after two baselines", grp)
	}
}

// Multi-core CPU percentages are NOT capped at 100.
func TestDiskCPUMultiCoreNotCapped(t *testing.T) {
	p := `\Processor(_Total)\% Privileged Time`
	src := &fakeDiskSource{paths: []string{p}, readings: []CounterReading{okReading(p, 120)}}
	g := newTestDiskCPU(src)
	t0 := time.Unix(1000, 0)
	for i := 0; i < 2; i++ { // two baselines before the rate
		collectDiskCPU(t, g, t0.Add(time.Duration(i)*15*time.Second))
	}
	grp := collectDiskCPU(t, g, t0.Add(30*time.Second))
	if grp == nil || grp.CPU == nil || grp.CPU.PrivilegedPct == nil {
		t.Fatalf("group = %+v, want CPU.PrivilegedPct present", grp)
	}
	if *grp.CPU.PrivilegedPct != 120 {
		t.Fatalf("PrivilegedPct = %v, want 120 (not capped at 100)", *grp.CPU.PrivilegedPct)
	}
}

// An invalid counter leaves its field nil (never 0); nothing valid → nil group.
func TestDiskCPUMissingOmitted(t *testing.T) {
	rp := `\PhysicalDisk(0 C:)\Avg. Disk sec/Read`
	wp := `\PhysicalDisk(0 C:)\Avg. Disk sec/Write`
	src := &fakeDiskSource{
		paths: []string{rp, wp},
		readings: []CounterReading{
			okReading(rp, 0.002),
			{Path: wp, Value: 9, Status: 1, OK: false}, // invalid: must stay nil
		},
	}
	grp := collectDiskCPU(t, newTestDiskCPU(src), time.Unix(1000, 0))
	if grp == nil || len(grp.Disks) != 1 {
		t.Fatalf("group = %+v, want one disk row", grp)
	}
	d := grp.Disks[0]
	if d.ReadMs == nil {
		t.Fatal("valid ReadMs missing")
	}
	if d.WriteMs != nil {
		t.Fatalf("WriteMs = %v, want nil for invalid counter (never 0)", *d.WriteMs)
	}

	// Every reading invalid: the whole group is omitted, never zero-filled.
	src.readings = []CounterReading{{Path: rp, Value: 1, Status: 1, OK: false}}
	if grp := collectDiskCPU(t, newTestDiskCPU(src), time.Unix(1000, 0)); grp != nil {
		t.Fatalf("all-invalid collect = %+v, want nil group", grp)
	}
}

// Wildcards re-expand only when ProcessSetChanged() reports a change.
func TestDiskCPURefreshOnChange(t *testing.T) {
	src := &fakeDiskSource{paths: []string{`\System\Processor Queue Length`}}
	g := newTestDiskCPU(src)
	changed := false
	g.changed = func() bool { return changed }
	t0 := time.Unix(1000, 0)

	collectDiskCPU(t, g, t0)
	if src.refreshes != 0 {
		t.Fatalf("refreshes = %d, want 0 without a change", src.refreshes)
	}
	changed = true
	collectDiskCPU(t, g, t0.Add(15*time.Second))
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1 after ProcessSetChanged", src.refreshes)
	}
	changed = false
	collectDiskCPU(t, g, t0.Add(30*time.Second))
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want no re-expand without a change", src.refreshes)
	}
}

// The group is registered mandatory (optional=false).
func TestDiskCPURegisteredMandatory(t *testing.T) {
	registryMu.Lock()
	reg, ok := registry["diskCpu"]
	registryMu.Unlock()
	if !ok {
		t.Fatal("diskCpu not registered")
	}
	if reg.optional {
		t.Fatal("diskCpu registered optional, want mandatory (false)")
	}
}