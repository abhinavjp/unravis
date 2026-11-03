//go:build windows

package tray

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procSendMessage             = user32.NewProc("SendMessageW")
	procIsWindowVisible         = user32.NewProc("IsWindowVisible")
	procGetParent               = user32.NewProc("GetParent")
	procGetWindowLong           = user32.NewProc("GetWindowLongW")
	procGetWindowThreadProcID   = user32.NewProc("GetWindowThreadProcessId")
	procPostThreadMessage       = user32.NewProc("PostThreadMessageW")
	procRegisterWindowMsgByName = user32.NewProc("RegisterWindowMessageW")
)

const (
	guard      = 10 * time.Second
	gwlStyle   = ^uintptr(15) // -16 as a uintptr
	wsChildBit = 0x40000000
)

func makeLong(lo, hi uint32) uintptr { return uintptr(lo&0xFFFF | hi<<16) }

func sendMessage(h windows.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	r, _, _ := procSendMessage.Call(uintptr(h), uintptr(msg), wparam, lparam)
	return r
}

func postMessage(h windows.Handle, msg uint32, wparam, lparam uintptr) {
	procPostMessage.Call(uintptr(h), uintptr(msg), wparam, lparam)
}

func isWindow(h windows.Handle) bool {
	r, _, _ := procIsWindow.Call(uintptr(h))
	return r != 0
}

// rig runs one tray with every icon, menu and timer call replaced.
type rig struct {
	t      *testing.T
	o      Options
	hwnd   windows.Handle
	cancel context.CancelFunc
	done   chan error

	adds, deletes, arms, disarms, popups atomic.Int32
	opens, selects, endSessions          atomic.Int32
	selected                             atomic.Uint32

	addErrs []error // consumed one per add call; nil entries succeed
	popup   func(hwnd windows.Handle, items []Item) (uint32, bool)
	items   []Item
	ending  atomic.Bool // what endingSession reports
}

func newRig(t *testing.T) *rig {
	r := &rig{t: t}
	r.items = []Item{{ID: 1, Text: "Open"}, {Separator: true}, {ID: 2, Text: "Quit"}}
	r.o = Options{
		Tooltip:      "Unravis",
		Menu:         func() []Item { return r.items },
		OnSelect:     func(id uint32) { r.selects.Add(1); r.selected.Store(id) },
		OnOpen:       func() { r.opens.Add(1) },
		OnEndSession: func() { r.endSessions.Add(1) },
		Logf:         t.Logf,
	}
	return r
}

// start installs the hooks, runs the tray and waits until its window exists.
func (r *rig) start() {
	t := r.t
	oAdd, oDel, oPop, oEnd, oArm, oDisarm, oReady := addIconHook, deleteIconHook, popupHook, endingSession, armRetryHook, disarmRetryHook, readyHook
	t.Cleanup(func() {
		addIconHook, deleteIconHook, popupHook, endingSession, armRetryHook, disarmRetryHook, readyHook = oAdd, oDel, oPop, oEnd, oArm, oDisarm, oReady
	})
	var addCall atomic.Int32
	addIconHook = func(windows.Handle, uint32, uint32, windows.Handle, string) error {
		i := int(addCall.Add(1)) - 1
		r.adds.Add(1)
		if i < len(r.addErrs) {
			return r.addErrs[i]
		}
		return nil
	}
	deleteIconHook = func(windows.Handle, uint32) { r.deletes.Add(1) }
	popupHook = func(h windows.Handle, items []Item) (uint32, bool) {
		r.popups.Add(1)
		if r.popup != nil {
			return r.popup(h, items)
		}
		return 0, false
	}
	armRetryHook = func(windows.Handle) { r.arms.Add(1) }
	disarmRetryHook = func(windows.Handle) { r.disarms.Add(1) }
	endingSession = func() bool { return r.ending.Load() }
	ready := make(chan windows.Handle, 1)
	readyHook = func(h windows.Handle) { ready <- h }

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan error, 1)
	go func() { r.done <- Run(ctx, r.o) }()
	t.Cleanup(func() { // runs before the hook restore above
		cancel()
		select {
		case <-r.done:
		case <-time.After(guard):
			t.Error("Run did not return after cancel")
		}
	})
	select {
	case r.hwnd = <-ready:
	case err := <-r.done:
		t.Fatalf("Run returned early: %v", err)
	case <-time.After(guard):
		t.Fatal("tray window was not created")
	}
}

