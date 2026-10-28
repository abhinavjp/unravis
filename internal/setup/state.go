package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Op is what a run does.
type Op string

const (
	OpInstall   Op = "install"
	OpUpgrade   Op = "upgrade"
	OpRepair    Op = "repair"
	OpUninstall Op = "uninstall"
)

// State is setup-state.json in the program folder: what the last install chose, so an upgrade, a repair and an uninstall know what
// is there without asking again.
type State struct {
	Version       string           `json:"version"`
	Scope         Scope            `json:"scope"`
	Location      string           `json:"location"`
	StartAtSignIn bool             `json:"startAtSignIn"`
	Blackbox      *BlackboxOptions `json:"blackbox,omitempty"`
	Firewall      *FirewallOptions `json:"firewall,omitempty"`
	FirewallRule  string           `json:"firewallRule,omitempty"`
	SharedRoot    string           `json:"sharedRoot,omitempty"`
	Installed     time.Time        `json:"installed"`
}

// Options returns the choices of the install the state describes (for an upgrade or a repair, which keep them).
func (s State) Options() Options {
	return Options{Scope: s.Scope, Location: s.Location, StartAtSignIn: s.StartAtSignIn, Blackbox: s.Blackbox, Firewall: s.Firewall}
}

// Installed is an install found on this PC.
type Installed struct {
	Root     Root   // where its Add/Remove Programs entry is
	Location string // its program folder
	Version  string
	State    State
	// StateErr says why setup-state.json could not be read ("" when it was read).
	StateErr string
}

// Detect looks for an install: the Add/Remove Programs entry for everyone, then the one for the person. It returns nil when there is
// none. An entry whose program folder lacks setup-state.json is still reported, so repair and uninstall can deal with it.
func Detect(reg Registry) (*Installed, error) {
	for _, root := range []Root{HKLM, HKCU} {
		loc, err := reg.GetString(root, ARPKey, "InstallLocation")
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		in := &Installed{Root: root, Location: filepath.Clean(loc)}
		in.Version, _ = reg.GetString(root, ARPKey, "DisplayVersion")
		st, serr := ReadState(filepath.Join(in.Location, StateFileName))
		if serr != nil {
			in.StateErr = serr.Error()
			in.State = State{Version: in.Version, Location: in.Location, Scope: ScopeUser}
			if root == HKLM {
				in.State.Scope = ScopeMachine
			}
		} else {
			in.State = st
			if in.Version == "" {
				in.Version = st.Version
			}
		}
		return in, nil
	}
	return nil, nil
}

// ResolveOp is what a run really does. Setup started again over an install of the same scope upgrades it (clumsy-user path "double
// run": it never installs a second copy), and an upgrade to the same version is a repair. A different scope is refused by preflight.
func ResolveOp(asked Op, old *Installed, version string) Op {
	if old == nil || (asked != OpInstall && asked != OpUpgrade) {
		return asked
	}
	if cmp, ok := CompareVersions(version, old.Version); ok && cmp == 0 {
		return OpRepair
	}
	return OpUpgrade
}

// ReadState reads setup-state.json.
func ReadState(p string) (State, error) {
	var s State
	data, err := os.ReadFile(p)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("%s cannot be read: %w", p, err)
	}
	return s, nil
}

// ---- payload ----

// ManifestName is the list of program files inside the setup program, with their SHA-256.
const ManifestName = "manifest.json"

// BlackboxExeName is the Blackbox program in the payload.
const BlackboxExeName = "unravis-blackbox.exe"

// PayloadFile is one program file the setup program carries.
type PayloadFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest lists the payload.
type Manifest struct {
	Version string        `json:"version"`
	Files   []PayloadFile `json:"files"`
}

// Payload is the program files embedded in the setup program.
type Payload struct {
	FS       fs.FS
	Manifest Manifest
}

var payloadNames = map[string]bool{AppExeName: true, KeeperExeName: true, BlackboxExeName: true}

