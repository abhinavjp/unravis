//go:build windows

package discover

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"api-audit-explorer/internal/discover/evtx"
)

// NewSystemEnv returns the real Env for this computer. Importers is left nil;
// the caller sets it. Nothing here writes to the system, and no call returns a
// full path in an error.
func NewSystemEnv() (Env, error) {
	return Env{
		FS:       winFS{},
		Procs:    winProcs{},
		Services: winServices{},
		Registry: winRegistry{},
		Channels: winChannels{},
		Machine:  winMachine{},
	}, nil
}

// mapErr turns a system error into one the contract knows. Access denied and a
// missing privilege both become ErrAccessDenied. The path is left out of the
// error text on purpose.
func mapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && (errno == windows.ERROR_ACCESS_DENIED || errno == windows.ERROR_PRIVILEGE_NOT_HELD) {
		return &fs.PathError{Op: op, Path: "(path)", Err: ErrAccessDenied}
	}
	return &fs.PathError{Op: op, Path: "(path)", Err: err}
}

func filetimeToTime(ft windows.Filetime) time.Time {
	if ft.HighDateTime == 0 && ft.LowDateTime == 0 {
		return time.Time{}
	}
	return time.Unix(0, ft.Nanoseconds()).UTC()
}

// --- FS ---

type winFS struct{}

const shareAll = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

// check validates the path shape and that it sits on a local fixed drive.
func (winFS) check(op, p string) (clean string, u16 *uint16, err error) {
	clean, err = ValidateAbsPath(p)
	if err != nil {
		return "", nil, &fs.PathError{Op: op, Path: "(refused)", Err: err}
	}
	root, _ := windows.UTF16PtrFromString(clean[:3])
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return "", nil, &fs.PathError{Op: op, Path: "(refused)", Err: unsafePathError("not-fixed-drive")}
	}
	u16, err = windows.UTF16PtrFromString(clean)
	if err != nil {
		return "", nil, &fs.PathError{Op: op, Path: "(refused)", Err: unsafePathError("chars")}
	}
	return clean, u16, nil
}

func unsafePathError(code string) error { return &PathError{Reason: ReasonUnsafePath, Code: code} }

func (f winFS) Lstat(path string) (FileInfo, error) {
	clean, p, err := f.check("lstat", path)
	if err != nil {
		return FileInfo{}, err
	}
	var d windows.Win32FileAttributeData
	if err := windows.GetFileAttributesEx(p, windows.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&d))); err != nil {
		return FileInfo{}, mapErr("lstat", err)
	}
	name := WinBase(clean)
	if name == "" {
		name = clean
	}
	return FileInfo{
		Name:    name,
		Size:    int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow),
		ModTime: filetimeToTime(d.LastWriteTime),
		IsDir:   d.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0,
		Reparse: d.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0,
	}, nil
}

// openAttrs opens a handle that can read attributes only, without following a
// reparse point. It cannot read content, so BACKUP_SEMANTICS (needed to open a
// folder) bypasses nothing.
func openAttrs(p *uint16) (windows.Handle, error) {
	return windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, shareAll, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

func (f winFS) Stat(path string) (FileInfo, error) {
	clean, p, err := f.check("stat", path)
	if err != nil {
		return FileInfo{}, err
	}
	h, err := openAttrs(p)
	if err != nil {
		return FileInfo{}, mapErr("stat", err)
	}
	defer windows.CloseHandle(h)
	var d windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &d); err != nil {
		return FileInfo{}, mapErr("stat", err)
	}
	name := WinBase(clean)
	if name == "" {
		name = clean
	}
	return FileInfo{
		Name:         name,
		Size:         int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow),
		ModTime:      filetimeToTime(d.LastWriteTime),
		IsDir:        d.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0,
		Reparse:      d.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0,
		VolumeSerial: d.VolumeSerialNumber,
		FileID:       uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow),
	}, nil
}

