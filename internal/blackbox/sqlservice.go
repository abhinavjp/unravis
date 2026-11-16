package blackbox

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"api-audit-explorer/internal/bbsql"
)

// The SQL service is a second service of the same program, in its own process: a
// hung SQL call can then never stall the host sampler. It runs under its own
// virtual account (no stored password) and its data folder is readable only by
// SYSTEM, Administrators and that account's service SID. The app never grants it
// any SQL right: the owner runs the grant in SSMS (BBX-5-02).
const (
	SQLServiceName    = "UnravisBlackboxSql"
	SQLServiceAccount = `NT SERVICE\` + SQLServiceName
	SQLDataSubdir     = "sql"
)

// ErrNotSQLInstalled says there is no SQL service to remove.
var ErrNotSQLInstalled = errors.New("The UnravisBlackboxSql service is not installed here.")

// SQLDataDir is the SQL service's own data folder inside the recordings folder.
func SQLDataDir(folder string) string { return filepath.Join(folder, SQLDataSubdir) }

// SQLManager installs and removes the SQL service. Every machine effect goes
// through Svc and Env, as in Manager, so the logic is tested with fakes.
type SQLManager struct {
	Svc ServiceControl
	Env Env
	// DeleteData also removes <folder>\sql on RemoveSQL, only after the name was
	// confirmed. Off by default: the data stays.
	DeleteData bool
	// RevokeScript is the owner-run revoke text RemoveSQL prints and never runs.
	// BBX-5-02 owns the text; empty prints nothing.
	RevokeScript string
}

// sqlDirSecurer is the SQL folder lock: SYSTEM, Administrators and the service
// SID only. OSEnv implements it on Windows. An Env that cannot lock the folder
// is refused: there is no fallback to a folder ordinary users can read.
type sqlDirSecurer interface {
	SecureSQLDir(dir, sid string) error
}

// The real Env must be able to lock the SQL folder on every build.
var _ sqlDirSecurer = OSEnv{}

// sqlLaunchGranter gives the SQL service SID the least-privilege access it
// needs to START (W7-01): read/execute on the installed program, read on the
// settings file, and directory traversal — nothing more. Write, owner and DACL
// rights stay administrator-only; the program and settings are never made
// writable, and the service never runs as SYSTEM. OSEnv implements it on
// Windows; an Env that cannot grant it is refused, because a service that
// cannot read its own program is a broken install.
type sqlLaunchGranter interface {
	GrantSQLLaunchAccess(exePath, configPath, sid string) error
}

// The real Env must be able to grant launch access on every build.
var _ sqlLaunchGranter = OSEnv{}

// sqlServiceSID is the per-service SID Windows gives NT SERVICE\<name>: S-1-5-80
// followed by the SHA-1 of the upper-case service name in UTF-16LE, read as five
// little-endian 32-bit numbers (the algorithm of internal/keeper/owner.go). Pure:
// it applies nothing, so it is tested without touching an ACL.
func sqlServiceSID(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("blackbox: a service SID needs a service name")
	}
	units := utf16.Encode([]rune(strings.ToUpper(name)))
	raw := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(raw[2*i:], u)
	}
	sum := sha1.Sum(raw)
	var b strings.Builder
	b.WriteString("S-1-5-80")
	for i := 0; i < 5; i++ {
		b.WriteByte('-')
		b.WriteString(strconv.FormatUint(uint64(binary.LittleEndian.Uint32(sum[4*i:])), 10))
	}
	return b.String(), nil
}

// sqlSDDL is the permission list of the SQL data folder: protected (nothing
// inherited), full control for SYSTEM, Administrators and the SQL service SID,
// and nobody else — no BUILTIN\Users, no Everyone. Pure.
func sqlSDDL(sid string) string {
	return "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid + ")"
}

// InstallSQL registers the SQL service under its own virtual account and locks
// its data folder to SYSTEM, Administrators and that service SID. The host
// service, its account and the recordings folder ACL are left as they are.
// Whatever it did is undone when a step fails.
//
// W8-03: the service is created STOPPED and unarmed. install-sql no longer
// starts it, because a first connection before the owner runs the printed
// grant would latch a denial that survives restarts. The owner runs the grant
// in SSMS and then activates the service with the administrator-only enable-sql
// operation (EnableSQL). The app never executes the grant (C14).
//
// W7-01: the SQL SID also gets least-privilege launch access — read/execute on
// the installed program, read on the settings file, traversal on their folders
// — because the host install locks those to SYSTEM and Administrators only.
// Write/owner/DACL rights stay administrator-only.
//
// configPath is the settings file the caller validated cfg from, and it must
// be ABSOLUTE (W8-09): the service is registered with exactly that path, and a
// relative path would resolve against the SCM's working directory, not the
// installer's. The locked folder is always the folder that path's cfg names.
//
// Every answer message is a fixed sentence; causes stay in Cause for local
// diagnostics and never carry paths into the public answer (W8-07).
func (m *SQLManager) InstallSQL(cfg Config, configPath string) (err error) {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(configPath) == "" {
		return &InstallError{Reason: ReasonOther, Message: "The SQL service needs the settings file it was installed with (--config)."}
	}
	if !filepath.IsAbs(configPath) {
		return &InstallError{Reason: ReasonOther, Message: "The SQL service needs an absolute settings-file path (--config)."}
	}
	if !m.Env.IsAdmin() {
		return msgNoAdmin()
	}
	st, serr := m.Svc.State(SQLServiceName)
	if serr != nil {
		return &InstallError{Reason: ReasonOther, Message: "Could not ask Windows about the SQL service.", Cause: serr}
	}
	if st != SvcNotInstalled {
		return &InstallError{Reason: ReasonExists, Message: fmt.Sprintf("A service named %s already exists. Remove it first (remove-sql).", SQLServiceName)}
	}
	sec, ok := m.Env.(sqlDirSecurer)
	if !ok {
		return &InstallError{Reason: ReasonOther, Message: "This program cannot lock the SQL data folder on this system."}
	}
	granter, ok := m.Env.(sqlLaunchGranter)
	if !ok {
		return &InstallError{Reason: ReasonOther, Message: "This program cannot give the SQL service access to its program and settings on this system."}
	}
	sid, serr := sqlServiceSID(SQLServiceName)
	if serr != nil {
		return &InstallError{Reason: ReasonOther, Message: "Could not work out the SQL service account.", Cause: serr}
	}
	progDir, perr := m.Env.ProgramDir()
	if perr != nil {
		return &InstallError{Reason: ReasonOther, Message: "Could not find the Program Files folder.", Cause: perr}
	}
	// W7-01: a supplied config alone does not prove a host installation. The
	// installed program must actually be there, and so must the settings file
	// the service will be bound to.
	exe := filepath.Join(progDir, InstalledExeName)
	if _, serr := os.Stat(exe); serr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The Blackbox recorder is not installed here. Run install first, then install-sql.", Cause: serr}
	}
	if _, serr := os.Stat(configPath); serr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The settings file for the SQL service could not be found.", Cause: serr}
	}

	var undo undoList
	defer func() {
		if err != nil {
			undo.run()
		}
	}()
	dir := SQLDataDir(cfg.Folder)
	folderErr := func(cause error) error {
		return &InstallError{Reason: ReasonFolder, Message: "The SQL data folder could not be made and locked for the SQL service only.", Cause: cause}
	}
	if derr := ensureDir(dir, &undo); derr != nil {
		return folderErr(derr)
	}
	// Lock the folder before the service exists, so no SQL data is ever written
	// somewhere ordinary users can read. SecureSQLDir verifies owner, ancestors
	// and existing children before it mutates anything (W7-03).
	if serr := sec.SecureSQLDir(dir, sid); serr != nil {
		return folderErr(serr)
	}
	// Least-privilege launch access for the service SID (W7-01). The grant is
	// read-only and idempotent; on rollback it is left in place because it gives
	// ordinary users nothing and a re-install re-uses it.
	if gerr := granter.GrantSQLLaunchAccess(exe, configPath, sid); gerr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The SQL service could not be given read access to its program and settings.", Cause: gerr}
	}
	spec := ServiceSpec{Name: SQLServiceName, DisplayName: "Unravis Blackbox SQL",
		Description: "Reads SQL Server performance counters for the Blackbox, in its own process and under its own virtual account. The owner grants its SQL rights.",
		ExePath:     exe,
		Args:        []string{"sql-service", "--config", configPath},
		Account:     SQLServiceAccount, SIDUnrestricted: true}
	if cerr := m.Svc.Create(spec); cerr != nil {
		return &InstallError{Reason: ReasonOther, Message: "Windows would not create the SQL service.", Cause: cerr}
	}
	undo.add(func() {
		_ = m.Svc.Stop(SQLServiceName)
		_ = m.waitSQL(SvcStopped, 10*time.Second)
		_ = m.Svc.Delete(SQLServiceName)
	})
	// W8-03: deliberately NOT started. The owner runs the grant, then enable-sql.
	// The service is Automatic, so a reboot or an SCM start before enable-sql would
	// otherwise find no state file (= on at base) and reach SQL without owner
	// activation. Persist an unarmed (auto-off) state now; EnableSQL clears it.
	unarmed := bbsql.NewState()
	unarmed.AutoOff = true
	unarmed.State = bbsql.StateAutoOff
	if serr := bbsql.Save(bbsql.StatePath(cfg.Folder), unarmed); serr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The SQL service could not be left switched off on disk, so it was not installed.", Cause: serr}
	}
	return nil
}

// EnableSQL is the administrator-only owner activation (W8-03): it confirms
// the installed program and settings, re-confirms the data-folder lock, durably
// clears ONLY the persisted auto-off/denied latch (the explicit owner re-enable
// path — a routine restart never clears a genuine denial), and starts/checks
// the service. Run it AFTER the owner has executed the printed grant in SSMS.
func (m *SQLManager) EnableSQL(cfg Config, configPath string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(configPath) == "" || !filepath.IsAbs(configPath) {
		return &InstallError{Reason: ReasonOther, Message: "The SQL service needs the absolute settings-file path it was installed with (--config)."}
	}
	if !m.Env.IsAdmin() {
		return msgNoAdmin()
	}
	st, serr := m.Svc.State(SQLServiceName)
	if serr != nil {
		return &InstallError{Reason: ReasonOther, Message: "Could not ask Windows about the SQL service.", Cause: serr}
	}
	if st == SvcNotInstalled {
		return ErrNotSQLInstalled
	}
	// Confirm the installed program and the settings the service is bound to.
	progDir, perr := m.Env.ProgramDir()
	if perr != nil {
		return &InstallError{Reason: ReasonOther, Message: "Could not find the Program Files folder.", Cause: perr}
	}
	if _, serr := os.Stat(filepath.Join(progDir, InstalledExeName)); serr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The installed Blackbox program could not be found. Re-run install.", Cause: serr}
	}
	loaded, lerr := LoadConfig(configPath)
	if lerr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The installed settings file could not be read. Re-run install-sql.", Cause: lerr}
	}
	if loaded.Folder != cfg.Folder {
		return &InstallError{Reason: ReasonOther, Message: "The settings file does not match the folder the SQL service was installed with. Re-run install-sql."}
	}
	sec, ok := m.Env.(sqlDirSecurer)
	if !ok {
		return &InstallError{Reason: ReasonOther, Message: "This program cannot lock the SQL data folder on this system."}
	}
	sid, serr := sqlServiceSID(SQLServiceName)
	if serr != nil {
		return &InstallError{Reason: ReasonOther, Message: "Could not work out the SQL service account.", Cause: serr}
	}
	// Re-confirm the lock (owner/ancestors/children) before activation.
	if serr := sec.SecureSQLDir(SQLDataDir(cfg.Folder), sid); serr != nil {
		return &InstallError{Reason: ReasonFolder, Message: "The SQL data folder could not be confirmed as locked for the SQL service only.", Cause: serr}
	}
	// Durably clear ONLY the intended latch. A healthy state is left untouched;
	// a corrupt state loads as auto-off and is re-armed by this explicit owner
	// action alone. If the clear cannot be persisted, the service is NOT started.
	statePath := bbsql.StatePath(cfg.Folder)
	state, _ := bbsql.Load(statePath)
	if state.AutoOff {
		bbsql.OwnerEnable(&state)
		if serr := bbsql.Save(statePath, state); serr != nil {
			return &InstallError{Reason: ReasonOther, Message: "The SQL service latch could not be cleared on disk, so the service was not started.", Cause: serr}
		}
	}
	if st != SvcRunning {
		if serr := m.Svc.Start(SQLServiceName); serr != nil {
			return &InstallError{Reason: ReasonOther, Message: "The SQL service did not start.", Cause: serr}
		}
		if werr := m.waitSQL(SvcRunning, 15*time.Second); werr != nil {
			return &InstallError{Reason: ReasonOther, Message: "The SQL service did not start.", Cause: werr}
		}
	}
	return nil
}

// PublishSQLStopped writes the C11 off status into <folder>\sql\status.json so
// a clean stop or remove never leaves the host advertising an old enabled:true
// record (W8-04). The public field set stays exactly {enabled,state,lastOK,
// failures}; "off" carries no lastOK. The write is atomic (temp + rename).
// The runner-side worker calls the same contract on a clean Run exit.
func PublishSQLStopped(folder string) error {
	dir := SQLDataDir(folder)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(bbsql.StatusDoc{Enabled: false, State: "off", Failures: 0})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "status-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(dir, "status.json")); err != nil {
		return err
	}
	done = true
	return nil
}

// RemoveSQL stops and deletes the SQL service and prints the owner-run revoke
// text (it never runs it). The Blackbox name must be confirmed before anything is
// changed. The data goes only when DeleteData is set, and only <folder>\sql: the
// recordings folder and everything else in it stay. A clean remove publishes
// the off status so /status never keeps advertising an old enabled:true record
// (W8-04). Messages are fixed sentences; causes stay in Cause (W8-07).
func (m *SQLManager) RemoveSQL(cfg Config, confirmName string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !m.Env.IsAdmin() {
		return ErrNotAdmin
	}
	if confirmName != cfg.Name {
		return ErrConfirmName
	}
	st, err := m.Svc.State(SQLServiceName)
	if err != nil {
		return &InstallError{Reason: ReasonOther, Message: "Could not ask Windows about the SQL service.", Cause: err}
	}
	if st == SvcNotInstalled {
		return ErrNotSQLInstalled
	}
	if st != SvcStopped {
		if err := m.Svc.Stop(SQLServiceName); err != nil {
			return &InstallError{Reason: ReasonOther, Message: "The SQL service did not stop.", Cause: err}
		}
		if err := m.waitSQL(SvcStopped, 30*time.Second); err != nil {
			return &InstallError{Reason: ReasonOther, Message: "The SQL service did not stop.", Cause: err}
		}
	}
	if err := m.Svc.Delete(SQLServiceName); err != nil {
		return &InstallError{Reason: ReasonOther, Message: "Windows would not remove the SQL service.", Cause: err}
	}
	// The service is gone: publish off so the host stops advertising it. A
	// failed publish is covered by the status freshness rule, not fatal here.
	_ = PublishSQLStopped(cfg.Folder)
	if m.RevokeScript != "" {
		fmt.Fprintln(os.Stdout, m.RevokeScript)
	}
	if m.DeleteData {
		return os.RemoveAll(SQLDataDir(cfg.Folder))
	}
	return nil
}

// waitSQL polls the SQL service until it reaches want or the time is up.
func (m *SQLManager) waitSQL(want SvcState, within time.Duration) error {
	const step = 250 * time.Millisecond
	for waited := time.Duration(0); ; waited += step {
		st, err := m.Svc.State(SQLServiceName)
		if err != nil {
			return err
		}
		if st == want {
			return nil
		}
		if waited >= within {
			return fmt.Errorf("the service is %s, expected %s", st, want)
		}
		m.Env.Sleep(step)
	}
}
