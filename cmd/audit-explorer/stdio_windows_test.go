//go:build windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"debug/pe"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// peSubsystemWindowsGUI is IMAGE_SUBSYSTEM_WINDOWS_GUI.
const peSubsystemWindowsGUI = 2

// guiEnv is the child's environment: the current one plus a private config
// folder, so the user's real Unravis data and logs are never touched, and no
// inherited session token.
func guiEnv(configDir string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		name := strings.ToUpper(kv[:max(0, strings.IndexByte(kv, '='))])
		if name == "AUDIT_CONFIG_DIR" || name == "AUDIT_SESSION_TOKEN" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "AUDIT_CONFIG_DIR="+configDir)
}

// buildGuiExe builds the app with the release flag -H=windowsgui into a
// directory the test framework removes, and returns the exe path.
func buildGuiExe(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the app")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH; run with the repo toolchain first on PATH")
	}
	exe := filepath.Join(t.TempDir(), "unravis-gui.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "build",
		"-ldflags", "-H=windowsgui -X api-audit-explorer/internal/version.Version=test-gui",
		"-o", exe, ".")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return exe
}

func TestGuiBuildHasTheWindowsSubsystem(t *testing.T) {
	exe := buildGuiExe(t)
	f, err := pe.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	hdr, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		t.Fatalf("optional header is %T, want *pe.OptionalHeader64", f.OptionalHeader)
	}
	if hdr.Subsystem != peSubsystemWindowsGUI {
		t.Fatalf("subsystem %d, want %d (windows GUI)", hdr.Subsystem, peSubsystemWindowsGUI)
	}
}

func TestGuiBuildVersionGoesToAPipe(t *testing.T) {
	exe := buildGuiExe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, "--version")
	cmd.Env = guiEnv(t.TempDir())
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("--version: %v (output %q)", err, out.String())
	}
	if !strings.Contains(out.String(), "test-gui") {
		t.Fatalf("--version wrote %q, want it to contain test-gui", out.String())
	}
}

func TestGuiBuildMcpStdioRoundTrip(t *testing.T) {
	exe := buildGuiExe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--mcp")
	cmd.Env = guiEnv(t.TempDir())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := false
	defer func() {
		if !done {
			cancel()
			_ = cmd.Wait()
		}
	}()

	const request = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`
	if _, err := stdin.Write([]byte(request + "\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the initialize reply: %v (got %q)", err, line)
	}
	var reply struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &reply); err != nil {
		t.Fatalf("reply is not JSON: %v (%q)", err, line)
	}
	if reply.Result.ProtocolVersion != "2025-11-25" {
		t.Fatalf("protocolVersion %q, want 2025-11-25 (reply %q)", reply.Result.ProtocolVersion, line)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	done = true
	if err := cmd.Wait(); err != nil {
		t.Fatalf("--mcp did not exit 0 when stdin closed: %v", err)
	}
}
