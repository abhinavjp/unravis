//go:build !windows

package main

// sqlServiceEntry: there is no service off Windows. The hidden "sql-service"
// command is handled by run() as a foreground run, so tests work everywhere.
func sqlServiceEntry([]string, deps) (bool, int) { return false, 0 }