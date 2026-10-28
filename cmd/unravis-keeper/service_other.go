//go:build !windows

package main

// serviceEntry: there is no service off Windows.
func serviceEntry([]string, deps) (bool, int) { return false, 0 }
