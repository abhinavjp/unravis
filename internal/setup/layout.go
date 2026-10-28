package setup

import (
	"errors"
	"path/filepath"
	"strings"

	"api-audit-explorer/internal/blackbox"
)

// Dirs are the folders of this PC that setup works with. The real ones come from Windows (Known Folders); tests point them at a
// temporary folder.
type Dirs struct {
	Windows        string `json:"windows"`
	ProgramFiles   string `json:"programFiles"`
	ProgramData    string `json:"programData"`
	CommonPrograms string `json:"commonPrograms"` // the all-users Start menu
	Temp           string `json:"temp"`
	// The person's own folders. The worker gets them from the setup window (an administrator approving over the shoulder has folders
	// of their own) and checks each one with CheckUserDirs.
	UserProfile  string `json:"userProfile"`
	LocalAppData string `json:"localAppData"`
	AppData      string `json:"appData"`
	Programs     string `json:"programs"` // the person's Start menu
	Startup      string `json:"startup"`  // the person's start-at-sign-in folder
}

// UserDirs are the person's own folders in Dirs.
type UserDirs struct {
	UserProfile  string `json:"userProfile"`
	LocalAppData string `json:"localAppData"`
	AppData      string `json:"appData"`
	Programs     string `json:"programs"`
	Startup      string `json:"startup"`
}

// User returns the person's folders.
func (d Dirs) User() UserDirs {
	return UserDirs{UserProfile: d.UserProfile, LocalAppData: d.LocalAppData, AppData: d.AppData, Programs: d.Programs, Startup: d.Startup}
}

// WithUser replaces the person's folders.
func (d Dirs) WithUser(u UserDirs) Dirs {
	d.UserProfile, d.LocalAppData, d.AppData, d.Programs, d.Startup = u.UserProfile, u.LocalAppData, u.AppData, u.Programs, u.Startup
	return d
}

// CheckUserDirs makes sure the folders the window sent are the usual Windows folders of one profile, so the elevated worker never
// writes a shortcut or deletes data anywhere else.
func CheckUserDirs(u UserDirs) error {
	check := func(p, suffix string) error {
		if p == "" || !filepath.IsAbs(p) || strings.Contains(p, "..") || strings.HasPrefix(p, `\\`) {
			return errors.New("a folder of the person's profile is not a full path")
		}
		if !strings.HasSuffix(strings.ToLower(filepath.Clean(p)), strings.ToLower(suffix)) {
			return errors.New(p + " is not a usual profile folder")
		}
		if !insideFolder(p, u.UserProfile) {
			return errors.New(p + " is not inside the person's profile")
		}
		return nil
	}
	if u.UserProfile == "" || !filepath.IsAbs(u.UserProfile) || strings.Contains(u.UserProfile, "..") {
		return errors.New("the person's profile folder is missing")
	}
	for _, c := range []struct{ p, s string }{
		{u.LocalAppData, `\AppData\Local`}, {u.AppData, `\AppData\Roaming`},
		{u.Programs, `\Microsoft\Windows\Start Menu\Programs`}, {u.Startup, `\Microsoft\Windows\Start Menu\Programs\Startup`},
	} {
		if err := check(c.p, c.s); err != nil {
			return err
		}
	}
	return nil
}

// Names of the files setup places and of the registry entries it writes.
const (
	AppExeName    = "unravis.exe"
	KeeperExeName = "unravis-keeper.exe"
	SetupExeName  = "unravis-setup.exe"
	StateFileName = "setup-state.json"
	ShortcutName  = "Unravis.lnk"
	ProductName   = "Unravis"
	// ARPKey is the Add/Remove Programs entry, under HKLM (for everyone) or HKCU (just for me).
	ARPKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\Unravis`
	// BlackboxProgramFolder is the folder under Program Files that holds the Blackbox program and its settings.
	BlackboxProgramFolder = "Unravis Blackbox"
	// SharedFolderName is the shared data folder under ProgramData (datadir.Default looks for the same name).
	SharedFolderName = "Unravis"
	// MachineJournalFolder holds the journal of an elevated run, under ProgramData, locked to administrators.
	MachineJournalFolder = "Unravis Setup"
)

// DefaultLocation is the program folder a new install offers.
func DefaultLocation(scope Scope, d Dirs) string {
	if scope == ScopeUser {
		return filepath.Join(d.LocalAppData, "Programs", "Unravis")
	}
	return filepath.Join(d.ProgramFiles, "Unravis")
}

// Layout is where everything goes for one set of options.
type Layout struct {
	Scope      Scope
	InstallDir string
	AppExe     string
	KeeperExe  string
	SetupExe   string
	StateFile  string
	// The shared data folder, its cases and keeper folders (machine scope only).
	SharedRoot  string
	SharedCases string
	KeeperDir   string
	// The person's own data folders (just-for-me scope; datadir's per-user layout).
	UserData []string
	// BlackboxDir is %ProgramFiles%\Unravis Blackbox.
	BlackboxDir   string
	StartMenuLink string
	StartupLink   string
	ARPRoot       Root
}

// NewLayout works out the layout for the options.
func NewLayout(o Options, d Dirs) Layout {
	l := Layout{Scope: o.Scope, InstallDir: filepath.Clean(o.Location)}
	l.AppExe = filepath.Join(l.InstallDir, AppExeName)
	l.SetupExe = filepath.Join(l.InstallDir, SetupExeName)
	l.StateFile = filepath.Join(l.InstallDir, StateFileName)
	l.BlackboxDir = filepath.Join(d.ProgramFiles, BlackboxProgramFolder)
	l.StartupLink = filepath.Join(d.Startup, ShortcutName)
	if o.Scope == ScopeMachine {
		l.KeeperExe = filepath.Join(l.InstallDir, KeeperExeName)
		l.SharedRoot = filepath.Join(d.ProgramData, SharedFolderName)
		l.SharedCases = filepath.Join(l.SharedRoot, "cases")
		l.KeeperDir = filepath.Join(l.SharedRoot, "keeper")
		l.StartMenuLink = filepath.Join(d.CommonPrograms, ShortcutName)
		l.ARPRoot = HKLM
	} else {
		l.StartMenuLink = filepath.Join(d.Programs, ShortcutName)
		l.ARPRoot = HKCU
		// The same folders datadir uses in per-user mode: the config folder and the cases folder.
		l.UserData = []string{filepath.Join(d.AppData, "APIAuditExplorer"), filepath.Join(d.LocalAppData, "APIAuditExplorer")}
	}
	return l
}

// BlackboxPaths are the settings and read key hash of an installed Blackbox.
func (l Layout) BlackboxPaths() (exe, config, key string) {
	return filepath.Join(l.BlackboxDir, blackbox.InstalledExeName), filepath.Join(l.BlackboxDir, blackbox.ConfigFileName),
		filepath.Join(l.BlackboxDir, blackbox.KeyFileName)
}

// NeedsAdmin reports whether the options need administrator approval.
func NeedsAdmin(o Options) bool { return o.Scope == ScopeMachine || o.Blackbox != nil }
