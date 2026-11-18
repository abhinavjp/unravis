package blackbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This file holds the wave-5/wave-12 residual tests:
//   - lossless (PID, StartTicks) joins: two same-PID lifetimes within one unix
//     second must never be conflated (clr/proc10/procIO/iis);
//   - IISWorker carries the verified worker creation so a recycled worker PID
//     stays distinguishable on the wire (additive, backward compatible);
//   - readSQLStatus bounds the file BEFORE allocation (W12-04 residual);
//   - handleFile/openRecordingFile refuse link/junction/ancestor substitution
//     and non-regular objects without serving private data (W12-05 residual).
//
// Identity tests use the existing fake-source seams (no Windows, no live
// process/service ops). Containment tests create links only inside temp dirs
// the test owns; no real private data is touched.

// --- lossless creation identity helpers ---

// ticksAt returns exact FILETIME ticks startUnix seconds plus offsetTicks into
// that second. Offsets used here are multiples of 16 so the value is exactly
// representable as a float64 at the current epoch (no rounding ambiguity).
func ticksAt(startUnix, offsetTicks int64) int64 {
	lo, _ := creationWindow(startUnix)
	return lo + offsetTicks
}

// Two lifetimes of one PID, 6 s apart: farther than pdhCreationSlack, so the
// derived-start join tells them apart (lifetimes closer than the slack cannot be
// told apart, and Windows does not recycle a PID that fast).
const (
	sameSecondLifeA = 2_000_000  // 0.2 s into the start second
	sameSecondLifeB = 62_000_000 // 6.2 s after the start second began
)

// elapsedForTicks is the Elapsed Time reading (seconds) that makes the group
// derive exactly creationTicks as the start on the first tick (clock 1000 s).
func elapsedForTicks(creationTicks int64) float64 {
	return float64(creationWindowTicks(identityTestBase)-creationTicks) / float64(filetimeTicksPerSecond)
}

func creationWindowTicks(startUnix int64) int64 {
	lo, _ := creationWindow(startUnix)
	return lo
}

func TestTargetCreationConfirmedLossless(t *testing.T) {
	start := int64(900)
	lifeA := ticksAt(start, sameSecondLifeA)
	lifeB := ticksAt(start, sameSecondLifeB)
	withTicks := ResolvedTarget{Key: "pool:a", PID: 100, Start: start, StartTicks: lifeA}
	secondsOnly := ResolvedTarget{Key: "pool:a", PID: 100, Start: start}

	// A later lifetime of the same PID (6 s apart) must NOT confirm.
	if targetCreationConfirmed(float64(lifeB), withTicks) {
		t.Fatal("a different lifetime confirmed the lossless identity")
	}
	if !targetCreationConfirmed(float64(lifeA), withTicks) {
		t.Fatal("the exact lossless creation did not confirm")
	}
	// The float64 formatting slack is honoured on both sides, never more.
	for _, d := range []int64{-pdhCreationSlack, pdhCreationSlack} {
		if !targetCreationConfirmed(float64(lifeA+d), withTicks) {
			t.Fatalf("slack %d must confirm", d)
		}
	}
	for _, d := range []int64{-pdhCreationSlack - 16, pdhCreationSlack + 16} {
		if targetCreationConfirmed(float64(lifeA+d), withTicks) {
			t.Fatalf("beyond slack %d must not confirm", d)
		}
	}
	// Seconds-only view keeps the old window behaviour (no regression while
	// the resolver view does not yet carry StartTicks).
	if !targetCreationConfirmed(float64(lifeA), secondsOnly) {
		t.Fatal("seconds-only fallback must keep confirming inside the window")
	}
	if targetCreationConfirmed(float64(ticksAt(start+10, filetimeTicksPerSecond/2)), secondsOnly) {
		t.Fatal("a start 10 s later must not confirm the fallback")
	}
	// No creation identity at all never confirms, and is reported as absent.
	none := ResolvedTarget{Key: "pool:a", PID: 100}
	if targetHasCreation(none) || targetCreationConfirmed(float64(lifeA), none) {
		t.Fatal("a target without creation must never confirm")
	}
	if !targetHasCreation(withTicks) || !targetHasCreation(secondsOnly) {
		t.Fatal("ticks or seconds must count as a creation identity")
	}
	// Lossless ticks alone (Start 0) still confirm: the ticks are the identity.
	ticksOnly := ResolvedTarget{Key: "pool:a", PID: 100, StartTicks: lifeA}
	if !targetHasCreation(ticksOnly) || !targetCreationConfirmed(float64(lifeA), ticksOnly) {
		t.Fatal("StartTicks alone must be a usable identity")
	}
}

