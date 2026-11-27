package cicli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/activitylog"
	"api-audit-explorer/internal/releasehold"
)

// fixClock sets the clock of the hold commands for one test.
func fixClock(t *testing.T, at time.Time) {
	t.Helper()
	old := holdNow
	holdNow = func() time.Time { return at }
	t.Cleanup(func() { holdNow = old })
}

var holdT0 = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

func addHold(t *testing.T, dir string, extra ...string) releasehold.Hold {
	t.Helper()
	args := append([]string{"hold", "add", "--state-dir", dir, "--json"}, extra...)
	code, out, errOut := cli(t, []string{"USERNAME=pat"}, args...)
	if code != exitOK {
		t.Fatalf("hold add %v: exit %d, %s", extra, code, errOut)
	}
	var h holdJSON
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		t.Fatalf("hold add --json: %v\n%s", err, out)
	}
	return h.Hold
}

func TestHoldCheckExitsHeldWithReasonOwnerAndEnd(t *testing.T) {
	fixClock(t, holdT0)
	dir := t.TempDir()
	if code, out, _ := cli(t, nil, "hold", "check", "--project", "alpha", "--state-dir", dir); code != exitOK || !strings.Contains(out, "no hold is active") {
		t.Fatalf("empty store: exit %d, %q", code, out)
	}
	h := addHold(t, dir, "--project", "alpha", "--until", "2h", "--reason", "quarter close audit")
	if h.Owner != "pat" || !h.End.Equal(holdT0.Add(2*time.Hour)) {
		t.Fatalf("hold = %+v", h)
	}
	code, out, _ := cli(t, nil, "hold", "check", "--project", "alpha", "--state-dir", dir)
	if code != exitHeld {
		t.Fatalf("held: exit %d, want %d", code, exitHeld)
	}
	for _, want := range []string{"Promote is held", "quarter close audit", "owner pat", "until 2026-11-27", h.ID} {
		if !strings.Contains(out, want) {
			t.Errorf("check output lacks %q:\n%s", want, out)
		}
	}
	if code, _, _ := cli(t, nil, "hold", "check", "--project", "beta", "--state-dir", dir); code != exitOK {
		t.Fatalf("another project: exit %d, want 0", code)
	}
	if code, _, _ := cli(t, nil, "hold", "check", "--state-dir", dir); code != exitHeld {
		t.Fatalf("any project: exit %d, want %d", code, exitHeld)
	}
	fixClock(t, holdT0.Add(2*time.Hour))
	if code, _, _ := cli(t, nil, "hold", "check", "--project", "alpha", "--state-dir", dir); code != exitOK {
		t.Fatalf("after the end: exit %d, want 0", code)
	}
}

func TestHoldAllCoversEveryProject(t *testing.T) {
	fixClock(t, holdT0)
	dir := t.TempDir()
	addHold(t, dir, "--all", "--until", "30m", "--reason", "holiday")
	code, out, _ := cli(t, nil, "hold", "check", "--project", "beta", "--state-dir", dir, "--json")
	var res holdCheckJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil || code != exitHeld || !res.Held || len(res.Holds) != 1 || res.Holds[0].State != "active" {
		t.Fatalf("exit %d, %+v, %v", code, res, err)
	}
}

func TestHoldCheckFailsClosedOnABadStore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, releasehold.FileName), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := cli(t, nil, "hold", "check", "--project", "alpha", "--state-dir", dir)
	if code == exitOK || code == exitHeld || out != "" || !strings.Contains(errOut, "treat Promote as held") {
		t.Fatalf("exit %d, out %q, err %q; want exit 2 and a plain refusal", code, out, errOut)
	}
}

func TestHoldAddUsageErrors(t *testing.T) {
	fixClock(t, holdT0)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"--until", "2h", "--reason", "r"},                                   // no project
		{"--project", "a", "--all", "--until", "2h", "--reason", "r"},        // both
		{"--project", "a", "--reason", "r"},                                  // no until
		{"--project", "a", "--until", "soon", "--reason", "r"},               // bad time
		{"--project", "a", "--until", "-1h", "--reason", "r"},                // past
		{"--project", "a", "--until", "2h"},                                  // no reason
		{"--project", "Bad Name", "--until", "2h", "--reason", "r"},          // bad project
		{"--project", "a", "--until", "2h", "--reason", "r", "--actor", "x"}, // bad actor
		{"--project", "a", "--until", "2h", "--reason", "r", "stray"},        // positional
	} {
		code, _, _ := cli(t, []string{"USERNAME=pat"}, append([]string{"hold", "add", "--state-dir", dir}, args...)...)
		if code != exitUsage {
			t.Errorf("hold add %v: exit %d, want %d", args, code, exitUsage)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, releasehold.FileName)); !os.IsNotExist(err) {
		t.Fatal("a refused add wrote the store")
	}
}

