//go:build windows

package server

import (
	"os"
	"os/exec"
	"path/filepath"
)

var errOpenFolderUnsupported = os.ErrInvalid

// openFolderInExplorer starts Explorer on dir. dir is already validated: it
// begins with a drive letter and is an existing directory. The program is the
// system Explorer by full path and the folder is one argument.
func openFolderInExplorer(dir string) error {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	cmd := exec.Command(filepath.Join(root, "explorer.exe"), dir)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
