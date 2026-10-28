package datadir

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestResolveLayouts(t *testing.T) {
	pd, uc := filepath.FromSlash("/pd"), filepath.FromSlash("/uc")
	shared := filepath.Join(pd, "Unravis")
	yes := func(p string) error {
		if p == shared {
			return nil
		}
		return ErrNotShared
	}
	no := func(string) error { return ErrNotShared }
	planted := func(string) error { return errors.New("the shared folder is owned by S-1-5-21-9-9-9-1002") }

	l := Resolve(env(nil), pd, uc, no)
	if l.Mode != ModeUser || l.Base != filepath.Join(uc, "APIAuditExplorer") || l.Cases != "" || l.Pipe != DefaultPipe || l.SharedRefused != "" {
		t.Fatalf("no shared folder: %+v", l)
	}
	// A folder that is present but not trusted (a person made it) keeps the per-user layout and says why.
	l = Resolve(env(nil), pd, uc, planted)
	if l.Mode != ModeUser || l.Root != "" || l.SharedRefused == "" || l.Base != filepath.Join(uc, "APIAuditExplorer") {
		t.Fatalf("planted shared folder: %+v", l)
	}
	if l = Resolve(env(nil), pd, uc, nil); l.Mode != ModeUser || l.SharedRefused != "" {
		t.Fatalf("no trust check: %+v", l)
	}
	l = Resolve(env(nil), pd, uc, yes)
	if !l.Shared() || l.SharedRefused != "" || l.Root != shared || l.Cases != filepath.Join(shared, "cases") || l.KeeperDir != filepath.Join(shared, "keeper") || l.UserDir != filepath.Join(uc, "APIAuditExplorer") {
		t.Fatalf("installer folder present: %+v", l)
	}
	// AUDIT_CONFIG_DIR keeps the old per-user layout even when the shared folder exists.
	l = Resolve(env(map[string]string{EnvConfigDir: "/cfg"}), pd, uc, yes)
	if l.Mode != ModeUser || l.Base != "/cfg" || l.Cases != filepath.Join("/cfg", "cases") {
		t.Fatalf("AUDIT_CONFIG_DIR: %+v", l)
	}
	// UNRAVIS_DATA_DIR forces shared mode, with the per-user folder still known for the migration.
	l = Resolve(env(map[string]string{EnvDataDir: "/data", EnvConfigDir: "/cfg", EnvKeeperDir: "/k", EnvKeeperPipe: `\\.\pipe\t`}), pd, uc, no)
	if !l.Shared() || l.Root != "/data" || l.UserDir != "/cfg" || l.KeeperDir != "/k" || l.Pipe != `\\.\pipe\t` {
		t.Fatalf("UNRAVIS_DATA_DIR: %+v", l)
	}
}

func TestKeeperDefault(t *testing.T) {
	dir, pipe := KeeperDefault(env(map[string]string{EnvDataDir: "/data"}))
	if dir != filepath.Join("/data", "keeper") || pipe != DefaultPipe {
		t.Fatalf("%s %s", dir, pipe)
	}
	dir, _ = KeeperDefault(env(map[string]string{EnvKeeperDir: "/k"}))
	if dir != "/k" {
		t.Fatal(dir)
	}
}

const (
	sidSystem = "S-1-5-18"
	sidAdmins = "S-1-5-32-544"
	sidUsers  = "S-1-5-32-545"
	sidPerson = "S-1-5-21-1-2-3-1001"
	sidAuth   = "S-1-5-11"
)

// installerACL is what the installer is expected to set on the shared folder and its cases folder: SYSTEM and Administrators in full,
// people may read and add, and full control over what they put inside only through inherit-only entries.
func installerACL() FolderSecurity {
	return FolderSecurity{Owner: sidAdmins, ACEs: []FolderACE{
		{Mask: 0x1F01FF, SID: sidSystem},
		{Mask: 0x1F01FF, SID: sidAdmins},
		{Mask: 0x1200A9 | 0x2 | 0x4, SID: sidUsers},                  // read, add files and folders
		{Flags: aceInheritOnly | 0x3, Mask: 0x1301BF, SID: sidUsers}, // modify, for files inside only
		{Flags: aceInheritOnly | 0x3, Mask: 0x10000000, SID: "S-1-3-0"},
		{Mask: 0x1F01FF, SID: "S-1-5-80-1-2-3-4-5"}, // the keeper's service SID
		{Type: 1, Mask: 0x1F01FF, SID: sidPerson},   // a deny takes nothing away
	}}
}

