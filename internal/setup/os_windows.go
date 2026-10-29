//go:build windows

package setup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/datadir"
)

// The real System. None of this runs in tests: it changes the registry, services, firewall and permissions of the PC it runs on.
// NOT RUN: needs a clean VM (installer-and-releases.md section 8 lists the proofs).

// NewOSSystem is the System of this PC.
func NewOSSystem() (*System, error) {
	m := osMachine{}
	return &System{
		Reg: osRegistry{}, Svc: osServices{}, FW: osFirewall{}, Sec: osSecurity{}, Proc: osProcesses{}, Mach: m,
		Disk:        blackbox.OSDisk{},
		Paths:       osPaths{},
		Trust:       datadir.TrustInstalled,
		Probe:       blackbox.OSEnv{}.ProbeStatus,
		RunVersion:  runVersion,
		DeleteLater: deleteLater,
		Lock:        setupLock,
	}, nil
}

// OSDirs are this PC's folders for the signed-in account (Windows known folders, never environment variables).
func OSDirs() (Dirs, error) {
	get := func(id *windows.KNOWNFOLDERID) (string, error) { return windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT) }
	var d Dirs
	var err error
	for _, f := range []struct {
		id   *windows.KNOWNFOLDERID
		into *string
	}{
		{windows.FOLDERID_Windows, &d.Windows}, {windows.FOLDERID_ProgramFiles, &d.ProgramFiles}, {windows.FOLDERID_ProgramData, &d.ProgramData},
		{windows.FOLDERID_CommonPrograms, &d.CommonPrograms}, {windows.FOLDERID_Profile, &d.UserProfile},
		{windows.FOLDERID_LocalAppData, &d.LocalAppData}, {windows.FOLDERID_RoamingAppData, &d.AppData},
		{windows.FOLDERID_Programs, &d.Programs}, {windows.FOLDERID_Startup, &d.Startup},
	} {
		if *f.into, err = get(f.id); err != nil {
			return d, fmt.Errorf("Windows did not say where a usual folder is: %w", err)
		}
	}
	d.Temp = os.TempDir()
	return d, nil
}

