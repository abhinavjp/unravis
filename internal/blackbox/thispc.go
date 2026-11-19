package blackbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// This file is how Unravis changes the Blackbox on the PC it runs on (decision
// A38). Unravis itself never runs elevated. Each change is the Blackbox program
// run with --json, once, through an Elevator (a Windows permission prompt). The
// service logic is the Manager in service.go, which is tested with fakes; the
// only code here that touches the machine is the Elevator, and tests use a fake.

// Op is one thing the helper does.
type Op string

// Helper operations.
const (
	OpInstall  Op = "install"
	OpStart    Op = "start"
	OpStop     Op = "stop"
	OpApply    Op = "apply"
	OpRemove   Op = "remove"
	OpSnapshot Op = "snapshot"
	// OpUpdate replaces the installed recorder program with the one Unravis ships.
	OpUpdate Op = "update"
)

// NeedsAdmin says whether an operation changes the service (a snapshot only
// copies files and runs as the person).
func (o Op) NeedsAdmin() bool { return o != OpSnapshot }

// NeedsGuard says whether the program is checked against the pinned build before
// the permission prompt. Only install and apply fail closed: they put new
// settings and a new service in place. Start, stop and remove never need the
// check to pass, so a Blackbox that was installed by another version can always
// be stopped and removed (SEC-FIX-15).
//
// Update fails closed as well: it copies the program that is about to run into
// Program Files, so that program must be the pinned build.
func (o Op) NeedsGuard() bool { return o == OpInstall || o == OpApply || o == OpUpdate }

// Failure reasons beyond the install and snapshot ones.
const (
	ReasonDeclined   = "declined"
	ReasonNoProgram  = "no-program"
	ReasonNotInstall = "not-installed"
	ReasonConfirm    = "confirm-name"
	ReasonInvalid    = "invalid-config"
	// ReasonHelperChanged: the Blackbox program is not the one Unravis shipped,
	// so no permission prompt was shown (SEC-01).
	ReasonHelperChanged = "helper-changed"
)

// ErrNoAnswer is a helper that ended without writing its answer.
var ErrNoAnswer = errors.New("the Blackbox program ended without an answer")

// ErrDeclined is a Windows permission prompt that was cancelled.
var ErrDeclined = errors.New("The Windows permission prompt was cancelled, so nothing was changed. Press the button again and choose Yes when Windows asks.")

// Elevator runs the Blackbox program and returns what it printed. With elevated
// set it asks Windows for administrator rights first and returns ErrDeclined when
// the person says no. The real one is in elevate_windows.go.
type Elevator interface {
	Run(ctx context.Context, exe string, args []string, elevated bool) ([]byte, error)
}

// HelperRequest is one call of the helper.
type HelperRequest struct {
	Op               Op
	Config           Config // install, apply and snapshot (snapshot reads the installed copy)
	DeleteRecordings bool   // remove
	ConfirmName      string // remove with DeleteRecordings: the name, typed
}

// HelperAnswer is what the helper printed.
type HelperAnswer struct {
	OK      bool   `json:"ok"`
	Reason  string `json:"reason"`
	Message string `json:"message"`

	ReadKey    string `json:"readKey"`
	Service    string `json:"service"`
	Folder     string `json:"folder"`
	Port       int    `json:"port"`
	Name       string `json:"name"`
	ExePath    string `json:"exePath"`
	ConfigPath string `json:"configPath"`

	Restarted         bool   `json:"restarted"`
	Updated           bool   `json:"updated"`
	WasRunning        bool   `json:"wasRunning"`
	OldHash           string `json:"oldHash"`
	NewHash           string `json:"newHash"`
	Version           string `json:"version"`
	ServiceRemoved    bool   `json:"serviceRemoved"`
	RecordingsDeleted bool   `json:"recordingsDeleted"`
	FilesDeleted      int    `json:"filesDeleted"`
	SQLServiceRemoved bool   `json:"sqlServiceRemoved"`
	Note              string `json:"note"`
	Dir               string `json:"dir"`
	Bytes             int64  `json:"bytes"`
	Recordings        int    `json:"recordings"`
	Logs              int    `json:"logs"`
}

// ThisPCError is a change that did not happen. Message is the plain sentence
// shown to the person; Reason is one of the install or snapshot reasons or one of
// the Reason* constants above.
type ThisPCError struct {
	Reason  string
	Message string
	Cause   error
}

