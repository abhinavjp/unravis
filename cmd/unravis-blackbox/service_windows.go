//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"

	"api-audit-explorer/internal/blackbox"
)

// serviceEntry runs as the Windows service when the service manager started us:
// either with the hidden "service" command that install registers, or with no
// arguments at all.
func serviceEntry(args []string, d deps) (bool, int) {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		return false, 0
	}
	hidden := len(args) > 0 && args[0] == "service"
	if !isSvc && !hidden {
		return false, 0
	}
	if !isSvc {
		// "service" from a console: the same as run, for testing a registered command line.
		return true, run(append([]string{"run"}, args[1:]...), os.Stdout, os.Stderr, d)
	}
	var rest []string
	if hidden {
		rest = args[1:]
	}
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	cfgPath := fs.String("config", filepath.Join(d.exeDir(), defaultConfigName), "")
	keyPath := fs.String("key-file", filepath.Join(d.exeDir(), defaultKeyName), "")
	_ = fs.Parse(rest)
	h := &handler{cfgPath: *cfgPath, keyPath: *keyPath, d: d}
	if err := svc.Run(blackbox.ServiceName, h); err != nil {
		return true, exitFail
	}
	return true, exitOK
}

type handler struct {
	cfgPath, keyPath string
	d                deps
}

// Execute is the service main loop: start recording, answer Stop and Shutdown by
// ending the run cleanly.
func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}
	var logf func(string, ...any)
	if el, err := eventlog.Open(blackbox.ServiceName); err == nil {
		defer el.Close()
		logf = func(f string, a ...any) { _ = el.Info(1, fmt.Sprintf(f, a...)) }
	}
	cfg, err := blackbox.LoadConfig(h.cfgPath)
	if err != nil {
		if logf != nil {
			logf("cannot start: %v", err)
		}
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- h.d.hostRun(ctx, blackbox.HostOptions{Config: cfg, KeyPath: h.keyPath, Version: version, Log: logf})
	}()
	status <- svc.Status{State: svc.Running, Accepts: accepted}
	for {
		select {
		case err := <-done:
			cancel()
			if err != nil {
				if logf != nil {
					logf("stopped: %v", err)
				}
				return false, 2
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}