// canary posts a left click and waits for it, so every earlier posted message
// has been handled.
func (r *rig) canary() {
	before := r.opens.Load()
	postMessage(r.hwnd, callbackMsg, 0, makeLong(wmLButtonUp, trayIconID))
	deadline := time.Now().Add(guard)
	for r.opens.Load() == before {
		if time.Now().After(deadline) {
			r.t.Fatal("canary message was not handled")
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *rig) notRunning() bool {
	select {
	case <-r.done:
		return false
	default:
		return true
	}
}

func TestTrayWindowIsTopLevelAndHidden(t *testing.T) {
	r := newRig(t)
	r.start()
	if p, _, _ := procGetParent.Call(uintptr(r.hwnd)); p != 0 {
		t.Errorf("GetParent = %#x, want 0", p)
	}
	if v, _, _ := procIsWindowVisible.Call(uintptr(r.hwnd)); v != 0 {
		t.Error("window is visible")
	}
	style, _, _ := procGetWindowLong.Call(uintptr(r.hwnd), gwlStyle)
	if style&wsChildBit != 0 {
		t.Errorf("window style %#x has WS_CHILD", style)
	}
}

func TestTrayLeftClickCallsOnOpen(t *testing.T) {
	r := newRig(t)
	r.start()
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(wmLButtonUp, trayIconID))
	if got := r.opens.Load(); got != 1 {
		t.Fatalf("after WM_LBUTTONUP opens = %d, want 1", got)
	}
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(ninKeySelect, trayIconID))
	if got := r.opens.Load(); got != 2 {
		t.Fatalf("after NIN_KEYSELECT opens = %d, want 2", got)
	}
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(ninSelect, trayIconID))
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(wmRButtonUp, trayIconID))
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(wmLButtonUp, 2))
	if got := r.opens.Load(); got != 2 {
		t.Errorf("NIN_SELECT, WM_RBUTTONUP or a foreign id opened: opens = %d, want 2", got)
	}
	if r.popups.Load() != 0 {
		t.Error("a click opened the menu")
	}
}

func TestTrayContextMenuIsBuiltFromMenuFunc(t *testing.T) {
	r := newRig(t)
	var got []Item
	var dismiss atomic.Bool
	r.popup = func(_ windows.Handle, items []Item) (uint32, bool) {
		if dismiss.Load() {
			return 0, false
		}
		got = items
		return 2, true
	}
	r.start()
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(wmContextMenu, trayIconID))
	if !reflect.DeepEqual(got, r.items) {
		t.Errorf("popup items = %+v, want %+v", got, r.items)
	}
	if r.selects.Load() != 1 || r.selected.Load() != 2 {
		t.Errorf("OnSelect calls = %d, id = %d; want 1 call with id 2", r.selects.Load(), r.selected.Load())
	}
	// A dismissed menu selects nothing.
	dismiss.Store(true)
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(wmContextMenu, trayIconID))
	if r.selects.Load() != 1 {
		t.Error("a dismissed menu called OnSelect")
	}
}

func TestTrayIgnoresASecondMenuRequestWhileOpen(t *testing.T) {
	r := newRig(t)
	var first atomic.Bool
	r.popup = func(h windows.Handle, _ []Item) (uint32, bool) {
		if first.CompareAndSwap(false, true) {
			sendMessage(h, callbackMsg, 0, makeLong(wmContextMenu, trayIconID))
		}
		return 0, false
	}
	r.start()
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(wmContextMenu, trayIconID))
	if got := r.popups.Load(); got != 1 {
		t.Errorf("popup ran %d times, want 1", got)
	}
	// The flag is released afterwards.
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(wmContextMenu, trayIconID))
	if got := r.popups.Load(); got != 2 {
		t.Errorf("popup ran %d times after the menu closed, want 2", got)
	}
}

func TestTrayReaddsTheIconOnTaskbarCreated(t *testing.T) {
	r := newRig(t)
	r.start()
	if got := r.adds.Load(); got != 1 {
		t.Fatalf("adds after start = %d, want 1", got)
	}
	name, _ := windows.UTF16PtrFromString("TaskbarCreated")
	msg, _, _ := procRegisterWindowMsgByName.Call(uintptr(unsafe.Pointer(name)))
	if msg == 0 {
		t.Fatal("cannot register TaskbarCreated")
	}
	sendMessage(r.hwnd, uint32(msg), 0, 0)
	if got := r.adds.Load(); got != 2 {
		t.Errorf("adds after TaskbarCreated = %d, want 2", got)
	}
}

func TestTrayRetriesWhenAddFails(t *testing.T) {
	r := newRig(t)
	r.addErrs = []error{errors.New("shell not ready")}
	r.start()
	if r.adds.Load() != 1 || r.arms.Load() != 1 {
		t.Fatalf("after start adds = %d, arms = %d; want 1 and 1", r.adds.Load(), r.arms.Load())
	}
	sendMessage(r.hwnd, wmTimer, retryTimerID, 0)
	if r.adds.Load() != 2 || r.disarms.Load() != 1 {
		t.Fatalf("after the timer adds = %d, disarms = %d; want 2 and 1", r.adds.Load(), r.disarms.Load())
	}
	sendMessage(r.hwnd, wmTimer, retryTimerID, 0)
	if r.adds.Load() != 2 || r.disarms.Load() != 1 {
		t.Errorf("a further timer changed things: adds = %d, disarms = %d", r.adds.Load(), r.disarms.Load())
	}
}

