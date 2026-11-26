package blackbox

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

type extraFakeSource struct {
	readings []CounterReading
	closed   bool
}

func (f *extraFakeSource) Collect() ([]CounterReading, error) { return f.readings, nil }
func (f *extraFakeSource) Close() error                       { f.closed = true; return nil }

func extraOK(path string, v float64) CounterReading {
	return CounterReading{Path: path, Value: v, OK: true}
}

func TestExtraCounterValidation(t *testing.T) {
	cases := []struct {
		path string
		ok   bool
	}{
		{`\Process(w3wp)\Page Faults/sec`, true},
		{`\Memory\Pool Nonpaged Bytes`, true},
		{`\Paging File(_Total)\% Usage`, true},
		{`\PhysicalDisk(0 C:)\Avg. Disk sec/Read`, true},
		{`\Process(*)\Page Faults/sec`, false}, // wildcard
		{`\\SERVER\Memory\Pages/sec`, false},   // another machine
		{`Memory\Pages/sec`, false},            // no leading backslash
		{`\Memory`, false},                     // no counter
		{`\Memory\Pages/sec; calc`, false},     // not a path
		{`\Memory\` + strings.Repeat("a", 220), false},
		{``, false},
	}
	for _, tc := range cases {
		s := CounterSettings{Extra: []string{tc.path}}
		err := s.validate()
		if tc.ok && err != nil {
			t.Errorf("%q refused: %v", tc.path, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%q was accepted", tc.path)
		}
		if err != nil && !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%q: error is not ErrInvalidConfig", tc.path)
		}
	}
	if err := (CounterSettings{Extra: []string{`\Memory\Pages/sec`, `\MEMORY\pages/sec`}}).validate(); err == nil {
		t.Error("a path listed twice (different case) was accepted")
	}
	many := make([]string, maxExtraCounters+1)
	for i := range many {
		many[i] = `\Memory\Counter` + strings.Repeat("x", i)
	}
	if err := (CounterSettings{Extra: many}).validate(); err == nil {
		t.Error("too many paths were accepted")
	}
	c := testConfig(t)
	c.Counters = &CounterSettings{Extra: []string{`\Process(*)\ID Process`}}
	if err := c.Validate(); err == nil {
		t.Error("Config.Validate does not check the counters block")
	}
}

func TestExtraCountersConfigRoundTripAndDefaultsOmitIt(t *testing.T) {
	c := testConfig(t)
	out, _ := c.Marshal()
	if strings.Contains(string(out), `"counters"`) {
		t.Error("a default config writes a counters key")
	}
	c.Counters = &CounterSettings{Extra: []string{`\Memory\Pool Nonpaged Bytes`}}
	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseConfig(data)
	if err != nil || len(back.ExtraCounterPaths()) != 1 {
		t.Fatalf("%v %+v", err, back.Counters)
	}
	if _, err := ParseConfig([]byte(`{"name":"bb","folder":` + jsonString(c.Folder) + `,"counters":{"extra":[],"script":"x"}}`)); err == nil {
		t.Error("an unknown field in the counters block was accepted")
	}
}

func TestExtraCountersGroupReadsAndSkips(t *testing.T) {
	gauge, rate, gone := `\Memory\Pool Nonpaged Bytes`, `\Process(w3wp)\Page Faults/sec`, `\Nope\Missing`
	cfg := testConfig(t)
	cfg.Counters = &CounterSettings{Extra: []string{gauge, rate, gone}}
	src := &extraFakeSource{readings: []CounterReading{
		extraOK(gauge, 1234), extraOK(rate, 55), {Path: gone, OK: false},
	}}
	g, err := newExtraCountersGroup(GroupDeps{Config: cfg, Source: src, Cores: 4})
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	var grp *ExtraCountersGroup
	for i := 0; i < 3; i++ { // a per-second counter needs two baselines first
		v, err := g.Collect(t0.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatal(err)
		}
		grp, _ = v.(*ExtraCountersGroup)
		if i == 0 && (grp == nil || grp.Values[gauge] != 1234 || len(grp.Values) != 1) {
			t.Fatalf("first tick: %+v (the rate must still be warming up)", grp)
		}
	}
	if grp.Values[gauge] != 1234 || grp.Values[rate] != 55 {
		t.Errorf("values %+v", grp.Values)
	}
	if len(grp.Skipped) != 1 || grp.Skipped[0] != gone {
		t.Errorf("skipped %v, want only %s", grp.Skipped, gone)
	}
	var s Sample
	applyGroup(&s, "extraCounters", grp)
	if s.ExtraCounters != grp {
		t.Error("applyGroup does not store the group in the sample")
	}
	if b, err := json.Marshal(s); err != nil || !strings.Contains(string(b), `"extraCounters"`) {
		t.Errorf("sample JSON: %v %s", err, b)
	}
	if err := g.(*extraCountersGroup).Close(); err != nil || src.closed {
		t.Error("a source the group did not create must not be closed by it")
	}
}

func TestExtraCountersNonFiniteValueDoesNotBreakTheSample(t *testing.T) {
	a, b := `\Memory\Pool Paged Bytes`, `\Memory\Pool Nonpaged Bytes`
	cfg := testConfig(t)
	cfg.Counters = &CounterSettings{Extra: []string{a, b}}
	src := &extraFakeSource{readings: []CounterReading{extraOK(a, math.NaN()), extraOK(b, math.Inf(1))}}
	g, _ := newExtraCountersGroup(GroupDeps{Config: cfg, Source: src})
	v, err := g.Collect(time.Now(), 15)
	if err != nil {
		t.Fatal(err)
	}
	if v != nil {
		if _, jerr := json.Marshal(v); jerr != nil {
			t.Fatalf("a non-finite reading reached the sample and cannot be written: %v", jerr)
		}
	}
}

func TestExtraCountersAbsentWhenNoneAsked(t *testing.T) {
	if _, err := newExtraCountersGroup(GroupDeps{Config: testConfig(t), Source: &extraFakeSource{}}); !errors.Is(err, errNoTargets) {
		t.Fatalf("got %v, want errNoTargets", err)
	}
	cfg := testConfig(t)
	cfg.Counters = &CounterSettings{}
	if _, err := newExtraCountersGroup(GroupDeps{Config: cfg, Source: &extraFakeSource{}}); !errors.Is(err, errNoTargets) {
		t.Fatalf("empty list: got %v, want errNoTargets", err)
	}
}
