package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/setup"
)

func TestExpectFlagsShape(t *testing.T) {
	good := strings.Repeat("ab", 32)
	cases := []struct {
		name string
		args []string
		ok   bool
	}{
		{"upgrade with both", []string{"--upgrade", "--expect-sha256", good, "--expect-version", "1.2.3"}, true},
		{"rc version", []string{"--upgrade", "--expect-sha256", good, "--expect-version", "1.2.3-rc.4"}, true},
		{"neither", []string{"--upgrade"}, true},
		{"only hash", []string{"--upgrade", "--expect-sha256", good}, false},
		{"only version", []string{"--upgrade", "--expect-version", "1.2.3"}, false},
		{"upper-case hash", []string{"--upgrade", "--expect-sha256", strings.ToUpper(good), "--expect-version", "1.2.3"}, false},
		{"short hash", []string{"--upgrade", "--expect-sha256", good[:60], "--expect-version", "1.2.3"}, false},
		{"v prefix", []string{"--upgrade", "--expect-sha256", good, "--expect-version", "v1.2.3"}, false},
		{"junk version", []string{"--upgrade", "--expect-sha256", good, "--expect-version", "latest"}, false},
		{"not an upgrade", []string{"--repair", "--expect-sha256", good, "--expect-version", "1.2.3"}, false},
		{"plain install", []string{"--expect-sha256", good, "--expect-version", "1.2.3"}, false},
		{"worker may carry them", []string{"--worker", "--pipe", `\\.\pipe\x`, "--parent", "7", "--expect-sha256", good, "--expect-version", "1.2.3"}, true},
	}
	for _, c := range cases {
		_, err := parseFlags(c.args, env{}, &bytes.Buffer{})
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestExpectArgsPassedToWorker(t *testing.T) {
	o := options{expectSHA: strings.Repeat("a", 64), expectVersion: "1.2.3"}
	got := strings.Join(o.expectArgs(), " ")
	if got != "--expect-sha256 "+strings.Repeat("a", 64)+" --expect-version 1.2.3" {
		t.Errorf("%q", got)
	}
	if (options{}).expectArgs() != nil {
		t.Error("args without expectations")
	}
}

func TestVerifyExpect(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "unravis-setup.exe")
	body := []byte("the installer")
	if err := os.WriteFile(exe, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	good := hex.EncodeToString(sum[:])
	e := env{setupExe: func() (string, error) { return exe, nil }}
	ok := options{expectSHA: good, expectVersion: "1.2.3"}
	check := func(o options, payload string) error {
		release, err := verifyExpect(o, e, payload)
		release()
		return err
	}
	if err := check(ok, "1.2.3"); err != nil {
		t.Fatalf("good: %v", err)
	}
	if err := check(ok, "v1.2.3"); err != nil {
		t.Fatalf("v prefix on the payload: %v", err)
	}
	if err := check(options{}, ""); err != nil {
		t.Fatalf("no expectation must pass: %v", err)
	}
	if err := check(ok, "1.2.4"); err == nil {
		t.Error("wrong payload version accepted")
	}
	if err := check(ok, ""); err == nil {
		t.Error("missing payload accepted")
	}
	if err := os.WriteFile(exe, []byte("another installer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := check(ok, "1.2.3"); err == nil {
		t.Error("a swapped setup file was accepted")
	}
	os.Remove(exe)
	if err := check(ok, "1.2.3"); err == nil {
		t.Error("an unreadable setup file was accepted")
	}
}

// The expectation is checked in every mode, before anything is changed.
func TestExpectIsEnforcedInSilentAndWindowMode(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	exe, _ := rg.env.setupExe()
	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	good := hex.EncodeToString(sum[:])
	bad := strings.Repeat("0", 64)
	cfg := rg.config(`{"scope":"user"}`)
	fail := setup.OutcomePreflightFailed.ExitCode()

	// silent, wrong file hash
	if got := run([]string{"--silent", "--upgrade", "--config", cfg, "--expect-sha256", bad, "--expect-version", "1.0.0"}, rg.env); got != fail {
		t.Fatalf("silent, wrong hash: exit %d\n%s", got, rg.out)
	}
	if !strings.Contains(rg.out.String(), "not the one the update check verified") {
		t.Errorf("no reason given: %s", rg.out)
	}
	// silent, right hash but the setup installs another version
	rg.out.b.Reset()
	if got := run([]string{"--silent", "--upgrade", "--config", cfg, "--expect-sha256", good, "--expect-version", "9.9.9"}, rg.env); got != fail {
		t.Fatalf("silent, wrong version: exit %d\n%s", got, rg.out)
	}
	if !strings.Contains(rg.out.String(), "announced") {
		t.Errorf("no reason given: %s", rg.out)
	}
	// silent, right file and version: the check passes (what happens next is the normal upgrade)
	rg.out.b.Reset()
	run([]string{"--silent", "--upgrade", "--config", cfg, "--expect-sha256", good, "--expect-version", "1.0.0"}, rg.env)
	if strings.Contains(rg.out.String(), "not the one the update check verified") || strings.Contains(rg.out.String(), "announced") {
		t.Errorf("a matching setup was refused: %s", rg.out)
	}
	// window mode stops before it serves anything
	if got := run([]string{"--upgrade", "--no-browser", "--expect-sha256", bad, "--expect-version", "1.0.0"}, rg.env); got != fail {
		t.Fatalf("window, wrong hash: exit %d", got)
	}
	if strings.Contains(rg.out.String(), "listening at") {
		t.Error("the window was served although the setup file did not match")
	}
	// the worker refuses too, before it dials the window (the rig fails the test if it dials)
	rg.f.Mach.Admin = true
	if got := run([]string{"--worker", "--pipe", "x", "--parent", "7", "--expect-sha256", bad, "--expect-version", "1.0.0"}, rg.env); got != fail {
		t.Fatalf("worker, wrong hash: exit %d", got)
	}
}

// The elevated worker is started with the same expectation, so it makes the same check.
func TestExpectIsPassedToTheWorker(t *testing.T) {
	rg := newRig(t)
	rg.f.Mach.Admin = false
	withDesktop(rg)
	exe, _ := rg.env.setupExe()
	body, _ := os.ReadFile(exe)
	sum := sha256.Sum256(body)
	good := hex.EncodeToString(sum[:])
	run([]string{"--silent", "--upgrade", "--config", rg.config(`{"scope":"machine"}`), "--expect-sha256", good, "--expect-version", "1.0.0"}, rg.env)
	if rg.elevated == 0 && rg.lastExtra == nil {
		t.Skip("this flow did not reach the administrator step on this system")
	}
	if strings.Join(rg.lastExtra, " ") != "--expect-sha256 "+good+" --expect-version 1.0.0" {
		t.Errorf("the worker was started with %v", rg.lastExtra)
	}
}
