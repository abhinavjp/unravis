//go:build windows

package tray

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Window messages and ids used by the tray window.
const (
	callbackMsg = 0x8000 + 1 // WM_APP+1: notification-area callback
	stopMsg     = 0x8000 + 2 // WM_APP+2: private stop request (gated by trayState.stopping)

	trayIconID   = 1
	retryTimerID = 1
	retryMillis  = 2000

	ninSelect    = 0x400 // NIN_SELECT
	ninKeySelect = 0x403 // NIN_KEYSELECT

	wmDestroy         = 0x0002
	wmClose           = 0x0010
	wmQuit            = 0x0012
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
	wmContextMenu     = 0x007B
	wmTimer           = 0x0113
	wmLButtonUp       = 0x0202
	wmRButtonUp       = 0x0205

	smShuttingDown = 0x2000

	wsPopup = 0x80000000
)

var (
	procRegisterClassEx      = user32.NewProc("RegisterClassExW")
	procUnregisterClass      = user32.NewProc("UnregisterClassW")
	procCreateWindowEx       = user32.NewProc("CreateWindowExW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procIsWindow             = user32.NewProc("IsWindow")
	procDefWindowProc        = user32.NewProc("DefWindowProcW")
	procGetMessage           = user32.NewProc("GetMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessage      = user32.NewProc("DispatchMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procRegisterWindowMsg    = user32.NewProc("RegisterWindowMessageW")
	procSetTimer             = user32.NewProc("SetTimer")
	procKillTimer            = user32.NewProc("KillTimer")
	procEndMenu              = user32.NewProc("EndMenu")
	wndProcCallback          = windows.NewCallback(wndProc)
	classCounter             atomic.Uint32
	active                   atomic.Pointer[trayState]
	errAlreadyRunning        = errors.New("tray: already running")
	errNoWindowClassOrWindow = errors.New("tray: cannot create the tray window")
)

// Test seams. The defaults are the real calls.
var (
	addIconHook        = addIcon
	deleteIconHook     = deleteIcon
	popupHook          = showMenu
	endingSession      = systemEnding
	armRetryHook       = realArmRetry
	disarmRetryHook    = realDisarmRetry
	shuttingDownMetric = realShuttingDownMetric
	readyHook          = func(hwnd windows.Handle) {}
)

// trayState is the one tray of this process. The window procedure runs on the
// tray thread and is the only place that touches the non-atomic fields.
type trayState struct {
	o          Options
	hwnd       windows.Handle
	taskbarMsg uint32

	icon     windows.Handle
	added    bool // the icon is currently in the notification area
	retrying bool // the retry timer is armed
	deleted  bool // deleteIconHook has run
	inLoop   bool // the message loop is running (WM_DESTROY may post WM_QUIT)

	menuOpen, stopping, destroying atomic.Bool
}

// wndClassEx mirrors WNDCLASSEXW.
type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

// winMsg mirrors MSG.
type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

// Run shows the notification-area icon and serves it until ctx is cancelled,
// then deletes the icon, destroys the window and returns nil. It runs the
// window on one locked OS thread. The window is a hidden top-level window, not
// a message-only window, because message-only windows do not receive the
// broadcast TaskbarCreated message. The message filter is never loosened, so
// the integrity-level wall against low-integrity senders stays up.
//
// Window messages carry no access check: only the private stop message (gated
// by an internal flag) and a genuine system end-of-session can end the loop. A
// foreign close, destroy, quit or posted end-of-session message does nothing.
//
// Accepted risk: any process of the same user at the same or higher integrity level can send the open or quit signal. Open re-arms a new launch code, so the previous link stops working, and each accepted open starts rundll32.exe, which adds a process-creation event. Quit ends the app. This is accepted: such a process already controls the user's session. The session token stays protected by the one-time code.
func Run(ctx context.Context, o Options) error {
	if err := o.validate(); err != nil {
		return err
	}
	st := &trayState{o: o}
	if !active.CompareAndSwap(nil, st) {
		return errAlreadyRunning
	}
	defer active.Store(nil)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	return st.run(ctx)
}

func (st *trayState) run(ctx context.Context) error {
	hInst, _, _ := procGetModuleHandle.Call(0)
	className, err := windows.UTF16PtrFromString(fmt.Sprintf("UnravisTrayWindow%d", classCounter.Add(1)))
	if err != nil {
		return err
	}
	wc := wndClassEx{
		lpfnWndProc:   wndProcCallback,
		hInstance:     hInst,
		lpszClassName: className,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if r, _, cerr := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("tray: register window class: %w", cerr)
	}
	defer procUnregisterClass.Call(uintptr(unsafe.Pointer(className)), hInst)

	h, _, cerr := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(className)), 0, wsPopup,
		0, 0, 0, 0, 0, 0, hInst, 0)
	if h == 0 {
		return fmt.Errorf("%w: %v", errNoWindowClassOrWindow, cerr)
	}
	hwnd := windows.Handle(h)
	st.hwnd = hwnd
	// From here on every path destroys the window and deletes the icon.
	defer st.teardown()

	name, _ := windows.UTF16PtrFromString("TaskbarCreated")
	if m, _, _ := procRegisterWindowMsg.Call(uintptr(unsafe.Pointer(name))); m != 0 {
		st.taskbarMsg = uint32(m)
	}
	st.icon = exeIcon()
	st.tryAdd(hwnd)
	readyHook(hwnd)

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			st.stopping.Store(true)
			procPostMessage.Call(uintptr(hwnd), stopMsg, 0, 0)
		case <-done:
		}
	}()

	st.inLoop = true
	var msg winMsg
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		n := int32(ret)
		if n == -1 {
			st.o.logf("tray: message loop failed, leaving it")
			break
		}
		if n == 0 {
			if st.destroying.Load() {
				break
			}
			// A quit posted by another process: ignore it and go on.
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
	st.inLoop = false
	return nil
}

