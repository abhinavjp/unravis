package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/tray"
)

var trayNow = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func inst(state, v string) *server.InstallInfo { return &server.InstallInfo{State: state, Version: v} }

func TestViewUpdateEachState(t *testing.T) {
	checked := trayNow.Add(-3 * time.Minute).Format(time.RFC3339)
	cases := []struct {
		name     string
		st       server.UpdateStatus
		checking bool
		line     string
		act      trayAct
		dot      bool
		tip      string
	}{
		{"checking", server.UpdateStatus{Status: "latest", Current: "1.0.0"}, true, "Checking for updates...", actNone, false, "Unravis 1.0.0"},
		{"launched", server.UpdateStatus{Status: "available", Current: "1.0.0", Install: inst("launched", "1.1.0")}, false, "Updating to 1.1.0...", actNone, false, "Unravis is updating to 1.1.0"},
		{"downloading", server.UpdateStatus{Status: "available", Current: "1.0.0", Install: inst("downloading", "1.1.0")}, false, "Downloading 1.1.0...", actNone, false, "Unravis 1.0.0 · downloading 1.1.0"},
		{"result failed", server.UpdateStatus{Status: "available", Current: "1.0.0", Result: &server.UpdateNotice{Outcome: "rolled-back", From: "1.0.0", To: "1.1.0"}}, false, "Update failed. Still on 1.0.0: see Updates", actUpdates, true, "Unravis 1.0.0 · the last update did not work"},
		{"install failed", server.UpdateStatus{Status: "available", Current: "1.0.0", Install: inst("failed", "1.1.0")}, false, "Update failed. Still on 1.0.0: see Updates", actUpdates, true, "Unravis 1.0.0 · the last update did not work"},
		{"ready", server.UpdateStatus{Status: "available", Current: "1.0.0", CanInstall: true, Install: inst("ready", "1.1.0")}, false, "Update 1.1.0 is ready: Restart now", actInstall, true, "Unravis 1.0.0 · update ready, restart to finish"},
		{"ready but cannot install here", server.UpdateStatus{Status: "available", Current: "1.0.0", Latest: "1.1.0", Install: inst("ready", "1.1.0")}, false, "Update 1.1.0 is available: see Updates", actUpdates, false, "Unravis 1.0.0 · update 1.1.0 available"},
		{"available", server.UpdateStatus{Status: "available", Current: "1.0.0", Latest: "1.1.0"}, false, "Update 1.1.0 is available: see Updates", actUpdates, false, "Unravis 1.0.0 · update 1.1.0 available"},
		{"latest", server.UpdateStatus{Status: "latest", Current: "1.0.0", CheckedAt: checked}, false, "Up to date, checked 3 minutes ago", actNone, false, "Unravis 1.0.0 · up to date"},
		{"latest no time", server.UpdateStatus{Status: "latest", Current: "1.0.0"}, false, "Up to date", actNone, false, "Unravis 1.0.0 · up to date"},
		{"unreachable", server.UpdateStatus{Status: "unreachable", Current: "1.0.0"}, false, "Could not check for updates", actNone, false, "Unravis 1.0.0 · could not check for updates"},
		{"unverified", server.UpdateStatus{Status: "unverified", Current: "1.0.0"}, false, "Update check failed its safety check: see Updates", actUpdates, false, "Unravis 1.0.0"},
		{"dev", server.UpdateStatus{Status: "dev", Current: "dev"}, false, "Development build: no update checks", actNone, false, "Unravis dev"},
		{"none", server.UpdateStatus{Status: "none", Current: "1.0.0"}, false, "No update check has run yet", actNone, false, "Unravis 1.0.0"},
		{"done result is not a failure", server.UpdateStatus{Status: "latest", Current: "1.1.0", Result: &server.UpdateNotice{Outcome: "done", From: "1.0.0", To: "1.1.0"}}, false, "Up to date", actNone, false, "Unravis 1.1.0 · up to date"},
	}
	for _, tc := range cases {
		v := viewUpdate(tc.st, tc.checking, trayNow)
		if v.Line != tc.line || v.Act != tc.act || v.Attention != tc.dot || v.Tip != tc.tip {
			t.Errorf("%s: got %+v, want line=%q act=%d dot=%v tip=%q", tc.name, v, tc.line, tc.act, tc.dot, tc.tip)
		}
	}
}

