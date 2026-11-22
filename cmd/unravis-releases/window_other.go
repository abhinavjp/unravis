//go:build !windows

package main

import (
	"errors"
	"os/exec"
)

// appWindow is only built for Windows. Elsewhere the program prints the link (or --open uses the default browser).
func appWindow(link, profile string) (*exec.Cmd, error) {
	return nil, errors.New("the app window is only available on Windows")
}
