package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	"api-audit-explorer/internal/appdirs"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/instance"
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
	// instanceListen receives Open and Quit signals from other copies; tests swap it.
	instanceListen = instance.Listen
)

// listenInstanceEvents lets other copies reach a pipe-mode owner through the per-user instance events, as they reach a browser-mode owner:
// --quit (setup, OTA, the tray) cancels the app, and a browser-mode start that lost the mutex signals Open and exits quietly. Open has no
// tab to show here: the shell is what shows the window, and swapping Open for the shell's open call is D19 (BL-306), so for now it is
// only logged. If the events cannot be created the owner still runs.
func listenInstanceEvents(ctx context.Context, cancel context.CancelFunc, names instance.Names) (stop func()) {
	stop, err := instanceListen(ctx, names, func() { log.Print("Open requested; pipe mode has no window to show") }, cancel)
	if err != nil {
		log.Printf("Other copies cannot reach this one (--quit will not work): %v", err)
		return func() {}
	}
	return stop
}

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
	if f.ownerSet && f.owner == "" {
		return fmt.Errorf("--owner needs a value: headless or shell")
	}
	if f.owner != "" && f.owner != "headless" && f.owner != "shell" {
		return fmt.Errorf("--owner must be headless or shell")
	}
	if f.owner != "" && !pipe {
		return fmt.Errorf("--owner is only valid with --pipe-server or --pipe-only")
	}
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
func enterPipeSingleton(demo bool, getenv func(string) string) (release func(), runDir string, names instance.Names, err error) {
	runDir, err = appdirs.RunDir(getenv)
	if err != nil {
		return nil, "", instance.Names{}, err
	}
	gate, err := enterSingleInstance(demo, true, true, pipeGateDepsFn(runDir, demo))
	if err != nil {
		return nil, "", instance.Names{}, err
	}
	if gate.attached {
		return nil, "", instance.Names{}, &exitCodeError{code: exitAttached, msg: "Unravis is already running; attach through " + locator.FilePath(runDir, demo)}
	}
	if !gate.proceed {
		return nil, "", instance.Names{}, errors.New("pipe mode: the singleton gate neither proceeded nor attached")
	}
	return gate.release, runDir, gate.names, nil
}

// ownerName is the owner that hello reports: the --owner value, or "app" for a copy that was not told.
func ownerName(flag string) string {
	if flag == "" {
		return "app"
	}
	return flag
}

// cliHandlerFor is the command line route of this owner: hello says the --owner of this copy, and app.stop (which quits through
// stop) works only when that is headless. The desktop shell's owner (--pipe-only with no --owner) is never stopped from the command line.
func cliHandlerFor(f runFlags, dashboard http.Handler, token string, stop func()) http.Handler {
	return newCLIHandler(dashboard, token, ownerName(f.owner), stop)
}
