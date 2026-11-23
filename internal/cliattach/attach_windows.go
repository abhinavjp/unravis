//go:build windows

// Package cliattach holds the platform part of the command line's attach to the app: where the locator lives, how the pipe is dialed
// and checked. It reads no environment variable; it is the only part of the command line that imports the Windows packages.
package cliattach

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/locator"
	"api-audit-explorer/internal/pipesec"
)

// Default is the attacher of a real Windows install. exe returns the path of this program. The server end of the pipe must be
// the app program `<install>\unravis.exe` in this install, in this session, and the process the locator names (pid), so a program that
// took the pipe name is never sent anything. The environment is not read: the run folder comes from the known folder API.
func Default(exe func() (string, error), userAgent string) clicore.Attacher {
	return clicore.Attacher{
		RunDir: func() (string, error) {
			d, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
			if err != nil {
				return "", err
			}
			return filepath.Join(d, "Unravis", "run"), nil
		},
		Prober: locator.SystemProber(),
		Dial: func(ctx context.Context, rec locator.Record) (net.Conn, error) {
			self, err := exe()
			if err != nil {
				return nil, err
			}
			if real, rerr := filepath.EvalSymlinks(self); rerr == nil { // junctions and 8.3 short names would not match the server's image path
				self = real
			}
			install := InstallDir(self)
			var session uint32
			if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
				return nil, err
			}
			sp := pipesec.ServerPolicy{ExpectedImage: filepath.Join(install, "unravis.exe"), InstallDir: install, ExpectedSession: session}
			conn, peer, err := pipesec.DialVerified(ctx, rec.AG, sp)
			if err != nil {
				return nil, err
			}
			if int(peer.PID) != rec.PID {
				_ = conn.Close()
				return nil, fmt.Errorf("%w: pid is not the one in the locator", pipesec.ErrServerMismatch)
			}
			return conn, nil
		},
		UserAgent: userAgent,
	}
}

// InstallDir is the install folder for the path of this program: the thin command line sits in `<install>\bin`, the app program in
// `<install>`.
func InstallDir(exe string) string {
	dir := filepath.Dir(exe)
	if strings.EqualFold(filepath.Base(dir), "bin") {
		return filepath.Dir(dir)
	}
	return dir
}
