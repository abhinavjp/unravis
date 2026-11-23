package mcp

import (
	"bytes"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// childEnv names the port of the dashboard the helper process below serves for.
const childEnv = "UNRAVIS_SPIKE_CHILD_PORT"

// TestSDKChildProcess is not a test: it is the SDK loop as a real process (the test binary run again), reading the real stdin and
// writing the real stdout, for TestSDKStdioProcess. It does nothing unless the parent set childEnv.
func TestSDKChildProcess(t *testing.T) {
	port := os.Getenv(childEnv)
	if port == "" {
		t.Skip("helper process")
	}
	n, _ := strconv.Atoi(port)
	b, err := New(n, WithToken(testSessionToken))
	if err != nil {
		os.Exit(2)
	}
	if err := b.serveSDK(t.Context(), os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// TestSDKStdioProcess runs the loop as a separate process over real OS pipes, a test client on the other end, against the real dashboard
// handler (real HTTP, real session proof, demo repository): both lifecycles, every one of the ten tools. With UNRAVIS_MCP_BINARY set to a
// built unravis binary it runs that binary (--mcp) the same way: the stdio server a host launches.
func TestSDKStdioProcess(t *testing.T) {
	srv := demoDashboard(t)
	u, _ := url.Parse(srv.URL)
	port := u.Port()
	type target struct {
		name string
		cmd  func() *exec.Cmd
	}
	targets := []target{{"sdk loop, test binary re-run", func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSDKChildProcess$")
		cmd.Env = append(os.Environ(), childEnv+"="+port)
		return cmd
	}}}
	if bin := os.Getenv("UNRAVIS_MCP_BINARY"); bin != "" {
		targets = append(targets, target{"binary " + bin, func() *exec.Cmd {
			cmd := exec.Command(bin, "--mcp", "--port", port)
			cmd.Env = append(os.Environ(), "AUDIT_SESSION_TOKEN="+testSessionToken)
			return cmd
		}})
	}
	for _, tg := range targets {
		for _, stateless := range []bool{false, true} {
			name := tg.name + " handshake"
			if stateless {
				name = tg.name + " stateless"
			}
			t.Run(name, func(t *testing.T) {
				if stateless && tg.name != targets[0].name && os.Getenv("UNRAVIS_MCP_BINARY_NO_STATELESS") != "" {
					t.Skip("this binary predates the 2026-07-28 lifecycle")
				}
				cmd := tg.cmd()
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				stdin, _ := cmd.StdinPipe()
				stdout, _ := cmd.StdoutPipe()
				started := time.Now()
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				w := newWire(t, stdin, stdout)
				w.stateless = stateless
				exited := make(chan error, 1)
				go func() { exited <- cmd.Wait() }()
				t.Cleanup(func() { _ = cmd.Process.Kill() })
				// Probe on a fresh process: how long from launch to the first answer.
				if stateless {
					w.request("server/discover", "")
				} else {
					w.handshake()
				}
				t.Logf("first answer %v after launch", time.Since(started).Round(time.Millisecond))
				answers := tenCalls(t, w.call)
				if len(answers) != 10 {
					t.Fatalf("%d of the ten tools answered", len(answers))
				}
				stdin.Close()
				select {
				case err := <-exited:
					if err != nil {
						t.Fatalf("process exit on EOF: %v (stderr %q)", err, stderr.String())
					}
				case <-time.After(10 * time.Second):
					t.Fatal("process did not exit when stdin closed")
				}
				if stderr.Len() > 0 && tg.name != targets[0].name {
					t.Logf("stderr: %q", stderr.String())
				}
			})
		}
	}
}
