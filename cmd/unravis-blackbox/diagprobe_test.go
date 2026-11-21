package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/dotnetdiag"
)

// probeFake records how the command called the probe and returns a canned Result.
type probeFake struct {
	calls  int
	target dotnetdiag.Target
	opts   dotnetdiag.Options
	res    dotnetdiag.Result
	err    error
}

func (f *probeFake) probe(_ context.Context, t dotnetdiag.Target, o dotnetdiag.Options) (dotnetdiag.Result, error) {
	f.calls++
	f.target, f.opts = t, o
	return f.res, f.err
}

func goodResult() dotnetdiag.Result {
	m := true
	return dotnetdiag.Result{
		OS: "windows", Transport: "pipe", RunAs: "user", PID: 4242, PipeOpened: true,
		ServerPID: 4242, ServerStart: 133800000000000000, StartMatch: &m, SessionID: "7",
		PreambleOK: true, TraceVersion: 4, TracePID: 4242, PointerSize: 8, TracePIDMatch: true,
		BytesBefore: 12345, BytesAfter: 100, StopAck: true, StopSession: true, ElapsedMs: 20100,
	}
}

func newProbeRig(t *testing.T) (*rig, *probeFake) {
	r := newRig(t)
	f := &probeFake{res: goodResult()}
	r.d.diagProbe = f.probe
	return r, f
}

func TestDiagProbeUsage(t *testing.T) {
	own := strconv.Itoa(os.Getpid())
	cases := [][]string{
		{},
		{"--pid"},
		{"--pid", "abc"},
		{"--pid", "0"},
		{"--pid", "-5"},
		{"--pid", "4294967296"},
		{"--pid", own},
		{"--pid", "4242", "--seconds", "4"},
		{"--pid", "4242", "--seconds", "61"},
		{"--pid", "4242", "--seconds", "x"},
		{"--pid", "4242", "--start-ticks", "-1"},
		{"--pid", "4242", "stray"},
		{"--pid", "4242", "--provider", "x"},
		{"--pid", "4242", "--keywords", "1"},
		{"--pid", "4242", "--command", "x"},
		{"--pid", "4242", "--config", "x.json"},
	}
	for _, c := range cases {
		r, f := newProbeRig(t)
		code, _, errs := r.do(append([]string{"diag-probe"}, c...)...)
		if code != exitUsage {
			t.Errorf("%v: exit %d, want 2", c, code)
		}
		if f.calls != 0 {
			t.Errorf("%v: the probe was called", c)
		}
		if !strings.Contains(errs, "diag-probe") {
			t.Errorf("%v: no usage text: %q", c, errs)
		}
	}
}

func TestDiagProbePassesTargetAndSeconds(t *testing.T) {
	r, f := newProbeRig(t)
	code, _, _ := r.do("diag-probe", "--pid", "4242", "--seconds", "7", "--start-ticks", "99")
	if code != 0 || f.calls != 1 {
		t.Fatalf("exit %d calls %d", code, f.calls)
	}
	if f.target.PID != 4242 || f.target.StartTicks != 99 || f.opts.Duration != 7*time.Second {
		t.Fatalf("%+v %+v", f.target, f.opts)
	}
	r, f = newProbeRig(t)
	r.do("diag-probe", "--pid", "4242")
	if f.opts.Duration != 20*time.Second || f.target.StartTicks != 0 {
		t.Fatalf("defaults: %+v %+v", f.target, f.opts)
	}
}

var wantJSONKeys = []string{
	"os", "transport", "runAs", "pid", "pipeOpened", "serverPid", "serverStart", "startMatch", "sessionId",
	"preambleOk", "traceVersion", "tracePid", "pointerSize", "tracePidMatch", "bytesBefore", "bytesAfter",
	"endedEarly", "stopAck", "stopSession", "elapsedMs", "reason", "code",
}

func TestDiagProbeJSONKeys(t *testing.T) {
	for name, mut := range map[string]func(*dotnetdiag.Result){
		"with start match":    func(*dotnetdiag.Result) {},
		"without start match": func(r *dotnetdiag.Result) { r.StartMatch = nil },
		"failure":             func(r *dotnetdiag.Result) { r.Reason = dotnetdiag.ReasonBusy },
	} {
		r, f := newProbeRig(t)
		mut(&f.res)
		code, out, _ := r.do("diag-probe", "--pid", "4242", "--json")
		if strings.Count(strings.TrimRight(out, "\n"), "\n") != 0 {
			t.Fatalf("%s: not one line: %q", name, out)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var got []string
		for k := range m {
			got = append(got, k)
		}
		want := append([]string(nil), wantJSONKeys...)
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: keys\n got %v\nwant %v", name, got, want)
		}
		if name == "failure" && (code != 12 || m["reason"] != "busy") {
			t.Fatalf("%s: exit %d %v", name, code, m["reason"])
		}
		if name == "without start match" && m["startMatch"] != nil {
			t.Fatalf("startMatch %v", m["startMatch"])
		}
		if m["sessionId"] != "7" {
			t.Fatalf("sessionId %v", m["sessionId"])
		}
	}
}

var allProbeReasons = []dotnetdiag.Reason{
	"", dotnetdiag.ReasonNotRunning, dotnetdiag.ReasonDenied, dotnetdiag.ReasonDisabled, dotnetdiag.ReasonBusy,
	dotnetdiag.ReasonPeerMismatch, dotnetdiag.ReasonUnsupported, dotnetdiag.ReasonRejected, dotnetdiag.ReasonTimeout,
	dotnetdiag.ReasonBadReply, dotnetdiag.ReasonBadStream, dotnetdiag.ReasonStopFailed, dotnetdiag.ReasonCancelled,
	dotnetdiag.ReasonOther,
}

