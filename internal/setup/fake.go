package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/datadir"
)

// The fakes stand in for the machine in tests and in the screenshot capture mode. They keep their state in memory (files stay real,
// under a temporary folder) and fail on request: Faults maps an operation name such as "svc.Create:UnravisKeeper" or
// "reg.SetString:HKLM\SOFTWARE\Unravis\SharedRoot" to the error it returns.

// Faults is a shared set of injected failures.
type Faults struct {
	mu   sync.Mutex
	m    map[string]error
	once map[string]bool
	// calls counts every checked call since StartCounting; failAt makes the failAt-th one fail with failErr (0: none).
	counting bool
	calls    []string
	failAt   int
	failErr  error
}

// StartCounting starts numbering the checked calls (1, 2, ...) and records their names. failAt, when above 0, makes that numbered call
// fail with err, once: a test can fail every fake-system call of a run in turn.
func (f *Faults) StartCounting(failAt int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counting, f.calls, f.failAt, f.failErr = true, nil, failAt, err
}

// StopCounting stops numbering and returns the names of the calls made since StartCounting, in order.
func (f *Faults) StopCounting() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counting, f.failAt, f.failErr = false, 0, nil
	c := f.calls
	f.calls = nil
	return c
}

// Once makes op fail with err the next time only (a fault that clears itself, so the undo of the same operation works).
func (f *Faults) Once(op string, err error) {
	f.Set(op, err)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.once == nil {
		f.once = map[string]bool{}
	}
	f.once[strings.ToLower(op)] = true
}

// Set makes op fail with err (nil clears it).
func (f *Faults) Set(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]error{}
	}
	if err == nil {
		delete(f.m, strings.ToLower(op))
		return
	}
	f.m[strings.ToLower(op)] = err
}

func (f *Faults) check(op string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := strings.ToLower(op)
	if f.counting {
		f.calls = append(f.calls, op)
		if f.failAt > 0 && len(f.calls) == f.failAt {
			return f.failErr
		}
	}
	err := f.m[k]
	if err != nil && f.once[k] {
		delete(f.m, k)
		delete(f.once, k)
	}
	return err
}

// ---- registry ----

type fakeValue struct {
	S     string
	D     uint32
	DWORD bool
}

type fakeKey struct {
	values map[string]fakeValue
	sddl   string
}

// FakeRegistry is an in-memory registry.
type FakeRegistry struct {
	mu     sync.Mutex
	keys   map[string]*fakeKey // "HKLM\software\unravis" (lower case)
	Faults *Faults
}

