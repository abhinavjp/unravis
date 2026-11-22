package blackbox

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// sqlLaunchRevoker takes back the launch entries install-sql added (SEC-6). It is
// given exactly the entries the same pure plan produced at install, so it can
// never strip anything this code did not add. OSEnv implements it on Windows and
// refuses off Windows; an Env without it is a test fake predating the seam
// (production always wires OSEnv, cmd/unravis-blackbox).
type sqlLaunchRevoker interface {
	RevokeSQLLaunchGrants(grants []sqlLaunchGrant) error
}

// The real Env must be able to revoke the launch grant on every build.
var _ sqlLaunchRevoker = OSEnv{}

// revokeSQLLaunch takes the planned entries back. Nothing is revoked on an Env
// that cannot do it, and no entry is ever replaced by a guess.
func revokeSQLLaunch(env Env, grants []sqlLaunchGrant) error {
	revoker, ok := env.(sqlLaunchRevoker)
	if !ok || len(grants) == 0 {
		return nil
	}
	return revoker.RevokeSQLLaunchGrants(grants)
}

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
	// Config is the settings file the launch grant was applied to (SEC-6), so
	// remove-sql revokes the entries on THAT file instead of guessing a path —
	// install-sql binds any absolute --config, not only the program-folder copy.
	// A record written before this packet has no config: then the program's own
	// entries are still revoked and nothing on an unknown file is touched.
	Config string `json:"config,omitempty"`
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

// sqlSettingsFileLocker assigns a settings file the administrator-only owner and
// DACL that VerifyAdminOnlyFile then proves (REG-1). OSEnv implements it on
// Windows and refuses off Windows. Like sqlConfigVerifier and
// sqlInstallMarkerLocker, the seam is optional only for test fakes predating it:
// production always wires OSEnv (cmd/unravis-blackbox), so the real install path
// always pins the owner before it proves it.
type sqlSettingsFileLocker interface {
	LockSQLSettingsFile(path string) error
}

// The real Env must be able to lock the settings file on every build.
var _ sqlSettingsFileLocker = OSEnv{}

// lockSQLConfigFile asks the Env to give configPath the owner and DACL the
// settings proof demands, before that proof runs. It repairs rather than
// merely detects because nothing else in the install ever assigns a file owner:
// the settings file would otherwise stay owned by whoever created it, which the
// owner rule refuses on purpose, so install-sql refused a healthy host (REG-1).
// A lock that fails is returned untouched — the caller proves the file right
// afterwards and still refuses, so nothing is ever bound on an unproven config.
// An Env with no lock seam is a test fake predating it; production always wires
// OSEnv, whose write and read-back are the same descriptor the record gets.
func lockSQLConfigFile(env Env, configPath string) error {
	l, ok := env.(sqlSettingsFileLocker)
	if !ok {
		return nil
	}
	return l.LockSQLSettingsFile(configPath)
}

// isInstalledConfigPath reports whether configPath is the settings file the
// host install wrote beside the installed program. Windows paths compare
// without case.
func isInstalledConfigPath(progDir, configPath string) bool {
	return strings.EqualFold(filepath.Clean(configPath), filepath.Clean(filepath.Join(progDir, ConfigFileName)))
}

// ResecureSettings gives the installed settings file back what install-sql
// gave it, after the host rewrote it (BL-139): the administrator owner and
// protected DACL, proven, then the SQL service's read entry. SaveConfig writes
// through a temp file and a rename, so every save drops both, and enable-sql
// would refuse again (REG-1 coming back).
//
// It does nothing when the SQL service is not installed, or when its install
// record binds another settings file (a custom --config is the operator's to
// lock, never the installer's). A missing or unreadable record means install-sql
// never finished, so there is nothing to restore either.
func (m *SQLManager) ResecureSettings(configPath string) error {
	st, err := m.Svc.State(SQLServiceName)
	if err != nil {
		return err
	}
	if st == SvcNotInstalled {
		return nil
	}
	progDir, err := m.Env.ProgramDir()
	if err != nil {
		return err
	}
	if !isInstalledConfigPath(progDir, configPath) {
		return nil
	}
	rec, err := readSQLInstallRecord(sqlInstallMarkerPath(progDir))
	if err != nil {
		return nil
	}
	if !isInstalledConfigPath(progDir, rec.Config) {
		return nil
	}
	granter, ok := m.Env.(sqlLaunchGranter)
	if !ok {
		return fmt.Errorf("blackbox: cannot grant the SQL service read access on this system")
	}
	sid, err := sqlServiceSID(SQLServiceName)
	if err != nil {
		return err
	}
	if err := lockSQLConfigFile(m.Env, configPath); err != nil {
		return err
	}
	if err := verifySQLConfigFile(m.Env, configPath); err != nil {
		return err
	}
	return granter.GrantSQLLaunchAccess(filepath.Join(progDir, InstalledExeName), configPath, sid)
}

