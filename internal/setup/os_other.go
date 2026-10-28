//go:build !windows

package setup

import (
	"context"
	"errors"
	"io"
	"time"
)

// Setup is a Windows program; on other systems these exist so the package builds and its tests run.

var errWindowsOnly = errors.New("Unravis setup runs on Windows only")

func NewOSSystem() (*System, error)              { return nil, errWindowsOnly }
func OSDirs() (Dirs, error)                      { return Dirs{}, errWindowsOnly }
func CurrentUserSID() (string, error)            { return "", errWindowsOnly }
func ProfileOf(sid string) (string, error)       { return "", errWindowsOnly }
func OpenWindow(browser, url string) error       { return errWindowsOnly }
func ShowFolder(dir string) error                { return errWindowsOnly }
func StartProgram(exe string) error              { return errWindowsOnly }
func DialWorkerPipe(string, uint32) (*WorkerPipe, error) { return nil, errWindowsOnly }

type OSElevator struct {
	SetupExe string
	Timeout  time.Duration
}

func (OSElevator) Start(context.Context) (io.ReadWriteCloser, error) { return nil, errWindowsOnly }

type WorkerPipe struct {
	Conn         io.ReadWriteCloser
	SID, Profile string
}