func TestSystemEndingFollowsTheShuttingDownMetric(t *testing.T) {
	orig := shuttingDownMetric
	t.Cleanup(func() { shuttingDownMetric = orig })
	shuttingDownMetric = func() int32 { return 1 }
	if !systemEnding() {
		t.Error("metric 1: systemEnding() = false")
	}
	shuttingDownMetric = func() int32 { return 0 }
	if systemEnding() {
		t.Error("metric 0: systemEnding() = true")
	}
	shuttingDownMetric = orig
	if systemEnding() {
		t.Error("real metric in a normal session: systemEnding() = true")
	}
}

func TestTrayEndSessionCallsOnEndSession(t *testing.T) {
	r := newRig(t)
	r.start()
	r.ending.Store(true)
	if got := sendMessage(r.hwnd, wmQueryEndSession, 0, 0); got != 1 {
		t.Errorf("WM_QUERYENDSESSION = %d, want 1", got)
	}
	sendMessage(r.hwnd, wmEndSession, 1, 0)
	if got := r.endSessions.Load(); got != 1 {
		t.Fatalf("WM_ENDSESSION wparam 1: OnEndSession calls = %d, want 1", got)
	}
	sendMessage(r.hwnd, wmEndSession, 0, 0)
	if got := r.endSessions.Load(); got != 1 {
		t.Errorf("WM_ENDSESSION wparam 0 called OnEndSession: %d", got)
	}
}

func TestTrayPostedEndSessionIsIgnored(t *testing.T) {
	r := newRig(t)
	r.start() // endingSession is faked false
	sendMessage(r.hwnd, wmEndSession, 1, 0)
	postMessage(r.hwnd, wmEndSession, 1, 0)
	r.canary()
	if got := r.endSessions.Load(); got != 0 {
		t.Errorf("OnEndSession calls = %d, want 0", got)
	}
}

func TestTrayIgnoresForeignClose(t *testing.T) {
	r := newRig(t)
	r.start()
	sendMessage(r.hwnd, wmClose, 0, 0)
	postMessage(r.hwnd, stopMsg, 0, 0)
	r.canary()
	if !isWindow(r.hwnd) {
		t.Error("window was destroyed")
	}
	if r.deletes.Load() != 0 {
		t.Error("icon was deleted")
	}
	if !r.notRunning() {
		t.Error("Run returned")
	}
}

func TestTrayIgnoresForeignDestroyAndQuit(t *testing.T) {
	r := newRig(t)
	r.start()
	sendMessage(r.hwnd, wmDestroy, 0, 0)
	tid, _, _ := procGetWindowThreadProcID.Call(uintptr(r.hwnd), 0)
	if tid == 0 {
		t.Fatal("no thread id")
	}
	if ok, _, err := procPostThreadMessage.Call(tid, wmQuit, 0, 0); ok == 0 {
		t.Fatalf("PostThreadMessage: %v", err)
	}
	r.canary()
	if !isWindow(r.hwnd) {
		t.Error("window was destroyed")
	}
	if r.deletes.Load() != 0 {
		t.Error("icon was deleted")
	}
	if !r.notRunning() {
		t.Fatal("Run returned")
	}
	r.cancel()
	select {
	case err := <-r.done:
		if err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
		r.done <- err
	case <-time.After(guard):
		t.Fatal("Run did not end after the context was cancelled")
	}
}

func TestTrayStopsWhenContextIsCancelled(t *testing.T) {
	r := newRig(t)
	r.start()
	r.cancel()
	select {
	case err := <-r.done:
		if err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
		r.done <- err
	case <-time.After(guard):
		t.Fatal("Run did not return")
	}
	if r.deletes.Load() != 1 {
		t.Errorf("icon deleted %d times, want 1", r.deletes.Load())
	}
	if isWindow(r.hwnd) {
		t.Error("window still exists")
	}
}

func TestTrayRejectsASecondConcurrentRun(t *testing.T) {
	r := newRig(t)
	r.start()
	err := Run(context.Background(), r.o)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second Run = %v, want an already running error", err)
	}
	if !r.notRunning() {
		t.Error("the first Run ended")
	}
}

// block returns the text of the braces that open at or after start.
func block(src string, start int) string {
	open := strings.Index(src[start:], "{")
	if open < 0 {
		return ""
	}
	open += start
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[open : i+1]
			}
		}
	}
	return src[open:]
}

func TestTrayLoopLeavesOnAGetMessageError(t *testing.T) {
	b, err := os.ReadFile("tray_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, "procGetMessage.Call(") {
		t.Fatal("no GetMessage call found")
	}
	i := strings.Index(src, "== -1 {")
	if i < 0 {
		t.Fatal("the GetMessage result is not compared with -1")
	}
	br := block(src, i)
	if !strings.Contains(br, "break") && !strings.Contains(br, "return") {
		t.Errorf("the -1 branch neither breaks nor returns:\n%s", br)
	}
	if strings.Contains(br, "continue") {
		t.Errorf("the -1 branch continues (busy spin):\n%s", br)
	}
}

func TestTrayNeverLoosensTheMessageFilter(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "ChangeWindowMessageFilter") {
			t.Errorf("%s mentions ChangeWindowMessageFilter", f)
		}
	}
}