// sqlInstallMarkerPath is the install record beside the installed program.
func sqlInstallMarkerPath(progDir string) string {
	return filepath.Join(progDir, sqlInstallMarkerName)
}

// writeSQLInstallMarker persists the install-time folder and the settings file
// the launch grant was applied to, atomically (temp + rename, the
// PublishSQLStopped pattern). The caller locks and proves the record afterwards;
// a write failure is an install failure, never a silently missing record.
func writeSQLInstallMarker(path, folder, config string) error {
	data, err := json.Marshal(sqlInstallRecord{Folder: folder, Config: config})
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
	rec, err := readSQLInstallRecord(path)
	if err != nil {
		return "", err
	}
	return rec.Folder, nil
}

// readSQLInstallRecord reads the whole install record (SEC-6 needs the settings
// file the grant was applied to). The folder rules are unchanged, so enable-sql
// still fails closed exactly as SEC-3 decided it. A settings path that is not a
// usable absolute path is dropped rather than trusted: remove-sql then revokes
// the program's own entries and touches nothing on a path it cannot name.
func readSQLInstallRecord(path string) (sqlInstallRecord, error) {
	var rec sqlInstallRecord
	raw, err := os.ReadFile(path)
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return rec, err
	}
	if strings.TrimSpace(rec.Folder) == "" || !filepath.IsAbs(rec.Folder) {
		return rec, errors.New("blackbox: the SQL install record names no usable folder")
	}
	if !filepath.IsAbs(rec.Config) {
		rec.Config = ""
	}
	return rec, nil
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

// fileACE is one access-control entry described in the only terms the rules
// need: the raw Windows ACE type, its inheritance/propagation flags, its access
// mask and its trustee SID string. The platform reader produces these; the
// rules below never touch an ACL.
type fileACE struct {
	Type byte
	// Flags is the Windows AceFlags byte: OBJECT_INHERIT (0x01),
	// CONTAINER_INHERIT (0x02), NO_PROPAGATE_INHERIT (0x04), INHERIT_ONLY (0x08)
	// and INHERITED (0x10). Two entries that differ here do not mean the same
	// thing, so both the grant rule and the revoke rule compare them.
	Flags byte
	Mask  uint32
	// Trustee is the SID string the entry names.
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

// settingsPathProblem accepts only a plain drive path: a drive letter, a colon and a separator (`C:\...`). Everything else is refused: a network
// (UNC) path, any device or NT-namespace path (`\\?\`, `\\.\`, `\??\UNC\...`, `\??\GLOBALROOT\...`), a path that starts with a separator
// in any mix of slashes, and a drive-relative path (`C:foo`). The owner and list of a file on a share are the file server's to change, so
// no proof here could stand for them. Pure, so it is tested on every platform. Which drive the letter really is (a local disk, a mapped share
// or a subst alias) is decided in Windows code: see localDriveProblem.
func settingsPathProblem(path string) error {
	if len(path) >= 3 && isASCIILetter(path[0]) && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return nil
	}
	return errors.New("blackbox: the settings file must be on a local drive, written like C:\\folder\\file")
}