func TestViewUpdateRespectsTheTrayLimits(t *testing.T) {
	long := strings.Repeat("x", 200)
	st := server.UpdateStatus{Status: "available", Current: long, Latest: long, Channel: long, Install: inst("downloading", long)}
	for _, s := range []server.UpdateStatus{st, {Status: "available", Current: long, Latest: long}} {
		v := viewUpdate(s, false, trayNow)
		if n := len([]rune(v.Line)); n > tray.MaxTextRunes {
			t.Errorf("line is %d characters", n)
		}
		if n := len([]rune(v.Tip)); n > tray.MaxTooltipRunes {
			t.Errorf("tooltip is %d characters", n)
		}
	}
	if n := len([]rune(versionLine(st))); n > tray.MaxTextRunes {
		t.Errorf("version line is %d characters", n)
	}
}

func TestVersionLine(t *testing.T) {
	if got := versionLine(server.UpdateStatus{Current: "1.2.3", Channel: "rc"}); got != "Unravis 1.2.3 (rc channel)" {
		t.Fatal(got)
	}
	if got := versionLine(server.UpdateStatus{Current: "1.2.3"}); got != "Unravis 1.2.3" {
		t.Fatal(got)
	}
}

func TestAgoWordsAndClip(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "just now",
		time.Minute:      "1 minute ago",
		59 * time.Minute: "59 minutes ago",
		time.Hour:        "1 hour ago",
		47 * time.Hour:   "47 hours ago",
		72 * time.Hour:   "3 days ago",
	} {
		if got := agoWords(trayNow, trayNow.Add(-d)); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
	if clipRunes("héllo wörld", 8) != "héllo..." || clipRunes("short", 8) != "short" {
		t.Fatal("clipRunes")
	}
}

func TestPickNotice(t *testing.T) {
	done := server.UpdateStatus{Current: "1.1.0", Result: &server.UpdateNotice{Outcome: "done", From: "1.0.0", To: "1.1.0"}}
	n, ok := pickNotice(done, false)
	if !ok || n.key != "done:1.0.0>1.1.0" || n.title != "Unravis has been updated to 1.1.0" || n.text != "Click to see what is new." || n.to != "whatsnew" {
		t.Fatalf("done: %+v %v", n, ok)
	}
	// A "done" for a version that is not the one running is stale: no notice.
	stale := done
	stale.Current = "1.0.5"
	if _, ok := pickNotice(stale, false); ok {
		t.Fatal("stale done result produced a notice")
	}
	failed := server.UpdateStatus{Current: "1.0.0", Result: &server.UpdateNotice{Outcome: "rolled-back", From: "1.0.0", To: "1.1.0"}}
	n, ok = pickNotice(failed, false)
	if !ok || n.title != "Unravis could not update to 1.1.0" || !strings.Contains(n.text, "still on 1.0.0") || n.to != "updates" {
		t.Fatalf("failed: %+v", n)
	}
	noTo := server.UpdateStatus{Current: "1.0.0", Result: &server.UpdateNotice{Outcome: "failed"}}
	if n, _ := pickNotice(noTo, false); n.title != "Unravis could not update" {
		t.Fatalf("no target: %+v", n)
	}
	ready := server.UpdateStatus{Current: "1.0.0", CanInstall: true, Install: inst("ready", "1.1.0")}
	n, ok = pickNotice(ready, false)
	if !ok || n.key != "ready:1.1.0" || n.to != "updates" {
		t.Fatalf("ready: %+v", n)
	}
	auto := ready
	auto.ApplyAuto = true
	if _, ok := pickNotice(auto, false); ok {
		t.Fatal("ready notice shown although the update applies by itself")
	}
	noInstall := ready
	noInstall.CanInstall = false
	if _, ok := pickNotice(noInstall, false); ok {
		t.Fatal("ready notice shown although this copy cannot install")
	}
	launched := server.UpdateStatus{Current: "1.0.0", Install: inst("launched", "1.1.0")}
	n, ok = pickNotice(launched, false)
	if !ok || n.key != "launching:1.1.0" || n.to != "" {
		t.Fatalf("launched: %+v", n)
	}
	if _, ok := pickNotice(launched, true); ok {
		t.Fatal("the person pressed Restart now, no pop-up wanted")
	}
	if _, ok := pickNotice(server.UpdateStatus{Status: "latest", Current: "1.0.0"}, false); ok {
		t.Fatal("a check that found nothing produced a notice")
	}
	// A failure outranks a ready update.
	both := ready
	both.Result = failed.Result
	if n, _ := pickNotice(both, false); !strings.HasPrefix(n.key, "rolled-back:") {
		t.Fatalf("both: %+v", n)
	}
}