func regPath(root Root, key string) string { return strings.ToLower(root.String() + `\` + strings.Trim(key, `\`)) }

func (r *FakeRegistry) key(root Root, key string) *fakeKey {
	if r.keys == nil {
		r.keys = map[string]*fakeKey{}
	}
	return r.keys[regPath(root, key)]
}

func (r *FakeRegistry) GetString(root Root, key, name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := r.key(root, key)
	if k == nil {
		return "", ErrNotFound
	}
	v, ok := k.values[strings.ToLower(name)]
	if !ok {
		return "", ErrNotFound
	}
	if v.DWORD {
		return "", ErrWrongType
	}
	return v.S, nil
}

func (r *FakeRegistry) GetDWORD(root Root, key, name string) (uint32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := r.key(root, key)
	if k == nil {
		return 0, ErrNotFound
	}
	v, ok := k.values[strings.ToLower(name)]
	if !ok {
		return 0, ErrNotFound
	}
	if !v.DWORD {
		return 0, ErrWrongType
	}
	return v.D, nil
}

func (r *FakeRegistry) set(root Root, key, name string, v fakeValue) error {
	if err := r.Faults.check("reg.Set:" + root.String() + `\` + key + `\` + name); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := r.key(root, key)
	if k == nil {
		return fmt.Errorf("registry key %s\\%s does not exist", root, key)
	}
	k.values[strings.ToLower(name)] = v
	return nil
}

func (r *FakeRegistry) SetString(root Root, key, name, value string) error {
	return r.set(root, key, name, fakeValue{S: value})
}

func (r *FakeRegistry) SetDWORD(root Root, key, name string, value uint32) error {
	return r.set(root, key, name, fakeValue{D: value, DWORD: true})
}

func (r *FakeRegistry) DeleteValue(root Root, key, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k := r.key(root, key); k != nil {
		delete(k.values, strings.ToLower(name))
	}
	return nil
}

func (r *FakeRegistry) KeyExists(root Root, key string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.key(root, key) != nil, nil
}

func (r *FakeRegistry) CreateKey(root Root, key, sddl string) error {
	if err := r.Faults.check("reg.CreateKey:" + root.String() + `\` + key); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := r.key(root, key)
	if k == nil {
		k = &fakeKey{values: map[string]fakeValue{}, sddl: "O:S-1-5-21-1000D:AI(A;CIID;KA;;;S-1-5-21-1000)"}
		r.keys[regPath(root, key)] = k
	}
	if sddl != "" {
		k.sddl = sddl
	}
	return nil
}

func (r *FakeRegistry) DeleteKey(root Root, key string) error {
	if err := r.Faults.check("reg.DeleteKey:" + root.String() + `\` + key); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p := regPath(root, key)
	for other := range r.keys {
		if strings.HasPrefix(other, p+`\`) {
			return fmt.Errorf("registry key %s has subkeys", p)
		}
	}
	delete(r.keys, p)
	return nil
}

func (r *FakeRegistry) KeySecurity(root Root, key string) (datadir.FolderSecurity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := r.key(root, key)
	if k == nil {
		return datadir.FolderSecurity{}, ErrNotFound
	}
	return ParseSDDL(k.sddl)
}

// SetKeySDDL changes a key's owner and access list (tests: someone loosened the record).
func (r *FakeRegistry) SetKeySDDL(root Root, key, sddl string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if k := r.key(root, key); k != nil {
		k.sddl = sddl
	}
}

// Snapshot is a stable text form of every key and value.
func (r *FakeRegistry) Snapshot() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var lines []string
	for p, k := range r.keys {
		lines = append(lines, p+" ["+k.sddl+"]")
		for n, v := range k.values {
			lines = append(lines, fmt.Sprintf("%s\\%s=%q/%d/%t", p, n, v.S, v.D, v.DWORD))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// ---- services ----

type fakeService struct {
	spec  ServiceSpec
	state blackbox.SvcState
}

// FakeServices is an in-memory service manager.
type FakeServices struct {
	mu     sync.Mutex
	m      map[string]*fakeService
	Faults *Faults
	// Log lists every call, for tests that check order.
	Log []string
}

func (s *FakeServices) get(name string) *fakeService {
	if s.m == nil {
		s.m = map[string]*fakeService{}
	}
	return s.m[strings.ToLower(name)]
}

func (s *FakeServices) note(op, name string) error {
	s.Log = append(s.Log, op+" "+name)
	return s.Faults.check("svc." + op + ":" + name)
}

func (s *FakeServices) State(name string) (blackbox.SvcState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.Faults.check("svc.State:" + name); err != nil {
		return 0, err
	}
	if v := s.get(name); v != nil {
		return v.state, nil
	}
	return blackbox.SvcNotInstalled, nil
}

func (s *FakeServices) Create(spec ServiceSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.note("Create", spec.Name); err != nil {
		return err
	}
	if s.get(spec.Name) != nil {
		return fmt.Errorf("the service %s already exists", spec.Name)
	}
	s.m[strings.ToLower(spec.Name)] = &fakeService{spec: spec, state: blackbox.SvcStopped}
	return nil
}

func (s *FakeServices) Config(name string) (ServiceSpec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.get(name)
	if v == nil {
		return ServiceSpec{}, ErrNotFound
	}
	return v.spec, nil
}

func (s *FakeServices) SetConfig(spec ServiceSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.note("SetConfig", spec.Name); err != nil {
		return err
	}
	v := s.get(spec.Name)
	if v == nil {
		return ErrNotFound
	}
	account := v.spec.Account
	v.spec = spec
	v.spec.Account = account // SetConfig never changes the account
	return nil
}

func (s *FakeServices) Start(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.note("Start", name); err != nil {
		return err
	}
	v := s.get(name)
	if v == nil {
		return ErrNotFound
	}
	v.state = blackbox.SvcRunning
	return nil
}

func (s *FakeServices) Stop(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.note("Stop", name); err != nil {
		return err
	}
	v := s.get(name)
	if v == nil {
		return ErrNotFound
	}
	v.state = blackbox.SvcStopped
	return nil
}

func (s *FakeServices) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.note("Delete", name); err != nil {
		return err
	}
	if s.get(name) == nil {
		return ErrNotFound
	}
	delete(s.m, strings.ToLower(name))
	return nil
}

// Put adds a service as if it were already installed.
func (s *FakeServices) Put(spec ServiceSpec, state blackbox.SvcState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.get(spec.Name)
	s.m[strings.ToLower(spec.Name)] = &fakeService{spec: spec, state: state}
}

// Snapshot is a stable text form of every service.
func (s *FakeServices) Snapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var lines []string
	for _, v := range s.m {
		lines = append(lines, fmt.Sprintf("%s %s %q %v %s %s", v.spec.Name, v.state, v.spec.ExePath, v.spec.Args, v.spec.Account, v.spec.DisplayName))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// ---- firewall ----

// FakeFirewall is an in-memory firewall.
type FakeFirewall struct {
	mu     sync.Mutex
	m      map[string]FirewallRule
	Faults *Faults
}

func (f *FakeFirewall) Get(name string) (FirewallRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.m[strings.ToLower(name)]
	if !ok {
		return FirewallRule{}, ErrNotFound
	}
	return r, nil
}

func (f *FakeFirewall) Add(rule FirewallRule) error {
	if err := f.Faults.check("fw.Add:" + rule.Name); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]FirewallRule{}
	}
	f.m[strings.ToLower(rule.Name)] = rule
	return nil
}

func (f *FakeFirewall) Remove(name string) error {
	if err := f.Faults.check("fw.Remove:" + name); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, strings.ToLower(name))
	return nil
}

// Snapshot is a stable text form of every rule.
func (f *FakeFirewall) Snapshot() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var lines []string
	for _, r := range f.m {
		lines = append(lines, fmt.Sprintf("%s %d %v %s", r.Name, r.Port, r.RemoteAddresses, r.Program))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// ---- file security ----

type fakeACL struct {
	sddl string
	id   string // the folder this list was set on; a folder made again later does not keep it
}

// FakeSecurity keeps owners and access lists in memory for real folders. A folder nobody set a list on is owned by the person (as a
// folder they made would be) and has an inherited list.
type FakeSecurity struct {
	mu     sync.Mutex
	m      map[string]fakeACL
	Faults *Faults
}

// DefaultUserSDDL is the list of a folder a person made.
const DefaultUserSDDL = "O:S-1-5-21-1000D:AI(A;OICIID;FA;;;S-1-5-21-1000)(A;OICIID;FA;;;SY)"

func (f *FakeSecurity) current(path string) (string, string, error) {
	id, reparse, err := fileID(path)
	if err != nil {
		return "", "", err
	}
	_ = reparse
	if a, ok := f.m[strings.ToLower(filepath.Clean(path))]; ok && a.id == id {
		return a.sddl, id, nil
	}
	return DefaultUserSDDL, id, nil
}

func (f *FakeSecurity) Read(path string) (datadir.FolderSecurity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.Faults.check("sec.Read:" + path); err != nil {
		return datadir.FolderSecurity{}, err
	}
	sddl, id, err := f.current(path)
	if err != nil {
		return datadir.FolderSecurity{}, err
	}
	fs, err := ParseSDDL(sddl)
	fs.ID = id
	_, fs.Reparse, _ = fileID(path)
	return fs, err
}

func (f *FakeSecurity) SDDL(path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, _, err := f.current(path)
	return s, err
}

func (f *FakeSecurity) SetSDDL(path, sddl string) error {
	if err := f.Faults.check("sec.SetSDDL:" + path); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id, _, err := fileID(path)
	if err != nil {
		return err
	}
	if _, err := ParseSDDL(sddl); err != nil {
		return err
	}
	if f.m == nil {
		f.m = map[string]fakeACL{}
	}
	if sddl == DefaultUserSDDL {
		// Back to what a folder nobody set a list on has: the snapshot equals the one from before.
		delete(f.m, strings.ToLower(filepath.Clean(path)))
		return nil
	}
	f.m[strings.ToLower(filepath.Clean(path))] = fakeACL{sddl: sddl, id: id}
	return nil
}

func (f *FakeSecurity) MkdirSDDL(path, sddl string) error {
	if err := f.Faults.check("sec.MkdirSDDL:" + path); err != nil {
		return err
	}
	if _, err := ParseSDDL(sddl); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		return err
	}
	return f.SetSDDL(path, sddl)
}

// Snapshot lists the lists set on folders that still exist.
func (f *FakeSecurity) Snapshot() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var lines []string
	for p, a := range f.m {
		if id, _, err := fileID(p); err == nil && id == a.id {
			lines = append(lines, p+" "+a.sddl)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// fileID returns a folder or file's ID, or datadir.ErrNotShared when it does not exist.
func fileID(path string) (string, bool, error) {
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, datadir.ErrNotShared
		}
		return "", false, err
	}
	return osFileID(path)
}

// ---- processes ----

// FakeProcesses is a list of running programs.
type FakeProcesses struct {
	mu     sync.Mutex
	List_  []Process
	Faults *Faults
	nextID uint32
}

func (p *FakeProcesses) List() ([]Process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Process(nil), p.List_...), nil
}

func (p *FakeProcesses) Terminate(pid uint32) error {
	if err := p.Faults.check(fmt.Sprintf("proc.Terminate:%d", pid)); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, q := range p.List_ {
		if q.PID == pid {
			p.List_ = append(p.List_[:i], p.List_[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// Run adds a running program.
func (p *FakeProcesses) Run(exe string) uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID += 100
	p.List_ = append(p.List_, Process{PID: p.nextID, Exe: exe})
	return p.nextID
}

// ---- machine ----

// FakeMachine answers with its fields.
type FakeMachine struct {
	Version     WindowsVersion
	Free        map[string]uint64 // by drive ("C:"); missing drives have plenty
	BusyPorts   map[int]bool
	BrowserPath string
	HasWebView2 bool
	Reboot      bool
	Admin       bool
	Elevate     bool
	Drives      map[string]bool // "C:\" → present; nil means every drive exists
}

func (m *FakeMachine) Windows() (WindowsVersion, error) { return m.Version, nil }
func (m *FakeMachine) FreeBytes(path string) (uint64, error) {
	if v, ok := m.Free[strings.ToUpper(filepath.VolumeName(path))]; ok {
		return v, nil
	}
	return 500 << 30, nil
}
func (m *FakeMachine) PortFree(bind string, port int) error {
	if m.BusyPorts[port] {
		return fmt.Errorf("port %d is in use", port)
	}
	return nil
}
func (m *FakeMachine) Browser() string     { return m.BrowserPath }
func (m *FakeMachine) WebView2() bool      { return m.HasWebView2 }
func (m *FakeMachine) PendingReboot() bool { return m.Reboot }
func (m *FakeMachine) IsAdmin() bool       { return m.Admin }
func (m *FakeMachine) CanElevate() bool    { return m.Elevate || m.Admin }
func (m *FakeMachine) DriveExists(root string) bool {
	if m.Drives == nil {
		return true
	}
	return m.Drives[strings.ToUpper(root)]
}

// fakeDisk answers blackbox's DiskSpace from the machine.
type fakeDisk struct{ m *FakeMachine }

func (d fakeDisk) Free(path string) (blackbox.DiskInfo, error) {
	f, _ := d.m.FreeBytes(path)
	return blackbox.DiskInfo{Free: f, Total: f * 2}, nil
}

// Fakes are the parts of a fake System, for tests to look at and to change.
type Fakes struct {
	Reg    *FakeRegistry
	Svc    *FakeServices
	FW     *FakeFirewall
	Sec    *FakeSecurity
	Proc   *FakeProcesses
	Mach   *FakeMachine
	Faults *Faults
	// Versions is what RunVersion prints for a program (missing: the version of the payload).
	Versions map[string]string
	// ProbeErr makes the Blackbox status check fail.
	ProbeErr error
	// LaterDeleted lists files DeleteLater was asked to remove.
	LaterDeleted []string
	locked       bool
	lockMu       sync.Mutex
	version      string
}

// NewFakeSystem returns a System whose machine is all fakes: a Windows 11 64-bit PC where the person is an administrator, with Edge
// and WebView2. version is what the new program prints for --version.
func NewFakeSystem(version string) (*System, *Fakes) {
	faults := &Faults{}
	f := &Fakes{
		Reg: &FakeRegistry{Faults: faults}, Svc: &FakeServices{Faults: faults}, FW: &FakeFirewall{Faults: faults},
		Sec: &FakeSecurity{Faults: faults}, Proc: &FakeProcesses{Faults: faults},
		Mach: &FakeMachine{Version: WindowsVersion{Major: 10, Build: 22631, Is64: true}, BrowserPath: `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			HasWebView2: true, Admin: true, Elevate: true},
		Faults: faults, Versions: map[string]string{}, version: version,
	}
	s := &System{Reg: f.Reg, Svc: f.Svc, FW: f.FW, Sec: f.Sec, Proc: f.Proc, Mach: f.Mach, Disk: fakeDisk{f.Mach},
		Trust: TrustWith(f.Sec, f.Reg),
		Probe: func(string, int, string) error { return f.ProbeErr },
		RunVersion: func(exe string) (string, error) {
			if err := faults.check("run.Version"); err != nil {
				return "", err
			}
			if v, ok := f.Versions[strings.ToLower(exe)]; ok {
				return v, nil
			}
			if _, err := os.Stat(exe); err != nil {
				return "", err
			}
			return "unravis " + f.version, nil
		},
		DeleteLater: func(p string) error { f.LaterDeleted = append(f.LaterDeleted, p); return nil },
		Lock: func() (func(), error) {
			f.lockMu.Lock()
			defer f.lockMu.Unlock()
			if f.locked {
				return nil, ErrAlreadyRunning
			}
			f.locked = true
			return func() { f.lockMu.Lock(); f.locked = false; f.lockMu.Unlock() }, nil
		},
		Sleep: func(time.Duration) {},
		Now:   func() time.Time { return time.Date(2026, 10, 28, 9, 30, 0, 0, time.UTC) },
	}
	return s, f
}

// Snapshot is the whole fake machine as text: registry, services, firewall and permissions.
func (f *Fakes) Snapshot() string {
	return "REG\n" + f.Reg.Snapshot() + "\nSVC\n" + f.Svc.Snapshot() + "\nFW\n" + f.FW.Snapshot() + "\nSEC\n" + f.Sec.Snapshot()
}
