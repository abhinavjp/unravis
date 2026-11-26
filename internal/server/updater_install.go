package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"api-audit-explorer/internal/update"
)

// Download and hand-off (docs/auto-updater/SPEC.md section 7). Nothing here runs by itself: a person presses "Update now" to
// download, and presses "Install" to open the installer. The downloaded file is checked against the signed manifest twice
// (after download and again right before it is started, with the file held open), and the installer checks itself once more
// (--expect-sha256).

// InstallInfo is the download and install step of an available update.
type InstallInfo struct {
	State   string `json:"state"` // idle, downloading, ready, failed or launched
	Version string `json:"version"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"`
	Error   string `json:"error,omitempty"` // plain words; never a response body or a path
}

// Launcher starts the installer for a verified file.
type Launcher func(path, sha256hex, version string) error

type installState struct {
	mu         sync.Mutex
	info       InstallInfo
	m          *update.Manifest // the verified manifest this download belongs to
	cancel     context.CancelFunc
	running    bool          // a download is in progress
	busy       bool          // an Install is in progress
	launchedAt time.Time     // when the last setup launch succeeded; a result note older than this belongs to an earlier run
	gen        int           // bumped when a download starts or is thrown away; an old download's result is then ignored
	note       *UpdateNotice // set when a launch timed out (expireStaleLaunch); cleared on dismiss, a new download or a new launch
}

// EnableInstall lets this Updater download and hand off updates. dir is the download folder; launch starts the installer.
// Without it the program only tells that an update exists.
func (u *Updater) EnableInstall(dir string, launch Launcher) {
	u.dl = &update.Downloader{Dir: dir, Current: u.fetcher.Current}
	u.launch = launch
}

// CanInstall tells the screens whether the Install buttons make sense.
func (u *Updater) CanInstall() bool { return u.channel != "" && u.dl != nil && u.launch != nil }

// Close stops a download that is running, then waits (at most autoCloseWait) for the quiet-time loop to end. The caller cancels
// the loop's context first; Close does not.
func (u *Updater) Close() {
	u.inst.mu.Lock()
	cancel := u.inst.cancel
	u.inst.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	u.waitAuto(autoCloseWait)
}

var (
	errNoUpdate  = errors.New("There is no verified update to install. Check for updates first.")
	errBusy      = errors.New("A download or install is already running.")
	errNotReady  = errors.New("The update is not downloaded yet.")
	errNoInstall = errors.New("Installing updates is not available in this build.")
	// The last look at the feed before a start (the app's own Apply now and the automatic apply both ask).
	errRecheckHeld    = errors.New("The publisher has put this update on hold. Nothing was started.")
	errRecheckOffline = errors.New("Could not reach the update feed to confirm this update is still safe. Nothing was started; it will try again.")
	errRecheckRefused = errors.New("The update feed's answer did not pass its checks, so this update was not started.")
	errStarting       = errors.New("The update is already starting. Give it a few seconds; Unravis closes and starts again by itself.") //nolint:staticcheck // ST1005: shown to the person as a sentence
	errFileChange     = errors.New("The downloaded file changed after it was checked. It was not started. Download it again.")
)

// availableManifest is the newest verified manifest that is still valid. A later check that failed (offline, bad signature) does
// not erase it: the file already on disk was verified against it. An expired manifest is never used.
func (u *Updater) availableManifest() *update.Manifest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.validAvail()
}

func (u *Updater) validAvail() *update.Manifest { // call with u.mu held
	if u.avail == nil || !time.Now().Before(u.avail.Expires) {
		return nil
	}
	return u.avail
}

func sameSetup(a, b *update.Manifest) bool {
	x, y := a.Assets[update.AssetSetup], b.Assets[update.AssetSetup]
	return a.Version == b.Version && x == y
}