// settingsHandleProblem is the rule for what an open settings file may be, judged from what the handle itself reports: a file on a disk (not a
// pipe or a device), not a folder (attribute 0x10), not a reparse point (0x400), with exactly one name (a hard link would let a file from a place
// someone else can write stand here) and no larger than maxConfigBytes. Pure, so it is tested on every platform; the literals match the Windows
// constants and are checked against them in settings_open_windows.go.
func settingsHandleProblem(isDisk bool, attrs, links uint32, size uint64) error {
	switch {
	case !isDisk:
		return refuse(WordNotDisk, errors.New("blackbox: the settings file is not a file on a disk"))
	case attrs&0x10 != 0:
		return refuse(WordFolder, errors.New("blackbox: the settings file is a folder"))
	case attrs&0x400 != 0:
		return refuse(WordLink, errors.New("blackbox: the settings file is a link"))
	case links != 1:
		return refuse(WordNames, fmt.Errorf("blackbox: the settings file has %d names", links))
	case size > uint64(maxConfigBytes):
		return refuse(WordTooLarge, errors.New("blackbox: the settings file is too large"))
	}
	return nil
}

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// localVolumeTarget reports whether a DOS-device target (QueryDosDevice of "C:") is a disk volume of this computer. A mapped network drive
// resolves to a redirector (`\Device\LanmanRedirector\...`) and a subst alias to `\??\<path>`; neither is a disk the proof can stand for.
//
// The match is deliberately strict: the WHOLE target must be `\Device\HarddiskVolume<digits>`. A prefix test would also pass a raw-target subst
// (`\Device\HarddiskVolume3\Users\bob`, DDD_RAW_TARGET_PATH) and a shadow copy (`\Device\HarddiskVolumeShadowCopy2`), whose root is an alias
// the chain proof would stop at.
func localVolumeTarget(target string) bool {
	return localVolumeRE.MatchString(target)
}

var localVolumeRE = regexp.MustCompile(`(?i)^\\Device\\HarddiskVolume[0-9]+$`)

// BL-140: the folders above the settings file. The file's own access list proves
// nobody else can edit it, but whoever may DELETE it, or delete or rename a folder
// above it, can swap in a file of their own, and the SQL service reads that file
// again at its next start. So every folder from the file's parent up to the volume
// root must also be changeable only by SYSTEM, Administrators and TrustedInstaller.
//
// adminOnlyDirChangeMask is every right that lets a trustee remove, rename, or
// re-own a folder, or remove an entry from it: delete-child, delete, DACL-change,
// owner-change and the generic/maximum grants that map onto them. Creating a file
// or a subfolder (add-file, add-subdirectory) is not in the mask: it cannot
// replace an entry that exists, and the stock root and ProgramData folders grant
// it to ordinary users.
const adminOnlyDirChangeMask uint32 = 0x00000040 | // FILE_DELETE_CHILD
	0x00010000 | // DELETE
	0x00040000 | // WRITE_DAC
	0x00080000 | // WRITE_OWNER
	0x02000000 | // MAXIMUM_ALLOWED
	0x10000000 | // GENERIC_ALL
	0x40000000 // GENERIC_WRITE

const (
	sidAdministratorsSID = "S-1-5-32-544"
	// sidTrustedInstallerSID is NT SERVICE\TrustedInstaller, which owns C:\ and
	// Program Files on a stock Windows.
	sidTrustedInstallerSID = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
	// aceInheritOnly marks an entry that only passes to children and does not
	// apply to the folder it sits on.
	aceInheritOnly byte = 0x08
)

// trustedChainTrustee reports whether sid may hold change rights on a folder
// above the settings file.
func trustedChainTrustee(sid string) bool {
	return strings.EqualFold(sid, sidSystemSID) || strings.EqualFold(sid, sidAdministratorsSID) || strings.EqualFold(sid, sidTrustedInstallerSID)
}

// trustedChainOwner reports whether sid may OWN a folder above the settings file.
// An owner holds implicit WRITE_DAC whatever the list says, so an ordinary user
// who owns any folder in the chain could re-grant themselves delete rights.
func trustedChainOwner(sid string) bool { return trustedChainTrustee(sid) }

