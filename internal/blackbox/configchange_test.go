package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func changeRig(t *testing.T) *svcRig {
	t.Helper()
	g := newSvcRig(t)
	// Linux: the default log folder is a Windows path, so the base is the test config, moved into the rig.
	c := testConfig(t)
	c.Name, c.Folder, c.Port = g.cfg.Name, g.cfg.Folder, g.cfg.Port
	g.cfg = c
	if _, err := g.m.Install(g.cfg); err != nil {
		t.Fatal(err)
	}
	tick := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	old := changeNow
	changeNow = func() time.Time { tick = tick.Add(time.Minute); return tick }
	t.Cleanup(func() { changeNow = old })
	return g
}

func (g *svcRig) changeDir() string { return filepath.Join(g.prog, changeDirName) }

func (g *svcRig) installedCfg(t *testing.T) Config {
	t.Helper()
	c, err := LoadConfig(g.p.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func off(id string) ChangeOp {
	f := false
	return ChangeOp{Verb: OpCollector, ID: id, On: &f}
}

func reasonIs(err error, want Reason) bool {
	var ae *ApplyError
	return errors.As(err, &ae) && ae.Reason == want
}

func TestChangeSwitchesACollectorAndWritesOneVerifiedLine(t *testing.T) {
	g := changeRig(t)
	res, err := g.m.ChangeConfig(off("web.iis"))
	if err != nil || !res.Restarted || res.Line.Result != "applied" || res.Line.Seq != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if on := g.installedCfg(t).CollectorOn("web.iis"); on {
		t.Error("web.iis is still on in the installed settings")
	}
	h, err := g.m.History()
	if err != nil || !h.Verified || len(h.Lines) != 1 || h.Lines[0].Detail != "collector web.iis off" || h.Lines[0].Before == h.Lines[0].After {
		t.Fatalf("%+v %v", h, err)
	}
	// A second change chains onto the first.
	every := 300
	if _, err := g.m.ChangeConfig(ChangeOp{Verb: OpCollector, ID: "web.iis", EverySec: &every}); err != nil {
		t.Fatal(err)
	}
	h, _ = g.m.History()
	if !h.Verified || len(h.Lines) != 2 || h.Lines[1].Prev != h.Lines[0].Hash {
		t.Fatalf("%+v", h)
	}
}

func TestChangeNotHealthyIsRolledBackByItself(t *testing.T) {
	g := changeRig(t)
	before := g.installedCfg(t)
	g.env.aliveFails = 1000
	_, err := g.m.ChangeConfig(off("web.iis"))
	if !reasonIs(err, ReasonUnhealthy) {
		t.Fatalf("%v", err)
	}
	if hashConfig(g.installedCfg(t)) != hashConfig(before) {
		t.Error("the previous settings were not put back")
	}
	if g.svc.state != SvcRunning {
		t.Errorf("the recorder is %s after the roll-back", g.svc.state)
	}
	h, _ := g.m.History()
	if len(h.Lines) != 1 || h.Lines[0].Result != "rolled-back" || h.Lines[0].After != h.Lines[0].Before || h.Pending {
		t.Fatalf("%+v", h)
	}
	if g.env.slept < healthWindow-healthStep {
		t.Errorf("gave up after %v; the recorder gets a minute", g.env.slept)
	}
	// Nothing to go back to: the snapshot of the failed change is gone.
	if _, err := g.m.RollbackConfig(); !reasonIs(err, ReasonNothingToRollBack) {
		t.Errorf("rollback after a rolled-back change: %v", err)
	}
}

func TestChangeAFlappingRecorderDoesNotCountAsHealthy(t *testing.T) {
	g := changeRig(t)
	g.env.aliveFails = 1 // one failed answer, then good: three good polls in a row are still reached
	if _, err := g.m.ChangeConfig(off("web.iis")); err != nil {
		t.Fatalf("one slow answer rolled back a good change: %v", err)
	}
}

func TestChangeRefusals(t *testing.T) {
	g := changeRig(t)
	bad := []ChangeOp{
		off("nope.nothing"),
		off("host.diskcpu"), // always on
		{Verb: OpCounterAdd, Path: `\\server\Process(*)\% Processor Time`},
		{Verb: OpCounterAdd, Path: "not a path"},
		{Verb: OpCounterRemove, Path: `\Memory\Available MBytes`}, // not there
		{Verb: OpCollector, ID: "web.iis"},                        // says nothing
		{Verb: "run", ID: "x"},
	}
	for _, op := range bad {
		if _, err := g.m.ChangeConfig(op); !reasonIs(err, ReasonChangeRefused) {
			t.Errorf("%+v: %v", op, err)
		}
	}
	every := 7 // not an allowed interval
	if _, err := g.m.ChangeConfig(ChangeOp{Verb: OpCollector, ID: "web.iis", EverySec: &every}); !reasonIs(err, ReasonChangeRefused) {
		t.Errorf("odd interval: %v", err)
	}
	// Setting what is already set changes nothing and says so.
	on := true
	if _, err := g.m.ChangeConfig(ChangeOp{Verb: OpCollector, ID: "web.iis", On: &on}); !reasonIs(err, ReasonChangeRefused) {
		t.Errorf("no-op: %v", err)
	}
	if g.svc.did("stop") != 0 {
		t.Errorf("a refused change restarted the recorder: %v", g.svc.calls)
	}
	g.env.admin = false
	if _, err := g.m.ChangeConfig(off("web.iis")); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("non-admin: %v", err)
	}
	g.env.admin = true
	g.svc.state = SvcNotInstalled
	if _, err := g.m.ChangeConfig(off("web.iis")); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("not installed: %v", err)
	}
}

