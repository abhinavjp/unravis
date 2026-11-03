package server

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// WP-72d: Rearm gives a running server a fresh one-time launch code.

func TestRearmReplacesTheCodeAndTheOldOneStopsWorking(t *testing.T) {
	oldCode := strings.Repeat("a", 32)
	newCode := strings.Repeat("b", 32)
	h := New(&fakeRepository{}, testAssets(), testToken, false, WithLaunchCode(oldCode, time.Now().Add(time.Minute)))
	if !Rearm(h, newCode, time.Now().Add(time.Minute)) {
		t.Fatal("Rearm refused a good code")
	}
	wantGonePage(t, "old code", launchRequest(h, "?c="+oldCode), oldCode, newCode)
	res := launchRequest(h, "?c="+newCode)
	if res.Code != http.StatusFound {
		t.Fatalf("new code = %d, want 302", res.Code)
	}
	if loc := res.Header().Get("Location"); loc != "/#t="+testToken {
		t.Fatalf("Location = %q", loc)
	}
}

func TestRearmedCodeWorksOnceThenIsGone(t *testing.T) {
	first := strings.Repeat("c", 32)
	second := strings.Repeat("d", 32)
	h := New(&fakeRepository{}, testAssets(), testToken, false, WithLaunchCode(first, time.Now().Add(time.Minute)))
	if res := launchRequest(h, "?c="+first); res.Code != http.StatusFound {
		t.Fatalf("first code = %d, want 302", res.Code)
	}
	wantGonePage(t, "spent first code", launchRequest(h, "?c="+first), first, second)
	if !Rearm(h, second, time.Now().Add(time.Minute)) {
		t.Fatal("Rearm refused a good code")
	}
	if res := launchRequest(h, "?c="+second); res.Code != http.StatusFound {
		t.Fatalf("rearmed code = %d, want 302", res.Code)
	}
	wantGonePage(t, "second use", launchRequest(h, "?c="+second), first, second)
}

func TestRearmWorksOnAServerStartedWithoutALaunchCode(t *testing.T) {
	code := strings.Repeat("e", 32)
	h := New(&fakeRepository{}, testAssets(), testToken, false)
	wantGonePage(t, "before rearm", launchRequest(h, "?c="+code), code)
	if !Rearm(h, code, time.Now().Add(time.Minute)) {
		t.Fatal("Rearm refused a good code")
	}
	if res := launchRequest(h, "?c="+code); res.Code != http.StatusFound {
		t.Fatalf("rearmed code = %d, want 302", res.Code)
	}
}

func TestRearmRejectsAHandlerThatIsNotTheServer(t *testing.T) {
	if Rearm(http.NewServeMux(), strings.Repeat("f", 32), time.Now().Add(time.Minute)) {
		t.Fatal("Rearm accepted a handler that is not the server")
	}
	var nilHandler *handler
	if Rearm(nilHandler, strings.Repeat("f", 32), time.Now().Add(time.Minute)) {
		t.Fatal("Rearm accepted a nil server handler")
	}
}

func TestRearmRejectsEmptyShortAndLongCodes(t *testing.T) {
	current := strings.Repeat("g", 32)
	h := New(&fakeRepository{}, testAssets(), testToken, false, WithLaunchCode(current, time.Now().Add(time.Minute)))
	for _, n := range []int{0, 15, 257} {
		if Rearm(h, strings.Repeat("h", n), time.Now().Add(time.Minute)) {
			t.Errorf("Rearm accepted a code of %d characters", n)
		}
	}
	if res := launchRequest(h, "?c="+current); res.Code != http.StatusFound {
		t.Fatalf("current code after rejected rearms = %d, want 302", res.Code)
	}
}

func TestRearmWithPastExpiryGivesAGonePage(t *testing.T) {
	code := strings.Repeat("i", 32)
	h := New(&fakeRepository{}, testAssets(), testToken, false)
	if !Rearm(h, code, time.Now().Add(-time.Second)) {
		t.Fatal("Rearm refused a good code")
	}
	wantGonePage(t, "expired", launchRequest(h, "?c="+code), code)
}