func TestCheckFolder(t *testing.T) {
	if err := CheckFolder("root", installerACL()); err != nil {
		t.Fatalf("installer ACL: %v", err)
	}
	for _, owner := range []string{sidSystem, trustedInstallerSID} {
		fs := installerACL()
		fs.Owner = owner
		if err := CheckFolder("root", fs); err != nil {
			t.Fatalf("owner %s: %v", owner, err)
		}
	}
	bad := map[string]FolderSecurity{}
	fs := installerACL()
	fs.Owner = sidPerson
	bad["owned by a person (made the folder before the installer)"] = fs
	fs = installerACL()
	fs.Reparse = true
	bad["a link to elsewhere"] = fs
	fs = installerACL()
	fs.NullDACL = true
	bad["no access list"] = fs
	fs = installerACL()
	fs.ACEs = append(fs.ACEs, FolderACE{Mask: 0x1F01FF, SID: sidEveryone})
	bad["Everyone gets full control"] = fs
	// The installer contract (security.md): if cases is made normally under a root whose Users modify entry is inherit-only, that entry
	// arrives on cases as a plain inherited entry (flag 0x10), and modify includes delete. cases needs a protected list instead.
	fs = installerACL()
	fs.ACEs = append(fs.ACEs, FolderACE{Flags: 0x10 | 0x3, Mask: 0x1301BF, SID: sidUsers})
	bad["cases inheriting the root's modify entry"] = fs
	for name, mask := range map[string]uint32{"full": 0x1F01FF, "write DAC": rightWriteDAC, "take ownership": rightWriteOwner, "delete": rightDelete, "delete children": rightDeleteChild, "generic all": rightGenericAll} {
		fs = installerACL()
		fs.ACEs = append(fs.ACEs, FolderACE{Mask: mask, SID: sidAuth})
		bad["users get "+name] = fs
	}
	fs = installerACL()
	fs.ACEs = append(fs.ACEs, FolderACE{Mask: rightWriteDAC}) // an object allow with no SID read
	bad["an allow of another shape"] = fs
	for name, fs := range bad {
		if err := CheckFolder("root", fs); err == nil {
			t.Errorf("%s: trusted", name)
		}
	}
}

const sidEveryone = "S-1-1-0"

// installerKey is what the installer is expected to set on its registry record: SYSTEM and Administrators in full, people read.
func installerKey() FolderSecurity {
	return FolderSecurity{Owner: sidSystem, ACEs: []FolderACE{
		{Mask: 0xF003F, SID: sidSystem},
		{Mask: 0xF003F, SID: sidAdmins},
		{Mask: 0x20019, SID: sidUsers}, // KEY_READ
	}}
}

func fakeRead(m map[string]FolderSecurity) func(string) (FolderSecurity, error) {
	return func(p string) (FolderSecurity, error) {
		if fs, ok := m[p]; ok {
			return fs, nil
		}
		return FolderSecurity{}, ErrNotShared
	}
}

func withID(fs FolderSecurity, id string) FolderSecurity {
	fs.ID = id
	return fs
}

func record(root, rootID, casesID string) func() (Anchor, error) {
	return func() (Anchor, error) {
		return Anchor{Root: root, RootID: rootID, CasesID: casesID, Security: installerKey()}, nil
	}
}

func noRecord() (Anchor, error) { return Anchor{}, ErrNoAnchor }

// refused is "present but not trusted": per-user mode with a reason, never shared mode and never silence.
func refused(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil || errors.Is(err, ErrNotShared) {
		t.Errorf("%s: %v", what, err)
	}
}

