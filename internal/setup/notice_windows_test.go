//go:build windows

package setup

import (
	"testing"
	"time"
)

// AskOKCancel gives up after its wait and answers Cancel. This proves the timeout and that nothing panics; it cannot prove the box is
// visible or comes to the front: that is only seen on a PC with other windows open.
func TestAskOKCancelGivesUpAfterTheWait(t *testing.T) {
	done := make(chan bool, 1)
	go func() {
		done <- AskOKCancel("Unravis test", "This box is shown by a test and answers Cancel by itself.", 200*time.Millisecond)
	}()
	select {
	case got := <-done:
		if got {
			t.Fatal("no click, yet OK")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskOKCancel did not give up after its wait")
	}
}
