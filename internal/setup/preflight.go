package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"api-audit-explorer/internal/blackbox"
)

// CheckStatus is the sticker of a preflight line.
type CheckStatus string

const (
	CheckReady  CheckStatus = "ready"
	CheckPartly CheckStatus = "partly" // a warning: setup can go on
	CheckFailed CheckStatus = "failed" // setup cannot go on until it is fixed
)

// Check is one preflight line: what was checked, the sticker, what was found and a plain fix.
type Check struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail"`
	Fix    string      `json:"fix,omitempty"`
}

// PreflightInput is what preflight looks at.
type PreflightInput struct {
	Op             Op
	Opts           Options
	Old            *Installed
	AllowDowngrade bool
	// Silent is a run without a window: nobody can be asked to close Unravis.
	Silent bool
}

// Blocked reports whether any check failed.
func Blocked(checks []Check) bool {
	for _, c := range checks {
		if c.Status == CheckFailed {
			return true
		}
	}
	return false
}

// MinAppFree is the free space the program folder needs (installer-and-releases.md section 2).
const MinAppFree = 100 << 20

// Preflight checks this PC before anything changes. It changes nothing.
func (r *Runner) Preflight(in PreflightInput) []Check {
	var out []Check
	add := func(c Check) { out = append(out, c) }
	sys := r.Sys
	opts := in.Opts
	if in.Op != OpInstall && in.Old != nil {
		opts = in.Old.State.Options()
		opts.CloseRunningApp = in.Opts.CloseRunningApp
		if opts.Location == "" {
			opts.Location = in.Old.Location
		}
	}
	if opts.Scope == "" {
		opts.Scope = ScopeMachine
	}
	if opts.Location == "" {
		opts.Location = DefaultLocation(opts.Scope, r.Dirs)
	}
	if c, err := opts.Validated(r.Dirs, sys.paths()); err == nil && in.Op == OpInstall {
		opts = c // the folders as Windows will read them
	}
	l := NewLayout(opts, r.Dirs)

	add(checkWindows(sys.Mach))

	if in.Op != OpUninstall {
		if r.Payload == nil {
			add(Check{ID: "payload", Title: "Setup program", Status: CheckFailed, Detail: "This setup program has no program files inside.", Fix: "Download the setup program again."})
		} else if err := r.Payload.Verify(); err != nil {
			add(Check{ID: "payload", Title: "Setup program", Status: CheckFailed, Detail: plainError(err), Fix: "Download the setup program again."})
		} else {
			add(Check{ID: "payload", Title: "Setup program", Status: CheckReady, Detail: "Unravis " + r.Payload.Manifest.Version + ", checked."})
		}
	}

	if sys.Lock != nil {
		if release, err := sys.Lock(); err != nil {
			add(Check{ID: "other-setup", Title: "Other setup", Status: CheckFailed, Detail: "Another Unravis setup is already running.", Fix: "Finish or close the other setup window, then try again."})
		} else {
			release()
		}
	}

	add(r.checkExisting(in, opts))

	if c, ok := r.checkRunningApp(in, l, opts); ok {
		add(c)
	}

	if opts.Blackbox != nil && in.Op == OpInstall {
		st, err := sys.Svc.State(blackbox.ServiceName)
		switch {
		case err != nil:
			add(Check{ID: "blackbox-service", Title: "Blackbox service", Status: CheckFailed, Detail: "Windows could not be asked about services: " + err.Error() + ".", Fix: "Restart the PC, then run setup again."})
		case st != blackbox.SvcNotInstalled:
			add(Check{ID: "blackbox-service", Title: "Blackbox service", Status: CheckFailed, Detail: "A Blackbox (" + blackbox.ServiceName + ") is already installed on this PC.",
				Fix: "Keep that Blackbox and untick “Add the Blackbox”, or remove it first."})
		default:
			add(Check{ID: "blackbox-service", Title: "Blackbox service", Status: CheckReady, Detail: "No Blackbox is installed yet."})
		}
	}

	if b := sys.Mach.Browser(); b != "" {
		add(Check{ID: "browser", Title: "Browser", Status: CheckReady, Detail: browserName(b) + " is installed."})
	} else {
		add(Check{ID: "browser", Title: "Browser", Status: CheckPartly, Detail: "Neither Microsoft Edge nor Google Chrome was found. Unravis opens in one of them.",
			Fix: "Install Microsoft Edge or Google Chrome before you open Unravis."})
	}
	if sys.Mach.WebView2() {
		add(Check{ID: "webview2", Title: "Setup window", Status: CheckReady, Detail: "WebView2 is installed."})
	} else {
		add(Check{ID: "webview2", Title: "Setup window", Status: CheckReady, Detail: "WebView2 is not installed; this window opens in the browser instead. Nothing to do."})
	}

	if in.Op != OpUninstall {
		add(checkFree("disk-app", "Space for the program", sys.Mach, l.InstallDir, MinAppFree, "100 MB"))
		if opts.Blackbox != nil {
			add(checkFree("disk-blackbox", "Space for the recordings", sys.Mach, opts.Blackbox.Folder, blackbox.MinInstallFree, "1 GB"))
			if in.Op == OpInstall {
				cfg, _ := opts.BlackboxConfig()
				if err := sys.Mach.PortFree(cfg.Bind, cfg.Port); err != nil {
					add(Check{ID: "port", Title: fmt.Sprintf("Blackbox port %d", cfg.Port), Status: CheckFailed, Detail: fmt.Sprintf("Port %d is in use by another program.", cfg.Port),
						Fix: "Choose another port in the Blackbox options."})
				} else {
					add(Check{ID: "port", Title: fmt.Sprintf("Blackbox port %d", cfg.Port), Status: CheckReady, Detail: "Free."})
				}
				add(checkRecordingsFolder(sys.Mach, opts.Blackbox.Folder))
			}
		}
	}

	add(checkAdmin(sys.Mach, sys.Svc, in, opts))

	if sys.Mach.PendingReboot() {
		add(Check{ID: "reboot", Title: "Restart", Status: CheckPartly, Detail: "Windows is waiting for a restart from an earlier update.", Fix: "Setup can go on. If a step fails, restart the PC and run setup again."})
	}

	elevated := NeedsAdminFor(sys.Svc, opts) || in.Op == OpUninstall && (in.Old != nil && in.Old.Root == HKLM)
	if _, err := os.Stat(filepath.Join(r.journalDir(elevated), JournalName)); err == nil {
		add(Check{ID: "journal", Title: "Earlier setup", Status: CheckPartly, Detail: "An earlier setup did not finish. Setup puts back what it changed before it starts."})
	}

	if opts.Scope == ScopeMachine && in.Op == OpInstall {
		if c, ok := r.checkSharedFolder(l); ok {
			add(c)
		}
	}
	if opts.Scope == ScopeMachine && in.Op != OpUninstall {
		if c, ok := r.checkMachineFolder(l.InstallDir, in.Old); ok {
			add(c)
		}
	} else if in.Op == OpInstall && in.Old == nil {
		if c, ok := checkInstallFolder(l.InstallDir); ok {
			add(c)
		}
	}
	if in.Op == OpInstall && opts.ReadKeyFile != "" && opts.Blackbox != nil {
		if _, err := os.Lstat(opts.ReadKeyFile); err == nil {
			add(Check{ID: "read-key-file", Title: "Read key file", Status: CheckFailed, Detail: opts.ReadKeyFile + " is already there. Setup does not replace a file.",
				Fix: "Choose a file name that does not exist yet."})
		}
	}
	return out
}

