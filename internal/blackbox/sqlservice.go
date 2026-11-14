package blackbox

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
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
func (m *SQLManager) InstallSQL(cfg Config) (err error) {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !m.Env.IsAdmin() {
		return msgNoAdmin()
	}
	st, serr := m.Svc.State(SQLServiceName)
	if serr != nil {
		return &InstallError{Reason: ReasonOther, Message: "Could not ask Windows about the service: " + serr.Error(), Cause: serr}
	}
	if st != SvcNotInstalled {
		return &InstallError{Reason: ReasonExists, Message: fmt.Sprintf("A service named %s already exists. Remove it first (remove-sql).", SQLServiceName)}
	}
	sec, ok := m.Env.(sqlDirSecurer)
	if !ok {
		return &InstallError{Reason: ReasonOther, Message: "This program cannot lock the SQL data folder on this system."}
	}
	sid, serr := sqlServiceSID(SQLServiceName)
	if serr != nil {
		return &InstallError{Reason: ReasonOther, Message: "Could not work out the SQL service account: " + serr.Error(), Cause: serr}
	}
	progDir, perr := m.Env.ProgramDir()
	if perr != nil {
		return &InstallError{Reason: ReasonOther, Message: "Could not find the Program Files folder: " + perr.Error(), Cause: perr}
	}

	var undo undoList
	defer func() {
		if err != nil {
			undo.run()
		}
	}()
	dir := SQLDataDir(cfg.Folder)
	folderErr := func(cause error) error {
		return &InstallError{Reason: ReasonFolder, Message: fmt.Sprintf("%s could not be made and locked for the SQL service only: %s", dir, cause.Error()), Cause: cause}
	}
	if derr := ensureDir(dir, &undo); derr != nil {
		return folderErr(derr)
	}
	// Lock the folder before the service exists, so no SQL data is ever written
	// somewhere ordinary users can read.
	if serr := sec.SecureSQLDir(dir, sid); serr != nil {
		return folderErr(serr)
	}
	spec := ServiceSpec{Name: SQLServiceName, DisplayName: "Unravis Blackbox SQL",
		Description: "Reads SQL Server performance counters for the Blackbox, in its own process and under its own virtual account. The owner grants its SQL rights.",
		ExePath:     filepath.Join(progDir, InstalledExeName),
		Args:        []string{"sql-service", "--config", filepath.Join(progDir, ConfigFileName)},
		Account:     SQLServiceAccount, SIDUnrestricted: true}
	if cerr := m.Svc.Create(spec); cerr != nil {
		return &InstallError{Reason: ReasonOther, Message: "Windows would not create the service: " + cerr.Error(), Cause: cerr}
	}
	undo.add(func() {
		_ = m.Svc.Stop(SQLServiceName)
		_ = m.waitSQL(SvcStopped, 10*time.Second)
		_ = m.Svc.Delete(SQLServiceName)
	})
	if serr := m.Svc.Start(SQLServiceName); serr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The service did not start: " + serr.Error(), Cause: serr}
	}
	if werr := m.waitSQL(SvcRunning, 15*time.Second); werr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The service did not start: " + werr.Error(), Cause: werr}
	}
	return nil
}

// RemoveSQL stops and deletes the SQL service and prints the owner-run revoke
// text (it never runs it). The Blackbox name must be confirmed before anything is
// changed. The data goes only when DeleteData is set, and only <folder>\sql: the
// recordings folder and everything else in it stay.
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
		return err
	}
	if st == SvcNotInstalled {
		return ErrNotSQLInstalled
	}
	if st != SvcStopped {
		if err := m.Svc.Stop(SQLServiceName); err != nil {
			return err
		}
		if err := m.waitSQL(SvcStopped, 30*time.Second); err != nil {
			return err
		}
	}
	if err := m.Svc.Delete(SQLServiceName); err != nil {
		return err
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
