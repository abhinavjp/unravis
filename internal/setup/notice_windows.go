//go:build windows

package setup

import (
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
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

// idOK and idCancel are the MessageBox answers for the OK and Cancel buttons.
const (
	idOK     = 1
	idCancel = 2
)

// askBox shows one native box, tells shown how to close it once the box's thread is known, and returns the answer. Production uses
// nativeBox. Tests replace it (never show a real box from a test: a box stays on the person's desktop until someone clicks it, and the
// local CI runs the Windows tests many times an hour). The real box is only tested behind UNRAVIS_REAL_DIALOG_TESTS=1.
var askBox = nativeBox

// AskOKCancel shows a native OK/Cancel box and returns true only when the person clicks OK. Cancel, a closed box, an error, or no answer
// within wait is false; on no answer the box is closed (WM_CLOSE, which answers Cancel) so it never stays on screen after this returns.
// No topmost flag and no focus tricks: the box comes to the front the way Windows lets a program ask for it (MB_SETFOREGROUND), and that
// is best effort.
func AskOKCancel(title, text string, wait time.Duration) bool {
	answer := make(chan int32, 1)
	var mu sync.Mutex
	var closeBox func()
	var gaveUp atomic.Bool
	go func() {
		if gaveUp.Load() { // the wait ran out before this goroutine even started: never show the box
			answer <- idCancel
			return
		}
		r := askBox(title, text, windows.MB_OKCANCEL|windows.MB_ICONINFORMATION|windows.MB_SETFOREGROUND, func(c func()) {
			mu.Lock()
			closeBox = c
			mu.Unlock()
		})
		answer <- r
	}()
	select {
	case r := <-answer:
		return r == idOK
	case <-time.After(wait):
		gaveUp.Store(true)
		select { // an answer that arrived at the same moment as the timeout still counts
		case r := <-answer:
			return r == idOK
		default:
		}
		// Close the box until it is gone. It may not exist yet (a slow start on a loaded PC), so ask again every 50 ms for up to 2 s.
		deadline := time.After(2 * time.Second)
		for {
			mu.Lock()
			c := closeBox
			mu.Unlock()
			if c != nil {
				c()
			}
			select {
			case <-answer:
				return false
			case <-deadline:
				return false
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}

var (
	user32dll            = windows.NewLazySystemDLL("user32.dll")
	procEnumThreadWindow = user32dll.NewProc("EnumThreadWindows")
	procPostMessageW     = user32dll.NewProc("PostMessageW")
)

const wmClose = 0x0010

// nativeBox runs the real MessageBox on a locked thread and hands shown a function that closes it from any other goroutine.
func nativeBox(title, text string, flags uint32, shown func(closeBox func())) int32 {
	t, err1 := windows.UTF16PtrFromString(title)
	c, err2 := windows.UTF16PtrFromString(text)
	if err1 != nil || err2 != nil {
		return idCancel
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tid := windows.GetCurrentThreadId()
	shown(func() { closeThreadWindows(tid) })
	r, _ := windows.MessageBox(0, c, t, flags)
	return r
}

// closeThreadWindows posts WM_CLOSE to every top-level window owned by thread tid (the message box). Best effort.
func closeThreadWindows(tid uint32) {
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
		return 1
	})
	_, _, _ = procEnumThreadWindow.Call(uintptr(tid), cb, 0)
}
