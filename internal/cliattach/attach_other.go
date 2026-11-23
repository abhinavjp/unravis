//go:build !windows

// Package cliattach holds the platform part of the command line's attach to the app: where the locator lives, how the pipe is dialed
// and checked. It reads no environment variable; it is the only part of the command line that imports the Windows packages.
package cliattach

import (
	"context"
	"errors"
	"net"

	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/locator"
)

// Default off Windows: the app and its pipes exist only on Windows, so nothing is ever found.
func Default(_ func() (string, error), userAgent string) clicore.Attacher {
	return clicore.Attacher{
		RunDir:    func() (string, error) { return "", errors.ErrUnsupported },
		Prober:    locator.SystemProber(),
		Dial:      func(context.Context, locator.Record) (net.Conn, error) { return nil, errors.ErrUnsupported },
		UserAgent: userAgent,
	}
}

// InstallDir is the install folder for the path of this program (see clicore.InstallDir).
func InstallDir(exe string) string { return clicore.InstallDir(exe) }