// --- procIO: same-second PID reuse at the join ---

// procIOIdentTicks appends identity readings with an explicit creation FILETIME
// (the existing procIOIdent only speaks unix seconds).
func procIOIdentTicks(src *procIOFakeSource, inst string, pid float64, creationTicks int64) {
	src.readings = append(src.readings,
		procIOReading(inst, "ID Process", pid),
		procIOReading(inst, "Elapsed Time", elapsedForTicks(creationTicks)),
	)
}

func TestProcIOSameSecondPIDReuseNotConflated(t *testing.T) {
	for _, tc := range []struct {
		name       string
		creation   int64
		wantRow    bool
		targetTick int64
	}{
		{"exact lifetime", ticksAt(900, sameSecondLifeA), true, ticksAt(900, sameSecondLifeA)},
		{"other same-second lifetime", ticksAt(900, sameSecondLifeB), false, ticksAt(900, sameSecondLifeA)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &procIOFakeSource{}
			procIOInstance(src, "w3wp")
			procIOIdentTicks(src, "w3wp", 100, tc.creation)
			src.readings = append(src.readings, procIOReading("w3wp", "Page File Bytes", 1000))
			g := newProcIOTestGroup(t, src, func() []ResolvedTarget {
				return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900, StartTicks: tc.targetTick}}
			}, nil)
			grp := collectProcIO(t, g, src, 1)
			if tc.wantRow {
				if grp == nil || len(grp.Targets) != 1 || grp.Targets[0].PageFileBytes == nil || *grp.Targets[0].PageFileBytes != 1000 {
					t.Fatalf("group = %+v, want the exact lifetime's row", grp)
				}
				return
			}
			if grp != nil {
				t.Fatalf("group = %+v, want nil (same-second reuse must not join)", grp)
			}
		})
	}
}

// --- proc10: same-second PID reuse at the join ---

// proc10InstanceTicks is proc10Instance with an explicit creation FILETIME.
func proc10InstanceTicks(src *proc10FakeSource, inst string, pid float64, creationTicks int64, cpu, ws, pb float64) {
	for _, c := range []string{"ID Process", "Elapsed Time", "% Processor Time", "Working Set", "Private Bytes"} {
		src.paths = append(src.paths, proc10Path(inst, c))
	}
	src.readings = append(src.readings,
		proc10Reading(inst, "ID Process", pid),
		proc10Reading(inst, "Elapsed Time", elapsedForTicks(creationTicks)),
		proc10Reading(inst, "% Processor Time", cpu),
		proc10Reading(inst, "Working Set", ws),
		proc10Reading(inst, "Private Bytes", pb),
	)
}

func TestProc10SameSecondPIDReuseNotConflated(t *testing.T) {
	for _, tc := range []struct {
		name     string
		creation int64
		wantRow  bool
	}{
		{"exact lifetime", ticksAt(900, sameSecondLifeA), true},
		{"other same-second lifetime", ticksAt(900, sameSecondLifeB), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &proc10FakeSource{}
			proc10InstanceTicks(src, "dotnet", 7777, tc.creation, 20, 3000, 4000)
			g := newProc10TestGroup(t, src, []ResolvedTarget{
				{Key: "pool:a", PID: 7777, Start: 900, StartTicks: ticksAt(900, sameSecondLifeA), Runtime: 2},
			}, nil)
			grp := proc10Collect(t, g, src, 3)
			if tc.wantRow {
				if grp == nil || len(grp.Targets) != 1 || grp.Targets[0].WorkingSet == nil || *grp.Targets[0].WorkingSet != 3000 {
					t.Fatalf("group = %+v, want the exact lifetime's row", grp)
				}
				return
			}
			if grp != nil {
				t.Fatalf("group = %+v, want nil (same-second reuse must not join)", grp)
			}
		})
	}
}

// --- CLR: same-second PID reuse at the join ---

