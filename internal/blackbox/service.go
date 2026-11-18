package blackbox

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SvcState is the state of the Windows service as far as the recorder cares.
type SvcState int

// Service states. SvcNotInstalled means no service of that name exists.
const (
	SvcNotInstalled SvcState = iota
	SvcStopped
	SvcStartPending
	SvcRunning
	SvcStopPending
)

func (s SvcState) String() string {
	switch s {
	case SvcNotInstalled:
		return "not installed"
	case SvcStopped:
		return "stopped"
	case SvcStartPending:
		return "starting"
	case SvcRunning:
		return "running"
	case SvcStopPending:
		return "stopping"
	}
	return "unknown"
}

// ServiceSpec is what Create registers.
type ServiceSpec struct {
	Name        string
	DisplayName string
	Description string
	ExePath     string
	Args        []string
	// Account is the account the service runs under (ServiceStartName). Empty
	// means LocalSystem, which is what the host recorder uses. A virtual account
	// such as NT SERVICE\UnravisBlackboxSql needs no password.
	Account string
	// SIDUnrestricted puts the service's own SID in its token, so a folder that
	// grants that SID (the SQL data folder) is usable. Off for the host service.
	SIDUnrestricted bool
}

// ServiceControl is the Windows service manager. The real one is in
// service_windows.go and is never used by tests; tests use a fake.
type ServiceControl interface {
	State(name string) (SvcState, error)
	Create(spec ServiceSpec) error
	Start(name string) error
	// Stop asks the service to stop and returns when the request is accepted.
	Stop(name string) error
	Delete(name string) error
}

// Env is everything install and remove ask of the machine. Tests use a fake.
type Env interface {
	IsAdmin() bool
	ExePath() (string, error)
	// ProgramDir is the folder the service program is installed into, one that
	// only administrators can write to (%ProgramFiles%\Unravis Blackbox).
	ProgramDir() (string, error)
	// SecureDir limits who may change dir: only SYSTEM and administrators can
	// write there. With usersRead, ordinary users may still read it.
	SecureDir(dir string, usersRead bool) error
	PortFree(bind string, port int) error
	FolderWritable(dir string) error
	FileSHA256(path string) (string, error)
	// ProbeStatus checks the status page at host:port (see ProbeHost): it
	// answers with the key and refuses without it.
	ProbeStatus(host string, port int, key string) error
	Sleep(d time.Duration)
}

// Paths are the config and the read key hash. Install puts them in the program
// folder next to the installed program; both live outside the recordings folder.
type Paths struct {
	ConfigPath string
	KeyPath    string
}

// installedPaths are the settings files in the program folder. Install writes
// them and registers ConfigPath with the service; Apply saves new settings to
// the same place, so there is one source of truth for the file the service
// reads (W3-01).
func installedPaths(progDir string) Paths {
	return Paths{ConfigPath: filepath.Join(progDir, ConfigFileName), KeyPath: filepath.Join(progDir, KeyFileName)}
}

// Reason names why an install could not finish (decision A39).
type Reason string

// Install failure reasons.
const (
	ReasonNoAdmin   Reason = "no-admin"
	ReasonExists    Reason = "service-exists"
	ReasonPortBusy  Reason = "port-busy"
	ReasonFolder    Reason = "folder"
	ReasonAntivirus Reason = "antivirus"
	ReasonOther     Reason = "other"
)

// Apply failure reasons (W3-01). The machine answer carries the fixed reason
// and a plain sentence, never a path or OS error text.
const (
	// ReasonSaveFailed: the new settings could not be saved to the installed
	// config file. Nothing was stopped; the previous settings are untouched.
	ReasonSaveFailed Reason = "save-failed"
	// ReasonRestartFailed: the settings were saved but the recorder did not
	// restart. The previous settings were put back and the previous recorder
	// was started again (ApplyError.Running says whether that worked).
	ReasonRestartFailed Reason = "restart-failed"
	// ReasonRemoveFailed: a remove stopped part way. The message says what was
	// removed, what was kept, and whether the recorder runs again.
	ReasonRemoveFailed Reason = "remove-failed"
)

// MinInstallFree is the free space install needs on the recordings drive.
const MinInstallFree = 1 << 30