func (e *ThisPCError) Error() string { return e.Message }
func (e *ThisPCError) Unwrap() error { return e.Cause }

// ThisPC changes the Blackbox on this PC.
type ThisPC struct {
	Elev    Elevator
	Program string // path of unravis-blackbox.exe to run
	// TempRoot is where the short-lived settings file goes ("" is the user temp).
	TempRoot string
	// Guard, when set, checks the program before an administrator prompt is shown
	// (SEC-01: the prompt would otherwise run whatever sits in that file). It
	// returns a release function that is called when the call ends: on Windows
	// the file stays locked against changes until then. An error stops the call.
	Guard func(program string) (release func(), err error)
}

// HelperArgs are the command line of one call. The settings travel in a file
// (cfgPath), never in the arguments, so nothing but plain flags is on the line.
// For apply the file is only the carrier of the new settings: the helper saves
// them to the installed config path it works out itself, exactly as install
// does, so the protocol needs no extra flag and stays backward compatible.
func HelperArgs(req HelperRequest, cfgPath string) []string {
	args := []string{string(req.Op), "--json"}
	switch req.Op {
	case OpInstall, OpApply:
		args = append(args, "--config", cfgPath)
	case OpRemove:
		if req.DeleteRecordings {
			args = append(args, "--delete-recordings", "--confirm-name", req.ConfirmName)
		}
	}
	return args
}

// ParseHelperAnswer reads the helper's JSON line. Anything else, including an
// empty answer, is a failure with a plain message, never a success.
func ParseHelperAnswer(out []byte) (HelperAnswer, error) {
	lines := bytes.Split(out, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		l := bytes.TrimSpace(lines[i])
		if len(l) == 0 || l[0] != '{' {
			continue
		}
		var a HelperAnswer
		if err := json.Unmarshal(l, &a); err != nil {
			break
		}
		return a, nil
	}
	return HelperAnswer{}, &ThisPCError{Reason: string(ReasonOther), Message: "The Blackbox program gave no readable answer, so Unravis cannot tell whether the change happened. Check the Blackbox status and try again."}
}

// failure turns a helper answer that said no into an error with a plain message.
func failure(a HelperAnswer) error {
	msg := strings.TrimSpace(a.Message)
	reason := a.Reason
	if reason == "" {
		reason = string(ReasonOther)
	}
	switch reason {
	case string(ReasonNoAdmin):
		msg = ErrNotAdmin.Error()
	case ReasonNotInstall:
		if msg == "" {
			msg = ErrNotInstalled.Error()
		}
	case ReasonConfirm:
		msg = ErrConfirmName.Error()
	}
	if msg == "" {
		msg = GenericFailure
	}
	return &ThisPCError{Reason: reason, Message: msg}
}

// FailureWords is the plain explanation and the next step for each reason, as the
// wizard and the status screen show them (wireframe 36).
func FailureWords(reason string) (what, next string) {
	switch reason {
	case string(ReasonNoAdmin):
		return "This account cannot approve administrator changes.", "Sign in as an administrator, or use the pack and set the Blackbox up on the server by hand."
	case string(ReasonExists):
		return "A Blackbox service is already installed on this PC.", "Open it from the list, or remove it first."
	case string(ReasonPortBusy):
		return "The status port is used by another program.", "Pick another port in the settings and try again."
	case string(ReasonFolder):
		return "The recordings folder cannot be written to, or the drive has too little free space.", "Pick another folder or free some space."
	case string(ReasonAntivirus):
		return "Antivirus stopped the Blackbox from starting.", "Allow the Blackbox in your antivirus, then try again."
	case ReasonDeclined:
		return "The Windows permission prompt was cancelled.", "Nothing was changed. Press the button again and choose Yes."
	case ReasonNoProgram:
		return "The Blackbox program is missing.", "Reinstall Unravis to restore the Blackbox program."
	case ReasonNotInstall:
		return "No Blackbox is installed on this PC.", "Install one first."
	case ReasonConfirm:
		return "The name typed does not match.", "Type the Blackbox name exactly to delete its recordings."
	case ReasonInvalid:
		return "The settings are not valid.", "Check the settings and try again."
	case string(ReasonSaveFailed):
		return "The new settings could not be saved.", "Nothing was changed. Try again. If it keeps happening, check that the Blackbox program folder is not locked by another program."
	case string(ReasonRestartFailed):
		return "The recorder did not restart with the new settings.", "The previous settings were put back. Try again, and check the Blackbox status if it keeps happening."
	case string(ReasonUpdateFailed):
		return "The recorder could not be updated.", "Your recordings and settings were not touched. Try again, and check the Blackbox status if it keeps happening."
	case string(ReasonFolderUnsafe):
		return "The recordings folder is not a safe place for the Blackbox.", "Choose a new, empty folder on a local drive, such as " + DefaultFolder + ", then try again."
	case ReasonHelperChanged:
		return "The Blackbox program is not the one that came with Unravis, so it was not started.", "Nothing was changed. Reinstall Unravis to restore the program, then try again."
	case string(ReasonRemoveFailed):
		return "The Blackbox could not be removed completely.", "Close any program that has the recordings open, then remove it again. The status shows what is still installed."
	case string(SnapDriveFull):
		return "The drive is full, so the snapshot was not written.", "Free some space or pick another folder. The recordings are untouched."
	case string(SnapFileOpen):
		return "A recording file was locked by another program.", "Try again in a minute. The recordings are untouched."
	}
	return GenericFailure, "Check the Blackbox status to see what state it is in, then try again."
}

