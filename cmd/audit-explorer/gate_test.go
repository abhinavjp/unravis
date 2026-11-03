package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/instance"
)

// fakeGate records every call so a test can say what the gate did and did not
// touch.
type fakeGate struct {
	app          string
	namesCalls   int
	acquireCalls int
	signalCalls  int
	sleepCalls   int
	released     int
	signalKinds  []instance.Kind

	first      bool
	acquireErr error
	signalErrs []error // consumed one per call; the last one repeats
	isElevated bool
}

func (f *fakeGate) deps() gateDeps {
	return gateDeps{
		names: func(app string) (instance.Names, error) {
			f.namesCalls++
			f.app = app
			return instance.NewNames(app, "S-1-5-21-1-2-3-1001")
		},
		acquire: func(instance.Names) (func(), bool, error) {
			f.acquireCalls++
			return func() { f.released++ }, f.first, f.acquireErr
		},
		signal: func(_ instance.Names, k instance.Kind) error {
			f.signalCalls++
			f.signalKinds = append(f.signalKinds, k)
			if len(f.signalErrs) == 0 {
				return nil
			}
			i := f.signalCalls - 1
			if i >= len(f.signalErrs) {
				i = len(f.signalErrs) - 1
			}
			return f.signalErrs[i]
		},
		sleep: func(d time.Duration) {
			f.sleepCalls++
			if d != 250*time.Millisecond {
				panic("unexpected sleep length")
			}
		},
		elevated: func() bool { return f.isElevated },
	}
}

func TestGateFirstCopyProceeds(t *testing.T) {
	f := &fakeGate{first: true}
	res, err := enterSingleInstance(false, false, f.deps())
	if err != nil || !res.proceed {
		t.Fatalf("proceed=%v err=%v, want proceed", res.proceed, err)
	}
	if res.names.Mutex == "" || res.release == nil {
		t.Fatalf("names %+v release nil=%v, want both set", res.names, res.release == nil)
	}
	if f.signalCalls != 0 {
		t.Errorf("signal called %d times by the first copy", f.signalCalls)
	}
	res.release()
	if f.released != 1 {
		t.Errorf("release ran the fake %d times, want 1", f.released)
	}
	if f.app != instance.DefaultApp {
		t.Errorf("app %q, want %q", f.app, instance.DefaultApp)
	}
}

func TestGateSecondCopySignalsOpenAndExitsZero(t *testing.T) {
	f := &fakeGate{}
	res, err := enterSingleInstance(false, false, f.deps())
	if err != nil || res.proceed {
		t.Fatalf("proceed=%v err=%v, want no proceed and no error", res.proceed, err)
	}
	if len(f.signalKinds) != 1 || f.signalKinds[0] != instance.Open {
		t.Fatalf("signals %v, want one Open", f.signalKinds)
	}
}

func TestGateBackgroundSecondCopyExitsSilently(t *testing.T) {
	f := &fakeGate{}
	res, err := enterSingleInstance(false, true, f.deps())
	if err != nil || res.proceed {
		t.Fatalf("proceed=%v err=%v, want no proceed and no error", res.proceed, err)
	}
	if f.signalCalls != 0 {
		t.Fatalf("signal called %d times for a background second copy", f.signalCalls)
	}
}

func TestGateRetriesWhileTheFirstCopyIsStarting(t *testing.T) {
	f := &fakeGate{signalErrs: []error{instance.ErrNoInstance, instance.ErrNoInstance, instance.ErrNoInstance, nil}}
	res, err := enterSingleInstance(false, false, f.deps())
	if err != nil || res.proceed {
		t.Fatalf("proceed=%v err=%v, want no proceed and no error", res.proceed, err)
	}
	if f.signalCalls != 4 || f.sleepCalls != 3 {
		t.Fatalf("signal %d times and slept %d times, want 4 and 3", f.signalCalls, f.sleepCalls)
	}
}

func TestGateGivesUpWithErrAlreadyRunning(t *testing.T) {
	f := &fakeGate{signalErrs: []error{instance.ErrNoInstance}}
	_, err := enterSingleInstance(false, false, f.deps())
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err %v, want ErrAlreadyRunning", err)
	}
	if f.signalCalls != 20 || f.sleepCalls != 19 {
		t.Fatalf("signal %d times and slept %d times, want 20 and 19", f.signalCalls, f.sleepCalls)
	}
}