// CurrentUserSID is the account this process runs as.
func CurrentUserSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// ProfileOf is the profile folder Windows records for an account (ProfileList), used by the worker to check the folders the window
// sent.
func ProfileOf(sid string) (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\`+sid, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer k.Close()
	p, _, err := k.GetStringValue("ProfileImagePath")
	if err != nil {
		return "", err
	}
	return registry.ExpandString(p)
}

/* ------------------------------------------------------------ registry */

type osRegistry struct{}

func hive(r Root) registry.Key {
	if r == HKLM {
		return registry.LOCAL_MACHINE
	}
	return registry.CURRENT_USER
}

func mapRegErr(err error) error {
	if errors.Is(err, registry.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

func (osRegistry) open(root Root, key string, access uint32) (registry.Key, error) {
	k, err := registry.OpenKey(hive(root), key, access|registry.WOW64_64KEY)
	return k, mapRegErr(err)
}

func (o osRegistry) GetString(root Root, key, name string) (string, error) {
	k, err := o.open(root, key, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	s, typ, err := k.GetStringValue(name)
	if errors.Is(err, registry.ErrUnexpectedType) {
		return "", ErrWrongType
	}
	if err != nil {
		return "", mapRegErr(err)
	}
	if typ != registry.SZ {
		return "", ErrWrongType
	}
	return s, nil
}

func (o osRegistry) GetDWORD(root Root, key, name string) (uint32, error) {
	k, err := o.open(root, key, registry.QUERY_VALUE)
	if err != nil {
		return 0, err
	}
	defer k.Close()
	v, typ, err := k.GetIntegerValue(name)
	if err != nil {
		if errors.Is(err, registry.ErrUnexpectedType) {
			return 0, ErrWrongType
		}
		return 0, mapRegErr(err)
	}
	if typ != registry.DWORD {
		return 0, ErrWrongType
	}
	return uint32(v), nil
}

func (o osRegistry) SetString(root Root, key, name, value string) error {
	k, err := o.open(root, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

func (o osRegistry) SetDWORD(root Root, key, name string, value uint32) error {
	k, err := o.open(root, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetDWordValue(name, value)
}

func (o osRegistry) DeleteValue(root Root, key, name string) error {
	k, err := o.open(root, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return mapRegErr(k.DeleteValue(name))
}

func (o osRegistry) KeyExists(root Root, key string) (bool, error) {
	k, err := o.open(root, key, registry.QUERY_VALUE)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	k.Close()
	return true, nil
}

func (osRegistry) CreateKey(root Root, key, sddl string) error {
	access := uint32(registry.CREATE_SUB_KEY | registry.SET_VALUE | registry.WOW64_64KEY)
	if sddl != "" {
		access |= windows.WRITE_DAC | windows.WRITE_OWNER
	}
	k, _, err := registry.CreateKey(hive(root), key, access)
	if err != nil {
		return err
	}
	defer k.Close()
	if sddl == "" {
		return nil
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	return setSecurity(windows.Handle(k), windows.SE_REGISTRY_KEY, sd, sddl)
}

func (osRegistry) DeleteKey(root Root, key string) error {
	err := registry.DeleteKey(hive(root), key) // RegDeleteKey: the 64-bit view, as the key was made
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

// KeySecurity is read only for the installer's record, and through the same reader Unravis and the keeper use (datadir.ReadAnchor), so
// setup checks exactly what they will check.
func (osRegistry) KeySecurity(root Root, key string) (datadir.FolderSecurity, error) {
	if root != HKLM || !strings.EqualFold(key, AnchorKey) {
		return datadir.FolderSecurity{}, errors.New("setup reads the permissions of the installer's record only")
	}
	a, err := datadir.ReadAnchor()
	if errors.Is(err, datadir.ErrNoAnchor) {
		return datadir.FolderSecurity{}, ErrNotFound
	}
	return a.Security, err
}

// setSecurity sets the owner (when the text has one) and the access list; "P" in the DACL flags stops inheritance.
func setSecurity(h windows.Handle, typ windows.SE_OBJECT_TYPE, sd *windows.SECURITY_DESCRIPTOR, sddl string) error {
	info, owner, dacl, err := securityParts(sd, sddl)
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(h, typ, info, owner, nil, dacl, nil)
}

func securityParts(sd *windows.SECURITY_DESCRIPTOR, sddl string) (windows.SECURITY_INFORMATION, *windows.SID, *windows.ACL, error) {
	var info windows.SECURITY_INFORMATION = windows.DACL_SECURITY_INFORMATION
	dacl, _, err := sd.DACL()
	if err != nil {
		return 0, nil, nil, err
	}
	if daclProtected(sddl) {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		info |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	owner, _, err := sd.Owner()
	if err == nil && owner != nil {
		info |= windows.OWNER_SECURITY_INFORMATION
	} else {
		owner = nil
	}
	return info, owner, dacl, nil
}

var daclFlags = regexp.MustCompile(`D:([A-Z]*)\(`)

func daclProtected(sddl string) bool {
	m := daclFlags.FindStringSubmatch(sddl)
	return m != nil && strings.Contains(m[1], "P")
}

/* ------------------------------------------------------------ services */

type osServices struct{}

// withService opens the SC manager and the service with full rights, for the calls (Create goes through mgr.Connect directly,
// Config, SetConfig and Delete here) that really need to change or remove the service.
func withService(name string, f func(s *mgr.Service) error) error {
	return withServiceAccess(windows.SC_MANAGER_ALL_ACCESS, windows.SERVICE_ALL_ACCESS, name, f)
}

// withServiceAccess is withService but with exactly the rights the caller asks for (mgr.Connect and mgr.OpenService always ask for
// ALL_ACCESS, which an unelevated process is refused even to look at a service that a machine-scope install left behind). State asks
// only SC_MANAGER_CONNECT and SERVICE_QUERY_STATUS, so it still works unelevated; Stop and Start ask for a little more.
func withServiceAccess(scAccess, svcAccess uint32, name string, f func(s *mgr.Service) error) error {
	h, err := windows.OpenSCManager(nil, nil, scAccess)
	if err != nil {
		return err
	}
	defer windows.CloseServiceHandle(h)
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	sh, err := windows.OpenService(h, namePtr, svcAccess)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return ErrNotFound
		}
		return err
	}
	s := &mgr.Service{Name: name, Handle: sh}
	defer s.Close()
	return f(s)
}

func (osServices) State(name string) (blackbox.SvcState, error) {
	st := blackbox.SvcNotInstalled
	err := withServiceAccess(windows.SC_MANAGER_CONNECT, windows.SERVICE_QUERY_STATUS, name, func(s *mgr.Service) error {
		q, err := s.Query()
		if err != nil {
			return err
		}
		switch q.State {
		case svc.Stopped:
			st = blackbox.SvcStopped
		case svc.StartPending:
			st = blackbox.SvcStartPending
		case svc.StopPending:
			st = blackbox.SvcStopPending
		default:
			st = blackbox.SvcRunning
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return blackbox.SvcNotInstalled, nil
	}
	return st, err
}

func (osServices) Create(spec ServiceSpec) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	c := mgr.Config{DisplayName: spec.DisplayName, Description: spec.Description, StartType: mgr.StartAutomatic,
		DelayedAutoStart: spec.DelayedStart, ServiceStartName: spec.Account}
	if spec.SIDUnrestricted {
		c.SidType = windows.SERVICE_SID_TYPE_UNRESTRICTED
	}
	s, err := m.CreateService(spec.Name, spec.ExePath, c, spec.Args...)
	if err != nil {
		return err
	}
	defer s.Close()
	// Restart after a crash: quickly twice, then after a minute; the count resets after a day. Best effort.
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 15 * time.Second}, {Type: mgr.ServiceRestart, Delay: time.Minute}}, 86400)
	return nil
}

func (osServices) Config(name string) (ServiceSpec, error) {
	var spec ServiceSpec
	err := withService(name, func(s *mgr.Service) error {
		c, err := s.Config()
		if err != nil {
			return err
		}
		parts, err := windows.DecomposeCommandLine(c.BinaryPathName)
		if err != nil || len(parts) == 0 {
			return fmt.Errorf("the service %s has a program path setup cannot read", name)
		}
		account := c.ServiceStartName
		if strings.EqualFold(account, "LocalSystem") {
			account = ""
		}
		spec = ServiceSpec{Name: name, DisplayName: c.DisplayName, Description: c.Description, ExePath: parts[0], Args: parts[1:],
			Account: account, SIDUnrestricted: c.SidType == windows.SERVICE_SID_TYPE_UNRESTRICTED, DelayedStart: c.DelayedAutoStart}
		return nil
	})
	return spec, err
}

// SetConfig changes the program and its names; the account is passed as "no change", so a keeper's sealed files stay readable.
func (osServices) SetConfig(spec ServiceSpec) error {
	return withService(spec.Name, func(s *mgr.Service) error {
		cmd := windows.EscapeArg(spec.ExePath)
		for _, a := range spec.Args {
			cmd += " " + windows.EscapeArg(a)
		}
		bin, _ := windows.UTF16PtrFromString(cmd)
		disp, _ := windows.UTF16PtrFromString(spec.DisplayName)
		const noChange = windows.SERVICE_NO_CHANGE
		if err := windows.ChangeServiceConfig(s.Handle, noChange, noChange, noChange, bin, nil, nil, nil, nil, nil, disp); err != nil {
			return err
		}
		desc, _ := windows.UTF16PtrFromString(spec.Description)
		d := windows.SERVICE_DESCRIPTION{Description: desc}
		return windows.ChangeServiceConfig2(s.Handle, windows.SERVICE_CONFIG_DESCRIPTION, (*byte)(unsafe.Pointer(&d)))
	})
}

func (osServices) Start(name string) error {
	err := withServiceAccess(windows.SC_MANAGER_CONNECT, windows.SERVICE_START|windows.SERVICE_QUERY_STATUS, name, func(s *mgr.Service) error {
		err := s.Start()
		if errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return nil
		}
		return err
	})
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return ErrAccessDenied
	}
	return err
}

func (osServices) Stop(name string) error {
	err := withServiceAccess(windows.SC_MANAGER_CONNECT, windows.SERVICE_STOP|windows.SERVICE_QUERY_STATUS, name, func(s *mgr.Service) error {
		_, err := s.Control(svc.Stop)
		if errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return nil
		}
		return err
	})
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return ErrAccessDenied
	}
	return err
}

func (osServices) Delete(name string) error {
	err := withService(name, func(s *mgr.Service) error { return s.Delete() })
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

/* ------------------------------------------------------------ firewall */

// osFirewall uses netsh, which every supported Windows has. Its "show" output is translated on non-English Windows, so Get only trusts
// the exit code for "the rule exists" and reads the port and addresses when the labels are the English ones.
type osFirewall struct{}

func netsh(args ...string) ([]byte, error) {
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "netsh.exe"), args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.CombinedOutput()
}

func (osFirewall) Get(name string) (FirewallRule, error) {
	out, err := netsh("advfirewall", "firewall", "show", "rule", "name="+name, "verbose")
	if err != nil {
		return FirewallRule{}, ErrNotFound
	}
	r := FirewallRule{Name: name}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "LocalPort":
			r.Port, _ = strconv.Atoi(v)
		case "RemoteIP":
			for _, a := range strings.Split(v, ",") {
				r.RemoteAddresses = append(r.RemoteAddresses, strings.TrimSuffix(strings.TrimSpace(a), "/255.255.255.255"))
			}
		case "Program":
			r.Program = v
		}
	}
	return r, nil
}

func (osFirewall) Add(rule FirewallRule) error {
	if err := CheckRemoteAddresses(rule.RemoteAddresses); err != nil || rule.Port <= 0 || rule.Port > 65535 {
		return errors.New("the firewall rule is not valid")
	}
	args := []string{"advfirewall", "firewall", "add", "rule", "name=" + rule.Name, "dir=in", "action=allow", "protocol=TCP",
		"localport=" + strconv.Itoa(rule.Port), "remoteip=" + strings.Join(rule.RemoteAddresses, ","), "profile=any", "enable=yes"}
	if rule.Program != "" {
		args = append(args, "program="+rule.Program)
	}
	if out, err := netsh(args...); err != nil {
		return fmt.Errorf("netsh: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (osFirewall) Remove(name string) error {
	if _, err := netsh("advfirewall", "firewall", "show", "rule", "name="+name); err != nil {
		return nil // already gone
	}
	if out, err := netsh("advfirewall", "firewall", "delete", "rule", "name="+name); err != nil {
		return fmt.Errorf("netsh: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

/* ------------------------------------------------------------ permissions */

type osSecurity struct{}

func (osSecurity) Read(path string) (datadir.FolderSecurity, error) { return datadir.ReadFolderSecurity(path) }

func (osSecurity) SDDL(path string) (string, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	return sd.String(), nil
}

func (osSecurity) SetSDDL(path, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	info, owner, dacl, err := securityParts(sd, sddl)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, owner, nil, dacl, nil)
}

func (osSecurity) MkdirSDDL(path, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if err := windows.CreateDirectory(p, sa); err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return os.ErrExist
		}
		return err
	}
	return nil
}

// CreateFileSDDL makes the file new (CREATE_NEW: an existing file is never opened) with the security descriptor in the create call. If
// Windows will not give the file the owner the list names (a different account approved the elevation), it is made with the default
// owner and the same access list: the list is what keeps the key from others.
func (osSecurity) CreateFileSDDL(path, sddl string, data []byte) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	create := func(sddl string) (windows.Handle, error) {
		sd, err := windows.SecurityDescriptorFromString(sddl)
		if err != nil {
			return windows.InvalidHandle, err
		}
		sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
		sa.Length = uint32(unsafe.Sizeof(*sa))
		return windows.CreateFile(p, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	}
	h, err := create(sddl)
	if errors.Is(err, windows.ERROR_INVALID_OWNER) {
		if i := strings.Index(sddl, "D:"); i > 0 {
			h, err = create(sddl[i:])
		}
	}
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return os.ErrExist
		}
		return err
	}
	var werr error
	for off := 0; off < len(data) && werr == nil; {
		var n uint32
		werr = windows.WriteFile(h, data[off:], &n, nil)
		off += int(n)
	}
	cerr := windows.CloseHandle(h)
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
	}
	return werr
}

/* ------------------------------------------------------------ paths */

type osPaths struct{}

// Canonical is GetFullPathName (dot parts resolved, trailing dots and spaces dropped) and then GetLongPathName on the part of the path
// that exists (8.3 names written out). The rest of the path, which does not exist yet, is appended as it is.
func (osPaths) Canonical(path string) (string, error) {
	full, err := windows.FullPath(path)
	if err != nil {
		return "", err
	}
	cur, rest := full, ""
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return full, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
	u, err := windows.UTF16PtrFromString(cur)
	if err != nil {
		return "", err
	}
	n, err := windows.GetLongPathName(u, nil, 0)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, n+1)
	n, err = windows.GetLongPathName(u, &buf[0], uint32(len(buf)))
	if err != nil {
		return "", err
	}
	long := windows.UTF16ToString(buf[:n])
	if rest == "" {
		return long, nil
	}
	return filepath.Join(long, rest), nil
}

/* ------------------------------------------------------------ processes */

type osProcesses struct{}

func (osProcesses) List() ([]Process, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []Process
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		exe := imagePath(e.ProcessID)
		if exe == "" {
			continue // a process this account cannot look at is not one setup placed for it
		}
		q := Process{PID: e.ProcessID, Exe: exe}
		if strings.EqualFold(filepath.Base(exe), AppExeName) {
			q.SID = processSID(e.ProcessID)
		}
		out = append(out, q)
	}
	return out, nil
}

// processSID is the account a process runs as, or UnknownOwner when its token cannot be read.
func processSID(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return UnknownOwner
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return UnknownOwner
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil || u.User.Sid == nil {
		return UnknownOwner
	}
	return u.User.Sid.String()
}

func imagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func (osProcesses) Terminate(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return err
	}
	_, err = windows.WaitForSingleObject(h, 10000)
	return err
}

/* ------------------------------------------------------------ machine */

type osMachine struct{}

func (osMachine) Windows() (WindowsVersion, error) {
	v := windows.RtlGetVersion()
	return WindowsVersion{Major: v.MajorVersion, Minor: v.MinorVersion, Build: v.BuildNumber, Is64: unsafe.Sizeof(uintptr(0)) == 8,
		Server: v.ProductType != 1 /* VER_NT_WORKSTATION */}, nil
}

func (osMachine) FreeBytes(path string) (uint64, error) {
	d, err := blackbox.OSDisk{}.Free(path)
	return d.Free, err
}

func (osMachine) PortFree(bind string, port int) error {
	l, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return l.Close()
}

// Browser is Edge (on every supported Windows) or Chrome, looked up where their installers put them.
func (osMachine) Browser() string {
	var cands []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		if base := os.Getenv(env); base != "" {
			cands = append(cands, filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`), filepath.Join(base, `Google\Chrome\Application\chrome.exe`))
		}
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// WebView2 reports the Evergreen runtime (machine-wide or per user).
func (osMachine) WebView2() bool {
	const client = `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	for _, k := range []struct {
		root registry.Key
		path string
	}{{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\` + client}, {registry.LOCAL_MACHINE, `SOFTWARE\` + client}, {registry.CURRENT_USER, `Software\` + client}} {
		if key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE); err == nil {
			v, _, err := key.GetStringValue("pv")
			key.Close()
			if err == nil && v != "" && v != "0.0.0.0" {
				return true
			}
		}
	}
	return false
}

func (osMachine) PendingReboot() bool {
	for _, k := range []string{`SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`} {
		if key, err := registry.OpenKey(registry.LOCAL_MACHINE, k, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
			key.Close()
			return true
		}
	}
	if key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager`, registry.QUERY_VALUE); err == nil {
		defer key.Close()
		if v, _, err := key.GetStringsValue("PendingFileRenameOperations"); err == nil && len(v) > 0 {
			return true
		}
	}
	return false
}

