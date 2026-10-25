package resource_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/winlive"
	"api-audit-explorer/internal/resource"
)

// A synthetic win-live batch, parsed by the real importer, gives the host and
// process series of the catalogue, with the same process identity as a
// procsnap file (name:pid, split at a process start).
func TestWinLiveBatchFillsTheCatalogue(t *testing.T) {
	start := t0.Add(-time.Hour)
	var b strings.Builder
	b.WriteString(winlive.Header(15, 4) + "\n" + winlive.ColumnLine() + "\n")
	ts := func(i int) string { return at(time.Duration(i) * 15 * time.Second).Format(winlive.TimeFormat) }
	fmt.Fprintf(&b, "%s,carry,w3wp,1240,%s,cpu-seconds,100\n", ts(-1), start.Format(winlive.TimeFormat))
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "%s,host,,,,host-cpu,%d\n", ts(i), 20+i)
		fmt.Fprintf(&b, "%s,host,,,,mem-available,%d\n", ts(i), 4_000_000_000+i)
		fmt.Fprintf(&b, "%s,host,,,,mem-committed,%d\n", ts(i), 6_000_000_000+i)
		// 3 cpu seconds in 15 seconds on 4 cores = 5 percent of the host.
		fmt.Fprintf(&b, "%s,proc,w3wp,1240,%s,cpu-seconds,%d\n", ts(i), start.Format(winlive.TimeFormat), 103+3*i)
		fmt.Fprintf(&b, "%s,proc,w3wp,1240,%s,private-bytes,%d\n", ts(i), start.Format(winlive.TimeFormat), 500_000_000+i)
		fmt.Fprintf(&b, "%s,proc,w3wp,1240,%s,working-set,%d\n", ts(i), start.Format(winlive.TimeFormat), 700_000_000+i)
		fmt.Fprintf(&b, "%s,proc,w3wp,1240,%s,handles,%d\n", ts(i), start.Format(winlive.TimeFormat), 900+i)
		fmt.Fprintf(&b, "%s,proc,w3wp,1240,%s,threads,%d\n", ts(i), start.Format(winlive.TimeFormat), 40+i)
	}
	fmt.Fprintf(&b, "%s,mark,w3wp,1240,%s,start,0\n", ts(8), start.Format(winlive.TimeFormat))

	evs, rep, err := importer.Collect(context.Background(), winlive.New(), strings.NewReader(b.String()), importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "n0",
		Host: &importer.HostHint{Label: "web-a", LogicalCores: 4},
	})
	if err != nil || rep.Rejected != 0 {
		t.Fatalf("parse: %v rejected %d", err, rep.Rejected)
	}

	h := newHarness(t)
	h.add(evs...)
	x := h.catalogue(importer.Window{From: t0, To: at(10 * time.Minute)})

	// Host series.
	for id, want := range map[string]float64{
		resource.HostCPU: 20, resource.HostMemAvailable: 4_000_000_000, resource.HostMemCommitted: 6_000_000_000,
	} {
		ss := x.Series(id, resource.Filter{})
		if len(ss) != 1 {
			t.Fatalf("%s: %d series", id, len(ss))
		}
		s := ss[0]
		if s.Source != "win-live" || s.Host != "web-a" || len(s.Samples) != 8 || s.Samples[0].Value != want {
			t.Errorf("%s: source %q host %q samples %d first %v", id, s.Source, s.Host, len(s.Samples), s.Samples[0].Value)
		}
	}
	// Process series.
	for id, want := range map[string]float64{
		resource.ProcCPU: 5, resource.ProcPrivate: 500_000_000, resource.ProcWS: 700_000_000,
		resource.ProcHandles: 900, resource.ProcThreads: 40,
	} {
		ss := x.Series(id, resource.Filter{PID: 1240})
		if len(ss) != 1 {
			t.Fatalf("%s: %d series", id, len(ss))
		}
		s := ss[0]
		if s.Proc.Name != "w3wp" || s.Proc.PID != 1240 || s.Source != "win-live" || len(s.Samples) != 8 {
			t.Errorf("%s: proc %+v source %q samples %d", id, s.Proc, s.Source, len(s.Samples))
			continue
		}
		if got := s.Samples[0].Value; got != want {
			t.Errorf("%s: first sample %v, want %v", id, got, want)
		}
	}
	// The process-start before the window names the process start of its series.
	if ss := x.Series(resource.ProcPrivate, resource.Filter{PID: 1240}); len(ss) == 1 && !ss[0].Proc.Start.IsZero() && !ss[0].Proc.Start.Equal(start) {
		t.Errorf("process start %v, want %v", ss[0].Proc.Start, start)
	}
}

func TestWinLiveIsAListedSourceOfTheHostAndProcessMetrics(t *testing.T) {
	have := map[string]bool{}
	for _, mi := range resource.Metrics() {
		for _, s := range mi.Sources {
			if s == "win-live" {
				have[mi.ID] = true
			}
		}
	}
	for _, id := range []string{resource.HostCPU, resource.HostMemAvailable, resource.HostMemCommitted,
		resource.ProcCPU, resource.ProcWS, resource.ProcPrivate, resource.ProcHandles, resource.ProcThreads} {
		if !have[id] {
			t.Errorf("%s does not list win-live", id)
		}
	}
	if len(have) != 8 {
		t.Errorf("win-live is a source of %d metrics, want 8", len(have))
	}
}
