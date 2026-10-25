//go:build windows

package discover

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
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

// The handle check after the open refuses a file that is not the one approved
// (security review F5): a different file at the expected path, or a path that
// differs from the handle's own.
func TestVerifyOpenedRefusesASwappedFile(t *testing.T) {
	root := tempRoot(t)
	a := filepath.Join(root, "a.log")
	b := filepath.Join(root, "b.log")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("2001-10-25 SENTINEL-7f3a-not-read\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fsys := winFS{}
	want, err := fsys.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	open := func(p string) (windows.Handle, *windows.ByHandleFileInformation) {
		t.Helper()
		u, _ := windows.UTF16PtrFromString(p)
		h, err := windows.CreateFile(u, windows.GENERIC_READ, shareAll, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { windows.CloseHandle(h) })
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(h, &info); err != nil {
			t.Fatal(err)
		}
		return h, &info
	}

	ha, ia := open(a)
	if err := verifyOpened(ha, ia, a, want); err != nil {
		t.Fatalf("the approved file was refused: %v", err)
	}
	hb, ib := open(b)
	if err := verifyOpened(hb, ib, a, want); ReasonFromError(err) != ReasonUnsafePath {
		t.Fatalf("another file under the approved path: %v", err)
	}
	// Same final path but another identity (the file was replaced).
	other := want
	other.FileID++
	if err := verifyOpened(ha, ia, a, other); ReasonFromError(err) != ReasonUnsafePath {
		t.Fatalf("a changed file identity: %v", err)
	}
	// Head still reads an ordinary file.
	if got, err := fsys.Head(a, 4); err != nil || string(got) != "2001" {
		t.Fatalf("Head %q %v", got, err)
	}
}

// ReadDir checks the leaf only (see its comment), so a folder below a junction
// lists. What keeps a scan out is the check callers run first: Final reports the
// junction, and the applog detector, which does that check, refuses the folder.
func TestJunctionParentIsRefusedWhereReadDirCallersRelyOnIt(t *testing.T) {
	env, err := NewSystemEnv()
	if err != nil {
		t.Fatal(err)
	}
	root := tempRoot(t)
	target := filepath.Join(root, "real")
	sub := filepath.Join(target, "sub")
	link := filepath.Join(root, "link")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `<configuration><log4net><appender type="FileAppender"><file value="logs\app.log"/></appender></log4net></configuration>`
	if err := os.WriteFile(filepath.Join(sub, "web.config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction here: %v %s", err, out)
	}
	through := filepath.Join(link, "sub")

	canon, reparse, err := env.FS.Final(through)
	if err != nil || !reparse || strings.EqualFold(canon, through) {
		t.Fatalf("Final of a folder below a junction: %q reparse=%v err=%v", canon, reparse, err)
	}
	fs, gaps := NewAppLog(nil, through).Detect(context.Background(), env)
	if len(fs) != 0 {
		t.Fatalf("a scan went through the junction: %v", fs)
	}
	var refused bool
	for _, g := range gaps {
		if g.Reason == ReasonUnsafePath {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("want an unsafe-path gap, got %v", gaps)
	}
	// The same folder by its real path is listed normally.
	if _, err := env.FS.ReadDir(sub); err != nil {
		t.Fatalf("ReadDir of the real folder: %v", err)
	}
}
