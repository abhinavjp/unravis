// Package fake holds in-memory implementations of the discover interfaces, for
// tests on any OS. Paths are Windows paths and compare ignoring case. A denied
// path, key or call returns an error wrapping discover.ErrAccessDenied, as the
// real implementation does.
package fake

import (
	"context"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/importer"
)

// Env bundles one of each fake. Build the discover.Env with Env.
type Env struct {
	FS       *FS
	Procs    *Procs
	Services *Services
	Registry *Registry
	Channels *Channels
	Machine  *Machine
}

// New returns an empty fake host: no files, no processes, a standard user on
// one core in UTC.
func New() *Env {
	return &Env{
		FS:       &FS{nodes: map[string]*node{}},
		Procs:    &Procs{},
		Services: &Services{},
		Registry: &Registry{vals: map[string]string{}, deny: map[string]bool{}},
		Channels: &Channels{},
		Machine:  &Machine{NameList: []string{"WEB-A"}, TZ: "UTC", Cores: 1},
	}
}

// Env returns the discover.Env over the fakes. importers may be nil.
func (e *Env) Env(importers *importer.Registry) discover.Env {
	return discover.Env{
		FS: e.FS, Procs: e.Procs, Services: e.Services, Registry: e.Registry,
		Channels: e.Channels, Machine: e.Machine, Importers: importers,
	}
}

// DenyAll makes every call on every fake return access denied, as a standard
// user sees on a locked-down host. Machine stays readable.
func (e *Env) DenyAll() {
	e.FS.denyAll = true
	e.Procs.Err = discover.ErrAccessDenied
	e.Services.Err = discover.ErrAccessDenied
	e.Registry.denyAll = true
	e.Channels.ListErr = discover.ErrAccessDenied
	e.Channels.DenyAll = true
}

// --- FS ---

type node struct {
	info    discover.FileInfo
	display string // path as added, for Final
	content []byte
}

// FS is an in-memory file tree.
type FS struct {
	mu      sync.Mutex
	nodes   map[string]*node
	deny    []string // lower-case path prefixes
	denyAll bool
	// Calls counts calls by method name, for assertions.
	Calls map[string]int
}

