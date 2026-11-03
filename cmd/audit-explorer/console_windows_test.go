//go:build windows

package main

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestStdHandleUsableRejectsNullAndInvalid(t *testing.T) {
	if stdHandleUsable(0) {
		t.Error("handle 0 reported usable")
	}
	if stdHandleUsable(windows.InvalidHandle) {
		t.Error("InvalidHandle reported usable")
	}
}

func TestStdHandleUsableAcceptsAPipeAndAFile(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if !stdHandleUsable(windows.Handle(r.Fd())) {
		t.Error("pipe reported unusable")
	}
	f, err := os.CreateTemp(t.TempDir(), "std")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if !stdHandleUsable(windows.Handle(f.Fd())) {
		t.Error("file reported unusable")
	}
}

func TestUnusableStdHandlesReportsEachHandleSeparately(t *testing.T) {
	get := func(id uint32) (windows.Handle, error) {
		switch id {
		case windows.STD_INPUT_HANDLE:
			return 1, nil
		case windows.STD_OUTPUT_HANDLE:
			return 2, nil
		}
		return 3, nil
	}
	usable := func(h windows.Handle) bool { return h != 3 }
	if got := unusableStdHandles(get, usable); got != [3]bool{false, false, true} {
		t.Errorf("got %v, want only the error handle unusable", got)
	}
	failing := func(id uint32) (windows.Handle, error) {
		if id == windows.STD_OUTPUT_HANDLE {
			return 0, errors.New("no handle")
		}
		return 1, nil
	}
	if got := unusableStdHandles(failing, func(windows.Handle) bool { return true }); got != [3]bool{false, true, false} {
		t.Errorf("got %v, want only stdout unusable after a get error", got)
	}
}

func TestAttachConsoleWithSkipsAttachWhenNothingIsUnusable(t *testing.T) {
	attach := func() bool { t.Error("attach called"); return true }
	replace := func(int) bool { t.Error("replace called"); return true }
	if attachConsoleWith([3]bool{}, attach, replace) {
		t.Error("returned true with nothing to do")
	}
}

func TestAttachConsoleWithStopsWhenAttachFails(t *testing.T) {
	replace := func(int) bool { t.Error("replace called"); return true }
	if attachConsoleWith([3]bool{true, true, true}, func() bool { return false }, replace) {
		t.Error("returned true after a failed attach")
	}
}

func TestAttachConsoleWithReplacesOnlyTheUnusableHandles(t *testing.T) {
	var got []int
	replace := func(i int) bool { got = append(got, i); return true }
	if !attachConsoleWith([3]bool{false, true, true}, func() bool { return true }, replace) {
		t.Error("returned false although handles were replaced")
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("replace called for %v, want [1 2]", got)
	}
	if attachConsoleWith([3]bool{false, true, true}, func() bool { return true }, func(int) bool { return false }) {
		t.Error("returned true although no replace succeeded")
	}
}
