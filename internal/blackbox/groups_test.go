package blackbox

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// stubCollector is a host-collector stand-in with a canned sample.
type stubCollector struct {
	sample Sample
	err    error
	closed bool
}

func (s *stubCollector) Collect(now time.Time) (Sample, error) { return s.sample, s.err }
func (s *stubCollector) Close() error                          { s.closed = true; return nil }

// fakeGroup is a counter-group stand-in that can succeed, fail or panic.
type fakeGroup struct {
	name     string
	optional bool
	result   any
	err      error
	panicVal any
	calls    int
	lastDT   float64
	closed   bool
}

func (f *fakeGroup) Name() string   { return f.name }
func (f *fakeGroup) Optional() bool { return f.optional }
func (f *fakeGroup) Collect(now time.Time, dt float64) (any, error) {
	f.calls++
	f.lastDT = dt
	if f.panicVal != nil {
		panic(f.panicVal)
	}
	return f.result, f.err
}
func (f *fakeGroup) Close() error { f.closed = true; return nil }

func missingHas(s Sample, name string) bool {
	for _, m := range s.Missing {
		if m == name {
			return true
		}
	}
	return false
}

func TestRegistryBuildsAndNames(t *testing.T) {
	registryMu.Lock()
	saved := registry
	registry = map[string]registration{}
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	})

	RegisterGroup("zeta", true, func(d GroupDeps) (Group, error) {
		if d.Cores != 4 {
			t.Errorf("factory got Cores = %d, want 4", d.Cores)
		}
		return &fakeGroup{name: "zeta", optional: true}, nil
	})
	RegisterGroup("alpha", false, func(GroupDeps) (Group, error) {
		return &fakeGroup{name: "alpha"}, nil
	})
	RegisterGroup("broken", false, func(GroupDeps) (Group, error) {
		return nil, errors.New("no source")
	})

	groups := BuildGroups(GroupDeps{Cores: 4})
	if len(groups) != 2 {
		t.Fatalf("BuildGroups returned %d groups, want 2 (broken factory skipped)", len(groups))
	}
	if groups[0].Name() != "alpha" || groups[1].Name() != "zeta" {
		t.Fatalf("BuildGroups order = %s,%s; want sorted alpha,zeta", groups[0].Name(), groups[1].Name())
	}
	if got := registeredNames(); !reflect.DeepEqual(got, []string{"alpha", "broken", "zeta"}) {
		t.Fatalf("registeredNames = %v, want sorted [alpha broken zeta]", got)
	}
}

func TestGroupCollectorAppliesResult(t *testing.T) {
	tcp := &TCPGroup{RetransmitsPerSec: fp64(1.5)}
	g := &fakeGroup{name: "tcp", result: tcp}
	inner := &stubCollector{}
	c := NewGroupCollector(inner, []Group{g}, NewBudget(15))

	now := time.Now()
	s, err := c.Collect(now)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if s.TCP != tcp {
		t.Fatal("s.TCP not set to the group result")
	}
	if len(s.Missing) != 0 {
		t.Fatalf("Missing = %v, want empty on success", s.Missing)
	}
	if g.lastDT != 0 {
		t.Fatalf("first dt = %v, want 0", g.lastDT)
	}
	if _, err := c.Collect(now.Add(15 * time.Second)); err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if g.lastDT != 15 {
		t.Fatalf("second dt = %v, want 15", g.lastDT)
	}
	inner.err = errors.New("host read failed")
	if _, err := c.Collect(now.Add(30 * time.Second)); err == nil {
		t.Fatal("inner collector error not propagated")
	}
}

func TestGroupCollectorSkippedOptional(t *testing.T) {
	b := NewBudget(15)
	b.SelfUsage(0.9, wsBytes(10))
	b.Cycle() // over budget: optional groups skipped
	opt := &fakeGroup{name: "tcp", optional: true, result: &TCPGroup{}}
	mand := &fakeGroup{name: "http", result: &HTTPGroup{}}
	c := NewGroupCollector(&stubCollector{}, []Group{opt, mand}, b)

	s, err := c.Collect(time.Now())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if s.TCP != nil {
		t.Fatal("skipped optional group was zero-filled")
	}
	if !missingHas(s, "tcp") {
		t.Fatalf("Missing = %v, want tcp", s.Missing)
	}
	if s.HTTP == nil {
		t.Fatal("mandatory group did not run while optional skipped")
	}
	if opt.calls != 0 || mand.calls != 1 {
		t.Fatalf("calls: optional %d, mandatory %d; want 0 and 1", opt.calls, mand.calls)
	}
}

func TestGroupCollectorSlowGroupMarked(t *testing.T) {
	b := NewBudget(15)
	b.GroupTiming("tcp", 2*time.Second, false) // marked slow last cycle
	g := &fakeGroup{name: "tcp", result: &TCPGroup{}}
	c := NewGroupCollector(&stubCollector{}, []Group{g}, b)

	now := time.Now()
	s, err := c.Collect(now)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if s.TCP != nil {
		t.Fatal("slow-skipped group was zero-filled")
	}
	if !missingHas(s, "skipped: slow") {
		t.Fatalf("Missing = %v, want skipped: slow", s.Missing)
	}
	if g.calls != 0 {
		t.Fatal("slow group ran despite the skip")
	}
	if len(b.SlowGroups()) != 0 {
		t.Fatal("slow mark not consumed by NoteSkipped")
	}
	// The mark is one-cycle: the group is re-probed next tick.
	s2, err := c.Collect(now.Add(15 * time.Second))
	if err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if s2.TCP == nil || g.calls != 1 {
		t.Fatal("slow group not re-probed next cycle")
	}
	if missingHas(s2, "skipped: slow") {
		t.Fatalf("second Missing = %v, want no slow mark", s2.Missing)
	}
}

