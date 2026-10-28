package datadir

import (
	"errors"
	"path/filepath"
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
		{Mask: 0x1200A9 | 0x2 | 0x4, SID: sidUsers},                 // read, add files and folders
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

func TestTrustSharedFolder(t *testing.T) {
	root := filepath.FromSlash("/pd/Unravis")
	cases := filepath.Join(root, "cases")
	read := func(m map[string]FolderSecurity) func(string) (FolderSecurity, error) {
		return func(p string) (FolderSecurity, error) {
			if fs, ok := m[p]; ok {
				return fs, nil
			}
			return FolderSecurity{}, ErrNotShared
		}
	}
	good := installerACL()
	if err := TrustSharedFolder(root, read(map[string]FolderSecurity{root: good, cases: good})); err != nil {
		t.Fatal(err)
	}
	if err := TrustSharedFolder(root, read(nil)); !errors.Is(err, ErrNotShared) {
		t.Fatalf("absent: %v", err)
	}
	if err := TrustSharedFolder(root, read(map[string]FolderSecurity{root: good})); err == nil || errors.Is(err, ErrNotShared) {
		t.Fatalf("no cases folder (one made later would belong to its maker): %v", err)
	}
	personal := installerACL()
	personal.Owner = sidPerson
	if err := TrustSharedFolder(root, read(map[string]FolderSecurity{root: good, cases: personal})); err == nil {
		t.Fatal("a cases folder a person owns must not be trusted")
	}
	if err := TrustSharedFolder(root, func(string) (FolderSecurity, error) { return FolderSecurity{}, errors.New("access denied") }); err == nil || errors.Is(err, ErrNotShared) {
		t.Fatalf("unreadable: %v", err)
	}
}