func (osMachine) IsAdmin() bool { return windows.GetCurrentProcessToken().IsElevated() }

// CanElevate is true for a member of Administrators (a split token) and for a standard account on a PC whose UAC asks for an
// administrator's password (ConsentPromptBehaviorUser is not 0, "automatically deny").
func (osMachine) CanElevate() bool {
	tok := windows.GetCurrentProcessToken()
	if tok.IsElevated() {
		return true
	}
	if linked, err := tok.GetLinkedToken(); err == nil {
		linked.Close()
		return true
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return true
	}
	defer key.Close()
	v, _, err := key.GetIntegerValue("ConsentPromptBehaviorUser")
	return err != nil || v != 0
}

func (osMachine) DriveExists(root string) bool {
	fi, err := os.Stat(root)
	return err == nil && fi.IsDir()
}

/* ------------------------------------------------------------ helpers */

func runVersion(exe string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--version")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// deleteLater asks Windows to delete a file at the next restart (MOVEFILE_DELAY_UNTIL_REBOOT needs administrator rights; a
// just-for-me uninstall leaves the copied setup program instead and says so).
func deleteLater(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(p, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}

// setupLock is one machine-wide mutex, so two setups (two windows, a window and a silent run, or a double click) never run at once.
func setupLock() (func(), error) {
	name, _ := windows.UTF16PtrFromString(`Global\UnravisSetupRun`)
	h, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil, ErrAlreadyRunning // held by a setup of another account
	}
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, err
	}
	ev, werr := windows.WaitForSingleObject(h, 0)
	if werr != nil || (ev != windows.WAIT_OBJECT_0 && ev != windows.WAIT_ABANDONED) {
		windows.CloseHandle(h)
		return nil, ErrAlreadyRunning
	}
	return func() {
		_ = windows.ReleaseMutex(h)
		windows.CloseHandle(h)
	}, nil
}