// GenericFailure is the words for a failure with no reason of its own.
const GenericFailure = "The change did not finish."

func (t *ThisPC) check() error {
	if t == nil || t.Elev == nil {
		return &ThisPCError{Reason: ReasonNoProgram, Message: "Changing the Blackbox on this PC is not available in this build."}
	}
	if t.Program == "" {
		return &ThisPCError{Reason: ReasonNoProgram, Message: ErrNoProgram.Error(), Cause: ErrNoProgram}
	}
	return nil
}

// do runs one request and returns the helper's answer when it said yes.
func (t *ThisPC) do(ctx context.Context, req HelperRequest) (HelperAnswer, error) {
	if err := t.check(); err != nil {
		return HelperAnswer{}, err
	}
	if req.DeleteRecordings && (req.ConfirmName == "" || req.ConfirmName != req.Config.Name) {
		// Caught here so a typo never costs a permission prompt.
		return HelperAnswer{}, &ThisPCError{Reason: ReasonConfirm, Message: ErrConfirmName.Error(), Cause: ErrConfirmName}
	}
	cfgPath := ""
	cfgSum := ""
	if req.Op == OpInstall || req.Op == OpApply {
		if err := req.Config.Validate(); err != nil {
			return HelperAnswer{}, &ThisPCError{Reason: ReasonInvalid, Message: err.Error(), Cause: err}
		}
		data, err := req.Config.Marshal()
		if err != nil {
			return HelperAnswer{}, &ThisPCError{Reason: string(ReasonOther), Message: err.Error(), Cause: err}
		}
		dir, err := os.MkdirTemp(t.TempRoot, "unravis-bb-")
		if err != nil {
			return HelperAnswer{}, &ThisPCError{Reason: string(ReasonOther), Message: "Unravis could not make a temporary folder for the settings.", Cause: err}
		}
		defer os.RemoveAll(dir)
		cfgPath = filepath.Join(dir, ConfigFileName)
		if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
			return HelperAnswer{}, &ThisPCError{Reason: string(ReasonOther), Message: "Unravis could not write the settings for the helper.", Cause: err}
		}
		// SEC-05, SEC-FIX-07: the hash is of the bytes written above, held in
		// memory, never of a second read of the file. The helper refuses a file
		// that is not these bytes.
		sum := sha256.Sum256(data)
		cfgSum = hex.EncodeToString(sum[:])
	}
	args := HelperArgs(req, cfgPath)
	if cfgPath != "" {
		args = append(args, "--config-sha256", cfgSum)
	}
	if t.Guard != nil && req.Op.NeedsGuard() {
		release, gerr := t.Guard(t.Program)
		if gerr != nil {
			return HelperAnswer{}, &ThisPCError{Reason: ReasonHelperChanged, Message: "The Blackbox program is not the one that came with Unravis, so no permission prompt was shown and nothing was changed. Reinstall Unravis to restore it, then try again.", Cause: gerr}
		}
		if release != nil {
			defer release()
		}
	}
	out, err := t.Elev.Run(ctx, t.Program, args, req.Op.NeedsAdmin())
	if err != nil {
		if errors.Is(err, ErrDeclined) {
			return HelperAnswer{}, &ThisPCError{Reason: ReasonDeclined, Message: ErrDeclined.Error(), Cause: err}
		}
		return HelperAnswer{}, &ThisPCError{Reason: string(ReasonOther), Message: startFailureWords(err), Cause: err}
	}
	a, err := ParseHelperAnswer(out)
	if err != nil {
		return HelperAnswer{}, err
	}
	if !a.OK {
		return HelperAnswer{}, failure(a)
	}
	return a, nil
}

