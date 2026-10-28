//go:build !windows

package server

import "os"

// moveNoReplace renames from to to and fails when to exists: a hard link fails on
// an existing name, and the old name goes only after the link is made.
func moveNoReplace(from, to string) error {
	if err := os.Link(from, to); err != nil {
		return err
	}
	return os.Remove(from)
}
