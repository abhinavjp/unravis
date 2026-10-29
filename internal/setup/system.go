package setup

import (
	"errors"
	"path/filepath"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/datadir"
)

// Root is a registry hive.
type Root int

const (
	HKLM Root = 1 // HKEY_LOCAL_MACHINE (64-bit view)
	HKCU Root = 2 // HKEY_CURRENT_USER
)

func (r Root) String() string {
	switch r {
	case HKLM:
		return "HKLM"
	case HKCU:
		return "HKCU"
	}
	return "HK?"
}

// Errors the system interfaces return.
var (
	// ErrNotFound is a registry key or value, a service or a firewall rule that does not exist.
	ErrNotFound = errors.New("not found")
	// ErrWrongType is a registry value that is not plain text (REG_SZ).
	ErrWrongType = errors.New("the registry value is not plain text")
	// ErrAlreadyRunning is a second setup while one runs.
	ErrAlreadyRunning = errors.New("setup is already running")
	// ErrCancelled is the person saying No at the Windows approval prompt, or pressing Cancel.
	ErrCancelled = errors.New("cancelled")
	// ErrAccessDenied is a service change Windows refused because setup is not elevated (osServices.Stop and Start map their
	// ERROR_ACCESS_DENIED to it), so plan.go can tell "ask for the UAC prompt" apart from "try again later".
	ErrAccessDenied = errors.New("access denied")
)

// Registry is the Windows registry. HKLM is always the 64-bit view, the one datadir.ReadAnchor reads.
type Registry interface {
	// GetString returns a REG_SZ value; ErrNotFound when the key or value is missing, ErrWrongType for any other type.
	GetString(root Root, key, name string) (string, error)
	GetDWORD(root Root, key, name string) (uint32, error)
	SetString(root Root, key, name, value string) error
	SetDWORD(root Root, key, name string, value uint32) error
	DeleteValue(root Root, key, name string) error
	KeyExists(root Root, key string) (bool, error)
	// CreateKey creates key when missing. With sddl, the key's owner and access list are set to it (also when the key exists).
	CreateKey(root Root, key, sddl string) error
	// DeleteKey deletes a key that has no subkeys, with its values. A missing key is not an error.
	DeleteKey(root Root, key string) error
	// KeySecurity reads the key's owner and access list, the way datadir.ReadAnchor does.
	KeySecurity(root Root, key string) (datadir.FolderSecurity, error)
}

// ServiceSpec is a Windows service setup registers.
type ServiceSpec struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Description string   `json:"description"`
	ExePath     string   `json:"exePath"`
	Args        []string `json:"args,omitempty"`
	// Account is the account the service runs as: "" is LocalSystem, "NT SERVICE\<name>" the service's own virtual account. The keeper
	// seals saved passwords for its account, so setup never changes the account of an existing keeper service.
	Account string `json:"account,omitempty"`
	// SIDUnrestricted gives the service its own service SID in its token (the SID the keeper's folder grants).
	SIDUnrestricted bool `json:"sidUnrestricted,omitempty"`
	DelayedStart    bool `json:"delayedStart,omitempty"`
}

// Services is the Windows service manager.
type Services interface {
	State(name string) (blackbox.SvcState, error)
	Create(spec ServiceSpec) error
	// Config returns the registered program, arguments and account; ErrNotFound when the service does not exist.
	Config(name string) (ServiceSpec, error)
	// SetConfig changes the program path, arguments, names and description. It never changes the account.
	SetConfig(spec ServiceSpec) error
	Start(name string) error
	Stop(name string) error
	Delete(name string) error
}

// FirewallRule is an inbound allow rule for one TCP port from listed addresses.
type FirewallRule struct {
	Name            string   `json:"name"`
	Port            int      `json:"port"`
	RemoteAddresses []string `json:"remoteAddresses"`
	Program         string   `json:"program,omitempty"`
}

// Firewall is Windows Defender Firewall.
type Firewall interface {
	// Get returns the rule; ErrNotFound when there is none.
	Get(name string) (FirewallRule, error)
	Add(rule FirewallRule) error
	Remove(name string) error
}

// FileSecurity reads and sets the owner and access list of folders and files.
type FileSecurity interface {
	// Read returns the folder's file ID, owner and access list (datadir.ReadFolderSecurity on Windows); datadir.ErrNotShared when it
	// does not exist.
	Read(path string) (datadir.FolderSecurity, error)
	// SDDL returns the owner and access list as text, so an undo can put them back.
	SDDL(path string) (string, error)
	// SetSDDL sets the owner and access list; a "P" in the DACL flags stops inheritance from the parent.
	SetSDDL(path, sddl string) error
	// MkdirSDDL creates one folder with the owner and access list already set (CreateDirectory with a security descriptor), so there
	// is no moment when it has the parent's permissions.
	MkdirSDDL(path, sddl string) error
	// CreateFileSDDL creates a new file holding data with the owner and access list already set in the create call, so there is no
	// moment when it has the parent's permissions. It fails with os.ErrExist when the file is already there.
	CreateFileSDDL(path, sddl string, data []byte) error
}

