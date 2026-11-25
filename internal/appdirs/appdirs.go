// Package appdirs names the per-user folders of the desktop sidecar that are not data folders. Today that is the run folder, which holds
// the locator file (internal/locator).
package appdirs

import (
	"errors"
	"path/filepath"
)

// Environment variables. EnvRunDir forces the run folder (tests and the CI scripts); AUDIT_CONFIG_DIR is the existing per-user override,
// and when it is set the run folder is its "run" subfolder, so a test that isolates its config also isolates its locator.
const (
	EnvRunDir    = "UNRAVIS_RUN_DIR"
	EnvMCPDir    = "UNRAVIS_MCP_DIR"
	EnvCrashDir  = "UNRAVIS_CRASH_DIR"
	envConfigDir = "AUDIT_CONFIG_DIR"
	envLocalApp  = "LOCALAPPDATA"
)

// RunDir is the folder of the locator file: UNRAVIS_RUN_DIR, else <AUDIT_CONFIG_DIR>\run, else %LOCALAPPDATA%\Unravis\run.
func RunDir(getenv func(string) string) (string, error) {
	if getenv == nil {
		return "", errors.New("appdirs: no environment")
	}
	if d := getenv(EnvRunDir); d != "" {
		return d, nil
	}
	if d := getenv(envConfigDir); d != "" {
		return filepath.Join(d, "run"), nil
	}
	if d := getenv(envLocalApp); d != "" {
		return filepath.Join(d, "Unravis", "run"), nil
	}
	return "", errors.New("appdirs: %LOCALAPPDATA% is not set")
}

// MCPDir is the folder of the MCP grant store (mcp.db, spec 03 D10): UNRAVIS_MCP_DIR, else <AUDIT_CONFIG_DIR>\mcp, else
// %LOCALAPPDATA%\Unravis\mcp. Setup and uninstall leave it alone (user data).
func MCPDir(getenv func(string) string) (string, error) {
	if getenv == nil {
		return "", errors.New("appdirs: no environment")
	}
	if d := getenv(EnvMCPDir); d != "" {
		return d, nil
	}
	if d := getenv(envConfigDir); d != "" {
		return filepath.Join(d, "mcp"), nil
	}
	if d := getenv(envLocalApp); d != "" {
		return filepath.Join(d, "Unravis", "mcp"), nil
	}
	return "", errors.New("appdirs: %LOCALAPPDATA% is not set")
}

// CrashDir is the folder of the local crash files (spec 01 D11, BL-315): UNRAVIS_CRASH_DIR, else <AUDIT_CONFIG_DIR>\crashes, else
// %LOCALAPPDATA%\Unravis\crashes. Nothing in it is ever sent anywhere.
func CrashDir(getenv func(string) string) (string, error) {
	if getenv == nil {
		return "", errors.New("appdirs: no environment")
	}
	if d := getenv(EnvCrashDir); d != "" {
		return d, nil
	}
	if d := getenv(envConfigDir); d != "" {
		return filepath.Join(d, "crashes"), nil
	}
	if d := getenv(envLocalApp); d != "" {
		return filepath.Join(d, "Unravis", "crashes"), nil
	}
	return "", errors.New("appdirs: %LOCALAPPDATA% is not set")
}
