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

// sqlConfigVerifier proves a settings file is administrator-locked (SEC-1):
// trusted owner, a DACL that grants change rights only to SYSTEM and
// Administrators, and no reparse point anywhere in its ancestor chain. The SQL
// service reads this file at every start, so a user-writable --config would let
// an ordinary user aim a privileged service at their own folder. OSEnv
// implements it on Windows with the real owner/DACL/reparse reads and refuses
// off Windows. The rule itself is decided in pure Go (adminOnlyDACL) so a fake
// ACL reader can drive it on any platform.
type sqlConfigVerifier interface {
	VerifyAdminOnlyFile(path string) error
}

// The real Env must be able to prove the settings lock on every build.
var _ sqlConfigVerifier = OSEnv{}

// sqlInstallMarkerName is the install record inside the installed program
// folder (SEC-3). It persists the folder the SQL service was INSTALLED with,
// so enable-sql can compare the loaded settings against a value that does not
// come from the settings file itself. The program folder is administrator-
// locked by the host install, and the record itself is explicitly locked to
// SYSTEM and Administrators at install and proven with VerifyAdminOnlyFile
// before it is ever trusted.
const sqlInstallMarkerName = "sql-install.json"

// sqlInstallRecord is the persisted install-time fact. It carries no
// credential and nothing the service process needs at run time.
type sqlInstallRecord struct {
	Folder string `json:"folder"`
}

// sqlInstallMarkerLocker locks the install record to SYSTEM and Administrators
// only (SEC-3): a protected DACL, no inheritance, and no rights for ordinary
// users — not even the SQL service SID, which never reads the record. OSEnv
// implements it on Windows and refuses off Windows. Like sqlConfigVerifier,
// the seam is optional only for test fakes predating it: production always
// wires OSEnv (cmd/unravis-blackbox), so the real install path always locks.
type sqlInstallMarkerLocker interface {
	LockSQLInstallMarker(path string) error
}

// The real Env must be able to lock the install record on every build.
var _ sqlInstallMarkerLocker = OSEnv{}

// sqlInstallMarkerPath is the install record beside the installed program.
func sqlInstallMarkerPath(progDir string) string {
	return filepath.Join(progDir, sqlInstallMarkerName)
}

// writeSQLInstallMarker persists the install-time folder atomically (temp +
// rename, the PublishSQLStopped pattern). The caller locks and proves the
// record afterwards; a write failure is an install failure, never a silently
// missing record.
func writeSQLInstallMarker(path, folder string) error {
	data, err := json.Marshal(sqlInstallRecord{Folder: folder})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "sql-install-*.tmp")
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
	if err := os.Rename(name, path); err != nil {
		return err
	}
	done = true
	return nil
}

// readSQLInstallMarker reads the persisted install-time folder. Any failure —
// missing, unreadable, malformed, or naming no usable absolute folder — is an
// error, never an empty string that could pass a comparison (fail closed).
func readSQLInstallMarker(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var rec sqlInstallRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", err
	}
	if strings.TrimSpace(rec.Folder) == "" || !filepath.IsAbs(rec.Folder) {
		return "", errors.New("blackbox: the SQL install record names no usable folder")
	}
	return rec.Folder, nil
}

// verifySQLConfigFile asks the Env's ACL reader to prove configPath is
// administrator-locked. Any error — a user-writable file, an unreadable or
// malformed ACL, a reparse ancestor — is a refusal (fail closed). An Env that
// carries no ACL reader at all is a test fake predating the seam: production
// always wires OSEnv (cmd/unravis-blackbox), which implements the proof on
// every platform, and the real Windows launch grant re-verifies the config
// itself before it touches any ACL, so the real install path stays closed.
func verifySQLConfigFile(env Env, configPath string) error {
	v, ok := env.(sqlConfigVerifier)
	if !ok {
		return nil
	}
	return v.VerifyAdminOnlyFile(configPath)
}

// adminOnlyWriteMask is every right that lets a trustee CHANGE a settings
// file: write/append data, extended attributes, attributes, delete (replace),
// delete-child, DACL-change, owner-change, and the generic/maximum grants that
// map onto them. The values are the Windows file-access constants; they are
// literals here so the rule is pure and testable off Windows.
const adminOnlyWriteMask uint32 = 0x00000002 | // FILE_WRITE_DATA
	0x00000004 | // FILE_APPEND_DATA
	0x00000010 | // FILE_WRITE_EA
	0x00000100 | // FILE_WRITE_ATTRIBUTES
	0x00000040 | // FILE_DELETE_CHILD
	0x00010000 | // DELETE
	0x00040000 | // WRITE_DAC
	0x00080000 | // WRITE_OWNER
	0x02000000 | // MAXIMUM_ALLOWED
	0x10000000 | // GENERIC_ALL
	0x40000000 // GENERIC_WRITE

// fileACE is one access-control entry described in the only terms the rule
// needs: the raw Windows ACE type, its access mask and its trustee SID string.
// The platform reader produces these; the rule below never touches an ACL.
type fileACE struct {
	Type    byte
	Mask    uint32
	Trustee string
}

