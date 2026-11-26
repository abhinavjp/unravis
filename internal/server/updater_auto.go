package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"api-audit-explorer/internal/update"
	"api-audit-explorer/internal/version"
)

// Silent updates (docs/auto-updater/SILENT-UPDATE.md). While nobody is using Unravis it downloads a verified update, applies it
// without any window and starts again. While somebody is using it, it never interrupts: the bar says the update is ready and the
// person picks "Apply now" or "Later".

// presenceWindow is how long after the last sign of use the app still counts as in use. The page pings every 45 seconds while it is
// visible and was touched in the last five minutes.
const presenceWindow = 2 * time.Minute

// autoTick is how often the quiet-time check runs. One timer; each tick is a few comparisons.
var autoTick = time.Minute

// autoMaxTries is how many times one version is applied automatically before the program stops trying and leaves it to the person.
const autoMaxTries = 2

// Presence remembers when a person last used the page.
type Presence struct {
	mu   sync.Mutex
	last time.Time
}

// Touch records a sign of use now.
func (p *Presence) Touch() {
	p.mu.Lock()
	p.last = time.Now()
	p.mu.Unlock()
}

// Active says whether there was a sign of use within window of now.
func (p *Presence) Active(now time.Time, window time.Duration) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.last.IsZero() && now.Sub(p.last) < window
}

type autoState struct {
	mu     sync.Mutex
	tries  map[string]int // version -> automatic applies tried; also kept on disk, so a restart does not forget it
	loaded bool
	loops  []chan struct{} // one per StartAuto; each closes when its loop has ended
	// failedByAuto is set when an automatic apply was launched; only then may autoStep turn a launch failure back into "ready"
	// (a failure of the person's own Apply now stays on screen with its reason).
	failedByAuto bool
	// Failed automatic downloads wait longer each time (5 minutes, then 15, 60, and 6 hours at most), so a PC with no route to the
	// feed does not try every minute.
	dlFails int
	dlNext  time.Time
	// The same wait for the last look at the feed before a start: when it cannot be reached or its answer is refused, the next try
	// is later (5 minutes, then 15, 60, and 6 hours at most).
	rcFails int
	rcNext  time.Time
	// fileOKFor and fileOKAt remember that the downloaded file of that version was found intact (see fileStillThere).
	fileOKFor string
	fileOKAt  time.Time
	// A launch that never got a result (see expireStaleLaunch) makes the automatic apply wait the same 5 minutes, 15, 60, 6 hours
	// before it starts that version again. The try itself was already counted (bumpAutoTries), so autoMaxTries still ends it.
	lnFor   string
	lnFails int
	lnNext  time.Time
}

func dlBackoff(fails int) time.Duration {
	d := []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour}
	if fails <= 0 {
		return 0
	}
	if fails <= len(d) {
		return d[fails-1]
	}
	return 6 * time.Hour
}

// EnableSilentApply lets this Updater apply an update without a window. launch starts the setup program in its silent mode.
// canSilently says whether an apply would need no administrator approval (a prompt nobody is there to answer would only fail);
// busy says whether the app is doing work that must not be cut off (nil: never busy).
func (u *Updater) EnableSilentApply(launch Launcher, canSilently func() bool, busy func() bool) {
	u.silent, u.canSilently, u.busy = launch, canSilently, busy
}

// EnableSilentReopen sets the launcher used when a person presses Apply now: like the silent one, but the new version also opens
// its page in the browser. The automatic apply keeps the plain silent launcher (nobody is there to look at a new tab).
func (u *Updater) EnableSilentReopen(launch Launcher) { u.silentOpen = launch }

// Presence is the tracker the page pings.
func (u *Updater) Presence() *Presence { return &u.presence }

// mcpWindow is how long after its last request an MCP client (unravis --mcp) still counts as in use: an assistant works in bursts.
const mcpWindow = 5 * time.Minute

// AddBusy adds a check for work that must not be cut off (a running import, say).
func (u *Updater) AddBusy(f func() bool) {
	if f != nil {
		u.busyMu.Lock()
		u.extraBusy = append(u.extraBusy, f)
		u.busyMu.Unlock()
	}
}

