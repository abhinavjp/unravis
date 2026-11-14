//go:build !windows

package blackbox

// Off Windows there are no app pools or services. Both enumerators report
// Windows-only, and Resolver.ResolveTargets (resolve_map.go) returns nil plus
// every selected name as "not found" because Inventory fails with
// errWindowsOnly — callers compile and behave the same on every platform.

func enumW3WPInstances() ([]string, error) { return nil, errWindowsOnly }

func enumServiceProcs() ([]svcProc, error) { return nil, errWindowsOnly }
