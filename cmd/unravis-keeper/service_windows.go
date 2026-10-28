//go:build windows

package main

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"

	"api-audit-explorer/internal/datadir"
)

// serviceEntry runs the service loop when the Windows service manager started this program. It registers nothing.
func serviceEntry(_ []string, d deps) (bool, int) {
	isSvc, err := svc.IsWindowsService()
	if err != nil || !isSvc {
		return false, 0
	}
	if err := svc.Run(ServiceName, &handler{d: d}); err != nil {
		return true, exitFail
	}
	return true, exitOK
}

type handler struct{ d deps }

// Execute answers on the pipe until Stop or Shutdown.
func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}
	var logf func(string, ...any)
	if el, err := eventlog.Open(ServiceName); err == nil {
		defer el.Close()
		logf = func(f string, a ...any) { _ = el.Info(1, fmt.Sprintf(f, a...)) }
	}
	dir, pipe := datadir.KeeperDefault(h.d.getenv)
	if err := checkRoot(h.d, dir); err != nil {
		if logf != nil {
			logf("did not start: %v", err)
		}
		return false, 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, h.d, dir, pipe, logf) }()
	status <- svc.Status{State: svc.Running, Accepts: accepted}
	for {
		select {
		case err := <-done:
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
