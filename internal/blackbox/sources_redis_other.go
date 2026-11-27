//go:build !windows

package blackbox

// redisServiceConfs: there is no Windows service list off Windows.
func redisServiceConfs() []string { return nil }
