package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// fakeVMCounter is one VM counter that always reads the same.
type fakeVMCounter struct {
	path  string
	value float64
}

func (f fakeVMCounter) Collect() ([]blackbox.CounterReading, error) {
	return []blackbox.CounterReading{{Path: f.path, Value: f.value, Status: 0, OK: true}}, nil
}
func (f fakeVMCounter) Close() error { return nil }

// fakeProbe runs the real probe logic over fake counters: the balloon and steal
// counters exist, everything else fails to open (no PDH status).
func fakeProbe(seen *[]int) func(n int) blackbox.VMProbeResult {
	return func(n int) blackbox.VMProbeResult {
		if seen != nil {
			*seen = append(*seen, n)
		}
		open := func(path string) (blackbox.CounterSource, error) {
			switch path {
			case `\VM Memory\Memory Ballooned in MB`:
				return fakeVMCounter{path, 512}, nil
			case `\VM Processor\CPU stolen time`:
				return fakeVMCounter{path, 3}, nil
			}
			return nil, errors.New("not here")
		}
		return blackbox.RunVMProbe(open, n, func(time.Duration) {})
	}
}

func runProbeCmd(args []string, probe func(int) blackbox.VMProbeResult) (int, string, string) {
	var out, errb bytes.Buffer
	code := runVMProbe(args, &out, &errb, probe)
	return code, out.String(), errb.String()
}

func TestVMProbeFlagsAndExitCodes(t *testing.T) {
	var seen []int
	probe := fakeProbe(&seen)
	for _, args := range [][]string{
		{"--samples", "0"}, {"--samples", "11"}, {"--samples", "-1"}, {"--samples", "x"},
		{"stray"}, {"--nonsense"}, {"--samples"},
	} {
		if c, out, _ := runProbeCmd(args, probe); c != exitUsage || out != "" {
			t.Errorf("%v: exit %d, stdout %q; want usage exit and no output", args, c, out)
		}
	}
	if len(seen) != 0 {
		t.Fatalf("the probe ran %v times for bad flags", seen)
	}
	for _, tc := range []struct {
		args []string
		want int
	}{{nil, 5}, {[]string{"--samples", "1"}, 1}, {[]string{"--samples", "10"}, 10}, {[]string{"--json", "--samples", "3"}, 3}} {
		seen = nil
		if c, _, errs := runProbeCmd(tc.args, probe); c != exitOK || errs != "" {
			t.Errorf("%v: exit %d stderr %q, want 0", tc.args, c, errs)
		}
		if len(seen) != 1 || seen[0] != tc.want {
			t.Errorf("%v: probe asked for %v samples, want %d", tc.args, seen, tc.want)
		}
	}
}

func TestVMProbeTextHasOnlyTheFixedNames(t *testing.T) {
	c, out, _ := runProbeCmd([]string{"--samples", "2"}, fakeProbe(nil))
	if c != exitOK {
		t.Fatalf("exit %d", c)
	}
	fixed := map[string]bool{}
	for _, p := range blackbox.VMProbePaths() {
		fixed[p] = true
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	counters := 0
	for _, l := range lines {
		if strings.HasPrefix(l, `\`) {
			name, _, _ := strings.Cut(l, ": ")
			if !fixed[name] {
				t.Errorf("line names a counter outside the fixed list: %q", l)
			}
			counters++
		}
	}
	if counters != len(blackbox.VMProbePaths()) {
		t.Errorf("%d counter lines, want %d", counters, len(blackbox.VMProbePaths()))
	}
	if !strings.Contains(out, `\VM Memory\Memory Ballooned in MB: found; 512 [0x0], 512 [0x0]`) {
		t.Errorf("balloon line missing:\n%s", out)
	}
	if !strings.Contains(out, "Found 2 of ") {
		t.Errorf("summary missing:\n%s", out)
	}
}

func TestVMProbeJSONKeys(t *testing.T) {
	c, out, _ := runProbeCmd([]string{"--json", "--samples", "2"}, fakeProbe(nil))
	if c != exitOK {
		t.Fatalf("exit %d", c)
	}
	if strings.Count(strings.TrimSuffix(out, "\n"), "\n") != 0 {
		t.Fatalf("JSON is not one line:\n%s", out)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		t.Fatal(err)
	}
	for k := range top {
		switch k {
		case "counters", "found", "samples", "pdhUnavailable":
		default:
			t.Errorf("unexpected top-level key %q", k)
		}
	}
	for _, k := range []string{"counters", "found", "samples"} {
		if _, ok := top[k]; !ok {
			t.Errorf("missing top-level key %q", k)
		}
	}
	var counters []map[string]json.RawMessage
	if err := json.Unmarshal(top["counters"], &counters); err != nil {
		t.Fatal(err)
	}
	if len(counters) != len(blackbox.VMProbePaths()) {
		t.Fatalf("%d counters, want %d", len(counters), len(blackbox.VMProbePaths()))
	}
	fixed := map[string]bool{}
	for _, p := range blackbox.VMProbePaths() {
		fixed[p] = true
	}
	for _, c := range counters {
		for k := range c {
			switch k {
			case "path", "found", "absentStatus", "failure", "samples":
			default:
				t.Errorf("unexpected counter key %q", k)
			}
		}
		var path string
		if err := json.Unmarshal(c["path"], &path); err != nil || !fixed[path] {
			t.Errorf("counter path %q is not in the fixed list", path)
		}
	}
}

func TestVMProbeThroughRunNeedsNothingElse(t *testing.T) {
	var out, errb bytes.Buffer
	// An empty deps: no service manager, no disk. vm-probe must not touch them.
	code := run([]string{"vm-probe", "--samples", "1"}, &out, &errb, deps{vmProbe: fakeProbe(nil)})
	if code != exitOK || !strings.Contains(out.String(), "VMware counter probe") {
		t.Fatalf("exit %d out %q err %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"vm-probe", "--samples", "99"}, &out, &errb, deps{vmProbe: fakeProbe(nil)}); code != exitUsage {
		t.Fatalf("bad samples through run: %d", code)
	}
}

func TestVMProbePDHUnavailableExitsOne(t *testing.T) {
	probe := func(n int) blackbox.VMProbeResult {
		open := func(string) (blackbox.CounterSource, error) { return nil, errors.New("no pdh") }
		return blackbox.RunVMProbe(open, n, func(time.Duration) {})
	}
	c, out, _ := runProbeCmd(nil, probe)
	if c != exitFail || !strings.Contains(out, "could not be opened") {
		t.Fatalf("exit %d out %q, want exit 1 and the unavailable line", c, out)
	}
}

// The real PDH on this machine, one reading. On the GitHub hosted runner (an
// Azure VM without VMware Tools) no VMware counter may exist; elsewhere it only
// logs what it saw.
func TestVMProbeRealPDH(t *testing.T) {
	c, out, errs := runProbeCmd([]string{"--samples", "1"}, nil)
	t.Logf("exit %d\n%s%s", c, out, errs)
	if c != exitOK {
		t.Fatalf("exit %d: the real probe could not run", c)
	}
	if os.Getenv("GITHUB_ACTIONS") == "true" && !strings.Contains(out, "No VMware counters on this host.") {
		t.Fatalf("a hosted runner has no VMware counters, but the probe says:\n%s", out)
	}
}
