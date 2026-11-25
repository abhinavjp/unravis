package cmdtree

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/locator"
	"api-audit-explorer/internal/version"
)

// Tests that came out of the review of doctor (PR 227): each one fails when the line it guards is removed.

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAppProgramOnDisk(t *testing.T) {
	root := t.TempDir()
	thin := filepath.Join(root, "a", "bin", "unravis.exe")
	touch(t, thin)
	touch(t, filepath.Join(root, "a", "unravis.exe"))
	dir, app := appProgram(thin)
	if dir != filepath.Join(root, "a") || app.Path != filepath.Join(root, "a", "unravis.exe") || !app.Exists {
		t.Errorf("found: %q %+v", dir, app)
	}
	// a folder or a missing file is not the app program
	touch(t, filepath.Join(root, "b", "bin", "unravis.exe"))
	if err := os.MkdirAll(filepath.Join(root, "b", "unravis.exe"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, app := appProgram(filepath.Join(root, "b", "bin", "unravis.exe")); app.Exists {
		t.Error("a folder is not the app program")
	}
	touch(t, filepath.Join(root, "c", "bin", "unravis.exe"))
	if _, app := appProgram(filepath.Join(root, "c", "bin", "unravis.exe")); app.Exists {
		t.Error("a missing file exists")
	}
	// the command line itself, sitting in the install folder, is not the app program
	if _, app := appProgram(filepath.Join(root, "a", "unravis.exe")); app.Exists {
		t.Error("the command line named itself as the app")
	}
	// a relative path is not looked up against the current folder
	cwd, _ := os.Getwd()
	touch(t, filepath.Join(root, "d", "unravis.exe"))
	if err := os.Chdir(filepath.Join(root, "d")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if _, app := appProgram("bin/unravis.exe"); app.Exists {
		t.Error("a relative path was looked up")
	}
	// a link to the folder of the command line leads to the real install folder, like the attach does
	if err := os.Symlink(filepath.Join(root, "a", "bin"), filepath.Join(root, "link")); err != nil {
		t.Skip("no symbolic links here")
	}
	if dir, app := appProgram(filepath.Join(root, "link", "unravis.exe")); dir != filepath.Join(root, "a") || !app.Exists {
		t.Errorf("link: %q %+v", dir, app)
	}
}

// One rule for the install folder, whatever the case of "bin", for doctor, the attach and the legacy-option message.
func TestInstallDirRule(t *testing.T) {
	for in, want := range map[string]string{
		"/opt/unravis/bin/unravis.exe": "/opt/unravis", "/opt/unravis/BIN/unravis.exe": "/opt/unravis", "/opt/unravis/unravis.exe": "/opt/unravis",
		"/Tools/unravis.exe": "/Tools",
	} {
		// the rule works on the path style of the platform, so the table is written with slashes and converted
		if got := clicore.InstallDir(filepath.FromSlash(in)); got != filepath.FromSlash(want) {
			t.Errorf("%s: %s", in, got)
		}
	}
	// the message of a legacy option names the same file doctor does
	o := newOwner(t, nil)
	d := data(t, o.run("doctor", "--json"))
	r := run(t, nil, "--mcp")
	if !strings.Contains(r.err, d["appExe"].(map[string]any)["path"].(string)) {
		t.Errorf("legacy message and doctor name different files: %q vs %v", r.err, d["appExe"])
	}
}

func TestDoctorTableRows(t *testing.T) {
	o := newOwner(t, nil)
	r := o.run("doctor")
	for _, want := range []string{"app program found", "no", "app owner", "app", "app process", "4242", "app version", "1.2.3", "protocol", "1.0", "app state", "ready", "app answered", "yes"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("table lacks %q:\n%s", want, r.out)
		}
	}
	o.set(func(o *owner) { o.state = "locked" })
	if r := o.run("doctor"); !strings.Contains(r.out, "locked") || strings.Contains(r.out, "ready") {
		t.Errorf("state:\n%s", r.out)
	}
	// not found: no owner rows, and a problem row for a failure
	o2 := newOwner(t, nil)
	o2.set(func(o *owner) { o.helloPID = 7 })
	r = o2.run("doctor")
	if !strings.Contains(r.out, "problem") || !strings.Contains(r.out, "ENDPOINT_UNTRUSTED") || !strings.Contains(r.out, "app answered") || strings.Contains(r.out, "app owner") {
		t.Errorf("untrusted:\n%s", r.out)
	}
	if !strings.Contains(r.out, "no") {
		t.Errorf("answered no:\n%s", r.out)
	}
	m := decode(t, o.run("doctor", "--json").out)["meta"].(map[string]any)
	if m["identifiers"] != "hidden" || m["coverage"] != "complete" {
		t.Errorf("meta: %v", m)
	}
	be := data(t, o.run("doctor", "--json"))["backend"].(map[string]any)["proto"].(map[string]any)
	if be["minor"] != float64(0) {
		t.Errorf("proto: %v", be)
	}
}

func TestDoctorVersionMismatchLine(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old })
	o := newOwner(t, nil) // the app says 1.2.3
	version.Version = "dev"
	if r := o.run("doctor"); strings.Contains(r.out, "Update both") {
		t.Errorf("a dev build is not a mismatch:\n%s", r.out)
	}
	version.Version = "1.2.3"
	if r := o.run("doctor"); strings.Contains(r.out, "Update both") {
		t.Errorf("same version:\n%s", r.out)
	}
	version.Version = "9.9.9"
	if r := o.run("doctor"); !strings.Contains(r.out, "This command line is 9.9.9 and the app is 1.2.3. Update both") {
		t.Errorf("mismatch:\n%s", r.out)
	}
}