// Paths turns a path into the one Windows would really use.
type Paths interface {
	// Canonical returns the full path with 8.3 short names written out, trailing dots and spaces dropped and dot parts resolved
	// (GetFullPathName and GetLongPathName on Windows). The path need not exist: the part that does is expanded.
	Canonical(path string) (string, error)
}

// cleanPaths is the Paths of tests: a clean of the path.
type cleanPaths struct{}

func (cleanPaths) Canonical(p string) (string, error) { return filepath.Clean(p), nil }

// Process is a running program.
type Process struct {
	PID uint32 `json:"pid"`
	Exe string `json:"exe"`
	// SID is the account the program runs as. "" means the list did not say; UnknownOwner means it could not be read.
	SID string `json:"sid,omitempty"`
}

// UnknownOwner is the SID of a program whose owner could not be read (it belongs to someone setup cannot look at).
const UnknownOwner = "unknown"

// Processes lists and ends running programs.
type Processes interface {
	List() ([]Process, error)
	Terminate(pid uint32) error
}

// WindowsVersion is the version of Windows.
type WindowsVersion struct {
	Major, Minor, Build uint32
	Is64                bool
	Server              bool
}

// Machine answers questions about this PC.
type Machine interface {
	Windows() (WindowsVersion, error)
	FreeBytes(path string) (uint64, error)
	PortFree(bind string, port int) error
	// Browser is the path of Edge or Chrome, for the setup window and for Unravis ("" when neither is found).
	Browser() string
	WebView2() bool
	PendingReboot() bool
	IsAdmin() bool
	// CanElevate reports whether the person can approve administrator changes (a member of Administrators, or a PC that asks for an
	// administrator's password).
	CanElevate() bool
	DriveExists(root string) bool
}

// System is everything setup asks of the machine. Tests build one with NewFakeSystem.
type System struct {
	Reg  Registry
	Svc  Services
	FW   Firewall
	Sec  FileSecurity
	Proc Processes
	Mach Machine
	Disk blackbox.DiskSpace
	// Paths canonicalises the paths the person typed before they are checked. Nil means a plain clean.
	Paths Paths
	// Trust checks the shared folder against the installer's record (datadir.TrustInstalled on Windows).
	Trust func(root string) error
	// Probe asks the Blackbox status page with the read key (blackbox.OSEnv.ProbeStatus on Windows).
	Probe func(host string, port int, key string) error
	// RunVersion runs "<exe> --version" and returns what it printed: proof the new program starts before the old files are let go.
	RunVersion func(exe string) (string, error)
	// DeleteLater removes a file at the next restart (a running setup cannot delete itself).
	DeleteLater func(path string) error
	// Lock takes the one-setup-at-a-time lock; ErrAlreadyRunning when another setup holds it.
	Lock  func() (release func(), err error)
	Sleep func(time.Duration)
	Now   func() time.Time
}

func (s *System) paths() Paths {
	if s.Paths != nil {
		return s.Paths
	}
	return cleanPaths{}
}

func (s *System) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *System) sleep(d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(d)
		return
	}
	time.Sleep(d)
}

// waitService polls a service until it reaches want or the time is up.
func (s *System) waitService(name string, want blackbox.SvcState, within time.Duration) error {
	const step = 250 * time.Millisecond
	for waited := time.Duration(0); ; waited += step {
		st, err := s.Svc.State(name)
		if err != nil {
			return err
		}
		if st == want {
			return nil
		}
		if waited >= within {
			return errors.New("the service " + name + " is " + st.String() + ", expected " + want.String())
		}
		s.sleep(step)
	}
}

// stopService stops a service that runs and waits for it. It reports whether the service was running.
func (s *System) stopService(name string) (bool, error) {
	st, err := s.Svc.State(name)
	if err != nil || st == blackbox.SvcNotInstalled || st == blackbox.SvcStopped {
		return false, err
	}
	if st != blackbox.SvcStopPending {
		if err := s.Svc.Stop(name); err != nil {
			return true, err
		}
	}
	return true, s.waitService(name, blackbox.SvcStopped, 30*time.Second)
}

// startService starts a service and waits until it runs.
func (s *System) startService(name string) error {
	st, err := s.Svc.State(name)
	if err != nil {
		return err
	}
	if st == blackbox.SvcRunning {
		return nil
	}
	if err := s.Svc.Start(name); err != nil {
		return err
	}
	return s.waitService(name, blackbox.SvcRunning, 20*time.Second)
}