func TestGateAnotherAccountIsReportedClearly(t *testing.T) {
	cases := map[string]*fakeGate{
		"acquire": {acquireErr: instance.ErrAccessDenied},
		"signal":  {signalErrs: []error{instance.ErrAccessDenied}},
	}
	for name, f := range cases {
		res, err := enterSingleInstance(false, false, f.deps())
		if !errors.Is(err, ErrRunningForAnotherAccount) || res.proceed {
			t.Fatalf("%s: err %v proceed %v, want ErrRunningForAnotherAccount", name, err, res.proceed)
		}
		if !strings.Contains(err.Error(), "or as administrator") {
			t.Errorf("%s: message %q lacks %q", name, err, "or as administrator")
		}
		if f.sleepCalls != 0 {
			t.Errorf("%s: retried %d times", name, f.sleepCalls)
		}
	}
}

func TestGateUsesTheDemoNamesForDemo(t *testing.T) {
	f := &fakeGate{first: true}
	if _, err := enterSingleInstance(true, false, f.deps()); err != nil {
		t.Fatal(err)
	}
	if f.app != instance.DemoApp {
		t.Fatalf("app %q, want %q", f.app, instance.DemoApp)
	}
}

func TestGateRefusesWhenElevatedBeforeTouchingTheMutex(t *testing.T) {
	for _, background := range []bool{false, true} {
		f := &fakeGate{first: true, isElevated: true}
		res, err := enterSingleInstance(false, background, f.deps())
		if !errors.Is(err, ErrElevated) || res.proceed {
			t.Fatalf("background=%v: err %v proceed %v, want ErrElevated", background, err, res.proceed)
		}
		if f.namesCalls != 0 || f.acquireCalls != 0 || f.signalCalls != 0 {
			t.Fatalf("background=%v: names %d acquire %d signal %d, want none", background, f.namesCalls, f.acquireCalls, f.signalCalls)
		}
	}
}

func TestGateReportsASquattedName(t *testing.T) {
	cases := map[string]*fakeGate{
		"acquire": {acquireErr: instance.ErrNameHeldByOther},
		"signal":  {signalErrs: []error{instance.ErrNameHeldByOther}},
	}
	for name, f := range cases {
		res, err := enterSingleInstance(false, false, f.deps())
		if !errors.Is(err, ErrNameHeld) || res.proceed {
			t.Fatalf("%s: err %v proceed %v, want ErrNameHeld", name, err, res.proceed)
		}
		if !strings.Contains(err.Error(), "holds Unravis's name") {
			t.Errorf("%s: message %q lacks %q", name, err, "holds Unravis's name")
		}
		if f.signalCalls > 1 || f.sleepCalls != 0 {
			t.Errorf("%s: signal %d times, slept %d times; want no retry", name, f.signalCalls, f.sleepCalls)
		}
	}
}

func TestStartupCodeIsEmptyInBackground(t *testing.T) {
	code, err := startupCode(true, func() (string, error) {
		t.Fatal("mint called for a background start")
		return "", nil
	})
	if code != "" || err != nil {
		t.Fatalf("code %q err %v, want empty and nil", code, err)
	}
}

func TestStartupCodeMintsOtherwise(t *testing.T) {
	code, err := startupCode(false, func() (string, error) { return "minted", nil })
	if code != "minted" || err != nil {
		t.Fatalf("code %q err %v, want minted", code, err)
	}
	want := errors.New("no entropy")
	if _, err := startupCode(false, func() (string, error) { return "", want }); !errors.Is(err, want) {
		t.Fatalf("err %v, want %v", err, want)
	}
}

func TestMainSkipsTheGateWithNoBrowser(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	decl := strings.Index(src, "interactive :=")
	if decl < 0 {
		t.Fatal("main.go has no interactive declaration")
	}
	line := src[decl:]
	if nl := strings.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	if !strings.Contains(line, "noBrowser") {
		t.Fatalf("interactive is not derived from noBrowser: %q", line)
	}
	if strings.Count(src, "enterSingleInstance(") != 1 {
		t.Fatalf("want exactly one call to enterSingleInstance, found %d", strings.Count(src, "enterSingleInstance("))
	}
	open := strings.Index(src, "if interactive {")
	call := strings.Index(src, "enterSingleInstance(")
	if open < 0 || call < open {
		t.Fatalf("enterSingleInstance (%d) is not after an `if interactive {` block opening (%d)", call, open)
	}
	// Find the end of the block by counting braces from its opening.
	depth, end := 0, -1
	for i := open + len("if interactive "); i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 || call > end {
		t.Fatalf("enterSingleInstance (%d) sits outside the `if interactive` block (ends %d)", call, end)
	}
}
