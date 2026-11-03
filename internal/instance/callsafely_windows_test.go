//go:build windows

package instance

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// A panic in a callback is recovered (the listener lives on) and reported once
// with its value and a stack, so a broken Open or Quit handler leaves a trace.
func TestCallSafelyReportsAPanic(t *testing.T) {
	old := reportPanic
	t.Cleanup(func() { reportPanic = old })
	var calls int
	var gotWhat string
	var gotValue any
	var gotStack string
	reportPanic = func(what string, v any, stack []byte) {
		calls++
		gotWhat, gotValue, gotStack = what, v, string(stack)
	}

	callSafely("open", func() { panic("handler exploded") })

	if calls != 1 {
		t.Fatalf("reportPanic called %d times, want 1", calls)
	}
	if gotWhat != "open" {
		t.Errorf("reported callback = %q, want open", gotWhat)
	}
	if gotValue != "handler exploded" {
		t.Errorf("reported value = %v, want the panic value", gotValue)
	}
	if !strings.Contains(gotStack, "TestCallSafelyReportsAPanic") {
		t.Errorf("stack does not reach the panicking callback: %q", gotStack)
	}
}

func TestCallSafelyNilAndQuietCallbacksReportNothing(t *testing.T) {
	old := reportPanic
	t.Cleanup(func() { reportPanic = old })
	reportPanic = func(string, any, []byte) { t.Error("reportPanic called without a panic") }
	callSafely("open", nil)
	ran := false
	callSafely("quit", func() { ran = true })
	if !ran {
		t.Error("the callback did not run")
	}
}

// requireOwner is the one owner refusal the guard and the signal functions share.
func TestRequireOwner(t *testing.T) {
	old := ownerCheck
	t.Cleanup(func() { ownerCheck = old })

	ownerCheck = func(windows.Handle) (bool, error) { return true, nil }
	if err := requireOwner(0); err != nil {
		t.Errorf("owner ok: %v, want nil", err)
	}
	ownerCheck = func(windows.Handle) (bool, error) { return false, nil }
	if err := requireOwner(0); err != ErrNameHeldByOther {
		t.Errorf("not owner: %v, want ErrNameHeldByOther", err)
	}
	ownerCheck = func(windows.Handle) (bool, error) { return true, errors.New("boom") }
	if err := requireOwner(0); err != ErrNameHeldByOther {
		t.Errorf("owner check error: %v, want ErrNameHeldByOther", err)
	}
}