func checkWindows(m Machine) Check {
	c := Check{ID: "windows", Title: "Windows"}
	v, err := m.Windows()
	if err != nil {
		c.Status, c.Detail, c.Fix = CheckFailed, "The Windows version could not be read.", "Run setup on Windows 10 or 11, 64-bit."
		return c
	}
	name := "Windows 10"
	switch {
	case v.Server:
		name = "Windows Server"
	case v.Major == 10 && v.Build >= 22000:
		name = "Windows 11"
	case v.Major < 10:
		name = fmt.Sprintf("Windows %d.%d", v.Major, v.Minor)
	}
	bits := "64-bit"
	if !v.Is64 {
		bits = "32-bit"
	}
	c.Title = name + " " + bits
	switch {
	case !v.Is64:
		c.Status, c.Detail, c.Fix = CheckFailed, "Unravis needs 64-bit Windows.", "Install Unravis on a 64-bit PC."
	case v.Major < 10 || v.Major == 10 && v.Minor == 0 && v.Build < 17763:
		c.Status, c.Detail, c.Fix = CheckFailed, fmt.Sprintf("Build %d is too old. Unravis needs Windows 10 version 1809 or later, or Windows Server 2019 or later.", v.Build),
			"Update Windows, then run setup again."
	default:
		c.Status, c.Detail = CheckReady, fmt.Sprintf("Build %d.", v.Build)
	}
	return c
}