// WorkBegin counts one request in flight (an import, export or capture runs inside one); call the returned func when it ends.
func (u *Updater) WorkBegin() func() {
	u.inflight.Add(1)
	return func() { u.inflight.Add(-1) }
}

// NoteMCP records a request from an MCP client.
func (u *Updater) NoteMCP() { u.mcp.Touch() }

// InUse says whether the app counts as in use now: a recent sign of use on the page, an MCP client at work, or work still running.
func (u *Updater) InUse() bool {
	now := time.Now()
	return u.presence.Active(now, presenceWindow) || u.mcp.Active(now, mcpWindow) || u.Busy()
}

// Busy is the shared work signal for background policies. Unlike InUse it does not extend a request's activity window.
func (u *Updater) Busy() bool {
	u.inst.mu.Lock()
	installing := u.inst.running || u.inst.busy
	u.inst.mu.Unlock()
	if installing || u.inflight.Load() > 0 || (u.busy != nil && u.busy()) {
		return true
	}
	u.busyMu.Lock()
	fs := append([]func() bool(nil), u.extraBusy...)
	u.busyMu.Unlock()
	for _, f := range fs {
		if f() {
			return true
		}
	}
	return false
}

// SetPowerTouch connects update downloads and installs to the owner's idle power policy.
func (u *Updater) SetPowerTouch(touch func()) {
	u.powerMu.Lock()
	u.powerTouch = touch
	u.powerMu.Unlock()
}

func (u *Updater) touchPower() {
	u.powerMu.RLock()
	touch := u.powerTouch
	u.powerMu.RUnlock()
	if touch != nil {
		touch()
	}
}

// autoCloseWait is how long Close waits for the quiet-time loop to end (RV-SRV-3). A step that is stuck in a slow call is left
// behind after this; the program is ending either way.
var autoCloseWait = 3 * time.Second

// StartAuto runs RunAuto in the background and remembers it, so Close can wait for it. The loop is registered before this returns:
// a Close that follows at once still sees it.
func (u *Updater) StartAuto(ctx context.Context) {
	done := make(chan struct{})
	u.auto.mu.Lock()
	u.auto.loops = append(u.auto.loops, done)
	u.auto.mu.Unlock()
	go func() {
		defer close(done)
		u.RunAuto(ctx)
	}()
}

// waitAuto waits for the loops StartAuto began, for at most d in all (the way fileJobs.shutdown waits).
func (u *Updater) waitAuto(d time.Duration) {
	u.auto.mu.Lock()
	loops := append([]chan struct{}(nil), u.auto.loops...)
	u.auto.mu.Unlock()
	timer := time.NewTimer(d)
	defer timer.Stop()
	for _, done := range loops {
		select {
		case <-done:
		case <-timer.C:
			return
		}
	}
}

// RunAuto is the quiet-time loop. It stops when ctx ends. It does nothing for a development build, while updates are off, or when
// automatic apply is switched off.
func (u *Updater) RunAuto(ctx context.Context) {
	if u.channel == "" {
		return
	}
	t := time.NewTicker(autoTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			u.autoStep()
		}
	}
}

