package setup

import (
	"fmt"
	"strconv"
	"strings"

	"api-audit-explorer/internal/datadir"
	"api-audit-explorer/internal/keeper"
)

// The permissions setup sets, as SDDL. The shared folder lists are the ones in buildout/docs/security.md ("The shared data folder"):
//
//   - C:\ProgramData\Unravis and its cases folder: owner Administrators; SYSTEM, Administrators and the keeper's service account have
//     full control; Users may read the folder and add files and folders to it, and modify what is inside only through inherit-only
//     entries (so they never hold delete-child, change-permissions or take-ownership on the folder itself). The list is protected:
//     nothing is inherited from C:\ProgramData, whose CREATOR OWNER and Users entries would let a person take control.
//   - keeper: SYSTEM, Administrators and the keeper's service account only.
//   - HKLM\SOFTWARE\Unravis (the record): owner Administrators; SYSTEM and Administrators full control; Users read.
//
// Users' 0x1200af on the folder itself is read, list, traverse, read attributes and extended attributes, add file and add folder;
// 0x1301bf on what is inside is modify (read, write, execute, delete) without change-permissions or take-ownership.
const (
	usersFolderSelf  = "0x1200af"
	usersFolderInner = "0x1301bf"
	usersReadExecute = "0x1200a9"
)

// SharedFolderSDDL is the access list of the shared folder and of its cases folder.
func SharedFolderSDDL() string {
	k := keeper.ServiceSID(keeper.ServiceName)
	return "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + k + ")(A;;" + usersFolderSelf + ";;;BU)(A;OICIIO;" + usersFolderInner + ";;;BU)"
}

// KeeperFolderSDDL is the access list of the keeper's folder.
func KeeperFolderSDDL() string {
	k := keeper.ServiceSID(keeper.ServiceName)
	return "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + k + ")"
}

// ProgramFolderSDDL is the program folder of an install for everyone: only administrators and the system may change it, everyone may
// run it. The keeper runs from here, so a person who could replace its program could read every saved password.
const ProgramFolderSDDL = "O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;" + usersReadExecute + ";;;BU)"

// BlackboxProgramSDDL and BlackboxRecordingsSDDL are what blackbox.OSEnv.SecureDir sets: administrators and the system only, with
// users reading the recordings.
const (
	BlackboxProgramSDDL    = "O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	BlackboxRecordingsSDDL = "O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GRGX;;;BU)"
)

// AdminOnlySDDL locks the folder that holds an elevated run's journal: a person who could write it could make the next elevated run
// "undo" something they chose.
const AdminOnlySDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// AnchorKeySDDL is the access list of HKLM\SOFTWARE\Unravis.
const AnchorKeySDDL = "O:BAD:P(A;CI;KA;;;SY)(A;CI;KA;;;BA)(A;CI;KR;;;BU)"

// ReadKeyFileSDDL limits the silent-mode read key file to the person (sid) and the system.
func ReadKeyFileSDDL(sid string) string {
	return "O:" + sid + "D:P(A;;FA;;;SY)(A;;FA;;;" + sid + ")"
}

// WorkerPipeSDDL is the access list of the pipe between the setup window and its elevated worker: the person's account and the system
// only (installer-and-releases.md section 3). Administrators are not listed.
func WorkerPipeSDDL(sid string) string { return "D:P(A;;GA;;;SY)(A;;GA;;;" + sid + ")" }

var sddlSIDs = map[string]string{
	"SY": "S-1-5-18", "BA": "S-1-5-32-544", "BU": "S-1-5-32-545", "WD": "S-1-1-0", "AU": "S-1-5-11",
	"CO": "S-1-3-0", "OW": "S-1-3-4", "LS": "S-1-5-19", "NS": "S-1-5-20",
}

