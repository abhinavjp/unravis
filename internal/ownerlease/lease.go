// Package ownerlease keeps one pipe-mode owner per profile run dir, even across Windows sessions
// (console and RDP) where the per-session Local\ mutex of internal/instance does not collide.
package ownerlease

import (
	"errors"
	"path/filepath"
)

// ErrBusy: another process holds the lease.
var ErrBusy = errors.New("ownerlease: another owner holds the lease")

// FileName and DemoFileName are the lock file names inside runDir.
const (
	FileName     = "owner.lock"
	DemoFileName = "owner-demo.lock"
)

// Path is the lock file path inside runDir (demo uses DemoFileName).
func Path(runDir string, demo bool) string {
	if demo {
		return filepath.Join(runDir, DemoFileName)
	}
	return filepath.Join(runDir, FileName)
}

// IsLeaseFile reports whether name is one of the lock files in the run dir (the locator folder check lets them stay).
func IsLeaseFile(name string) bool { return name == FileName || name == DemoFileName }
