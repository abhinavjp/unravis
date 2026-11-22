package releasedash

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func liveDir(t *testing.T, started time.Time, audit string) string {
	t.Helper()
	dir := t.TempDir()
	li := `{"id":"abc","pid":4242,"requester":"SECRET-USER@SECRET-PC","version":"","stage":"Starting","started":"` + started.UTC().Format(time.RFC3339Nano) + `","nonce":"SECRET-NONCE"}`
	if err := os.WriteFile(filepath.Join(dir, "lock-info.json"), []byte(li), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audit.jsonl"), []byte(audit), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func ev(at time.Time, rest string) string {
	return `{"ts":"` + at.UTC().Format(time.RFC3339Nano) + `",` + rest + "}\n"
}

func withAlive(t *testing.T, alive bool) {
	old := processAlive
	processAlive = func(int, time.Time) bool { return alive }
	t.Cleanup(func() { processAlive = old })
}

func TestLiveWaitingForCIBeforeAnyVersion(t *testing.T) {
	withAlive(t, true)
	now := time.Now()
	start := now.Add(-3 * time.Minute)
	old := ev(start.Add(-48*time.Hour), `"event":"done","version":"0.1.0-rc.57"`) // an older release must not count
	dir := liveDir(t, start, old+ev(start, `"event":"lock-acquired"`))
	l := ReadLocal(dir)
	if l.Live == nil || l.Live.Version != "" || l.Live.Step != "Preflight" || l.Live.Gone || !strings.Contains(l.Live.Label, "CI") {
		t.Fatalf("got %+v", l.Live)
	}
	s := Snapshot{Run: l.Run, Live: l.Live}
	b, _ := json.Marshal(s)
	for _, bad := range []string{"SECRET", "4242", "nonce", "abc"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("snapshot leaks %q: %s", bad, b)
		}
	}
}

func TestLiveFollowsAuditSteps(t *testing.T) {
	withAlive(t, true)
	start := time.Now().Add(-10 * time.Minute)
	steps := []struct{ line, want string }{
		{`"event":"pinned"`, "Preflight"},
		{`"event":"run-start","version":"0.1.0-rc.58","from":"Preflight"`, "Preflight"},
		{`"event":"preflight-ok","version":"0.1.0-rc.58"`, "Tag"},
		{`"event":"tagged","version":"0.1.0-rc.58"`, "Build"},
		{`"event":"built","version":"0.1.0-rc.58"`, "Sign"},
		{`"event":"signed","version":"0.1.0-rc.58"`, "Publish"},
		{`"event":"published","version":"0.1.0-rc.58"`, "Feed"},
		{`"event":"feed-updated","version":"0.1.0-rc.58"`, "Verify"},
	}
	audit := ""
	for i, s := range steps {
		audit += ev(start.Add(time.Duration(i+1)*time.Second*10), s.line)
		l := ReadLocal(liveDir(t, start, audit))
		if l.Live == nil || l.Live.Step != s.want {
			t.Fatalf("after %s: got %+v want %s", s.line, l.Live, s.want)
		}
		if i >= 1 && l.Live.Version != "0.1.0-rc.58" {
			t.Fatalf("version missing: %+v", l.Live)
		}
		rels := merge(nil, nil, l)
		if i >= 1 {
			if len(rels) != 1 {
				t.Fatalf("want one row, got %+v", rels)
			}
			run := ""
			for _, st := range rels[0].Steps {
				if st.State == "running" {
					run = st.Name
				}
			}
			if run != s.want {
				t.Fatalf("running step %q want %q: %+v", run, s.want, rels[0].Steps)
			}
		}
	}
	// done: the process ended well, so nothing is live
	audit += ev(start.Add(2*time.Minute), `"event":"verified","version":"0.1.0-rc.58"`) + ev(start.Add(2*time.Minute), `"event":"done","version":"0.1.0-rc.58"`)
	if l := ReadLocal(liveDir(t, start, audit)); l.Live != nil {
		t.Fatalf("finished release still live: %+v", l.Live)
	}
}

func TestLiveResumeStartsAtItsStep(t *testing.T) {
	withAlive(t, true)
	start := time.Now().Add(-time.Minute)
	l := ReadLocal(liveDir(t, start, ev(start.Add(time.Second), `"event":"run-start","version":"0.1.0-rc.58","from":"Wait"`)))
	if l.Live == nil || l.Live.Step != "Build" {
		t.Fatalf("got %+v", l.Live)
	}
}

func TestLiveStoppedAndDeadAndStale(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	stopped := ev(start.Add(time.Second), `"event":"run-start","version":"0.1.0-rc.58","from":"Preflight"`) + ev(start.Add(5*time.Second), `"event":"stopped","version":"0.1.0-rc.58","stage":"Preflight","reason":"CI red"`)
	withAlive(t, true)
	if l := ReadLocal(liveDir(t, start, stopped)); l.Live != nil {
		t.Fatalf("stopped release still live: %+v", l.Live)
	}
	// the process died with no closing line: shown as ended, never as running
	withAlive(t, false)
	l := ReadLocal(liveDir(t, start, ev(start.Add(time.Second), `"event":"run-start","version":"0.1.0-rc.58","from":"Preflight"`)))
	if l.Live == nil || !l.Live.Gone || !strings.Contains(l.Live.Label, "ended without finishing") {
		t.Fatalf("got %+v", l.Live)
	}
	rels := merge(nil, nil, l)
	for _, st := range rels[0].Steps {
		if st.State == "running" {
			t.Fatalf("dead process shown running: %+v", rels[0].Steps)
		}
	}
	// a lock note from a day ago is leftover, not a release
	if l := ReadLocal(liveDir(t, time.Now().Add(-30*time.Hour), "")); l.Live != nil {
		t.Fatalf("stale note shown: %+v", l.Live)
	}
	// no lock note: nothing live
	dir := writeLocal(t)
	if l := ReadLocal(dir); l.Live != nil {
		t.Fatalf("no lock note but live: %+v", l.Live)
	}
	// garbage lock note: nothing live, no panic
	os.WriteFile(filepath.Join(dir, "lock-info.json"), []byte("{not json"), 0o600)
	if l := ReadLocal(dir); l.Live != nil {
		t.Fatalf("garbage note shown: %+v", l.Live)
	}
}

func TestSnapshotCarriesLive(t *testing.T) {
	withAlive(t, true)
	start := time.Now().Add(-time.Minute)
	d := New(liveDir(t, start, ev(start.Add(time.Second), `"event":"run-start","version":"0.1.0-rc.58","from":"Preflight"`)), "", nil)
	d.GH = &GH{}
	d.FeedGap = time.Hour
	d.feedAt = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s := d.Snapshot(ctx, false)
	if s.Live == nil || s.Live.Version != "0.1.0-rc.58" {
		t.Fatalf("got %+v", s.Live)
	}
}

func TestPidAliveSelf(t *testing.T) {
	if !pidAlive(os.Getpid(), time.Now()) {
		t.Fatal("own process must be alive")
	}
	if pidAlive(0, time.Now()) || pidAlive(-1, time.Now()) {
		t.Fatal("bad pid must not be alive")
	}
}

func TestPidAliveReusedPidIsNotAlive(t *testing.T) {
	if runtimeIsWindows() && pidAlive(os.Getpid(), time.Now().Add(-time.Hour)) {
		t.Fatal("a process newer than the lock note is a reused pid")
	}
}

func runtimeIsWindows() bool { return os.PathSeparator == 92 }
