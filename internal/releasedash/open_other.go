//go:build !windows

package releasedash

import "os"

func openShared(path string) (*os.File, error) { return os.Open(path) } //nolint:gosec // the path is built from the release data folder, not from input
