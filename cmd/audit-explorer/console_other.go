//go:build !windows

package main

// attachParentConsole only matters for the windowless Windows build; elsewhere
// the standard handles are always the terminal's.
func attachParentConsole() bool { return false }
