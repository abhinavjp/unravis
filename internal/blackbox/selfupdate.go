package blackbox

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/update"
)

// Self-update of the Blackbox service (docs/auto-updater/SPEC.md section 8.1).
//
// The service checks the signed feed on a timer. On a newer signed version it downloads the Blackbox zip into a folder only
// administrators and the system can write, checks the zip against the signed manifest and the program inside it against the signed
// program hash, and starts that verified program with the "update" command. That command is the same code the Unravis "Update the
// recorder" button uses (service_update.go): it stops the service, swaps the program, starts it again and puts the old one back if
// the new one does not stay up. Settings, the read key and the recordings are never touched.

// Update modes in blackbox.json ("autoUpdate": {"mode": ...}).
const (
	UpdateInstall = "install" // check, download, verify and install (the default)
	UpdateNotify  = "notify"  // check and say so on the status page; install nothing
	UpdateOff     = "off"     // no network call at all
)

const (
	// MinUpdateMinutes and MaxUpdateMinutes bound "intervalMinutes". The lower bound cannot be lowered: a check is a network call.
	MinUpdateMinutes     = 30
	MaxUpdateMinutes     = 24 * 60
	DefaultUpdateMinutes = 30

	maxProgramSize = 200 << 20
	programName    = "unravis-blackbox.exe"
	pendingName    = "pending.json"

	// ManagedMarkerName is a file Unravis (its installer, or its install button) leaves in the program folder. It says Unravis updates
	// this Blackbox together with itself: Unravis pins the exact recorder program it ships, so a Blackbox that updated itself
	// separately would no longer match that pin. A marker file (not a settings key) keeps the settings file readable by older
	// recorder programs, which refuse keys they do not know.
	ManagedMarkerName = "managed-by-unravis"
)

// MarkManaged leaves the managed marker in the Blackbox program folder.
func MarkManaged(progDir string) error {
	return os.WriteFile(filepath.Join(progDir, ManagedMarkerName), []byte("Unravis updates this Blackbox together with itself.\r\n"), 0o644)
}

// AutoUpdate is the "autoUpdate" block of blackbox.json. A missing block means the defaults: install, every 30 minutes.
type AutoUpdate struct {
	Mode            string `json:"mode,omitempty"`
	IntervalMinutes int    `json:"intervalMinutes,omitempty"`
}

// UpdateMode is the mode in force.
func (c Config) UpdateMode() string {
	if c.AutoUpdate == nil || c.AutoUpdate.Mode == "" {
		return UpdateInstall
	}
	return c.AutoUpdate.Mode
}

// UpdateInterval is the time between checks.
func (c Config) UpdateInterval() time.Duration {
	if c.AutoUpdate == nil || c.AutoUpdate.IntervalMinutes == 0 {
		return DefaultUpdateMinutes * time.Minute
	}
	return time.Duration(c.AutoUpdate.IntervalMinutes) * time.Minute
}

func (a *AutoUpdate) validate() error {
	if a == nil {
		return nil
	}
	switch a.Mode {
	case "", UpdateInstall, UpdateNotify, UpdateOff:
	default:
		return invalidf(`"autoUpdate.mode" must be install, notify or off`)
	}
	if a.IntervalMinutes != 0 && (a.IntervalMinutes < MinUpdateMinutes || a.IntervalMinutes > MaxUpdateMinutes) {
		return invalidf(`"autoUpdate.intervalMinutes" must be %d to %d`, MinUpdateMinutes, MaxUpdateMinutes)
	}
	return nil
}

// UpdateStatus is what /status says about updates. It never carries a URL, a file path or an error text from the network.
type UpdateStatus struct {
	Mode    string     `json:"mode"`
	State   string     `json:"state"` // idle, available, skipped, downloading, installing, failed, off
	Latest  string     `json:"latest,omitempty"`
	Checked *time.Time `json:"checked,omitempty"`
	// Result is one plain sentence about the last check.
	Result string `json:"result,omitempty"`
}

// SelfUpdater is the update goroutine's logic. Every outside effect is a field, so tests run it with fakes.
type SelfUpdater struct {
	Mode     string
	Interval time.Duration
	Version  string
	// Dir is the update folder: staged files, the pending marker and the skip list. Only administrators and the system may write it.
	Dir string

	Check func(ctx context.Context) update.Result
	// Stage downloads and verifies the new program and returns its path and SHA-256.
	Stage func(ctx context.Context, m *update.Manifest) (path, sha string, err error)
	// Apply starts the verified program's update command.
	Apply func(path, sha string) error
	Log   func(format string, a ...any)
	Now   func() time.Time
	Rand  *rand.Rand
	// Managed (optional) says Unravis updates this Blackbox. It is asked before every check, because the marker can appear after the
	// service started (Unravis installs, then marks). A managed Blackbox checks nothing and installs nothing.
	Managed func() bool
	// Confirm is how long a freshly updated program must stay up before the update counts as good. The helper that installed it
	// rolls back when the program does not answer, so success is not decided at the first moment.
	Confirm time.Duration

	mu     sync.Mutex
	status UpdateStatus
}

