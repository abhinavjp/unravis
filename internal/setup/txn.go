package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"api-audit-explorer/internal/blackbox"
)

// Txn is what a step uses to change the machine: every method writes its journal entry first, then acts. A step that fails part way
// leaves only journalled changes, so the rollback takes back exactly what was done.
type Txn struct {
	sys     *System
	j       *Journal
	step    string
	backups string // folder for replaced files, made on first use
	nback   int
	log     func(string)
	// keep lists working folders removed when the run is committed (staging, backups).
	keep *[]string
}

func (t *Txn) record(u Undo) error {
	u.Step = t.step
	if u.Kind == UndoBlackbox && u.Dir != "" {
		// Settings, key and program files already in the Blackbox folder are copied to the backup folder first, so the rollback puts
		// them back instead of deleting them. Their entries come before this one, so they are undone after it.
		for _, name := range []string{blackbox.KeyFileName, blackbox.ConfigFileName, blackbox.ConfigFileName + ".bak", blackbox.InstalledExeName} {
			had, err := t.keepCopy(filepath.Join(u.Dir, name), "Blackbox file")
			if err != nil {
				return err
			}
			if had {
				u.Kept = append(u.Kept, name)
			}
		}
	}
	return t.j.Record(u)
}

// Logf writes a line to the setup log.
func (t *Txn) Logf(format string, a ...any) {
	if t != nil && t.log != nil {
		t.log(fmt.Sprintf(format, a...))
	}
}