var sddlRights = map[string]uint32{
	"FA": 0x1F01FF, "FR": 0x120089, "FW": 0x120116, "FX": 0x1200A0,
	"KA": 0xF003F, "KR": 0x20019, "KW": 0x20006, "KX": 0x20019,
	"GA": 0x10000000, "GX": 0x20000000, "GW": 0x40000000, "GR": 0x80000000,
	"SD": 0x10000, "RC": 0x20000, "WD": 0x40000, "WO": 0x80000,
}

var sddlFlags = map[string]uint8{"OI": 0x01, "CI": 0x02, "NP": 0x04, "IO": 0x08, "ID": 0x10}

// ParseSDDL turns the owner and DACL of an SDDL string into what datadir's trust check reads. It understands the forms setup writes
// and the common forms Windows returns; anything else is an error, never a silent guess.
func ParseSDDL(s string) (datadir.FolderSecurity, error) {
	var fs datadir.FolderSecurity
	rest := s
	sawDACL := false
	for rest != "" {
		if len(rest) < 2 || rest[1] != ':' {
			return fs, fmt.Errorf("sddl: cannot read %q", rest)
		}
		part := rest[0]
		rest = rest[2:]
		end := nextSection(rest)
		body := rest[:end]
		rest = rest[end:]
		switch part {
		case 'O':
			fs.Owner = sidOf(body)
		case 'G':
		case 'D':
			sawDACL = true
			if body == "NO_ACCESS_CONTROL" {
				fs.NullDACL = true
				continue
			}
			i := strings.IndexByte(body, '(')
			if i < 0 {
				i = len(body)
			}
			for _, ace := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(body[i:], "("), ")"), ")(") {
				if ace == "" {
					continue
				}
				a, err := parseACE(ace)
				if err != nil {
					return fs, err
				}
				fs.ACEs = append(fs.ACEs, a)
			}
		case 'S':
		default:
			return fs, fmt.Errorf("sddl: unknown part %c", part)
		}
	}
	if !sawDACL {
		fs.NullDACL = true
	}
	return fs, nil
}

// nextSection finds where the next "X:" part starts (outside parentheses).
func nextSection(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		default:
			if depth == 0 && i+1 < len(s) && s[i+1] == ':' && strings.IndexByte("OGDS", s[i]) >= 0 && i > 0 {
				return i
			}
		}
	}
	return len(s)
}

func sidOf(s string) string {
	if v, ok := sddlSIDs[s]; ok {
		return v
	}
	return s
}

func parseACE(s string) (datadir.FolderACE, error) {
	f := strings.Split(s, ";")
	if len(f) < 6 {
		return datadir.FolderACE{}, fmt.Errorf("sddl: cannot read entry %q", s)
	}
	var a datadir.FolderACE
	switch f[0] {
	case "A":
		a.Type = 0
	case "D":
		a.Type = 1
	default:
		return a, fmt.Errorf("sddl: entry type %q is not handled", f[0])
	}
	for fl := f[1]; fl != ""; fl = fl[2:] {
		if len(fl) < 2 {
			return a, fmt.Errorf("sddl: bad flags %q", f[1])
		}
		v, ok := sddlFlags[fl[:2]]
		if !ok {
			return a, fmt.Errorf("sddl: flag %q is not handled", fl[:2])
		}
		a.Flags |= v
	}
	if strings.HasPrefix(f[2], "0x") || strings.HasPrefix(f[2], "0X") {
		v, err := strconv.ParseUint(f[2][2:], 16, 32)
		if err != nil {
			return a, fmt.Errorf("sddl: bad rights %q", f[2])
		}
		a.Mask = uint32(v)
	} else {
		for r := f[2]; r != ""; r = r[2:] {
			if len(r) < 2 {
				return a, fmt.Errorf("sddl: bad rights %q", f[2])
			}
			v, ok := sddlRights[r[:2]]
			if !ok {
				return a, fmt.Errorf("sddl: right %q is not handled", r[:2])
			}
			a.Mask |= v
		}
	}
	a.SID = sidOf(f[5])
	return a, nil
}