type pending struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	At   time.Time `json:"at"`
}

func (u *SelfUpdater) logf(f string, a ...any) {
	if u.Log != nil {
		u.Log(f, a...)
	}
}

func (u *SelfUpdater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *SelfUpdater) tracker() *update.Tracker { return &update.Tracker{StateDir: u.Dir} }

func (u *SelfUpdater) set(state, latest, result string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := u.now().UTC()
	u.status = UpdateStatus{Mode: u.Mode, State: state, Latest: latest, Checked: &now, Result: result}
}

// Status is the current update status for /status.
func (u *SelfUpdater) Status() *UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.status
	if s.Mode == "" {
		s.Mode = u.Mode
		s.State = "idle"
		if u.Mode == UpdateOff {
			s.State = "off"
		}
	}
	return &s
}

// Reconcile is called once when the service starts. A pending marker means the last run asked for an update: if this program is
// the version it asked for, the update worked; if not, it failed (and was rolled back), so that version is skipped and not tried
// again until a newer one appears.
func (u *SelfUpdater) Reconcile(ctx context.Context) {
	path := filepath.Join(u.Dir, pendingName)
	var p pending
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if len(data) > 4096 || json.Unmarshal(data, &p) != nil {
		_ = os.Remove(path)
		return
	}
	if p.To == u.Version {
		if u.Confirm > 0 {
			t := time.NewTimer(u.Confirm)
			select {
			case <-ctx.Done():
				t.Stop()
				return // stopped early: the marker stays and the next start decides
			case <-t.C:
			}
		}
		u.logf("update to %s finished", p.To)
	} else {
		u.logf("update to %s did not take; staying on %s and not trying %s again", p.To, u.Version, p.To)
		u.tracker().Skip(p.To)
	}
	_ = os.Remove(path)
	u.cleanStage()
}

// cleanStage removes staged programs and downloaded zips. It touches only names this updater makes.
func (u *SelfUpdater) cleanStage() {
	ents, err := os.ReadDir(u.Dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		n := e.Name()
		switch {
		case e.IsDir() && strings.HasPrefix(n, "stage-"):
			_ = os.RemoveAll(filepath.Join(u.Dir, n))
		case e.Type().IsRegular() && (strings.HasPrefix(n, ".part-") || (strings.HasPrefix(n, "unravis-blackbox-v") && strings.HasSuffix(n, ".zip"))):
			_ = os.Remove(filepath.Join(u.Dir, n))
		}
	}
}

// CheckOnce makes one check and, in install mode, the whole update. It returns the result of the check so the loop can back off.
func (u *SelfUpdater) CheckOnce(ctx context.Context) update.Result {
	if u.Mode == UpdateOff {
		return update.Result{Status: update.StatusLatest}
	}
	if u.Managed != nil && u.Managed() {
		u.set("managed", "", "This Blackbox is updated together with Unravis.")
		return update.Result{Status: update.StatusLatest}
	}
	res := u.Check(ctx)
	if ctx.Err() != nil {
		return res
	}
	if res.Status != update.StatusAvailable || res.Manifest == nil {
		u.set("idle", "", resultSentence(res))
		return res
	}
	v := res.Manifest.Version
	if u.tracker().Skipped(v) {
		u.set("skipped", v, "Version "+v+" failed on this server before, so it is not offered again.")
		return res
	}
	if u.Mode == UpdateNotify {
		u.set("available", v, "Version "+v+" is available. Automatic install is off.")
		return res
	}
	u.set("downloading", v, "Downloading version "+v+".")
	path, sha, err := u.Stage(ctx, res.Manifest)
	if err != nil {
		u.logf("update %s: could not prepare it: %v", v, err)
		u.set("failed", v, "Could not prepare version "+v+": "+err.Error())
		return res
	}
	pend, _ := json.Marshal(pending{From: u.Version, To: v, At: u.now().UTC()})
	if err := os.WriteFile(filepath.Join(u.Dir, pendingName), pend, 0o600); err != nil {
		u.set("failed", v, "Could not write the update marker; nothing was changed.")
		return res
	}
	u.set("installing", v, "Installing version "+v+". The recorder restarts for a few seconds.")
	if err := u.Apply(path, sha); err != nil {
		_ = os.Remove(filepath.Join(u.Dir, pendingName))
		u.logf("update %s: could not start the update: %v", v, err)
		u.set("failed", v, "Could not start the update of version "+v+": "+err.Error())
		return res
	}
	u.logf("update %s started", v)
	return res
}

func resultSentence(r update.Result) string {
	switch r.Status {
	case update.StatusLatest:
		return "Up to date."
	case update.StatusUnreachable:
		return "Could not reach the update feed."
	case update.StatusRejected:
		return "The update feed was refused (it did not pass the signature checks)."
	case update.StatusDev:
		return "A development build does not check for updates."
	}
	return "Checked."
}

