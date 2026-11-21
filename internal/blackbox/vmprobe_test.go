package blackbox

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// probeFake is one fake counter: it answers the same value on every collect.
type probeFake struct {
	path   string
	value  float64
	status uint32
	ok     bool
	err    error
	closed *int
}

func (f *probeFake) Collect() ([]CounterReading, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []CounterReading{{Path: f.path, Value: f.value, Status: f.status, OK: f.ok}}, nil
}
func (f *probeFake) Close() error { *f.closed++; return nil }

type probeRig struct {
	present  map[string]float64
	failWith map[string]error // open errors by path
	opened   []string
	closed   int
	sleeps   []time.Duration
}

func (r *probeRig) open(path string) (CounterSource, error) {
	r.opened = append(r.opened, path)
	if err, ok := r.failWith[path]; ok {
		return nil, err
	}
	if v, ok := r.present[path]; ok {
		return &probeFake{path: path, value: v, ok: true, closed: &r.closed}, nil
	}
	return nil, &pdhStatusErr{call: "PdhAddEnglishCounter", path: path, code: 0xC0000BB8}
}

func (r *probeRig) sleep(d time.Duration) { r.sleeps = append(r.sleeps, d) }

func TestVMProbeListsOnlyTheFixedNames(t *testing.T) {
	r := &probeRig{present: map[string]float64{}}
	res := RunVMProbe(r.open, 3, r.sleep)
	want := VMProbePaths()
	if len(want) != 18 {
		t.Fatalf("fixed list has %d names, want the 18 the spec lists (11 VM Memory + 7 VM Processor)", len(want))
	}
	if len(r.opened) != len(want) || len(res.Counters) != len(want) {
		t.Fatalf("opened %d, reported %d, want %d", len(r.opened), len(res.Counters), len(want))
	}
	seen := map[string]bool{}
	for i, p := range want {
		if r.opened[i] != p || res.Counters[i].Path != p {
			t.Fatalf("counter %d = open %q report %q, want %q", i, r.opened[i], res.Counters[i].Path, p)
		}
		if !strings.HasPrefix(p, `\VM Memory\`) && !strings.HasPrefix(p, `\VM Processor\`) {
			t.Errorf("%q is outside the two VM objects", p)
		}
		if seen[p] {
			t.Errorf("duplicate %q", p)
		}
		seen[p] = true
	}
	// The returned list is a copy: changing it must not change the probe.
	want[0] = "tampered"
	if VMProbePaths()[0] == "tampered" {
		t.Fatal("VMProbePaths exposes the internal slice")
	}
}

func TestVMProbeNoVMwareHost(t *testing.T) {
	r := &probeRig{present: map[string]float64{}}
	res := RunVMProbe(r.open, 5, r.sleep)
	if res.Found != 0 || res.PDHUnavailable {
		t.Fatalf("Found=%d PDHUnavailable=%v, want 0 and false", res.Found, res.PDHUnavailable)
	}
	if len(r.sleeps) != 0 {
		t.Errorf("slept %d times with nothing to read", len(r.sleeps))
	}
	for _, c := range res.Counters {
		if c.Found || c.AbsentStatus != "0xC0000BB8" || len(c.Samples) != 0 {
			t.Fatalf("absent line = %+v, want the status code and no samples", c)
		}
	}
	text := FormatVMProbeText(res)
	if !strings.Contains(text, "No VMware counters on this host.") {
		t.Fatalf("text lacks the no-VMware line:\n%s", text)
	}
	if got := strings.Count(text, "absent (PDH status 0xC0000BB8)"); got != 18 {
		t.Fatalf("absent lines = %d, want 18", got)
	}
}

func TestVMProbeGoldenMixedOutput(t *testing.T) {
	r := &probeRig{present: map[string]float64{
		`\VM Memory\Memory Ballooned in MB`: 512,
		`\VM Processor\CPU stolen time`:     12.5,
	}}
	res := RunVMProbe(r.open, 2, r.sleep)
	if res.Found != 2 || res.Samples != 2 {
		t.Fatalf("Found=%d Samples=%d, want 2 and 2", res.Found, res.Samples)
	}
	text := FormatVMProbeText(res)
	for _, want := range []string{
		"VMware counter probe (read-only): 18 fixed counters, 2 samples 1 s apart\n",
		`\VM Memory\Memory Active in MB: absent (PDH status 0xC0000BB8)` + "\n",
		`\VM Memory\Memory Ballooned in MB: found; 512 [0x0], 512 [0x0]` + "\n",
		`\VM Processor\CPU stolen time: found; 12.5 [0x0], 12.5 [0x0]` + "\n",
		"Found 2 of 18 VMware counters.\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "No VMware counters") {
		t.Error("says no VMware counters although two were found")
	}
	// Every found counter is closed, and the pause is 1 s per round.
	if r.closed != 2 {
		t.Errorf("closed %d sources, want 2", r.closed)
	}
	if len(r.sleeps) != 2 || r.sleeps[0] != time.Second {
		t.Errorf("sleeps = %v, want two 1 s pauses", r.sleeps)
	}
	// JSON carries the same facts and decodes back.
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var back VMProbeResult
	if err := json.Unmarshal(b, &back); err != nil || back.Found != 2 || len(back.Counters) != 18 {
		t.Fatalf("round trip: %v %+v", err, back)
	}
}

func TestVMProbeSamplesAreCapped(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{-3, 1}, {0, 1}, {1, 1}, {10, 10}, {11, 10}, {500, 10}} {
		r := &probeRig{present: map[string]float64{`\VM Memory\Memory Used in MB`: 1}}
		res := RunVMProbe(r.open, tc.in, r.sleep)
		if res.Samples != tc.want || len(res.Counters[10].Samples) != tc.want || len(r.sleeps) != tc.want {
			t.Errorf("n=%d: Samples=%d points=%d sleeps=%d, want %d", tc.in, res.Samples, len(res.Counters[10].Samples), len(r.sleeps), tc.want)
		}
	}
}

func TestVMProbeFailuresAreFixedWords(t *testing.T) {
	// No counter can be tried at all: say so instead of "absent" everywhere.
	all := &probeRig{failWith: map[string]error{}}
	for _, p := range VMProbePaths() {
		all.failWith[p] = errors.New("secret machine text C:\\Users\\bob")
	}
	res := RunVMProbe(all.open, 3, all.sleep)
	if !res.PDHUnavailable {
		t.Fatal("PDHUnavailable = false although nothing could be tried")
	}
	text := FormatVMProbeText(res)
	if strings.Contains(text, "secret") || strings.Contains(text, "bob") {
		t.Fatalf("error text leaked into the output:\n%s", text)
	}
	if !strings.Contains(text, "could not be opened") {
		t.Fatalf("no unavailable line:\n%s", text)
	}
	// One odd failure among absent ones is "not read", not "absent".
	one := &probeRig{present: map[string]float64{}, failWith: map[string]error{`\VM Memory\Memory Shares`: errors.New("x")}}
	res = RunVMProbe(one.open, 1, one.sleep)
	if res.PDHUnavailable {
		t.Fatal("PDHUnavailable = true with 17 clean answers")
	}
	if !strings.Contains(FormatVMProbeText(res), `\VM Memory\Memory Shares: not read (no PDH status)`) {
		t.Fatalf("odd failure not reported:\n%s", FormatVMProbeText(res))
	}
}

func TestVMProbeUnusableValuesAreNotPrinted(t *testing.T) {
	r := &probeRig{present: map[string]float64{
		`\VM Memory\Memory Used in MB`:   math.NaN(),
		`\VM Memory\Memory Active in MB`: math.Inf(1),
		`\VM Memory\Memory Shares`:       0,
		`\VM Processor\CPU stolen time`:  -4,
		`\VM Memory\Memory Mapped in MB`: 1e9,
	}}
	res := RunVMProbe(r.open, 1, r.sleep)
	if _, err := json.Marshal(res); err != nil {
		t.Fatalf("result does not marshal (NaN or Inf leaked): %v", err)
	}
	text := FormatVMProbeText(res)
	for _, want := range []string{
		`\VM Memory\Memory Used in MB: found; n/a [0x0]`,
		`\VM Memory\Memory Active in MB: found; n/a [0x0]`,
		`\VM Memory\Memory Shares: found; 0 [0x0]`,
		`\VM Processor\CPU stolen time: found; -4 [0x0]`,
		`\VM Memory\Memory Mapped in MB: found; 1e+09 [0x0]`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}

func TestVMProbeCollectFailureIsRecorded(t *testing.T) {
	closed := 0
	open := func(path string) (CounterSource, error) {
		return &probeFake{path: path, err: errors.New("boom"), closed: &closed}, nil
	}
	res := RunVMProbe(open, 2, func(time.Duration) {})
	if closed != len(VMProbePaths()) {
		t.Errorf("closed %d, want all %d", closed, len(VMProbePaths()))
	}
	s := res.Counters[0].Samples
	if len(s) != 2 || s[0].OK || s[0].Value != nil || s[0].Status != "none" {
		t.Fatalf("samples = %+v, want two unread samples", s)
	}
	if strings.Contains(FormatVMProbeText(res), "boom") {
		t.Fatal("collect error text leaked")
	}
}
