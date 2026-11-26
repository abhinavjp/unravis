//go:build windows

package setup

import (
	"os"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"unsafe"
	"time"

	"golang.org/x/sys/windows"
)

// fakeBox stands in for the real MessageBox: it blocks like a box that nobody answers, and answers Cancel when asked to close, as
// WM_CLOSE does. No window is ever created.
func fakeBox(closed *sync.WaitGroup) func(title, text string, flags uint32, shown func(func())) int32 {
	return func(title, text string, flags uint32, shown func(func())) int32 {
		release := make(chan struct{})
		var once sync.Once
		shown(func() { once.Do(func() { close(release); closed.Done() }) })
		<-release
		return idCancel
	}
}

// With no answer, AskOKCancel gives up after its wait, answers Cancel, and closes the box (nothing is left on the desktop).
func TestAskOKCancelGivesUpAfterTheWaitAndClosesTheBox(t *testing.T) {
	var closed sync.WaitGroup
	closed.Add(1)
	old := askBox
	askBox = fakeBox(&closed)
	t.Cleanup(func() { askBox = old })

	const wait = 200 * time.Millisecond
	done := make(chan bool, 1)
	start := time.Now()
	go func() { done <- AskOKCancel("t", "x", wait) }() // dialogok: askBox is replaced by fakeBox
	select {
	case got := <-done:
		if got {
			t.Fatal("no click, yet OK")
		}
		if el := time.Since(start); el < wait*3/4 {
			t.Fatalf("answered after %v, before the wait of %v", el, wait)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("AskOKCancel did not give up after its wait")
	}
	closedCh := make(chan struct{})
	go func() { closed.Wait(); close(closedCh) }()
	select {
	case <-closedCh:
	case <-time.After(time.Second):
		t.Fatal("the box was not closed after the wait")
	}
}

// A click on OK is the only true answer.
func TestAskOKCancelAnswersTrueOnlyForOK(t *testing.T) {
	old := askBox
	t.Cleanup(func() { askBox = old })
	for _, tc := range []struct {
		r    int32
		want bool
	}{{idOK, true}, {idCancel, false}, {0, false}} {
		askBox = func(string, string, uint32, func(func())) int32 { return tc.r }
		if got := AskOKCancel("t", "x", time.Second); got != tc.want { // dialogok: askBox is replaced by a fake
			t.Errorf("answer %d: got %v, want %v", tc.r, got, tc.want)
		}
	}
}

// The real MessageBox. It shows a window on the desktop for a moment, so it runs only when asked for (UNRAVIS_REAL_DIALOG_TESTS=1);
// neither CI nor the local runner sets that. It proves the box shows, times out to Cancel and is closed by WM_CLOSE.
func TestAskOKCancelRealBoxIsClosedAfterTheWait(t *testing.T) {
	if os.Getenv("UNRAVIS_REAL_DIALOG_TESTS") != "1" {
		t.Skip("shows a real window; set UNRAVIS_REAL_DIALOG_TESTS=1 to run it by hand")
	}
	title := "Unravis test " + strconv.Itoa(os.Getpid()) // unique, so a box of another run is never mistaken for ours
	if AskOKCancel(title, "This box is shown by a test and closes itself.", 500*time.Millisecond) { // dialogok: opt-in by env var, closes itself
		t.Fatal("no click, yet OK")
	}
	find := syscall.NewLazyDLL("user32.dll").NewProc("FindWindowW")
	name, _ := windows.UTF16PtrFromString(title)
	deadline := time.Now().Add(3 * time.Second)
	for {
		h, _, _ := find.Call(0, uintptr(unsafePtr(name)))
		if h == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the box is still on screen after AskOKCancel returned")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func unsafePtr(p *uint16) uintptr { return uintptr(unsafe.Pointer(p)) }
