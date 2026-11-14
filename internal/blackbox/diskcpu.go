package blackbox

import (
	"sort"
	"strings"
	"time"
)

func init() { RegisterGroup("diskCpu", false, newDiskCPUGroup) }

// diskCPUPaths are the English PDH paths this group samples. The disk
// wildcards expand per physical disk; the _Total aggregate is not a disk row.
func diskCPUPaths() []string {
	return []string{
		`\PhysicalDisk(*)\Avg. Disk sec/Read`,
		`\PhysicalDisk(*)\Avg. Disk sec/Write`,
		`\PhysicalDisk(*)\Disk Reads/sec`,
		`\PhysicalDisk(*)\Disk Writes/sec`,
		`\PhysicalDisk(*)\Split IO/Sec`,
		`\System\Processor Queue Length`,
		`\Processor(_Total)\% Privileged Time`,
		`\Processor(_Total)\% DPC Time`,
		`\Processor(_Total)\% Interrupt Time`,
	}
}

// diskCPUSource is the PDH surface this group needs (faked in tests).
type diskCPUSource interface {
	CounterSource
	Paths() []string
	RefreshInstances() error
}

type diskCPUGroup struct {
	src     diskCPUSource
	eng     *Engine
	cores   int
	changed func() bool
}

func newDiskCPUGroup(d GroupDeps) (Group, error) {
	src, err := NewPDHSource(diskCPUPaths())
	if err != nil {
		return nil, err
	}
	return &diskCPUGroup{
		src:     src,
		eng:     NewEngine(diskCPUSpecs(src.Paths()), d.Cores),
		cores:   d.Cores,
		changed: d.ProcessSetChanged,
	}, nil
}

func (g *diskCPUGroup) Name() string   { return "diskCpu" }
func (g *diskCPUGroup) Optional() bool { return false }
func (g *diskCPUGroup) Close() error   { return g.src.Close() }

// diskCPUSpecs binds every expanded path to a field. Disk fields are keyed by
// the full path (unique per instance+counter); the latency gauges stay in
// seconds here and are converted to ms in Collect.
func diskCPUSpecs(paths []string) []CounterSpec {
	specs := make([]CounterSpec, 0, len(paths))
	for _, p := range paths {
		switch {
		case strings.HasPrefix(p, `\PhysicalDisk(`):
			kind := KindPDHRate
			if strings.HasSuffix(p, `\Avg. Disk sec/Read`) || strings.HasSuffix(p, `\Avg. Disk sec/Write`) {
				kind = KindGauge
			}
			specs = append(specs, CounterSpec{Path: p, Field: p, Kind: kind})
		case p == `\System\Processor Queue Length`:
			specs = append(specs, CounterSpec{Path: p, Field: "queueLength", Kind: KindGauge})
		case p == `\Processor(_Total)\% Privileged Time`:
			specs = append(specs, CounterSpec{Path: p, Field: "privilegedPct", Kind: KindPDHRate})
		case p == `\Processor(_Total)\% DPC Time`:
			specs = append(specs, CounterSpec{Path: p, Field: "dpcPct", Kind: KindPDHRate})
		case p == `\Processor(_Total)\% Interrupt Time`:
			specs = append(specs, CounterSpec{Path: p, Field: "interruptPct", Kind: KindPDHRate})
		}
	}
	return specs
}

// diskInstanceCounter splits an expanded disk path into instance and counter.
func diskInstanceCounter(path string) (inst, counter string, ok bool) {
	const prefix = `\PhysicalDisk(`
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := path[len(prefix):]
	i := strings.Index(rest, `)\`)
	if i < 0 {
		return "", "", false
	}
	return rest[:i], rest[i+2:], true
}

func (g *diskCPUGroup) Collect(now time.Time, _ float64) (any, error) {
	// Disks rarely change: re-expand wildcards only when the process set
	// changed, never per tick; rebuild specs/engine for the new instances.
	if g.changed != nil && g.changed() {
		if err := g.src.RefreshInstances(); err != nil {
			return nil, err
		}
		g.eng = NewEngine(diskCPUSpecs(g.src.Paths()), g.cores)
	}
	readings, err := g.src.Collect()
	if err != nil {
		return nil, err
	}
	out := g.eng.Process(now, readings)

	byDisk := make(map[string]*DiskSplit)
	for field, v := range out {
		inst, counter, ok := diskInstanceCounter(field)
		if !ok || inst == "_Total" {
			continue // per-disk rows only; the aggregate is not a disk
		}
		d := byDisk[inst]
		if d == nil {
			d = &DiskSplit{Name: inst}
			byDisk[inst] = d
		}
		switch counter {
		case "Avg. Disk sec/Read":
			d.ReadMs = diskF64(v * 1000) // PDH reports seconds
		case "Avg. Disk sec/Write":
			d.WriteMs = diskF64(v * 1000)
		case "Disk Reads/sec":
			d.ReadsPerSec = diskF64(v)
		case "Disk Writes/sec":
			d.WritesPerSec = diskF64(v)
		case "Split IO/Sec":
			d.SplitIOPerSec = diskF64(v)
		}
	}

	var grp DiskCPUGroup
	if len(byDisk) > 0 {
		names := make([]string, 0, len(byDisk))
		for n := range byDisk {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			grp.Disks = append(grp.Disks, *byDisk[n])
		}
	}
	var cpu CPUSplit
	have := false
	if v, ok := out["queueLength"]; ok {
		cpu.QueueLength = diskF64(v)
		have = true
	}
	if v, ok := out["privilegedPct"]; ok {
		cpu.PrivilegedPct = diskF64(v) // multi-core: never capped at 100
		have = true
	}
	if v, ok := out["dpcPct"]; ok {
		cpu.DPCPct = diskF64(v)
		have = true
	}
	if v, ok := out["interruptPct"]; ok {
		cpu.InterruptPct = diskF64(v)
		have = true
	}
	if have {
		grp.CPU = &cpu
	}
	if len(grp.Disks) == 0 && grp.CPU == nil {
		return nil, nil // nothing valid: omit the group, never zero-fill
	}
	return &grp, nil
}

func diskF64(v float64) *float64 { return &v }