//go:build !e2e

package pipesec

// extraAllowed is false in release builds: there is no way to add an allowed image at run time.
func extraAllowed(string) bool { return false }
