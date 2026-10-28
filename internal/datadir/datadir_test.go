package datadir

import (
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestResolveLayouts(t *testing.T) {
	pd, uc := filepath.FromSlash("/pd"), filepath.FromSlash("/uc")
	shared := filepath.Join(pd, "Unravis")
	yes := func(p string) bool { return p == shared }
	no := func(string) bool { return false }

	l := Resolve(env(nil), pd, uc, no)
	if l.Mode != ModeUser || l.Base != filepath.Join(uc, "APIAuditExplorer") || l.Cases != "" || l.Pipe != DefaultPipe {
		t.Fatalf("no shared folder: %+v", l)
	}
	l = Resolve(env(nil), pd, uc, yes)
	if !l.Shared() || l.Root != shared || l.Cases != filepath.Join(shared, "cases") || l.KeeperDir != filepath.Join(shared, "keeper") || l.UserDir != filepath.Join(uc, "APIAuditExplorer") {
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
