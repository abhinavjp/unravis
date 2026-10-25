package discover

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"

	"api-audit-explorer/internal/importer"
)

// ErrNotSupportedOS is what NewSystemEnv returns on a system with no real Env.
var ErrNotSupportedOS = errors.New("host discovery is not supported on this OS")

// ErrAccessDenied marks a probe the current account may not make. It wraps
// fs.ErrPermission, so errors.Is works with either. Real and fake Env
// implementations return it (or an error that wraps fs.ErrPermission) for every
// denied call; GapFromError turns it into a needs-elevation gap.
var ErrAccessDenied = fmt.Errorf("access denied: %w", fs.ErrPermission)

// ErrRegistryNotAllowed means a registry read named a key or value outside the
// allowlist in this file.
var ErrRegistryNotAllowed = errors.New("registry key or value is not on the discovery allowlist")

// ErrTooManyEntries is returned with a partial listing when a directory holds
// more than MaxDirEntries entries.
var ErrTooManyEntries = errors.New("directory holds more entries than the scan reads")

// MaxDirEntries is the most entries one FS.ReadDir call returns (spec 2.2).
const MaxDirEntries = 5000

// MaxConfigBytes caps what FS.Head returns, so a config file is read in full
// only up to 4 MiB (spec 1.4).
const MaxConfigBytes = 4 << 20

// FileInfo is what a scan learns about one directory entry from metadata alone.
type FileInfo struct {
	Name    string // base name
	Size    int64
	ModTime time.Time
	IsDir   bool
	// Reparse is true for a symlink, junction or any other reparse point. A
	// scan never follows one.
	Reparse bool
	// VolumeSerial and FileID identify the file for the swap check at read
	// time. Lstat and ReadDir leave them zero; Stat fills them.
	VolumeSerial uint32
	FileID       uint64
}

// FS is the read-only file view a detector gets. Every path is an absolute
// local path; an implementation refuses a path whose shape fails ValidateAbsPath.
// No method follows a reparse point.
type FS interface {
	// Lstat describes the entry itself.
	Lstat(path string) (FileInfo, error)
	// Stat describes the entry and also fills VolumeSerial and FileID.
	Stat(path string) (FileInfo, error)
	// ReadDir lists a directory. At most MaxDirEntries entries come back; a
	// larger directory returns the first MaxDirEntries and ErrTooManyEntries.
	ReadDir(path string) ([]FileInfo, error)
	// Final resolves path to the name the system reports for it (long names,
	// real casing) and says whether the file or any parent folder is a reparse
	// point. A caller refuses the path when reparse is true or when canonical
	// differs from the expected path ignoring case (8.3 alias, junction).
	Final(path string) (canonical string, reparse bool, err error)
	// Head returns up to n leading bytes of a file, never more than
	// MaxConfigBytes. It opens read-only with share-all and does not follow a
	// reparse point.
	Head(path string, n int) ([]byte, error)
}

// Proc is one running process. Image is the base name only.
type Proc struct {
	Image string
	PID   uint32
	Start time.Time // zero when the account may not query the process
}

// Processes lists running processes.
type Processes interface {
	List() ([]Proc, error)
}

// Service is one installed Windows service. BinaryDir is the folder of the
// service image, with every argument dropped.
type Service struct {
	Name      string
	State     string // running, stopped, other
	BinaryDir string
	Image     string // image base name, no arguments
}

// Services lists installed services.
type Services interface {
	List() ([]Service, error)
}

// Registry reads single string values, and only the ones RegistryAllowed lists.
type Registry interface {
	String(key, value string) (string, error)
}

// EventChannels lists Windows event channels and tests read access.
type EventChannels interface {
	List() ([]string, error)
	CanRead(name string) error
}

// Machine describes this computer.
type Machine interface {
	Names() []string
	TZKey() string
	LogicalCores() int
	Elevated() bool
}

// Env is everything a detector may touch. Detectors never call the os package.
type Env struct {
	FS        FS
	Procs     Processes
	Services  Services
	Registry  Registry
	Channels  EventChannels
	Machine   Machine
	Importers *importer.Registry
}

// registryRule allows one key (matched after normalising) and the values read
// from it.
type registryRule struct {
	key   *regexp.Regexp
	value *regexp.Regexp
}

// registryAllowlist is the only registry data discovery reads (spec 5). Keys
// are matched in full, case-insensitively, with the HKLM\ prefix.
var registryAllowlist = []registryRule{
	// HTTP.sys error log folder.
	{regexp.MustCompile(`(?i)^HKLM\\SYSTEM\\CurrentControlSet\\Services\\HTTP\\Parameters$`), regexp.MustCompile(`(?i)^ErrorLoggingDir$`)},
	// Time zone key name, for the host's time zone.
	{regexp.MustCompile(`(?i)^HKLM\\SYSTEM\\CurrentControlSet\\Control\\TimeZoneInformation$`), regexp.MustCompile(`(?i)^TimeZoneKeyName$`)},
	// SQL Server instance name to instance ID map.
	{regexp.MustCompile(`(?i)^HKLM\\SOFTWARE\\Microsoft\\Microsoft SQL Server\\Instance Names\\SQL$`), regexp.MustCompile(`^[A-Za-z0-9_$#.-]{1,128}$`)},
	// SQL Server startup parameters, SQLArg0, SQLArg1, ...
	{regexp.MustCompile(`(?i)^HKLM\\SOFTWARE\\Microsoft\\Microsoft SQL Server\\[A-Za-z0-9_$#.-]{1,128}\\MSSQLServer\\Parameters$`), regexp.MustCompile(`(?i)^SQLArg[0-9]{1,3}$`)},
}

// RegistryAllowed reports whether discovery may read value from key.
func RegistryAllowed(key, value string) bool {
	key = strings.TrimRight(key, `\`)
	if strings.Contains(key, "..") {
		return false
	}
	for _, r := range registryAllowlist {
		if r.key.MatchString(key) && r.value.MatchString(value) {
			return true
		}
	}
	return false
}

// IsAccessDenied reports whether err is a denied probe.
func IsAccessDenied(err error) bool {
	return err != nil && errors.Is(err, fs.ErrPermission)
}

// IsNotFound reports whether err means the thing does not exist.
func IsNotFound(err error) bool {
	return err != nil && errors.Is(err, fs.ErrNotExist)
}

// notSupportedEnv is what NewSystemEnv returns where there is no real Env. It
// has no build tag so every OS tests it.
func notSupportedEnv() (Env, error) { return Env{}, ErrNotSupportedOS }

// GapFromError maps a failed probe to the gap a detector must report. The gap
// never carries err text, which can hold a path.
func GapFromError(detector string, kind Kind, display string, err error) Gap {
	return Gap{Detector: detector, Kind: kind, Display: display, Reason: ReasonFromError(err)}
}

// ReasonFromError picks the gap reason for a failed probe. Access denied is
// always needs-elevation.
func ReasonFromError(err error) Reason {
	var pe *PathError
	switch {
	case IsAccessDenied(err):
		return ReasonNeedsElevation
	case errors.Is(err, ErrNotSupportedOS):
		return ReasonNotSupportedOS
	case errors.Is(err, ErrTooManyEntries):
		return ReasonTooLarge
	case errors.As(err, &pe):
		return pe.Reason
	case IsNotFound(err):
		return ReasonNotFound
	default:
		return ReasonUnreadable
	}
}