func TestGroupCollectorErrorMissingNotZero(t *testing.T) {
	g := &fakeGroup{name: "tcp", err: errors.New("PDH path unavailable")}
	c := NewGroupCollector(&stubCollector{}, []Group{g}, NewBudget(15))

	s, err := c.Collect(time.Now())
	if err != nil {
		t.Fatalf("group error failed the whole tick: %v", err)
	}
	if s.TCP != nil {
		t.Fatal("failed group was zero-filled")
	}
	if !reflect.DeepEqual(s.Missing, []string{"tcp"}) {
		t.Fatalf("Missing = %v, want [tcp] only (fixed name, no error text)", s.Missing)
	}
}

func TestGroupCollectorPanicRecovered(t *testing.T) {
	bad := &fakeGroup{name: "tcp", panicVal: "counter path exploded"}
	good := &fakeGroup{name: "http", result: &HTTPGroup{}}
	c := NewGroupCollector(&stubCollector{}, []Group{bad, good}, NewBudget(15))

	s, err := c.Collect(time.Now()) // must not panic
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if s.TCP != nil {
		t.Fatal("panicking group was zero-filled")
	}
	if !missingHas(s, "tcp") {
		t.Fatalf("Missing = %v, want tcp", s.Missing)
	}
	if s.HTTP == nil || good.calls != 1 {
		t.Fatal("one group's panic stopped the tick")
	}
}

func TestGroupCollectorMissingDeduped(t *testing.T) {
	// Inner already reports tcp missing; the group fails too: one entry.
	inner := &stubCollector{sample: Sample{Missing: []string{"tcp"}}}
	g := &fakeGroup{name: "tcp", err: errors.New("x")}
	c := NewGroupCollector(inner, []Group{g}, NewBudget(15))
	s, err := c.Collect(time.Now())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !reflect.DeepEqual(s.Missing, []string{"tcp"}) {
		t.Fatalf("Missing = %v, want a single tcp", s.Missing)
	}

	// Two slow groups produce one "skipped: slow" entry.
	b := NewBudget(15)
	b.GroupTiming("tcp", 2*time.Second, false)
	b.GroupTiming("http", 2*time.Second, true)
	c2 := NewGroupCollector(&stubCollector{}, []Group{
		&fakeGroup{name: "tcp"},
		&fakeGroup{name: "http", optional: true},
	}, b)
	s2, err := c2.Collect(time.Now())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !reflect.DeepEqual(s2.Missing, []string{"skipped: slow"}) {
		t.Fatalf("Missing = %v, want a single skipped: slow", s2.Missing)
	}
}

func TestApplyGroupUnknownIgnored(t *testing.T) {
	var s Sample
	applyGroup(&s, "nope", &TCPGroup{}) // unknown name: no panic
	applyGroup(&s, "tcp", "some text")  // wrong type: ignored
	applyGroup(&s, "tcp", nil)          // nil result: ignored
	if s.TCP != nil {
		t.Fatal("wrong type or nil was assigned")
	}
	tcp := &TCPGroup{}
	applyGroup(&s, "tcp", tcp)
	if s.TCP != tcp {
		t.Fatal("matching type not assigned")
	}
}

func TestApplyGroupAllFields(t *testing.T) {
	cases := []struct {
		name string
		v    any
		get  func(*Sample) any
	}{
		{"http", &HTTPGroup{}, func(s *Sample) any { return s.HTTP }},
		{"iis", &IISGroup{}, func(s *Sample) any { return s.IIS }},
		{"diskCpu", &DiskCPUGroup{}, func(s *Sample) any { return s.DiskCPU }},
		{"sqlHost", &SQLHostGroup{}, func(s *Sample) any { return s.SQLHost }},
		{"clr", &CLRGroup{}, func(s *Sample) any { return s.CLR }},
		{"proc10", &Proc10Group{}, func(s *Sample) any { return s.Proc10 }},
		{"procIO", &ProcIOGroup{}, func(s *Sample) any { return s.ProcIO }},
		{"events", &EventsGroup{}, func(s *Sample) any { return s.Events }},
		{"tcp", &TCPGroup{}, func(s *Sample) any { return s.TCP }},
		{"aspnet", &ASPNETGroup{}, func(s *Sample) any { return s.ASPNET }},
		{"memPressure", &MemPressureGroup{}, func(s *Sample) any { return s.MemPressure }},
		{"vm", &VMGroup{}, func(s *Sample) any { return s.VM }},
		{"dumps", &DumpsGroup{}, func(s *Sample) any { return s.Dumps }},
	}
	for _, c := range cases {
		var s Sample
		applyGroup(&s, c.name, c.v)
		if c.get(&s) != c.v {
			t.Errorf("%s: matching field not set", c.name)
		}
	}
}

func TestGroupCollectorClose(t *testing.T) {
	inner := &stubCollector{}
	g := &fakeGroup{name: "tcp"}
	c := NewGroupCollector(inner, []Group{g}, NewBudget(15))
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !inner.closed {
		t.Fatal("inner collector not closed")
	}
	if !g.closed {
		t.Fatal("group implementing io.Closer not closed")
	}
}
