//go:build !windows

package blackbox

import "errors"

// errWindowsOnly is returned by every process-resolution entry point off
// Windows. The shared Resolver compiles here and surfaces this error.
var errWindowsOnly = errors.New("process resolution is Windows only")

func snapshotProcesses() ([]ProcInfo, error) { return nil, errWindowsOnly }

func loadModules(uint32) ([]string, error) { return nil, errWindowsOnly }