// adminOnlyDirDACL decides whether a described folder access list leaves the
// folder (and the entries in it) changeable only by SYSTEM, Administrators and
// TrustedInstaller. Same fail-closed shape as adminOnlyDACL: a missing (NULL)
// list, an unrecognized entry type, or any allow-family entry (plain, object or
// callback) that carries a change right for another trustee refuses the folder.
// An entry marked inherit-only does not apply to the folder itself, so it is
// skipped (the stock CREATOR OWNER and Authenticated Users entries are of that
// kind). Deny, audit, alarm and label entries grant nothing and are skipped.
func adminOnlyDirDACL(present bool, aces []fileACE) error {
	if !present {
		return errors.New("blackbox: a folder above the settings file has no access list, so anyone may change it")
	}
	for _, a := range aces {
		switch a.Type {
		case 0x1, 0x6, 0xA, 0xC, // access-denied
			0x2, 0x3, 0x7, 0x8, 0xD, 0xE, 0xF, 0x10, 0x11, 0x12, 0x13: // audit, alarm, label, attribute, policy
			continue
		case 0x0, 0x5, 0x9, 0xB: // access-allowed (plain, object, callback)
			if a.Flags&aceInheritOnly != 0 || a.Mask&adminOnlyDirChangeMask == 0 || trustedChainTrustee(a.Trustee) {
				continue
			}
			return errors.New("blackbox: a folder above the settings file lets a non-administrator delete or rename it, or its entries")
		default:
			return fmt.Errorf("blackbox: an unrecognized access entry (type %d) on a folder above the settings file", a.Type)
		}
	}
	return nil
}

// The least-privilege launch rights (W7-01) as pure literals, so the grant plan,
// its idempotency rule and its revocation decide off Windows exactly as the
// Windows code applies them (the precedent is adminOnlyWriteMask). The values are
// the Windows file-access constants: FILE_GENERIC_READ|FILE_GENERIC_EXECUTE,
// FILE_GENERIC_READ, and FILE_TRAVERSE|FILE_READ_ATTRIBUTES|SYNCHRONIZE.
const (
	aceTypeAllow byte = 0x0 // ACCESS_ALLOWED_ACE_TYPE
	// aceFlagsExplicit is the AceFlags byte of an entry that lives on this object
	// only: no inheritance or propagation flags, and not inherited from a parent.
	aceFlagsExplicit byte   = 0x0
	maskSQLProgram   uint32 = 0x001200A9
	maskSQLSettings  uint32 = 0x00120089
	maskSQLTraverse  uint32 = 0x001000A0
)

// sqlLaunchGrant is one entry of the launch grant: the object it belongs on and
// the entry. Pure, so what install adds and what remove takes back are literally
// the same list.
type sqlLaunchGrant struct {
	Path string
	ACE  fileACE
}

// sqlAllowACE is one explicit, non-inheritable access-allowed entry for sid.
func sqlAllowACE(mask uint32, sid string) fileACE {
	return fileACE{Type: aceTypeAllow, Flags: aceFlagsExplicit, Mask: mask, Trustee: sid}
}

// sqlObjectLaunchGrants is the grant for one file: the given read-side rights on
// the file itself plus traverse and read-attributes on the folder holding it. A
// relative path or a missing service account is refused — the plan is applied to
// a real ACL, so guessing at a path is never safe (SEC-6).
func sqlObjectLaunchGrants(path string, mask uint32, sid string) ([]sqlLaunchGrant, error) {
	if strings.TrimSpace(sid) == "" {
		return nil, errors.New("blackbox: a launch grant needs the service account")
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return nil, errors.New("blackbox: a launch grant needs an absolute object path")
	}
	return []sqlLaunchGrant{
		{Path: path, ACE: sqlAllowACE(mask, sid)},
		{Path: filepath.Dir(path), ACE: sqlAllowACE(maskSQLTraverse, sid)},
	}, nil
}

// sqlProgramLaunchGrants is the program half of the launch grant.
func sqlProgramLaunchGrants(exePath, sid string) ([]sqlLaunchGrant, error) {
	return sqlObjectLaunchGrants(exePath, maskSQLProgram, sid)
}

// sqlSettingsLaunchGrants is the settings half of the launch grant.
func sqlSettingsLaunchGrants(configPath, sid string) ([]sqlLaunchGrant, error) {
	return sqlObjectLaunchGrants(configPath, maskSQLSettings, sid)
}