// noteCheck is called after every check. A new manifest for the same file is adopted; a different one (a newer version, or
// "latest" now) throws the old download away, so a stale download never runs or hides the new one.
func (u *Updater) noteCheck(m *update.Manifest) {
	u.inst.mu.Lock()
	defer u.inst.mu.Unlock()
	if u.inst.m == nil {
		return
	}
	if m != nil && sameSetup(u.inst.m, m) {
		u.inst.m = m
		return
	}
	if u.inst.cancel != nil {
		u.inst.cancel()
	}
	u.inst.gen++
	u.inst.running, u.inst.m, u.inst.info = false, nil, InstallInfo{}
}

// installInfo is the state for the version the last check found (an older download state is not shown).
func (u *Updater) installInfo(latest string) *InstallInfo {
	u.inst.mu.Lock()
	defer u.inst.mu.Unlock()
	if u.inst.info.State == "" || u.inst.info.Version != latest {
		return nil
	}
	c := u.inst.info
	return &c
}

// StartDownload starts downloading the setup program of the version the last check found. It returns at once.
// It takes u.mu and then inst.mu (the order Status uses) and keeps both while it records the download, so a check that finds a
// newer version meanwhile either runs before it (and is used) or after it (and noteCheck then sees this download and drops it).
func (u *Updater) StartDownload() error {
	if !u.CanInstall() {
		return errNoInstall
	}
	u.mu.Lock()
	m := u.validAvail()
	if m == nil {
		u.mu.Unlock()
		return errNoUpdate
	}
	u.inst.mu.Lock()
	if u.inst.running || u.inst.busy {
		u.inst.mu.Unlock()
		u.mu.Unlock()
		return errBusy
	}
	a := m.Assets[update.AssetSetup]
	ctx, cancel := context.WithCancel(context.Background())
	u.inst.gen++
	gen := u.inst.gen
	u.inst.cancel, u.inst.running, u.inst.m = cancel, true, m
	u.inst.info = InstallInfo{State: "downloading", Version: m.Version, Total: a.Size}
	u.inst.note = nil
	u.inst.mu.Unlock()
	u.mu.Unlock()
	u.touchPower()
	go u.runDownload(ctx, gen, m)
	return nil
}

func (u *Updater) runDownload(ctx context.Context, gen int, m *update.Manifest) {
	got, err := u.dl.Download(ctx, m, update.AssetSetup, func(done, total int64) {
		u.inst.mu.Lock()
		if u.inst.gen == gen {
			u.inst.info.Done = done
		}
		u.inst.mu.Unlock()
	})
	u.inst.mu.Lock()
	defer u.inst.mu.Unlock()
	if u.inst.gen != gen {
		return // thrown away while it ran
	}
	u.inst.running = false
	if err != nil {
		u.inst.info = InstallInfo{State: "failed", Version: m.Version, Total: u.inst.info.Total, Error: failText(err)}
		return
	}
	u.dl.Prune(m.Assets[update.AssetSetup].Name)
	u.inst.info = InstallInfo{State: "ready", Version: m.Version, Done: got.Size, Total: got.Size}
}

// failText is a message that is safe to show: the Downloader writes plain sentences without paths or bodies.
func failText(err error) string {
	s := err.Error()
	if s == "" || len(s) > 200 {
		return "The download failed."
	}
	return s
}

// Install starts the installer for the downloaded file. The person pressed the button; nothing else calls this. It may be pressed
// again after "launched" (the person closed the installer window without updating).
func (u *Updater) Install() error { return u.install(true) }

