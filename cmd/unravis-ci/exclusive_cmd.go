package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"api-audit-explorer/internal/localci"
)

// cmdExclusive runs one command while it holds an exclusive resource of the PC, the same lock the agent's jobs take
// (.github/local-ci.yml): `unravis-ci exclusive desktop-first-instance -- go test ./cmd/audit-explorer`. A developer's own
// test run then waits for a CI job that uses the resource, and the job waits for it, instead of both fighting over the
// per-user first-instance name. The command's exit code is returned.
func cmdExclusive(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	split := len(args)
	for i, a := range args {
		if a == "--" {
			split = i
			break
		}
	}
	if split == len(args) || split+1 == len(args) {
		_, _ = fmt.Fprintln(stderr, "exclusive: usage: unravis-ci exclusive <resource> [--state DIR] -- <command> [args...]")
		return exitUsage
	}
	fs := flag.NewFlagSet("exclusive", flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "", "the CI store folder (default: under LOCALAPPDATA)")
	pos, err := parseInterspersed(fs, args[:split])
	if err != nil || len(pos) != 1 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "exclusive: name exactly one resource before --")
		}
		return exitUsage
	}
	dir := stateDirOf(*state, environ)
	release, err := localci.HoldExclusive(ctx, pos[0], filepath.Join(dir, "locks"), func() {
		_, _ = fmt.Fprintf(stderr, "exclusive: waiting for %q (a CI job or another run holds it)\n", pos[0])
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "exclusive:", err)
		return exitFailed
	}
	defer release()
	cmd := exec.CommandContext(ctx, args[split+1], args[split+2:]...) //nolint:gosec // the command is the caller's own
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	cmd.Env = environ
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		_, _ = fmt.Fprintln(stderr, "exclusive:", err)
		return exitFailed
	}
	return exitOK
}
