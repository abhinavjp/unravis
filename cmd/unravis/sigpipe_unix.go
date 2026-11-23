//go:build !windows

package main

import (
	"os/signal"
	"syscall"
)

// A reader that closes the pipe must give a write error (so the command stops with exit 6 and no text), not kill the program with a
// signal. Windows has no such signal: the write fails with an error there already.
func init() { signal.Ignore(syscall.SIGPIPE) }