// sqlLaunchGrants is the whole grant: the installed program and the bound
// settings file, each with the folder that holds it, with a folder two files
// share listed once.
//
// SEC-6: nothing above those two folders is granted. The code before this packet
// climbed to the drive root and appended a traverse entry to every ancestor on
// EVERY install, and never took one back. A service process holds bypass traverse
// checking (SeChangeNotifyPrivilege), so it never needs FILE_TRAVERSE on a folder
// it only passes through. The two folders that do keep an entry are the ones the
// host install locks to SYSTEM and Administrators with a PROTECTED DACL
// (secureSDDL), where nothing is inherited and no other account appears at all:
// the service must still be able to read the attributes of the folder holding the
// settings it opens on every start, and a site policy can turn bypass traverse
// checking off. Those entries are read-side only, non-inheritable and idempotent,
// so keeping them costs nothing; removing them would cut a protection the service
// genuinely uses.
func sqlLaunchGrants(exePath, configPath, sid string) ([]sqlLaunchGrant, error) {
	prog, err := sqlProgramLaunchGrants(exePath, sid)
	if err != nil {
		return nil, err
	}
	settings, err := sqlSettingsLaunchGrants(configPath, sid)
	if err != nil {
		return nil, err
	}
	return mergeLaunchGrants(prog, settings), nil
}

// mergeLaunchGrants joins grant lists in order, dropping an entry that repeats
// the path and an equal ACE of one already kept (SEC-6). The same folder can be
// named by two halves of a plan — the program folder is also the settings folder
// when --config sits next to the exe — and one ACE must be written and revoked
// once, never twice.
func mergeLaunchGrants(lists ...[]sqlLaunchGrant) []sqlLaunchGrant {
	var out []sqlLaunchGrant
	for _, list := range lists {
		for _, gr := range list {
			dup := false
			for _, have := range out {
				if have.Path == gr.Path && aceEqual(have.ACE, gr.ACE) {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, gr)
			}
		}
	}
	return out
}

// aceEqual reports whether two entries mean exactly the same permission: same
// type, same inheritance/propagation flags, same rights, same trustee. Comparing
// only the trustee would call a read entry and a full-control entry the same and
// skip a grant the service needs (SEC-6).
func aceEqual(a, b fileACE) bool {
	return a.Type == b.Type && a.Flags == b.Flags && a.Mask == b.Mask && strings.EqualFold(a.Trustee, b.Trustee)
}

// aceGrantNeeded says whether want still has to be added to an object that
// already holds aces. An equal entry already present means nothing changes, so a
// repeated install never piles up duplicates. An inherited entry with the same
// rights is NOT equal — it belongs to a parent and disappears when the parent
// changes — so the explicit grant is still made.
func aceGrantNeeded(aces []fileACE, want fileACE) bool {
	for _, a := range aces {
		if aceEqual(a, want) {
			return false
		}
	}
	return true
}