// adminOnlyDACL decides whether a described DACL leaves a settings file
// changeable only by SYSTEM and Administrators (SEC-1). Fail closed: a missing
// (NULL) DACL means everyone may change the file, an unrecognized ACE type is
// refused, and any allow-family entry (including conditional/callback and
// object ACEs, whose conditions cannot be evaluated here) that carries a
// change right for anyone else refuses the file. Deny, audit, alarm and label
// entries grant nothing and are skipped; a deny is never trusted to cancel a
// later allow, because that depends on ACE order. Read-only entries for
// ordinary users or the service SID are fine.
func adminOnlyDACL(present bool, aces []fileACE) error {
	if !present {
		return errors.New("blackbox: the settings file has no access list, so anyone may change it")
	}
	for _, a := range aces {
		switch a.Type {
		case 0x1, 0x6, 0xA, 0xC: // access-denied (plain, object, callback): grants nothing
			continue
		case 0x2, 0x3, 0x7, 0x8, 0xD, 0xE, 0xF, 0x10, // audit/alarm families
			0x11, 0x12, 0x13: // mandatory label, resource attribute, scoped policy: grant nothing
			continue
		case 0x0, 0x5, 0x9, 0xB: // access-allowed (plain, object, callback): may grant
			if a.Mask&adminOnlyWriteMask == 0 {
				continue
			}
			if strings.EqualFold(a.Trustee, "S-1-5-18") || strings.EqualFold(a.Trustee, "S-1-5-32-544") {
				continue // SYSTEM or Administrators
			}
			return errors.New("blackbox: the settings file allows a non-administrator to change it")
		default:
			return fmt.Errorf("blackbox: an unrecognized access entry (type %d) on the settings file", a.Type)
		}
	}
	return nil
}

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
// SEC-1: the file must also be administrator-locked (trusted owner, change
// rights only for SYSTEM/Administrators, no reparse ancestor); a user-writable
// or unverifiable --config is refused before anything is registered.
// SEC-3: the install-time folder is persisted in an administrator-locked
// install record beside the program, so enable-sql can detect a later edit of
// Folder in the settings file.
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
	// SEC-1: the settings file the service will be bound to must be
	// administrator-locked BEFORE anything is registered or mutated. A
	// user-writable --config would let an ordinary user aim the service at
	// their own folder on its next start. Unverifiable means refused.
	if verr := verifySQLConfigFile(m.Env, configPath); verr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The settings file for the SQL service is not locked to administrators, so it was refused. Use the installed settings file (re-run install if needed).", Cause: verr}
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
	// SEC-3: persist the INSTALL-TIME folder in an admin-locked install record
	// beside the installed program, so enable-sql compares the loaded settings
	// against a persisted fact instead of the settings file itself. Write,
	// lock, then prove; any failure refuses the install and undoes the record.
	markerPath := sqlInstallMarkerPath(progDir)
	if werr := writeSQLInstallMarker(markerPath, cfg.Folder); werr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The SQL service install record could not be written.", Cause: werr}
	}
	undo.add(func() { _ = os.Remove(markerPath) })
	if locker, ok := m.Env.(sqlInstallMarkerLocker); ok {
		if lerr := locker.LockSQLInstallMarker(markerPath); lerr != nil {
			return &InstallError{Reason: ReasonOther, Message: "The SQL service install record could not be locked to administrators.", Cause: lerr}
		}
	}
	if verr := verifySQLConfigFile(m.Env, markerPath); verr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The SQL service install record is not locked to administrators, so the install was refused.", Cause: verr}
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
	return nil
}

// EnableSQL is the administrator-only owner activation (W8-03): it confirms
// the installed program and settings, checks the loaded folder against the
// persisted INSTALL-TIME folder in the admin-locked install record (SEC-3),
// re-confirms the data-folder lock, durably
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
		return &InstallError{Reason: ReasonOther, Message: "The settings file does not match the folder given for the SQL service. Re-run install-sql."}
	}
	// SEC-3: the folder the service was INSTALLED with comes from the
	// admin-locked install record written at install-sql — never from the
	// settings file being checked, because a file compared with itself can
	// never fail. Prove the record's lock first (unreadable or unverifiable
	// means refused), then read it, then compare. A missing or malformed
	// record is a refusal too: no record, no activation.
	markerPath := sqlInstallMarkerPath(progDir)
	if verr := verifySQLConfigFile(m.Env, markerPath); verr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The SQL service install record is not locked to administrators, so the service was not started. Re-run install-sql.", Cause: verr}
	}
	installedFolder, merr := readSQLInstallMarker(markerPath)
	if merr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The SQL service install record could not be read, so the service was not started. Re-run install-sql.", Cause: merr}
	}
	if loaded.Folder != installedFolder {
		return &InstallError{Reason: ReasonOther, Message: "The settings file does not match the folder the SQL service was installed with. Re-run install-sql."}
	}
	// SEC-1: re-prove the settings lock at activation — the file may have been
	// replaced or its ACL widened since install-sql. Unverifiable means refused.
	if verr := verifySQLConfigFile(m.Env, configPath); verr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The settings file for the SQL service is no longer locked to administrators, so it was not started. Re-run install-sql with the installed settings file.", Cause: verr}
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
		// SEC-1: prove the settings lock again immediately before the start, so
		// a config swapped after the entry check cannot ride into a running
		// service. This is the last moment the installer controls the file the
		// SCM will hand to the service process.
		if verr := verifySQLConfigFile(m.Env, configPath); verr != nil {
			return &InstallError{Reason: ReasonOther, Message: "The settings file for the SQL service is no longer locked to administrators, so it was not started. Re-run install-sql with the installed settings file.", Cause: verr}
		}
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
	// SEC-3: the install record belonged to the removed service; a re-install
	// writes a fresh one. Best effort, like the status publish above.
	if dir, derr := m.Env.ProgramDir(); derr == nil {
		_ = os.Remove(sqlInstallMarkerPath(dir))
	}
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
