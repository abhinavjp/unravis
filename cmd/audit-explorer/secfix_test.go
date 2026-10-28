package main

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

// L2: the session token is never in a startup log line or in the link.

func TestStartupLinesNeverContainTheToken(t *testing.T) {
	token := strings.Repeat("T", 43)
	code, err := newLaunchCode()
	if err != nil {
		t.Fatal(err)
	}
	if code == token || len(code) < 32 {
		t.Fatalf("launch code %q is not a separate random value", code)
	}
	for _, opened := range []bool{true, false} {
		for _, demo := range []bool{true, false} {
			lines := startupLines("http://127.0.0.1:8123", code, launchCodeLifeManual, demo, opened)
			all := strings.Join(lines, "\n")
			if strings.Contains(all, token) || strings.Contains(all, "#t=") {
				t.Errorf("opened=%v demo=%v: token material in log lines: %q", opened, demo, all)
			}
			if opened && strings.Contains(all, code) {
				t.Errorf("a link is logged although the browser was opened for the user: %q", all)
			}
			if !opened && !strings.Contains(all, launchLink("http://127.0.0.1:8123", code)) {
				t.Errorf("no launch link when the user must open it by hand: %q", all)
			}
		}
	}
}

func TestLaunchLinkIsAPathOnTheLocalServerWithoutAFragment(t *testing.T) {
	link := launchLink("http://127.0.0.1:8123", "abc")
	if link != "http://127.0.0.1:8123/launch?c=abc" {
		t.Fatalf("link = %q", link)
	}
}

func TestLaunchCodesAreFreshAndBrowserCodesAreShortLived(t *testing.T) {
	a, _ := newLaunchCode()
	b, _ := newLaunchCode()
	if a == b {
		t.Fatal("two launch codes are equal")
	}
	if launchCodeLifeBrowser >= launchCodeLifeManual || launchCodeLifeBrowser > 5*time.Minute {
		t.Fatalf("lifetimes: browser %v, manual %v", launchCodeLifeBrowser, launchCodeLifeManual)
	}
}

// D-C-18: a busy port is explained in plain words, with the next step, and without the raw bind text.

func TestBusyPortGivesAPlainMessageWithTheNextStep(t *testing.T) {
	taken, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	port := taken.Addr().(*net.TCPAddr).Port

	l, err := listenLoopback(port)
	if err == nil {
		l.Close()
		t.Fatal("listening on a port that is already taken did not fail")
	}
	want := fmt.Sprintf("port %d on 127.0.0.1 is already in use. Another Unravis may already be running; close it, or start this one with a different --port (--port 0 picks a free one)", port)
	if err.Error() != want {
		t.Fatalf("message = %q\nwant      %q", err.Error(), want)
	}
	for _, raw := range []string{"bind", "socket address", "listen tcp4", "could not listen"} {
		if strings.Contains(err.Error(), raw) {
			t.Errorf("the raw bind text %q is still in the message: %q", raw, err.Error())
		}
	}
}

func TestListenLoopbackWithPortZeroWorksAndOtherErrorsStayRaw(t *testing.T) {
	l, err := listenLoopback(0)
	if err != nil {
		t.Fatalf("port 0 must always listen: %v", err)
	}
	if l.Addr().(*net.TCPAddr).Port == 0 {
		t.Error("port 0 did not pick a port")
	}
	l.Close()

	// A listen error that is not "in use" keeps the raw error behind the old lead, so nothing is hidden.
	if isAddressInUse(errors.New("listen tcp4 127.0.0.1:80: permission denied")) {
		t.Error("permission denied was taken for a busy port")
	}
	// The in-use signals: the Windows and the POSIX code, and the text of a wrapped error.
	for name, err := range map[string]error{
		"windows code": &net.OpError{Op: "listen", Err: syscall.Errno(10048)},
		"posix code":   &net.OpError{Op: "listen", Err: syscall.EADDRINUSE},
		"text only":    errors.New("listen tcp4 127.0.0.1:1: bind: Address already in use"),
	} {
		if !isAddressInUse(err) {
			t.Errorf("%s was not recognised as a busy port", name)
		}
	}
}