// clrProcessIdentTicks is clrProcessIdent with an explicit creation FILETIME.
func clrProcessIdentTicks(src *clrFakeSource, procInst string, pid float64, creationTicks int64) {
	src.paths = append(src.paths, procIOPath(procInst, "ID Process"), procIOPath(procInst, "Elapsed Time"))
	src.readings = append(src.readings,
		procIOReading(procInst, "ID Process", pid),
		procIOReading(procInst, "Elapsed Time", elapsedForTicks(creationTicks)),
	)
}

func TestCLRSameSecondPIDReuseNotConflated(t *testing.T) {
	for _, tc := range []struct {
		name     string
		creation int64
		wantRow  bool
	}{
		{"exact lifetime", ticksAt(900, sameSecondLifeA), true},
		{"other same-second lifetime", ticksAt(900, sameSecondLifeB), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &clrFakeSource{}
			src.paths = append(src.paths, clrInstancePaths("w3wp")...)
			src.readings = append(src.readings,
				clrReading(".NET CLR Memory", "w3wp", "Process ID", 100),
				clrReading(".NET CLR Memory", "w3wp", "Gen 0 heap size", 111),
			)
			clrProcessIdentTicks(src, "w3wp", 100, tc.creation)
			g := newCLRTestGroup(t, src, func() []ResolvedTarget {
				return []ResolvedTarget{{Key: "pool:a", PID: 100, Start: 900, StartTicks: ticksAt(900, sameSecondLifeA), Runtime: 1}}
			}, nil)
			grp := clrCollect(t, g, time.Unix(1000, 0))
			if tc.wantRow {
				if grp == nil || len(grp.Targets) != 1 || grp.Targets[0].Gen0Bytes == nil || *grp.Targets[0].Gen0Bytes != 111 {
					t.Fatalf("group = %+v, want the exact lifetime's row", grp)
				}
				return
			}
			if grp != nil {
				t.Fatalf("group = %+v, want nil (same-second reuse must not join)", grp)
			}
		})
	}
}

// --- IIS: same-second worker reuse and the wire creation field ---

// iisWorkerIdentTicks is iisWorkerIdent with an explicit creation FILETIME.
func iisWorkerIdentTicks(src *iisFakeSource, procInst string, pid float64, creationTicks int64) {
	src.paths = append(src.paths, procIOPath(procInst, "ID Process"), procIOPath(procInst, "Elapsed Time"))
	src.readings = append(src.readings,
		procIOReading(procInst, "ID Process", pid),
		procIOReading(procInst, "Elapsed Time", elapsedForTicks(creationTicks)),
	)
}

func TestIISSameSecondWorkerReuseNotConflated(t *testing.T) {
	src := &iisFakeSource{paths: iisPoolPaths("100_pool-a")}
	src.readings = iisPoolReadings("100_pool-a", 1, 2, 3)
	// Worker creation is lifeB; the captured target identity is lifeA — same
	// PID, same unix second, different lifetime. Must not join.
	iisWorkerIdentTicks(src, "w3wp", 100, ticksAt(10, sameSecondLifeB))
	g := newIISTestGroup(t, src, []ResolvedTarget{
		{Key: "pool:pool-a", PID: 100, Start: 10, StartTicks: ticksAt(10, sameSecondLifeA), Pool: "pool-a"},
	}, nil)
	if grp := collectIIS(t, g, time.Unix(1000, 0)); grp != nil {
		t.Fatalf("group = %+v, want nil (same-second recycled worker must not join)", grp)
	}
}