func TestHoldLiftAndActivity(t *testing.T) {
	fixClock(t, holdT0)
	dir := t.TempDir()
	h := addHold(t, dir, "--project", "alpha", "--until", "2026-11-28", "--reason", "release review", "--actor", "claude", "--owner", "sam")
	fixClock(t, holdT0.Add(time.Hour))
	code, out, errOut := cli(t, []string{"USERNAME=pat", actorEnv + "=ui"}, "hold", "lift", h.ID, "--reason", "review done", "--state-dir", dir)
	if code != exitOK || !strings.Contains(out, "lifted by pat") {
		t.Fatalf("lift: exit %d, %q %q", code, out, errOut)
	}
	if code, _, _ := cli(t, nil, "hold", "check", "--project", "alpha", "--state-dir", dir); code != exitOK {
		t.Fatalf("check after lift: exit %d", code)
	}
	if code, _, _ := cli(t, []string{"USERNAME=pat"}, "hold", "lift", h.ID, "--state-dir", dir); code != exitUsage {
		t.Fatalf("second lift: exit %d, want %d", code, exitUsage)
	}
	if code, _, _ := cli(t, []string{"USERNAME=pat"}, "hold", "lift", "h-unknown", "--state-dir", dir); code != exitFailed {
		t.Fatalf("unknown id: exit %d, want %d", code, exitFailed)
	}

	// list: the lifted hold shows only with --all
	if _, out, _ := cli(t, nil, "hold", "list", "--state-dir", dir); !strings.Contains(out, "no active or upcoming holds") {
		t.Fatalf("list: %q", out)
	}
	code, out, _ = cli(t, nil, "hold", "list", "--all", "--json", "--state-dir", dir)
	var list holdListJSON
	if err := json.Unmarshal([]byte(out), &list); err != nil || code != exitOK || len(list.Holds) != 1 || list.Holds[0].State != "lifted" || list.Holds[0].LiftedBy != "pat" {
		t.Fatalf("list --all --json: exit %d, %+v, %v", code, list, err)
	}

	// activity: the add (actor claude, run by pat for owner sam), the lift (actor ui from the env, lifter pat),
	// then the two failed lifts, each logged with its outcome (BL-705)
	code, out, _ = cli(t, nil, "activity", "--json", "--state-dir", dir)
	var act activityJSON
	if err := json.Unmarshal([]byte(out), &act); err != nil || code != exitOK || len(act.Entries) != 4 {
		t.Fatalf("activity --json: exit %d, %+v, %v", code, act, err)
	}
	a, l := act.Entries[0], act.Entries[1]
	if again, unknown := act.Entries[2], act.Entries[3]; again.Action != "hold.lift" || again.Outcome != "invalid" ||
		unknown.Action != "hold.lift" || unknown.Outcome != "refused" {
		t.Errorf("failed lift entries = %+v, %+v", again, unknown)
	}
	if a.Actor != activitylog.ActorClaude || a.Person != "pat" || a.Action != "hold.add" || !strings.HasPrefix(a.Target, h.ID) || !strings.Contains(a.Target, "owner sam") || a.Reason != "release review" {
		t.Errorf("add entry = %+v", a)
	}
	if l.Actor != activitylog.ActorUI || l.Person != "pat" || l.Action != "hold.lift" || l.Reason != "review done" {
		t.Errorf("lift entry = %+v", l)
	}
	_, out, _ = cli(t, nil, "activity", "--actor", "ui", "--state-dir", dir)
	if !strings.Contains(out, "hold.lift") || strings.Contains(out, "hold.add") {
		t.Errorf("activity --actor ui:\n%s", out)
	}
	_, out, _ = cli(t, nil, "activity", "--limit", "1", "--json", "--state-dir", dir)
	if err := json.Unmarshal([]byte(out), &act); err != nil || len(act.Entries) != 1 || act.Entries[0].Action != "hold.lift" {
		t.Errorf("activity --limit 1: %+v %v", act, err)
	}
	if code, _, _ := cli(t, nil, "activity", "--actor", "admin", "--state-dir", dir); code != exitUsage {
		t.Errorf("activity --actor admin: exit %d", code)
	}
}

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.Local)
	cases := map[string]time.Time{
		"90m":                  now.Add(90 * time.Minute),
		"3d":                   now.Add(72 * time.Hour),
		"2026-12-01T10:00:00Z": time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC),
		"2026-12-01 18:30":     time.Date(2026, 12, 1, 18, 30, 0, 0, time.Local),
	}
	for in, want := range cases {
		got, err := parseWhen(in, now, true)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseWhen(%q) = %s, %v; want %s", in, got, err, want)
		}
	}
	if got, _ := parseWhen("2026-12-01", now, true); !got.Equal(time.Date(2026, 12, 2, 0, 0, 0, 0, time.Local)) {
		t.Errorf("a bare --until date must hold the whole day: %s", got)
	}
	if got, _ := parseWhen("2026-12-01", now, false); !got.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.Local)) {
		t.Errorf("a bare --from date is the start of the day: %s", got)
	}
	for _, bad := range []string{"", "soon", "0s", "-2h", "0d"} {
		if _, err := parseWhen(bad, now, true); err == nil {
			t.Errorf("parseWhen(%q) accepted", bad)
		}
	}
}
