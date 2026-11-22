//go:build windows

package setup

import (
	"time"

	"golang.org/x/sys/windows"
)

// ShowNotice shows a small message box. It is for the one case where an update nobody watched left the person without a running
// program, so there is no other place to tell them. The result is ignored.
func ShowNotice(title, text string) {
	t, err1 := windows.UTF16PtrFromString(title)
	c, err2 := windows.UTF16PtrFromString(text)
	if err1 != nil || err2 != nil {
		return
	}
	windows.MessageBox(0, c, t, windows.MB_OK|windows.MB_ICONWARNING|windows.MB_SETFOREGROUND)
}

// idOK is the MessageBox answer for the OK button.
const idOK = 1

// AskOKCancel shows a native OK/Cancel box and returns true only when the person clicks OK. Cancel, a closed box, an error, or no answer
// within wait is false (then the process ends, and the box with it). No topmost flag and no focus tricks: the box comes to the front the
// way Windows lets a program ask for it (MB_SETFOREGROUND), and that is best effort.
func AskOKCancel(title, text string, wait time.Duration) bool {
	t, err1 := windows.UTF16PtrFromString(title)
	c, err2 := windows.UTF16PtrFromString(text)
	if err1 != nil || err2 != nil {
		return false
	}
	answer := make(chan int32, 1)
	go func() {
		r, _ := windows.MessageBox(0, c, t, windows.MB_OKCANCEL|windows.MB_ICONINFORMATION|windows.MB_SETFOREGROUND)
		answer <- r
	}()
	select {
	case r := <-answer:
		return r == idOK
	case <-time.After(wait):
		return false
	}
}