// InstallError is an install that stopped and was undone. Message is the plain
// sentence shown to the person.
type InstallError struct {
	Reason  Reason
	Message string
	Cause   error
}

func (e *InstallError) Error() string { return e.Message }
func (e *InstallError) Unwrap() error { return e.Cause }

// ApplyError is an apply that did not happen. Message is the plain sentence
// shown to the person; Reason is ReasonSaveFailed or ReasonRestartFailed.
type ApplyError struct {
	Reason  Reason
	Message string
	Cause   error
	// Running is set on restart-failed when the previous recorder was started
	// again with the previous settings; false means it is stopped.
	Running bool
}

func (e *ApplyError) Error() string { return e.Message }
func (e *ApplyError) Unwrap() error { return e.Cause }

// Errors of the other commands.
var (
	ErrNotAdmin     = errors.New("This account cannot make admin changes. Sign in as an administrator, then retry.")
	ErrNotInstalled = errors.New("The UnravisBlackbox service is not installed here.")
	ErrConfirmName  = errors.New("To delete the recordings, type the Blackbox name exactly.")
)

func msgNoAdmin() *InstallError {
	return &InstallError{Reason: ReasonNoAdmin, Message: ErrNotAdmin.Error()}
}

// Manager installs, controls and removes the service. Every machine effect goes
// through Svc and Env.
type Manager struct {
	Svc  ServiceControl
	Env  Env
	Disk DiskSpace
	// SQL, when set, is removed together with the Blackbox (E2E-10): Remove
	// stops and deletes the SQL service too, and its data folder when the
	// recordings are deleted. Nil leaves the SQL service alone.
	SQL *SQLManager
}

// InstallResult is a finished install. ReadKey is shown once; only its hash is
// kept.
type InstallResult struct {
	ReadKey string
	Service string
	Folder  string
	Port    int
	// Where the service runs from: the installed program and its settings.
	ExePath    string
	ConfigPath string
	KeyPath    string
}

// InstalledExeName is the program file inside the program folder.
const InstalledExeName = "unravis-blackbox.exe"

// ConfigFileName and KeyFileName are the settings files inside the program
// folder.
const (
	ConfigFileName = "blackbox.json"
	KeyFileName    = "readkey.sha256"
)

// undoList runs undo steps in reverse order.
type undoList []func()

func (u *undoList) add(f func()) { *u = append(*u, f) }
func (u undoList) run() {
	for i := len(u) - 1; i >= 0; i-- {
		u[i]()
	}
}

// fileMemory remembers a file so an undo can put it back (or remove it when it
// did not exist).
func fileMemory(path string) func() {
	old, err := os.ReadFile(path)
	existed := err == nil
	return func() {
		if existed {
			_ = os.WriteFile(path, old, 0o644)
		} else {
			_ = os.Remove(path)
		}
	}
}

