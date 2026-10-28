//go:build !windows

package setup

import (
	"fmt"
	"os"
	"syscall"
)

func osFileID(path string) (string, bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", false, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false, fmt.Errorf("no file ID for %s", path)
	}
	return fmt.Sprintf("%08x-%016x", uint64(st.Dev), uint64(st.Ino)), fi.Mode()&os.ModeSymlink != 0, nil
}
