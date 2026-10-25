//go:build windows

package discover

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tempRoot returns a temp folder whose path passes the shape check. A user
// profile with an 8.3 name (USER~1) cannot be used, so the test skips there.
func tempRoot(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Skipf("temp dir: %v", err)
	}
	if _, err := ValidateAbsPath(d); err != nil {
		t.Skip("temp dir path shape is not usable on this machine")
	}
	return d
}

func TestSystemEnvRealFS(t *testing.T) {
	env, err := NewSystemEnv()
	if err != nil {
		t.Fatal(err)
	}
	root := tempRoot(t)
	logs := filepath.Join(root, "web-a")
	if err := os.Mkdir(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(logs, "u_ex010911.log")
	if err := os.WriteFile(file, []byte("#Software: Microsoft Internet Information Services 10.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := env.FS.ReadDir(logs)
	if err != nil || len(list) != 1 || list[0].Name != "u_ex010911.log" || list[0].IsDir {
		t.Fatalf("ReadDir %+v %v", list, err)
	}
	st, err := env.FS.Stat(file)
	if err != nil || st.FileID == 0 || st.Size == 0 {
		t.Fatalf("Stat %+v %v", st, err)
	}
	head, err := env.FS.Head(file, 9)
	if err != nil || string(head) != "#Software" {
		t.Fatalf("Head %q %v", head, err)
	}
	canon, reparse, err := env.FS.Final(filepath.Join(root, "WEB-A", "U_EX010911.LOG"))
	if err != nil || reparse || canon != file {
		t.Fatalf("Final %q reparse=%v err=%v", canon, reparse, err)
	}
	if _, err := env.FS.Lstat(filepath.Join(root, "missing")); !IsNotFound(err) {
		t.Fatalf("missing: %v", err)
	}
	// Shape refusals reach the real implementation too.
	if _, err := env.FS.Lstat(`\\web-a.example.test\share\x`); ReasonFromError(err) != ReasonUnsafePath {
		t.Fatalf("UNC: %v", err)
	}
	if _, err := env.FS.Head(file+":stream", 4); ReasonFromError(err) != ReasonUnsafePath {
		t.Fatalf("ADS: %v", err)
	}
}

func TestSystemEnvJunctionIsNotFollowed(t *testing.T) {
	env, err := NewSystemEnv()
	if err != nil {
		t.Fatal(err)
	}
	root := tempRoot(t)
	target := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "a.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v %s", err, out)
	}
	li, err := env.FS.Lstat(link)
	if err != nil || !li.Reparse {
		t.Fatalf("Lstat junction %+v %v", li, err)
	}
	if _, rp, err := env.FS.Final(filepath.Join(link, "a.log")); err == nil && !rp {
		t.Fatal("Final did not report the junction")
	}
	if _, err := env.FS.ReadDir(link); err == nil {
		t.Fatal("ReadDir followed a junction")
	}
	if _, err := env.FS.Head(filepath.Join(link, "a.log"), 1); err == nil {
		t.Fatal("Head read through a junction")
	}
}

func TestSystemEnvMachineAndRegistry(t *testing.T) {
	env, err := NewSystemEnv()
	if err != nil {
		t.Fatal(err)
	}
	if env.Machine.LogicalCores() < 1 || len(env.Machine.Names()) == 0 {
		t.Fatalf("machine: cores=%d names=%v", env.Machine.LogicalCores(), env.Machine.Names())
	}
	if _, err := env.Registry.String(`HKLM\SAM\SAM`, "Anything"); err == nil || ReasonFromError(err) != ReasonUnreadable {
		t.Fatalf("registry outside the allowlist: %v", err)
	}
	if _, err := env.Procs.List(); err != nil {
		t.Fatalf("process list: %v", err)
	}
}