// teardown destroys the window if it still exists and makes sure the icon is
// gone. It runs on the tray thread on every path that created the window.
func (st *trayState) teardown() {
	st.inLoop = false
	st.destroying.Store(true)
	if r, _, _ := procIsWindow.Call(uintptr(st.hwnd)); r != 0 {
		procDestroyWindow.Call(uintptr(st.hwnd))
	}
	if !st.deleted {
		st.deleted = true
		deleteIconHook(st.hwnd, trayIconID)
	}
}

// tryAdd (re)adds the icon. A failure arms the retry timer; a success disarms it.
func (st *trayState) tryAdd(hwnd windows.Handle) {
	st.added = false
	if err := addIconHook(hwnd, trayIconID, callbackMsg, st.icon, st.o.Tooltip); err != nil {
		st.o.logf("tray: %v; will retry", err)
		if !st.retrying {
			st.retrying = true
			armRetryHook(hwnd)
		}
		return
	}
	st.added = true
	if st.retrying {
		st.retrying = false
		disarmRetryHook(hwnd)
	}
}

// wndProc is the single window procedure. It never lets a panic escape.
func wndProc(hwnd, msg, wparam, lparam uintptr) (ret uintptr) {
	st := active.Load()
	if st == nil {
		return defWindowProc(hwnd, msg, wparam, lparam)
	}
	defer func() {
		if r := recover(); r != nil {
			st.o.logf("tray: recovered from a panic in a callback: %v", r)
			ret = 0
		}
	}()
	return st.handle(windows.Handle(hwnd), uint32(msg), wparam, lparam)
}

func defWindowProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	r, _, _ := procDefWindowProc.Call(hwnd, msg, wparam, lparam)
	return r
}

func (st *trayState) handle(hwnd windows.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	if st.taskbarMsg != 0 && msg == st.taskbarMsg {
		st.tryAdd(hwnd)
		return 0
	}
	switch msg {
	case callbackMsg:
		// NOTIFYICON_VERSION_4: event in LOWORD(lParam), icon id in HIWORD(lParam).
		event := uint32(lparam) & 0xFFFF
		if id := (uint32(lparam) >> 16) & 0xFFFF; id != trayIconID {
			return 0
		}
		switch event {
		case wmLButtonUp, ninKeySelect:
			st.o.OnOpen()
		case wmContextMenu:
			st.showPopup(hwnd)
		}
		// WM_RBUTTONUP and NIN_SELECT also arrive in version 4 and are ignored.
		return 0
	case wmTimer:
		// Handled here, never passed on: a timer message from outside could
		// carry a callback address in lParam.
		if wparam == retryTimerID && !st.added {
			st.tryAdd(hwnd)
		}
		return 0
	case wmQueryEndSession:
		return 1
	case wmEndSession:
		if wparam != 0 && endingSession() && st.o.OnEndSession != nil {
			st.o.OnEndSession()
		}
		return 0
	case wmClose:
		return 0
	case stopMsg:
		if !st.stopping.Load() {
			return 0
		}
		if st.menuOpen.Load() {
			procEndMenu.Call()
			procPostMessage.Call(uintptr(hwnd), stopMsg, 0, 0)
			return 0
		}
		st.destroying.Store(true)
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	case wmDestroy:
		if !st.destroying.Load() {
			return 0
		}
		if !st.deleted {
			st.deleted = true
			deleteIconHook(hwnd, trayIconID)
		}
		if st.inLoop {
			procPostQuitMessage.Call(0)
		}
		return 0
	}
	return defWindowProc(uintptr(hwnd), uintptr(msg), wparam, lparam)
}

// showPopup shows the menu once; a request while one is open is dropped.
func (st *trayState) showPopup(hwnd windows.Handle) {
	if !st.menuOpen.CompareAndSwap(false, true) {
		return
	}
	defer st.menuOpen.Store(false)
	if id, ok := popupHook(hwnd, st.o.Menu()); ok {
		st.o.OnSelect(id)
	}
}

func realArmRetry(hwnd windows.Handle) {
	procSetTimer.Call(uintptr(hwnd), retryTimerID, retryMillis, 0)
}

func realDisarmRetry(hwnd windows.Handle) {
	procKillTimer.Call(uintptr(hwnd), retryTimerID)
}

func realShuttingDownMetric() int32 {
	r, _, _ := procGetSystemMetrics.Call(smShuttingDown)
	return int32(r)
}

// systemEnding reports whether Windows itself is ending the session. It reads
// the system metric only: InSendMessage would be true for any sender.
func systemEnding() bool {
	return shuttingDownMetric() != 0
}