// LoadPayload reads the manifest. Only the known program names are accepted, so a damaged manifest cannot make setup write anywhere
// else.
func LoadPayload(fsys fs.FS) (*Payload, error) {
	data, err := fs.ReadFile(fsys, ManifestName)
	if err != nil {
		return nil, fmt.Errorf("this setup program has no program files inside (%v). Download it again", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("this setup program is damaged (its file list cannot be read). Download it again")
	}
	if strings.TrimSpace(m.Version) == "" {
		return nil, errors.New("this setup program is damaged (its file list has no version). Download it again")
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if !payloadNames[f.Name] || seen[f.Name] || len(f.SHA256) != 64 {
			return nil, fmt.Errorf("this setup program is damaged (unexpected file %q in its list). Download it again", f.Name)
		}
		seen[f.Name] = true
	}
	if !seen[AppExeName] {
		return nil, errors.New("this setup program is damaged (unravis.exe is missing). Download it again")
	}
	return &Payload{FS: fsys, Manifest: m}, nil
}

// Has reports whether the payload carries name.
func (p *Payload) Has(name string) bool {
	for _, f := range p.Manifest.Files {
		if f.Name == name {
			return true
		}
	}
	return false
}

func (p *Payload) file(name string) (PayloadFile, bool) {
	for _, f := range p.Manifest.Files {
		if f.Name == name {
			return f, true
		}
	}
	return PayloadFile{}, false
}

// Size is the total size of the payload in bytes.
func (p *Payload) Size() int64 {
	var n int64
	for _, f := range p.Manifest.Files {
		n += f.Size
	}
	return n
}

// Verify checks every listed file against its SHA-256 (preflight: a damaged download is found before anything changes).
func (p *Payload) Verify() error {
	for _, f := range p.Manifest.Files {
		got, err := fsSHA256(p.FS, f.Name)
		if err != nil {
			return fmt.Errorf("this setup program is damaged (%s cannot be read). Download it again", f.Name)
		}
		if !strings.EqualFold(got, f.SHA256) {
			return fmt.Errorf("this setup program is damaged (%s does not match its checksum). Download it again", f.Name)
		}
	}
	return nil
}

func fsSHA256(fsys fs.FS, name string) (string, error) {
	f, err := fsys.Open(path.Clean(name))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ---- versions ----

// CompareVersions compares two release versions ("1.2.3", "v1.2.3", "1.2.3-rc1") and the git-describe form build.ps1 stamps
// ("v1.2.3-4-gabc1234", "v1.2.3-dirty"): that is the release plus 4 commits, which is newer than the bare tag and older than 5 commits;
// "-dirty" does not change the order. Pre-release parts are compared piece by piece with numbers as numbers (rc2 is older than rc10).
// ok is false when either is not a release number (a commit hash, "dev"): then setup does not refuse anything on the grounds of version.
func CompareVersions(a, b string) (cmp int, ok bool) {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			if pa.n[i] < pb.n[i] {
				return -1, true
			}
			return 1, true
		}
	}
	switch {
	case pa.pre == pb.pre:
	case pa.pre == "": // a release is newer than its pre-releases
		return 1, true
	case pb.pre == "":
		return -1, true
	default:
		if c := comparePre(pa.pre, pb.pre); c != 0 {
			return c, true
		}
	}
	switch {
	case pa.commits < pb.commits:
		return -1, true
	case pa.commits > pb.commits:
		return 1, true
	}
	return 0, true
}

type parsedVersion struct {
	n       [3]int
	pre     string
	commits int // commits after the tag, from a git-describe version
}

// describeTail matches the "-N-gHASH" git describe adds after a tag.
var describeTail = regexp.MustCompile(`-(\d+)-g[0-9a-fA-F]+$`)

func parseVersion(s string) (parsedVersion, bool) {
	var v parsedVersion
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(s, "-dirty") // a build with local changes sorts as the same numbers
	if m := describeTail.FindStringSubmatchIndex(s); m != nil {
		n, err := strconv.Atoi(s[m[2]:m[3]])
		if err != nil {
			return v, false
		}
		v.commits = n
		s = s[:m[0]]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s, v.pre = s[:i], s[i+1:]
		if v.pre == "" {
			return v, false
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" {
			return v, false
		}
		v.n[i] = n
	}
	return v, true
}

// comparePre compares two pre-release strings part by part (split at dots); within a part, runs of digits compare as numbers.
func comparePre(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if c := compareNatural(pa[i], pb[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(pa) < len(pb):
		return -1
	case len(pa) > len(pb):
		return 1
	}
	return 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// compareNatural compares two strings with runs of digits compared as numbers ("rc2" < "rc10").
func compareNatural(a, b string) int {
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			i, j := 0, 0
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			na, nb := strings.TrimLeft(a[:i], "0"), strings.TrimLeft(b[:j], "0")
			if len(na) != len(nb) {
				if len(na) < len(nb) {
					return -1
				}
				return 1
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			a, b = a[i:], b[j:]
			continue
		}
		if a[0] != b[0] {
			if a[0] < b[0] {
				return -1
			}
			return 1
		}
		a, b = a[1:], b[1:]
	}
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return -1
	}
	return 1
}