// autoStep is one look: download when there is something to get, apply when nobody is using the app.
func (u *Updater) autoStep() {
	u.expireStaleLaunch(u.readApplyResultIfLaunched()) // nobody may be looking at the page: a stuck "launched" is let go here too
	st := u.currentSettings()
	if !st.Auto || !st.ApplyAuto || !u.CanInstall() || u.silent == nil {
		return
	}
	m := u.availableManifest()
	if m == nil {
		return
	}
	tries := u.autoTries(m.Version)
	if tries >= autoMaxTries {
		return // it did not work twice; the bar tells the person and they decide
	}
	u.inst.mu.Lock()
	state, ver := u.inst.info.State, u.inst.info.Version
	u.inst.mu.Unlock()
	switch {
	case state == "downloading":
		return
	case state == "ready" && ver == m.Version:
		u.auto.mu.Lock()
		u.auto.dlFails, u.auto.dlNext = 0, time.Time{}
		waitRC := time.Now().Before(u.auto.rcNext) || u.now().Before(u.auto.lnNext)
		u.auto.mu.Unlock()
		if waitRC {
			return // the feed could not confirm it a moment ago; no call every minute
		}
	case state == "failed" && ver == m.Version && u.fileStillThere(m):
		if !u.autoFailed() {
			return // the person's own Apply now failed: leave it on screen with its reason; nothing is downloaded again
		}
		// The file is fine: it was the start of the setup program that failed (a blocked or refused launch), not the download. Back
		// to "ready" so the next tick can try again, within the cap above; it is not a download failure and earns no download wait.
		u.auto.mu.Lock()
		u.auto.failedByAuto = false
		u.auto.mu.Unlock()
		u.inst.mu.Lock()
		if u.inst.info.State == "failed" && u.inst.info.Version == m.Version {
			u.inst.info = InstallInfo{State: "ready", Version: m.Version, Done: u.inst.info.Total, Total: u.inst.info.Total}
		}
		u.inst.mu.Unlock()
		return
	case state == "" || ver != m.Version || state == "failed":
		// A download is small next to the day and does not interrupt anyone, so it starts whether or not the app is in use.
		u.auto.mu.Lock()
		if state == "failed" && ver == m.Version && u.auto.dlNext.IsZero() {
			// first sight of this failure: count it and wait before trying again
			u.auto.dlFails++
			u.auto.dlNext = time.Now().Add(dlBackoff(u.auto.dlFails))
		}
		wait := time.Now().Before(u.auto.dlNext)
		if !wait {
			u.auto.dlNext = time.Time{}
		}
		u.auto.mu.Unlock()
		if !wait {
			_ = u.StartDownload()
		}
		return
	default:
		return // launched: a setup run is in progress or was closed by the person; leave it to the bar
	}
	if u.InUse() || (u.canSilently != nil && !u.canSilently()) {
		return
	}
	u.bumpAutoTries(m.Version) // written before the launch: a try that kills the app is still counted after the restart
	u.auto.mu.Lock()
	u.auto.failedByAuto = true
	u.auto.mu.Unlock()
	err := u.install(false)
	u.auto.mu.Lock()
	switch {
	case errors.Is(err, errRecheckOffline), errors.Is(err, errRecheckRefused):
		u.auto.rcFails++
		u.auto.rcNext = time.Now().Add(dlBackoff(u.auto.rcFails))
	default:
		u.auto.rcFails, u.auto.rcNext = 0, time.Time{}
	}
	u.auto.mu.Unlock()
	if errors.Is(err, errBusy) || errors.Is(err, errNotReady) || errors.Is(err, errNoUpdate) || errors.Is(err, errNoInstall) ||
		errors.Is(err, errRecheckHeld) || errors.Is(err, errRecheckOffline) || errors.Is(err, errRecheckRefused) {
		u.unbumpAutoTries(m.Version) // nothing was launched (the person pressed Apply now at the same moment, or the file is not ready): not a try
		u.auto.mu.Lock()
		u.auto.failedByAuto = false
		u.auto.mu.Unlock()
	} // quits this app through the setup program; the new (or, after a rollback, old) version starts by itself
}

// SetResultDir says where the setup program leaves the result of an unattended update. Status reports it until it is dismissed.
func (u *Updater) SetResultDir(dir string) { u.resultDir = dir }

// DismissResult removes the result once the person has seen it.
func (u *Updater) DismissResult() {
	if u.resultDir != "" {
		update.ClearApplyResult(u.resultDir)
	}
	u.inst.mu.Lock()
	u.inst.note = nil
	u.inst.mu.Unlock()
}

// readApplyResult is the note the setup program left, or nil. Call it with no lock held (a slow disk must not hold one up).
func (u *Updater) readApplyResult() *update.ApplyResult {
	if u.resultDir == "" {
		return nil
	}
	if r, ok := update.ReadApplyResult(u.resultDir); ok {
		return &r
	}
	return nil
}

// readApplyResultIfLaunched reads the note only when a launch is waiting for one, so the minute tick does not touch the disk otherwise.
func (u *Updater) readApplyResultIfLaunched() *update.ApplyResult {
	u.inst.mu.Lock()
	launched := u.inst.info.State == "launched"
	u.inst.mu.Unlock()
	if !launched {
		return nil
	}
	return u.readApplyResult()
}