// A recycled worker PID (same PID, same unix second, new creation) is
// distinguishable downstream: each emitted row carries its own verified
// StartTicks, and the wrong generation never joins.
func TestIISWorkerCreationDistinguishesRecycledPID(t *testing.T) {
	src := &iisFakeSource{paths: iisPoolPaths("100_pool-a")}
	src.readings = iisPoolReadings("100_pool-a", 1, 2, 3)
	lifeA := ticksAt(10, sameSecondLifeA)
	lifeB := ticksAt(10, sameSecondLifeB)
	iisWorkerIdentTicks(src, "w3wp", 100, lifeA)
	targets := []ResolvedTarget{{Key: "pool:pool-a", PID: 100, Start: 10, StartTicks: lifeA, Pool: "pool-a"}}
	g := newIISTestGroup(t, src, targets, nil)

	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 1 {
		t.Fatalf("group = %+v, want 1 worker", grp)
	}
	first := grp.Workers[0]
	if first.PID != 100 || first.StartTicks != lifeA {
		t.Fatalf("worker = %+v, want pid 100 startTicks %d", first, lifeA)
	}

	// The worker recycles within the same second: same PID 100, creation lifeB.
	// The captured view now carries lifeB. The old seconds-only join could not
	// tell the two apart; the wire rows must.
	for i := range src.readings {
		if strings.HasSuffix(src.readings[i].Path, `\Elapsed Time`) {
			src.readings[i].Value = elapsedForTicks(lifeB) + 15 // tick 2 is 15 s later
		}
	}
	targets[0].StartTicks = lifeB
	grp = collectIIS(t, g, time.Unix(1015, 0))
	if grp == nil || len(grp.Workers) != 1 {
		t.Fatalf("group = %+v, want 1 worker after recycle", grp)
	}
	second := grp.Workers[0]
	if second.PID != first.PID || second.StartTicks != lifeB || second.StartTicks == first.StartTicks {
		t.Fatalf("recycled worker = %+v (first %+v): same PID but a distinct creation must be visible", second, first)
	}

	// A stale captured identity (lifeA) against the recycled worker (lifeB)
	// must not join at all.
	targets[0].StartTicks = lifeA
	if grp := collectIIS(t, g, time.Unix(1030, 0)); grp != nil {
		t.Fatalf("group = %+v, want nil (stale identity vs recycled worker)", grp)
	}
}

// Out-of-process: the worker row carries the PARENT worker's own verified
// creation, never the application child's identity.
func TestIISWorkerCreationIsTheWorkersOwn(t *testing.T) {
	src := &iisFakeSource{paths: iisPoolPaths("1000_api")}
	src.readings = iisPoolReadings("1000_api", 3, 4, 5)
	parentTicks := ticksAt(5, sameSecondLifeA)
	childTicks := ticksAt(10, sameSecondLifeB)
	iisWorkerIdentTicks(src, "w3wp", 1000, parentTicks)
	iisWorkerIdentTicks(src, "dotnet", 2000, childTicks)
	g := newIISTestGroup(t, src, []ResolvedTarget{
		{Key: "pool:api", PID: 2000, Start: 10, StartTicks: childTicks, Runtime: 2, Pool: "api"},
	}, nil)
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 1 {
		t.Fatalf("group = %+v, want 1 worker (out-of-process parent)", grp)
	}
	w := grp.Workers[0]
	if w.PID != 1000 || w.StartTicks != parentTicks {
		t.Fatalf("worker = %+v, want the parent's pid 1000 and creation %d", w, parentTicks)
	}
}

// An unreadable (zero) worker creation stays omitted on the wire, never a
// guessed stamp; the verified-alive out-of-process row is still emitted.
func TestIISWorkerCreationOmittedWhenUnreadable(t *testing.T) {
	src := &iisFakeSource{paths: iisPoolPaths("1000_api")}
	src.readings = iisPoolReadings("1000_api", 3, 4, 5)
	iisWorkerIdentTicks(src, "w3wp", 1000, 0) // creation present but unreadable
	iisWorkerIdentTicks(src, "dotnet", 2000, ticksAt(10, sameSecondLifeA))
	g := newIISTestGroup(t, src, []ResolvedTarget{
		{Key: "pool:api", PID: 2000, Start: 10, StartTicks: ticksAt(10, sameSecondLifeA), Runtime: 2, Pool: "api"},
	}, nil)
	grp := collectIIS(t, g, time.Unix(1000, 0))
	if grp == nil || len(grp.Workers) != 1 || grp.Workers[0].StartTicks != 0 {
		t.Fatalf("group = %+v, want the worker with no creation stamp", grp)
	}
	b, err := json.Marshal(grp.Workers[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "startTicks") {
		t.Fatalf("an unreadable creation must stay omitted: %s", b)
	}
}

// The wire shape stays backward compatible: startTicks is additive/optional,
// old rows decode unchanged, and no existing field was renamed.
func TestIISWorkerWireBackwardCompatible(t *testing.T) {
	old := `{"pool":"p","pid":7,"activeRequests":2,"activeThreads":3,"pingLatencyMs":4}`
	var w IISWorker
	if err := json.Unmarshal([]byte(old), &w); err != nil {
		t.Fatal(err)
	}
	if w.Pool != "p" || w.PID != 7 || w.StartTicks != 0 ||
		w.ActiveRequests == nil || *w.ActiveRequests != 2 ||
		w.ActiveThreads == nil || *w.ActiveThreads != 3 ||
		w.PingLatencyMs == nil || *w.PingLatencyMs != 4 {
		t.Fatalf("old row decoded wrong: %+v", w)
	}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "startTicks") {
		t.Fatalf("zero creation must be omitted: %s", b)
	}
	w.StartTicks = ticksAt(10, sameSecondLifeA)
	b, err = json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), fmt.Sprintf(`"startTicks":%d`, w.StartTicks)) {
		t.Fatalf("set creation must be written: %s", b)
	}
	var back IISWorker
	if err := json.Unmarshal(b, &back); err != nil || back.StartTicks != w.StartTicks {
		t.Fatalf("round trip lost the creation: %+v (%v)", back, err)
	}
}

