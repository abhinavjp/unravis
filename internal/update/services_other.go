//go:build !windows

package update

// anyServiceInstalled is false off Windows: there are no such services.
func anyServiceInstalled(names ...string) bool { return false }