// startFailureWords is the plain sentence for a Blackbox program that Windows
// could not start. The OS error text stays in Cause: it names paths and codes
// that mean nothing to the person.
func startFailureWords(err error) string {
	switch {
	case errors.Is(err, ErrNoAnswer):
		return "The Blackbox program ended without an answer. The change may or may not have happened: check the Blackbox status before trying again."
	case errors.Is(err, os.ErrNotExist):
		return "Windows could not find the Blackbox program, so nothing was changed. Reinstall Unravis, or put unravis-blackbox.exe next to it."
	case errors.Is(err, os.ErrPermission):
		return "Windows would not let the Blackbox program start, so nothing was changed. Antivirus may be blocking unravis-blackbox.exe: allow it, then try again."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "The Blackbox program did not finish in time. The change may or may not have happened: check the Blackbox status before trying again."
	}
	return "Windows could not start the Blackbox program, so nothing was changed. Antivirus or a Windows policy may be blocking it. Allow unravis-blackbox.exe, then try again."
}

// InstallOutcome is a finished install. ReadKey is the one time the key is known:
// the caller seals it at once and drops it, and it is never shown or logged.
type InstallOutcome struct {
	ReadKey    string
	Name       string
	Folder     string
	Port       int
	ExePath    string
	ConfigPath string
}

// Install installs the Blackbox service on this PC with the settings given.
func (t *ThisPC) Install(ctx context.Context, cfg Config) (InstallOutcome, error) {
	a, err := t.do(ctx, HelperRequest{Op: OpInstall, Config: cfg})
	if err != nil {
		return InstallOutcome{}, err
	}
	if a.ReadKey == "" {
		return InstallOutcome{}, &ThisPCError{Reason: string(ReasonOther), Message: "The Blackbox installed but gave no read key, so Unravis cannot read it. Remove it and install again."}
	}
	return InstallOutcome{ReadKey: a.ReadKey, Name: a.Name, Folder: a.Folder, Port: a.Port, ExePath: a.ExePath, ConfigPath: a.ConfigPath}, nil
}

// Start starts the service.
func (t *ThisPC) Start(ctx context.Context) error {
	_, err := t.do(ctx, HelperRequest{Op: OpStart})
	return err
}

// Stop stops the service. The recordings are kept.
func (t *ThisPC) Stop(ctx context.Context) error {
	_, err := t.do(ctx, HelperRequest{Op: OpStop})
	return err
}

// Apply saves new settings to the installed blackbox.json and restarts the
// service. The settings travel in a short-lived file, but the helper saves them
// to the installed copy the service reads, so the selection outlives the
// temporary file. It reports whether it restarted (a stopped Blackbox applies
// them at its next start).
func (t *ThisPC) Apply(ctx context.Context, cfg Config) (restarted bool, err error) {
	a, err := t.do(ctx, HelperRequest{Op: OpApply, Config: cfg})
	return a.Restarted, err
}

// UpdateOutcome is a finished update. Updated is false when the installed
// recorder already was the one Unravis ships (nothing was stopped or restarted).
type UpdateOutcome struct {
	Updated    bool
	WasRunning bool
	Restarted  bool
	// OldHash and NewHash are the first 12 characters of the program hashes
	// (OldHash is empty when the installed program was missing). NewVersion is the
	// version the new program reports ("dev" for a local build).
	OldHash    string
	NewHash    string
	NewVersion string
}

