package resource_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/linuxperf"
	"api-audit-explorer/internal/resource"
)

// importLinux imports one synthetic Linux fixture through the case store with
// the real importer, the way the service does, and returns the case.
func importLinux(t *testing.T, c casestore.Case, id string, imp importer.Importer, fixture string, cores int) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "importer", "linuxperf", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	o := importer.Options{
		CaseID: id, ImportNonce: "nonce-" + fixture,
		Host: &importer.HostHint{Label: "lnx-a", Role: "app", TZ: time.UTC, LogicalCores: cores},
		Pseudonymize: func(kind, value string) string {
			sum := sha256.Sum256([]byte(kind + "\x00" + value))
			return kind + "-" + hex.EncodeToString(sum[:4])
		},
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		evs, rep, err := importer.Collect(context.Background(), imp, r, o)
		if err != nil {
			return rep, err
		}
		for _, e := range evs {
			if err := emit(e); err != nil {
				return rep, err
			}
		}
		return rep, nil
	}
	_, err = c.Import(context.Background(), casestore.ImportMeta{
		Importer: imp.Name(), FileName: fixture, HostLabel: "lnx-a", AckGating: true, AckText: "ack",
		AllowedAttrs: imp.AllowedAttrs(),
	}, strings.NewReader(string(body)), run)
	if err != nil {
		t.Fatalf("import %s: %v", fixture, err)
	}
}

func TestLinuxImportersFeedTheResourceSeries(t *testing.T) {
	ctx := context.Background()
	store := casestore.NewMemory()
	info, err := store.Create(ctx, "linux")
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	id := string(info.ID)

	importLinux(t, c, id, linuxperf.NewSadf(), "sadf_cpu_mem.txt", 4)
	importLinux(t, c, id, linuxperf.NewPidstat(), "pidstat_ur.txt", 4)
	importLinux(t, c, id, linuxperf.NewVmstat(), "vmstat_t.txt", 4)

	x, err := resource.Catalogue(c, importer.Window{})
	if err != nil {
		t.Fatal(err)
	}

	// Host CPU from sadf: 100 minus idle, the whole-host rows only.
	ss := x.Series(resource.HostCPU, resource.Filter{Host: "lnx-a", Source: "linux-sadf"})
	if len(ss) != 1 || ss[0].Metric.Unit != "percent-host" || !near(values(ss[0]), []float64{16.25, 56.5, 97.5}) {
		t.Fatalf("sadf host.cpu %+v", ss)
	}
	// Host CPU from vmstat: 100 minus id (92, 85, 20); the first sample is a
	// since-boot average and is skipped.
	ss = x.Series(resource.HostCPU, resource.Filter{Host: "lnx-a", Source: "linux-vmstat"})
	if len(ss) != 1 || !near(values(ss[0]), []float64{15, 80}) {
		t.Fatalf("vmstat host.cpu %+v", ss)
	}

	// Available memory is in bytes: kbavail 3145728 KiB is 3 GiB.
	ss = x.Series(resource.HostMemAvailable, resource.Filter{Host: "lnx-a"})
	if len(ss) != 1 || ss[0].Metric.Unit != "bytes" || !near(values(ss[0]), []float64{3 << 30, 2.5 * (1 << 30), 2 << 30}) {
		t.Fatalf("host.mem.available %+v", ss)
	}

	// Process CPU: pidstat %CPU is percent of one core, divided by the 4 cores.
	ss = x.Series(resource.ProcCPU, resource.Filter{Host: "lnx-a", Process: "dotnet"})
	if len(ss) != 1 || ss[0].Proc.PID != 2210 || !near(values(ss[0]), []float64{25, 17.5}) {
		t.Fatalf("dotnet proc.cpu %+v", ss)
	}
	// Resident set size is KiB in the file and bytes in the series.
	ss = x.Series(resource.ProcWS, resource.Filter{Host: "lnx-a", Process: "dotnet"})
	if len(ss) != 1 || ss[0].Metric.Unit != "bytes" || !near(values(ss[0]), []float64{2500000 * 1024, 2600000 * 1024}) {
		t.Fatalf("dotnet proc.ws %+v", ss)
	}
	// The executable base name is the identity, never the command line.
	for _, s := range x.Series(resource.ProcCPU, resource.Filter{Host: "lnx-a"}) {
		if strings.ContainsAny(s.Proc.Name, " /:") {
			t.Errorf("process name %q is not an executable base name", s.Proc.Name)
		}
	}
}

func near(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		d := a[i] - b[i]
		if d < -1e-6 || d > 1e-6 {
			return false
		}
	}
	return true
}
