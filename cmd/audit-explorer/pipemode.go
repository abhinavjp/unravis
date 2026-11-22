package main

import (
	"api-audit-explorer/internal/appdirs"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/locator"
	"api-audit-explorer/internal/pipesec"
	"api-audit-explorer/internal/pipesrv"
	"api-audit-explorer/internal/version"
)

// The seams the pipe-mode tests replace. Production values are the real listeners.
var (
	listenTCP = listenLoopback
	openPipes = func() (*pipesrv.Pipes, error) { return pipesrv.Open(pipesec.Listen, pipesec.DefaultPeerPolicy()) }
	// afterServing runs once everything is serving; cancel starts the normal shutdown. Production does nothing.
	afterServing = func(context.CancelFunc) {}
	// pipeGateDepsFn builds the singleton gate of pipe mode; tests swap it for fakes.
	pipeGateDepsFn = pipeGateDeps
	// selfStart is this process's creation time for the locator; tests swap it off Windows.
	selfStart = locator.SelfStart
)

// writeLocator publishes backend.json for the owner (ui and ag pipe names, pid, creation time, exe, version) and returns the function
// that removes it. The file is restricted to the current user before any content is written.
func writeLocator(path string, pipes *pipesrv.Pipes) (remove func(), err error) {
	start, err := selfStart()
	if err != nil {
		return nil, fmt.Errorf("locator: own start time: %w", err)
	}
	exe, _ := os.Executable()
	rec := locator.Record{
		Version: locator.Schema, PID: os.Getpid(), StartTime: start, Exe: exe, AppVersion: version.Version,
		UI: pipes.UIName, AG: pipes.AGName,
	}
	if err := locator.Write(path, rec, locator.Options{SecureDir: casestore.SecureDir, SecureFile: casestore.SecureFile}); err != nil {
		return nil, err
	}
	return func() { _ = locator.Remove(path, rec) }, nil
}

// checkPipeFlags validates --pipe-server, --pipe-only and --loopback (BL-301).
func checkPipeFlags(f runFlags) error {
	pipe := f.pipeServer || f.pipeOnly
	if f.loopback && f.pipeOnly {
		return fmt.Errorf("--pipe-only never opens a TCP port; do not combine it with --loopback")
	}
	if f.loopback && !f.pipeServer {
		return fmt.Errorf("--loopback is only valid with --pipe-server")
	}
	if !pipe {
		return nil
	}
	if hostOS != "windows" {
		return fmt.Errorf("--pipe-server and --pipe-only are only available on Windows")
	}
	if f.noBrowser {
		return fmt.Errorf("--no-browser does not apply to --pipe-server and --pipe-only: they never open a browser")
	}
	if f.mcp || f.background {
		return fmt.Errorf("--pipe-server and --pipe-only run the dashboard backend; do not combine them with --mcp or --background")
	}
	if (f.portSet || f.port != defaultPort) && !f.loopback {
		return fmt.Errorf("--port needs --loopback in pipe mode")
	}
	return nil
}

// listenerPort is the TCP port of l, or 0 when there is no TCP listener (pipe-only).
func listenerPort(l net.Listener) int {
	if l == nil {
		return 0
	}
	if t, ok := l.Addr().(*net.TCPAddr); ok {
		return t.Port
	}
	return 0
}

// stoppers shuts several servers down together.
type stoppers []shutdowner

func (s stoppers) Shutdown(ctx context.Context) error {
	errs := make([]error, len(s))
	var wg sync.WaitGroup
	for i, x := range s {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = x.Shutdown(ctx) }()
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (s stoppers) Close() error {
	var errs []error
	for _, x := range s {
		errs = append(errs, x.Close())
	}
	return errors.Join(errs...)
}

// enterPipeSingleton takes the per-user singleton for a pipe-mode start. A copy that is not the owner waits for the owner's live locator
// and returns an exitCodeError with exitAttached; the owner gets the release function and the run folder of its locator.
func enterPipeSingleton(demo bool, getenv func(string) string) (release func(), runDir string, err error) {
	runDir, err = appdirs.RunDir(getenv)
	if err != nil {
		return nil, "", err
	}
	gate, err := enterSingleInstance(demo, true, true, pipeGateDepsFn(runDir, demo))
	if err != nil {
		return nil, "", err
	}
	if gate.attached {
		return nil, "", &exitCodeError{code: exitAttached, msg: "Unravis is already running; attach through " + locator.FilePath(runDir, demo)}
	}
	if !gate.proceed {
		return nil, "", errors.New("pipe mode: the singleton gate neither proceeded nor attached")
	}
	return gate.release, runDir, nil
}
