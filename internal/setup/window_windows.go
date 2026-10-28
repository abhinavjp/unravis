//go:build windows

package setup

import (
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// The setup window is the page in a browser app window: Edge (on every supported Windows) or Chrome with --app, otherwise the default
// browser. NOT RUN: needs a clean VM.

// OpenWindow shows url. browser is from Machine.Browser ("" for the default browser).
func OpenWindow(browser, url string) error {
	if browser != "" {
		cmd := exec.Command(browser, "--app="+url, "--window-size=880,640", "--no-first-run")
		if err := cmd.Start(); err == nil {
			go cmd.Wait()
			return nil
		}
	}
	verb, _ := windows.UTF16PtrFromString("open")
	u, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, u, nil, nil, windows.SW_SHOWNORMAL)
}

// ShowFolder opens a folder in Explorer.
func ShowFolder(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return os.ErrNotExist
	}
	cmd := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "explorer.exe"), dir)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// StartProgram starts Unravis as the person running the window (never from the elevated worker).
func StartProgram(exe string) error {
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