func (r *Runner) checkExisting(in PreflightInput, opts Options) Check {
	c := Check{ID: "existing", Title: "Earlier install", Status: CheckReady}
	old := in.Old
	if old == nil {
		if in.Op != OpInstall {
			c.Status, c.Detail, c.Fix = CheckFailed, "Unravis is not installed on this PC.", "Run setup without --upgrade, --repair or --uninstall to install it."
			return c
		}
		c.Detail = "None. This is a new install."
		return c
	}
	where := fmt.Sprintf("Unravis %s is installed in %s", old.Version, old.Location)
	switch in.Op {
	case OpInstall:
		if in.Opts.Scope != "" && in.Opts.Scope != old.State.Scope {
			c.Status = CheckFailed
			c.Detail = where + " " + scopeWords(old.State.Scope) + "."
			c.Fix = "Uninstall it first (your data is kept unless you ask), then install " + scopeWords(in.Opts.Scope) + "."
			return c
		}
		c.Status, c.Detail = CheckPartly, where+". Setup upgrades it instead."
	case OpUpgrade:
		if r.Payload == nil {
			break
		}
		cmp, ok := CompareVersions(r.Payload.Manifest.Version, old.Version)
		switch {
		case ok && cmp < 0 && !in.AllowDowngrade:
			c.Status = CheckFailed
			c.Detail = fmt.Sprintf("This setup is Unravis %s, older than the installed %s.", r.Payload.Manifest.Version, old.Version)
			c.Fix = "Download the newest setup program. To go back to an older version on purpose, run setup with --allow-downgrade."
		case ok && cmp < 0:
			c.Status, c.Detail = CheckPartly, fmt.Sprintf("Going back from %s to %s, as asked (--allow-downgrade).", old.Version, r.Payload.Manifest.Version)
		case ok && cmp == 0:
			c.Status, c.Detail = CheckPartly, where+". It is the same version: setup repairs it."
		default:
			c.Detail = fmt.Sprintf("%s. Setup upgrades it to %s and keeps your cases, saved connections and recordings.", where, r.Payload.Manifest.Version)
		}
	default:
		c.Detail = where + "."
	}
	if old.StateErr != "" && in.Op != OpInstall {
		c.Status = worst(c.Status, CheckPartly)
		c.Detail += " Its setup choices file could not be read, so setup uses what it can find."
	}
	return c
}

func scopeWords(s Scope) string {
	if s == ScopeUser {
		return "just for one person"
	}
	return "for everyone on this PC"
}

