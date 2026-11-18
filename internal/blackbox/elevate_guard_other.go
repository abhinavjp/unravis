//go:build !windows

package blackbox

// GuardHelper does nothing off Windows: there is no permission prompt to guard.
func GuardHelper(string) (func(), error) { return func() {}, nil }