func key(p string) string {
	return strings.ToLower(strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`))
}

// AddDir adds a folder and its parents.
func (f *FS) AddDir(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addDir(path)
}

func (f *FS) addDir(path string) {
	path = strings.TrimRight(strings.ReplaceAll(path, "/", `\`), `\`)
	for p := path; p != ""; p = discover.WinDir(p) {
		k := key(p)
		if _, ok := f.nodes[k]; ok {
			break
		}
		f.nodes[k] = &node{display: p, info: discover.FileInfo{Name: discover.WinBase(p), IsDir: true}}
		if len(p) <= 3 {
			break
		}
	}
}

// AddFile adds a file with the given size and modification time. content is
// what Head returns.
func (f *FS) AddFile(path string, size int64, mod time.Time, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addDir(discover.WinDir(path))
	f.nodes[key(path)] = &node{
		display: path, content: content,
		info: discover.FileInfo{Name: discover.WinBase(path), Size: size, ModTime: mod, VolumeSerial: 0x1234ABCD, FileID: uint64(len(f.nodes) + 1)},
	}
}

// AddReparse adds a junction or symlink at path. Anything below it is not
// reachable by a detector, and Final reports reparse for paths through it.
func (f *FS) AddReparse(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addDir(discover.WinDir(path))
	f.nodes[key(path)] = &node{
		display: path,
		info:    discover.FileInfo{Name: discover.WinBase(path), IsDir: true, Reparse: true},
	}
}

// Deny makes every call on path, or on anything below it, return access denied.
func (f *FS) Deny(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deny = append(f.deny, key(path))
}

func (f *FS) denied(path string) bool {
	if f.denyAll {
		return true
	}
	k := key(path)
	for _, d := range f.deny {
		if k == d || strings.HasPrefix(k, d+`\`) {
			return true
		}
	}
	return false
}

func (f *FS) begin(op, path string) error {
	if f.Calls == nil {
		f.Calls = map[string]int{}
	}
	f.Calls[op]++
	if _, err := discover.ValidateAbsPath(path); err != nil {
		return &fs.PathError{Op: op, Path: "(refused)", Err: err}
	}
	if f.denied(path) {
		return &fs.PathError{Op: op, Path: path, Err: discover.ErrAccessDenied}
	}
	return nil
}

// reparseOnPath reports whether path or a parent of it is a reparse point.
func (f *FS) reparseOnPath(path string) bool {
	for p := strings.TrimRight(path, `\`); len(p) > 3; p = discover.WinDir(p) {
		if n, ok := f.nodes[key(p)]; ok && n.info.Reparse {
			return true
		}
	}
	return false
}

func (f *FS) lookup(op, path string) (*node, error) {
	if err := f.begin(op, path); err != nil {
		return nil, err
	}
	n, ok := f.nodes[key(path)]
	if !ok {
		return nil, &fs.PathError{Op: op, Path: path, Err: fs.ErrNotExist}
	}
	return n, nil
}

func (f *FS) Lstat(path string) (discover.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.lookup("lstat", path)
	if err != nil {
		return discover.FileInfo{}, err
	}
	i := n.info
	i.VolumeSerial, i.FileID = 0, 0
	return i, nil
}

func (f *FS) Stat(path string) (discover.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.lookup("stat", path)
	if err != nil {
		return discover.FileInfo{}, err
	}
	return n.info, nil
}

func (f *FS) ReadDir(path string) ([]discover.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.lookup("readdir", path)
	if err != nil {
		return nil, err
	}
	if !n.info.IsDir || n.info.Reparse {
		return nil, &fs.PathError{Op: "readdir", Path: path, Err: fs.ErrInvalid}
	}
	prefix := key(path) + `\`
	var out []discover.FileInfo
	for k, c := range f.nodes {
		if strings.HasPrefix(k, prefix) && !strings.Contains(k[len(prefix):], `\`) {
			i := c.info
			i.VolumeSerial, i.FileID = 0, 0
			out = append(out, i)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	if len(out) > discover.MaxDirEntries {
		return out[:discover.MaxDirEntries], discover.ErrTooManyEntries
	}
	return out, nil
}

func (f *FS) Final(path string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.lookup("final", path)
	if err != nil {
		return "", false, err
	}
	return strings.ReplaceAll(n.display, "/", `\`), f.reparseOnPath(path), nil
}

func (f *FS) Head(path string, n int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	nd, err := f.lookup("head", path)
	if err != nil {
		return nil, err
	}
	if nd.info.IsDir || nd.info.Reparse {
		return nil, &fs.PathError{Op: "head", Path: path, Err: fs.ErrInvalid}
	}
	if n > discover.MaxConfigBytes {
		n = discover.MaxConfigBytes
	}
	if n > len(nd.content) {
		n = len(nd.content)
	}
	return append([]byte(nil), nd.content[:n]...), nil
}

// --- Processes ---

// Procs is a fixed process list.
type Procs struct {
	Items []discover.Proc
	Err   error
}

func (p *Procs) List() ([]discover.Proc, error) {
	if p.Err != nil {
		return nil, p.Err
	}
	return append([]discover.Proc(nil), p.Items...), nil
}

// --- Services ---

// Services is a fixed service list.
type Services struct {
	Items []discover.Service
	Err   error
}

func (s *Services) List() ([]discover.Service, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	return append([]discover.Service(nil), s.Items...), nil
}

// --- Registry ---

// Registry is a map of string values. It enforces discover.RegistryAllowed, so a
// detector that reads outside the allowlist fails its test.
type Registry struct {
	vals    map[string]string
	deny    map[string]bool
	denyAll bool
}

func regKey(k, v string) string {
	return strings.ToLower(strings.TrimRight(k, `\`)) + "\x00" + strings.ToLower(v)
}

// Set stores a value.
func (r *Registry) Set(k, v, data string) { r.vals[regKey(k, v)] = data }

// Deny makes reads of one value return access denied.
func (r *Registry) Deny(k, v string) { r.deny[regKey(k, v)] = true }

func (r *Registry) String(k, v string) (string, error) {
	if !discover.RegistryAllowed(k, v) {
		return "", discover.ErrRegistryNotAllowed
	}
	if r.denyAll || r.deny[regKey(k, v)] {
		return "", discover.ErrAccessDenied
	}
	d, ok := r.vals[regKey(k, v)]
	if !ok {
		return "", fs.ErrNotExist
	}
	return d, nil
}

// --- Event channels ---

// Channels is a fixed channel list. Denied maps a channel name to the error
// CanRead returns for it.
type Channels struct {
	Names   []string
	ListErr error
	Denied  map[string]error
	DenyAll bool
}

func (c *Channels) List() ([]string, error) {
	if c.ListErr != nil {
		return nil, c.ListErr
	}
	return append([]string(nil), c.Names...), nil
}

func (c *Channels) CanRead(name string) error {
	if c.DenyAll {
		return discover.ErrAccessDenied
	}
	if err := c.Denied[name]; err != nil {
		return err
	}
	for _, n := range c.Names {
		if n == name {
			return nil
		}
	}
	return fs.ErrNotExist
}

// --- Machine ---

// Machine is a fixed description of the computer.
type Machine struct {
	NameList []string
	TZ       string
	Cores    int
	Admin    bool
}

func (m *Machine) Names() []string   { return append([]string(nil), m.NameList...) }
func (m *Machine) TZKey() string     { return m.TZ }
func (m *Machine) LogicalCores() int { return m.Cores }
func (m *Machine) Elevated() bool    { return m.Admin }

// --- Contract helpers ---

// AssertDeniedGap is the access-denied contract test. It runs d against a host
// where every probe is denied and fails the test unless d reports at least one
// gap, every gap is needs-elevation, and no finding appears. Seed the fake with
// the files and services the detector expects first, so a denied call is
// reached.
func AssertDeniedGap(t testing.TB, d discover.Detector, e *Env) {
	t.Helper()
	e.DenyAll()
	findings, gaps := d.Detect(context.Background(), e.Env(nil))
	if len(findings) != 0 {
		t.Errorf("%s: %d finding(s) from a host that denies everything", d.Name(), len(findings))
	}
	if len(gaps) == 0 {
		t.Errorf("%s: access denied was swallowed, no gap reported", d.Name())
	}
	for _, g := range gaps {
		if g.Reason != discover.ReasonNeedsElevation {
			t.Errorf("%s: gap %q has reason %q, want needs-elevation", d.Name(), g.Display, g.Reason)
		}
	}
}

// AssertFindingsValid is the shape contract: every path passes ValidLogPath,
// every importer exists in the registry (when given), every Shows and How key
// has wording, and a finding has an ID once it comes out of Run.
func AssertFindingsValid(t testing.TB, findings []discover.Finding, importers *importer.Registry) {
	t.Helper()
	for _, f := range findings {
		for _, p := range f.Paths() {
			if _, err := discover.ValidLogPath(p, ""); err != nil {
				t.Errorf("%s %q: path fails ValidLogPath (%v)", f.Detector, f.Display, err)
			}
		}
		if f.Importer != "" && importers != nil {
			if _, ok := importers.ByName(f.Importer); !ok {
				t.Errorf("%s %q: importer %q is not registered", f.Detector, f.Display, f.Importer)
			}
		}
		if _, ok := discover.ShowsText(f.Shows); !ok {
			t.Errorf("%s %q: Shows key %q has no wording", f.Detector, f.Display, f.Shows)
		}
		if f.How != "" {
			if _, ok := discover.HowText(f.How); !ok {
				t.Errorf("%s %q: How key %q has no wording", f.Detector, f.Display, f.How)
			}
		}
	}
}