// Run checks until ctx ends. The first check waits a short random time, so a fleet restarted together does not call at once.
func (u *SelfUpdater) Run(ctx context.Context) {
	u.Reconcile(ctx)
	if u.Mode == UpdateOff {
		u.set("off", "", "Automatic update is off.")
		return
	}
	rnd := u.Rand
	if rnd == nil {
		rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	delay := time.Duration(60+rnd.Intn(240)) * time.Second
	failures := 0
	for {
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		res := u.CheckOnce(ctx)
		if res.Backoff {
			failures++
		} else {
			failures = 0
		}
		delay = update.NextDelay(u.Interval, failures, res.RetryIn, rnd)
	}
}

// NewServiceUpdater wires the real update path for the Blackbox service whose program is exe. It returns nil, with the reason, when no
// update can be trusted on this server (no trusted keys in this build, or a development build); the service then runs as before.
func NewServiceUpdater(cfg Config, version, exe string, logf func(string, ...any)) (*SelfUpdater, error) {
	if cfg.UpdateMode() == UpdateOff {
		return nil, errors.New("automatic update is off")
	}
	v, err := update.ParseVersion(version)
	if err != nil {
		return nil, errors.New("a development build does not update itself")
	}
	keys, err := update.TrustedKeys()
	if err != nil {
		return nil, fmt.Errorf("no trusted update keys: %w", err)
	}
	dir := filepath.Join(filepath.Dir(exe), "updates")
	fetcher := &update.Fetcher{Keys: keys, Build: update.ChannelOf(v), Current: v.String(), StatePath: filepath.Join(dir, "state.json")}
	dl := &update.Downloader{Dir: dir, Current: v.String()}
	u := &SelfUpdater{
		Mode: cfg.UpdateMode(), Interval: cfg.UpdateInterval(), Version: v.String(), Dir: dir, Log: logf,
		Check: func(ctx context.Context) update.Result {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return update.Result{Status: update.StatusUnreachable, Detail: "no update folder"}
			}
			return fetcher.Check(ctx)
		},
		Stage: func(ctx context.Context, m *update.Manifest) (string, string, error) {
			a, ok := m.Assets[update.AssetBlackbox]
			if !ok || !strings.HasPrefix(a.Name, "unravis-blackbox-v") || !strings.HasSuffix(a.Name, ".zip") {
				return "", "", errors.New("the release has no Blackbox package")
			}
			got, err := dl.Download(ctx, m, update.AssetBlackbox, nil)
			if err != nil {
				return "", "", err
			}
			return stageFromZip(got.Path, filepath.Join(dir, "stage-"+m.Version), m.BlackboxExeSHA256)
		},
		Apply:   func(path, sha string) error { return update.StartVerified(path, sha, "update", "--expect-sha256", sha) },
		Confirm: 2 * time.Minute,
	}
	// Managed by Unravis: updated with it, unless the settings ask for self-update by name.
	if cfg.AutoUpdate == nil {
		marker := filepath.Join(filepath.Dir(exe), ManagedMarkerName)
		u.Managed = func() bool { _, serr := os.Stat(marker); return serr == nil }
	}
	return u, nil
}

// stageFromZip extracts the one program in the verified zip into stageDir and returns it only if its SHA-256 is want, the program
// hash the signed manifest carries.
func stageFromZip(zipPath, stageDir, want string) (string, string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", "", errors.New("the Blackbox package cannot be opened")
	}
	defer zr.Close()
	var found *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, programName) {
			if found != nil {
				return "", "", errors.New("the Blackbox package has more than one program")
			}
			found = f
		}
	}
	if found == nil || found.UncompressedSize64 == 0 || found.UncompressedSize64 > maxProgramSize {
		return "", "", errors.New("the Blackbox package does not hold the program")
	}
	if err := os.RemoveAll(stageDir); err != nil {
		return "", "", errors.New("could not clear the staging folder")
	}
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return "", "", errors.New("could not make the staging folder")
	}
	rc, err := found.Open()
	if err != nil {
		return "", "", errors.New("the program in the package cannot be read")
	}
	defer rc.Close()
	dst := filepath.Join(stageDir, programName)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", "", errors.New("could not write the program")
	}
	n, cerr := io.Copy(out, io.LimitReader(rc, maxProgramSize+1))
	if cerr == nil {
		cerr = out.Close()
	} else {
		out.Close()
	}
	if cerr != nil || n > maxProgramSize {
		_ = os.RemoveAll(stageDir)
		return "", "", errors.New("could not write the program")
	}
	got, err := fileSHA256Hex(dst)
	if err != nil || !strings.EqualFold(got, want) {
		_ = os.RemoveAll(stageDir)
		return "", "", errors.New("the program in the package is not the one the signed release names")
	}
	return dst, strings.ToLower(got), nil
}

func fileSHA256Hex(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256Hex(data), nil
}

// DropStalePending removes a pending update marker beside exe without judging it. The service calls it when self-update cannot run
// (off, development build, no keys), so an old marker is not read later, after self-update is switched on, and wrongly skip a version.
func DropStalePending(exe string) {
	_ = os.Remove(filepath.Join(filepath.Dir(exe), "updates", pendingName))
}
