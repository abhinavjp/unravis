package mcp

import (
	"bytes"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// childEnv names the port of the dashboard the helper process below serves for.
const (
	childEnv      = "UNRAVIS_SPIKE_CHILD_PORT"
	childIdentEnv = "UNRAVIS_SPIKE_CHILD_IDENTIFIERS" // set: the helper process runs as if started with --mcp-allow-identifiers
)

// TestSDKChildProcess is not a test: it is the SDK loop as a real process (the test binary run again), reading the real stdin and
// writing the real stdout, for TestSDKStdioProcess. It does nothing unless the parent set childEnv.
func TestSDKChildProcess(t *testing.T) {
	port := os.Getenv(childEnv)
	if port == "" {
		t.Skip("helper process")
	}
	n, _ := strconv.Atoi(port)
	b, err := New(n, WithToken(testSessionToken), WithIdentifiers(os.Getenv(childIdentEnv) != ""))
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
	for _, tg := range processTargets(srv) {
		for _, stateless := range []bool{false, true} {
			name := tg.name + " handshake"
			if stateless {
				name = tg.name + " stateless"
			}
			t.Run(name, func(t *testing.T) {
				if stateless && tg.name != processTargets(srv)[0].name && os.Getenv("UNRAVIS_MCP_BINARY_NO_STATELESS") != "" {
					t.Skip("this binary predates the 2026-07-28 lifecycle (the hand-rolled loop has no stateless lifecycle)")
				}
				p := startProcess(t, tg.cmd(false))
				p.stateless = stateless
				started := time.Now()
				if stateless {
					p.request("server/discover", "")
				} else {
					p.handshake()
				}
				t.Logf("first answer %v after launch", time.Since(started).Round(time.Millisecond))
				if answers := tenCalls(t, p.call); len(answers) != 10 {
					t.Fatalf("%d of the ten tools answered", len(answers))
				}
				p.stopOnEOF(t)
			})
		}
	}
}

// The operator switch --mcp-allow-identifiers still reaches the bridge through the SDK loop of the real process: off, an identifier filter
// is refused; on, it is accepted.
func TestSDKStdioProcessIdentifierSwitch(t *testing.T) {
	srv := demoDashboard(t)
	query := map[string]any{"query": map[string]any{
		"sourceIds": []string{"demo-west"}, "start": demoNow.Add(-15 * time.Minute).Format(time.RFC3339), "end": demoNow.Format(time.RFC3339),
		"baselineStart": demoNow.Add(-30 * time.Minute).Format(time.RFC3339), "baselineEnd": demoNow.Add(-15 * time.Minute).Format(time.RFC3339),
		"kind": "all", "outcome": "all", "metric": "total", "bucketSeconds": 60, "userIds": []int{1000}}}
	for _, tg := range processTargets(srv) {
		for _, allowed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s identifiers=%v", tg.name, allowed), func(t *testing.T) {
				p := startProcess(t, tg.cmd(allowed))
				p.handshake()
				text, isError := p.call("audit_compare", query)
				if refused := isError && strings.Contains(text, "--mcp-allow-identifiers"); refused == allowed {
					t.Fatalf("isError=%v text=%.200s", isError, text)
				}
				p.stopOnEOF(t)
			})
		}
	}
}

type processTarget struct {
	name string
	cmd  func(identifiers bool) *exec.Cmd
}

// processTargets are the stdio servers to launch: the SDK loop in a re-run of the test binary, and, when UNRAVIS_MCP_BINARY names a built
// binary, that binary with --mcp (the server a host starts), both pointed at the dashboard srv.
func processTargets(srv *httptest.Server) []processTarget {
	u, _ := url.Parse(srv.URL)
	port := u.Port()
	targets := []processTarget{{"sdk loop in a test process", func(identifiers bool) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSDKChildProcess$") //nolint:gosec // this test binary run again
		cmd.Env = append(os.Environ(), childEnv+"="+port)
		if identifiers {
			cmd.Env = append(cmd.Env, childIdentEnv+"=1")
		}
		return cmd
	}}}
	if bin := os.Getenv("UNRAVIS_MCP_BINARY"); bin != "" {
		targets = append(targets, processTarget{"binary " + filepath.Base(bin), func(identifiers bool) *exec.Cmd {
			args := []string{"--mcp", "--port", port}
			if identifiers {
				args = append(args, "--mcp-allow-identifiers")
			}
			cmd := exec.Command(bin, args...) //nolint:gosec // a binary the person running the test names
			cmd.Env = append(os.Environ(), "AUDIT_SESSION_TOKEN="+testSessionToken)
			return cmd
		}})
	}
	return targets
}

// process is a wire to a launched stdio server.
type process struct {
	*wire
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	exited chan error
	stderr *bytes.Buffer
}

func startProcess(t *testing.T, cmd *exec.Cmd) *process {
	t.Helper()
	p := &process{cmd: cmd, stderr: new(bytes.Buffer), exited: make(chan error, 1)}
	cmd.Stderr = p.stderr
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p.stdin = stdin
	p.wire = newWire(t, stdin, stdout)
	go func() { p.exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return p
}

// stopOnEOF closes the process's stdin, as a host does when it goes, and requires a clean and prompt exit.
func (p *process) stopOnEOF(t *testing.T) {
	t.Helper()
	_ = p.stdin.Close()
	select {
	case err := <-p.exited:
		if err != nil {
			t.Fatalf("process exit on EOF: %v (stderr %q)", err, p.stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("process did not exit when stdin closed")
	}
}
