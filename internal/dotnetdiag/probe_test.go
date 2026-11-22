package dotnetdiag

import (
	"bytes"
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func testPID() uint32 { return uint32(os.Getpid()) + 1 }

func testOptions() Options {
	return Options{Duration: 50 * time.Millisecond, ReplyTimeout: 300 * time.Millisecond,
		StopTimeout: 300 * time.Millisecond, StreamCap: 1 << 20}
}

func reasonOf(t testing.TB, err error) Reason {
	t.Helper()
	if err == nil {
		return ""
	}
	var de *Error
	if !errors.As(err, &de) {
		t.Fatalf("error %v is not *Error", err)
	}
	return de.Reason
}

func TestProbeHappyPath(t *testing.T) {
	pid := testPID()
	h := newHonest(7, pid)
	d := &fakeDialer{handler: h.handle}
	res, err := Probe(context.Background(), d, Target{PID: pid}, testOptions())
	if err != nil || res.Reason != "" {
		t.Fatalf("err %v result %+v", err, res)
	}
	if !res.PipeOpened || res.ServerPID != pid || res.SessionID != "7" || !res.PreambleOK ||
		!res.StopAck || !res.StopSession || res.BytesBefore == 0 || res.EndedEarly {
		t.Fatalf("result %+v", res)
	}
	if res.TraceVersion != 4 || res.PointerSize != 8 || res.TracePID != pid || !res.TracePIDMatch {
		t.Fatalf("preamble fields %+v", res)
	}
	if res.StartMatch != nil {
		t.Fatal("StartMatch must be nil without start ticks")
	}
	if res.BytesBefore+res.BytesAfter < uint64(PreambleLen+3000) {
		t.Fatalf("bytes %d+%d", res.BytesBefore, res.BytesAfter)
	}
}

func TestProbeSendsOnlyStartAndStop(t *testing.T) {
	pid := testPID()
	h := newHonest(7, pid)
	d := &fakeDialer{handler: h.handle}
	if _, err := Probe(context.Background(), d, Target{PID: pid}, testOptions()); err != nil {
		t.Fatal(err)
	}
	if d.dials() != 2 {
		t.Fatalf("%d dials, want 2", d.dials())
	}
	if got := d.conn(0).Written(); !bytes.Equal(got, encodeStart()) {
		t.Fatalf("conn 1 got %x", got)
	}
	if got := d.conn(1).Written(); !bytes.Equal(got, encodeStop(7)) {
		t.Fatalf("conn 2 got %x", got)
	}
	for i := 0; i < 2; i++ {
		if !d.conn(i).Closed() {
			t.Fatalf("conn %d left open", i)
		}
	}
}

func TestProbeNoWriteBeforePeerCheck(t *testing.T) {
	pid := testPID()
	for name, peer := range map[string]Peer{
		"other pid":      {PID: pid + 1},
		"zero pid":       {PID: 0},
		"other start":    {PID: pid, StartTicks: 99},
		"pid and start":  {PID: 1, StartTicks: 1},
		"start via zero": {PID: pid, StartTicks: 0},
	} {
		t.Run(name, func(t *testing.T) {
			d := &fakeDialer{handler: newHonest(7, pid).handle, peer: func(int) Peer { return peer }}
			tg := Target{PID: pid}
			if name == "other start" || name == "start via zero" {
				tg.StartTicks = 5
			}
			res, err := Probe(context.Background(), d, tg, testOptions())
			if reasonOf(t, err) != ReasonPeerMismatch || res.Reason != ReasonPeerMismatch {
				t.Fatalf("err %v result %+v", err, res)
			}
			c := d.conn(0)
			if got := c.Written(); len(got) != 0 {
				t.Fatalf("wrote %d bytes before the peer check", len(got))
			}
			if !c.Closed() {
				t.Fatal("conn not closed")
			}
			if d.dials() != 1 {
				t.Fatalf("%d dials; no stop may follow a refused peer", d.dials())
			}
		})
	}
}

func TestProbeStartMatchReported(t *testing.T) {
	pid := testPID()
	d := &fakeDialer{handler: newHonest(7, pid).handle}
	res, err := Probe(context.Background(), d, Target{PID: pid, StartTicks: 1234}, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if res.StartMatch == nil || !*res.StartMatch || res.ServerStart != 1234 {
		t.Fatalf("result %+v", res)
	}
}

func TestProbeStopUsesSecondDial(t *testing.T) {
	pid := testPID()
	d := &fakeDialer{
		handler: newHonest(7, pid).handle,
		peer:    func(int) Peer { return Peer{PID: pid, StartTicks: 777} },
	}
	if _, err := Probe(context.Background(), d, Target{PID: pid}, testOptions()); err != nil {
		t.Fatal(err)
	}
	if len(d.targets) != 2 {
		t.Fatalf("%d dials", len(d.targets))
	}
	if d.targets[1].PID != pid || d.targets[1].StartTicks != 777 {
		t.Fatalf("second target %+v, want start ticks 777", d.targets[1])
	}
}

func TestProbeRefusesBadTargets(t *testing.T) {
	d := &fakeDialer{}
	for _, pid := range []uint32{0, uint32(os.Getpid())} {
		_, err := Probe(context.Background(), d, Target{PID: pid}, testOptions())
		if !errors.Is(err, ErrBadTarget) {
			t.Fatalf("pid %d: %v", pid, err)
		}
	}
	if d.dials() != 0 {
		t.Fatalf("%d dials before refusing", d.dials())
	}
}

func TestExitCodeMap(t *testing.T) {
	want := map[Reason]int{
		"": 0, ReasonOther: 1, ReasonDenied: 10, ReasonDisabled: 11, ReasonBusy: 12,
		ReasonPeerMismatch: 13, ReasonUnsupported: 14, ReasonRejected: 14, ReasonTimeout: 15,
		ReasonBadReply: 16, ReasonBadStream: 16, ReasonNotRunning: 17, ReasonStopFailed: 18,
		ReasonCancelled: 19,
	}
	for r, c := range want {
		if got := ExitCode(r); got != c {
			t.Errorf("%q: got %d, want %d", r, got, c)
		}
	}
	if ExitCode("never-heard-of-it") != 1 {
		t.Error("unknown reason must be exit 1")
	}
}

var allReasons = []Reason{
	ReasonNotRunning, ReasonDenied, ReasonDisabled, ReasonBusy, ReasonPeerMismatch, ReasonUnsupported,
	ReasonRejected, ReasonTimeout, ReasonBadReply, ReasonBadStream, ReasonStopFailed, ReasonCancelled, ReasonOther,
}

func TestReasonSentencesAreFixed(t *testing.T) {
	long := regexp.MustCompile(`[0-9]{9,}`)
	for _, r := range append([]Reason{""}, allReasons...) {
		s, ok := sentences[r]
		if !ok || s == "" {
			t.Errorf("reason %q has no sentence", r)
		}
		if strings.ContainsAny(s, `%\/`) || long.MatchString(s) {
			t.Errorf("sentence for %q has a format verb, path separator or long number: %q", r, s)
		}
		if Sentence(r) != s {
			t.Errorf("Sentence(%q) differs", r)
		}
	}
	if Sentence("zzz") != sentences[ReasonOther] {
		t.Error("unknown reason must read as other")
	}
}