// Update replaces the installed recorder program with the one that came with
// Unravis and restarts the recorder if it was running. Settings, key and
// recordings are kept. One administrator prompt.
func (t *ThisPC) Update(ctx context.Context) (UpdateOutcome, error) {
	a, err := t.do(ctx, HelperRequest{Op: OpUpdate})
	if err != nil {
		return UpdateOutcome{}, err
	}
	return UpdateOutcome{Updated: a.Updated, WasRunning: a.WasRunning, Restarted: a.Restarted,
		OldHash: ShortHash(a.OldHash), NewHash: ShortHash(a.NewHash), NewVersion: a.Version}, nil
}

// RemoveOutcome is a finished remove.
type RemoveOutcome struct {
	ServiceRemoved    bool
	RecordingsDeleted bool
	FilesDeleted      int
	SQLServiceRemoved bool
	// Note is a warning from the remove that did not stop it (for example SQL
	// launch permissions to take back by hand).
	Note string
}

// Remove removes the service. The recordings stay unless deleteRecordings is set
// and confirmName is the Blackbox name, typed exactly.
func (t *ThisPC) Remove(ctx context.Context, name string, deleteRecordings bool, confirmName string) (RemoveOutcome, error) {
	a, err := t.do(ctx, HelperRequest{Op: OpRemove, Config: Config{Name: name}, DeleteRecordings: deleteRecordings, ConfirmName: confirmName})
	if err != nil {
		return RemoveOutcome{}, err
	}
	return RemoveOutcome{ServiceRemoved: a.ServiceRemoved, RecordingsDeleted: a.RecordingsDeleted, FilesDeleted: a.FilesDeleted, SQLServiceRemoved: a.SQLServiceRemoved, Note: a.Note}, nil
}

// SnapshotOutcome is a finished snapshot.
type SnapshotOutcome struct {
	Dir        string
	Bytes      int64
	Recordings int
	Logs       int
}

// Snapshot copies the recordings into the snapshots folder. It does not need
// administrator rights, so there is no permission prompt.
func (t *ThisPC) Snapshot(ctx context.Context) (SnapshotOutcome, error) {
	a, err := t.do(ctx, HelperRequest{Op: OpSnapshot})
	if err != nil {
		return SnapshotOutcome{}, err
	}
	return SnapshotOutcome{Dir: a.Dir, Bytes: a.Bytes, Recordings: a.Recordings, Logs: a.Logs}, nil
}

// LocalFound is what a look at this PC found (the "Found on this PC" list of
// wireframe 35). It reads the service and its installed settings; it does not
// open the network and never reads a key.
type LocalFound struct {
	Installed bool     `json:"installed"`
	State     SvcState `json:"-"`
	Running   bool     `json:"running"`
	Name      string   `json:"name,omitempty"`
	Folder    string   `json:"folder,omitempty"`
	Port      int      `json:"port,omitempty"`
	Bind      string   `json:"bind,omitempty"`
	// ConfigUnreadable is set when the service exists but its settings could not be read.
	ConfigUnreadable bool `json:"configUnreadable,omitempty"`
	// Settings is the whole blackbox.json that was read (it holds no secret). nil when unreadable.
	Settings *Config `json:"-"`
}

// ScanThisPC looks for a Blackbox service on this PC. A failure to ask the
// service manager is not "none found": it is returned.
func ScanThisPC(svc ServiceControl, env Env, load func(path string) (Config, error)) (LocalFound, error) {
	st, err := svc.State(ServiceName)
	if err != nil {
		return LocalFound{}, err
	}
	if st == SvcNotInstalled {
		return LocalFound{}, nil
	}
	f := LocalFound{Installed: true, State: st, Running: st == SvcRunning || st == SvcStartPending}
	dir, err := env.ProgramDir()
	if err != nil || load == nil {
		f.ConfigUnreadable = true
		return f, nil
	}
	cfg, err := load(filepath.Join(dir, ConfigFileName))
	if err != nil {
		f.ConfigUnreadable = true
		return f, nil
	}
	f.Name, f.Folder, f.Port, f.Bind = cfg.Name, cfg.Folder, cfg.Port, cfg.Bind
	settings := cfg
	f.Settings = &settings
	return f, nil
}

// LocalURL is where the status page of a Blackbox on this PC answers.
func (f LocalFound) LocalURL() string {
	if !f.Installed || f.Port == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", f.Port)
}