// launchedLimit is how long a launched setup program may go without leaving a result before the program stops waiting for it.
// An update (download is already done) takes well under a minute; 20 minutes allows a slow disk, a security scan or a PC that went to
// sleep half-way, and is still short enough that a person is not left with a bar that never changes.
const launchedLimit = 20 * time.Minute

// launchTimeoutNote is the plain-words note for the person after a launch timed out, or nil. It is only about the "ready" state it
// left behind: any later step (a new download, a new launch) moves the state on and the note no longer applies. A note the late
// setup program wrote after all (res newer than the launch) says more than this one and is shown instead.
func (u *Updater) launchTimeoutNote(res *update.ApplyResult) *UpdateNotice {
	u.inst.mu.Lock()
	defer u.inst.mu.Unlock()
	n := u.inst.note
	if n == nil || u.inst.info.State != "ready" || u.inst.info.Version != n.To {
		return nil
	}
	if res != nil && res.At.After(u.inst.launchedAt) {
		return nil
	}
	c := *n
	return &c
}

// expireStaleLaunch ends the wait for a setup program that left no result (RV-SUP-1): it was killed, crashed, or the PC slept through
// it. The state goes from "launched" back to "ready" with a note, and the automatic apply may then try again after its wait.
// It changes state and a note only. It never starts anything, never writes a result and never reports an update as done: a start
// goes only through the normal Install path, which checks the file and the feed again.
//
// It applies only to the no-window launch (silent set): there the person has nothing open and the bar would say "being applied"
// forever. With a windowed installer "launched" means "the updater window is open", which a person may leave open for a long time and
// may press again at any moment, so it is left alone.
//
// res is the result note on disk (read by the caller with no lock held). A note written after the launch is that run's own: it stays
// the one thing that decides (Status turns a bad one into "failed"). A note from before the launch is an earlier run's and is ignored.
func (u *Updater) expireStaleLaunch(res *update.ApplyResult) {
	if u.silent == nil {
		return
	}
	at, ver, total, ok := u.staleLaunch(res)
	if !ok {
		return
	}
	if probe := u.setupAlive; probe != nil && probe() {
		return // the setup program is still running: it is working, not lost
	}
	u.inst.mu.Lock()
	// Look again: it may have moved on while the probe ran (another launch, a download, a check that dropped the offer), and the
	// run may have written its result in that window. The file is read once more under the lock, so a result that exists at the
	// moment of the flip always wins and nothing flips. (A small file read; nothing else takes inst.mu around a disk wait.)
	if now := u.readApplyResult(); now != nil && now.At.After(u.inst.launchedAt) {
		u.inst.mu.Unlock()
		return
	}
	if !u.inst.launchedAt.Equal(at) || u.inst.info.State != "launched" || u.inst.info.Version != ver || u.inst.running || u.inst.busy {
		u.inst.mu.Unlock()
		return
	}
	// launchedAt is kept: resultIsFromThisLaunch still needs it to call an older note old.
	u.inst.info = InstallInfo{State: "ready", Version: ver, Done: total, Total: total}
	u.inst.note = &UpdateNotice{Outcome: "failed", From: version.Version, To: ver, Message: launchTimeoutMessage}
	u.inst.mu.Unlock()
	u.auto.mu.Lock()
	u.auto.failedByAuto = false // this "ready" is not a launch failure that autoStep should turn back; the back-off below paces the retry
	if u.auto.lnFor != ver {
		u.auto.lnFor, u.auto.lnFails = ver, 0
	}
	u.auto.lnFails++
	u.auto.lnNext = u.now().Add(dlBackoff(u.auto.lnFails))
	u.auto.mu.Unlock()
}

// launchTimeoutMessage is shown to the person when a started update never reported back.
const launchTimeoutMessage = "The update has not reported back after 20 minutes. Unravis is still on the old version for now. You can try again with Apply now."