// aceGrantKept returns the entries that survive revoking want — every entry not
// exactly equal to it — and how many went. Only entries this code itself added can
// match (explicit, non-inheritable, access-allowed, read-side, the service SID),
// so an operator's entry, an inherited one, a deny and SYSTEM or Administrators
// are never touched (SEC-6).
func aceGrantKept(aces []fileACE, want fileACE) ([]fileACE, int) {
	kept := make([]fileACE, 0, len(aces))
	removed := 0
	for _, a := range aces {
		if aceEqual(a, want) {
			removed++
			continue
		}
		kept = append(kept, a)
	}
	return kept, removed
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

// errSQLFolderOwner is the one sentence the install ever produces when the SQL
// data folder could not be given the owner the sample-folder rule demands
// (R1-s1). The folder rule (internal/bbsql.CheckFolderAccess) refuses a folder
// whose owner is not one of its trustees, because an owner outside the DACL holds
// implicit WRITE_DAC — so a folder this install leaves owned by whoever created it
// would be refused at every service start, with no way for the operator to fix it
// except a re-install. The write needs a privilege only an elevated token holds,
// so the sentence names the remedy that works. Declared here, off the platform
// files, so the fail-closed order is testable on every build; the Windows installer
// produces it from both the failed write and a failed owner read-back.
var errSQLFolderOwner = errors.New("blackbox: the SQL data folder owner could not be set to Administrators; run this from an elevated command prompt")

// sidSystemSID is NT AUTHORITY\SYSTEM, the owner a LocalSystem service gives
// everything it creates, including a SQL data folder it recreates after a
// delete/repoint.
const sidSystemSID = "S-1-5-18"

// trustedSQLFolderOwner is the read-back test for the SQL data folder's owner
// after the install locked it (R1-s1): the BUILTIN\Administrators owner
// install-sql writes, SYSTEM, or the SQL service SID itself — exactly the three
// owners internal/bbsql's sample-folder rule trusts, named through its one
// constant so the installer and the rule cannot drift apart. The SQL service SID
// stays accepted because the service runs as that account and is the only one who
// can re-create the folder once it is gone; it already holds full control in the
// DACL install-sql writes.
func trustedSQLFolderOwner(owner, sid string) bool {
	return strings.EqualFold(owner, bbsql.FolderOwnerSID()) ||
		strings.EqualFold(owner, sidSystemSID) ||
		(sid != "" && strings.EqualFold(owner, sid))
}

// trustedFileOwner is the owner rule for a file the installer writes and then
// trusts with its own rights: SYSTEM or BUILTIN\Administrators, nothing else
// (R1-s2). The account that ran the install is deliberately NOT accepted here,
// which is the difference from trustedOwner: the owner keeps implicit WRITE_DAC
// whatever the DACL says — measured, a DACL write still succeeds after every entry
// naming the account was removed — so a settings file or install record owned by
// it could re-open itself at any later moment, and the install record is exactly
// what SEC-3's folder comparison is anchored to. The installer assigns this owner
// itself (lockSQLInstallMarker), so accepting only these two asks nothing of the
// operator.
func trustedFileOwner(owner string) bool {
	return strings.EqualFold(owner, sidSystemSID) || strings.EqualFold(owner, bbsql.FolderOwnerSID())
}

// InstallSQL registers the SQL service under its own virtual account and locks
// its data folder to SYSTEM, Administrators and that service SID. It also gives
// the folder the BUILTIN\Administrators OWNER the sampler's own folder rule
// demands (R1-s1): that rule refuses a folder whose owner is not one of its
// trustees, so a folder left owned by whoever ran the install would brick the DB
// lane at every service start. The host service, its account and the recordings
// folder ACL are left as they are. Whatever it did is undone when a step fails,
// including a folder whose owner could not be assigned.
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
	// administrator-locked BEFORE anything is registered. A user-writable
	// --config would let an ordinary user aim the service at their own folder on
	// its next start. Unverifiable means refused.
	// REG-1: the lock is what makes the file admin-locked, so it is applied here
	// first and proven right after — the same write-then-prove order the install
	// record below uses, and the only reason the proof can pass at all on a real
	// host, where the file is otherwise owned by the account that wrote it. The
	// settings-file lock is the single thing mutated before this point; a failed
	// lock or a failed proof still refuses with nothing registered.
	// REG-1 follow-up: only the INSTALLED settings file is pinned. A --config
	// anywhere else (BBX-5-02 allows one) is proven as it stands and must
	// already be administrator-locked: pinning it here would take over a file an
	// ordinary user wrote and make SEC-1's refusal pass for it.
	// The lock is deliberately not on the undo list: a refused install leaves the
	// settings file stricter than it found it, which takes nothing from anyone
	// (SYSTEM and Administrators keep full control) and is what a retry needs.
	// The match is on the path text, so an 8.3 short name or a \\?\ prefix of
	// the installed file is not pinned and is refused by the proof below.
	if isInstalledConfigPath(progDir, configPath) {
		if lerr := lockSQLConfigFile(m.Env, configPath); lerr != nil {
			return &InstallError{Reason: ReasonOther, Message: "The settings file for the SQL service could not be locked to administrators. Run this from an elevated command prompt.", Cause: lerr}
		}
	}
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
	// Lock AND own the folder before the service exists, so no SQL data is ever
	// written somewhere ordinary users can read. SecureSQLDir verifies the ancestors
	// and the existing children before it mutates anything (W7-03), then writes the
	// owner and the protected DACL in one descriptor and reads the owner back: this
	// is the only code path that sets an owner, and it stops here, before the
	// service, the grant and the install record exist, so an install that cannot
	// assign the owner never leaves a folder the service would refuse forever
	// (R1-s1). Any failure below runs the undo list above.
	if serr := sec.SecureSQLDir(dir, sid); serr != nil {
		return folderErr(serr)
	}
	// Least-privilege launch access for the service SID (W7-01). The grant is
	// read-only and IDEMPOTENT (SEC-6): applying it to an object that already
	// holds exactly that entry changes nothing, so a rollback that leaves the
	// entries in place and the install that follows it cannot pile up duplicates.
	// On rollback the grant is left in place deliberately: it gives ordinary
	// users nothing and a re-install re-uses it. remove-sql takes it back.
	if gerr := granter.GrantSQLLaunchAccess(exe, configPath, sid); gerr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The SQL service could not be given read access to its program and settings.", Cause: gerr}
	}
	// SEC-3: persist the INSTALL-TIME folder in an admin-locked install record
	// beside the installed program, so enable-sql compares the loaded settings
	// against a persisted fact instead of the settings file itself. Write,
	// lock, then prove; any failure refuses the install and undoes the record.
	// The lock is what assigns the record's owner (R1-s2), so the three steps
	// here must stay in this order: prove-after-write proves an owner the install
	// has already set, and the folder re-own above has already happened before
	// any of it.
	// SEC-6: the record also names the settings file the grant was applied to,
	// so remove-sql knows exactly which file's entries to take back.
	markerPath := sqlInstallMarkerPath(progDir)
	if werr := writeSQLInstallMarker(markerPath, cfg.Folder, configPath); werr != nil {
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
	// Re-confirm the lock before activation: SecureSQLDir re-checks the owner it
	// assigned, the ancestors and the children, and re-writes the folder's owner
	// and DACL together (R1-s1). This runs after the install record is written and
	// proved, so the folder is never reported as usable on an owner the sampler's
	// own rule refuses.
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
// changed. SEC-6: it also takes back the least-privilege launch entries
// install-sql added to the program, to the settings file it was bound to and to
// those two folders — those entries only, never anything an operator put there.
// The data goes only when DeleteData is set, and only <folder>\sql: the
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
	// SEC-6: take back the launch entries install-sql added, while the record
	// that names the settings file they were added to is still there. Only the
	// entries of the same pure plan are revoked; what cannot be proven was added
	// (no record, no settings path in it) is left exactly as it is, never
	// guessed at. The service is already deleted, so a failure here is reported
	// rather than left silent.
	var revokeErr error
	if dir, derr := m.Env.ProgramDir(); derr == nil {
		marker := sqlInstallMarkerPath(dir)
		rec, rerr := readSQLInstallRecord(marker)
		sid, serr := sqlServiceSID(SQLServiceName)
		var grants []sqlLaunchGrant
		if serr == nil {
			var prog, settings []sqlLaunchGrant
			if prog, revokeErr = sqlProgramLaunchGrants(filepath.Join(dir, InstalledExeName), sid); revokeErr == nil {
				if rerr == nil && rec.Config != "" {
					settings, revokeErr = sqlSettingsLaunchGrants(rec.Config, sid)
				}
			}
			grants = mergeLaunchGrants(prog, settings)
		} else {
			revokeErr = serr
		}
		if revokeErr == nil {
			revokeErr = revokeSQLLaunch(m.Env, grants)
		}
		// SEC-3: the install record belonged to the removed service; a re-install
		// writes a fresh one. Best effort, like the status publish above.
		_ = os.Remove(marker)
	}
	if m.RevokeScript != "" {
		fmt.Fprintln(os.Stdout, m.RevokeScript)
	}
	if m.DeleteData {
		if rerr := os.RemoveAll(SQLDataDir(cfg.Folder)); rerr != nil {
			return rerr
		}
	}
	if revokeErr != nil {
		return &InstallError{Reason: ReasonOther, Message: "The SQL service was removed, but its launch permissions could not all be taken back. Remove them by hand: they grant read access to the service account alone.", Cause: revokeErr}
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
