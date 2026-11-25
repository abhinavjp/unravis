package localci

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		if k, v, ok := splitKV(kv); ok {
			m[strings.ToUpper(k)] = v
		}
	}
	return m
}

func TestStepEnvGivesTheRunAProfileOfItsOwn(t *testing.T) {
	s := newStore(t)
	base := []string{"PATH=/bin", "HOME=/home/owner", "USERPROFILE=C:\\Users\\owner", "APPDATA=C:\\Users\\owner\\AppData\\Roaming",
		"LOCALAPPDATA=C:\\Users\\owner\\AppData\\Local", "GH_CONFIG_DIR=/home/owner/.config/gh", "XDG_CONFIG_HOME=/home/owner/.config",
		"GIT_CONFIG_GLOBAL=/home/owner/.gitconfig", "GIT_ASKPASS=/usr/bin/askpass", "SSH_AUTH_SOCK=/tmp/agent", "GH_TOKEN=ghp_owner",
		"GOPATH=/home/owner/go", "GOENV=/home/owner/go.env", "npm_config_userconfig=/home/owner/.npmrc", "GOCACHE=/home/owner/gocache", "KEEP=yes"}
	env, cleanup, err := s.StepEnv(base, "run1", false)
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(env)
	home := filepath.Join(s.HomesPath(), "run1")
	for _, k := range []string{"HOME", "USERPROFILE", "GH_CONFIG_DIR", "XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA", "GIT_CONFIG_GLOBAL", "GOENV", "NPM_CONFIG_USERCONFIG"} {
		if !pathInside(home, m[k]) {
			t.Errorf("%s = %q, want a folder of the run's profile %s", k, m[k], home)
		}
	}
	// TEMP and TMP are a short folder under the owner's temp folder, removed with the run
	if m["TEMP"] != m["TMP"] || m["TEMP"] == "" || pathInside(home, m["TEMP"]) || !exists(m["TEMP"]) {
		t.Errorf("TEMP %q TMP %q must be one short folder outside the profile", m["TEMP"], m["TMP"])
	}
	if !pathInside(s.CachesPath(), m["GOPATH"]) || !pathInside(s.CachesPath(), m["GOCACHE"]) {
		t.Errorf("GOPATH %q GOCACHE %q must be inside the store's caches", m["GOPATH"], m["GOCACHE"])
	}
	for k, want := range map[string]string{"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "credential.helper", "GIT_CONFIG_VALUE_0": "", "GIT_TERMINAL_PROMPT": "0", "GIT_ASKPASS": "", "KEEP": "yes", "GH_TOKEN": NoTokenPlaceholder, "GITHUB_TOKEN": NoTokenPlaceholder, "GH_PROMPT_DISABLED": "1"} {
		if got, ok := m[k]; !ok || got != want {
			t.Errorf("%s = %q (set %v), want %q", k, got, ok, want)
		}
	}
	for _, k := range []string{"SSH_AUTH_SOCK"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s must be removed", k)
		}
	}
	// no variable is set twice: a duplicate lets the first (the owner's) value win on some systems
	seen := map[string]bool{}
	for _, kv := range env {
		k, _, _ := splitKV(kv)
		if seen[strings.ToUpper(k)] {
			t.Errorf("%s is set twice", k)
		}
		seen[strings.ToUpper(k)] = true
	}
	if !exists(home) {
		t.Fatal("the profile folder is missing")
	}
	tempDir := m["TEMP"]
	cleanup()
	if exists(home) || exists(tempDir) {
		t.Fatal("cleanup must delete the profile and the run's temp folder")
	}
	// an untrusted run gets caches of its own
	env, cleanup, err = s.StepEnv(base, "run2", true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	m = envMap(env)
	if !pathInside(s.UntrustedCachesPath(), m["GOCACHE"]) || !pathInside(s.UntrustedCachesPath(), m["GOPATH"]) || pathInside(s.CachesPath(), m["GOMODCACHE"]) {
		t.Fatalf("untrusted caches: %v", m)
	}
	if _, _, err := s.StepEnv(base, "../x", false); err == nil {
		t.Fatal("a bad run id must be refused")
	}
}

// A step that asks git for the owner's config, credentials or helper finds nothing.
func TestStepEnvHidesTheOwnersGitSetup(t *testing.T) {
	needBash(t)
	hermeticGit(t)
	owner := t.TempDir()
	writeT(t, owner, ".gitconfig", "[user]\n\tname = The Owner\n[credential]\n\thelper = store\n")
	writeT(t, owner, ".git-credentials", "https://owner:secret-token@github.com\n")
	s := newStore(t)
	base := append(os.Environ(), "HOME="+owner, "USERPROFILE="+owner, "GIT_CONFIG_GLOBAL="+filepath.Join(owner, ".gitconfig"))
	env, cleanup, err := s.StepEnv(base, "run1", false)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	run := func(script string) string {
		cmd := exec.Command("bash", "-c", script) //nolint:gosec // fixed shell, scripts written by the test
		cmd.Env = env
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	if out := run("git config --global --list; git config --get user.name; git config --get-all credential.helper"); strings.Contains(out, "Owner") || strings.Contains(out, "store") {
		t.Fatalf("the owner's git config is visible to a step:\n%s", out)
	}
	if out := run(`printf 'protocol=https\nhost=github.com\n\n' | git credential fill 2>&1`); strings.Contains(out, "secret-token") {
		t.Fatalf("git credential fill returned the owner's credential:\n%s", out)
	}
	if out := run(`echo "$HOME|$USERPROFILE"`); strings.Contains(out, owner) {
		t.Fatalf("HOME is still the owner's: %s", out)
	}
}

// A run's profile has the folders Windows names as usual ones, so a test or tool that asks Windows for one still gets an answer.
func TestStepEnvProfileHasTheUsualWindowsFolders(t *testing.T) {
	s := newStore(t)
	_, cleanup, err := s.StepEnv(nil, "run1", false)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	home := filepath.Join(s.HomesPath(), "run1")
	for _, rel := range []string{"Desktop", filepath.Join("AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs", "Startup")} {
		if fi, err := os.Stat(filepath.Join(home, rel)); err != nil || !fi.IsDir() {
			t.Errorf("profile has no %s folder: %v", rel, err)
		}
	}
}
