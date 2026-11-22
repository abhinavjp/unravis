//go:build windows

package setup

import (
	"testing"
	"time"
)

// AskOKCancel gives up after its wait and answers Cancel. This proves the timeout and that nothing panics; it cannot prove the box is
// visible or comes to the front: that is only seen on a PC with other windows open.
func TestAskOKCancelGivesUpAfterTheWait(t *testing.T) {
	const wait = 200 * time.Millisecond
	done := make(chan bool, 1)
	start := time.Now()
	go func() {
		done <- AskOKCancel("Unravis test", "This box is shown by a test and answers Cancel by itself.", wait)
	}()
	select {
	case got := <-done:
		if got {
			t.Fatal("no click, yet OK")
		}
		// A box that failed to show answers at once; only a real wait proves the timeout path.
		if el := time.Since(start); el < wait*3/4 {
			t.Fatalf("answered after %v, before the wait of %v: the box did not wait (it failed to show?), so the timeout was not tested", el, wait)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskOKCancel did not give up after its wait")
	}
}
