package blackbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func newVMTestGroup(t *testing.T, src CounterSource) *vmGroup {
	t.Helper()
	g, err := newVMGroup(GroupDeps{Source: src, Cores: 4})
	if err != nil {
		t.Fatalf("newVMGroup: %v", err)
	}
	return g.(*vmGroup)
}

func collectVM(t *testing.T, g *vmGroup) *VMGroup {
	t.Helper()
	v, err := g.Collect(time.Unix(1000, 0), 15)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if isNilPayload(v) {
		return nil
	}
	grp, ok := v.(*VMGroup)
	if !ok {
		t.Fatalf("Collect returned %T, want *VMGroup", v)
	}
	return grp
}

func TestVMGroupWritesBalloonBytes(t *testing.T) {
	g := newVMTestGroup(t, &memPressureFakeSource{readings: []CounterReading{memReading(vmBalloonPath, 512)}})
	grp := collectVM(t, g)
	if grp == nil || grp.BalloonBytes == nil || *grp.BalloonBytes != 536870912 {
		t.Fatalf("group = %+v, want BalloonBytes 536870912", grp)
	}
	if grp.StealPct != nil {
		t.Fatal("StealPct written; this group never writes it")
	}
	b, err := json.Marshal(Sample{VM: grp})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"vm":{"balloonBytes":536870912}`) {
		t.Fatalf("sample JSON = %s, want the vm balloonBytes line", b)
	}
	if strings.Contains(string(b), "stealPct") {
		t.Fatalf("sample JSON = %s, must not contain stealPct", b)
	}
}

func TestVMGroupZeroIsAReading(t *testing.T) {
	g := newVMTestGroup(t, &memPressureFakeSource{readings: []CounterReading{memReading(vmBalloonPath, 0)}})
	grp := collectVM(t, g)
	if grp == nil || grp.BalloonBytes == nil || *grp.BalloonBytes != 0 {
		t.Fatalf("group = %+v, want a pointer to 0 (a real reading)", grp)
	}
	b, _ := json.Marshal(Sample{VM: grp})
	if !strings.Contains(string(b), `"balloonBytes":0`) {
		t.Fatalf("sample JSON = %s, want \"balloonBytes\":0", b)
	}
}

func TestVMGroupInvalidIsMissing(t *testing.T) {
	cases := []struct {
		name     string
		readings []CounterReading
	}{
		{"not ok", []CounterReading{{Path: vmBalloonPath, Value: 5, Status: 0, OK: false}}},
		{"invalid status", []CounterReading{{Path: vmBalloonPath, Value: 5, Status: 0xC0000BBA, OK: true}}},
		{"empty readings", nil},
		{"other path only", []CounterReading{memReading(`\VM Memory\Other`, 5)}},
		{"negative", []CounterReading{memReading(vmBalloonPath, -1)}},
		{"NaN", []CounterReading{memReading(vmBalloonPath, math.NaN())}},
		{"+Inf", []CounterReading{memReading(vmBalloonPath, math.Inf(1))}},
		{"over cap", []CounterReading{memReading(vmBalloonPath, 2e7)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newVMTestGroup(t, &memPressureFakeSource{readings: tc.readings})
			if grp := collectVM(t, g); grp != nil {
				t.Fatalf("payload = %+v, want none", grp)
			}
			// Through the collector: the group is named in Missing, no field.
			c := NewGroupCollector(&stubCollector{}, []Group{g}, NewBudget(15))
			s, err := c.Collect(time.Unix(2000, 0))
			if err != nil {
				t.Fatal(err)
			}
			if !missingHas(s, "vm") || s.VM != nil {
				t.Fatalf("Missing = %v, VM = %+v; want vm in Missing and no field", s.Missing, s.VM)
			}
			if b, _ := json.Marshal(s); strings.Contains(string(b), "balloonBytes") {
				t.Fatalf("JSON %s carries balloonBytes for a missing reading", b)
			}
		})
	}
}

func TestVMGroupSourceErrorIsAnError(t *testing.T) {
	g := newVMTestGroup(t, &memPressureFakeSource{err: errors.New("collect failed")})
	if v, err := g.Collect(time.Unix(1000, 0), 15); err == nil || v != nil {
		t.Fatalf("Collect = (%v, %v), want an error and no payload", v, err)
	}
}

func TestVMGroupCapBoundary(t *testing.T) {
	g := newVMTestGroup(t, &memPressureFakeSource{readings: []CounterReading{memReading(vmBalloonPath, vmMaxBalloonMB)}})
	grp := collectVM(t, g)
	if grp == nil || *grp.BalloonBytes != uint64(vmMaxBalloonMB)<<20 {
		t.Fatalf("exactly the cap: group = %+v, want accepted", grp)
	}
}

func TestVMGroupRoundsToWholeMB(t *testing.T) {
	for _, tc := range []struct {
		mb   float64
		want uint64
	}{{1.4, 1 << 20}, {1.6, 2 << 20}} {
		g := newVMTestGroup(t, &memPressureFakeSource{readings: []CounterReading{memReading(vmBalloonPath, tc.mb)}})
		grp := collectVM(t, g)
		if grp == nil || *grp.BalloonBytes != tc.want {
			t.Errorf("%v MB: group = %+v, want %d bytes", tc.mb, grp, tc.want)
		}
	}
}

func TestVMFactoryAbsentIsNotApplicable(t *testing.T) {
	saved := newVMSource
	t.Cleanup(func() { newVMSource = saved })

	// An absent error (also when wrapped the way the PDH source wraps it).
	newVMSource = func([]string) (CounterSource, error) {
		return nil, fmt.Errorf("%w: %w", errors.New("no counters"), errPDHAbsent)
	}
	g, err := newVMGroup(GroupDeps{})
	if !errors.Is(err, errNotApplicable) || g != nil {
		t.Fatalf("absent: (%v, %v), want (nil, errNotApplicable)", g, err)
	}

	// A plain error comes back unchanged, and is not "not applicable".
	plain := errors.New("PdhOpenQuery: 0x1")
	newVMSource = func([]string) (CounterSource, error) { return nil, plain }
	g, err = newVMGroup(GroupDeps{})
	if err != plain || g != nil || errors.Is(err, errNotApplicable) {
		t.Fatalf("plain: (%v, %v), want (nil, the same error)", g, err)
	}

	// A nil source with a nil error never yields a nil group with a nil error.
	newVMSource = func([]string) (CounterSource, error) { return nil, nil }
	g, err = newVMGroup(GroupDeps{})
	if g == nil && err == nil {
		t.Fatal("factory returned a nil group with a nil error")
	}

	// The factory asks for exactly the one fixed path.
	var asked []string
	newVMSource = func(p []string) (CounterSource, error) {
		asked = p
		return &memPressureFakeSource{}, nil
	}
	if _, err := newVMGroup(GroupDeps{}); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != `\VM Memory\Memory Ballooned in MB` {
		t.Fatalf("source asked for %v, want the one fixed balloon path", asked)
	}
}

func TestVMFactoryUsesSharedSource(t *testing.T) {
	src := &memPressureFakeSource{readings: []CounterReading{memReading(vmBalloonPath, 1)}}
	g := newVMTestGroup(t, src)
	if g.owned {
		t.Fatal("owned = true for a shared source")
	}
	if err := g.Close(); err != nil || src.closed {
		t.Fatalf("Close closed a shared source (closed=%v err=%v)", src.closed, err)
	}
}

func TestVMFactoryOwnedSourceIsClosed(t *testing.T) {
	saved := newVMSource
	t.Cleanup(func() { newVMSource = saved })
	src := &memPressureFakeSource{}
	newVMSource = func([]string) (CounterSource, error) { return src, nil }
	gi, err := newVMGroup(GroupDeps{})
	if err != nil {
		t.Fatal(err)
	}
	g := gi.(*vmGroup)
	if !g.owned {
		t.Fatal("owned = false for the factory's own source")
	}
	if err := g.Close(); err != nil || !src.closed {
		t.Fatalf("Close did not close the owned source (closed=%v err=%v)", src.closed, err)
	}
}

func TestVMGroupIsOptionalAndNamed(t *testing.T) {
	g := newVMTestGroup(t, &memPressureFakeSource{})
	if g.Name() != "vm" || !g.Optional() {
		t.Fatalf("Name=%q Optional=%v, want vm and optional", g.Name(), g.Optional())
	}
	registryMu.Lock()
	reg, ok := registry["vm"]
	registryMu.Unlock()
	if !ok || !reg.optional {
		t.Fatalf("registry vm = %+v (found %v), want registered and optional", reg, ok)
	}
}