func TestToldStoreRemembersAcrossRestartsAndKeepsTheLastFew(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "tray-notices.json")
	s := openToldStore(p)
	if s.has("a") {
		t.Fatal("fresh store has a key")
	}
	s.add("a")
	if !openToldStore(p).has("a") {
		t.Fatal("not remembered after reopen")
	}
	for i := 0; i < 30; i++ {
		s.add(string(rune('A' + i)))
	}
	r := openToldStore(p)
	if len(r.keys) != toldKeep || r.has("a") || !r.has(string(rune('A'+29))) {
		t.Fatalf("keys = %v", r.keys)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}
}

func TestToldStoreToleratesBadFilesAndNoPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := openToldStore(p)
	s.add("x")
	if !s.has("x") || !openToldStore(p).has("x") {
		t.Fatal("did not recover from a bad file")
	}
	m := openToldStore("")
	m.add("y")
	if !m.has("y") {
		t.Fatal("memory-only store lost a key")
	}
}

type noticeRig struct {
	n      *noticer
	mu     sync.Mutex
	shown  []tray.Notice
	err    error
	opened []string
	logs   []string
}

func newNoticeRig(t *testing.T) *noticeRig {
	r := &noticeRig{}
	r.n = &noticer{
		told: openToldStore(filepath.Join(t.TempDir(), "told.json")),
		notify: func(n tray.Notice) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.err != nil {
				return r.err
			}
			r.shown = append(r.shown, n)
			return nil
		},
		openTo:   func(to string) { r.opened = append(r.opened, to) },
		byPerson: func() bool { return false },
		logf:     func(f string, a ...any) { r.logs = append(r.logs, f) },
	}
	return r
}

var doneStatus = server.UpdateStatus{Current: "1.1.0", Result: &server.UpdateNotice{Outcome: "done", From: "1.0.0", To: "1.1.0"}}

func TestNoticerShowsEachNoticeOnceAndClickOpensItsScreen(t *testing.T) {
	r := newNoticeRig(t)
	r.n.onStatus(doneStatus)
	r.n.onStatus(doneStatus)
	if len(r.shown) != 1 {
		t.Fatalf("shown %d times", len(r.shown))
	}
	r.shown[0].OnClick()
	if len(r.opened) != 1 || r.opened[0] != "whatsnew" {
		t.Fatalf("opened = %v", r.opened)
	}
	// A restart (a new noticer on the same file) does not repeat it.
	again := &noticer{told: openToldStore(r.n.told.path), notify: r.n.notify}
	again.onStatus(doneStatus)
	if len(r.shown) != 1 {
		t.Fatal("repeated after a restart")
	}
}

