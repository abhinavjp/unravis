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

// showError has nowhere to show a box off Windows; the message was already printed to the console.
func showError(string) {}

// rundll32Path is only used on Windows; the browser command off Windows never calls it.
func rundll32Path() string { return "" }
