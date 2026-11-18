package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/blackbox"
)

func fileSum(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// SEC-05: the settings file that was approved is the one used.
func TestCheckConfigHash(t *testing.T) {
	cfg := blackbox.Default()
	cfg.Name = "box-1"
	p := submitFile(t, cfg)
	if err := checkConfigHash(p, fileSum(t, p)); err != nil {
		t.Fatal(err)
	}
	if err := checkConfigHash(p, strings.ToUpper(fileSum(t, p))); err != nil {
		t.Fatalf("upper-case hash: %v", err)
	}
	err := checkConfigHash(p, strings.Repeat("0", 64))
	if !errors.Is(err, blackbox.ErrInvalidConfig) {
		t.Fatalf("a changed file was accepted: %v", err)
	}
	if err := checkConfigHash(filepath.Join(t.TempDir(), "gone.json"), "00"); !errors.Is(err, blackbox.ErrInvalidConfig) {
		t.Fatalf("a missing file: %v", err)
	}
}

func TestInstallAndApplyRefuseASettingsFileThatChanged(t *testing.T) {
	r := newRig(t)
	cfg := blackbox.Default()
	cfg.Name = "box-1"
	cfg.Folder = filepath.Join(r.dir, "rec")
	file := submitFile(t, cfg)
	sum := fileSum(t, file)
	// The file is rewritten after Unravis hashed it.
	cfg.Bind = "0.0.0.0"
	b, _ := cfg.Marshal()
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	c, out, _ := r.do("install", "--json", "--config", file, "--config-sha256", sum)
	if c == 0 || !strings.Contains(out, `"reason":"invalid-config"`) {
		t.Fatalf("%d %s", c, out)
	}
	if r.svc.state != blackbox.SvcNotInstalled || len(r.svc.calls) != 0 {
		t.Fatalf("something was changed: %v", r.svc.calls)
	}
	// The approved file goes through.
	if c, out, errs := r.do("install", "--json", "--config", file, "--config-sha256", fileSum(t, file)); c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	cfg.KeepDays++
	b, _ = cfg.Marshal()
	changed := submitFile(t, cfg)
	if c, out, _ := r.do("apply", "--json", "--config", changed, "--config-sha256", strings.Repeat("a", 64)); c == 0 || !strings.Contains(out, `"reason":"invalid-config"`) {
		t.Fatalf("apply with a wrong hash: %d %s", c, out)
	}
}

// SEC-02: an elevated helper cannot print back to the app, so it writes its
// answer to the file it is given, and nothing goes to the console.
func TestAnswerFileCarriesTheAnswer(t *testing.T) {
	r := newRig(t)
	cfg := blackbox.Default()
	cfg.Name = "box-1"
	cfg.Folder = filepath.Join(r.dir, "rec")
	file := submitFile(t, cfg)
	out := preMadeAnswerFile(t)
	c, stdout, errs := r.do("install", "--json", "--config", file, "--config-sha256", fileSum(t, file), "--answer-file", out)
	if c != 0 || strings.TrimSpace(stdout) != "" {
		t.Fatalf("%d stdout=%q stderr=%q", c, stdout, errs)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil || got["ok"] != true {
		t.Fatalf("%s %v", b, err)
	}
	if k, _ := got["readKey"].(string); !strings.HasPrefix(k, "ubk-") {
		t.Fatalf("no key in the answer file: %s", b)
	}
	// A failure is written there too.
	out2 := preMadeAnswerFile(t)
	r.env.admin = false
	c, stdout, _ = r.do("start", "--json", "--answer-file", out2)
	b2, err := os.ReadFile(out2)
	if c == 0 || err != nil || !strings.Contains(string(b2), `"ok":false`) || strings.TrimSpace(stdout) != "" {
		t.Fatalf("%d %q %s %v", c, stdout, b2, err)
	}
}

// preMadeAnswerFile is the empty file Unravis makes before the prompt.
func preMadeAnswerFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "answer.json")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// SEC-FIX-04: the elevated helper never creates the answer file.
func TestAnswerFileMustAlreadyExist(t *testing.T) {
	r := newRig(t)
	out := filepath.Join(t.TempDir(), "answer.json")
	c, _, errs := r.do("start", "--json", "--answer-file", out)
	if c == 0 || !strings.Contains(errs, "cannot write the answer file") {
		t.Fatalf("%d %q", c, errs)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("the helper created the answer file")
	}
	if len(r.svc.calls) != 0 {
		t.Fatalf("something ran: %v", r.svc.calls)
	}
}

// SEC-FIX-08: an elevated install or apply without the checked hash is refused,
// and the file is read once: what is hashed is what is used.
func TestElevatedInstallAndApplyNeedTheCheckedHash(t *testing.T) {
	r := newRig(t)
	cfg := blackbox.Default()
	cfg.Name = "box-1"
	cfg.Folder = filepath.Join(r.dir, "rec")
	file := submitFile(t, cfg)
	for _, cmd := range []string{"install", "apply"} {
		out := preMadeAnswerFile(t)
		c, _, _ := r.do(cmd, "--json", "--config", file, "--answer-file", out)
		b, _ := os.ReadFile(out)
		if c == 0 || !strings.Contains(string(b), `"reason":"invalid-config"`) {
			t.Fatalf("%s without a hash: %d %s", cmd, c, b)
		}
	}
	if len(r.svc.calls) != 0 {
		t.Fatalf("something was changed: %v", r.svc.calls)
	}
}

// SEC-15: an elevated apply or remove never takes settings from beside the
// program, where an ordinary user may be able to write.
func TestApplyAndRemoveNeverReadSettingsFromBesideTheProgram(t *testing.T) {
	r := newRig(t)
	planted := blackbox.Default()
	planted.Name = "planted"
	planted.Folder = filepath.Join(r.dir, "attacker")
	b, _ := planted.Marshal()
	if err := os.WriteFile(filepath.Join(r.dir, defaultConfigName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"apply", "remove"} {
		c, out, _ := r.do(cmd, "--json")
		if c == 0 {
			t.Fatalf("%s took the planted settings: %s", cmd, out)
		}
		if strings.Contains(out, "planted") || strings.Contains(out, "attacker") {
			t.Fatalf("%s used the planted settings: %s", cmd, out)
		}
	}
	if len(r.svc.calls) != 0 {
		t.Fatalf("the service was touched: %v", r.svc.calls)
	}
}

// SEC-10: an install that listens beyond this PC says so in its answer.
func TestInstallAnswerCarriesTheBindWarning(t *testing.T) {
	r := newRig(t)
	_, loop, _ := r.do(append([]string{"install", "--json"}, r.args...)...)
	if strings.Contains(loop, "bindWarning") {
		t.Fatalf("a loopback install warned: %s", loop)
	}
	r2 := newRig(t)
	c, out, errs := r2.do(append([]string{"install", "--json", "--bind", "0.0.0.0"}, r2.args...)...)
	if c != 0 {
		t.Fatalf("%d %s %s", c, out, errs)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if w, _ := got["bindWarning"].(string); !strings.Contains(w, "Firewall") {
		t.Fatalf("no warning: %s", out)
	}
}
