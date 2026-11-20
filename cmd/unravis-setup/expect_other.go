//go:build !windows

package main

import "os"

// openHeld opens path for reading. Only Windows can hold a file against rename and delete; setup is a Windows program.
func openHeld(path string) (*os.File, error) { return os.Open(path) }