// --- W12-04 residual: readSQLStatus is bounded before allocation ---

// An oversized status file returns the fixed off shape. The file is created
// with Truncate (no data written), far beyond the bound: readSQLStatus must
// refuse it from Stat alone, before allocating/reading the file body.
func TestSQLStatusHugeFileOff(t *testing.T) {
	g := newSrvRig(t)
	dir := SQLDataDir(g.s.Dir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "status.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true,"state":"on","failures":0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	const huge = 1 << 28 // 256 MiB: unreadable-in-practice size, cheap to create
	if err := os.Truncate(path, huge); err != nil {
		t.Fatal(err)
	}
	now := g.clk.now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	if d := statusSQL(t, g); d.Enabled || d.State != "off" || d.Failures != 0 || d.LastOK != nil {
		t.Fatalf("a %d-byte status file must read as the whole off object: %+v", huge, d)
	}
	// The direct seam agrees with the HTTP answer.
	if d := readSQLStatus(g.s.Dir(), g.clk.now()); d.Enabled || d.State != "off" {
		t.Fatalf("readSQLStatus trusted the huge file: %+v", d)
	}
}

// The bound is exact: a valid document of exactly sqlStatusMaxBytes is still
// served; one byte more is the whole off object.
func TestSQLStatusSizeBoundary(t *testing.T) {
	g := newSrvRig(t)
	base := `{"enabled":true,"state":"on","failures":0}`
	atLimit := base + strings.Repeat(" ", sqlStatusMaxBytes-len(base))
	writeSQLStatusFile(t, g, atLimit)
	if d := statusSQL(t, g); !d.Enabled || d.State != "on" {
		t.Fatalf("a document of exactly the bound must be served: %+v", d)
	}
	writeSQLStatusFile(t, g, atLimit+" ")
	if d := statusSQL(t, g); d.Enabled || d.State != "off" {
		t.Fatalf("one byte over the bound must be wholly off: %+v", d)
	}
}

// A non-regular object (a directory named status.json) is off, never read.
func TestSQLStatusNonRegularOff(t *testing.T) {
	g := newSrvRig(t)
	dir := SQLDataDir(g.s.Dir())
	if err := os.MkdirAll(filepath.Join(dir, "status.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if d := statusSQL(t, g); d.Enabled || d.State != "off" || d.Failures != 0 || d.LastOK != nil {
		t.Fatalf("a non-regular status object must read as off: %+v", d)
	}
}

// --- W12-05 residual: handle-based containment / no-follow ---

// makeDirLink creates a directory link at link pointing to target. On Windows
// it prefers a junction (mklink /J needs no privilege); elsewhere, or when
// mklink is unavailable, it falls back to a symlink and skips the test when
// link creation is not permitted. Only temp dirs owned by the test are used.
func makeDirLink(t *testing.T, link, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err == nil {
			return
		}
		t.Logf("mklink /J unavailable (%v): %s", err, out)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a directory link fixture: %v", err)
	}
}

// makeFileLink creates a file symlink, skipping when not permitted.
func makeFileLink(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a file link fixture: %v", err)
	}
}

