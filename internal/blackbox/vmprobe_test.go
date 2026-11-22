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
	present    map[string]float64
	failWith   map[string]error  // open errors by path
	unreadable map[string]uint32 // paths that open but read with this PDH status
	opened     []string
	closed     int
	sleeps     []time.Duration
}

func (r *probeRig) open(path string) (CounterSource, error) {
	r.opened = append(r.opened, path)
	if err, ok := r.failWith[path]; ok {
		return nil, err
	}
	if code, ok := r.unreadable[path]; ok {
		return &probeFake{path: path, status: code, ok: false, closed: &r.closed}, nil
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
	if len(res.Counters) != len(want) {
		t.Fatalf("reported %d, want %d", len(res.Counters), len(want))
	}
	// Only the fixed paths and their instance forms may ever be opened.
	allowed := map[string]bool{}
	for _, p := range want {
		for _, v := range vmProbeVariants(p) {
			allowed[v] = true
		}
	}
	for _, o := range r.opened {
		if !allowed[o] {
			t.Errorf("opened %q, which is not a fixed path or one of its instance forms", o)
		}
	}
	// 11 memory names (one form each) and 7 processor names (three forms each).
	if len(r.opened) != 11+7*3 {
		t.Errorf("opened %d paths on a host with none, want %d", len(r.opened), 11+7*3)
	}
	seen := map[string]bool{}
	for i, p := range want {
		if res.Counters[i].Path != p {
			t.Fatalf("counter %d = %q, want %q", i, res.Counters[i].Path, p)
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
	if got := strings.Count(text, "absent (PDH status 0xC0000BB8"); got != 18 {
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
		`\VM Processor\Limit in MHz: absent (PDH status 0xC0000BB8; \VM Processor(_Total)\Limit in MHz: 0xC0000BB8; \VM Processor(*)\Limit in MHz: 0xC0000BB8)` + "\n",
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
	closed, opened := 0, 0
	open := func(path string) (CounterSource, error) {
		opened++
		return &probeFake{path: path, err: errors.New("boom"), closed: &closed}, nil
	}
	res := RunVMProbe(open, 2, func(time.Duration) {})
	if closed != opened {
		t.Errorf("closed %d of %d opened sources, want all", closed, opened)
	}
	s := res.Counters[0].Samples
	if len(s) != 2 || s[0].OK || s[0].Value != nil || s[0].Status != "none" {
		t.Fatalf("samples = %+v, want two unread samples", s)
	}
	if strings.Contains(FormatVMProbeText(res), "boom") {
		t.Fatal("collect error text leaked")
	}
}

// On a real VMware guest VM Processor has instances (per vCPU plus _Total), so the
// plain path may not exist while the instance form does. The probe must find it.
func TestVMProbeFindsVMProcessorInstanceForms(t *testing.T) {
	r := &probeRig{present: map[string]float64{
		`\VM Processor(_Total)\CPU stolen time`:      42,
		`\VM Processor(_Total)\% Processor Time`:     7,
		`\VM Processor(*)\Effective VM Speed in MHz`: 2400,
	}}
	res := RunVMProbe(r.open, 1, r.sleep)
	byPath := map[string]VMProbeLine{}
	for _, c := range res.Counters {
		byPath[c.Path] = c
	}
	steal := byPath[`\VM Processor\CPU stolen time`]
	if !steal.Found || steal.Variant != `\VM Processor(_Total)\CPU stolen time` {
		t.Fatalf("steal line = %+v, want found through the (_Total) form", steal)
	}
	if len(steal.AlsoTried) != 0 || steal.AbsentStatus != "" {
		t.Errorf("a found line must carry no failure facts: %+v", steal)
	}
	if got := steal.Samples; len(got) != 1 || got[0].Path != `\VM Processor(_Total)\CPU stolen time` || got[0].Value == nil || *got[0].Value != 42 {
		t.Errorf("steal samples = %+v, want the (_Total) reading with its own path", got)
	}
	// The plain form is tried first and the wildcard last.
	eff := byPath[`\VM Processor\Effective VM Speed in MHz`]
	if !eff.Found || eff.Variant != `\VM Processor(*)\Effective VM Speed in MHz` {
		t.Fatalf("effective speed line = %+v, want found through the (*) form", eff)
	}
	if res.Found != 3 {
		t.Errorf("Found = %d, want 3", res.Found)
	}
	text := FormatVMProbeText(res)
	if !strings.Contains(text, `\VM Processor\CPU stolen time: found; \VM Processor(_Total)\CPU stolen time=42 [0x0]`) {
		t.Errorf("text lacks the (_Total) steal reading:\n%s", text)
	}
	// A host that has the plain form never tries the others.
	r2 := &probeRig{present: map[string]float64{`\VM Processor\CPU stolen time`: 1}}
	RunVMProbe(r2.open, 1, r2.sleep)
	for _, o := range r2.opened {
		if strings.Contains(o, `CPU stolen time`) && o != `\VM Processor\CPU stolen time` {
			t.Errorf("tried %q although the plain path opened", o)
		}
	}
}

// A status that is not an absent one (for example no instance, or access denied)
// is "failed", not "absent": the capture must not claim a counter is missing.
func TestVMProbeNonAbsentStatusIsFailedNotAbsent(t *testing.T) {
	path := `\VM Memory\Memory Used in MB`
	r := &probeRig{present: map[string]float64{}, failWith: map[string]error{
		path: &pdhStatusErr{call: "PdhAddEnglishCounter", path: path, code: 0x800007D1},
	}}
	text := FormatVMProbeText(RunVMProbe(r.open, 1, r.sleep))
	if !strings.Contains(text, path+": failed (PDH status 0x800007D1)") {
		t.Fatalf("non-absent status not reported as failed:\n%s", text)
	}
	if strings.Contains(text, path+": absent") {
		t.Fatalf("non-absent status reported as absent:\n%s", text)
	}
}

// Several instances (one per vCPU) are all reported, and the count is bounded.
func TestVMProbeReportsEveryInstanceBounded(t *testing.T) {
	closed := 0
	open := func(path string) (CounterSource, error) {
		if path != `\VM Processor(*)\Shares` {
			return nil, &pdhStatusErr{call: "PdhAddEnglishCounter", path: path, code: 0xC0000BB8}
		}
		return multiFake{closed: &closed, n: 1000}, nil
	}
	res := RunVMProbe(open, 1, func(time.Duration) {})
	for _, c := range res.Counters {
		if c.Path == `\VM Processor\Shares` {
			if !c.Found || len(c.Samples) != maxProbeInstances {
				t.Fatalf("shares line = found %v with %d samples, want %d (bounded)", c.Found, len(c.Samples), maxProbeInstances)
			}
			if c.Samples[0].Path == "" {
				t.Error("instance reading carries no path")
			}
		}
	}
	if closed != 1 {
		t.Errorf("closed %d, want 1", closed)
	}
}

type multiFake struct {
	closed *int
	n      int
}

func (m multiFake) Collect() ([]CounterReading, error) {
	out := make([]CounterReading, m.n)
	for i := range out {
		out[i] = CounterReading{Path: fmt.Sprintf(`\VM Processor(%d)\Shares`, i), Value: 1000, OK: true}
	}
	return out, nil
}
func (m multiFake) Close() error { *m.closed++; return nil }

// PDH usually accepts a path without an instance on a multi-instance object and
// only fails the read with NO_INSTANCE. The probe must then move on to (_Total).
func TestVMProbeMovesOnWhenThePlainPathOpensButCannotBeRead(t *testing.T) {
	plain := `\VM Processor\CPU stolen time`
	r := &probeRig{
		present:    map[string]float64{`\VM Processor(_Total)\CPU stolen time`: 42},
		unreadable: map[string]uint32{plain: 0x800007D1},
	}
	res := RunVMProbe(r.open, 2, r.sleep)
	var line VMProbeLine
	for _, c := range res.Counters {
		if c.Path == plain {
			line = c
		}
	}
	if !line.Found || line.Variant != `\VM Processor(_Total)\CPU stolen time` {
		t.Fatalf("line = %+v, want found through (_Total)", line)
	}
	if len(line.Samples) != 2 || line.Samples[0].Value == nil || *line.Samples[0].Value != 42 || !line.Samples[0].OK {
		t.Fatalf("samples = %+v, want two readings of 42", line.Samples)
	}
	if len(line.AlsoTried) != 1 || line.AlsoTried[0].Path != plain || !line.AlsoTried[0].Opened || line.AlsoTried[0].Status != "0x800007D1" {
		t.Fatalf("AlsoTried = %+v, want the plain path opened but unreadable (0x800007D1)", line.AlsoTried)
	}
	text := FormatVMProbeText(res)
	want := plain + ": found; \\VM Processor(_Total)\\CPU stolen time=42 [0x0], \\VM Processor(_Total)\\CPU stolen time=42 [0x0] (read through \\VM Processor(_Total)\\CPU stolen time) (not readable; " + plain + ": opened, read status 0x800007D1)"
	if !strings.Contains(text, want) {
		t.Fatalf("text lacks the form note:\n%s\nwant: %s", text, want)
	}
	if r.closed != 2 { // the unreadable plain source at once, the (_Total) one at the end
		t.Errorf("closed %d sources, want 2", r.closed)
	}
}

// When no form can be read, the first form that opened stays on the line as
// found-but-unreadable, so the capture shows the status instead of "absent".
func TestVMProbeKeepsTheFirstOpenedFormWhenNothingReads(t *testing.T) {
	plain := `\VM Processor\CPU stolen time`
	r := &probeRig{
		present: map[string]float64{},
		unreadable: map[string]uint32{
			plain:                                   0x800007D1,
			`\VM Processor(_Total)\CPU stolen time`: 0x800007D1,
		},
	}
	res := RunVMProbe(r.open, 1, r.sleep)
	var line VMProbeLine
	for _, c := range res.Counters {
		if c.Path == plain {
			line = c
		}
	}
	if !line.Found || line.Variant != "" {
		t.Fatalf("line = %+v, want found through the plain path (the first that opened)", line)
	}
	if len(line.Samples) != 1 || line.Samples[0].OK || line.Samples[0].Status != "0x800007D1" {
		t.Fatalf("samples = %+v, want one unreadable sample with the status", line.Samples)
	}
	if len(line.AlsoTried) != 2 {
		t.Fatalf("AlsoTried = %+v, want both opened-but-unreadable forms", line.AlsoTried)
	}
	// The extra source that was opened but not kept is closed: plain stays open
	// until the end, the (_Total) one is closed at once, and both end up closed.
	if r.closed != 2 {
		t.Errorf("closed %d sources, want 2 (the unused one at once, the kept one at the end)", r.closed)
	}
}