func TestDoctorRefusesProfiles(t *testing.T) {
	o := newOwner(t, nil)
	if r := o.run("doctor", "--profile", "cli-x"); r.code != 2 {
		t.Errorf("flag: %+v", r)
	}
	if r := runOn(t, o.attacher(), "", map[string]string{"UNRAVIS_PROFILE": "cli-x"}, "doctor"); r.code != 2 {
		t.Errorf("env: %+v", r)
	}
}

func TestDoctorReportsABusyOrSilentApp(t *testing.T) {
	old := doctorAttachWait
	doctorAttachWait = 100 * time.Millisecond
	t.Cleanup(func() { doctorAttachWait = old })
	o := newOwner(t, nil)
	d := data(t, runOn(t, withDialBlock(o.attacher()), "", nil, "doctor", "--json"))
	if d["locator"].(map[string]any)["state"] != "unknown" || d["backend"].(map[string]any)["error"] != "TIMEOUT" || d["cli"].(map[string]any)["role"] != "cli" {
		t.Errorf("%v", d)
	}
}

func TestDoctorStopIsAnErrorNotAFinding(t *testing.T) {
	o := newOwner(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	r := runCtx(t, ctx, withDialBlock(o.attacher()), "", nil, "doctor", "--json")
	if r.code != 6 || errCode(t, r) != "CANCELED" {
		t.Errorf("%+v", r)
	}
}

// The owner's own words are shown to the person: a free text or a long version is refused, not printed.
func TestHelloTextIsClosed(t *testing.T) {
	for name, mut := range map[string]func(map[string]any){
		"owner text":     func(m map[string]any) { m["owner"] = "x\x1b[2J\r\nTOKEN=abc" },
		"unknown owner":  func(m map[string]any) { m["owner"] = "other" },
		"empty version":  func(m map[string]any) { m["appVersion"] = "" },
		"long version":   func(m map[string]any) { m["appVersion"] = strings.Repeat("1", 65) },
		"escape version": func(m map[string]any) { m["appVersion"] = "1.0\x1b]0;x\a" },
		"space version":  func(m map[string]any) { m["appVersion"] = "1 2" },
	} {
		o := newOwner(t, nil)
		o.serve(200, o.helloBody(mut), nil)
		r := o.run("doctor", "--json")
		d := data(t, r)
		if d["backend"].(map[string]any)["error"] != "ENDPOINT_UNTRUSTED" || strings.Contains(r.out, "TOKEN") {
			t.Errorf("%s: %s", name, r.out)
		}
	}
	for _, ok := range []string{"1.2.3", "dev", "1.2.3-rc.59+abc"} {
		o := newOwner(t, nil)
		o.serve(200, o.helloBody(func(m map[string]any) { m["appVersion"] = ok; m["owner"] = "shell" }), nil)
		if d := data(t, o.run("doctor", "--json")); d["backend"].(map[string]any)["reachable"] != true {
			t.Errorf("%s: %v", ok, d)
		}
	}
}

// Doctor dials the app pipe at most once and sends no operation: nothing leaves the PC.
func TestDoctorNoNetwork(t *testing.T) {
	o := newOwner(t, nil)
	a := o.attacher()
	dials := 0
	inner := a.Dial
	a.Dial = func(ctx context.Context, rec locator.Record) (net.Conn, error) { dials++; return inner(ctx, rec) }
	if r := runOn(t, a, "", nil, "doctor", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if dials != 1 || len(o.opsSeen()) != 0 {
		t.Errorf("dials=%d ops=%v", dials, o.opsSeen())
	}
}

// No state shows a token, a pipe name or the locator's own words.
func TestDoctorNoSecretsInAnyState(t *testing.T) {
	for _, mut := range []func(*owner){func(o *owner) { o.state = "locked" }, func(o *owner) { o.alive = false }, func(o *owner) { o.helloPID = 7 }} {
		o := newOwner(t, nil)
		o.set(mut)
		for _, args := range [][]string{{"doctor", "--json"}, {"doctor"}} {
			r := o.run(args...)
			for _, bad := range []string{ownerToken, "unravis.ag", "unravis.ui", `\\.\`, "0123456789abcdef"} {
				if strings.Contains(r.out, bad) || strings.Contains(r.err, bad) {
					t.Errorf("%v shows %q", args, bad)
				}
			}
		}
	}
}
