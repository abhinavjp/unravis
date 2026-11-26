//go:build windows

package tray

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/windows"
)

func TestBalloonConstantsAreTheShellsOwn(t *testing.T) {
	// NIN_BALLOONSHOW..NIN_BALLOONUSERCLICK are 0x402..0x405, NIN_KEYSELECT is 0x401. A wrong value here once made a hidden
	// balloon open the app.
	for name, c := range map[string][2]uint32{
		"NIN_SELECT": {ninSelect, 0x400}, "NIN_KEYSELECT": {ninKeySelect, 0x401}, "NIN_BALLOONSHOW": {ninBalloonShow, 0x402},
		"NIN_BALLOONHIDE": {ninBalloonHide, 0x403}, "NIN_BALLOONTIMEOUT": {ninBalloonTimeout, 0x404}, "NIN_BALLOONUSERCLICK": {ninBalloonUserClick, 0x405},
	} {
		if c[0] != c[1] {
			t.Errorf("%s = %#x, want %#x", name, c[0], c[1])
		}
	}
}

func TestBalloonHideDoesNotOpenTheApp(t *testing.T) {
	r := newRig(t)
	r.start()
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(ninBalloonHide, trayIconID))
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(ninBalloonShow, trayIconID))
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(ninBalloonTimeout, trayIconID))
	if r.opens.Load() != 0 {
		t.Errorf("a balloon event opened the app (%d)", r.opens.Load())
	}
}

func TestNotifyShowsTheNoticeAndAClickRunsItsAction(t *testing.T) {
	r := newRig(t)
	r.start()
	var clicks atomic.Int32
	if err := Notify(Notice{Title: "Unravis has been updated to 1.4.0", Text: "New in this version.", OnClick: func() { clicks.Add(1) }}); err != nil {
		t.Fatal(err)
	}
	r.canary()
	opensBefore := r.opens.Load() // the canary is one open
	got := r.shownNotices()
	if len(got) != 1 || got[0].Title != "Unravis has been updated to 1.4.0" || got[0].Text != "New in this version." {
		t.Fatalf("shown = %+v", got)
	}
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(ninBalloonUserClick, trayIconID))
	if clicks.Load() != 1 {
		t.Fatalf("clicks = %d, want 1", clicks.Load())
	}
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(ninBalloonUserClick, trayIconID))
	if clicks.Load() != 1 {
		t.Errorf("a second click ran the action again (%d)", clicks.Load())
	}
	if r.opens.Load() != opensBefore {
		t.Error("a notice click also opened the app by the tray-icon path")
	}
}

func TestNoticeClickIsForgottenWhenTheNoticeEnds(t *testing.T) {
	for _, ev := range []uint32{ninBalloonTimeout, ninBalloonHide} {
		t.Run(fmt.Sprintf("%#x", ev), func(t *testing.T) {
			r := newRig(t)
			r.start()
			var clicks atomic.Int32
			if err := Notify(Notice{Title: "t", OnClick: func() { clicks.Add(1) }}); err != nil {
				t.Fatal(err)
			}
			r.canary()
			sendMessage(r.hwnd, callbackMsg, 0, makeLong(ev, trayIconID))
			sendMessage(r.hwnd, callbackMsg, 0, makeLong(ninBalloonUserClick, trayIconID))
			if clicks.Load() != 0 {
				t.Errorf("event %#x then a click ran the action", ev)
			}
		})
	}
}

func TestANewNoticeReplacesTheClickOfTheOldOne(t *testing.T) {
	r := newRig(t)
	r.start()
	var a, b atomic.Int32
	_ = Notify(Notice{Title: "a", OnClick: func() { a.Add(1) }})
	r.canary()
	_ = Notify(Notice{Title: "b", OnClick: func() { b.Add(1) }})
	r.canary()
	sendMessage(r.hwnd, callbackMsg, 0, makeLong(ninBalloonUserClick, trayIconID))
	if a.Load() != 0 || b.Load() != 1 {
		t.Errorf("clicks a=%d b=%d, want 0 and 1", a.Load(), b.Load())
	}
}

func TestNotifyWithoutAnIconAndWithABadNotice(t *testing.T) {
	if err := Notify(Notice{Title: "x"}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Notify with no tray = %v, want ErrNotRunning", err)
	}
	r := newRig(t)
	r.start()
	if err := Notify(Notice{}); err == nil {
		t.Error("a notice without a title was accepted")
	}
}

