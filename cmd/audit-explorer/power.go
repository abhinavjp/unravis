package main

import (
	"log"
	"log/slog"
	"runtime/debug"
	"sync"

	"api-audit-explorer/internal/applog"
	"api-audit-explorer/internal/power"
	"api-audit-explorer/internal/server"
)

func newPowerPolicy(updater *server.Updater, logFile *applog.File) *power.Policy {
	var warned sync.Once
	var logger *slog.Logger
	if logFile != nil {
		logger = logFile.Logger
	}
	return power.New(power.Config{
		// Updater.InUse keeps multi-minute request windows; this policy owns the exact 30-second grace.
		InUse:  func() bool { return updater != nil && updater.Busy() },
		OnIdle: []func(){debug.FreeOSMemory},
		Apply: func(mode power.Mode) error {
			err := power.Apply(mode)
			if err != nil {
				warned.Do(func() {
					if logger != nil {
						logger.Warn("Idle power policy unavailable", "error", err)
					} else {
						log.Printf("Idle power policy unavailable: %v", err)
					}
				})
			}
			return err
		},
	})
}