// staleLaunch says whether the launch is old enough and still without a result of its own.
func (u *Updater) staleLaunch(res *update.ApplyResult) (at time.Time, ver string, total int64, ok bool) {
	u.inst.mu.Lock()
	defer u.inst.mu.Unlock()
	if u.inst.info.State != "launched" || u.inst.running || u.inst.busy || u.inst.launchedAt.IsZero() {
		return
	}
	if u.now().Sub(u.inst.launchedAt) < launchedLimit {
		return
	}
	if res != nil && res.At.After(u.inst.launchedAt) {
		return // this run left its own note
	}
	return u.inst.launchedAt, u.inst.info.Version, u.inst.info.Total, true
}

// now is the clock of this updater (the real one unless a test sets it).
func (u *Updater) now() time.Time {
	if u.clock != nil {
		return u.clock()
	}
	return time.Now()
}

// The count of automatic applies per version is kept in auto-tries.json beside the settings. A try that closes the app and ends in a
// rollback restarts the program, and a counter held only in memory would start again from zero: the same failing update would be
// applied again every few minutes. Only the version on offer is kept; a newer version starts fresh.
type autoTriesFile struct {
	Version string `json:"version"`
	Tries   int    `json:"tries"`
}

func (u *Updater) autoTriesPath() string { return filepath.Join(u.dir, "auto-tries.json") }

func (u *Updater) autoTries(version string) int {
	u.auto.mu.Lock()
	defer u.auto.mu.Unlock()
	if !u.auto.loaded {
		u.auto.loaded = true
		if b, err := os.ReadFile(u.autoTriesPath()); err == nil && len(b) < 1024 {
			var f autoTriesFile
			if json.Unmarshal(b, &f) == nil && f.Version != "" && f.Tries > 0 {
				if u.auto.tries == nil {
					u.auto.tries = map[string]int{}
				}
				u.auto.tries[f.Version] = f.Tries
			}
		}
	}
	return u.auto.tries[version]
}

func (u *Updater) bumpAutoTries(version string) {
	n := u.autoTries(version) + 1
	u.auto.mu.Lock()
	defer u.auto.mu.Unlock()
	u.auto.tries = map[string]int{version: n}
	if b, err := json.Marshal(autoTriesFile{Version: version, Tries: n}); err == nil {
		if os.MkdirAll(u.dir, 0o700) == nil {
			_ = os.WriteFile(u.autoTriesPath(), b, 0o600)
		}
	}
}

// fileStillThere says the downloaded setup for m is still on disk and unchanged.
// A positive answer is remembered for ten minutes: the check reads and hashes the whole file (tens of MB), and the minute tick asks
// it every time while a failed Apply now stays on screen. The start itself (Install) always hashes the file again.
func (u *Updater) fileStillThere(m *update.Manifest) bool {
	u.auto.mu.Lock()
	if u.auto.fileOKFor == m.Version && time.Since(u.auto.fileOKAt) < 10*time.Minute {
		u.auto.mu.Unlock()
		return true
	}
	u.auto.mu.Unlock()
	_, ok := u.dl.Recheck(m, update.AssetSetup)
	if ok {
		u.auto.mu.Lock()
		u.auto.fileOKFor, u.auto.fileOKAt = m.Version, time.Now()
		u.auto.mu.Unlock()
	}
	return ok
}

func (u *Updater) autoFailed() bool {
	u.auto.mu.Lock()
	defer u.auto.mu.Unlock()
	return u.auto.failedByAuto
}

func (u *Updater) unbumpAutoTries(version string) {
	n := u.autoTries(version) - 1
	u.auto.mu.Lock()
	defer u.auto.mu.Unlock()
	if n <= 0 {
		u.auto.tries = map[string]int{}
		_ = os.Remove(u.autoTriesPath())
		return
	}
	u.auto.tries = map[string]int{version: n}
	if b, err := json.Marshal(autoTriesFile{Version: version, Tries: n}); err == nil {
		_ = os.WriteFile(u.autoTriesPath(), b, 0o600)
	}
}

// fileOKClear forgets a remembered "file is intact" answer (the file was found changed).
func (u *Updater) fileOKClear() {
	u.auto.mu.Lock()
	u.auto.fileOKFor, u.auto.fileOKAt = "", time.Time{}
	u.auto.mu.Unlock()
}
