//go:build windows

package instance

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// hangGuard only stops a broken test from hanging; no test asserts a duration.
const hangGuard = 10 * time.Second

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(hangGuard):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// chanCallback returns a callback that sends on a buffered channel.
func chanCallback() (func(), <-chan struct{}) {
	ch := make(chan struct{}, 16)
	return func() { ch <- struct{}{} }, ch
}

func mustListen(t *testing.T, ctx context.Context, n Names, onOpen, onQuit func()) func() {
	t.Helper()
	stop, err := Listen(ctx, n, onOpen, onQuit)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(stop)
	return stop
}

func TestSignalOpenReachesTheListener(t *testing.T) {
	n := uniqueNames(t)
	onOpen, opened := chanCallback()
	onQuit, quit := chanCallback()
	mustListen(t, context.Background(), n, onOpen, onQuit)
	if err := Signal(n, Open); err != nil {
		t.Fatalf("Signal(Open): %v", err)
	}
	waitFor(t, opened, "the open callback")
	select {
	case <-quit:
		t.Fatal("the quit callback ran for an open signal")
	default:
	}
}

func TestSignalQuitReachesTheListener(t *testing.T) {
	n := uniqueNames(t)
	onOpen, opened := chanCallback()
	onQuit, quit := chanCallback()
	mustListen(t, context.Background(), n, onOpen, onQuit)
	if err := Signal(n, Quit); err != nil {
		t.Fatalf("Signal(Quit): %v", err)
	}
	waitFor(t, quit, "the quit callback")
	select {
	case <-opened:
		t.Fatal("the open callback ran for a quit signal")
	default:
	}
}

func TestSignalWithNoListenerIsErrNoInstance(t *testing.T) {
	n := uniqueNames(t)
	for _, k := range []Kind{Open, Quit} {
		if err := Signal(n, k); !errors.Is(err, ErrNoInstance) {
			t.Errorf("Signal(%v) with no listener: %v, want ErrNoInstance", k, err)
		}
	}
}