func worst(a, b CheckStatus) CheckStatus {
	rank := map[CheckStatus]int{CheckReady: 0, CheckPartly: 1, CheckFailed: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func (r *Runner) checkRunningApp(in PreflightInput, l Layout, opts Options) (Check, bool) {
	if in.Old == nil {
		return Check{}, false
	}
	p := &Plan{Layout: l, r: r}
	running, err := p.runningApps()
	c := Check{ID: "app-running", Title: "Unravis open", Status: CheckReady, Detail: "Unravis is not open."}
	_, others := r.splitApps(running)
	switch {
	case err != nil:
		c.Status, c.Detail = CheckPartly, "Setup could not see which programs are open."
	case len(running) == 0:
	case len(others) > 0:
		// Setup never closes another person's Unravis, whatever was ticked: they did not say yes.
		c.Status, c.Detail, c.Fix = CheckFailed, ErrAppOpenForOther.Msg, ErrAppOpenForOther.Fix
	case opts.CloseRunningApp:
		c.Status, c.Detail = CheckPartly, "Unravis is open. Setup closes it, and opens it again when it is done."
	case in.Silent:
		c.Status, c.Detail, c.Fix = CheckFailed, "Unravis is open.", "Close Unravis, or set \"closeRunningApp\": true in the config file."
	default:
		c.Status, c.Detail, c.Fix = CheckFailed, "Unravis is open.", "Close Unravis, or tick “Close Unravis for me”."
	}
	return c, true
}

func browserName(p string) string {
	if strings.Contains(strings.ToLower(p), "chrome") {
		return "Google Chrome"
	}
	return "Microsoft Edge"
}

func checkFree(id, title string, m Machine, path string, need uint64, needText string) Check {
	c := Check{ID: id, Title: title, Status: CheckReady}
	vol := filepath.VolumeName(path)
	if vol == "" || !m.DriveExists(vol+`\`) {
		c.Status, c.Detail, c.Fix = CheckFailed, "The drive of "+path+" is not on this PC.", "Choose a folder on a drive this PC has."
		return c
	}
	free, err := m.FreeBytes(path)
	if err != nil {
		c.Status, c.Detail = CheckPartly, "The free space on "+vol+" could not be read."
		return c
	}
	if free < need {
		c.Status, c.Detail, c.Fix = CheckFailed, fmt.Sprintf("%s has %s free; it needs %s.", vol, sizeText(free), needText), "Free some space on "+vol+", or choose a folder on another drive."
		return c
	}
	c.Detail = fmt.Sprintf("%s free on %s.", sizeText(free), vol)
	return c
}

func sizeText(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	default:
		return fmt.Sprintf("%d MB", n>>20)
	}
}

func checkRecordingsFolder(m Machine, folder string) Check {
	// The folder name itself (drive roots, system folders) is checked with the options; this is about what is on the disk.
	c := Check{ID: "recordings-folder", Title: "Recordings folder", Status: CheckReady}
	st, err := os.Stat(folder)
	switch {
	case err == nil && !st.IsDir():
		c.Status, c.Detail, c.Fix = CheckFailed, folder+" is a file, not a folder.", "Choose another recordings folder."
	case err == nil:
		c.Detail = folder + " is there; recordings already in it are kept."
	case errors.Is(err, os.ErrNotExist):
		c.Detail = "Setup makes " + folder + "."
	default:
		c.Status, c.Detail, c.Fix = CheckFailed, folder+" cannot be used: "+err.Error()+".", "Choose another recordings folder."
	}
	return c
}

func checkAdmin(m Machine, svc Services, in PreflightInput, opts Options) Check {
	c := Check{ID: "admin", Title: "Administrator approval", Status: CheckReady}
	need := NeedsAdminFor(svc, opts) || in.Op == OpUninstall && in.Old != nil && in.Old.Root == HKLM
	switch {
	case !need:
		c.Detail = "Not needed for this install."
	case m.IsAdmin():
		c.Detail = "Setup already has it."
	case m.CanElevate():
		c.Detail = "Windows asks you to approve before setup changes anything for everyone."
	default:
		c.Status, c.Detail = CheckFailed, "This account cannot approve changes for everyone on this PC."
		c.Fix = "Choose “Just for me” without the Blackbox, or ask an administrator to run setup."
	}
	return c
}

func (r *Runner) checkSharedFolder(l Layout) (Check, bool) {
	if _, err := os.Lstat(l.SharedRoot); err != nil {
		return Check{}, false
	}
	c := Check{ID: "shared-folder", Title: "Shared data folder", Status: CheckReady}
	if err := r.Sys.Trust(l.SharedRoot); err == nil {
		c.Detail = l.SharedRoot + " was made by an earlier setup; it is kept with everything in it."
		return c, true
	} else if (&Plan{Layout: l, r: r}).anchorMatches() {
		c.Status, c.Detail = CheckPartly, l.SharedRoot+" was made by an earlier setup but its permissions were changed. Setup puts them back."
		return c, true
	} else {
		c.Status = CheckFailed
		c.Detail = l.SharedRoot + " is already there, and it was not made by the Unravis installer."
		c.Fix = "Move that folder somewhere else (it may hold someone's files), then run setup again."
	}
	return c, true
}

// checkMachineFolder is the program-folder check of an install for everyone: a folder that is there must be one this installer made,
// or an empty one only administrators own (otherwise setup would take over somebody's folder).
func (r *Runner) checkMachineFolder(dir string, old *Installed) (Check, bool) {
	title := "Program folder"
	if err := r.refuseLink(dir); err != nil {
		return Check{ID: "install-folder", Title: title, Status: CheckFailed, Detail: err.Error(), Fix: "Ask an administrator to remove that link, or choose another folder."}, true
	}
	why, err := r.programFolderProblem(dir, old)
	switch {
	case err != nil:
		return Check{ID: "install-folder", Title: title, Status: CheckFailed, Detail: dir + " cannot be checked: " + plainError(err) + ".", Fix: "Choose another folder."}, true
	case why != "":
		return Check{ID: "install-folder", Title: title, Status: CheckFailed, Detail: why, Fix: "Choose an empty folder, or a new one."}, true
	}
	return Check{}, false
}

func checkInstallFolder(dir string) (Check, bool) {
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) == 0 {
		return Check{}, false
	}
	for _, e := range ents {
		if strings.EqualFold(e.Name(), AppExeName) || strings.EqualFold(e.Name(), StateFileName) {
			return Check{}, false
		}
	}
	return Check{ID: "install-folder", Title: "Program folder", Status: CheckFailed, Detail: dir + " already holds other files.",
		Fix: "Choose an empty folder, or a new one."}, true
}
