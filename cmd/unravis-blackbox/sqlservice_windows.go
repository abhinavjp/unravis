//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"

	"api-audit-explorer/internal/blackbox"
)

// sqlServiceEntry runs as the SQL Windows service when the service manager
// started us with the hidden "sql-service" command install-sql registers. It is
// checked before serviceEntry, so the SQL service never runs the host handler.
// From a console it falls through to run(), which runs the same code in this
// window.
func sqlServiceEntry(args []string, d deps) (bool, int) {
	if len(args) == 0 || args[0] != "sql-service" {
		return false, 0
	}
	isSvc, err := svc.IsWindowsService()
	if err != nil || !isSvc {
		return false, 0
	}
	fs := flag.NewFlagSet("sql-service", flag.ContinueOnError)
	cfgPath := fs.String("config", filepath.Join(d.exeDir(), defaultConfigName), "")
	_ = fs.Parse(args[1:])
	h := &sqlHandler{cfgPath: *cfgPath, d: d}
	if err := svc.Run(blackbox.SQLServiceName, h); err != nil {
		return true, exitFail
	}
	return true, exitOK
}

type sqlHandler struct {
	cfgPath string
	d       deps
}

// Execute is the SQL service main loop: run the SQL runner under the service
// account, answer Stop and Shutdown by ending the run cleanly. No HTTP server
// is started in this mode. The event log gets fixed sentences only, never
// driver error text (C14).
func (h *sqlHandler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}
	var logf func(string, ...any)
	if el, err := eventlog.Open(blackbox.SQLServiceName); err == nil {
		defer el.Close()
		logf = func(f string, a ...any) { _ = el.Info(1, fmt.Sprintf(f, a...)) }
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runSQLService(ctx, blackbox.Paths{ConfigPath: h.cfgPath}, h.d, logf)
	}()
	status <- svc.Status{State: svc.Running, Accepts: accepted}
	for {
		select {
		case err := <-done:
			cancel()
			if err != nil {
				if logf != nil {
					logf("the SQL run stopped with an error")
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