// ensureDir makes dir and remembers which folders it created so they can be
// removed again (only while empty).
func ensureDir(dir string, undo *undoList) error {
	var missing []string
	for p := filepath.Clean(dir); ; {
		if _, err := os.Stat(p); err == nil {
			break
		}
		missing = append(missing, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, p := range missing { // deepest first: removed in the order listed
		p := p
		undo.add(func() { _ = os.Remove(p) })
	}
	// undoList runs in reverse of add order; deepest folder was added first, so
	// it would be removed last. Reverse the registration to remove it first.
	n := len(missing)
	if n > 1 {
		tail := (*undo)[len(*undo)-n:]
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			tail[i], tail[j] = tail[j], tail[i]
		}
	}
	return nil
}

// createFolder makes dir, and any folder above it that is missing, for the
// recordings (SEC-FIX-06). Each missing folder is made with an exclusive create,
// so a name that appears meanwhile is an error and not silently reused; the last
// one is made with its owner and permissions in the create call when the Env can
// (SecureCreator). An existing folder is left as it is: SecureDir then checks its
// owner and applies the permissions through a handle. What it made is undone
// (while empty) when a later step fails, deepest first.
func (m *Manager) createFolder(dir string, usersRead bool, undo *undoList) error {
	var missing []string // deepest first
	for p := filepath.Clean(dir); ; {
		if _, err := os.Lstat(p); err == nil {
			break
		}
		missing = append(missing, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	sc, canSecure := m.Env.(SecureCreator)
	for i := len(missing) - 1; i >= 0; i-- {
		p := missing[i]
		var err error
		if i == 0 && canSecure {
			err = sc.CreateSecureDir(p, usersRead)
		} else {
			err = os.Mkdir(p, 0o755)
		}
		if err != nil {
			if errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("%w: %s appeared while it was being made", ErrFolderChanged, p)
			}
			return err
		}
		undo.add(func() { _ = os.Remove(p) }) // runs in reverse: deepest first
	}
	return nil
}

// Install places the recorder as a service on this PC. It checks the reasons an
// install can fail, in this order: administrator rights, a service of the same
// name, the port, the folder, then antivirus. Whatever it did is undone when a
// step fails.
//
// The service runs as LocalSystem, so the program is never run from where the
// zip was unpacked (a folder under C:\ or D:\ lets ordinary users replace it).
// Install copies the program into the program folder, locks that folder to
// administrators and SYSTEM before anything goes in, and keeps the settings and
// the read key hash there too. The recordings folder is locked so only
// administrators and SYSTEM can write to it. The service name is checked before the port because a first
// install that is running holds the port: a second install must say "already
// installed", not "port busy".
func (m *Manager) Install(cfg Config) (res InstallResult, err error) {
	if err := cfg.Validate(); err != nil {
		return res, err
	}
	if !m.Env.IsAdmin() {
		return res, msgNoAdmin()
	}
	st, serr := m.Svc.State(ServiceName)
	if serr != nil {
		return res, &InstallError{Reason: ReasonOther, Message: "Could not ask Windows about the service: " + serr.Error(), Cause: serr}
	}
	if st != SvcNotInstalled {
		return res, &InstallError{Reason: ReasonExists, Message: fmt.Sprintf("A service named %s already exists. A Blackbox is already installed here. Open its status, or Remove it first.", ServiceName)}
	}
	if perr := m.Env.PortFree(cfg.Bind, cfg.Port); perr != nil {
		return res, &InstallError{Reason: ReasonPortBusy, Message: fmt.Sprintf("Port %d is busy. Another program uses it. Change the port in Settings, or stop that program.", cfg.Port), Cause: perr}
	}

	var undo undoList
	defer func() {
		if err != nil {
			undo.run()
		}
	}()
	folderErr := func(cause error) error {
		if errors.Is(cause, ErrFolderChanged) {
			return &InstallError{Reason: ReasonFolderUnsafe, Message: fmt.Sprintf("%s changed while it was being locked down, or belongs to another account, so nothing was changed. Choose a new, empty folder in Settings, then try again.", cfg.Folder), Cause: cause}
		}
		return &InstallError{Reason: ReasonFolder, Message: fmt.Sprintf("%s is read-only or has under 1 GB free. Choose another folder in Settings, or free space.", cfg.Folder), Cause: cause}
	}
	if msg := m.folderProblem(cfg.Folder); msg != "" {
		return res, &InstallError{Reason: ReasonFolderUnsafe, Message: msg}
	}
	before := folderNames(cfg.Folder) // what was already there is never undone
	if derr := m.createFolder(cfg.Folder, true, &undo); derr != nil {
		return res, folderErr(derr)
	}
	// The service may have written samples before a later step fails. Undo runs
	// last-added first: this goes after the service is stopped and deleted, and
	// before the folder itself (only an empty folder can be removed).
	undo.add(func() { _, _ = removeRecordings(cfg.Folder, before, false) })
	if werr := m.Env.FolderWritable(cfg.Folder); werr != nil {
		return res, folderErr(werr)
	}
	info, ferr := m.Disk.Free(cfg.Folder)
	if ferr != nil {
		return res, folderErr(ferr)
	}
	if info.Free < MinInstallFree {
		return res, folderErr(fmt.Errorf("%d bytes free", info.Free))
	}

	if serr := m.Env.SecureDir(cfg.Folder, true); serr != nil {
		return res, folderErr(serr)
	}

	avErr := func(cause error) error {
		return &InstallError{Reason: ReasonAntivirus, Message: "Antivirus blocked it. Your security software quarantined the program. Allow the folder, then try again.", Cause: cause}
	}
	src, eerr := m.Env.ExePath()
	if eerr != nil {
		return res, avErr(eerr)
	}
	progDir, perr2 := m.Env.ProgramDir()
	if perr2 != nil {
		return res, &InstallError{Reason: ReasonOther, Message: "Could not find the Program Files folder: " + perr2.Error(), Cause: perr2}
	}
	lockErr := func(cause error) error {
		return &InstallError{Reason: ReasonOther, Message: fmt.Sprintf("Could not make %s safe (only administrators may change it): %s", progDir, cause.Error()), Cause: cause}
	}
	if derr := ensureDir(progDir, &undo); derr != nil {
		return res, lockErr(derr)
	}
	// Lock the folder before the program goes in, so there is no moment when a
	// user could swap it.
	if serr := m.Env.SecureDir(progDir, false); serr != nil {
		return res, lockErr(serr)
	}
	p := installedPaths(progDir)
	exe := filepath.Join(progDir, InstalledExeName)
	if !samePath(src, exe) {
		undo.add(fileMemory(exe))
		if cerr := copyProgram(src, exe); cerr != nil {
			return res, avErr(cerr)
		}
	}
	want, herr := m.Env.FileSHA256(exe)
	if herr != nil {
		return res, avErr(herr)
	}

	key, kerr := NewReadKey()
	if kerr != nil {
		return res, &InstallError{Reason: ReasonOther, Message: "Could not make a read key: " + kerr.Error(), Cause: kerr}
	}
	undo.add(fileMemory(p.KeyPath))
	if serr := SaveKeyHash(p.KeyPath, key); serr != nil {
		return res, folderErr(serr)
	}
	undo.add(fileMemory(p.ConfigPath))
	undo.add(fileMemory(p.ConfigPath + ".bak"))
	if serr := SaveConfig(p.ConfigPath, cfg, true); serr != nil {
		return res, folderErr(serr)
	}

	spec := ServiceSpec{Name: ServiceName, DisplayName: "Unravis Blackbox",
		Description: "Records CPU, memory, disk, network and process load every few seconds so the evidence exists when something goes wrong. Read only.",
		ExePath:     exe, Args: []string{"service", "--config", p.ConfigPath, "--key-file", p.KeyPath}}
	if cerr := m.Svc.Create(spec); cerr != nil {
		return res, &InstallError{Reason: ReasonOther, Message: "Windows would not create the service: " + cerr.Error(), Cause: cerr}
	}
	undo.add(func() {
		_ = m.Svc.Stop(ServiceName)
		_ = m.waitFor(SvcStopped, 10*time.Second)
		_ = m.Svc.Delete(ServiceName)
	})

	// failedStart decides between antivirus and a plain failure: if the program
	// file is gone or changed since install began, security software took it.
	failedStart := func(cause error) error {
		if got, gerr := m.Env.FileSHA256(exe); gerr != nil || got != want {
			return avErr(cause)
		}
		return &InstallError{Reason: ReasonOther, Message: "The service did not start: " + cause.Error(), Cause: cause}
	}
	if serr := m.Svc.Start(ServiceName); serr != nil {
		return res, failedStart(serr)
	}
	if werr := m.waitFor(SvcRunning, 15*time.Second); werr != nil {
		return res, failedStart(werr)
	}
	if perr := m.Env.ProbeStatus(ProbeHost(cfg.Bind), cfg.Port, key); perr != nil {
		return res, failedStart(fmt.Errorf("the status page did not answer as expected: %w", perr))
	}
	return InstallResult{ReadKey: key, Service: ServiceName, Folder: cfg.Folder, Port: cfg.Port,
		ExePath: exe, ConfigPath: p.ConfigPath, KeyPath: p.KeyPath}, nil
}

// ProbeHost is where to ask a service that listens on bind. A service bound to
// every interface (0.0.0.0 or ::) answers on loopback; one bound to a specific
// address, a LAN address or ::1, listens only there, so that is the address to
// ask (nothing listens on 127.0.0.1 then).
func ProbeHost(bind string) string {
	ip := net.ParseIP(strings.Trim(bind, "[]"))
	switch {
	case bind == "" || (ip != nil && ip.IsUnspecified()):
		return "127.0.0.1"
	case ip == nil:
		return bind
	default:
		return ip.String()
	}
}

// samePath says whether two paths name the same file (Windows paths are not
// case sensitive).
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// copyProgram copies the program to dst through a temporary file, so a crash or
// full disk never leaves half a program where the service expects a whole one.
func copyProgram(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFileAtomic(dst, data, 0o755)
}

// waitFor polls the service until it reaches want or the time is up.
func (m *Manager) waitFor(want SvcState, within time.Duration) error {
	const step = 250 * time.Millisecond
	var last SvcState
	for waited := time.Duration(0); ; waited += step {
		st, err := m.Svc.State(ServiceName)
		if err != nil {
			return err
		}
		if st == want {
			return nil
		}
		last = st
		if waited >= within {
			return fmt.Errorf("the service is %s, expected %s", last, want)
		}
		m.Env.Sleep(step)
	}
}

// Start starts the installed service and waits until it runs. Starting a
// service that already runs is not an error.
func (m *Manager) Start() error {
	if !m.Env.IsAdmin() {
		return ErrNotAdmin
	}
	st, err := m.Svc.State(ServiceName)
	if err != nil {
		return err
	}
	switch st {
	case SvcNotInstalled:
		return ErrNotInstalled
	case SvcRunning:
		return nil
	}
	if err := m.Svc.Start(ServiceName); err != nil {
		return err
	}
	return m.waitFor(SvcRunning, 15*time.Second)
}

// Stop stops the service and waits. Stopping a stopped service is not an error.
// The recorder ends the current run cleanly when it is stopped.
func (m *Manager) Stop() error {
	if !m.Env.IsAdmin() {
		return ErrNotAdmin
	}
	st, err := m.Svc.State(ServiceName)
	if err != nil {
		return err
	}
	switch st {
	case SvcNotInstalled:
		return ErrNotInstalled
	case SvcStopped:
		return nil
	}
	if err := m.Svc.Stop(ServiceName); err != nil {
		return err
	}
	return m.waitFor(SvcStopped, 30*time.Second)
}

// ApplyResult says what Apply did.
type ApplyResult struct {
	Config    Config
	Restarted bool // false when the service was stopped: new settings apply at its next start
}

// Apply saves the validated settings to the installed config file (the same
// path Install registers with the service) and only then restarts the service
// so they take effect. The file the settings come from may be a temporary one
// that is gone right after this call, so it is never what the service reads
// (W3-01). A config that does not validate is refused before anything is saved
// or stopped, so a typo cannot take the recorder down. A stopped recorder keeps
// the new settings for its next start and Restarted is false.
//
// Before anything is stopped Apply runs the Install checks on what changed
// (E2E-03): a new port must be free, a new folder must be writable, have room
// and is locked to administrators and SYSTEM. A refused check changes nothing.
//
// When the save fails, nothing is stopped and the reason is save-failed. When
// the restart fails after the save, the previous settings (and their backup) are
// put back the way a failed Install undoes itself, the previous recorder is
// started again (E2E-02), and the reason is restart-failed. The answer says
// whether the recorder is running again. It carries a fixed reason and a plain
// sentence, never a path or OS error text.
func (m *Manager) Apply(configPath string) (res ApplyResult, err error) {
	if !m.Env.IsAdmin() {
		return res, ErrNotAdmin
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return res, err
	}
	return m.ApplyConfig(cfg)
}

// ApplyConfig is Apply for settings that were already read and checked, so a
// caller that hashed the bytes it parsed does not read the file a second time
// (SEC-FIX-08). It repeats the administrator check.
func (m *Manager) ApplyConfig(cfg Config) (res ApplyResult, err error) {
	if !m.Env.IsAdmin() {
		return res, ErrNotAdmin
	}
	if err = cfg.Validate(); err != nil {
		return res, err
	}
	res.Config = cfg
	st, err := m.Svc.State(ServiceName)
	if err != nil {
		return res, err
	}
	if st == SvcNotInstalled {
		return res, ErrNotInstalled
	}
	progDir, err := m.Env.ProgramDir()
	if err != nil {
		return res, applySaveError(err)
	}
	installed := installedPaths(progDir).ConfigPath

	var undo undoList
	restartTried := false
	defer func() {
		if err == nil {
			return
		}
		undo.run()
		if !restartTried {
			return
		}
		// The old settings are back: bring the old recorder back with them.
		running := m.recoverService()
		var ae *ApplyError
		if errors.As(err, &ae) && ae.Reason == ReasonRestartFailed {
			err = applyRestartError(ae.Cause, running)
		}
	}()

	// What the service runs with now. When it cannot be read, every check runs
	// and the port check is left out (it would meet the service's own listener).
	old, oerr := LoadConfig(installed)
	known := oerr == nil
	listenChanged := known && (old.Port != cfg.Port || old.Bind != cfg.Bind)
	folderChanged := !known || !samePath(old.Folder, cfg.Folder)
	if listenChanged {
		if perr := m.Env.PortFree(cfg.Bind, cfg.Port); perr != nil {
			return res, &ApplyError{Reason: ReasonPortBusy, Message: fmt.Sprintf("Port %d is busy. Another program uses it, so nothing was changed. Choose another port, then apply again.", cfg.Port), Cause: perr}
		}
	}
	if folderChanged {
		if msg := m.folderProblem(cfg.Folder); msg != "" {
			return res, &ApplyError{Reason: ReasonFolderUnsafe, Message: msg}
		}
		folderErr := func(cause error) error {
			if errors.Is(cause, ErrFolderChanged) {
				return &ApplyError{Reason: ReasonFolderUnsafe, Message: fmt.Sprintf("%s changed while it was being locked down, or belongs to another account, so nothing was changed. Choose a new, empty folder, then apply again.", cfg.Folder), Cause: cause}
			}
			return &ApplyError{Reason: ReasonFolder, Message: fmt.Sprintf("%s is read-only or has under 1 GB free, so nothing was changed. Choose another folder, or free space, then apply again.", cfg.Folder), Cause: cause}
		}
		if derr := m.createFolder(cfg.Folder, true, &undo); derr != nil {
			return res, folderErr(derr)
		}
		if werr := m.Env.FolderWritable(cfg.Folder); werr != nil {
			return res, folderErr(werr)
		}
		info, ferr := m.Disk.Free(cfg.Folder)
		if ferr != nil {
			return res, folderErr(ferr)
		}
		if info.Free < MinInstallFree {
			return res, folderErr(fmt.Errorf("%d bytes free", info.Free))
		}
		if serr := m.Env.SecureDir(cfg.Folder, true); serr != nil {
			return res, folderErr(serr)
		}
	}

	undo.add(fileMemory(installed))
	undo.add(fileMemory(installed + ".bak"))
	if err = SaveConfig(installed, cfg, true); err != nil {
		return res, applySaveError(err)
	}
	if st == SvcStopped {
		// Nothing is running: the saved settings apply at the next start.
		return res, nil
	}
	restartTried = true
	if st == SvcRunning || st == SvcStartPending {
		if err = m.Svc.Stop(ServiceName); err != nil {
			return res, applyRestartError(err, false)
		}
	}
	if err = m.waitFor(SvcStopped, 30*time.Second); err != nil {
		return res, applyRestartError(err, false)
	}
	if err = m.Svc.Start(ServiceName); err != nil {
		return res, applyRestartError(err, false)
	}
	if err = m.waitFor(SvcRunning, 15*time.Second); err != nil {
		return res, applyRestartError(err, false)
	}
	res.Restarted = true
	return res, nil
}

// recoverService brings the recorder back after a failed restart, with the old
// settings already on disk. It reports whether the recorder runs again.
func (m *Manager) recoverService() bool {
	st, err := m.Svc.State(ServiceName)
	if err != nil {
		return false
	}
	switch st {
	case SvcRunning:
		return true
	case SvcNotInstalled:
		return false
	case SvcStopped:
	default:
		if m.Svc.Stop(ServiceName) != nil || m.waitFor(SvcStopped, 15*time.Second) != nil {
			return false
		}
	}
	if m.Svc.Start(ServiceName) != nil {
		return false
	}
	return m.waitFor(SvcRunning, 15*time.Second) == nil
}

// applySaveError is the fixed answer when the new settings could not be saved.
// The cause stays in Cause for the log; the message never names a path.
func applySaveError(cause error) *ApplyError {
	return &ApplyError{Reason: ReasonSaveFailed, Message: "The new settings could not be saved, so nothing was changed. The recorder keeps its previous settings. Try again.", Cause: cause}
}

// applyRestartError is the fixed answer when the recorder did not restart after
// the save. The undo put the previous settings back; running says whether the
// previous recorder is up again, so the words never claim more than is true.
func applyRestartError(cause error, running bool) *ApplyError {
	msg := "The recorder did not restart with the new settings. The previous settings were put back and the recorder is running with them again. Check the new settings, then apply again."
	if !running {
		msg = "The recorder did not restart with the new settings. The previous settings were put back, but the recorder is stopped. Press Start on the Blackbox, or check Services in Windows."
	}
	return &ApplyError{Reason: ReasonRestartFailed, Message: msg, Cause: cause, Running: running}
}

// RemoveResult says what Remove did.
type RemoveResult struct {
	ServiceRemoved    bool
	RecordingsDeleted bool
	FilesDeleted      int
	// SQLServiceRemoved is set when the UnravisBlackboxSql service was found and
	// removed with the host service. SQLNote carries a warning from that step
	// (for example launch permissions to take back by hand).
	SQLServiceRemoved bool
	SQLNote           string
}

// RemoveError is a remove that stopped part way. Message is the plain sentence
// shown to the person: it says what was removed and what was kept.
type RemoveError struct {
	Reason  string
	Message string
	Cause   error
}

func (e *RemoveError) Error() string { return e.Message }
func (e *RemoveError) Unwrap() error { return e.Cause }

// Remove deletes the service, the read key hash, the installed program copy and
// its settings file. With deleteRecordings it also deletes the recordings, but
// only when confirmName is the Blackbox name: that check happens before anything
// is changed. Deleting the recordings removes the recording files, the manifest,
// the snapshots folder and leftover temporary files, never anything else in the
// folder, and the folder itself only when that leaves it empty.
//
// Remove is ordered so a failure leaves a working Blackbox rather than a half
// removed one (E2E-09, E2E-10): the recorder is stopped, then the SQL service is
// removed, then the recordings are deleted (a held file stops it here, with the
// service still installed and started again), and only then the service, key,
// program copy and settings go. When a step fails the answer names what was
// already removed and what was kept.
func (m *Manager) Remove(cfg Config, keyPath string, deleteRecordings bool, confirmName string) (RemoveResult, error) {
	var res RemoveResult
	if !m.Env.IsAdmin() {
		return res, ErrNotAdmin
	}
	if deleteRecordings && confirmName != cfg.Name {
		return res, ErrConfirmName
	}
	st, err := m.Svc.State(ServiceName)
	if err != nil {
		return res, &RemoveError{Reason: string(ReasonRemoveFailed), Message: "Windows could not say whether the Blackbox service exists, so nothing was removed. Try again, and check Services in Windows if it keeps happening.", Cause: err}
	}
	installed := st != SvcNotInstalled
	if !installed && !deleteRecordings {
		return res, &RemoveError{Reason: "not-installed", Message: ErrNotInstalled.Error() + " There is nothing to remove. If it is still in the list, take it off the list instead.", Cause: ErrNotInstalled}
	}
	wasRunning := installed && st != SvcStopped
	// fail puts the recorder back when it was running, then words the answer
	// from what is true now.
	fail := func(what string, cause error) (RemoveResult, error) {
		running := false
		if installed && wasRunning {
			running = m.recoverService()
		}
		tail := ""
		switch {
		case !installed:
		case running:
			tail = " The Blackbox service is still installed and is running again."
		case wasRunning:
			tail = " The Blackbox service is still installed but stopped. Press Start on the Blackbox."
		default:
			tail = " The Blackbox service is still installed."
		}
		return res, &RemoveError{Reason: string(ReasonRemoveFailed), Message: what + tail, Cause: cause}
	}
	if installed && wasRunning {
		if err := m.Svc.Stop(ServiceName); err != nil {
			return fail("The Blackbox did not stop, so nothing was removed. Try again, or stop it in Services in Windows.", err)
		}
		if err := m.waitFor(SvcStopped, 30*time.Second); err != nil {
			return fail("The Blackbox did not stop in time, so nothing was removed. Try again, or stop it in Services in Windows.", err)
		}
	}
	if m.SQL != nil {
		sql := *m.SQL
		sql.DeleteData = deleteRecordings
		serr := sql.RemoveSQL(cfg, cfg.Name)
		switch {
		case serr == nil:
			res.SQLServiceRemoved = true
		case errors.Is(serr, ErrNotSQLInstalled):
		default:
			// A service that is gone but whose permissions need tidying is a
			// warning. One that is still there stops the remove.
			if left, lerr := m.Svc.State(SQLServiceName); lerr == nil && left == SvcNotInstalled {
				res.SQLServiceRemoved = true
				res.SQLNote = serr.Error()
			} else {
				return fail("The SQL service of this Blackbox could not be removed, so nothing else was removed. Try again, or run remove-sql as an administrator.", serr)
			}
		}
	}
	if deleteRecordings {
		n, rerr := RemoveRecordings(cfg.Folder)
		res.FilesDeleted = n
		if rerr != nil {
			what := fmt.Sprintf("%d recording items were deleted, then a file could not be deleted. A recording is probably open in another program. Close it and remove again.", n)
			if n == 0 {
				what = "A recording could not be deleted, probably because another program has it open. Close that program and remove again."
			}
			return fail(what, rerr)
		}
		res.RecordingsDeleted = true
	}
	if installed {
		if derr := m.Svc.Delete(ServiceName); derr != nil {
			return fail("Windows would not remove the Blackbox service. Close the Services window if it is open, then remove again.", derr)
		}
		res.ServiceRemoved = true
	}
	if keyPath != "" {
		_ = os.Remove(keyPath)
	}
	// The installed program copy and its settings go too. A program that is
	// running (this one, when it was started from the program folder) cannot
	// delete itself: it is left and can be deleted by hand.
	if dir, derr := m.Env.ProgramDir(); derr == nil {
		p := installedPaths(dir)
		_ = os.Remove(filepath.Join(dir, InstalledExeName))
		_ = os.Remove(p.ConfigPath)
		_ = os.Remove(p.ConfigPath + ".bak")
	}
	return res, nil
}

// snapshotDirRe matches the folders Snapshot makes: <name>-YYYY-MM-DD-HHMM, with
// -2, -3 ... when two are made in the same minute.
var snapshotDirRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*-\d{4}-\d{2}-\d{2}-\d{4}(-\d+)?$`)

// isStoreTemp is the store's own temporary file (the manifest is written through
// <manifest>.tmp-*). Other .tmp- files in the folder are not ours.
func isStoreTemp(name string) bool { return strings.HasPrefix(name, ManifestName+".tmp-") }

// RemoveRecordings deletes what the recorder wrote in dir and nothing else: the
// recording files, the manifest (and its .bad- copies and temp files), and the
// snapshot folders Snapshot made. The folder itself goes only when that leaves
// it empty. A drive root or a system folder is refused outright.
func RemoveRecordings(dir string) (int, error) {
	return removeRecordings(dir, nil, true)
}

// removeRecordings is RemoveRecordings that leaves the names in keep alone (what
// was in the folder before an install started) and removes the folder itself
// only when removeDir is set.
func removeRecordings(dir string, keep map[string]bool, removeDir bool) (int, error) {
	if why := unsafeFolder(dir); why != "" {
		return 0, fmt.Errorf("blackbox: will not delete recordings in %s: it is %s", dir, why)
	}
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range ents {
		name := e.Name()
		path := filepath.Join(dir, name)
		if keep[name] {
			continue
		}
		switch {
		case e.IsDir() && name == SnapshotsDir:
			sub, serr := os.ReadDir(path)
			if serr != nil {
				continue
			}
			for _, se := range sub {
				if se.IsDir() && snapshotDirRe.MatchString(se.Name()) {
					if err := os.RemoveAll(filepath.Join(path, se.Name())); err != nil {
						return n, err
					}
					n++
				}
			}
			_ = os.Remove(path) // only succeeds when empty
		case !e.IsDir() && (ValidFileName(name) || name == ManifestName || strings.HasPrefix(name, ManifestName+".bad-") || isStoreTemp(name)):
			if err := os.Remove(path); err != nil {
				return n, err
			}
			n++
		}
	}
	if removeDir {
		_ = os.Remove(dir) // only succeeds when empty
	}
	return n, nil
}

// folderNames lists the names in dir (nil when it cannot be read).
func folderNames(dir string) map[string]bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	m := make(map[string]bool, len(ents))
	for _, e := range ents {
		m[e.Name()] = true
	}
	return m
}