func TestDiagProbeTextHasNoOSText(t *testing.T) {
	longHex := regexp.MustCompile(`0x[0-9a-fA-F]{9,}`)
	for _, reason := range allProbeReasons {
		for _, mode := range [][]string{{}, {"--json"}} {
			r, f := newProbeRig(t)
			f.res.Reason = reason
			f.res.Code = 0x80131515
			if reason != "" {
				f.err = errors.New(`C:\Windows\secret path: access is denied 0x1234567890abcdef`)
			}
			_, out, errs := r.do(append([]string{"diag-probe", "--pid", "4242"}, mode...)...)
			for _, s := range []string{out, errs} {
				if strings.Contains(s, `\`) || longHex.MatchString(s) ||
					strings.Contains(s, "secret") || strings.Contains(s, "access is denied") {
					t.Errorf("reason %q %v: output carries OS text: %q", reason, mode, s)
				}
			}
			if len(mode) == 0 && !strings.Contains(out, dotnetdiag.Sentence(reason)) {
				t.Errorf("reason %q: no fixed sentence in %q", reason, out)
			}
		}
	}
}

func TestDiagProbeExitCodes(t *testing.T) {
	want := map[dotnetdiag.Reason]int{
		"": 0, dotnetdiag.ReasonOther: 1, dotnetdiag.ReasonDenied: 10, dotnetdiag.ReasonDisabled: 11,
		dotnetdiag.ReasonBusy: 12, dotnetdiag.ReasonPeerMismatch: 13, dotnetdiag.ReasonUnsupported: 14,
		dotnetdiag.ReasonRejected: 14, dotnetdiag.ReasonTimeout: 15, dotnetdiag.ReasonBadReply: 16,
		dotnetdiag.ReasonBadStream: 16, dotnetdiag.ReasonNotRunning: 17, dotnetdiag.ReasonStopFailed: 18,
		dotnetdiag.ReasonCancelled: 19,
	}
	if len(want) != len(allProbeReasons) {
		t.Fatalf("table has %d reasons, test list %d", len(want), len(allProbeReasons))
	}
	for reason, code := range want {
		for _, json := range []bool{false, true} {
			r, f := newProbeRig(t)
			f.res.Reason = reason
			args := []string{"diag-probe", "--pid", "4242"}
			if json {
				args = append(args, "--json")
			}
			if got, _, _ := r.do(args...); got != code {
				t.Errorf("reason %q json=%v: exit %d, want %d", reason, json, got, code)
			}
		}
	}
	// The probe refusing the target at its own door is a usage error.
	r, f := newProbeRig(t)
	f.res, f.err = dotnetdiag.Result{}, dotnetdiag.ErrBadTarget
	if got, _, _ := r.do("diag-probe", "--pid", "4242"); got != exitUsage {
		t.Errorf("ErrBadTarget: exit %d", got)
	}
	// An error with no Result is exit 1 (other).
	r, f = newProbeRig(t)
	f.res, f.err = dotnetdiag.Result{}, errors.New("boom")
	if got, out, _ := r.do("diag-probe", "--pid", "4242"); got != 1 || strings.Contains(out, "boom") {
		t.Errorf("bare error: exit %d %q", got, out)
	}
	// An *Error with no Result keeps its reason and code.
	r, f = newProbeRig(t)
	f.res, f.err = dotnetdiag.Result{}, fmt.Errorf("wrapped: %w", &dotnetdiag.Error{Reason: dotnetdiag.ReasonDenied, Code: 5})
	if got, _, _ := r.do("diag-probe", "--pid", "4242"); got != 10 {
		t.Errorf("wrapped denied: exit %d", got)
	}
}

func TestDiagProbeFillsRunAs(t *testing.T) {
	r, f := newProbeRig(t)
	f.res.RunAs = ""
	_, out, _ := r.do("diag-probe", "--pid", "4242", "--json")
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	switch m["runAs"] {
	case "system", "elevated", "user", "unknown":
	default:
		t.Fatalf("runAs %v", m["runAs"])
	}
}

// diag-probe must not read or write settings, key, service or recordings.
func TestDiagProbeDoesNotTouchConfig(t *testing.T) {
	r, f := newProbeRig(t)
	hostRan := false
	r.d.hostRun = func(context.Context, blackbox.HostOptions) error { hostRan = true; return nil }
	before, _ := os.ReadDir(r.dir)
	if code, _, _ := r.do("diag-probe", "--pid", "4242", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if f.calls != 1 {
		t.Fatalf("probe called %d times", f.calls)
	}
	if len(r.svc.calls) != 0 || len(r.env.probed) != 0 || hostRan {
		t.Fatalf("service calls %v, probes %v, host run %v", r.svc.calls, r.env.probed, hostRan)
	}
	after, _ := os.ReadDir(r.dir)
	if len(after) != len(before) {
		t.Fatalf("the folder changed: %d -> %d entries", len(before), len(after))
	}
}

func TestUsageListsDiagProbe(t *testing.T) {
	r := newRig(t)
	if _, _, errs := r.do(); !strings.Contains(errs, "diag-probe") {
		t.Fatalf("usage does not list diag-probe: %q", errs)
	}
}
