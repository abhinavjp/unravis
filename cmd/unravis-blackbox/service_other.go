//go:build !windows

package main

import "os"

// serviceEntry: there is no service off Windows. The hidden "service" command is
// the foreground run, so a registered command line still works in tests.
func serviceEntry(args []string, d deps) (bool, int) {
	if len(args) > 0 && args[0] == "service" {
		return true, run(append([]string{"run"}, args[1:]...), os.Stdout, os.Stderr, d)
	}
	return false, 0
}