// privateTree makes a temp dir holding one "private" file with secret content.
func privateTree(t *testing.T) (dir, secret string) {
	t.Helper()
	dir = t.TempDir()
	secret = "private-data-must-not-be-served"
	if err := os.WriteFile(filepath.Join(dir, "private.ndjson"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, secret
}

// A valid recording basename that is a FILE LINK to a private file outside the
// recordings folder is refused: 404, fixed message, no private bytes, and the
// read-key guard still runs first.
func TestHandleFileRefusesLinkEntry(t *testing.T) {
	g := newSrvRig(t)
	priv, secret := privateTree(t)
	name := "bb-20261027-11.ndjson" // valid shape, not the rig's current file
	link := filepath.Join(g.s.Dir(), name)
	makeFileLink(t, link, filepath.Join(priv, "private.ndjson"))

	// Without the key the guard refuses before any filesystem work.
	if resp, _ := g.get(t, "/files/"+name, ""); resp.StatusCode != 401 {
		t.Fatalf("no key: %d, want 401", resp.StatusCode)
	}
	resp, body := g.get(t, "/files/"+name, g.key)
	if resp.StatusCode != 404 {
		t.Fatalf("link entry: %d, want 404", resp.StatusCode)
	}
	if strings.Contains(string(body), secret) {
		t.Fatal("the link target's private data was served")
	}
	if !strings.Contains(string(body), "no such recording file") {
		t.Fatalf("refusal must carry the fixed message: %s", body)
	}
	if b, err := os.ReadFile(filepath.Join(priv, "private.ndjson")); err != nil || string(b) != secret {
		t.Fatalf("private target was modified: %v", err)
	}
}

// A valid recording basename that is a JUNCTION (or directory symlink) to a
// private folder is refused the same way.
func TestHandleFileRefusesJunctionEntry(t *testing.T) {
	g := newSrvRig(t)
	priv, secret := privateTree(t)
	name := "bb-20261027-12.ndjson"
	makeDirLink(t, filepath.Join(g.s.Dir(), name), priv)
	resp, body := g.get(t, "/files/"+name, g.key)
	if resp.StatusCode != 404 {
		t.Fatalf("junction entry: %d, want 404", resp.StatusCode)
	}
	if strings.Contains(string(body), secret) {
		t.Fatal("the junction target's private data was served")
	}
}

// A non-regular object (a real directory) carrying a valid recording basename
// is refused: only regular files are served.
func TestHandleFileRefusesNonRegularEntry(t *testing.T) {
	g := newSrvRig(t)
	name := "bb-20261027-13.ndjson"
	if err := os.Mkdir(filepath.Join(g.s.Dir(), name), 0o755); err != nil {
		t.Fatal(err)
	}
	resp, body := g.get(t, "/files/"+name, g.key)
	if resp.StatusCode != 404 {
		t.Fatalf("directory entry: %d, want 404", resp.StatusCode)
	}
	if strings.Contains(string(body), g.s.Dir()) {
		t.Fatalf("refusal leaked a path: %s", body)
	}
}

// Ancestor substitution INSIDE the root: a nested path whose ancestor is a
// junction/symlink to a private folder is refused by the handle-relative walk.
// (handleFile itself only accepts basenames; this exercises the containment
// seam's ancestor validation directly.)
func TestOpenRecordingFileRefusesAncestorSubstitution(t *testing.T) {
	root := t.TempDir()
	priv, secret := privateTree(t)
	// A real recording file exists in the private folder: if the ancestor link
	// were followed, this is what would leak.
	name := "bb-20261027-10.ndjson"
	if err := os.WriteFile(filepath.Join(priv, name), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	makeDirLink(t, filepath.Join(root, "anc"), priv)
	f, err := openRecordingFile(root, filepath.Join("anc", name))
	if err == nil {
		f.Close()
		t.Fatal("an ancestor link escaped the root")
	}
	// A real subdirectory with a link LEAF is refused too.
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	makeFileLink(t, filepath.Join(sub, name), filepath.Join(priv, name))
	if f, err := openRecordingFile(root, filepath.Join("sub", name)); err == nil {
		f.Close()
		t.Fatal("a link leaf under a real ancestor escaped the root")
	}
}

// The root itself being a link/junction (ancestor substitution above the
// recordings folder) is refused before the root is pinned.
func TestOpenRecordingFileRefusesLinkRoot(t *testing.T) {
	real := t.TempDir()
	name := "bb-20261027-10.ndjson"
	if err := os.WriteFile(filepath.Join(real, name), []byte(`{"v":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	linkRoot := filepath.Join(parent, "recordings")
	makeDirLink(t, linkRoot, real)
	if f, err := openRecordingFile(linkRoot, name); err == nil {
		f.Close()
		t.Fatal("a link root was accepted")
	} else if !errors.Is(err, errRecordingRefused) {
		t.Fatalf("want the fixed refusal, got %v", err)
	}
}

// Positive control: a regular file under a real root still opens and reads
// byte-for-byte (the policy must not over-refuse), and a missing entry is a
// plain refusal.
func TestOpenRecordingFileServesRegular(t *testing.T) {
	root := t.TempDir()
	name := "bb-20261027-10.ndjson"
	want := `{"v":1,"run":"r"}` + "\n"
	if err := os.WriteFile(filepath.Join(root, name), []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openRecordingFile(root, name)
	if err != nil {
		t.Fatalf("regular file refused: %v", err)
	}
	defer f.Close()
	got := make([]byte, len(want))
	if _, err := f.Read(got); err != nil || string(got) != want {
		t.Fatalf("read %q (%v), want %q", got, err, want)
	}
	if _, err := openRecordingFile(root, "bb-20261027-11.ndjson"); err == nil {
		t.Fatal("a missing entry must be refused")
	}
}

// The normal download path keeps working end to end through the containment
// open (guard + shape + regular file), including ranges.
func TestHandleFileStillServesRegularRecording(t *testing.T) {
	g := newSrvRig(t)
	name := g.s.CurrentFile()
	want, err := os.ReadFile(filepath.Join(g.s.Dir(), name))
	if err != nil {
		t.Fatal(err)
	}
	resp, body := g.get(t, "/files/"+name, g.key)
	if resp.StatusCode != http.StatusOK || string(body) != string(want) {
		t.Fatalf("regular recording: %d (want 200, %d bytes)", resp.StatusCode, len(want))
	}
}

// --- SEC-4 residual: an in-root link entry must not be followed ---

// os.Root.Open keeps the open inside the root, but on Unix it FOLLOWS a
// symlink whose target is inside the root (containment is not no-follow).
// A link carrying a valid recording basename and pointing at another regular
// file in the SAME folder must still be refused: the bytes served under a
// recording name must come from that directory entry, never from an alias.
// The genuine target under its own name must keep being served (no
// over-refusal). On Windows Root.Open already refuses reparse points, so this
// test exercises the Unix follow behaviour; makeFileLink skips with a stated
// reason when the host grants no symlink-creation privilege.
func TestHandleFileRefusesInFolderRecordingLink(t *testing.T) {
	g := newSrvRig(t)
	target := "bb-20261027-12.ndjson"
	body := `{"v":1,"entry":"link-target"}` + "\n"
	if err := os.WriteFile(filepath.Join(g.s.Dir(), target), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "bb-20261027-11.ndjson" // valid shape, not the rig's current file
	// The link target is RELATIVE and inside the root: os.Root refuses
	// absolute and escaping links by itself, so only a relative in-root
	// target exercises the follow-vs-refuse behaviour under test.
	makeFileLink(t, filepath.Join(g.s.Dir(), name), target)

	resp, b := g.get(t, "/files/"+name, g.key)
	if resp.StatusCode != 404 {
		t.Fatalf("in-folder link entry: %d, want 404 (body %q)", resp.StatusCode, b)
	}
	if strings.Contains(string(b), "link-target") {
		t.Fatal("the link target's data was served under the link's name")
	}
	if !strings.Contains(string(b), "no such recording file") {
		t.Fatalf("refusal must carry the fixed message: %s", b)
	}
	// No over-refusal: the genuine file under its own name is still served.
	resp, b = g.get(t, "/files/"+target, g.key)
	if resp.StatusCode != http.StatusOK || string(b) != body {
		t.Fatalf("genuine target: %d (want 200 with its own bytes), body %q", resp.StatusCode, b)
	}
}

// The containment seam itself must refuse an in-root link leaf, not only
// links that escape the root (which Root.Open already refuses on every
// platform). The refusal error is not asserted to be errRecordingRefused:
// on Windows the no-follow open refuses first with its own error.
func TestOpenRecordingFileRefusesInFolderLink(t *testing.T) {
	root := t.TempDir()
	target := "bb-20261027-12.ndjson"
	if err := os.WriteFile(filepath.Join(root, target), []byte(`{"v":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "bb-20261027-11.ndjson"
	makeFileLink(t, filepath.Join(root, name), target) // relative, in-root
	if f, err := openRecordingFile(root, name); err == nil {
		f.Close()
		t.Fatal("an in-folder link was followed instead of refused")
	}
}
