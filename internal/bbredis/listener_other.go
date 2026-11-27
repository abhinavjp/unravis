//go:build !windows

package bbredis

// The recorder runs on Windows. Elsewhere (developer machines, tests) there is no service account to check, so
// nothing is refused here.
func listenerTrusted(int) error { return nil }