func TestNoticeIsDroppedWhileTheIconIsNotShowing(t *testing.T) {
	r := newRig(t)
	r.addErrs = []error{errors.New("explorer is not ready")}
	r.start()
	_ = Notify(Notice{Title: "x"})
	r.canary()
	if n := len(r.shownNotices()); n != 0 {
		t.Errorf("a notice was shown with no icon (%d)", n)
	}
}

func TestStateTooltipAndDotAtStart(t *testing.T) {
	r := newRig(t)
	r.o.State = func() State { return State{Tooltip: "Unravis 1.4.0 · update ready", Attention: true} }
	r.start()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.addTips) != 1 || r.addTips[0] != "Unravis 1.4.0 · update ready" {
		t.Errorf("add tooltips = %q", r.addTips)
	}
	if len(r.addIcons) != 1 || r.addIcons[0] != dotSentinel {
		t.Errorf("add icons = %v, want the dot icon", r.addIcons)
	}
}

func TestPollChangesTheTooltipAndDotOnlyWhenTheyChange(t *testing.T) {
	r := newRig(t)
	var cur atomic.Pointer[State]
	set := func(s State) { cur.Store(&s) }
	set(State{Tooltip: "Unravis 1.4.0 · up to date"})
	r.o.State = func() State { return *cur.Load() }
	r.start()
	poll := func() { sendMessage(r.hwnd, wmTimer, pollTimerID, 0) }

	poll()
	if n := len(r.modifyCalls()); n != 0 {
		t.Fatalf("an unchanged state made %d modify calls", n)
	}
	set(State{Tooltip: "Unravis 1.4.0 · update ready", Attention: true})
	poll()
	poll()
	m := r.modifyCalls()
	if len(m) != 1 || m[0].tip != "Unravis 1.4.0 · update ready" || m[0].icon != dotSentinel {
		t.Fatalf("modify calls = %+v, want one with the dot", m)
	}
	set(State{Tooltip: "Unravis 1.4.0 · up to date"})
	poll()
	m = r.modifyCalls()
	if len(m) != 2 || m[1].icon == dotSentinel || m[1].icon == 0 {
		t.Fatalf("after the state cleared: %+v, want the plain icon", m)
	}
	if r.dotMade.Load() != 1 {
		t.Errorf("the dot icon was made %d times, want once", r.dotMade.Load())
	}
}

func TestPollRetriesAfterAFailedModify(t *testing.T) {
	r := newRig(t)
	var cur atomic.Pointer[State]
	cur.Store(&State{Tooltip: "a"})
	r.o.State = func() State { return *cur.Load() }
	r.start()
	cur.Store(&State{Tooltip: "b"})
	r.mu.Lock()
	r.modifyErr = errors.New("shell busy")
	r.mu.Unlock()
	sendMessage(r.hwnd, wmTimer, pollTimerID, 0)
	r.mu.Lock()
	r.modifyErr = nil
	r.mu.Unlock()
	sendMessage(r.hwnd, wmTimer, pollTimerID, 0)
	if n := len(r.modifyCalls()); n != 2 {
		t.Errorf("modify calls = %d, want 2 (a failure is tried again on the next poll)", n)
	}
	sendMessage(r.hwnd, wmTimer, pollTimerID, 0)
	if n := len(r.modifyCalls()); n != 2 {
		t.Errorf("modify calls = %d after success, want still 2", n)
	}
}

func TestDotIconIsFreedWhenTheTrayStops(t *testing.T) {
	r := newRig(t)
	r.o.State = func() State { return State{Tooltip: "x", Attention: true} }
	r.start()
	r.cancel()
	<-r.done
	r.done <- nil // the cleanup in start waits for a value
	if r.destroyed.Load() != 1 {
		t.Errorf("dot icon freed %d times, want 1", r.destroyed.Load())
	}
}

func TestUnknownTimerIdIsIgnored(t *testing.T) {
	r := newRig(t)
	called := atomic.Int32{}
	r.o.State = func() State { called.Add(1); return State{Tooltip: "x"} }
	r.start()
	before := called.Load()
	sendMessage(r.hwnd, wmTimer, 99, 0)
	if called.Load() != before {
		t.Error("a timer with a foreign id polled the state")
	}
}

var _ = windows.Handle(0)
