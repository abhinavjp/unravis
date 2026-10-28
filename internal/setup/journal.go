package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// UndoKind names how an action is undone. The journal stores kinds and data, never code, so a run that was cut off (power loss, a
// killed process) is undone by the next run from the file alone.
type UndoKind string

const (
	UndoRemoveFile  UndoKind = "remove-file"  // a file setup created: delete it
	UndoRestoreFile UndoKind = "restore-file" // a file setup replaced: put the backup back
	UndoRemoveDir   UndoKind = "remove-dir"   // a folder setup created: remove it when empty
	UndoRemoveTree  UndoKind = "remove-tree"  // a working folder setup created (staging, backup): remove it with what is in it
	UndoRegValue    UndoKind = "reg-value"    // a registry value setup set: put the old value back, or delete it
	UndoRegKey      UndoKind = "reg-key"      // a registry key setup created: delete it
	UndoSvcDelete   UndoKind = "svc-delete"   // a service setup created: stop and delete it
	UndoSvcStart    UndoKind = "svc-start"    // a service setup stopped: start it again
	UndoSvcStop     UndoKind = "svc-stop"     // a service setup started: stop it (before its old program is put back)
	UndoSvcConfig   UndoKind = "svc-config"   // a service setup changed: put the old program and arguments back
	UndoFwRemove    UndoKind = "fw-remove"    // a firewall rule setup added: remove it
	UndoFwRestore   UndoKind = "fw-restore"   // a firewall rule setup replaced: put the old one back
	UndoSDDL        UndoKind = "sddl"         // permissions setup changed: put the old ones back
	UndoBlackbox    UndoKind = "blackbox"     // the Blackbox setup installed: remove service, settings, key and new recordings
)

// Undo is one journal entry: what an action is about to do and how to take it back. It is written before the action runs.
type Undo struct {
	Step string   `json:"step"`
	Kind UndoKind `json:"kind"`
	// Note says in plain words what the action does, for the log ("service created: delete service UnravisKeeper").
	Note   string `json:"note"`
	Path   string `json:"path,omitempty"`
	Backup string `json:"backup,omitempty"`
	SDDL   string `json:"sddl,omitempty"`
	Root   Root   `json:"root,omitempty"`
	Key    string `json:"key,omitempty"`
	Name   string `json:"name,omitempty"`
	// Old is the registry value before (nil: there was none). OldDWORD marks a DWORD value.
	Old      *string       `json:"old,omitempty"`
	OldDWORD *uint32       `json:"oldDword,omitempty"`
	Service  *ServiceSpec  `json:"service,omitempty"`
	Rule     *FirewallRule `json:"rule,omitempty"`
	// Existed says the recordings folder was there before the Blackbox install (then no recording is deleted on undo).
	Existed bool `json:"existed,omitempty"`
	// Dir is the Blackbox program folder; DirCreated says setup made it.
	Dir        string `json:"dir,omitempty"`
	DirCreated bool   `json:"dirCreated,omitempty"`
}

// JournalFile is the journal on disk.
type JournalFile struct {
	Op      Op        `json:"op"`
	Version string    `json:"version"`
	Started time.Time `json:"started"`
	Undos   []Undo    `json:"undos"`
}

// Journal is the write-ahead list of what a run did. Every Record is on disk before the action it describes runs.
type Journal struct {
	path string
	f    JournalFile
}

// JournalName is the journal's file name.
const JournalName = "journal.json"

// MachineJournalDir is where an elevated run keeps its journal: a folder under ProgramData that only administrators can change.
func MachineJournalDir(d Dirs) string { return filepath.Join(d.ProgramData, MachineJournalFolder) }

// UserJournalDir is where a run without administrator rights keeps its journal.
func UserJournalDir(d Dirs) string { return filepath.Join(d.LocalAppData, MachineJournalFolder) }

// NewJournal starts a journal at path. A journal already there is an earlier run that did not finish: the caller rolls it back first.
func NewJournal(path string, op Op, version string, now time.Time) (*Journal, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("an earlier setup left a journal at %s", path)
	}
	j := &Journal{path: path, f: JournalFile{Op: op, Version: version, Started: now}}
	return j, j.save()
}

// Record writes u to the journal before its action runs.
func (j *Journal) Record(u Undo) error {
	j.f.Undos = append(j.f.Undos, u)
	if err := j.save(); err != nil {
		j.f.Undos = j.f.Undos[:len(j.f.Undos)-1]
		return fmt.Errorf("could not write the setup journal: %w", err)
	}
	return nil
}

// Undos returns the entries in the order they were written.
func (j *Journal) Undos() []Undo { return append([]Undo(nil), j.f.Undos...) }

// Path is the journal file.
func (j *Journal) Path() string { return j.path }

// Close deletes the journal: the run is finished (or fully undone).
func (j *Journal) Close() error {
	err := os.Remove(j.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (j *Journal) save() error {
	data, err := json.MarshalIndent(j.f, "", " ")
	if err != nil {
		return err
	}
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, j.path)
}

// LoadJournal reads a journal left by a run that did not finish. It returns os.ErrNotExist when there is none.
func LoadJournal(path string) (*Journal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f JournalFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("the setup journal at %s cannot be read: %w", path, err)
	}
	return &Journal{path: path, f: f}, nil
}