// MkdirAll makes dir and each missing parent, journalling each one so the rollback removes them (only while empty).
func (t *Txn) MkdirAll(dir string) error {
	var missing []string
	for p := filepath.Clean(dir); ; {
		if _, err := os.Lstat(p); err == nil {
			break
		}
		missing = append(missing, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	for i := len(missing) - 1; i >= 0; i-- { // outermost first, so the rollback removes the innermost first
		if err := t.record(Undo{Kind: UndoRemoveDir, Path: missing[i], Note: "folder created: remove " + missing[i]}); err != nil {
			return err
		}
		if err := os.Mkdir(missing[i], 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	return nil
}

// WorkDir makes a working folder that is removed by the rollback and by the commit.
func (t *Txn) WorkDir(dir string) error {
	if err := t.record(Undo{Kind: UndoRemoveTree, Path: dir, Note: "working folder created: remove " + dir}); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	*t.keep = append(*t.keep, dir)
	return nil
}

// backupPath returns a fresh path for the old copy of a file in the run's backup folder.
func (t *Txn) backupPath(name string) (string, error) {
	if t.backups == "" {
		return "", errors.New("no backup folder")
	}
	if _, err := os.Stat(t.backups); errors.Is(err, os.ErrNotExist) {
		if err := t.WorkDir(t.backups); err != nil {
			return "", err
		}
	}
	t.nback++
	return filepath.Join(t.backups, strconv.Itoa(t.nback)+"-"+filepath.Base(name)), nil
}

// moveFile renames, or copies and deletes when the two paths are on different drives.
func moveFile(from, to string) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	if err := copyFile(context.Background(), from, to); err != nil {
		return err
	}
	return os.Remove(from)
}

// keepOld journals a file that is about to be replaced or created at path: a file already there is moved to the backup folder (the
// rollback moves it back); a new one is deleted by the rollback.
func (t *Txn) keepOld(path, what string) error {
	if _, err := os.Lstat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return t.record(Undo{Kind: UndoRemoveFile, Path: path, Note: what + " created: delete " + path})
	}
	b, err := t.backupPath(path)
	if err != nil {
		return err
	}
	if err := t.record(Undo{Kind: UndoRestoreFile, Path: path, Backup: b, Note: what + " replaced: restore from backup " + b}); err != nil {
		return err
	}
	return moveFile(path, b)
}

// keepCopy journals a file that exists and will be overwritten by something else: a copy goes to the backup folder and the rollback puts
// it back. It reports whether the file was there; a file that was not there is left to the caller's own undo.
func (t *Txn) keepCopy(path, what string) (bool, error) {
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	b, err := t.backupPath(path)
	if err != nil {
		return false, err
	}
	if err := t.record(Undo{Kind: UndoRestoreFile, Path: path, Backup: b, Note: what + " overwritten: restore from backup " + b}); err != nil {
		return false, err
	}
	if err := copyFile(context.Background(), path, b+".part"); err != nil {
		return false, err
	}
	return true, os.Rename(b+".part", b)
}

// WriteFile writes data to path, keeping any old file for the rollback.
func (t *Txn) WriteFile(path string, data []byte, what string) error {
	if err := t.keepOld(path, what); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// PlaceFile moves a checked file from the staging folder into place, keeping any old file for the rollback.
func (t *Txn) PlaceFile(staged, path string) error {
	if err := t.keepOld(path, "file"); err != nil {
		return err
	}
	return moveFile(staged, path)
}

// RemoveFile deletes a file, keeping it in the backup folder until the run is committed.
func (t *Txn) RemoveFile(path, what string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	b, err := t.backupPath(path)
	if err != nil {
		return err
	}
	if err := t.record(Undo{Kind: UndoRestoreFile, Path: path, Backup: b, Note: what + " removed: restore from backup " + b}); err != nil {
		return err
	}
	return moveFile(path, b)
}

// SetSDDL sets a folder's permissions, journalling the old ones.
func (t *Txn) SetSDDL(path, sddl string) error {
	old, err := t.sys.Sec.SDDL(path)
	if err != nil {
		return err
	}
	if err := t.record(Undo{Kind: UndoSDDL, Path: path, SDDL: old, Note: "permissions set on " + path + ": put the old ones back"}); err != nil {
		return err
	}
	return t.sys.Sec.SetSDDL(path, sddl)
}

// CreateKey creates a registry key when missing (journalled) and sets its permissions.
func (t *Txn) CreateKey(root Root, key, sddl string) error {
	ok, err := t.sys.Reg.KeyExists(root, key)
	if err != nil {
		return err
	}
	if !ok {
		if err := t.record(Undo{Kind: UndoRegKey, Root: root, Key: key, Note: fmt.Sprintf("registry key created: delete %s\\%s", root, key)}); err != nil {
			return err
		}
	}
	return t.sys.Reg.CreateKey(root, key, sddl)
}

// SetString sets a REG_SZ value, journalling the old one.
func (t *Txn) SetString(root Root, key, name, value string) error {
	u := Undo{Kind: UndoRegValue, Root: root, Key: key, Name: name, Note: fmt.Sprintf("registry value set: restore %s\\%s\\%s", root, key, name)}
	if old, err := t.sys.Reg.GetString(root, key, name); err == nil {
		u.Old = &old
	} else if old, derr := t.sys.Reg.GetDWORD(root, key, name); derr == nil {
		u.OldDWORD = &old
	}
	if err := t.record(u); err != nil {
		return err
	}
	return t.sys.Reg.SetString(root, key, name, value)
}

// SetDWORD sets a REG_DWORD value, journalling the old one.
func (t *Txn) SetDWORD(root Root, key, name string, value uint32) error {
	u := Undo{Kind: UndoRegValue, Root: root, Key: key, Name: name, Note: fmt.Sprintf("registry value set: restore %s\\%s\\%s", root, key, name)}
	if old, err := t.sys.Reg.GetDWORD(root, key, name); err == nil {
		u.OldDWORD = &old
	} else if old, serr := t.sys.Reg.GetString(root, key, name); serr == nil {
		u.Old = &old
	}
	if err := t.record(u); err != nil {
		return err
	}
	return t.sys.Reg.SetDWORD(root, key, name, value)
}

// CreateService registers a service (journalled: the rollback stops and deletes it).
func (t *Txn) CreateService(spec ServiceSpec) error {
	if err := t.record(Undo{Kind: UndoSvcDelete, Name: spec.Name, Note: "service created: delete service " + spec.Name}); err != nil {
		return err
	}
	return t.sys.Svc.Create(spec)
}

// StopService stops a running service (journalled: the rollback starts it again). It reports whether it was running.
func (t *Txn) StopService(name string) (bool, error) {
	st, err := t.sys.Svc.State(name)
	if err != nil || st == blackbox.SvcNotInstalled || st == blackbox.SvcStopped {
		return false, err
	}
	if err := t.record(Undo{Kind: UndoSvcStart, Name: name, Note: "service stopped: start service " + name}); err != nil {
		return false, err
	}
	return t.sys.stopService(name)
}

// StartService starts a service and waits until it runs (journalled: the rollback stops it, so its old program can be put back and the
// earlier "service stopped" entry start the old one).
func (t *Txn) StartService(name string) error {
	if err := t.record(Undo{Kind: UndoSvcStop, Name: name, Note: "service started: stop service " + name}); err != nil {
		return err
	}
	return t.sys.startService(name)
}

// MkdirSDDL makes one folder with its owner and access list already set, so nobody can put anything in it in between (journalled: the
// rollback removes it while empty).
func (t *Txn) MkdirSDDL(dir, sddl string) error {
	if err := t.record(Undo{Kind: UndoRemoveDir, Path: dir, Note: "folder created: remove " + dir}); err != nil {
		return err
	}
	return t.sys.Sec.MkdirSDDL(dir, sddl)
}

// SetServiceConfig changes a service's program and arguments (journalled: the rollback puts the old ones back).
func (t *Txn) SetServiceConfig(spec ServiceSpec) error {
	old, err := t.sys.Svc.Config(spec.Name)
	if err != nil {
		return err
	}
	if err := t.record(Undo{Kind: UndoSvcConfig, Name: spec.Name, Service: &old, Note: "service changed: restore the program of " + spec.Name}); err != nil {
		return err
	}
	return t.sys.Svc.SetConfig(spec)
}

// AddFirewallRule adds (or replaces) a rule, journalling what was there.
func (t *Txn) AddFirewallRule(rule FirewallRule) error {
	u := Undo{Kind: UndoFwRemove, Name: rule.Name, Note: "firewall rule added: remove rule " + rule.Name}
	if old, err := t.sys.FW.Get(rule.Name); err == nil {
		u = Undo{Kind: UndoFwRestore, Name: rule.Name, Rule: &old, Note: "firewall rule replaced: restore rule " + rule.Name}
		if err := t.record(u); err != nil {
			return err
		}
		if err := t.sys.FW.Remove(rule.Name); err != nil {
			return err
		}
		return t.sys.FW.Add(rule)
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := t.record(u); err != nil {
		return err
	}
	return t.sys.FW.Add(rule)
}

// copyFile copies src to dst, checking ctx between chunks so Cancel stops a long copy.
func copyFile(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := copyCtx(ctx, out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// copyCtx copies in 256 KB chunks and stops when ctx is cancelled.
func copyCtx(ctx context.Context, w io.Writer, r io.Reader) error {
	buf := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// isUnder reports whether p is inside dir (not dir itself).
func isUnder(p, dir string) bool {
	return dir != "" && strings.HasPrefix(strings.ToLower(filepath.Clean(p)), strings.ToLower(strings.TrimSuffix(filepath.Clean(dir), `\`))+`\`)
}
