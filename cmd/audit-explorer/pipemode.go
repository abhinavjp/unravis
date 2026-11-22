package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"api-audit-explorer/internal/pipesec"
	"api-audit-explorer/internal/pipesrv"
)

// The seams the pipe-mode tests replace. Production values are the real listeners.
var (
	listenTCP = listenLoopback
	openPipes = func() (*pipesrv.Pipes, error) { return pipesrv.Open(pipesec.Listen, pipesec.DefaultPeerPolicy()) }
	// afterServing runs once everything is serving; cancel starts the normal shutdown. Production does nothing.
	afterServing = func(context.CancelFunc) {}
)

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
