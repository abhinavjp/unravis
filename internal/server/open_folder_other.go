//go:build !windows

package server

import "errors"

var errOpenFolderUnsupported = errors.New("open folder is not supported on this OS")

func openFolderInExplorer(string) error { return errOpenFolderUnsupported }
