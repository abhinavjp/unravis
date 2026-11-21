package server

import (
	"context"
	"sync"
	"time"

	"api-audit-explorer/internal/update"
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
	mu    sync.Mutex
	tries map[string]int // version -> automatic applies that did not end the app
	// Failed automatic downloads wait longer each time (5 minutes, then 15, 60, and 6 hours at most), so a PC with no route to the
	// feed does not try every minute.
	dlFails int
	dlNext  time.Time
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

// Presence is the tracker the page pings.
func (u *Updater) Presence() *Presence { return &u.presence }

// InUse says whether the app counts as in use now: a recent sign of use on the page, or work that must not be cut off.
func (u *Updater) InUse() bool {
	if u.presence.Active(time.Now(), presenceWindow) {
		return true
	}
	return u.busy != nil && u.busy()
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
	st := u.currentSettings()
	if !st.Auto || !st.ApplyAuto || !u.CanInstall() || u.silent == nil {
		return
	}
	m := u.availableManifest()
	if m == nil {
		return
	}
	u.auto.mu.Lock()
	tries := u.auto.tries[m.Version]
	u.auto.mu.Unlock()
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
		u.auto.mu.Unlock()
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
	u.auto.mu.Lock()
	if u.auto.tries == nil {
		u.auto.tries = map[string]int{}
	}
	u.auto.tries[m.Version]++
	u.auto.mu.Unlock()
	_ = u.Install() // quits this app through the setup program; the new (or, after a rollback, old) version starts by itself
}

// SetResultDir says where the setup program leaves the result of an unattended update. Status reports it until it is dismissed.
func (u *Updater) SetResultDir(dir string) { u.resultDir = dir }

// DismissResult removes the result once the person has seen it.
func (u *Updater) DismissResult() {
	if u.resultDir != "" {
		update.ClearApplyResult(u.resultDir)
	}
}
