package blackbox

import (
	"testing"
	"time"
)

// aspnetFakeSource is a fake CounterSource: fixed paths, no expansion, no
// refresh. The same readings slice is returned on every call.
type aspnetFakeSource struct {
	readings []CounterReading
	closed   bool
}

func (f *aspnetFakeSource) Collect() ([]CounterReading, error) { return f.readings, nil }
func (f *aspnetFakeSource) Close() error                       { f.closed = true; return nil }

func aspnetReading(path string, v float64) CounterReading {
	return CounterReading{Path: path, Value: v, OK: true}
}

func newASPNETTestGroup(t *testing.T, src *aspnetFakeSource) *aspnetGroup {
	t.Helper()
	g, err := newASPNETGroup(GroupDeps{Source: src, Cores: 4})
	if err != nil {
		t.Fatalf("newASPNETGroup: %v", err)
	}
	return g.(*aspnetGroup)
}

func TestASPNETMapsFields(t *testing.T) {
	src := &aspnetFakeSource{
		readings: []CounterReading{
			aspnetReading(`\ASP.NET\Requests Queued`, 3),
			aspnetReading(`\ASP.NET\Requests Rejected`, 2),
			aspnetReading(`\ASP.NET\Request Wait Time`, 120),
			aspnetReading(`\ASP.NET\Application Restarts`, 1),
			aspnetReading(`\ASP.NET Applications(__Total__)\Requests Current`, 7),
		},
	}
	g := newASPNETTestGroup(t, src)
	now := time.Unix(1000, 0)
	var grp *ASPNETGroup
	for i := 0; i < 3; i++ { // two baselines, then the rate emits
		v, err := g.Collect(now.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		grp, _ = v.(*ASPNETGroup)
		if grp == nil {
			t.Fatalf("tick %d: nil group with valid readings", i+1)
		}
	}
	check := func(name string, got *float64, want float64) {
		t.Helper()
		if got == nil || *got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("RequestsQueued", grp.RequestsQueued, 3)
	check("RequestsCurrent", grp.RequestsCurrent, 7)
	check("RequestsRejected", grp.RequestsRejected, 2)
	check("RequestWaitTimeMs", grp.RequestWaitTimeMs, 120)
	check("ApplicationRestarts", grp.ApplicationRestarts, 1)
}

func TestASPNETMissingOmitted(t *testing.T) {
	src := &aspnetFakeSource{
		readings: []CounterReading{
			aspnetReading(`\ASP.NET\Requests Queued`, 5),
			// Invalid status: must stay absent, never 0.
			{Path: `\ASP.NET\Request Wait Time`, Value: 99, Status: 1, OK: false},
			// Requests Current: no reading at all.
		},
	}
	g := newASPNETTestGroup(t, src)
	v, err := g.Collect(time.Unix(1000, 0), 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	grp := v.(*ASPNETGroup)
	if grp.RequestsQueued == nil || *grp.RequestsQueued != 5 {
		t.Fatalf("RequestsQueued = %v, want 5", grp.RequestsQueued)
	}
	if grp.RequestWaitTimeMs != nil {
		t.Fatalf("invalid counter emitted %v; missing must stay nil, never 0", *grp.RequestWaitTimeMs)
	}
	if grp.RequestsCurrent != nil {
		t.Fatalf("absent counter emitted %v; missing must stay nil, never 0", *grp.RequestsCurrent)
	}
	if grp.RequestsRejected != nil || grp.ApplicationRestarts != nil {
		t.Fatal("absent counters emitted; missing must stay nil, never 0")
	}
}

func TestASPNETEmptyNil(t *testing.T) {
	src := &aspnetFakeSource{
		readings: []CounterReading{
			{Path: `\ASP.NET\Requests Queued`, Value: 5, Status: 1, OK: false},
		},
	}
	g := newASPNETTestGroup(t, src)
	v, err := g.Collect(time.Unix(1000, 0), 0)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v != nil {
		t.Fatalf("group = %v, want nil when nothing is valid", v)
	}
	src.readings = nil // no readings at all: still nil, nil
	v, err = g.Collect(time.Unix(1015, 0), 15)
	if err != nil || v != nil {
		t.Fatalf("empty readings: (%v, %v), want (nil, nil)", v, err)
	}
}

func TestASPNETRateGated(t *testing.T) {
	src := &aspnetFakeSource{
		readings: []CounterReading{
			aspnetReading(`\ASP.NET\Requests Queued`, 1),
			aspnetReading(`\ASP.NET\Requests Rejected`, 2),
		},
	}
	g := newASPNETTestGroup(t, src)
	now := time.Unix(1000, 0)
	for i := 0; i < 2; i++ {
		v, err := g.Collect(now.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		grp := v.(*ASPNETGroup)
		if grp.RequestsRejected != nil {
			t.Fatalf("tick %d: rate emitted before two baselines", i+1)
		}
		if grp.RequestsQueued == nil {
			t.Fatalf("tick %d: gauge absent", i+1)
		}
	}
	v, err := g.Collect(now.Add(30*time.Second), 15)
	if err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	grp := v.(*ASPNETGroup)
	if grp.RequestsRejected == nil || *grp.RequestsRejected != 2 {
		t.Fatalf("RequestsRejected = %v, want 2 after two baselines", grp.RequestsRejected)
	}
}