func TestListenStopsWhenContextIsCancelled(t *testing.T) {
	n := uniqueNames(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l, err := startListener(ctx, n, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitFor(t, l.done, "the listener goroutine to exit")
	stopped := make(chan struct{})
	go func() { l.stop(); l.stop(); close(stopped) }()
	waitFor(t, stopped, "stop after cancel")
	// The events are released, so the names are free again.
	stop, err := Listen(context.Background(), n, nil, nil)
	if err != nil {
		t.Fatalf("Listen after cancel: %v", err)
	}
	stop()
}

func TestStopTwiceIsSafeAndReleasesTheNames(t *testing.T) {
	n := uniqueNames(t)
	stop, err := Listen(context.Background(), n, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	stop()
	if err := Signal(n, Open); !errors.Is(err, ErrNoInstance) {
		t.Fatalf("Signal after stop: %v, want ErrNoInstance", err)
	}
}

func TestListenRefusesEventsThatAlreadyExist(t *testing.T) {
	n := uniqueNames(t)
	onOpen, opened := chanCallback()
	mustListen(t, context.Background(), n, onOpen, nil)
	stop2, err := Listen(context.Background(), n, nil, nil)
	if !errors.Is(err, ErrEventsTaken) {
		t.Fatalf("second Listen: %v, want ErrEventsTaken", err)
	}
	stop2() // must be callable
	// The first listener is unaffected.
	if err := Signal(n, Open); err != nil {
		t.Fatal(err)
	}
	waitFor(t, opened, "the first listener's callback")
}

func TestRepeatedOpenSignalsAreHandledOneAtATime(t *testing.T) {
	n := uniqueNames(t)
	var active, calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	second := make(chan struct{})
	onOpen := func() {
		if active.Add(1) > 1 {
			t.Error("two callbacks overlapped")
		}
		switch calls.Add(1) {
		case 1:
			close(started)
			<-release
		case 2:
			defer close(second)
		}
		active.Add(-1)
	}
	stop := mustListen(t, context.Background(), n, onOpen, nil)
	if err := Signal(n, Open); err != nil {
		t.Fatal(err)
	}
	waitFor(t, started, "the first callback")
	for i := 0; i < 5; i++ {
		if err := Signal(n, Open); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	waitFor(t, second, "the second callback")
	stop()
	if got := calls.Load(); got != 2 {
		t.Fatalf("callback ran %d times, want 2 (repeated signals merge)", got)
	}
}

func TestCallbackPanicIsRecovered(t *testing.T) {
	n := uniqueNames(t)
	onQuit, quit := chanCallback()
	mustListen(t, context.Background(), n, func() { panic("boom") }, onQuit)
	if err := Signal(n, Open); err != nil {
		t.Fatal(err)
	}
	if err := Signal(n, Quit); err != nil {
		t.Fatal(err)
	}
	waitFor(t, quit, "the quit callback after a panicking open callback")
}

func TestSignalUnknownKindIsRejected(t *testing.T) {
	n := uniqueNames(t)
	onOpen, opened := chanCallback()
	onQuit, quit := chanCallback()
	stop := mustListen(t, context.Background(), n, onOpen, onQuit)
	for _, k := range []Kind{0, -1, 3, 99} {
		err := Signal(n, k)
		if err == nil || errors.Is(err, ErrNoInstance) {
			t.Errorf("Signal(%d): %v, want a non-nil error that is not ErrNoInstance", int(k), err)
		}
	}
	stop()
	if len(opened) != 0 || len(quit) != 0 {
		t.Fatal("an unknown kind reached a callback")
	}
}

func TestListenAndSignalRejectNamesOutsideLocal(t *testing.T) {
	bad := Names{Open: `Global\x.Open`, Quit: `Global\x.Quit`}
	if _, err := Listen(context.Background(), bad, nil, nil); err == nil {
		t.Error("Listen accepted Global\\ names")
	}
	if err := Signal(bad, Open); err == nil || errors.Is(err, ErrNoInstance) {
		t.Errorf("Signal on a Global\\ name: %v, want a plain error", err)
	}
}

func TestListenAndSignalDetectEventsOwnedBySomeoneElse(t *testing.T) {
	n := uniqueNames(t)
	var opens, quits atomic.Int32
	stop := mustListen(t, context.Background(), n,
		func() { opens.Add(1) }, func() { quits.Add(1) })

	old := ownerCheck
	t.Cleanup(func() { ownerCheck = old })
	ownerCheck = func(windows.Handle) (bool, error) { return false, nil }

	stop2, err := Listen(context.Background(), n, nil, nil)
	if !errors.Is(err, ErrNameHeldByOther) {
		t.Fatalf("second Listen: %v, want ErrNameHeldByOther", err)
	}
	stop2()
	for _, k := range []Kind{Open, Quit} {
		if err := Signal(n, k); !errors.Is(err, ErrNameHeldByOther) {
			t.Fatalf("Signal(%v): %v, want ErrNameHeldByOther", k, err)
		}
	}
	// A failing owner check is refused the same way.
	ownerCheck = func(windows.Handle) (bool, error) { return true, errors.New("boom") }
	if err := Signal(n, Open); !errors.Is(err, ErrNameHeldByOther) {
		t.Fatalf("Signal with a failing check: %v, want ErrNameHeldByOther", err)
	}

	stop() // waits for the goroutine, so no callback can still be pending
	if o, q := opens.Load(), quits.Load(); o != 0 || q != 0 {
		t.Fatalf("callbacks ran for a refused signal: open=%d quit=%d", o, q)
	}
}

func TestSignalDoesNotKeepTheEventAlive(t *testing.T) {
	n := uniqueNames(t)
	onOpen, opened := chanCallback()
	stop, err := Listen(context.Background(), n, onOpen, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Signal(n, Open); err != nil {
		t.Fatal(err)
	}
	waitFor(t, opened, "the open callback")
	stop()
	stop2, err := Listen(context.Background(), n, nil, nil)
	if err != nil {
		t.Fatalf("Listen after the signal and stop: %v (the sender kept the event alive)", err)
	}
	stop2()
}

func TestEventsGrantWriteOnlyToTheUserSystemAndAdministrators(t *testing.T) {
	n := uniqueNames(t)
	mustListen(t, context.Background(), n, nil, nil)

	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	tu, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	allowed := []*windows.SID{tu.User.Sid, mustSID(t, "S-1-5-18"), mustSID(t, "S-1-5-32-544")}
	// The default descriptor Windows builds from an interactive token also names
	// that token's own logon-session SID (S-1-5-5-X-Y) with full access. Only
	// processes of this very sign-in session carry it, so it is the same user
	// and not another account. It is allowed here as the one extra entry; any
	// other SID (Everyone, Users, Authenticated Users, another session's logon
	// SID) still fails.
	groups, err := token.GetTokenGroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups.AllGroups() {
		if g.Attributes&windows.SE_GROUP_LOGON_ID == windows.SE_GROUP_LOGON_ID {
			allowed = append(allowed, g.Sid)
		}
	}

	const (
		genericWrite = 0x40000000
		genericAll   = 0x10000000
		writeMask    = windows.EVENT_MODIFY_STATE | genericWrite | genericAll | windows.WRITE_DAC | windows.WRITE_OWNER
	)
	for _, name := range []string{n.Open, n.Quit} {
		p, err := windows.UTF16PtrFromString(name)
		if err != nil {
			t.Fatal(err)
		}
		h, err := windows.OpenEvent(windows.READ_CONTROL|windows.SYNCHRONIZE, false, p)
		if err != nil {
			t.Fatalf("OpenEvent(%s): %v", name, err)
		}
		sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
		windows.CloseHandle(h)
		if err != nil {
			t.Fatalf("GetSecurityInfo(%s): %v", name, err)
		}
		dacl, _, err := sd.DACL()
		if err != nil {
			t.Fatalf("DACL(%s): %v", name, err)
		}
		if dacl == nil {
			t.Fatalf("%s has a NULL DACL, which grants everyone full access", name)
		}
		for i := uint32(0); i < uint32(dacl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(dacl, i, &ace); err != nil {
				t.Fatalf("GetAce(%d): %v", i, err)
			}
			if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || uint32(ace.Mask)&writeMask == 0 {
				continue
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			ok := false
			for _, a := range allowed {
				if windows.EqualSid(sid, a) {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%s: write access (mask %#x) is granted to %s, only the user, SYSTEM and Administrators may have it",
					name, uint32(ace.Mask), sid.String())
			}
		}
	}
}
