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
	mu      sync.Mutex
	info    InstallInfo
	m       *update.Manifest // the verified manifest this download belongs to
	cancel  context.CancelFunc
	running bool // a download is in progress
	busy    bool // an Install is in progress
	gen     int  // bumped when a download starts or is thrown away; an old download's result is then ignored
}

// EnableInstall lets this Updater download and hand off updates. dir is the download folder; launch starts the installer.
// Without it the program only tells that an update exists.
func (u *Updater) EnableInstall(dir string, launch Launcher) {
	u.dl = &update.Downloader{Dir: dir, Current: u.fetcher.Current}
	u.launch = launch
}

// CanInstall tells the screens whether the Install buttons make sense.
func (u *Updater) CanInstall() bool { return u.channel != "" && u.dl != nil && u.launch != nil }

// Close stops a download that is running.
func (u *Updater) Close() {
	u.inst.mu.Lock()
	cancel := u.inst.cancel
	u.inst.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

var (
	errNoUpdate   = errors.New("There is no verified update to install. Check for updates first.")
	errBusy       = errors.New("A download or install is already running.")
	errNotReady   = errors.New("The update is not downloaded yet.")
	errNoInstall  = errors.New("Installing updates is not available in this build.")
	errFileChange = errors.New("The downloaded file changed after it was checked. It was not started. Download it again.")
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
func (u *Updater) StartDownload() error {
	if !u.CanInstall() {
		return errNoInstall
	}
	m := u.availableManifest()
	if m == nil {
		return errNoUpdate
	}
	u.inst.mu.Lock()
	defer u.inst.mu.Unlock()
	if u.inst.running || u.inst.busy {
		return errBusy
	}
	a := m.Assets[update.AssetSetup]
	ctx, cancel := context.WithCancel(context.Background())
	u.inst.gen++
	gen := u.inst.gen
	u.inst.cancel, u.inst.running, u.inst.m = cancel, true, m
	u.inst.info = InstallInfo{State: "downloading", Version: m.Version, Total: a.Size}
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
func (u *Updater) Install() error {
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
	u.inst.busy = true
	gen := u.inst.gen
	u.inst.mu.Unlock()
	defer func() { u.inst.mu.Lock(); u.inst.busy = false; u.inst.mu.Unlock() }()

	// The hash of a big file and the process start happen outside the lock, so status reads are never held up.
	got, ok := u.dl.Recheck(m, update.AssetSetup)
	if !ok {
		u.setInstall(gen, InstallInfo{State: "failed", Version: m.Version, Error: errFileChange.Error()})
		return errFileChange
	}
	if err := u.launch(got.Path, got.SHA256, m.Version); err != nil {
		u.setInstall(gen, InstallInfo{State: "failed", Version: m.Version, Error: failText(err)})
		return err
	}
	u.setInstall(gen, InstallInfo{State: "launched", Version: m.Version, Done: got.Size, Total: got.Size})
	return nil
}

func (u *Updater) setInstall(gen int, info InstallInfo) {
	u.inst.mu.Lock()
	defer u.inst.mu.Unlock()
	if u.inst.gen == gen {
		u.inst.info = info
	}
}
