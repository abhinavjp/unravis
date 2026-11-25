package localci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StepEnv returns the environment the steps of one run get, built from base (which the caller has already
// reduced to what steps may see):
//
//   - a throw-away profile under homes/<runid>: USERPROFILE, HOME, APPDATA, LOCALAPPDATA, GH_CONFIG_DIR,
//     XDG_*, TEMP and TMP point into it, so the owner's git, gh, npm and Go settings and credential files are
//     not found where tools look first;
//   - git without the owner's setup: a global config of the run (empty), no system config, an empty
//     credential.helper, no prompt, no askpass, no ssh agent;
//   - the persistent caches: GOCACHE, GOMODCACHE, GOPATH and the npm cache come from the store. A run whose
//     code is not all the owner's (untrusted) gets caches of its own, apart from the owner's.
//
// cleanup deletes the profile folder; call it when the run is over.
//
// This stops casual access only. A step still runs as the runner's Windows user and can call the Windows
// Credential Manager API, read files elsewhere in the profile by absolute path, or start a credential helper
// by name. Only a separate Windows user for the runner closes that.
func (s *Store) StepEnv(base []string, runID string, untrusted bool) (env []string, cleanup func(), err error) {
	if !ValidRunID(runID) {
		return nil, nil, fmt.Errorf("%q is not a run id", runID)
	}
	home := filepath.Join(s.HomesPath(), runID)
	_ = os.RemoveAll(home)
	roaming := filepath.Join(home, "AppData", "Roaming")
	local := filepath.Join(home, "AppData", "Local")
	tmp := filepath.Join(home, "tmp")
	for _, d := range []string{roaming, local, tmp, filepath.Join(home, "gh"), filepath.Join(home, "config"), filepath.Join(home, "cache"), filepath.Join(home, "data")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, nil, err
		}
	}
	cleanup = func() { _ = os.RemoveAll(home) }
	gitconfig := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(gitconfig, nil, 0o600); err != nil {
		cleanup()
		return nil, nil, err
	}
	cached, err := s.cacheEnv(nil, untrusted)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	goPath := filepath.Join(s.cachesFor(untrusted), "go-path")
	if err := os.MkdirAll(goPath, 0o750); err != nil {
		cleanup()
		return nil, nil, err
	}
	set := [][2]string{
		{"USERPROFILE", home}, {"HOME", home}, {"APPDATA", roaming}, {"LOCALAPPDATA", local},
		{"GH_CONFIG_DIR", filepath.Join(home, "gh")}, {"XDG_CONFIG_HOME", filepath.Join(home, "config")},
		{"XDG_CACHE_HOME", filepath.Join(home, "cache")}, {"XDG_DATA_HOME", filepath.Join(home, "data")},
		{"TEMP", tmp}, {"TMP", tmp},
		{"GIT_CONFIG_GLOBAL", gitconfig}, {"GIT_CONFIG_NOSYSTEM", "1"},
		{"GIT_CONFIG_COUNT", "1"}, {"GIT_CONFIG_KEY_0", "credential.helper"}, {"GIT_CONFIG_VALUE_0", ""},
		{"GIT_TERMINAL_PROMPT", "0"}, {"GIT_ASKPASS", ""}, {"GCM_INTERACTIVE", "never"},
		{"GOPATH", goPath}, {"GOENV", filepath.Join(home, "go-env")},
		{"npm_config_userconfig", filepath.Join(home, "npmrc")}, {"npm_config_globalconfig", filepath.Join(home, "npmrc-global")},
	}
	set = append(set, cacheKV(cached)...)
	drop := map[string]bool{}
	for _, kv := range set {
		drop[strings.ToUpper(kv[0])] = true
	}
	for _, name := range []string{"HOMEDRIVE", "HOMEPATH", "SSH_AUTH_SOCK", "SSH_ASKPASS", "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
		drop[name] = true
	}
	for _, kv := range base {
		k, _, ok := splitKV(kv)
		if !ok {
			continue
		}
		u := strings.ToUpper(k)
		if drop[u] || strings.HasPrefix(u, "GIT_") || strings.HasPrefix(u, "GCM_") {
			continue
		}
		env = append(env, kv)
	}
	for _, kv := range set {
		env = append(env, kv[0]+"="+kv[1])
	}
	return env, cleanup, nil
}

func cacheKV(env []string) [][2]string {
	var out [][2]string
	for _, kv := range env {
		if k, v, ok := splitKV(kv); ok {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}
