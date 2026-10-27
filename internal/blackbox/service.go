package blackbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	PortFree(bind string, port int) error
	FolderWritable(dir string) error
	FileSHA256(path string) (string, error)
	// ProbeStatus checks the status page: it answers with the key and refuses
	// without it.
	ProbeStatus(port int, key string) error
	Sleep(d time.Duration)
}

// Paths are the files next to the program: the config and the read key hash.
// Both live outside the recordings folder.
type Paths struct {
	ConfigPath string
	KeyPath    string
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

// Errors of the other commands.
var (
	ErrNotAdmin     = errors.New("This account cannot approve administrator changes. Sign in as an administrator and try again.")
	ErrNotInstalled = errors.New("The UnravisBlackbox service is not installed here.")
	ErrConfirmName  = errors.New("To delete the recordings, type the Blackbox name exactly (--confirm-name).")
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
}

// InstallResult is a finished install. ReadKey is shown once; only its hash is
// kept.
type InstallResult struct {
	ReadKey string
	Service string
	Folder  string
	Port    int
}

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

// Install places the recorder as a service on this PC. It checks the reasons an
// install can fail, in this order: administrator rights, a service of the same
// name, the port, the folder, then antivirus. Whatever it did is undone when a
// step fails. The service name is checked before the port because a first
// install that is running holds the port: a second install must say "already
// installed", not "port busy".
func (m *Manager) Install(cfg Config, p Paths) (res InstallResult, err error) {
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
		return &InstallError{Reason: ReasonFolder, Message: fmt.Sprintf("%s is read-only or has under 1 GB free. Choose another folder in Settings, or free space.", cfg.Folder), Cause: cause}
	}
	if derr := ensureDir(cfg.Folder, &undo); derr != nil {
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

	avErr := func(cause error) error {
		return &InstallError{Reason: ReasonAntivirus, Message: "Antivirus blocked it. Your security software quarantined the program. Allow the folder, then try again.", Cause: cause}
	}
	exe, eerr := m.Env.ExePath()
	if eerr != nil {
		return res, avErr(eerr)
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
	if perr := m.Env.ProbeStatus(cfg.Port, key); perr != nil {
		return res, failedStart(fmt.Errorf("the status page did not answer as expected: %w", perr))
	}
	return InstallResult{ReadKey: key, Service: ServiceName, Folder: cfg.Folder, Port: cfg.Port}, nil
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

// Apply reads the config file again and restarts the service so the settings
// take effect. A config that does not validate is refused before anything is
// stopped, so a typo cannot take the recorder down.
func (m *Manager) Apply(configPath string) (ApplyResult, error) {
	var res ApplyResult
	if !m.Env.IsAdmin() {
		return res, ErrNotAdmin
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return res, err
	}
	res.Config = cfg
	st, err := m.Svc.State(ServiceName)
	if err != nil {
		return res, err
	}
	switch st {
	case SvcNotInstalled:
		return res, ErrNotInstalled
	case SvcStopped:
		return res, nil
	}
	if st == SvcRunning || st == SvcStartPending {
		if err := m.Svc.Stop(ServiceName); err != nil {
			return res, err
		}
	}
	if err := m.waitFor(SvcStopped, 30*time.Second); err != nil {
		return res, err
	}
	if err := m.Svc.Start(ServiceName); err != nil {
		return res, err
	}
	if err := m.waitFor(SvcRunning, 15*time.Second); err != nil {
		return res, err
	}
	res.Restarted = true
	return res, nil
}

// RemoveResult says what Remove did.
type RemoveResult struct {
	ServiceRemoved    bool
	RecordingsDeleted bool
	FilesDeleted      int
}

// Remove stops and deletes the service and the read key hash. With
// deleteRecordings it also deletes the recordings, but only when confirmName is
// the Blackbox name: the check happens before anything is changed. Deleting the
// recordings removes the recording files, the manifest, the snapshots folder and
// leftover temporary files, never anything else in the folder, and the folder
// itself only when that leaves it empty.
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
		return res, err
	}
	if st != SvcNotInstalled {
		if st != SvcStopped {
			if err := m.Svc.Stop(ServiceName); err != nil {
				return res, err
			}
			if err := m.waitFor(SvcStopped, 30*time.Second); err != nil {
				return res, err
			}
		}
		if err := m.Svc.Delete(ServiceName); err != nil {
			return res, err
		}
		res.ServiceRemoved = true
	}
	if keyPath != "" {
		_ = os.Remove(keyPath)
	}
	if deleteRecordings {
		n, err := RemoveRecordings(cfg.Folder)
		res.FilesDeleted = n
		if err != nil {
			return res, err
		}
		res.RecordingsDeleted = true
	}
	if !res.ServiceRemoved && !deleteRecordings {
		return res, ErrNotInstalled
	}
	return res, nil
}

// RemoveRecordings deletes what the recorder wrote in dir and nothing else.
func RemoveRecordings(dir string) (int, error) {
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
		switch {
		case e.IsDir() && name == "snapshots":
			if err := os.RemoveAll(path); err != nil {
				return n, err
			}
			n++
		case !e.IsDir() && (ValidFileName(name) || name == ManifestName || strings.HasPrefix(name, ManifestName+".bad-") || strings.Contains(name, ".tmp-")):
			if err := os.Remove(path); err != nil {
				return n, err
			}
			n++
		}
	}
	_ = os.Remove(dir) // only succeeds when empty
	return n, nil
}
