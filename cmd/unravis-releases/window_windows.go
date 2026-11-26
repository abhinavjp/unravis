//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// rundll32Path is rundll32.exe by full path in the Windows system folder, so the program is never looked up through PATH or the
// current folder. It asks Windows for the folder (GetSystemDirectory), not the SystemRoot variable, which a parent process can change.
func rundll32Path() string {
	dir, err := windows.GetSystemDirectory()
	if err != nil || dir == "" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		dir = filepath.Join(root, "System32")
	}
	return filepath.Join(dir, "rundll32.exe")
}

// showError opens a message box. The program has no console, so this is the only way a start-up error reaches the person.
func showError(msg string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	box := user32.NewProc("MessageBoxW")
	text, _ := syscall.UTF16PtrFromString(msg)
	title, _ := syscall.UTF16PtrFromString("Unravis Releases")
	const mbOK, mbIconError = 0x0, 0x10
	_, _, _ = box.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbOK|mbIconError)
}

// appWindow opens link in its own window with no address bar or tabs, using the Microsoft Edge that Windows already has
// (the same WebView2 engine other desktop apps use). It has its own profile folder, so closing the window ends this
// process and nothing else is touched. The returned command is already started; wait for it to learn the window closed.
func appWindow(link, profile string) (*exec.Cmd, error) {
	edge := findEdge()
	if edge == "" {
		return nil, errors.New("Microsoft Edge was not found")
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, err
	}
	cmd := exec.Command(edge, "--app="+link, "--user-data-dir="+profile, "--window-size=1120,740",
		"--no-first-run", "--no-default-browser-check", "--disable-features=Translate,msEdgeShopping,msEdgeSidebar")
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func findEdge() string {
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
		if base := os.Getenv(env); base != "" {
			p := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
				return p
			}
		}
	}
	if p, err := exec.LookPath("msedge.exe"); err == nil {
		return p
	}
	return ""
}
