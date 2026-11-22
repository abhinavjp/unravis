//go:build !windows

package releasedash

import "os"

func openShared(path string) (*os.File, error) { return os.Open(path) }
