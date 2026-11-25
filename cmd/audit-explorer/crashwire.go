package main

import (
	"log"

	"api-audit-explorer/internal/appdirs"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/crashlog"
)

// startCrashCapture turns on the local crash record (BL-315): an unhandled panic or fatal runtime error writes its goroutine dump into the
// crash folder (appdirs.CrashDir), which the next start sanitises and prunes. It never fails the start: a program that cannot record its
// crash must still run, so trouble is only logged. Local only; nothing is sent anywhere. The caller defers Close on the result (nil-safe).
func startCrashCapture(getenv func(string) string) *crashlog.Capture {
	dir, err := appdirs.CrashDir(getenv)
	if err != nil {
		log.Printf("Crash capture is off: %v", err)
		return nil
	}
	c, err := crashlog.Start(crashlog.Options{Dir: dir, SecureDir: casestore.SecureDir, SecureFile: casestore.SecureFile})
	if err != nil {
		log.Printf("Crash capture: %v", err)
	}
	return c
}