func (f winFS) ReadDir(path string) ([]FileInfo, error) {
	clean, _, err := f.check("readdir", path)
	if err != nil {
		return nil, err
	}
	self, err := f.Lstat(clean)
	if err != nil {
		return nil, err
	}
	if !self.IsDir || self.Reparse {
		return nil, &fs.PathError{Op: "readdir", Path: "(path)", Err: fs.ErrInvalid}
	}
	pat, err := windows.UTF16PtrFromString(strings.TrimRight(clean, `\`) + `\*`)
	if err != nil {
		return nil, unsafePathError("chars")
	}
	var d windows.Win32finddata
	h, err := windows.FindFirstFile(pat, &d)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return nil, nil
		}
		return nil, mapErr("readdir", err)
	}
	defer windows.FindClose(h)
	var out []FileInfo
	for {
		name := windows.UTF16ToString(d.FileName[:])
		if name != "." && name != ".." {
			if len(out) >= MaxDirEntries {
				return out, ErrTooManyEntries
			}
			out = append(out, FileInfo{
				Name:    name,
				Size:    int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow),
				ModTime: filetimeToTime(d.LastWriteTime),
				IsDir:   d.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0,
				Reparse: d.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0,
			})
		}
		if err := windows.FindNextFile(h, &d); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return out, nil
			}
			return out, mapErr("readdir", err)
		}
	}
}

func (f winFS) Final(path string) (string, bool, error) {
	clean, p, err := f.check("final", path)
	if err != nil {
		return "", false, err
	}
	// Any folder on the way down that is a reparse point.
	reparse := false
	for cur := clean; len(cur) > 3; cur = WinDir(cur) {
		ptr, _ := windows.UTF16PtrFromString(cur)
		attrs, err := windows.GetFileAttributes(ptr)
		if err != nil {
			return "", false, mapErr("final", err)
		}
		if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			reparse = true
		}
	}
	h, err := openAttrs(p)
	if err != nil {
		return "", false, mapErr("final", err)
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return "", false, mapErr("final", err)
	}
	if int(n) > len(buf) {
		return "", false, unsafePathError("length")
	}
	name := windows.UTF16ToString(buf[:n])
	switch {
	case strings.HasPrefix(name, `\\?\UNC\`):
		return "", false, unsafePathError("unc")
	case strings.HasPrefix(name, `\\?\`):
		name = name[4:]
	}
	return name, reparse, nil
}

func (f winFS) Head(path string, n int) ([]byte, error) {
	clean, p, err := f.check("head", path)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, nil
	}
	if n > MaxConfigBytes {
		n = MaxConfigBytes
	}
	canonical, reparse, err := f.Final(clean)
	if err != nil {
		return nil, err
	}
	if reparse {
		return nil, unsafePathError("reparse")
	}
	if !strings.EqualFold(canonical, clean) {
		return nil, unsafePathError("alias")
	}
	// Read-only, share-all, no BACKUP_SEMANTICS: an elevated account gets no
	// extra rights from this open.
	h, err := windows.CreateFile(p, windows.GENERIC_READ, shareAll, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_SEQUENTIAL_SCAN|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, mapErr("head", err)
	}
	defer windows.CloseHandle(h)
	if t, err := windows.GetFileType(h); err != nil || t != windows.FILE_TYPE_DISK {
		return nil, unsafePathError("not-disk")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return nil, mapErr("head", err)
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return nil, unsafePathError("not-file")
	}
	buf := make([]byte, n)
	got := 0
	for got < n {
		var read uint32
		if err := windows.ReadFile(h, buf[got:], &read, nil); err != nil {
			if errors.Is(err, windows.ERROR_HANDLE_EOF) {
				break
			}
			return nil, mapErr("head", err)
		}
		if read == 0 {
			break
		}
		got += int(read)
	}
	return buf[:got], nil
}

// --- Processes ---

type winProcs struct{}

func (winProcs) List() ([]Proc, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, mapErr("procs", err)
	}
	defer windows.CloseHandle(snap)
	e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snap, &e); err != nil {
		return nil, mapErr("procs", err)
	}
	var out []Proc
	for {
		if e.ProcessID != 0 {
			out = append(out, Proc{
				Image: windows.UTF16ToString(e.ExeFile[:]),
				PID:   e.ProcessID,
				Start: processStart(e.ProcessID),
			})
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return out, nil
			}
			return out, mapErr("procs", err)
		}
	}
}

// processStart is the creation time, or the zero time when the account may not
// query the process. A denied query is not an error: the image name is still
// listed.
func processStart(pid uint32) time.Time {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return time.Time{}
	}
	defer windows.CloseHandle(h)
	var c, x, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &x, &k, &u); err != nil {
		return time.Time{}
	}
	return filetimeToTime(c)
}

// --- Services ---

type winServices struct{}

func (winServices) List() ([]Service, error) {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, mapErr("services", err)
	}
	defer windows.CloseServiceHandle(m)

	var out []Service
	var resume uint32
	buf := make([]byte, 64<<10)
	for {
		var needed, returned uint32
		err := windows.EnumServicesStatusEx(m, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32,
			windows.SERVICE_STATE_ALL, &buf[0], uint32(len(buf)), &needed, &returned, &resume, nil)
		if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
			return out, mapErr("services", err)
		}
		if returned > 0 {
			entries := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), int(returned))
			for _, e := range entries {
				name := windows.UTF16PtrToString(e.ServiceName)
				s := Service{Name: name, State: serviceState(e.ServiceStatusProcess.CurrentState)}
				s.BinaryDir, s.Image = serviceImage(m, e.ServiceName)
				out = append(out, s)
			}
		}
		runtime.KeepAlive(buf)
		if err == nil {
			return out, nil
		}
	}
}

func serviceState(s uint32) string {
	switch s {
	case windows.SERVICE_RUNNING:
		return "running"
	case windows.SERVICE_STOPPED:
		return "stopped"
	default:
		return "other"
	}
}

// serviceImage reads the image path of one service and cuts it to a folder and
// a base name. Arguments are dropped. A service the account cannot open gives
// empty values; the service is still listed.
func serviceImage(m windows.Handle, name *uint16) (dir, image string) {
	h, err := windows.OpenService(m, name, windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return "", ""
	}
	defer windows.CloseServiceHandle(h)
	var needed uint32
	err = windows.QueryServiceConfig(h, nil, 0, &needed)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || needed == 0 {
		return "", ""
	}
	buf := make([]byte, needed)
	cfg := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buf[0]))
	if err := windows.QueryServiceConfig(h, cfg, needed, &needed); err != nil {
		return "", ""
	}
	path := windows.UTF16PtrToString(cfg.BinaryPathName)
	runtime.KeepAlive(buf)
	return SplitImagePath(path)
}

// --- Registry ---

type winRegistry struct{}

func (winRegistry) String(key, value string) (string, error) {
	if !RegistryAllowed(key, value) {
		return "", ErrRegistryNotAllowed
	}
	return readHKLMString(key, value)
}

// readHKLMString reads one REG_SZ value from the 64-bit view. Callers other than
// winRegistry.String must already have passed the allowlist.
func readHKLMString(key, value string) (string, error) {
	sub := strings.TrimPrefix(strings.TrimRight(key, `\`), `HKLM\`)
	if len(sub) == len(key) { // prefix in another case
		if i := strings.IndexByte(key, '\\'); i >= 0 {
			sub = strings.TrimRight(key[i+1:], `\`)
		}
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, sub, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", mapErr("registry", err)
	}
	defer k.Close()
	s, _, err := k.GetStringValue(value)
	if err != nil {
		return "", mapErr("registry", err)
	}
	return s, nil
}

// --- Event channels ---

// winChannels lists channels and tests read access through wevtapi (see the
// evtx package). Access denied wraps fs.ErrPermission, so a detector turns it
// into a needs-elevation gap. No event is read here and no channel name appears
// in an error.
type winChannels struct{}

func (winChannels) List() ([]string, error) {
	names, err := evtx.ListChannels()
	if err != nil {
		return nil, mapChannelErr(err)
	}
	return names, nil
}

func (winChannels) CanRead(name string) error {
	return mapChannelErr(evtx.CanRead(name))
}

// mapChannelErr keeps the contract errors (access denied, not found) and hides
// everything else behind a path-free error.
func mapChannelErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrPermission):
		return &fs.PathError{Op: "channel", Path: "(channel)", Err: ErrAccessDenied}
	case errors.Is(err, fs.ErrNotExist):
		return &fs.PathError{Op: "channel", Path: "(channel)", Err: fs.ErrNotExist}
	}
	return &fs.PathError{Op: "channel", Path: "(channel)", Err: err}
}

// --- Machine ---

type winMachine struct{}

func (winMachine) Names() []string {
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n != "" && !seen[strings.ToLower(n)] {
			seen[strings.ToLower(n)] = true
			out = append(out, n)
		}
	}
	if n, err := windows.ComputerName(); err == nil {
		add(n)
	}
	if n, err := os.Hostname(); err == nil {
		add(n)
	}
	var size uint32
	_ = windows.GetComputerNameEx(windows.ComputerNameDnsFullyQualified, nil, &size)
	if size > 0 {
		buf := make([]uint16, size)
		if err := windows.GetComputerNameEx(windows.ComputerNameDnsFullyQualified, &buf[0], &size); err == nil {
			add(windows.UTF16ToString(buf))
		}
	}
	return out
}

func (winMachine) TZKey() string {
	s, err := readHKLMString(`HKLM\SYSTEM\CurrentControlSet\Control\TimeZoneInformation`, "TimeZoneKeyName")
	if err != nil {
		return ""
	}
	return s
}

func (winMachine) LogicalCores() int {
	return int(windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS))
}

func (winMachine) Elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }
