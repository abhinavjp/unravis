// Package setup is the logic of unravis-setup.exe (P11): the checks made before anything changes (preflight), the list of actions an
// install, upgrade, repair or uninstall takes (the plan), the actions themselves with a journal that undoes them in reverse on a failure
// or a cancel, the silent-mode config file, the log, and the pipe between the setup window (unelevated) and the elevated worker.
//
// Everything that changes the machine goes through an interface (Registry, Services, Firewall, FileSecurity, Processes, Machine), so
// the logic is tested with fakes in temporary folders. The real Windows implementations are in *_windows.go; tests never use them.
package setup

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"api-audit-explorer/internal/blackbox"
)

// Scope is who the install is for.
type Scope string

const (
	// ScopeMachine installs for everyone on this PC: Program Files, the shared data folder, its registry record and the key keeper
	// service. It needs administrator approval. This is the default (decision A44: data is shared by everyone on the PC).
	ScopeMachine Scope = "machine"
	// ScopeUser installs for the person running setup: their own program folder and their own data folder, no keeper. It needs no
	// approval unless the Blackbox is added.
	ScopeUser Scope = "user"
)

// BlackboxOptions are the Blackbox settings a person may choose (the rest of blackbox.json keeps its defaults).
type BlackboxOptions struct {
	Name           string  `json:"name"`
	Folder         string  `json:"folder"`
	KeepDays       int     `json:"keepDays"`
	MaxGB          float64 `json:"maxGB"`
	MinFreePercent int     `json:"minFreePercent"`
	Port           int     `json:"port"`
}

// FirewallOptions opens the Blackbox port to the listed addresses only (decision A53: opt-in and scoped).
type FirewallOptions struct {
	RemoteAddresses []string `json:"remoteAddresses"`
}

// Options are the choices of an install, upgrade or repair.
type Options struct {
	Scope         Scope            `json:"scope"`
	Location      string           `json:"location"`
	Blackbox      *BlackboxOptions `json:"blackbox"`
	Firewall      *FirewallOptions `json:"firewall"`
	StartAtSignIn bool             `json:"startAtSignIn"`
	OpenWhenDone  bool             `json:"openWhenDone"`
	// CloseRunningApp is the person's yes to closing a running Unravis. Without it a running Unravis stops the install before anything
	// changes.
	CloseRunningApp bool `json:"closeRunningApp"`
	// ReadKeyFile is where silent mode writes the Blackbox read key (current user only). Empty in the window, which shows the key.
	ReadKeyFile string `json:"readKeyFile,omitempty"`
}

// UninstallOptions are the choices of an uninstall. Both deletions are off unless asked for, and each needs its typed confirmation.
type UninstallOptions struct {
	DeleteCases      bool `json:"deleteCases"`
	DeleteRecordings bool `json:"deleteRecordings"`
	// ConfirmCases must be "DELETE" and ConfirmRecordings the Blackbox name when the matching deletion is asked for. Silent mode fills
	// them itself after an explicit true in the config file.
	ConfirmCases      string `json:"confirmCases,omitempty"`
	ConfirmRecordings string `json:"confirmRecordings,omitempty"`
	CloseRunningApp   bool   `json:"closeRunningApp"`
}

// ConfirmDeleteWord is what a person types to delete cases and saved connections.
const ConfirmDeleteWord = "DELETE"

// DefaultBlackbox is the Blackbox a new install offers: blackbox.Default with the recordings folder chosen for this PC.
func DefaultBlackbox(name string, driveExists func(string) bool) BlackboxOptions {
	d := blackbox.Default()
	return BlackboxOptions{Name: name, Folder: blackbox.ChooseFolder(driveExists), KeepDays: d.KeepDays, MaxGB: d.MaxGB,
		MinFreePercent: d.MinFreePercent, Port: d.Port}
}

// BlackboxConfig turns the options into blackbox.json. The recorder listens on loopback unless the firewall rule is chosen: other PCs
// can only reach it when the person opened the port to them.
func (o Options) BlackboxConfig() (blackbox.Config, bool) {
	if o.Blackbox == nil {
		return blackbox.Config{}, false
	}
	c := blackbox.Default()
	b := o.Blackbox
	c.Name, c.Folder, c.KeepDays, c.MaxGB, c.MinFreePercent, c.Port = b.Name, b.Folder, b.KeepDays, b.MaxGB, b.MinFreePercent, b.Port
	if o.Firewall != nil {
		c.Bind = "0.0.0.0"
	}
	return c, true
}

// Problem is a choice that cannot be used, with the field it belongs to and a plain sentence that says what to do.
type Problem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (p *Problem) Error() string { return p.Message }

func problem(field, format string, a ...any) error {
	return &Problem{Field: field, Message: fmt.Sprintf(format, a...)}
}

// ProblemField returns the field of a *Problem, or "".
func ProblemField(err error) string {
	var p *Problem
	if errors.As(err, &p) {
		return p.Field
	}
	return ""
}

const maxPathLen = 200 // leaves room under MAX_PATH for the files inside

// Validate checks the options against the folders of this PC. It never touches the machine.
func (o Options) Validate(d Dirs) error {
	switch o.Scope {
	case ScopeMachine, ScopeUser:
	default:
		return problem("scope", `Choose "Just for me" or "For everyone on this PC".`)
	}
	if err := checkLocation(o.Scope, o.Location, d); err != nil {
		return err
	}
	if o.Blackbox != nil {
		cfg, _ := o.BlackboxConfig()
		if err := cfg.Validate(); err != nil {
			if why := blackbox.PlainReason(err); why != "" {
				return problem("blackbox", "%s", why)
			}
			return problem("blackbox", "%s", strings.TrimPrefix(err.Error(), blackbox.ErrInvalidConfig.Error()+": "))
		}
		if insideFolder(o.Blackbox.Folder, o.Location) || insideFolder(o.Location, o.Blackbox.Folder) {
			return problem("blackbox", "Keep the recordings folder apart from the program folder.")
		}
	}
	if o.Firewall != nil {
		if o.Blackbox == nil {
			return problem("firewall", "The firewall rule is only for the Blackbox. Add the Blackbox, or untick the rule.")
		}
		if err := CheckRemoteAddresses(o.Firewall.RemoteAddresses); err != nil {
			return err
		}
	}
	if o.ReadKeyFile != "" && !filepath.IsAbs(o.ReadKeyFile) {
		return problem("readKeyFile", "The read key file must be a full path such as C:\\setup\\readkey.txt.")
	}
	return nil
}

// checkLocation refuses a program folder that is not a full path, is a drive root or a system folder, is too long, or (for an install
// just for me) is under Program Files, which only administrators can write.
func checkLocation(scope Scope, loc string, d Dirs) error {
	const field = "location"
	if strings.TrimSpace(loc) == "" || !filepath.IsAbs(loc) || strings.HasPrefix(loc, `\\`) {
		return problem(field, "Choose a folder on this PC with a full path, such as %s.", DefaultLocation(scope, d))
	}
	if !utf8.ValidString(loc) || strings.ContainsAny(loc, `<>"|?*`) || strings.ContainsRune(loc, 0) {
		return problem(field, "The folder name has characters Windows does not allow (< > \" | ? *).")
	}
	for _, part := range strings.Split(filepath.Clean(loc), `\`) {
		if part == ".." {
			return problem(field, "Write the folder without \"..\".")
		}
	}
	if len(loc) > maxPathLen {
		return problem(field, "The folder path is too long. Choose a shorter one.")
	}
	clean := filepath.Clean(loc)
	if filepath.Dir(clean) == clean {
		return problem(field, "Choose a folder of its own, not a whole drive.")
	}
	for _, sys := range []string{d.Windows, d.ProgramFiles, d.ProgramData, d.LocalAppData, d.AppData, d.UserProfile, d.Temp} {
		if sys != "" && samePath(clean, sys) {
			return problem(field, "Choose a folder of its own inside %s, not %s itself.", sys, sys)
		}
	}
	if d.Windows != "" && insideFolder(clean, d.Windows) {
		return problem(field, "Choose a folder outside the Windows folder.")
	}
	if d.ProgramData != "" && insideFolder(clean, d.ProgramData) {
		return problem(field, "Choose a folder outside %s: that is where the shared data lives.", d.ProgramData)
	}
	if scope == ScopeUser && d.ProgramFiles != "" && insideFolder(clean, d.ProgramFiles) {
		return problem(field, "Only administrators can write to %s. Choose \"For everyone on this PC\", or a folder of your own.", d.ProgramFiles)
	}
	return nil
}

// CheckRemoteAddresses accepts single IP addresses and address ranges (10.0.0.0/24). It refuses an empty list and anything that means
// "every address", so the rule always stays scoped.
func CheckRemoteAddresses(list []string) error {
	const field = "remoteAddresses"
	if len(list) == 0 {
		return problem(field, "Type the addresses of the PCs that may read the Blackbox, such as 10.0.0.5 or 10.0.0.0/24.")
	}
	if len(list) > 50 {
		return problem(field, "List at most 50 addresses or ranges.")
	}
	for _, a := range list {
		a = strings.TrimSpace(a)
		if ip := net.ParseIP(a); ip != nil {
			if ip.IsUnspecified() {
				return problem(field, "%s means every address. Type the addresses of the PCs that may read the Blackbox.", a)
			}
			continue
		}
		_, n, err := net.ParseCIDR(a)
		if err != nil {
			return problem(field, "%q is not an address or a range. Write it like 10.0.0.5 or 10.0.0.0/24.", a)
		}
		if ones, _ := n.Mask.Size(); ones == 0 {
			return problem(field, "%s means every address. Type a narrower range.", a)
		}
	}
	return nil
}

// insideFolder reports whether p is dir or inside it (Windows paths: case does not matter).
func insideFolder(p, dir string) bool {
	if p == "" || dir == "" {
		return false
	}
	p, dir = strings.ToLower(filepath.Clean(p)), strings.ToLower(filepath.Clean(dir))
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, `\`)+`\`)
}

// samePath compares two Windows paths the way the file system does.
func samePath(a, b string) bool {
	return a != "" && b != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