func TestNoticerRetriesWhenTheIconIsNotUpYet(t *testing.T) {
	r := newNoticeRig(t)
	r.err = tray.ErrNotRunning
	r.n.onStatus(doneStatus)
	if len(r.shown) != 0 {
		t.Fatal("shown with no icon")
	}
	r.err = nil
	r.n.onStatus(doneStatus)
	if len(r.shown) != 1 {
		t.Fatalf("not retried: shown %d", len(r.shown))
	}
}

func TestNoticerGivesUpOnARefusedNotice(t *testing.T) {
	r := newNoticeRig(t)
	r.err = errors.New("shell said no")
	r.n.onStatus(doneStatus)
	r.err = nil
	r.n.onStatus(doneStatus)
	if len(r.shown) != 0 || len(r.logs) != 1 {
		t.Fatalf("shown=%d logs=%d", len(r.shown), len(r.logs))
	}
}

func TestNoticerWithNoClickTargetHasNoClickAction(t *testing.T) {
	r := newNoticeRig(t)
	r.n.onStatus(server.UpdateStatus{Current: "1.0.0", Install: inst("launched", "1.1.0")})
	if len(r.shown) != 1 || r.shown[0].OnClick != nil {
		t.Fatalf("shown = %+v", r.shown)
	}
}

func TestNoticerKeepsNoticesInsideTheBalloonLimits(t *testing.T) {
	r := newNoticeRig(t)
	long := strings.Repeat("v", 300)
	r.n.onStatus(server.UpdateStatus{Current: long, Result: &server.UpdateNotice{Outcome: "failed", From: long, To: long}})
	if len(r.shown) != 1 {
		t.Fatal("not shown")
	}
	if err := tray.ValidateNotice(r.shown[0]); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateCacheCheckRunsOneAtATimeAndRefreshes(t *testing.T) {
	var reads atomic.Int32
	c := newUpdateCache(func() server.UpdateStatus {
		reads.Add(1)
		return server.UpdateStatus{Status: "latest", Current: "1.0.0"}
	})
	if reads.Load() != 1 {
		t.Fatal("no first read")
	}
	started, release := make(chan struct{}), make(chan struct{})
	var runs atomic.Int32
	run := func(context.Context) server.UpdateStatus {
		runs.Add(1)
		close(started)
		<-release
		return server.UpdateStatus{}
	}
	done := make(chan struct{})
	go func() { c.check(run); close(done) }()
	<-started
	if !c.checking.Load() {
		t.Fatal("not marked as checking")
	}
	c.check(run) // a second press while one runs does nothing
	close(release)
	<-done
	if runs.Load() != 1 || c.checking.Load() {
		t.Fatalf("runs=%d checking=%v", runs.Load(), c.checking.Load())
	}
	if reads.Load() != 2 {
		t.Fatalf("status not re-read after the check: %d reads", reads.Load())
	}
}

func TestUpdateCacheRunHandsOverStatusUntilCancelled(t *testing.T) {
	var n atomic.Int32
	c := newUpdateCache(func() server.UpdateStatus {
		return server.UpdateStatus{Current: string(rune('a' + n.Add(1)))}
	})
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan string, 8)
	done := make(chan struct{})
	go func() {
		c.run(ctx, 5*time.Millisecond, func(st server.UpdateStatus) { got <- st.Current })
		close(done)
	}()
	first, second := <-got, <-got
	if first == second {
		t.Fatalf("status did not refresh: %q %q", first, second)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not stop")
	}
}

func TestWireTrayUpdatesWithNoUpdaterChangesNothing(t *testing.T) {
	d := trayDeps{}
	stop := wireTrayUpdates(context.Background(), &d, nil, nil, nil)
	stop()
	if d.update != nil || d.checkUpdates != nil || d.installUpdate != nil {
		t.Fatal("wired something without an updater")
	}
}