// Rollback undoes the entries newest first. It never stops at a failed undo: it carries on and returns what could not be put back,
// in plain words, for the Failed screen and the log. report is told about each undo.
func Rollback(sys *System, undos []Undo, report func(u Undo, err error)) (left []string) {
	for i := len(undos) - 1; i >= 0; i-- {
		u := undos[i]
		err := undoOne(sys, u)
		if report != nil {
			report(u, err)
		}
		if err != nil {
			left = append(left, fmt.Sprintf("%s (%v)", leftText(u), err))
		}
	}
	return left
}

// leftText says what is left when an undo fails.
func leftText(u Undo) string {
	switch u.Kind {
	case UndoRemoveFile, UndoRestoreFile:
		return "File " + u.Path
	case UndoRemoveDir, UndoRemoveTree:
		return "Folder " + u.Path
	case UndoRegValue:
		return fmt.Sprintf("Registry value %s\\%s\\%s", u.Root, u.Key, u.Name)
	case UndoRegKey:
		return fmt.Sprintf("Registry key %s\\%s", u.Root, u.Key)
	case UndoSvcDelete, UndoSvcStart, UndoSvcStop, UndoSvcConfig:
		return "Service " + u.Name
	case UndoFwRemove, UndoFwRestore:
		return "Firewall rule " + u.Name
	case UndoSDDL:
		return "Permissions of " + u.Path
	case UndoBlackbox:
		return "Blackbox service " + blackbox.ServiceName
	}
	return u.Note
}

func undoOne(sys *System, u Undo) error {
	switch u.Kind {
	case UndoRemoveFile:
		if err := os.Remove(u.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	case UndoRestoreFile:
		if _, err := os.Stat(u.Backup); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil // the action never moved the file away
			}
			return err
		}
		if err := os.Remove(u.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Rename(u.Backup, u.Path)
	case UndoRemoveDir:
		err := os.Remove(u.Path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if ents, rerr := os.ReadDir(u.Path); rerr == nil && len(ents) > 0 {
			names := make([]string, 0, len(ents))
			for _, e := range ents {
				names = append(names, e.Name())
			}
			return fmt.Errorf("it holds files setup did not put there: %s", strings.Join(names, ", "))
		}
		return err
	case UndoRemoveTree:
		return os.RemoveAll(u.Path)
	case UndoRegValue:
		switch {
		case u.OldDWORD != nil:
			return sys.Reg.SetDWORD(u.Root, u.Key, u.Name, *u.OldDWORD)
		case u.Old != nil:
			return sys.Reg.SetString(u.Root, u.Key, u.Name, *u.Old)
		}
		if ok, _ := sys.Reg.KeyExists(u.Root, u.Key); !ok {
			return nil
		}
		return sys.Reg.DeleteValue(u.Root, u.Key, u.Name)
	case UndoRegKey:
		return sys.Reg.DeleteKey(u.Root, u.Key)
	case UndoSvcDelete:
		st, err := sys.Svc.State(u.Name)
		if err != nil {
			return err
		}
		if st == blackbox.SvcNotInstalled {
			return nil
		}
		if _, err := sys.stopService(u.Name); err != nil {
			return err
		}
		return sys.Svc.Delete(u.Name)
	case UndoSvcStart:
		st, err := sys.Svc.State(u.Name)
		if err != nil || st == blackbox.SvcNotInstalled {
			return err
		}
		return sys.startService(u.Name)
	case UndoSvcStop:
		_, err := sys.stopService(u.Name)
		return err
	case UndoSvcConfig:
		if u.Service == nil {
			return nil
		}
		return sys.Svc.SetConfig(*u.Service)
	case UndoFwRemove:
		if _, err := sys.FW.Get(u.Name); errors.Is(err, ErrNotFound) {
			return nil
		}
		return sys.FW.Remove(u.Name)
	case UndoFwRestore:
		if u.Rule == nil {
			return nil
		}
		if err := sys.FW.Remove(u.Name); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		return sys.FW.Add(*u.Rule)
	case UndoSDDL:
		if _, err := os.Stat(u.Path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return sys.Sec.SetSDDL(u.Path, u.SDDL)
	case UndoBlackbox:
		return undoBlackbox(sys, u)
	}
	return fmt.Errorf("unknown journal entry %q", u.Kind)
}

// undoBlackbox takes back a Blackbox install: the service, the read key hash, the settings and the program copy, and the recordings the
// service wrote, but only when setup made the recordings folder (recordings that were there before are never touched).
func undoBlackbox(sys *System, u Undo) error {
	var errs []error
	if st, err := sys.Svc.State(blackbox.ServiceName); err != nil {
		errs = append(errs, err)
	} else if st != blackbox.SvcNotInstalled {
		if _, err := sys.stopService(blackbox.ServiceName); err != nil {
			errs = append(errs, err)
		} else if err := sys.Svc.Delete(blackbox.ServiceName); err != nil {
			errs = append(errs, err)
		}
	}
	for _, name := range []string{blackbox.KeyFileName, blackbox.ConfigFileName, blackbox.ConfigFileName + ".bak", blackbox.InstalledExeName} {
		if err := os.Remove(filepath.Join(u.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if u.DirCreated {
		_ = os.Remove(u.Dir)
	}
	if !u.Existed && u.Path != "" {
		if _, err := blackbox.RemoveRecordings(u.Path); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
