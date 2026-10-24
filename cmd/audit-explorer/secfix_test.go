package main

import (
	"strings"
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