func TestTrustSharedFolder(t *testing.T) {
	root := filepath.FromSlash("/pd/Unravis")
	cases := filepath.Join(root, "cases")
	installed := map[string]FolderSecurity{root: withID(installerACL(), "0a-10"), cases: withID(installerACL(), "0a-11")}
	anchor := record(root, "0a-10", "0a-11")
	if err := TrustSharedFolder(root, fakeRead(installed), anchor); err != nil {
		t.Fatal(err)
	}
	// The record's path is compared the way Windows compares paths.
	if err := TrustSharedFolder(root, fakeRead(installed), record(strings.ToUpper(root)+string(filepath.Separator), "0a-10", "0a-11")); err != nil {
		t.Fatalf("same folder, other spelling: %v", err)
	}
	// No folder: the registry is never looked at.
	never := func() (Anchor, error) {
		t.Fatal("record read with no folder")
		return Anchor{}, nil
	}
	if err := TrustSharedFolder(root, fakeRead(nil), never); !errors.Is(err, ErrNotShared) {
		t.Fatalf("absent: %v", err)
	}
	refused(t, "no installer record", TrustSharedFolder(root, fakeRead(installed), noRecord))
	refused(t, "no record reader", TrustSharedFolder(root, fakeRead(installed), nil))
	refused(t, "record unreadable", TrustSharedFolder(root, fakeRead(installed), func() (Anchor, error) { return Anchor{}, errors.New("access denied") }))
	refused(t, "record names another folder", TrustSharedFolder(root, fakeRead(installed), record(filepath.FromSlash("/pd/Other"), "0a-10", "0a-11")))
	refused(t, "record with no IDs", TrustSharedFolder(root, fakeRead(map[string]FolderSecurity{root: installerACL(), cases: installerACL()}), record(root, "", "")))
	refused(t, "folder copied or replaced", TrustSharedFolder(root, fakeRead(installed), record(root, "0a-99", "0a-11")))
	refused(t, "cases folder copied or replaced", TrustSharedFolder(root, fakeRead(installed), record(root, "0a-10", "0a-99")))
	refused(t, "no cases folder (one made later would belong to its maker)", TrustSharedFolder(root, fakeRead(map[string]FolderSecurity{root: installed[root]}), anchor))
	personal := withID(installerACL(), "0a-11")
	personal.Owner = sidPerson
	refused(t, "a cases folder a person owns", TrustSharedFolder(root, fakeRead(map[string]FolderSecurity{root: installed[root], cases: personal}), anchor))
	refused(t, "unreadable folder", TrustSharedFolder(root, func(string) (FolderSecurity, error) { return FolderSecurity{}, errors.New("access denied") }, anchor))

	// The record itself must be out of people's reach, or a person could point it at a folder of their own.
	loose := map[string]func(*FolderSecurity){
		"owned by a person": func(k *FolderSecurity) { k.Owner = sidPerson },
		"no access list":    func(k *FolderSecurity) { k.NullDACL = true },
	}
	for name, mask := range map[string]uint32{"set a value": keySetValue, "add a subkey": keyCreateSubKey, "add a link": keyCreateLink, "write generically": rightGenericW, "change permissions": rightWriteDAC, "full control": 0xF003F} {
		mask := mask
		loose["people may "+name] = func(k *FolderSecurity) { k.ACEs = append(k.ACEs, FolderACE{Mask: mask, SID: sidUsers}) }
	}
	for name, spoil := range loose {
		key := installerKey()
		spoil(&key)
		a := func() (Anchor, error) {
			return Anchor{Root: root, RootID: "0a-10", CasesID: "0a-11", Security: key}, nil
		}
		refused(t, "record "+name, TrustSharedFolder(root, fakeRead(installed), a))
	}
}

// The rename trick: a person moves an existing folder under C:\ProgramData that SYSTEM owns but that grants Everyone full control (this
// is common; one is on the PC this was written on) to C:\ProgramData\Unravis, adds a cases folder the same way, and rewrites both access
// lists to look exactly like the installer's. Owner and ACL are then perfect, so only the installer's record can tell: with no record, or
// with the record of the folder the installer really made, these folders carry the wrong IDs and are refused.
func TestRenamedSystemFolderIsNotTrusted(t *testing.T) {
	pd := filepath.FromSlash("/pd")
	root := filepath.Join(pd, "Unravis")
	cases := filepath.Join(root, "cases")
	everyoneFull := FolderSecurity{Owner: sidSystem, ACEs: []FolderACE{{Mask: 0x1F01FF, SID: sidSystem}, {Mask: 0x1F01FF, SID: sidEveryone}}}
	asMoved := map[string]FolderSecurity{root: withID(everyoneFull, "0a-500"), cases: withID(everyoneFull, "0a-501")}
	rewritten := map[string]FolderSecurity{root: withID(installerACL(), "0a-500"), cases: withID(installerACL(), "0a-501")}
	for name, folders := range map[string]map[string]FolderSecurity{"as moved": asMoved, "access lists rewritten": rewritten} {
		for rec, a := range map[string]func() (Anchor, error){"no installer record": noRecord, "record of the real folder": record(root, "0a-10", "0a-11")} {
			refused(t, name+", "+rec, TrustSharedFolder(root, fakeRead(folders), a))
			folders, a := folders, a
			l := Resolve(env(nil), pd, filepath.FromSlash("/uc"), func(p string) error { return TrustSharedFolder(p, fakeRead(folders), a) })
			if l.Shared() || l.SharedRefused == "" {
				t.Errorf("%s, %s: layout %+v", name, rec, l)
			}
		}
	}
	// Even the folder the installer really made is refused once it grants Everyone full control (an administrator loosened it).
	loose := map[string]FolderSecurity{root: withID(everyoneFull, "0a-10"), cases: withID(installerACL(), "0a-11")}
	refused(t, "installed folder granting Everyone full control", TrustSharedFolder(root, fakeRead(loose), record(root, "0a-10", "0a-11")))
	loose = map[string]FolderSecurity{root: withID(installerACL(), "0a-10"), cases: withID(everyoneFull, "0a-11")}
	refused(t, "installed cases folder granting Everyone full control", TrustSharedFolder(root, fakeRead(loose), record(root, "0a-10", "0a-11")))
}