// install is Install; byPerson is false for the automatic apply, which must not open a browser page afterwards.
func (u *Updater) install(byPerson bool) error {
	if !u.CanInstall() {
		return errNoInstall
	}
	m := u.availableManifest()
	if m == nil {
		return errNoUpdate
	}
	u.inst.mu.Lock()
	if u.inst.running || u.inst.busy {
		u.inst.mu.Unlock()
		return errBusy
	}
	if (u.inst.info.State != "ready" && u.inst.info.State != "launched") || u.inst.m == nil || !sameSetup(u.inst.m, m) {
		u.inst.mu.Unlock()
		return errNotReady
	}
	if u.silent != nil && u.inst.info.State == "launched" && u.now().Sub(u.inst.launchedAt) < startingGrace {
		u.inst.mu.Unlock()
		return errStarting // a second setup now could start the app twice
	}
	u.inst.busy = true
	gen := u.inst.gen
	u.inst.mu.Unlock()
	defer func() { u.inst.mu.Lock(); u.inst.busy = false; u.inst.mu.Unlock() }()
	u.touchPower()

	// The hash of a big file happens outside the lock, so status reads are never held up. It runs first, so the last look at the
	// feed is the final step before the start.
	got, ok := u.dl.Recheck(m, update.AssetSetup)
	if !ok {
		u.fileOKClear()
		u.setInstall(gen, InstallInfo{State: "failed", Version: m.Version, Error: errFileChange.Error()})
		return errFileChange
	}
	// Ask the feed once more right before the start: a pause or a withdrawal that came out while the file was downloading, or since
	// the last check, must win. Any answer other than "this very file is still on offer" starts nothing (fail closed).
	m, err := u.recheckFeed(m)
	if err != nil {
		return err
	}
	u.inst.mu.Lock()
	changed := u.inst.gen != gen
	u.inst.mu.Unlock()
	if changed {
		return errNotReady // a check during the last look replaced or withdrew the offer
	}
	launch := u.launch
	if u.silent != nil {
		launch = u.silent // no window: the setup program quits this app, updates and starts it again
		if byPerson && u.silentOpen != nil {
			launch = u.silentOpen // and opens the new page, so the page the person pressed the button on is not left behind
		}
	}
	if err := launch(got.Path, got.SHA256, m.Version); err != nil {
		u.setInstall(gen, InstallInfo{State: "failed", Version: m.Version, Error: failText(err)})
		return err
	}
	u.setInstall(gen, InstallInfo{State: "launched", Version: m.Version, Done: got.Size, Total: got.Size})
	u.inst.mu.Lock()
	u.inst.launchedAt = u.now()
	u.inst.note = nil
	u.inst.mu.Unlock()
	return nil
}

func (u *Updater) setInstall(gen int, info InstallInfo) {
	u.inst.mu.Lock()
	defer u.inst.mu.Unlock()
	if u.inst.gen == gen {
		u.inst.info = info
	}
}

// startingGrace is how long after a silent setup launch another press is refused (no window to close, so no "closed it without updating" case): the first setup is still closing the app.
const startingGrace = 20 * time.Second

// recheckTimeout bounds the last look at the feed before a setup is started.
const recheckTimeout = 45 * time.Second

// heldError is errRecheckHeld with the publisher's reason in plain words.
type heldError struct{ detail string }

func (e heldError) Error() string {
	if e.detail == "" {
		return errRecheckHeld.Error()
	}
	return "The publisher has put this update on hold: " + e.detail + ". Nothing was started."
}
func (e heldError) Is(target error) bool { return target == errRecheckHeld }

// recheckFeed asks the feed again and returns the manifest to start, or the reason nothing may start. Call with no lock held.
func (u *Updater) recheckFeed(m *update.Manifest) (*update.Manifest, error) {
	ctx, cancel := context.WithTimeout(context.Background(), recheckTimeout)
	defer cancel()
	res, ok := u.checkOnce(ctx)
	if !ok {
		return nil, errRecheckOffline
	}
	switch res.Status {
	case update.StatusAvailable:
		if res.Manifest != nil && sameSetup(m, res.Manifest) {
			return res.Manifest, nil
		}
		return nil, errNotReady // a newer release replaced it; the old download was dropped
	case update.StatusLatest:
		if res.Held && res.ViaMirror {
			return nil, errRecheckOffline // only the backup address answered: not a hold by the publisher
		}
		if res.Held {
			return nil, heldError{res.Detail}
		}
		return nil, errNoUpdate
	case update.StatusRejected:
		return nil, errRecheckRefused
	}
	return nil, errRecheckOffline
}