func TestChangeCountersAddAndRemove(t *testing.T) {
	g := changeRig(t)
	p := `\Memory\Available MBytes`
	if _, err := g.m.ChangeConfig(ChangeOp{Verb: OpCounterAdd, Path: p}); err != nil {
		t.Fatal(err)
	}
	if got := g.installedCfg(t).ExtraCounterPaths(); len(got) != 1 || got[0] != p {
		t.Fatalf("%v", got)
	}
	if _, err := g.m.ChangeConfig(ChangeOp{Verb: OpCounterAdd, Path: strings.ToLower(p)}); !reasonIs(err, ReasonChangeRefused) {
		t.Errorf("twice: %v", err)
	}
	if _, err := g.m.ChangeConfig(ChangeOp{Verb: OpCounterRemove, Path: p}); err != nil {
		t.Fatal(err)
	}
	if got := g.installedCfg(t).ExtraCounterPaths(); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestRollbackUndoesTheLastChangeAndGoesBackAndForth(t *testing.T) {
	g := changeRig(t)
	if _, err := g.m.RollbackConfig(); !reasonIs(err, ReasonNothingToRollBack) {
		t.Fatalf("fresh: %v", err)
	}
	orig := hashConfig(g.installedCfg(t))
	if _, err := g.m.ChangeConfig(off("web.iis")); err != nil {
		t.Fatal(err)
	}
	changed := hashConfig(g.installedCfg(t))
	if _, err := g.m.RollbackConfig(); err != nil || hashConfig(g.installedCfg(t)) != orig {
		t.Fatalf("rollback: %v", err)
	}
	if _, err := g.m.RollbackConfig(); err != nil || hashConfig(g.installedCfg(t)) != changed {
		t.Fatalf("rollback of the rollback: %v", err)
	}
	h, _ := g.m.History()
	if !h.Verified || len(h.Lines) != 3 || h.Lines[1].Result != "rollback" {
		t.Fatalf("%+v", h)
	}
}

func TestAuditTamperingStopsChanges(t *testing.T) {
	g := changeRig(t)
	for i := 0; i < 3; i++ {
		on := i%2 == 1
		if _, err := g.m.ChangeConfig(ChangeOp{Verb: OpCollector, ID: "web.iis", On: &on}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(g.changeDir(), auditFileName)
	raw, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	cases := map[string]string{
		"edited":  strings.Join([]string{strings.Replace(lines[0], "web.iis off", "web.iis on", 1), lines[1], lines[2]}, "\n"),
		"removed": strings.Join([]string{lines[0], lines[2]}, "\n"),
		"swapped": strings.Join([]string{lines[1], lines[0], lines[2]}, "\n"),
		"junk":    strings.Join([]string{lines[0], "not json", lines[2]}, "\n"),
	}
	for name, body := range cases {
		if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		h, err := g.m.History()
		if err != nil || h.Verified || h.Problem == "" {
			t.Errorf("%s: history says verified (%+v, %v)", name, h, err)
		}
		before := hashConfig(g.installedCfg(t))
		if _, err := g.m.ChangeConfig(off("web.iis")); !reasonIs(err, ReasonAuditBroken) {
			t.Errorf("%s: %v", name, err)
		}
		if hashConfig(g.installedCfg(t)) != before {
			t.Errorf("%s: settings changed although the history is broken", name)
		}
	}
}

func TestPendingChangeIsFinishedByTheNextCommand(t *testing.T) {
	g := changeRig(t)
	orig := g.installedCfg(t)
	dir := g.changeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A change that died in its minute: settings changed, snapshot of the old ones, marker past its deadline.
	snap, err := writeSnapshot(dir, 1, orig)
	if err != nil {
		t.Fatal(err)
	}
	changed, _ := off("web.iis").Apply(orig)
	if err := SaveConfig(g.p.ConfigPath, changed, false); err != nil {
		t.Fatal(err)
	}
	if err := writePending(dir, pendingMark{Seq: 1, Snapshot: snap, Deadline: changeNow().Add(-time.Hour), Detail: "collector web.iis off"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.m.RollbackConfig(); !reasonIs(err, ReasonNothingToRollBack) {
		t.Fatalf("%v", err) // the unfinished change is finished first, and then there is nothing earlier to go back to
	}
	if hashConfig(g.installedCfg(t)) != hashConfig(orig) {
		t.Fatal("the unfinished change was not undone")
	}
	h, _ := g.m.History()
	if !h.Verified || len(h.Lines) == 0 || h.Lines[0].Result != "recovered" || h.Pending {
		t.Fatalf("%+v", h)
	}
	// A marker still inside its deadline is another change in progress: refused, nothing touched.
	if err := writePending(dir, pendingMark{Seq: 9, Snapshot: snapName(9), Deadline: changeNow().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.m.ChangeConfig(off("web.iis")); !reasonIs(err, ReasonChangeRefused) {
		t.Errorf("%v", err)
	}
}

func TestChangeOnAStoppedRecorderSavesWithoutWatching(t *testing.T) {
	g := changeRig(t)
	g.svc.state = SvcStopped
	g.env.aliveFails = 1000
	res, err := g.m.ChangeConfig(off("web.iis"))
	if err != nil || res.Restarted || res.Line.Result != "applied" {
		t.Fatalf("%+v %v", res, err)
	}
	if g.installedCfg(t).CollectorOn("web.iis") {
		t.Error("not saved")
	}
}

func TestOnlyTheNewestTwentySnapshotsAreKept(t *testing.T) {
	g := changeRig(t)
	for i := 0; i < 25; i++ {
		on := i%2 == 1
		if _, err := g.m.ChangeConfig(ChangeOp{Verb: OpCollector, ID: "web.iis", On: &on}); err != nil {
			t.Fatalf("change %d: %v", i, err)
		}
	}
	ents, _ := os.ReadDir(g.changeDir())
	n := 0
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "snap-") {
			n++
		}
	}
	if n != historyKeep {
		t.Errorf("%d snapshots kept, want %d", n, historyKeep)
	}
	if h, _ := g.m.History(); len(h.Lines) != historyKeep || !h.Verified {
		t.Errorf("history shows %d lines", len(h.Lines))
	}
}
